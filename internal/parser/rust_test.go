package parser

import (
	"os"
	"strings"
	"testing"
)

const rustSampleFile = "../../testdata/rust/sample.rs"

func TestRustParser_ParseFile(t *testing.T) {
	p := NewRustParser()
	syms, err := p.ParseFile(rustSampleFile)
	if err != nil {
		t.Fatalf("ParseFile returned unexpected error: %v", err)
	}

	tests := []struct {
		name          string
		symbolName    string
		wantKind      string
		wantParent    string
		wantDocstring string
	}{
		{
			name:          "top-level function with doc comment",
			symbolName:    "top_level_function",
			wantKind:      "function",
			wantDocstring: "/// Outer doc comment for helper function",
		},
		{
			name:          "struct with doc comment",
			symbolName:    "Greeter",
			wantKind:      "struct",
			wantDocstring: "/// Greeter struct definition",
		},
		{
			name:          "enum with doc comment",
			symbolName:    "Direction",
			wantKind:      "enum",
			wantDocstring: "/// Direction enum",
		},
		{
			name:          "trait (interface) with doc comment",
			symbolName:    "Speak",
			wantKind:      "interface",
			wantDocstring: "/// Speak trait",
		},
		{
			name:       "mod item (type)",
			symbolName: "utils",
			wantKind:   "type",
		},
		{
			name:          "impl method new",
			symbolName:    "new",
			wantKind:      "method",
			wantParent:    "Greeter",
			wantDocstring: "/// New greeter constructor",
		},
		{
			name:          "impl method greet",
			symbolName:    "greet",
			wantKind:      "method",
			wantParent:    "Greeter",
			wantDocstring: "/// Greet method",
		},
		{
			name:       "trait impl method speak",
			symbolName: "speak",
			wantKind:   "method",
			wantParent: "Greeter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var found *SymbolData
			for _, s := range syms {
				if s.Name == tt.symbolName {
					if tt.wantParent != "" {
						if s.Parent != nil && *s.Parent == tt.wantParent {
							found = &s
							break
						}
					} else {
						found = &s
						break
					}
				}
			}
			if found == nil {
				t.Fatalf("symbol %q (parent: %q) not found in ParseFile output", tt.symbolName, tt.wantParent)
			}
			if found.Kind != tt.wantKind {
				t.Errorf("Kind = %q, want %q", found.Kind, tt.wantKind)
			}
			if tt.wantParent != "" {
				if found.Parent == nil || *found.Parent != tt.wantParent {
					pStr := "<nil>"
					if found.Parent != nil {
						pStr = *found.Parent
					}
					t.Errorf("Parent = %q, want %q", pStr, tt.wantParent)
				}
			}
			if tt.wantDocstring != "" {
				if found.Docstring == nil || *found.Docstring != tt.wantDocstring {
					dStr := "<nil>"
					if found.Docstring != nil {
						dStr = *found.Docstring
					}
					t.Errorf("Docstring = %q, want %q", dStr, tt.wantDocstring)
				}
			}
		})
	}
}

func TestRustParser_ExtractSymbol(t *testing.T) {
	p := NewRustParser()

	tests := []struct {
		name       string
		symbolPath string
		wantKind   string
		wantName   string
		wantParent string
	}{
		{
			name:       "top level function",
			symbolPath: "top_level_function",
			wantKind:   "function",
			wantName:   "top_level_function",
		},
		{
			name:       "struct",
			symbolPath: "Greeter",
			wantKind:   "struct",
			wantName:   "Greeter",
		},
		{
			name:       "impl method by dotted path",
			symbolPath: "Greeter.greet",
			wantKind:   "method",
			wantName:   "greet",
			wantParent: "Greeter",
		},
		{
			name:       "trait",
			symbolPath: "Speak",
			wantKind:   "interface",
			wantName:   "Speak",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sym, err := p.ExtractSymbol(rustSampleFile, tt.symbolPath)
			if err != nil {
				t.Fatalf("ExtractSymbol(%q) returned unexpected error: %v", tt.symbolPath, err)
			}
			if sym.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", sym.Name, tt.wantName)
			}
			if sym.Kind != tt.wantKind {
				t.Errorf("Kind = %q, want %q", sym.Kind, tt.wantKind)
			}
			if tt.wantParent != "" {
				if sym.Parent == nil || *sym.Parent != tt.wantParent {
					pStr := "<nil>"
					if sym.Parent != nil {
						pStr = *sym.Parent
					}
					t.Errorf("Parent = %q, want %q", pStr, tt.wantParent)
				}
			}
			if sym.Code == nil || *sym.Code == "" {
				t.Errorf("Code is empty")
			}
		})
	}
}

