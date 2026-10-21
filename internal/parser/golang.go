// Package parser: golang.go implements the Parser interface for Go source
// files using the tree-sitter-go grammar. It corresponds to
// ast_digger/parsers/go.py in the Python implementation.
//
// The concrete type is named GolangParser (rather than "GoParser") and this
// file is named golang.go (rather than "go.go") to avoid clashing, in name
// only, with Go's own standard-library go/parser package and the "go" build
// tool/keyword; there is no functional reason for the naming beyond reducing
// confusion when this package is imported alongside go/parser or similar.
package parser

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"

	"github.com/snaga/ast-digger/internal/apperr"
)

// GolangParser implements the Parser interface for Go (.go) source files.
//
// It corresponds to the Python `GoParser` class defined in
// ast_digger/parsers/go.py.
type GolangParser struct {
	lang *tree_sitter.Language
}

// Compile-time assertion that GolangParser satisfies the Parser interface.
var _ Parser = (*GolangParser)(nil)

// NewGolangParser constructs a GolangParser with the tree-sitter-go grammar
// loaded.
func NewGolangParser() *GolangParser {
	return &GolangParser{lang: tree_sitter.NewLanguage(tree_sitter_go.Language())}
}

// parseSource parses source using a fresh tree-sitter parser configured with
// the Go grammar. The caller owns the returned tree and must call
// tree.Close() once done with it.
func (p *GolangParser) parseSource(source []byte) (*tree_sitter.Tree, error) {
	ts := tree_sitter.NewParser()
	defer ts.Close()

	if err := ts.SetLanguage(p.lang); err != nil {
		return nil, fmt.Errorf("failed to set tree-sitter-go language: %w", err)
	}

	tree := ts.Parse(source, nil)
	if tree == nil {
		return nil, fmt.Errorf("tree-sitter-go failed to parse source")
	}
	return tree, nil
}

// findGoDocComment walks backwards from node through its immediately
// preceding siblings (in its parent's child list): if it finds a "comment"
// sibling before encountering any other named sibling, that comment's
// (trimmed) text is returned; unnamed siblings (e.g. punctuation) in between
// are skipped over. It mirrors Python's `_find_doc_comment`.
func findGoDocComment(node *tree_sitter.Node, source []byte) *string {
	if node.Parent() == nil {
		return nil
	}

	for sib := node.PrevSibling(); sib != nil; sib = sib.PrevSibling() {
		if sib.Kind() == "comment" {
			text := strings.TrimSpace(sib.Utf8Text(source))
			return &text
		}
		if sib.IsNamed() {
			break
		}
	}
	return nil
}

// getReceiverType returns the receiver type name of a method_declaration
// node (e.g. "MyStruct" for both "func (m MyStruct) ..." and
// "func (m *MyStruct) ..."), or nil if funcNode has no receiver. It mirrors
// Python's `_get_receiver_type`.
func getReceiverType(funcNode *tree_sitter.Node, source []byte) *string {
	paramList := funcNode.ChildByFieldName("receiver")
	if paramList == nil {
		return nil
	}

	for i := uint(0); i < paramList.ChildCount(); i++ {
		child := paramList.Child(i)
		if child.Kind() != "parameter_declaration" {
			continue
		}
		typeNode := child.ChildByFieldName("type")
		if typeNode == nil {
			continue
		}
		if typeNode.Kind() == "pointer_type" {
			inner := typeNode.NamedChild(0)
			if inner != nil {
				text := strings.TrimSpace(inner.Utf8Text(source))
				return &text
			}
		}
		text := strings.TrimSpace(typeNode.Utf8Text(source))
		return &text
	}
	return nil
}

// getFuncSignature builds funcNode's signature text: everything from
// funcNode's start up to the start of its "body" field (or funcNode's own
// end if it has no body, e.g. an interface method signature or a body-less
// forward declaration). It mirrors Python's `_get_func_signature`.
func getFuncSignature(funcNode *tree_sitter.Node, source []byte) string {
	body := funcNode.ChildByFieldName("body")
	endByte := funcNode.EndByte()
	if body != nil {
		endByte = body.StartByte()
	}
	return strings.TrimSpace(string(source[funcNode.StartByte():endByte]))
}

