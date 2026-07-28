package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/snaga/ast-digger/internal/apperr"
)

func TestNewRootCmd_Use(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{
			name: "root command name is ast-digger",
			want: "ast-digger",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newRootCmd()

			if got := cmd.Use; got != tt.want {
				t.Errorf("newRootCmd().Use = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewRootCmd_Version(t *testing.T) {
	tests := []struct {
		name        string
		overrideVer string
		args        []string
		want        string
	}{
		{
			name: "--version flag prints default version string",
			args: []string{"--version"},
			want: "dev",
		},
		{
			name:        "--version flag prints overridden version string",
			overrideVer: "1.2.3",
			args:        []string{"--version"},
			want:        "1.2.3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.overrideVer != "" {
				origVer := version
				version = tt.overrideVer
				t.Cleanup(func() { version = origVer })
			}

			cmd := newRootCmd()

			out := &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(out)
			cmd.SetArgs(tt.args)

			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() returned unexpected error: %v", err)
			}

			if got := out.String(); !strings.Contains(got, tt.want) {
				t.Errorf("Execute() output = %q, want it to contain %q", got, tt.want)
			}
		})
	}
}

func TestRootHelpCmd_ContainsSupportedLanguages(t *testing.T) {
	cmd := newRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() returned unexpected error: %v", err)
	}

	got := out.String()
	wantSubstrings := []string{
		"Supported languages and extensions:",
		"Python (.py)",
		"TypeScript (.ts, .tsx)",
		"Golang (.go)",
		"Java (.java)",
		"JavaScript (.js, .jsx)",
		"Rust (.rs)",
		"Markdown (.md, .markdown)",
	}

	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("Execute(--help) output does not contain %q. Output:\n%s", want, got)
		}
	}
}

// cliResult captures the observable outcome of running the CLI: what was
// written to stdout/stderr, and the process exit code that main() would
// have chosen. It mirrors main()'s own error-reporting/exit-code logic
// (outputError + decideExitCode) so runCLI is an accurate stand-in for
// invoking the built binary via os/exec, without the overhead of actually
// building/spawning a subprocess for every test case.
type cliResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// runCLI executes newRootCmd() with args, returning the captured
// stdout/stderr and the exit code main() would report. On success (no
// error), ExitCode is apperr.ExitOK (0) and Stderr is whatever the command
// itself wrote there (normally empty).
func runCLI(t *testing.T, args ...string) cliResult {
	t.Helper()

	cmd := newRootCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)

	executedCmd, err := cmd.ExecuteC()

	exitCode := apperr.ExitOK
	if err != nil {
		outputError(&errBuf, err, jsonFlag(executedCmd))
		exitCode = decideExitCode(executedCmd, err)
	}

	return cliResult{
		Stdout:   outBuf.String(),
		Stderr:   errBuf.String(),
		ExitCode: exitCode,
	}
}

// chdirTest changes the process working directory to dir for the duration
// of t, restoring the original working directory via t.Cleanup. Several
// subcommands (directory, feedback, resolve-import's relative-import
// resolution) resolve paths relative to the process's working directory, so
// tests that exercise those need a controlled cwd.
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

const sampleFile = "../../testdata/python/sample.py"

func TestOutlineCmd_Text(t *testing.T) {
	res := runCLI(t, "outline", sampleFile)

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "class Greeter") {
		t.Errorf("stdout does not contain %q:\n%s", "class Greeter", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "...") {
		t.Errorf("stdout does not contain a replaced function body (\"...\"):\n%s", res.Stdout)
	}
}

func TestOutlineCmd_JSON(t *testing.T) {
	res := runCLI(t, "outline", sampleFile, "--json")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}

	var symbols []map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &symbols); err != nil {
		t.Fatalf("stdout is not a valid JSON array: %v\nstdout=%s", err, res.Stdout)
	}

	var found bool
	for _, s := range symbols {
		if s["name"] == "Greeter" && s["kind"] == "class" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a %q class symbol in JSON output, got: %s", "Greeter", res.Stdout)
	}
}

// TestOutlineCmd_JSON_NoSymbols guards against a regression where `outline
// --json` printed "null" (rather than "[]", matching Python's
// `json.dumps([])`) for a file containing no classes/functions.
func TestOutlineCmd_JSON_NoSymbols(t *testing.T) {
	tmp := t.TempDir()
	noSymbolsFile := filepath.Join(tmp, "no_symbols.py")
	writeFile(t, noSymbolsFile, "x = 1\ny = 2\n")

	res := runCLI(t, "outline", noSymbolsFile, "--json")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if got := strings.TrimSpace(res.Stdout); got != "[]" {
		t.Errorf("stdout = %q, want %q", got, "[]")
	}
}

func TestOutlineCmd_FileNotFound(t *testing.T) {
	res := runCLI(t, "outline", "../../testdata/python/does_not_exist.py")

	if res.ExitCode != apperr.ExitFileAccessError {
		t.Fatalf("ExitCode = %d, want %d (ExitFileAccessError); stdout=%q stderr=%q", res.ExitCode, apperr.ExitFileAccessError, res.Stdout, res.Stderr)
	}
	if !strings.HasPrefix(res.Stderr, "Error: ") {
		t.Errorf("stderr = %q, want it to start with %q", res.Stderr, "Error: ")
	}
}

func TestOutlineCmd_FileNotFound_JSON(t *testing.T) {
	res := runCLI(t, "outline", "../../testdata/python/does_not_exist.py", "--json")

	if res.ExitCode != apperr.ExitFileAccessError {
		t.Fatalf("ExitCode = %d, want %d (ExitFileAccessError)", res.ExitCode, apperr.ExitFileAccessError)
	}

	var payload map[string]string
	if err := json.Unmarshal([]byte(res.Stderr), &payload); err != nil {
		t.Fatalf("stderr is not valid JSON: %v\nstderr=%s", err, res.Stderr)
	}
	if payload["error"] == "" {
		t.Errorf("expected a non-empty %q field in stderr JSON, got: %s", "error", res.Stderr)
	}
}

// javaSampleFile/jsSampleFile/jsxSampleFile mirror sampleFile above, for the
// task 23 Java/JavaScript CLI-layer integration tests added below (outline,
// symbol, resolve-import, references, directory), which exercise the
// dispatch wiring task 22 added to internal/outline.Outliner's
// extToLang/getParser through the CLI, using the fixtures task 18-21 built
// for internal/parser's own JavaParser/JavaScriptParser tests.
const (
	javaSampleFile     = "../../testdata/java/sample.java"
	jsSampleFile       = "../../testdata/javascript/sample.js"
	jsxSampleFile      = "../../testdata/javascript/sample.jsx"
	rustSampleFile     = "../../testdata/rust/sample.rs"
	markdownSampleFile = "../../testdata/markdown/sample.md"
)

func TestOutlineCmd_Markdown_Text(t *testing.T) {
	res := runCLI(t, "outline", markdownSampleFile)
	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "## Section 1") {
		t.Errorf("stdout does not contain '## Section 1':\n%s", res.Stdout)
	}
}

func TestOutlineCmd_Markdown_JSON(t *testing.T) {
	res := runCLI(t, "outline", markdownSampleFile, "--json")
	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	var symbols []map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &symbols); err != nil {
		t.Fatalf("stdout is not valid JSON array: %v\nstdout=%s", err, res.Stdout)
	}
	found := false
	for _, s := range symbols {
		if s["name"] == "Section 1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected symbol 'Section 1' in JSON output: %s", res.Stdout)
	}
}

func TestSymbolCmd_Markdown_Text(t *testing.T) {
	res := runCLI(t, "symbol", markdownSampleFile, "Section 1")
	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "Section 1 content.") {
		t.Errorf("stdout does not contain 'Section 1 content.':\n%s", res.Stdout)
	}
}

func TestSymbolCmd_Markdown_JSON(t *testing.T) {
	res := runCLI(t, "symbol", markdownSampleFile, "Section 1", "--json")
	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, res.Stdout)
	}
	if payload["name"] != "Section 1" {
		t.Errorf("payload name = %v, want 'Section 1'", payload["name"])
	}
}

func TestLocateCmd_Markdown(t *testing.T) {
	res := runCLI(t, "locate", markdownSampleFile, "Section 1")
	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "sample.md:") {
		t.Errorf("stdout does not contain 'sample.md:':\n%s", res.Stdout)
	}
}

func TestResolveImportCmd_Markdown(t *testing.T) {
	orig, _ := os.Getwd()
	absProjDir, _ := filepath.Abs("../../testdata/markdown/importproj")
	_ = os.Chdir(absProjDir)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	res := runCLI(t, "resolve-import", "main.md", "#section-2")
	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "main.md:53") {
		t.Errorf("stdout = %q, want it to contain main.md:53", res.Stdout)
	}
}

func TestReferencesCmd_Markdown(t *testing.T) {
	orig, _ := os.Getwd()
	absProjDir, _ := filepath.Abs("../../testdata/markdown/references")
	_ = os.Chdir(absProjDir)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	res := runCLI(t, "references", "SPECS/design.md#architecture")
	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "ref.md:") {
		t.Errorf("stdout does not contain ref.md reference:\n%s", res.Stdout)
	}
}