func TestRustParser_GenerateSkeleton(t *testing.T) {
	p := NewRustParser()
	skel, err := p.GenerateSkeleton(rustSampleFile)
	if err != nil {
		t.Fatalf("GenerateSkeleton returned unexpected error: %v", err)
	}

	if strings.Contains(skel, "\r\n") {
		t.Errorf("GenerateSkeleton output contains CRLF, expected normalized LF")
	}

	if !strings.Contains(skel, "{ /* ... */ }") {
		t.Errorf("GenerateSkeleton output does not contain replacement '{ /* ... */ }'")
	}

	if strings.Contains(skel, "x * 2") {
		t.Errorf("GenerateSkeleton output contains function body content 'x * 2'")
	}

	if !strings.Contains(skel, "pub struct Greeter") {
		t.Errorf("GenerateSkeleton output missing struct definition header")
	}
}

func TestRustParser_NormalizeCRLF(t *testing.T) {
	// Verify CRLF normalization in docstrings and code
	p := NewRustParser()

	tmpFile := t.TempDir() + "/crlf_sample.rs"
	crlfContent := "/// Line 1\r\n/// Line 2\r\nfn crlf_fn() {\r\n    let _a = 1;\r\n}\r\n"
	if err := os.WriteFile(tmpFile, []byte(crlfContent), 0644); err != nil {
		t.Fatalf("Failed to write tmp file: %v", err)
	}

	skel, err := p.GenerateSkeleton(tmpFile)
	if err != nil {
		t.Fatalf("GenerateSkeleton error: %v", err)
	}
	if strings.Contains(skel, "\r\n") {
		t.Errorf("Skeleton still contains CRLF")
	}

	sym, err := p.ExtractSymbol(tmpFile, "crlf_fn")
	if err != nil {
		t.Fatalf("ExtractSymbol error: %v", err)
	}
	if sym.Docstring != nil && strings.Contains(*sym.Docstring, "\r\n") {
		t.Errorf("Docstring still contains CRLF")
	}
}

func TestRustParser_DocComment_AttributeAndBlock(t *testing.T) {
	p := NewRustParser()

	tmpFile := t.TempDir() + "/attr_block_sample.rs"
	content := `/// Doc with attribute
#[derive(Debug)]
#[inline]
fn attr_fn() {}

/** Block doc comment */
#[must_use]
fn block_fn() {}
`
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write tmp file: %v", err)
	}

	syms, err := p.ParseFile(tmpFile)
	if err != nil {
		t.Fatalf("ParseFile error: %v", err)
	}

	var attrFn, blockFn *SymbolData
	for _, s := range syms {
		if s.Name == "attr_fn" {
			attrFn = &s
		} else if s.Name == "block_fn" {
			blockFn = &s
		}
	}

	if attrFn == nil || attrFn.Docstring == nil || *attrFn.Docstring != "/// Doc with attribute" {
		doc := "<nil>"
		if attrFn != nil && attrFn.Docstring != nil {
			doc = *attrFn.Docstring
		}
		t.Errorf("attr_fn docstring = %q, want %q", doc, "/// Doc with attribute")
	}

	if blockFn == nil || blockFn.Docstring == nil || *blockFn.Docstring != "/** Block doc comment */" {
		doc := "<nil>"
		if blockFn != nil && blockFn.Docstring != nil {
			doc = *blockFn.Docstring
		}
		t.Errorf("block_fn docstring = %q, want %q", doc, "/** Block doc comment */")
	}
}

