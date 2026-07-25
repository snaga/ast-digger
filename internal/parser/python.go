// Package parser: python.go implements the Parser interface for Python
// source files using the tree-sitter-python grammar. It corresponds to
// ast_digger/parsers/python.py in the Python implementation.
package parser

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"

	"github.com/snaga/ast-digger/internal/apperr"
)

// PythonParser implements the Parser interface for Python source files.
//
// It corresponds to the Python `PythonParser` class defined in
// ast_digger/parsers/python.py.
type PythonParser struct {
	lang *tree_sitter.Language
}

// Compile-time assertion that PythonParser satisfies the Parser interface.
var _ Parser = (*PythonParser)(nil)

// NewPythonParser constructs a PythonParser with the tree-sitter-python
// grammar loaded.
func NewPythonParser() *PythonParser {
	return &PythonParser{lang: tree_sitter.NewLanguage(tree_sitter_python.Language())}
}

// parseSource parses source using a fresh tree-sitter parser configured with
// the Python grammar. The caller owns the returned tree and must call
// tree.Close() once done with it.
func (p *PythonParser) parseSource(source []byte) (*tree_sitter.Tree, error) {
	ts := tree_sitter.NewParser()
	defer ts.Close()

	if err := ts.SetLanguage(p.lang); err != nil {
		return nil, fmt.Errorf("failed to set tree-sitter-python language: %w", err)
	}

	tree := ts.Parse(source, nil)
	if tree == nil {
		return nil, fmt.Errorf("tree-sitter-python failed to parse source")
	}
	return tree, nil
}

// readFile reads filePath's contents, translating filesystem errors into
// apperr.FileAccessError as done by Python's `_read_file`.
func readFile(filePath string) ([]byte, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, &apperr.FileAccessError{Path: filePath, Err: err}
	}
	return data, nil
}

// fileExists reports whether path refers to a file (or directory) that can
// be stat'd successfully.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// relFilePath returns filePath relative to the current working directory
// (the project root), with path separators normalized to "/", mirroring
// Python's `_get_rel_path`. If filePath cannot be made relative (e.g. it
// lives outside the working directory), filePath is returned unmodified.
func relFilePath(filePath string) string {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return filePath
	}
	cwd, err := os.Getwd()
	if err != nil {
		return filePath
	}
	rel, err := filepath.Rel(cwd, abs)
	if err != nil {
		return filePath
	}
	return filepath.ToSlash(rel)
}

// getNodeName returns the text of node's first "identifier" child, or "" if
// none is found. It mirrors Python's `_get_node_name`.
func getNodeName(node *tree_sitter.Node, source []byte) string {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "identifier" {
			return strings.TrimSpace(child.Utf8Text(source))
		}
	}
	return ""
}

// findChildByKind returns node's first direct child whose Kind() equals
// kind, or nil if none is found.
func findChildByKind(node *tree_sitter.Node, kind string) *tree_sitter.Node {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == kind {
			return child
		}
	}
	return nil
}

// getDocstring extracts the docstring from a definition's "block" node, if
// present, stripping the surrounding quote characters. It mirrors Python's
// `_get_docstring`.
func getDocstring(blockNode *tree_sitter.Node, source []byte) *string {
	if blockNode == nil || blockNode.Kind() != "block" || blockNode.ChildCount() == 0 {
		return nil
	}

	firstChild := blockNode.Child(0)
	if firstChild.Kind() != "expression_statement" || firstChild.ChildCount() == 0 {
		return nil
	}

	stmtChild := firstChild.Child(0)
	if stmtChild.Kind() != "string" {
		return nil
	}

	doc := strings.TrimSpace(stmtChild.Utf8Text(source))
	switch {
	case strings.HasPrefix(doc, `"""`) || strings.HasPrefix(doc, `'''`):
		if len(doc) >= 6 {
			doc = strings.TrimSpace(doc[3 : len(doc)-3])
		}
	case strings.HasPrefix(doc, `"`) || strings.HasPrefix(doc, `'`):
		if len(doc) >= 2 {
			doc = strings.TrimSpace(doc[1 : len(doc)-1])
		}
	}
	return &doc
}

