// Package parser: markdown.go implements the Parser interface for Markdown source
// files using the tree-sitter-markdown grammar.
package parser

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/snaga/ast-digger/internal/apperr"
	"github.com/snaga/ast-digger/internal/parser/markdown"
)

// MarkdownParser implements the Parser interface for Markdown (.md) source files.
type MarkdownParser struct {
	lang *tree_sitter.Language
}

// Compile-time assertion that MarkdownParser satisfies the Parser interface.
var _ Parser = (*MarkdownParser)(nil)

// NewMarkdownParser constructs a MarkdownParser with the tree-sitter-markdown grammar loaded.
func NewMarkdownParser() *MarkdownParser {
	return &MarkdownParser{lang: tree_sitter.NewLanguage(markdown.Language())}
}

func (p *MarkdownParser) parseSource(source []byte) (*tree_sitter.Tree, error) {
	ts := tree_sitter.NewParser()
	defer ts.Close()

	if err := ts.SetLanguage(p.lang); err != nil {
		return nil, fmt.Errorf("failed to set tree-sitter-markdown language: %w", err)
	}

	tree := ts.Parse(source, nil)
	if tree == nil {
		return nil, fmt.Errorf("tree-sitter-markdown failed to parse source")
	}
	return tree, nil
}

type headingStackItem struct {
	level int
	title string
}

// getHeadingLevel returns the 1-indexed level (1-6) of an atx_heading node.
func getHeadingLevel(headingNode *tree_sitter.Node) int {
	for i := uint(0); i < headingNode.ChildCount(); i++ {
		kind := headingNode.Child(i).Kind()
		switch kind {
		case "atx_h1_marker":
			return 1
		case "atx_h2_marker":
			return 2
		case "atx_h3_marker":
			return 3
		case "atx_h4_marker":
			return 4
		case "atx_h5_marker":
			return 5
		case "atx_h6_marker":
			return 6
		}
	}
	return 1
}

// getHeadingTitle extracts the plain text title of an atx_heading node.
func getHeadingTitle(headingNode *tree_sitter.Node, source []byte) string {
	var parts []string
	for i := uint(0); i < headingNode.ChildCount(); i++ {
		child := headingNode.Child(i)
		if strings.HasPrefix(child.Kind(), "atx_h") && strings.HasSuffix(child.Kind(), "_marker") {
			continue
		}
		parts = append(parts, child.Utf8Text(source))
	}
	title := strings.TrimSpace(strings.Join(parts, ""))
	return unescapeSymbolPath(title)
}

// getCodeBlockLanguage extracts language identifier from info_string node in fenced_code_block.
func getCodeBlockLanguage(blockNode *tree_sitter.Node, source []byte) string {
	info := blockNode.ChildByFieldName("info_string")
	if info != nil {
		for i := uint(0); i < info.ChildCount(); i++ {
			c := info.Child(i)
			if c.Kind() == "language" {
				langText := strings.TrimSpace(c.Utf8Text(source))
				if langText != "" {
					return langText
				}
			}
		}
		text := strings.TrimSpace(info.Utf8Text(source))
		if text != "" {
			return text
		}
	}
	// Also search direct children of fenced_code_block for info_string / language
	for i := uint(0); i < blockNode.ChildCount(); i++ {
		c := blockNode.Child(i)
		if c.Kind() == "info_string" {
			text := strings.TrimSpace(c.Utf8Text(source))
			if text != "" {
				return text
			}
		}
	}
	return "text"
}

