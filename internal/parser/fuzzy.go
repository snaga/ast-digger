// Package parser: fuzzy.go implements the "fuzzy match" logic shared by
// PythonParser, TypeScriptParser, and GolangParser's ExtractSymbol, used to
// populate apperr.SymbolNotFoundError.Suggestions with up-to-5 candidate
// symbol names when a requested symbol_path cannot be found (F-10, task 14).
//
// It corresponds to Python's BaseParser.suggest_symbols
// (ast_digger/parsers/base.py), which delegates to the standard library's
// difflib.get_close_matches (cutoff=0.6, n=5). difflib.get_close_matches
// ranks candidates by difflib.SequenceMatcher.ratio(), which implements the
// Ratcliff/Obershelp algorithm: 2*M/T, where T is the combined length of the
// two strings and M is the total length of a recursively-found sequence of
// matching (non-overlapping, longest-first) contiguous blocks.
//
// fuzzyRatio below reimplements that same ratio (not merely an "equivalent"
// similarity measure): findLongestMatch is a direct, from-scratch port of
// the core loop in CPython's difflib.SequenceMatcher.find_longest_match
// (the "j2len" dynamic-programming sweep), and sumMatchingBlockSizes
// mirrors difflib's recursive decomposition around the longest match to
// accumulate the same total M. difflib's "autojunk" heuristic (which only
// engages for sequences of 200+ elements, e.g. very long lines, and treats
// elements appearing in >1% of a long sequence as noise) is not implemented
// here, since the only inputs this file ever compares are short symbol
// names/paths (e.g. "MyClass.my_method"), for which autojunk categorically
// never activates in CPython either.
package parser

// fuzzyRatio returns a similarity ratio between a and b in [0.0, 1.0],
// computed the same way as Python's
// difflib.SequenceMatcher(None, a, b).ratio(): 2*M/T, where T is the
// combined rune length of a and b, and M is the number of matching runes
// found by recursively locating the longest common contiguous run and then
// recursing on the (disjoint) runs to either side of it. Two empty strings
// are considered identical (ratio 1.0), matching difflib's convention.
func fuzzyRatio(a, b string) float64 {
	ar := []rune(a)
	br := []rune(b)

	total := len(ar) + len(br)
	if total == 0 {
		return 1.0
	}

	b2j := buildB2J(br)
	matches := sumMatchingBlockSizes(ar, br, 0, len(ar), 0, len(br), b2j)
	return 2.0 * float64(matches) / float64(total)
}

// buildB2J indexes b: for each distinct rune in b, the ascending list of
// positions at which it occurs. It corresponds to the "b2j" map difflib's
// SequenceMatcher precomputes once per (a, b) comparison (via __chain_b) and
// reuses across every find_longest_match call for that pair.
func buildB2J(b []rune) map[rune][]int {
	b2j := make(map[rune][]int, len(b))
	for j, r := range b {
		b2j[r] = append(b2j[r], j)
	}
	return b2j
}

// findLongestMatch locates the longest contiguous run common to a[alo:ahi]
// and b[blo:bhi], returning its start offset in each and its length (0 if
// there is no overlap at all). Ties are broken the same way CPython's
// difflib does: the earliest-starting match in a, and, among those, the
// earliest-starting match in b.
//
// This is a direct port of the "j2len" dynamic-programming sweep at the
// heart of CPython's difflib.SequenceMatcher.find_longest_match (the
// non-autojunk code path): for each position i in a's range, j2len[j] holds
// the length of the run ending at (i-1, j) that is being extended by
// matching a[i] against b[j].
func findLongestMatch(a, b []rune, alo, ahi, blo, bhi int, b2j map[rune][]int) (besti, bestj, bestsize int) {
	besti, bestj, bestsize = alo, blo, 0

	j2len := make(map[int]int)
	for i := alo; i < ahi; i++ {
		newj2len := make(map[int]int)
		for _, j := range b2j[a[i]] {
			if j < blo {
				continue
			}
			if j >= bhi {
				break
			}
			k := j2len[j-1] + 1
			newj2len[j] = k
			if k > bestsize {
				besti, bestj, bestsize = i-k+1, j-k+1, k
			}
		}
		j2len = newj2len
	}

	return besti, bestj, bestsize
}

