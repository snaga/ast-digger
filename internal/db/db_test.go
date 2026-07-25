package db

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/snaga/ast-digger/internal/parser"
)

func strPtr(s string) *string { return &s }

// symbolFileCache is a small (file_path, name) pair used to make test
// assertions about GetSymbolsInDirectory results easier to read.
type symbolFileCache struct {
	filePath string
	name     string
}

func namesOf(t *testing.T, syms []parser.SymbolData) []symbolFileCache {
	t.Helper()
	out := make([]symbolFileCache, 0, len(syms))
	for _, s := range syms {
		fp := ""
		if s.FilePath != nil {
			fp = *s.FilePath
		}
		out = append(out, symbolFileCache{filePath: fp, name: s.Name})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].filePath != out[j].filePath {
			return out[i].filePath < out[j].filePath
		}
		return out[i].name < out[j].name
	})
	return out
}

func TestNewSymbolDatabase_InMemory(t *testing.T) {
	sdb, err := NewSymbolDatabase(":memory:")
	if err != nil {
		t.Fatalf("NewSymbolDatabase(:memory:) failed: %v", err)
	}
	defer func() { _ = sdb.Close() }()

	files, err := sdb.GetAllFiles()
	if err != nil {
		t.Fatalf("GetAllFiles failed: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected empty cache, got %d files", len(files))
	}
}

func TestUpdateFileCache_ReplacesSymbolsViaCascadeOnSecondCall(t *testing.T) {
	sdb, err := NewSymbolDatabase(":memory:")
	if err != nil {
		t.Fatalf("NewSymbolDatabase failed: %v", err)
	}
	defer func() { _ = sdb.Close() }()

	firstSymbols := []parser.SymbolData{
		{Name: "Foo", Kind: "class", StartLine: 1, EndLine: 10},
		{Name: "bar", Kind: "method", Parent: strPtr("Foo"), StartLine: 2, EndLine: 4, Signature: strPtr("(self, x)"), Docstring: strPtr("bar doc")},
	}
	if err := sdb.UpdateFileCache("pkg/sample.py", 100.0, "hash-v1", "python", firstSymbols); err != nil {
		t.Fatalf("UpdateFileCache (1st call) failed: %v", err)
	}

	got, err := sdb.GetSymbolsInDirectory("pkg")
	if err != nil {
		t.Fatalf("GetSymbolsInDirectory failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 symbols after first UpdateFileCache, got %d: %+v", len(got), got)
	}

	// Second call for the SAME file path with different symbols: this is the
	// critical CASCADE-delete-through-INSERT-OR-REPLACE case. The old
	// symbols (Foo, bar) must be gone and only the new ones must remain.
	secondSymbols := []parser.SymbolData{
		{Name: "baz", Kind: "function", StartLine: 1, EndLine: 3},
	}
	if err := sdb.UpdateFileCache("pkg/sample.py", 200.0, "hash-v2", "python", secondSymbols); err != nil {
		t.Fatalf("UpdateFileCache (2nd call) failed: %v", err)
	}

	got, err = sdb.GetSymbolsInDirectory("pkg")
	if err != nil {
		t.Fatalf("GetSymbolsInDirectory (after 2nd call) failed: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 symbol after replacing file cache, got %d: %+v", len(got), got)
	}
	if got[0].Name != "baz" {
		t.Fatalf("expected surviving symbol to be %q, got %q", "baz", got[0].Name)
	}

	files, err := sdb.GetAllFiles()
	if err != nil {
		t.Fatalf("GetAllFiles failed: %v", err)
	}
	meta, ok := files["pkg/sample.py"]
	if !ok {
		t.Fatalf("expected pkg/sample.py to be present in GetAllFiles, got %+v", files)
	}
	if meta.Mtime != 200.0 || meta.Hash != "hash-v2" || meta.Language != "python" {
		t.Fatalf("unexpected FileMeta after replace: %+v", meta)
	}
}

func TestGetAllFiles(t *testing.T) {
	sdb, err := NewSymbolDatabase(":memory:")
	if err != nil {
		t.Fatalf("NewSymbolDatabase failed: %v", err)
	}
	defer func() { _ = sdb.Close() }()

	if err := sdb.UpdateFileCache("a.py", 1.5, "hasha", "python", nil); err != nil {
		t.Fatalf("UpdateFileCache(a.py) failed: %v", err)
	}
	if err := sdb.UpdateFileCache("b.go", 2.5, "hashb", "go", nil); err != nil {
		t.Fatalf("UpdateFileCache(b.go) failed: %v", err)
	}

	files, err := sdb.GetAllFiles()
	if err != nil {
		t.Fatalf("GetAllFiles failed: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %+v", len(files), files)
	}
	if files["a.py"] != (FileMeta{Mtime: 1.5, Hash: "hasha", Language: "python"}) {
		t.Fatalf("unexpected metadata for a.py: %+v", files["a.py"])
	}
	if files["b.go"] != (FileMeta{Mtime: 2.5, Hash: "hashb", Language: "go"}) {
		t.Fatalf("unexpected metadata for b.go: %+v", files["b.go"])
	}
}

func TestDeleteFileCache_CascadesToSymbols(t *testing.T) {
	sdb, err := NewSymbolDatabase(":memory:")
	if err != nil {
		t.Fatalf("NewSymbolDatabase failed: %v", err)
	}
	defer func() { _ = sdb.Close() }()

	symbols := []parser.SymbolData{{Name: "Foo", Kind: "class", StartLine: 1, EndLine: 5}}
	if err := sdb.UpdateFileCache("x.py", 1.0, "h", "python", symbols); err != nil {
		t.Fatalf("UpdateFileCache failed: %v", err)
	}

	if err := sdb.DeleteFileCache("x.py"); err != nil {
		t.Fatalf("DeleteFileCache failed: %v", err)
	}

	files, err := sdb.GetAllFiles()
	if err != nil {
		t.Fatalf("GetAllFiles failed: %v", err)
	}
	if _, ok := files["x.py"]; ok {
		t.Fatalf("expected x.py to be removed from files, still present: %+v", files)
	}

	got, err := sdb.GetSymbolsInDirectory(".")
	if err != nil {
		t.Fatalf("GetSymbolsInDirectory failed: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected symbols for x.py to be cascade-deleted, got %+v", got)
	}
}