// ParseFile parses filePath and returns the list of Markdown symbols.
func (p *MarkdownParser) ParseFile(filePath string) ([]SymbolData, error) {
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

	var symbols []SymbolData
	var headingStack []headingStackItem

	// Traversal to collect nodes
	type rawNode struct {
		kind      string
		startLine int
		endLine   int
		level     int
		title     string
		lang      string
	}

	var rawNodes []rawNode

	var collect func(node *tree_sitter.Node)
	collect = func(node *tree_sitter.Node) {
		kind := node.Kind()
		if kind == "minus_metadata" {
			endLine := int(node.EndPosition().Row) + 1
			if node.EndPosition().Column == 0 && endLine > int(node.StartPosition().Row)+1 {
				endLine--
			}
			rawNodes = append(rawNodes, rawNode{
				kind:      "frontmatter",
				startLine: int(node.StartPosition().Row) + 1,
				endLine:   endLine,
			})
			return
		}
		if kind == "atx_heading" {
			lvl := getHeadingLevel(node)
			title := getHeadingTitle(node, source)
			rawNodes = append(rawNodes, rawNode{
				kind:      "heading",
				startLine: int(node.StartPosition().Row) + 1,
				endLine:   int(node.EndPosition().Row) + 1, // temporary
				level:     lvl,
				title:     title,
			})
			return
		}
		if kind == "fenced_code_block" {
			lang := getCodeBlockLanguage(node, source)
			rawNodes = append(rawNodes, rawNode{
				kind:      "code_block",
				startLine: int(node.StartPosition().Row) + 1,
				endLine:   int(node.EndPosition().Row) + 1,
				lang:      lang,
			})
			return
		}

		for i := uint(0); i < node.ChildCount(); i++ {
			collect(node.Child(i))
		}
	}

	collect(tree.RootNode())

	lines := splitLines(string(source))
	totalLines := len(lines)
	if totalLines == 0 {
		totalLines = 1
	}

	// Code block counts per section path + lang
	codeBlockCounts := make(map[string]int)

	// We first calculate heading parent paths and section end lines.
	// Filter headings to compute end lines
	var headingIndices []int
	for i, r := range rawNodes {
		if r.kind == "heading" {
			headingIndices = append(headingIndices, i)
		}
	}

	// Calculate endLine for headings
	for idx, hIdx := range headingIndices {
		currentLvl := rawNodes[hIdx].level
		endLine := totalLines

		// Look for next heading with level <= currentLvl
		for nextIdx := idx + 1; nextIdx < len(headingIndices); nextIdx++ {
			nextHIdx := headingIndices[nextIdx]
			if rawNodes[nextHIdx].level <= currentLvl {
				endLine = rawNodes[nextHIdx].startLine - 1
				break
			}
		}
		rawNodes[hIdx].endLine = endLine
	}

	// Now build symbols with stack management
	for _, rn := range rawNodes {
		switch rn.kind {
		case "frontmatter":
			parentStr := ""
			symbols = append(symbols, SymbolData{
				Name:      "frontmatter",
				Kind:      "frontmatter",
				Parent:    &parentStr,
				FilePath:  &relPath,
				StartLine: rn.startLine,
				EndLine:   rn.endLine,
			})

		case "heading":
			// Pop stack until top level < rn.level
			for len(headingStack) > 0 && headingStack[len(headingStack)-1].level >= rn.level {
				headingStack = headingStack[:len(headingStack)-1]
			}

			// Build parent path
			var parentPathParts []string
			for _, item := range headingStack {
				parentPathParts = append(parentPathParts, item.title)
			}

			var parentStr string
			if len(parentPathParts) > 0 {
				parentStr = strings.Join(parentPathParts, "/")
			}

			// Push to stack
			headingStack = append(headingStack, headingStackItem{
				level: rn.level,
				title: rn.title,
			})

			var pPtr *string
			if parentStr != "" {
				pPtr = &parentStr
			}

			symbols = append(symbols, SymbolData{
				Name:      rn.title,
				Kind:      "heading",
				Parent:    pPtr,
				FilePath:  &relPath,
				StartLine: rn.startLine,
				EndLine:   rn.endLine,
			})

		case "code_block":
			// Parent path is current heading stack path
			var parentPathParts []string
			for _, item := range headingStack {
				parentPathParts = append(parentPathParts, item.title)
			}
			parentStr := strings.Join(parentPathParts, "/")

			countKey := parentStr + "::" + rn.lang
			codeBlockCounts[countKey]++
			count := codeBlockCounts[countKey]

			blockName := fmt.Sprintf("code_block:%s", rn.lang)
			if count > 1 {
				blockName = fmt.Sprintf("code_block:%s#%d", rn.lang, count)
			}

			var pPtr *string
			if parentStr != "" {
				pPtr = &parentStr
			}

			symbols = append(symbols, SymbolData{
				Name:      blockName,
				Kind:      "code_block",
				Parent:    pPtr,
				FilePath:  &relPath,
				StartLine: rn.startLine,
				EndLine:   rn.endLine,
			})
		}
	}

	return symbols, nil
}

// unescapeSymbolPath unescapes \/ to / in symbolPath unless quoted.
func unescapeSymbolPath(s string) string {
	var result strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && s[i+1] == '/' {
			result.WriteByte('/')
			i++
		} else {
			result.WriteByte(s[i])
		}
	}
	return result.String()
}

