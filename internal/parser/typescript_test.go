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
	tsSampleFile    = "../../testdata/typescript/sample.ts"
	tsxSampleFile   = "../../testdata/typescript/sample.tsx"
	tsImportProjDir = "../../testdata/typescript/importproj"
	tsReferencesDir = "../../testdata/typescript/references"
)

// TestTypeScriptParser_ParseFile exercises class/interface/type/function/
// method/arrow-function symbol extraction, decorator + JSDoc handling
// (including the deliberately-preserved quirk where a decorator applied to
// an exported class is not reflected in its signature -- see
// getTSSignature's doc comment), all cross-validated byte-for-byte against
// ast_digger.parsers.typescript.TypeScriptParser.parse_file run against this
// same file via `.venv/Scripts/python.exe`.
func TestTypeScriptParser_ParseFile(t *testing.T) {
	p := NewTypeScriptParser()
	syms, err := p.ParseFile(tsSampleFile)
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
			name:          "top-level function with JSDoc",
			symbolName:    "topLevelFunction",
			wantKind:      "function",
			wantStartLine: 4,
			wantEndLine:   6,
			wantSignature: "function topLevelFunction(x: number): number",
			wantDocstring: "Doubles x.",
		},
		{
			name:          "class",
			symbolName:    "Greeter",
			wantKind:      "class",
			wantStartLine: 11,
			wantEndLine:   27,
			wantSignature: "class Greeter",
			wantDocstring: "A friendly greeter.",
		},
		{
			name:          "constructor method",
			symbolName:    "constructor",
			wantKind:      "method",
			wantParent:    "Greeter",
			wantStartLine: 17,
			wantEndLine:   19,
			wantSignature: "constructor(name: string)",
			wantDocstring: "Store the name.",
		},
		{
			name:          "static method",
			symbolName:    "shout",
			wantKind:      "method",
			wantParent:    "Greeter",
			wantStartLine: 24,
			wantEndLine:   26,
			wantSignature: "static shout(message: string): string",
			wantDocstring: "Shout a message.",
		},
		{
			name:          "interface",
			symbolName:    "Greeting",
			wantKind:      "interface",
			wantStartLine: 32,
			wantEndLine:   39,
			wantSignature: "interface Greeting",
			wantDocstring: "Describes something that can be greeted.",
		},
		{
			name:          "interface method signature",
			symbolName:    "format",
			wantKind:      "method",
			wantParent:    "Greeting",
			wantStartLine: 38,
			wantEndLine:   38,
			wantSignature: "format(): string",
			wantDocstring: "Format the greeting.",
		},
		{
			name:          "type alias",
			symbolName:    "GreetingId",
			wantKind:      "type",
			wantStartLine: 44,
			wantEndLine:   44,
			wantSignature: "type GreetingId = string | number;",
			wantDocstring: "A greeting identifier: either a string or a numeric code.",
		},
		{
			name:          "arrow function assigned to const",
			symbolName:    "arrowGreet",
			wantKind:      "function",
			wantStartLine: 49,
			wantEndLine:   51,
			wantSignature: "const arrowGreet = (name: string): string =>",
			wantDocstring: "An arrow function assigned to a const.",
		},
		{
			// The decorator "@decoratorFactory()" precedes "export class
			// DecoratedThing" but, per tree-sitter-typescript's grammar, is
			// parsed as a *child* of the export_statement node (preceding
			// the "export" keyword and class_declaration within it), not as
			// a sibling immediately before it. getTSSignature's decorator
			// lookup only walks prev_sibling of the (possibly
			// export_statement-unwrapped) node, so it never sees this
			// decorator: the signature is "class DecoratedThing" with no
			// decorator prefix. This is not a Go-side bug -- Python's
			// TypeScriptParser.parse_file exhibits the identical behavior
			// against this same file (verified via
			// TypeScriptParser().parse_file(...)).
			name:          "exported decorated class (decorator not reflected in signature)",
			symbolName:    "DecoratedThing",
			wantKind:      "class",
			wantStartLine: 63,
			wantEndLine:   68,
			wantSignature: "class DecoratedThing",
			wantDocstring: "A class with a decorator, to exercise decorator + JSDoc handling together.",
		},
		{
			// Unlike the class-level decorator above, a decorator directly
			// preceding a method_definition within a class_body *is* a
			// genuine prev_sibling of that method_definition (no
			// export_statement wrapping to route around), so it is picked
			// up and prepended to the signature as expected.
			name:          "decorated method (decorator is a genuine sibling, so it is captured)",
			symbolName:    "decoratedMethod",
			wantKind:      "method",
			wantParent:    "DecoratedThing",
			wantStartLine: 65,
			wantEndLine:   67,
			wantSignature: "@decoratorFactory()\ndecoratedMethod(): void",
			wantDocstring: "",
		},
		{
			name:          "function without JSDoc",
			symbolName:    "noDocstringFunction",
			wantKind:      "function",
			wantStartLine: 70,
			wantEndLine:   72,
			wantSignature: "function noDocstringFunction(): void",
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
			if sym.FilePath == nil || *sym.FilePath != tsSampleFile {
				t.Errorf("FilePath = %v, want %q", sym.FilePath, tsSampleFile)
			}
		})
	}
}

