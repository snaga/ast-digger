// Package db implements SQLite-backed persistence for the file/symbol cache
// shared between the Go and Python implementations of AST-Digger. It
// corresponds to ast_digger/db.py in the Python implementation.
package db

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/snaga/ast-digger/internal/apperr"
	"github.com/snaga/ast-digger/internal/parser"
)

// FileMeta holds the cached metadata AST-Digger tracks per source file, as
// stored in the `files` table. It mirrors the (mtime, hash, language) tuple
// returned by Python's SymbolDatabase.get_all_files().
type FileMeta struct {
	// Mtime is the last-observed modification time of the file (seconds
	// since epoch, matching Python's os.path.getmtime()).
	Mtime float64
	// Hash is the content hash (SHA-256) of the file at Mtime.
	Hash string
	// Language identifies the parser/language used ("python", "typescript",
	// "go", ...).
	Language string
}

// SymbolDatabase manages the SQLite cache database (`.workspace_cache/symbols.db`)
// shared with the Python implementation. It corresponds to Python's
// ast_digger.db.SymbolDatabase.
type SymbolDatabase struct {
	dbPath string
	conn   *sql.DB
}

// NewSymbolDatabase opens (creating if necessary) the SQLite database at
// dbPath and ensures its schema matches the shared cache schema. dbPath may
// be ":memory:" for an in-memory database.
//
// If dbPath refers to an existing file whose schema is missing required
// tables/columns (i.e. it was created by an incompatible/older version of
// either implementation), the file is deleted and rebuilt from scratch. This
// mirrors Python's SymbolDatabase._init_db() "detect and self-heal" behavior;
// callers are not expected to see an error in that case.
func NewSymbolDatabase(dbPath string) (*SymbolDatabase, error) {
	if dbPath != ":memory:" {
		if err := repairSchemaIfNeeded(dbPath); err != nil {
			return nil, err
		}
	}

	conn, err := connect(dbPath)
	if err != nil {
		return nil, fmt.Errorf("db: failed to open %s: %w", dbPath, err)
	}

	if err := createSchema(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("db: failed to initialize schema for %s: %w", dbPath, err)
	}

	return &SymbolDatabase{dbPath: dbPath, conn: conn}, nil
}

// connect opens a new connection to dbPath with foreign key enforcement
// enabled, matching Python's SymbolDatabase._connect() (which re-issues
// "PRAGMA foreign_keys = ON;" on every connection since SQLite does not
// persist that pragma in the database file itself).
//
// database/sql.DB is a connection *pool*: "PRAGMA foreign_keys = ON;" only
// affects whichever single physical connection happens to run the Exec
// below, not connections opened later by the pool. UpdateFileCache's
// INSERT-OR-REPLACE-triggers-CASCADE-DELETE trick (see its doc comment)
// silently stops working on any physical connection where the pragma was
// never applied, leaving orphaned symbol rows behind with no error. We pin
// the pool to a single physical connection via SetMaxOpenConns(1) so the
// pragma set here always applies to every statement this *sql.DB ever
// executes; this is simpler and more portable than relying on
// driver-specific DSN pragma syntax, and the (local, single-process)
// SQLite cache database does not need real connection concurrency.
func connect(dbPath string) (*sql.DB, error) {
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	if _, err := conn.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// repairSchemaIfNeeded inspects an existing database file at dbPath and, if
// its schema is missing required tables/columns, deletes the file so that a
// fresh schema can be created by the caller. It is a no-op if dbPath does not
// exist. This is a direct port of the file-existence branch of Python's
// SymbolDatabase._init_db().
func repairSchemaIfNeeded(dbPath string) error {
	if _, err := os.Stat(dbPath); err != nil {
		// File does not exist (or is otherwise inaccessible); nothing to
		// repair. Schema creation below will create it fresh.
		return nil
	}

	corrupted := false

	func() {
		conn, err := connect(dbPath)
		if err != nil {
			corrupted = true
			return
		}
		defer func() { _ = conn.Close() }()

		if err := checkSchema(conn); err != nil {
			corrupted = true
		}
	}()

	if corrupted {
		if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("db: failed to remove corrupted database %s: %w", dbPath, err)
		}
	}

	return nil
}