// getSignature builds the definition's signature text (everything from the
// definition's start up to its "block" child, or its own end if it has no
// block), stripping a trailing ":" and prepending decorator text. It mirrors
// Python's `_get_signature`.
func getSignature(node *tree_sitter.Node, decorators []*tree_sitter.Node, source []byte) string {
	blockNode := findChildByKind(node, "block")
	endByte := node.EndByte()
	if blockNode != nil {
		endByte = blockNode.StartByte()
	}

	sig := strings.TrimSpace(string(source[node.StartByte():endByte]))
	if strings.HasSuffix(sig, ":") {
		sig = strings.TrimSpace(sig[:len(sig)-1])
	}

	if len(decorators) > 0 {
		decStrs := make([]string, 0, len(decorators))
		for _, dec := range decorators {
			decStrs = append(decStrs, strings.TrimSpace(dec.Utf8Text(source)))
		}
		sig = strings.Join(decStrs, "\n") + "\n" + sig
	}
	return sig
}

// ParseFile parses filePath and returns the list of symbols (classes,
// functions, methods) found in it. It corresponds to Python's
// `PythonParser.parse_file`.
func (p *PythonParser) ParseFile(filePath string) ([]SymbolData, error) {
	source, err := readFile(filePath)
	if err != nil {
		return nil, err
	}
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
	// as "[]" (matching Python's `json.dumps([])`) instead of "null".
	symbols := []SymbolData{}

	var traverse func(node *tree_sitter.Node, parentClass *string)
	var processDefinition func(node *tree_sitter.Node, parentClass *string, decorators []*tree_sitter.Node)

	traverse = func(node *tree_sitter.Node, parentClass *string) {
		switch node.Kind() {
		case "decorated_definition":
			var decorators []*tree_sitter.Node
			var definitionNode *tree_sitter.Node
			for i := uint(0); i < node.ChildCount(); i++ {
				child := node.Child(i)
				switch child.Kind() {
				case "decorator":
					decorators = append(decorators, child)
				case "class_definition", "function_definition":
					definitionNode = child
				}
			}
			// definitionNode (the inner class/function node) is passed to
			// processDefinition instead of node (the decorated_definition
			// itself), so the resulting symbol's StartLine/EndLine exclude
			// the decorator lines. This intentionally mirrors Python's
			// `parse_file` in ast_digger/parsers/python.py; do not "fix"
			// this to include the decorator lines without also updating
			// the Python source. (See ExtractSymbol below, which is
			// intentionally asymmetric with this behavior.)
			if definitionNode != nil {
				processDefinition(definitionNode, parentClass, decorators)
			}
			return
		case "class_definition", "function_definition":
			processDefinition(node, parentClass, nil)
			return
		}

		for i := uint(0); i < node.ChildCount(); i++ {
			traverse(node.Child(i), parentClass)
		}
	}

	processDefinition = func(node *tree_sitter.Node, parentClass *string, decorators []*tree_sitter.Node) {
		name := getNodeName(node, source)
		if name == "" {
			return
		}

		startLine := int(node.StartPosition().Row) + 1
		endLine := int(node.EndPosition().Row) + 1
		blockNode := findChildByKind(node, "block")
		docstring := getDocstring(blockNode, source)
		signature := getSignature(node, decorators, source)

		if node.Kind() == "class_definition" {
			className := name
			symbols = append(symbols, SymbolData{
				Name:      name,
				Kind:      "class",
				StartLine: startLine,
				EndLine:   endLine,
				Parent:    parentClass,
				Signature: &signature,
				Docstring: docstring,
				FilePath:  &relPath,
			})
			if blockNode != nil {
				for i := uint(0); i < blockNode.ChildCount(); i++ {
					traverse(blockNode.Child(i), &className)
				}
			}
			return
		}

		kind := "function"
		if parentClass != nil {
			kind = "method"
		}
		symbols = append(symbols, SymbolData{
			Name:      name,
			Kind:      kind,
			StartLine: startLine,
			EndLine:   endLine,
			Parent:    parentClass,
			Signature: &signature,
			Docstring: docstring,
			FilePath:  &relPath,
		})
	}

	traverse(tree.RootNode(), nil)
	return symbols, nil
}

