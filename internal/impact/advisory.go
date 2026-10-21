// Package impact provides syntax-aware source code change impact analysis.
package impact

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// PromptActionModuleLevel is the advisory prompt for module-level documentation.
	PromptActionModuleLevel = "Verify if module-level documentation (such as overview docs, specifications, or architectural guides) should be created or updated to preserve design intent."
	// PromptActionCrossModule is the advisory prompt for cross-module documentation.
	PromptActionCrossModule = "Ensure system-level documentation or architecture decision records (ADR) reflect this cross-cutting update if applicable."
)

// BuildDocumentationAdvisory evaluates module-level and cross-module structural changes
// and produces a DocumentationAdvisory if documentation review or update is recommended.
func BuildDocumentationAdvisory(symbols []*SymbolImpact, fileImpacts []*FileImpact) *DocumentationAdvisory {
	modStatements := make(map[string]int)
	modSymbols := make(map[string][]*SymbolImpact)
	allModulesMap := make(map[string]bool)

	for _, fi := range fileImpacts {
		if fi == nil || fi.FilePath == "" {
			continue
		}
		mod := extractModule(fi.FilePath)
		modStatements[mod] += fi.Statements.Added + fi.Statements.Deleted
		allModulesMap[mod] = true
	}

	for _, sym := range symbols {
		if sym == nil || sym.File == "" {
			continue
		}
		mod := extractModule(sym.File)
		modSymbols[mod] = append(modSymbols[mod], sym)
		allModulesMap[mod] = true
	}

	if len(allModulesMap) == 0 {
		return &DocumentationAdvisory{
			RequiresIntentReview: false,
		}
	}

	var allModules []string
	for mod := range allModulesMap {
		allModules = append(allModules, mod)
	}
	sort.Strings(allModules)

	var affectedScopes []AffectedScope
	hasModuleLevel := false

	for _, mod := range allModules {
		stmtChanges := modStatements[mod]
		syms := modSymbols[mod]

		hasNewPublicAPI := false
		for _, s := range syms {
			if s.ChangeType == "added" && s.Visibility == "public" {
				hasNewPublicAPI = true
				break
			}
		}

		isModerate := stmtChanges >= 20

		if hasNewPublicAPI || isModerate {
			hasModuleLevel = true
			var desc string
			if hasNewPublicAPI && isModerate {
				desc = fmt.Sprintf("Significant structural changes (%d statements modified) and new public API introduced in module %s.", stmtChanges, mod)
			} else if hasNewPublicAPI {
				desc = fmt.Sprintf("New public API introduced in module %s.", mod)
			} else {
				desc = fmt.Sprintf("Significant structural changes in module %s (%d statements modified).", mod, stmtChanges)
			}

			affectedScopes = append(affectedScopes, AffectedScope{
				Scope:        mod,
				Category:     "module_level",
				Description:  desc,
				PromptAction: PromptActionModuleLevel,
			})
		}
	}

	hasCrossModule := len(allModules) >= 2
	if hasCrossModule {
		affectedScopes = append(affectedScopes, AffectedScope{
			Scope:        "cross_module",
			Category:     "cross_module",
			Description:  fmt.Sprintf("Changes span across multiple modules (%s).", strings.Join(allModules, ", ")),
			PromptAction: PromptActionCrossModule,
		})
	}

	if len(affectedScopes) == 0 {
		return &DocumentationAdvisory{
			RequiresIntentReview: false,
		}
	}

	var reason string
	if hasCrossModule && hasModuleLevel {
		reason = "CROSS_MODULE_AND_STRUCTURAL_CHANGE"
	} else if hasCrossModule {
		reason = "CROSS_MODULE_CHANGE"
	} else {
		reason = "STRUCTURAL_CHANGE_IN_MODULE"
	}

	return &DocumentationAdvisory{
		RequiresIntentReview: true,
		Reason:               reason,
		AffectedScopes:       affectedScopes,
	}
}

// ExtractModule extracts normalized directory boundary representation for a file path.
func ExtractModule(path string) string {
	if path == "" {
		return "."
	}
	clean := filepath.Clean(filepath.ToSlash(path))
	dir := filepath.Dir(clean)
	dir = filepath.ToSlash(dir)
	if dir == "" || dir == "." || dir == "/" {
		return "."
	}
	return dir
}

func extractModule(path string) string {
	return ExtractModule(path)
}