// checkSchema verifies that conn's database has the tables/columns required
// by the current schema, returning an *apperr.DatabaseCorruptedError
// (wrapped) if not. It mirrors the checks performed by Python's
// SymbolDatabase._init_db().
func checkSchema(conn *sql.DB) error {
	tables, err := tableNames(conn)
	if err != nil {
		return fmt.Errorf("db: failed to list tables: %w", err)
	}
	if !tables["files"] || !tables["symbols"] {
		return &apperr.DatabaseCorruptedError{Err: fmt.Errorf("missing tables")}
	}

	filesCols, err := columnNames(conn, "files")
	if err != nil {
		return fmt.Errorf("db: failed to inspect files table: %w", err)
	}
	if !filesCols["language"] {
		return &apperr.DatabaseCorruptedError{Err: fmt.Errorf("missing language column in files table")}
	}

	symbolsCols, err := columnNames(conn, "symbols")
	if err != nil {
		return fmt.Errorf("db: failed to inspect symbols table: %w", err)
	}
	if !symbolsCols["signature"] || !symbolsCols["docstring"] {
		return &apperr.DatabaseCorruptedError{Err: fmt.Errorf("outdated schema")}
	}

	return nil
}

// tableNames returns the set of table names present in sqlite_master.
func tableNames(conn *sql.DB) (map[string]bool, error) {
	rows, err := conn.Query("SELECT name FROM sqlite_master WHERE type='table';")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	names := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names[name] = true
	}
	return names, rows.Err()
}

// columnNames returns the set of column names present in table, via
// PRAGMA table_info(table).
func columnNames(conn *sql.DB, table string) (map[string]bool, error) {
	// PRAGMA statements don't support bind parameters; table is always one of
	// our own hard-coded schema table names, so this is safe from injection.
	rows, err := conn.Query(fmt.Sprintf("PRAGMA table_info(%s);", table))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	names := make(map[string]bool)
	for rows.Next() {
		// PRAGMA table_info columns: cid, name, type, notnull, dflt_value, pk
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		for i, col := range cols {
			if col == "name" {
				if s, ok := values[i].(string); ok {
					names[s] = true
				}
			}
		}
	}
	return names, rows.Err()
}

