package parser

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snaga/ast-digger/internal/apperr"
)

const (
	sampleFile      = "../../testdata/python/sample.py"
	syntaxErrorFile = "../../testdata/python/syntax_error.py"

	importProjDir = "../../testdata/python/importproj"
	referencesDir = "../../testdata/python/references"
)

// chdirTest changes the process working directory to dir (resolved relative
// to the test's original working directory) for the duration of t, restoring
// the original working directory via t.Cleanup. This is needed to exercise
// ResolveImports' absolute-import resolution, which (like Python's
// resolve_imports) resolves absolute imports relative to the process's
// current working directory ("project root").
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

// deref is a nil-safe dereference helper that keeps the table declarations
// below readable.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// findSymbol returns the first symbol in syms whose Name matches name, or
// nil if none is found.
func findSymbol(syms []SymbolData, name string) *SymbolData {
	for i := range syms {
		if syms[i].Name == name {
			return &syms[i]
		}
	}
	return nil
}

func TestPythonParser_ParseFile(t *testing.T) {
	p := NewPythonParser()
	syms, err := p.ParseFile(sampleFile)
	if err != nil {
		t.Fatalf("ParseFile returned unexpected error: %v", err)
	}

	tests := []struct {
		name          string
		symbolName    string
		wantKind      string
		wantParent    string
		wantStartLine int
		wantEndLine   int
		wantSignature string
		wantDocstring string
	}{
		{
			name:          "top-level function",
			symbolName:    "top_level_function",
			wantKind:      "function",
			wantParent:    "",
			wantStartLine: 4,
			wantEndLine:   6,
			wantSignature: "def top_level_function(x: int) -> int",
			wantDocstring: "Return x doubled.",
		},
		{
			name:          "class",
			symbolName:    "Greeter",
			wantKind:      "class",
			wantParent:    "",
			wantStartLine: 9,
			wantEndLine:   19,
			wantSignature: "class Greeter",
			wantDocstring: "A friendly greeter.",
		},
		{
			name:          "method without decorator",
			symbolName:    "__init__",
			wantKind:      "method",
			wantParent:    "Greeter",
			wantStartLine: 12,
			wantEndLine:   14,
			wantSignature: "def __init__(self, name: str) -> None",
			wantDocstring: "Store the name.",
		},
		{
			name:          "decorated method",
			symbolName:    "shout",
			wantKind:      "method",
			wantParent:    "Greeter",
			wantStartLine: 17,
			wantEndLine:   19,
			wantSignature: "@staticmethod\ndef shout(message: str) -> str",
			wantDocstring: "Shout a message.",
		},
		{
			name:          "decorated top-level function without docstring",
			symbolName:    "legacy_function",
			wantKind:      "function",
			wantParent:    "",
			wantStartLine: 23,
			wantEndLine:   24,
			wantSignature: "@deprecated\ndef legacy_function() -> None",
			wantDocstring: "",
		},
		{
			name:          "single-quoted docstring",
			symbolName:    "single_quote_docstring_function",
			wantKind:      "function",
			wantParent:    "",
			wantStartLine: 27,
			wantEndLine:   29,
			wantSignature: "def single_quote_docstring_function() -> str",
			wantDocstring: "Uses single quotes.",
		},
		{
			name:          "double-quoted docstring",
			symbolName:    "double_quote_docstring_function",
			wantKind:      "function",
			wantParent:    "",
			wantStartLine: 32,
			wantEndLine:   34,
			wantSignature: "def double_quote_docstring_function() -> str",
			wantDocstring: "Uses double quotes.",
		},
		{
			name:          "triple-single-quoted docstring",
			symbolName:    "triple_single_quote_docstring_function",
			wantKind:      "function",
			wantParent:    "",
			wantStartLine: 37,
			wantEndLine:   39,
			wantSignature: "def triple_single_quote_docstring_function() -> str",
			wantDocstring: "Uses triple single quotes.",
		},
		{
			// The function's first statement is an expression_statement
			// whose child is an assignment, not a string literal. This
			// exercises the getDocstring branch that rejects non-string
			// first statements (distinct from the pass_statement case
			// covered by legacy_function above).
			name:          "non-string first statement has no docstring",
			symbolName:    "non_string_first_statement_function",
			wantKind:      "function",
			wantParent:    "",
			wantStartLine: 42,
			wantEndLine:   44,
			wantSignature: "def non_string_first_statement_function() -> None",
			wantDocstring: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sym := findSymbol(syms, tt.symbolName)
			if sym == nil {
				t.Fatalf("symbol %q not found in ParseFile results", tt.symbolName)
			}
			if sym.Kind != tt.wantKind {
				t.Errorf("Kind = %q, want %q", sym.Kind, tt.wantKind)
			}
			if deref(sym.Parent) != tt.wantParent {
				t.Errorf("Parent = %q, want %q", deref(sym.Parent), tt.wantParent)
			}
			if sym.StartLine != tt.wantStartLine {
				t.Errorf("StartLine = %d, want %d", sym.StartLine, tt.wantStartLine)
			}
			if sym.EndLine != tt.wantEndLine {
				t.Errorf("EndLine = %d, want %d", sym.EndLine, tt.wantEndLine)
			}
			if deref(sym.Signature) != tt.wantSignature {
				t.Errorf("Signature = %q, want %q", deref(sym.Signature), tt.wantSignature)
			}
			if deref(sym.Docstring) != tt.wantDocstring {
				t.Errorf("Docstring = %q, want %q", deref(sym.Docstring), tt.wantDocstring)
			}
			if sym.FilePath == nil || *sym.FilePath != sampleFile {
				t.Errorf("FilePath = %v, want %q", sym.FilePath, sampleFile)
			}
		})
	}
}