func TestRustParser_ResolveImports(t *testing.T) {
	p := NewRustParser()
	mainFile := "../../testdata/rust/importproj/main.rs"

	tests := []struct {
		name         string
		symbolName   string
		wantFile     string
		wantLine     int
		expectErr    bool
		wantNotFound bool
	}{
		{
			name:       "crate import single name",
			symbolName: "add",
			wantFile:   "testdata/rust/importproj/utils.rs",
			wantLine:   1,
		},
		{
			name:       "crate import grouped name",
			symbolName: "multiply",
			wantFile:   "testdata/rust/importproj/utils.rs",
			wantLine:   5,
		},
		{
			name:       "crate import aliased name",
			symbolName: "div",
			wantFile:   "testdata/rust/importproj/utils.rs",
			wantLine:   9,
		},
		{
			name:       "crate import subpackage mod",
			symbolName: "sub_fn",
			wantFile:   "testdata/rust/importproj/subpkg/helper.rs",
			wantLine:   1,
		},
		{
			name:         "unresolved symbol in imported project",
			symbolName:   "non_existent",
			expectErr:    true,
			wantNotFound: true,
		},
		{
			name:         "external crate / std library (std::fmt)",
			symbolName:   "Display",
			expectErr:    true,
			wantNotFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFile, gotLine, err := p.ResolveImports(mainFile, tt.symbolName)
			if tt.expectErr {
				if err == nil {
					t.Fatalf("ResolveImports(%q) expected error, got nil (file=%s, line=%d)", tt.symbolName, gotFile, gotLine)
				}
				return
			}

			if err != nil {
				t.Fatalf("ResolveImports(%q) unexpected error: %v", tt.symbolName, err)
			}

			// Normalize slashes for comparison
			gotFileNormalized := strings.ReplaceAll(gotFile, "\\", "/")
			wantFileNormalized := strings.ReplaceAll(tt.wantFile, "\\", "/")
			if !strings.HasSuffix(gotFileNormalized, wantFileNormalized) && gotFileNormalized != wantFileNormalized {
				t.Errorf("File = %q, want %q", gotFileNormalized, wantFileNormalized)
			}
			if gotLine != tt.wantLine {
				t.Errorf("Line = %d, want %d", gotLine, tt.wantLine)
			}
		})
	}
}