func TestDirectoryCmd_Markdown(t *testing.T) {
	tmp := t.TempDir()
	orig, _ := os.Getwd()
	_ = os.Chdir(tmp)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	writeFile(t, filepath.Join(tmp, "doc.md"), "# Header 1\n\nSome text\n")
	res := runCLI(t, "directory", ".")
	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "doc.md") {
		t.Errorf("stdout does not contain doc.md:\n%s", res.Stdout)
	}
}

func TestOutlineCmd_Rust_Text(t *testing.T) {
	res := runCLI(t, "outline", rustSampleFile)

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "pub struct Greeter") {
		t.Errorf("stdout does not contain %q:\n%s", "pub struct Greeter", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "{ /* ... */ }") {
		t.Errorf("stdout does not contain a replaced function/method body (\"{ /* ... */ }\"):\n%s", res.Stdout)
	}
}

func TestOutlineCmd_Rust_JSON(t *testing.T) {
	res := runCLI(t, "outline", rustSampleFile, "--json")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}

	var symbols []map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &symbols); err != nil {
		t.Fatalf("stdout is not a valid JSON array: %v\nstdout=%s", err, res.Stdout)
	}

	var found bool
	for _, s := range symbols {
		if s["name"] == "Greeter" && s["kind"] == "struct" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a %q struct symbol in JSON output, got: %s", "Greeter", res.Stdout)
	}
}

func TestSymbolCmd_Rust_Text(t *testing.T) {
	res := runCLI(t, "symbol", rustSampleFile, "Greeter.greet")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "pub fn greet(&self) -> String") {
		t.Errorf("stdout does not contain %q:\n%s", "pub fn greet(&self) -> String", res.Stdout)
	}
	if strings.Contains(res.Stdout, "{ /* ... */ }") {
		t.Errorf("stdout should contain the full implementation, not a skeleton:\n%s", res.Stdout)
	}
}

func TestSymbolCmd_Rust_JSON(t *testing.T) {
	res := runCLI(t, "symbol", rustSampleFile, "Greeter.greet", "--json")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, res.Stdout)
	}
	if payload["name"] != "greet" {
		t.Errorf(`payload["name"] = %v, want "greet"`, payload["name"])
	}
	if payload["parent"] != "Greeter" {
		t.Errorf(`payload["parent"] = %v, want "Greeter"`, payload["parent"])
	}
	code, _ := payload["code"].(string)
	if !strings.Contains(code, "pub fn greet(&self) -> String") {
		t.Errorf(`payload["code"] = %q, want it to contain "pub fn greet(&self) -> String"`, code)
	}
}

func TestLocateCmd_Rust(t *testing.T) {
	res := runCLI(t, "locate", rustSampleFile, "Greeter.greet")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "sample.rs:38-40") {
		t.Errorf("stdout = %q, want it to contain %q", res.Stdout, "sample.rs:38-40")
	}
}

func TestResolveImportCmd_Rust(t *testing.T) {
	orig, _ := os.Getwd()
	absProjDir, _ := filepath.Abs("../../testdata/rust/importproj")
	_ = os.Chdir(absProjDir)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	res := runCLI(t, "resolve-import", "main.rs", "add")
	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "utils.rs:1") {
		t.Errorf("stdout = %q, want it to contain utils.rs:1", res.Stdout)
	}
}

func TestReferencesCmd_Rust(t *testing.T) {
	orig, _ := os.Getwd()
	absProjDir, _ := filepath.Abs("../../testdata/mixed/references")
	_ = os.Chdir(absProjDir)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	res := runCLI(t, "references", "helperFn")
	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "caller.rs") {
		t.Errorf("stdout does not contain reference in caller.rs:\n%s", res.Stdout)
	}
}

func TestOutlineCmd_Java_Text(t *testing.T) {
	res := runCLI(t, "outline", javaSampleFile)

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "public class SampleClass") {
		t.Errorf("stdout does not contain %q:\n%s", "public class SampleClass", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "{ /* ... */ }") {
		t.Errorf("stdout does not contain a replaced method body (\"{ /* ... */ }\"):\n%s", res.Stdout)
	}
}

func TestOutlineCmd_Java_JSON(t *testing.T) {
	res := runCLI(t, "outline", javaSampleFile, "--json")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}

	var symbols []map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &symbols); err != nil {
		t.Fatalf("stdout is not a valid JSON array: %v\nstdout=%s", err, res.Stdout)
	}

	var found bool
	for _, s := range symbols {
		if s["name"] == "SampleClass" && s["kind"] == "class" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a %q class symbol in JSON output, got: %s", "SampleClass", res.Stdout)
	}
}

func TestOutlineCmd_JavaScript_Text(t *testing.T) {
	res := runCLI(t, "outline", jsSampleFile)

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "class Greeter") {
		t.Errorf("stdout does not contain %q:\n%s", "class Greeter", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "{ /* ... */ }") {
		t.Errorf("stdout does not contain a replaced function body (\"{ /* ... */ }\"):\n%s", res.Stdout)
	}
}

func TestOutlineCmd_JavaScript_JSON(t *testing.T) {
	res := runCLI(t, "outline", jsSampleFile, "--json")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}

	var symbols []map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &symbols); err != nil {
		t.Fatalf("stdout is not a valid JSON array: %v\nstdout=%s", err, res.Stdout)
	}

	var found bool
	for _, s := range symbols {
		if s["name"] == "Greeter" && s["kind"] == "class" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a %q class symbol in JSON output, got: %s", "Greeter", res.Stdout)
	}
}

// TestOutlineCmd_JavaScript_JSX verifies that the outline command also
// dispatches ".jsx" files (not just ".js") to JavaScriptParser, per task
// 22's extToLang entry mapping both extensions to "javascript".
func TestOutlineCmd_JavaScript_JSX(t *testing.T) {
	res := runCLI(t, "outline", jsxSampleFile)

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "function GreetingBanner") {
		t.Errorf("stdout does not contain %q:\n%s", "function GreetingBanner", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "{ /* ... */ }") {
		t.Errorf("stdout does not contain a replaced function body (\"{ /* ... */ }\"):\n%s", res.Stdout)
	}
}

func TestSymbolCmd_Text(t *testing.T) {
	res := runCLI(t, "symbol", sampleFile, "Greeter.shout")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "def shout") {
		t.Errorf("stdout does not contain %q:\n%s", "def shout", res.Stdout)
	}
	if strings.Contains(res.Stdout, "...") {
		t.Errorf("stdout should contain the full implementation, not a skeleton:\n%s", res.Stdout)
	}
}

func TestSymbolCmd_JSON(t *testing.T) {
	res := runCLI(t, "symbol", sampleFile, "Greeter.shout", "--json")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, res.Stdout)
	}
	if payload["name"] != "shout" {
		t.Errorf(`payload["name"] = %v, want "shout"`, payload["name"])
	}
	if payload["parent"] != "Greeter" {
		t.Errorf(`payload["parent"] = %v, want "Greeter"`, payload["parent"])
	}
	code, _ := payload["code"].(string)
	if !strings.Contains(code, "def shout") {
		t.Errorf(`payload["code"] = %q, want it to contain "def shout"`, code)
	}
}

func TestSymbolCmd_NotFound(t *testing.T) {
	res := runCLI(t, "symbol", sampleFile, "NoSuchClass")

	if res.ExitCode != apperr.ExitSymbolNotFoundError {
		t.Fatalf("ExitCode = %d, want %d (ExitSymbolNotFoundError); stdout=%q stderr=%q", res.ExitCode, apperr.ExitSymbolNotFoundError, res.Stdout, res.Stderr)
	}
}

// TestSymbolCmd_NotFound_SuggestsCloseMatch verifies F-10 (task 14)
// end-to-end through the CLI: a near-miss symbol path's error message
// suggests the existing symbol it was probably meant to be, while a symbol
// path with no close match in the file gets no such suggestion.
func TestSymbolCmd_NotFound_SuggestsCloseMatch(t *testing.T) {
	t.Run("near-miss suggests the existing symbol", func(t *testing.T) {
		res := runCLI(t, "symbol", sampleFile, "Greeter.shot")
		if res.ExitCode != apperr.ExitSymbolNotFoundError {
			t.Fatalf("ExitCode = %d, want %d (ExitSymbolNotFoundError); stderr=%q", res.ExitCode, apperr.ExitSymbolNotFoundError, res.Stderr)
		}
		if !strings.Contains(res.Stderr, "Did you mean?") || !strings.Contains(res.Stderr, "Greeter.shout") {
			t.Errorf("stderr = %q, want it to suggest %q via a %q section", res.Stderr, "Greeter.shout", "Did you mean?")
		}
	})

	t.Run("no close match yields no suggestion section", func(t *testing.T) {
		res := runCLI(t, "symbol", sampleFile, "CompletelyUnrelatedXyz123")
		if res.ExitCode != apperr.ExitSymbolNotFoundError {
			t.Fatalf("ExitCode = %d, want %d (ExitSymbolNotFoundError); stderr=%q", res.ExitCode, apperr.ExitSymbolNotFoundError, res.Stderr)
		}
		if strings.Contains(res.Stderr, "Did you mean?") {
			t.Errorf("stderr = %q, want it to not contain %q", res.Stderr, "Did you mean?")
		}
	})
}

