// Package impact provides syntax-aware source code change impact analysis.
// It measures statement counts and cyclomatic complexity deltas using Tree-sitter ASTs.
package impact

// ImpactLevel represents the magnitude of source code change.
type ImpactLevel string

const (
	// ImpactMinor indicates minor changes (statements < 20 and complexity delta <= 2).
	ImpactMinor ImpactLevel = "MINOR"
	// ImpactModerate indicates moderate changes (statements 20-60 or complexity delta > 2 with statements < 20).
	ImpactModerate ImpactLevel = "MODERATE"
	// ImpactSignificant indicates significant changes (statements 61-150).
	ImpactSignificant ImpactLevel = "SIGNIFICANT"
	// ImpactExtreme indicates extreme changes (statements > 150).
	ImpactExtreme ImpactLevel = "EXTREME"
)

// StatementDelta represents the number of added and deleted statements.
type StatementDelta struct {
	Added   int `json:"added"`
	Deleted int `json:"deleted"`
}

// FileImpact represents the impact analysis result for a single file.
type FileImpact struct {
	FilePath        string         `json:"file_path,omitempty"`
	Statements      StatementDelta `json:"statements"`
	ComplexityDelta int            `json:"complexity_delta"`
	ImpactLevel     ImpactLevel    `json:"impact_level"`
}

// FileDiffInfo contains git diff information for a single file.
type FileDiffInfo struct {
	FilePath     string `json:"file_path"`
	OldPath      string `json:"old_path,omitempty"`
	IsNew        bool   `json:"is_new"`
	IsDeleted    bool   `json:"is_deleted"`
	AddedLines   []int  `json:"added_lines"`
	DeletedLines []int  `json:"deleted_lines"`
	OldContent   []byte `json:"old_content,omitempty"`
	NewContent   []byte `json:"new_content,omitempty"`
}

// CalculateImpactLevel determines the ImpactLevel from statement changes and complexity delta.
func CalculateImpactLevel(statementChanges int, complexityDelta int) ImpactLevel {
	if statementChanges > 150 {
		return ImpactExtreme
	}
	if statementChanges > 60 {
		return ImpactSignificant
	}
	if statementChanges >= 20 || complexityDelta > 2 {
		return ImpactModerate
	}
	return ImpactMinor
}

// DocAuditRequirement specifies the required documentation level for symbol audit.
type DocAuditRequirement string

const (
	// DocAuditExported requires docstrings only for exported/public symbols (default).
	DocAuditExported DocAuditRequirement = "exported"
	// DocAuditAll requires docstrings for all symbols (both public and private).
	DocAuditAll DocAuditRequirement = "all"
	// DocAuditNone performs no docstring audit.
	DocAuditNone DocAuditRequirement = "none"
)

// SymbolImpact represents impact analysis for a single symbol.
type SymbolImpact struct {
	File            string `json:"file"`
	SymbolPath      string `json:"symbol_path"`
	Kind            string `json:"kind"`
	Visibility      string `json:"visibility"`  // "public" or "private"
	ChangeType      string `json:"change_type"` // "added", "modified", "deleted"
	StatementsDelta int    `json:"statements_delta"`
	HasDocstring    bool   `json:"has_docstring"`
}

// DocAuditResult represents documentation audit metrics and missing docstrings.
type DocAuditResult struct {
	PublicCoverage    float64  `json:"public_api_doc_coverage"`
	PrivateCoverage   float64  `json:"private_api_doc_coverage"`
	MissingDocstrings []string `json:"missing_docstrings"`
}

// DocumentationAdvisory represents guidance on synchronizing external documentation.
type DocumentationAdvisory struct {
	RequiresIntentReview bool            `json:"requires_intent_review"`
	Reason               string          `json:"reason,omitempty"`
	AffectedScopes       []AffectedScope `json:"affected_scopes,omitempty"`
}

// AffectedScope represents a scope (module or cross-module) that may require documentation updates.
type AffectedScope struct {
	Scope        string `json:"scope"`
	Category     string `json:"category"` // "module_level" or "cross_module"
	Description  string `json:"description"`
	PromptAction string `json:"prompt_action"`
}
