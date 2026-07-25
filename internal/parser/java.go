// Package parser: java.go implements the Parser interface for Java source
// files using the tree-sitter-java grammar. It follows the same structure
// and conventions as golang.go (GolangParser), adapted for Java's class-
// based symbol model.
package parser

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"

	"github.com/snaga/ast-digger/internal/apperr"
)

// JavaParser implements the Parser interface for Java (.java) source files.
type JavaParser struct {
	lang *tree_sitter.Language
}

// Compile-time assertion that JavaParser satisfies the Parser interface.
var _ Parser = (*JavaParser)(nil)

// NewJavaParser constructs a JavaParser with the tree-sitter-java grammar
// loaded.
func NewJavaParser() *JavaParser {
	return &JavaParser{lang: tree_sitter.NewLanguage(tree_sitter_java.Language())}
}

// parseSource parses source using a fresh tree-sitter parser configured with
// the Java grammar. The caller owns the returned tree and must call
// tree.Close() once done with it.
func (p *JavaParser) parseSource(source []byte) (*tree_sitter.Tree, error) {
	ts := tree_sitter.NewParser()
	defer ts.Close()

	if err := ts.SetLanguage(p.lang); err != nil {
		return nil, fmt.Errorf("failed to set tree-sitter-java language: %w", err)
	}

	tree := ts.Parse(source, nil)
	if tree == nil {
		return nil, fmt.Errorf("tree-sitter-java failed to parse source")
	}
	return tree, nil
}

// javaTypeKind classifies a class_declaration/interface_declaration/
// enum_declaration node's kind into the SymbolData.Kind string used for it
// ("class", "interface", "enum"). It returns "" for any other node kind.
func javaTypeKind(nodeKind string) string {
	switch nodeKind {
	case "class_declaration":
		return "class"
	case "interface_declaration":
		return "interface"
	case "enum_declaration":
		return "enum"
	}
	return ""
}

// findJavaDocComment returns node's Javadoc comment text, or nil if node has
// none. It walks backwards from node through its immediately preceding
// siblings (mirroring findGoDocComment in golang.go): if the immediately
// preceding named sibling is a "block_comment" whose (trimmed) text starts
// with "/**", that text is returned. A plain "/* ... */" block comment (one
// that does not start with "/**") or a "// ..." line_comment immediately
// preceding node is not treated as a doc comment (and, per this function's
// contract of only inspecting the *immediately* preceding named sibling,
// stops the search rather than looking further back).
func findJavaDocComment(node *tree_sitter.Node, source []byte) *string {
	if node.Parent() == nil {
		return nil
	}

	for sib := node.PrevSibling(); sib != nil; sib = sib.PrevSibling() {
		if sib.Kind() == "block_comment" {
			text := strings.TrimSpace(sib.Utf8Text(source))
			if strings.HasPrefix(text, "/**") {
				return &text
			}
			return nil
		}
		if sib.IsNamed() {
			break
		}
	}
	return nil
}

// getJavaSignature builds node's signature text: everything from node's
// start up to the start of its "body" field (or node's own end if it has no
// body, e.g. an interface/abstract method declaration with no block). It
// mirrors getFuncSignature in golang.go, generalized to the several Java
// declaration kinds (class_declaration, interface_declaration,
// enum_declaration, method_declaration, constructor_declaration) that all
// expose their body via a field named "body".
func getJavaSignature(node *tree_sitter.Node, source []byte) string {
	body := node.ChildByFieldName("body")
	endByte := node.EndByte()
	if body != nil {
		endByte = body.StartByte()
	}
	return strings.TrimSpace(string(source[node.StartByte():endByte]))
}

