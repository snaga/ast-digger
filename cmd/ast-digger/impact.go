package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/snaga/ast-digger/internal/apperr"
	"github.com/snaga/ast-digger/internal/impact"
)

// ChangeImpactReport represents the comprehensive change impact analysis report.
type ChangeImpactReport struct {
	Summary               ChangeImpactSummary           `json:"summary"`
	Symbols               []*impact.SymbolImpact        `json:"symbols"`
	Audit                 impact.DocAuditResult         `json:"audit"`
	DocumentationAdvisory *impact.DocumentationAdvisory `json:"documentation_advisory"`
}

// ChangeImpactSummary represents the aggregated summary metrics across all changed files.
type ChangeImpactSummary struct {
	ImpactLevel       impact.ImpactLevel `json:"impact_level"`
	StatementsAdded   int                `json:"statements_added"`
	StatementsDeleted int                `json:"statements_deleted"`
	ComplexityDelta   int                `json:"complexity_delta"`
	SymbolsAdded      int                `json:"symbols_added"`
	SymbolsModified   int                `json:"symbols_modified"`
	SymbolsDeleted    int                `json:"symbols_deleted"`
	FilesChanged      int                `json:"files_changed"`
	ModulesAffected   []string           `json:"modules_affected"`
}

// newChangeImpactCmd builds the `change-impact` subcommand: it analyzes git diff changes
// against baseRef (default HEAD), measuring syntax statements added/deleted, complexity deltas,
// symbol alterations, docstring coverage, and external documentation advisories.
func newChangeImpactCmd() *cobra.Command {
	var (
		baseRef          string
		maxStatements    int
		requireDoc       string
		failOnMissingDoc bool
	)

	cmd := &cobra.Command{
		Use:   "change-impact",
		Short: "Analyze semantic change impact, complexity deltas, docstring coverage, and advisories across git diff.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Validate --require-doc
			switch requireDoc {
			case string(impact.DocAuditExported), string(impact.DocAuditAll), string(impact.DocAuditNone):
				// valid
			default:
				return &apperr.GuardrailError{
					Message: fmt.Sprintf("invalid --require-doc value %q: must be one of exported, all, none", requireDoc),
				}
			}

			// 1. Get diff information against baseRef
			diffInfos, err := impact.GetGitDiff(".", baseRef)
			if err != nil {
				return err
			}

			// 2. Analyze each diff file
			var fileImpacts []*impact.FileImpact
			var allSymbols []*impact.SymbolImpact
			modulesMap := make(map[string]bool)

			for _, info := range diffInfos {
				targetPath := info.FilePath
				if targetPath == "" {
					targetPath = info.OldPath
				}
				if targetPath != "" {
					modulesMap[impact.ExtractModule(targetPath)] = true
				}

				// Source statements and complexity delta
				fi, err := impact.AnalyzeFileSource(targetPath, "", info.OldContent, info.NewContent)
				if err == nil && fi != nil {
					fileImpacts = append(fileImpacts, fi)
				}

				// Symbol impact and docstring audit
				syms, _, err := impact.AuditFileSymbols(info, impact.DocAuditRequirement(requireDoc))
				if err == nil && syms != nil {
					allSymbols = append(allSymbols, syms...)
				}
			}

			// 3. Build summary metrics
			totalAdded := 0
			totalDeleted := 0
			totalComplexityDelta := 0
			for _, fi := range fileImpacts {
				totalAdded += fi.Statements.Added
				totalDeleted += fi.Statements.Deleted
				totalComplexityDelta += fi.ComplexityDelta
			}

			symAdded := 0
			symModified := 0
			symDeleted := 0
			totalPublic := 0
			docPublic := 0
			totalPrivate := 0
			docPrivate := 0
			var missingDocstrings []string

			docReq := impact.DocAuditRequirement(requireDoc)
			for _, sym := range allSymbols {
				switch sym.ChangeType {
				case "added":
					symAdded++
				case "modified":
					symModified++
				case "deleted":
					symDeleted++
					continue // Deleted symbols are not audited for docstrings
				}

				if sym.Visibility == "public" {
					totalPublic++
					if sym.HasDocstring {
						docPublic++
					} else if docReq == impact.DocAuditExported || docReq == impact.DocAuditAll {
						missingDocstrings = append(missingDocstrings, sym.File+"::"+sym.SymbolPath)
					}
				} else {
					totalPrivate++
					if sym.HasDocstring {
						docPrivate++
					} else if docReq == impact.DocAuditAll {
						missingDocstrings = append(missingDocstrings, sym.File+"::"+sym.SymbolPath)
					}
				}
			}

			pubCoverage := 1.0
			if totalPublic > 0 {
				pubCoverage = float64(docPublic) / float64(totalPublic)
			}
			privCoverage := 1.0
			if totalPrivate > 0 {
				privCoverage = float64(docPrivate) / float64(totalPrivate)
			}
			if missingDocstrings == nil {
				missingDocstrings = []string{}
			}

			var modulesAffected []string
			for mod := range modulesMap {
				modulesAffected = append(modulesAffected, mod)
			}
			sort.Strings(modulesAffected)
			if modulesAffected == nil {
				modulesAffected = []string{}
			}

			if allSymbols == nil {
				allSymbols = []*impact.SymbolImpact{}
			}

			advisory := impact.BuildDocumentationAdvisory(allSymbols, fileImpacts)
			impactLevel := impact.CalculateImpactLevel(totalAdded+totalDeleted, totalComplexityDelta)

			report := ChangeImpactReport{
				Summary: ChangeImpactSummary{
					ImpactLevel:       impactLevel,
					StatementsAdded:   totalAdded,
					StatementsDeleted: totalDeleted,
					ComplexityDelta:   totalComplexityDelta,
					SymbolsAdded:      symAdded,
					SymbolsModified:   symModified,
					SymbolsDeleted:    symDeleted,
					FilesChanged:      len(diffInfos),
					ModulesAffected:   modulesAffected,
				},
				Symbols: allSymbols,
				Audit: impact.DocAuditResult{
					PublicCoverage:    pubCoverage,
					PrivateCoverage:   privCoverage,
					MissingDocstrings: missingDocstrings,
				},
				DocumentationAdvisory: advisory,
			}

			// 4. Output report
			if jsonFlag(cmd) {
				if err := outputResult(cmd.OutOrStdout(), report, true); err != nil {
					return err
				}
			} else {
				textReport := formatTextReport(report)
				if err := outputResult(cmd.OutOrStdout(), textReport, false); err != nil {
					return err
				}
			}

			// 5. Guardrail validations
			var violations []string
			if maxStatements > 0 && totalAdded > maxStatements {
				violations = append(violations, fmt.Sprintf("statements added (%d) exceeded maximum allowed (%d)", totalAdded, maxStatements))
			}
			if failOnMissingDoc && len(missingDocstrings) > 0 {
				violations = append(violations, fmt.Sprintf("%d symbol(s) missing required docstrings: %s", len(missingDocstrings), strings.Join(missingDocstrings, ", ")))
			}
			if len(violations) > 0 {
				return &apperr.GuardrailError{
					Message: fmt.Sprintf("guardrail check failed: %s", strings.Join(violations, "; ")),
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&baseRef, "base", "HEAD", "Base git reference to compare working tree against.")
	cmd.Flags().IntVar(&maxStatements, "max-statements", 0, "Maximum allowed added statements guardrail (0 = unlimited).")
	cmd.Flags().StringVar(&requireDoc, "require-doc", "exported", "Required docstring audit scope: exported, all, none.")
	cmd.Flags().BoolVar(&failOnMissingDoc, "fail-on-missing-doc", false, "Fail with exit code 2 if any required docstring is missing.")

	return cmd
}

func formatTextReport(report ChangeImpactReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Change Impact Analysis Summary:\n")
	fmt.Fprintf(&b, "  Impact Level     : %s\n", report.Summary.ImpactLevel)
	fmt.Fprintf(&b, "  Statements Delta : +%d / -%d\n", report.Summary.StatementsAdded, report.Summary.StatementsDeleted)
	fmt.Fprintf(&b, "  Complexity Delta : %+d\n", report.Summary.ComplexityDelta)
	fmt.Fprintf(&b, "  Symbols Changed  : +%d added, ~%d modified, -%d deleted\n", report.Summary.SymbolsAdded, report.Summary.SymbolsModified, report.Summary.SymbolsDeleted)
	fmt.Fprintf(&b, "  Files Changed    : %d\n", report.Summary.FilesChanged)
	fmt.Fprintf(&b, "  Modules Affected : %s\n\n", strings.Join(report.Summary.ModulesAffected, ", "))

	fmt.Fprintf(&b, "Impacted Symbols:\n")
	if len(report.Symbols) == 0 {
		fmt.Fprintf(&b, "  (none)\n")
	} else {
		for _, sym := range report.Symbols {
			sign := "~"
			if sym.ChangeType == "added" {
				sign = "+"
			} else if sym.ChangeType == "deleted" {
				sign = "-"
			}
			docStatus := "docstring present"
			if !sym.HasDocstring {
				docStatus = "no docstring"
			}
			fmt.Fprintf(&b, "  [%s] %s::%s (%s, %s, delta: %d) [%s]\n",
				sign, sym.File, sym.SymbolPath, sym.Kind, sym.Visibility, sym.StatementsDelta, docStatus)
		}
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "Documentation Audit:\n")
	fmt.Fprintf(&b, "  Public API Doc Coverage  : %.1f%%\n", report.Audit.PublicCoverage*100)
	fmt.Fprintf(&b, "  Private API Doc Coverage : %.1f%%\n", report.Audit.PrivateCoverage*100)
	if len(report.Audit.MissingDocstrings) > 0 {
		fmt.Fprintf(&b, "  Missing Docstrings:\n")
		for _, m := range report.Audit.MissingDocstrings {
			fmt.Fprintf(&b, "    - %s\n", m)
		}
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "Documentation Advisory:\n")
	if report.DocumentationAdvisory != nil && report.DocumentationAdvisory.RequiresIntentReview {
		fmt.Fprintf(&b, "  Requires Intent Review: Yes (%s)\n", report.DocumentationAdvisory.Reason)
		for _, scope := range report.DocumentationAdvisory.AffectedScopes {
			fmt.Fprintf(&b, "  * [%s] (%s): %s\n", scope.Scope, scope.Category, scope.Description)
			fmt.Fprintf(&b, "    Action: %s\n", scope.PromptAction)
		}
	} else {
		fmt.Fprintf(&b, "  Requires Intent Review: No\n")
	}

	return strings.TrimRight(b.String(), "\n")
}