// goTypeSpecKind classifies a type_spec/type_alias node's "type" field,
// returning "struct" for struct_type, "interface" for interface_type, or
// "type" for anything else (aliases, named base types, slices, maps, ...).
// It mirrors the inline kind ternary duplicated in Python's `parse_file`,
// `extract_symbol`, and `_find_symbol_line`.
func goTypeSpecKind(typeNode *tree_sitter.Node) string {
	if typeNode == nil {
		return "type"
	}
	switch typeNode.Kind() {
	case "struct_type":
		return "struct"
	case "interface_type":
		return "interface"
	default:
		return "type"
	}
}

// ParseFile parses filePath and returns the list of symbols (functions,
// methods, structs, interfaces, types) found in it. It corresponds to
// Python's `GoParser.parse_file`.
func (p *GolangParser) ParseFile(filePath string) ([]SymbolData, error) {
	source, err := readFile(filePath)
	if err != nil {
		return nil, err
	}
	return p.ParseBytes(source, filePath)
}

// ParseBytes parses source and returns the list of symbols found in it.
func (p *GolangParser) ParseBytes(source []byte, filePath string) ([]SymbolData, error) {
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

	var traverse func(node *tree_sitter.Node)
	traverse = func(node *tree_sitter.Node) {
		switch node.Kind() {
		case "function_declaration":
			nameNode := node.ChildByFieldName("name")
			if nameNode != nil {
				name := nodeText(nameNode, source)
				sig := getFuncSignature(node, source)
				symbols = append(symbols, SymbolData{
					Name:      name,
					Kind:      "function",
					StartLine: int(node.StartPosition().Row) + 1,
					EndLine:   int(node.EndPosition().Row) + 1,
					Signature: &sig,
					Docstring: findGoDocComment(node, source),
					FilePath:  &relPath,
				})
			}
			return

		case "method_declaration":
			nameNode := node.ChildByFieldName("name")
			receiverType := getReceiverType(node, source)
			if nameNode != nil {
				name := nodeText(nameNode, source)
				sig := getFuncSignature(node, source)
				symbols = append(symbols, SymbolData{
					Name:      name,
					Kind:      "method",
					StartLine: int(node.StartPosition().Row) + 1,
					EndLine:   int(node.EndPosition().Row) + 1,
					Parent:    receiverType,
					Signature: &sig,
					Docstring: findGoDocComment(node, source),
					FilePath:  &relPath,
				})
			}
			return

		case "type_declaration":
			for i := uint(0); i < node.ChildCount(); i++ {
				spec := node.Child(i)
				if spec.Kind() != "type_spec" && spec.Kind() != "type_alias" {
					continue
				}
				nameNode := spec.ChildByFieldName("name")
				typeNode := spec.ChildByFieldName("type")
				if nameNode == nil || typeNode == nil {
					continue
				}
				symbols = append(symbols, SymbolData{
					Name:      nodeText(nameNode, source),
					Kind:      goTypeSpecKind(typeNode),
					StartLine: int(node.StartPosition().Row) + 1,
					EndLine:   int(node.EndPosition().Row) + 1,
					// Doc comment lookup is anchored on the enclosing
					// type_declaration node (not the inner type_spec/
					// type_alias), matching Python's `_find_doc_comment(node,
					// ...)` call in `parse_file`'s type_declaration branch.
					Docstring: findGoDocComment(node, source),
					FilePath:  &relPath,
				})
			}
			return
		}

		for i := uint(0); i < node.ChildCount(); i++ {
			traverse(node.Child(i))
		}
	}

	traverse(tree.RootNode())
	return symbols, nil
}