// TestPythonParser_ParseFile_SyntaxError verifies that ParseFile does not
// return an error for a file containing syntax errors, and that it still
// returns the symbols tree-sitter was able to recover around the error
// (stderr warning output is not asserted here).
func TestPythonParser_ParseFile_SyntaxError(t *testing.T) {
	p := NewPythonParser()
	syms, err := p.ParseFile(syntaxErrorFile)
	if err != nil {
		t.Fatalf("ParseFile returned unexpected error for a file with syntax errors: %v", err)
	}
	if len(syms) == 0 {
		t.Fatalf("ParseFile returned no symbols for a file with syntax errors; want at least a partial result")
	}

	valid := findSymbol(syms, "valid_function")
	if valid == nil {
		t.Fatalf("expected valid_function (defined before the syntax error) to be recovered, got: %+v", syms)
	}
	if valid.Kind != "function" {
		t.Errorf("valid_function.Kind = %q, want %q", valid.Kind, "function")
	}
	if valid.StartLine != 1 || valid.EndLine != 3 {
		t.Errorf("valid_function lines = [%d,%d], want [1,3]", valid.StartLine, valid.EndLine)
	}
	if deref(valid.Docstring) != "A valid function before the error." {
		t.Errorf("valid_function.Docstring = %q, want %q", deref(valid.Docstring), "A valid function before the error.")
	}
}