// TestTypeScriptParser_ParseFile_TSX verifies that ".tsx" files are parsed
// with the TSX grammar (LanguageTSX rather than LanguageTypescript), using a
// JSX-returning function declaration and arrow function. It also documents a
// signature-extraction quirk shared with the plain .ts case: a parameter's
// object-type annotation (e.g. "props: { name: string }") is itself an
// "object_type" node, which getTSSignature's depth-first, first-match body
// search finds *before* it reaches the function's actual statement_block
// body (since parameters are visited before the body in document order),
// truncating the extracted signature there. This was cross-validated against
// Python's TypeScriptParser.parse_file run against this same file.
func TestTypeScriptParser_ParseFile_TSX(t *testing.T) {
	p := NewTypeScriptParser()
	syms, err := p.ParseFile(tsxSampleFile)
	if err != nil {
		t.Fatalf("ParseFile returned unexpected error: %v", err)
	}

	tests := []struct {
		name          string
		symbolName    string
		wantKind      string
		wantStartLine int
		wantEndLine   int
		wantSignature string
		wantDocstring string
	}{
		{
			name:          "JSX-returning function declaration",
			symbolName:    "GreetingBanner",
			wantKind:      "function",
			wantStartLine: 6,
			wantEndLine:   8,
			wantSignature: "function GreetingBanner(props:",
			wantDocstring: "Renders a greeting banner.",
		},
		{
			name:          "JSX-returning arrow function",
			symbolName:    "SmallBanner",
			wantKind:      "function",
			wantStartLine: 13,
			wantEndLine:   15,
			wantSignature: "const SmallBanner = (props:",
			wantDocstring: "An arrow-function React component.",
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
		})
	}
}