func TestSymbolCmd_Java_Text(t *testing.T) {
	res := runCLI(t, "symbol", javaSampleFile, "SampleClass.greet")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "public String greet()") {
		t.Errorf("stdout does not contain %q:\n%s", "public String greet()", res.Stdout)
	}
	if strings.Contains(res.Stdout, "{ /* ... */ }") {
		t.Errorf("stdout should contain the full implementation, not a skeleton:\n%s", res.Stdout)
	}
}

func TestSymbolCmd_Java_JSON(t *testing.T) {
	res := runCLI(t, "symbol", javaSampleFile, "SampleClass.greet", "--json")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, res.Stdout)
	}
	if payload["name"] != "greet" {
		t.Errorf(`payload["name"] = %v, want "greet"`, payload["name"])
	}
	if payload["parent"] != "SampleClass" {
		t.Errorf(`payload["parent"] = %v, want "SampleClass"`, payload["parent"])
	}
	code, _ := payload["code"].(string)
	if !strings.Contains(code, "public String greet()") {
		t.Errorf(`payload["code"] = %q, want it to contain "public String greet()"`, code)
	}
}

func TestSymbolCmd_JavaScript_Text(t *testing.T) {
	res := runCLI(t, "symbol", jsSampleFile, "Greeter.shout")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "static shout(message)") {
		t.Errorf("stdout does not contain %q:\n%s", "static shout(message)", res.Stdout)
	}
	if strings.Contains(res.Stdout, "{ /* ... */ }") {
		t.Errorf("stdout should contain the full implementation, not a skeleton:\n%s", res.Stdout)
	}
}

func TestSymbolCmd_JavaScript_JSON(t *testing.T) {
	res := runCLI(t, "symbol", jsSampleFile, "Greeter.shout", "--json")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, res.Stdout)
	}
	if payload["name"] != "shout" {
		t.Errorf(`payload["name"] = %v, want "shout"`, payload["name"])
	}
	if payload["parent"] != "Greeter" {
		t.Errorf(`payload["parent"] = %v, want "Greeter"`, payload["parent"])
	}
	code, _ := payload["code"].(string)
	if !strings.Contains(code, "static shout(message)") {
		t.Errorf(`payload["code"] = %q, want it to contain "static shout(message)"`, code)
	}
}

func TestLocateCmd_Text(t *testing.T) {
	res := runCLI(t, "locate", sampleFile, "Greeter")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	want := sampleFile + ":9-19\n"
	if res.Stdout != want {
		t.Errorf("stdout = %q, want %q", res.Stdout, want)
	}
}