func TestGetSymbolsInDirectory(t *testing.T) {
	sdb, err := NewSymbolDatabase(":memory:")
	if err != nil {
		t.Fatalf("NewSymbolDatabase failed: %v", err)
	}
	defer func() { _ = sdb.Close() }()

	must := func(filePath string, syms []parser.SymbolData) {
		t.Helper()
		if err := sdb.UpdateFileCache(filePath, 1.0, "h", "python", syms); err != nil {
			t.Fatalf("UpdateFileCache(%s) failed: %v", filePath, err)
		}
	}
	must("pkg/a.py", []parser.SymbolData{{Name: "A", Kind: "class", StartLine: 1, EndLine: 2}})
	must("pkg/sub/b.py", []parser.SymbolData{{Name: "B", Kind: "class", StartLine: 1, EndLine: 2}})
	must("pkgother/c.py", []parser.SymbolData{{Name: "C", Kind: "class", StartLine: 1, EndLine: 2}})
	must("top.py", []parser.SymbolData{{Name: "T", Kind: "function", StartLine: 1, EndLine: 2}})

	tests := []struct {
		name    string
		dirPath string
		want    []symbolFileCache
	}{
		{
			name:    "empty dirPath returns everything",
			dirPath: "",
			want: []symbolFileCache{
				{filePath: "pkg/a.py", name: "A"},
				{filePath: "pkg/sub/b.py", name: "B"},
				{filePath: "pkgother/c.py", name: "C"},
				{filePath: "top.py", name: "T"},
			},
		},
		{
			name:    "dot dirPath returns everything",
			dirPath: ".",
			want: []symbolFileCache{
				{filePath: "pkg/a.py", name: "A"},
				{filePath: "pkg/sub/b.py", name: "B"},
				{filePath: "pkgother/c.py", name: "C"},
				{filePath: "top.py", name: "T"},
			},
		},
		{
			name:    "directory prefix matches only files under it, not lexical siblings sharing the prefix",
			dirPath: "pkg",
			want: []symbolFileCache{
				{filePath: "pkg/a.py", name: "A"},
				{filePath: "pkg/sub/b.py", name: "B"},
			},
		},
		{
			name:    "exact file path match",
			dirPath: "top.py",
			want: []symbolFileCache{
				{filePath: "top.py", name: "T"},
			},
		},
		{
			name:    "backslash-separated dirPath is normalized",
			dirPath: `pkg\sub`,
			want: []symbolFileCache{
				{filePath: "pkg/sub/b.py", name: "B"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sdb.GetSymbolsInDirectory(tt.dirPath)
			if err != nil {
				t.Fatalf("GetSymbolsInDirectory(%q) failed: %v", tt.dirPath, err)
			}
			gotSorted := namesOf(t, got)
			if len(gotSorted) != len(tt.want) {
				t.Fatalf("GetSymbolsInDirectory(%q) = %+v, want %+v", tt.dirPath, gotSorted, tt.want)
			}
			for i := range gotSorted {
				if gotSorted[i] != tt.want[i] {
					t.Fatalf("GetSymbolsInDirectory(%q) = %+v, want %+v", tt.dirPath, gotSorted, tt.want)
				}
			}
		})
	}
}

func TestGetSymbolsInDirectory_PreservesSignatureAndDocstring(t *testing.T) {
	sdb, err := NewSymbolDatabase(":memory:")
	if err != nil {
		t.Fatalf("NewSymbolDatabase failed: %v", err)
	}
	defer func() { _ = sdb.Close() }()

	symbols := []parser.SymbolData{
		{
			Name:      "bar",
			Kind:      "method",
			Parent:    strPtr("Foo"),
			StartLine: 2,
			EndLine:   4,
			Signature: strPtr("(self, x: int) -> int"),
			Docstring: strPtr("Adds one to x."),
		},
		{
			Name:      "baz",
			Kind:      "function",
			StartLine: 6,
			EndLine:   7,
			// Parent, Signature, Docstring intentionally left nil to verify
			// NULL round-tripping.
		},
	}
	if err := sdb.UpdateFileCache("m.py", 1.0, "h", "python", symbols); err != nil {
		t.Fatalf("UpdateFileCache failed: %v", err)
	}

	got, err := sdb.GetSymbolsInDirectory(".")
	if err != nil {
		t.Fatalf("GetSymbolsInDirectory failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 symbols, got %d", len(got))
	}

	byName := map[string]parser.SymbolData{}
	for _, s := range got {
		byName[s.Name] = s
	}

	bar, ok := byName["bar"]
	if !ok {
		t.Fatalf("expected symbol 'bar' in result: %+v", got)
	}
	if bar.Parent == nil || *bar.Parent != "Foo" {
		t.Errorf("bar.Parent = %v, want Foo", bar.Parent)
	}
	if bar.Signature == nil || *bar.Signature != "(self, x: int) -> int" {
		t.Errorf("bar.Signature = %v, want signature", bar.Signature)
	}
	if bar.Docstring == nil || *bar.Docstring != "Adds one to x." {
		t.Errorf("bar.Docstring = %v, want docstring", bar.Docstring)
	}

	baz, ok := byName["baz"]
	if !ok {
		t.Fatalf("expected symbol 'baz' in result: %+v", got)
	}
	if baz.Parent != nil {
		t.Errorf("baz.Parent = %v, want nil", baz.Parent)
	}
	if baz.Signature != nil {
		t.Errorf("baz.Signature = %v, want nil", baz.Signature)
	}
	if baz.Docstring != nil {
		t.Errorf("baz.Docstring = %v, want nil", baz.Docstring)
	}
}

func TestUpdateFileMtime(t *testing.T) {
	sdb, err := NewSymbolDatabase(":memory:")
	if err != nil {
		t.Fatalf("NewSymbolDatabase failed: %v", err)
	}
	defer func() { _ = sdb.Close() }()

	if err := sdb.UpdateFileCache("f.py", 1.0, "h", "python", nil); err != nil {
		t.Fatalf("UpdateFileCache failed: %v", err)
	}
	if err := sdb.UpdateFileMtime("f.py", 42.5); err != nil {
		t.Fatalf("UpdateFileMtime failed: %v", err)
	}

	files, err := sdb.GetAllFiles()
	if err != nil {
		t.Fatalf("GetAllFiles failed: %v", err)
	}
	meta, ok := files["f.py"]
	if !ok {
		t.Fatalf("expected f.py to be present")
	}
	if meta.Mtime != 42.5 {
		t.Fatalf("expected mtime 42.5, got %v", meta.Mtime)
	}
	if meta.Hash != "h" || meta.Language != "python" {
		t.Fatalf("UpdateFileMtime unexpectedly changed hash/language: %+v", meta)
	}
}

// TestNewSymbolDatabase_AutoRepairsCorruptSchema verifies that opening a
// file-backed database whose schema is missing required tables/columns
// causes the file to be silently deleted and rebuilt from scratch (mirroring
// Python's SymbolDatabase._init_db() self-healing behavior), rather than
// NewSymbolDatabase returning an error.
func TestNewSymbolDatabase_AutoRepairsCorruptSchema(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, dbPath string)
	}{
		{
			name: "missing symbols table",
			setup: func(t *testing.T, dbPath string) {
				conn, err := sql.Open("sqlite", dbPath)
				if err != nil {
					t.Fatalf("failed to open %s: %v", dbPath, err)
				}
				defer func() { _ = conn.Close() }()
				if _, err := conn.Exec(`CREATE TABLE files (
					path TEXT PRIMARY KEY,
					mtime REAL NOT NULL,
					hash TEXT NOT NULL,
					language TEXT NOT NULL
				);`); err != nil {
					t.Fatalf("failed to create files table: %v", err)
				}
			},
		},
		{
			name: "missing language column in files",
			setup: func(t *testing.T, dbPath string) {
				conn, err := sql.Open("sqlite", dbPath)
				if err != nil {
					t.Fatalf("failed to open %s: %v", dbPath, err)
				}
				defer func() { _ = conn.Close() }()
				if _, err := conn.Exec(`CREATE TABLE files (
					path TEXT PRIMARY KEY,
					mtime REAL NOT NULL,
					hash TEXT NOT NULL
				);`); err != nil {
					t.Fatalf("failed to create files table: %v", err)
				}
				if _, err := conn.Exec(`CREATE TABLE symbols (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					file_path TEXT NOT NULL,
					name TEXT NOT NULL,
					kind TEXT NOT NULL,
					parent TEXT,
					start_line INTEGER NOT NULL,
					end_line INTEGER NOT NULL,
					signature TEXT,
					docstring TEXT
				);`); err != nil {
					t.Fatalf("failed to create symbols table: %v", err)
				}
			},
		},
		{
			name: "outdated symbols schema missing signature/docstring",
			setup: func(t *testing.T, dbPath string) {
				conn, err := sql.Open("sqlite", dbPath)
				if err != nil {
					t.Fatalf("failed to open %s: %v", dbPath, err)
				}
				defer func() { _ = conn.Close() }()
				if _, err := conn.Exec(`CREATE TABLE files (
					path TEXT PRIMARY KEY,
					mtime REAL NOT NULL,
					hash TEXT NOT NULL,
					language TEXT NOT NULL
				);`); err != nil {
					t.Fatalf("failed to create files table: %v", err)
				}
				if _, err := conn.Exec(`CREATE TABLE symbols (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					file_path TEXT NOT NULL,
					name TEXT NOT NULL,
					kind TEXT NOT NULL,
					parent TEXT,
					start_line INTEGER NOT NULL,
					end_line INTEGER NOT NULL
				);`); err != nil {
					t.Fatalf("failed to create symbols table: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "symbols.db")
			tt.setup(t, dbPath)

			sdb, err := NewSymbolDatabase(dbPath)
			if err != nil {
				t.Fatalf("NewSymbolDatabase should self-heal rather than error, got: %v", err)
			}
			defer func() { _ = sdb.Close() }()

			// The rebuilt database must have a fully working schema: writes
			// and reads should succeed with no leftover data from before the
			// repair (the old, incompatible files/symbols content is gone).
			if err := sdb.UpdateFileCache("new.py", 1.0, "h", "python", []parser.SymbolData{
				{Name: "New", Kind: "class", StartLine: 1, EndLine: 2},
			}); err != nil {
				t.Fatalf("UpdateFileCache after repair failed: %v", err)
			}
			files, err := sdb.GetAllFiles()
			if err != nil {
				t.Fatalf("GetAllFiles after repair failed: %v", err)
			}
			if len(files) != 1 {
				t.Fatalf("expected exactly the freshly-written file after repair, got %+v", files)
			}
		})
	}
}

// TestNewSymbolDatabase_ValidExistingSchemaIsPreserved verifies that opening
// a valid, pre-existing database file does NOT trigger the delete/rebuild
// path and that its data survives the reopen.
func TestNewSymbolDatabase_ValidExistingSchemaIsPreserved(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "symbols.db")

	sdb1, err := NewSymbolDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewSymbolDatabase (1st open) failed: %v", err)
	}
	if err := sdb1.UpdateFileCache("keep.py", 1.0, "h", "python", []parser.SymbolData{
		{Name: "Keep", Kind: "class", StartLine: 1, EndLine: 2},
	}); err != nil {
		t.Fatalf("UpdateFileCache failed: %v", err)
	}
	if err := sdb1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	sdb2, err := NewSymbolDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewSymbolDatabase (2nd open) failed: %v", err)
	}
	defer func() { _ = sdb2.Close() }()

	files, err := sdb2.GetAllFiles()
	if err != nil {
		t.Fatalf("GetAllFiles failed: %v", err)
	}
	if _, ok := files["keep.py"]; !ok {
		t.Fatalf("expected keep.py to survive reopen of a valid schema, got %+v", files)
	}
}

