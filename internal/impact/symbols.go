package impact

import (
	"bytes"
	"path/filepath"
	"strings"
	"unicode"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/snaga/ast-digger/internal/parser"
)

// AuditFileSymbols analyzes the symbols in info against git diff lines and returns
// impacted symbols and docstring audit results.
func AuditFileSymbols(info *FileDiffInfo, req DocAuditRequirement) ([]*SymbolImpact, *DocAuditResult, error) {
	if info == nil {
		return nil, nil, nil
	}
	if req == "" {
		req = DocAuditExported
	}

	targetPath := info.FilePath
	if targetPath == "" {
		targetPath = info.OldPath
	}
	if targetPath == "" {
		return nil, nil, nil
	}

	p, err := parser.ForPath(targetPath)
	if err != nil {
		// Unsupported file type, return empty results gracefully
		return nil, nil, nil
	}

	var oldSymbols []parser.SymbolData
	if !info.IsNew && len(info.OldContent) > 0 {
		oldSyms, err := p.ParseBytes(info.OldContent, targetPath)
		if err == nil {
			oldSymbols = oldSyms
		}
	}

	var newSymbols []parser.SymbolData
	if !info.IsDeleted && len(info.NewContent) > 0 {
		newSyms, err := p.ParseBytes(info.NewContent, targetPath)
		if err == nil {
			newSymbols = newSyms
		}
	}

	lang, _ := GetLanguage(targetPath)

	oldMap := make(map[string]parser.SymbolData, len(oldSymbols))
	for _, s := range oldSymbols {
		oldMap[getSymbolPath(s)] = s
	}

	newMap := make(map[string]parser.SymbolData, len(newSymbols))
	for _, s := range newSymbols {
		newMap[getSymbolPath(s)] = s
	}

	var impacts []*SymbolImpact

	// 1. Process new symbols (added or modified)
	for _, newSym := range newSymbols {
		symPath := getSymbolPath(newSym)
		oldSym, existsOld := oldMap[symPath]

		hasDoc := newSym.Docstring != nil && len(strings.TrimSpace(*newSym.Docstring)) > 0
		visibility := determineVisibility(targetPath, newSym)

		if !existsOld {
			// Added symbol
			newStmts := countSymbolStatements(info.NewContent, newSym.StartLine, newSym.EndLine, lang)
			impacts = append(impacts, &SymbolImpact{
				File:            targetPath,
				SymbolPath:      symPath,
				Kind:            newSym.Kind,
				Visibility:      visibility,
				ChangeType:      "added",
				StatementsDelta: newStmts,
				HasDocstring:    hasDoc,
			})
		} else {
			// Symbol exists in both old and new. Check if modified.
			isModified := linesOverlap(newSym.StartLine, newSym.EndLine, info.AddedLines)
			if !isModified && len(info.DeletedLines) > 0 {
				isModified = linesOverlap(oldSym.StartLine, oldSym.EndLine, info.DeletedLines)
			}
			if isModified {
				oldStmts := countSymbolStatements(info.OldContent, oldSym.StartLine, oldSym.EndLine, lang)
				newStmts := countSymbolStatements(info.NewContent, newSym.StartLine, newSym.EndLine, lang)
				impacts = append(impacts, &SymbolImpact{
					File:            targetPath,
					SymbolPath:      symPath,
					Kind:            newSym.Kind,
					Visibility:      visibility,
					ChangeType:      "modified",
					StatementsDelta: newStmts - oldStmts,
					HasDocstring:    hasDoc,
				})
			}
		}
	}

	// 2. Process deleted symbols
	for _, oldSym := range oldSymbols {
		symPath := getSymbolPath(oldSym)
		if _, exists := newMap[symPath]; !exists {
			hasDoc := oldSym.Docstring != nil && len(strings.TrimSpace(*oldSym.Docstring)) > 0
			visibility := determineVisibility(targetPath, oldSym)
			oldStmts := countSymbolStatements(info.OldContent, oldSym.StartLine, oldSym.EndLine, lang)

			impacts = append(impacts, &SymbolImpact{
				File:            targetPath,
				SymbolPath:      symPath,
				Kind:            oldSym.Kind,
				Visibility:      visibility,
				ChangeType:      "deleted",
				StatementsDelta: -oldStmts,
				HasDocstring:    hasDoc,
			})
		}
	}

	auditResult := calculateDocAudit(impacts, targetPath, req)
	return impacts, auditResult, nil
}