// ExtractSymbol resolves symbolPath (e.g. "MyStruct.MyMethod") within
// filePath and returns the corresponding SymbolData, including its source
// code. It corresponds to Python's `GoParser.extract_symbol`.
func (p *GolangParser) ExtractSymbol(filePath, symbolPath string) (SymbolData, error) {
	source, err := readFile(filePath)
	if err != nil {
		return SymbolData{}, err
	}

	tree, err := p.parseSource(source)
	if err != nil {
		return SymbolData{}, err
	}
	defer tree.Close()

	// symbolPath is split on "." at most once (unlike Python/TypeScript,
	// which split on every "."), matching Python's `symbol_path.split(".",
	// 1)`: a Go symbol path is either a bare top-level name ("Add") or
	// exactly "ReceiverType.MethodName" ("Greeter.Greet"), never a deeper
	// dotted chain.
	parts := strings.SplitN(symbolPath, ".", 2)
	topName := parts[0]
	hasMember := len(parts) > 1
	var memberName string
	if hasMember {
		memberName = parts[1]
	}

	var foundNode *tree_sitter.Node
	foundKind := "function"
	var foundParent *string

	var search func(node *tree_sitter.Node)
	search = func(node *tree_sitter.Node) {
		if foundNode != nil {
			return
		}

		if node.Kind() == "function_declaration" && !hasMember {
			nameNode := node.ChildByFieldName("name")
			if nameNode != nil && nodeText(nameNode, source) == topName {
				foundNode = node
				foundKind = "function"
				return
			}
		}

		if node.Kind() == "method_declaration" {
			nameNode := node.ChildByFieldName("name")
			receiverType := getReceiverType(node, source)
			if nameNode != nil {
				methodName := nodeText(nameNode, source)
				if hasMember && receiverType != nil && *receiverType == topName && methodName == memberName {
					foundNode = node
					foundKind = "method"
					foundParent = receiverType
					return
				}
				if !hasMember && methodName == topName {
					foundNode = node
					foundKind = "method"
					foundParent = receiverType
					return
				}
			}
		}

		if node.Kind() == "type_declaration" {
			for i := uint(0); i < node.ChildCount(); i++ {
				spec := node.Child(i)
				if spec.Kind() != "type_spec" && spec.Kind() != "type_alias" {
					continue
				}
				nameNode := spec.ChildByFieldName("name")
				if nameNode != nil && !hasMember && nodeText(nameNode, source) == topName {
					foundNode = node
					foundKind = goTypeSpecKind(spec.ChildByFieldName("type"))
					return
				}
			}
		}

		for i := uint(0); i < node.ChildCount(); i++ {
			if foundNode == nil {
				search(node.Child(i))
			}
		}
	}

	search(tree.RootNode())

	if foundNode == nil {
		return SymbolData{}, &apperr.SymbolNotFoundError{
			FilePath:    filePath,
			SymbolPath:  symbolPath,
			Suggestions: suggestSymbols(p.ParseFile, filePath, symbolPath),
		}
	}

	code := foundNode.Utf8Text(source)
	name := topName
	if hasMember {
		name = memberName
	}

	var signature *string
	if foundNode.Kind() == "function_declaration" || foundNode.Kind() == "method_declaration" {
		sig := getFuncSignature(foundNode, source)
		signature = &sig
	}

	relPath := relFilePath(filePath)

	return SymbolData{
		Name:      name,
		Kind:      foundKind,
		StartLine: int(foundNode.StartPosition().Row) + 1,
		EndLine:   int(foundNode.EndPosition().Row) + 1,
		Parent:    foundParent,
		Signature: signature,
		Docstring: findGoDocComment(foundNode, source),
		FilePath:  &relPath,
		Code:      &code,
	}, nil
}

// readGoModuleName reads "<projectRoot>/go.mod" and returns the module path
// declared by its "module <name>" line, or nil if go.mod does not exist,
// cannot be read, or has no such line. It mirrors the go.mod-reading prelude
// in Python's `resolve_imports`, which swallows any read error rather than
// propagating it.
func readGoModuleName(projectRoot string) *string {
	data, err := os.ReadFile(filepath.Join(projectRoot, "go.mod"))
	if err != nil {
		return nil
	}
	for _, line := range splitLines(string(data)) {
		if !strings.HasPrefix(line, "module ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil
		}
		name := fields[1]
		return &name
	}
	return nil
}

// collectGoImportPaths walks node (and its descendants) depth-first,
// appending the (quote-stripped) import path string of every "import_spec"
// node found to *out. It mirrors Python's nested `collect_imports` closure
// in `resolve_imports`.
func collectGoImportPaths(node *tree_sitter.Node, source []byte, out *[]string) {
	if node.Kind() == "import_spec" {
		pathNode := node.ChildByFieldName("path")
		if pathNode != nil {
			*out = append(*out, strings.Trim(nodeText(pathNode, source), `"`))
		}
		return
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		collectGoImportPaths(node.Child(i), source, out)
	}
}

// resolveGoPackageDir resolves pkgPath (an import path string, e.g.
// "example.com/mod/sub" or "./sub") to a local directory on disk, or returns
// "" if it cannot be resolved as a local package. It mirrors Python's nested
// `resolve_package_dir` closure in `resolve_imports`:
//
//   - If moduleName is known and pkgPath starts with it, the remainder (with
//     any leading "/" stripped) is joined onto projectRoot.
//   - Otherwise, if pkgPath starts with ".", it is resolved relative to
//     fileDir (the importing file's directory).
//   - Otherwise (an external/third-party package path), "" is returned.
//
// In every case, the candidate is only returned if it names an existing
// directory.
func resolveGoPackageDir(pkgPath string, moduleName *string, projectRoot, fileDir string) string {
	if moduleName != nil && strings.HasPrefix(pkgPath, *moduleName) {
		rel := strings.TrimPrefix(strings.TrimPrefix(pkgPath, *moduleName), "/")
		candidate := filepath.Join(projectRoot, filepath.FromSlash(rel))
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}

	if strings.HasPrefix(pkgPath, ".") {
		candidate := filepath.Join(fileDir, filepath.FromSlash(pkgPath))
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}

	return ""
}

