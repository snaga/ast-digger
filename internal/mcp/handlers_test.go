package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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

// TestMCPServer_HandleTrace_Definition_Success verifies that HandleTrace resolves the origin file
// and line number of an imported symbol.
func TestMCPServer_HandleTrace_Definition_Success(t *testing.T) {
	chdirTest(t, "../../testdata/python/importproj")

	outliner := outline.NewOutliner()
	server := NewServer("test-server", "1.0.0", outliner)
	ctx := context.Background()

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameTrace,
			Arguments: map[string]any{
				"action":    "definition",
				"symbol":    "add",
				"file_path": "main.py",
			},
		},
	}

	res, err := server.HandleTrace(ctx, req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", extractResultText(t, res))
	}

	text := extractResultText(t, res)
	var payload struct {
		DefinitionFile string `json:"definition_file"`
		Line           int    `json:"line"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v, text: %s", err, text)
	}
	if payload.DefinitionFile != "mathutils.py" || payload.Line != 1 {
		t.Errorf("expected mathutils.py:1, got %s:%d", payload.DefinitionFile, payload.Line)
	}
}

// TestMCPServer_HandleTrace_Definition_NotFound verifies that HandleTrace returns an error
// when the requested symbol is not imported by the file.
func TestMCPServer_HandleTrace_Definition_NotFound(t *testing.T) {
	chdirTest(t, "../../testdata/python/importproj")

	outliner := outline.NewOutliner()
	server := NewServer("test-server", "1.0.0", outliner)
	ctx := context.Background()

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameTrace,
			Arguments: map[string]any{
				"action":    "definition",
				"symbol":    "nonexistent_func",
				"file_path": "main.py",
			},
		},
	}

	res, err := server.HandleTrace(ctx, req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error result for nonexistent symbol")
	}
}

// TestMCPServer_HandleTrace_Definition_FileNotFound verifies that HandleTrace reports an error
// if file_path does not exist.
func TestMCPServer_HandleTrace_Definition_FileNotFound(t *testing.T) {
	chdirTest(t, "../../testdata/python/importproj")

	outliner := outline.NewOutliner()
	server := NewServer("test-server", "1.0.0", outliner)
	ctx := context.Background()

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameTrace,
			Arguments: map[string]any{
				"action":    "definition",
				"symbol":    "add",
				"file_path": "does_not_exist.py",
			},
		},
	}

	res, err := server.HandleTrace(ctx, req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error result for nonexistent file")
	}
	text := extractResultText(t, res)
	if !strings.Contains(text, "file not found") {
		t.Errorf("expected 'file not found' error, got: %s", text)
	}
}

// TestMCPServer_HandleTrace_References_Success verifies that HandleTrace searches all project-wide references.
func TestMCPServer_HandleTrace_References_Success(t *testing.T) {
	chdirTest(t, "../../testdata/python/references")

	outliner := outline.NewOutliner()
	server := NewServer("test-server", "1.0.0", outliner)
	ctx := context.Background()

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameTrace,
			Arguments: map[string]any{
				"action": "references",
				"symbol": "target_func",
				"limit":  30,
			},
		},
	}

	res, err := server.HandleTrace(ctx, req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", extractResultText(t, res))
	}

	text := extractResultText(t, res)
	if !strings.Contains(text, "caller_a.py") || !strings.Contains(text, "caller_b.py") {
		t.Errorf("expected references in caller_a.py and caller_b.py, got:\n%s", text)
	}
}

// TestMCPServer_HandleTrace_References_LimitTruncated verifies that HandleTrace truncates results
// and appends a hint when matches exceed limit.
func TestMCPServer_HandleTrace_References_LimitTruncated(t *testing.T) {
	chdirTest(t, "../../testdata/python/references")

	outliner := outline.NewOutliner()
	server := NewServer("test-server", "1.0.0", outliner)
	ctx := context.Background()

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameTrace,
			Arguments: map[string]any{
				"action": "references",
				"symbol": "target_func",
				"limit":  1,
			},
		},
	}

	res, err := server.HandleTrace(ctx, req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", extractResultText(t, res))
	}

	text := extractResultText(t, res)
	if !strings.Contains(text, "truncated: 1 of 2 results shown") {
		t.Errorf("expected truncation hint in output, got:\n%s", text)
	}
}

// TestMCPServer_HandleTrace_References_NoMatches verifies that HandleTrace gracefully returns a notice
// when no references are found.
func TestMCPServer_HandleTrace_References_NoMatches(t *testing.T) {
	chdirTest(t, "../../testdata/python/references")

	outliner := outline.NewOutliner()
	server := NewServer("test-server", "1.0.0", outliner)
	ctx := context.Background()

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameTrace,
			Arguments: map[string]any{
				"action": "references",
				"symbol": "unreferenced_symbol_12345",
			},
		},
	}

	res, err := server.HandleTrace(ctx, req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected non-error result, got error: %s", extractResultText(t, res))
	}

	text := extractResultText(t, res)
	if !strings.Contains(text, "no references found") {
		t.Errorf("expected 'no references found', got: %s", text)
	}
}

// TestMCPServer_HandleTrace_ValidationErrors verifies parameter validation in HandleTrace.
func TestMCPServer_HandleTrace_ValidationErrors(t *testing.T) {
	server := NewServer("test-server", "1.0.0", outline.NewOutliner())
	ctx := context.Background()

	// Missing action
	res, err := server.HandleTrace(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameTrace,
			Arguments: map[string]any{
				"symbol": "foo",
			},
		},
	})
	if err != nil || !res.IsError {
		t.Errorf("expected error for missing action")
	}

	// Invalid action
	res, err = server.HandleTrace(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameTrace,
			Arguments: map[string]any{
				"action": "unknown_action",
				"symbol": "foo",
			},
		},
	})
	if err != nil || !res.IsError {
		t.Errorf("expected error for invalid action")
	}

	// Missing symbol
	res, err = server.HandleTrace(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameTrace,
			Arguments: map[string]any{
				"action": "references",
			},
		},
	})
	if err != nil || !res.IsError {
		t.Errorf("expected error for missing symbol")
	}

	// Missing file_path in definition mode
	res, err = server.HandleTrace(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameTrace,
			Arguments: map[string]any{
				"action": "definition",
				"symbol": "foo",
			},
		},
	})
	if err != nil || !res.IsError {
		t.Errorf("expected error for missing file_path in definition mode")
	}
}

// setupTestRepo creates a temporary git repository with initial git configuration for testing HandleAudit.
func setupTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	commands := [][]string{
		{"git", "init"},
		{"git", "config", "user.name", "Test User"},
		{"git", "config", "user.email", "test@example.com"},
		{"git", "config", "core.autocrlf", "false"},
	}

	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup git command %v failed: %v, output: %s", args, err, string(out))
		}
	}

	return dir
}

// execGit runs a git command within dir.
func execGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v, output: %s", args, err, string(out))
	}
	return string(out)
}

// TestMCPServer_HandleAudit_Success verifies that HandleAudit inspects git changes, calculates
// statement and complexity metrics, and outputs a formatted visual summary.
func TestMCPServer_HandleAudit_Success(t *testing.T) {
	repoDir := setupTestRepo(t)
	chdirTest(t, repoDir)

	// Create initial file and commit
	initialFile := filepath.Join(repoDir, "calc.py")
	initialContent := "def add(a, b):\n    '''Add two numbers.'''\n    return a + b\n"
	if err := os.WriteFile(initialFile, []byte(initialContent), 0644); err != nil {
		t.Fatal(err)
	}
	execGit(t, repoDir, "add", "calc.py")
	execGit(t, repoDir, "commit", "-m", "initial commit")

	// Modify file in working tree: add multiply function without docstring
	modifiedContent := initialContent + "\ndef multiply(a, b):\n    if a > 0:\n        return a * b\n    return 0\n"
	if err := os.WriteFile(initialFile, []byte(modifiedContent), 0644); err != nil {
		t.Fatal(err)
	}

	server := NewServer("test-server", "1.0.0", outline.NewOutliner())
	ctx := context.Background()

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameAudit,
			Arguments: map[string]any{
				"base":           "HEAD",
				"require_doc":    true,
				"max_complexity": 1,
			},
		},
	}

	res, err := server.HandleAudit(ctx, req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", extractResultText(t, res))
	}

	text := extractResultText(t, res)
	if !strings.Contains(text, "Change Impact Analysis Summary") {
		t.Errorf("expected summary section in output, got:\n%s", text)
	}
	if !strings.Contains(text, "Impacted Symbols:") {
		t.Errorf("expected impacted symbols section, got:\n%s", text)
	}
	if !strings.Contains(text, "Documentation Audit:") {
		t.Errorf("expected doc audit section, got:\n%s", text)
	}
	if !strings.Contains(text, "multiply") {
		t.Errorf("expected 'multiply' in output, got:\n%s", text)
	}
}

// TestMCPServer_HandleAudit_NoDocRequirement verifies that HandleAudit respects require_doc=false.
func TestMCPServer_HandleAudit_NoDocRequirement(t *testing.T) {
	repoDir := setupTestRepo(t)
	chdirTest(t, repoDir)

	initialFile := filepath.Join(repoDir, "calc.py")
	initialContent := "def add(a, b):\n    return a + b\n"
	if err := os.WriteFile(initialFile, []byte(initialContent), 0644); err != nil {
		t.Fatal(err)
	}
	execGit(t, repoDir, "add", "calc.py")
	execGit(t, repoDir, "commit", "-m", "initial commit")

	server := NewServer("test-server", "1.0.0", outline.NewOutliner())
	ctx := context.Background()

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameAudit,
			Arguments: map[string]any{
				"base":        "HEAD",
				"require_doc": false,
			},
		},
	}

	res, err := server.HandleAudit(ctx, req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", extractResultText(t, res))
	}

	text := extractResultText(t, res)
	if strings.Contains(text, "Documentation Audit:") {
		t.Errorf("expected Documentation Audit to be omitted when require_doc=false, got:\n%s", text)
	}
}

// TestMCPServer_HandleAudit_InvalidBase verifies that HandleAudit returns a safe error result
// if an invalid base git reference is provided.
func TestMCPServer_HandleAudit_InvalidBase(t *testing.T) {
	repoDir := setupTestRepo(t)
	chdirTest(t, repoDir)

	server := NewServer("test-server", "1.0.0", outline.NewOutliner())
	ctx := context.Background()

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: ToolNameAudit,
			Arguments: map[string]any{
				"base": "nonexistent_ref_12345",
			},
		},
	}

	res, err := server.HandleAudit(ctx, req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error result for invalid base revision")
	}
}