// ParseFile parses filePath and returns the list of symbols (classes,
// interfaces, enums, methods, constructors) found in it.
//
// Field declarations (field_declaration) are intentionally not extracted as
// symbols, matching the same "properties are not individually extracted"
// design decision already applied by PythonParser/TypeScriptParser/
// GolangParser. Constructors are reported with Kind "method" (there is no
// dedicated "constructor" kind), per design.md's F-1 policy.
func (p *JavaParser) ParseFile(filePath string) ([]SymbolData, error) {
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
	// as "[]" instead of "null".
	symbols := []SymbolData{}

	var traverse func(node *tree_sitter.Node, parent *string)
	traverse = func(node *tree_sitter.Node, parent *string) {
		switch node.Kind() {
		case "class_declaration", "interface_declaration", "enum_declaration":
			nameNode := node.ChildByFieldName("name")
			if nameNode == nil {
				break
			}
			name := nodeText(nameNode, source)
			sig := getJavaSignature(node, source)
			symbols = append(symbols, SymbolData{
				Name:      name,
				Kind:      javaTypeKind(node.Kind()),
				Parent:    parent,
				StartLine: int(node.StartPosition().Row) + 1,
				EndLine:   int(node.EndPosition().Row) + 1,
				Signature: &sig,
				Docstring: findJavaDocComment(node, source),
				FilePath:  &relPath,
			})

			childParent := name
			for i := uint(0); i < node.ChildCount(); i++ {
				traverse(node.Child(i), &childParent)
			}
			return

		case "method_declaration", "constructor_declaration":
			nameNode := node.ChildByFieldName("name")
			if nameNode != nil {
				name := nodeText(nameNode, source)
				sig := getJavaSignature(node, source)
				symbols = append(symbols, SymbolData{
					Name:      name,
					Kind:      "method",
					Parent:    parent,
					StartLine: int(node.StartPosition().Row) + 1,
					EndLine:   int(node.EndPosition().Row) + 1,
					Signature: &sig,
					Docstring: findJavaDocComment(node, source),
					FilePath:  &relPath,
				})
			}
			// Method/constructor bodies are not descended into: local/
			// anonymous classes declared within a method body are not
			// extracted as symbols, mirroring GolangParser's treatment of
			// func_literal bodies in ParseFile.
			return

		case "field_declaration":
			// Field declarations are never extracted as symbols; do not
			// descend into them.
			return
		}

		for i := uint(0); i < node.ChildCount(); i++ {
			traverse(node.Child(i), parent)
		}
	}

	traverse(tree.RootNode(), nil)
	return symbols, nil
}

// findJavaMember searches body (a class_body, interface_body, or enum_body
// node) for the first method_declaration or constructor_declaration named
// memberName, in declaration order, returning nil if none matches. When two
// or more overloads share memberName, the first one encountered is returned
// (design decision: ExtractSymbol does not disambiguate overloads by
// parameter list). enum_body's members are nested one level deeper, inside
// an enum_body_declarations wrapper node; that wrapper is transparently
// searched, but nested class/interface/enum/method bodies are not (so only
// body's own direct members are considered, not members of any nested
// type declared within it).
func findJavaMember(body *tree_sitter.Node, memberName string, source []byte) *tree_sitter.Node {
	for i := uint(0); i < body.ChildCount(); i++ {
		child := body.Child(i)
		switch child.Kind() {
		case "method_declaration", "constructor_declaration":
			nameNode := child.ChildByFieldName("name")
			if nameNode != nil && nodeText(nameNode, source) == memberName {
				return child
			}
		case "enum_body_declarations":
			if found := findJavaMember(child, memberName, source); found != nil {
				return found
			}
		}
	}
	return nil
}