func TestTypeScriptParser_ParseFile_FileNotFound(t *testing.T) {
	p := NewTypeScriptParser()
	_, err := p.ParseFile("../../testdata/typescript/does_not_exist.ts")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestTypeScriptParser_ParseFile_NoSymbols exercises ParseFile against a
// file with no class/interface/type/function declarations, guarding against
// the same nil-vs-empty-slice regression documented in
// TestPythonParser_ParseFile_NoSymbols.
func TestTypeScriptParser_ParseFile_NoSymbols(t *testing.T) {
	dir := t.TempDir()
	noSymbolsFile := filepath.Join(dir, "no_symbols.ts")
	if err := os.WriteFile(noSymbolsFile, []byte("const x = 1;\nconst y = 2;\n"), 0o644); err != nil {
		t.Fatalf("failed to write test fixture: %v", err)
	}

	p := NewTypeScriptParser()
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

// TestTypeScriptParser_ExtractSymbol was cross-validated against Python's
// TypeScriptParser.extract_symbol run against the same testdata/typescript/
// sample.ts file.
func TestTypeScriptParser_ExtractSymbol(t *testing.T) {
	p := NewTypeScriptParser()

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
			wantStartLine: 11,
			wantEndLine:   27,
			wantCode: "class Greeter {\n" +
				"  name: string;\n" +
				"\n" +
				"  /**\n" +
				"   * Store the name.\n" +
				"   */\n" +
				"  constructor(name: string) {\n" +
				"    this.name = name;\n" +
				"  }\n" +
				"\n" +
				"  /**\n" +
				"   * Shout a message.\n" +
				"   */\n" +
				"  static shout(message: string): string {\n" +
				"    return message.toUpperCase();\n" +
				"  }\n" +
				"}",
		},
		{
			name:          "static method via dotted path",
			symbolPath:    "Greeter.shout",
			wantKind:      "method",
			wantParent:    "Greeter",
			wantStartLine: 24,
			wantEndLine:   26,
			wantCode: "static shout(message: string): string {\n" +
				"    return message.toUpperCase();\n" +
				"  }",
		},
		{
			name:          "top-level function",
			symbolPath:    "topLevelFunction",
			wantKind:      "function",
			wantStartLine: 4,
			wantEndLine:   6,
			wantCode: "function topLevelFunction(x: number): number {\n" +
				"  return x * 2;\n" +
				"}",
		},
		{
			// foundNode is the lexical_declaration statement itself (matching
			// classifyTSNode's actualNode == node behavior for this form),
			// so Code includes the "const ... = ... ;" wrapper verbatim.
			name:          "arrow function assigned to const",
			symbolPath:    "arrowGreet",
			wantKind:      "function",
			wantStartLine: 49,
			wantEndLine:   51,
			wantCode: "const arrowGreet = (name: string): string => {\n" +
				"  return `Hello, ${name}!`;\n" +
				"};",
		},
		{
			name:          "decorated method via dotted path",
			symbolPath:    "DecoratedThing.decoratedMethod",
			wantKind:      "method",
			wantParent:    "DecoratedThing",
			wantStartLine: 65,
			wantEndLine:   67,
			wantCode: "decoratedMethod(): void {\n" +
				"    console.log(\"decorated\");\n" +
				"  }",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sym, err := p.ExtractSymbol(tsSampleFile, tt.symbolPath)
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
			var gotCode string
			if sym.Code != nil {
				gotCode = strings.ReplaceAll(*sym.Code, "\r\n", "\n")
			}
			if gotCode != tt.wantCode {
				t.Errorf("Code = %q, want %q", deref(sym.Code), tt.wantCode)
			}
			if sym.FilePath == nil || *sym.FilePath != tsSampleFile {
				t.Errorf("FilePath = %v, want %q", sym.FilePath, tsSampleFile)
			}
		})
	}
}

