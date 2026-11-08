package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/snaga/ast-digger/internal/impact"
)

// HandleExplore handles the "ast_explore" MCP tool call.
// It inspects structural code skeletons without loading full file bodies,
// generating either a directory tree outline or a single-file AST skeleton.
func (s *MCPServer) HandleExplore(ctx context.Context, request mcp.CallToolRequest) (res *mcp.CallToolResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			res = mcp.NewToolResultError(fmt.Sprintf("internal error (panic): %v", r))
			err = nil
		}
	}()

	path, err := request.RequireString("path")
	if err != nil || strings.TrimSpace(path) == "" {
		return mcp.NewToolResultError("missing required parameter: path"), nil
	}

	recursive := request.GetBool("recursive", true)
	limit := request.GetInt("limit", 50)
	full := request.GetBool("full", false)

	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return mcp.NewToolResultError(fmt.Sprintf("path not found: %s", path)), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("failed to inspect path: %v", err)), nil
	}

	if fi.IsDir() {
		tree, err := s.outliner.BuildDirectoryTree(path, recursive, limit)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to build directory tree: %v", err)), nil
		}
		return mcp.NewToolResultText(tree), nil
	}

	skeleton, err := s.outliner.GenerateSkeleton(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to generate skeleton: %v", err)), nil
	}

	if !full {
		skeleton, _ = truncateLines(skeleton, 300)
	}

	return mcp.NewToolResultText(skeleton), nil
}

// HandleSymbol handles the "ast_symbol" MCP tool call.
// It extracts specific class or function implementations, optionally tracing
// dependencies, or locates symbol line ranges without loading full bodies.
func (s *MCPServer) HandleSymbol(ctx context.Context, request mcp.CallToolRequest) (res *mcp.CallToolResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			res = mcp.NewToolResultError(fmt.Sprintf("internal error (panic): %v", r))
			err = nil
		}
	}()

	filePath, err := request.RequireString("file_path")
	if err != nil || strings.TrimSpace(filePath) == "" {
		return mcp.NewToolResultError("missing required parameter: file_path"), nil
	}

	symbol, err := request.RequireString("symbol")
	if err != nil || strings.TrimSpace(symbol) == "" {
		return mcp.NewToolResultError("missing required parameter: symbol"), nil
	}

	followImports := request.GetBool("follow_imports", false)
	locateOnly := request.GetBool("locate_only", false)

	fi, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return mcp.NewToolResultError(fmt.Sprintf("file not found: %s", filePath)), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("failed to inspect file: %v", err)), nil
	}
	if fi.IsDir() {
		return mcp.NewToolResultError(fmt.Sprintf("path is a directory, not a file: %s", filePath)), nil
	}

	if locateOnly {
		resolvedPath, startLine, endLine, err := s.outliner.LocateSymbol(filePath, symbol)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to locate symbol '%s': %v", symbol, err)), nil
		}
		if resolvedPath == "" {
			resolvedPath = filePath
		}
		loc := map[string]any{
			"file_path":  resolvedPath,
			"start_line": startLine,
			"end_line":   endLine,
		}
		jsonBytes, err := json.Marshal(loc)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to format location result: %v", err)), nil
		}
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}

	if followImports {
		symWithDeps, err := s.outliner.ExtractSymbolWithDependencies(filePath, symbol, true)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to extract symbol '%s': %v", symbol, err)), nil
		}

		code := ""
		if symWithDeps.SymbolData.Code != nil {
			code = *symWithDeps.SymbolData.Code
		}

		if len(symWithDeps.Dependencies) > 0 {
			var b strings.Builder
			b.WriteString(code)
			for _, dep := range symWithDeps.Dependencies {
				depPath := ""
				if dep.FilePath != nil {
					depPath = *dep.FilePath
				}
				depCode := ""
				if dep.Code != nil {
					depCode = *dep.Code
				}
				fmt.Fprintf(&b, "\n\n--- Dependency: %s::%s ---\n%s", depPath, dep.Name, depCode)
			}
			code = b.String()
		}

		return mcp.NewToolResultText(code), nil
	}

	symData, err := s.outliner.ExtractSymbol(filePath, symbol)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to extract symbol '%s': %v", symbol, err)), nil
	}

	code := ""
	if symData.Code != nil {
		code = *symData.Code
	}

	return mcp.NewToolResultText(code), nil
}

