// Command ast-digger is the Go implementation of the AST-Digger CLI.
//
// It wires cobra subcommands (outline, symbol, resolve-import, directory,
// feedback, locate, references, agent-context, change-impact, mcp) to
// outline.Outliner, internal/impact, and internal/mcp, translating results/errors to the
// text/JSON output conventions and exit code taxonomy documented in
// SPECS/design_GOLANG.md. It corresponds to ast_digger/__main__.py in the
// Python implementation.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/snaga/ast-digger/internal/outline"
	"github.com/snaga/ast-digger/internal/parser"
)

// version is the ast-digger CLI version. It defaults to "dev" and can be
// overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

// newRootCmd builds the ast-digger root command with every currently
// implemented subcommand registered. SilenceUsage/SilenceErrors are set so
// that a failing RunE does not also trigger cobra's own usage/error output;
// main() is solely responsible for reporting errors (via outputError, in
// the text/JSON shape the failing subcommand's --json flag calls for) and
// choosing the process exit code (via decideExitCode, which wraps
// apperr.ExitCode; see exitcode.go).
//
// PersistentPreRunE is set to markBusinessLogicReached (rather than left
// unset) so that decideExitCode can tell cobra's own request-shape
// validation errors (unknown subcommand, wrong positional argument count,
// invalid flag value -- none of which have a dedicated Go error type of
// their own) apart from errors returned by a subcommand's own RunE, which
// always runs after PersistentPreRunE (task 14, F-11).
func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "ast-digger",
		Short: "AST-Digger: an AST-aware code exploration CLI for AI agents",
		Long: `AST-Digger: an AST-aware code exploration CLI for AI agents.

Supported languages and extensions:
  - Python (.py)
  - TypeScript (.ts, .tsx)
  - Golang (.go)
  - Java (.java)
  - JavaScript (.js, .jsx)
  - Rust (.rs)
  - SQL (.sql)
  - Markdown (.md, .markdown)`,
		Version:           version,
		SilenceUsage:      true,
		SilenceErrors:     true,
		PersistentPreRunE: markBusinessLogicReached,
	}

	rootCmd.PersistentFlags().BoolP("json", "j", false, "Output structure in JSON format")

	rootCmd.AddCommand(
		newOutlineCmd(),
		newSymbolCmd(),
		newResolveImportCmd(),
		newDirectoryCmd(),
		newFeedbackCmd(),
		newLocateCmd(),
		newReferencesCmd(),
		newAgentContextCmd(),
		newChangeImpactCmd(),
		newMcpCmd(),
	)

	return rootCmd
}

// jsonFlag reads the --json/-j flag from cmd (inherited from the root
// command's persistent flag set).
func jsonFlag(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("json")
	return v
}

// newOutlineCmd builds the `outline` subcommand: it prints a rendered
// skeleton (source with function/method bodies replaced by "...") in text
// mode, or the raw parsed symbol list in JSON mode. It corresponds to
// Python's `outline` command in ast_digger/__main__.py.
func newOutlineCmd() *cobra.Command {
	var noTruncate bool

	cmd := &cobra.Command{
		Use:   "outline <file_path>",
		Short: "Generate a class/function outline of a source file.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath := args[0]
			o := outline.NewOutliner()

			if jsonFlag(cmd) {
				symbols, err := o.ListSymbols(filePath)
				if err != nil {
					return err
				}
				return outputResult(cmd.OutOrStdout(), symbols, true)
			}

			skeleton, err := o.GenerateSkeleton(filePath)
			if err != nil {
				return err
			}
			// Truncation only applies to the text-mode skeleton (never to
			// `--json`'s raw symbol list), matching Python's `outline`
			// command in ast_digger/__main__.py.
			if !noTruncate {
				skeleton, _ = truncateText(skeleton, truncationLimit)
			}
			return outputResult(cmd.OutOrStdout(), skeleton, false)
		},
	}

	cmd.Flags().BoolVar(&noTruncate, "no-truncate", false, "Disable output truncation for large results.")

	return cmd
}

