// Package impact provides syntax-aware source code change impact analysis.
package impact

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	tree_sitter_sql "github.com/DerekStride/tree-sitter-sql/bindings/go"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

var (
	languages     map[string]*tree_sitter.Language
	languagesOnce sync.Once
)

func initLanguages() {
	languagesOnce.Do(func() {
		languages = map[string]*tree_sitter.Language{
			"go":   tree_sitter.NewLanguage(tree_sitter_go.Language()),
			"py":   tree_sitter.NewLanguage(tree_sitter_python.Language()),
			"ts":   tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript()),
			"tsx":  tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTSX()),
			"js":   tree_sitter.NewLanguage(tree_sitter_javascript.Language()),
			"jsx":  tree_sitter.NewLanguage(tree_sitter_javascript.Language()),
			"mjs":  tree_sitter.NewLanguage(tree_sitter_javascript.Language()),
			"cjs":  tree_sitter.NewLanguage(tree_sitter_javascript.Language()),
			"rs":   tree_sitter.NewLanguage(tree_sitter_rust.Language()),
			"java": tree_sitter.NewLanguage(tree_sitter_java.Language()),
			"sql":  tree_sitter.NewLanguage(tree_sitter_sql.Language()),
		}
	})
}

// GetLanguage returns the Tree-sitter Language for the given extension or file path.
func GetLanguage(extOrPath string) (*tree_sitter.Language, error) {
	initLanguages()
	ext := strings.ToLower(extOrPath)
	if strings.Contains(ext, ".") {
		ext = filepath.Ext(ext)
	}
	ext = strings.TrimPrefix(ext, ".")

	lang, ok := languages[ext]
	if !ok {
		return nil, fmt.Errorf("unsupported file extension: %s", extOrPath)
	}
	return lang, nil
}

// AnalyzeSource parses oldSrc and newSrc using the Tree-sitter language matching ext,
// and calculates the StatementDelta, ComplexityDelta, and overall ImpactLevel.
func AnalyzeSource(ext string, oldSrc, newSrc []byte) (*FileImpact, error) {
	return AnalyzeFileSource("", ext, oldSrc, newSrc)
}

// AnalyzeFileSource parses oldSrc and newSrc for filePath and returns a FileImpact.
func AnalyzeFileSource(filePath, ext string, oldSrc, newSrc []byte) (*FileImpact, error) {
	if ext == "" && filePath != "" {
		ext = filepath.Ext(filePath)
	}
	lang, err := GetLanguage(ext)
	if err != nil {
		return nil, err
	}

	oldStats, err := countASTStats(lang, oldSrc)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze old source: %w", err)
	}

	newStats, err := countASTStats(lang, newSrc)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze new source: %w", err)
	}

	delta := StatementDelta{}
	if newStats.Statements >= oldStats.Statements {
		delta.Added = newStats.Statements - oldStats.Statements
	} else {
		delta.Deleted = oldStats.Statements - newStats.Statements
	}

	complexityDelta := newStats.Complexity - oldStats.Complexity
	totalChanges := delta.Added + delta.Deleted
	level := CalculateImpactLevel(totalChanges, complexityDelta)

	return &FileImpact{
		FilePath:        filePath,
		Statements:      delta,
		ComplexityDelta: complexityDelta,
		ImpactLevel:     level,
	}, nil
}

type astStats struct {
	Statements int
	Complexity int
}

func countASTStats(lang *tree_sitter.Language, source []byte) (astStats, error) {
	if len(source) == 0 {
		return astStats{}, nil
	}

	ts := tree_sitter.NewParser()
	defer ts.Close()

	if err := ts.SetLanguage(lang); err != nil {
		return astStats{}, fmt.Errorf("failed to set language: %w", err)
	}

	tree := ts.Parse(source, nil)
	if tree == nil {
		return astStats{}, fmt.Errorf("failed to parse source")
	}
	defer tree.Close()

	root := tree.RootNode()
	if root == nil {
		return astStats{}, nil
	}

	var stats astStats
	traverseNode(root, source, false, &stats)
	return stats, nil
}

