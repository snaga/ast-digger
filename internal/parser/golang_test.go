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
	goSampleFile    = "../../testdata/go/sample.go"
	goImportProjDir = "../../testdata/go/importproj"
	goReferencesDir = "../../testdata/go/references"
)

// TestGolangParser_ParseFile exercises function/method/struct/interface/type
// symbol extraction, doc-comment handling, and receiver-type-as-parent
// mapping for methods, against testdata/go/sample.go. It was cross-validated
// against the intended behavior of ast_digger.parsers.go.GoParser.parse_file
// (see ast_digger/parsers/go.py's GoParser.parse_file, which this Go
// implementation ports line-for-line).
func TestGolangParser_ParseFile(t *testing.T) {
	p := NewGolangParser()
	syms, err := p.ParseFile(goSampleFile)
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
			name:          "top-level function with doc comment",
			symbolName:    "TopLevelFunction",
			wantKind:      "function",
			wantStartLine: 6,
			wantEndLine:   8,
			wantSignature: "func TopLevelFunction(x int) int",
			wantDocstring: "// TopLevelFunction doubles x.",
		},
		{
			name:          "struct",
			symbolName:    "Greeter",
			wantKind:      "struct",
			wantStartLine: 11,
			wantEndLine:   13,
			wantDocstring: "// Greeter is a struct responsible for greetings.",
		},
		{
			name:          "method with pointer receiver",
			symbolName:    "Greet",
			wantKind:      "method",
			wantParent:    "Greeter",
			wantStartLine: 16,
			wantEndLine:   18,
			wantSignature: "func (g *Greeter) Greet(name string) string",
			wantDocstring: "// Greet returns a greeting string using a pointer receiver.",
		},
		{
			name:          "method with value receiver",
			symbolName:    "Shout",
			wantKind:      "method",
			wantParent:    "Greeter",
			wantStartLine: 21,
			wantEndLine:   23,
			wantSignature: "func (g Greeter) Shout(message string) string",
			wantDocstring: "// Shout returns an uppercased message using a value receiver.",
		},
		{
			name:          "interface",
			symbolName:    "Animal",
			wantKind:      "interface",
			wantStartLine: 26,
			wantEndLine:   29,
			wantDocstring: "// Animal describes something that can speak.",
		},
		{
			name:          "type alias",
			symbolName:    "ID",
			wantKind:      "type",
			wantStartLine: 32,
			wantEndLine:   32,
			wantDocstring: "// ID is a type alias for an identifier.",
		},
		{
			name:          "named type",
			symbolName:    "Count",
			wantKind:      "type",
			wantStartLine: 35,
			wantEndLine:   35,
			wantDocstring: "// Count is a named type based on int.",
		},
		{
			name:          "function without doc comment",
			symbolName:    "NoDocFunction",
			wantKind:      "function",
			wantStartLine: 37,
			wantEndLine:   38,
			wantSignature: "func NoDocFunction()",
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
			if tt.wantSignature != "" && deref(sym.Signature) != tt.wantSignature {
				t.Errorf("Signature = %q, want %q", deref(sym.Signature), tt.wantSignature)
			}
			if deref(sym.Docstring) != tt.wantDocstring {
				t.Errorf("Docstring = %q, want %q", deref(sym.Docstring), tt.wantDocstring)
			}
			if sym.FilePath == nil || *sym.FilePath != goSampleFile {
				t.Errorf("FilePath = %v, want %q", sym.FilePath, goSampleFile)
			}
		})
	}
}

// TestGolangParser_ParseFile_TypeKindsHaveNoSignature verifies that
// struct/interface/type symbols (unlike function/method symbols) have a nil
// Signature, matching Python's `GoParser.parse_file`, which never passes a
// `signature=` argument for its type_declaration branch.
func TestGolangParser_ParseFile_TypeKindsHaveNoSignature(t *testing.T) {
	p := NewGolangParser()
	syms, err := p.ParseFile(goSampleFile)
	if err != nil {
		t.Fatalf("ParseFile returned unexpected error: %v", err)
	}

	for _, name := range []string{"Greeter", "Animal", "ID", "Count"} {
		sym := findSymbol(syms, name)
		if sym == nil {
			t.Fatalf("symbol %q not found in ParseFile results", name)
		}
		if sym.Signature != nil {
			t.Errorf("symbol %q: Signature = %q, want nil", name, *sym.Signature)
		}
	}
}

