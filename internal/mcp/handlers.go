package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
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
