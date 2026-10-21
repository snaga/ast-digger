// Package parser: typescript.go implements the Parser interface for
// TypeScript and TSX source files using the tree-sitter-typescript grammar.
// It corresponds to ast_digger/parsers/typescript.py in the Python
// implementation.
package parser

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"

	"github.com/snaga/ast-digger/internal/apperr"
)

// TypeScriptParser implements the Parser interface for TypeScript (.ts) and
// TSX (.tsx) source files.
//
// It corresponds to the Python `TypeScriptParser` class defined in
// ast_digger/parsers/typescript.py.
type TypeScriptParser struct {
	tsLang  *tree_sitter.Language
	tsxLang *tree_sitter.Language
}

// Compile-time assertion that TypeScriptParser satisfies the Parser
// interface.
var _ Parser = (*TypeScriptParser)(nil)

// NewTypeScriptParser constructs a TypeScriptParser with both the
// tree-sitter-typescript and tree-sitter-tsx grammars loaded.
func NewTypeScriptParser() *TypeScriptParser {
	return &TypeScriptParser{
		tsLang:  tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript()),
		tsxLang: tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTSX()),
	}
}

// languageForPath selects the TSX grammar for ".tsx" files (case-insensitive)
// and the plain TypeScript grammar for everything else (".ts" and any other
// extension), mirroring Python's `TSX_LANGUAGE if ext == ".tsx" else
// TS_LANGUAGE`.
func (p *TypeScriptParser) languageForPath(filePath string) *tree_sitter.Language {
	if strings.ToLower(filepath.Ext(filePath)) == ".tsx" {
		return p.tsxLang
	}
	return p.tsLang
}

// parseSource parses source using a fresh tree-sitter parser configured with
// lang. The caller owns the returned tree and must call tree.Close() once
// done with it.
func (p *TypeScriptParser) parseSource(source []byte, lang *tree_sitter.Language) (*tree_sitter.Tree, error) {
	ts := tree_sitter.NewParser()
	defer ts.Close()

	if err := ts.SetLanguage(lang); err != nil {
		return nil, fmt.Errorf("failed to set tree-sitter-typescript language: %w", err)
	}

	tree := ts.Parse(source, nil)
	if tree == nil {
		return nil, fmt.Errorf("tree-sitter-typescript failed to parse source")
	}
	return tree, nil
}

// nodeText returns node's source text with leading/trailing whitespace
// trimmed, or "" if node is nil.
func nodeText(node *tree_sitter.Node, source []byte) string {
	if node == nil {
		return ""
	}
	return strings.TrimSpace(node.Utf8Text(source))
}

// findJSDoc walks up from node through any enclosing export_statement /
// lexical_declaration / variable_declaration wrapper nodes, then looks at the
// immediately preceding sibling: if it is a "/**"-prefixed comment node, its
// raw text (including the "/**"/"*/" delimiters) is returned; "export" and
// "decorator" siblings are skipped over (continuing to look further back);
// anything else stops the search. It mirrors Python's `_find_jsdoc`.
func findJSDoc(node *tree_sitter.Node, source []byte) *string {
	curr := node
	for curr.Parent() != nil {
		pk := curr.Parent().Kind()
		if pk != "export_statement" && pk != "lexical_declaration" && pk != "variable_declaration" {
			break
		}
		curr = curr.Parent()
	}

	sibling := curr.PrevSibling()
	for sibling != nil {
		switch sibling.Kind() {
		case "comment":
			text := strings.TrimSpace(sibling.Utf8Text(source))
			if strings.HasPrefix(text, "/**") {
				return &text
			}
			return nil
		case "export", "decorator":
			sibling = sibling.PrevSibling()
		default:
			return nil
		}
	}
	return nil
}