func TestPythonParser_ParseFile_FileNotFound(t *testing.T) {
	p := NewPythonParser()
	_, err := p.ParseFile("../../testdata/python/does_not_exist.py")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestPythonParser_ParseFile_NoSymbols exercises ParseFile against a file
// with no classes/functions, guarding against a regression where ParseFile's
// symbols slice started out nil and (never being appended to) was returned
// as nil, which encoding/json marshals as "null" rather than "[]" -- unlike
// Python's `json.dumps([])`, which always produces "[]".
func TestPythonParser_ParseFile_NoSymbols(t *testing.T) {
	dir := t.TempDir()
	noSymbolsFile := filepath.Join(dir, "no_symbols.py")
	if err := os.WriteFile(noSymbolsFile, []byte("x = 1\ny = 2\n"), 0o644); err != nil {
		t.Fatalf("failed to write test fixture: %v", err)
	}

	p := NewPythonParser()
	syms, err := p.ParseFile(noSymbolsFile)
	if err != nil {
		t.Fatalf("ParseFile returned unexpected error: %v", err)
	}
	if len(syms) != 0 {
		t.Fatalf("ParseFile(%q) = %+v, want no symbols", noSymbolsFile, syms)
	}
	if syms == nil {
		t.Fatalf("ParseFile(%q) returned a nil slice; want a non-nil empty slice so it marshals to JSON as []", noSymbolsFile)
	}

	data, err := json.Marshal(syms)
	if err != nil {
		t.Fatalf("json.Marshal(syms) failed: %v", err)
	}
	if got := string(data); got != "[]" {
		t.Errorf("json.Marshal(syms) = %q, want %q", got, "[]")
	}
}

func TestPythonParser_ExtractSymbol(t *testing.T) {
	p := NewPythonParser()

	tests := []struct {
		name          string
		symbolPath    string
		wantKind      string
		wantParent    string
		wantStartLine int
		wantEndLine   int
		wantCode      string
	}{
		{
			name:          "whole class",
			symbolPath:    "Greeter",
			wantKind:      "class",
			wantParent:    "",
			wantStartLine: 9,
			wantEndLine:   19,
			wantCode: "class Greeter:\n" +
				`    """A friendly greeter."""` + "\n" +
				"\n" +
				"    def __init__(self, name: str) -> None:\n" +
				`        """Store the name."""` + "\n" +
				"        self.name = name\n" +
				"\n" +
				"    @staticmethod\n" +
				"    def shout(message: str) -> str:\n" +
				`        """Shout a message."""` + "\n" +
				"        return message.upper()",
		},
		{
			name:       "decorated method",
			symbolPath: "Greeter.shout",
			wantKind:   "method",
			wantParent: "Greeter",
			// StartLine covers the decorated_definition node (including the
			// @staticmethod decorator line), matching Python's
			// extract_symbol behavior where found_node's own start/end are
			// reported when the match is a decorated_definition.
			wantStartLine: 16,
			wantEndLine:   19,
			wantCode: "@staticmethod\n" +
				"    def shout(message: str) -> str:\n" +
				`        """Shout a message."""` + "\n" +
				"        return message.upper()",
		},
		{
			name:          "top-level function",
			symbolPath:    "top_level_function",
			wantKind:      "function",
			wantParent:    "",
			wantStartLine: 4,
			wantEndLine:   6,
			wantCode: "def top_level_function(x: int) -> int:\n" +
				`    """Return x doubled."""` + "\n" +
				"    return x * 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sym, err := p.ExtractSymbol(sampleFile, tt.symbolPath)
			if err != nil {
				t.Fatalf("ExtractSymbol(%q) returned unexpected error: %v", tt.symbolPath, err)
			}
			if sym.Kind != tt.wantKind {
				t.Errorf("Kind = %q, want %q", sym.Kind, tt.wantKind)
			}
			if deref(sym.Parent) != tt.wantParent {
				t.Errorf("Parent = %q, want %q", deref(sym.Parent), tt.wantParent)
			}
			if sym.StartLine != tt.wantStartLine {
				t.Errorf("StartLine = %d, want %d", sym.StartLine, tt.wantStartLine)
			}
			if sym.EndLine != tt.wantEndLine {
				t.Errorf("EndLine = %d, want %d", sym.EndLine, tt.wantEndLine)
			}
			if sym.Code == nil || *sym.Code != tt.wantCode {
				t.Errorf("Code = %q, want %q", deref(sym.Code), tt.wantCode)
			}
			if sym.FilePath == nil || *sym.FilePath != sampleFile {
				t.Errorf("FilePath = %v, want %q", sym.FilePath, sampleFile)
			}
		})
	}
}

func TestPythonParser_ExtractSymbol_NotFound(t *testing.T) {
	p := NewPythonParser()

	tests := []struct {
		name       string
		symbolPath string
	}{
		{name: "unknown top-level symbol", symbolPath: "NoSuchClass"},
		{name: "unknown method on known class", symbolPath: "Greeter.nonexistent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.ExtractSymbol(sampleFile, tt.symbolPath)
			if err == nil {
				t.Fatalf("expected an error for symbol path %q, got nil", tt.symbolPath)
			}
			var symErr *apperr.SymbolNotFoundError
			if !errors.As(err, &symErr) {
				t.Errorf("expected *apperr.SymbolNotFoundError, got %T: %v", err, err)
			}
		})
	}
}

