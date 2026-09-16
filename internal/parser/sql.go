// Package parser: sql.go implements the Parser interface for SQL source
// files using the tree-sitter-sql grammar.
package parser

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	tree_sitter_sql "github.com/DerekStride/tree-sitter-sql/bindings/go"
	"github.com/snaga/ast-digger/internal/apperr"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

// SqlParser implements the Parser interface for SQL (.sql) source files.
type SqlParser struct {
	lang        *tree_sitter.Language
	warnedFiles sync.Map
}

// Compile-time assertion that SqlParser satisfies the Parser interface.
var _ Parser = (*SqlParser)(nil)

// NewSqlParser constructs a SqlParser with the tree-sitter-sql grammar loaded.
func NewSqlParser() *SqlParser {
	return &SqlParser{lang: tree_sitter.NewLanguage(tree_sitter_sql.Language())}
}

// readSqlFile reads filePath's contents and normalizes CRLF newlines to LF.
func readSqlFile(filePath string) ([]byte, error) {
	data, err := readFile(filePath)
	if err != nil {
		return nil, err
	}
	return bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")), nil
}

// parseSource parses source using a fresh tree-sitter parser configured with the SQL grammar.
func (p *SqlParser) parseSource(source []byte) (*tree_sitter.Tree, error) {
	ts := tree_sitter.NewParser()
	defer ts.Close()

	if err := ts.SetLanguage(p.lang); err != nil {
		return nil, fmt.Errorf("failed to set tree-sitter-sql language: %w", err)
	}

	tree := ts.Parse(source, nil)
	if tree == nil {
		return nil, fmt.Errorf("tree-sitter-sql failed to parse source")
	}
	return tree, nil
}

// sqlIgnoreKeywords lists non-table tokens or built-in table functions to exclude from table references.
var sqlIgnoreKeywords = map[string]bool{
	"LATERAL":        true,
	"FLATTEN":        true,
	"TABLE":          true,
	"UNNEST":         true,
	"GENERATOR":      true,
	"SPLIT_TO_TABLE": true,
}

// extractDependencies traverses node and extracts referenced table or upstream CTE names.
// If skipCtes is true, it skips common_table_expression / cte blocks so that top-level
// statements do not pick up tables only referenced inside CTEs.
func extractDependencies(root *tree_sitter.Node, source []byte, skipCtes bool) []string {
	var deps []string
	seen := make(map[string]bool)

	var walk func(n *tree_sitter.Node)
	walk = func(n *tree_sitter.Node) {
		if skipCtes && (n.Kind() == "cte" || n.Kind() == "common_table_expression") {
			return
		}

		// Handle MERGE ... USING <source_table>
		if n.Kind() == "keyword_using" {
			parent := n.Parent()
			if parent != nil {
				foundUsing := false
				for i := uint(0); i < parent.ChildCount(); i++ {
					c := parent.Child(i)
					if c.Id() == n.Id() {
						foundUsing = true
						continue
					}
					if foundUsing {
						if c.Kind() == "object_reference" || c.Kind() == "identifier" {
							tbl := strings.TrimSpace(c.Utf8Text(source))
							if tbl != "" && !sqlIgnoreKeywords[strings.ToUpper(tbl)] && !seen[tbl] {
								seen[tbl] = true
								deps = append(deps, tbl)
							}
							break
						}
					}
				}
			}
		}

		// Handle relation nodes inside FROM or JOIN
		if n.Kind() == "relation" {
			// Check if this relation wraps a derived table subquery: (SELECT ...) AS alias
			isSubquery := false
			for i := uint(0); i < n.ChildCount(); i++ {
				cKind := n.Child(i).Kind()
				if cKind == "subquery" || cKind == "statement" || cKind == "select" || cKind == "select_statement" || cKind == "(" {
					isSubquery = true
					break
				}
			}

			if !isSubquery {
				// Find object_reference or identifier
				var tbl string
				for i := uint(0); i < n.ChildCount(); i++ {
					c := n.Child(i)
					if c.Kind() == "object_reference" {
						tbl = strings.TrimSpace(c.Utf8Text(source))
						break
					}
				}
				if tbl == "" {
					for i := uint(0); i < n.ChildCount(); i++ {
						c := n.Child(i)
						if c.Kind() == "identifier" {
							tbl = strings.TrimSpace(c.Utf8Text(source))
							break
						}
					}
				}

				if tbl != "" && !sqlIgnoreKeywords[strings.ToUpper(tbl)] && !seen[tbl] {
					seen[tbl] = true
					deps = append(deps, tbl)
				}
			}
		}

		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}

	walk(root)
	return deps
}

