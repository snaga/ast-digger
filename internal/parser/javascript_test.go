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
	jsSampleFile    = "../../testdata/javascript/sample.js"
	jsxSampleFile   = "../../testdata/javascript/sample.jsx"
	jsImportProjDir = "../../testdata/javascript/importproj"
	jsReferencesDir = "../../testdata/javascript/references"
)

// TestJavaScriptParser_ParseFile exercises class/function/method/
// arrow-function symbol extraction and JSDoc handling against
// testdata/javascript/sample.js. Expected values were captured from the
// JavaScriptParser implementation itself (there being no Python
// implementation of a JavaScriptParser to cross-validate against, unlike
// TypeScriptParser).
func TestJavaScriptParser_ParseFile(t *testing.T) {
	p := NewJavaScriptParser()
	syms, err := p.ParseFile(jsSampleFile)
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
			wantSignature: "function topLevelFunction(x)",
			wantDocstring: "Doubles x.",
		},
		{
			name:          "class",
			symbolName:    "Greeter",
			wantKind:      "class",
			wantStartLine: 11,
			wantEndLine:   25,
			wantSignature: "class Greeter",
			wantDocstring: "A friendly greeter.",
		},
		{
			name:          "constructor method",
			symbolName:    "constructor",
			wantKind:      "method",
			wantParent:    "Greeter",
			wantStartLine: 15,
			wantEndLine:   17,
			wantSignature: "constructor(name)",
			wantDocstring: "Store the name.",
		},
		{
			name:          "static method",
			symbolName:    "shout",
			wantKind:      "method",
			wantParent:    "Greeter",
			wantStartLine: 22,
			wantEndLine:   24,
			wantSignature: "static shout(message)",
			wantDocstring: "Shout a message.",
		},
		{
			name:          "arrow function assigned to const",
			symbolName:    "arrowGreet",
			wantKind:      "function",
			wantStartLine: 30,
			wantEndLine:   32,
			wantSignature: "const arrowGreet = (name) =>",
			wantDocstring: "An arrow function assigned to a const.",
		},
		{
			name:          "function without JSDoc",
			symbolName:    "noDocstringFunction",
			wantKind:      "function",
			wantStartLine: 34,
			wantEndLine:   36,
			wantSignature: "function noDocstringFunction()",
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
			if sym.FilePath == nil || *sym.FilePath != jsSampleFile {
				t.Errorf("FilePath = %v, want %q", sym.FilePath, jsSampleFile)
			}
		})
	}
}

// TestJavaScriptParser_ParseFile_JSX verifies that ".jsx" files (JSX-
// returning function declarations and arrow functions) are parsed
// correctly by the single tree-sitter-javascript grammar, without needing a
// dedicated JSX/TSX-style grammar switch.
func TestJavaScriptParser_ParseFile_JSX(t *testing.T) {
	p := NewJavaScriptParser()
	syms, err := p.ParseFile(jsxSampleFile)
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
			wantSignature: "function GreetingBanner(props)",
			wantDocstring: "Renders a greeting banner.",
		},
		{
			name:          "JSX-returning arrow function",
			symbolName:    "SmallBanner",
			wantKind:      "function",
			wantStartLine: 13,
			wantEndLine:   15,
			wantSignature: "const SmallBanner = (props) =>",
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
			if sym.FilePath == nil || *sym.FilePath != jsxSampleFile {
				t.Errorf("FilePath = %v, want %q", sym.FilePath, jsxSampleFile)
			}
		})
	}
}