// cleanJSDoc strips the "/**"..."*/" delimiters from raw and the leading "*"
// (plus surrounding whitespace) from each interior line, joining the result
// back into a single doc comment string. It mirrors Python's `_clean_jsdoc`.
func cleanJSDoc(raw string) string {
	if strings.HasPrefix(raw, "/**") && strings.HasSuffix(raw, "*/") {
		content := ""
		// Guard against a degenerate "/**/" (4 bytes) input, where slicing
		// off 3 leading and 2 trailing bytes would otherwise underflow;
		// Python's equivalent raw[3:-2] slice silently yields "" in that
		// case rather than raising, so this mirrors that.
		if len(raw) >= 5 {
			content = strings.TrimSpace(raw[3 : len(raw)-2])
		}
		lines := splitLines(content)
		cleaned := make([]string, 0, len(lines))
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "*") {
				line = strings.TrimSpace(line[1:])
			}
			cleaned = append(cleaned, line)
		}
		return strings.TrimSpace(strings.Join(cleaned, "\n"))
	}
	return strings.TrimSpace(raw)
}

// getTSSignature builds node's signature text: everything from node's start
// up to the start of its first descendant that is a "statement_block",
// "object_type", "class_body", or "interface_body" node (found via a
// depth-first, first-match search), or up to node's own end if no such body
// node exists. A trailing "=" (e.g. from "const x = (): void => {") is
// stripped, and any decorator nodes immediately preceding node (after
// unwrapping enclosing export_statement wrappers) are prepended. It mirrors
// Python's `_get_signature`.
func getTSSignature(node *tree_sitter.Node, source []byte) string {
	if node == nil {
		return ""
	}

	var bodyNode *tree_sitter.Node
	var findBody func(curr *tree_sitter.Node)
	findBody = func(curr *tree_sitter.Node) {
		if bodyNode != nil {
			return
		}
		switch curr.Kind() {
		case "statement_block", "object_type", "class_body", "interface_body":
			bodyNode = curr
			return
		}
		for i := uint(0); i < curr.ChildCount(); i++ {
			findBody(curr.Child(i))
		}
	}
	findBody(node)

	endByte := node.EndByte()
	if bodyNode != nil {
		endByte = bodyNode.StartByte()
	}

	sig := strings.TrimSpace(string(source[node.StartByte():endByte]))
	if strings.HasSuffix(sig, "=") {
		sig = strings.TrimSpace(sig[:len(sig)-1])
	}

	var decorators []string
	curr := node
	for curr.Parent() != nil && curr.Parent().Kind() == "export_statement" {
		curr = curr.Parent()
	}

	sibling := curr.PrevSibling()
	for sibling != nil && sibling.Kind() == "decorator" {
		decorators = append([]string{strings.TrimSpace(sibling.Utf8Text(source))}, decorators...)
		sibling = sibling.PrevSibling()
	}

	if len(decorators) > 0 {
		sig = strings.Join(decorators, "\n") + "\n" + sig
	}
	return sig
}

// classifyTSNode reports whether node is one of the TypeScript/TSX
// declaration forms recognized by ParseFile/ExtractSymbol, returning its
// symbol kind ("class", "interface", "type", "function", or "method") and
// name, or kind == "" if node is not a recognized declaration.
//
// actualNode is the node subsequent signature/body lookups should be
// anchored on: it equals node itself in every case except when node is a
// "lexical_declaration"/"variable_declaration" wrapping a
// "variable_declarator" whose value is an arrow_function/function_expression
// (e.g. "const f = () => {...}"), where node (the declaration statement, not
// the inner variable_declarator) is kept as the anchor -- matching Python's
// `actual_node = node` assignment in that branch.
//
// It mirrors the classification logic duplicated inline in both Python's
// `parse_file` and `extract_symbol` (kept identical between the two there;
// factored out into a shared helper here rather than duplicated).
func classifyTSNode(node *tree_sitter.Node, source []byte) (kind, name string, actualNode *tree_sitter.Node) {
	actualNode = node

	switch node.Kind() {
	case "class_declaration":
		return "class", nodeText(node.ChildByFieldName("name"), source), actualNode
	case "interface_declaration":
		return "interface", nodeText(node.ChildByFieldName("name"), source), actualNode
	case "type_alias_declaration":
		return "type", nodeText(node.ChildByFieldName("name"), source), actualNode
	case "function_declaration":
		return "function", nodeText(node.ChildByFieldName("name"), source), actualNode
	case "method_definition", "method_signature":
		return "method", nodeText(node.ChildByFieldName("name"), source), actualNode
	case "lexical_declaration", "variable_declaration":
		for i := uint(0); i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child.Kind() != "variable_declarator" {
				continue
			}
			valNode := child.ChildByFieldName("value")
			if valNode == nil {
				continue
			}
			if valNode.Kind() == "arrow_function" || valNode.Kind() == "function_expression" {
				nameNode := child.ChildByFieldName("name")
				if nameNode != nil {
					return "function", nodeText(nameNode, source), node
				}
			}
		}
	}
	return "", "", actualNode
}

