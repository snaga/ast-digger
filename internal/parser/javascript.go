// Package parser: javascript.go implements the Parser interface for
// JavaScript source files using the tree-sitter-javascript grammar. It
// follows the same structure and conventions as typescript.go
// (TypeScriptParser), adapted for JavaScript's simpler (untyped) symbol
// model: there is no interface_declaration/type_alias_declaration/
// method_signature (none of those node kinds exist in JavaScript's
// grammar).
//
// Unlike TypeScript (which needs a dedicated TSX grammar to parse JSX
// syntax), a single tree-sitter-javascript grammar parses ".js"/".jsx"
// (and ".mjs"/".cjs") source uniformly, JSX elements included -- so
// JavaScriptParser needs only one *tree_sitter.Language, not two.
package parser

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"

	"github.com/snaga/ast-digger/internal/apperr"
)

// JavaScriptParser implements the Parser interface for JavaScript source
// files (".js", ".jsx", ".mjs", ".cjs").
type JavaScriptParser struct {
	lang *tree_sitter.Language
}

// Compile-time assertion that JavaScriptParser satisfies the Parser
// interface.
var _ Parser = (*JavaScriptParser)(nil)

// NewJavaScriptParser constructs a JavaScriptParser with the
// tree-sitter-javascript grammar loaded.
func NewJavaScriptParser() *JavaScriptParser {
	return &JavaScriptParser{
		lang: tree_sitter.NewLanguage(tree_sitter_javascript.Language()),
	}
}

// parseSource parses source using a fresh tree-sitter parser configured with
// the JavaScript grammar. The caller owns the returned tree and must call
// tree.Close() once done with it.
func (p *JavaScriptParser) parseSource(source []byte) (*tree_sitter.Tree, error) {
	ts := tree_sitter.NewParser()
	defer ts.Close()

	if err := ts.SetLanguage(p.lang); err != nil {
		return nil, fmt.Errorf("failed to set tree-sitter-javascript language: %w", err)
	}

	tree := ts.Parse(source, nil)
	if tree == nil {
		return nil, fmt.Errorf("tree-sitter-javascript failed to parse source")
	}
	return tree, nil
}

// findJSDocComment walks up from node through any enclosing export_statement
// / lexical_declaration / variable_declaration wrapper nodes, then looks at
// the immediately preceding sibling: if it is a "/**"-prefixed comment node,
// its raw text (including the "/**"/"*/" delimiters) is returned; an
// "export" sibling is skipped over (continuing to look further back);
// anything else stops the search. It mirrors TypeScriptParser's findJSDoc.
func findJSDocComment(node *tree_sitter.Node, source []byte) *string {
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
		case "export":
			sibling = sibling.PrevSibling()
		default:
			return nil
		}
	}
	return nil
}