func TestLocateCmd_JSON(t *testing.T) {
	res := runCLI(t, "locate", sampleFile, "Greeter.shout", "--json")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}

	var payload struct {
		File      string `json:"file"`
		Symbol    string `json:"symbol"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, res.Stdout)
	}
	if payload.Symbol != "shout" {
		t.Errorf("payload.Symbol = %q, want %q", payload.Symbol, "shout")
	}
	if payload.StartLine != 16 || payload.EndLine != 19 {
		t.Errorf("payload lines = [%d,%d], want [16,19]", payload.StartLine, payload.EndLine)
	}
}

func TestLocateCmd_SymbolNotFound(t *testing.T) {
	res := runCLI(t, "locate", sampleFile, "NoSuchClass")

	if res.ExitCode != apperr.ExitSymbolNotFoundError {
		t.Fatalf("ExitCode = %d, want %d (ExitSymbolNotFoundError); stdout=%q stderr=%q", res.ExitCode, apperr.ExitSymbolNotFoundError, res.Stdout, res.Stderr)
	}
}

func TestLocateCmd_FileNotFound(t *testing.T) {
	res := runCLI(t, "locate", "../../testdata/python/does_not_exist.py", "Greeter")

	if res.ExitCode != apperr.ExitFileAccessError {
		t.Fatalf("ExitCode = %d, want %d (ExitFileAccessError); stdout=%q stderr=%q", res.ExitCode, apperr.ExitFileAccessError, res.Stdout, res.Stderr)
	}
}

// TestResolveImportCmd exercises the resolve-import command from within the
// importproj testdata fixture's own directory, since PythonParser's import
// resolution is relative to the importing file's location.
func TestResolveImportCmd(t *testing.T) {
	chdirTest(t, "../../testdata/python/importproj")

	t.Run("text", func(t *testing.T) {
		res := runCLI(t, "resolve-import", "main.py", "add")
		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		want := "mathutils.py:1\n"
		if res.Stdout != want {
			t.Errorf("stdout = %q, want %q", res.Stdout, want)
		}
	})

	t.Run("json", func(t *testing.T) {
		res := runCLI(t, "resolve-import", "main.py", "add", "--json")
		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		var payload struct {
			FilePath  string `json:"file_path"`
			StartLine int    `json:"start_line"`
		}
		if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
			t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, res.Stdout)
		}
		if payload.FilePath != "mathutils.py" || payload.StartLine != 1 {
			t.Errorf("payload = %+v, want {FilePath: mathutils.py, StartLine: 1}", payload)
		}
	})

	// Task 14: "symbol isn't imported by an otherwise-readable file" is a
	// SymbolNotFoundError (exit code 4), not a FileAccessError (exit code
	// 3) -- an intentional semantic improvement over Python's resolve_imports
	// (see PythonParser.ResolveImports's doc comment).
	t.Run("symbol not imported -> symbol not found error exit code", func(t *testing.T) {
		res := runCLI(t, "resolve-import", "main.py", "no_such_symbol")
		if res.ExitCode != apperr.ExitSymbolNotFoundError {
			t.Fatalf("ExitCode = %d, want %d (ExitSymbolNotFoundError); stdout=%q stderr=%q", res.ExitCode, apperr.ExitSymbolNotFoundError, res.Stdout, res.Stderr)
		}
	})

	// Module file genuinely not resolvable (an import statement referring to
	// a module that doesn't exist on disk) remains a FileAccessError (exit
	// code 3): the distinction task 14 draws is between "file not found" and
	// "symbol not imported by a file that was found", not between different
	// kinds of import statements.
	t.Run("module file not found -> file access error exit code", func(t *testing.T) {
		res := runCLI(t, "resolve-import", "bad_caller.py", "something")
		if res.ExitCode != apperr.ExitFileAccessError {
			t.Fatalf("ExitCode = %d, want %d (ExitFileAccessError); stdout=%q stderr=%q", res.ExitCode, apperr.ExitFileAccessError, res.Stdout, res.Stderr)
		}
	})
}

// TestResolveImportCmd_Java exercises the resolve-import command from
// within the Java importproj fixture's own directory (task 19's
// "src/main/java" source-root convention), mirroring TestResolveImportCmd
// above.
func TestResolveImportCmd_Java(t *testing.T) {
	chdirTest(t, "../../testdata/java/importproj")

	const mainFile = "src/main/java/com/example/app/Main.java"

	t.Run("text", func(t *testing.T) {
		res := runCLI(t, "resolve-import", mainFile, "MathUtils")
		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		const wantSuffix = "src/main/java/com/example/util/MathUtils.java:3\n"
		if !strings.HasSuffix(filepath.ToSlash(res.Stdout), wantSuffix) {
			t.Errorf("stdout = %q, want suffix %q", res.Stdout, wantSuffix)
		}
	})

	t.Run("json", func(t *testing.T) {
		res := runCLI(t, "resolve-import", mainFile, "MathUtils", "--json")
		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		var payload struct {
			FilePath  string `json:"file_path"`
			StartLine int    `json:"start_line"`
		}
		if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
			t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, res.Stdout)
		}
		const wantSuffix = "src/main/java/com/example/util/MathUtils.java"
		if !strings.HasSuffix(filepath.ToSlash(payload.FilePath), wantSuffix) || payload.StartLine != 3 {
			t.Errorf("payload = %+v, want {FilePath: ...%s, StartLine: 3}", payload, wantSuffix)
		}
	})

	// Task 19: a symbol not declared in any resolvable wildcard-imported
	// package is a SymbolNotFoundError (exit code 4), matching
	// TestJavaParser_ResolveImports_SymbolNotFoundInImports.
	t.Run("symbol not imported -> symbol not found error exit code", func(t *testing.T) {
		res := runCLI(t, "resolve-import", "src/main/java/com/example/app/WildcardCaller.java", "TotallyAbsentSymbol")
		if res.ExitCode != apperr.ExitSymbolNotFoundError {
			t.Fatalf("ExitCode = %d, want %d (ExitSymbolNotFoundError); stdout=%q stderr=%q", res.ExitCode, apperr.ExitSymbolNotFoundError, res.Stdout, res.Stderr)
		}
	})

	// An imported package that cannot be located as a local directory is a
	// FileAccessError (exit code 3), matching
	// TestJavaParser_ResolveImports_UnresolvableImport.
	t.Run("unresolvable imported package -> file access error exit code", func(t *testing.T) {
		res := runCLI(t, "resolve-import", "src/main/java/com/example/app/BadCaller.java", "Something")
		if res.ExitCode != apperr.ExitFileAccessError {
			t.Fatalf("ExitCode = %d, want %d (ExitFileAccessError); stdout=%q stderr=%q", res.ExitCode, apperr.ExitFileAccessError, res.Stdout, res.Stderr)
		}
	})
}

// TestResolveImportCmd_JavaScript exercises the resolve-import command from
// within the JavaScript importproj fixture's own directory (task 21's "@/"
// -> "src/" alias and other absolute-import conventions are resolved
// relative to the process's working directory), mirroring
// TestResolveImportCmd above.
func TestResolveImportCmd_JavaScript(t *testing.T) {
	chdirTest(t, "../../testdata/javascript/importproj")

	t.Run("text", func(t *testing.T) {
		res := runCLI(t, "resolve-import", "main.js", "add")
		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		want := "mathutils.js:1\n"
		if res.Stdout != want {
			t.Errorf("stdout = %q, want %q", res.Stdout, want)
		}
	})

	t.Run("json", func(t *testing.T) {
		res := runCLI(t, "resolve-import", "main.js", "add", "--json")
		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		var payload struct {
			FilePath  string `json:"file_path"`
			StartLine int    `json:"start_line"`
		}
		if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
			t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, res.Stdout)
		}
		if payload.FilePath != "mathutils.js" || payload.StartLine != 1 {
			t.Errorf("payload = %+v, want {FilePath: mathutils.js, StartLine: 1}", payload)
		}
	})

	// Task 21: a symbol not imported anywhere in the target file is a
	// SymbolNotFoundError (exit code 4), matching
	// TestJavaScriptParser_ResolveImports_SymbolNotImported.
	t.Run("symbol not imported -> symbol not found error exit code", func(t *testing.T) {
		res := runCLI(t, "resolve-import", "main.js", "totally_absent_symbol")
		if res.ExitCode != apperr.ExitSymbolNotFoundError {
			t.Fatalf("ExitCode = %d, want %d (ExitSymbolNotFoundError); stdout=%q stderr=%q", res.ExitCode, apperr.ExitSymbolNotFoundError, res.Stdout, res.Stderr)
		}
	})

	// A module specifier that cannot be located on disk is a
	// FileAccessError (exit code 3), matching
	// TestJavaScriptParser_ResolveImports_UnresolvableModule.
	t.Run("unresolvable module -> file access error exit code", func(t *testing.T) {
		res := runCLI(t, "resolve-import", "bad_caller.js", "something")
		if res.ExitCode != apperr.ExitFileAccessError {
			t.Fatalf("ExitCode = %d, want %d (ExitFileAccessError); stdout=%q stderr=%q", res.ExitCode, apperr.ExitFileAccessError, res.Stdout, res.Stderr)
		}
	})
}

// TestDirectoryCmd exercises the directory command against a scratch
// project (rather than go/testdata, to keep it independent of that
// fixture's own .workspace_cache and other tests running concurrently
// against it).
func TestDirectoryCmd(t *testing.T) {
	tmp := t.TempDir()
	chdirTest(t, tmp)

	writeFile(t, filepath.Join(tmp, "pkg", "mod.py"), "def alpha():\n    pass\n")

	t.Run("text, recursive (default)", func(t *testing.T) {
		res := runCLI(t, "directory", ".")
		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		if !strings.Contains(res.Stdout, "mod.py") || !strings.Contains(res.Stdout, "def alpha") {
			t.Errorf("stdout missing expected entries:\n%s", res.Stdout)
		}
	})

	t.Run("non-recursive omits nested files", func(t *testing.T) {
		res := runCLI(t, "directory", ".", "--recursive=false")
		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		if strings.Contains(res.Stdout, "mod.py") {
			t.Errorf("stdout should not contain the nested file %q with --recursive=false:\n%s", "mod.py", res.Stdout)
		}
	})

	// --no-recursive mirrors Python's typer paired option
	// `--recursive/--no-recursive`; unlike --recursive=false, it takes no
	// "=value" argument.
	t.Run("--no-recursive omits nested files", func(t *testing.T) {
		res := runCLI(t, "directory", ".", "--no-recursive")
		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		if strings.Contains(res.Stdout, "mod.py") {
			t.Errorf("stdout should not contain the nested file %q with --no-recursive:\n%s", "mod.py", res.Stdout)
		}
	})

	t.Run("explicit --recursive includes nested files", func(t *testing.T) {
		res := runCLI(t, "directory", ".", "--recursive")
		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		if !strings.Contains(res.Stdout, "mod.py") {
			t.Errorf("stdout should contain the nested file %q with --recursive:\n%s", "mod.py", res.Stdout)
		}
	})

	t.Run("--no-recursive overrides an explicit --recursive", func(t *testing.T) {
		res := runCLI(t, "directory", ".", "--recursive", "--no-recursive")
		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		if strings.Contains(res.Stdout, "mod.py") {
			t.Errorf("stdout should not contain the nested file %q with --recursive --no-recursive:\n%s", "mod.py", res.Stdout)
		}
	})

	t.Run("json wraps the tree text in a result object", func(t *testing.T) {
		res := runCLI(t, "directory", ".", "--json")
		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		var payload struct {
			Result string `json:"result"`
		}
		if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
			t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, res.Stdout)
		}
		if !strings.Contains(payload.Result, "mod.py") {
			t.Errorf("payload.Result missing %q:\n%s", "mod.py", payload.Result)
		}
	})
}

// TestDirectoryCmd_MultiLanguage is an end-to-end (CLI-layer) check that the
// `directory` command, on top of task 10's (Python/TypeScript/Go) and task
// 22's (Java/JavaScript) extToLang/getParser dispatch extensions, correctly
// walks and renders a project mixing all six supported languages' source
// files -- not just single-language fixtures (covered above by
// TestDirectoryCmd and, more thoroughly at the outline.Outliner layer, by
// TestOutliner_BuildDirectoryTree_MultiLanguage/_DiffCacheStillWorks in
// internal/outline).
func TestDirectoryCmd_MultiLanguage(t *testing.T) {
	tmp := t.TempDir()
	chdirTest(t, tmp)

	writeFile(t, filepath.Join(tmp, "app.py"), "def greet_py() -> None:\n    pass\n")
	writeFile(t, filepath.Join(tmp, "app.ts"), "export function greetTs(): void {}\n")
	writeFile(t, filepath.Join(tmp, "app.go"), "package app\n\nfunc GreetGo() {}\n")
	writeFile(t, filepath.Join(tmp, "App.java"), "public class App {\n    public static void greetJava() {\n    }\n}\n")
	writeFile(t, filepath.Join(tmp, "app.js"), "export function greetJs() {}\n")
	writeFile(t, filepath.Join(tmp, "app.rs"), "pub fn greet_rs() {}\n")

	res := runCLI(t, "directory", ".")
	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	for _, want := range []string{
		"app.py", "app.ts", "app.go", "App.java", "app.js", "app.rs",
		"def greet_py", "def greetTs", "def GreetGo",
		"class App", "def greetJava", "def greetJs", "def greet_rs",
	} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("stdout missing expected entry %q:\n%s", want, res.Stdout)
		}
	}
}

func TestFeedbackCmd(t *testing.T) {
	tmp := t.TempDir()
	chdirTest(t, tmp)

	t.Run("text", func(t *testing.T) {
		res := runCLI(t, "feedback", "the outline command was confusing")
		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		want := "Feedback recorded successfully.\n"
		if res.Stdout != want {
			t.Errorf("stdout = %q, want %q", res.Stdout, want)
		}
	})

	t.Run("json", func(t *testing.T) {
		res := runCLI(t, "feedback", "another message", "--json")
		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		var payload struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
			t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, res.Stdout)
		}
		if payload.Message != "Feedback recorded successfully." {
			t.Errorf("payload.Message = %q, want %q", payload.Message, "Feedback recorded successfully.")
		}
	})

	logPath := filepath.Join(tmp, ".workspace_cache", "feedback.jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read feedback log %q: %v", logPath, err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 feedback log lines, got %d: %q", len(lines), string(data))
	}
}

// writeFile writes content to path (creating parent directories as
// needed), failing the test on any error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create parent dir for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write %q: %v", path, err)
	}
}

// TestReferencesCmd_Text exercises the `references` command in text mode
// against go/testdata/python/references (task 4/6's fixture: 2 genuine
// call sites, plus a comment mention and a same-named parameter that must
// both be excluded).
func TestReferencesCmd_Text(t *testing.T) {
	res := runCLI(t, "references", "target_func", "--project-root", "../../testdata/python/references")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}

	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 reference lines, got %d:\n%s", len(lines), res.Stdout)
	}
	for _, want := range []string{"caller_a.py:2:", "caller_b.py:6:"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("stdout missing expected line prefix %q:\n%s", want, res.Stdout)
		}
	}
	if res.Stderr != "" {
		t.Errorf("stderr = %q, want empty (no truncation expected)", res.Stderr)
	}
}

// TestReferencesCmd_JSON checks the JSON shape of a non-truncated result:
// references/truncated/total_count/limit, with no "hint" key.
func TestReferencesCmd_JSON(t *testing.T) {
	res := runCLI(t, "references", "target_func", "--project-root", "../../testdata/python/references", "--json")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}

	var payload struct {
		References []struct {
			FilePath   string `json:"file_path"`
			LineNumber int    `json:"line_number"`
			Snippet    string `json:"snippet"`
		} `json:"references"`
		Truncated  bool `json:"truncated"`
		TotalCount int  `json:"total_count"`
		Limit      int  `json:"limit"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, res.Stdout)
	}
	if len(payload.References) != 2 {
		t.Errorf("len(payload.References) = %d, want 2", len(payload.References))
	}
	if payload.Truncated {
		t.Errorf("payload.Truncated = true, want false")
	}
	if payload.TotalCount != 2 {
		t.Errorf("payload.TotalCount = %d, want 2", payload.TotalCount)
	}
	if payload.Limit != 50 {
		t.Errorf("payload.Limit = %d, want 50 (default)", payload.Limit)
	}
	if strings.Contains(res.Stdout, `"hint"`) {
		t.Errorf("stdout should not contain a %q key when results are not truncated:\n%s", "hint", res.Stdout)
	}
}

