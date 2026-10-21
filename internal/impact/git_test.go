package impact

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// setupTestGitRepo initializes a temporary Git repository with user config and autocrlf disabled.
func setupTestGitRepo(t *testing.T) string {
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

func runCmd(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("command %s %v failed: %v, output: %s", name, args, err, string(out))
	}
}

func TestGetGitDiff(t *testing.T) {
	repoDir := setupTestGitRepo(t)

	// 1. Initial commit files
	initialGo := `package main

func Hello() {
	println("hello")
}

func OldFunction() {
	println("to be deleted")
}
`
	toDeleteGo := `package main

func Bye() {
	println("bye")
}
`
	subdir := filepath.Join(repoDir, "pkg", "sub")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}
	subInitialGo := `package sub

var Initial = 1
`
	if err := os.WriteFile(filepath.Join(repoDir, "initial.go"), []byte(initialGo), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "to_delete.go"), []byte(toDeleteGo), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "sub.go"), []byte(subInitialGo), 0644); err != nil {
		t.Fatal(err)
	}

	runCmd(t, repoDir, "git", "add", ".")
	runCmd(t, repoDir, "git", "commit", "-m", "initial commit")

	// Verify no diff right after commit
	diffs, err := GetGitDiff(repoDir, "")
	if err != nil {
		t.Fatalf("unexpected error on clean repo: %v", err)
	}
	if len(diffs) != 0 {
		t.Fatalf("expected 0 diffs on clean repo, got %d", len(diffs))
	}

	// 2. Make changes
	// (a) Modify initial.go (unstaged)
	modifiedGo := `package main

func Hello() {
	println("hello world")
	println("new statement")
}

func NewFunction() {
	println("new func")
}
`
	if err := os.WriteFile(filepath.Join(repoDir, "initial.go"), []byte(modifiedGo), 0644); err != nil {
		t.Fatal(err)
	}

	// (b) Delete to_delete.go (unstaged)
	if err := os.Remove(filepath.Join(repoDir, "to_delete.go")); err != nil {
		t.Fatal(err)
	}

	// (c) Add staged_new.go (staged)
	stagedNewGo := `package main

func Staged() {}
`
	if err := os.WriteFile(filepath.Join(repoDir, "staged_new.go"), []byte(stagedNewGo), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, repoDir, "git", "add", "staged_new.go")

	// (d) Add untracked.go in sub directory (untracked)
	untrackedGo := `package sub

func Untracked() {
	println("untracked")
}
`
	if err := os.WriteFile(filepath.Join(subdir, "untracked.go"), []byte(untrackedGo), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. Run GetGitDiff with baseRef "HEAD"
	diffs, err = GetGitDiff(repoDir, "HEAD")
	if err != nil {
		t.Fatalf("GetGitDiff failed: %v", err)
	}

	if len(diffs) != 4 {
		t.Fatalf("expected 4 diffs, got %d", len(diffs))
	}

	diffMap := make(map[string]*FileDiffInfo)
	for _, d := range diffs {
		diffMap[d.FilePath] = d
	}

	// Check initial.go
	if d, ok := diffMap["initial.go"]; !ok {
		t.Errorf("missing diff for initial.go")
	} else {
		if d.IsNew {
			t.Errorf("initial.go should not be marked IsNew")
		}
		if d.IsDeleted {
			t.Errorf("initial.go should not be marked IsDeleted")
		}
		if !bytes.Equal(d.OldContent, []byte(initialGo)) {
			t.Errorf("initial.go OldContent mismatch: got %q, want %q", string(d.OldContent), initialGo)
		}
		if !bytes.Equal(d.NewContent, []byte(modifiedGo)) {
			t.Errorf("initial.go NewContent mismatch: got %q, want %q", string(d.NewContent), modifiedGo)
		}
		if len(d.AddedLines) == 0 {
			t.Errorf("initial.go expected added lines, got empty")
		}
		if len(d.DeletedLines) == 0 {
			t.Errorf("initial.go expected deleted lines, got empty")
		}
		// Added lines should be [4, 5, 8, 9] in modifiedGo
		expectedAdded := []int{4, 5, 8, 9}
		if !reflect.DeepEqual(d.AddedLines, expectedAdded) {
			t.Errorf("initial.go AddedLines = %v, want %v", d.AddedLines, expectedAdded)
		}
		// Deleted lines should be [4, 7, 8] in initialGo (the closing brace '}' on line 9 is common context)
		expectedDeleted := []int{4, 7, 8}
		if !reflect.DeepEqual(d.DeletedLines, expectedDeleted) {
			t.Errorf("initial.go DeletedLines = %v, want %v", d.DeletedLines, expectedDeleted)
		}
	}

	// Check to_delete.go
	if d, ok := diffMap["to_delete.go"]; !ok {
		t.Errorf("missing diff for to_delete.go")
	} else {
		if !d.IsDeleted {
			t.Errorf("to_delete.go should be marked IsDeleted")
		}
		if d.IsNew {
			t.Errorf("to_delete.go should not be marked IsNew")
		}
		if d.NewContent != nil {
			t.Errorf("to_delete.go NewContent should be nil, got %q", string(d.NewContent))
		}
		if !bytes.Equal(d.OldContent, []byte(toDeleteGo)) {
			t.Errorf("to_delete.go OldContent mismatch: got %q, want %q", string(d.OldContent), toDeleteGo)
		}
		if len(d.AddedLines) != 0 {
			t.Errorf("to_delete.go AddedLines should be empty, got %v", d.AddedLines)
		}
		if len(d.DeletedLines) != 5 {
			t.Errorf("to_delete.go DeletedLines should have 5 lines, got %v", d.DeletedLines)
		}
	}

	// Check staged_new.go
	if d, ok := diffMap["staged_new.go"]; !ok {
		t.Errorf("missing diff for staged_new.go")
	} else {
		if !d.IsNew {
			t.Errorf("staged_new.go should be marked IsNew")
		}
		if d.IsDeleted {
			t.Errorf("staged_new.go should not be marked IsDeleted")
		}
		if d.OldContent != nil {
			t.Errorf("staged_new.go OldContent should be nil, got %q", string(d.OldContent))
		}
		if !bytes.Equal(d.NewContent, []byte(stagedNewGo)) {
			t.Errorf("staged_new.go NewContent mismatch: got %q, want %q", string(d.NewContent), stagedNewGo)
		}
		if len(d.DeletedLines) != 0 {
			t.Errorf("staged_new.go DeletedLines should be empty, got %v", d.DeletedLines)
		}
		expectedAdded := []int{1, 2, 3}
		if !reflect.DeepEqual(d.AddedLines, expectedAdded) {
			t.Errorf("staged_new.go AddedLines = %v, want %v", d.AddedLines, expectedAdded)
		}
	}

	// Check untracked file pkg/sub/untracked.go (normalized path with slash)
	expectedUntrackedPath := "pkg/sub/untracked.go"
	if d, ok := diffMap[expectedUntrackedPath]; !ok {
		t.Errorf("missing diff for %s (available keys: %v)", expectedUntrackedPath, reflect.ValueOf(diffMap).MapKeys())
	} else {
		if !d.IsNew {
			t.Errorf("%s should be marked IsNew", expectedUntrackedPath)
		}
		if d.IsDeleted {
			t.Errorf("%s should not be marked IsDeleted", expectedUntrackedPath)
		}
		if d.OldContent != nil {
			t.Errorf("%s OldContent should be nil", expectedUntrackedPath)
		}
		if !bytes.Equal(d.NewContent, []byte(untrackedGo)) {
			t.Errorf("%s NewContent mismatch: got %q, want %q", expectedUntrackedPath, string(d.NewContent), untrackedGo)
		}
		expectedAdded := []int{1, 2, 3, 4, 5}
		if !reflect.DeepEqual(d.AddedLines, expectedAdded) {
			t.Errorf("%s AddedLines = %v, want %v", expectedUntrackedPath, d.AddedLines, expectedAdded)
		}
	}
}
