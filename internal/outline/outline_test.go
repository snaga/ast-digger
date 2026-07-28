package outline

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/snaga/ast-digger/internal/apperr"
	"github.com/snaga/ast-digger/internal/db"
	"github.com/snaga/ast-digger/internal/parser"
)

const (
	sampleFile    = "../../testdata/python/sample.py"
	referencesDir = "../../testdata/python/references"

	// mixedReferencesDir holds a small Python+TypeScript+Go+Java+JavaScript
	// fixture used to verify that FindReferences dispatches to the correct
	// language-specific parser.Parser for each file extension it encounters
	// during a single project-wide scan (task 10: multi-language dispatch;
	// extended by task 22 to cover Java/JavaScript).
	mixedReferencesDir = "../../testdata/mixed/references"

	// importProjDir holds the same relative/absolute import resolution
	// fixture used directly by parser.TestPythonParser_ResolveImports_*,
	// reused here to test Outliner.ResolveImports' thin delegation.
	importProjDir = "../../testdata/python/importproj"
)

// chdirTest changes the process working directory to dir for the duration
// of t, restoring the original working directory via t.Cleanup. BuildDirectoryTree/
// WriteFeedbackLog/FindReferences resolve paths relative to the process's
// working directory ("project root"), mirroring Python's
// pathlib.Path.cwd()-based Outliner.
func chdirTest(t *testing.T, dir string) {
	t.Helper()

	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("failed to resolve %q: %v", dir, err)
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	if err := os.Chdir(abs); err != nil {
		t.Fatalf("failed to chdir to %q: %v", abs, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("failed to restore working directory to %q: %v", orig, err)
		}
	})
}

// writeFile writes content to path (creating parent directories as
// needed), failing the test on any error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create parent dir for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write %q: %v", path, err)
	}
}

// TestOutliner_GenerateSkeleton verifies that Outliner.GenerateSkeleton
// delegates to the resolved parser.Parser's GenerateSkeleton (here,
// PythonParser's), rather than duplicating its body-replacement logic. Full
// coverage of the skeleton-generation behavior itself (docstring
// preservation, decorator preservation, nested-definition handling, ...)
// lives in parser.TestPythonParser_GenerateSkeleton; this test only checks
// that Outliner's thin wrapper produces the same result PythonParser would
// produce directly.
func TestOutliner_GenerateSkeleton(t *testing.T) {
	o := NewOutliner()
	got, err := o.GenerateSkeleton(sampleFile)
	if err != nil {
		t.Fatalf("GenerateSkeleton returned unexpected error: %v", err)
	}

	want, err := parser.NewPythonParser().GenerateSkeleton(sampleFile)
	if err != nil {
		t.Fatalf("PythonParser.GenerateSkeleton returned unexpected error: %v", err)
	}

	if got != want {
		t.Errorf("GenerateSkeleton =\n%s\nwant\n%s", got, want)
	}
	if !strings.Contains(got, "...") {
		t.Errorf("GenerateSkeleton output does not contain a replaced function body (\"...\"):\n%s", got)
	}
}

func TestOutliner_GenerateSkeleton_UnsupportedExtension(t *testing.T) {
	o := NewOutliner()
	_, err := o.GenerateSkeleton("something.rb")
	if err == nil {
		t.Fatal("expected an error for an unsupported extension, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestOutliner_GenerateSkeleton_MultiLanguage verifies that
// Outliner.GenerateSkeleton dispatches (via getParser/extToLang) to the
// correct language-specific parser.Parser for every supported extension --
// including Java/JavaScript (task 22) alongside the previously-covered
// Python/TypeScript/Go -- by checking that its output matches calling that
// parser directly, mirroring TestOutliner_GenerateSkeleton's Python-only
// check for each language in turn.
func TestOutliner_GenerateSkeleton_MultiLanguage(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		parser   parser.Parser
	}{
		{name: "python", filePath: "../../testdata/python/sample.py", parser: parser.NewPythonParser()},
		{name: "typescript", filePath: "../../testdata/typescript/sample.ts", parser: parser.NewTypeScriptParser()},
		{name: "go", filePath: "../../testdata/go/sample.go", parser: parser.NewGolangParser()},
		{name: "java", filePath: "../../testdata/java/sample.java", parser: parser.NewJavaParser()},
		{name: "javascript", filePath: "../../testdata/javascript/sample.js", parser: parser.NewJavaScriptParser()},
		{name: "rust", filePath: "../../testdata/rust/sample.rs", parser: parser.NewRustParser()},
		{name: "markdown", filePath: "../../testdata/markdown/sample.md", parser: parser.NewMarkdownParser()},
	}

	o := NewOutliner()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := o.GenerateSkeleton(tt.filePath)
			if err != nil {
				t.Fatalf("GenerateSkeleton(%q) returned unexpected error: %v", tt.filePath, err)
			}

			want, err := tt.parser.GenerateSkeleton(tt.filePath)
			if err != nil {
				t.Fatalf("direct parser GenerateSkeleton(%q) returned unexpected error: %v", tt.filePath, err)
			}

			if got != want {
				t.Errorf("GenerateSkeleton(%q) =\n%s\nwant\n%s", tt.filePath, got, want)
			}
		})
	}
}