// pythonSchemaSQL is a verbatim copy of the DDL executed by Python's
// SymbolDatabase._init_db() (ast_digger/db.py), used as a schema-fidelity
// fallback for the shared-file compatibility test (requirement J-2) when the
// Python CLI itself is not available in the test environment (see
// TestPythonCLICompatibility, which prefers exercising the real `uv run
// ast-digger` CLI when possible).
const pythonSchemaSQL = `
CREATE TABLE IF NOT EXISTS files (
    path TEXT PRIMARY KEY,
    mtime REAL NOT NULL,
    hash TEXT NOT NULL,
    language TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS symbols (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_path TEXT NOT NULL,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    parent TEXT,
    start_line INTEGER NOT NULL,
    end_line INTEGER NOT NULL,
    signature TEXT,
    docstring TEXT,
    FOREIGN KEY (file_path) REFERENCES files(path) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_symbols_name ON symbols(name);
CREATE INDEX IF NOT EXISTS idx_symbols_file_path ON symbols(file_path);
`

// TestSharedSchemaCompatibility_PythonDDL builds a database file using the
// exact DDL that Python's SymbolDatabase._init_db() executes (and inserts a
// row the way Python's update_file_cache()/INSERT statements do), then
// verifies the Go SymbolDatabase can open it without triggering schema
// repair and can read/append data. This is a CLI-independent stand-in for
// the shared-file compatibility requirement (J-2); see
// TestPythonCLICompatibility below for the end-to-end version that shells
// out to the real Python CLI when `uv` is available.
func TestSharedSchemaCompatibility_PythonDDL(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "symbols.db")

	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open %s: %v", dbPath, err)
	}
	for _, stmt := range splitStatements(pythonSchemaSQL) {
		if _, err := conn.Exec(stmt); err != nil {
			_ = conn.Close()
			t.Fatalf("failed to execute Python DDL statement %q: %v", stmt, err)
		}
	}
	if _, err := conn.Exec(
		"INSERT INTO files (path, mtime, hash, language) VALUES (?, ?, ?, ?);",
		"legacy.py", 111.0, "legacy-hash", "python",
	); err != nil {
		_ = conn.Close()
		t.Fatalf("failed to insert legacy file row: %v", err)
	}
	if _, err := conn.Exec(
		`INSERT INTO symbols (file_path, name, kind, parent, start_line, end_line, signature, docstring)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?);`,
		"legacy.py", "LegacyClass", "class", nil, 1, 5, nil, "legacy docstring",
	); err != nil {
		_ = conn.Close()
		t.Fatalf("failed to insert legacy symbol row: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("failed to close legacy db: %v", err)
	}

	sdb, err := NewSymbolDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewSymbolDatabase should open a Python-schema-compatible DB without error, got: %v", err)
	}
	defer func() { _ = sdb.Close() }()

	files, err := sdb.GetAllFiles()
	if err != nil {
		t.Fatalf("GetAllFiles failed: %v", err)
	}
	meta, ok := files["legacy.py"]
	if !ok || meta != (FileMeta{Mtime: 111.0, Hash: "legacy-hash", Language: "python"}) {
		t.Fatalf("expected legacy.py metadata to be preserved (no schema repair), got %+v (present=%v)", meta, ok)
	}

	syms, err := sdb.GetSymbolsInDirectory(".")
	if err != nil {
		t.Fatalf("GetSymbolsInDirectory failed: %v", err)
	}
	if len(syms) != 1 || syms[0].Name != "LegacyClass" {
		t.Fatalf("expected the pre-existing LegacyClass symbol to be readable, got %+v", syms)
	}

	// Append a second file's worth of symbols the way Go would during normal
	// operation, and confirm both the legacy and the new data coexist.
	if err := sdb.UpdateFileCache("new.py", 222.0, "new-hash", "python", []parser.SymbolData{
		{Name: "NewFunc", Kind: "function", StartLine: 1, EndLine: 2},
	}); err != nil {
		t.Fatalf("UpdateFileCache failed: %v", err)
	}
	syms, err = sdb.GetSymbolsInDirectory(".")
	if err != nil {
		t.Fatalf("GetSymbolsInDirectory (after append) failed: %v", err)
	}
	if len(syms) != 2 {
		t.Fatalf("expected legacy + new symbols to coexist, got %+v", syms)
	}
}

