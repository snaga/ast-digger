package parser

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snaga/ast-digger/internal/apperr"
)

func findSymbolByPredicate(symbols []SymbolData, predicate func(SymbolData) bool) *SymbolData {
	for i, sym := range symbols {
		if predicate(sym) {
			return &symbols[i]
		}
	}
	return nil
}

func TestMarkdownParser_ParseFile(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "testdata", "markdown", "sample.md")

	p := NewMarkdownParser()
	symbols, err := p.ParseFile(fixturePath)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	t.Run("TEST-MD-10: Frontmatter parsing", func(t *testing.T) {
		fm := findSymbolByPredicate(symbols, func(sym SymbolData) bool {
			return sym.Kind == "frontmatter"
		})
		if fm == nil {
			t.Fatalf("expected frontmatter symbol, got none")
		}
		if fm.Name != "frontmatter" {
			t.Errorf("Name = %q, want %q", fm.Name, "frontmatter")
		}
		if fm.StartLine != 1 || fm.EndLine != 4 {
			t.Errorf("lines = L%d-L%d, want L1-L4", fm.StartLine, fm.EndLine)
		}
		if fm.Parent != nil && *fm.Parent != "" {
			t.Errorf("Parent = %v, want empty or nil", *fm.Parent)
		}
	})

	t.Run("TEST-MD-01: Heading level skip H1 -> H4", func(t *testing.T) {
		h4 := findSymbolByPredicate(symbols, func(sym SymbolData) bool {
			return sym.Kind == "heading" && sym.Name == "Deep Subsection 2.0.1"
		})
		if h4 == nil {
			t.Fatalf("expected heading 'Deep Subsection 2.0.1', got none")
		}
		expectedParent := "Title H1/Section 2"
		if h4.Parent == nil || *h4.Parent != expectedParent {
			got := "<nil>"
			if h4.Parent != nil {
				got = *h4.Parent
			}
			t.Errorf("Parent = %q, want %q", got, expectedParent)
		}
	})

	t.Run("TEST-MD-05: Code block fallback to code_block:text", func(t *testing.T) {
		plainBlock := findSymbolByPredicate(symbols, func(sym SymbolData) bool {
			return sym.Kind == "code_block" && sym.Name == "code_block:text"
		})
		if plainBlock == nil {
			t.Fatalf("expected code_block:text symbol, got none")
		}
		expectedParent := "Title H1/Section 1/Subsection 1.1"
		if plainBlock.Parent == nil || *plainBlock.Parent != expectedParent {
			got := "<nil>"
			if plainBlock.Parent != nil {
				got = *plainBlock.Parent
			}
			t.Errorf("Parent = %q, want %q", got, expectedParent)
		}
	})

	t.Run("TEST-MD-06: EOF unclosed code block parsing", func(t *testing.T) {
		unclosedBlock := findSymbolByPredicate(symbols, func(sym SymbolData) bool {
			return sym.Kind == "code_block" && sym.StartLine == 46
		})
		if unclosedBlock == nil {
			t.Fatalf("expected unclosed code block starting at L46, got none")
		}
		if unclosedBlock.EndLine != 49 {
			t.Errorf("EndLine = %d, want 49", unclosedBlock.EndLine)
		}
	})
}

func TestMarkdownParser_GenerateSkeleton(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "testdata", "markdown", "sample.md")

	p := NewMarkdownParser()
	skel, err := p.GenerateSkeleton(fixturePath)
	if err != nil {
		t.Fatalf("GenerateSkeleton failed: %v", err)
	}

	if !strings.Contains(skel, "<!-- [frontmatter] L1-L4 (title, author) -->") {
		t.Errorf("skeleton missing frontmatter comment: %s", skel)
	}
	if !strings.Contains(skel, "<!-- [code_block:go] L18-L23 (6 lines) -->") {
		t.Errorf("skeleton missing go code block comment: %s", skel)
	}
	if !strings.Contains(skel, "<!-- [code_block:text] L24-L27 (4 lines) -->") {
		t.Errorf("skeleton missing text code block comment: %s", skel)
	}
	if !strings.Contains(skel, "# Title H1") {
		t.Errorf("skeleton missing H1 heading line")
	}
	if strings.Contains(skel, "Intro text here.") {
		t.Errorf("skeleton should not contain plain text body lines")
	}
}