// createSchema creates the `files`/`symbols` tables and indexes if they do
// not already exist. The DDL is a direct port of Python's
// SymbolDatabase._init_db() and must be kept byte-for-byte compatible with
// ast_digger/db.py (table/column names, types, and indexes).
func createSchema(conn *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS files (
			path TEXT PRIMARY KEY,
			mtime REAL NOT NULL,
			hash TEXT NOT NULL,
			language TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS symbols (
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
		);`,
		`CREATE INDEX IF NOT EXISTS idx_symbols_name ON symbols(name);`,
		`CREATE INDEX IF NOT EXISTS idx_symbols_file_path ON symbols(file_path);`,
	}
	for _, stmt := range stmts {
		if _, err := conn.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// GetAllFiles returns the metadata of every file currently tracked in the
// cache, keyed by file path.
func (db *SymbolDatabase) GetAllFiles() (map[string]FileMeta, error) {
	rows, err := db.conn.Query("SELECT path, mtime, hash, language FROM files;")
	if err != nil {
		return nil, fmt.Errorf("db: GetAllFiles query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	files := make(map[string]FileMeta)
	for rows.Next() {
		var path string
		var meta FileMeta
		if err := rows.Scan(&path, &meta.Mtime, &meta.Hash, &meta.Language); err != nil {
			return nil, fmt.Errorf("db: GetAllFiles scan failed: %w", err)
		}
		files[path] = meta
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: GetAllFiles iteration failed: %w", err)
	}
	return files, nil
}

// UpdateFileCache replaces the cached metadata and symbols for filePath with
// mtime/hash/language/symbols, inside a single transaction.
//
// NOTE on the CASCADE-delete-through-INSERT-OR-REPLACE trick: like Python's
// SymbolDatabase.update_file_cache(), this method does NOT explicitly
// "DELETE FROM symbols WHERE file_path = ?" before inserting the new
// symbols. Instead it relies on a SQLite implementation detail: "INSERT OR
// REPLACE INTO files ..." resolves a PRIMARY KEY conflict by deleting the
// conflicting row and then inserting the new one, and because this
// connection has "PRAGMA foreign_keys = ON" set, that internal DELETE
// triggers the "ON DELETE CASCADE" on symbols.file_path, wiping out all of
// the previous symbol rows for filePath before the fresh INSERTs below run.
// This is intentional behavior-parity with the Python implementation (see
// ast_digger/db.py: SymbolDatabase.update_file_cache) and must not be
// "simplified" by adding an explicit DELETE, since callers/tests may depend
// on the exact statement ordering matching the Python original.
func (db *SymbolDatabase) UpdateFileCache(filePath string, mtime float64, hash string, language string, symbols []parser.SymbolData) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("db: UpdateFileCache failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback is a no-op after a successful commit

	if _, err := tx.Exec(
		"INSERT OR REPLACE INTO files (path, mtime, hash, language) VALUES (?, ?, ?, ?);",
		filePath, mtime, hash, language,
	); err != nil {
		return fmt.Errorf("db: UpdateFileCache failed to upsert file %s: %w", filePath, err)
	}

	for _, sym := range symbols {
		if _, err := tx.Exec(
			`INSERT INTO symbols (file_path, name, kind, parent, start_line, end_line, signature, docstring)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?);`,
			filePath, sym.Name, sym.Kind, sym.Parent, sym.StartLine, sym.EndLine, sym.Signature, sym.Docstring,
		); err != nil {
			return fmt.Errorf("db: UpdateFileCache failed to insert symbol %q for %s: %w", sym.Name, filePath, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: UpdateFileCache failed to commit transaction: %w", err)
	}
	return nil
}

// DeleteFileCache removes filePath's metadata (and, via ON DELETE CASCADE,
// its symbols) from the cache.
func (db *SymbolDatabase) DeleteFileCache(filePath string) error {
	if _, err := db.conn.Exec("DELETE FROM files WHERE path = ?;", filePath); err != nil {
		return fmt.Errorf("db: DeleteFileCache failed for %s: %w", filePath, err)
	}
	return nil
}

// GetSymbolsInDirectory returns all cached symbols whose file path is
// dirPath itself or lies under dirPath. An empty dirPath or "." returns every
// cached symbol.
func (db *SymbolDatabase) GetSymbolsInDirectory(dirPath string) ([]parser.SymbolData, error) {
	const query = `SELECT file_path, name, kind, parent, start_line, end_line, signature, docstring
		FROM symbols`

	var rows *sql.Rows
	var err error

	if dirPath == "" || dirPath == "." {
		rows, err = db.conn.Query(query + ";")
	} else {
		normalized := strings.ReplaceAll(dirPath, "\\", "/")
		prefix := normalized
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		rows, err = db.conn.Query(query+" WHERE file_path = ? OR file_path LIKE ?;", normalized, prefix+"%")
	}
	if err != nil {
		return nil, fmt.Errorf("db: GetSymbolsInDirectory query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var symbols []parser.SymbolData
	for rows.Next() {
		var filePath string
		var sym parser.SymbolData
		if err := rows.Scan(&filePath, &sym.Name, &sym.Kind, &sym.Parent, &sym.StartLine, &sym.EndLine, &sym.Signature, &sym.Docstring); err != nil {
			return nil, fmt.Errorf("db: GetSymbolsInDirectory scan failed: %w", err)
		}
		sym.FilePath = &filePath
		symbols = append(symbols, sym)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: GetSymbolsInDirectory iteration failed: %w", err)
	}
	return symbols, nil
}

// UpdateFileMtime updates the cached mtime of filePath without touching its
// hash, language, or symbols. It corresponds to Python's
// SymbolDatabase.update_file_mtime(), used by the Outliner (task 6) to
// refresh mtime after a cache hit that skips re-parsing.
func (db *SymbolDatabase) UpdateFileMtime(filePath string, mtime float64) error {
	if _, err := db.conn.Exec("UPDATE files SET mtime = ? WHERE path = ?;", mtime, filePath); err != nil {
		return fmt.Errorf("db: UpdateFileMtime failed for %s: %w", filePath, err)
	}
	return nil
}

// Close closes the underlying database connection.
func (db *SymbolDatabase) Close() error {
	if err := db.conn.Close(); err != nil {
		return fmt.Errorf("db: failed to close %s: %w", db.dbPath, err)
	}
	return nil
}
