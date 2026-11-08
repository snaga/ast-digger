package impact

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/snaga/ast-digger/internal/parser"
)

func TestCleanGitDiffPath(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "a prefix", raw: "a/pkg/mod.go", want: "pkg/mod.go"},
		{name: "b prefix", raw: "b/pkg/mod.go", want: "pkg/mod.go"},
		{name: "no prefix", raw: "pkg/mod.go", want: "pkg/mod.go"},
		{name: "quoted path", raw: "\"a/pkg/my file.go\"", want: "pkg/my file.go"},
		{name: "quoted path without prefix", raw: "\"pkg/my file.go\"", want: "pkg/my file.go"},
		{name: "quoted with escape", raw: "\"a/pkg/quote\\\"file.go\"", want: "pkg/quote\"file.go"},
		{name: "windows backslash", raw: "a/pkg\\mod.go", want: "pkg/mod.go"},
		{name: "whitespace padded", raw: "  a/pkg/mod.go  ", want: "pkg/mod.go"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanGitDiffPath(tt.raw)
			if got != tt.want {
				t.Errorf("cleanGitDiffPath(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestCleanGitStatusPath(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "standard path", raw: "pkg/mod.go", want: "pkg/mod.go"},
		{name: "quoted path", raw: "\"pkg/spaced file.go\"", want: "pkg/spaced file.go"},
		{name: "quoted path with escape", raw: "\"pkg/file\\\"name.go\"", want: "pkg/file\"name.go"},
		{name: "whitespace padded", raw: "  pkg/mod.go  ", want: "pkg/mod.go"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanGitStatusPath(tt.raw)
			if got != tt.want {
				t.Errorf("cleanGitStatusPath(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseDiffGitLine(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		wantOldPath string
		wantNewPath string
	}{
		{
			name:        "standard diff line",
			line:        "diff --git a/pkg/mod.go b/pkg/mod.go",
			wantOldPath: "pkg/mod.go",
			wantNewPath: "pkg/mod.go",
		},
		{
			name:        "quoted paths with spaces",
			line:        "diff --git \"a/my dir/file.go\" \"b/my dir/file.go\"",
			wantOldPath: "my dir/file.go",
			wantNewPath: "my dir/file.go",
		},
		{
			name:        "quoted new path only",
			line:        "diff --git a/old.go \"b/new dir/file.go\"",
			wantOldPath: "old.go",
			wantNewPath: "new dir/file.go",
		},
		{
			name:        "invalid line without b/",
			line:        "diff --git a/foo.go",
			wantOldPath: "",
			wantNewPath: "",
		},
		{
			name:        "not a diff line",
			line:        "index 1234..5678 100644",
			wantOldPath: "",
			wantNewPath: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldPath, newPath := parseDiffGitLine(tt.line)
			if oldPath != tt.wantOldPath || newPath != tt.wantNewPath {
				t.Errorf("parseDiffGitLine(%q) = (%q, %q), want (%q, %q)",
					tt.line, oldPath, newPath, tt.wantOldPath, tt.wantNewPath)
			}
		})
	}
}

func TestCountLines(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		want    int
	}{
		{name: "empty content", content: []byte{}, want: 0},
		{name: "nil content", content: nil, want: 0},
		{name: "single line with newline", content: []byte("line1\n"), want: 1},
		{name: "single line without newline", content: []byte("line1"), want: 1},
		{name: "multiple lines trailing newline", content: []byte("line1\nline2\nline3\n"), want: 3},
		{name: "multiple lines no trailing newline", content: []byte("line1\nline2\nline3"), want: 3},
		{name: "crlf newlines", content: []byte("line1\r\nline2\r\n"), want: 2},
		{name: "only newlines", content: []byte("\n\n\n"), want: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countLines(tt.content)
			if got != tt.want {
				t.Errorf("countLines() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRunGit_Errors(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Invalid git command (exit error with stderr)
	_, err := runGit(tempDir, "invalid-command-xyz")
	if err == nil {
		t.Fatalf("expected error for invalid git subcommand, got nil")
	}

	// 2. Non-existent working directory
	nonExistent := filepath.Join(tempDir, "no_such_directory_12345")
	_, err = runGit(nonExistent, "status")
	if err == nil {
		t.Fatalf("expected error for non-existent directory, got nil")
	}
}

func TestIsFunctionLiteral(t *testing.T) {
	positives := []string{
		"func_literal",
		"arrow_function",
		"function_expression",
		"lambda",
		"closure_expression",
	}
	for _, kind := range positives {
		if !isFunctionLiteral(kind) {
			t.Errorf("isFunctionLiteral(%q) = false, want true", kind)
		}
	}

	negatives := []string{
		"function_declaration",
		"call_expression",
		"identifier",
	}
	for _, kind := range negatives {
		if isFunctionLiteral(kind) {
			t.Errorf("isFunctionLiteral(%q) = true, want false", kind)
		}
	}
}

func TestIsSimpleStatement(t *testing.T) {
	positives := []string{
		"expression_statement",
		"return_statement",
		"return_expression",
		"assignment_statement",
		"short_var_declaration",
		"inc_dec_statement",
		"variable_statement",
		"lexical_declaration",
		"let_declaration",
		"var_declaration",
		"const_declaration",
		"type_declaration",
		"type_alias_declaration",
		"import_statement",
		"import_from_statement",
		"import_declaration",
		"use_declaration",
		"package_clause",
		"package_declaration",
		"export_statement",
		"throw_statement",
		"raise_statement",
		"assert_statement",
		"break_statement",
		"continue_statement",
		"pass_statement",
		"yield_statement",
		"branch_statement",
		"defer_statement",
		"go_statement",
		"send_statement",
		"macro_invocation",
		"local_variable_declaration",
		"field_declaration",
		"custom_statement",
		"custom_declaration",
	}
	for _, kind := range positives {
		if !isSimpleStatement(kind) {
			t.Errorf("isSimpleStatement(%q) = false, want true", kind)
		}
	}

	negatives := []string{
		"empty_statement",
		"if_statement_node",
		"block",
		"identifier",
	}
	for _, kind := range negatives {
		if isSimpleStatement(kind) {
			t.Errorf("isSimpleStatement(%q) = true, want false", kind)
		}
	}
}

func TestIsOnlyPunctuationOrWhitespace(t *testing.T) {
	if !isOnlyPunctuationOrWhitespace("") {
		t.Errorf("empty string should be true")
	}
	if !isOnlyPunctuationOrWhitespace("   \t\n  ") {
		t.Errorf("whitespace only should be true")
	}
	if !isOnlyPunctuationOrWhitespace("{};(),[]") {
		t.Errorf("punctuation only should be true")
	}
	if isOnlyPunctuationOrWhitespace("x") {
		t.Errorf("identifier should be false")
	}
	if isOnlyPunctuationOrWhitespace("{ x }") {
		t.Errorf("identifier inside brackets should be false")
	}
}

func TestAnalyzer_FunctionLiteralsAndLambdas(t *testing.T) {
	// JavaScript with arrow function and function expression
	jsSrc := []byte(`
const add = (a, b) => {
	if (a > 0) {
		return a + b;
	}
	return b;
};

const sub = function(a, b) {
	return a - b;
};
`)

	impactJS, err := AnalyzeFileSource("calc.js", ".js", []byte{}, jsSrc)
	if err != nil {
		t.Fatalf("AnalyzeFileSource for js failed: %v", err)
	}
	if impactJS.Statements.Added <= 0 {
		t.Errorf("expected added statements in JS arrow functions, got %d", impactJS.Statements.Added)
	}
	if impactJS.ComplexityDelta <= 0 {
		t.Errorf("expected positive complexity due to if-branch, got %d", impactJS.ComplexityDelta)
	}

	// Python with lambda
	pySrc := []byte(`
double = lambda x: x * 2
items = list(map(lambda x: x + 1, [1, 2, 3]))
`)
	impactPy, err := AnalyzeFileSource("calc.py", ".py", []byte{}, pySrc)
	if err != nil {
		t.Fatalf("AnalyzeFileSource for py failed: %v", err)
	}
	if impactPy.Statements.Added <= 0 {
		t.Errorf("expected added statements in Python lambdas, got %d", impactPy.Statements.Added)
	}

	// Unsupported extension error
	_, err = AnalyzeFileSource("unknown.xyz", ".xyz", []byte{}, []byte{})
	if err == nil {
		t.Errorf("expected error for unsupported extension, got nil")
	}

	// analyze with filePath without ext argument
	impactNoExtArg, err := AnalyzeFileSource("calc.py", "", []byte{}, pySrc)
	if err != nil || impactNoExtArg == nil {
		t.Fatalf("AnalyzeFileSource without ext argument failed: %v", err)
	}
}

func TestTraverseNode_NilOrUnnamed(t *testing.T) {
	var stats astStats
	// Nil node should not panic
	traverseNode(nil, []byte(""), false, &stats)
	if stats.Statements != 0 || stats.Complexity != 0 {
		t.Errorf("traverseNode(nil) modified stats: %+v", stats)
	}
}

func TestFormatMissingDoc(t *testing.T) {
	if got := formatMissingDoc("main.go", "Hello"); got != "main.go::Hello" {
		t.Errorf("formatMissingDoc with file = %q, want %q", got, "main.go::Hello")
	}
	if got := formatMissingDoc("", "Hello"); got != "Hello" {
		t.Errorf("formatMissingDoc without file = %q, want %q", got, "Hello")
	}
}

func TestExtractSymbolBytes(t *testing.T) {
	src := []byte("line1\nline2\nline3\nline4\nline5")

	tests := []struct {
		name      string
		src       []byte
		startLine int
		endLine   int
		want      []byte
	}{
		{name: "empty source", src: nil, startLine: 1, endLine: 2, want: nil},
		{name: "invalid startLine <= 0", src: src, startLine: 0, endLine: 2, want: nil},
		{name: "endLine < startLine", src: src, startLine: 3, endLine: 2, want: nil},
		{name: "startLine > total lines", src: src, startLine: 10, endLine: 12, want: nil},
		{name: "normal range", src: src, startLine: 2, endLine: 4, want: []byte("line2\nline3\nline4")},
		{name: "clamp endLine > total lines", src: src, startLine: 4, endLine: 10, want: []byte("line4\nline5")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSymbolBytes(tt.src, tt.startLine, tt.endLine)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("extractSymbolBytes() = %q, want %q", string(got), string(tt.want))
			}
		})
	}
}

func TestCountSymbolStatements(t *testing.T) {
	goLang, err := GetLanguage(".go")
	if err != nil {
		t.Fatalf("GetLanguage failed: %v", err)
	}

	src := []byte(`package main

func Add(a, b int) int {
	res := a + b
	return res
}
`)

	if got := countSymbolStatements(nil, 1, 5, goLang); got != 0 {
		t.Errorf("nil source should return 0, got %d", got)
	}
	if got := countSymbolStatements(src, 1, 5, nil); got != 0 {
		t.Errorf("nil language should return 0, got %d", got)
	}
	if got := countSymbolStatements(src, 10, 5, goLang); got != 0 {
		t.Errorf("invalid line range should return 0, got %d", got)
	}

	stmts := countSymbolStatements(src, 3, 6, goLang)
	if stmts <= 0 {
		t.Errorf("valid function snippet should have statements > 0, got %d", stmts)
	}
}

// TestDetermineVisibility_AllLanguages verifies the language-specific visibility detection logic.
func TestDetermineVisibility_AllLanguages(t *testing.T) {
	makeSym := func(name, sig string) parser.SymbolData {
		var s *string
		if sig != "" {
			s = &sig
		}
		return parser.SymbolData{
			Name:      name,
			Signature: s,
		}
	}

	tests := []struct {
		name     string
		file     string
		sym      parser.SymbolData
		expected string
	}{
		// Go
		{name: "go public", file: "foo.go", sym: makeSym("Exported", ""), expected: "public"},
		{name: "go private", file: "foo.go", sym: makeSym("unexported", ""), expected: "private"},
		{name: "go empty name", file: "foo.go", sym: makeSym("", ""), expected: "private"},

		// Python
		{name: "py public", file: "foo.py", sym: makeSym("my_func", ""), expected: "public"},
		{name: "py private", file: "foo.py", sym: makeSym("_my_private", ""), expected: "private"},

		// TypeScript / JavaScript
		{name: "ts private #", file: "foo.ts", sym: makeSym("#privateField", ""), expected: "private"},
		{name: "ts private _", file: "foo.tsx", sym: makeSym("_privateFunc", ""), expected: "private"},
		{name: "ts private sig keyword", file: "foo.js", sym: makeSym("method", "private doWork()"), expected: "private"},
		{name: "ts public", file: "foo.jsx", sym: makeSym("method", "public doWork()"), expected: "public"},

		// Java
		{name: "java public", file: "Foo.java", sym: makeSym("run", "public void run()"), expected: "public"},
		{name: "java private", file: "Foo.java", sym: makeSym("run", "private void run()"), expected: "private"},
		{name: "java package-private", file: "Foo.java", sym: makeSym("run", "void run()"), expected: "private"},

		// Rust
		{name: "rust pub", file: "lib.rs", sym: makeSym("foo", "pub fn foo()"), expected: "public"},
		{name: "rust pub(crate)", file: "lib.rs", sym: makeSym("foo", "pub(crate) fn foo()"), expected: "public"},
		{name: "rust private", file: "lib.rs", sym: makeSym("foo", "fn foo()"), expected: "private"},

		// Markdown
		{name: "markdown heading", file: "README.md", sym: makeSym("Header", ""), expected: "private"},
		{name: "markdown doc", file: "guide.markdown", sym: makeSym("Section", ""), expected: "private"},

		// Default
		{name: "other lang", file: "query.sql", sym: makeSym("tbl", ""), expected: "public"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := determineVisibility(tt.file, tt.sym)
			if got != tt.expected {
				t.Errorf("determineVisibility(%q, %q) = %q, want %q",
					tt.file, tt.sym.Name, got, tt.expected)
			}
		})
	}
}

func TestCalculateDocAudit_DeletedAndCoverage(t *testing.T) {
	impacts := []*SymbolImpact{
		{
			SymbolPath:   "DeletedFunc",
			ChangeType:   "deleted",
			Visibility:   "public",
			HasDocstring: false,
		},
		{
			SymbolPath:   "PublicWithDoc",
			ChangeType:   "modified",
			Visibility:   "public",
			HasDocstring: true,
		},
		{
			SymbolPath:   "PublicWithoutDoc",
			ChangeType:   "added",
			Visibility:   "public",
			HasDocstring: false,
		},
		{
			SymbolPath:   "PrivateWithoutDoc",
			ChangeType:   "added",
			Visibility:   "private",
			HasDocstring: false,
		},
	}

	// DocAuditExported: only public missing documented
	resExp := calculateDocAudit(impacts, "main.go", DocAuditExported)
	if resExp.PublicCoverage != 0.5 {
		t.Errorf("PublicCoverage = %f, want 0.5", resExp.PublicCoverage)
	}
	if len(resExp.MissingDocstrings) != 1 || resExp.MissingDocstrings[0] != "main.go::PublicWithoutDoc" {
		t.Errorf("MissingDocstrings = %v, want ['main.go::PublicWithoutDoc']", resExp.MissingDocstrings)
	}

	// DocAuditAll: both public and private missing documented
	resAll := calculateDocAudit(impacts, "main.go", DocAuditAll)
	if len(resAll.MissingDocstrings) != 2 {
		t.Errorf("DocAuditAll missing len = %d, want 2 (%v)", len(resAll.MissingDocstrings), resAll.MissingDocstrings)
	}

	// Empty impacts
	resEmpty := calculateDocAudit(nil, "main.go", DocAuditNone)
	if resEmpty.PublicCoverage != 1.0 || resEmpty.PrivateCoverage != 1.0 {
		t.Errorf("empty impacts coverage should be 1.0, got (%f, %f)", resEmpty.PublicCoverage, resEmpty.PrivateCoverage)
	}
}