func TestGolangParser_ParseFile_FileNotFound(t *testing.T) {
	p := NewGolangParser()
	_, err := p.ParseFile("../../testdata/go/does_not_exist.go")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestGolangParser_ParseFile_NoSymbols exercises ParseFile against a file
// with no function/method/struct/interface/type declarations, guarding
// against the same nil-vs-empty-slice regression documented in
// TestPythonParser_ParseFile_NoSymbols/TestTypeScriptParser_ParseFile_NoSymbols.
func TestGolangParser_ParseFile_NoSymbols(t *testing.T) {
	dir := t.TempDir()
	noSymbolsFile := filepath.Join(dir, "no_symbols.go")
	if err := os.WriteFile(noSymbolsFile, []byte("package empty\n\nvar x = 1\n"), 0o644); err != nil {
		t.Fatalf("failed to write test fixture: %v", err)
	}

	p := NewGolangParser()
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

// TestGolangParser_ExtractSymbol exercises ExtractSymbol against
// testdata/go/sample.go, covering bare top-level symbols (function/struct/
// interface/type) and dotted "ReceiverType.MethodName" paths.
func TestGolangParser_ExtractSymbol(t *testing.T) {
	p := NewGolangParser()

	tests := []struct {
		name          string
		symbolPath    string
		wantKind      string
		wantParent    string
		wantStartLine int
		wantEndLine   int
		wantCode      string
		wantSignature string
	}{
		{
			name:          "top-level function",
			symbolPath:    "TopLevelFunction",
			wantKind:      "function",
			wantStartLine: 6,
			wantEndLine:   8,
			wantCode: "func TopLevelFunction(x int) int {\n" +
				"\treturn x * 2\n" +
				"}",
			wantSignature: "func TopLevelFunction(x int) int",
		},
		{
			name:          "whole struct",
			symbolPath:    "Greeter",
			wantKind:      "struct",
			wantStartLine: 11,
			wantEndLine:   13,
			wantCode: "type Greeter struct {\n" +
				"\tPrefix string\n" +
				"}",
		},
		{
			name:          "method via dotted path (pointer receiver)",
			symbolPath:    "Greeter.Greet",
			wantKind:      "method",
			wantParent:    "Greeter",
			wantStartLine: 16,
			wantEndLine:   18,
			wantCode: "func (g *Greeter) Greet(name string) string {\n" +
				"\treturn fmt.Sprintf(\"%s, %s!\", g.Prefix, name)\n" +
				"}",
			wantSignature: "func (g *Greeter) Greet(name string) string",
		},
		{
			name:          "method via dotted path (value receiver)",
			symbolPath:    "Greeter.Shout",
			wantKind:      "method",
			wantParent:    "Greeter",
			wantStartLine: 21,
			wantEndLine:   23,
			wantCode: "func (g Greeter) Shout(message string) string {\n" +
				"\treturn message\n" +
				"}",
			wantSignature: "func (g Greeter) Shout(message string) string",
		},
		{
			name:          "method via bare name (no receiver type prefix)",
			symbolPath:    "Greet",
			wantKind:      "method",
			wantParent:    "Greeter",
			wantStartLine: 16,
			wantEndLine:   18,
			wantCode: "func (g *Greeter) Greet(name string) string {\n" +
				"\treturn fmt.Sprintf(\"%s, %s!\", g.Prefix, name)\n" +
				"}",
			wantSignature: "func (g *Greeter) Greet(name string) string",
		},
		{
			name:          "interface",
			symbolPath:    "Animal",
			wantKind:      "interface",
			wantStartLine: 26,
			wantEndLine:   29,
			wantCode: "type Animal interface {\n" +
				"\t// Speak returns the sound the animal makes.\n" +
				"\tSpeak() string\n" +
				"}",
		},
		{
			name:          "type alias",
			symbolPath:    "ID",
			wantKind:      "type",
			wantStartLine: 32,
			wantEndLine:   32,
			wantCode:      "type ID = string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sym, err := p.ExtractSymbol(goSampleFile, tt.symbolPath)
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
			if tt.wantSignature == "" {
				if sym.Signature != nil {
					t.Errorf("Signature = %q, want nil", *sym.Signature)
				}
			} else if deref(sym.Signature) != tt.wantSignature {
				t.Errorf("Signature = %q, want %q", deref(sym.Signature), tt.wantSignature)
			}
			if sym.FilePath == nil || *sym.FilePath != goSampleFile {
				t.Errorf("FilePath = %v, want %q", sym.FilePath, goSampleFile)
			}
		})
	}
}

func TestGolangParser_ExtractSymbol_NotFound(t *testing.T) {
	p := NewGolangParser()

	tests := []struct {
		name       string
		symbolPath string
	}{
		{name: "unknown top-level symbol", symbolPath: "NoSuchFunc"},
		{name: "unknown method on known struct", symbolPath: "Greeter.NoSuchMethod"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.ExtractSymbol(goSampleFile, tt.symbolPath)
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

// TestGolangParser_ExtractSymbol_NotFound_Suggestions verifies F-10 (task
// 14) for GolangParser: a not-found symbol path that's a near-miss for an
// existing symbol gets that symbol suggested (via
// apperr.SymbolNotFoundError.Suggestions, surfaced in Error()'s "Did you
// mean?" section), while one with no close match in the file gets none.
func TestGolangParser_ExtractSymbol_NotFound_Suggestions(t *testing.T) {
	p := NewGolangParser()

	t.Run("near-miss symbol path is suggested", func(t *testing.T) {
		_, err := p.ExtractSymbol(goSampleFile, "Greeter.Shot")
		var symErr *apperr.SymbolNotFoundError
		if !errors.As(err, &symErr) {
			t.Fatalf("expected *apperr.SymbolNotFoundError, got %T: %v", err, err)
		}
		if len(symErr.Suggestions) == 0 {
			t.Fatal("expected at least one suggestion for a near-miss symbol path, got none")
		}
		if symErr.Suggestions[0] != "Greeter.Shout" {
			t.Errorf("Suggestions[0] = %q, want %q", symErr.Suggestions[0], "Greeter.Shout")
		}
		if !strings.Contains(symErr.Error(), "Did you mean?") {
			t.Errorf("Error() = %q, want it to contain %q", symErr.Error(), "Did you mean?")
		}
	})

	t.Run("no close match yields no suggestions", func(t *testing.T) {
		_, err := p.ExtractSymbol(goSampleFile, "CompletelyUnrelatedXyz123")
		var symErr *apperr.SymbolNotFoundError
		if !errors.As(err, &symErr) {
			t.Fatalf("expected *apperr.SymbolNotFoundError, got %T: %v", err, err)
		}
		if len(symErr.Suggestions) != 0 {
			t.Errorf("Suggestions = %v, want none", symErr.Suggestions)
		}
	})
}

func TestGolangParser_ExtractSymbol_FileNotFound(t *testing.T) {
	p := NewGolangParser()
	_, err := p.ExtractSymbol("../../testdata/go/does_not_exist.go", "Foo")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestGolangParser_ImplementsParser is a compile-time-and-runtime sanity
// check that GolangParser satisfies the Parser interface in full.
func TestGolangParser_ImplementsParser(t *testing.T) {
	var _ Parser = NewGolangParser()
}

// TestGolangParser_GenerateSkeleton verifies that GenerateSkeleton replaces
// every function/method body with "{ /* ... */ }", leaving doc comments and
// signatures byte-for-byte identical to testdata/go/sample.go.
func TestGolangParser_GenerateSkeleton(t *testing.T) {
	p := NewGolangParser()
	got, err := p.GenerateSkeleton(goSampleFile)
	if err != nil {
		t.Fatalf("GenerateSkeleton returned unexpected error: %v", err)
	}

	want := strings.Join([]string{
		`package sample`,
		``,
		`import "fmt"`,
		``,
		`// TopLevelFunction doubles x.`,
		`func TopLevelFunction(x int) int { /* ... */ }`,
		``,
		`// Greeter is a struct responsible for greetings.`,
		`type Greeter struct {`,
		`	Prefix string`,
		`}`,
		``,
		`// Greet returns a greeting string using a pointer receiver.`,
		`func (g *Greeter) Greet(name string) string { /* ... */ }`,
		``,
		`// Shout returns an uppercased message using a value receiver.`,
		`func (g Greeter) Shout(message string) string { /* ... */ }`,
		``,
		`// Animal describes something that can speak.`,
		`type Animal interface {`,
		`	// Speak returns the sound the animal makes.`,
		`	Speak() string`,
		`}`,
		``,
		`// ID is a type alias for an identifier.`,
		`type ID = string`,
		``,
		`// Count is a named type based on int.`,
		`type Count int`,
		``,
		`func NoDocFunction() { /* ... */ }`,
		``,
	}, "\n")

	if got != want {
		t.Errorf("GenerateSkeleton =\n%s\nwant\n%s", got, want)
	}
}

func TestGolangParser_GenerateSkeleton_FileNotFound(t *testing.T) {
	p := NewGolangParser()
	_, err := p.GenerateSkeleton("../../testdata/go/does_not_exist.go")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestGolangParser_ResolveImports_Relative covers relative import
// resolution ("../pkg" style import paths), which does not depend on the
// process's working directory or a go.mod module name.
func TestGolangParser_ResolveImports_Relative(t *testing.T) {
	p := NewGolangParser()
	callerFile := goImportProjDir + "/subpkg/caller.go"

	gotPath, gotLine, err := p.ResolveImports(callerFile, "Add")
	if err != nil {
		t.Fatalf("ResolveImports(%q, %q) returned unexpected error: %v", callerFile, "Add", err)
	}
	gotPath = filepath.ToSlash(gotPath)
	const wantSuffix = "testdata/go/importproj/mathutils/mathutils.go"
	if !strings.HasSuffix(gotPath, wantSuffix) {
		t.Errorf("path = %q, want suffix %q", gotPath, wantSuffix)
	}
	if gotLine != 4 {
		t.Errorf("startLine = %d, want %d", gotLine, 4)
	}
}

// TestGolangParser_ResolveImports_Module covers module-path-based local
// package resolution, using the go.mod at testdata/go/importproj/go.mod
// (module "example.com/importproj"). This requires the process's working
// directory to be the project root containing go.mod, matching Python's
// `project_root = pathlib.Path.cwd().resolve()`.
func TestGolangParser_ResolveImports_Module(t *testing.T) {
	chdirTest(t, goImportProjDir)
	p := NewGolangParser()

	tests := []struct {
		name              string
		symbolName        string
		wantPathHasSuffix string
		wantStartLine     int
	}{
		{
			name:              "Add",
			symbolName:        "Add",
			wantPathHasSuffix: "mathutils/mathutils.go",
			wantStartLine:     4,
		},
		{
			name:              "Subtract",
			symbolName:        "Subtract",
			wantPathHasSuffix: "mathutils/mathutils.go",
			wantStartLine:     9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotLine, err := p.ResolveImports("main.go", tt.symbolName)
			if err != nil {
				t.Fatalf("ResolveImports(%q, %q) returned unexpected error: %v", "main.go", tt.symbolName, err)
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

// TestGolangParser_ResolveImports_UnresolvableModule verifies that an
// imported package path that cannot be located as a local directory (an
// external/third-party package, or a module-prefixed path with no matching
// directory on disk) returns an *apperr.FileAccessError.
func TestGolangParser_ResolveImports_UnresolvableModule(t *testing.T) {
	chdirTest(t, goImportProjDir)
	p := NewGolangParser()
	_, _, err := p.ResolveImports("bad_caller.go", "Something")
	if err == nil {
		t.Fatal("expected an error for an unresolvable imported package, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestGolangParser_ResolveImports_SymbolNotFoundInImports verifies that a
// symbol which is not defined in any of the file's resolvable local package
// imports (here, "fmt" is external/unresolvable but "mathutils" *is*
// resolved -- it just doesn't define the requested symbol) returns an
// *apperr.SymbolNotFoundError, not an *apperr.FileAccessError.
//
// Task 14 (go_reviewer's task 4 follow-up): unlike Python's
// GoParser.resolve_imports (which has no dedicated "not found" error type
// and reuses FileAccessError for this case too), Go's GolangParser
// distinguishes "no import resolved to a real local package at all" (still
// FileAccessError, see TestGolangParser_ResolveImports_UnresolvableModule)
// from "at least one import resolved, but doesn't define the requested
// symbol" (SymbolNotFoundError, exit code 4) -- an intentional semantic
// improvement, not a parity bug.
func TestGolangParser_ResolveImports_SymbolNotFoundInImports(t *testing.T) {
	chdirTest(t, goImportProjDir)
	p := NewGolangParser()
	_, _, err := p.ResolveImports("main.go", "TotallyAbsentSymbol")
	if err == nil {
		t.Fatal("expected an error for a symbol not defined in any resolvable import, got nil")
	}
	var symbolErr *apperr.SymbolNotFoundError
	if !errors.As(err, &symbolErr) {
		t.Errorf("expected *apperr.SymbolNotFoundError, got %T: %v", err, err)
	}
}

func TestGolangParser_ResolveImports_FileNotFound(t *testing.T) {
	p := NewGolangParser()
	_, _, err := p.ResolveImports(goImportProjDir+"/does_not_exist.go", "Add")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestGolangParser_FindReferencesInFile documents that, unlike
// PythonParser/TypeScriptParser, GolangParser.FindReferencesInFile does not
// exclude definition-site identifiers -- only comment and string-literal
// occurrences are excluded, matching ast_digger.parsers.go.GoParser.
// find_references_in_file verbatim.
func TestGolangParser_FindReferencesInFile(t *testing.T) {
	p := NewGolangParser()

	tests := []struct {
		name       string
		filePath   string
		symbolName string
		want       []ReferenceData
	}{
		{
			name:       "definition site is included (no is_definition exclusion in Go)",
			filePath:   goReferencesDir + "/target.go",
			symbolName: "TargetFunc",
			want: []ReferenceData{
				{LineNumber: 4, LineContent: "func TargetFunc() string {"},
			},
		},
		{
			name:       "comment mention is ignored, call site is found",
			filePath:   goReferencesDir + "/caller_a.go",
			symbolName: "TargetFunc",
			want: []ReferenceData{
				{LineNumber: 4, LineContent: "TargetFunc() // actual reference on this line, this comment also mentions TargetFunc"},
			},
		},
		{
			name:       "string literal mention is ignored, value use is found",
			filePath:   goReferencesDir + "/caller_b.go",
			symbolName: "TargetFunc",
			want: []ReferenceData{
				{LineNumber: 5, LineContent: "value := TargetFunc"},
			},
		},
		{
			name:       "fast-path: symbol text absent from file",
			filePath:   goReferencesDir + "/no_mention.go",
			symbolName: "TargetFunc",
			want:       nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.FindReferencesInFile(tt.filePath, tt.symbolName)
			if err != nil {
				t.Fatalf("FindReferencesInFile(%q, %q) returned unexpected error: %v", tt.filePath, tt.symbolName, err)
			}
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

func TestGolangParser_FindReferencesInFile_FileNotFound(t *testing.T) {
	p := NewGolangParser()
	got, err := p.FindReferencesInFile(goReferencesDir+"/does_not_exist.go", "TargetFunc")
	if err != nil {
		t.Fatalf("FindReferencesInFile returned unexpected error for a nonexistent file: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d references for a nonexistent file, want 0: %+v", len(got), got)
	}
}