// HandleTrace handles the "ast_trace" MCP tool call.
// It traces symbol relationships across the codebase using AST precision:
// - action="definition": resolves the origin file and line where an imported symbol is defined.
// - action="references": searches all project-wide references/call sites for a given symbol.
func (s *MCPServer) HandleTrace(ctx context.Context, request mcp.CallToolRequest) (res *mcp.CallToolResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			res = mcp.NewToolResultError(fmt.Sprintf("internal error (panic): %v", r))
			err = nil
		}
	}()

	action, err := request.RequireString("action")
	if err != nil || strings.TrimSpace(action) == "" {
		return mcp.NewToolResultError("missing required parameter: action ('definition' or 'references')"), nil
	}
	action = strings.TrimSpace(action)

	symbol, err := request.RequireString("symbol")
	if err != nil || strings.TrimSpace(symbol) == "" {
		return mcp.NewToolResultError("missing required parameter: symbol"), nil
	}
	symbol = strings.TrimSpace(symbol)

	switch action {
	case "definition":
		filePath, err := request.RequireString("file_path")
		if err != nil || strings.TrimSpace(filePath) == "" {
			return mcp.NewToolResultError("missing required parameter: file_path (required when action='definition')"), nil
		}
		filePath = strings.TrimSpace(filePath)

		fi, err := os.Stat(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				return mcp.NewToolResultError(fmt.Sprintf("file not found: %s", filePath)), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("failed to inspect file: %v", err)), nil
		}
		if fi.IsDir() {
			return mcp.NewToolResultError(fmt.Sprintf("path is a directory, not a file: %s", filePath)), nil
		}

		defFile, line, err := s.outliner.ResolveImports(filePath, symbol)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to resolve import for '%s': %v", symbol, err)), nil
		}
		if defFile == "" || line <= 0 {
			return mcp.NewToolResultError(fmt.Sprintf("definition not found for symbol '%s'", symbol)), nil
		}

		resObj := map[string]any{
			"definition_file": defFile,
			"line":            line,
		}
		jsonBytes, err := json.Marshal(resObj)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to format definition result: %v", err)), nil
		}
		return mcp.NewToolResultText(string(jsonBytes)), nil

	case "references":
		limit := request.GetInt("limit", 30)
		if limit <= 0 {
			limit = 30
		}

		refs, err := s.outliner.FindReferences(".", symbol)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to find references for '%s': %v", symbol, err)), nil
		}

		if len(refs) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("no references found for symbol '%s'", symbol)), nil
		}

		totalCount := len(refs)
		truncated := false
		if totalCount > limit {
			refs = refs[:limit]
			truncated = true
		}

		var b strings.Builder
		for i, r := range refs {
			if i > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "%s:%d: %s", r.FilePath, r.LineNumber, r.LineContent)
		}
		if truncated {
			fmt.Fprintf(&b, "\n# ... (truncated: %d of %d results shown. Increase 'limit' to see more)", limit, totalCount)
		}
		return mcp.NewToolResultText(b.String()), nil

	default:
		return mcp.NewToolResultError(fmt.Sprintf("invalid action '%s': must be 'definition' or 'references'", action)), nil
	}
}