// TestOutliner_ListSymbols verifies that Outliner.ListSymbols delegates to
// the resolved parser.Parser's ParseFile (here, PythonParser's), matching
// its result exactly, and that the returned symbols include both the
// module-level and nested definitions of testdata/python/sample.py.
func TestOutliner_ListSymbols(t *testing.T) {
	o := NewOutliner()
	got, err := o.ListSymbols(sampleFile)
	if err != nil {
		t.Fatalf("ListSymbols returned unexpected error: %v", err)
	}

	want, err := parser.NewPythonParser().ParseFile(sampleFile)
	if err != nil {
		t.Fatalf("PythonParser.ParseFile returned unexpected error: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("ListSymbols returned %d symbols, want %d (from PythonParser.ParseFile directly): got=%+v want=%+v", len(got), len(want), got, want)
	}

	var haveGreeter, haveTopLevel bool
	for _, s := range got {
		switch s.Name {
		case "Greeter":
			haveGreeter = true
		case "top_level_function":
			haveTopLevel = true
		}
	}
	if !haveGreeter || !haveTopLevel {
		t.Errorf("expected ListSymbols to include Greeter and top_level_function, got %+v", got)
	}
}

func TestOutliner_ListSymbols_UnsupportedExtension(t *testing.T) {
	o := NewOutliner()
	_, err := o.ListSymbols("something.rb")
	if err == nil {
		t.Fatal("expected an error for an unsupported extension, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestOutliner_ExtractSymbol verifies that Outliner.ExtractSymbol delegates
// to the resolved parser.Parser's ExtractSymbol, returning the full
// SymbolData (including source code) for a nested class in
// testdata/python/sample.py.
func TestOutliner_ExtractSymbol(t *testing.T) {
	o := NewOutliner()
	got, err := o.ExtractSymbol(sampleFile, "Greeter")
	if err != nil {
		t.Fatalf("ExtractSymbol returned unexpected error: %v", err)
	}

	want, err := parser.NewPythonParser().ExtractSymbol(sampleFile, "Greeter")
	if err != nil {
		t.Fatalf("PythonParser.ExtractSymbol returned unexpected error: %v", err)
	}
	if got.Code == nil || want.Code == nil {
		t.Fatalf("expected both Code pointers to be non-nil: got=%v want=%v", got.Code, want.Code)
	}
	if got.Name != want.Name || got.Kind != want.Kind || *got.Code != *want.Code {
		t.Errorf("ExtractSymbol = %+v (Code=%q), want %+v (Code=%q)", got, *got.Code, want, *want.Code)
	}
	if got.Kind != "class" {
		t.Errorf("Kind = %q, want %q", got.Kind, "class")
	}
	if !strings.Contains(*got.Code, "class Greeter:") {
		t.Errorf("Code = %q, want it to contain %q", *got.Code, "class Greeter:")
	}
}

func TestOutliner_ExtractSymbol_NotFound(t *testing.T) {
	o := NewOutliner()
	_, err := o.ExtractSymbol(sampleFile, "NoSuchClass")
	if err == nil {
		t.Fatal("expected an error for an unknown symbol, got nil")
	}
	var symErr *apperr.SymbolNotFoundError
	if !errors.As(err, &symErr) {
		t.Errorf("expected *apperr.SymbolNotFoundError, got %T: %v", err, err)
	}
}

func TestOutliner_ExtractSymbol_UnsupportedExtension(t *testing.T) {
	o := NewOutliner()
	_, err := o.ExtractSymbol("something.rb", "X")
	if err == nil {
		t.Fatal("expected an error for an unsupported extension, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestOutliner_ResolveImports verifies that Outliner.ResolveImports
// delegates to the resolved parser.Parser's ResolveImports (here,
// PythonParser's relative-import resolution, which does not depend on the
// process's working directory), matching its result exactly.
func TestOutliner_ResolveImports(t *testing.T) {
	o := NewOutliner()
	callerFile := importProjDir + "/subpkg/caller.py"

	gotPath, gotLine, err := o.ResolveImports(callerFile, "util_func")
	if err != nil {
		t.Fatalf("ResolveImports returned unexpected error: %v", err)
	}

	wantPath, wantLine, err := parser.NewPythonParser().ResolveImports(callerFile, "util_func")
	if err != nil {
		t.Fatalf("PythonParser.ResolveImports returned unexpected error: %v", err)
	}
	if gotPath != wantPath || gotLine != wantLine {
		t.Errorf("ResolveImports = (%q, %d), want (%q, %d)", gotPath, gotLine, wantPath, wantLine)
	}
	if !strings.HasSuffix(filepath.ToSlash(gotPath), "testdata/python/importproj/subpkg/utils.py") {
		t.Errorf("path = %q, want suffix %q", gotPath, "testdata/python/importproj/subpkg/utils.py")
	}
}

func TestOutliner_ResolveImports_SymbolNotImported(t *testing.T) {
	o := NewOutliner()
	_, _, err := o.ResolveImports(importProjDir+"/main.py", "totally_absent_symbol")
	if err == nil {
		t.Fatal("expected an error for a symbol that is not imported, got nil")
	}
	var symErr *apperr.SymbolNotFoundError
	if !errors.As(err, &symErr) {
		t.Errorf("expected *apperr.SymbolNotFoundError, got %T: %v", err, err)
	}
}

func TestOutliner_ResolveImports_UnsupportedExtension(t *testing.T) {
	o := NewOutliner()
	_, _, err := o.ResolveImports("something.rb", "X")
	if err == nil {
		t.Fatal("expected an error for an unsupported extension, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestOutliner_BuildDirectoryTree_ReparsesOnContentAndMtimeChange verifies
// that changing both a file's content and its mtime causes
// BuildDirectoryTree to re-parse it and reflect the new symbols in its
// output.
func TestOutliner_BuildDirectoryTree_ReparsesOnContentAndMtimeChange(t *testing.T) {
	tmp := t.TempDir()
	chdirTest(t, tmp)

	filePath := filepath.Join(tmp, "pkg", "sample.py")
	writeFile(t, filePath, "def alpha():\n    pass\n")

	o := NewOutliner()

	tree1, err := o.BuildDirectoryTree(".", true, 50)
	if err != nil {
		t.Fatalf("BuildDirectoryTree (1st call) failed: %v", err)
	}
	if !strings.Contains(tree1, "def alpha") {
		t.Fatalf("expected 1st tree to contain %q, got:\n%s", "def alpha", tree1)
	}

	// Change both content and mtime.
	writeFile(t, filePath, "def beta():\n    pass\n")
	future := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(filePath, future, future); err != nil {
		t.Fatalf("failed to update mtime: %v", err)
	}

	tree2, err := o.BuildDirectoryTree(".", true, 50)
	if err != nil {
		t.Fatalf("BuildDirectoryTree (2nd call) failed: %v", err)
	}
	if !strings.Contains(tree2, "def beta") {
		t.Fatalf("expected 2nd tree to contain %q, got:\n%s", "def beta", tree2)
	}
	if strings.Contains(tree2, "def alpha") {
		t.Fatalf("expected 2nd tree to no longer contain %q, got:\n%s", "def alpha", tree2)
	}
}

// TestOutliner_BuildDirectoryTree_SkipsReparseWhenMtimeUnchanged verifies
// the differential-scan short-circuit: when a file's on-disk mtime matches
// the cached mtime exactly, BuildDirectoryTree must not re-parse it (or
// even hash it), leaving whatever symbols are already cached untouched.
// This is exercised by seeding the cache with a deliberately "wrong"
// symbol (and a mismatching hash, which would trigger a re-parse if mtime
// comparison were skipped) at the file's real current mtime, then
// confirming that stale/seeded symbol -- not the file's real content -- is
// what BuildDirectoryTree reports.
func TestOutliner_BuildDirectoryTree_SkipsReparseWhenMtimeUnchanged(t *testing.T) {
	tmp := t.TempDir()
	chdirTest(t, tmp)

	filePath := filepath.Join(tmp, "foo.py")
	writeFile(t, filePath, "def real_func():\n    pass\n")

	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("failed to stat %q: %v", filePath, err)
	}
	mtime := fileMtime(info)

	cacheDir := filepath.Join(tmp, ".workspace_cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("failed to create cache dir: %v", err)
	}
	sdb, err := db.NewSymbolDatabase(filepath.Join(cacheDir, "symbols.db"))
	if err != nil {
		t.Fatalf("NewSymbolDatabase failed: %v", err)
	}
	if err := sdb.UpdateFileCache("foo.py", mtime, "bogus-hash-that-does-not-match-content", "python", []parser.SymbolData{
		{Name: "stale_symbol", Kind: "function", StartLine: 1, EndLine: 1},
	}); err != nil {
		t.Fatalf("UpdateFileCache (seed) failed: %v", err)
	}
	if err := sdb.Close(); err != nil {
		t.Fatalf("failed to close seeded db: %v", err)
	}

	o := NewOutliner()
	tree, err := o.BuildDirectoryTree(".", true, 50)
	if err != nil {
		t.Fatalf("BuildDirectoryTree failed: %v", err)
	}
	if !strings.Contains(tree, "def stale_symbol") {
		t.Fatalf("expected tree to still contain the seeded stale symbol (mtime unchanged -> no reparse), got:\n%s", tree)
	}
	if strings.Contains(tree, "def real_func") {
		t.Fatalf("expected tree NOT to contain the real file content (mtime unchanged should skip reparse entirely), got:\n%s", tree)
	}
}

// TestOutliner_BuildDirectoryTree_DeletesStaleCacheEntries verifies that a
// file removed from disk between two BuildDirectoryTree calls has its
// cached metadata/symbols dropped from the shared cache (the
// "dbFile not in currentFiles -> DeleteFileCache" cleanup loop), rather than
// continuing to appear in subsequent trees.
func TestOutliner_BuildDirectoryTree_DeletesStaleCacheEntries(t *testing.T) {
	tmp := t.TempDir()
	chdirTest(t, tmp)

	keepPath := filepath.Join(tmp, "keep.py")
	removedPath := filepath.Join(tmp, "removed.py")
	writeFile(t, keepPath, "def keep_func():\n    pass\n")
	writeFile(t, removedPath, "def removed_func():\n    pass\n")

	o := NewOutliner()
	tree1, err := o.BuildDirectoryTree(".", true, 50)
	if err != nil {
		t.Fatalf("BuildDirectoryTree (1st call) failed: %v", err)
	}
	if !strings.Contains(tree1, "def keep_func") || !strings.Contains(tree1, "def removed_func") {
		t.Fatalf("expected 1st tree to contain both functions, got:\n%s", tree1)
	}

	if err := os.Remove(removedPath); err != nil {
		t.Fatalf("failed to remove %q: %v", removedPath, err)
	}

	tree2, err := o.BuildDirectoryTree(".", true, 50)
	if err != nil {
		t.Fatalf("BuildDirectoryTree (2nd call) failed: %v", err)
	}
	if !strings.Contains(tree2, "def keep_func") {
		t.Fatalf("expected 2nd tree to still contain the untouched function, got:\n%s", tree2)
	}
	if strings.Contains(tree2, "removed.py") || strings.Contains(tree2, "def removed_func") {
		t.Fatalf("expected 2nd tree to no longer mention the deleted file/its cached symbols, got:\n%s", tree2)
	}

	// The deleted file's cache row itself must be gone too (not merely
	// absent from the rendered tree), confirmed by scanning "." again after
	// re-creating it under a cache-unaffected differential path: GetAllFiles
	// via a fresh BuildDirectoryTree scan of "." should not resurrect stale
	// data for removed.py.
	tree3, err := o.BuildDirectoryTree(".", true, 50)
	if err != nil {
		t.Fatalf("BuildDirectoryTree (3rd call) failed: %v", err)
	}
	if strings.Contains(tree3, "removed_func") {
		t.Fatalf("expected removed.py's symbols to remain purged from the cache, got:\n%s", tree3)
	}
}

// TestOutliner_BuildDirectoryTree_SubdirectoryScanPreservesEntriesOutsideIt
// verifies that scanning a subdirectory of the project root does not delete
// (or otherwise disturb) cache entries for files that lie outside that
// subdirectory (the "!isPathUnder(targetDir, dbFileAbs) -> continue" guard
// in the stale-cache-cleanup loop), and that the rendered tree for the
// subdirectory only reflects files within it.
func TestOutliner_BuildDirectoryTree_SubdirectoryScanPreservesEntriesOutsideIt(t *testing.T) {
	tmp := t.TempDir()
	chdirTest(t, tmp)

	writeFile(t, filepath.Join(tmp, "top.py"), "def top_func():\n    pass\n")
	writeFile(t, filepath.Join(tmp, "sub", "inner.py"), "def inner_func():\n    pass\n")

	o := NewOutliner()
	// Seed the cache for the whole project first.
	if _, err := o.BuildDirectoryTree(".", true, 50); err != nil {
		t.Fatalf("BuildDirectoryTree(\".\") failed: %v", err)
	}

	subTree, err := o.BuildDirectoryTree("sub", true, 50)
	if err != nil {
		t.Fatalf("BuildDirectoryTree(\"sub\") failed: %v", err)
	}
	if !strings.Contains(subTree, "def inner_func") {
		t.Fatalf("expected subdirectory tree to contain inner_func, got:\n%s", subTree)
	}
	if strings.Contains(subTree, "def top_func") {
		t.Fatalf("expected subdirectory tree NOT to contain top.py's symbol, got:\n%s", subTree)
	}

	// A subsequent whole-project scan must still show top.py: the
	// subdirectory-only scan above must not have deleted its cache entry.
	fullTree, err := o.BuildDirectoryTree(".", true, 50)
	if err != nil {
		t.Fatalf("BuildDirectoryTree(\".\") (2nd call) failed: %v", err)
	}
	if !strings.Contains(fullTree, "def top_func") {
		t.Fatalf("expected whole-project tree to still contain top.py's symbol after a subdirectory-only scan, got:\n%s", fullTree)
	}
	if !strings.Contains(fullTree, "def inner_func") {
		t.Fatalf("expected whole-project tree to still contain sub/inner.py's symbol, got:\n%s", fullTree)
	}
}

// TestOutliner_BuildDirectoryTree_NonRecursive verifies that recursive=false
// limits both the on-disk scan (collectSourceFiles) and the rendered tree to
// dirPath's direct entries, ignoring files in subdirectories entirely (they
// are neither scanned nor cached).
func TestOutliner_BuildDirectoryTree_NonRecursive(t *testing.T) {
	tmp := t.TempDir()
	chdirTest(t, tmp)

	writeFile(t, filepath.Join(tmp, "top.py"), "def top_func():\n    pass\n")
	writeFile(t, filepath.Join(tmp, "sub", "inner.py"), "def inner_func():\n    pass\n")

	o := NewOutliner()
	tree, err := o.BuildDirectoryTree(".", false, 50)
	if err != nil {
		t.Fatalf("BuildDirectoryTree failed: %v", err)
	}
	if !strings.Contains(tree, "def top_func") {
		t.Fatalf("expected non-recursive tree to contain top.py's symbol, got:\n%s", tree)
	}
	if strings.Contains(tree, "def inner_func") || strings.Contains(tree, "inner.py") {
		t.Fatalf("expected non-recursive tree NOT to descend into subdirectories, got:\n%s", tree)
	}
}

// TestOutliner_BuildDirectoryTree_MkdirAllFailureReturnsError verifies that
// BuildDirectoryTree surfaces an *apperr.FileAccessError when the
// .workspace_cache path cannot be created as a directory because a regular
// file already occupies that name (mirroring
// TestOutliner_WriteFeedbackLog_MkdirAllFailureReturnsError).
func TestOutliner_BuildDirectoryTree_MkdirAllFailureReturnsError(t *testing.T) {
	tmp := t.TempDir()
	chdirTest(t, tmp)
	writeFile(t, filepath.Join(tmp, ".workspace_cache"), "not a directory")

	o := NewOutliner()
	_, err := o.BuildDirectoryTree(".", true, 50)
	if err == nil {
		t.Fatal("expected an error when .workspace_cache is occupied by a regular file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestOutliner_BuildDirectoryTree_SymbolDatabaseOpenFailureIsPropagated
// verifies that a db.NewSymbolDatabase failure (here: .workspace_cache/
// symbols.db is a non-empty directory, which the self-healing schema-repair
// logic cannot delete and therefore reports as an error rather than silently
// rebuilding) is returned as-is by BuildDirectoryTree, rather than being
// swallowed or wrapped into a different error type.
func TestOutliner_BuildDirectoryTree_SymbolDatabaseOpenFailureIsPropagated(t *testing.T) {
	tmp := t.TempDir()
	chdirTest(t, tmp)

	dbDir := filepath.Join(tmp, ".workspace_cache")
	symbolsDbPath := filepath.Join(dbDir, "symbols.db")
	if err := os.MkdirAll(symbolsDbPath, 0o755); err != nil {
		t.Fatalf("failed to pre-create symbols.db as a directory: %v", err)
	}
	// Make the directory non-empty so the schema-repair logic's os.Remove
	// fails instead of silently self-healing.
	writeFile(t, filepath.Join(symbolsDbPath, "inner.txt"), "x")

	o := NewOutliner()
	_, err := o.BuildDirectoryTree(".", true, 50)
	if err == nil {
		t.Fatal("expected an error when symbols.db is occupied by a non-empty directory, got nil")
	}
	if !strings.Contains(err.Error(), "db: failed to remove corrupted database") {
		t.Errorf("expected the underlying db package error to propagate unwrapped, got: %v", err)
	}
}

// TestOutliner_BuildDirectoryTree_MtimeOnlyChangeRefreshesCacheWithoutReparse
// verifies the differential-scan branch that is neither a full skip (mtime
// unchanged) nor a full re-parse (content hash changed): when only the
// on-disk mtime changes but the file's content (and therefore hash) does
// not, BuildDirectoryTree must refresh the cached mtime (via
// UpdateFileMtime) without re-parsing the file, so the cached symbols
// survive untouched.
func TestOutliner_BuildDirectoryTree_MtimeOnlyChangeRefreshesCacheWithoutReparse(t *testing.T) {
	tmp := t.TempDir()
	chdirTest(t, tmp)

	content := "def stable_func():\n    pass\n"
	filePath := filepath.Join(tmp, "stable.py")
	writeFile(t, filePath, content)

	o := NewOutliner()
	tree1, err := o.BuildDirectoryTree(".", true, 50)
	if err != nil {
		t.Fatalf("BuildDirectoryTree (1st call) failed: %v", err)
	}
	if !strings.Contains(tree1, "def stable_func") {
		t.Fatalf("expected 1st tree to contain %q, got:\n%s", "def stable_func", tree1)
	}

	// Touch the mtime forward without changing the file's content/hash.
	future := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(filePath, future, future); err != nil {
		t.Fatalf("failed to update mtime: %v", err)
	}

	tree2, err := o.BuildDirectoryTree(".", true, 50)
	if err != nil {
		t.Fatalf("BuildDirectoryTree (2nd call) failed: %v", err)
	}
	if !strings.Contains(tree2, "def stable_func") {
		t.Fatalf("expected 2nd tree to still contain %q after an mtime-only change, got:\n%s", "def stable_func", tree2)
	}

	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("failed to stat %q: %v", filePath, err)
	}
	cacheDir := filepath.Join(tmp, ".workspace_cache")
	sdb, err := db.NewSymbolDatabase(filepath.Join(cacheDir, "symbols.db"))
	if err != nil {
		t.Fatalf("NewSymbolDatabase failed: %v", err)
	}
	defer func() { _ = sdb.Close() }()
	files, err := sdb.GetAllFiles()
	if err != nil {
		t.Fatalf("GetAllFiles failed: %v", err)
	}
	meta, ok := files["stable.py"]
	if !ok {
		t.Fatalf("expected stable.py to be present in the cache, got %+v", files)
	}
	if meta.Mtime != fileMtime(info) {
		t.Errorf("expected the cached mtime to be refreshed to the new on-disk mtime, got cached=%v want=%v", meta.Mtime, fileMtime(info))
	}
}

// TestOutliner_BuildDirectoryTree_LimitTruncation verifies that exceeding
// limit truncates the rendered file list and appends the truncation hint.
func TestOutliner_BuildDirectoryTree_LimitTruncation(t *testing.T) {
	tmp := t.TempDir()
	chdirTest(t, tmp)

	writeFile(t, filepath.Join(tmp, "a.py"), "def a():\n    pass\n")
	writeFile(t, filepath.Join(tmp, "b.py"), "def b():\n    pass\n")
	writeFile(t, filepath.Join(tmp, "c.py"), "def c():\n    pass\n")

	o := NewOutliner()
	tree, err := o.BuildDirectoryTree(".", true, 1)
	if err != nil {
		t.Fatalf("BuildDirectoryTree failed: %v", err)
	}

	if !strings.Contains(tree, "... (output truncated due to limit)") {
		t.Errorf("expected truncation marker in output, got:\n%s", tree)
	}
	if !strings.Contains(tree, "Hint: Use '--limit N' or '--filter' to scope down your search.") {
		t.Errorf("expected truncation hint in output, got:\n%s", tree)
	}

	fileLines := 0
	for _, line := range strings.Split(tree, "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), ".py") {
			fileLines++
		}
	}
	if fileLines != 1 {
		t.Errorf("expected exactly 1 file entry after truncation to limit=1, got %d in:\n%s", fileLines, tree)
	}
}

// TestOutliner_BuildDirectoryTree_OutsideProjectRoot verifies that a
// directory outside the current working directory (project root) is
// rejected with an *apperr.FileAccessError.
func TestOutliner_BuildDirectoryTree_OutsideProjectRoot(t *testing.T) {
	tmp := t.TempDir()
	projDir := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("failed to create proj dir: %v", err)
	}
	chdirTest(t, projDir)

	o := NewOutliner()
	_, err := o.BuildDirectoryTree("..", true, 50)
	if err == nil {
		t.Fatal("expected an error for a directory outside the project root, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

func TestOutliner_WriteFeedbackLog(t *testing.T) {
	tmp := t.TempDir()
	chdirTest(t, tmp)

	o := NewOutliner()
	if err := o.WriteFeedbackLog("first message"); err != nil {
		t.Fatalf("WriteFeedbackLog (1st) failed: %v", err)
	}
	if err := o.WriteFeedbackLog("second message"); err != nil {
		t.Fatalf("WriteFeedbackLog (2nd) failed: %v", err)
	}

	feedbackPath := filepath.Join(tmp, ".workspace_cache", "feedback.jsonl")
	data, err := os.ReadFile(feedbackPath)
	if err != nil {
		t.Fatalf("failed to read feedback log: %v", err)
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 log lines, got %d: %q", len(lines), string(data))
	}

	wantMessages := []string{"first message", "second message"}
	for i, line := range lines {
		var entry feedbackLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("line %d is not valid JSON (%q): %v", i, line, err)
		}
		if entry.Message != wantMessages[i] {
			t.Errorf("line %d Message = %q, want %q", i, entry.Message, wantMessages[i])
		}
		if _, err := time.Parse("2006-01-02T15:04:05Z", entry.Timestamp); err != nil {
			t.Errorf("line %d Timestamp %q does not match expected format: %v", i, entry.Timestamp, err)
		}
	}
}

// TestOutliner_WriteFeedbackLog_MkdirAllFailureReturnsError verifies that
// WriteFeedbackLog surfaces an *apperr.FileAccessError when the
// .workspace_cache path cannot be created as a directory because a regular
// file already occupies that name.
func TestOutliner_WriteFeedbackLog_MkdirAllFailureReturnsError(t *testing.T) {
	tmp := t.TempDir()
	chdirTest(t, tmp)
	writeFile(t, filepath.Join(tmp, ".workspace_cache"), "not a directory")

	o := NewOutliner()
	err := o.WriteFeedbackLog("hi")
	if err == nil {
		t.Fatal("expected an error when .workspace_cache is occupied by a regular file, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestOutliner_WriteFeedbackLog_OpenFileFailureReturnsError verifies that
// WriteFeedbackLog surfaces an *apperr.FileAccessError when
// .workspace_cache/feedback.jsonl cannot be opened for writing because a
// directory already occupies that path.
func TestOutliner_WriteFeedbackLog_OpenFileFailureReturnsError(t *testing.T) {
	tmp := t.TempDir()
	chdirTest(t, tmp)
	if err := os.MkdirAll(filepath.Join(tmp, ".workspace_cache", "feedback.jsonl"), 0o755); err != nil {
		t.Fatalf("failed to pre-create feedback.jsonl as a directory: %v", err)
	}

	o := NewOutliner()
	err := o.WriteFeedbackLog("hi")
	if err == nil {
		t.Fatal("expected an error when feedback.jsonl is occupied by a directory, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

func TestOutliner_LocateSymbol(t *testing.T) {
	o := NewOutliner()

	filePath, startLine, endLine, err := o.LocateSymbol(sampleFile, "Greeter")
	if err != nil {
		t.Fatalf("LocateSymbol returned unexpected error: %v", err)
	}
	if filePath != sampleFile {
		t.Errorf("filePath = %q, want %q", filePath, sampleFile)
	}
	if startLine != 9 || endLine != 19 {
		t.Errorf("lines = [%d,%d], want [9,19]", startLine, endLine)
	}
}

func TestOutliner_LocateSymbol_NotFound(t *testing.T) {
	o := NewOutliner()
	_, _, _, err := o.LocateSymbol(sampleFile, "NoSuchClass")
	if err == nil {
		t.Fatal("expected an error for an unknown symbol, got nil")
	}
	var symErr *apperr.SymbolNotFoundError
	if !errors.As(err, &symErr) {
		t.Errorf("expected *apperr.SymbolNotFoundError, got %T: %v", err, err)
	}
}

func TestOutliner_LocateSymbol_UnsupportedExtension(t *testing.T) {
	o := NewOutliner()
	_, _, _, err := o.LocateSymbol("something.rb", "X")
	if err == nil {
		t.Fatal("expected an error for an unsupported extension, got nil")
	}
	var fileErr *apperr.FileAccessError
	if !errors.As(err, &fileErr) {
		t.Errorf("expected *apperr.FileAccessError, got %T: %v", err, err)
	}
}

// TestOutliner_FindReferences verifies that FindReferences aggregates
// exactly the real call-site references across a small project (excluding
// definition sites, parameter names, comment mentions, and files where the
// symbol is entirely absent), with neither omissions nor duplicates.
func TestOutliner_FindReferences(t *testing.T) {
	o := NewOutliner()
	got, err := o.FindReferences(referencesDir, "target_func")
	if err != nil {
		t.Fatalf("FindReferences returned unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected exactly 2 references, got %d: %+v", len(got), got)
	}

	type key struct {
		file string
		line int
	}
	want := map[key]string{
		{file: filepath.ToSlash(referencesDir) + "/caller_a.py", line: 2}: "target_func()  # actual reference on this line",
		{file: filepath.ToSlash(referencesDir) + "/caller_b.py", line: 6}: "value = target_func",
	}

	seen := map[key]bool{}
	for _, ref := range got {
		k := key{file: filepath.ToSlash(ref.FilePath), line: ref.LineNumber}
		wantContent, ok := want[k]
		if !ok {
			t.Errorf("unexpected reference: %+v", ref)
			continue
		}
		if seen[k] {
			t.Errorf("duplicate reference: %+v", ref)
		}
		seen[k] = true
		if ref.LineContent != wantContent {
			t.Errorf("reference %+v LineContent = %q, want %q", k, ref.LineContent, wantContent)
		}
	}
	if len(seen) != len(want) {
		t.Errorf("expected all of %+v to be found, got %+v", want, seen)
	}
}

func TestOutliner_FindReferences_NonexistentRoot(t *testing.T) {
	o := NewOutliner()
	got, err := o.FindReferences("../../testdata/python/does_not_exist_dir", "target_func")
	if err != nil {
		t.Fatalf("FindReferences returned unexpected error for a nonexistent root: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 references for a nonexistent root, got %d: %+v", len(got), got)
	}
}

// TestOutliner_FindReferences_MultiLanguage verifies that FindReferences
// dispatches every file it walks to the parser.Parser matching its
// extension (getParser/extToLang, extended by task 10, task 22, and task 31),
// aggregating results from Python, TypeScript, Go, Java, JavaScript, and Rust
// source files in the same scan. The per-language reference counts are
// intentionally asymmetric (see the per-key comments below): Python/
// TypeScript/JavaScript/RustParser all exclude a symbol's own definition-site
// identifier, while Go/JavaParser do not (a faithfully-ported quirk of
// ast_digger/parsers/go.py, documented in task 9's notes and golang.go's
// FindReferencesInFile doc comment, and mirrored by JavaParser per its own
// doc comment). This test intentionally exercises that asymmetry rather
// than hiding it, so a future change to any one parser's
// definition-exclusion behavior is caught here as well as in that parser's
// own tests.
func TestOutliner_FindReferences_MultiLanguage(t *testing.T) {
	o := NewOutliner()
	got, err := o.FindReferences(mixedReferencesDir, "helperFn")
	if err != nil {
		t.Fatalf("FindReferences returned unexpected error: %v", err)
	}

	type key struct {
		file string
		line int
	}
	want := map[key]string{
		// Python: target.py's own "def helperFn():" line is excluded
		// (definition-site exclusion); only caller.py's actual call site
		// counts.
		{file: filepath.ToSlash(mixedReferencesDir) + "/caller.py", line: 2}: "helperFn()  # actual reference on this line",
		// TypeScript: same definition-site exclusion as Python.
		{file: filepath.ToSlash(mixedReferencesDir) + "/caller.ts", line: 2}: "helperFn(); // actual reference on this line",
		// Go: GolangParser does NOT exclude the definition site, so
		// target.go's "func helperFn() string {" line is itself reported as
		// a reference, in addition to caller.go's actual call site.
		{file: filepath.ToSlash(mixedReferencesDir) + "/target.go", line: 4}: "func helperFn() string {",
		{file: filepath.ToSlash(mixedReferencesDir) + "/caller.go", line: 5}: "return helperFn() // actual reference on this line",
		// Java: JavaParser also does NOT exclude the definition site, so
		// target.java's "public static String helperFn() {" line is itself
		// reported as a reference, in addition to caller.java's actual call
		// site.
		{file: filepath.ToSlash(mixedReferencesDir) + "/target.java", line: 6}: "public static String helperFn() {",
		{file: filepath.ToSlash(mixedReferencesDir) + "/caller.java", line: 5}: "Target.helperFn(); // actual reference on this line",
		// JavaScript: JavaScriptParser excludes the definition site, same as
		// Python/TypeScript, so only caller.js's actual call site counts.
		{file: filepath.ToSlash(mixedReferencesDir) + "/caller.js", line: 2}: "helperFn(); // actual reference on this line",
		// Rust: RustParser excludes the definition site, same as
		// Python/TypeScript/JavaScript, so only caller.rs's actual call site counts.
		{file: filepath.ToSlash(mixedReferencesDir) + "/caller.rs", line: 3}: "helperFn() // actual reference on this line",
	}

	seen := map[key]bool{}
	for _, ref := range got {
		k := key{file: filepath.ToSlash(ref.FilePath), line: ref.LineNumber}
		wantContent, ok := want[k]
		if !ok {
			t.Errorf("unexpected reference: %+v", ref)
			continue
		}
		if seen[k] {
			t.Errorf("duplicate reference: %+v", ref)
		}
		seen[k] = true
		if ref.LineContent != wantContent {
			t.Errorf("reference %+v LineContent = %q, want %q", k, ref.LineContent, wantContent)
		}
	}
	if len(seen) != len(want) {
		t.Errorf("expected all of %+v to be found, got %+v (raw: %+v)", want, seen, got)
	}
}

// TestOutliner_BuildDirectoryTree_MultiLanguage verifies that
// BuildDirectoryTree, via the extended extToLang/getParser dispatch tables,
// discovers, caches, and renders symbols from Python (.py), TypeScript
// (.ts), and Go (.go) source files within the same directory scan, while
// still honoring the excludeDirs skip-list (here, node_modules) regardless
// of which language's files it contains.
func TestOutliner_BuildDirectoryTree_MultiLanguage(t *testing.T) {
	tmp := t.TempDir()
	chdirTest(t, tmp)

	writeFile(t, filepath.Join(tmp, "app.py"), strings.Join([]string{
		"class MixedGreeter:",
		"    def greet(self) -> str:",
		"        return \"hello from python\"",
		"",
		"",
		"def mixed_top_level_py() -> None:",
		"    return None",
		"",
	}, "\n"))

	writeFile(t, filepath.Join(tmp, "app.ts"), strings.Join([]string{
		"export class MixedWidget {",
		"  render(): string {",
		"    return \"hello from typescript\";",
		"  }",
		"}",
		"",
		"export function mixedTopLevelTs(): void {}",
		"",
	}, "\n"))

	writeFile(t, filepath.Join(tmp, "app.go"), strings.Join([]string{
		"package mixed",
		"",
		"// MixedStruct is a simple struct for the mixed-language directory tree test.",
		"type MixedStruct struct {",
		"\tName string",
		"}",
		"",
		"// Describe returns a description.",
		"func (m MixedStruct) Describe() string {",
		"\treturn \"hello from go\"",
		"}",
		"",
		"// MixedTopLevelGo is a top-level function.",
		"func MixedTopLevelGo() string {",
		"\treturn \"top level\"",
		"}",
		"",
	}, "\n"))

	writeFile(t, filepath.Join(tmp, "app.java"), strings.Join([]string{
		"package mixed;",
		"",
		"public class MixedJavaGreeter {",
		"    public String greetJava() {",
		"        return \"hello from java\";",
		"    }",
		"}",
		"",
	}, "\n"))

	writeFile(t, filepath.Join(tmp, "app.js"), strings.Join([]string{
		"export class MixedJsWidget {",
		"  renderJs() {",
		"    return \"hello from javascript\";",
		"  }",
		"}",
		"",
		"export function mixedTopLevelJs() {}",
		"",
	}, "\n"))

	writeFile(t, filepath.Join(tmp, "app.rs"), strings.Join([]string{
		"pub struct MixedRsWidget;",
		"impl MixedRsWidget {",
		"    pub fn render_rs(&self) -> &'static str {",
		"        \"hello from rust\"",
		"    }",
		"}",
		"",
		"pub fn mixed_top_level_rs() {}",
		"",
	}, "\n"))

	// node_modules must be excluded regardless of the file extensions it
	// contains (excludeDirs is language-agnostic).
	writeFile(t, filepath.Join(tmp, "node_modules", "ignored.ts"), "export function shouldBeIgnored(): void {}\n")

	o := NewOutliner()
	tree, err := o.BuildDirectoryTree(".", true, 50)
	if err != nil {
		t.Fatalf("BuildDirectoryTree failed: %v", err)
	}

	for _, want := range []string{
		"app.py", "app.ts", "app.go", "app.java", "app.js", "app.rs",
		"class MixedGreeter", "def greet",
		"def mixed_top_level_py",
		"class MixedWidget", "def render",
		"def mixedTopLevelTs",
		// Go structs are cataloged with kind "struct" (not "class"), which
		// BuildDirectoryTree's per-file outline rendering does not
		// special-case -- a faithfully-ported limitation of Python's
		// build_directory_tree (ast_digger/outline.py), which likewise only
		// special-cases kind == "class". Only Go's top-level function is
		// expected to appear in the rendered tree; MixedStruct/Describe are
		// intentionally not asserted here (see ParseFile-level coverage in
		// golang_test.go for their extraction).
		"def MixedTopLevelGo",
		// Java (task 22): classes/methods use the same "class"/"def"
		// rendering as Python/TypeScript.
		"class MixedJavaGreeter", "def greetJava",
		// JavaScript (task 22): same rendering as TypeScript.
		"class MixedJsWidget", "def renderJs", "def mixedTopLevelJs",
		// Rust (task 31): top-level functions appear in the rendered tree.
		"def mixed_top_level_rs",
	} {
		if !strings.Contains(tree, want) {
			t.Errorf("expected tree to contain %q, got:\n%s", want, tree)
		}
	}

	if strings.Contains(tree, "shouldBeIgnored") {
		t.Errorf("expected node_modules to be excluded from the tree, got:\n%s", tree)
	}
}

// TestOutliner_BuildDirectoryTree_MultiLanguage_DiffCacheStillWorks is a
// lightweight regression check that the differential-scan cache (fully
// exercised, single-language, in
// TestOutliner_BuildDirectoryTree_ReparsesOnContentAndMtimeChange /
// TestOutliner_BuildDirectoryTree_SkipsReparseWhenMtimeUnchanged) is not
// broken by task 10's multi-language dispatch: editing only the Go file in
// a mixed-language project causes just that file to be re-parsed and
// reflected, while the untouched Python/TypeScript files' cached symbols
// are unaffected.
func TestOutliner_BuildDirectoryTree_MultiLanguage_DiffCacheStillWorks(t *testing.T) {
	tmp := t.TempDir()
	chdirTest(t, tmp)

	writeFile(t, filepath.Join(tmp, "a.py"), "def alpha():\n    pass\n")
	writeFile(t, filepath.Join(tmp, "b.ts"), "export function bravo(): void {}\n")
	goFile := filepath.Join(tmp, "c.go")
	writeFile(t, goFile, "package c\n\nfunc Charlie() {}\n")

	o := NewOutliner()
	tree1, err := o.BuildDirectoryTree(".", true, 50)
	if err != nil {
		t.Fatalf("BuildDirectoryTree (1st call) failed: %v", err)
	}
	for _, want := range []string{"def alpha", "def bravo", "def Charlie"} {
		if !strings.Contains(tree1, want) {
			t.Fatalf("expected 1st tree to contain %q, got:\n%s", want, tree1)
		}
	}

	writeFile(t, goFile, "package c\n\nfunc Delta() {}\n")
	future := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(goFile, future, future); err != nil {
		t.Fatalf("failed to update mtime: %v", err)
	}

	tree2, err := o.BuildDirectoryTree(".", true, 50)
	if err != nil {
		t.Fatalf("BuildDirectoryTree (2nd call) failed: %v", err)
	}
	if !strings.Contains(tree2, "def Delta") {
		t.Fatalf("expected 2nd tree to contain the reparsed Go symbol %q, got:\n%s", "def Delta", tree2)
	}
	if strings.Contains(tree2, "def Charlie") {
		t.Fatalf("expected 2nd tree to no longer contain the stale Go symbol %q, got:\n%s", "def Charlie", tree2)
	}
	// The untouched Python/TypeScript files' cached entries must survive
	// unchanged.
	if !strings.Contains(tree2, "def alpha") {
		t.Fatalf("expected 2nd tree to still contain the untouched Python symbol %q, got:\n%s", "def alpha", tree2)
	}
	if !strings.Contains(tree2, "def bravo") {
		t.Fatalf("expected 2nd tree to still contain the untouched TypeScript symbol %q, got:\n%s", "def bravo", tree2)
	}
}

// TestOutliner_FindReferences_ExcludesConfiguredDirectories verifies that
// FindReferences never descends into excludeDirs (here, node_modules),
// mirroring BuildDirectoryTree's/collectSourceFiles' identical skip-list
// even though FindReferences walks the tree independently.
func TestOutliner_FindReferences_ExcludesConfiguredDirectories(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "caller.py"), "target_func()  # actual reference\n")
	writeFile(t, filepath.Join(tmp, "node_modules", "vendored.py"), "target_func()  # should be excluded\n")

	o := NewOutliner()
	got, err := o.FindReferences(tmp, "target_func")
	if err != nil {
		t.Fatalf("FindReferences returned unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 reference (node_modules excluded), got %d: %+v", len(got), got)
	}
	if !strings.HasSuffix(filepath.ToSlash(got[0].FilePath), "caller.py") {
		t.Errorf("expected the sole reference to come from caller.py, got %+v", got[0])
	}
}

// TestOutliner_FindReferences_IgnoresUnsupportedExtensions verifies that
// FindReferences skips files whose extension is not in extToLang (here, a
// plain .txt file that happens to mention the symbol name), rather than
// erroring or misinterpreting them as a supported language.
func TestOutliner_FindReferences_IgnoresUnsupportedExtensions(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "caller.py"), "target_func()  # actual reference\n")
	writeFile(t, filepath.Join(tmp, "notes.txt"), "target_func is mentioned here too\n")

	o := NewOutliner()
	got, err := o.FindReferences(tmp, "target_func")
	if err != nil {
		t.Fatalf("FindReferences returned unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 reference (notes.txt ignored), got %d: %+v", len(got), got)
	}
}

// --- Unit-level tests for outline.go's unexported helpers, exercised
// directly (rather than only indirectly through Outliner's exported
// methods) to reach error branches that are otherwise hard or impossible to
// trigger through the public API alone.

// TestIsPathUnder covers isPathUnder's three return paths: target equal to
// base, target nested under base, target outside base, and the
// filepath.Rel error path (mixing an absolute and a relative argument,
// which filepath.Rel cannot resolve without more context).
func TestIsPathUnder(t *testing.T) {
	tmp := t.TempDir()
	nested := filepath.Join(tmp, "a", "b")

	tests := []struct {
		name   string
		base   string
		target string
		want   bool
	}{
		{name: "target equals base", base: tmp, target: tmp, want: true},
		{name: "target nested under base", base: tmp, target: nested, want: true},
		{name: "target outside base", base: nested, target: tmp, want: false},
		{
			name:   "filepath.Rel error (absolute vs relative)",
			base:   filepath.Join(tmp, "abs"),
			target: "relative/path",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPathUnder(tt.base, tt.target); got != tt.want {
				t.Errorf("isPathUnder(%q, %q) = %v, want %v", tt.base, tt.target, got, tt.want)
			}
		})
	}
}

// TestFileHash_NonexistentFileReturnsError verifies fileHash's os.Open error
// branch.
func TestFileHash_NonexistentFileReturnsError(t *testing.T) {
	_, err := fileHash(filepath.Join(t.TempDir(), "does-not-exist.py"))
	if err == nil {
		t.Fatal("expected an error for a nonexistent file, got nil")
	}
}

// TestFileHash_MatchesContent verifies fileHash's success path returns a
// deterministic, content-derived digest (rather than merely returning
// without erroring): identical content hashes identically, and different
// content hashes differently.
func TestFileHash_MatchesContent(t *testing.T) {
	tmp := t.TempDir()
	pathA := filepath.Join(tmp, "a.py")
	pathA2 := filepath.Join(tmp, "a2.py")
	pathB := filepath.Join(tmp, "b.py")
	writeFile(t, pathA, "def a():\n    pass\n")
	writeFile(t, pathA2, "def a():\n    pass\n")
	writeFile(t, pathB, "def b():\n    pass\n")

	hashA, err := fileHash(pathA)
	if err != nil {
		t.Fatalf("fileHash(pathA) failed: %v", err)
	}
	hashA2, err := fileHash(pathA2)
	if err != nil {
		t.Fatalf("fileHash(pathA2) failed: %v", err)
	}
	hashB, err := fileHash(pathB)
	if err != nil {
		t.Fatalf("fileHash(pathB) failed: %v", err)
	}
	if hashA != hashA2 {
		t.Errorf("expected identical content to hash identically: %q != %q", hashA, hashA2)
	}
	if hashA == hashB {
		t.Errorf("expected different content to hash differently, both were %q", hashA)
	}
}

// TestCollectSourceFiles_NonRecursiveSkipsSubdirectories is a direct
// unit-level test of collectSourceFiles' recursive=false behavior (in
// addition to the Outliner.BuildDirectoryTree-level coverage above),
// confirming subdirectory entries are never even walked (as opposed to
// walked-but-filtered).
func TestCollectSourceFiles_NonRecursiveSkipsSubdirectories(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "top.py"), "def top():\n    pass\n")
	writeFile(t, filepath.Join(tmp, "sub", "inner.py"), "def inner():\n    pass\n")

	got := collectSourceFiles(tmp, tmp, false)
	if _, ok := got["top.py"]; !ok {
		t.Errorf("expected top.py to be collected, got %+v", got)
	}
	if _, ok := got["sub/inner.py"]; ok {
		t.Errorf("expected sub/inner.py NOT to be collected with recursive=false, got %+v", got)
	}
	if len(got) != 1 {
		t.Errorf("expected exactly 1 collected file, got %d: %+v", len(got), got)
	}
}

// TestCollectSourceFiles_ExcludesConfiguredDirectories verifies that
// collectSourceFiles never descends into excludeDirs even when recursive is
// true.
func TestCollectSourceFiles_ExcludesConfiguredDirectories(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "top.py"), "def top():\n    pass\n")
	writeFile(t, filepath.Join(tmp, "node_modules", "vendored.py"), "def vendored():\n    pass\n")

	got := collectSourceFiles(tmp, tmp, true)
	if _, ok := got["top.py"]; !ok {
		t.Errorf("expected top.py to be collected, got %+v", got)
	}
	if _, ok := got["node_modules/vendored.py"]; ok {
		t.Errorf("expected node_modules/vendored.py to be excluded, got %+v", got)
	}
}

// TestCollectSourceFiles_IgnoresUnsupportedExtensions verifies that files
// whose extension is not a key of extToLang are skipped rather than causing
// an error or being mis-tagged with a language.
func TestCollectSourceFiles_IgnoresUnsupportedExtensions(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "top.py"), "def top():\n    pass\n")
	writeFile(t, filepath.Join(tmp, "notes.txt"), "not a source file\n")

	got := collectSourceFiles(tmp, tmp, true)
	if _, ok := got["top.py"]; !ok {
		t.Errorf("expected top.py to be collected, got %+v", got)
	}
	if _, ok := got["notes.txt"]; ok {
		t.Errorf("expected notes.txt to be ignored, got %+v", got)
	}
	if len(got) != 1 {
		t.Errorf("expected exactly 1 collected file, got %d: %+v", len(got), got)
	}
}

// TestCollectSourceFiles_UnreadableTargetDirReturnsEmptyResult verifies the
// "os.ReadDir(dir) fails -> return" branch of collectSourceFiles' internal
// walk: passing a path that exists but is a regular file (not a directory)
// as targetDir causes os.ReadDir to fail immediately, yielding an empty (but
// non-nil) result rather than a panic.
func TestCollectSourceFiles_UnreadableTargetDirReturnsEmptyResult(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "a.py")
	writeFile(t, filePath, "def a():\n    pass\n")

	got := collectSourceFiles(filePath, filePath, true)
	if len(got) != 0 {
		t.Errorf("expected no files when targetDir is not a directory, got %+v", got)
	}
}

// TestCollectSourceFiles_RelPathFailureIsSkipped verifies the
// "filepath.Rel(projectRoot, full) fails -> continue" branch: passing a
// projectRoot that cannot be related to the (absolute) targetDir -- because
// one is absolute and the other is a bare relative path, which
// filepath.Rel cannot resolve without more context -- causes the file to be
// silently skipped rather than panicking or returning a malformed key.
func TestCollectSourceFiles_RelPathFailureIsSkipped(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "top.py"), "def top():\n    pass\n")

	got := collectSourceFiles(tmp, "relative-root-that-cannot-be-related", true)
	if len(got) != 0 {
		t.Errorf("expected no files to be collected when projectRoot cannot be related to targetDir, got %+v", got)
	}
}

// TestSymbolOutlineLines_SortsMultipleClassesFunctionsAndMethods exercises
// symbolOutlineLines' three sort.SliceStable comparator closures (classes,
// top-level functions, and a class's methods) with more than one element
// each -- the previous BuildDirectoryTree-level tests only ever produced
// single-element (or empty) slices for at least one of the three, so their
// "less" closures were declared but never actually invoked.
func TestSymbolOutlineLines_SortsMultipleClassesFunctionsAndMethods(t *testing.T) {
	strPtr := func(s string) *string { return &s }
	syms := []parser.SymbolData{
		// Two top-level functions, deliberately out of StartLine order.
		{Name: "second_func", Kind: "function", StartLine: 20, EndLine: 21},
		{Name: "first_func", Kind: "function", StartLine: 5, EndLine: 6},
		// Two classes, deliberately out of StartLine order.
		{Name: "BClass", Kind: "class", StartLine: 15, EndLine: 25},
		{Name: "AClass", Kind: "class", StartLine: 1, EndLine: 10},
		// BClass's two methods, deliberately out of StartLine order.
		{Name: "second_method", Kind: "method", Parent: strPtr("BClass"), StartLine: 22, EndLine: 22},
		{Name: "first_method", Kind: "method", Parent: strPtr("BClass"), StartLine: 16, EndLine: 16},
	}

	got := symbolOutlineLines(syms, "", "  ", "")
	want := []string{
		"class AClass",
		"class BClass",
		"  def first_method",
		"  def second_method",
		"def first_func",
		"def second_func",
	}
	if len(got) != len(want) {
		t.Fatalf("symbolOutlineLines returned %d lines, want %d: got=%v want=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q (full got=%v)", i, got[i], want[i], got)
		}
	}
}

// TestOutliner_FindReferences_UnreadableRootReturnsEmptyResult verifies the
// "os.ReadDir(dir) fails -> return" branch of FindReferences' internal walk:
// passing a path that exists but is a regular file (not a directory) as
// projectRoot causes os.ReadDir to fail immediately, and FindReferences
// reports zero references rather than erroring.
func TestOutliner_FindReferences_UnreadableRootReturnsEmptyResult(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "a.py")
	writeFile(t, filePath, "target_func()\n")

	o := NewOutliner()
	got, err := o.FindReferences(filePath, "target_func")
	if err != nil {
		t.Fatalf("FindReferences returned unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no references when projectRoot is not a directory, got %+v", got)
	}
}

// TestOutliner_FindReferences_RecursesIntoNestedNonExcludedDirectories
// verifies that FindReferences' own directory walk (independent of
// collectSourceFiles/BuildDirectoryTree's) actually descends into ordinary
// (non-excludeDirs) subdirectories, rather than only scanning projectRoot's
// direct entries.
func TestOutliner_FindReferences_RecursesIntoNestedNonExcludedDirectories(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "caller.py"), "target_func()  # top-level reference\n")
	writeFile(t, filepath.Join(tmp, "nested", "deep", "caller2.py"), "target_func()  # nested reference\n")

	o := NewOutliner()
	got, err := o.FindReferences(tmp, "target_func")
	if err != nil {
		t.Fatalf("FindReferences returned unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 references (top-level + nested), got %d: %+v", len(got), got)
	}
}