// ExtractSymbol extracts the symbol definition/content specified by symbolPath in filePath.
func (p *MarkdownParser) ExtractSymbol(filePath, symbolPath string) (SymbolData, error) {
	symbols, err := p.ParseFile(filePath)
	if err != nil {
		return SymbolData{}, err
	}

	cleanPath := unescapeSymbolPath(symbolPath)

	var matched []SymbolData
	var candidates []string

	for _, sym := range symbols {
		fullPath := sym.Name
		if sym.Parent != nil && *sym.Parent != "" {
			fullPath = *sym.Parent + "/" + sym.Name
		}
		candidates = append(candidates, fullPath)

		if fullPath == cleanPath || fullPath == symbolPath || sym.Name == cleanPath || sym.Name == symbolPath {
			matched = append(matched, sym)
		}
	}

	if len(matched) == 0 {
		suggestions := closestSymbolMatches(symbolPath, candidates)
		return SymbolData{}, &apperr.SymbolNotFoundError{
			FilePath:    filePath,
			SymbolPath:  symbolPath,
			Suggestions: suggestions,
		}
	}

	if len(matched) > 1 {
		// Disambiguation: if one of matched has fullPath == cleanPath (exact full path query), prefer it.
		var exact []SymbolData
		for _, m := range matched {
			fullPath := m.Name
			if m.Parent != nil && *m.Parent != "" {
				fullPath = *m.Parent + "/" + m.Name
			}
			if (fullPath == cleanPath || fullPath == symbolPath) && strings.Contains(cleanPath, "/") {
				exact = append(exact, m)
			}
		}
		if len(exact) == 1 {
			matched = exact
		} else {
			var sugg []string
			for _, m := range matched {
				fullPath := m.Name
				if m.Parent != nil && *m.Parent != "" {
					fullPath = *m.Parent + "/" + m.Name
				}
				sugg = append(sugg, fullPath)
			}
			return SymbolData{}, &apperr.SymbolNotFoundError{
				FilePath:    filePath,
				SymbolPath:  symbolPath,
				Suggestions: sugg,
			}
		}
	}

	sym := matched[0]

	// Now extract code content from source file
	source, err := readFile(filePath)
	if err != nil {
		return SymbolData{}, err
	}
	lines := splitLines(string(source))

	if sym.Kind == "frontmatter" {
		if sym.StartLine <= len(lines) && sym.EndLine <= len(lines) {
			codeText := strings.Join(lines[sym.StartLine-1:sym.EndLine], "\n") + "\n"
			sym.Code = &codeText
		}
	} else if sym.Kind == "code_block" {
		// Code block body area (excluding backticks ``` if present, or inner area)
		// Wait, requirement: "コードブロックパス（例: アーキテクチャ/code_block:mermaid や code_block:go#2）を指定された場合、コードブロックの本文領域を抽出。"
		start := sym.StartLine - 1
		end := sym.EndLine - 1
		if end >= len(lines) {
			end = len(lines) - 1
		}
		if start < len(lines) {
			// check if start line has ```
			if strings.HasPrefix(strings.TrimSpace(lines[start]), "```") {
				start++
			}
		}
		if end >= 0 && end < len(lines) && end >= start {
			if strings.HasPrefix(strings.TrimSpace(lines[end]), "```") {
				end--
			}
		}
		if start <= end && start < len(lines) {
			codeText := strings.Join(lines[start:end+1], "\n") + "\n"
			sym.Code = &codeText
		} else {
			empty := ""
			sym.Code = &empty
		}
	} else {
		// Heading section full text (StartLine .. EndLine)
		if sym.StartLine <= len(lines) {
			end := sym.EndLine
			if end > len(lines) {
				end = len(lines)
			}
			codeText := strings.Join(lines[sym.StartLine-1:end], "\n") + "\n"
			sym.Code = &codeText
		}
	}

	return sym, nil
}

// targetToAnchor converts heading title or target to markdown anchor ID format (e.g. "Section 2" -> "section-2").
func targetToAnchor(target string) string {
	target = strings.TrimLeft(target, "# \t")
	s := strings.ToLower(strings.TrimSpace(target))
	var result strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			result.WriteRune(r)
		} else if r == ' ' {
			result.WriteRune('-')
		}
	}
	return strings.Trim(result.String(), "-")
}