func getSymbolPath(sym parser.SymbolData) string {
	if sym.Parent != nil && *sym.Parent != "" {
		return *sym.Parent + "." + sym.Name
	}
	return sym.Name
}

func determineVisibility(filePath string, sym parser.SymbolData) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	name := sym.Name
	sig := ""
	if sym.Signature != nil {
		sig = *sym.Signature
	}

	switch ext {
	case ".go":
		// Go: First letter uppercase -> public
		for _, r := range name {
			if unicode.IsUpper(r) {
				return "public"
			}
			return "private"
		}
		return "private"

	case ".py":
		// Python: starts with _ -> private
		if strings.HasPrefix(name, "_") {
			return "private"
		}
		return "public"

	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		// TS/JS: private fields (#) or leading _ or "private" keyword
		if strings.HasPrefix(name, "#") || strings.HasPrefix(name, "_") {
			return "private"
		}
		sigLower := strings.ToLower(sig)
		if strings.Contains(sigLower, "private ") || strings.HasPrefix(sigLower, "private") {
			return "private"
		}
		return "public"

	case ".java":
		// Java: explicitly public -> public; otherwise private
		if strings.Contains(sig, "public ") || strings.HasPrefix(sig, "public") {
			return "public"
		}
		return "private"

	case ".rs":
		// Rust: pub keyword -> public; otherwise private
		if strings.HasPrefix(sig, "pub ") || strings.HasPrefix(sig, "pub(") || strings.Contains(sig, "pub ") {
			return "public"
		}
		return "private"

	case ".md", ".markdown":
		// Markdown: Document structure headings/code blocks are not exported APIs
		return "private"

	default:
		return "public"
	}
}

func linesOverlap(startLine, endLine int, lines []int) bool {
	for _, l := range lines {
		if l >= startLine && l <= endLine {
			return true
		}
	}
	return false
}

func extractSymbolBytes(source []byte, startLine, endLine int) []byte {
	if len(source) == 0 || startLine <= 0 || endLine < startLine {
		return nil
	}
	lines := bytes.Split(source, []byte("\n"))
	if startLine > len(lines) {
		return nil
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	subLines := lines[startLine-1 : endLine]
	return bytes.Join(subLines, []byte("\n"))
}

func countSymbolStatements(source []byte, startLine, endLine int, lang *tree_sitter.Language) int {
	if lang == nil || len(source) == 0 {
		return 0
	}
	snippet := extractSymbolBytes(source, startLine, endLine)
	if len(snippet) == 0 {
		return 0
	}
	stats, err := countASTStats(lang, snippet)
	if err != nil {
		return 0
	}
	return stats.Statements
}

func calculateDocAudit(impacts []*SymbolImpact, filePath string, req DocAuditRequirement) *DocAuditResult {
	totalPublic := 0
	docPublic := 0
	totalPrivate := 0
	docPrivate := 0
	var missing []string

	for _, imp := range impacts {
		if imp.ChangeType == "deleted" {
			continue // Deleted symbols are not audited for docstrings
		}

		if imp.Visibility == "public" {
			totalPublic++
			if imp.HasDocstring {
				docPublic++
			} else if req == DocAuditExported || req == DocAuditAll {
				missing = append(missing, formatMissingDoc(filePath, imp.SymbolPath))
			}
		} else {
			totalPrivate++
			if imp.HasDocstring {
				docPrivate++
			} else if req == DocAuditAll {
				missing = append(missing, formatMissingDoc(filePath, imp.SymbolPath))
			}
		}
	}

	pubCoverage := 1.0
	if totalPublic > 0 {
		pubCoverage = float64(docPublic) / float64(totalPublic)
	}

	privCoverage := 1.0
	if totalPrivate > 0 {
		privCoverage = float64(docPrivate) / float64(totalPrivate)
	}

	if missing == nil {
		missing = []string{}
	}

	return &DocAuditResult{
		PublicCoverage:    pubCoverage,
		PrivateCoverage:   privCoverage,
		MissingDocstrings: missing,
	}
}

func formatMissingDoc(file, symbolPath string) string {
	if file == "" {
		return symbolPath
	}
	return file + "::" + symbolPath
}