func TestTypeScriptParser_ExtractSymbol_NotFound(t *testing.T) {
	p := NewTypeScriptParser()

	tests := []struct {
		name       string
		symbolPath string
	}{
		{name: "unknown top-level symbol", symbolPath: "NoSuchClass"},
		{name: "unknown method on known class", symbolPath: "Greeter.nonexistent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.ExtractSymbol(tsSampleFile, tt.symbolPath)
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

// TestTypeScriptParser_ExtractSymbol_NotFound_Suggestions verifies F-10
// (task 14) for TypeScriptParser: a not-found symbol path that's a
// near-miss for an existing symbol gets that symbol suggested (via
// apperr.SymbolNotFoundError.Suggestions, surfaced in Error()'s "Did you
// mean?" section), while one with no close match in the file gets none.
func TestTypeScriptParser_ExtractSymbol_NotFound_Suggestions(t *testing.T) {
	p := NewTypeScriptParser()

	t.Run("near-miss symbol path is suggested", func(t *testing.T) {
		_, err := p.ExtractSymbol(tsSampleFile, "Greeter.shot")
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
		_, err := p.ExtractSymbol(tsSampleFile, "CompletelyUnrelatedXyz123")
		var symErr *apperr.SymbolNotFoundError
		if !errors.As(err, &symErr) {
			t.Fatalf("expected *apperr.SymbolNotFoundError, got %T: %v", err, err)
		}
		if len(symErr.Suggestions) != 0 {
			t.Errorf("Suggestions = %v, want none", symErr.Suggestions)
		}
	})
}

func TestTypeScriptParser_ExtractSymbol_FileNotFound(t *testing.T) {
	p := NewTypeScriptParser()
	_, err := p.ExtractSymbol("../../testdata/typescript/does_not_exist.ts", "Foo")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestTypeScriptParser_ImplementsParser is a compile-time-and-runtime sanity
// check that TypeScriptParser satisfies the Parser interface in full.
func TestTypeScriptParser_ImplementsParser(t *testing.T) {
	var _ Parser = NewTypeScriptParser()
}

// TestTypeScriptParser_GenerateSkeleton verifies that GenerateSkeleton
// replaces every function/method/arrow-function body with "{ /* ... */ }",
// leaving JSDoc comments, decorators, and signatures byte-for-byte identical
// to testdata/typescript/sample.ts. The expected output was cross-validated
// against Python's TypeScriptParser.generate_skeleton run against this same
// file via `.venv/Scripts/python.exe`.
func TestTypeScriptParser_GenerateSkeleton(t *testing.T) {
	p := NewTypeScriptParser()
	got, err := p.GenerateSkeleton(tsSampleFile)
	if err != nil {
		t.Fatalf("GenerateSkeleton returned unexpected error: %v", err)
	}

	want := strings.Join([]string{
		`/**`,
		` * Doubles x.`,
		` */`,
		`export function topLevelFunction(x: number): number { /* ... */ }`,
		``,
		`/**`,
		` * A friendly greeter.`,
		` */`,
		`export class Greeter {`,
		`  name: string;`,
		``,
		`  /**`,
		`   * Store the name.`,
		`   */`,
		`  constructor(name: string) { /* ... */ }`,
		``,
		`  /**`,
		`   * Shout a message.`,
		`   */`,
		`  static shout(message: string): string { /* ... */ }`,
		`}`,
		``,
		`/**`,
		` * Describes something that can be greeted.`,
		` */`,
		`export interface Greeting {`,
		`  message: string;`,
		``,
		`  /**`,
		`   * Format the greeting.`,
		`   */`,
		`  format(): string;`,
		`}`,
		``,
		`/**`,
		` * A greeting identifier: either a string or a numeric code.`,
		` */`,
		`export type GreetingId = string | number;`,
		``,
		`/**`,
		` * An arrow function assigned to a const.`,
		` */`,
		`export const arrowGreet = (name: string): string => { /* ... */ };`,
		``,
		`function decoratorFactory() { /* ... */ }`,
		``,
		`/**`,
		` * A class with a decorator, to exercise decorator + JSDoc handling together.`,
		` */`,
		`@decoratorFactory()`,
		`export class DecoratedThing {`,
		`  @decoratorFactory()`,
		`  decoratedMethod(): void { /* ... */ }`,
		`}`,
		``,
		`function noDocstringFunction(): void { /* ... */ }`,
		``,
	}, "\n")

	if strings.ReplaceAll(got, "\r\n", "\n") != want {
		t.Errorf("GenerateSkeleton =\n%s\nwant\n%s", got, want)
	}
}

func TestTypeScriptParser_GenerateSkeleton_FileNotFound(t *testing.T) {
	p := NewTypeScriptParser()
	_, err := p.GenerateSkeleton("../../testdata/typescript/does_not_exist.ts")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestTypeScriptParser_ResolveImports_Relative covers relative import
// resolution ("./" / "../" module specifiers), which (like the equivalent
// Python behavior) does not depend on the process's working directory.
func TestTypeScriptParser_ResolveImports_Relative(t *testing.T) {
	p := NewTypeScriptParser()
	callerFile := tsImportProjDir + "/subpkg/caller.ts"

	tests := []struct {
		name              string
		symbolName        string
		wantPathHasSuffix string
		wantStartLine     int
	}{
		{
			name:              "import { utilFunc } from './utils'",
			symbolName:        "utilFunc",
			wantPathHasSuffix: "testdata/typescript/importproj/subpkg/utils.ts",
			wantStartLine:     1,
		},
		{
			name:              "import { add } from '../mathutils'",
			symbolName:        "add",
			wantPathHasSuffix: "testdata/typescript/importproj/mathutils.ts",
			wantStartLine:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotLine, err := p.ResolveImports(callerFile, tt.symbolName)
			if err != nil {
				t.Fatalf("ResolveImports(%q, %q) returned unexpected error: %v", callerFile, tt.symbolName, err)
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

// TestTypeScriptParser_ResolveImports_UnresolvableModule verifies that an
// import whose module specifier cannot be located on disk (none of
// ".ts"/".tsx"/".d.ts"/"index.ts"/"index.tsx"/"index.d.ts" exist) returns an
// *apperr.FileAccessError.
func TestTypeScriptParser_ResolveImports_UnresolvableModule(t *testing.T) {
	p := NewTypeScriptParser()
	_, _, err := p.ResolveImports(tsImportProjDir+"/bad_caller.ts", "something")
	if err == nil {
		t.Fatal("expected an error for an unresolvable imported module, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestTypeScriptParser_ResolveImports_SymbolNotImported verifies that a
// symbol which is not imported anywhere in the target file returns an
// *apperr.SymbolNotFoundError.
//
// Task 14 (go_reviewer's task 4 follow-up): TypeScriptParser.resolve_imports
// in Python has no dedicated "not imported" error type and reuses
// FileAccessError for this case; Go's TypeScriptParser deliberately does
// not, matching PythonParser's identical change -- SymbolNotFoundError
// (exit code 4) is semantically correct here in a way FileAccessError (exit
// code 3) is not. This is an intentional improvement over Python's
// behavior, not a parity bug.
func TestTypeScriptParser_ResolveImports_SymbolNotImported(t *testing.T) {
	p := NewTypeScriptParser()
	_, _, err := p.ResolveImports(tsImportProjDir+"/main.ts", "totally_absent_symbol")
	if err == nil {
		t.Fatal("expected an error for a symbol that is not imported, got nil")
	}
	var symbolErr *apperr.SymbolNotFoundError
	if !errors.As(err, &symbolErr) {
		t.Errorf("expected *apperr.SymbolNotFoundError, got %T: %v", err, err)
	}
}

func TestTypeScriptParser_ResolveImports_FileNotFound(t *testing.T) {
	p := NewTypeScriptParser()
	_, _, err := p.ResolveImports(tsImportProjDir+"/does_not_exist.ts", "anything")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestTypeScriptParser_ResolveImports_Absolute covers absolute (bare
// module-specifier) import resolution, the "@/" -> "<project_root>/src/"
// alias, named/aliased-named/namespace/default imports, and a CommonJS
// "const x = require(...)" declaration -- all resolved relative to the
// process's current working directory, which is temporarily changed to
// testdata/typescript/importproj so that it plays the role of the "project
// root". Expected paths/lines were cross-validated against Python's
// TypeScriptParser.resolve_imports run the same way.
func TestTypeScriptParser_ResolveImports_Absolute(t *testing.T) {
	chdirTest(t, tsImportProjDir)
	p := NewTypeScriptParser()

	tests := []struct {
		name              string
		symbolName        string
		wantPathHasSuffix string
		wantStartLine     int
	}{
		{
			name:              `import { add, divide as div } from "mathutils" (plain symbol)`,
			symbolName:        "add",
			wantPathHasSuffix: "mathutils.ts",
			wantStartLine:     1,
		},
		{
			name:              `import { add, divide as div } from "mathutils" (aliased symbol)`,
			symbolName:        "div",
			wantPathHasSuffix: "mathutils.ts",
			wantStartLine:     13,
		},
		{
			name:              `import { subtract as sub } from "mathutils"`,
			symbolName:        "sub",
			wantPathHasSuffix: "mathutils.ts",
			wantStartLine:     5,
		},
		{
			name:       `import * as subUtils from "subpkg/utils"`,
			symbolName: "subUtils",
			// subpkg/utils.ts has no symbol literally named "subUtils"
			// (namespace imports don't resolve to a specific target symbol),
			// so this falls back to line 1.
			wantPathHasSuffix: "subpkg/utils.ts",
			wantStartLine:     1,
		},
		{
			name:              `import greetDefault from "defaultExport" (default import)`,
			symbolName:        "greetDefault",
			wantPathHasSuffix: "defaultExport.ts",
			wantStartLine:     1,
		},
		{
			name:              `import { widgetName } from "@/widget" (project-root "src/" alias)`,
			symbolName:        "widgetName",
			wantPathHasSuffix: "src/widget.ts",
			wantStartLine:     1,
		},
		{
			name:       `const requiredUtils = require("subpkg/utils") (CommonJS)`,
			symbolName: "requiredUtils",
			// subpkg/utils.ts has no symbol literally named "requiredUtils",
			// so this falls back to line 1.
			wantPathHasSuffix: "subpkg/utils.ts",
			wantStartLine:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotLine, err := p.ResolveImports("main.ts", tt.symbolName)
			if err != nil {
				t.Fatalf("ResolveImports(%q, %q) returned unexpected error: %v", "main.ts", tt.symbolName, err)
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

// TestTypeScriptParser_FindReferencesInFile was cross-validated against
// Python's TypeScriptParser.find_references_in_file run against the same
// testdata/typescript/references/*.ts files.
func TestTypeScriptParser_FindReferencesInFile(t *testing.T) {
	p := NewTypeScriptParser()

	tests := []struct {
		name       string
		filePath   string
		symbolName string
		want       []ReferenceData
	}{
		{
			name:       "definition site is excluded",
			filePath:   tsReferencesDir + "/target.ts",
			symbolName: "targetFunc",
			want:       nil,
		},
		{
			name:       "comment mention is ignored, call site is found",
			filePath:   tsReferencesDir + "/caller_a.ts",
			symbolName: "targetFunc",
			want: []ReferenceData{
				{LineNumber: 1, LineContent: "targetFunc();  // actual reference on this line"},
			},
		},
		{
			name:       "parameter name is excluded, later use is found",
			filePath:   tsReferencesDir + "/caller_b.ts",
			symbolName: "targetFunc",
			want: []ReferenceData{
				{LineNumber: 2, LineContent: "const value = targetFunc;"},
			},
		},
		{
			name:       "fast-path: symbol text absent from file",
			filePath:   tsReferencesDir + "/no_mention.ts",
			symbolName: "targetFunc",
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

func TestTypeScriptParser_FindReferencesInFile_FileNotFound(t *testing.T) {
	p := NewTypeScriptParser()
	got, err := p.FindReferencesInFile(tsReferencesDir+"/does_not_exist.ts", "targetFunc")
	if err != nil {
		t.Fatalf("FindReferencesInFile returned unexpected error for a nonexistent file: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d references for a nonexistent file, want 0: %+v", len(got), got)
	}
}
