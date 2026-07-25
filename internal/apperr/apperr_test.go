package apperr

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestErrorTypesSatisfyErrorInterface verifies that each custom error type
// implements the standard error interface and produces a non-empty message.
func TestErrorTypesSatisfyErrorInterface(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "FileAccessError",
			err:  &FileAccessError{Path: "/tmp/missing.py", Err: fmt.Errorf("no such file")},
		},
		{
			name: "SymbolNotFoundError",
			err: &SymbolNotFoundError{
				FilePath:    "/tmp/example.py",
				SymbolPath:  "UserManager.regsiter_user",
				Suggestions: []string{"UserManager.register_user"},
			},
		},
		{
			name: "DatabaseCorruptedError",
			err:  &DatabaseCorruptedError{Path: ".workspace_cache/symbols.db", Err: fmt.Errorf("malformed")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Compile-time-ish check: assigning to the error interface.
			e := tt.err
			if e.Error() == "" {
				t.Errorf("Error() returned empty string for %s", tt.name)
			}
		})
	}
}

// TestSymbolNotFoundErrorIncludesSuggestions verifies that suggested symbol
// names are surfaced in the error message (F-10).
func TestSymbolNotFoundErrorIncludesSuggestions(t *testing.T) {
	err := &SymbolNotFoundError{
		FilePath:    "example.py",
		SymbolPath:  "Foo.br",
		Suggestions: []string{"Foo.bar", "Foo.baz"},
	}

	msg := err.Error()
	for _, want := range []string{"Foo.br", "Did you mean?", "Foo.bar", "Foo.baz"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Error() = %q, want it to contain %q", msg, want)
		}
	}
}

// TestSymbolNotFoundErrorNoSuggestions verifies that the "Did you mean?"
// section is omitted when there are no suggestions.
func TestSymbolNotFoundErrorNoSuggestions(t *testing.T) {
	err := &SymbolNotFoundError{
		FilePath:   "example.py",
		SymbolPath: "Foo.br",
	}

	msg := err.Error()
	if strings.Contains(msg, "Did you mean?") {
		t.Errorf("Error() = %q, want it to not contain %q", msg, "Did you mean?")
	}
}

// TestFileAccessErrorEmptyPath verifies the error message wording when Path
// is empty, both with and without an underlying error.
func TestFileAccessErrorEmptyPath(t *testing.T) {
	tests := []struct {
		name string
		err  *FileAccessError
		want string
	}{
		{
			name: "no underlying error",
			err:  &FileAccessError{},
			want: "file access error",
		},
		{
			name: "with underlying error",
			err:  &FileAccessError{Err: fmt.Errorf("no such file")},
			want: "file access error: no such file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDatabaseCorruptedErrorEmptyPath verifies the error message wording when
// Path is empty, both with and without an underlying error.
func TestDatabaseCorruptedErrorEmptyPath(t *testing.T) {
	tests := []struct {
		name string
		err  *DatabaseCorruptedError
		want string
	}{
		{
			name: "no underlying error",
			err:  &DatabaseCorruptedError{},
			want: "database corrupted",
		},
		{
			name: "with underlying error",
			err:  &DatabaseCorruptedError{Err: fmt.Errorf("malformed")},
			want: "database corrupted: malformed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestUnwrap verifies that FileAccessError and DatabaseCorruptedError expose
// their underlying error via Unwrap so that errors.Is/errors.As work.
func TestUnwrap(t *testing.T) {
	sentinel := errors.New("sentinel")

	fileErr := &FileAccessError{Path: "x", Err: sentinel}
	if !errors.Is(fileErr, sentinel) {
		t.Errorf("errors.Is(FileAccessError, sentinel) = false, want true")
	}

	dbErr := &DatabaseCorruptedError{Path: "x", Err: sentinel}
	if !errors.Is(dbErr, sentinel) {
		t.Errorf("errors.Is(DatabaseCorruptedError, sentinel) = false, want true")
	}
}

// TestExitCode verifies the exit code taxonomy mapping defined in
// design_GOLANG.md ("終了コードのタクソノミー").
func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil error", nil, ExitOK},
		{"FileAccessError", &FileAccessError{Path: "x"}, ExitFileAccessError},
		{"SymbolNotFoundError", &SymbolNotFoundError{FilePath: "x", SymbolPath: "y"}, ExitSymbolNotFoundError},
		{"DatabaseCorruptedError", &DatabaseCorruptedError{Path: "x"}, ExitDatabaseCorruptedError},
		{"unclassified error", errors.New("boom"), ExitInternalError},
		{
			name: "wrapped FileAccessError",
			err:  fmt.Errorf("context: %w", &FileAccessError{Path: "x"}),
			want: ExitFileAccessError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.err); got != tt.want {
				t.Errorf("ExitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}