// splitStatements splits a semicolon-terminated SQL script into individual
// statements for sequential execution (mirroring how sqlite3.Connection.
// executescript() would run the Python DDL, since database/sql's Exec only
// runs a single statement per call for the sqlite driver used here).
func splitStatements(script string) []string {
	var stmts []string
	var cur []byte
	for i := 0; i < len(script); i++ {
		c := script[i]
		cur = append(cur, c)
		if c == ';' {
			s := trimSpace(string(cur))
			if s != "" {
				stmts = append(stmts, s)
			}
			cur = nil
		}
	}
	if s := trimSpace(string(cur)); s != "" {
		stmts = append(stmts, s)
	}
	return stmts
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && isSpace(s[start]) {
		start++
	}
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// TestPythonCLICompatibility drives the real Python CLI (`uv run
// ast-digger`) to produce a `.workspace_cache/symbols.db` the way an actual
// end user's workflow would, then verifies the Go SymbolDatabase can open
// and append to that exact file, and that the Python CLI can subsequently
// read the Go-modified database back without error (requirement J-2: shared
// cache file compatibility). The test is skipped if `uv` is not available in
// the environment executing the test.
func TestPythonCLICompatibility(t *testing.T) {
	uvPath, err := exec.LookPath("uv")
	if err != nil {
		t.Skip("uv not found on PATH; skipping Python CLI shared-file compatibility test")
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("failed to resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "pyproject.toml")); err != nil {
		t.Skipf("could not locate repo root pyproject.toml at %s; skipping: %v", repoRoot, err)
	}

	projectDir := t.TempDir()
	samplePy := "class Foo:\n    \"\"\"Foo docstring.\"\"\"\n\n    def bar(self, x):\n        \"\"\"Bar docstring.\"\"\"\n        return x + 1\n\n\ndef baz():\n    return 42\n"
	if err := os.WriteFile(filepath.Join(projectDir, "sample.py"), []byte(samplePy), 0o644); err != nil {
		t.Fatalf("failed to write sample.py: %v", err)
	}

	runPython := func(args ...string) {
		t.Helper()
		fullArgs := append([]string{"run", "--project", repoRoot, "ast-digger"}, args...)
		cmd := exec.Command(uvPath, fullArgs...)
		cmd.Dir = projectDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("uv run ast-digger %v failed: %v\noutput:\n%s", args, err, out)
		}
	}

	// Step 1: let the Python CLI create .workspace_cache/symbols.db with its
	// own schema/data by scanning the temp project directory.
	runPython("directory", ".")

	dbPath := filepath.Join(projectDir, ".workspace_cache", "symbols.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected Python CLI to create %s: %v", dbPath, err)
	}

	// Step 2: open the Python-created database with the Go implementation
	// and confirm it reads the symbols the Python parser found, without
	// triggering the schema-repair path (which would silently wipe the data
	// Python produced).
	sdb, err := NewSymbolDatabase(dbPath)
	if err != nil {
		t.Fatalf("Go NewSymbolDatabase failed to open Python-created database: %v", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = sdb.Close()
		}
	}()

	files, err := sdb.GetAllFiles()
	if err != nil {
		t.Fatalf("GetAllFiles failed: %v", err)
	}
	if _, ok := files["sample.py"]; !ok {
		t.Fatalf("expected sample.py to be present in Python-created cache, got %+v", files)
	}

	syms, err := sdb.GetSymbolsInDirectory(".")
	if err != nil {
		t.Fatalf("GetSymbolsInDirectory failed: %v", err)
	}
	var haveFoo, haveBar, haveBaz bool
	for _, s := range syms {
		switch s.Name {
		case "Foo":
			haveFoo = true
		case "bar":
			haveBar = true
		case "baz":
			haveBaz = true
		}
	}
	if !haveFoo || !haveBar || !haveBaz {
		t.Fatalf("expected Foo/bar/baz symbols parsed by Python to be readable via Go, got %+v", syms)
	}

	// Step 3: append a Go-authored file entry to the same database.
	if err := sdb.UpdateFileCache("go_added.py", 999.0, "go-hash", "python", []parser.SymbolData{
		{Name: "GoAdded", Kind: "function", StartLine: 1, EndLine: 1},
	}); err != nil {
		t.Fatalf("UpdateFileCache (Go append) failed: %v", err)
	}
	if err := sdb.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	closed = true

	// Step 4: hand the database back to the Python CLI and confirm it still
	// opens/operates on the Go-modified file without error (round trip).
	runPython("directory", ".")
}