// sumMatchingBlockSizes returns the combined length of every matching block
// difflib's get_matching_blocks would find between a[alo:ahi] and
// b[blo:bhi]: it finds the single longest match in that range, then
// recurses on the (disjoint) sub-ranges strictly to its left and strictly
// to its right, summing sizes throughout. This mirrors the queue-driven
// recursive decomposition in CPython's
// difflib.SequenceMatcher.get_matching_blocks.
func sumMatchingBlockSizes(a, b []rune, alo, ahi, blo, bhi int, b2j map[rune][]int) int {
	if alo >= ahi || blo >= bhi {
		return 0
	}

	i, j, size := findLongestMatch(a, b, alo, ahi, blo, bhi, b2j)
	if size == 0 {
		return 0
	}

	return size +
		sumMatchingBlockSizes(a, b, alo, i, blo, j, b2j) +
		sumMatchingBlockSizes(a, b, i+size, ahi, j+size, bhi, b2j)
}

// fuzzyMatchCutoff and fuzzyMatchLimit mirror the cutoff=0.6, n=5 defaults
// Python's suggest_symbols passes to difflib.get_close_matches.
const (
	fuzzyMatchCutoff = 0.6
	fuzzyMatchLimit  = 5
)

// closestSymbolMatches returns up to fuzzyMatchLimit (5) entries from
// candidates whose fuzzyRatio against target is at least fuzzyMatchCutoff
// (0.6), ordered by descending ratio (ties broken by candidates' original
// relative order, for deterministic output). It corresponds to Python's
// `difflib.get_close_matches(symbol_path, possibilities, n=5, cutoff=0.6)`
// call in BaseParser.suggest_symbols. A nil/empty result means no
// sufficiently similar candidate was found, matching Python's
// "(No similar symbols found in this file)" case (see
// apperr.SymbolNotFoundError.Error(), which renders an empty Suggestions
// slice as no "Did you mean?" section at all).
func closestSymbolMatches(target string, candidates []string) []string {
	type scored struct {
		name  string
		ratio float64
	}

	matches := make([]scored, 0, len(candidates))
	for _, c := range candidates {
		if r := fuzzyRatio(target, c); r >= fuzzyMatchCutoff {
			matches = append(matches, scored{name: c, ratio: r})
		}
	}

	// Stable sort: candidates with equal ratios keep their original
	// relative order, since matches was built by iterating candidates in
	// order.
	for i := 1; i < len(matches); i++ {
		for j := i; j > 0 && matches[j].ratio > matches[j-1].ratio; j-- {
			matches[j], matches[j-1] = matches[j-1], matches[j]
		}
	}

	if len(matches) > fuzzyMatchLimit {
		matches = matches[:fuzzyMatchLimit]
	}

	result := make([]string, len(matches))
	for i, m := range matches {
		result[i] = m.name
	}
	return result
}

// symbolPossibilities converts symbols (as returned by a Parser's ParseFile)
// into the flat "Parent.Name" / "Name" candidate strings that
// closestSymbolMatches compares symbolPath against. It corresponds to the
// `possibilities` list built in Python's BaseParser.suggest_symbols.
func symbolPossibilities(symbols []SymbolData) []string {
	possibilities := make([]string, len(symbols))
	for i, s := range symbols {
		if s.Parent != nil {
			possibilities[i] = *s.Parent + "." + s.Name
		} else {
			possibilities[i] = s.Name
		}
	}
	return possibilities
}

// suggestSymbols re-parses filePath (via parseFile, normally a Parser's own
// ParseFile method) and returns up to 5 existing symbol names/paths that
// closely match symbolPath, for use as apperr.SymbolNotFoundError.
// Suggestions. Any error from parseFile is swallowed and reported as "no
// suggestions" (an empty, nil slice), matching Python's
// `except Exception: return ""` in BaseParser.suggest_symbols: candidate
// generation is a best-effort UX nicety, not something that should ever mask
// or replace the original SymbolNotFoundError.
func suggestSymbols(parseFile func(string) ([]SymbolData, error), filePath, symbolPath string) []string {
	symbols, err := parseFile(filePath)
	if err != nil {
		return nil
	}
	return closestSymbolMatches(symbolPath, symbolPossibilities(symbols))
}