// findCteName extracts the CTE name identifier from a cte AST node.
func findCteName(node *tree_sitter.Node, source []byte) string {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "keyword_as" {
			break
		}
		if child.Kind() == "identifier" || child.Kind() == "table_name" {
			return child.Utf8Text(source)
		}
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "identifier" {
			return child.Utf8Text(source)
		}
	}
	return ""
}

// findCteHeader extracts the CTE name and any column list before keyword_as, e.g. "raw_orders" or "cte(a, b)".
func findCteHeader(node *tree_sitter.Node, source []byte) string {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "keyword_as" {
			if child.StartByte() >= node.StartByte() && child.StartByte() <= node.EndByte() {
				headerText := string(source[node.StartByte():child.StartByte()])
				return strings.TrimSpace(headerText)
			}
			break
		}
	}
	return findCteName(node, source)
}

// extractCteQueryBody extracts the SQL query body enclosed in parentheses within a cte node.
// It skips any column specification list like cte(a, b) AS (...) by finding the parenthesis after keyword_as.
func extractCteQueryBody(node *tree_sitter.Node, source []byte) string {
	text := node.Utf8Text(source)

	// Locate keyword_as to skip any column specification like cte(a, b) AS (...)
	asOffset := 0
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "keyword_as" {
			if child.EndByte() >= node.StartByte() && child.EndByte() <= node.EndByte() {
				asOffset = int(child.EndByte() - node.StartByte())
			}
			break
		}
	}

	searchStr := text
	if asOffset > 0 && asOffset < len(text) {
		searchStr = text[asOffset:]
	} else {
		// Fallback for cases where keyword_as might not be an immediate child
		if idx := strings.Index(strings.ToUpper(text), " AS"); idx >= 0 {
			asOffset = idx + 3
			if asOffset < len(text) {
				searchStr = text[asOffset:]
			}
		}
	}

	firstOpen := strings.Index(searchStr, "(")
	lastClose := strings.LastIndex(text, ")")
	if firstOpen >= 0 {
		absOpen := asOffset + firstOpen
		if absOpen < len(text) && lastClose > absOpen && lastClose <= len(text) {
			return strings.TrimSpace(text[absOpen+1 : lastClose])
		}
	}
	return strings.TrimSpace(text)
}

// extractSubqueryCode extracts the SQL text of a subquery, stripping outer parentheses if present.
func extractSubqueryCode(node *tree_sitter.Node, source []byte) string {
	text := strings.TrimSpace(node.Utf8Text(source))
	if strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")") && len(text) >= 2 {
		return strings.TrimSpace(text[1 : len(text)-1])
	}
	return text
}

// findSubqueryAlias locates any alias identifier following the subquery within its parent expression.
func findSubqueryAlias(subqueryNode *tree_sitter.Node, source []byte) string {
	parent := subqueryNode.Parent()
	if parent == nil {
		return ""
	}
	foundSelf := false
	for i := uint(0); i < parent.ChildCount(); i++ {
		child := parent.Child(i)
		if child.Id() == subqueryNode.Id() {
			foundSelf = true
			continue
		}
		if foundSelf {
			if child.Kind() == "identifier" || child.Kind() == "object_reference" {
				return strings.TrimSpace(child.Utf8Text(source))
			}
		}
	}
	return ""
}

var reSubqueryHash = regexp.MustCompile(`(?i)subquery#(\d+)`)

// normalizeSymbolPath normalizes symbol paths like subquery#0 to subquery[0].
func normalizeSymbolPath(p string) string {
	return reSubqueryHash.ReplaceAllString(p, "subquery[$1]")
}