// findGoSymbolLine searches root's direct children (not further descendants)
// for a top-level function_declaration, method_declaration, or
// type_declaration (via its type_spec/type_alias children) named
// symbolName, returning its 1-indexed start line. It mirrors Python's
// `_find_symbol_line`.
func findGoSymbolLine(root *tree_sitter.Node, source []byte, symbolName string) (int, bool) {
	for i := uint(0); i < root.ChildCount(); i++ {
		node := root.Child(i)
		switch node.Kind() {
		case "function_declaration", "method_declaration":
			nameNode := node.ChildByFieldName("name")
			if nameNode != nil && nodeText(nameNode, source) == symbolName {
				return int(node.StartPosition().Row) + 1, true
			}
		case "type_declaration":
			for j := uint(0); j < node.ChildCount(); j++ {
				spec := node.Child(j)
				if spec.Kind() != "type_spec" && spec.Kind() != "type_alias" {
					continue
				}
				nameNode := spec.ChildByFieldName("name")
				if nameNode != nil && nodeText(nameNode, source) == symbolName {
					return int(node.StartPosition().Row) + 1, true
				}
			}
		}
	}
	return 0, false
}

// ResolveImports resolves the definition location of symbolName as imported
// by filePath, returning the defining file's path and the 1-indexed line its
// definition starts on. It corresponds to Python's
// `GoParser.resolve_imports`.
//
// Unlike PythonParser/TypeScriptParser's ResolveImports (which return a path
// relative to the working directory via relFilePath), the returned path here
// is whatever path resolveGoPackageDir/filepath.Join produced when locating
// the defining file (an absolute path in practice, since both projectRoot
// and fileDir are resolved to absolute paths below) -- this intentionally
// mirrors Python's `return str(go_file), line` in ast_digger/parsers/go.py,
// which likewise does not normalize the result to a project-root-relative
// path.
func (p *GolangParser) ResolveImports(filePath, symbolName string) (string, int, error) {
	source, err := readFile(filePath)
	if err != nil {
		return "", 0, err
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", 0, &apperr.FileAccessError{Path: filePath, Err: err}
	}
	fileDir := filepath.Dir(absPath)

	cwd, err := os.Getwd()
	if err != nil {
		return "", 0, &apperr.FileAccessError{Path: filePath, Err: err}
	}
	projectRoot, err := filepath.Abs(cwd)
	if err != nil {
		return "", 0, &apperr.FileAccessError{Path: filePath, Err: err}
	}

	moduleName := readGoModuleName(projectRoot)

	tree, err := p.parseSource(source)
	if err != nil {
		return "", 0, err
	}
	defer tree.Close()

	var importPaths []string
	collectGoImportPaths(tree.RootNode(), source, &importPaths)

	// anyPackageResolved tracks whether at least one imported package's
	// local directory was actually located and read (as opposed to no
	// import path resolving to a real local package directory at all),
	// distinguishing the two failure modes below (task 14, go_reviewer's
	// task 4 follow-up): "module/package file not found" (nothing here
	// resolved) stays a FileAccessError (exit code 3), while "symbol not
	// found within an otherwise-resolved package" becomes a
	// SymbolNotFoundError (exit code 4). Python's `GoParser.resolve_imports`
	// does not draw this distinction (it raises FileAccessError
	// unconditionally in both cases); this is an intentional semantic
	// improvement, not a parity bug -- see PythonParser.ResolveImports's
	// identical change for the same rationale.
	anyPackageResolved := false

	for _, pkgPath := range importPaths {
		pkgDir := resolveGoPackageDir(pkgPath, moduleName, projectRoot, fileDir)
		if pkgDir == "" {
			continue
		}

		entries, err := os.ReadDir(pkgDir)
		if err != nil {
			continue
		}
		anyPackageResolved = true
		var goFileNames []string
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if strings.HasSuffix(entry.Name(), ".go") {
				goFileNames = append(goFileNames, entry.Name())
			}
		}
		sort.Strings(goFileNames)

		for _, name := range goFileNames {
			goFilePath := filepath.Join(pkgDir, name)
			fileSource, err := os.ReadFile(goFilePath)
			if err != nil {
				continue
			}
			fileTree, err := p.parseSource(fileSource)
			if err != nil {
				continue
			}
			line, found := findGoSymbolLine(fileTree.RootNode(), fileSource, symbolName)
			fileTree.Close()
			if found {
				return goFilePath, line, nil
			}
		}
	}

	if anyPackageResolved {
		return "", 0, &apperr.SymbolNotFoundError{
			FilePath:   filePath,
			SymbolPath: symbolName,
		}
	}

	return "", 0, &apperr.FileAccessError{
		Path: filePath,
		Err:  fmt.Errorf("could not resolve symbol %s from imports in %s", symbolName, filePath),
	}
}

