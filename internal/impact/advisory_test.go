package impact

import (
	"reflect"
	"testing"
)

func TestBuildDocumentationAdvisory(t *testing.T) {
	tests := []struct {
		name        string
		symbols     []*SymbolImpact
		fileImpacts []*FileImpact
		want        *DocumentationAdvisory
	}{
		{
			name:        "Empty input returns no advisory",
			symbols:     nil,
			fileImpacts: nil,
			want: &DocumentationAdvisory{
				RequiresIntentReview: false,
			},
		},
		{
			name: "Minor changes in single module - advisory not required",
			symbols: []*SymbolImpact{
				{
					File:            "internal/parser/sql.go",
					SymbolPath:      "helperFunc",
					Kind:            "function",
					Visibility:      "private",
					ChangeType:      "added",
					StatementsDelta: 5,
				},
				{
					File:            "internal/parser/sql.go",
					SymbolPath:      "ExistingFunc",
					Kind:            "function",
					Visibility:      "public",
					ChangeType:      "modified",
					StatementsDelta: 2,
				},
			},
			fileImpacts: []*FileImpact{
				{
					FilePath: "internal/parser/sql.go",
					Statements: StatementDelta{
						Added:   7,
						Deleted: 2,
					}, // total 9 < 20
				},
			},
			want: &DocumentationAdvisory{
				RequiresIntentReview: false,
			},
		},
		{
			name: "Moderate statements (>= 20) in single module triggers module-level advisory",
			symbols: []*SymbolImpact{
				{
					File:            "internal/parser/sql.go",
					SymbolPath:      "internalParserLogic",
					Kind:            "function",
					Visibility:      "private",
					ChangeType:      "modified",
					StatementsDelta: 25,
				},
			},
			fileImpacts: []*FileImpact{
				{
					FilePath: "internal/parser/sql.go",
					Statements: StatementDelta{
						Added:   20,
						Deleted: 5,
					}, // total 25 >= 20
				},
			},
			want: &DocumentationAdvisory{
				RequiresIntentReview: true,
				Reason:               "STRUCTURAL_CHANGE_IN_MODULE",
				AffectedScopes: []AffectedScope{
					{
						Scope:        "internal/parser",
						Category:     "module_level",
						Description:  "Significant structural changes in module internal/parser (25 statements modified).",
						PromptAction: PromptActionModuleLevel,
					},
				},
			},
		},
		{
			name: "New public API in single module triggers module-level advisory even with minor statements",
			symbols: []*SymbolImpact{
				{
					File:            "internal/impact/types.go",
					SymbolPath:      "DocumentationAdvisory",
					Kind:            "struct",
					Visibility:      "public",
					ChangeType:      "added",
					StatementsDelta: 5,
				},
			},
			fileImpacts: []*FileImpact{
				{
					FilePath: "internal/impact/types.go",
					Statements: StatementDelta{
						Added:   5,
						Deleted: 0,
					}, // total 5 < 20
				},
			},
			want: &DocumentationAdvisory{
				RequiresIntentReview: true,
				Reason:               "STRUCTURAL_CHANGE_IN_MODULE",
				AffectedScopes: []AffectedScope{
					{
						Scope:        "internal/impact",
						Category:     "module_level",
						Description:  "New public API introduced in module internal/impact.",
						PromptAction: PromptActionModuleLevel,
					},
				},
			},
		},
		{
			name: "New public API and >= 20 statements in single module triggers combined description",
			symbols: []*SymbolImpact{
				{
					File:            "internal/impact/advisory.go",
					SymbolPath:      "BuildDocumentationAdvisory",
					Kind:            "function",
					Visibility:      "public",
					ChangeType:      "added",
					StatementsDelta: 30,
				},
			},
			fileImpacts: []*FileImpact{
				{
					FilePath: "internal/impact/advisory.go",
					Statements: StatementDelta{
						Added:   30,
						Deleted: 0,
					},
				},
			},
			want: &DocumentationAdvisory{
				RequiresIntentReview: true,
				Reason:               "STRUCTURAL_CHANGE_IN_MODULE",
				AffectedScopes: []AffectedScope{
					{
						Scope:        "internal/impact",
						Category:     "module_level",
						Description:  "Significant structural changes (30 statements modified) and new public API introduced in module internal/impact.",
						PromptAction: PromptActionModuleLevel,
					},
				},
			},
		},
		{
			name: "Cross-module change with minor modifications generates cross-module advisory",
			symbols: []*SymbolImpact{
				{
					File:       "cmd/ast-digger/main.go",
					SymbolPath: "main",
					ChangeType: "modified",
					Visibility: "private",
				},
				{
					File:       "internal/outline/outline.go",
					SymbolPath: "helper",
					ChangeType: "modified",
					Visibility: "private",
				},
			},
			fileImpacts: []*FileImpact{
				{
					FilePath:   "cmd/ast-digger/main.go",
					Statements: StatementDelta{Added: 2, Deleted: 1},
				},
				{
					FilePath:   "internal/outline/outline.go",
					Statements: StatementDelta{Added: 3, Deleted: 0},
				},
			},
			want: &DocumentationAdvisory{
				RequiresIntentReview: true,
				Reason:               "CROSS_MODULE_CHANGE",
				AffectedScopes: []AffectedScope{
					{
						Scope:        "cross_module",
						Category:     "cross_module",
						Description:  "Changes span across multiple modules (cmd/ast-digger, internal/outline).",
						PromptAction: PromptActionCrossModule,
					},
				},
			},
		},
		{
			name: "Cross-module change with structural change in one module generates both scopes",
			symbols: []*SymbolImpact{
				{
					File:       "internal/parser/sql.go",
					SymbolPath: "NewParserFunction",
					Kind:       "function",
					Visibility: "public",
					ChangeType: "added",
				},
				{
					File:       "cmd/ast-digger/main.go",
					SymbolPath: "init",
					ChangeType: "modified",
					Visibility: "private",
				},
			},
			fileImpacts: []*FileImpact{
				{
					FilePath:   "internal/parser/sql.go",
					Statements: StatementDelta{Added: 25, Deleted: 0},
				},
				{
					FilePath:   "cmd/ast-digger/main.go",
					Statements: StatementDelta{Added: 2, Deleted: 0},
				},
			},
			want: &DocumentationAdvisory{
				RequiresIntentReview: true,
				Reason:               "CROSS_MODULE_AND_STRUCTURAL_CHANGE",
				AffectedScopes: []AffectedScope{
					{
						Scope:        "internal/parser",
						Category:     "module_level",
						Description:  "Significant structural changes (25 statements modified) and new public API introduced in module internal/parser.",
						PromptAction: PromptActionModuleLevel,
					},
					{
						Scope:        "cross_module",
						Category:     "cross_module",
						Description:  "Changes span across multiple modules (cmd/ast-digger, internal/parser).",
						PromptAction: PromptActionCrossModule,
					},
				},
			},
		},
		{
			name: "Windows path separators are normalized to slash directory",
			symbols: []*SymbolImpact{
				{
					File:       `internal\parser\sql.go`,
					SymbolPath: "ExportedFunction",
					Kind:       "function",
					Visibility: "public",
					ChangeType: "added",
				},
			},
			fileImpacts: []*FileImpact{
				{
					FilePath:   `internal\parser\sql.go`,
					Statements: StatementDelta{Added: 5, Deleted: 0},
				},
			},
			want: &DocumentationAdvisory{
				RequiresIntentReview: true,
				Reason:               "STRUCTURAL_CHANGE_IN_MODULE",
				AffectedScopes: []AffectedScope{
					{
						Scope:        "internal/parser",
						Category:     "module_level",
						Description:  "New public API introduced in module internal/parser.",
						PromptAction: PromptActionModuleLevel,
					},
				},
			},
		},
		{
			name: "Root directory files are grouped under '.'",
			symbols: []*SymbolImpact{
				{
					File:       "main.go",
					SymbolPath: "RootPublicFunc",
					Kind:       "function",
					Visibility: "public",
					ChangeType: "added",
				},
			},
			fileImpacts: []*FileImpact{
				{
					FilePath:   "main.go",
					Statements: StatementDelta{Added: 2, Deleted: 0},
				},
			},
			want: &DocumentationAdvisory{
				RequiresIntentReview: true,
				Reason:               "STRUCTURAL_CHANGE_IN_MODULE",
				AffectedScopes: []AffectedScope{
					{
						Scope:        ".",
						Category:     "module_level",
						Description:  "New public API introduced in module ..",
						PromptAction: PromptActionModuleLevel,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildDocumentationAdvisory(tt.symbols, tt.fileImpacts)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("BuildDocumentationAdvisory() =\n%+v\nwant:\n%+v", got, tt.want)
			}
		})
	}
}

func TestExtractModule(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "."},
		{".", "."},
		{"main.go", "."},
		{"./main.go", "."},
		{"internal/parser/sql.go", "internal/parser"},
		{`internal\parser\sql.go`, "internal/parser"},
		{"cmd/ast-digger/main.go", "cmd/ast-digger"},
		{"a/b/c/d.go", "a/b/c"},
	}

	for _, tt := range tests {
		got := extractModule(tt.input)
		if got != tt.want {
			t.Errorf("extractModule(%q) = %q; want %q", tt.input, got, tt.want)
		}
	}
}
