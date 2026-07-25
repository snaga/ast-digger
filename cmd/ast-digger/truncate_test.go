package main

import (
	"strconv"
	"strings"
	"testing"
)

// numberedLines returns a "\n"-joined string of n lines, each reading
// "lineK" (1-indexed), for use as synthetic truncateText input.
func numberedLines(n int) string {
	lines := make([]string, n)
	for i := 0; i < n; i++ {
		lines[i] = "line" + strconv.Itoa(i+1)
	}
	return strings.Join(lines, "\n")
}

func TestTruncateText_UnderLimit(t *testing.T) {
	text := numberedLines(300)

	got, truncated := truncateText(text, 300)

	if truncated {
		t.Errorf("truncated = true, want false for exactly %d lines (limit)", 300)
	}
	if got != text {
		t.Errorf("got = %q, want the original text unchanged", got)
	}
}

func TestTruncateText_Small(t *testing.T) {
	text := numberedLines(10)

	got, truncated := truncateText(text, 300)

	if truncated {
		t.Errorf("truncated = true, want false for a 10-line input")
	}
	if got != text {
		t.Errorf("got = %q, want the original text unchanged", got)
	}
}

func TestTruncateText_OverLimit(t *testing.T) {
	text := numberedLines(350)

	got, truncated := truncateText(text, 300)

	if !truncated {
		t.Fatalf("truncated = false, want true for a 350-line input over the 300-line limit")
	}

	lines := strings.Split(got, "\n")
	// 100 top + 2 marker + 100 bottom.
	if want := 202; len(lines) != want {
		t.Fatalf("len(lines) = %d, want %d", len(lines), want)
	}

	if lines[0] != "line1" {
		t.Errorf("lines[0] = %q, want %q", lines[0], "line1")
	}
	if lines[99] != "line100" {
		t.Errorf("lines[99] = %q, want %q", lines[99], "line100")
	}
	if lines[201] != "line350" {
		t.Errorf("lines[201] = %q, want %q", lines[201], "line350")
	}
	if lines[102] != "line251" {
		t.Errorf("lines[102] = %q, want %q", lines[102], "line251")
	}

	if !strings.Contains(got, "# === TRUNCATED (150 lines omitted) ===") {
		t.Errorf("got does not contain the expected truncation marker (150 lines omitted):\n%s", got)
	}
	if !strings.Contains(got, "# === Use --no-truncate to print full output ===") {
		t.Errorf("got does not contain the expected --no-truncate hint:\n%s", got)
	}

	// The omitted middle content must not survive truncation.
	if strings.Contains(got, "line200") {
		t.Errorf("got contains %q, which should have been truncated away:\n%s", "line200", got)
	}
}

// TestTruncateText_TrailingNewline verifies that a text ending in "\n"
// (as outline's generated skeletons typically do) does not lose a real
// line or gain a synthetic trailing empty line at truncation, and that
// the omitted-line count in the marker matches Python's
// text.splitlines() behavior (no phantom trailing empty line counted).
func TestTruncateText_TrailingNewline(t *testing.T) {
	text := numberedLines(350) + "\n"

	got, truncated := truncateText(text, 300)

	if !truncated {
		t.Fatalf("truncated = false, want true for a 350-line input over the 300-line limit")
	}

	lines := strings.Split(got, "\n")
	// 100 top + 2 marker + 100 bottom.
	if want := 202; len(lines) != want {
		t.Fatalf("len(lines) = %d, want %d", want, len(lines))
	}

	if lines[0] != "line1" {
		t.Errorf("lines[0] = %q, want %q", lines[0], "line1")
	}
	if lines[99] != "line100" {
		t.Errorf("lines[99] = %q, want %q", lines[99], "line100")
	}
	// The last real line must be "line350", not a synthetic empty line
	// produced by splitting on the trailing "\n".
	if lines[201] != "line350" {
		t.Errorf("lines[201] = %q, want %q (no data should be dropped, no phantom empty line)", lines[201], "line350")
	}
	if lines[102] != "line251" {
		t.Errorf("lines[102] = %q, want %q", lines[102], "line251")
	}

	if !strings.Contains(got, "# === TRUNCATED (150 lines omitted) ===") {
		t.Errorf("got does not contain the expected truncation marker (150 lines omitted):\n%s", got)
	}
}

// TestTruncateText_CRLF verifies that CRLF-terminated input is normalized
// to "\n"-separated output with no stray "\r" characters, matching
// Python's str.splitlines() + "\n".join(...) behavior.
func TestTruncateText_CRLF(t *testing.T) {
	text := strings.ReplaceAll(numberedLines(350), "\n", "\r\n")

	got, truncated := truncateText(text, 300)

	if !truncated {
		t.Fatalf("truncated = false, want true for a 350-line CRLF input over the 300-line limit")
	}
	if strings.Contains(got, "\r") {
		t.Errorf("got contains %q, want CRLF normalized away entirely:\n%q", "\\r", got)
	}

	lines := strings.Split(got, "\n")
	if want := 202; len(lines) != want {
		t.Fatalf("len(lines) = %d, want %d", want, len(lines))
	}
	if lines[0] != "line1" {
		t.Errorf("lines[0] = %q, want %q", lines[0], "line1")
	}
	if lines[201] != "line350" {
		t.Errorf("lines[201] = %q, want %q", lines[201], "line350")
	}
}

func TestPySplitLines_Empty(t *testing.T) {
	got := pySplitLines("")
	if len(got) != 0 {
		t.Errorf("pySplitLines(%q) = %#v, want an empty slice", "", got)
	}
}

func TestPySplitLines_NoNewline(t *testing.T) {
	got := pySplitLines("single line")
	want := []string{"single line"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("pySplitLines(%q) = %#v, want %#v", "single line", got, want)
	}
}

func TestPySplitLines_TrailingNewline(t *testing.T) {
	got := pySplitLines("a\nb\n")
	want := []string{"a", "b"}
	if len(got) != len(want) {
		t.Fatalf("pySplitLines(%q) = %#v, want %#v", "a\\nb\\n", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pySplitLines(%q)[%d] = %q, want %q", "a\\nb\\n", i, got[i], want[i])
		}
	}
}

func TestPySplitLines_MixedLineEndings(t *testing.T) {
	got := pySplitLines("a\r\nb\rc\nd")
	want := []string{"a", "b", "c", "d"}
	if len(got) != len(want) {
		t.Fatalf("pySplitLines(...) = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pySplitLines(...)[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