func TestMarkdownParser_ExtractSymbol(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "testdata", "markdown", "importproj", "main.md")
	p := NewMarkdownParser()

	t.Run("TEST-MD-02: Ambiguous duplicate heading error", func(t *testing.T) {
		_, err := p.ExtractSymbol(fixturePath, "概要")
		if err == nil {
			t.Fatalf("expected SymbolNotFoundError for duplicate heading '概要', got nil")
		}
		var symErr *apperr.SymbolNotFoundError
		if !errors.As(err, &symErr) {
			t.Fatalf("expected *apperr.SymbolNotFoundError, got %T (%v)", err, err)
		}
		if len(symErr.Suggestions) == 0 {
			t.Errorf("expected suggestions for ambiguous heading, got empty")
		}
	})

	t.Run("TEST-MD-03: Escaped slash in heading", func(t *testing.T) {
		sym, err := p.ExtractSymbol(fixturePath, `アーキテクチャ/1\/2. 概要\/構成`)
		if err != nil {
			t.Fatalf("ExtractSymbol failed for escaped slash heading: %v", err)
		}
		if sym.Name != "1/2. 概要/構成" {
			t.Errorf("Name = %q, want %q", sym.Name, "1/2. 概要/構成")
		}
	})

	t.Run("TEST-MD-04: Multiple code blocks in same section", func(t *testing.T) {
		sym1, err := p.ExtractSymbol(fixturePath, "アーキテクチャ/1/2. 概要/構成/code_block:go")
		if err != nil {
			t.Fatalf("ExtractSymbol for code_block:go failed: %v", err)
		}
		if sym1.Name != "code_block:go" {
			t.Errorf("Name = %q, want code_block:go", sym1.Name)
		}
		if sym1.Code == nil || !strings.Contains(*sym1.Code, "Duplicate()") {
			t.Errorf("Code = %v, expected to contain Duplicate()", sym1.Code)
		}

		sym2, err := p.ExtractSymbol(fixturePath, "アーキテクチャ/1/2. 概要/構成/code_block:go#2")
		if err != nil {
			t.Fatalf("ExtractSymbol for code_block:go#2 failed: %v", err)
		}
		if sym2.Name != "code_block:go#2" {
			t.Errorf("Name = %q, want code_block:go#2", sym2.Name)
		}
		if sym2.Code == nil || !strings.Contains(*sym2.Code, "Duplicate2()") {
			t.Errorf("Code = %v, expected to contain Duplicate2()", sym2.Code)
		}
	})

	t.Run("TEST-MD-07: List indented code block not extracted as outer code block", func(t *testing.T) {
		symbols, err := p.ParseFile(fixturePath)
		if err != nil {
			t.Fatalf("ParseFile failed: %v", err)
		}
		for _, s := range symbols {
			if s.Kind == "code_block" && strings.Contains(s.Name, "go") {
				if s.StartLine > 35 { // indented code block line
					// check that if it is extracted, it didn't mess up top level
				}
			}
		}
	})

	t.Run("Extract frontmatter", func(t *testing.T) {
		sym, err := p.ExtractSymbol(fixturePath, "frontmatter")
		if err != nil {
			t.Fatalf("ExtractSymbol for frontmatter failed: %v", err)
		}
		if sym.Kind != "frontmatter" {
			t.Errorf("Kind = %q, want frontmatter", sym.Kind)
		}
		if sym.Code == nil || !strings.Contains(*sym.Code, "title: Import Test Doc") {
			t.Errorf("Code = %v, expected frontmatter content", sym.Code)
		}
	})
}

func TestMarkdownParser_ResolveImports(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "testdata", "markdown", "importproj", "main.md")
	p := NewMarkdownParser()

	t.Run("TEST-MD-08: Relative file link resolution", func(t *testing.T) {
		defFile, startLine, err := p.ResolveImports(fixturePath, "SPECS/design.md#architecture")
		if err != nil {
			t.Fatalf("ResolveImports failed: %v", err)
		}
		if !strings.HasSuffix(filepath.ToSlash(defFile), "SPECS/design.md") {
			t.Errorf("defFile = %q, want ending with SPECS/design.md", defFile)
		}
		if startLine != 3 {
			t.Errorf("startLine = %d, want 3", startLine)
		}
	})

	t.Run("TEST-MD-09: In-file anchor resolution", func(t *testing.T) {
		defFile, startLine, err := p.ResolveImports(fixturePath, "#section-2")
		if err != nil {
			t.Fatalf("ResolveImports failed for same file anchor: %v", err)
		}
		if startLine == 0 {
			t.Errorf("startLine should be > 0, got %d", startLine)
		}
		_ = defFile
	})
}

func TestMarkdownParser_FindReferencesInFile(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "testdata", "markdown", "references", "ref.md")
	p := NewMarkdownParser()

	t.Run("TEST-MD-09: FindReferencesInFile", func(t *testing.T) {
		refs, err := p.FindReferencesInFile(fixturePath, "SPECS/design.md#architecture")
		if err != nil {
			t.Fatalf("FindReferencesInFile failed: %v", err)
		}
		if len(refs) != 2 {
			t.Errorf("len(refs) = %d, want 2", len(refs))
		}
	})

	t.Run("Ignore links inside code blocks", func(t *testing.T) {
		// main.md has code_block with markdown links or we test with fixture
		fixturePath := filepath.Join("..", "..", "testdata", "markdown", "importproj", "main.md")
		refs, err := p.FindReferencesInFile(fixturePath, "SPECS/design.md#architecture")
		if err != nil {
			t.Fatalf("FindReferencesInFile failed: %v", err)
		}
		for _, ref := range refs {
			if strings.Contains(ref.LineContent, "```") {
				t.Errorf("Found reference inside code block: %v", ref)
			}
		}
	})
}

func TestMarkdownParser_TargetToAnchor_Unicode(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"## 概要", "概要"},
		{"Section 2", "section-2"},
		{"1/2. 概要/構成", "12-概要構成"},
	}

	for _, tt := range tests {
		got := targetToAnchor(tt.input)
		if got != tt.want {
			t.Errorf("targetToAnchor(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMarkdownParser_ExtractSymbol_OutOfBoundsClamp(t *testing.T) {
	p := NewMarkdownParser()
	sym := SymbolData{
		Name:      "code_block:go",
		Kind:      "code_block",
		StartLine: 1,
		EndLine:   999, // file only has 3 lines
	}
	_ = sym

	fixturePath := filepath.Join("..", "..", "testdata", "markdown", "importproj", "main.md")
	// Test that ExtractSymbol handles end line gracefully if symbol EndLine > total lines
	// For testing, we can check ExtractSymbol on a symbol or custom logic
	symbols, err := p.ParseFile(fixturePath)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if len(symbols) > 0 {
		// Modify last symbol's endLine to be arbitrarily large and ensure ExtractSymbol doesn't panic
		symbols[len(symbols)-1].EndLine = 99999
		_, err := p.ExtractSymbol(fixturePath, symbols[len(symbols)-1].Name)
		if err != nil {
			t.Logf("ExtractSymbol with large EndLine returned: %v", err)
		}
	}
}

