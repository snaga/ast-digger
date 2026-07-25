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
	javaSampleFile    = "../../testdata/java/sample.java"
	javaImportProjDir = "../../testdata/java/importproj"
	javaReferencesDir = "../../testdata/java/references"
)

// TestJavaParser_ParseFile exercises class/interface/enum/method/constructor
// symbol extraction, Javadoc handling, and enclosing-type-as-parent mapping
// for methods/constructors, against testdata/java/sample.java.
func TestJavaParser_ParseFile(t *testing.T) {
	p := NewJavaParser()
	syms, err := p.ParseFile(javaSampleFile)
	if err != nil {
		t.Fatalf("ParseFile returned unexpected error: %v", err)
	}

	// The top-level class itself is checked directly (rather than via
	// memberTests below), since "SampleClass" the class and "SampleClass"
	// its constructor share the same Name and are only distinguished by
	// Parent.
	classSym := findSymbol(syms, "SampleClass")
	if classSym == nil {
		t.Fatalf("symbol %q not found in ParseFile results", "SampleClass")
	}
	if classSym.Kind != "class" {
		t.Errorf("SampleClass Kind = %q, want %q", classSym.Kind, "class")
	}
	if classSym.StartLine != 9 || classSym.EndLine != 45 {
		t.Errorf("SampleClass StartLine/EndLine = %d/%d, want 9/45", classSym.StartLine, classSym.EndLine)
	}
	if deref(classSym.Signature) != "public class SampleClass" {
		t.Errorf("SampleClass Signature = %q, want %q", deref(classSym.Signature), "public class SampleClass")
	}
	wantClassDoc := "/**\n * SampleClass demonstrates fields, a constructor, plain methods, and an\n * overloaded method pair for JavaParser test coverage.\n */"
	if strings.ReplaceAll(deref(classSym.Docstring), "\r\n", "\n") != wantClassDoc {
		t.Errorf("SampleClass Docstring = %q, want %q", deref(classSym.Docstring), wantClassDoc)
	}

	// findSymbolWithParent returns the first symbol in syms whose Name and
	// Parent both match, or nil if none is found. Used below because
	// "SampleClass" (the class) and "SampleClass" (its constructor) share
	// the same Name, distinguished only by Parent.
	findSymbolWithParent := func(name, parent string) *SymbolData {
		for i := range syms {
			if syms[i].Name == name && deref(syms[i].Parent) == parent {
				return &syms[i]
			}
		}
		return nil
	}

	memberTests := []struct {
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
			name:          "constructor",
			symbolName:    "SampleClass",
			wantKind:      "method",
			wantParent:    "SampleClass",
			wantStartLine: 18,
			wantEndLine:   21,
			wantSignature: "public SampleClass(String name)",
			wantDocstring: "/**\n     * Constructs a SampleClass with the given name.\n     *\n     * @param name the name to use\n     */",
		},
		{
			name:          "method with Javadoc",
			symbolName:    "greet",
			wantKind:      "method",
			wantParent:    "SampleClass",
			wantStartLine: 26,
			wantEndLine:   28,
			wantSignature: "public String greet()",
			wantDocstring: "/**\n     * Returns a greeting for this instance.\n     */",
		},
		{
			name:          "first overload",
			symbolName:    "add",
			wantKind:      "method",
			wantParent:    "SampleClass",
			wantStartLine: 33,
			wantEndLine:   35,
			wantSignature: "public int add(int a, int b)",
			wantDocstring: "/**\n     * Adds two integers.\n     */",
		},
		{
			name:          "method preceded by a plain (non-Javadoc) comment has no docstring",
			symbolName:    "noDoc",
			wantKind:      "method",
			wantParent:    "SampleClass",
			wantStartLine: 43,
			wantEndLine:   44,
			wantSignature: "public void noDoc()",
			wantDocstring: "",
		},
		{
			name:          "interface with Javadoc",
			symbolName:    "Greetable",
			wantKind:      "interface",
			wantStartLine: 50,
			wantEndLine:   55,
			wantSignature: "interface Greetable",
			wantDocstring: "/**\n * Greetable describes something that can produce a greeting.\n */",
		},
		{
			name:          "interface method (no body)",
			symbolName:    "greet",
			wantKind:      "method",
			wantParent:    "Greetable",
			wantStartLine: 54,
			wantEndLine:   54,
			wantSignature: "String greet();",
			wantDocstring: "/**\n     * Produces a greeting string.\n     */",
		},
		{
			name:          "enum with Javadoc",
			symbolName:    "Color",
			wantKind:      "enum",
			wantStartLine: 60,
			wantEndLine:   71,
			wantSignature: "enum Color",
			wantDocstring: "/**\n * Color enumerates a small set of colors.\n */",
		},
		{
			name:          "method inside enum",
			symbolName:    "isRed",
			wantKind:      "method",
			wantParent:    "Color",
			wantStartLine: 68,
			wantEndLine:   70,
			wantSignature: "public boolean isRed()",
			wantDocstring: "/**\n     * Returns whether this color is RED.\n     */",
		},
	}

	for _, tt := range memberTests {
		t.Run(tt.name, func(t *testing.T) {
			var sym *SymbolData
			if tt.wantParent != "" {
				sym = findSymbolWithParent(tt.symbolName, tt.wantParent)
			} else {
				sym = findSymbol(syms, tt.symbolName)
			}
			if sym == nil {
				t.Fatalf("symbol %q (parent %q) not found in ParseFile results", tt.symbolName, tt.wantParent)
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
			if strings.ReplaceAll(deref(sym.Docstring), "\r\n", "\n") != tt.wantDocstring {
				t.Errorf("Docstring = %q, want %q", deref(sym.Docstring), tt.wantDocstring)
			}
			if sym.FilePath == nil || *sym.FilePath != javaSampleFile {
				t.Errorf("FilePath = %v, want %q", sym.FilePath, javaSampleFile)
			}
		})
	}
}