// TestPythonParser_ExtractSymbol_NotFound_Suggestions verifies F-10 (task
// 14): a not-found symbol path that's a near-miss for an existing symbol
// gets that symbol suggested (via apperr.SymbolNotFoundError.Suggestions,
// surfaced in Error()'s "Did you mean?" section), while one with no close
// match in the file gets no suggestions at all -- mirroring Python's
// BaseParser.suggest_symbols (difflib.get_close_matches, cutoff=0.6).
func TestPythonParser_ExtractSymbol_NotFound_Suggestions(t *testing.T) {
	p := NewPythonParser()

	t.Run("near-miss symbol path is suggested", func(t *testing.T) {
		_, err := p.ExtractSymbol(sampleFile, "Greeter.shot")
		var symErr *apperr.SymbolNotFoundError
		if !errors.As(err, &symErr) {
			t.Fatalf("expected *apperr.SymbolNotFoundError, got %T: %v", err, err)
		}
		if len(symErr.Suggestions) == 0 {
			t.Fatal("expected at least one suggestion for a near-miss symbol path, got none")
		}
		if symErr.Suggestions[0] != "Greeter.shout" {
			t.Errorf("Suggestions[0] = %q, want %q", symErr.Suggestions[0], "Greeter.shout")
		}
		if !strings.Contains(symErr.Error(), "Did you mean?") {
			t.Errorf("Error() = %q, want it to contain %q", symErr.Error(), "Did you mean?")
		}
	})

	t.Run("no close match yields no suggestions", func(t *testing.T) {
		_, err := p.ExtractSymbol(sampleFile, "CompletelyUnrelatedXyz123")
		var symErr *apperr.SymbolNotFoundError
		if !errors.As(err, &symErr) {
			t.Fatalf("expected *apperr.SymbolNotFoundError, got %T: %v", err, err)
		}
		if len(symErr.Suggestions) != 0 {
			t.Errorf("Suggestions = %v, want none", symErr.Suggestions)
		}
		if strings.Contains(symErr.Error(), "Did you mean?") {
			t.Errorf("Error() = %q, want it to not contain %q", symErr.Error(), "Did you mean?")
		}
	})
}

