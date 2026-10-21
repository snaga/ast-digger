// Package parser: rust.go implements the Parser interface for Rust source
// files using the tree-sitter-rust grammar.
package parser

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"

	"github.com/snaga/ast-digger/internal/apperr"
)

// normalizeNewlines converts all CRLF ("\r\n") line endings to LF ("\n").
func normalizeNewlines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// RustParser implements the Parser interface for Rust (.rs) source files.
type RustParser struct {
	lang *tree_sitter.Language
}

// Compile-time assertion that RustParser satisfies the Parser interface.
var _ Parser = (*RustParser)(nil)

// NewRustParser constructs a RustParser with the tree-sitter-rust grammar loaded.
func NewRustParser() *RustParser {
	return &RustParser{lang: tree_sitter.NewLanguage(tree_sitter_rust.Language())}
}

// parseSource parses source using a fresh tree-sitter parser configured with the Rust grammar.
func (p *RustParser) parseSource(source []byte) (*tree_sitter.Tree, error) {
	ts := tree_sitter.NewParser()
	defer ts.Close()

	if err := ts.SetLanguage(p.lang); err != nil {
		return nil, fmt.Errorf("failed to set tree-sitter-rust language: %w", err)
	}

	tree := ts.Parse(source, nil)
	if tree == nil {
		return nil, fmt.Errorf("tree-sitter-rust failed to parse source")
	}
	return tree, nil
}

// findRustDocComment walks backwards from node through preceding comment nodes
// and aggregates contiguous doc comments (///, //!, /**, /*!) into a single normalized docstring.
func findRustDocComment(node *tree_sitter.Node, source []byte) *string {
	if node.Parent() == nil {
		return nil
	}

	var commentNodes []*tree_sitter.Node
	var lastPrefix string
	for sib := node.PrevSibling(); sib != nil; sib = sib.PrevSibling() {
		if sib.Kind() == "attribute_item" {
			continue
		}
		if sib.Kind() == "line_comment" {
			text := strings.TrimSpace(sib.Utf8Text(source))
			var prefix string
			if strings.HasPrefix(text, "///") {
				prefix = "///"
			} else if strings.HasPrefix(text, "//!") {
				prefix = "//!"
			}

			if prefix != "" {
				if lastPrefix == "" || lastPrefix == prefix {
					lastPrefix = prefix
					commentNodes = append(commentNodes, sib)
					continue
				}
			}
			break
		}
		if sib.Kind() == "block_comment" {
			text := strings.TrimSpace(sib.Utf8Text(source))
			var prefix string
			if strings.HasPrefix(text, "/**") {
				prefix = "/**"
			} else if strings.HasPrefix(text, "/*!") {
				prefix = "/*!"
			}

			if prefix != "" {
				if lastPrefix == "" || lastPrefix == prefix {
					lastPrefix = prefix
					commentNodes = append(commentNodes, sib)
					continue
				}
			}
			break
		}
		if sib.IsNamed() {
			break
		}
	}

	if len(commentNodes) == 0 {
		return nil
	}

	// Reverse to restore original top-to-bottom line order
	for i, j := 0, len(commentNodes)-1; i < j; i, j = i+1, j-1 {
		commentNodes[i], commentNodes[j] = commentNodes[j], commentNodes[i]
	}

	var lines []string
	for _, commentNode := range commentNodes {
		text := normalizeNewlines(commentNode.Utf8Text(source))
		lines = append(lines, strings.TrimSpace(text))
	}

	result := strings.Join(lines, "\n")
	return &result
}

// getRustFuncSignature extracts signature text from fn node (start to body start or fn end).
func getRustFuncSignature(funcNode *tree_sitter.Node, source []byte) string {
	body := funcNode.ChildByFieldName("body")
	endByte := funcNode.EndByte()
	if body != nil {
		endByte = body.StartByte()
	}
	sig := string(source[funcNode.StartByte():endByte])
	return strings.TrimSpace(normalizeNewlines(sig))
}