func TestJavaScriptParser_ParseFile_FileNotFound(t *testing.T) {
	p := NewJavaScriptParser()
	_, err := p.ParseFile("../../testdata/javascript/does_not_exist.js")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestJavaScriptParser_ParseFile_NoSymbols exercises ParseFile against a
// file with no class/function declarations, guarding against a nil-vs-
// empty-slice regression (ParseFile must return a non-nil empty slice so it
// marshals to JSON as "[]" rather than "null").
func TestJavaScriptParser_ParseFile_NoSymbols(t *testing.T) {
	dir := t.TempDir()
	noSymbolsFile := filepath.Join(dir, "no_symbols.js")
	if err := os.WriteFile(noSymbolsFile, []byte("const x = 1;\nconst y = 2;\n"), 0o644); err != nil {
		t.Fatalf("failed to write test fixture: %v", err)
	}

	p := NewJavaScriptParser()
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

// TestJavaScriptParser_ExtractSymbol was cross-validated against the
// JavaScriptParser.ParseFile output for testdata/javascript/sample.js.
func TestJavaScriptParser_ExtractSymbol(t *testing.T) {
	p := NewJavaScriptParser()

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
			wantEndLine:   25,
			wantCode: "class Greeter {\n" +
				"  /**\n" +
				"   * Store the name.\n" +
				"   */\n" +
				"  constructor(name) {\n" +
				"    this.name = name;\n" +
				"  }\n" +
				"\n" +
				"  /**\n" +
				"   * Shout a message.\n" +
				"   */\n" +
				"  static shout(message) {\n" +
				"    return message.toUpperCase();\n" +
				"  }\n" +
				"}",
		},
		{
			name:          "static method via dotted path",
			symbolPath:    "Greeter.shout",
			wantKind:      "method",
			wantParent:    "Greeter",
			wantStartLine: 22,
			wantEndLine:   24,
			wantCode: "static shout(message) {\n" +
				"    return message.toUpperCase();\n" +
				"  }",
		},
		{
			name:          "constructor via dotted path",
			symbolPath:    "Greeter.constructor",
			wantKind:      "method",
			wantParent:    "Greeter",
			wantStartLine: 15,
			wantEndLine:   17,
			wantCode: "constructor(name) {\n" +
				"    this.name = name;\n" +
				"  }",
		},
		{
			name:          "top-level function",
			symbolPath:    "topLevelFunction",
			wantKind:      "function",
			wantStartLine: 4,
			wantEndLine:   6,
			wantCode: "function topLevelFunction(x) {\n" +
				"  return x * 2;\n" +
				"}",
		},
		{
			// foundNode is the lexical_declaration statement itself
			// (matching classifyJSNode's actualNode == node behavior for
			// this form), so Code includes the "const ... = ... ;" wrapper
			// verbatim.
			name:          "arrow function assigned to const",
			symbolPath:    "arrowGreet",
			wantKind:      "function",
			wantStartLine: 30,
			wantEndLine:   32,
			wantCode: "const arrowGreet = (name) => {\n" +
				"  return `Hello, ${name}!`;\n" +
				"};",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sym, err := p.ExtractSymbol(jsSampleFile, tt.symbolPath)
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
			gotCode := ""
			if sym.Code != nil {
				gotCode = strings.ReplaceAll(*sym.Code, "\r\n", "\n")
			}
			if gotCode != tt.wantCode {
				t.Errorf("Code = %q, want %q", gotCode, tt.wantCode)
			}
			if sym.FilePath == nil || *sym.FilePath != jsSampleFile {
				t.Errorf("FilePath = %v, want %q", sym.FilePath, jsSampleFile)
			}
		})
	}
}

// TestJavaScriptParser_ExtractSymbol_JSX exercises ExtractSymbol against
// testdata/javascript/sample.jsx, covering both a JSX-returning function
// declaration and a JSX-returning arrow function.
func TestJavaScriptParser_ExtractSymbol_JSX(t *testing.T) {
	p := NewJavaScriptParser()

	tests := []struct {
		name          string
		symbolPath    string
		wantKind      string
		wantStartLine int
		wantEndLine   int
	}{
		{
			name:          "JSX-returning function declaration",
			symbolPath:    "GreetingBanner",
			wantKind:      "function",
			wantStartLine: 6,
			wantEndLine:   8,
		},
		{
			name:          "JSX-returning arrow function",
			symbolPath:    "SmallBanner",
			wantKind:      "function",
			wantStartLine: 13,
			wantEndLine:   15,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sym, err := p.ExtractSymbol(jsxSampleFile, tt.symbolPath)
			if err != nil {
				t.Fatalf("ExtractSymbol(%q) returned unexpected error: %v", tt.symbolPath, err)
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
			if sym.Code == nil || !strings.Contains(*sym.Code, "return") {
				t.Errorf("Code = %q, want it to contain a JSX return statement", deref(sym.Code))
			}
		})
	}
}