// ResolveImports resolves markdown relative file links or anchor references.
func (p *MarkdownParser) ResolveImports(filePath, symbolName string) (string, int, error) {
	symbols, err := p.ParseFile(filePath)
	if err != nil {
		return "", 0, err
	}

	// 1. Check if symbolName is anchor in current file (e.g. "#section-2" or "section-2" or heading title)
	targetAnchor := strings.TrimPrefix(symbolName, "#")
	for _, sym := range symbols {
		if sym.Kind == "heading" {
			if sym.Name == targetAnchor || targetToAnchor(sym.Name) == targetAnchor {
				return *sym.FilePath, sym.StartLine, nil
			}
		}
	}

	// 2. Search for link target in source file of filePath
	source, err := readFile(filePath)
	if err != nil {
		return "", 0, err
	}

	// Link pattern in markdown: [text](linkTarget)
	// We need to parse links or search for links referencing symbolName or link containing #
	// Let's parse tree-sitter or regex/string search for links
	tree, err := p.parseSource(source)
	if err != nil {
		return "", 0, err
	}
	defer tree.Close()

	// Find inline links in AST
	type linkInfo struct {
		dest string
		line int
	}
	var links []linkInfo

	var findLinks func(node *tree_sitter.Node)
	findLinks = func(node *tree_sitter.Node) {
		if node.Kind() == "inline" || node.Kind() == "link" {
			text := node.Utf8Text(source)
			// Check if text contains link pattern [text](url)
			// Extract url in (...)
			idx := strings.Index(text, "](")
			if idx != -1 {
				endIdx := strings.Index(text[idx+2:], ")")
				if endIdx != -1 {
					url := text[idx+2 : idx+2+endIdx]
					links = append(links, linkInfo{
						dest: url,
						line: int(node.StartPosition().Row) + 1,
					})
				}
			}
		}
		for i := uint(0); i < node.ChildCount(); i++ {
			findLinks(node.Child(i))
		}
	}
	findLinks(tree.RootNode())

	dir := filepath.Dir(filePath)

	for _, l := range links {
		// Check if destination matches symbolName or contains anchor matching symbolName
		// Format: path/to/file.md#anchor or #anchor or path/to/file.md
		parts := strings.SplitN(l.dest, "#", 2)
		relPath := parts[0]
		anchor := ""
		if len(parts) == 2 {
			anchor = parts[1]
		}

		if relPath == "" && anchor != "" {
			// same file anchor
			if anchor == targetAnchor {
				for _, sym := range symbols {
					if sym.Kind == "heading" && (sym.Name == anchor || targetToAnchor(sym.Name) == anchor) {
						return *sym.FilePath, sym.StartLine, nil
					}
				}
			}
		} else if relPath != "" {
			targetFilePath := filepath.Join(dir, relPath)
			if relPath == symbolName || anchor == targetAnchor || l.dest == symbolName || targetFilePath == symbolName {
				// Parse target file and resolve anchor or return start line 1 / heading line
				targetSyms, err := p.ParseFile(targetFilePath)
				if err != nil {
					return "", 0, &apperr.SymbolNotFoundError{FilePath: filePath, SymbolPath: symbolName}
				}
				if anchor != "" {
					for _, tsym := range targetSyms {
						if tsym.Kind == "heading" && (tsym.Name == anchor || targetToAnchor(tsym.Name) == anchor) {
							return *tsym.FilePath, tsym.StartLine, nil
						}
					}
				}
				if len(targetSyms) > 0 {
					return *targetSyms[0].FilePath, targetSyms[0].StartLine, nil
				}
				relTarget := relFilePath(targetFilePath)
				return relTarget, 1, nil
			}
		}
	}

	return "", 0, &apperr.SymbolNotFoundError{
		FilePath:   filePath,
		SymbolPath: symbolName,
	}
}