// getRustImplType extracts the receiver type name for an impl_item node.
// e.g. `impl Greeter` -> "Greeter", `impl Display for Greeter` -> "Greeter".
func getRustImplType(implNode *tree_sitter.Node, source []byte) string {
	typeNode := implNode.ChildByFieldName("type")
	if typeNode != nil {
		return strings.TrimSpace(typeNode.Utf8Text(source))
	}
	return ""
}

// ParseFile parses filePath and returns the list of symbols found in it.
func (p *RustParser) ParseFile(filePath string) ([]SymbolData, error) {
	source, err := readFile(filePath)
	if err != nil {
		return nil, err
	}
	return p.ParseBytes(source, filePath)
}

// ParseBytes parses source and returns the list of symbols found in it.
func (p *RustParser) ParseBytes(source []byte, filePath string) ([]SymbolData, error) {
	relPath := relFilePath(filePath)

	tree, err := p.parseSource(source)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	symbols := []SymbolData{}

	var traverse func(node *tree_sitter.Node, parentType *string)
	traverse = func(node *tree_sitter.Node, parentType *string) {
		switch node.Kind() {
		case "struct_item":
			nameNode := node.ChildByFieldName("name")
			if nameNode != nil {
				symbols = append(symbols, SymbolData{
					Name:      nodeText(nameNode, source),
					Kind:      "struct",
					StartLine: int(node.StartPosition().Row) + 1,
					EndLine:   int(node.EndPosition().Row) + 1,
					Docstring: findRustDocComment(node, source),
					FilePath:  &relPath,
				})
			}
			return

		case "enum_item":
			nameNode := node.ChildByFieldName("name")
			if nameNode != nil {
				symbols = append(symbols, SymbolData{
					Name:      nodeText(nameNode, source),
					Kind:      "enum",
					StartLine: int(node.StartPosition().Row) + 1,
					EndLine:   int(node.EndPosition().Row) + 1,
					Docstring: findRustDocComment(node, source),
					FilePath:  &relPath,
				})
			}
			return

		case "trait_item":
			nameNode := node.ChildByFieldName("name")
			if nameNode != nil {
				symbols = append(symbols, SymbolData{
					Name:      nodeText(nameNode, source),
					Kind:      "interface",
					StartLine: int(node.StartPosition().Row) + 1,
					EndLine:   int(node.EndPosition().Row) + 1,
					Docstring: findRustDocComment(node, source),
					FilePath:  &relPath,
				})
			}
			return

		case "mod_item":
			nameNode := node.ChildByFieldName("name")
			if nameNode != nil {
				symbols = append(symbols, SymbolData{
					Name:      nodeText(nameNode, source),
					Kind:      "type",
					StartLine: int(node.StartPosition().Row) + 1,
					EndLine:   int(node.EndPosition().Row) + 1,
					Docstring: findRustDocComment(node, source),
					FilePath:  &relPath,
				})
			}
			return

		case "impl_item":
			implTypeName := getRustImplType(node, source)
			var pType *string
			if implTypeName != "" {
				pType = &implTypeName
			}
			body := node.ChildByFieldName("body")
			if body != nil {
				for i := uint(0); i < body.ChildCount(); i++ {
					traverse(body.Child(i), pType)
				}
			}
			return

		case "function_item":
			nameNode := node.ChildByFieldName("name")
			if nameNode != nil {
				name := nodeText(nameNode, source)
				sig := getRustFuncSignature(node, source)
				kind := "function"
				if parentType != nil {
					kind = "method"
				}
				symbols = append(symbols, SymbolData{
					Name:      name,
					Kind:      kind,
					Parent:    parentType,
					StartLine: int(node.StartPosition().Row) + 1,
					EndLine:   int(node.EndPosition().Row) + 1,
					Signature: &sig,
					Docstring: findRustDocComment(node, source),
					FilePath:  &relPath,
				})
			}
			return
		}

		for i := uint(0); i < node.ChildCount(); i++ {
			traverse(node.Child(i), parentType)
		}
	}

	traverse(tree.RootNode(), nil)
	return symbols, nil
}

