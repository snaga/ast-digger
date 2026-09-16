package parser

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snaga/ast-digger/internal/apperr"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

func TestSqlParser_DependsOn(t *testing.T) {
	p := NewSqlParser()
	fixturePath := filepath.Join("..", "..", "testdata", "sql", "snowflake_sample.sql")

	symbols, err := p.ParseFile(fixturePath)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	expectedDeps := map[string]string{
		"raw_orders":                "depends_on: [source_db.lake.orders, source_db.lake.blacklisted_users]",
		"cleaned_orders":            "depends_on: [raw_orders]",
		"user_master":               "depends_on: [source_db.lake.users]",
		"joined_sales":              "depends_on: [source_db.lake.tier_bonuses, cleaned_orders, user_master]",
		"tax_calculation":           "depends_on: [joined_sales]",
		"marts.daily_sales_summary": "depends_on: [tax_calculation]",
	}

	for _, sym := range symbols {
		if expectedSig, ok := expectedDeps[sym.Name]; ok {
			if sym.Signature == nil {
				t.Errorf("symbol %q expected Signature %q, got nil", sym.Name, expectedSig)
			} else if *sym.Signature != expectedSig {
				t.Errorf("symbol %q expected Signature %q, got %q", sym.Name, expectedSig, *sym.Signature)
			}
		}
	}

	// Verify ExtractSymbol also carries depends_on signature
	sym, err := p.ExtractSymbol(fixturePath, "raw_orders")
	if err != nil {
		t.Fatalf("ExtractSymbol raw_orders failed: %v", err)
	}
	expectedRawSig := "depends_on: [source_db.lake.orders, source_db.lake.blacklisted_users]"
	if sym.Signature == nil || *sym.Signature != expectedRawSig {
		t.Errorf("ExtractSymbol raw_orders Signature mismatch: got %v, expected %s", sym.Signature, expectedRawSig)
	}

	// Verify dialect cases: LATERAL FLATTEN and MERGE INTO
	t.Run("LATERAL FLATTEN ignores lateral table functions", func(t *testing.T) {
		dir := t.TempDir()
		sqlFile := filepath.Join(dir, "flatten_test.sql")
		content := `WITH parsed_events AS (
    SELECT event_id, parse_json(payload) AS json_data FROM raw_events
)
SELECT p.event_id, f.value:user_id::string AS user_id
FROM parsed_events p,
LATERAL FLATTEN(input => p.json_data:users) f;
`
		if err := os.WriteFile(sqlFile, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		syms, err := p.ParseFile(sqlFile)
		if err != nil {
			t.Fatalf("ParseFile failed: %v", err)
		}
		for _, s := range syms {
			if s.Name == "main_query" {
				if s.Signature == nil || *s.Signature != "depends_on: [parsed_events]" {
					t.Errorf("main_query expected 'depends_on: [parsed_events]', got %v", s.Signature)
				}
			}
		}
	})

	t.Run("MERGE INTO USING extracts source table", func(t *testing.T) {
		dir := t.TempDir()
		sqlFile := filepath.Join(dir, "merge_test.sql")
		content := `MERGE INTO target_schema.dim_customer AS target
USING staging_schema.stg_customer AS source
ON target.customer_id = source.customer_id
WHEN MATCHED THEN
    UPDATE SET target.customer_name = source.customer_name;
`
		if err := os.WriteFile(sqlFile, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		syms, err := p.ParseFile(sqlFile)
		if err != nil {
			t.Fatalf("ParseFile failed: %v", err)
		}
		if len(syms) < 1 {
			t.Fatal("expected at least 1 symbol")
		}
		if syms[0].Signature == nil || *syms[0].Signature != "depends_on: [staging_schema.stg_customer]" {
			t.Errorf("merge expected 'depends_on: [staging_schema.stg_customer]', got %v", syms[0].Signature)
		}
	})
}

func TestSqlParser_GenerateSkeleton(t *testing.T) {
	p := NewSqlParser()
	fixturePath := filepath.Join("..", "..", "testdata", "sql", "snowflake_sample.sql")

	skeleton, err := p.GenerateSkeleton(fixturePath)
	if err != nil {
		t.Fatalf("GenerateSkeleton failed: %v", err)
	}

	// CRLF normalization check
	if strings.Contains(skeleton, "\r") {
		t.Error("GenerateSkeleton output contains \\r carriage returns")
	}

	// Verify header
	if !strings.Contains(skeleton, "-- CTE Outline (Snowflake Dialect)\nWITH\n") {
		t.Errorf("expected skeleton header '-- CTE Outline (Snowflake Dialect)\\nWITH\\n', got:\n%s", skeleton)
	}

	// Verify all CTEs with line range, depends_on, and /* ... */
	expectedCteLines := []string{
		"  raw_orders AS (L6-L27, depends_on: [source_db.lake.orders, source_db.lake.blacklisted_users]) /* ... */,",
		"  cleaned_orders AS (L29-L48, depends_on: [raw_orders]) /* ... */,",
		"  user_master AS (L50-L62, depends_on: [source_db.lake.users]) /* ... */,",
		"  joined_sales AS (L64-L86, depends_on: [source_db.lake.tier_bonuses, cleaned_orders, user_master]) /* ... */,",
		"  tax_calculation AS (L88-L102, depends_on: [joined_sales]) /* ... */",
	}

	for _, expectedLine := range expectedCteLines {
		if !strings.Contains(skeleton, expectedLine) {
			t.Errorf("skeleton does not contain expected CTE line:\n  %s\nGot skeleton:\n%s", expectedLine, skeleton)
		}
	}

	// Verify main statement outline is preserved
	if !strings.Contains(skeleton, "INSERT INTO marts.daily_sales_summary") {
		t.Errorf("skeleton does not contain INSERT INTO statement")
	}
	if !strings.Contains(skeleton, "FROM tax_calculation") {
		t.Errorf("skeleton does not contain FROM tax_calculation")
	}

	// Verify CTE with column list
	t.Run("CTE with explicit column list", func(t *testing.T) {
		dir := t.TempDir()
		sqlFile := filepath.Join(dir, "col_list.sql")
		content := `WITH explicit_cols(id, val) AS (
    SELECT 1 AS x, 'hello' AS y
)
SELECT id, val FROM explicit_cols;
`
		if err := os.WriteFile(sqlFile, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		skel, err := p.GenerateSkeleton(sqlFile)
		if err != nil {
			t.Fatalf("GenerateSkeleton failed: %v", err)
		}
		expectedLine := "  explicit_cols(id, val) AS (L1-L3, depends_on: []) /* ... */"
		if !strings.Contains(skel, expectedLine) {
			t.Errorf("expected %q in skeleton, got:\n%s", expectedLine, skel)
		}
		if !strings.Contains(skel, "SELECT id, val FROM explicit_cols;") {
			t.Errorf("expected main select in skeleton, got:\n%s", skel)
		}
	})

	// Verify pure statement without CTE
	t.Run("Pure statement without CTE", func(t *testing.T) {
		dir := t.TempDir()
		sqlFile := filepath.Join(dir, "no_cte.sql")
		content := "SELECT id, user_name FROM users WHERE is_active = TRUE;\n"
		if err := os.WriteFile(sqlFile, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		skel, err := p.GenerateSkeleton(sqlFile)
		if err != nil {
			t.Fatalf("GenerateSkeleton failed: %v", err)
		}
		if !strings.Contains(skel, "-- SQL Outline\n") {
			t.Errorf("expected '-- SQL Outline', got:\n%s", skel)
		}
		if !strings.Contains(skel, "SELECT id, user_name FROM users WHERE is_active = TRUE;") {
			t.Errorf("expected original query, got:\n%s", skel)
		}
	})
}

func TestSqlParser_Initialization(t *testing.T) {
	p := NewSqlParser()
	if p == nil {
		t.Fatal("expected non-nil SqlParser")
	}
	if p.lang == nil {
		t.Fatal("expected non-nil Language in SqlParser")
	}
}

func TestSqlParser_ParseFile(t *testing.T) {
	p := NewSqlParser()
	fixturePath := filepath.Join("..", "..", "testdata", "sql", "snowflake_sample.sql")

	symbols, err := p.ParseFile(fixturePath)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	rawOrdersParent := "raw_orders"
	joinedSalesParent := "joined_sales"
	martsParent := "marts.daily_sales_summary"

	expectedSymbols := []struct {
		name      string
		kind      string
		parent    *string
		startLine int
		endLine   int
		codeCheck string
	}{
		{
			name:      "raw_orders",
			kind:      "cte",
			parent:    nil,
			startLine: 6,
			endLine:   27,
			codeCheck: "FROM source_db.lake.orders",
		},
		{
			name:      "subquery[0]",
			kind:      "subquery",
			parent:    &rawOrdersParent,
			startLine: 22,
			endLine:   26,
			codeCheck: "FROM source_db.lake.blacklisted_users",
		},
		{
			name:      "cleaned_orders",
			kind:      "cte",
			parent:    nil,
			startLine: 29,
			endLine:   48,
			codeCheck: "QUALIFY ROW_NUMBER() OVER",
		},
		{
			name:      "user_master",
			kind:      "cte",
			parent:    nil,
			startLine: 50,
			endLine:   62,
			codeCheck: "FROM source_db.lake.users",
		},
		{
			name:      "joined_sales",
			kind:      "cte",
			parent:    nil,
			startLine: 64,
			endLine:   86,
			codeCheck: "INNER JOIN user_master u",
		},
		{
			name:      "subquery[0]",
			kind:      "subquery",
			parent:    &joinedSalesParent,
			startLine: 79,
			endLine:   79,
			codeCheck: "SELECT MAX(bonus_multiplier)",
		},
		{
			name:      "bonus_multiplier",
			kind:      "subquery",
			parent:    &joinedSalesParent,
			startLine: 79,
			endLine:   79,
			codeCheck: "SELECT MAX(bonus_multiplier)",
		},
		{
			name:      "subquery[1]",
			kind:      "subquery",
			parent:    &joinedSalesParent,
			startLine: 80,
			endLine:   84,
			codeCheck: "FROM cleaned_orders",
		},
		{
			name:      "sub_items",
			kind:      "subquery",
			parent:    &joinedSalesParent,
			startLine: 80,
			endLine:   84,
			codeCheck: "FROM cleaned_orders",
		},
		{
			name:      "tax_calculation",
			kind:      "cte",
			parent:    nil,
			startLine: 88,
			endLine:   102,
			codeCheck: "GROUP BY sales_date, country_code, membership_tier",
		},
		{
			name:      "marts.daily_sales_summary",
			kind:      "statement",
			parent:    nil,
			startLine: 105,
			endLine:   127,
			codeCheck: "INSERT INTO marts.daily_sales_summary",
		},
		{
			name:      "subquery[0]",
			kind:      "subquery",
			parent:    &martsParent,
			startLine: 127,
			endLine:   127,
			codeCheck: "SELECT MIN(sales_date) FROM tax_calculation",
		},
	}

	if len(symbols) != len(expectedSymbols) {
		t.Fatalf("expected %d symbols, got %d:\n%+v", len(expectedSymbols), len(symbols), symbols)
	}

	for i, expected := range expectedSymbols {
		actual := symbols[i]
		if actual.Name != expected.name {
			t.Errorf("[%d] expected Name %q, got %q", i, expected.name, actual.Name)
		}
		if actual.Kind != expected.kind {
			t.Errorf("[%d] expected Kind %q, got %q", i, expected.kind, actual.Kind)
		}
		if expected.parent == nil && actual.Parent != nil {
			t.Errorf("[%d] %s: expected nil Parent, got %q", i, expected.name, *actual.Parent)
		} else if expected.parent != nil {
			if actual.Parent == nil {
				t.Errorf("[%d] %s: expected Parent %q, got nil", i, expected.name, *expected.parent)
			} else if *actual.Parent != *expected.parent {
				t.Errorf("[%d] %s: expected Parent %q, got %q", i, expected.name, *expected.parent, *actual.Parent)
			}
		}
		if actual.StartLine != expected.startLine {
			t.Errorf("[%d] %s: expected StartLine %d, got %d", i, expected.name, expected.startLine, actual.StartLine)
		}
		if actual.EndLine != expected.endLine {
			t.Errorf("[%d] %s: expected EndLine %d, got %d", i, expected.name, expected.endLine, actual.EndLine)
		}
		if actual.Code == nil {
			t.Errorf("[%d] %s: expected non-nil Code", i, expected.name)
		} else if !strings.Contains(*actual.Code, expected.codeCheck) {
			t.Errorf("[%d] %s: Code does not contain %q. Got:\n%s", i, expected.name, expected.codeCheck, *actual.Code)
		}
	}
}

func TestSqlParser_ExtractSymbol(t *testing.T) {
	p := NewSqlParser()
	fixturePath := filepath.Join("..", "..", "testdata", "sql", "snowflake_sample.sql")

	t.Run("extract existing CTE cleaned_orders", func(t *testing.T) {
		sym, err := p.ExtractSymbol(fixturePath, "cleaned_orders")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "cleaned_orders" {
			t.Errorf("expected Name cleaned_orders, got %s", sym.Name)
		}
		if sym.Kind != "cte" {
			t.Errorf("expected Kind cte, got %s", sym.Kind)
		}
		if sym.StartLine != 29 {
			t.Errorf("expected StartLine 29, got %d", sym.StartLine)
		}
		if sym.EndLine != 48 {
			t.Errorf("expected EndLine 48, got %d", sym.EndLine)
		}
		if sym.Code == nil {
			t.Fatal("expected non-nil Code")
		}

		normCode := normalizeNewlines(*sym.Code)
		if !strings.HasPrefix(normCode, "cleaned_orders AS (") {
			t.Errorf("expected Code to start with 'cleaned_orders AS (', got:\n%s", normCode)
		}
		if !strings.HasSuffix(normCode, ")") {
			t.Errorf("expected Code to end with ')', got:\n%s", normCode)
		}
		if !strings.Contains(normCode, "QUALIFY ROW_NUMBER() OVER") {
			t.Errorf("expected Code to contain QUALIFY clause, got:\n%s", normCode)
		}
	})

	t.Run("extract existing statement marts.daily_sales_summary", func(t *testing.T) {
		sym, err := p.ExtractSymbol(fixturePath, "marts.daily_sales_summary")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "marts.daily_sales_summary" {
			t.Errorf("expected Name marts.daily_sales_summary, got %s", sym.Name)
		}
		if sym.Kind != "statement" {
			t.Errorf("expected Kind statement, got %s", sym.Kind)
		}
		if sym.StartLine != 105 {
			t.Errorf("expected StartLine 105, got %d", sym.StartLine)
		}
		if sym.EndLine != 127 {
			t.Errorf("expected EndLine 127, got %d", sym.EndLine)
		}
		if sym.Code == nil || !strings.Contains(*sym.Code, "INSERT INTO marts.daily_sales_summary") {
			t.Errorf("expected Code to contain INSERT statement, got: %v", sym.Code)
		}
	})

	t.Run("extract hierarchical subquery with bracket cte/subquery[0]", func(t *testing.T) {
		sym, err := p.ExtractSymbol(fixturePath, "raw_orders/subquery[0]")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "subquery[0]" {
			t.Errorf("expected Name subquery[0], got %s", sym.Name)
		}
		if sym.Kind != "subquery" {
			t.Errorf("expected Kind subquery, got %s", sym.Kind)
		}
		if sym.Parent == nil || *sym.Parent != "raw_orders" {
			t.Errorf("expected Parent raw_orders, got %v", sym.Parent)
		}
		if sym.StartLine != 22 || sym.EndLine != 26 {
			t.Errorf("expected lines 22-26, got %d-%d", sym.StartLine, sym.EndLine)
		}
		if sym.Code == nil || !strings.Contains(*sym.Code, "FROM source_db.lake.blacklisted_users") {
			t.Errorf("unexpected Code: %v", sym.Code)
		}
		// Inner query should not have enclosing parentheses
		if strings.HasPrefix(*sym.Code, "(") || strings.HasSuffix(*sym.Code, ")") {
			t.Errorf("expected Code without outer parentheses, got: %q", *sym.Code)
		}
	})

	t.Run("extract hierarchical subquery with hash cte/subquery#0", func(t *testing.T) {
		sym, err := p.ExtractSymbol(fixturePath, "raw_orders/subquery#0")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "subquery[0]" {
			t.Errorf("expected Name subquery[0], got %s", sym.Name)
		}
		if sym.StartLine != 22 || sym.EndLine != 26 {
			t.Errorf("expected lines 22-26, got %d-%d", sym.StartLine, sym.EndLine)
		}
	})

	t.Run("extract hierarchical subquery by alias cte/sub_items", func(t *testing.T) {
		sym, err := p.ExtractSymbol(fixturePath, "joined_sales/sub_items")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "sub_items" {
			t.Errorf("expected Name sub_items, got %s", sym.Name)
		}
		if sym.Kind != "subquery" {
			t.Errorf("expected Kind subquery, got %s", sym.Kind)
		}
		if sym.Parent == nil || *sym.Parent != "joined_sales" {
			t.Errorf("expected Parent joined_sales, got %v", sym.Parent)
		}
		if sym.StartLine != 80 || sym.EndLine != 84 {
			t.Errorf("expected lines 80-84, got %d-%d", sym.StartLine, sym.EndLine)
		}
		if sym.Code == nil || !strings.Contains(*sym.Code, "FROM cleaned_orders") {
			t.Errorf("unexpected Code: %v", sym.Code)
		}
	})

	t.Run("extract hierarchical subquery by index joined_sales/subquery[1]", func(t *testing.T) {
		sym, err := p.ExtractSymbol(fixturePath, "joined_sales/subquery[1]")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "subquery[1]" {
			t.Errorf("expected Name subquery[1], got %s", sym.Name)
		}
		if sym.StartLine != 80 || sym.EndLine != 84 {
			t.Errorf("expected lines 80-84, got %d-%d", sym.StartLine, sym.EndLine)
		}
	})

	t.Run("extract hierarchical scalar subquery joined_sales/bonus_multiplier", func(t *testing.T) {
		sym, err := p.ExtractSymbol(fixturePath, "joined_sales/bonus_multiplier")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "bonus_multiplier" {
			t.Errorf("expected Name bonus_multiplier, got %s", sym.Name)
		}
		if sym.StartLine != 79 || sym.EndLine != 79 {
			t.Errorf("expected line 79, got %d-%d", sym.StartLine, sym.EndLine)
		}
		if sym.Code == nil || !strings.Contains(*sym.Code, "SELECT MAX(bonus_multiplier)") {
			t.Errorf("unexpected Code: %v", sym.Code)
		}
	})

	t.Run("extract flat subquery index subquery[0] across file", func(t *testing.T) {
		sym, err := p.ExtractSymbol(fixturePath, "subquery[0]")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// First subquery in file is raw_orders/subquery[0]
		if sym.Parent == nil || *sym.Parent != "raw_orders" {
			t.Errorf("expected Parent raw_orders, got %v", sym.Parent)
		}
		if sym.StartLine != 22 || sym.EndLine != 26 {
			t.Errorf("expected lines 22-26, got %d-%d", sym.StartLine, sym.EndLine)
		}
	})

	t.Run("extract flat subquery index with hash subquery#0", func(t *testing.T) {
		sym, err := p.ExtractSymbol(fixturePath, "subquery#0")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Parent == nil || *sym.Parent != "raw_orders" {
			t.Errorf("expected Parent raw_orders, got %v", sym.Parent)
		}
	})

	t.Run("extract flat subquery index subquery[2] across file", func(t *testing.T) {
		sym, err := p.ExtractSymbol(fixturePath, "subquery[2]")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Third subquery in file is joined_sales/subquery[1] (sub_items)
		if sym.Parent == nil || *sym.Parent != "joined_sales" {
			t.Errorf("expected Parent joined_sales, got %v", sym.Parent)
		}
		if sym.StartLine != 80 || sym.EndLine != 84 {
			t.Errorf("expected lines 80-84, got %d-%d", sym.StartLine, sym.EndLine)
		}
	})

	t.Run("extract flat subquery by alias sub_items", func(t *testing.T) {
		sym, err := p.ExtractSymbol(fixturePath, "sub_items")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "sub_items" {
			t.Errorf("expected Name sub_items, got %s", sym.Name)
		}
		if sym.StartLine != 80 || sym.EndLine != 84 {
			t.Errorf("expected lines 80-84, got %d-%d", sym.StartLine, sym.EndLine)
		}
	})

	t.Run("extract main statement subquery marts.daily_sales_summary/subquery[0]", func(t *testing.T) {
		sym, err := p.ExtractSymbol(fixturePath, "marts.daily_sales_summary/subquery[0]")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "subquery[0]" {
			t.Errorf("expected Name subquery[0], got %s", sym.Name)
		}
		if sym.Parent == nil || *sym.Parent != "marts.daily_sales_summary" {
			t.Errorf("expected Parent marts.daily_sales_summary, got %v", sym.Parent)
		}
		if sym.StartLine != 127 || sym.EndLine != 127 {
			t.Errorf("expected line 127, got %d-%d", sym.StartLine, sym.EndLine)
		}
		if sym.Code == nil || !strings.Contains(*sym.Code, "SELECT MIN(sales_date) FROM tax_calculation") {
			t.Errorf("unexpected Code: %v", sym.Code)
		}
	})

	t.Run("extract non-existent symbol returns SymbolNotFoundError with suggestions", func(t *testing.T) {
		_, err := p.ExtractSymbol(fixturePath, "cleand_orders") // typo
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var symErr *apperr.SymbolNotFoundError
		if !errors.As(err, &symErr) {
			t.Fatalf("expected *apperr.SymbolNotFoundError, got %T: %v", err, err)
		}
		if symErr.SymbolPath != "cleand_orders" {
			t.Errorf("expected SymbolPath cleand_orders, got %s", symErr.SymbolPath)
		}
		if len(symErr.Suggestions) == 0 {
			t.Error("expected suggestions for cleand_orders, got none")
		} else if symErr.Suggestions[0] != "cleaned_orders" {
			t.Errorf("expected first suggestion to be 'cleaned_orders', got %q", symErr.Suggestions[0])
		}
	})
}

func TestSqlParser_ParseFile_AdditionalDialects(t *testing.T) {
	p := NewSqlParser()

	t.Run("MERGE INTO statement", func(t *testing.T) {
		dir := t.TempDir()
		sqlFile := filepath.Join(dir, "merge_test.sql")
		content := `MERGE INTO target_schema.dim_customer AS target
USING staging_schema.stg_customer AS source
ON target.customer_id = source.customer_id
WHEN MATCHED AND target.updated_at < source.updated_at THEN
    UPDATE SET target.customer_name = source.customer_name, target.updated_at = source.updated_at
WHEN NOT MATCHED THEN
    INSERT (customer_id, customer_name, updated_at)
    VALUES (source.customer_id, source.customer_name, source.updated_at);
`
		if err := os.WriteFile(sqlFile, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		syms, err := p.ParseFile(sqlFile)
		if err != nil {
			t.Fatalf("ParseFile failed: %v", err)
		}
		if len(syms) != 1 {
			t.Fatalf("expected 1 symbol, got %d", len(syms))
		}
		if syms[0].Name != "target_schema.dim_customer" {
			t.Errorf("expected Name target_schema.dim_customer, got %s", syms[0].Name)
		}
		if syms[0].Kind != "statement" {
			t.Errorf("expected Kind statement, got %s", syms[0].Kind)
		}
	})

	t.Run("CREATE TABLE AS WITH CTE", func(t *testing.T) {
		dir := t.TempDir()
		sqlFile := filepath.Join(dir, "create_table_test.sql")
		content := `CREATE TABLE marts.monthly_report AS
WITH monthly_agg AS (
    SELECT month_str, SUM(amount) AS total_amt
    FROM source_sales
    GROUP BY month_str
)
SELECT month_str, total_amt
FROM monthly_agg;
`
		if err := os.WriteFile(sqlFile, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		syms, err := p.ParseFile(sqlFile)
		if err != nil {
			t.Fatalf("ParseFile failed: %v", err)
		}
		if len(syms) != 2 {
			t.Fatalf("expected 2 symbols (CTE + statement), got %d: %+v", len(syms), syms)
		}
		if syms[0].Name != "monthly_agg" || syms[0].Kind != "cte" {
			t.Errorf("expected monthly_agg cte, got %+v", syms[0])
		}
		if syms[1].Name != "marts.monthly_report" || syms[1].Kind != "statement" {
			t.Errorf("expected marts.monthly_report statement, got %+v", syms[1])
		}
	})

	t.Run("Pure SELECT statement with CTE and LATERAL FLATTEN", func(t *testing.T) {
		dir := t.TempDir()
		sqlFile := filepath.Join(dir, "flatten_test.sql")
		content := `WITH parsed_events AS (
    SELECT
        event_id,
        parse_json(payload) AS json_data
    FROM raw_events
)
SELECT
    p.event_id,
    f.value:user_id::string AS user_id
FROM parsed_events p,
LATERAL FLATTEN(input => p.json_data:users) f;
`
		if err := os.WriteFile(sqlFile, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		syms, err := p.ParseFile(sqlFile)
		if err != nil {
			t.Fatalf("ParseFile failed: %v", err)
		}
		if len(syms) != 2 {
			t.Fatalf("expected 2 symbols, got %d: %+v", len(syms), syms)
		}
		if syms[0].Name != "parsed_events" || syms[0].Kind != "cte" {
			t.Errorf("expected parsed_events cte, got %+v", syms[0])
		}
		if syms[1].Name != "main_query" || syms[1].Kind != "statement" {
			t.Errorf("expected main_query statement, got %+v", syms[1])
		}
	})
}

func TestSqlParser_CRLFNormalization(t *testing.T) {
	p := NewSqlParser()
	dir := t.TempDir()
	sqlFile := filepath.Join(dir, "crlf_sample.sql")
	crlfContent := "WITH cte_crlf AS (\r\n    SELECT 100 AS num\r\n)\r\nSELECT num FROM cte_crlf;\r\n"

	if err := os.WriteFile(sqlFile, []byte(crlfContent), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	syms, err := p.ParseFile(sqlFile)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if len(syms) != 2 {
		t.Fatalf("expected 2 symbols, got %d", len(syms))
	}
	if syms[0].Name != "cte_crlf" || syms[0].StartLine != 1 || syms[0].EndLine != 3 {
		t.Errorf("unexpected cte symbol: %+v", syms[0])
	}
	if syms[0].Code == nil || strings.Contains(*syms[0].Code, "\r") {
		t.Errorf("expected LF normalized Code without CR, got: %q", *syms[0].Code)
	}
}

func TestSqlParser_CTE_WithColumnList(t *testing.T) {
	p := NewSqlParser()
	dir := t.TempDir()
	sqlFile := filepath.Join(dir, "col_list_cte.sql")
	content := `WITH explicit_cols(id, val) AS (
    SELECT 1 AS x, 'hello' AS y
)
SELECT id, val FROM explicit_cols;
`
	if err := os.WriteFile(sqlFile, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	syms, err := p.ParseFile(sqlFile)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if len(syms) < 1 {
		t.Fatalf("expected at least 1 symbol, got %d", len(syms))
	}
	if syms[0].Name != "explicit_cols" || syms[0].Kind != "cte" {
		t.Errorf("expected explicit_cols cte, got %+v", syms[0])
	}
	if syms[0].Code == nil {
		t.Fatal("expected non-nil Code for explicit_cols")
	}
	expectedBody := "SELECT 1 AS x, 'hello' AS y"
	if *syms[0].Code != expectedBody {
		t.Errorf("expected Code %q, got %q", expectedBody, *syms[0].Code)
	}

	// Also verify ExtractSymbol extracts full block
	extracted, err := p.ExtractSymbol(sqlFile, "explicit_cols")
	if err != nil {
		t.Fatalf("ExtractSymbol failed: %v", err)
	}
	if extracted.Code == nil || !strings.Contains(*extracted.Code, "explicit_cols(id, val) AS") {
		t.Errorf("expected full block with column list, got: %v", extracted.Code)
	}
}

func TestSqlParser_ExtractSymbol_NotFoundHierarchical(t *testing.T) {
	p := NewSqlParser()
	fixturePath := filepath.Join("..", "..", "testdata", "sql", "snowflake_sample.sql")

	_, err := p.ExtractSymbol(fixturePath, "raw_orders/non_existent_subquery")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var symErr *apperr.SymbolNotFoundError
	if !errors.As(err, &symErr) {
		t.Fatalf("expected *apperr.SymbolNotFoundError, got %T: %v", err, err)
	}
	if symErr.SymbolPath != "raw_orders/non_existent_subquery" {
		t.Errorf("expected SymbolPath raw_orders/non_existent_subquery, got %s", symErr.SymbolPath)
	}
}

func TestSqlParser_ResolveImports(t *testing.T) {
	p := NewSqlParser()
	fixturePath := filepath.Join("..", "..", "testdata", "sql", "snowflake_sample.sql")

	tests := []struct {
		name       string
		symbolName string
		wantLine   int
		wantErr    bool
	}{
		{
			name:       "resolve raw_orders CTE",
			symbolName: "raw_orders",
			wantLine:   6,
			wantErr:    false,
		},
		{
			name:       "resolve cleaned_orders CTE",
			symbolName: "cleaned_orders",
			wantLine:   29,
			wantErr:    false,
		},
		{
			name:       "resolve user_master CTE",
			symbolName: "user_master",
			wantLine:   50,
			wantErr:    false,
		},
		{
			name:       "resolve joined_sales CTE",
			symbolName: "joined_sales",
			wantLine:   64,
			wantErr:    false,
		},
		{
			name:       "resolve tax_calculation CTE",
			symbolName: "tax_calculation",
			wantLine:   88,
			wantErr:    false,
		},
		{
			name:       "resolve with case-insensitive symbol",
			symbolName: "CLEANED_ORDERS",
			wantLine:   29,
			wantErr:    false,
		},
		{
			name:       "external physical table returns SymbolNotFoundError",
			symbolName: "source_db.lake.orders",
			wantErr:    true,
		},
		{
			name:       "non-existent CTE returns SymbolNotFoundError",
			symbolName: "completely_unknown_cte",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath, line, err := p.ResolveImports(fixturePath, tt.symbolName)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %s, got nil", tt.symbolName)
				}
				var symErr *apperr.SymbolNotFoundError
				if !errors.As(err, &symErr) {
					t.Fatalf("expected *apperr.SymbolNotFoundError, got %T: %v", err, err)
				}
				if symErr.SymbolPath != tt.symbolName {
					t.Errorf("expected SymbolPath %q, got %q", tt.symbolName, symErr.SymbolPath)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error for %s: %v", tt.symbolName, err)
			}
			if filePath != fixturePath {
				t.Errorf("filePath = %q, want %q", filePath, fixturePath)
			}
			if line != tt.wantLine {
				t.Errorf("line = %d, want %d", line, tt.wantLine)
			}
		})
	}

	t.Run("non-existent file returns error", func(t *testing.T) {
		_, _, err := p.ResolveImports("non_existent_file.sql", "raw_orders")
		if err == nil {
			t.Fatal("expected error for non-existent file, got nil")
		}
	})
}

func TestSqlParser_FindReferencesInFile(t *testing.T) {
	p := NewSqlParser()
	fixturePath := filepath.Join("..", "..", "testdata", "sql", "snowflake_sample.sql")

	t.Run("references to raw_orders excludes definition at line 6", func(t *testing.T) {
		refs, err := p.FindReferencesInFile(fixturePath, "raw_orders")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 1 {
			t.Fatalf("expected 1 reference to raw_orders, got %d: %+v", len(refs), refs)
		}
		if refs[0].LineNumber != 42 {
			t.Errorf("expected reference at line 42, got %d", refs[0].LineNumber)
		}
		if !strings.Contains(refs[0].LineContent, "FROM raw_orders") {
			t.Errorf("expected LineContent containing 'FROM raw_orders', got %q", refs[0].LineContent)
		}
	})

	t.Run("references to cleaned_orders from downstream joined_sales", func(t *testing.T) {
		refs, err := p.FindReferencesInFile(fixturePath, "cleaned_orders")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 1 {
			t.Fatalf("expected 1 reference to cleaned_orders, got %d: %+v", len(refs), refs)
		}
		if refs[0].LineNumber != 82 {
			t.Errorf("expected reference at line 82, got %d", refs[0].LineNumber)
		}
		if !strings.Contains(refs[0].LineContent, "FROM cleaned_orders") {
			t.Errorf("expected LineContent containing 'FROM cleaned_orders', got %q", refs[0].LineContent)
		}
	})

	t.Run("references to user_master via INNER JOIN", func(t *testing.T) {
		refs, err := p.FindReferencesInFile(fixturePath, "user_master")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 1 {
			t.Fatalf("expected 1 reference to user_master, got %d: %+v", len(refs), refs)
		}
		if refs[0].LineNumber != 85 {
			t.Errorf("expected reference at line 85, got %d", refs[0].LineNumber)
		}
		if !strings.Contains(refs[0].LineContent, "INNER JOIN user_master") {
			t.Errorf("expected LineContent containing 'INNER JOIN user_master', got %q", refs[0].LineContent)
		}
	})

	t.Run("references to tax_calculation duplicate scan rule (SNOW-005)", func(t *testing.T) {
		refs, err := p.FindReferencesInFile(fixturePath, "tax_calculation")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// tax_calculation is referenced twice: line 126 and line 127
		if len(refs) != 2 {
			t.Fatalf("expected 2 references to tax_calculation, got %d: %+v", len(refs), refs)
		}
		lines := []int{refs[0].LineNumber, refs[1].LineNumber}
		if lines[0] != 126 || lines[1] != 127 {
			t.Errorf("expected lines [126, 127], got %v", lines)
		}
	})

	t.Run("case-insensitive search RAW_ORDERS", func(t *testing.T) {
		refs, err := p.FindReferencesInFile(fixturePath, "RAW_ORDERS")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 1 {
			t.Fatalf("expected 1 reference for RAW_ORDERS, got %d", len(refs))
		}
		if refs[0].LineNumber != 42 {
			t.Errorf("expected line 42, got %d", refs[0].LineNumber)
		}
	})

	t.Run("qualified physical table reference source_db.lake.orders", func(t *testing.T) {
		refs, err := p.FindReferencesInFile(fixturePath, "source_db.lake.orders")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 1 {
			t.Fatalf("expected 1 reference for source_db.lake.orders, got %d", len(refs))
		}
		if refs[0].LineNumber != 20 {
			t.Errorf("expected line 20, got %d", refs[0].LineNumber)
		}
	})

	t.Run("unqualified match for qualified table orders", func(t *testing.T) {
		refs, err := p.FindReferencesInFile(fixturePath, "orders")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 1 {
			t.Fatalf("expected 1 reference for orders matching source_db.lake.orders, got %d", len(refs))
		}
		if refs[0].LineNumber != 20 {
			t.Errorf("expected line 20, got %d", refs[0].LineNumber)
		}
	})

	t.Run("ignores comments and string literals containing table name", func(t *testing.T) {
		dir := t.TempDir()
		sqlFile := filepath.Join(dir, "comments_test.sql")
		content := `-- Header comment mentioning raw_orders table
/* Block comment with raw_orders reference */
WITH raw_orders AS (
    SELECT 'raw_orders' AS literal_name, 1 AS id
    FROM source_table -- end-of-line comment raw_orders
)
SELECT * FROM raw_orders;
`
		if err := os.WriteFile(sqlFile, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		refs, err := p.FindReferencesInFile(sqlFile, "raw_orders")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Only "SELECT * FROM raw_orders;" at line 7 should match.
		// Line 1, 2 (comments), Line 3 (CTE definition), Line 4 (string literal 'raw_orders'), Line 5 (inline comment) must all be excluded.
		if len(refs) != 1 {
			t.Fatalf("expected exactly 1 reference, got %d: %+v", len(refs), refs)
		}
		if refs[0].LineNumber != 7 {
			t.Errorf("expected reference at line 7, got %d (%q)", refs[0].LineNumber, refs[0].LineContent)
		}
	})

	t.Run("MERGE INTO USING source table reference", func(t *testing.T) {
		dir := t.TempDir()
		sqlFile := filepath.Join(dir, "merge_ref.sql")
		content := `MERGE INTO dim_customer AS target
USING stg_customer AS source
ON target.id = source.id
WHEN MATCHED THEN UPDATE SET name = source.name;
`
		if err := os.WriteFile(sqlFile, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		refs, err := p.FindReferencesInFile(sqlFile, "stg_customer")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 1 {
			t.Fatalf("expected 1 reference to stg_customer, got %d", len(refs))
		}
		if refs[0].LineNumber != 2 {
			t.Errorf("expected line 2, got %d", refs[0].LineNumber)
		}
	})

	t.Run("non-existent table returns empty slice without error", func(t *testing.T) {
		refs, err := p.FindReferencesInFile(fixturePath, "completely_unreferenced_table")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 0 {
			t.Errorf("expected 0 references, got %d", len(refs))
		}
	})

	t.Run("non-existent file returns error", func(t *testing.T) {
		_, err := p.FindReferencesInFile("non_existent_file.sql", "raw_orders")
		if err == nil {
			t.Fatal("expected error for non-existent file, got nil")
		}
	})
}

func TestSqlParser_UpstreamDependencyTracking_FollowImports(t *testing.T) {
	p := NewSqlParser()
	fixturePath := filepath.Join("..", "..", "testdata", "sql", "snowflake_sample.sql")

	// Simulate --follow-imports DAG collection for tax_calculation:
	// tax_calculation -> joined_sales -> [cleaned_orders, user_master] -> raw_orders -> [orders (external)]
	visited := make(map[string]bool)
	var resolvedCtes []string

	var collectUpstream func(cteName string)
	collectUpstream = func(cteName string) {
		if visited[cteName] {
			return
		}
		visited[cteName] = true

		sym, err := p.ExtractSymbol(fixturePath, cteName)
		if err != nil {
			return
		}

		// Parse identifiers / upstream CTEs from sym.Code
		if sym.Code == nil {
			return
		}

		// Inspect dependencies from AST of this CTE
		source, readErr := readSqlFile(fixturePath)
		if readErr != nil {
			return
		}
		tree, parseErr := p.parseSource(source)
		if parseErr != nil {
			return
		}
		defer tree.Close()

		var cteNode *tree_sitter.Node
		var findNode func(n *tree_sitter.Node)
		findNode = func(n *tree_sitter.Node) {
			if cteNode != nil {
				return
			}
			if n.Kind() == "cte" || n.Kind() == "common_table_expression" {
				if strings.EqualFold(findCteName(n, source), cteName) {
					cteNode = n
					return
				}
			}
			for i := uint(0); i < n.ChildCount(); i++ {
				findNode(n.Child(i))
			}
		}
		findNode(tree.RootNode())

		if cteNode != nil {
			deps := extractDependencies(cteNode, source, false)
			for _, dep := range deps {
				// Resolve import (only resolve internal CTEs, external tables return error)
				_, _, err := p.ResolveImports(fixturePath, dep)
				if err == nil {
					collectUpstream(dep)
				}
			}
		}

		resolvedCtes = append(resolvedCtes, cteName)
	}

	collectUpstream("tax_calculation")

	// Post-order / topological dependency resolution:
	// tax_calculation should have gathered all upstream CTEs:
	// raw_orders, cleaned_orders, user_master, joined_sales, tax_calculation
	expectedContained := []string{"raw_orders", "cleaned_orders", "user_master", "joined_sales", "tax_calculation"}
	for _, expected := range expectedContained {
		found := false
		for _, r := range resolvedCtes {
			if r == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected CTE %q to be collected in upstream DAG, got %v", expected, resolvedCtes)
		}
	}
}

func TestSqlParser_CircularReferencePrevention(t *testing.T) {
	p := NewSqlParser()
	dir := t.TempDir()
	sqlFile := filepath.Join(dir, "circular.sql")
	content := `WITH RECURSIVE
cte_a AS (
    SELECT * FROM cte_b
),
cte_b AS (
    SELECT * FROM cte_a
)
SELECT * FROM cte_a;
`
	if err := os.WriteFile(sqlFile, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Verify both CTEs can be resolved individually without infinite loops
	pathA, lineA, errA := p.ResolveImports(sqlFile, "cte_a")
	if errA != nil {
		t.Fatalf("ResolveImports cte_a failed: %v", errA)
	}
	if pathA != sqlFile || lineA != 2 {
		t.Errorf("cte_a resolved to %s:%d, want line 2", pathA, lineA)
	}

	pathB, lineB, errB := p.ResolveImports(sqlFile, "cte_b")
	if errB != nil {
		t.Fatalf("ResolveImports cte_b failed: %v", errB)
	}
	if pathB != sqlFile || lineB != 5 {
		t.Errorf("cte_b resolved to %s:%d, want line 5", pathB, lineB)
	}

	// Simulate visited-set dependency tracking to ensure no deadlock / infinite recursion
	visited := make(map[string]bool)
	var chain []string
	var walkCycle func(curr string)
	walkCycle = func(curr string) {
		if visited[curr] {
			return
		}
		visited[curr] = true
		chain = append(chain, curr)

		sym, err := p.ExtractSymbol(sqlFile, curr)
		if err != nil {
			return
		}
		if sym.Code == nil {
			return
		}

		source, _ := readSqlFile(sqlFile)
		tree, _ := p.parseSource(source)
		defer tree.Close()

		var cteNode *tree_sitter.Node
		var findNode func(n *tree_sitter.Node)
		findNode = func(n *tree_sitter.Node) {
			if cteNode != nil {
				return
			}
			if n.Kind() == "cte" || n.Kind() == "common_table_expression" {
				if strings.EqualFold(findCteName(n, source), curr) {
					cteNode = n
					return
				}
			}
			for i := uint(0); i < n.ChildCount(); i++ {
				findNode(n.Child(i))
			}
		}
		findNode(tree.RootNode())

		if cteNode != nil {
			deps := extractDependencies(cteNode, source, false)
			for _, dep := range deps {
				if _, _, err := p.ResolveImports(sqlFile, dep); err == nil {
					walkCycle(dep)
				}
			}
		}
	}

	walkCycle("cte_a")
	if len(chain) != 2 {
		t.Fatalf("expected chain of 2 CTEs (cte_a, cte_b), got %v", chain)
	}
}

func TestSqlParser_ViewAndDynamicTable(t *testing.T) {
	p := NewSqlParser()

	t.Run("CREATE VIEW without CTE", func(t *testing.T) {
		dir := t.TempDir()
		sqlFile := filepath.Join(dir, "create_view.sql")
		content := `CREATE VIEW v_simple_orders AS
SELECT order_id, user_id, total_amount
FROM lake_db.sales.orders
WHERE status = 'COMPLETED';
`
		if err := os.WriteFile(sqlFile, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		symbols, err := p.ParseFile(sqlFile)
		if err != nil {
			t.Fatalf("ParseFile failed: %v", err)
		}

		// Must not pick up inner SELECT as main_query
		for _, s := range symbols {
			if s.Name == "main_query" {
				t.Errorf("expected view name symbol, got main_query: %+v", s)
			}
		}

		if len(symbols) != 1 {
			t.Fatalf("expected 1 symbol, got %d", len(symbols))
		}
		if symbols[0].Name != "v_simple_orders" {
			t.Errorf("expected symbol name 'v_simple_orders', got %q", symbols[0].Name)
		}
		if symbols[0].Kind != "statement" {
			t.Errorf("expected kind 'statement', got %q", symbols[0].Kind)
		}

		// ExtractSymbol with view name
		sym, err := p.ExtractSymbol(sqlFile, "v_simple_orders")
		if err != nil {
			t.Fatalf("ExtractSymbol failed: %v", err)
		}
		if sym.Name != "v_simple_orders" {
			t.Errorf("expected symbol name 'v_simple_orders', got %q", sym.Name)
		}
		if sym.Code == nil || !strings.Contains(*sym.Code, "CREATE VIEW v_simple_orders") || !strings.Contains(*sym.Code, "lake_db.sales.orders") {
			t.Errorf("expected full view definition code, got: %v", sym.Code)
		}
	})

	t.Run("CREATE OR REPLACE SECURE VIEW with CTE", func(t *testing.T) {
		dir := t.TempDir()
		sqlFile := filepath.Join(dir, "secure_view.sql")
		content := `CREATE OR REPLACE SECURE VIEW v_secure_sales AS
WITH sales_cte AS (
    SELECT order_id, amount FROM raw_sales WHERE amount > 0
)
SELECT order_id, amount * 1.1 AS gross_amount
FROM sales_cte;
`
		if err := os.WriteFile(sqlFile, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		symbols, err := p.ParseFile(sqlFile)
		if err != nil {
			t.Fatalf("ParseFile failed: %v", err)
		}

		var names []string
		for _, s := range symbols {
			names = append(names, s.Name)
			if s.Name == "main_query" {
				t.Errorf("inner SELECT should not become main_query in secure view: %+v", s)
			}
		}

		// Expect sales_cte and v_secure_sales
		if len(symbols) != 2 {
			t.Fatalf("expected 2 symbols (sales_cte, v_secure_sales), got %d: %v", len(symbols), names)
		}

		// ExtractSymbol with view name
		sym, err := p.ExtractSymbol(sqlFile, "v_secure_sales")
		if err != nil {
			t.Fatalf("ExtractSymbol v_secure_sales failed: %v", err)
		}
		if sym.Code == nil || !strings.Contains(*sym.Code, "CREATE OR REPLACE SECURE VIEW v_secure_sales") {
			t.Errorf("expected full view definition, got: %v", sym.Code)
		}

		// ExtractSymbol with CTE name
		cteSym, err := p.ExtractSymbol(sqlFile, "sales_cte")
		if err != nil {
			t.Fatalf("ExtractSymbol sales_cte failed: %v", err)
		}
		if cteSym.Kind != "cte" {
			t.Errorf("expected kind cte, got %s", cteSym.Kind)
		}
	})

	t.Run("CREATE MATERIALIZED VIEW", func(t *testing.T) {
		dir := t.TempDir()
		sqlFile := filepath.Join(dir, "mat_view.sql")
		content := `CREATE MATERIALIZED VIEW mv_daily_summary AS
SELECT date_trunc('day', created_at) AS sale_date, sum(amount) AS total_amount
FROM transactions
GROUP BY 1;
`
		if err := os.WriteFile(sqlFile, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		symbols, err := p.ParseFile(sqlFile)
		if err != nil {
			t.Fatalf("ParseFile failed: %v", err)
		}

		if len(symbols) != 1 {
			t.Fatalf("expected 1 symbol, got %d", len(symbols))
		}
		if symbols[0].Name != "mv_daily_summary" {
			t.Errorf("expected symbol name 'mv_daily_summary', got %q", symbols[0].Name)
		}

		sym, err := p.ExtractSymbol(sqlFile, "mv_daily_summary")
		if err != nil {
			t.Fatalf("ExtractSymbol mv_daily_summary failed: %v", err)
		}
		if sym.Code == nil || !strings.Contains(*sym.Code, "CREATE MATERIALIZED VIEW mv_daily_summary") {
			t.Errorf("expected full materialized view definition, got: %v", sym.Code)
		}
	})

	t.Run("CREATE DYNAMIC TABLE", func(t *testing.T) {
		dir := t.TempDir()
		sqlFile := filepath.Join(dir, "dynamic_table.sql")
		content := `CREATE DYNAMIC TABLE dt_product_stats
    TARGET_LAG = '5 minutes'
    WAREHOUSE = compute_wh
AS
SELECT product_id, count(*) AS event_count
FROM raw_events
GROUP BY product_id;
`
		if err := os.WriteFile(sqlFile, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		symbols, err := p.ParseFile(sqlFile)
		if err != nil {
			t.Fatalf("ParseFile failed: %v", err)
		}

		for _, s := range symbols {
			if s.Name == "main_query" {
				t.Errorf("inner SELECT should not become main_query in dynamic table: %+v", s)
			}
		}

		if len(symbols) != 1 {
			t.Fatalf("expected 1 symbol, got %d", len(symbols))
		}
		if symbols[0].Name != "dt_product_stats" {
			t.Errorf("expected symbol name 'dt_product_stats', got %q", symbols[0].Name)
		}

		sym, err := p.ExtractSymbol(sqlFile, "dt_product_stats")
		if err != nil {
			t.Fatalf("ExtractSymbol dt_product_stats failed: %v", err)
		}
		if sym.Code == nil || !strings.Contains(*sym.Code, "CREATE DYNAMIC TABLE dt_product_stats") {
			t.Errorf("expected full dynamic table definition, got: %v", sym.Code)
		}
	})

	t.Run("CREATE OR REPLACE TRANSIENT TABLE", func(t *testing.T) {
		dir := t.TempDir()
		sqlFile := filepath.Join(dir, "transient_table.sql")
		content := `CREATE OR REPLACE TRANSIENT TABLE stg_sessions (
    session_id VARCHAR,
    user_id VARCHAR,
    created_at TIMESTAMP_NTZ
);
`
		if err := os.WriteFile(sqlFile, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		symbols, err := p.ParseFile(sqlFile)
		if err != nil {
			t.Fatalf("ParseFile failed: %v", err)
		}

		if len(symbols) != 1 {
			t.Fatalf("expected 1 symbol, got %d", len(symbols))
		}
		if symbols[0].Name != "stg_sessions" {
			t.Errorf("expected symbol name 'stg_sessions', got %q", symbols[0].Name)
		}
	})
}

func TestSqlParser_WarningSuppression(t *testing.T) {
	p := NewSqlParser()
	fixturePath := filepath.Join("..", "..", "testdata", "sql", "snowflake_sample.sql")

	// Capture os.Stderr across multiple ParseFile invocations
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stderr = w

	// Call ParseFile 3 times
	for i := 0; i < 3; i++ {
		_, err := p.ParseFile(fixturePath)
		if err != nil {
			t.Fatalf("iteration %d ParseFile failed: %v", i, err)
		}
	}

	w.Close()
	os.Stderr = oldStderr

	var buf bytes.Buffer
	io.Copy(&buf, r)
	r.Close()

	output := buf.String()
	warningPattern := "Warning: Syntax errors or dialect extensions detected in"
	count := strings.Count(output, warningPattern)

	if count != 1 {
		t.Errorf("expected exactly 1 warning output across 3 ParseFile calls, got %d. Output:\n%s", count, output)
	}
}