// symbolResult is the JSON shape printed by `symbol --json`: parser.SymbolData's
// fields (in the same order/under the same keys, so it serializes exactly
// like SymbolData when nothing was truncated), plus four fields that are
// only populated -- and, thanks to `omitempty`, only present in the
// marshaled JSON -- when the code was truncated. It matches Python's
// `symbol_data.__dict__.copy()` plus conditionally-added
// truncated/total_lines/limit_lines/message keys in the `symbol` command in
// ast_digger/__main__.py.
type symbolResult struct {
	Name         string              `json:"name"`
	Kind         string              `json:"kind"`
	Parent       *string             `json:"parent"`
	FilePath     *string             `json:"file_path"`
	StartLine    int                 `json:"start_line"`
	EndLine      int                 `json:"end_line"`
	Signature    *string             `json:"signature"`
	Docstring    *string             `json:"docstring"`
	Code         *string             `json:"code"`
	Dependencies []parser.SymbolData `json:"dependencies,omitempty"`

	Truncated  bool   `json:"truncated,omitempty"`
	TotalLines int    `json:"total_lines,omitempty"`
	LimitLines int    `json:"limit_lines,omitempty"`
	Message    string `json:"message,omitempty"`
}

// newSymbolCmd builds the `symbol` subcommand: it extracts and prints the
// full implementation code of symbol_path within file_path, as plain text
// (just the code) or as the full SymbolData in JSON mode. It corresponds to
// Python's `symbol` command in ast_digger/__main__.py.
func newSymbolCmd() *cobra.Command {
	var noTruncate bool
	var followImports bool

	cmd := &cobra.Command{
		Use:   "symbol <file_path> <symbol_path>",
		Short: "Extract the full implementation code of a targeted class or method from a file.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath, symbolPath := args[0], args[1]
			o := outline.NewOutliner()

			symDataWithDeps, err := o.ExtractSymbolWithDependencies(filePath, symbolPath, followImports)
			if err != nil {
				return err
			}
			symbolData := symDataWithDeps.SymbolData

			originalCode := ""
			if symbolData.Code != nil {
				originalCode = *symbolData.Code
			}
			totalLines := len(strings.Split(originalCode, "\n"))

			isTruncated := false
			if totalLines > truncationLimit && !noTruncate {
				truncatedCode, truncated := truncateText(originalCode, truncationLimit)
				isTruncated = truncated
				symbolData.Code = &truncatedCode
			}

			if jsonFlag(cmd) {
				result := symbolResult{
					Name:         symbolData.Name,
					Kind:         symbolData.Kind,
					Parent:       symbolData.Parent,
					FilePath:     symbolData.FilePath,
					StartLine:    symbolData.StartLine,
					EndLine:      symbolData.EndLine,
					Signature:    symbolData.Signature,
					Docstring:    symbolData.Docstring,
					Code:         symbolData.Code,
					Dependencies: symDataWithDeps.Dependencies,
				}
				if isTruncated {
					result.Truncated = true
					result.TotalLines = totalLines
					result.LimitLines = truncationLimit
					result.Message = "Output truncated to protect context. Re-run with --no-truncate to get full output."
				}
				return outputResult(cmd.OutOrStdout(), result, true)
			}

			code := ""
			if symbolData.Code != nil {
				code = *symbolData.Code
			}

			if followImports && len(symDataWithDeps.Dependencies) > 0 {
				var b strings.Builder
				b.WriteString(code)
				for _, dep := range symDataWithDeps.Dependencies {
					depPath := ""
					if dep.FilePath != nil {
						depPath = *dep.FilePath
					}
					depCode := ""
					if dep.Code != nil {
						depCode = *dep.Code
					}
					fmt.Fprintf(&b, "\n\n--- Dependency: %s::%s ---\n%s", depPath, dep.Name, depCode)
				}
				code = b.String()
			}

			if !noTruncate {
				code, _ = truncateText(code, truncationLimit)
			}

			return outputResult(cmd.OutOrStdout(), code, false)
		},
	}

	cmd.Flags().BoolVar(&noTruncate, "no-truncate", false, "Disable output truncation for large results.")
	cmd.Flags().BoolVar(&followImports, "follow-imports", false, "Automatically follow imported symbols and extract their definition code.")

	return cmd
}