// ExtractSymbol resolves symbolPath (e.g. "Greeter.greet" or "main") within filePath and returns SymbolData.
func (p *RustParser) ExtractSymbol(filePath, symbolPath string) (SymbolData, error) {
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
	foundKind := "function"
	var foundParent *string

	var search func(node *tree_sitter.Node, parentType *string)
	search = func(node *tree_sitter.Node, parentType *string) {
		if foundNode != nil {
			return
		}

		switch node.Kind() {
		case "struct_item":
			if !hasMember {
				nameNode := node.ChildByFieldName("name")
				if nameNode != nil && nodeText(nameNode, source) == topName {
					foundNode = node
					foundKind = "struct"
					return
				}
			}
		case "enum_item":
			if !hasMember {
				nameNode := node.ChildByFieldName("name")
				if nameNode != nil && nodeText(nameNode, source) == topName {
					foundNode = node
					foundKind = "enum"
					return
				}
			}
		case "trait_item":
			if !hasMember {
				nameNode := node.ChildByFieldName("name")
				if nameNode != nil && nodeText(nameNode, source) == topName {
					foundNode = node
					foundKind = "interface"
					return
				}
			}
		case "mod_item":
			if !hasMember {
				nameNode := node.ChildByFieldName("name")
				if nameNode != nil && nodeText(nameNode, source) == topName {
					foundNode = node
					foundKind = "type"
					return
				}
			}
		case "impl_item":
			implTypeName := getRustImplType(node, source)
			var pType *string
			if implTypeName != "" {
				pType = &implTypeName
			}
			body := node.ChildByFieldName("body")
			if body != nil {
				for i := uint(0); i < body.ChildCount(); i++ {
					search(body.Child(i), pType)
				}
			}
			return
		case "function_item":
			nameNode := node.ChildByFieldName("name")
			if nameNode != nil {
				funcName := nodeText(nameNode, source)
				if hasMember && parentType != nil && *parentType == topName && funcName == memberName {
					foundNode = node
					foundKind = "method"
					foundParent = parentType
					return
				}
				if !hasMember && funcName == topName {
					foundNode = node
					if parentType != nil {
						foundKind = "method"
						foundParent = parentType
					} else {
						foundKind = "function"
					}
					return
				}
			}
		}

		for i := uint(0); i < node.ChildCount(); i++ {
			if foundNode == nil {
				search(node.Child(i), parentType)
			}
		}
	}

	search(tree.RootNode(), nil)

	if foundNode == nil {
		return SymbolData{}, &apperr.SymbolNotFoundError{
			FilePath:    filePath,
			SymbolPath:  symbolPath,
			Suggestions: suggestSymbols(p.ParseFile, filePath, symbolPath),
		}
	}

	code := normalizeNewlines(foundNode.Utf8Text(source))
	name := topName
	if hasMember {
		name = memberName
	}

	var signature *string
	if foundNode.Kind() == "function_item" {
		sig := getRustFuncSignature(foundNode, source)
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
		Docstring: findRustDocComment(foundNode, source),
		FilePath:  &relPath,
		Code:      &code,
	}, nil
}

type rustImportMatch struct {
	PathSegments []string
	OriginalName string
}

func parseRustUseClause(node *tree_sitter.Node, source []byte, currentPrefix []string, symbolName string, out *[]rustImportMatch) {
	text := func(n *tree_sitter.Node) string {
		if n == nil {
			return ""
		}
		return strings.TrimSpace(n.Utf8Text(source))
	}

	switch node.Kind() {
	case "use_declaration":
		for i := uint(0); i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child.Kind() != "use" && child.Kind() != ";" && child.Kind() != "visibility_modifier" {
				parseRustUseClause(child, source, currentPrefix, symbolName, out)
			}
		}
		return

	case "scoped_use_list":
		var prefix []string
		pathNode := node.ChildByFieldName("path")
		if pathNode != nil {
			prefix = parseRustPathSegments(pathNode, source)
		}
		newPrefix := append(append([]string(nil), currentPrefix...), prefix...)

		listNode := node.ChildByFieldName("list")
		if listNode != nil {
			for i := uint(0); i < listNode.ChildCount(); i++ {
				parseRustUseClause(listNode.Child(i), source, newPrefix, symbolName, out)
			}
		}
		return

	case "use_wildcard":
		return

	case "use_as_clause":
		pathNode := node.ChildByFieldName("path")
		aliasNode := node.ChildByFieldName("alias")
		if aliasNode != nil && text(aliasNode) == symbolName {
			if pathNode != nil {
				seg := parseRustPathSegments(pathNode, source)
				if len(seg) > 0 {
					origName := seg[len(seg)-1]
					modSeg := append(append([]string(nil), currentPrefix...), seg[:len(seg)-1]...)
					*out = append(*out, rustImportMatch{
						PathSegments: modSeg,
						OriginalName: origName,
					})
				}
			}
		}
		return

	case "use_list":
		for i := uint(0); i < node.ChildCount(); i++ {
			parseRustUseClause(node.Child(i), source, currentPrefix, symbolName, out)
		}
		return

	case "identifier":
		if text(node) == symbolName {
			*out = append(*out, rustImportMatch{
				PathSegments: currentPrefix,
				OriginalName: symbolName,
			})
		}
		return

	case "scoped_identifier":
		segments := parseRustPathSegments(node, source)
		if len(segments) > 0 {
			lastName := segments[len(segments)-1]
			if lastName == symbolName {
				modSeg := append(append([]string(nil), currentPrefix...), segments[:len(segments)-1]...)
				*out = append(*out, rustImportMatch{
					PathSegments: modSeg,
					OriginalName: symbolName,
				})
			}
		}
		return
	}
}