// TestJavaParser_ParseFile_OverloadedMethodsBothPresent verifies that both
// overloads of add() are extracted as separate "method" symbols (rather
// than one clobbering the other).
func TestJavaParser_ParseFile_OverloadedMethodsBothPresent(t *testing.T) {
	p := NewJavaParser()
	syms, err := p.ParseFile(javaSampleFile)
	if err != nil {
		t.Fatalf("ParseFile returned unexpected error: %v", err)
	}

	count := 0
	for _, s := range syms {
		if s.Name == "add" && deref(s.Parent) == "SampleClass" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("found %d symbols named add() under SampleClass, want 2 (both overloads)", count)
	}
}

// TestJavaParser_ParseFile_NoFieldSymbols verifies that field declarations
// (field_declaration, e.g. SampleClass's "name"/"value" fields) are never
// extracted as symbols, matching design.md's "properties are not
// individually extracted" policy.
func TestJavaParser_ParseFile_NoFieldSymbols(t *testing.T) {
	p := NewJavaParser()
	syms, err := p.ParseFile(javaSampleFile)
	if err != nil {
		t.Fatalf("ParseFile returned unexpected error: %v", err)
	}

	for _, fieldName := range []string{"name", "value"} {
		if sym := findSymbol(syms, fieldName); sym != nil {
			t.Errorf("field %q was extracted as a symbol (Kind=%q); fields must not be extracted", fieldName, sym.Kind)
		}
	}
}