// TestReferencesCmd_NoMatches checks that a symbol with zero references
// across the project yields an empty (non-null) JSON array, matching
// Python's json.dumps([]) -- see task 7's "outline --json null bug" lesson.
func TestReferencesCmd_NoMatches(t *testing.T) {
	res := runCLI(t, "references", "no_such_symbol_anywhere", "--project-root", "../../testdata/python/references", "--json")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}

	var payload struct {
		References []map[string]any `json:"references"`
		TotalCount int              `json:"total_count"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, res.Stdout)
	}
	if payload.References == nil {
		t.Errorf("payload.References is nil (JSON null), want an empty array")
	}
	if len(payload.References) != 0 || payload.TotalCount != 0 {
		t.Errorf("expected zero references, got %d (total_count=%d)", len(payload.References), payload.TotalCount)
	}

	if !strings.HasPrefix(res.Stdout, `{`) {
		t.Fatalf("expected a JSON object, got: %s", res.Stdout)
	}
}

// TestReferencesCmd_NonexistentProjectRoot mirrors Python's
// Outliner.find_references: a --project-root that does not exist on disk
// is not an error, it simply yields zero references.
func TestReferencesCmd_NonexistentProjectRoot(t *testing.T) {
	res := runCLI(t, "references", "anything", "--project-root", "../../testdata/does_not_exist_dir")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "" {
		t.Errorf("stdout = %q, want empty (zero references, one blank line)", res.Stdout)
	}
}

// TestReferencesCmd_LimitTruncation exercises the --limit truncation path
// against go/testdata/python/references_bulk, which contains 60 genuine
// call sites of bulk_symbol (well above any reasonable --limit).
func TestReferencesCmd_LimitTruncation(t *testing.T) {
	const bulkRoot = "../../testdata/python/references_bulk"

	t.Run("text", func(t *testing.T) {
		res := runCLI(t, "references", "bulk_symbol", "--project-root", bulkRoot, "--limit", "10")

		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}

		lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
		if len(lines) != 10 {
			t.Fatalf("expected 10 reference lines after truncation, got %d:\n%s", len(lines), res.Stdout)
		}

		wantHint := "# ... (truncated: 10 of 60 results shown. Use --limit to increase or narrow down using --project-root)\n"
		if res.Stderr != wantHint {
			t.Errorf("stderr = %q, want %q", res.Stderr, wantHint)
		}
	})

	t.Run("json", func(t *testing.T) {
		res := runCLI(t, "references", "bulk_symbol", "--project-root", bulkRoot, "--limit", "10", "--json")

		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}

		var payload struct {
			References []map[string]any `json:"references"`
			Truncated  bool             `json:"truncated"`
			TotalCount int              `json:"total_count"`
			Limit      int              `json:"limit"`
			Hint       string           `json:"hint"`
		}
		if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
			t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, res.Stdout)
		}
		if len(payload.References) != 10 {
			t.Errorf("len(payload.References) = %d, want 10", len(payload.References))
		}
		if !payload.Truncated {
			t.Errorf("payload.Truncated = false, want true")
		}
		if payload.TotalCount != 60 {
			t.Errorf("payload.TotalCount = %d, want 60", payload.TotalCount)
		}
		if payload.Limit != 10 {
			t.Errorf("payload.Limit = %d, want 10", payload.Limit)
		}
		wantHint := "Narrow down the scope using --project-root or increase --limit"
		if payload.Hint != wantHint {
			t.Errorf("payload.Hint = %q, want %q", payload.Hint, wantHint)
		}
		// stderr in --json mode should not carry the human-readable
		// truncation hint: the machine-readable "hint" field above already
		// conveys it, matching Python's references command (the stderr
		// print only happens in the non-JSON branch).
		if res.Stderr != "" {
			t.Errorf("stderr = %q, want empty in --json mode", res.Stderr)
		}
	})

	t.Run("limit exactly at total count is not truncated", func(t *testing.T) {
		res := runCLI(t, "references", "bulk_symbol", "--project-root", bulkRoot, "--limit", "60")

		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
		if len(lines) != 60 {
			t.Fatalf("expected 60 reference lines (no truncation), got %d", len(lines))
		}
		if res.Stderr != "" {
			t.Errorf("stderr = %q, want empty (--limit equal to total_count must not trigger truncation)", res.Stderr)
		}
	})
}

// TestReferencesCmd_Java_Text exercises the `references` command in text
// mode against testdata/java/references (task 19's fixture). Unlike
// Python/TypeScript/JavaScript, JavaParser does not exclude the definition
// site itself (matching GolangParser's asymmetry, documented on
// TestJavaParser_FindReferencesInFile), so all three occurrences --
// definition plus two genuine call sites -- are expected, with the comment
// mention in caller_a.java excluded.
func TestReferencesCmd_Java_Text(t *testing.T) {
	res := runCLI(t, "references", "TargetMethod", "--project-root", "../../testdata/java/references")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}

	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 reference lines, got %d:\n%s", len(lines), res.Stdout)
	}
	for _, want := range []string{"caller_a.java:5:", "caller_b.java:6:", "target.java:4:"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("stdout missing expected line prefix %q:\n%s", want, res.Stdout)
		}
	}
	if res.Stderr != "" {
		t.Errorf("stderr = %q, want empty (no truncation expected)", res.Stderr)
	}
}

// TestReferencesCmd_Java_JSON checks the JSON shape of a Java references
// result, mirroring TestReferencesCmd_JSON.
func TestReferencesCmd_Java_JSON(t *testing.T) {
	res := runCLI(t, "references", "TargetMethod", "--project-root", "../../testdata/java/references", "--json")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}

	var payload struct {
		References []struct {
			FilePath   string `json:"file_path"`
			LineNumber int    `json:"line_number"`
			Snippet    string `json:"snippet"`
		} `json:"references"`
		Truncated  bool `json:"truncated"`
		TotalCount int  `json:"total_count"`
		Limit      int  `json:"limit"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, res.Stdout)
	}
	if len(payload.References) != 3 {
		t.Errorf("len(payload.References) = %d, want 3", len(payload.References))
	}
	if payload.Truncated {
		t.Errorf("payload.Truncated = true, want false")
	}
	if payload.TotalCount != 3 {
		t.Errorf("payload.TotalCount = %d, want 3", payload.TotalCount)
	}
	if payload.Limit != 50 {
		t.Errorf("payload.Limit = %d, want 50 (default)", payload.Limit)
	}
}

// TestReferencesCmd_JavaScript_Text exercises the `references` command in
// text mode against testdata/javascript/references (task 21's fixture).
// Unlike Java/Go, JavaScriptParser excludes the definition site itself
// (matching Python/TypeScript), so only the two genuine call sites plus the
// template-literal substitution reference are expected; the comment mention
// in caller_a.js is excluded.
func TestReferencesCmd_JavaScript_Text(t *testing.T) {
	res := runCLI(t, "references", "targetFunc", "--project-root", "../../testdata/javascript/references")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}

	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 reference lines, got %d:\n%s", len(lines), res.Stdout)
	}
	for _, want := range []string{"caller_a.js:1:", "caller_b.js:2:", "template_literal.js:2:"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("stdout missing expected line prefix %q:\n%s", want, res.Stdout)
		}
	}
	if res.Stderr != "" {
		t.Errorf("stderr = %q, want empty (no truncation expected)", res.Stderr)
	}
}