// TestOutliner_ExtractSymbolWithDependencies_SingleLanguage verifies that
// ExtractSymbolWithDependencies extracts the target symbol and, when followImports
// is true, automatically resolves and attaches dependent local symbols (and
// omits them when followImports is false).
func TestOutliner_ExtractSymbolWithDependencies_SingleLanguage(t *testing.T) {
	o := NewOutliner()
	chdirTest(t, importProjDir)

	// 1. followImports = false
	noDeps, err := o.ExtractSymbolWithDependencies("main.py", "compute", false)
	if err != nil {
		t.Fatalf("ExtractSymbolWithDependencies(followImports=false) returned error: %v", err)
	}
	if noDeps.Name != "compute" {
		t.Errorf("got symbol name %q, want 'compute'", noDeps.Name)
	}
	if len(noDeps.Dependencies) != 0 {
		t.Errorf("expected 0 dependencies when followImports=false, got %d", len(noDeps.Dependencies))
	}

	// 2. followImports = true
	withDeps, err := o.ExtractSymbolWithDependencies("main.py", "compute", true)
	if err != nil {
		t.Fatalf("ExtractSymbolWithDependencies(followImports=true) returned error: %v", err)
	}
	if withDeps.Name != "compute" {
		t.Errorf("got symbol name %q, want 'compute'", withDeps.Name)
	}
	if len(withDeps.Dependencies) == 0 {
		t.Fatalf("expected dependencies when followImports=true, got 0")
	}

	foundAdd := false
	for _, dep := range withDeps.Dependencies {
		if dep.Name == "add" {
			foundAdd = true
			if dep.Code == nil || !strings.Contains(*dep.Code, "def add") {
				t.Errorf("expected dependency 'add' to contain code, got %+v", dep)
			}
		}
	}
	if !foundAdd {
		t.Errorf("expected dependency 'add' in dependencies list: %+v", withDeps.Dependencies)
	}
}