// --- Error-path coverage below (task: raise internal/db coverage to >=90%).
//
// These tests exercise the modernc.org/sqlite driver's actual observed
// behavior for a handful of "invalid database file" scenarios rather than
// mocking database/sql, per the reproduction steps verified experimentally
// for this task:
//
//  1. Passing a directory path as dbPath: sql.Open succeeds lazily, but the
//     first statement executed against it (connect's "PRAGMA foreign_keys =
//     ON;") fails with "unable to open database file", since SQLite cannot
//     open a directory as a database file.
//  2. Passing a path to a non-SQLite ("garbage") file: the PRAGMA succeeds
//     (it never touches the file's page contents), but any statement that
//     reads/writes the SQLite page structure -- including a plain
//     "SELECT ... FROM sqlite_master" -- fails with "file is not a
//     database". This means checkSchema (which lists tables via
//     sqlite_master) treats a garbage file exactly like a corrupted schema:
//     repairSchemaIfNeeded deletes it and NewSymbolDatabase transparently
//     rebuilds a fresh, valid database in its place, so NewSymbolDatabase
//     itself never surfaces this error for a garbage *file*. createSchema's
//     own error branch is instead exercised directly here, and
//     NewSymbolDatabase's createSchema-failure branch is exercised via a
//     schema that passes checkSchema (valid files/symbols tables/columns)
//     but pre-occupies one of createSchema's index names with a
//     conflicting table, which checkSchema does not (and Python's
//     equivalent _init_db does not) inspect.
//  3. Operating on a *SymbolDatabase after Close(): database/sql surfaces
//     "sql: database is closed" for any subsequent query/exec/Begin call.

// TestConnect_DirectoryPathReturnsError verifies that connect() surfaces the
// underlying driver error (rather than panicking or silently succeeding)
// when dbPath refers to a directory instead of a file.
func TestConnect_DirectoryPathReturnsError(t *testing.T) {
	dirPath := filepath.Join(t.TempDir(), "not-a-file")
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatalf("os.Mkdir failed: %v", err)
	}

	conn, err := connect(dirPath)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("connect(%q) succeeded unexpectedly, want error", dirPath)
	}
	if !strings.Contains(err.Error(), "unable to open database file") {
		t.Fatalf("connect(%q) error = %v, want it to mention 'unable to open database file'", dirPath, err)
	}
}