// ExtractSymbol resolves symbolPath within filePath and returns the
// corresponding SymbolData, including its source code. symbolPath is either
// a bare top-level type name ("SampleClass", referring to a top-level
// class/interface/enum declaration itself) or a dotted
// "ClassName.methodName" path (referring to a method or constructor declared
// directly inside a class/interface/enum named ClassName, searched
// recursively through the whole file so ClassName may itself be a nested
// type). When methodName is overloaded, the first matching declaration (in
// source order) is returned.
func (p *JavaParser) ExtractSymbol(filePath, symbolPath string) (SymbolData, error) {
	source, err := readFile(filePath)
	if err != nil {
		return SymbolData{}, err
	}

	tree, err := p.parseSource(source)
	if err != nil {
		return SymbolData{}, err
	}
	defer tree.Close()

	parts := strings.SplitN(symbolPath, ".", 2)
	topName := parts[0]
	hasMember := len(parts) > 1
	var memberName string
	if hasMember {
		memberName = parts[1]
	}

	var foundNode *tree_sitter.Node
	foundKind := ""
	var foundParent *string

	var search func(node *tree_sitter.Node)
	search = func(node *tree_sitter.Node) {
		if foundNode != nil {
			return
		}

		switch node.Kind() {
		case "class_declaration", "interface_declaration", "enum_declaration":
			nameNode := node.ChildByFieldName("name")
			if nameNode != nil && nodeText(nameNode, source) == topName {
				if !hasMember {
					foundNode = node
					foundKind = javaTypeKind(node.Kind())
					return
				}
				body := node.ChildByFieldName("body")
				if body != nil {
					if member := findJavaMember(body, memberName, source); member != nil {
						foundNode = member
						foundKind = "method"
						parentName := topName
						foundParent = &parentName
					}
				}
				return
			}
		}

		for i := uint(0); i < node.ChildCount(); i++ {
			search(node.Child(i))
			if foundNode != nil {
				return
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

	sig := getJavaSignature(foundNode, source)
	relPath := relFilePath(filePath)

	return SymbolData{
		Name:      name,
		Kind:      foundKind,
		Parent:    foundParent,
		StartLine: int(foundNode.StartPosition().Row) + 1,
		EndLine:   int(foundNode.EndPosition().Row) + 1,
		Signature: &sig,
		Docstring: findJavaDocComment(foundNode, source),
		FilePath:  &relPath,
		Code:      &code,
	}, nil
}

// javaImportSpec describes a single import_declaration parsed from a Java
// source file.
type javaImportSpec struct {
	// qualifiedName is the dotted name preceding any wildcard: for a plain
	// import ("import com.example.Foo;") this is the fully-qualified class
	// name ("com.example.Foo"); for a wildcard import
	// ("import com.example.*;") this is just the package name
	// ("com.example").
	qualifiedName string
	// wildcard is true for "import <package>.*;" style imports.
	wildcard bool
}

// collectJavaImports walks root's direct children, returning a
// javaImportSpec for every top-level import_declaration found. It mirrors
// collectGoImportPaths in golang.go, but Java import declarations are always
// direct children of the compilation unit (program) root, so only
// root.ChildCount() top-level children are inspected (not a full recursive
// descendant walk).
func collectJavaImports(root *tree_sitter.Node, source []byte) []javaImportSpec {
	var specs []javaImportSpec
	for i := uint(0); i < root.ChildCount(); i++ {
		node := root.Child(i)
		if node.Kind() != "import_declaration" {
			continue
		}

		var nameNode *tree_sitter.Node
		wildcard := false
		for j := uint(0); j < node.ChildCount(); j++ {
			child := node.Child(j)
			switch child.Kind() {
			case "scoped_identifier", "identifier":
				nameNode = child
			case "asterisk":
				wildcard = true
			}
		}
		if nameNode == nil {
			continue
		}

		specs = append(specs, javaImportSpec{
			qualifiedName: nodeText(nameNode, source),
			wildcard:      wildcard,
		})
	}
	return specs
}

// javaSourceRoot resolves the Java source root directory to search for
// imported packages under, given projectRoot (the process's current working
// directory, resolved to an absolute path). If "<projectRoot>/src/main/java"
// exists and is a directory (the conventional Maven/Gradle source root),
// that is preferred; otherwise projectRoot itself is used.
func javaSourceRoot(projectRoot string) string {
	candidate := filepath.Join(projectRoot, "src", "main", "java")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	return projectRoot
}

// findJavaTopLevelSymbolLine searches root's direct children for a
// top-level class_declaration, interface_declaration, or enum_declaration
// named symbolName, returning its 1-indexed start line. It mirrors
// findGoSymbolLine in golang.go. Unlike Go (where a package can export
// functions/methods/types at its top level), a Java import always names a
// type (class/interface/enum), so only those three node kinds are
// considered.
func findJavaTopLevelSymbolLine(root *tree_sitter.Node, source []byte, symbolName string) (int, bool) {
	for i := uint(0); i < root.ChildCount(); i++ {
		node := root.Child(i)
		switch node.Kind() {
		case "class_declaration", "interface_declaration", "enum_declaration":
			nameNode := node.ChildByFieldName("name")
			if nameNode != nil && nodeText(nameNode, source) == symbolName {
				return int(node.StartPosition().Row) + 1, true
			}
		}
	}
	return 0, false
}

// ResolveImports resolves the definition location of symbolName as imported
// by filePath, returning the defining file's path and the 1-indexed line its
// top-level class/interface/enum declaration starts on.
//
// The search root ("project root") is the process's current working
// directory, matching GolangParser.ResolveImports/PythonParser.
// ResolveImports (Python's `project_root = pathlib.Path.cwd().resolve()`).
// Within it, javaSourceRoot prefers a "src/main/java" directory if present,
// falling back to the project root itself.
//
// Both plain imports ("import com.example.Foo;", resolved only when its
// last dotted component equals symbolName) and wildcard imports
// ("import com.example.*;", resolved by scanning every ".java" file directly
// in the resolved package directory, in sorted filename order, for a
// top-level declaration named symbolName) are supported.
//
// As in GolangParser.ResolveImports, two distinct failure modes are
// distinguished: if no import's package directory could be located on disk
// at all, an *apperr.FileAccessError is returned; if at least one import's
// package directory was found but none of it (or the specific imported
// file) declares symbolName, an *apperr.SymbolNotFoundError is returned
// instead.
func (p *JavaParser) ResolveImports(filePath, symbolName string) (string, int, error) {
	source, err := readFile(filePath)
	if err != nil {
		return "", 0, err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", 0, &apperr.FileAccessError{Path: filePath, Err: err}
	}
	projectRoot, err := filepath.Abs(cwd)
	if err != nil {
		return "", 0, &apperr.FileAccessError{Path: filePath, Err: err}
	}
	sourceRoot := javaSourceRoot(projectRoot)

	tree, err := p.parseSource(source)
	if err != nil {
		return "", 0, err
	}
	defer tree.Close()

	imports := collectJavaImports(tree.RootNode(), source)

	// anyPackageResolved tracks whether at least one imported package's
	// directory (or, for a plain import, its specific .java file) was
	// actually located on disk and read -- see the FileAccessError vs.
	// SymbolNotFoundError distinction documented on the function above,
	// which mirrors GolangParser.ResolveImports' identical anyPackageResolved
	// flag.
	anyPackageResolved := false

	for _, imp := range imports {
		if imp.wildcard {
			pkgDir := filepath.Join(sourceRoot, filepath.FromSlash(strings.ReplaceAll(imp.qualifiedName, ".", "/")))
			entries, err := os.ReadDir(pkgDir)
			if err != nil {
				continue
			}
			anyPackageResolved = true

			var javaFileNames []string
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				if strings.HasSuffix(entry.Name(), ".java") {
					javaFileNames = append(javaFileNames, entry.Name())
				}
			}
			sort.Strings(javaFileNames)

			for _, name := range javaFileNames {
				javaFilePath := filepath.Join(pkgDir, name)
				fileSource, err := os.ReadFile(javaFilePath)
				if err != nil {
					continue
				}
				fileTree, err := p.parseSource(fileSource)
				if err != nil {
					continue
				}
				line, found := findJavaTopLevelSymbolLine(fileTree.RootNode(), fileSource, symbolName)
				fileTree.Close()
				if found {
					return javaFilePath, line, nil
				}
			}
			continue
		}

		// Plain (non-wildcard) import: only relevant if its imported class
		// name (the qualified name's last dotted component) is exactly
		// symbolName -- otherwise this import cannot define symbolName, and
		// there is no reason to touch the filesystem for it at all.
		nameParts := strings.Split(imp.qualifiedName, ".")
		className := nameParts[len(nameParts)-1]
		if className != symbolName {
			continue
		}
		pkgParts := nameParts[:len(nameParts)-1]

		javaFilePath := filepath.Join(append([]string{sourceRoot}, pkgParts...)...)
		javaFilePath = filepath.Join(javaFilePath, className+".java")

		fileSource, err := os.ReadFile(javaFilePath)
		if err != nil {
			continue
		}
		anyPackageResolved = true

		fileTree, err := p.parseSource(fileSource)
		if err != nil {
			continue
		}
		line, found := findJavaTopLevelSymbolLine(fileTree.RootNode(), fileSource, symbolName)
		fileTree.Close()
		if found {
			return javaFilePath, line, nil
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
// referenced or invoked, excluding matches inside comments
// (line_comment/block_comment) and string literals (string_literal). It
// mirrors GolangParser.FindReferencesInFile: definition-site identifiers are
// not excluded, only comment and string-literal occurrences are.
func (p *JavaParser) FindReferencesInFile(filePath, symbolName string) ([]ReferenceData, error) {
	// File-not-found / read errors are swallowed (an empty result is
	// returned rather than an error), matching GolangParser.
	// FindReferencesInFile: this method is called across every file in a
	// project-wide scan, where one unreadable file should not abort the
	// whole search.
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
		case "line_comment", "block_comment", "string_literal":
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

// javaSkeletonReplacement describes a byte range within a source file (a
// method/constructor body) that GenerateSkeleton replaces with
// "{ /* ... */ }".
type javaSkeletonReplacement struct {
	start, end int
}

// collectJavaSkeletonReplacements walks node (and its descendants),
// recording a javaSkeletonReplacement for every method_declaration/
// constructor_declaration node's body. It does not recurse into a matched
// node's own children (mirroring collectGoSkeletonReplacements), so any
// local/anonymous class bodies nested within a method are left alone: they
// are included verbatim in the outer method's replaced range.
func collectJavaSkeletonReplacements(node *tree_sitter.Node, out *[]javaSkeletonReplacement) {
	switch node.Kind() {
	case "method_declaration", "constructor_declaration":
		body := node.ChildByFieldName("body")
		if body != nil {
			*out = append(*out, javaSkeletonReplacement{
				start: int(body.StartByte()),
				end:   int(body.EndByte()),
			})
		}
		return
	}

	for i := uint(0); i < node.ChildCount(); i++ {
		collectJavaSkeletonReplacements(node.Child(i), out)
	}
}

// GenerateSkeleton reconstructs filePath's source with every method/
// constructor body replaced by "{ /* ... */ }", leaving signatures,
// modifiers, and Javadoc comments untouched.
func (p *JavaParser) GenerateSkeleton(filePath string) (string, error) {
	source, err := readFile(filePath)
	if err != nil {
		return "", err
	}

	tree, err := p.parseSource(source)
	if err != nil {
		return "", err
	}
	defer tree.Close()

	var replacements []javaSkeletonReplacement
	collectJavaSkeletonReplacements(tree.RootNode(), &replacements)

	// Sorted by start byte descending so each replacement is applied before
	// any byte-offset shift it causes could affect the (not yet processed)
	// replacements that precede it in the file, mirroring GenerateSkeleton in
	// golang.go.
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })

	result := append([]byte(nil), source...)
	for _, r := range replacements {
		replacement := []byte("{ /* ... */ }")
		result = append(result[:r.start], append(replacement, result[r.end:]...)...)
	}

	return string(result), nil
}