// TestOutliner_ExtractSymbolWithDependencies_MultiLanguage verifies that
// ExtractSymbolWithDependencies correctly resolves dependencies across Python,
// TypeScript, Go, Java, JavaScript, and Rust files.
func TestOutliner_ExtractSymbolWithDependencies_MultiLanguage(t *testing.T) {
	tmp := t.TempDir()

	// Python
	writeFile(t, filepath.Join(tmp, "py", "dep.py"), "def helper():\n    return 42\n")
	writeFile(t, filepath.Join(tmp, "py", "main.py"), "from dep import helper\n\ndef main_fn():\n    return helper()\n")

	// TypeScript
	writeFile(t, filepath.Join(tmp, "ts", "dep.ts"), "export function helper() { return 42; }\n")
	writeFile(t, filepath.Join(tmp, "ts", "main.ts"), "import { helper } from './dep';\nexport function main_fn() { return helper(); }\n")

	// Go
	writeFile(t, filepath.Join(tmp, "go", "dep", "dep.go"), "package dep\nfunc Helper() int { return 42 }\n")
	writeFile(t, filepath.Join(tmp, "go", "main.go"), "package main\nimport \"./dep\"\nfunc MainFn() int { return dep.Helper() }\n")

	// Java
	writeFile(t, filepath.Join(tmp, "java", "com", "example", "Dep.java"), "package com.example;\npublic class Dep {\n    public static int helper() { return 42; }\n}\n")
	writeFile(t, filepath.Join(tmp, "java", "com", "example", "Main.java"), "package com.example;\nimport com.example.Dep;\npublic class Main {\n    public static int mainFn() { return Dep.helper(); }\n}\n")

	// JavaScript
	writeFile(t, filepath.Join(tmp, "js", "dep.js"), "export function helper() { return 42; }\n")
	writeFile(t, filepath.Join(tmp, "js", "main.js"), "import { helper } from './dep';\nexport function main_fn() { return helper(); }\n")

	// Rust
	writeFile(t, filepath.Join(tmp, "rs", "dep.rs"), "pub fn helper() -> i32 { 42 }\n")
	writeFile(t, filepath.Join(tmp, "rs", "main.rs"), "use crate::dep::helper;\nmod dep;\npub fn main_fn() -> i32 { helper() }\n")

	tests := []struct {
		lang       string
		dir        string
		filePath   string
		symbolName string
		depName    string
	}{
		{lang: "python", dir: filepath.Join(tmp, "py"), filePath: "main.py", symbolName: "main_fn", depName: "helper"},
		{lang: "typescript", dir: filepath.Join(tmp, "ts"), filePath: "main.ts", symbolName: "main_fn", depName: "helper"},
		{lang: "go", dir: filepath.Join(tmp, "go"), filePath: "main.go", symbolName: "MainFn", depName: "Helper"},
		{lang: "java", dir: filepath.Join(tmp, "java"), filePath: filepath.Join("com", "example", "Main.java"), symbolName: "Main.mainFn", depName: "Dep"},
		{lang: "javascript", dir: filepath.Join(tmp, "js"), filePath: "main.js", symbolName: "main_fn", depName: "helper"},
		{lang: "rust", dir: filepath.Join(tmp, "rs"), filePath: "main.rs", symbolName: "main_fn", depName: "helper"},
	}

	o := NewOutliner()

	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			chdirTest(t, tt.dir)
			res, err := o.ExtractSymbolWithDependencies(tt.filePath, tt.symbolName, true)
			if err != nil {
				t.Fatalf("ExtractSymbolWithDependencies(%s) failed: %v", tt.lang, err)
			}
			found := false
			for _, dep := range res.Dependencies {
				if dep.Name == tt.depName {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s: expected dependency %q, got dependencies: %+v", tt.lang, tt.depName, res.Dependencies)
			}
		})
	}
}