// TestReferencesCmd_JavaScript_JSON checks the JSON shape of a JavaScript
// references result, mirroring TestReferencesCmd_JSON.
func TestReferencesCmd_JavaScript_JSON(t *testing.T) {
	res := runCLI(t, "references", "targetFunc", "--project-root", "../../testdata/javascript/references", "--json")

	if res.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}

	var payload struct {
		References []struct {
			FilePath   string `json:"file_path"`
			LineNumber int    `json:"line_number"`
			Snippet    string `json:"snippet"`
		} `json:"references"`
		Truncated  bool `json:"truncated"`
		TotalCount int  `json:"total_count"`
		Limit      int  `json:"limit"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, res.Stdout)
	}
	if len(payload.References) != 3 {
		t.Errorf("len(payload.References) = %d, want 3", len(payload.References))
	}
	if payload.Truncated {
		t.Errorf("payload.Truncated = true, want false")
	}
	if payload.TotalCount != 3 {
		t.Errorf("payload.TotalCount = %d, want 3", payload.TotalCount)
	}
	if payload.Limit != 50 {
		t.Errorf("payload.Limit = %d, want 50 (default)", payload.Limit)
	}
}

// bigPythonModule returns a syntactically valid Python module of exactly n
// top-level statement lines (no functions/classes), starting with a
// "top_marker = ..." assignment, ending with a "bottom_marker = ..."
// assignment, and containing a "middle_marker = ..." assignment roughly
// halfway through. Since GenerateSkeleton only rewrites function/method
// bodies, none of which exist here, its output is exactly this module's
// source, letting outline truncation tests target the "generated text" side
// (as opposed to the "extracted symbol code" side, covered by
// bigPythonFunction) independent of the parser's skeleton-rewriting logic.
func bigPythonModule(n int) string {
	lines := make([]string, n)
	mid := n / 2
	for i := 0; i < n; i++ {
		switch i {
		case 0:
			lines[i] = "top_marker = 1"
		case n - 1:
			lines[i] = "bottom_marker = 1"
		case mid:
			lines[i] = "middle_marker = 1"
		default:
			lines[i] = "x" + strconv.Itoa(i) + " = " + strconv.Itoa(i)
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// bigPythonFunction returns a syntactically valid Python module defining a
// single top-level function big_function with a body of n statement lines
// (top_marker first, bottom_marker last, middle_marker roughly halfway),
// for use in `symbol` command truncation tests.
func bigPythonFunction(n int) string {
	lines := make([]string, n+1)
	lines[0] = "def big_function():"
	mid := n / 2
	for i := 0; i < n; i++ {
		var stmt string
		switch i {
		case 0:
			stmt = "top_marker = 1"
		case n - 1:
			stmt = "bottom_marker = 1"
		case mid:
			stmt = "middle_marker = 1"
		default:
			stmt = "v" + strconv.Itoa(i) + " = " + strconv.Itoa(i)
		}
		lines[i+1] = "    " + stmt
	}
	return strings.Join(lines, "\n") + "\n"
}

// TestOutlineCmd_Truncation exercises task 12's auto-truncation (default
// on, at a 300-line threshold) and --no-truncate opt-out for the `outline`
// command. Truncation only ever applies to the text-mode skeleton output --
// `outline --json` always returns the raw, untruncated symbol list, since
// Python's `outline --json` branch never calls truncate_text at all.
func TestOutlineCmd_Truncation(t *testing.T) {
	tmp := t.TempDir()
	bigFile := filepath.Join(tmp, "big_module.py")
	writeFile(t, bigFile, bigPythonModule(350))

	smallFile := filepath.Join(tmp, "small_module.py")
	writeFile(t, smallFile, bigPythonModule(10))

	exactFile := filepath.Join(tmp, "exact_module.py")
	exactSource := bigPythonModule(truncationLimit)
	writeFile(t, exactFile, exactSource)

	t.Run("text mode truncates by default", func(t *testing.T) {
		res := runCLI(t, "outline", bigFile)

		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		if !strings.Contains(res.Stdout, "top_marker") {
			t.Errorf("stdout missing %q:\n%s", "top_marker", res.Stdout)
		}
		if !strings.Contains(res.Stdout, "bottom_marker") {
			t.Errorf("stdout missing %q:\n%s", "bottom_marker", res.Stdout)
		}
		if strings.Contains(res.Stdout, "middle_marker") {
			t.Errorf("stdout should not contain the truncated-away %q:\n%s", "middle_marker", res.Stdout)
		}
		if !strings.Contains(res.Stdout, "TRUNCATED") {
			t.Errorf("stdout missing the truncation marker:\n%s", res.Stdout)
		}
	})

	t.Run("--json is never truncated", func(t *testing.T) {
		res := runCLI(t, "outline", bigFile, "--json")

		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		if got := strings.TrimSpace(res.Stdout); got != "[]" {
			t.Errorf("stdout = %q, want %q (no classes/functions in the fixture)", got, "[]")
		}
	})

	t.Run("--no-truncate prints the full skeleton", func(t *testing.T) {
		res := runCLI(t, "outline", bigFile, "--no-truncate")

		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		if !strings.Contains(res.Stdout, "middle_marker") {
			t.Errorf("stdout missing %q (should not be truncated with --no-truncate):\n%s", "middle_marker", res.Stdout)
		}
		if strings.Contains(res.Stdout, "TRUNCATED") {
			t.Errorf("stdout should not contain the truncation marker with --no-truncate:\n%s", res.Stdout)
		}
	})

	t.Run("small file is never truncated", func(t *testing.T) {
		res := runCLI(t, "outline", smallFile)

		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		if !strings.Contains(res.Stdout, "middle_marker") {
			t.Errorf("stdout missing %q:\n%s", "middle_marker", res.Stdout)
		}
		if strings.Contains(res.Stdout, "TRUNCATED") {
			t.Errorf("stdout should not contain the truncation marker for a small file:\n%s", res.Stdout)
		}
	})

	// Boundary case: a file whose skeleton has exactly truncationLimit (300)
	// lines must not be truncated, since truncateText's own threshold check
	// is "<= limit passes through unchanged" (see TestTruncateText_UnderLimit
	// in truncate_test.go). This exercises that boundary through the actual
	// `outline` command rather than the truncateText helper in isolation.
	t.Run("exactly 300 lines is not truncated", func(t *testing.T) {
		res := runCLI(t, "outline", exactFile)

		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		if got := strings.TrimRight(res.Stdout, "\n"); got != strings.TrimRight(exactSource, "\n") {
			t.Errorf("stdout was altered for an exactly-300-line file, want it unchanged:\ngot:\n%s\nwant:\n%s", got, exactSource)
		}
		if strings.Contains(res.Stdout, "TRUNCATED") {
			t.Errorf("stdout should not contain the truncation marker for an exactly-300-line file:\n%s", res.Stdout)
		}
	})
}

// TestSymbolCmd_Truncation exercises task 12's auto-truncation and
// --no-truncate opt-out for the `symbol` command. Unlike `outline`,
// `symbol`'s code truncation applies in both text and JSON mode (only the
// truncated/total_lines/limit_lines/message metadata fields are JSON-only).
func TestSymbolCmd_Truncation(t *testing.T) {
	tmp := t.TempDir()
	bigFile := filepath.Join(tmp, "big_function.py")
	writeFile(t, bigFile, bigPythonFunction(400))

	smallFile := filepath.Join(tmp, "small_function.py")
	writeFile(t, smallFile, bigPythonFunction(10))

	// bigPythonFunction(n) extracts to n+1 lines of symbol code (the "def
	// big_function():" line plus n body lines), so n=299 yields exactly
	// truncationLimit (300) total_lines.
	exactFile := filepath.Join(tmp, "exact_function.py")
	writeFile(t, exactFile, bigPythonFunction(truncationLimit-1))

	t.Run("text mode truncates by default", func(t *testing.T) {
		res := runCLI(t, "symbol", bigFile, "big_function")

		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		if !strings.Contains(res.Stdout, "top_marker") {
			t.Errorf("stdout missing %q:\n%s", "top_marker", res.Stdout)
		}
		if !strings.Contains(res.Stdout, "bottom_marker") {
			t.Errorf("stdout missing %q:\n%s", "bottom_marker", res.Stdout)
		}
		if strings.Contains(res.Stdout, "middle_marker") {
			t.Errorf("stdout should not contain the truncated-away %q:\n%s", "middle_marker", res.Stdout)
		}
		if !strings.Contains(res.Stdout, "TRUNCATED") {
			t.Errorf("stdout missing the truncation marker:\n%s", res.Stdout)
		}
	})

	t.Run("JSON mode truncates the code and adds truncation metadata", func(t *testing.T) {
		res := runCLI(t, "symbol", bigFile, "big_function", "--json")

		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}

		var payload map[string]any
		if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
			t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, res.Stdout)
		}

		code, _ := payload["code"].(string)
		if !strings.Contains(code, "top_marker") || !strings.Contains(code, "bottom_marker") {
			t.Errorf(`payload["code"] missing top/bottom markers:\n%s`, code)
		}
		if strings.Contains(code, "middle_marker") {
			t.Errorf(`payload["code"] should not contain the truncated-away "middle_marker":\n%s`, code)
		}

		if payload["truncated"] != true {
			t.Errorf(`payload["truncated"] = %v, want true`, payload["truncated"])
		}
		// The extracted symbol code is "def big_function():" plus its 400
		// body lines, with no trailing newline included in the extracted
		// byte range.
		if payload["total_lines"] != float64(401) {
			t.Errorf(`payload["total_lines"] = %v, want 401`, payload["total_lines"])
		}
		if payload["limit_lines"] != float64(300) {
			t.Errorf(`payload["limit_lines"] = %v, want 300`, payload["limit_lines"])
		}
		want := "Output truncated to protect context. Re-run with --no-truncate to get full output."
		if payload["message"] != want {
			t.Errorf(`payload["message"] = %v, want %q`, payload["message"], want)
		}
	})

	t.Run("--no-truncate prints the full code in both modes", func(t *testing.T) {
		textRes := runCLI(t, "symbol", bigFile, "big_function", "--no-truncate")
		if textRes.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", textRes.ExitCode, textRes.Stderr)
		}
		if !strings.Contains(textRes.Stdout, "middle_marker") {
			t.Errorf("text stdout missing %q with --no-truncate:\n%s", "middle_marker", textRes.Stdout)
		}
		if strings.Contains(textRes.Stdout, "TRUNCATED") {
			t.Errorf("text stdout should not contain the truncation marker with --no-truncate:\n%s", textRes.Stdout)
		}

		jsonRes := runCLI(t, "symbol", bigFile, "big_function", "--no-truncate", "--json")
		if jsonRes.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", jsonRes.ExitCode, jsonRes.Stderr)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(jsonRes.Stdout), &payload); err != nil {
			t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, jsonRes.Stdout)
		}
		code, _ := payload["code"].(string)
		if !strings.Contains(code, "middle_marker") {
			t.Errorf(`payload["code"] missing %q with --no-truncate:\n%s`, "middle_marker", code)
		}
		if _, present := payload["truncated"]; present {
			t.Errorf(`payload should not contain a "truncated" key with --no-truncate, got: %s`, jsonRes.Stdout)
		}
	})

	t.Run("small symbol is never truncated and JSON omits truncation keys", func(t *testing.T) {
		textRes := runCLI(t, "symbol", smallFile, "big_function")
		if textRes.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", textRes.ExitCode, textRes.Stderr)
		}
		if !strings.Contains(textRes.Stdout, "middle_marker") {
			t.Errorf("stdout missing %q:\n%s", "middle_marker", textRes.Stdout)
		}

		jsonRes := runCLI(t, "symbol", smallFile, "big_function", "--json")
		if jsonRes.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", jsonRes.ExitCode, jsonRes.Stderr)
		}
		for _, key := range []string{"truncated", "total_lines", "limit_lines", "message"} {
			if strings.Contains(jsonRes.Stdout, `"`+key+`"`) {
				t.Errorf("stdout should not contain the %q key for a non-truncated symbol:\n%s", key, jsonRes.Stdout)
			}
		}
	})

	// Boundary case: a symbol whose extracted code has exactly
	// truncationLimit (300) total_lines must not be truncated, since the
	// `symbol` command guards truncation with "totalLines > truncationLimit"
	// (strictly greater) rather than ">=". This exercises that boundary
	// through the actual `symbol` command, independent of
	// TestTruncateText_UnderLimit's coverage of the truncateText helper
	// itself.
	t.Run("exactly 300 total_lines is not truncated", func(t *testing.T) {
		textRes := runCLI(t, "symbol", exactFile, "big_function")
		if textRes.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", textRes.ExitCode, textRes.Stderr)
		}
		if !strings.Contains(textRes.Stdout, "middle_marker") {
			t.Errorf("stdout missing %q for an exactly-300-line symbol:\n%s", "middle_marker", textRes.Stdout)
		}
		if strings.Contains(textRes.Stdout, "TRUNCATED") {
			t.Errorf("stdout should not contain the truncation marker for an exactly-300-line symbol:\n%s", textRes.Stdout)
		}

		jsonRes := runCLI(t, "symbol", exactFile, "big_function", "--json")
		if jsonRes.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", jsonRes.ExitCode, jsonRes.Stderr)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(jsonRes.Stdout), &payload); err != nil {
			t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, jsonRes.Stdout)
		}
		code, _ := payload["code"].(string)
		if !strings.Contains(code, "middle_marker") {
			t.Errorf(`payload["code"] missing %q for an exactly-300-line symbol:\n%s`, "middle_marker", code)
		}
		for _, key := range []string{"truncated", "total_lines", "limit_lines", "message"} {
			if strings.Contains(jsonRes.Stdout, `"`+key+`"`) {
				t.Errorf("stdout should not contain the %q key for an exactly-300-line symbol:\n%s", key, jsonRes.Stdout)
			}
		}
	})
}

// TestCLI_UnknownCommand verifies that an unrecognized subcommand maps to
// exit code 2 (ExitCLIParamError), matching Python's typer/Click, which maps
// its UsageError-family exceptions (which an unknown command raises) to exit
// code 2. Before task 14, this fell through to ExitInternalError (1); see
// task 7's and task 1's "追記" notes in SPECS/tasks_GOLANG.md.
func TestCLI_UnknownCommand(t *testing.T) {
	res := runCLI(t, "frobnicate")
	if res.ExitCode != apperr.ExitCLIParamError {
		t.Fatalf("ExitCode = %d, want %d (ExitCLIParamError); stderr=%q", res.ExitCode, apperr.ExitCLIParamError, res.Stderr)
	}
	if !strings.HasPrefix(res.Stderr, "Error: ") {
		t.Errorf("stderr = %q, want it to start with %q", res.Stderr, "Error: ")
	}
}

// TestCLI_MissingRequiredArgs verifies that omitting a required positional
// argument maps to exit code 2 (ExitCLIParamError), for the same reason as
// TestCLI_UnknownCommand above.
func TestCLI_MissingRequiredArgs(t *testing.T) {
	res := runCLI(t, "symbol")
	if res.ExitCode != apperr.ExitCLIParamError {
		t.Fatalf("ExitCode = %d, want %d (ExitCLIParamError); stderr=%q", res.ExitCode, apperr.ExitCLIParamError, res.Stderr)
	}
	if !strings.HasPrefix(res.Stderr, "Error: ") {
		t.Errorf("stderr = %q, want it to start with %q", res.Stderr, "Error: ")
	}
}

// TestCLI_TooManyArgs verifies that supplying more positional arguments than
// a command accepts (cobra.ExactArgs/MaximumNArgs failing the other
// direction from TestCLI_MissingRequiredArgs) also maps to exit code 2.
func TestCLI_TooManyArgs(t *testing.T) {
	res := runCLI(t, "locate", "a.py", "Sym", "unexpected-extra-arg")
	if res.ExitCode != apperr.ExitCLIParamError {
		t.Fatalf("ExitCode = %d, want %d (ExitCLIParamError); stderr=%q", res.ExitCode, apperr.ExitCLIParamError, res.Stderr)
	}
}

// TestCLI_InvalidFlagValue verifies that an invalid value for a typed flag
// (--limit expects an int) is a cobra flag-parsing error, and so also maps
// to exit code 2, not the ExitInternalError (1) fallback it would receive
// without task 14's businessLogicReached-based classification (decideExitCode
// in exitcode.go).
func TestCLI_InvalidFlagValue(t *testing.T) {
	res := runCLI(t, "directory", ".", "--limit", "not-a-number")
	if res.ExitCode != apperr.ExitCLIParamError {
		t.Fatalf("ExitCode = %d, want %d (ExitCLIParamError); stderr=%q", res.ExitCode, apperr.ExitCLIParamError, res.Stderr)
	}
}

// TestDecideExitCode_Taxonomy is a focused unit test of decideExitCode
// covering every exit code in design_GOLANG.md's taxonomy, including the two
// codes (5: DatabaseCorruptedError, and the ExitInternalError fallback for a
// genuine unexpected error reached via a command's own business logic) that
// are impractical to trigger end-to-end through runCLI: DatabaseCorruptedError
// is only ever produced by internal/db's self-healing schema check (which,
// by design, recovers from every case it currently detects before any error
// reaches the CLI layer -- see internal/db/db.go's repairSchemaIfNeeded), and
// a "true" internal bug is by definition not something a real command
// currently returns.
func TestDecideExitCode_Taxonomy(t *testing.T) {
	businessLogicCmd := &cobra.Command{Use: "x"}
	if err := markBusinessLogicReached(businessLogicCmd, nil); err != nil {
		t.Fatalf("markBusinessLogicReached: %v", err)
	}

	preValidationCmd := &cobra.Command{Use: "x"}
	preValidationCmd.SetContext(context.Background())

	tests := []struct {
		name string
		cmd  *cobra.Command
		err  error
		want int
	}{
		{"nil error", businessLogicCmd, nil, apperr.ExitOK},
		{"FileAccessError", businessLogicCmd, &apperr.FileAccessError{Path: "x"}, apperr.ExitFileAccessError},
		{"SymbolNotFoundError", businessLogicCmd, &apperr.SymbolNotFoundError{FilePath: "x", SymbolPath: "y"}, apperr.ExitSymbolNotFoundError},
		{"DatabaseCorruptedError", businessLogicCmd, &apperr.DatabaseCorruptedError{Path: "x"}, apperr.ExitDatabaseCorruptedError},
		{
			name: "cobra validation error (business logic never reached)",
			cmd:  preValidationCmd,
			err:  errors.New(`unknown command "frobnicate" for "ast-digger"`),
			want: apperr.ExitCLIParamError,
		},
		{
			name: "unexpected internal error reached via business logic",
			cmd:  businessLogicCmd,
			err:  errors.New("boom: something truly unexpected"),
			want: apperr.ExitInternalError,
		},
		{
			name: "apperr error takes priority over the businessLogicReached check",
			cmd:  preValidationCmd,
			err:  &apperr.SymbolNotFoundError{FilePath: "x", SymbolPath: "y"},
			want: apperr.ExitSymbolNotFoundError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decideExitCode(tt.cmd, tt.err); got != tt.want {
				t.Errorf("decideExitCode(...) = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestSymbolCmd_FollowImports_SingleLanguage verifies that running `symbol --follow-imports`
// on a Python file extracts the dependent symbol in both text and JSON modes.
func TestSymbolCmd_FollowImports_SingleLanguage(t *testing.T) {
	orig, _ := os.Getwd()
	absProjDir, _ := filepath.Abs("../../testdata/python/importproj")
	_ = os.Chdir(absProjDir)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// 1. Text mode with --follow-imports
	resText := runCLI(t, "symbol", "main.py", "compute", "--follow-imports")
	if resText.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", resText.ExitCode, resText.Stderr)
	}
	if !strings.Contains(resText.Stdout, "def compute():") {
		t.Errorf("stdout missing main code 'def compute():':\n%s", resText.Stdout)
	}
	if !strings.Contains(resText.Stdout, "--- Dependency: ") || !strings.Contains(resText.Stdout, "def add") {
		t.Errorf("stdout missing dependency code header / def add:\n%s", resText.Stdout)
	}

	// 2. JSON mode with --follow-imports
	resJSON := runCLI(t, "symbol", "main.py", "compute", "--follow-imports", "--json")
	if resJSON.ExitCode != apperr.ExitOK {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", resJSON.ExitCode, resJSON.Stderr)
	}
	var payload struct {
		Name         string         `json:"name"`
		Dependencies []symbolResult `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(resJSON.Stdout), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%s", err, resJSON.Stdout)
	}
	if payload.Name != "compute" {
		t.Errorf("payload.Name = %q, want 'compute'", payload.Name)
	}
	if len(payload.Dependencies) == 0 {
		t.Fatalf("payload.Dependencies is empty, expected at least 1 dependency")
	}
	foundAdd := false
	for _, dep := range payload.Dependencies {
		if dep.Name == "add" {
			foundAdd = true
		}
	}
	if !foundAdd {
		t.Errorf("expected 'add' in dependencies array: %+v", payload.Dependencies)
	}
}