// findTSBodyChild returns node's first direct child whose Kind() is one of
// kinds, or nil if none is found.
func findTSBodyChild(node *tree_sitter.Node, kinds ...string) *tree_sitter.Node {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		for _, k := range kinds {
			if child.Kind() == k {
				return child
			}
		}
	}
	return nil
}

// ParseFile parses filePath and returns the list of symbols (classes,
// interfaces, type aliases, functions, methods) found in it. It corresponds
// to Python's `TypeScriptParser.parse_file`.
func (p *TypeScriptParser) ParseFile(filePath string) ([]SymbolData, error) {
	source, err := readFile(filePath)
	if err != nil {
		return nil, err
	}
	return p.ParseBytes(source, filePath)
}

// ParseBytes parses source and returns the list of symbols found in it.
func (p *TypeScriptParser) ParseBytes(source []byte, filePath string) ([]SymbolData, error) {
	relPath := relFilePath(filePath)

	tree, err := p.parseSource(source, p.languageForPath(filePath))
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	if tree.RootNode().HasError() {
		fmt.Fprintf(os.Stderr, "Warning: Syntax errors detected in %s\n", filePath)
	}

	// symbols is initialized as an empty (non-nil) slice, rather than a nil
	// slice, so that ParseFile returning zero symbols still marshals to JSON
	// as "[]" (matching Python's `json.dumps([])`) instead of "null".
	symbols := []SymbolData{}

	var traverse func(node *tree_sitter.Node, parentName *string)
	traverse = func(node *tree_sitter.Node, parentName *string) {
		kind, name, actualNode := classifyTSNode(node, source)

		if kind != "" && name != "" {
			startLine := int(node.StartPosition().Row) + 1
			endLine := int(node.EndPosition().Row) + 1

			var docstring *string
			if raw := findJSDoc(node, source); raw != nil {
				cleaned := cleanJSDoc(*raw)
				docstring = &cleaned
			}

			signature := getTSSignature(actualNode, source)

			symbols = append(symbols, SymbolData{
				Name:      name,
				Kind:      kind,
				StartLine: startLine,
				EndLine:   endLine,
				Parent:    parentName,
				Signature: &signature,
				Docstring: docstring,
				FilePath:  &relPath,
			})

			bodyNode := findTSBodyChild(actualNode, "class_body", "object_type", "interface_body")
			if bodyNode != nil {
				childName := name
				for i := uint(0); i < bodyNode.ChildCount(); i++ {
					traverse(bodyNode.Child(i), &childName)
				}
			}
			return
		}

		for i := uint(0); i < node.ChildCount(); i++ {
			traverse(node.Child(i), parentName)
		}
	}

	traverse(tree.RootNode(), nil)
	return symbols, nil
}