// extractSubqueries extracts all subquery nodes within rootNode, assigning 0-indexed sequential names.
func extractSubqueries(rootNode *tree_sitter.Node, parentName string, source []byte, relPath string, skipCtes bool) []SymbolData {
	var subNodes []*tree_sitter.Node
	var walk func(n *tree_sitter.Node)
	walk = func(n *tree_sitter.Node) {
		if skipCtes && (n.Kind() == "cte" || n.Kind() == "common_table_expression") {
			return
		}
		if n.Kind() == "subquery" {
			subNodes = append(subNodes, n)
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(rootNode)

	var symbols []SymbolData
	for idx, subNode := range subNodes {
		subName := fmt.Sprintf("subquery[%d]", idx)
		body := extractSubqueryCode(subNode, source)
		alias := findSubqueryAlias(subNode, source)
		startLine := int(subNode.StartPosition().Row) + 1
		endLine := int(subNode.EndPosition().Row) + 1

		var sig *string
		if alias != "" {
			sig = &alias
		}

		// Sequential index symbol (subquery[N])
		symbols = append(symbols, SymbolData{
			Name:      subName,
			Kind:      "subquery",
			Parent:    &parentName,
			StartLine: startLine,
			EndLine:   endLine,
			Signature: sig,
			Code:      &body,
			FilePath:  &relPath,
		})

		// Alias symbol if present
		if alias != "" {
			symbols = append(symbols, SymbolData{
				Name:      alias,
				Kind:      "subquery",
				Parent:    &parentName,
				StartLine: startLine,
				EndLine:   endLine,
				Signature: sig,
				Code:      &body,
				FilePath:  &relPath,
			})
		}
	}
	return symbols
}

var (
	reInsertTable = regexp.MustCompile(`(?i)\bINSERT\s+INTO\s+([a-zA-Z0-9_.]+)`)
	reCreateTable = regexp.MustCompile(`(?i)\bCREATE\s+(?:OR\s+REPLACE\s+)?(?:(?:TRANSIENT|DYNAMIC|TEMPORARY|TEMP|VOLATILE)\s+)*TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-zA-Z0-9_.]+)`)
	reCreateView  = regexp.MustCompile(`(?i)\bCREATE\s+(?:OR\s+REPLACE\s+)?(?:(?:SECURE|MATERIALIZED|TRANSIENT|RECURSIVE)\s+)*VIEW\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-zA-Z0-9_.]+)`)
	reMergeTable  = regexp.MustCompile(`(?i)\bMERGE\s+INTO\s+([a-zA-Z0-9_.]+)`)
)

// findObjectReferenceText recursively locates the first object_reference or identifier node text.
func findObjectReferenceText(node *tree_sitter.Node, source []byte) string {
	if node.Kind() == "object_reference" {
		return node.Utf8Text(source)
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		if res := findObjectReferenceText(node.Child(i), source); res != "" {
			return res
		}
	}
	return ""
}

// extractInsertTableName extracts the target table name from an insert node.
func extractInsertTableName(node *tree_sitter.Node, source []byte) string {
	ref := findObjectReferenceText(node, source)
	if ref != "" {
		return ref
	}
	text := node.Utf8Text(source)
	if m := reInsertTable.FindStringSubmatch(text); len(m) > 1 {
		return m[1]
	}
	return "insert"
}

// extractCreateTableTableName extracts the target table name from a create_table node.
func extractCreateTableTableName(node *tree_sitter.Node, source []byte) string {
	text := node.Utf8Text(source)
	if m := reCreateTable.FindStringSubmatch(text); len(m) > 1 {
		return m[1]
	}
	ref := findObjectReferenceText(node, source)
	if ref != "" {
		return ref
	}
	return "create_table"
}

// extractCreateViewName extracts the target view name from a create_view node or statement text.
func extractCreateViewName(node *tree_sitter.Node, source []byte) string {
	text := node.Utf8Text(source)
	if m := reCreateView.FindStringSubmatch(text); len(m) > 1 {
		return m[1]
	}
	ref := findObjectReferenceText(node, source)
	if ref != "" {
		return ref
	}
	return "create_view"
}

// extractMergeTableName extracts the target table name from a merge statement.
func extractMergeTableName(node *tree_sitter.Node, source []byte) string {
	ref := findObjectReferenceText(node, source)
	if ref != "" {
		return ref
	}
	text := node.Utf8Text(source)
	if m := reMergeTable.FindStringSubmatch(text); len(m) > 1 {
		return m[1]
	}
	return "merge"
}

// ParseFile parses filePath and returns the list of symbols found in it.
func (p *SqlParser) ParseFile(filePath string) ([]SymbolData, error) {
	source, err := readSqlFile(filePath)
	if err != nil {
		return nil, err
	}
	relPath := relFilePath(filePath)

	tree, err := p.parseSource(source)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	if tree.RootNode().HasError() {
		// Log warning only once per file across the SqlParser instance lifetime.
		// Snowflake dialect features (QUALIFY, MERGE, etc.) may trigger recovery.
		if _, loaded := p.warnedFiles.LoadOrStore(filePath, true); !loaded {
			fmt.Fprintf(os.Stderr, "Warning: Syntax errors or dialect extensions detected in %s\n", filePath)
		}
	}

	symbols := []SymbolData{}
	root := tree.RootNode()

	for i := uint(0); i < root.ChildCount(); i++ {
		stmtNode := root.Child(i)
		if !stmtNode.IsNamed() {
			continue
		}

		// 1. Traverse and extract CTEs
		var ctes []*tree_sitter.Node
		var findCtes func(n *tree_sitter.Node)
		findCtes = func(n *tree_sitter.Node) {
			if n.Kind() == "cte" || n.Kind() == "common_table_expression" {
				ctes = append(ctes, n)
				return
			}
			for j := uint(0); j < n.ChildCount(); j++ {
				findCtes(n.Child(j))
			}
		}
		findCtes(stmtNode)

		for _, cteNode := range ctes {
			cteName := findCteName(cteNode, source)
			if cteName != "" {
				body := extractCteQueryBody(cteNode, source)
				deps := extractDependencies(cteNode, source, false)
				depsSig := fmt.Sprintf("depends_on: [%s]", strings.Join(deps, ", "))
				symbols = append(symbols, SymbolData{
					Name:      cteName,
					Kind:      "cte",
					StartLine: int(cteNode.StartPosition().Row) + 1,
					EndLine:   int(cteNode.EndPosition().Row) + 1,
					Signature: &depsSig,
					Code:      &body,
					FilePath:  &relPath,
				})

				// Extract subqueries inside this CTE
				cteSubqueries := extractSubqueries(cteNode, cteName, source, relPath, false)
				symbols = append(symbols, cteSubqueries...)
			}
		}

		// 2. Detect main statement (VIEW, TABLE, INSERT, MERGE, SELECT)
		var insertNode *tree_sitter.Node
		var createTableNode *tree_sitter.Node
		var createViewNode *tree_sitter.Node
		var isMerge bool
		var mainSelectNode *tree_sitter.Node

		var scanStmt func(n *tree_sitter.Node)
		scanStmt = func(n *tree_sitter.Node) {
			if n.Kind() == "cte" || n.Kind() == "common_table_expression" {
				return
			}
			if n.Kind() == "create_view" || n.Kind() == "create_view_statement" {
				if createViewNode == nil {
					createViewNode = n
				}
				return
			}
			if n.Kind() == "insert" || n.Kind() == "insert_statement" {
				if insertNode == nil {
					insertNode = n
				}
				return
			}
			if n.Kind() == "create_table" || n.Kind() == "create_table_statement" {
				if createTableNode == nil {
					createTableNode = n
				}
				return
			}
			if n.Kind() == "keyword_merge" {
				isMerge = true
			}
			if n.Kind() == "select" || n.Kind() == "select_statement" {
				if mainSelectNode == nil {
					mainSelectNode = n
				}
			}
			for j := uint(0); j < n.ChildCount(); j++ {
				scanStmt(n.Child(j))
			}
		}
		scanStmt(stmtNode)

		var mainStmtSymbol *SymbolData
		stmtText := stmtNode.Utf8Text(source)

		if createViewNode != nil {
			name := extractCreateViewName(createViewNode, source)
			code := strings.TrimSpace(stmtText)
			mainStmtSymbol = &SymbolData{
				Name:      name,
				Kind:      "statement",
				StartLine: int(stmtNode.StartPosition().Row) + 1,
				EndLine:   int(stmtNode.EndPosition().Row) + 1,
				Code:      &code,
				FilePath:  &relPath,
			}
		} else if m := reCreateView.FindStringSubmatch(stmtText); len(m) > 1 {
			name := m[1]
			code := strings.TrimSpace(stmtText)
			mainStmtSymbol = &SymbolData{
				Name:      name,
				Kind:      "statement",
				StartLine: int(stmtNode.StartPosition().Row) + 1,
				EndLine:   int(stmtNode.EndPosition().Row) + 1,
				Code:      &code,
				FilePath:  &relPath,
			}
		} else if createTableNode != nil {
			name := extractCreateTableTableName(createTableNode, source)
			code := strings.TrimSpace(stmtText)
			mainStmtSymbol = &SymbolData{
				Name:      name,
				Kind:      "statement",
				StartLine: int(stmtNode.StartPosition().Row) + 1,
				EndLine:   int(stmtNode.EndPosition().Row) + 1,
				Code:      &code,
				FilePath:  &relPath,
			}
		} else if m := reCreateTable.FindStringSubmatch(stmtText); len(m) > 1 {
			name := m[1]
			code := strings.TrimSpace(stmtText)
			mainStmtSymbol = &SymbolData{
				Name:      name,
				Kind:      "statement",
				StartLine: int(stmtNode.StartPosition().Row) + 1,
				EndLine:   int(stmtNode.EndPosition().Row) + 1,
				Code:      &code,
				FilePath:  &relPath,
			}
		} else if insertNode != nil {
			name := extractInsertTableName(insertNode, source)
			code := strings.TrimSpace(insertNode.Utf8Text(source))
			mainStmtSymbol = &SymbolData{
				Name:      name,
				Kind:      "statement",
				StartLine: int(insertNode.StartPosition().Row) + 1,
				EndLine:   int(insertNode.EndPosition().Row) + 1,
				Code:      &code,
				FilePath:  &relPath,
			}
		} else if isMerge {
			name := extractMergeTableName(stmtNode, source)
			code := strings.TrimSpace(stmtText)
			mainStmtSymbol = &SymbolData{
				Name:      name,
				Kind:      "statement",
				StartLine: int(stmtNode.StartPosition().Row) + 1,
				EndLine:   int(stmtNode.EndPosition().Row) + 1,
				Code:      &code,
				FilePath:  &relPath,
			}
		} else if mainSelectNode != nil {
			startLine := int(mainSelectNode.StartPosition().Row) + 1
			endLine := int(stmtNode.EndPosition().Row) + 1
			var code string
			if len(ctes) > 0 {
				code = strings.TrimSpace(string(source[mainSelectNode.StartByte():stmtNode.EndByte()]))
				code = strings.TrimSuffix(code, ";")
				code = strings.TrimSpace(code)
			} else {
				code = strings.TrimSpace(stmtText)
			}
			mainStmtSymbol = &SymbolData{
				Name:      "main_query",
				Kind:      "statement",
				StartLine: startLine,
				EndLine:   endLine,
				Code:      &code,
				FilePath:  &relPath,
			}
		}

		if mainStmtSymbol != nil {
			mainDeps := extractDependencies(stmtNode, source, true)
			mainDepsSig := fmt.Sprintf("depends_on: [%s]", strings.Join(mainDeps, ", "))
			mainStmtSymbol.Signature = &mainDepsSig
			symbols = append(symbols, *mainStmtSymbol)
			// Extract subqueries inside main statement (excluding CTE blocks)
			mainSubqueries := extractSubqueries(stmtNode, mainStmtSymbol.Name, source, relPath, true)
			symbols = append(symbols, mainSubqueries...)
		}
	}

	return symbols, nil
}

// ExtractSymbol extracts the source code and metadata for the named symbol.
func (p *SqlParser) ExtractSymbol(filePath, symbolPath string) (SymbolData, error) {
	normPath := normalizeSymbolPath(symbolPath)

	symbols, err := p.ParseFile(filePath)
	if err != nil {
		return SymbolData{}, err
	}
	relPath := relFilePath(filePath)

	// 1. Hierarchical path: "parent/child" (e.g. "cte_orders/subquery[0]", "cte_orders/sub_items")
	if slashIdx := strings.Index(normPath, "/"); slashIdx >= 0 {
		parentPart := normPath[:slashIdx]
		childPart := normPath[slashIdx+1:]

		for _, sym := range symbols {
			if sym.Parent != nil && *sym.Parent == parentPart && sym.Name == childPart {
				return sym, nil
			}
		}

		return SymbolData{}, &apperr.SymbolNotFoundError{
			FilePath:    filePath,
			SymbolPath:  symbolPath,
			Suggestions: suggestSymbols(p.ParseFile, filePath, symbolPath),
		}
	}

	// 2. Flat lookup:
	// If it's a CTE name, extract the full CTE definition block "name AS (...)"
	source, err := readSqlFile(filePath)
	if err != nil {
		return SymbolData{}, err
	}
	tree, err := p.parseSource(source)
	if err != nil {
		return SymbolData{}, err
	}
	defer tree.Close()

	var foundCteNode *tree_sitter.Node
	var searchCte func(n *tree_sitter.Node)
	searchCte = func(n *tree_sitter.Node) {
		if foundCteNode != nil {
			return
		}
		if n.Kind() == "cte" || n.Kind() == "common_table_expression" {
			name := findCteName(n, source)
			if name == normPath {
				foundCteNode = n
				return
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			searchCte(n.Child(i))
		}
	}
	searchCte(tree.RootNode())

	if foundCteNode != nil {
		cteBlock := strings.TrimSpace(foundCteNode.Utf8Text(source))
		deps := extractDependencies(foundCteNode, source, false)
		depsSig := fmt.Sprintf("depends_on: [%s]", strings.Join(deps, ", "))
		return SymbolData{
			Name:      normPath,
			Kind:      "cte",
			StartLine: int(foundCteNode.StartPosition().Row) + 1,
			EndLine:   int(foundCteNode.EndPosition().Row) + 1,
			Signature: &depsSig,
			Code:      &cteBlock,
			FilePath:  &relPath,
		}, nil
	}

	// 3. Flat subquery index: "subquery[0]" across the entire file
	flatSubIdx := -1
	var flatSubMatched bool
	if strings.HasPrefix(normPath, "subquery[") && strings.HasSuffix(normPath, "]") {
		idxStr := normPath[len("subquery[") : len(normPath)-1]
		if n, scanErr := fmt.Sscanf(idxStr, "%d", &flatSubIdx); scanErr == nil && n == 1 {
			flatSubMatched = true
		}
	}

	if flatSubMatched {
		var subquerySyms []SymbolData
		for _, sym := range symbols {
			if sym.Kind == "subquery" && strings.HasPrefix(sym.Name, "subquery[") {
				subquerySyms = append(subquerySyms, sym)
			}
		}
		if flatSubIdx >= 0 && flatSubIdx < len(subquerySyms) {
			return subquerySyms[flatSubIdx], nil
		}
	}

	// 4. Direct match by Name (statement name or alias or exact match)
	for _, sym := range symbols {
		if sym.Name == normPath {
			return sym, nil
		}
	}
	for _, sym := range symbols {
		if strings.EqualFold(sym.Name, normPath) {
			return sym, nil
		}
	}

	return SymbolData{}, &apperr.SymbolNotFoundError{
		FilePath:    filePath,
		SymbolPath:  symbolPath,
		Suggestions: suggestSymbols(p.ParseFile, filePath, symbolPath),
	}
}

// ResolveImports resolves the definition location of symbolName as defined as a CTE in filePath.
// If symbolName matches a CTE name defined in filePath, it returns filePath and the 1-indexed start line.
// Otherwise, it returns apperr.SymbolNotFoundError.
func (p *SqlParser) ResolveImports(filePath, symbolName string) (string, int, error) {
	source, err := readSqlFile(filePath)
	if err != nil {
		return "", 0, err
	}

	tree, err := p.parseSource(source)
	if err != nil {
		return "", 0, err
	}
	defer tree.Close()

	var cteNode *tree_sitter.Node
	var walk func(n *tree_sitter.Node)
	walk = func(n *tree_sitter.Node) {
		if cteNode != nil {
			return
		}
		if n.Kind() == "cte" || n.Kind() == "common_table_expression" {
			name := findCteName(n, source)
			if strings.EqualFold(name, symbolName) {
				cteNode = n
				return
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(tree.RootNode())

	if cteNode != nil {
		startLine := int(cteNode.StartPosition().Row) + 1
		return filePath, startLine, nil
	}

	return "", 0, &apperr.SymbolNotFoundError{
		FilePath:    filePath,
		SymbolPath:  symbolName,
		Suggestions: suggestSymbols(p.ParseFile, filePath, symbolName),
	}
}

// matchSqlTable checks if tbl matches target symbol, ignoring case and supporting
// partial suffix matches for schema-qualified tables (e.g. "orders" matching "source_db.lake.orders").
func matchSqlTable(tbl, target string) bool {
	if strings.EqualFold(tbl, target) {
		return true
	}
	lowerTbl := strings.ToLower(tbl)
	lowerTarget := strings.ToLower(target)
	return strings.HasSuffix(lowerTbl, "."+lowerTarget)
}

// FindReferencesInFile searches filePath for references to symbolName (a table name, qualified table name, or CTE name).
// It searches table references in FROM, JOIN, MERGE USING, and INSERT/MERGE INTO clauses,
// excluding comments, string literals, and CTE definition identifier nodes.
func (p *SqlParser) FindReferencesInFile(filePath, symbolName string) ([]ReferenceData, error) {
	source, err := readSqlFile(filePath)
	if err != nil {
		return nil, err
	}
	if len(source) == 0 {
		return []ReferenceData{}, nil
	}

	tree, err := p.parseSource(source)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	type byteRange struct {
		start, end uint
	}
	var excluded []byteRange
	var collectExcluded func(n *tree_sitter.Node)
	collectExcluded = func(n *tree_sitter.Node) {
		switch n.Kind() {
		case "comment", "marginalia", "string_literal", "raw_string_literal":
			excluded = append(excluded, byteRange{start: n.StartByte(), end: n.EndByte()})
			return
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			collectExcluded(n.Child(i))
		}
	}
	collectExcluded(tree.RootNode())

	isExcluded := func(start, end uint) bool {
		for _, r := range excluded {
			if start >= r.start && end <= r.end {
				return true
			}
		}
		return false
	}

	var cteDefRanges []byteRange
	var collectCteDefs func(n *tree_sitter.Node)
	collectCteDefs = func(n *tree_sitter.Node) {
		if n.Kind() == "cte" || n.Kind() == "common_table_expression" {
			for i := uint(0); i < n.ChildCount(); i++ {
				c := n.Child(i)
				if c.Kind() == "keyword_as" {
					break
				}
				if c.Kind() == "identifier" || c.Kind() == "table_name" {
					cteDefRanges = append(cteDefRanges, byteRange{start: c.StartByte(), end: c.EndByte()})
					break
				}
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			collectCteDefs(n.Child(i))
		}
	}
	collectCteDefs(tree.RootNode())

	isCteDef := func(start, end uint) bool {
		for _, r := range cteDefRanges {
			if start >= r.start && end <= r.end {
				return true
			}
		}
		return false
	}

	lines := splitLines(string(source))
	relPath := relFilePath(filePath)
	var references []ReferenceData
	seenPos := make(map[uint]bool)

	addRef := func(node *tree_sitter.Node) {
		pos := node.StartByte()
		if seenPos[pos] {
			return
		}
		seenPos[pos] = true

		lineIdx := int(node.StartPosition().Row)
		snippet := ""
		if lineIdx < len(lines) {
			snippet = strings.TrimSpace(lines[lineIdx])
		}
		references = append(references, ReferenceData{
			FilePath:    relPath,
			LineNumber:  lineIdx + 1,
			LineContent: snippet,
		})
	}

	var walk func(n *tree_sitter.Node)
	walk = func(n *tree_sitter.Node) {
		// 1. relation nodes in FROM or JOIN
		if n.Kind() == "relation" {
			isSubquery := false
			for i := uint(0); i < n.ChildCount(); i++ {
				cKind := n.Child(i).Kind()
				if cKind == "subquery" || cKind == "statement" || cKind == "select" || cKind == "select_statement" || cKind == "(" {
					isSubquery = true
					break
				}
			}
			if !isSubquery {
				var tableNode *tree_sitter.Node
				for i := uint(0); i < n.ChildCount(); i++ {
					c := n.Child(i)
					if c.Kind() == "object_reference" {
						tableNode = c
						break
					}
				}
				if tableNode == nil {
					for i := uint(0); i < n.ChildCount(); i++ {
						c := n.Child(i)
						if c.Kind() == "identifier" {
							tableNode = c
							break
						}
					}
				}
				if tableNode != nil {
					tbl := strings.TrimSpace(tableNode.Utf8Text(source))
					if tbl != "" && !sqlIgnoreKeywords[strings.ToUpper(tbl)] {
						if !isExcluded(tableNode.StartByte(), tableNode.EndByte()) &&
							!isCteDef(tableNode.StartByte(), tableNode.EndByte()) &&
							matchSqlTable(tbl, symbolName) {
							addRef(tableNode)
						}
					}
				}
			}
		}

		// 2. MERGE INTO ... USING <source_table>
		if n.Kind() == "keyword_using" {
			parent := n.Parent()
			if parent != nil {
				foundUsing := false
				for i := uint(0); i < parent.ChildCount(); i++ {
					c := parent.Child(i)
					if c.Id() == n.Id() {
						foundUsing = true
						continue
					}
					if foundUsing {
						if c.Kind() == "object_reference" || c.Kind() == "identifier" {
							tbl := strings.TrimSpace(c.Utf8Text(source))
							if tbl != "" && !sqlIgnoreKeywords[strings.ToUpper(tbl)] {
								if !isExcluded(c.StartByte(), c.EndByte()) &&
									!isCteDef(c.StartByte(), c.EndByte()) &&
									matchSqlTable(tbl, symbolName) {
									addRef(c)
								}
							}
							break
						}
					}
				}
			}
		}

		// 3. INSERT INTO / MERGE INTO <target_table>
		if n.Kind() == "keyword_into" {
			parent := n.Parent()
			if parent != nil {
				foundInto := false
				for i := uint(0); i < parent.ChildCount(); i++ {
					c := parent.Child(i)
					if c.Id() == n.Id() {
						foundInto = true
						continue
					}
					if foundInto {
						if c.Kind() == "object_reference" || c.Kind() == "identifier" {
							tbl := strings.TrimSpace(c.Utf8Text(source))
							if tbl != "" && !sqlIgnoreKeywords[strings.ToUpper(tbl)] {
								if !isExcluded(c.StartByte(), c.EndByte()) &&
									!isCteDef(c.StartByte(), c.EndByte()) &&
									matchSqlTable(tbl, symbolName) {
									addRef(c)
								}
							}
							break
						}
					}
				}
			}
		}

		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(tree.RootNode())

	if references == nil {
		references = []ReferenceData{}
	}
	return references, nil
}

// GenerateSkeleton returns a collapsed skeleton view of the source file.
func (p *SqlParser) GenerateSkeleton(filePath string) (string, error) {
	source, err := readSqlFile(filePath)
	if err != nil {
		return "", err
	}

	tree, err := p.parseSource(source)
	if err != nil {
		return "", err
	}
	defer tree.Close()

	root := tree.RootNode()

	// 1. Find all CTEs
	var ctes []*tree_sitter.Node
	var findCtes func(n *tree_sitter.Node)
	findCtes = func(n *tree_sitter.Node) {
		if n.Kind() == "cte" || n.Kind() == "common_table_expression" {
			ctes = append(ctes, n)
			return
		}
		for j := uint(0); j < n.ChildCount(); j++ {
			findCtes(n.Child(j))
		}
	}
	findCtes(root)

	// 2. Find main statement symbol from ParseFile
	symbols, err := p.ParseFile(filePath)
	if err != nil {
		return "", err
	}

	var mainStmt *SymbolData
	for _, sym := range symbols {
		if sym.Kind == "statement" {
			mainStmt = &sym
			break
		}
	}

	var sb strings.Builder

	if len(ctes) > 0 {
		sb.WriteString("-- CTE Outline (Snowflake Dialect)\nWITH\n")
		for i, cteNode := range ctes {
			startLine := int(cteNode.StartPosition().Row) + 1
			endLine := int(cteNode.EndPosition().Row) + 1

			header := findCteHeader(cteNode, source)
			deps := extractDependencies(cteNode, source, false)
			depsStr := strings.Join(deps, ", ")

			comma := ","
			if i == len(ctes)-1 {
				comma = ""
			}

			fmt.Fprintf(&sb, "  %s AS (L%d-L%d, depends_on: [%s]) /* ... */%s\n",
				header, startLine, endLine, depsStr, comma)
		}

		if mainStmt != nil && mainStmt.Code != nil && *mainStmt.Code != "" {
			sb.WriteString("\n")
			sb.WriteString(*mainStmt.Code)
			if !strings.HasSuffix(*mainStmt.Code, ";") {
				sb.WriteString(";")
			}
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString("-- SQL Outline\n")
		if mainStmt != nil && mainStmt.Code != nil && *mainStmt.Code != "" {
			sb.WriteString(*mainStmt.Code)
			if !strings.HasSuffix(*mainStmt.Code, ";") {
				sb.WriteString(";")
			}
			sb.WriteString("\n")
		} else {
			sb.WriteString(strings.TrimSpace(string(source)))
			sb.WriteString("\n")
		}
	}

	res := strings.ReplaceAll(sb.String(), "\r\n", "\n")
	return res, nil
}
