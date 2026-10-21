package parser

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/snaga/ast-digger/internal/apperr"
)

// ForPath returns the appropriate Parser implementation for the given file path
// based on its extension.
func ForPath(filePath string) (Parser, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".py":
		return NewPythonParser(), nil
	case ".ts", ".tsx":
		return NewTypeScriptParser(), nil
	case ".go":
		return NewGolangParser(), nil
	case ".java":
		return NewJavaParser(), nil
	case ".js", ".jsx":
		return NewJavaScriptParser(), nil
	case ".rs":
		return NewRustParser(), nil
	case ".sql":
		return NewSqlParser(), nil
	case ".md", ".markdown":
		return NewMarkdownParser(), nil
	}
	if strings.ToLower(filePath) == "markdown" || strings.ToLower(filePath) == ".md" || strings.ToLower(filePath) == ".markdown" {
		return NewMarkdownParser(), nil
	}
	return nil, &apperr.FileAccessError{
		Path: filePath,
		Err:  fmt.Errorf("unsupported file type: %s", ext),
	}
}