// ExtractSymbol resolves symbolPath (e.g. "MyClass.myMethod") within
// filePath and returns the corresponding SymbolData, including its source
// code. It corresponds to Python's `TypeScriptParser.extract_symbol`.
func (p *TypeScriptParser) ExtractSymbol(filePath, symbolPath string) (SymbolData, error) {
	source, err := readFile(filePath)
	if err != nil {
		return SymbolData{}, err
	}

	tree, err := p.parseSource(source, p.languageForPath(filePath))
	if err != nil {
		return SymbolData{}, err
	}
	defer tree.Close()

	targetParts := strings.Split(symbolPath, ".")

	var foundNode *tree_sitter.Node
	var foundKind string
	var foundActualNode *tree_sitter.Node

	var findTarget func(node *tree_sitter.Node, depth int)
	findTarget = func(node *tree_sitter.Node, depth int) {
		if foundNode != nil {
			return
		}

		kind, name, actualNode := classifyTSNode(node, source)

		if kind != "" && name != "" && depth < len(targetParts) && name == targetParts[depth] {
			if depth == len(targetParts)-1 {
				foundNode = node
				foundKind = kind
				foundActualNode = actualNode
				return
			}

			// Note: unlike ParseFile (which also descends through
			// "interface_body"), this deeper-level body lookup only checks
			// "class_body"/"object_type", matching Python's extract_symbol
			// exactly (nested members of an interface_declaration cannot be
			// reached via a dotted symbol_path there either). This is an
			// intentional port of an apparent Python-side inconsistency, not
			// a Go-side omission; do not "fix" it without also updating
			// ast_digger/parsers/typescript.py.
			bodyNode := findTSBodyChild(actualNode, "class_body", "object_type")
			if bodyNode != nil {
				for i := uint(0); i < bodyNode.ChildCount(); i++ {
					findTarget(bodyNode.Child(i), depth+1)
				}
			}
			return
		}

		// Falls through here (rather than returning) both when node is not a
		// recognized declaration at all, and when it is one whose name did
		// not match targetParts[depth]: in the latter case this continues
		// searching node's children at the *same* depth (not depth+1),
		// mirroring Python's extract_symbol, where the equivalent recursive
		// call site sits outside the "if kind and name" block entirely.
		for i := uint(0); i < node.ChildCount(); i++ {
			findTarget(node.Child(i), depth)
		}
	}

	root := tree.RootNode()
	for i := uint(0); i < root.ChildCount(); i++ {
		findTarget(root.Child(i), 0)
	}

	if foundNode == nil {
		return SymbolData{}, &apperr.SymbolNotFoundError{
			FilePath:    filePath,
			SymbolPath:  symbolPath,
			Suggestions: suggestSymbols(p.ParseFile, filePath, symbolPath),
		}
	}

	code := foundNode.Utf8Text(source)
	name := targetParts[len(targetParts)-1]

	var parent *string
	if len(targetParts) > 1 {
		p0 := targetParts[0]
		parent = &p0
	}

	startLine := int(foundNode.StartPosition().Row) + 1
	endLine := int(foundNode.EndPosition().Row) + 1

	var docstring *string
	if raw := findJSDoc(foundNode, source); raw != nil {
		cleaned := cleanJSDoc(*raw)
		docstring = &cleaned
	}

	signature := getTSSignature(foundActualNode, source)
	relPath := relFilePath(filePath)

	return SymbolData{
		Name:      name,
		Kind:      foundKind,
		StartLine: startLine,
		EndLine:   endLine,
		Parent:    parent,
		Signature: &signature,
		Docstring: docstring,
		FilePath:  &relPath,
		Code:      &code,
	}, nil
}