// TestNewSymbolDatabase_ConnectFailureReturnsWrappedError verifies that when
// connect(dbPath) fails inside NewSymbolDatabase (here: dbPath's parent
// directory does not exist, so os.Stat inside repairSchemaIfNeeded reports
// "not found" and the repair step is skipped as a no-op, isolating the
// failure to NewSymbolDatabase's own connect(dbPath) call), the error is
// wrapped with the dbPath and returned rather than panicking or silently
// producing a broken *SymbolDatabase.
func TestNewSymbolDatabase_ConnectFailureReturnsWrappedError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "missing-parent-dir", "symbols.db")

	sdb, err := NewSymbolDatabase(dbPath)
	if err == nil {
		_ = sdb.Close()
		t.Fatalf("NewSymbolDatabase(%q) succeeded unexpectedly, want error", dbPath)
	}
	if !strings.Contains(err.Error(), "db: failed to open") {
		t.Fatalf("NewSymbolDatabase(%q) error = %v, want it to start with 'db: failed to open'", dbPath, err)
	}
	if !strings.Contains(err.Error(), "unable to open database file") {
		t.Fatalf("NewSymbolDatabase(%q) error = %v, want it to mention the underlying driver error", dbPath, err)
	}
}

// TestNewSymbolDatabase_CreateSchemaFailureClosesConnectionAndReturnsError
// verifies that when createSchema fails inside NewSymbolDatabase, the
// connection it just opened is closed and a wrapped error is returned
// (rather than leaking the connection or returning a half-initialized
// *SymbolDatabase). The pre-existing database file here has a fully valid
// files/symbols schema (so checkSchema/repairSchemaIfNeeded treat it as
// healthy and do not delete it) but also has an unrelated table squatting on
// the name createSchema wants to use for one of its indexes, which SQLite
// rejects because table and index names share a single per-database
// namespace.
func TestNewSymbolDatabase_CreateSchemaFailureClosesConnectionAndReturnsError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "index-name-conflict.db")

	setupConn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open %s: %v", dbPath, err)
	}
	setupStmts := []string{
		`CREATE TABLE files (
			path TEXT PRIMARY KEY,
			mtime REAL NOT NULL,
			hash TEXT NOT NULL,
			language TEXT NOT NULL
		);`,
		`CREATE TABLE symbols (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			file_path TEXT NOT NULL,
			name TEXT NOT NULL,
			kind TEXT NOT NULL,
			parent TEXT,
			start_line INTEGER NOT NULL,
			end_line INTEGER NOT NULL,
			signature TEXT,
			docstring TEXT
		);`,
		// Occupies the name createSchema's "CREATE INDEX IF NOT EXISTS
		// idx_symbols_name ..." wants to use, with an incompatible object
		// type (a table, not an index), which checkSchema never inspects.
		`CREATE TABLE idx_symbols_name (x INTEGER);`,
	}
	for _, stmt := range setupStmts {
		if _, err := setupConn.Exec(stmt); err != nil {
			_ = setupConn.Close()
			t.Fatalf("setup statement failed: %v", err)
		}
	}
	if err := setupConn.Close(); err != nil {
		t.Fatalf("failed to close setup connection: %v", err)
	}

	sdb, err := NewSymbolDatabase(dbPath)
	if err == nil {
		_ = sdb.Close()
		t.Fatalf("NewSymbolDatabase(%q) succeeded unexpectedly, want error", dbPath)
	}
	if !strings.Contains(err.Error(), "db: failed to initialize schema") {
		t.Fatalf("NewSymbolDatabase(%q) error = %v, want it to start with 'db: failed to initialize schema'", dbPath, err)
	}
}

// TestRepairSchemaIfNeeded_NoOpWhenPathMissing verifies the "file does not
// exist" fast path returns nil without attempting to connect/inspect
// anything.
func TestRepairSchemaIfNeeded_NoOpWhenPathMissing(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "does-not-exist.db")
	if err := repairSchemaIfNeeded(dbPath); err != nil {
		t.Fatalf("repairSchemaIfNeeded(%q) = %v, want nil for a missing path", dbPath, err)
	}
}

// TestRepairSchemaIfNeeded_ConnectFailureRemovesUnreadablePath verifies the
// corrupted=true branch taken when connect(dbPath) itself fails (as opposed
// to connecting successfully but failing checkSchema): dbPath exists (an
// empty directory created via os.Mkdir) so os.Stat succeeds, but connect()
// fails to open it as a database, and repairSchemaIfNeeded successfully
// removes the offending path so that a fresh database can be created in its
// place.
func TestRepairSchemaIfNeeded_ConnectFailureRemovesUnreadablePath(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "unreadable-path")
	if err := os.Mkdir(dbPath, 0o755); err != nil {
		t.Fatalf("os.Mkdir failed: %v", err)
	}

	if err := repairSchemaIfNeeded(dbPath); err != nil {
		t.Fatalf("repairSchemaIfNeeded(%q) = %v, want nil (path should be removed and repair considered successful)", dbPath, err)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("expected %q to have been removed by repairSchemaIfNeeded, stat err = %v", dbPath, err)
	}
}

// TestRepairSchemaIfNeeded_RemoveFailureReturnsWrappedError verifies that
// when repairSchemaIfNeeded decides dbPath is corrupted but os.Remove(dbPath)
// itself fails (here: dbPath is a non-empty directory, which os.Remove
// refuses to delete), the removal error is wrapped and returned rather than
// silently ignored.
func TestRepairSchemaIfNeeded_RemoveFailureReturnsWrappedError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "non-empty-dir")
	if err := os.Mkdir(dbPath, 0o755); err != nil {
		t.Fatalf("os.Mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dbPath, "inner.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("failed to populate %s: %v", dbPath, err)
	}

	err := repairSchemaIfNeeded(dbPath)
	if err == nil {
		t.Fatalf("repairSchemaIfNeeded(%q) succeeded unexpectedly, want error since the directory is not empty", dbPath)
	}
	if !strings.Contains(err.Error(), "db: failed to remove corrupted database") {
		t.Fatalf("repairSchemaIfNeeded(%q) error = %v, want it to start with 'db: failed to remove corrupted database'", dbPath, err)
	}
	if _, statErr := os.Stat(dbPath); statErr != nil {
		t.Fatalf("expected %q to still exist after a failed removal, stat err = %v", dbPath, statErr)
	}
}