// ExtractSymbol resolves symbolPath (e.g. "MyClass.my_method") within
// filePath and returns the corresponding SymbolData, including its source
// code. It corresponds to Python's `PythonParser.extract_symbol`.
func (p *PythonParser) ExtractSymbol(filePath, symbolPath string) (SymbolData, error) {
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

	var findTarget func(node *tree_sitter.Node, depth int)
	findTarget = func(node *tree_sitter.Node, depth int) {
		if foundNode != nil {
			return
		}

		actualNode := node
		if node.Kind() == "decorated_definition" {
			for i := uint(0); i < node.ChildCount(); i++ {
				child := node.Child(i)
				if child.Kind() == "class_definition" || child.Kind() == "function_definition" {
					actualNode = child
				}
			}
		}

		if actualNode.Kind() == "class_definition" || actualNode.Kind() == "function_definition" {
			name := getNodeName(actualNode, source)
			if depth < len(targetParts) && name == targetParts[depth] {
				if depth == len(targetParts)-1 {
					// Unlike ParseFile (which resolves StartLine/EndLine
					// from the inner class/function node and excludes
					// decorator lines), foundNode here is kept as the
					// decorated_definition node itself when present, so the
					// returned symbol's StartLine/EndLine/Code include the
					// decorator lines. This intentionally mirrors Python's
					// `extract_symbol` in ast_digger/parsers/python.py,
					// which is likewise asymmetric with `parse_file`; do
					// not "fix" this without also updating the Python
					// source.
					if node.Kind() == "decorated_definition" {
						foundNode = node
					} else {
						foundNode = actualNode
					}
					return
				}
				blockNode := findChildByKind(actualNode, "block")
				if blockNode != nil {
					for i := uint(0); i < blockNode.ChildCount(); i++ {
						findTarget(blockNode.Child(i), depth+1)
					}
				}
			}
			return
		}

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

	actualNode := foundNode
	var decorators []*tree_sitter.Node
	if foundNode.Kind() == "decorated_definition" {
		for i := uint(0); i < foundNode.ChildCount(); i++ {
			child := foundNode.Child(i)
			switch child.Kind() {
			case "decorator":
				decorators = append(decorators, child)
			case "class_definition", "function_definition":
				actualNode = child
			}
		}
	}

	code := foundNode.Utf8Text(source)
	name := targetParts[len(targetParts)-1]

	kind := "function"
	if actualNode.Kind() == "class_definition" {
		kind = "class"
	} else if strings.Contains(symbolPath, ".") {
		kind = "method"
	}

	var parent *string
	if strings.Contains(symbolPath, ".") {
		p0 := targetParts[0]
		parent = &p0
	}

	startLine := int(foundNode.StartPosition().Row) + 1
	endLine := int(foundNode.EndPosition().Row) + 1
	blockNode := findChildByKind(actualNode, "block")
	docstring := getDocstring(blockNode, source)
	signature := getSignature(actualNode, decorators, source)
	relPath := relFilePath(filePath)

	return SymbolData{
		Name:      name,
		Kind:      kind,
		StartLine: startLine,
		EndLine:   endLine,
		Parent:    parent,
		Signature: &signature,
		Docstring: docstring,
		FilePath:  &relPath,
		Code:      &code,
	}, nil
}

// pythonImportMatch describes an import statement/from-statement, found by
// scanPythonImports, that brings symbolName into filePath's scope.
type pythonImportMatch struct {
	// Module is the dotted module path being imported from (e.g. "os.path",
	// or "" for a bare "from . import x").
	Module string
	// IsRelative is true for an import_from_statement using one or more
	// leading dots (i.e. it had a relative_import/relative_source/
	// import_prefix child).
	IsRelative bool
	// DotsCount is the number of leading dots for a relative import.
	DotsCount int
	// OriginalName is the name symbolName resolves to within Module: either
	// symbolName itself, or (for "as" aliases, and for dotted
	// "import x.y as z") the un-aliased name to look up in the target
	// file's parsed symbol list.
	OriginalName string
}

// scanPythonImports walks node (and its descendants) depth-first looking for
// an import_statement or import_from_statement that brings symbolName into
// scope. It mirrors the nested `scan_imports`/`check_names` closures in
// Python's `resolve_imports`.
//
// The second return value reports whether a match was found; a matched
// Module of "" (e.g. "from . import x") is a valid result distinct from "no
// match found", so found cannot be inferred from Module alone.
func scanPythonImports(node *tree_sitter.Node, source []byte, symbolName string) (pythonImportMatch, bool) {
	match := pythonImportMatch{OriginalName: symbolName}
	found := false

	text := func(n *tree_sitter.Node) string {
		if n == nil {
			return ""
		}
		return strings.TrimSpace(n.Utf8Text(source))
	}

	// checkNames looks for symbolName among the names introduced by an
	// import_from_statement's name list (the children following the
	// "import" keyword), recording the un-aliased original name into
	// match.OriginalName when the match came from an "as" alias.
	var checkNames func(n *tree_sitter.Node) bool
	checkNames = func(n *tree_sitter.Node) bool {
		switch n.Kind() {
		case "aliased_import":
			var nameNode, aliasNode *tree_sitter.Node
			for i := uint(0); i < n.ChildCount(); i++ {
				sub := n.Child(i)
				switch sub.Kind() {
				case "dotted_name":
					nameNode = sub
				case "identifier":
					aliasNode = sub
				}
			}
			if aliasNode != nil && text(aliasNode) == symbolName {
				if nameNode != nil {
					match.OriginalName = text(nameNode)
				}
				return true
			}
		case "dotted_name", "identifier":
			if text(n) == symbolName {
				match.OriginalName = symbolName
				return true
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			if checkNames(n.Child(i)) {
				return true
			}
		}
		return false
	}

	var scan func(n *tree_sitter.Node)
	scan = func(n *tree_sitter.Node) {
		if found {
			return
		}

		switch n.Kind() {
		case "import_statement":
			for i := uint(0); i < n.ChildCount(); i++ {
				child := n.Child(i)
				switch child.Kind() {
				case "dotted_name":
					modName := text(child)
					if modName == symbolName {
						match.Module = modName
						found = true
						return
					}
				case "aliased_import":
					var nameNode, aliasNode *tree_sitter.Node
					for j := uint(0); j < child.ChildCount(); j++ {
						sub := child.Child(j)
						switch sub.Kind() {
						case "dotted_name":
							nameNode = sub
						case "identifier":
							aliasNode = sub
						}
					}
					if aliasNode != nil && text(aliasNode) == symbolName {
						match.Module = text(nameNode)
						parts := strings.Split(match.Module, ".")
						match.OriginalName = parts[len(parts)-1]
						found = true
						return
					}
				}
			}

		case "import_from_statement":
			modName := ""
			hasPrefix := false
			prefixDots := 0
			for i := uint(0); i < n.ChildCount(); i++ {
				child := n.Child(i)
				switch child.Kind() {
				case "relative_import":
					hasPrefix = true
					for j := uint(0); j < child.ChildCount(); j++ {
						sub := child.Child(j)
						switch sub.Kind() {
						case "import_prefix":
							prefixDots = strings.Count(text(sub), ".")
						case "dotted_name":
							modName = text(sub)
						}
					}
				case "relative_source":
					// Not produced by the tree-sitter-python grammar version
					// this package currently vendors (dots + dotted module
					// name are represented via "relative_import" above
					// instead), but Python's resolve_imports handles it, so
					// it is kept here for forward/backward grammar
					// compatibility.
					modName = text(child)
					hasPrefix = true
					prefixDots = strings.Count(modName, ".")
					modName = strings.TrimLeft(modName, ".")
				case "import_prefix":
					hasPrefix = true
					prefixDots += strings.Count(text(child), ".")
				case "dotted_name":
					if modName == "" {
						modName = text(child)
					}
				}
			}

			isImported := false
			importKwFound := false
			for i := uint(0); i < n.ChildCount(); i++ {
				child := n.Child(i)
				if child.Kind() == "import" {
					importKwFound = true
					continue
				}
				if importKwFound && checkNames(child) {
					isImported = true
					break
				}
			}

			if isImported {
				match.Module = modName
				match.IsRelative = hasPrefix
				match.DotsCount = prefixDots
				found = true
				return
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
	return match, found
}

// ResolveImports resolves the definition location of symbolName as imported
// by filePath, returning the defining file's path (relative to the current
// working directory / project root) and the 1-indexed line its definition
// starts on. It corresponds to Python's `PythonParser.resolve_imports`.
func (p *PythonParser) ResolveImports(filePath, symbolName string) (string, int, error) {
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

	match, found := scanPythonImports(tree.RootNode(), source, symbolName)
	if !found {
		// Task 14 (go_reviewer's task 4 follow-up): unlike Python's
		// resolve_imports (which has no dedicated "symbol not imported"
		// error type and raises FileAccessError here too), this is
		// deliberately a SymbolNotFoundError, not a FileAccessError:
		// filePath itself was read successfully, the problem is that
		// symbolName isn't imported by it, which maps to exit code 4
		// (ExitSymbolNotFoundError) rather than 3 (ExitFileAccessError) in
		// design_GOLANG.md's exit code taxonomy. This is an intentional
		// semantic improvement over Python's behavior (see task 14's notes
		// in SPECS/tasks_GOLANG.md), not a parity bug.
		return "", 0, &apperr.SymbolNotFoundError{
			FilePath:   filePath,
			SymbolPath: symbolName,
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", 0, &apperr.FileAccessError{Path: filePath, Err: err}
	}

	var baseDir string
	if match.IsRelative {
		baseDir = filepath.Dir(absPath)
		for i := 0; i < match.DotsCount-1; i++ {
			baseDir = filepath.Dir(baseDir)
		}
	} else {
		baseDir = cwd
	}

	var modParts []string
	if match.Module != "" {
		modParts = strings.Split(match.Module, ".")
	}
	if len(modParts) == 0 && match.IsRelative {
		modParts = []string{match.OriginalName}
	}

	pyParts := make([]string, 0, len(modParts)+1)
	pyParts = append(pyParts, baseDir)
	pyParts = append(pyParts, modParts...)
	candidatePy := filepath.Join(pyParts...) + ".py"

	initParts := make([]string, 0, len(modParts)+2)
	initParts = append(initParts, baseDir)
	initParts = append(initParts, modParts...)
	initParts = append(initParts, "__init__.py")
	candidateInit := filepath.Join(initParts...)

	var targetFile string
	switch {
	case fileExists(candidatePy):
		targetFile = candidatePy
	case fileExists(candidateInit):
		targetFile = candidateInit
	default:
		return "", 0, &apperr.FileAccessError{
			Path: filePath,
			Err:  fmt.Errorf("could not resolve imported module %s in %s", match.Module, filePath),
		}
	}

	relTarget := relFilePath(targetFile)

	// A fresh PythonParser instance is used here (mirroring Python's
	// `target_parser = PythonParser()`), rather than reusing p / its
	// tree-sitter parser, to keep this call independent of any state left
	// over from parsing filePath above.
	targetParser := NewPythonParser()
	symbols, err := targetParser.ParseFile(targetFile)
	if err != nil {
		// Mirrors Python's fallback: if the resolved target file cannot be
		// parsed, still report its location, defaulting to line 1.
		return relTarget, 1, nil
	}

	for _, sym := range symbols {
		if sym.Name == match.OriginalName {
			return relTarget, sym.StartLine, nil
		}
	}
	return relTarget, 1, nil
}

// isPythonDefinitionIdentifier reports whether node (expected to satisfy
// Kind() == "identifier") denotes a definition site rather than a
// reference/use: either the name identifier of an enclosing
// class_definition/function_definition, or a parameter name (an identifier
// whose ancestor chain includes a "parameters" node). It mirrors Python's
// nested `is_definition` function in `find_references_in_file`.
//
// Node identity is compared via Id() rather than pointer equality, since
// Child()/Parent() may return a freshly allocated *Node wrapping the same
// underlying tree-sitter node on each call.
func isPythonDefinitionIdentifier(node *tree_sitter.Node) bool {
	parent := node.Parent()
	if parent == nil {
		return false
	}

	if parent.Kind() == "class_definition" || parent.Kind() == "function_definition" {
		for i := uint(0); i < parent.ChildCount(); i++ {
			child := parent.Child(i)
			if child.Kind() == "identifier" && child.Id() == node.Id() {
				return true
			}
		}
	}

	for curr := parent; curr != nil; curr = curr.Parent() {
		if curr.Kind() == "parameters" {
			return true
		}
	}
	return false
}

// splitLines splits s into lines the way Python's str.splitlines() does for
// the line-ending conventions relevant to source files ("\n", "\r\n", "\r"),
// without retaining the line terminators. It is used to look up the source
// line a reference occurs on, mirroring Python's `find_references_in_file`.
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(s, "\n")
}

// FindReferencesInFile searches filePath for occurrences where symbolName is
// referenced or invoked, excluding definition sites (see
// isPythonDefinitionIdentifier). It corresponds to Python's
// `PythonParser.find_references_in_file`.
func (p *PythonParser) FindReferencesInFile(filePath, symbolName string) ([]ReferenceData, error) {
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

	tree, err := p.parseSource(source)
	if err != nil {
		return []ReferenceData{}, nil
	}
	defer tree.Close()

	lines := splitLines(string(source))
	// Note: unlike ParseFile/ExtractSymbol (which use relFilePath, falling
	// back to the original, possibly-relative filePath if it cannot be made
	// relative to the working directory), Python's find_references_in_file
	// falls back to the resolved *absolute* path in that case. relFilePath
	// is reused here for consistency with the rest of this file; the
	// fallback case only differs for paths outside the project root, which
	// is not the common case this method is used for (project-wide scans
	// rooted at the working directory).
	relPath := relFilePath(filePath)

	references := []ReferenceData{}

	var traverse func(node *tree_sitter.Node)
	traverse = func(node *tree_sitter.Node) {
		if node.Kind() == "identifier" {
			text := strings.TrimSpace(node.Utf8Text(source))
			if text == symbolName && !isPythonDefinitionIdentifier(node) {
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

// skeletonReplacement describes a byte range within a source file (the body
// of a function/method, minus a leading docstring statement if present) that
// GenerateSkeleton replaces with "\n" + indent spaces + "...". indent is the
// column (0-indexed) the enclosing "block" node starts on, matching Python's
// `child.start_point[1]`.
type skeletonReplacement struct {
	start  int
	end    int
	indent int
}

// collectSkeletonReplacements walks node (and its descendants), recording a
// skeletonReplacement for every "function_definition" node's body. It does
// not recurse into a function_definition's own body (mirroring Python's
// `return` immediately after handling the "function_definition" case in
// `collect_blocks`), so nested/inner function definitions are left alone:
// their body is included verbatim in the outer function's replaced range.
// "class_definition" nodes are not treated as replacement targets themselves;
// their children are simply walked to find nested function/method
// definitions, exactly as Python's `generate_skeleton.collect_blocks` does.
func collectSkeletonReplacements(node *tree_sitter.Node, out *[]skeletonReplacement) {
	switch node.Kind() {
	case "function_definition":
		blockNode := findChildByKind(node, "block")
		if blockNode != nil {
			start := blockNode.StartByte()
			if blockNode.ChildCount() > 0 {
				firstChild := blockNode.Child(0)
				if firstChild.Kind() == "expression_statement" && firstChild.ChildCount() > 0 {
					stmtChild := firstChild.Child(0)
					if stmtChild.Kind() == "string" {
						start = stmtChild.EndByte()
					}
				}
			}
			*out = append(*out, skeletonReplacement{
				start:  int(start),
				end:    int(blockNode.EndByte()),
				indent: int(blockNode.StartPosition().Column),
			})
		}
		return
	case "class_definition":
		for i := uint(0); i < node.ChildCount(); i++ {
			collectSkeletonReplacements(node.Child(i), out)
		}
		return
	}

	for i := uint(0); i < node.ChildCount(); i++ {
		collectSkeletonReplacements(node.Child(i), out)
	}
}

// GenerateSkeleton reconstructs filePath's source with every function/method
// body replaced by "..." (preceded by a preserved docstring, if the body
// starts with one), leaving signatures, decorators, and docstrings
// untouched. It corresponds to Python's `PythonParser.generate_skeleton`.
func (p *PythonParser) GenerateSkeleton(filePath string) (string, error) {
	source, err := readFile(filePath)
	if err != nil {
		return "", err
	}

	tree, err := p.parseSource(source)
	if err != nil {
		return "", err
	}
	defer tree.Close()

	var replacements []skeletonReplacement
	root := tree.RootNode()
	for i := uint(0); i < root.ChildCount(); i++ {
		collectSkeletonReplacements(root.Child(i), &replacements)
	}

	// Sorted by start byte descending so each replacement is applied before
	// any byte-offset shift it causes could affect the (not yet processed)
	// replacements that precede it in the file, mirroring Python's
	// `blocks_to_replace.sort(key=lambda x: x[0], reverse=True)`.
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })

	result := append([]byte(nil), source...)
	for _, r := range replacements {
		replacement := []byte("\n" + strings.Repeat(" ", r.indent) + "...")
		result = append(result[:r.start], append(replacement, result[r.end:]...)...)
	}

	return string(result), nil
}
