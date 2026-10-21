package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snaga/ast-digger/internal/apperr"
)

// setupTestRepo initializes a temporary Git repository with user config and autocrlf disabled.
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

func execGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git command %v failed: %v, output: %s", args, err, string(out))
	}
}

func TestChangeImpactCmd_TextOutput(t *testing.T) {
	repoDir := setupTestRepo(t)
	chdirTest(t, repoDir)

	// 1. Initial commit
	initialFile := filepath.Join(repoDir, "calc.go")
	content1 := `package main

// Add returns the sum of two integers.
func Add(a, b int) int {
	return a + b
}
`
	if err := os.WriteFile(initialFile, []byte(content1), 0644); err != nil {
		t.Fatal(err)
	}
	execGit(t, repoDir, "add", "calc.go")
	execGit(t, repoDir, "commit", "-m", "initial commit")

	// 2. Modify calc.go and add new function
	content2 := `package main

// Add returns the sum of two integers.
func Add(a, b int) int {
	res := a + b
	return res
}

// Multiply returns the product of two integers.
func Multiply(a, b int) int {
	return a * b
}
`
	if err := os.WriteFile(initialFile, []byte(content2), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. Run change-impact command in text mode
	res := runCLI(t, "change-impact")
	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want %d; stderr: %s", res.ExitCode, apperr.ExitOK, res.Stderr)
	}

	wantSubstrings := []string{
		"Change Impact Analysis Summary:",
		"Impact Level",
		"Statements Delta",
		"Complexity Delta",
		"Symbols Changed",
		"Files Changed    : 1",
		"Impacted Symbols:",
		"Add",
		"Multiply",
		"Documentation Audit:",
		"Public API Doc Coverage",
		"Documentation Advisory:",
	}

	for _, sub := range wantSubstrings {
		if !strings.Contains(res.Stdout, sub) {
			t.Errorf("stdout does not contain %q\nstdout:\n%s", sub, res.Stdout)
		}
	}
}

func TestChangeImpactCmd_JSONOutput(t *testing.T) {
	repoDir := setupTestRepo(t)
	chdirTest(t, repoDir)

	// 1. Initial commit
	initialFile := filepath.Join(repoDir, "service.go")
	content1 := `package main

// Service represents a sample service.
type Service struct{}

// Run starts the service.
func (s *Service) Run() error {
	return nil
}
`
	if err := os.WriteFile(initialFile, []byte(content1), 0644); err != nil {
		t.Fatal(err)
	}
	execGit(t, repoDir, "add", "service.go")
	execGit(t, repoDir, "commit", "-m", "initial commit")

	// 2. Modify service.go
	content2 := `package main

// Service represents a sample service.
type Service struct{}

// Run starts the service.
func (s *Service) Run() error {
	started := true
	if started {
		return nil
	}
	return nil
}

// Stop stops the service.
func (s *Service) Stop() {
}
`
	if err := os.WriteFile(initialFile, []byte(content2), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. Run change-impact --json
	res := runCLI(t, "change-impact", "--json")
	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want %d; stderr: %s", res.ExitCode, apperr.ExitOK, res.Stderr)
	}

	var report ChangeImpactReport
	if err := json.Unmarshal([]byte(res.Stdout), &report); err != nil {
		t.Fatalf("failed to parse stdout as ChangeImpactReport JSON: %v\nstdout:\n%s", err, res.Stdout)
	}

	if report.Summary.FilesChanged != 1 {
		t.Errorf("Summary.FilesChanged = %d, want 1", report.Summary.FilesChanged)
	}
	if report.Summary.StatementsAdded <= 0 {
		t.Errorf("Summary.StatementsAdded = %d, want > 0", report.Summary.StatementsAdded)
	}
	if len(report.Symbols) == 0 {
		t.Errorf("Symbols is empty, want at least 1 symbol")
	}
	if report.DocumentationAdvisory == nil {
		t.Errorf("DocumentationAdvisory is nil")
	}
}