// FindReferencesInFile searches filePath for occurrences where symbolName is
// referenced or invoked, excluding matches inside comments and string
// literals. It corresponds to Python's `GoParser.find_references_in_file`.
//
// Unlike PythonParser.FindReferencesInFile/TypeScriptParser.
// FindReferencesInFile (which additionally exclude definition-site
// identifiers via an is_definition check), this method does not exclude
// definition sites: it mirrors Python's `GoParser.find_references_in_file`
// verbatim, which performs only the comment/string-literal exclusion below.
// Do not "fix" this asymmetry without also updating
// ast_digger/parsers/go.py.
func (p *GolangParser) FindReferencesInFile(filePath, symbolName string) ([]ReferenceData, error) {
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

	type byteRange struct {
		start, end uint
	}
	var excluded []byteRange

	var collectExcluded func(node *tree_sitter.Node)
	collectExcluded = func(node *tree_sitter.Node) {
		switch node.Kind() {
		case "comment", "interpreted_string_literal", "raw_string_literal":
			excluded = append(excluded, byteRange{start: node.StartByte(), end: node.EndByte()})
			return
		}
		for i := uint(0); i < node.ChildCount(); i++ {
			collectExcluded(node.Child(i))
		}
	}
	collectExcluded(tree.RootNode())

	isExcluded := func(offset uint) bool {
		for _, r := range excluded {
			if r.start <= offset && offset < r.end {
				return true
			}
		}
		return false
	}

	lines := splitLines(string(source))
	relPath := relFilePath(filePath)

	references := []ReferenceData{}

	var traverse func(node *tree_sitter.Node)
	traverse = func(node *tree_sitter.Node) {
		if node.Kind() == "identifier" {
			text := nodeText(node, source)
			if text == symbolName && !isExcluded(node.StartByte()) {
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

// goSkeletonReplacement describes a byte range within a source file (a
// function/method/func-literal body) that GenerateSkeleton replaces with
// "{ /* ... */ }".
type goSkeletonReplacement struct {
	start, end int
}

// collectGoSkeletonReplacements walks node (and its descendants), recording
// a goSkeletonReplacement for every function_declaration/method_declaration/
// func_literal node's body. It does not recurse into a matched node's own
// children (mirroring Python's `return` immediately after handling the
// matching case in `collect_blocks`), so nested function literals are left
// alone: their body is included verbatim in the outer function's replaced
// range.
func collectGoSkeletonReplacements(node *tree_sitter.Node, out *[]goSkeletonReplacement) {
	switch node.Kind() {
	case "function_declaration", "method_declaration", "func_literal":
		body := node.ChildByFieldName("body")
		if body != nil {
			*out = append(*out, goSkeletonReplacement{
				start: int(body.StartByte()),
				end:   int(body.EndByte()),
			})
		}
		return
	}

	for i := uint(0); i < node.ChildCount(); i++ {
		collectGoSkeletonReplacements(node.Child(i), out)
	}
}

// GenerateSkeleton reconstructs filePath's source with every
// function/method/func-literal body replaced by "{ /* ... */ }", leaving
// signatures and doc comments untouched. It corresponds to Python's
// `GoParser.generate_skeleton`.
func (p *GolangParser) GenerateSkeleton(filePath string) (string, error) {
	source, err := readFile(filePath)
	if err != nil {
		return "", err
	}

	tree, err := p.parseSource(source)
	if err != nil {
		return "", err
	}
	defer tree.Close()

	var replacements []goSkeletonReplacement
	collectGoSkeletonReplacements(tree.RootNode(), &replacements)

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
