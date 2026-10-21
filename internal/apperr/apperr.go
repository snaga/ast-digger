// Package apperr defines AST-Digger's custom error types and the exit code
// taxonomy used to translate errors into process exit codes.
//
// It corresponds to ast_digger/error.py in the Python implementation, with
// the addition of an exit code mapping (design_GOLANG.md, "エラーハンドリング"
// section) since Go does not have a shared exception hierarchy that a CLI
// entry point can inspect the way Python's does.
package apperr

import (
	"errors"
	"fmt"
)

// Exit codes for the CLI, per the exit code taxonomy defined in
// design_GOLANG.md ("終了コードのタクソノミー").
const (
	// ExitOK indicates successful completion.
	ExitOK = 0
	// ExitInternalError indicates an unclassified internal error.
	ExitInternalError = 1
	// ExitCLIParamError indicates invalid CLI parameters/flags.
	ExitCLIParamError = 2
	// ExitFileAccessError indicates a file access error (see FileAccessError).
	ExitFileAccessError = 3
	// ExitSymbolNotFoundError indicates a symbol lookup failure (see
	// SymbolNotFoundError).
	ExitSymbolNotFoundError = 4
	// ExitDatabaseCorruptedError indicates a database corruption/schema
	// mismatch that persisted after automatic recovery (see
	// DatabaseCorruptedError).
	ExitDatabaseCorruptedError = 5
)

// FileAccessError indicates that a target file could not be accessed, read,
// or was of an unsupported type. It corresponds to Python's
// ast_digger.error.FileAccessError.
type FileAccessError struct {
	// Path is the file path that could not be accessed, if known.
	Path string
	// Err is the underlying error, if any (e.g. from the os package).
	Err error
}

func (e *FileAccessError) Error() string {
	if e.Path != "" {
		if e.Err != nil {
			return fmt.Sprintf("file access error: %s: %v", e.Path, e.Err)
		}
		return fmt.Sprintf("file access error: %s", e.Path)
	}
	if e.Err != nil {
		return fmt.Sprintf("file access error: %v", e.Err)
	}
	return "file access error"
}

// Unwrap allows errors.Is / errors.As to see through to the underlying error.
func (e *FileAccessError) Unwrap() error {
	return e.Err
}

// SymbolNotFoundError indicates that a requested symbol path could not be
// found within a file. It corresponds to Python's
// ast_digger.error.SymbolNotFoundError.
type SymbolNotFoundError struct {
	// FilePath is the file that was searched.
	FilePath string
	// SymbolPath is the symbol path that could not be resolved.
	SymbolPath string
	// Suggestions holds candidate symbol names (up to 5) that exist in the
	// file, for inclusion in the error message (F-10, task 14).
	Suggestions []string
}

func (e *SymbolNotFoundError) Error() string {
	msg := fmt.Sprintf("symbol not found: %q in %s", e.SymbolPath, e.FilePath)
	if len(e.Suggestions) > 0 {
		msg += "\nDid you mean?"
		for _, s := range e.Suggestions {
			msg += fmt.Sprintf("\n  - %s", s)
		}
	}
	return msg
}

// DatabaseCorruptedError indicates that the SQLite cache database was
// corrupted or had a mismatched schema. It corresponds to Python's
// ast_digger.error.DatabaseCorruptedError.
type DatabaseCorruptedError struct {
	// Path is the database file path, if known.
	Path string
	// Err is the underlying error, if any.
	Err error
}

func (e *DatabaseCorruptedError) Error() string {
	if e.Path != "" {
		if e.Err != nil {
			return fmt.Sprintf("database corrupted: %s: %v", e.Path, e.Err)
		}
		return fmt.Sprintf("database corrupted: %s", e.Path)
	}
	if e.Err != nil {
		return fmt.Sprintf("database corrupted: %v", e.Err)
	}
	return "database corrupted"
}

// Unwrap allows errors.Is / errors.As to see through to the underlying error.
func (e *DatabaseCorruptedError) Unwrap() error {
	return e.Err
}

// GuardrailError indicates that a change impact guardrail check failed
// (e.g. statement threshold exceeded or required docstring missing).
type GuardrailError struct {
	Message string
}

func (e *GuardrailError) Error() string {
	return e.Message
}

// ExitCode maps err to the process exit code defined by the taxonomy in
// design_GOLANG.md. A nil err maps to ExitOK. Errors that are not one of the
// custom types defined in this package map to ExitInternalError; CLI
// parameter validation errors (ExitCLIParamError) are expected to be
// detected and reported directly by the CLI layer (cobra flag validation)
// or via GuardrailError.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}

	var guardrailErr *GuardrailError
	if errors.As(err, &guardrailErr) {
		return ExitCLIParamError
	}

	var fileErr *FileAccessError
	if errors.As(err, &fileErr) {
		return ExitFileAccessError
	}

	var symbolErr *SymbolNotFoundError
	if errors.As(err, &symbolErr) {
		return ExitSymbolNotFoundError
	}

	var dbErr *DatabaseCorruptedError
	if errors.As(err, &dbErr) {
		return ExitDatabaseCorruptedError
	}

	return ExitInternalError
}