func TestChangeImpactCmd_MaxStatementsGuardrail(t *testing.T) {
	repoDir := setupTestRepo(t)
	chdirTest(t, repoDir)

	// 1. Initial commit
	initialFile := filepath.Join(repoDir, "heavy.go")
	content1 := `package main

func DoWork() {
}
`
	if err := os.WriteFile(initialFile, []byte(content1), 0644); err != nil {
		t.Fatal(err)
	}
	execGit(t, repoDir, "add", "heavy.go")
	execGit(t, repoDir, "commit", "-m", "initial commit")

	// 2. Add multiple statements
	content2 := `package main

func DoWork() {
	a := 1
	b := 2
	c := a + b
	d := c * 2
	_ = d
}
`
	if err := os.WriteFile(initialFile, []byte(content2), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. Run with --max-statements=2 (should fail with exit code 2)
	res := runCLI(t, "change-impact", "--max-statements=2")
	if res.ExitCode != apperr.ExitCLIParamError {
		t.Fatalf("ExitCode = %d, want %d (ExitCLIParamError); stderr: %s", res.ExitCode, apperr.ExitCLIParamError, res.Stderr)
	}

	if !strings.Contains(res.Stderr, "guardrail check failed") || !strings.Contains(res.Stderr, "exceeded maximum allowed") {
		t.Errorf("stderr does not mention guardrail violation; stderr: %q", res.Stderr)
	}

	// 4. Run with --max-statements=100 (should pass)
	resPass := runCLI(t, "change-impact", "--max-statements=100")
	if resPass.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want %d; stderr: %s", resPass.ExitCode, apperr.ExitOK, resPass.Stderr)
	}
}

func TestChangeImpactCmd_FailOnMissingDocGuardrail(t *testing.T) {
	repoDir := setupTestRepo(t)
	chdirTest(t, repoDir)

	// 1. Initial commit
	initialFile := filepath.Join(repoDir, "api.go")
	content1 := `package main

// ExistingAPI has docstring
func ExistingAPI() {}
`
	if err := os.WriteFile(initialFile, []byte(content1), 0644); err != nil {
		t.Fatal(err)
	}
	execGit(t, repoDir, "add", "api.go")
	execGit(t, repoDir, "commit", "-m", "initial commit")

	// 2. Add new exported API without docstring
	content2 := `package main

// ExistingAPI has docstring
func ExistingAPI() {}

func UndocumentedAPI() {
	println("no doc")
}
`
	if err := os.WriteFile(initialFile, []byte(content2), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. Without --fail-on-missing-doc, it should succeed with exit code 0
	resDefault := runCLI(t, "change-impact")
	if resDefault.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr: %s", resDefault.ExitCode, resDefault.Stderr)
	}

	// 4. With --fail-on-missing-doc, it should fail with exit code 2
	resFail := runCLI(t, "change-impact", "--fail-on-missing-doc")
	if resFail.ExitCode != apperr.ExitCLIParamError {
		t.Fatalf("ExitCode = %d, want %d (ExitCLIParamError); stderr: %s", resFail.ExitCode, apperr.ExitCLIParamError, resFail.Stderr)
	}

	if !strings.Contains(resFail.Stderr, "missing required docstring") {
		t.Errorf("stderr does not mention missing docstrings; stderr: %q", resFail.Stderr)
	}

	// 5. If --require-doc=none, then --fail-on-missing-doc should pass
	resNone := runCLI(t, "change-impact", "--fail-on-missing-doc", "--require-doc=none")
	if resNone.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0 when require-doc=none; stderr: %s", resNone.ExitCode, resNone.Stderr)
	}
}

func TestChangeImpactCmd_InvalidRequireDoc(t *testing.T) {
	res := runCLI(t, "change-impact", "--require-doc=unknown_mode")
	if res.ExitCode != apperr.ExitCLIParamError {
		t.Fatalf("ExitCode = %d, want %d; stderr: %s", res.ExitCode, apperr.ExitCLIParamError, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --require-doc value") {
		t.Errorf("stderr does not mention invalid --require-doc; stderr: %q", res.Stderr)
	}
}

func TestChangeImpactCmd_NoDiff(t *testing.T) {
	repoDir := setupTestRepo(t)
	chdirTest(t, repoDir)

	initialFile := filepath.Join(repoDir, "main.go")
	content := "package main\n"
	if err := os.WriteFile(initialFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	execGit(t, repoDir, "add", "main.go")
	execGit(t, repoDir, "commit", "-m", "initial commit")

	res := runCLI(t, "change-impact")
	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr: %s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "Files Changed    : 0") {
		t.Errorf("expected 0 files changed, got: %s", res.Stdout)
	}
}