// locateResult is the JSON shape printed by `locate --json`, matching
// Python's `locate` command's `result` dict.
type locateResult struct {
	File      string `json:"file"`
	Symbol    string `json:"symbol"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// newLocateCmd builds the `locate` subcommand: it resolves symbol_path's
// definition location within file_path without loading its source code. It
// corresponds to Python's `locate` command in ast_digger/__main__.py.
func newLocateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "locate <file_path> <symbol_path>",
		Short: "Locate the line range of a targeted class or method without loading its source code.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath, symbolPath := args[0], args[1]
			o := outline.NewOutliner()

			resolvedPath, startLine, endLine, err := o.LocateSymbol(filePath, symbolPath)
			if err != nil {
				return err
			}

			if jsonFlag(cmd) {
				// symbol_path's final dot-separated component is the symbol's
				// own name (e.g. "MyClass.my_method" -> "my_method"), matching
				// Python's `symbol_data.name` (Outliner.locate_symbol returns
				// the full SymbolData there; Go's LocateSymbol intentionally
				// returns only the lightweight location tuple, see
				// outline.Outliner.LocateSymbol's doc comment).
				parts := strings.Split(symbolPath, ".")
				symbolName := parts[len(parts)-1]
				result := locateResult{
					File:      resolvedPath,
					Symbol:    symbolName,
					StartLine: startLine,
					EndLine:   endLine,
				}
				return outputResult(cmd.OutOrStdout(), result, true)
			}

			return outputResult(cmd.OutOrStdout(), fmt.Sprintf("%s:%d-%d", resolvedPath, startLine, endLine), false)
		},
	}
}

// resolveImportResult is the JSON shape printed by `resolve-import --json`,
// matching Python's `resolve_import` command's `result` dict.
type resolveImportResult struct {
	FilePath  string `json:"file_path"`
	StartLine int    `json:"start_line"`
}

// newResolveImportCmd builds the `resolve-import` subcommand: it resolves
// the definition file/line of symbol_name as imported by file_path. It
// corresponds to Python's `resolve_import` command in
// ast_digger/__main__.py.
func newResolveImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resolve-import <file_path> <symbol_name>",
		Short: "Resolve the definition source file and start line of an imported symbol.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath, symbolName := args[0], args[1]
			o := outline.NewOutliner()

			resFile, resLine, err := o.ResolveImports(filePath, symbolName)
			if err != nil {
				return err
			}

			if jsonFlag(cmd) {
				result := resolveImportResult{FilePath: resFile, StartLine: resLine}
				return outputResult(cmd.OutOrStdout(), result, true)
			}

			return outputResult(cmd.OutOrStdout(), fmt.Sprintf("%s:%d", resFile, resLine), false)
		},
	}
}

// newDirectoryCmd builds the `directory` subcommand: it prints dir_path's
// directory tree, annotated with the (incrementally cached) symbol outline
// of every supported source file within it. It corresponds to Python's
// `directory` command in ast_digger/__main__.py.
func newDirectoryCmd() *cobra.Command {
	var recursive bool
	var noRecursive bool
	var limit int

	cmd := &cobra.Command{
		Use:   "directory [dir_path]",
		Short: "Print directory tree structure populated with incremental cached symbol outline.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dirPath := "."
			if len(args) > 0 {
				dirPath = args[0]
			}

			// --no-recursive mirrors Python's `--recursive/--no-recursive`
			// paired option: when passed, it overrides --recursive (whether
			// or not --recursive was also explicitly passed) so that
			// `--no-recursive` always disables the recursive scan, matching
			// typer's paired-flag semantics.
			if noRecursive {
				recursive = false
			}

			o := outline.NewOutliner()
			tree, err := o.BuildDirectoryTree(dirPath, recursive, limit)
			if err != nil {
				return err
			}

			return outputResult(cmd.OutOrStdout(), tree, jsonFlag(cmd))
		},
	}

	cmd.Flags().BoolVar(&recursive, "recursive", true, "Scan recursively down the directory tree (pass --recursive=false or --no-recursive to scan only the top level).")
	cmd.Flags().BoolVar(&noRecursive, "no-recursive", false, "Scan only the top level of the directory tree; overrides --recursive.")

	cmd.Flags().IntVar(&limit, "limit", 50, "Max files to display in the output to avoid token bloating.")

	return cmd
}

// newFeedbackCmd builds the `feedback` subcommand: it appends message to
// the local workspace feedback log. It corresponds to Python's `feedback`
// command in ast_digger/__main__.py.
func newFeedbackCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "feedback <message>",
		Short: "Record friction or error messages to the local workspace feedback log file.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o := outline.NewOutliner()
			if err := o.WriteFeedbackLog(args[0]); err != nil {
				return err
			}

			const successMessage = "Feedback recorded successfully."
			if jsonFlag(cmd) {
				return outputResult(cmd.OutOrStdout(), map[string]string{"message": successMessage}, true)
			}
			return outputResult(cmd.OutOrStdout(), successMessage, false)
		},
	}
}

// referencesResult is the JSON shape printed by `references --json`,
// matching Python's `references` command's `result` dict (field order:
// references, truncated, total_count, limit, and -- only when truncated --
// hint).
type referencesResult struct {
	References []parser.ReferenceData `json:"references"`
	Truncated  bool                   `json:"truncated"`
	TotalCount int                    `json:"total_count"`
	Limit      int                    `json:"limit"`
	Hint       string                 `json:"hint,omitempty"`
}

// newReferencesCmd builds the `references` subcommand: it searches every
// supported source file under --project-root for occurrences of
// symbol_name, capping the number of results returned at --limit (default
// 50) to avoid token bloating. It corresponds to Python's `references`
// command in ast_digger/__main__.py.
func newReferencesCmd() *cobra.Command {
	var projectRoot string
	var limit int

	cmd := &cobra.Command{
		Use:   "references <symbol_name>",
		Short: "Search every supported source file under a project root for references to a symbol.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			symbolName := args[0]
			o := outline.NewOutliner()

			refs, err := o.FindReferences(projectRoot, symbolName)
			if err != nil {
				return err
			}

			totalCount := len(refs)
			truncated := false
			if totalCount > limit {
				refs = refs[:limit]
				truncated = true
			}

			if jsonFlag(cmd) {
				result := referencesResult{
					References: refs,
					Truncated:  truncated,
					TotalCount: totalCount,
					Limit:      limit,
				}
				if truncated {
					result.Hint = "Narrow down the scope using --project-root or increase --limit"
				}
				return outputResult(cmd.OutOrStdout(), result, true)
			}

			if err := outputResult(cmd.OutOrStdout(), refs, false); err != nil {
				return err
			}
			if truncated {
				_, err := fmt.Fprintf(cmd.ErrOrStderr(), "# ... (truncated: %d of %d results shown. Use --limit to increase or narrow down using --project-root)\n", limit, totalCount)
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&projectRoot, "project-root", ".", "Project root directory path to search in.")
	cmd.Flags().IntVar(&limit, "limit", 50, "Max references to display to avoid token bloating.")

	return cmd
}

func main() {
	rootCmd := newRootCmd()

	cmd, err := rootCmd.ExecuteC()
	if err != nil {
		outputError(os.Stderr, err, jsonFlag(cmd))
		os.Exit(decideExitCode(cmd, err))
	}
}