func parseRustPathSegments(node *tree_sitter.Node, source []byte) []string {
	if node == nil {
		return nil
	}
	var segments []string
	var walk func(n *tree_sitter.Node)
	walk = func(n *tree_sitter.Node) {
		switch n.Kind() {
		case "identifier", "crate", "super", "self":
			segments = append(segments, strings.TrimSpace(n.Utf8Text(source)))
			return
		case "scoped_identifier":
			pathNode := n.ChildByFieldName("path")
			nameNode := n.ChildByFieldName("name")
			if pathNode != nil {
				walk(pathNode)
			}
			if nameNode != nil {
				walk(nameNode)
			}
			return
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(node)
	return segments
}

func scanRustImports(node *tree_sitter.Node, source []byte, symbolName string) ([]rustImportMatch, bool) {
	var matches []rustImportMatch
	var scan func(n *tree_sitter.Node)
	scan = func(n *tree_sitter.Node) {
		if n.Kind() == "use_declaration" {
			parseRustUseClause(n, source, nil, symbolName, &matches)
			return
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			scan(n.Child(i))
		}
	}
	scan(node)
	return matches, len(matches) > 0
}

// ResolveImports resolves relative/crate/super/self imports in filePath for symbolName.
func (p *RustParser) ResolveImports(filePath, symbolName string) (string, int, error) {
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

	matches, found := scanRustImports(tree.RootNode(), source, symbolName)
	if !found {
		return "", 0, &apperr.SymbolNotFoundError{
			FilePath:   filePath,
			SymbolPath: symbolName,
		}
	}

	fileDir := filepath.Dir(absPath)
	projectRoot := fileDir
	// Find project root by searching upwards for Cargo.toml or src directory
	for d := fileDir; d != filepath.Dir(d); d = filepath.Dir(d) {
		if fileExists(filepath.Join(d, "Cargo.toml")) {
			projectRoot = d
			break
		}
	}

	for _, match := range matches {
		if len(match.PathSegments) == 0 {
			continue
		}

		// Count leading "super" segments
		superCount := 0
		for _, seg := range match.PathSegments {
			if seg == "super" {
				superCount++
			} else {
				break
			}
		}

		firstSeg := match.PathSegments[0]

		var baseDirs []string
		var modulePath []string

		if superCount > 0 {
			d := fileDir
			for i := 0; i < superCount; i++ {
				d = filepath.Dir(d)
			}
			baseDirs = []string{d}
			modulePath = match.PathSegments[superCount:]
		} else {
			switch firstSeg {
			case "crate":
				baseDirs = []string{projectRoot, filepath.Join(projectRoot, "src")}
				modulePath = match.PathSegments[1:]
			case "self":
				baseDirs = []string{fileDir}
				modulePath = match.PathSegments[1:]
			default:
				// Relative module or external crate
				baseDirs = []string{fileDir}
				modulePath = match.PathSegments
			}
		}

		var candidateFiles []string
		for _, baseDir := range baseDirs {
			if len(modulePath) == 0 {
				candidateFiles = append(candidateFiles,
					filepath.Join(baseDir, "mod.rs"),
					filepath.Join(baseDir, "lib.rs"),
					filepath.Join(baseDir, "main.rs"),
				)
			} else {
				modJoined := filepath.Join(modulePath...)
				candidateFiles = append(candidateFiles,
					filepath.Join(baseDir, modJoined+".rs"),
					filepath.Join(baseDir, modJoined, "mod.rs"),
				)
			}
		}

		var targetFile string
		for _, cand := range candidateFiles {
			if info, statErr := os.Stat(cand); statErr == nil && !info.IsDir() {
				targetFile = cand
				break
			}
		}

		if targetFile == "" {
			continue
		}

		targetParser := NewRustParser()
		targetSyms, parseErr := targetParser.ParseFile(targetFile)
		if parseErr != nil {
			relTarget := relFilePath(targetFile)
			return relTarget, 1, nil
		}

		for _, sym := range targetSyms {
			if sym.Name == match.OriginalName {
				relTarget := relFilePath(targetFile)
				return relTarget, sym.StartLine, nil
			}
		}

		relTarget := relFilePath(targetFile)
		return relTarget, 1, nil
	}

	return "", 0, &apperr.SymbolNotFoundError{
		FilePath:   filePath,
		SymbolPath: symbolName,
	}
}

func isRustDefinitionIdentifier(node *tree_sitter.Node, source []byte) bool {
	parent := node.Parent()
	if parent == nil {
		return false
	}

	switch parent.Kind() {
	case "function_item", "struct_item", "enum_item", "trait_item", "mod_item", "type_item", "const_item", "static_item", "field_declaration":
		nameNode := parent.ChildByFieldName("name")
		if nameNode != nil && nameNode.Id() == node.Id() {
			return true
		}
	case "let_declaration":
		patternNode := parent.ChildByFieldName("pattern")
		if patternNode != nil && patternNode.Id() == node.Id() {
			return true
		}
	}
	return false
}

// FindReferencesInFile searches filePath for identifier nodes matching symbolName,
// excluding comments, string literals, and definition site identifiers.
func (p *RustParser) FindReferencesInFile(filePath, symbolName string) ([]ReferenceData, error) {
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
		case "line_comment", "block_comment", "string_literal", "raw_string_literal":
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

	var references []ReferenceData

	var traverse func(node *tree_sitter.Node)
	traverse = func(node *tree_sitter.Node) {
		if node.Kind() == "identifier" || node.Kind() == "field_identifier" {
			text := strings.TrimSpace(node.Utf8Text(source))
			if text == symbolName && !isExcluded(node.StartByte()) && !isRustDefinitionIdentifier(node, source) {
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

type rustSkeletonReplacement struct {
	start, end int
}

func collectRustSkeletonReplacements(node *tree_sitter.Node, out *[]rustSkeletonReplacement) {
	if node.Kind() == "function_item" {
		body := node.ChildByFieldName("body")
		if body != nil && body.Kind() == "block" {
			*out = append(*out, rustSkeletonReplacement{
				start: int(body.StartByte()),
				end:   int(body.EndByte()),
			})
			return
		}
	}

	for i := uint(0); i < node.ChildCount(); i++ {
		collectRustSkeletonReplacements(node.Child(i), out)
	}
}

// GenerateSkeleton reconstructs filePath's source with every function/method body block replaced by { /* ... */ }.
func (p *RustParser) GenerateSkeleton(filePath string) (string, error) {
	source, err := readFile(filePath)
	if err != nil {
		return "", err
	}

	tree, err := p.parseSource(source)
	if err != nil {
		return "", err
	}
	defer tree.Close()

	var replacements []rustSkeletonReplacement
	collectRustSkeletonReplacements(tree.RootNode(), &replacements)

	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })

	result := append([]byte(nil), source...)
	for _, r := range replacements {
		replacement := []byte("{ /* ... */ }")
		result = append(result[:r.start], append(replacement, result[r.end:]...)...)
	}

	return normalizeNewlines(string(result)), nil
}
