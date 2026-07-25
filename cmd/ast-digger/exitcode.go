package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/snaga/ast-digger/internal/apperr"
)

// businessLogicCtxKey is the context.Context key used to record, on a
// command's own Context(), that cobra got as far as invoking that command's
// business logic (its PersistentPreRunE/PreRunE/RunE chain) -- as opposed to
// failing earlier, during cobra's own command-lookup/flag-parsing/argument
// validation (an unknown subcommand, a wrong number of positional
// arguments, an invalid flag value, ...).
//
// This distinction is what lets decideExitCode (task 14, F-11) tell apart
// "CLI parameter error" (exit code 2) from "unclassified internal error"
// (exit code 1) for the many cobra-generated errors (e.g. "unknown command
// ...", "accepts N arg(s), received M") that are plain *errors.errorString
// values with no dedicated type of their own to test for. Python's
// equivalent distinction is typer/Click's dedicated `click.UsageError` /
// `typer.BadParameter` exception types, which Go's cobra has no analogue
// of.
type businessLogicCtxKey struct{}

// markBusinessLogicReached records, on cmd's Context(), that cobra reached
// cmd's business logic. It is installed as newRootCmd's PersistentPreRunE,
// which cobra invokes (for whichever subcommand was actually found) only
// after its own flag-parsing and positional-argument-count validation have
// already succeeded, and strictly before that command's own PreRunE/RunE.
func markBusinessLogicReached(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		// cobra's own ExecuteC() always populates cmd.Context() with
		// context.Background() before invoking PersistentPreRunE, so this
		// only matters for callers (e.g. unit tests) that invoke this
		// function directly against a *cobra.Command that was never run
		// through Execute()/ExecuteC().
		ctx = context.Background()
	}
	cmd.SetContext(context.WithValue(ctx, businessLogicCtxKey{}, true))
	return nil
}

// businessLogicReached reports whether markBusinessLogicReached ran for cmd,
// i.e. whether cobra's own request-shape validation (command lookup, flag
// parsing, positional argument count) had already succeeded by the time
// whatever error is being classified occurred.
func businessLogicReached(cmd *cobra.Command) bool {
	if cmd == nil || cmd.Context() == nil {
		return false
	}
	reached, _ := cmd.Context().Value(businessLogicCtxKey{}).(bool)
	return reached
}

// decideExitCode maps (cmd, err) -- as returned by rootCmd.ExecuteC() -- to
// the process exit code defined by design_GOLANG.md's exit code taxonomy
// (see also apperr.ExitCode's doc comment). It is the single source of
// truth main() and the test helper runCLI (main_test.go) both use, so that
// tests exercise exactly the logic the built binary runs.
//
// err falls into one of three buckets:
//   - nil: ExitOK.
//   - one of apperr's custom error types (FileAccessError,
//     SymbolNotFoundError, DatabaseCorruptedError), possibly wrapped: its
//     designated exit code via apperr.ExitCode, regardless of
//     businessLogicReached (an apperr error is unambiguous regardless of
//     where it originated).
//   - anything else: ExitCLIParamError (2) if cobra never reached cmd's
//     business logic (see businessLogicReached) -- this is what catches
//     cobra's own command-lookup/flag-parsing/argument-count errors, which
//     have no dedicated Go error type of their own to test for -- or
//     ExitInternalError (1) otherwise (an unexpected, truly internal bug in
//     a RunE that didn't return one of apperr's custom types).
func decideExitCode(cmd *cobra.Command, err error) int {
	if err == nil {
		return apperr.ExitOK
	}

	if code := apperr.ExitCode(err); code != apperr.ExitInternalError {
		return code
	}

	if !businessLogicReached(cmd) {
		return apperr.ExitCLIParamError
	}

	return apperr.ExitInternalError
}