// scanTSImports walks node (and its descendants) depth-first looking for an
// import_statement, import_require_clause, or CommonJS
// "const x = require(...)" variable_declarator that brings symbolName into
// scope. It mirrors Python's nested `scan_imports`/`get_node_text` closures
// in `resolve_imports`.
//
// The returned bool reports whether a match was found. originalName is
// symbolName itself unless the match came through a named-import alias (e.g.
// "import { Foo as Bar } from ...") or an aliased dotted-module import (e.g.
// "import * as ns from ..."), in which case it is not adjusted for namespace
// imports (there is no "original name" to resolve within the target module
// for a namespace import, matching Python's behavior of leaving
// original_name as symbolName in that case).
func scanTSImports(node *tree_sitter.Node, source []byte, symbolName string) (importedPath, originalName string, found bool) {
	originalName = symbolName

	var scan func(n *tree_sitter.Node)
	scan = func(n *tree_sitter.Node) {
		if found {
			return
		}

		switch n.Kind() {
		case "import_statement":
			var clause, sourceNode *tree_sitter.Node
			for i := uint(0); i < n.ChildCount(); i++ {
				child := n.Child(i)
				switch child.Kind() {
				case "import_clause":
					clause = child
				case "string":
					sourceNode = child
				}
			}
			if sourceNode != nil && clause != nil {
				srcVal := strings.Trim(nodeText(sourceNode, source), `"'`)

				var named *tree_sitter.Node
				for i := uint(0); i < clause.ChildCount(); i++ {
					if clause.Child(i).Kind() == "named_imports" {
						named = clause.Child(i)
						break
					}
				}
				if named != nil {
					for i := uint(0); i < named.ChildCount(); i++ {
						spec := named.Child(i)
						if spec.Kind() != "import_specifier" {
							continue
						}
						nameNode := spec.ChildByFieldName("name")
						aliasNode := spec.ChildByFieldName("alias")
						if aliasNode != nil && nodeText(aliasNode, source) == symbolName {
							if nameNode != nil {
								originalName = nodeText(nameNode, source)
								importedPath = srcVal
								found = true
								return
							}
						} else if nameNode != nil && nodeText(nameNode, source) == symbolName {
							importedPath = srcVal
							found = true
							return
						}
					}
				}

				var ns *tree_sitter.Node
				for i := uint(0); i < clause.ChildCount(); i++ {
					if clause.Child(i).Kind() == "namespace_import" {
						ns = clause.Child(i)
						break
					}
				}
				if ns != nil {
					for i := uint(0); i < ns.ChildCount(); i++ {
						child := ns.Child(i)
						if child.Kind() == "identifier" && nodeText(child, source) == symbolName {
							importedPath = srcVal
							found = true
							return
						}
					}
				}

				if clause.ChildCount() > 0 && clause.Child(0).Kind() == "identifier" {
					if nodeText(clause.Child(0), source) == symbolName {
						importedPath = srcVal
						found = true
						return
					}
				}
			}

		case "import_require_clause":
			nameNode := n.ChildByFieldName("name")
			if nameNode != nil && nodeText(nameNode, source) == symbolName {
				for i := uint(0); i < n.ChildCount(); i++ {
					child := n.Child(i)
					if child.Kind() == "string" {
						importedPath = strings.Trim(nodeText(child, source), `"'`)
						found = true
						return
					}
				}
			}

		case "variable_declarator":
			valNode := n.ChildByFieldName("value")
			if valNode != nil && valNode.Kind() == "call_expression" {
				funcNode := valNode.ChildByFieldName("function")
				if funcNode != nil && nodeText(funcNode, source) == "require" {
					nameNode := n.ChildByFieldName("name")
					if nameNode != nil && nodeText(nameNode, source) == symbolName {
						argsNode := valNode.ChildByFieldName("arguments")
						if argsNode != nil {
							for i := uint(0); i < argsNode.ChildCount(); i++ {
								child := argsNode.Child(i)
								if child.Kind() == "string" {
									importedPath = strings.Trim(nodeText(child, source), `"'`)
									found = true
									return
								}
							}
						}
					}
				}
			}
		}

		for i := uint(0); i < n.ChildCount(); i++ {
			scan(n.Child(i))
			if found {
				return
			}
		}
	}

	scan(node)
	return importedPath, originalName, found
}

// tsSuffixed replaces path's existing extension (if any) with suffix,
// mirroring pathlib.Path.with_suffix() as used by Python's `resolve_imports`
// to build candidate module file paths (e.g. cand_base.with_suffix(".ts")).
func tsSuffixed(path, suffix string) string {
	ext := filepath.Ext(path)
	return strings.TrimSuffix(path, ext) + suffix
}