// findJSBodyChild returns node's first direct child whose Kind() is one of
// kinds, or nil if none is found.
func findJSBodyChild(node *tree_sitter.Node, kinds ...string) *tree_sitter.Node {
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

// getJSSignature builds node's signature text: everything from node's start
// up to the start of its first descendant that is a "statement_block" or
// "class_body" node (found via a depth-first, first-match search), or up to
// node's own end if no such body node exists. A trailing "=" (e.g. from
// "const f = () => {") is stripped. It mirrors TypeScriptParser's
// getTSSignature, minus the TSX-only body kinds ("object_type"/
// "interface_body") and decorator handling (JavaScript's grammar has no
// applicable decorator support here).
func getJSSignature(node *tree_sitter.Node, source []byte) string {
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
		case "statement_block", "class_body":
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
	return sig
}

// classifyJSNode reports whether node is one of the JavaScript declaration
// forms recognized by ParseFile/ExtractSymbol, returning its symbol kind
// ("class", "function", or "method") and name, or kind == "" if node is not
// a recognized declaration.
//
// actualNode is the node subsequent signature/body lookups should be
// anchored on: it equals node itself in every case except when node is a
// "lexical_declaration"/"variable_declaration" wrapping a
// "variable_declarator" whose value is an arrow_function/function_expression
// (e.g. "const f = () => {...}"), where node (the declaration statement, not
// the inner variable_declarator) is kept as the anchor.
//
// It mirrors TypeScriptParser's classifyTSNode, minus the
// interface_declaration/type_alias_declaration/method_signature cases (none
// of which exist in JavaScript's grammar).
func classifyJSNode(node *tree_sitter.Node, source []byte) (kind, name string, actualNode *tree_sitter.Node) {
	actualNode = node

	switch node.Kind() {
	case "class_declaration":
		return "class", nodeText(node.ChildByFieldName("name"), source), actualNode
	case "function_declaration":
		return "function", nodeText(node.ChildByFieldName("name"), source), actualNode
	case "method_definition":
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

// ParseFile parses filePath and returns the list of symbols (classes,
// functions, methods) found in it.
func (p *JavaScriptParser) ParseFile(filePath string) ([]SymbolData, error) {
	source, err := readFile(filePath)
	if err != nil {
		return nil, err
	}
	return p.ParseBytes(source, filePath)
}

// ParseBytes parses source and returns the list of symbols found in it.
func (p *JavaScriptParser) ParseBytes(source []byte, filePath string) ([]SymbolData, error) {
	relPath := relFilePath(filePath)

	tree, err := p.parseSource(source)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	if tree.RootNode().HasError() {
		fmt.Fprintf(os.Stderr, "Warning: Syntax errors detected in %s\n", filePath)
	}

	// symbols is initialized as an empty (non-nil) slice, rather than a nil
	// slice, so that ParseFile returning zero symbols still marshals to JSON
	// as "[]" instead of "null".
	symbols := []SymbolData{}

	var traverse func(node *tree_sitter.Node, parentName *string)
	traverse = func(node *tree_sitter.Node, parentName *string) {
		kind, name, actualNode := classifyJSNode(node, source)

		if kind != "" && name != "" {
			startLine := int(node.StartPosition().Row) + 1
			endLine := int(node.EndPosition().Row) + 1

			var docstring *string
			if raw := findJSDocComment(node, source); raw != nil {
				cleaned := cleanJSDoc(*raw)
				docstring = &cleaned
			}

			signature := getJSSignature(actualNode, source)

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

			bodyNode := findJSBodyChild(actualNode, "class_body")
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
// code.
func (p *JavaScriptParser) ExtractSymbol(filePath, symbolPath string) (SymbolData, error) {
	source, err := readFile(filePath)
	if err != nil {
		return SymbolData{}, err
	}

	tree, err := p.parseSource(source)
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

		kind, name, actualNode := classifyJSNode(node, source)

		if kind != "" && name != "" && depth < len(targetParts) && name == targetParts[depth] {
			if depth == len(targetParts)-1 {
				foundNode = node
				foundKind = kind
				foundActualNode = actualNode
				return
			}

			bodyNode := findJSBodyChild(actualNode, "class_body")
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
		// searching node's children at the *same* depth (not depth+1).
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
	if raw := findJSDocComment(foundNode, source); raw != nil {
		cleaned := cleanJSDoc(*raw)
		docstring = &cleaned
	}

	signature := getJSSignature(foundActualNode, source)
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

// scanJSImports walks node (and its descendants) depth-first looking for an
// import_statement or CommonJS "const x = require(...)" variable_declarator
// that brings symbolName into scope. It mirrors TypeScriptParser's
// scanTSImports, minus the TS-only "import x = require(...)"
// (import_require_clause) form, which does not exist in JavaScript's
// grammar.
//
// The returned bool reports whether a match was found. originalName is
// symbolName itself unless the match came through a named-import alias
// (e.g. "import { Foo as Bar } from ..."); it is not adjusted for namespace
// imports, matching TypeScriptParser's behavior.
func scanJSImports(node *tree_sitter.Node, source []byte, symbolName string) (importedPath, originalName string, found bool) {
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

// jsSuffixed replaces path's existing extension (if any) with suffix,
// mirroring TypeScriptParser's tsSuffixed, used to build candidate module
// file paths (e.g. jsSuffixed(candBase, ".js")).
func jsSuffixed(path, suffix string) string {
	ext := filepath.Ext(path)
	return strings.TrimSuffix(path, ext) + suffix
}

// ResolveImports resolves the definition location of symbolName as imported
// by filePath, returning the defining file's path (relative to the current
// working directory / project root) and the 1-indexed line its definition
// starts on. It mirrors TypeScriptParser.ResolveImports, except candidate
// module file extensions are tried in ".js"/".jsx" priority order (rather
// than TypeScript's ".ts"/".tsx" order), with ".mjs"/".cjs" tried afterward
// as further fallbacks.
func (p *JavaScriptParser) ResolveImports(filePath, symbolName string) (string, int, error) {
	source, err := readFile(filePath)
	if err != nil {
		return "", 0, err
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", 0, &apperr.FileAccessError{Path: filePath, Err: err}
	}

	tree, err := p.parseSource(source)
	if err != nil {
		return "", 0, err
	}
	defer tree.Close()

	importedPath, originalName, found := scanJSImports(tree.RootNode(), source, symbolName)
	if !found {
		// As with TypeScriptParser.ResolveImports (task 14), this is
		// deliberately a SymbolNotFoundError (not a FileAccessError):
		// filePath itself was read successfully, the problem is that
		// symbolName isn't imported by it.
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
		jsSuffixed(candBase, ".js"),
		jsSuffixed(candBase, ".jsx"),
		jsSuffixed(candBase, ".mjs"),
		jsSuffixed(candBase, ".cjs"),
		filepath.Join(candBase, "index.js"),
		filepath.Join(candBase, "index.jsx"),
		filepath.Join(candBase, "index.mjs"),
		filepath.Join(candBase, "index.cjs"),
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

	// A fresh JavaScriptParser instance is used here, rather than reusing p,
	// to keep this call independent of any state left over from parsing
	// filePath above.
	targetParser := NewJavaScriptParser()
	symbols, err := targetParser.ParseFile(targetFile)
	if err != nil {
		// Mirrors TypeScriptParser's fallback: if the resolved target file
		// cannot be parsed, still report its location, defaulting to line 1.
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

// isJSDefinitionIdentifier reports whether node (expected to satisfy Kind()
// == "identifier") denotes a definition site rather than a reference/use:
// the name identifier of an enclosing class/function declaration or method
// definition, the name identifier of a variable_declarator, or any
// identifier that is a direct child of a formal_parameters node. It mirrors
// TypeScriptParser's isTSDefinitionIdentifier, minus the TS-only
// "required_parameter" wrapper (JavaScript's formal_parameters holds bare
// identifier/pattern children directly, with no such wrapper).
//
// Node identity is compared via Id() rather than pointer equality, since
// Child()/Parent()/ChildByFieldName() may return a freshly allocated *Node
// wrapping the same underlying tree-sitter node on each call.
func isJSDefinitionIdentifier(node *tree_sitter.Node) bool {
	parent := node.Parent()
	if parent == nil {
		return false
	}

	switch parent.Kind() {
	case "class_declaration", "function_declaration", "method_definition", "variable_declarator":
		nameNode := parent.ChildByFieldName("name")
		return nameNode != nil && nameNode.Id() == node.Id()
	case "formal_parameters":
		return true
	}
	return false
}

// FindReferencesInFile searches filePath for occurrences where symbolName is
// referenced or invoked, excluding definition sites (see
// isJSDefinitionIdentifier). Occurrences inside comments and plain string
// literals are excluded implicitly: tree-sitter-javascript's grammar
// represents "comment" and "string" nodes as opaque leaves with no
// "identifier" descendants, so the traversal below never visits any text
// inside them. Template-literal ("template_string") text is excluded the
// same way, except for genuine "${...}" substitution expressions, which do
// parse into real identifier/expression nodes and are correctly counted as
// references.
func (p *JavaScriptParser) FindReferencesInFile(filePath, symbolName string) ([]ReferenceData, error) {
	// Read/parse errors are swallowed (an empty result is returned) rather
	// than propagated, matching TypeScriptParser.FindReferencesInFile: this
	// method is called across every file in a project-wide scan, where one
	// unreadable file should not abort the whole search.
	source, err := readFile(filePath)
	if err != nil {
		return []ReferenceData{}, nil
	}

	if !bytes.Contains(source, []byte(symbolName)) {
		return []ReferenceData{}, nil
	}

	tree, err := p.parseSource(source)
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
			if text == symbolName && !isJSDefinitionIdentifier(node) {
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

// jsSkeletonReplacement describes a byte range within a source file (a
// function/method/arrow-function body) that GenerateSkeleton replaces with
// "{ /* ... */ }".
type jsSkeletonReplacement struct {
	start int
	end   int
}

// collectJSSkeletonReplacements walks node (and its descendants), recording
// a jsSkeletonReplacement for every function_declaration/method_definition/
// arrow_function/generator_function_declaration node's body. It does not
// recurse into a matched node's own children, so nested/inner function
// definitions are left alone: their body is included verbatim in the outer
// function's replaced range. It mirrors TypeScriptParser's
// collectTSSkeletonReplacements.
func collectJSSkeletonReplacements(node *tree_sitter.Node, out *[]jsSkeletonReplacement) {
	switch node.Kind() {
	case "function_declaration", "method_definition", "arrow_function", "generator_function_declaration":
		var body *tree_sitter.Node
		if node.Kind() == "arrow_function" {
			body = node.ChildByFieldName("body")
		} else {
			body = findJSBodyChild(node, "statement_block")
		}
		if body != nil {
			*out = append(*out, jsSkeletonReplacement{
				start: int(body.StartByte()),
				end:   int(body.EndByte()),
			})
		}
		return
	}

	for i := uint(0); i < node.ChildCount(); i++ {
		collectJSSkeletonReplacements(node.Child(i), out)
	}
}

// GenerateSkeleton reconstructs filePath's source with every
// function/method/arrow-function body replaced by "{ /* ... */ }", leaving
// signatures and JSDoc comments untouched.
func (p *JavaScriptParser) GenerateSkeleton(filePath string) (string, error) {
	source, err := readFile(filePath)
	if err != nil {
		return "", err
	}

	tree, err := p.parseSource(source)
	if err != nil {
		return "", err
	}
	defer tree.Close()

	var replacements []jsSkeletonReplacement
	collectJSSkeletonReplacements(tree.RootNode(), &replacements)

	// Sorted by start byte descending so each replacement is applied before
	// any byte-offset shift it causes could affect the (not yet processed)
	// replacements that precede it in the file.
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })

	result := append([]byte(nil), source...)
	for _, r := range replacements {
		replacement := []byte("{ /* ... */ }")
		result = append(result[:r.start], append(replacement, result[r.end:]...)...)
	}

	return string(result), nil
}