// writeGarbageFile writes a file at path that is neither empty nor a valid
// SQLite database, matching the "non-SQLite garbage file" reproduction
// scenario used throughout this test file.
func writeGarbageFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("not a sqlite file"), 0o644); err != nil {
		t.Fatalf("failed to write garbage file %s: %v", path, err)
	}
}

// TestCreateSchema_NonSQLiteFileReturnsError verifies createSchema's error
// branch directly: connecting to a garbage (non-SQLite) file succeeds
// (PRAGMA foreign_keys never touches the page structure), but createSchema's
// first "CREATE TABLE" statement fails because the file is not a valid
// SQLite database.
func TestCreateSchema_NonSQLiteFileReturnsError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "garbage.db")
	writeGarbageFile(t, dbPath)

	conn, err := connect(dbPath)
	if err != nil {
		t.Fatalf("connect(%q) failed unexpectedly: %v (want PRAGMA to succeed on a garbage file)", dbPath, err)
	}
	defer func() { _ = conn.Close() }()

	if err := createSchema(conn); err == nil {
		t.Fatalf("createSchema succeeded unexpectedly for a garbage file, want error")
	} else if !strings.Contains(err.Error(), "file is not a database") {
		t.Fatalf("createSchema error = %v, want it to mention 'file is not a database'", err)
	}
}

// TestNewSymbolDatabase_GarbageFileSelfHeals documents (and verifies) that,
// unlike a directory path, a pre-existing garbage file at dbPath does NOT
// surface an error from NewSymbolDatabase: repairSchemaIfNeeded's
// checkSchema call fails to even list tables (the same "file is not a
// database" error createSchema would hit), so the file is treated as
// corrupted and deleted before NewSymbolDatabase's own connect+createSchema
// run against a freshly created, valid database file.
func TestNewSymbolDatabase_GarbageFileSelfHeals(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "garbage.db")
	writeGarbageFile(t, dbPath)

	sdb, err := NewSymbolDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewSymbolDatabase(%q) = %v, want the garbage file to be self-healed like a corrupted schema", dbPath, err)
	}
	defer func() { _ = sdb.Close() }()

	files, err := sdb.GetAllFiles()
	if err != nil {
		t.Fatalf("GetAllFiles after self-heal failed: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected an empty, freshly-rebuilt cache after self-heal, got %+v", files)
	}
}

// TestCheckSchema_ClosedConnectionReturnsError, TestTableNames_..., and
// TestColumnNames_... exercise the query-failure branches of checkSchema/
// tableNames/columnNames (as opposed to the "missing table/column" branches
// already covered by TestNewSymbolDatabase_AutoRepairsCorruptSchema), by
// running each function directly against a connection that has already been
// closed.
func TestTableNames_ClosedConnectionReturnsError(t *testing.T) {
	conn, err := connect(":memory:")
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if _, err := tableNames(conn); err == nil {
		t.Fatalf("tableNames on a closed connection succeeded unexpectedly")
	}
}

func TestColumnNames_ClosedConnectionReturnsError(t *testing.T) {
	conn, err := connect(":memory:")
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if _, err := columnNames(conn, "files"); err == nil {
		t.Fatalf("columnNames on a closed connection succeeded unexpectedly")
	}
}

func TestCheckSchema_ClosedConnectionReturnsError(t *testing.T) {
	conn, err := connect(":memory:")
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if err := checkSchema(conn); err == nil {
		t.Fatalf("checkSchema on a closed connection succeeded unexpectedly")
	} else if !strings.Contains(err.Error(), "db: failed to list tables") {
		t.Fatalf("checkSchema error = %v, want it to start with 'db: failed to list tables'", err)
	}
}

// TestSymbolDatabaseMethods_AfterCloseReturnErrors verifies that every
// *SymbolDatabase method that issues a query/exec against db.conn returns a
// wrapped error (rather than panicking) once the underlying connection has
// been closed, matching the documented "sql: database is closed" behavior
// of database/sql.
func TestSymbolDatabaseMethods_AfterCloseReturnErrors(t *testing.T) {
	sdb, err := NewSymbolDatabase(":memory:")
	if err != nil {
		t.Fatalf("NewSymbolDatabase failed: %v", err)
	}
	if err := sdb.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if _, err := sdb.GetAllFiles(); err == nil {
		t.Error("GetAllFiles after Close succeeded unexpectedly")
	} else if !strings.Contains(err.Error(), "db: GetAllFiles") {
		t.Errorf("GetAllFiles error = %v, want it prefixed with 'db: GetAllFiles'", err)
	}

	if err := sdb.UpdateFileCache("f.py", 1.0, "h", "python", nil); err == nil {
		t.Error("UpdateFileCache after Close succeeded unexpectedly")
	} else if !strings.Contains(err.Error(), "db: UpdateFileCache") {
		t.Errorf("UpdateFileCache error = %v, want it prefixed with 'db: UpdateFileCache'", err)
	}

	if err := sdb.DeleteFileCache("f.py"); err == nil {
		t.Error("DeleteFileCache after Close succeeded unexpectedly")
	} else if !strings.Contains(err.Error(), "db: DeleteFileCache") {
		t.Errorf("DeleteFileCache error = %v, want it prefixed with 'db: DeleteFileCache'", err)
	}

	if _, err := sdb.GetSymbolsInDirectory("."); err == nil {
		t.Error("GetSymbolsInDirectory after Close succeeded unexpectedly")
	} else if !strings.Contains(err.Error(), "db: GetSymbolsInDirectory") {
		t.Errorf("GetSymbolsInDirectory error = %v, want it prefixed with 'db: GetSymbolsInDirectory'", err)
	}

	if err := sdb.UpdateFileMtime("f.py", 2.0); err == nil {
		t.Error("UpdateFileMtime after Close succeeded unexpectedly")
	} else if !strings.Contains(err.Error(), "db: UpdateFileMtime") {
		t.Errorf("UpdateFileMtime error = %v, want it prefixed with 'db: UpdateFileMtime'", err)
	}
}