// TestSymbolCmd_FollowImports_MultiLanguage verifies that `symbol --follow-imports --json`
// works across Python, TypeScript, Go, Java, and JavaScript.
func TestSymbolCmd_FollowImports_MultiLanguage(t *testing.T) {
	tmp := t.TempDir()

	// Python
	writeFile(t, filepath.Join(tmp, "py", "dep.py"), "def helper():\n    return 42\n")
	writeFile(t, filepath.Join(tmp, "py", "main.py"), "from dep import helper\n\ndef main_fn():\n    return helper()\n")

	// TypeScript
	writeFile(t, filepath.Join(tmp, "ts", "dep.ts"), "export function helper() { return 42; }\n")
	writeFile(t, filepath.Join(tmp, "ts", "main.ts"), "import { helper } from './dep';\nexport function main_fn() { return helper(); }\n")

	// Go
	writeFile(t, filepath.Join(tmp, "go", "dep", "dep.go"), "package dep\nfunc Helper() int { return 42 }\n")
	writeFile(t, filepath.Join(tmp, "go", "main.go"), "package main\nimport \"./dep\"\nfunc MainFn() int { return dep.Helper() }\n")

	// Java
	writeFile(t, filepath.Join(tmp, "java", "com", "example", "Dep.java"), "package com.example;\npublic class Dep {\n    public static int helper() { return 42; }\n}\n")
	writeFile(t, filepath.Join(tmp, "java", "com", "example", "Main.java"), "package com.example;\nimport com.example.Dep;\npublic class Main {\n    public static int mainFn() { return Dep.helper(); }\n}\n")

	// JavaScript
	writeFile(t, filepath.Join(tmp, "js", "dep.js"), "export function helper() { return 42; }\n")
	writeFile(t, filepath.Join(tmp, "js", "main.js"), "import { helper } from './dep';\nexport function main_fn() { return helper(); }\n")

	// Rust
	writeFile(t, filepath.Join(tmp, "rs", "dep.rs"), "pub fn helper() -> i32 { 42 }\n")
	writeFile(t, filepath.Join(tmp, "rs", "main.rs"), "mod dep;\nuse dep::helper;\npub fn main_fn() -> i32 { helper() }\n")

	tests := []struct {
		lang       string
		dir        string
		filePath   string
		symbolName string
		depName    string
	}{
		{lang: "python", dir: filepath.Join(tmp, "py"), filePath: "main.py", symbolName: "main_fn", depName: "helper"},
		{lang: "typescript", dir: filepath.Join(tmp, "ts"), filePath: "main.ts", symbolName: "main_fn", depName: "helper"},
		{lang: "go", dir: filepath.Join(tmp, "go"), filePath: "main.go", symbolName: "MainFn", depName: "Helper"},
		{lang: "java", dir: filepath.Join(tmp, "java"), filePath: filepath.Join("com", "example", "Main.java"), symbolName: "Main.mainFn", depName: "Dep"},
		{lang: "javascript", dir: filepath.Join(tmp, "js"), filePath: "main.js", symbolName: "main_fn", depName: "helper"},
		{lang: "rust", dir: filepath.Join(tmp, "rs"), filePath: "main.rs", symbolName: "main_fn", depName: "helper"},
	}

	orig, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(orig) })

	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			_ = os.Chdir(tt.dir)
			res := runCLI(t, "symbol", tt.filePath, tt.symbolName, "--follow-imports", "--json")
			if res.ExitCode != apperr.ExitOK {
				t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
			}
			var payload struct {
				Dependencies []symbolResult `json:"dependencies"`
			}
			if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
				t.Fatalf("failed to unmarshal JSON: %v\nstdout=%s", err, res.Stdout)
			}
			found := false
			for _, dep := range payload.Dependencies {
				if dep.Name == tt.depName {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s: expected dependency %q, got: %+v", tt.lang, tt.depName, payload.Dependencies)
			}
		})
	}
}