func TestJavaScriptParser_ExtractSymbol_NotFound(t *testing.T) {
	p := NewJavaScriptParser()

	tests := []struct {
		name       string
		symbolPath string
	}{
		{name: "unknown top-level symbol", symbolPath: "NoSuchClass"},
		{name: "unknown method on known class", symbolPath: "Greeter.nonexistent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.ExtractSymbol(jsSampleFile, tt.symbolPath)
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

// TestJavaScriptParser_ExtractSymbol_NotFound_Suggestions verifies that a
// not-found symbol path that's a near-miss for an existing symbol gets that
// symbol suggested (via apperr.SymbolNotFoundError.Suggestions, surfaced in
// Error()'s "Did you mean?" section), while one with no close match in the
// file gets none.
func TestJavaScriptParser_ExtractSymbol_NotFound_Suggestions(t *testing.T) {
	p := NewJavaScriptParser()

	t.Run("near-miss symbol path is suggested", func(t *testing.T) {
		_, err := p.ExtractSymbol(jsSampleFile, "Greeter.shot")
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
		_, err := p.ExtractSymbol(jsSampleFile, "CompletelyUnrelatedXyz123")
		var symErr *apperr.SymbolNotFoundError
		if !errors.As(err, &symErr) {
			t.Fatalf("expected *apperr.SymbolNotFoundError, got %T: %v", err, err)
		}
		if len(symErr.Suggestions) != 0 {
			t.Errorf("Suggestions = %v, want none", symErr.Suggestions)
		}
	})
}

func TestJavaScriptParser_ExtractSymbol_FileNotFound(t *testing.T) {
	p := NewJavaScriptParser()
	_, err := p.ExtractSymbol("../../testdata/javascript/does_not_exist.js", "Foo")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestJavaScriptParser_ImplementsParser is a compile-time-and-runtime
// sanity check that JavaScriptParser satisfies the Parser interface in
// full.
func TestJavaScriptParser_ImplementsParser(t *testing.T) {
	var _ Parser = NewJavaScriptParser()
}

// TestJavaScriptParser_GenerateSkeleton verifies that GenerateSkeleton
// replaces every function/method/arrow-function body with "{ /* ... */ }",
// leaving JSDoc comments and signatures byte-for-byte identical to
// testdata/javascript/sample.js.
func TestJavaScriptParser_GenerateSkeleton(t *testing.T) {
	p := NewJavaScriptParser()
	got, err := p.GenerateSkeleton(jsSampleFile)
	if err != nil {
		t.Fatalf("GenerateSkeleton returned unexpected error: %v", err)
	}

	want := strings.Join([]string{
		`/**`,
		` * Doubles x.`,
		` */`,
		`export function topLevelFunction(x) { /* ... */ }`,
		``,
		`/**`,
		` * A friendly greeter.`,
		` */`,
		`export class Greeter {`,
		`  /**`,
		`   * Store the name.`,
		`   */`,
		`  constructor(name) { /* ... */ }`,
		``,
		`  /**`,
		`   * Shout a message.`,
		`   */`,
		`  static shout(message) { /* ... */ }`,
		`}`,
		``,
		`/**`,
		` * An arrow function assigned to a const.`,
		` */`,
		`export const arrowGreet = (name) => { /* ... */ };`,
		``,
		`function noDocstringFunction() { /* ... */ }`,
		``,
	}, "\n")

	if strings.ReplaceAll(got, "\r\n", "\n") != want {
		t.Errorf("GenerateSkeleton =\n%s\nwant\n%s", got, want)
	}
}

// TestJavaScriptParser_GenerateSkeleton_JSX verifies that GenerateSkeleton
// replaces function/arrow-function bodies (including ones returning JSX)
// with "{ /* ... */ }", leaving the import statement and JSDoc comments
// untouched, against testdata/javascript/sample.jsx.
func TestJavaScriptParser_GenerateSkeleton_JSX(t *testing.T) {
	p := NewJavaScriptParser()
	got, err := p.GenerateSkeleton(jsxSampleFile)
	if err != nil {
		t.Fatalf("GenerateSkeleton returned unexpected error: %v", err)
	}

	want := strings.Join([]string{
		`import * as React from "react";`,
		``,
		`/**`,
		` * Renders a greeting banner.`,
		` */`,
		`export function GreetingBanner(props) { /* ... */ }`,
		``,
		`/**`,
		` * An arrow-function React component.`,
		` */`,
		`export const SmallBanner = (props) => { /* ... */ };`,
		``,
	}, "\n")

	if strings.ReplaceAll(got, "\r\n", "\n") != want {
		t.Errorf("GenerateSkeleton =\n%s\nwant\n%s", got, want)
	}
}

func TestJavaScriptParser_GenerateSkeleton_FileNotFound(t *testing.T) {
	p := NewJavaScriptParser()
	_, err := p.GenerateSkeleton("../../testdata/javascript/does_not_exist.js")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestJavaScriptParser_ResolveImports_Relative covers relative import
// resolution ("./" / "../" module specifiers), which does not depend on the
// process's working directory.
func TestJavaScriptParser_ResolveImports_Relative(t *testing.T) {
	p := NewJavaScriptParser()
	callerFile := jsImportProjDir + "/subpkg/caller.js"

	tests := []struct {
		name              string
		symbolName        string
		wantPathHasSuffix string
		wantStartLine     int
	}{
		{
			name:              "import { utilFunc } from './utils'",
			symbolName:        "utilFunc",
			wantPathHasSuffix: "testdata/javascript/importproj/subpkg/utils.js",
			wantStartLine:     1,
		},
		{
			name:              "import { add } from '../mathutils'",
			symbolName:        "add",
			wantPathHasSuffix: "testdata/javascript/importproj/mathutils.js",
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

// TestJavaScriptParser_ResolveImports_UnresolvableModule verifies that an
// import whose module specifier cannot be located on disk (none of
// ".js"/".jsx"/".mjs"/".cjs"/"index.js"/"index.jsx"/"index.mjs"/"index.cjs"
// exist) returns an *apperr.FileAccessError.
func TestJavaScriptParser_ResolveImports_UnresolvableModule(t *testing.T) {
	p := NewJavaScriptParser()
	_, _, err := p.ResolveImports(jsImportProjDir+"/bad_caller.js", "something")
	if err == nil {
		t.Fatal("expected an error for an unresolvable imported module, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestJavaScriptParser_ResolveImports_SymbolNotImported verifies that a
// symbol which is not imported anywhere in the target file returns an
// *apperr.SymbolNotFoundError.
func TestJavaScriptParser_ResolveImports_SymbolNotImported(t *testing.T) {
	p := NewJavaScriptParser()
	_, _, err := p.ResolveImports(jsImportProjDir+"/main.js", "totally_absent_symbol")
	if err == nil {
		t.Fatal("expected an error for a symbol that is not imported, got nil")
	}
	var symbolErr *apperr.SymbolNotFoundError
	if !errors.As(err, &symbolErr) {
		t.Errorf("expected *apperr.SymbolNotFoundError, got %T: %v", err, err)
	}
}

func TestJavaScriptParser_ResolveImports_FileNotFound(t *testing.T) {
	p := NewJavaScriptParser()
	_, _, err := p.ResolveImports(jsImportProjDir+"/does_not_exist.js", "anything")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestJavaScriptParser_ResolveImports_Absolute covers absolute (bare
// module-specifier) import resolution, the "@/" -> "<project_root>/src/"
// alias, named/aliased-named/namespace/default imports, an "index.js"
// directory-module fallback, and a CommonJS "const x = require(...)"
// declaration -- all resolved relative to the process's current working
// directory, which is temporarily changed to testdata/javascript/importproj
// so that it plays the role of the "project root".
func TestJavaScriptParser_ResolveImports_Absolute(t *testing.T) {
	chdirTest(t, jsImportProjDir)
	p := NewJavaScriptParser()

	tests := []struct {
		name              string
		symbolName        string
		wantPathHasSuffix string
		wantStartLine     int
	}{
		{
			name:              `import { add, divide as div } from "mathutils" (plain symbol)`,
			symbolName:        "add",
			wantPathHasSuffix: "mathutils.js",
			wantStartLine:     1,
		},
		{
			name:              `import { add, divide as div } from "mathutils" (aliased symbol)`,
			symbolName:        "div",
			wantPathHasSuffix: "mathutils.js",
			wantStartLine:     13,
		},
		{
			name:              `import { subtract as sub } from "mathutils"`,
			symbolName:        "sub",
			wantPathHasSuffix: "mathutils.js",
			wantStartLine:     5,
		},
		{
			name:       `import * as subUtils from "subpkg/utils"`,
			symbolName: "subUtils",
			// subpkg/utils.js has no symbol literally named "subUtils"
			// (namespace imports don't resolve to a specific target
			// symbol), so this falls back to line 1.
			wantPathHasSuffix: "subpkg/utils.js",
			wantStartLine:     1,
		},
		{
			name:              `import greetDefault from "defaultExport" (default import)`,
			symbolName:        "greetDefault",
			wantPathHasSuffix: "defaultExport.js",
			wantStartLine:     1,
		},
		{
			name:              `import { widgetName } from "@/widget" (project-root "src/" alias)`,
			symbolName:        "widgetName",
			wantPathHasSuffix: "src/widget.js",
			wantStartLine:     1,
		},
		{
			name:              `import { uiThing } from "./ui" (index.js directory-module fallback)`,
			symbolName:        "uiThing",
			wantPathHasSuffix: "ui/index.js",
			wantStartLine:     1,
		},
		{
			name:       `const requiredUtils = require("subpkg/utils") (CommonJS)`,
			symbolName: "requiredUtils",
			// subpkg/utils.js has no symbol literally named
			// "requiredUtils", so this falls back to line 1.
			wantPathHasSuffix: "subpkg/utils.js",
			wantStartLine:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotLine, err := p.ResolveImports("main.js", tt.symbolName)
			if err != nil {
				t.Fatalf("ResolveImports(%q, %q) returned unexpected error: %v", "main.js", tt.symbolName, err)
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

// TestJavaScriptParser_FindReferencesInFile exercises reference discovery
// against testdata/javascript/references/*.js, including exclusion of
// definition sites, comment mentions, plain string-literal mentions, and
// template-literal text (while still finding genuine "${...}" substitution
// references within a template literal).
func TestJavaScriptParser_FindReferencesInFile(t *testing.T) {
	p := NewJavaScriptParser()

	tests := []struct {
		name       string
		filePath   string
		symbolName string
		want       []ReferenceData
	}{
		{
			name:       "definition site is excluded",
			filePath:   jsReferencesDir + "/target.js",
			symbolName: "targetFunc",
			want:       nil,
		},
		{
			name:       "comment mention is ignored, call site is found",
			filePath:   jsReferencesDir + "/caller_a.js",
			symbolName: "targetFunc",
			want: []ReferenceData{
				{LineNumber: 1, LineContent: "targetFunc();  // actual reference on this line"},
			},
		},
		{
			name:       "parameter name is excluded, later use is found",
			filePath:   jsReferencesDir + "/caller_b.js",
			symbolName: "targetFunc",
			want: []ReferenceData{
				{LineNumber: 2, LineContent: "const value = targetFunc;"},
			},
		},
		{
			name:       "fast-path: symbol text absent from file",
			filePath:   jsReferencesDir + "/no_mention.js",
			symbolName: "targetFunc",
			want:       nil,
		},
		{
			name:       "string-literal mention is ignored, template-literal substitution is found",
			filePath:   jsReferencesDir + "/template_literal.js",
			symbolName: "targetFunc",
			want: []ReferenceData{
				{LineNumber: 2, LineContent: "const templated = `calling ${targetFunc()} inside a template literal`;"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.FindReferencesInFile(tt.filePath, tt.symbolName)
			if err != nil {
				t.Fatalf("FindReferencesInFile(%q, %q) returned unexpected error: %v", tt.filePath, tt.symbolName, err)
			}
			// FindReferencesInFile must return a non-nil (possibly empty)
			// slice so JSON marshaling produces "[]" rather than "null".
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

func TestJavaScriptParser_FindReferencesInFile_FileNotFound(t *testing.T) {
	p := NewJavaScriptParser()
	got, err := p.FindReferencesInFile(jsReferencesDir+"/does_not_exist.js", "targetFunc")
	if err != nil {
		t.Fatalf("FindReferencesInFile returned unexpected error for a nonexistent file: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d references for a nonexistent file, want 0: %+v", len(got), got)
	}
}