// TestOutliner_ExtractSymbolWithDependencies_EdgeCases covers circular imports
// (which must terminate without infinite loop) and unresolvable/standard-library
// imports (which must be skipped without errors).
func TestOutliner_ExtractSymbolWithDependencies_EdgeCases(t *testing.T) {
	tmp := t.TempDir()

	// 1. Circular import
	writeFile(t, filepath.Join(tmp, "circ_a.py"), "from circ_b import func_b\n\ndef func_a():\n    return func_b()\n")
	writeFile(t, filepath.Join(tmp, "circ_b.py"), "from circ_a import func_a\n\ndef func_b():\n    return func_a()\n")

	// 2. Standard library / unresolvable import
	writeFile(t, filepath.Join(tmp, "stdlib.py"), "import os\nimport sys\n\ndef my_func():\n    return os.getcwd()\n")

	o := NewOutliner()

	t.Run("circular_import", func(t *testing.T) {
		chdirTest(t, tmp)
		res, err := o.ExtractSymbolWithDependencies("circ_a.py", "func_a", true)
		if err != nil {
			t.Fatalf("unexpected error on circular import: %v", err)
		}
		if len(res.Dependencies) != 1 || res.Dependencies[0].Name != "func_b" {
			t.Errorf("expected exactly 1 dependency 'func_b', got %+v", res.Dependencies)
		}
	})

	t.Run("unresolvable_import", func(t *testing.T) {
		chdirTest(t, tmp)
		res, err := o.ExtractSymbolWithDependencies("stdlib.py", "my_func", true)
		if err != nil {
			t.Fatalf("unexpected error on stdlib import: %v", err)
		}
		if len(res.Dependencies) != 0 {
			t.Errorf("expected 0 dependencies for stdlib imports, got %d", len(res.Dependencies))
		}
	})
}

func TestOutliner_BuildDirectoryTree_Markdown(t *testing.T) {
	tmp := t.TempDir()
	chdirTest(t, tmp)

	filePath := filepath.Join(tmp, "doc.md")
	writeFile(t, filePath, "# Header 1\n\nSome text\n\n## Subheader\n")

	o := NewOutliner()
	tree, err := o.BuildDirectoryTree(".", true, 50)
	if err != nil {
		t.Fatalf("BuildDirectoryTree failed: %v", err)
	}
	if !strings.Contains(tree, "doc.md") {
		t.Errorf("expected tree to contain doc.md, got:\n%s", tree)
	}
}

func TestOutliner_FindReferences_Markdown(t *testing.T) {
	o := NewOutliner()
	got, err := o.FindReferences("../../testdata/markdown/references", "SPECS/design.md#architecture")
	if err != nil {
		t.Fatalf("FindReferences returned unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 references, got %d: %+v", len(got), got)
	}
}

