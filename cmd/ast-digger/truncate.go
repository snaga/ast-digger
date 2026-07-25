package main

import (
	"fmt"
	"strings"
)

// truncationLimit is the default line-count threshold above which outline
// and symbol command output is truncated to protect the caller's context
// window, matching Python's `truncate_text(text, limit=300)` default in
// ast_digger/__main__.py.
const truncationLimit = 300

// pySplitLines splits text into lines the way Python's str.splitlines()
// does for the line separators expected in this package's inputs (source
// code, skeletons, and symbol text): "\n", "\r\n", and "\r". Unlike
// strings.Split(text, "\n"), it does not produce a trailing empty element
// when text ends with a line separator, and it does not leave a stray "\r"
// on lines split from "\r\n"-terminated input. An empty text returns an
// empty (non-nil) slice, matching Python's "".splitlines() == [].
func pySplitLines(text string) []string {
	if text == "" {
		return []string{}
	}

	var lines []string
	start := 0
	i := 0
	for i < len(text) {
		c := text[i]
		switch c {
		case '\n':
			lines = append(lines, text[start:i])
			i++
			start = i
		case '\r':
			lines = append(lines, text[start:i])
			if i+1 < len(text) && text[i+1] == '\n' {
				i += 2
			} else {
				i++
			}
			start = i
		default:
			i++
		}
	}
	if start < len(text) {
		lines = append(lines, text[start:])
	}
	return lines
}

// truncateText returns text unchanged (and false) when it has at most limit
// lines. Otherwise it keeps text's first 100 and last 100 lines, replacing
// everything in between with a two-line marker noting how many lines were
// omitted and how to opt out (--no-truncate), and returns true.
//
// It corresponds to Python's `truncate_text` helper in
// ast_digger/__main__.py, splitting lines with pySplitLines to match
// Python's str.splitlines() semantics (no trailing empty element for
// input ending in a line separator; "\r\n"/"\r" normalized like "\n").
func truncateText(text string, limit int) (string, bool) {
	lines := pySplitLines(text)
	if len(lines) <= limit {
		return text, false
	}

	truncatedCount := len(lines) - 200
	topLines := lines[:100]
	bottomLines := lines[len(lines)-100:]

	middle := []string{
		fmt.Sprintf("    # === TRUNCATED (%d lines omitted) ===", truncatedCount),
		"    # === Use --no-truncate to print full output ===",
	}

	result := make([]string, 0, len(topLines)+len(middle)+len(bottomLines))
	result = append(result, topLines...)
	result = append(result, middle...)
	result = append(result, bottomLines...)

	return strings.Join(result, "\n"), true
}
