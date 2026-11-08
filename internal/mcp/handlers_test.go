package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/snaga/ast-digger/internal/outline"
)

const (
	testPythonSample = "../../testdata/python/sample.py"
	testPythonDir    = "../../testdata/python"
)

// chdirTest temporarily changes the working directory to dir for the duration of the test.
func chdirTest(t *testing.T, dir string) {
	t.Helper()

	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("failed to resolve %q: %v", dir, err)
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	if err := os.Chdir(abs); err != nil {
		t.Fatalf("failed to chdir to %q: %v", abs, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("failed to restore working directory to %q: %v", orig, err)
		}
	})
}

// extractResultText extracts the plain text from the first TextContent item in res.
func extractResultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatalf("expected at least one content item in CallToolResult, got 0")
	}
	tc, ok := mcp.AsTextContent(res.Content[0])
	if !ok {
		t.Fatalf("first content item is not TextContent: %T", res.Content[0])
	}
	return tc.Text
}

// TestMCPServer_HandleExplore_Directory verifies that HandleExplore returns a directory tree structure
// for directory paths.
func TestMCPServer_HandleExplore_Directory(t *testing.T) {
	chdirTest(t, "../..")

	outliner := outline.NewOutliner()
	server := NewServer("test-server", "1.0.0", outliner)
	ctx := context.Background()

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameExplore,
			Arguments: map[string]any{
				"path":      "testdata/python",
				"recursive": false,
				"limit":     20,
			},
		},
	}

	res, err := server.HandleExplore(ctx, req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error result: %s", extractResultText(t, res))
	}

	text := extractResultText(t, res)
	if !strings.Contains(text, "sample.py") {
		t.Errorf("expected directory tree to contain 'sample.py', got:\n%s", text)
	}
}

// TestMCPServer_HandleExplore_File verifies that HandleExplore returns an AST skeleton
// for single file paths.
func TestMCPServer_HandleExplore_File(t *testing.T) {
	outliner := outline.NewOutliner()
	server := NewServer("test-server", "1.0.0", outliner)
	ctx := context.Background()

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameExplore,
			Arguments: map[string]any{
				"path": testPythonSample,
				"full": true,
			},
		},
	}

	res, err := server.HandleExplore(ctx, req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error result: %s", extractResultText(t, res))
	}

	text := extractResultText(t, res)
	if !strings.Contains(text, "class Greeter") {
		t.Errorf("expected skeleton to contain 'class Greeter', got:\n%s", text)
	}
}

// TestMCPServer_HandleExplore_NotFound verifies that HandleExplore reports an error
// when the specified path does not exist.
func TestMCPServer_HandleExplore_NotFound(t *testing.T) {
	outliner := outline.NewOutliner()
	server := NewServer("test-server", "1.0.0", outliner)
	ctx := context.Background()

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameExplore,
			Arguments: map[string]any{
				"path": "../../testdata/nonexistent_path_xyz",
			},
		},
	}

	res, err := server.HandleExplore(ctx, req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error result for nonexistent path, got success: %s", extractResultText(t, res))
	}
	text := extractResultText(t, res)
	if !strings.Contains(text, "path not found") {
		t.Errorf("expected error message to contain 'path not found', got: %s", text)
	}
}

// TestMCPServer_HandleExplore_MissingPath verifies that HandleExplore rejects requests
// with a missing required path parameter.
func TestMCPServer_HandleExplore_MissingPath(t *testing.T) {
	outliner := outline.NewOutliner()
	server := NewServer("test-server", "1.0.0", outliner)
	ctx := context.Background()

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      ToolNameExplore,
			Arguments: map[string]any{},
		},
	}

	res, err := server.HandleExplore(ctx, req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error result for missing path argument, got success")
	}
	text := extractResultText(t, res)
	if !strings.Contains(text, "missing required parameter") {
		t.Errorf("expected error message to mention missing parameter, got: %s", text)
	}
}

// TestMCPServer_HandleSymbol_ExtractSuccess verifies that HandleSymbol successfully extracts
// targeted symbol implementation code.
func TestMCPServer_HandleSymbol_ExtractSuccess(t *testing.T) {
	outliner := outline.NewOutliner()
	server := NewServer("test-server", "1.0.0", outliner)
	ctx := context.Background()

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameSymbol,
			Arguments: map[string]any{
				"file_path": testPythonSample,
				"symbol":    "Greeter",
			},
		},
	}

	res, err := server.HandleSymbol(ctx, req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error result: %s", extractResultText(t, res))
	}

	code := extractResultText(t, res)
	if !strings.Contains(code, "class Greeter") {
		t.Errorf("expected extracted code to contain 'class Greeter', got:\n%s", code)
	}
}

// TestMCPServer_HandleSymbol_LocateOnly verifies that HandleSymbol returns start and end line ranges
// formatted as JSON when locate_only is enabled.
func TestMCPServer_HandleSymbol_LocateOnly(t *testing.T) {
	outliner := outline.NewOutliner()
	server := NewServer("test-server", "1.0.0", outliner)
	ctx := context.Background()

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameSymbol,
			Arguments: map[string]any{
				"file_path":   testPythonSample,
				"symbol":      "Greeter",
				"locate_only": true,
			},
		},
	}

	res, err := server.HandleSymbol(ctx, req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error result: %s", extractResultText(t, res))
	}

	text := extractResultText(t, res)
	var loc map[string]any
	if err := json.Unmarshal([]byte(text), &loc); err != nil {
		t.Fatalf("failed to parse locate result as JSON: %v, raw text: %s", err, text)
	}

	if loc["file_path"] == "" {
		t.Errorf("expected non-empty file_path in locate JSON: %v", loc)
	}
	startLine, ok1 := loc["start_line"].(float64)
	endLine, ok2 := loc["end_line"].(float64)
	if !ok1 || !ok2 || startLine <= 0 || endLine < startLine {
		t.Errorf("invalid line range in locate JSON: start_line=%v, end_line=%v", loc["start_line"], loc["end_line"])
	}
}