func TestPythonParser_ExtractSymbol_FileNotFound(t *testing.T) {
	p := NewPythonParser()
	_, err := p.ExtractSymbol("../../testdata/python/does_not_exist.py", "Foo")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestPythonParser_ImplementsParser is a compile-time-and-runtime sanity
// check that PythonParser satisfies the Parser interface in full (ParseFile
// and ExtractSymbol from task 3; ResolveImports and FindReferencesInFile
// from task 4, exercised in detail by the tests below).
func TestPythonParser_ImplementsParser(t *testing.T) {
	var _ Parser = NewPythonParser()
}

// TestPythonParser_GenerateSkeleton verifies that GenerateSkeleton replaces
// every function/method body with "..." (preceded by a preserved docstring,
// when the body starts with one), leaving everything else -- module
// docstring, blank lines, decorators, signatures -- byte-for-byte identical
// to testdata/python/sample.py.
//
// legacy_function and non_string_first_statement_function (whose bodies do
// not start with a docstring) expect an extra 4-space-only line before their
// "..." line: tree-sitter-python's "block" node's start_byte begins at the
// first statement's own start (after its leading indentation whitespace,
// which therefore is *not* included in the replaced byte range and is left
// behind verbatim), so the replacement's own leading "\n" ends up appended
// after that already-present indentation rather than in place of it. This
// exactly mirrors Python's `PythonParser.generate_skeleton` (verified via
// `PythonParser().generate_skeleton(...)` against this same file), which
// walks the identical tree-sitter-python grammar with the same start_byte
// semantics, so it is intentionally preserved here rather than "fixed".
func TestPythonParser_GenerateSkeleton(t *testing.T) {
	p := NewPythonParser()
	got, err := p.GenerateSkeleton(sampleFile)
	if err != nil {
		t.Fatalf("GenerateSkeleton returned unexpected error: %v", err)
	}

	want := strings.Join([]string{
		`"""Module docstring."""`,
		``,
		``,
		`def top_level_function(x: int) -> int:`,
		`    """Return x doubled."""`,
		`    ...`,
		``,
		``,
		`class Greeter:`,
		`    """A friendly greeter."""`,
		``,
		`    def __init__(self, name: str) -> None:`,
		`        """Store the name."""`,
		`        ...`,
		``,
		`    @staticmethod`,
		`    def shout(message: str) -> str:`,
		`        """Shout a message."""`,
		`        ...`,
		``,
		``,
		`@deprecated`,
		`def legacy_function() -> None:`,
		`    `,
		`    ...`,
		``,
		``,
		`def single_quote_docstring_function() -> str:`,
		`    'Uses single quotes.'`,
		`    ...`,
		``,
		``,
		`def double_quote_docstring_function() -> str:`,
		`    "Uses double quotes."`,
		`    ...`,
		``,
		``,
		`def triple_single_quote_docstring_function() -> str:`,
		`    '''Uses triple single quotes.'''`,
		`    ...`,
		``,
		``,
		`def non_string_first_statement_function() -> None:`,
		`    `,
		`    ...`,
		``,
	}, "\n")

	if got != want {
		t.Errorf("GenerateSkeleton =\n%s\nwant\n%s", got, want)
	}
}

// TestPythonParser_GenerateSkeleton_FileNotFound verifies that
// GenerateSkeleton surfaces an *apperr.FileAccessError for a nonexistent
// file, matching readFile's behavior used by ParseFile/ExtractSymbol.
func TestPythonParser_GenerateSkeleton_FileNotFound(t *testing.T) {
	p := NewPythonParser()
	_, err := p.GenerateSkeleton("../../testdata/python/does_not_exist.py")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestPythonParser_ResolveImports_Relative covers relative import resolution
// ("from ." / "from .." forms), which does not depend on the process's
// working directory (it is resolved relative to the importing file's own
// directory), plus the "symbol not imported" / "module cannot be resolved"
// error cases, which likewise fail before any working-directory-relative
// path resolution happens.
func TestPythonParser_ResolveImports_Relative(t *testing.T) {
	p := NewPythonParser()
	callerFile := importProjDir + "/subpkg/caller.py"

	tests := []struct {
		name              string
		filePath          string
		symbolName        string
		wantPathHasSuffix string
		wantStartLine     int
	}{
		{
			name:              "from .utils import util_func",
			filePath:          callerFile,
			symbolName:        "util_func",
			wantPathHasSuffix: "testdata/python/importproj/subpkg/utils.py",
			wantStartLine:     1,
		},
		{
			name:              "from .utils import util_func as alias_func",
			filePath:          callerFile,
			symbolName:        "alias_func",
			wantPathHasSuffix: "testdata/python/importproj/subpkg/utils.py",
			wantStartLine:     1,
		},
		{
			name:              "from . import utils (bare relative module import)",
			filePath:          callerFile,
			symbolName:        "utils",
			wantPathHasSuffix: "testdata/python/importproj/subpkg/utils.py",
			// utils.py has no symbol literally named "utils", so this falls
			// back to line 1, mirroring Python's resolve_imports.
			wantStartLine: 1,
		},
		{
			name:              "from ..subpkg.utils import util_func as double_dot_func",
			filePath:          callerFile,
			symbolName:        "double_dot_func",
			wantPathHasSuffix: "testdata/python/importproj/subpkg/utils.py",
			wantStartLine:     1,
		},
		{
			name:              "from .utils import missing_func (symbol not found in target -> fallback to line 1)",
			filePath:          callerFile,
			symbolName:        "missing_func",
			wantPathHasSuffix: "testdata/python/importproj/subpkg/utils.py",
			wantStartLine:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotLine, err := p.ResolveImports(tt.filePath, tt.symbolName)
			if err != nil {
				t.Fatalf("ResolveImports(%q, %q) returned unexpected error: %v", tt.filePath, tt.symbolName, err)
			}
			gotPath = filepath.ToSlash(gotPath)
			if !strings.HasSuffix(gotPath, tt.wantPathHasSuffix) {
				t.Errorf("path = %q, want suffix %q", gotPath, tt.wantPathHasSuffix)
			}
			if gotLine != tt.wantStartLine {
				t.Errorf("startLine = %d, want %d", gotLine, tt.wantStartLine)
			}
		})
	}
}

// TestPythonParser_ResolveImports_UnresolvableModule verifies that an
// import_from_statement whose module cannot be located on disk (neither
// "<module>.py" nor "<module>/__init__.py" exists) returns an
// *apperr.FileAccessError, per Python's resolve_imports.
func TestPythonParser_ResolveImports_UnresolvableModule(t *testing.T) {
	p := NewPythonParser()
	_, _, err := p.ResolveImports(importProjDir+"/bad_caller.py", "something")
	if err == nil {
		t.Fatal("expected an error for an unresolvable imported module, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestPythonParser_ResolveImports_SymbolNotImported verifies that a symbol
// which is not imported anywhere in the target file returns an
// *apperr.SymbolNotFoundError.
//
// Task 14 (go_reviewer's task 4 follow-up): Python's resolve_imports has no
// dedicated "not imported" error type and reuses FileAccessError for this
// case; Go's PythonParser deliberately does not, since the file itself was
// read successfully -- SymbolNotFoundError (exit code 4) is semantically
// correct here in a way FileAccessError (exit code 3) is not. This is an
// intentional improvement over Python's behavior, not a parity bug.
func TestPythonParser_ResolveImports_SymbolNotImported(t *testing.T) {
	p := NewPythonParser()
	_, _, err := p.ResolveImports(importProjDir+"/main.py", "totally_absent_symbol")
	if err == nil {
		t.Fatal("expected an error for a symbol that is not imported, got nil")
	}
	var symbolErr *apperr.SymbolNotFoundError
	if !errors.As(err, &symbolErr) {
		t.Errorf("expected *apperr.SymbolNotFoundError, got %T: %v", err, err)
	}
}

func TestPythonParser_ResolveImports_FileNotFound(t *testing.T) {
	p := NewPythonParser()
	_, _, err := p.ResolveImports(importProjDir+"/does_not_exist.py", "anything")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestPythonParser_ResolveImports_Absolute covers absolute import resolution
// ("import x" / "from x import y" without leading dots), which (like
// Python's resolve_imports) is resolved relative to the process's current
// working directory. The working directory is temporarily changed to
// testdata/python/importproj so that it plays the role of the "project
// root".
func TestPythonParser_ResolveImports_Absolute(t *testing.T) {
	chdirTest(t, importProjDir)
	p := NewPythonParser()

	tests := []struct {
		name              string
		filePath          string
		symbolName        string
		wantPathHasSuffix string
		wantStartLine     int
	}{
		{
			name:       "import mathutils (bare module import)",
			filePath:   "main.py",
			symbolName: "mathutils",
			// mathutils.py has no symbol literally named "mathutils", so
			// this falls back to line 1.
			wantPathHasSuffix: "mathutils.py",
			wantStartLine:     1,
		},
		{
			name:              "from mathutils import add",
			filePath:          "main.py",
			symbolName:        "add",
			wantPathHasSuffix: "mathutils.py",
			wantStartLine:     1,
		},
		{
			name:              "from mathutils import subtract as sub",
			filePath:          "main.py",
			symbolName:        "sub",
			wantPathHasSuffix: "mathutils.py",
			wantStartLine:     6,
		},
		{
			// Covers a single import_from_statement bringing in multiple
			// symbols ("from X import a, b as c"), where the requested
			// symbol ("multiply") is the plain (non-aliased) one.
			name:              "from mathutils import multiply, divide as div (multi-import line, plain symbol)",
			filePath:          "main.py",
			symbolName:        "multiply",
			wantPathHasSuffix: "mathutils.py",
			wantStartLine:     11,
		},
		{
			// Same import_from_statement as above, but resolving the
			// aliased symbol ("div" -> "divide") mixed in on the same line.
			name:              "from mathutils import multiply, divide as div (multi-import line, aliased symbol)",
			filePath:          "main.py",
			symbolName:        "div",
			wantPathHasSuffix: "mathutils.py",
			wantStartLine:     16,
		},
		{
			// Covers "import x.y as z": an aliased import of a dotted
			// module path, where the alias resolves to the dotted module
			// "subpkg.utils" and the un-aliased original name used to look
			// up the target symbol is "utils" (the last dotted component).
			name:       "import subpkg.utils as sub_utils (dotted module path with alias)",
			filePath:   "main.py",
			symbolName: "sub_utils",
			// utils.py has no symbol literally named "utils", so this falls
			// back to line 1.
			wantPathHasSuffix: "subpkg/utils.py",
			wantStartLine:     1,
		},
		{
			name:       "import subpkg as direct_pkg (absolute package import -> __init__.py)",
			filePath:   "subpkg/caller.py",
			symbolName: "direct_pkg",
			// __init__.py has no symbol literally named "subpkg", so this
			// falls back to line 1.
			wantPathHasSuffix: "subpkg/__init__.py",
			wantStartLine:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotLine, err := p.ResolveImports(tt.filePath, tt.symbolName)
			if err != nil {
				t.Fatalf("ResolveImports(%q, %q) returned unexpected error: %v", tt.filePath, tt.symbolName, err)
			}
			gotPath = filepath.ToSlash(gotPath)
			if !strings.HasSuffix(gotPath, tt.wantPathHasSuffix) {
				t.Errorf("path = %q, want suffix %q", gotPath, tt.wantPathHasSuffix)
			}
			if gotLine != tt.wantStartLine {
				t.Errorf("startLine = %d, want %d", gotLine, tt.wantStartLine)
			}
		})
	}
}

func TestPythonParser_FindReferencesInFile(t *testing.T) {
	p := NewPythonParser()

	tests := []struct {
		name       string
		filePath   string
		symbolName string
		want       []ReferenceData
	}{
		{
			name:       "definition site is excluded",
			filePath:   referencesDir + "/target.py",
			symbolName: "target_func",
			want:       nil,
		},
		{
			name:       "comment mention is ignored, call site is found",
			filePath:   referencesDir + "/caller_a.py",
			symbolName: "target_func",
			want: []ReferenceData{
				{LineNumber: 2, LineContent: "target_func()  # actual reference on this line"},
			},
		},
		{
			name:       "parameter name is excluded, later use is found",
			filePath:   referencesDir + "/caller_b.py",
			symbolName: "target_func",
			want: []ReferenceData{
				{LineNumber: 6, LineContent: "value = target_func"},
			},
		},
		{
			name:       "fast-path: symbol text absent from file",
			filePath:   referencesDir + "/no_mention.py",
			symbolName: "target_func",
			want:       nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.FindReferencesInFile(tt.filePath, tt.symbolName)
			if err != nil {
				t.Fatalf("FindReferencesInFile(%q, %q) returned unexpected error: %v", tt.filePath, tt.symbolName, err)
			}
			// FindReferencesInFile must return a non-nil (possibly empty)
			// slice so JSON marshaling produces "[]" rather than "null",
			// matching Python's `json.dumps([])`.
			if got == nil {
				t.Fatalf("FindReferencesInFile(%q, %q) returned a nil slice; want a non-nil empty slice when there are no references", tt.filePath, tt.symbolName)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d references, want %d: %+v", len(got), len(tt.want), got)
			}
			wantPath := filepath.ToSlash(tt.filePath)
			for i := range got {
				if got[i].FilePath != wantPath {
					t.Errorf("references[%d].FilePath = %q, want %q", i, got[i].FilePath, wantPath)
				}
				if got[i].LineNumber != tt.want[i].LineNumber {
					t.Errorf("references[%d].LineNumber = %d, want %d", i, got[i].LineNumber, tt.want[i].LineNumber)
				}
				if got[i].LineContent != tt.want[i].LineContent {
					t.Errorf("references[%d].LineContent = %q, want %q", i, got[i].LineContent, tt.want[i].LineContent)
				}
			}
		})
	}
}

func TestPythonParser_FindReferencesInFile_FileNotFound(t *testing.T) {
	p := NewPythonParser()
	got, err := p.FindReferencesInFile(referencesDir+"/does_not_exist.py", "target_func")
	if err != nil {
		t.Fatalf("FindReferencesInFile returned unexpected error for a nonexistent file: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d references for a nonexistent file, want 0: %+v", len(got), got)
	}
}