// HandleAudit handles the "ast_audit" MCP tool call.
// It analyzes git diff changes against a base revision, calculating syntax statement
// deltas, cyclomatic complexity changes, docstring coverage, and external documentation advisories.
func (s *MCPServer) HandleAudit(ctx context.Context, request mcp.CallToolRequest) (res *mcp.CallToolResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			res = mcp.NewToolResultError(fmt.Sprintf("internal error (panic): %v", r))
			err = nil
		}
	}()

	base := request.GetString("base", "HEAD")
	if strings.TrimSpace(base) == "" {
		base = "HEAD"
	}
	requireDocBool := request.GetBool("require_doc", true)
	maxComplexity := request.GetInt("max_complexity", 15)

	docReq := impact.DocAuditExported
	if !requireDocBool {
		docReq = impact.DocAuditNone
	}

	gitRoot := findGitRoot(".")
	diffInfos, err := impact.GetGitDiff(gitRoot, base)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get git diff against '%s': %v", base, err)), nil
	}

	var fileImpacts []*impact.FileImpact
	var allSymbols []*impact.SymbolImpact
	modulesMap := make(map[string]bool)

	for _, info := range diffInfos {
		targetPath := info.FilePath
		if targetPath == "" {
			targetPath = info.OldPath
		}
		if targetPath != "" {
			modulesMap[impact.ExtractModule(targetPath)] = true
		}

		fi, err := impact.AnalyzeFileSource(targetPath, "", info.OldContent, info.NewContent)
		if err == nil && fi != nil {
			fileImpacts = append(fileImpacts, fi)
		}

		syms, _, err := impact.AuditFileSymbols(info, docReq)
		if err == nil && syms != nil {
			allSymbols = append(allSymbols, syms...)
		}
	}

	totalAdded := 0
	totalDeleted := 0
	totalComplexityDelta := 0
	for _, fi := range fileImpacts {
		totalAdded += fi.Statements.Added
		totalDeleted += fi.Statements.Deleted
		totalComplexityDelta += fi.ComplexityDelta
	}

	symAdded := 0
	symModified := 0
	symDeleted := 0
	totalPublic := 0
	docPublic := 0
	totalPrivate := 0
	docPrivate := 0
	var missingDocstrings []string

	for _, sym := range allSymbols {
		switch sym.ChangeType {
		case "added":
			symAdded++
		case "modified":
			symModified++
		case "deleted":
			symDeleted++
			continue
		}

		if sym.Visibility == "public" {
			totalPublic++
			if sym.HasDocstring {
				docPublic++
			} else if docReq == impact.DocAuditExported || docReq == impact.DocAuditAll {
				missingDocstrings = append(missingDocstrings, sym.File+"::"+sym.SymbolPath)
			}
		} else {
			totalPrivate++
			if sym.HasDocstring {
				docPrivate++
			} else if docReq == impact.DocAuditAll {
				missingDocstrings = append(missingDocstrings, sym.File+"::"+sym.SymbolPath)
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

	var modulesAffected []string
	for mod := range modulesMap {
		modulesAffected = append(modulesAffected, mod)
	}
	sort.Strings(modulesAffected)

	advisory := impact.BuildDocumentationAdvisory(allSymbols, fileImpacts)
	impactLevel := impact.CalculateImpactLevel(totalAdded+totalDeleted, totalComplexityDelta)

	var b strings.Builder
	fmt.Fprintf(&b, "Change Impact Analysis Summary (base: %s):\n", base)
	fmt.Fprintf(&b, "  Impact Level     : %s\n", impactLevel)
	fmt.Fprintf(&b, "  Statements Delta : +%d / -%d\n", totalAdded, totalDeleted)
	fmt.Fprintf(&b, "  Complexity Delta : %+d\n", totalComplexityDelta)
	fmt.Fprintf(&b, "  Symbols Changed  : +%d added, ~%d modified, -%d deleted\n", symAdded, symModified, symDeleted)
	fmt.Fprintf(&b, "  Files Changed    : %d\n", len(diffInfos))
	if len(modulesAffected) > 0 {
		fmt.Fprintf(&b, "  Modules Affected : %s\n\n", strings.Join(modulesAffected, ", "))
	} else {
		fmt.Fprintf(&b, "  Modules Affected : (none)\n\n")
	}

	fmt.Fprintf(&b, "Impacted Symbols:\n")
	if len(allSymbols) == 0 {
		fmt.Fprintf(&b, "  (none)\n")
	} else {
		for _, sym := range allSymbols {
			sign := "~"
			if sym.ChangeType == "added" {
				sign = "+"
			} else if sym.ChangeType == "deleted" {
				sign = "-"
			}
			docStatus := "docstring present"
			if !sym.HasDocstring {
				docStatus = "no docstring"
			}
			fmt.Fprintf(&b, "  [%s] %s::%s (%s, %s, delta: %d) [%s]\n",
				sign, sym.File, sym.SymbolPath, sym.Kind, sym.Visibility, sym.StatementsDelta, docStatus)
		}
	}
	fmt.Fprintf(&b, "\n")

	if docReq != impact.DocAuditNone {
		fmt.Fprintf(&b, "Documentation Audit:\n")
		fmt.Fprintf(&b, "  Public API Doc Coverage  : %.1f%%\n", pubCoverage*100)
		fmt.Fprintf(&b, "  Private API Doc Coverage : %.1f%%\n", privCoverage*100)
		if len(missingDocstrings) > 0 {
			fmt.Fprintf(&b, "  Missing Docstrings:\n")
			for _, m := range missingDocstrings {
				fmt.Fprintf(&b, "    - %s\n", m)
			}
		}
		fmt.Fprintf(&b, "\n")
	}

	// Complexity warnings if threshold is exceeded
	var complexityWarnings []string
	if maxComplexity > 0 {
		for _, fi := range fileImpacts {
			if fi.ComplexityDelta > maxComplexity {
				complexityWarnings = append(complexityWarnings, fmt.Sprintf("File %s complexity delta (+%d) exceeds threshold (%d)", fi.FilePath, fi.ComplexityDelta, maxComplexity))
			}
		}
	}
	if len(complexityWarnings) > 0 {
		fmt.Fprintf(&b, "Complexity Warnings:\n")
		for _, w := range complexityWarnings {
			fmt.Fprintf(&b, "  ! %s\n", w)
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "Documentation Advisory:\n")
	if advisory != nil && advisory.RequiresIntentReview {
		fmt.Fprintf(&b, "  Requires Intent Review: Yes (%s)\n", advisory.Reason)
		for _, scope := range advisory.AffectedScopes {
			fmt.Fprintf(&b, "  * [%s] (%s): %s\n", scope.Scope, scope.Category, scope.Description)
			fmt.Fprintf(&b, "    Action: %s\n", scope.PromptAction)
		}
	} else {
		fmt.Fprintf(&b, "  Requires Intent Review: No\n")
	}

	return mcp.NewToolResultText(strings.TrimRight(b.String(), "\n")), nil
}

// truncateLines truncates text if it exceeds limit lines, retaining the first 100
// and last 100 lines with an informational notice in the middle.
func truncateLines(text string, limit int) (string, bool) {
	lines := splitLines(text)
	if len(lines) <= limit {
		return text, false
	}

	truncatedCount := len(lines) - 200
	topLines := lines[:100]
	bottomLines := lines[len(lines)-100:]

	middle := []string{
		fmt.Sprintf("    # === TRUNCATED (%d lines omitted) ===", truncatedCount),
		"    # === Pass full=true to print full output ===",
	}

	result := make([]string, 0, len(topLines)+len(middle)+len(bottomLines))
	result = append(result, topLines...)
	result = append(result, middle...)
	result = append(result, bottomLines...)

	return strings.Join(result, "\n"), true
}

// splitLines splits text into lines, handling both CRLF and LF line endings
// without producing empty elements for trailing newlines.
func splitLines(text string) []string {
	if text == "" {
		return []string{}
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return []string{""}
	}
	return strings.Split(text, "\n")
}

// findGitRoot traverses upwards from startDir until a directory containing ".git" is found,
// or returns startDir if not found.
func findGitRoot(startDir string) string {
	curr, err := filepath.Abs(startDir)
	if err != nil {
		return startDir
	}
	for {
		gitPath := filepath.Join(curr, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			return curr
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}
	return startDir
}
