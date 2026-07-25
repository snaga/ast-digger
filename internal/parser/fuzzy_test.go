package parser

import (
	"math"
	"reflect"
	"testing"
)

// TestFuzzyRatio_MatchesPythonDifflib cross-checks fuzzyRatio against the
// exact ratios difflib.SequenceMatcher(None, a, b).ratio() produces for the
// same inputs (computed via `.venv/Scripts/python.exe`; see the shell
// transcript referenced in task 14's implementation notes). fuzzyRatio is a
// from-scratch reimplementation of the same Ratcliff/Obershelp algorithm, so
// it is expected to match to floating-point precision, not just
// approximately.
func TestFuzzyRatio_MatchesPythonDifflib(t *testing.T) {
	tests := []struct {
		a, b string
		want float64
	}{
		{"Greeter.shot", "Greeter.shout", 0.96},
		{"Greeter.shou", "Greeter.shout", 0.96},
		{"add", "Add", 0.6666666666666666},
		{"Foo.Bar", "Foo.Baz", 0.8571428571428571},
		{"completely_different", "xyz", 0.08695652173913043},
		{"", "", 1.0},
		{"a", "", 0.0},
		{"", "a", 0.0},
		{"abc", "abc", 1.0},
		{"NoSuchClass", "Greeter", 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.a+"__"+tt.b, func(t *testing.T) {
			got := fuzzyRatio(tt.a, tt.b)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("fuzzyRatio(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// TestClosestSymbolMatches_MatchesPythonDifflib cross-checks
// closestSymbolMatches against
// difflib.get_close_matches(target, possibilities, n=5, cutoff=0.6),
// computed the same way as above.
func TestClosestSymbolMatches_MatchesPythonDifflib(t *testing.T) {
	possibilities := []string{"Greeter", "Greeter.shout", "Greeter.name", "add", "Adder.add"}

	tests := []struct {
		name   string
		target string
		want   []string
	}{
		{
			name:   "close match ranks by similarity, most similar first",
			target: "Greeter.shot",
			want:   []string{"Greeter.shout", "Greeter", "Greeter.name"},
		},
		{
			name:   "no candidate above cutoff yields nil/empty",
			target: "NoSuchClass",
			want:   nil,
		},
		{
			name:   "single close match",
			target: "ad",
			want:   []string{"add"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := closestSymbolMatches(tt.target, possibilities)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("closestSymbolMatches(%q, ...) = %v, want %v", tt.target, got, tt.want)
			}
		})
	}
}

// TestClosestSymbolMatches_LimitsToFive verifies the n=5 cap is applied even
// when more than 5 candidates clear the cutoff.
func TestClosestSymbolMatches_LimitsToFive(t *testing.T) {
	candidates := []string{
		"target1", "target2", "target3", "target4", "target5", "target6",
	}

	got := closestSymbolMatches("target", candidates)
	if len(got) != fuzzyMatchLimit {
		t.Fatalf("closestSymbolMatches returned %d matches, want %d (n=5 cap)", len(got), fuzzyMatchLimit)
	}
}

// TestSuggestSymbols_SwallowsParseError verifies that suggestSymbols returns
// no suggestions (rather than propagating an error) when parseFile fails,
// mirroring Python's `except Exception: return ""` in
// BaseParser.suggest_symbols: candidate generation is best-effort and must
// never mask the caller's original SymbolNotFoundError.
func TestSuggestSymbols_SwallowsParseError(t *testing.T) {
	failingParse := func(string) ([]SymbolData, error) {
		return nil, errFuzzyTestParse
	}

	got := suggestSymbols(failingParse, "irrelevant.py", "Whatever")
	if got != nil {
		t.Errorf("suggestSymbols with a failing parseFile = %v, want nil", got)
	}
}

var errFuzzyTestParse = &testParseError{}

type testParseError struct{}

func (e *testParseError) Error() string { return "simulated parse failure" }
