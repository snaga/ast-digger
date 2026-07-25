package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/snaga/ast-digger/internal/parser"
)

// marshalIndentNoEscape JSON-encodes v with 2-space indentation, matching
// Python's json.dumps(v, ensure_ascii=False, indent=2). Unlike
// json.MarshalIndent, HTML-unsafe characters (<, >, &) are left unescaped:
// Go's encoding/json escapes them by default (a browser-embedding safety
// measure that is irrelevant for a CLI's stdout/stderr), which would
// otherwise turn e.g. a Go type signature's "->"/"<T>" or a Python return
// annotation's "->" into ">" and diverge from Python's output.
func marshalIndentNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// json.Encoder.Encode appends a trailing newline; trim it so callers
	// control line-ending behavior the same way json.MarshalIndent callers
	// would.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// outputResult writes result to w, either as pretty-printed (2-space
// indent) JSON when jsonOut is true, or as plain text otherwise. It
// corresponds to Python's `output_result` helper in ast_digger/__main__.py.
//
// Every command in this package already reduces its result to either a
// plain string (already formatted for text display, e.g. "file:10-20"), a
// []parser.ReferenceData (the `references` command's text-mode result), or
// a JSON-marshalable struct/map/slice (e.g. a parser.SymbolData or a
// map[string]any command-specific result) before calling outputResult, so
// unlike Python's dynamic dispatch (which inspects dataclasses/lists/dicts
// at runtime), this only needs to special-case plain strings and
// []parser.ReferenceData: Python's `output_result` wraps a bare string
// result as {"result": "..."} in JSON mode (see the
// `else: out_str = json.dumps({"result": str(result)})` branch), since a
// bare JSON string wouldn't round-trip as a JSON object the way a
// dict/dataclass/list result does; and in text mode it renders a list of
// items exposing a `snippet` attribute (i.e. ReferenceData) as
// "{file_path}:{line_number}: {snippet}" lines (see the
// `elif isinstance(result, list): ... if hasattr(item, "snippet")` branch).
func outputResult(w io.Writer, result any, jsonOut bool) error {
	if jsonOut {
		toMarshal := result
		if s, ok := result.(string); ok {
			toMarshal = map[string]string{"result": s}
		}
		data, err := marshalIndentNoEscape(toMarshal)
		if err != nil {
			return fmt.Errorf("failed to marshal result as JSON: %w", err)
		}
		_, err = fmt.Fprintln(w, string(data))
		return err
	}

	if s, ok := result.(string); ok {
		_, err := fmt.Fprintln(w, s)
		return err
	}

	if refs, ok := result.([]parser.ReferenceData); ok {
		lines := make([]string, len(refs))
		for i, r := range refs {
			lines[i] = fmt.Sprintf("%s:%d: %s", r.FilePath, r.LineNumber, r.LineContent)
		}
		_, err := fmt.Fprintln(w, strings.Join(lines, "\n"))
		return err
	}

	_, err := fmt.Fprintln(w, result)
	return err
}

// outputError writes err's message to w, formatted the same way
// outputResult would render a {"error": "..."} result: pretty-printed JSON
// when jsonOut is true, or "Error: <message>" as plain text otherwise. It
// corresponds to Python's `output_result({"error": ...}, is_error=True)`
// calls made from `handle_exception` in ast_digger/__main__.py.
func outputError(w io.Writer, err error, jsonOut bool) {
	if jsonOut {
		data, mErr := marshalIndentNoEscape(map[string]string{"error": err.Error()})
		if mErr == nil {
			_, _ = fmt.Fprintln(w, string(data))
			return
		}
	}
	_, _ = fmt.Fprintf(w, "Error: %s\n", err.Error())
}