func TestJavaParser_ParseFile_FileNotFound(t *testing.T) {
	p := NewJavaParser()
	_, err := p.ParseFile("../../testdata/java/does_not_exist.java")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestJavaParser_ParseFile_NoSymbols exercises ParseFile against a file with
// no class/interface/enum/method/constructor declarations, guarding against
// the nil-vs-empty-slice regression documented in the equivalent Go/Python/
// TypeScript tests.
func TestJavaParser_ParseFile_NoSymbols(t *testing.T) {
	dir := t.TempDir()
	noSymbolsFile := filepath.Join(dir, "NoSymbols.java")
	if err := os.WriteFile(noSymbolsFile, []byte("package empty;\n"), 0o644); err != nil {
		t.Fatalf("failed to write test fixture: %v", err)
	}

	p := NewJavaParser()
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

// TestJavaParser_ExtractSymbol exercises ExtractSymbol against
// testdata/java/sample.java, covering bare top-level symbols
// (class/interface/enum) and dotted "ClassName.memberName" paths (including
// a constructor, whose member name equals its class name).
func TestJavaParser_ExtractSymbol(t *testing.T) {
	p := NewJavaParser()

	tests := []struct {
		name          string
		symbolPath    string
		wantKind      string
		wantParent    string
		wantStartLine int
		wantEndLine   int
		wantSignature string
		wantCode      string
	}{
		{
			name:          "whole class",
			symbolPath:    "SampleClass",
			wantKind:      "class",
			wantStartLine: 9,
			wantEndLine:   45,
			wantSignature: "public class SampleClass",
			wantCode: "public class SampleClass {\n    private String name;\n    private int value;\n\n    " +
				"/**\n     * Constructs a SampleClass with the given name.\n     *\n     * @param name the name to use\n     */\n    " +
				"public SampleClass(String name) {\n        this.name = name;\n        this.value = 0;\n    }\n\n    " +
				"/**\n     * Returns a greeting for this instance.\n     */\n    public String greet() {\n        return \"Hello, \" + name;\n    }\n\n    " +
				"/**\n     * Adds two integers.\n     */\n    public int add(int a, int b) {\n        return a + b;\n    }\n\n    " +
				"// Overload of add(int, int); this is a plain comment, not Javadoc, so\n    // this method must have no Docstring.\n    " +
				"public int add(int a, int b, int c) {\n        return a + b + c;\n    }\n\n    public void noDoc() {\n    }\n}",
		},
		{
			name:          "constructor via dotted path",
			symbolPath:    "SampleClass.SampleClass",
			wantKind:      "method",
			wantParent:    "SampleClass",
			wantStartLine: 18,
			wantEndLine:   21,
			wantSignature: "public SampleClass(String name)",
			wantCode:      "public SampleClass(String name) {\n        this.name = name;\n        this.value = 0;\n    }",
		},
		{
			name:          "method via dotted path",
			symbolPath:    "SampleClass.greet",
			wantKind:      "method",
			wantParent:    "SampleClass",
			wantStartLine: 26,
			wantEndLine:   28,
			wantSignature: "public String greet()",
			wantCode:      "public String greet() {\n        return \"Hello, \" + name;\n    }",
		},
		{
			name:          "overloaded method: first match wins",
			symbolPath:    "SampleClass.add",
			wantKind:      "method",
			wantParent:    "SampleClass",
			wantStartLine: 33,
			wantEndLine:   35,
			wantSignature: "public int add(int a, int b)",
			wantCode:      "public int add(int a, int b) {\n        return a + b;\n    }",
		},
		{
			name:          "interface",
			symbolPath:    "Greetable",
			wantKind:      "interface",
			wantStartLine: 50,
			wantEndLine:   55,
			wantSignature: "interface Greetable",
			wantCode:      "interface Greetable {\n    /**\n     * Produces a greeting string.\n     */\n    String greet();\n}",
		},
		{
			name:          "interface method via dotted path",
			symbolPath:    "Greetable.greet",
			wantKind:      "method",
			wantParent:    "Greetable",
			wantStartLine: 54,
			wantEndLine:   54,
			wantSignature: "String greet();",
			wantCode:      "String greet();",
		},
		{
			name:          "enum",
			symbolPath:    "Color",
			wantKind:      "enum",
			wantStartLine: 60,
			wantEndLine:   71,
			wantSignature: "enum Color",
			wantCode:      "enum Color {\n    RED,\n    GREEN,\n    BLUE;\n\n    /**\n     * Returns whether this color is RED.\n     */\n    public boolean isRed() {\n        return this == RED;\n    }\n}",
		},
		{
			name:          "enum method via dotted path",
			symbolPath:    "Color.isRed",
			wantKind:      "method",
			wantParent:    "Color",
			wantStartLine: 68,
			wantEndLine:   70,
			wantSignature: "public boolean isRed()",
			wantCode:      "public boolean isRed() {\n        return this == RED;\n    }",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sym, err := p.ExtractSymbol(javaSampleFile, tt.symbolPath)
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
			if deref(sym.Signature) != tt.wantSignature {
				t.Errorf("Signature = %q, want %q", deref(sym.Signature), tt.wantSignature)
			}
			gotCode := ""
			if sym.Code != nil {
				gotCode = strings.ReplaceAll(*sym.Code, "\r\n", "\n")
			}
			if gotCode != tt.wantCode {
				t.Errorf("Code = %q, want %q", gotCode, tt.wantCode)
			}
			if sym.FilePath == nil || *sym.FilePath != javaSampleFile {
				t.Errorf("FilePath = %v, want %q", sym.FilePath, javaSampleFile)
			}
		})
	}
}

func TestJavaParser_ExtractSymbol_NotFound(t *testing.T) {
	p := NewJavaParser()

	tests := []struct {
		name       string
		symbolPath string
	}{
		{name: "unknown top-level symbol", symbolPath: "NoSuchClass"},
		{name: "unknown method on known class", symbolPath: "SampleClass.noSuchMethod"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.ExtractSymbol(javaSampleFile, tt.symbolPath)
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

func TestJavaParser_ExtractSymbol_FileNotFound(t *testing.T) {
	p := NewJavaParser()
	_, err := p.ExtractSymbol("../../testdata/java/does_not_exist.java", "Foo")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestJavaParser_ImplementsParser is a compile-time-and-runtime sanity check
// that JavaParser satisfies the Parser interface in full.
func TestJavaParser_ImplementsParser(t *testing.T) {
	var _ Parser = NewJavaParser()
}

// TestJavaParser_GenerateSkeleton verifies that GenerateSkeleton replaces
// every method/constructor body with "{ /* ... */ }", leaving Javadoc
// comments, signatures, and modifiers byte-for-byte identical to
// testdata/java/sample.java. Interface method declarations (which have no
// body) and enum constants are left untouched.
func TestJavaParser_GenerateSkeleton(t *testing.T) {
	p := NewJavaParser()
	got, err := p.GenerateSkeleton(javaSampleFile)
	if err != nil {
		t.Fatalf("GenerateSkeleton returned unexpected error: %v", err)
	}

	want := strings.Join([]string{
		`package com.example.sample;`,
		``,
		`import java.util.List;`,
		``,
		`/**`,
		` * SampleClass demonstrates fields, a constructor, plain methods, and an`,
		` * overloaded method pair for JavaParser test coverage.`,
		` */`,
		`public class SampleClass {`,
		`    private String name;`,
		`    private int value;`,
		``,
		`    /**`,
		`     * Constructs a SampleClass with the given name.`,
		`     *`,
		`     * @param name the name to use`,
		`     */`,
		`    public SampleClass(String name) { /* ... */ }`,
		``,
		`    /**`,
		`     * Returns a greeting for this instance.`,
		`     */`,
		`    public String greet() { /* ... */ }`,
		``,
		`    /**`,
		`     * Adds two integers.`,
		`     */`,
		`    public int add(int a, int b) { /* ... */ }`,
		``,
		`    // Overload of add(int, int); this is a plain comment, not Javadoc, so`,
		`    // this method must have no Docstring.`,
		`    public int add(int a, int b, int c) { /* ... */ }`,
		``,
		`    public void noDoc() { /* ... */ }`,
		`}`,
		``,
		`/**`,
		` * Greetable describes something that can produce a greeting.`,
		` */`,
		`interface Greetable {`,
		`    /**`,
		`     * Produces a greeting string.`,
		`     */`,
		`    String greet();`,
		`}`,
		``,
		`/**`,
		` * Color enumerates a small set of colors.`,
		` */`,
		`enum Color {`,
		`    RED,`,
		`    GREEN,`,
		`    BLUE;`,
		``,
		`    /**`,
		`     * Returns whether this color is RED.`,
		`     */`,
		`    public boolean isRed() { /* ... */ }`,
		`}`,
		``,
	}, "\n")

	if strings.ReplaceAll(got, "\r\n", "\n") != want {
		t.Errorf("GenerateSkeleton =\n%s\nwant\n%s", got, want)
	}
}

func TestJavaParser_GenerateSkeleton_FileNotFound(t *testing.T) {
	p := NewJavaParser()
	_, err := p.GenerateSkeleton("../../testdata/java/does_not_exist.java")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestJavaParser_ResolveImports_SingleImport covers plain (non-wildcard)
// import resolution, using the "src/main/java" source-root convention under
// testdata/java/importproj.
func TestJavaParser_ResolveImports_SingleImport(t *testing.T) {
	chdirTest(t, javaImportProjDir)
	p := NewJavaParser()

	gotPath, gotLine, err := p.ResolveImports("src/main/java/com/example/app/Main.java", "MathUtils")
	if err != nil {
		t.Fatalf("ResolveImports returned unexpected error: %v", err)
	}
	gotPath = filepath.ToSlash(gotPath)
	const wantSuffix = "src/main/java/com/example/util/MathUtils.java"
	if !strings.HasSuffix(gotPath, wantSuffix) {
		t.Errorf("path = %q, want suffix %q", gotPath, wantSuffix)
	}
	if gotLine != 3 {
		t.Errorf("startLine = %d, want %d", gotLine, 3)
	}
}

// TestJavaParser_ResolveImports_Wildcard covers wildcard
// ("import com.example.util.*;") import resolution: multiple candidate
// files exist in the imported package directory, and the correct one must
// be selected based on which one actually declares the requested symbol.
func TestJavaParser_ResolveImports_Wildcard(t *testing.T) {
	chdirTest(t, javaImportProjDir)
	p := NewJavaParser()

	tests := []struct {
		name              string
		symbolName        string
		wantPathHasSuffix string
		wantStartLine     int
	}{
		{
			name:              "MathUtils",
			symbolName:        "MathUtils",
			wantPathHasSuffix: "src/main/java/com/example/util/MathUtils.java",
			wantStartLine:     3,
		},
		{
			name:              "StringUtils",
			symbolName:        "StringUtils",
			wantPathHasSuffix: "src/main/java/com/example/util/StringUtils.java",
			wantStartLine:     3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotLine, err := p.ResolveImports("src/main/java/com/example/app/WildcardCaller.java", tt.symbolName)
			if err != nil {
				t.Fatalf("ResolveImports(%q) returned unexpected error: %v", tt.symbolName, err)
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

// TestJavaParser_ResolveImports_UnresolvableImport verifies that an
// imported package that cannot be located as a local directory returns an
// *apperr.FileAccessError.
func TestJavaParser_ResolveImports_UnresolvableImport(t *testing.T) {
	chdirTest(t, javaImportProjDir)
	p := NewJavaParser()
	_, _, err := p.ResolveImports("src/main/java/com/example/app/BadCaller.java", "Something")
	if err == nil {
		t.Fatal("expected an error for an unresolvable imported package, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestJavaParser_ResolveImports_SymbolNotFoundInImports verifies that a
// symbol which is not declared in any resolvable imported package (here,
// the wildcard-imported "com.example.util" package resolves fine, it just
// doesn't declare the requested symbol) returns an
// *apperr.SymbolNotFoundError, not an *apperr.FileAccessError.
func TestJavaParser_ResolveImports_SymbolNotFoundInImports(t *testing.T) {
	chdirTest(t, javaImportProjDir)
	p := NewJavaParser()
	_, _, err := p.ResolveImports("src/main/java/com/example/app/WildcardCaller.java", "TotallyAbsentSymbol")
	if err == nil {
		t.Fatal("expected an error for a symbol not declared in any resolvable import, got nil")
	}
	var symbolErr *apperr.SymbolNotFoundError
	if !errors.As(err, &symbolErr) {
		t.Errorf("expected *apperr.SymbolNotFoundError, got %T: %v", err, err)
	}
}

func TestJavaParser_ResolveImports_FileNotFound(t *testing.T) {
	p := NewJavaParser()
	_, _, err := p.ResolveImports(javaImportProjDir+"/does_not_exist.java", "MathUtils")
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestJavaParser_FindReferencesInFile documents that, like GolangParser,
// JavaParser.FindReferencesInFile does not exclude definition-site
// identifiers -- only comment (line_comment/block_comment) and
// string-literal occurrences are excluded.
func TestJavaParser_FindReferencesInFile(t *testing.T) {
	p := NewJavaParser()

	tests := []struct {
		name       string
		filePath   string
		symbolName string
		want       []ReferenceData
	}{
		{
			name:       "definition site is included (no is_definition exclusion)",
			filePath:   javaReferencesDir + "/target.java",
			symbolName: "TargetMethod",
			want: []ReferenceData{
				{LineNumber: 4, LineContent: "public static String TargetMethod() {"},
			},
		},
		{
			name:       "comment mention is ignored, call site is found",
			filePath:   javaReferencesDir + "/caller_a.java",
			symbolName: "TargetMethod",
			want: []ReferenceData{
				{LineNumber: 5, LineContent: "Target.TargetMethod(); // actual reference on this line, this comment also mentions TargetMethod"},
			},
		},
		{
			name:       "string literal mention is ignored, method reference is found",
			filePath:   javaReferencesDir + "/caller_b.java",
			symbolName: "TargetMethod",
			want: []ReferenceData{
				{LineNumber: 6, LineContent: "Runnable ref = Target::TargetMethod;"},
			},
		},
		{
			name:       "fast-path: symbol text absent from file",
			filePath:   javaReferencesDir + "/no_mention.java",
			symbolName: "TargetMethod",
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

func TestJavaParser_FindReferencesInFile_FileNotFound(t *testing.T) {
	p := NewJavaParser()
	got, err := p.FindReferencesInFile(javaReferencesDir+"/does_not_exist.java", "TargetMethod")
	if err != nil {
		t.Fatalf("FindReferencesInFile returned unexpected error for a nonexistent file: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d references for a nonexistent file, want 0: %+v", len(got), got)
	}
}