// ResolveImports resolves the definition location of symbolName as imported
// by filePath, returning the defining file's path (relative to the current
// working directory / project root) and the 1-indexed line its definition
// starts on. It corresponds to Python's `TypeScriptParser.resolve_imports`.
func (p *TypeScriptParser) ResolveImports(filePath, symbolName string) (string, int, error) {
	source, err := readFile(filePath)
	if err != nil {
		return "", 0, err
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", 0, &apperr.FileAccessError{Path: filePath, Err: err}
	}

	tree, err := p.parseSource(source, p.languageForPath(filePath))
	if err != nil {
		return "", 0, err
	}
	defer tree.Close()

	importedPath, originalName, found := scanTSImports(tree.RootNode(), source, symbolName)
	if !found {
		// Task 14 (go_reviewer's task 4 follow-up): unlike Python's
		// resolve_imports (which has no dedicated "symbol not imported"
		// error type and raises FileAccessError here too), this is
		// deliberately a SymbolNotFoundError, not a FileAccessError: filePath
		// itself was read successfully, the problem is that symbolName isn't
		// imported by it, which maps to exit code 4 (ExitSymbolNotFoundError)
		// rather than 3 (ExitFileAccessError) in design_GOLANG.md's exit
		// code taxonomy. This is an intentional semantic improvement over
		// Python's behavior (see task 14's notes in
		// SPECS/tasks_GOLANG.md and PythonParser.ResolveImports's identical
		// change), not a parity bug.
		return "", 0, &apperr.SymbolNotFoundError{
			FilePath:   filePath,
			SymbolPath: symbolName,
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", 0, &apperr.FileAccessError{Path: filePath, Err: err}
	}
	projectRoot, err := filepath.Abs(cwd)
	if err != nil {
		return "", 0, &apperr.FileAccessError{Path: filePath, Err: err}
	}

	baseDir := filepath.Dir(absPath)

	var candBase string
	switch {
	case strings.HasPrefix(importedPath, "."):
		candBase = filepath.Join(baseDir, importedPath)
	case strings.HasPrefix(importedPath, "@/"):
		candBase = filepath.Join(projectRoot, "src", importedPath[2:])
	default:
		candBase = filepath.Join(projectRoot, importedPath)
	}

	candidates := []string{
		tsSuffixed(candBase, ".ts"),
		tsSuffixed(candBase, ".tsx"),
		tsSuffixed(candBase, ".d.ts"),
		filepath.Join(candBase, "index.ts"),
		filepath.Join(candBase, "index.tsx"),
		filepath.Join(candBase, "index.d.ts"),
	}

	var targetFile string
	for _, cand := range candidates {
		if info, statErr := os.Stat(cand); statErr == nil && !info.IsDir() {
			targetFile = cand
			break
		}
	}

	if targetFile == "" {
		return "", 0, &apperr.FileAccessError{
			Path: filePath,
			Err:  fmt.Errorf("could not resolve imported module %s in %s", importedPath, filePath),
		}
	}

	relTarget := relFilePath(targetFile)

	// A fresh TypeScriptParser instance is used here (mirroring Python's
	// `target_parser = TypeScriptParser()`), rather than reusing p, to keep
	// this call independent of any state left over from parsing filePath
	// above.
	targetParser := NewTypeScriptParser()
	symbols, err := targetParser.ParseFile(targetFile)
	if err != nil {
		// Mirrors Python's fallback: if the resolved target file cannot be
		// parsed, still report its location, defaulting to line 1.
		return relTarget, 1, nil
	}

	for _, sym := range symbols {
		if sym.Name == originalName {
			return relTarget, sym.StartLine, nil
		}
	}
	if len(symbols) > 0 {
		minLine := symbols[0].StartLine
		for _, sym := range symbols[1:] {
			if sym.StartLine < minLine {
				minLine = sym.StartLine
			}
		}
		return relTarget, minLine, nil
	}

	return relTarget, 1, nil
}

// isTSDefinitionIdentifier reports whether node (expected to satisfy Kind()
// == "identifier") denotes a definition site rather than a reference/use:
// the name identifier of an enclosing class/interface/type-alias/function
// declaration or method definition/signature, the name identifier of a
// variable_declarator, or any identifier that is a direct child of a
// required_parameter/formal_parameters node. It mirrors Python's nested
// `is_definition` function in `find_references_in_file`.
//
// Node identity is compared via Id() rather than pointer equality, since
// Child()/Parent()/ChildByFieldName() may return a freshly allocated *Node
// wrapping the same underlying tree-sitter node on each call.
func isTSDefinitionIdentifier(node *tree_sitter.Node) bool {
	parent := node.Parent()
	if parent == nil {
		return false
	}

	switch parent.Kind() {
	case "class_declaration", "interface_declaration", "type_alias_declaration", "function_declaration",
		"method_definition", "method_signature", "variable_declarator":
		nameNode := parent.ChildByFieldName("name")
		return nameNode != nil && nameNode.Id() == node.Id()
	case "required_parameter", "formal_parameters":
		return true
	}
	return false
}

// FindReferencesInFile searches filePath for occurrences where symbolName is
// referenced or invoked, excluding definition sites (see
// isTSDefinitionIdentifier). It corresponds to Python's
// `TypeScriptParser.find_references_in_file`.
func (p *TypeScriptParser) FindReferencesInFile(filePath, symbolName string) ([]ReferenceData, error) {
	// Python's find_references_in_file swallows file-not-found / read
	// errors and returns an empty result rather than propagating an error:
	// this method is called across every file in a project-wide scan, where
	// one unreadable file should not abort the whole search. Mirror that
	// here rather than surfacing readFile's apperr.FileAccessError.
	source, err := readFile(filePath)
	if err != nil {
		return []ReferenceData{}, nil
	}

	if !bytes.Contains(source, []byte(symbolName)) {
		return []ReferenceData{}, nil
	}

	tree, err := p.parseSource(source, p.languageForPath(filePath))
	if err != nil {
		return []ReferenceData{}, nil
	}
	defer tree.Close()

	lines := splitLines(string(source))
	relPath := relFilePath(filePath)

	references := []ReferenceData{}

	var traverse func(node *tree_sitter.Node)
	traverse = func(node *tree_sitter.Node) {
		if node.Kind() == "identifier" {
			text := nodeText(node, source)
			if text == symbolName && !isTSDefinitionIdentifier(node) {
				lineIdx := int(node.StartPosition().Row)
				snippet := ""
				if lineIdx < len(lines) {
					snippet = lines[lineIdx]
				}
				references = append(references, ReferenceData{
					FilePath:    relPath,
					LineNumber:  lineIdx + 1,
					LineContent: strings.TrimSpace(snippet),
				})
			}
		}
		for i := uint(0); i < node.ChildCount(); i++ {
			traverse(node.Child(i))
		}
	}
	traverse(tree.RootNode())

	return references, nil
}

// tsSkeletonReplacement describes a byte range within a source file (a
// function/method/arrow-function body) that GenerateSkeleton replaces with
// "{ /* ... */ }".
type tsSkeletonReplacement struct {
	start int
	end   int
}

// collectTSSkeletonReplacements walks node (and its descendants), recording
// a tsSkeletonReplacement for every function_declaration/method_definition/
// arrow_function/generator_function_declaration node's body. It does not
// recurse into a matched node's own children (mirroring Python's `return`
// immediately after handling the matching case in `collect_blocks`), so
// nested/inner function definitions are left alone: their body is included
// verbatim in the outer function's replaced range.
func collectTSSkeletonReplacements(node *tree_sitter.Node, out *[]tsSkeletonReplacement) {
	switch node.Kind() {
	case "function_declaration", "method_definition", "arrow_function", "generator_function_declaration":
		var body *tree_sitter.Node
		if node.Kind() == "arrow_function" {
			body = node.ChildByFieldName("body")
		} else {
			body = findTSBodyChild(node, "statement_block")
		}
		if body != nil {
			*out = append(*out, tsSkeletonReplacement{
				start: int(body.StartByte()),
				end:   int(body.EndByte()),
			})
		}
		return
	}

	for i := uint(0); i < node.ChildCount(); i++ {
		collectTSSkeletonReplacements(node.Child(i), out)
	}
}

// GenerateSkeleton reconstructs filePath's source with every
// function/method/arrow-function body replaced by "{ /* ... */ }", leaving
// signatures, decorators, and JSDoc comments untouched. It corresponds to
// Python's `TypeScriptParser.generate_skeleton`.
func (p *TypeScriptParser) GenerateSkeleton(filePath string) (string, error) {
	source, err := readFile(filePath)
	if err != nil {
		return "", err
	}

	tree, err := p.parseSource(source, p.languageForPath(filePath))
	if err != nil {
		return "", err
	}
	defer tree.Close()

	var replacements []tsSkeletonReplacement
	collectTSSkeletonReplacements(tree.RootNode(), &replacements)

	// Sorted by start byte descending so each replacement is applied before
	// any byte-offset shift it causes could affect the (not yet processed)
	// replacements that precede it in the file, mirroring Python's
	// `blocks_to_replace.sort(key=lambda x: x[0], reverse=True)`.
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })

	result := append([]byte(nil), source...)
	for _, r := range replacements {
		replacement := []byte("{ /* ... */ }")
		result = append(result[:r.start], append(replacement, result[r.end:]...)...)
	}

	return string(result), nil
}