// FindReferencesInFile finds occurrences where symbolName or anchor is referenced (linked) in filePath.
func (p *MarkdownParser) FindReferencesInFile(filePath, symbolName string) ([]ReferenceData, error) {
	source, err := readFile(filePath)
	if err != nil {
		return nil, err
	}
	relPath := relFilePath(filePath)
	lines := splitLines(string(source))

	targetAnchor := strings.TrimPrefix(symbolName, "#")

	tree, err := p.parseSource(source)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	var codeBlockRanges [][2]int
	var collectCodeBlocks func(node *tree_sitter.Node)
	collectCodeBlocks = func(node *tree_sitter.Node) {
		kind := node.Kind()
		if kind == "fenced_code_block" || kind == "indented_code_block" {
			startLine := int(node.StartPosition().Row) + 1
			endLine := int(node.EndPosition().Row) + 1
			codeBlockRanges = append(codeBlockRanges, [2]int{startLine, endLine})
			return
		}
		for i := uint(0); i < node.ChildCount(); i++ {
			collectCodeBlocks(node.Child(i))
		}
	}
	collectCodeBlocks(tree.RootNode())

	var refs []ReferenceData

	for lineIdx, line := range lines {
		lineNum := lineIdx + 1
		inCodeBlock := false
		for _, cb := range codeBlockRanges {
			if lineNum >= cb[0] && lineNum <= cb[1] {
				inCodeBlock = true
				break
			}
		}
		if inCodeBlock {
			continue
		}

		// Check for markdown links [text](url)
		for {
			idx := strings.Index(line, "](")
			if idx == -1 {
				break
			}
			endIdx := strings.Index(line[idx+2:], ")")
			if endIdx == -1 {
				break
			}
			url := line[idx+2 : idx+2+endIdx]
			line = line[idx+2+endIdx+1:] // advance line

			// match url against symbolName / anchor / heading
			parts := strings.SplitN(url, "#", 2)
			destPath := parts[0]
			anchor := ""
			if len(parts) == 2 {
				anchor = parts[1]
			}

			matched := false
			if url == symbolName || destPath == symbolName || anchor == targetAnchor || anchor == symbolName {
				matched = true
			} else {
				// Also check if symbolName matches heading name whose targetToAnchor == anchor
				if anchor != "" && targetToAnchor(symbolName) == anchor {
					matched = true
				}
			}

			if matched {
				refs = append(refs, ReferenceData{
					FilePath:    relPath,
					LineNumber:  lineIdx + 1,
					LineContent: lines[lineIdx],
				})
				break
			}
		}
	}

	return refs, nil
}

// GenerateSkeleton reconstructs filePath's source into a skeleton format with metadata comments.
func (p *MarkdownParser) GenerateSkeleton(filePath string) (string, error) {
	source, err := readFile(filePath)
	if err != nil {
		return "", err
	}

	symbols, err := p.ParseFile(filePath)
	if err != nil {
		return "", err
	}

	lines := splitLines(string(source))

	type lineRange struct {
		startLine int // 1-indexed
		endLine   int // 1-indexed
		comment   string
	}

	var ranges []lineRange

	for _, sym := range symbols {
		if sym.Kind == "frontmatter" {
			var keys []string
			for l := sym.StartLine - 1; l < sym.EndLine; l++ {
				if l >= 0 && l < len(lines) {
					line := strings.TrimSpace(lines[l])
					if line != "" && line != "---" {
						parts := strings.SplitN(line, ":", 2)
						if len(parts) > 0 {
							keys = append(keys, strings.TrimSpace(parts[0]))
						}
					}
				}
			}
			keyStr := strings.Join(keys, ", ")
			if keyStr != "" {
				keyStr = " (" + keyStr + ")"
			}
			comment := fmt.Sprintf("<!-- [frontmatter] L%d-L%d%s -->", sym.StartLine, sym.EndLine, keyStr)
			ranges = append(ranges, lineRange{
				startLine: sym.StartLine,
				endLine:   sym.EndLine,
				comment:   comment,
			})
		} else if sym.Kind == "code_block" {
			lineCount := sym.EndLine - sym.StartLine + 1
			// Name is code_block:<lang> or code_block:<lang>#<num>
			langName := sym.Name
			if idx := strings.Index(langName, "#"); idx != -1 {
				langName = langName[:idx]
			}
			comment := fmt.Sprintf("<!-- [%s] L%d-L%d (%d lines) -->", langName, sym.StartLine, sym.EndLine, lineCount)
			ranges = append(ranges, lineRange{
				startLine: sym.StartLine,
				endLine:   sym.EndLine,
				comment:   comment,
			})
		}
	}

	// We replace frontmatter range (StartLine..EndLine) and code_block ranges (StartLine..EndLine) with comments
	// and omit normal text. But wait! Heading lines should be preserved.
	// Let's build skeleton line by line.

	// Collect heading lines
	headingLines := make(map[int]string)
	for _, sym := range symbols {
		if sym.Kind == "heading" {
			headingLines[sym.StartLine] = lines[sym.StartLine-1]
		}
	}

	sort.Slice(ranges, func(i, j int) bool { return ranges[i].startLine < ranges[j].startLine })

	var outLines []string
	lineNum := 1
	for lineNum <= len(lines) {
		// Check if lineNum is inside any range
		inRange := false
		for _, r := range ranges {
			if lineNum >= r.startLine && lineNum <= r.endLine {
				if lineNum == r.startLine {
					outLines = append(outLines, r.comment)
				}
				lineNum = r.endLine + 1
				inRange = true
				break
			}
		}
		if inRange {
			continue
		}

		if hText, ok := headingLines[lineNum]; ok {
			outLines = append(outLines, hText)
		}
		lineNum++
	}

	return strings.Join(outLines, "\n") + "\n", nil
}