func traverseNode(node *tree_sitter.Node, source []byte, isInsideStatement bool, stats *astStats) {
	if node == nil || !node.IsNamed() {
		return
	}

	kind := node.Kind()
	if isCommentNode(kind) {
		return
	}

	// Control flow complexity branching check
	if isBranchNode(kind) {
		stats.Complexity++
	}

	nodeText := node.Utf8Text(source)
	if isOnlyPunctuationOrWhitespace(nodeText) {
		return
	}

	// 1. Containers: do not count themselves, traverse children with isInsideStatement = false
	if isContainerNode(kind) {
		count := node.NamedChildCount()
		for i := uint(0); i < count; i++ {
			traverseNode(node.NamedChild(i), source, false, stats)
		}
		return
	}

	// 2. Anonymous functions / Lambdas: reset isInsideStatement to false for the inner body
	if isFunctionLiteral(kind) {
		count := node.NamedChildCount()
		for i := uint(0); i < count; i++ {
			traverseNode(node.NamedChild(i), source, false, stats)
		}
		return
	}

	// 3. Compound statements / declarations: count this node, traverse inner children with isInsideStatement = false
	if isCompoundStatementOrDecl(kind) {
		stats.Statements++
		count := node.NamedChildCount()
		for i := uint(0); i < count; i++ {
			traverseNode(node.NamedChild(i), source, false, stats)
		}
		return
	}

	// 4. Simple statements / declarations:
	if isSimpleStatement(kind) {
		if !isInsideStatement {
			stats.Statements++
		}
		count := node.NamedChildCount()
		for i := uint(0); i < count; i++ {
			traverseNode(node.NamedChild(i), source, true, stats)
		}
		return
	}

	// 5. Other nodes (expressions, etc.):
	if !isInsideStatement {
		// If an expression or item appears directly in a block/container without statement wrapper
		stats.Statements++
		count := node.NamedChildCount()
		for i := uint(0); i < count; i++ {
			traverseNode(node.NamedChild(i), source, true, stats)
		}
		return
	}

	// Inside statement: continue traversing children in case there are nested branch nodes or lambdas
	count := node.NamedChildCount()
	for i := uint(0); i < count; i++ {
		traverseNode(node.NamedChild(i), source, true, stats)
	}
}

func isBranchNode(kind string) bool {
	switch kind {
	case "if_statement", "if_expression", "elif_clause",
		"for_statement", "for_expression", "for_in_statement", "for_of_statement", "enhanced_for_statement",
		"while_statement", "while_expression", "do_statement", "loop_expression",
		"switch_statement", "switch_expression", "type_switch_statement", "select_statement",
		"case_clause", "switch_case", "match_arm",
		"catch_clause", "except_clause",
		"conditional_expression", "ternary_expression":
		return true
	}
	return false
}

func isContainerNode(kind string) bool {
	switch kind {
	case "program", "source_file", "source", "module", "translation_unit",
		"block", "statement_block", "compound_statement", "suite",
		"declaration_list", "field_declaration_list", "class_body",
		"statement_list", "interface_body", "enum_body",
		"parenthesized_expression":
		return true
	}
	return false
}

func isCompoundStatementOrDecl(kind string) bool {
	switch kind {
	case "function_declaration", "function_definition", "method_declaration", "method_definition", "function_item",
		"class_declaration", "class_definition", "struct_item", "impl_item", "interface_declaration", "trait_item",
		"if_statement", "if_expression", "elif_clause",
		"for_statement", "for_expression", "for_in_statement", "for_of_statement", "enhanced_for_statement",
		"while_statement", "while_expression", "do_statement", "loop_expression",
		"switch_statement", "switch_expression", "type_switch_statement", "select_statement", "match_statement", "match_expression",
		"try_statement", "try_with_resources_statement", "catch_clause", "except_clause", "finally_clause",
		"with_statement":
		return true
	}
	return false
}

func isSimpleStatement(kind string) bool {
	switch kind {
	case "expression_statement", "return_statement", "return_expression",
		"assignment_statement", "short_var_declaration", "inc_dec_statement",
		"variable_statement", "lexical_declaration", "let_declaration",
		"var_declaration", "const_declaration", "type_declaration", "type_alias_declaration",
		"import_statement", "import_from_statement", "import_declaration", "use_declaration",
		"package_clause", "package_declaration",
		"export_statement",
		"throw_statement", "raise_statement", "assert_statement",
		"break_statement", "continue_statement", "pass_statement", "yield_statement", "branch_statement",
		"defer_statement", "go_statement", "send_statement",
		"macro_invocation",
		"local_variable_declaration", "field_declaration":
		return true
	}
	if strings.HasSuffix(kind, "_statement") && kind != "empty_statement" {
		return true
	}
	if strings.HasSuffix(kind, "_declaration") {
		return true
	}
	return false
}

func isFunctionLiteral(kind string) bool {
	switch kind {
	case "func_literal", "arrow_function", "function_expression", "lambda", "closure_expression":
		return true
	}
	return false
}

func isCommentNode(kind string) bool {
	return strings.Contains(kind, "comment")
}

func isOnlyPunctuationOrWhitespace(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return true
	}
	for _, r := range s {
		if r != '{' && r != '}' && r != '(' && r != ')' && r != '[' && r != ']' && r != ';' && r != ',' && !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}