// TestSymbolCmd_FollowImports_EdgeCases verifies circular import termination
// and safe skipping of unresolvable/stdlib imports via the CLI.
func TestSymbolCmd_FollowImports_EdgeCases(t *testing.T) {
	tmp := t.TempDir()

	// Circular import
	writeFile(t, filepath.Join(tmp, "circ_a.py"), "from circ_b import func_b\n\ndef func_a():\n    return func_b()\n")
	writeFile(t, filepath.Join(tmp, "circ_b.py"), "from circ_a import func_a\n\ndef func_b():\n    return func_a()\n")

	// Stdlib import
	writeFile(t, filepath.Join(tmp, "stdlib.py"), "import os\n\ndef my_func():\n    return os.getcwd()\n")

	orig, _ := os.Getwd()
	_ = os.Chdir(tmp)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	t.Run("circular_import", func(t *testing.T) {
		res := runCLI(t, "symbol", "circ_a.py", "func_a", "--follow-imports", "--json")
		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		var payload struct {
			Dependencies []symbolResult `json:"dependencies"`
		}
		if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
			t.Fatalf("failed to unmarshal JSON: %v\nstdout=%s", err, res.Stdout)
		}
		if len(payload.Dependencies) != 1 || payload.Dependencies[0].Name != "func_b" {
			t.Errorf("expected 1 dependency 'func_b', got %+v", payload.Dependencies)
		}
	})

	t.Run("stdlib_import", func(t *testing.T) {
		res := runCLI(t, "symbol", "stdlib.py", "my_func", "--follow-imports", "--json")
		if res.ExitCode != apperr.ExitOK {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
		}
		var payload struct {
			Dependencies []symbolResult `json:"dependencies"`
		}
		if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
			t.Fatalf("failed to unmarshal JSON: %v\nstdout=%s", err, res.Stdout)
		}
		if len(payload.Dependencies) != 0 {
			t.Errorf("expected 0 dependencies, got %d", len(payload.Dependencies))
		}
	})
}
