// Package parser provides language-specific AST parsing and symbol extraction.
package parser

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	tree_sitter_sql "github.com/DerekStride/tree-sitter-sql/bindings/go"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"

	"github.com/snaga/ast-digger/internal/apperr"
)

var (
	languages     map[string]*tree_sitter.Language
	languagesOnce sync.Once
)

func initLanguages() {
	languagesOnce.Do(func() {
		goLang := tree_sitter.NewLanguage(tree_sitter_go.Language())
		pyLang := tree_sitter.NewLanguage(tree_sitter_python.Language())
		tsLang := tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())
		tsxLang := tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTSX())
		jsLang := tree_sitter.NewLanguage(tree_sitter_javascript.Language())
		rsLang := tree_sitter.NewLanguage(tree_sitter_rust.Language())
		javaLang := tree_sitter.NewLanguage(tree_sitter_java.Language())
		sqlLang := tree_sitter.NewLanguage(tree_sitter_sql.Language())

		languages = map[string]*tree_sitter.Language{
			"go":         goLang,
			"golang":     goLang,
			".go":        goLang,
			"py":         pyLang,
			"python":     pyLang,
			".py":        pyLang,
			"ts":         tsLang,
			"typescript": tsLang,
			".ts":        tsLang,
			"tsx":        tsxLang,
			".tsx":       tsxLang,
			"js":         jsLang,
			"javascript": jsLang,
			".js":        jsLang,
			"jsx":        jsLang,
			".jsx":       jsLang,
			"mjs":        jsLang,
			".mjs":       jsLang,
			"cjs":        jsLang,
			".cjs":       jsLang,
			"rs":         rsLang,
			"rust":       rsLang,
			".rs":        rsLang,
			"java":       javaLang,
			".java":      javaLang,
			"sql":        sqlLang,
			".sql":       sqlLang,
		}
	})
}

// GetLanguage returns the Tree-sitter Language instance for the given extension,
// language name, or file path. If the language is unsupported, it returns an error.
func GetLanguage(extOrLang string) (*tree_sitter.Language, error) {
	initLanguages()
	key := strings.ToLower(strings.TrimSpace(extOrLang))
	if strings.Contains(key, ".") {
		ext := filepath.Ext(key)
		if ext != "" {
			key = ext
		}
	}
	key = strings.TrimPrefix(key, ".")

	lang, ok := languages[key]
	if !ok {
		return nil, &apperr.FileAccessError{
			Path: extOrLang,
			Err:  fmt.Errorf("unsupported file extension: %s", extOrLang),
		}
	}
	return lang, nil
}