func TestRustParser_ResolveImports_SrcRootAndMultipleSuper(t *testing.T) {
	p := NewRustParser()
	tmpDir := t.TempDir()

	// Setup structure:
	// tmpDir/Cargo.toml
	// tmpDir/src/main.rs (use crate::utils::src_func;)
	// tmpDir/src/utils.rs (pub fn src_func() {})
	// tmpDir/src/a/b/c/leaf.rs (use super::super::parent_func;)
	// tmpDir/src/a/parent.rs (pub fn parent_func() {}) -- wait, super::super from src/a/b/c is src/a!

	if err := os.WriteFile(tmpDir+"/Cargo.toml", []byte("[package]\nname = \"test\"\nversion = \"0.1.0\"\n"), 0644); err != nil {
		t.Fatalf("failed to write Cargo.toml: %v", err)
	}

	srcDir := tmpDir + "/src"
	abcDir := srcDir + "/a/b/c"
	aDir := srcDir + "/a"
	if err := os.MkdirAll(abcDir, 0755); err != nil {
		t.Fatalf("failed to mkdir: %v", err)
	}

	mainContent := "use crate::utils::src_func;\n"
	if err := os.WriteFile(srcDir+"/main.rs", []byte(mainContent), 0644); err != nil {
		t.Fatalf("failed to write main.rs: %v", err)
	}

	utilsContent := "pub fn src_func() {}\n"
	if err := os.WriteFile(srcDir+"/utils.rs", []byte(utilsContent), 0644); err != nil {
		t.Fatalf("failed to write utils.rs: %v", err)
	}

	leafContent := "use super::super::parent_func;\n"
	if err := os.WriteFile(abcDir+"/leaf.rs", []byte(leafContent), 0644); err != nil {
		t.Fatalf("failed to write leaf.rs: %v", err)
	}

	aParentContent := "pub fn parent_func() {}\n"
	if err := os.WriteFile(aDir+"/mod.rs", []byte(aParentContent), 0644); err != nil {
		t.Fatalf("failed to write mod.rs: %v", err)
	}

	// 1. Test src/ root resolution
	gotFile, gotLine, err := p.ResolveImports(srcDir+"/main.rs", "src_func")
	if err != nil {
		t.Fatalf("ResolveImports src_func error: %v", err)
	}
	if !strings.HasSuffix(strings.ReplaceAll(gotFile, "\\", "/"), "src/utils.rs") {
		t.Errorf("gotFile = %q, want ending with src/utils.rs", gotFile)
	}
	if gotLine != 1 {
		t.Errorf("gotLine = %d, want 1", gotLine)
	}

	// 2. Test multiple super:: resolution
	gotFileSuper, gotLineSuper, err := p.ResolveImports(abcDir+"/leaf.rs", "parent_func")
	if err != nil {
		t.Fatalf("ResolveImports super::super parent_func error: %v", err)
	}
	if !strings.HasSuffix(strings.ReplaceAll(gotFileSuper, "\\", "/"), "src/a/mod.rs") {
		t.Errorf("gotFileSuper = %q, want ending with src/a/mod.rs", gotFileSuper)
	}
	if gotLineSuper != 1 {
		t.Errorf("gotLineSuper = %d, want 1", gotLineSuper)
	}
}

func TestRustParser_FindReferencesInFile_FieldIdentifierAndConstStatic(t *testing.T) {
	p := NewRustParser()
	tmpDir := t.TempDir()

	content := `
const MY_CONST: i32 = 10; // definition of MY_CONST
static MY_STATIC: i32 = 20; // definition of MY_STATIC

struct Foo {
    my_field: i32,
}

fn test_fn(foo: Foo) -> i32 {
    let a = MY_CONST;
    let b = MY_STATIC;
    let c = foo.my_field; // field_identifier
    c
}
`
	tmpFile := tmpDir + "/test_ref.rs"
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write tmpFile: %v", err)
	}

	// Test const exclusion & reference
	refsConst, err := p.FindReferencesInFile(tmpFile, "MY_CONST")
	if err != nil {
		t.Fatalf("FindReferencesInFile MY_CONST error: %v", err)
	}
	if len(refsConst) != 1 {
		t.Fatalf("refsConst count = %d, want 1. Got: %+v", len(refsConst), refsConst)
	}
	if refsConst[0].LineNumber != 10 {
		t.Errorf("refsConst LineNumber = %d, want 10", refsConst[0].LineNumber)
	}

	// Test static exclusion & reference
	refsStatic, err := p.FindReferencesInFile(tmpFile, "MY_STATIC")
	if err != nil {
		t.Fatalf("FindReferencesInFile MY_STATIC error: %v", err)
	}
	if len(refsStatic) != 1 {
		t.Fatalf("refsStatic count = %d, want 1. Got: %+v", len(refsStatic), refsStatic)
	}
	if refsStatic[0].LineNumber != 11 {
		t.Errorf("refsStatic LineNumber = %d, want 11", refsStatic[0].LineNumber)
	}

	// Test field_identifier reference
	refsField, err := p.FindReferencesInFile(tmpFile, "my_field")
	if err != nil {
		t.Fatalf("FindReferencesInFile my_field error: %v", err)
	}
	if len(refsField) != 1 {
		t.Fatalf("refsField count = %d, want 1. Got: %+v", len(refsField), refsField)
	}
	if refsField[0].LineNumber != 12 {
		t.Errorf("refsField LineNumber = %d, want 12", refsField[0].LineNumber)
	}
}