// TestMCPServer_HandleSymbol_FollowImports verifies that HandleSymbol traces imported dependencies
// when follow_imports is enabled.
func TestMCPServer_HandleSymbol_FollowImports(t *testing.T) {
	outliner := outline.NewOutliner()
	server := NewServer("test-server", "1.0.0", outliner)
	ctx := context.Background()

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameSymbol,
			Arguments: map[string]any{
				"file_path":      testPythonSample,
				"symbol":         "Greeter",
				"follow_imports": true,
			},
		},
	}

	res, err := server.HandleSymbol(ctx, req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error result: %s", extractResultText(t, res))
	}
	code := extractResultText(t, res)
	if !strings.Contains(code, "class Greeter") {
		t.Errorf("expected code to contain 'class Greeter', got:\n%s", code)
	}
}

// TestMCPServer_HandleSymbol_NotFound verifies that HandleSymbol reports an error
// when the requested symbol is not found in the file.
func TestMCPServer_HandleSymbol_NotFound(t *testing.T) {
	outliner := outline.NewOutliner()
	server := NewServer("test-server", "1.0.0", outliner)
	ctx := context.Background()

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameSymbol,
			Arguments: map[string]any{
				"file_path": testPythonSample,
				"symbol":    "NoSuchSymbol123",
			},
		},
	}

	res, err := server.HandleSymbol(ctx, req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error result for nonexistent symbol, got success: %s", extractResultText(t, res))
	}
}

// TestMCPServer_HandleSymbol_FileNotFound verifies that HandleSymbol reports an error
// when the target source file does not exist.
func TestMCPServer_HandleSymbol_FileNotFound(t *testing.T) {
	outliner := outline.NewOutliner()
	server := NewServer("test-server", "1.0.0", outliner)
	ctx := context.Background()

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameSymbol,
			Arguments: map[string]any{
				"file_path": "../../testdata/python/nonexistent_file.py",
				"symbol":    "Greeter",
			},
		},
	}

	res, err := server.HandleSymbol(ctx, req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error result for nonexistent file, got success")
	}
	text := extractResultText(t, res)
	if !strings.Contains(text, "file not found") {
		t.Errorf("expected 'file not found' message, got: %s", text)
	}
}

// TestMCPServer_HandleSymbol_DirectoryError verifies that HandleSymbol rejects directories
// passed as file_path.
func TestMCPServer_HandleSymbol_DirectoryError(t *testing.T) {
	outliner := outline.NewOutliner()
	server := NewServer("test-server", "1.0.0", outliner)
	ctx := context.Background()

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameSymbol,
			Arguments: map[string]any{
				"file_path": testPythonDir,
				"symbol":    "Greeter",
			},
		},
	}

	res, err := server.HandleSymbol(ctx, req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error result when passing directory to HandleSymbol, got success")
	}
	text := extractResultText(t, res)
	if !strings.Contains(text, "directory") {
		t.Errorf("expected error message to mention directory, got: %s", text)
	}
}

// TestMCPServer_HandleSymbol_MissingParams verifies that HandleSymbol validates
// both file_path and symbol presence.
func TestMCPServer_HandleSymbol_MissingParams(t *testing.T) {
	outliner := outline.NewOutliner()
	server := NewServer("test-server", "1.0.0", outliner)
	ctx := context.Background()

	// Missing file_path
	req1 := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameSymbol,
			Arguments: map[string]any{
				"symbol": "Greeter",
			},
		},
	}
	res1, err := server.HandleSymbol(ctx, req1)
	if err != nil || !res1.IsError {
		t.Errorf("expected error for missing file_path")
	}

	// Missing symbol
	req2 := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameSymbol,
			Arguments: map[string]any{
				"file_path": testPythonSample,
			},
		},
	}
	res2, err := server.HandleSymbol(ctx, req2)
	if err != nil || !res2.IsError {
		t.Errorf("expected error for missing symbol")
	}
}

// TestTruncateLines verifies that truncateLines preserves short content and truncates long text.
func TestTruncateLines(t *testing.T) {
	// Under limit
	shortText := "line 1\nline 2\nline 3"
	got, truncated := truncateLines(shortText, 5)
	if truncated || got != shortText {
		t.Errorf("expected unchanged text and false, got truncated=%v, text=%s", truncated, got)
	}

	// Over limit
	lines := make([]string, 250)
	for i := 0; i < 250; i++ {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	longText := strings.Join(lines, "\n")
	got, truncated = truncateLines(longText, 200)
	if !truncated {
		t.Fatalf("expected truncated to be true")
	}
	if !strings.Contains(got, "TRUNCATED (50 lines omitted)") {
		t.Errorf("expected truncation notice with 50 lines omitted, got:\n%s", got)
	}
}