// TestGetAllFiles_ScanFailureReturnsError verifies GetAllFiles' row-scan
// error branch: a "files.mtime" value that cannot be converted to float64
// (here, stored as-is because SQLite's REAL column affinity leaves
// non-numeric TEXT untouched rather than forcing a conversion) causes
// rows.Scan to fail, and that failure is wrapped and returned rather than
// silently producing zero-valued/incorrect FileMeta.
func TestGetAllFiles_ScanFailureReturnsError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "badtype.db")
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open %s: %v", dbPath, err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Exec(`CREATE TABLE files (path TEXT PRIMARY KEY, mtime REAL NOT NULL, hash TEXT NOT NULL, language TEXT NOT NULL);`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO files (path, mtime, hash, language) VALUES ('a.py', 'not-a-number', 'h', 'python');`); err != nil {
		t.Fatalf("setup insert failed: %v", err)
	}

	sdb := &SymbolDatabase{dbPath: dbPath, conn: conn}
	if _, err := sdb.GetAllFiles(); err == nil {
		t.Fatalf("GetAllFiles succeeded unexpectedly with a non-numeric mtime value")
	} else if !strings.Contains(err.Error(), "db: GetAllFiles scan failed") {
		t.Fatalf("GetAllFiles error = %v, want it prefixed with 'db: GetAllFiles scan failed'", err)
	}
}

// TestUpdateFileCache_FilesUpsertFailureReturnsError verifies the
// "failed to upsert file" error branch: a files table missing the hash
// column createSchema would normally create (constructed directly here,
// bypassing NewSymbolDatabase, to isolate the scenario) causes the initial
// "INSERT OR REPLACE INTO files ..." statement to fail.
func TestUpdateFileCache_FilesUpsertFailureReturnsError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "missing-hash-column.db")
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open %s: %v", dbPath, err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Exec(`CREATE TABLE files (path TEXT PRIMARY KEY, mtime REAL NOT NULL, language TEXT NOT NULL);`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if _, err := conn.Exec(`CREATE TABLE symbols (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		file_path TEXT NOT NULL,
		name TEXT NOT NULL,
		kind TEXT NOT NULL,
		parent TEXT,
		start_line INTEGER NOT NULL,
		end_line INTEGER NOT NULL,
		signature TEXT,
		docstring TEXT
	);`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	sdb := &SymbolDatabase{dbPath: dbPath, conn: conn}
	if err := sdb.UpdateFileCache("a.py", 1.0, "h", "python", nil); err == nil {
		t.Fatalf("UpdateFileCache succeeded unexpectedly against a files table missing the hash column")
	} else if !strings.Contains(err.Error(), "failed to upsert file") {
		t.Fatalf("UpdateFileCache error = %v, want it to mention 'failed to upsert file'", err)
	}
}

// TestUpdateFileCache_SymbolInsertFailureReturnsError verifies the
// "failed to insert symbol" error branch: a symbols table missing the kind
// column (constructed directly, bypassing NewSymbolDatabase) causes the
// per-symbol INSERT statement -- which runs only after the files upsert has
// already succeeded -- to fail.
func TestUpdateFileCache_SymbolInsertFailureReturnsError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "missing-kind-column.db")
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open %s: %v", dbPath, err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Exec(`CREATE TABLE files (path TEXT PRIMARY KEY, mtime REAL NOT NULL, hash TEXT NOT NULL, language TEXT NOT NULL);`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if _, err := conn.Exec(`CREATE TABLE symbols (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		file_path TEXT NOT NULL,
		name TEXT NOT NULL,
		parent TEXT,
		start_line INTEGER NOT NULL,
		end_line INTEGER NOT NULL,
		signature TEXT,
		docstring TEXT
	);`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	sdb := &SymbolDatabase{dbPath: dbPath, conn: conn}
	err = sdb.UpdateFileCache("a.py", 1.0, "h", "python", []parser.SymbolData{
		{Name: "Foo", Kind: "class", StartLine: 1, EndLine: 2},
	})
	if err == nil {
		t.Fatalf("UpdateFileCache succeeded unexpectedly against a symbols table missing the kind column")
	} else if !strings.Contains(err.Error(), "failed to insert symbol") {
		t.Fatalf("UpdateFileCache error = %v, want it to mention 'failed to insert symbol'", err)
	}

	// The files upsert must have already committed before the symbol insert
	// failed... except the whole transaction is rolled back on error, so the
	// files row must NOT be visible afterwards either.
	files, err := sdb.GetAllFiles()
	if err != nil {
		t.Fatalf("GetAllFiles failed: %v", err)
	}
	if _, ok := files["a.py"]; ok {
		t.Fatalf("expected the files upsert to be rolled back alongside the failed symbol insert, got %+v", files)
	}
}

// TestGetSymbolsInDirectory_NonDotDirPathAfterCloseReturnsError additionally
// exercises the "dirPath != \"\" && dirPath != \".\"" query branch of
// GetSymbolsInDirectory (as opposed to the "everything" branch already
// covered above) against a closed connection.
func TestGetSymbolsInDirectory_NonDotDirPathAfterCloseReturnsError(t *testing.T) {
	sdb, err := NewSymbolDatabase(":memory:")
	if err != nil {
		t.Fatalf("NewSymbolDatabase failed: %v", err)
	}
	if err := sdb.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if _, err := sdb.GetSymbolsInDirectory("pkg"); err == nil {
		t.Fatalf("GetSymbolsInDirectory(\"pkg\") after Close succeeded unexpectedly")
	}
}
