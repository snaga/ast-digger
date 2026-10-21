package parser

import (
	"errors"
	"fmt"
	"testing"

	"github.com/snaga/ast-digger/internal/apperr"
)

func TestForPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantType string
		wantErr  bool
	}{
		{name: "python", path: "main.py", wantType: "*parser.PythonParser"},
		{name: "python upper", path: "MAIN.PY", wantType: "*parser.PythonParser"},
		{name: "typescript ts", path: "index.ts", wantType: "*parser.TypeScriptParser"},
		{name: "typescript tsx", path: "App.tsx", wantType: "*parser.TypeScriptParser"},
		{name: "golang", path: "main.go", wantType: "*parser.GolangParser"},
		{name: "java", path: "App.java", wantType: "*parser.JavaParser"},
		{name: "javascript js", path: "index.js", wantType: "*parser.JavaScriptParser"},
		{name: "javascript jsx", path: "Component.jsx", wantType: "*parser.JavaScriptParser"},
		{name: "rust", path: "main.rs", wantType: "*parser.RustParser"},
		{name: "sql", path: "query.sql", wantType: "*parser.SqlParser"},
		{name: "markdown md", path: "README.md", wantType: "*parser.MarkdownParser"},
		{name: "markdown markdown", path: "DOC.markdown", wantType: "*parser.MarkdownParser"},
		{name: "filename markdown", path: "markdown", wantType: "*parser.MarkdownParser"},
		{name: "filename .md", path: ".md", wantType: "*parser.MarkdownParser"},
		{name: "filename .markdown", path: ".markdown", wantType: "*parser.MarkdownParser"},
		{name: "unsupported txt", path: "notes.txt", wantErr: true},
		{name: "unsupported empty", path: "", wantErr: true},
		{name: "unsupported no ext", path: "Dockerfile", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ForPath(tt.path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ForPath(%q) expected error, got nil", tt.path)
				}
				var faErr *apperr.FileAccessError
				if !errors.As(err, &faErr) {
					t.Errorf("expected *apperr.FileAccessError, got %T (%v)", err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ForPath(%q) unexpected error: %v", tt.path, err)
			}
			if p == nil {
				t.Fatalf("ForPath(%q) returned nil parser", tt.path)
			}
			gotType := fmt.Sprintf("%T", p)
			if gotType != tt.wantType {
				t.Errorf("ForPath(%q) parser type = %q, want %q", tt.path, gotType, tt.wantType)
			}
		})
	}
}

func TestParser_ParseFile_NonExistent(t *testing.T) {
	parsers := map[string]Parser{
		"golang":     NewGolangParser(),
		"java":       NewJavaParser(),
		"javascript": NewJavaScriptParser(),
		"markdown":   NewMarkdownParser(),
		"python":     NewPythonParser(),
		"rust":       NewRustParser(),
		"sql":        NewSqlParser(),
		"typescript": NewTypeScriptParser(),
	}

	for lang, p := range parsers {
		t.Run(lang, func(t *testing.T) {
			_, err := p.ParseFile("non_existent_file_xyz_12345.ext")
			if err == nil {
				t.Errorf("%s parser: expected error for non-existent file, got nil", lang)
			}
			_, err = p.GenerateSkeleton("non_existent_file_xyz_12345.ext")
			if err == nil {
				t.Errorf("%s parser GenerateSkeleton: expected error for non-existent file, got nil", lang)
			}
			_, err = p.ExtractSymbol("non_existent_file_xyz_12345.ext", "Foo")
			if err == nil {
				t.Errorf("%s parser ExtractSymbol: expected error for non-existent file, got nil", lang)
			}
			_, _, err = p.ResolveImports("non_existent_file_xyz_12345.ext", "Foo")
			if err == nil {
				t.Errorf("%s parser ResolveImports: expected error for non-existent file, got nil", lang)
			}
		})
	}
}

