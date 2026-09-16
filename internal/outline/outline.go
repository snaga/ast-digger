// Package outline implements Outliner, which orchestrates skeleton
// generation, cached directory-tree synthesis, symbol location, reference
// search, and friction-feedback logging on top of the parser.Parser
// implementations and db.SymbolDatabase. It corresponds to
// ast_digger/outline.py (the Outliner class) in the Python implementation.
package outline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/snaga/ast-digger/internal/apperr"
	"github.com/snaga/ast-digger/internal/db"
	"github.com/snaga/ast-digger/internal/parser"
)

// SymbolDataWithDeps encapsulates a SymbolData along with its resolved
// dependencies (when requested via followImports).
type SymbolDataWithDeps struct {
	parser.SymbolData
	Dependencies []parser.SymbolData `json:"dependencies,omitempty"`
}

// extToLang maps supported source file extensions to the "language" value
// recorded in the shared cache database. It corresponds to Python's
// build_directory_tree/find_references `ext_to_lang`/`supported_exts`.
var extToLang = map[string]string{
	".py":       "python",
	".ts":       "typescript",
	".tsx":      "typescript",
	".go":       "go",
	".java":     "java",
	".js":       "javascript",
	".jsx":      "javascript",
	".rs":       "rust",
	".sql":      "sql",
	".md":       "markdown",
	".markdown": "markdown",
}

// excludeDirs lists directory names that are never descended into while
// scanning a project tree, matching Python's `exclude_dirs` sets in
// build_directory_tree/find_references.
var excludeDirs = map[string]bool{
	".venv":            true,
	".git":             true,
	"__pycache__":      true,
	".workspace_cache": true,
	"node_modules":     true,
}

// Outliner orchestrates skeleton generation, directory-tree synthesis,
// symbol location, and reference search. It corresponds to Python's
// `Outliner` class in ast_digger/outline.py.
type Outliner struct{}

// NewOutliner constructs an Outliner. Outliner currently holds no state; the
// constructor exists so call sites don't depend on the zero value directly
// and so state (e.g. injected dependencies) can be added later without
// breaking callers.
func NewOutliner() *Outliner {
	return &Outliner{}
}

// getParser returns the Parser implementation appropriate for filePath's
// extension, or an *apperr.FileAccessError for unsupported extensions. It
// corresponds to Python's `Outliner._get_parser`.
func (o *Outliner) getParser(filePath string) (parser.Parser, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".py":
		return parser.NewPythonParser(), nil
	case ".ts", ".tsx":
		return parser.NewTypeScriptParser(), nil
	case ".go":
		return parser.NewGolangParser(), nil
	case ".java":
		return parser.NewJavaParser(), nil
	case ".js", ".jsx":
		return parser.NewJavaScriptParser(), nil
	case ".rs":
		return parser.NewRustParser(), nil
	case ".sql":
		return parser.NewSqlParser(), nil
	case ".md", ".markdown":
		return parser.NewMarkdownParser(), nil
	}
	if strings.ToLower(filePath) == "markdown" || strings.ToLower(filePath) == ".md" || strings.ToLower(filePath) == ".markdown" {
		return parser.NewMarkdownParser(), nil
	}
	return nil, &apperr.FileAccessError{
		Path: filePath,
		Err:  fmt.Errorf("unsupported file type: %s", ext),
	}
}

// GenerateSkeleton generates filePath's skeleton code: its source with every
// function/method body replaced by "..." (or a language-appropriate
// equivalent), preserving signatures, decorators, and docstrings/doc
// comments verbatim. It delegates to the file's language-specific
// parser.Parser.GenerateSkeleton implementation. It corresponds to Python's
// `Outliner.generate_skeleton`.
func (o *Outliner) GenerateSkeleton(filePath string) (string, error) {
	p, err := o.getParser(filePath)
	if err != nil {
		return "", err
	}
	return p.GenerateSkeleton(filePath)
}

// ListSymbols parses filePath and returns the list of symbols found in it.
// It is a thin delegation to the file's language-specific parser.Parser
// (resolved via getParser), used by the CLI's `outline --json` mode, which
// needs the raw symbol list rather than the rendered skeleton text that
// GenerateSkeleton produces. It corresponds to the `outliner._get_parser(...).parse_file(...)`
// call in Python's `outline` command.
func (o *Outliner) ListSymbols(filePath string) ([]parser.SymbolData, error) {
	p, err := o.getParser(filePath)
	if err != nil {
		return nil, err
	}
	return p.ParseFile(filePath)
}

// ExtractSymbol resolves symbolPath (e.g. "MyClass.my_method") within
// filePath and returns the corresponding SymbolData, including its source
// code. It is a thin delegation to the file's language-specific
// parser.Parser (resolved via getParser), used by the CLI's `symbol`
// command, which -- unlike LocateSymbol -- needs the full SymbolData
// (including Code) rather than just the definition location.
func (o *Outliner) ExtractSymbol(filePath, symbolPath string) (parser.SymbolData, error) {
	p, err := o.getParser(filePath)
	if err != nil {
		return parser.SymbolData{}, err
	}
	return p.ExtractSymbol(filePath, symbolPath)
}

var identRegexp = regexp.MustCompile(`[a-zA-Z_][a-zA-Z0-9_]*`)

func extractIdentifiers(code string) []string {
	matches := identRegexp.FindAllString(code, -1)
	seen := make(map[string]bool)
	var result []string
	for _, m := range matches {
		if !seen[m] {
			seen[m] = true
			result = append(result, m)
		}
	}
	return result
}

func visitedKey(filePath, symbolName string) string {
	cleanPath := filepath.ToSlash(filepath.Clean(filePath))
	return cleanPath + "::" + symbolName
}

// ExtractSymbolWithDependencies resolves symbolPath within filePath and
// extracts its SymbolData. If followImports is true, it recursively parses
// import statements and references within the symbol's AST/code, resolving
// imported local symbols via ResolveImports, skipping circular references
// using a visited map ("file_path::symbol_name"), and returning the target
// symbol alongside its dependent SymbolData items. Unresolvable imports or
// external dependencies are safely skipped.
func (o *Outliner) ExtractSymbolWithDependencies(filePath, symbolPath string, followImports bool) (SymbolDataWithDeps, error) {
	targetSym, err := o.ExtractSymbol(filePath, symbolPath)
	if err != nil {
		return SymbolDataWithDeps{}, err
	}

	result := SymbolDataWithDeps{
		SymbolData: targetSym,
	}

	if !followImports {
		return result, nil
	}

	visited := make(map[string]bool)
	initPath := filePath
	if targetSym.FilePath != nil && *targetSym.FilePath != "" {
		initPath = *targetSym.FilePath
	}
	visited[visitedKey(initPath, targetSym.Name)] = true

	var dependencies []parser.SymbolData
	var collectDeps func(curFilePath string, sym parser.SymbolData)

	collectDeps = func(curFilePath string, sym parser.SymbolData) {
		if sym.Code == nil {
			return
		}
		idents := extractIdentifiers(*sym.Code)
		for _, ident := range idents {
			if ident == sym.Name {
				continue
			}

			defFile, _, err := o.ResolveImports(curFilePath, ident)
			if err != nil {
				continue
			}

			key := visitedKey(defFile, ident)
			if visited[key] {
				continue
			}
			visited[key] = true

			depSym, err := o.ExtractSymbol(defFile, ident)
			if err != nil {
				continue
			}

			if depSym.FilePath != nil && *depSym.FilePath != "" {
				visited[visitedKey(*depSym.FilePath, ident)] = true
			}

			dependencies = append(dependencies, depSym)

			nextFilePath := defFile
			if depSym.FilePath != nil && *depSym.FilePath != "" {
				nextFilePath = *depSym.FilePath
			}
			collectDeps(nextFilePath, depSym)
		}
	}

	collectDeps(initPath, targetSym)

	result.Dependencies = dependencies
	return result, nil
}

// ResolveImports resolves the definition location of symbolName as imported
// by filePath, returning the defining file path and the 1-indexed line the
// definition starts on. It is a thin delegation to the file's
// language-specific parser.Parser (resolved via getParser), used by the
// CLI's `resolve-import` command. It corresponds to the
// `outliner._get_parser(...).resolve_imports(...)` call in Python's
// `resolve_import` command.
func (o *Outliner) ResolveImports(filePath, symbolName string) (string, int, error) {
	p, err := o.getParser(filePath)
	if err != nil {
		return "", 0, err
	}
	return p.ResolveImports(filePath, symbolName)
}

// symbolOutlineLines renders syms as an indented outline: each "class"
// symbol, followed (indented by methodIndent) by its "method" symbols
// (matched by Parent == class name), both groups sorted by StartLine; then
// every top-level "function" symbol (Parent == nil), sorted by StartLine.
// classIndent/funcIndent prefix the class and top-level-function lines
// respectively. It mirrors the per-file symbol-hierarchy section of
// Python's `build_directory_tree`, factored out here so GenerateSkeleton
// and BuildDirectoryTree can share the same layout logic.
func symbolOutlineLines(syms []parser.SymbolData, classIndent, methodIndent, funcIndent string) []string {
	var classes []parser.SymbolData
	var topFuncs []parser.SymbolData
	for _, s := range syms {
		switch {
		case s.Kind == "class":
			classes = append(classes, s)
		case s.Kind == "function" && s.Parent == nil:
			topFuncs = append(topFuncs, s)
		}
	}
	sort.SliceStable(classes, func(i, j int) bool { return classes[i].StartLine < classes[j].StartLine })
	sort.SliceStable(topFuncs, func(i, j int) bool { return topFuncs[i].StartLine < topFuncs[j].StartLine })

	var lines []string
	for _, cls := range classes {
		lines = append(lines, classIndent+"class "+cls.Name)
		var methods []parser.SymbolData
		for _, s := range syms {
			if s.Kind == "method" && s.Parent != nil && *s.Parent == cls.Name {
				methods = append(methods, s)
			}
		}
		sort.SliceStable(methods, func(i, j int) bool { return methods[i].StartLine < methods[j].StartLine })
		for _, m := range methods {
			lines = append(lines, methodIndent+"def "+m.Name)
		}
	}
	for _, f := range topFuncs {
		lines = append(lines, funcIndent+"def "+f.Name)
	}
	return lines
}

// isPathUnder reports whether target is base itself, or lies within base,
// after resolving the relative path between them. It is used both to check
// that a requested directory lies within the project root
// (BuildDirectoryTree) and that a cached file path lies within the target
// directory being scanned.
func isPathUnder(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// fileMtime returns info's modification time as a float64 number of seconds
// since the Unix epoch, matching the representation of Python's
// os.path.getmtime() / pathlib.Path.stat().st_mtime that the shared cache
// database's `files.mtime` column stores.
func fileMtime(info os.FileInfo) float64 {
	t := info.ModTime()
	return float64(t.Unix()) + float64(t.Nanosecond())/1e9
}

// fileHash returns the lowercase hex-encoded SHA-256 digest of path's
// contents, matching Python's `get_file_hash` in ast_digger/outline.py.
func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// currentFileInfo describes a source file discovered on disk during a
// directory scan (BuildDirectoryTree/collectSourceFiles): its absolute path,
// on-disk modification time, and resolved language.
type currentFileInfo struct {
	AbsPath  string
	Mtime    float64
	Language string
}

// collectSourceFiles walks targetDir (recursively if recursive is true, or
// only its direct entries otherwise), returning every file whose extension
// is a key of extToLang, keyed by its path relative to projectRoot
// (forward-slash separated, matching the `files.path` convention used by
// the shared cache database). Directories named in excludeDirs are not
// descended into. It mirrors the file-discovery half of Python's
// `build_directory_tree` (the `target_dir.glob(...)` loop).
//
// Unreadable directories/files are silently skipped, mirroring Python's
// `except Exception: continue` around each file's `stat()` call.
func collectSourceFiles(targetDir, projectRoot string, recursive bool) map[string]currentFileInfo {
	result := make(map[string]currentFileInfo)

	var walk func(dir string)
	walk = func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			name := entry.Name()
			full := filepath.Join(dir, name)

			if entry.IsDir() {
				if excludeDirs[name] {
					continue
				}
				if recursive {
					walk(full)
				}
				continue
			}

			ext := strings.ToLower(filepath.Ext(name))
			lang, ok := extToLang[ext]
			if !ok {
				continue
			}

			info, err := entry.Info()
			if err != nil {
				continue
			}
			relPath, err := filepath.Rel(projectRoot, full)
			if err != nil {
				continue
			}
			relPath = filepath.ToSlash(relPath)

			result[relPath] = currentFileInfo{
				AbsPath:  full,
				Mtime:    fileMtime(info),
				Language: lang,
			}
		}
	}
	walk(targetDir)

	return result
}

// BuildDirectoryTree synchronizes the shared SQLite cache
// (.workspace_cache/symbols.db, relative to the current working directory /
// project root) with the current contents of dirPath, re-parsing only files
// whose modification time (and, on a mismatch, content hash) has changed
// since the last sync, then renders a symbol-annotated directory tree for
// dirPath from the now up-to-date cache. recursive controls whether
// subdirectories are scanned; limit caps the number of file entries
// rendered (excess files are dropped and a truncation hint is appended). It
// corresponds to Python's `Outliner.build_directory_tree`.
func (o *Outliner) BuildDirectoryTree(dirPath string, recursive bool, limit int) (string, error) {
	targetDir, err := filepath.Abs(dirPath)
	if err != nil {
		return "", &apperr.FileAccessError{Path: dirPath, Err: err}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", &apperr.FileAccessError{Err: err}
	}
	projectRoot, err := filepath.Abs(cwd)
	if err != nil {
		return "", &apperr.FileAccessError{Err: err}
	}

	if !isPathUnder(projectRoot, targetDir) {
		return "", &apperr.FileAccessError{
			Path: dirPath,
			Err:  fmt.Errorf("directory '%s' is outside the project root '%s'", dirPath, projectRoot),
		}
	}

	dbDir := filepath.Join(projectRoot, ".workspace_cache")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		return "", &apperr.FileAccessError{Path: dbDir, Err: err}
	}

	sdb, err := db.NewSymbolDatabase(filepath.Join(dbDir, "symbols.db"))
	if err != nil {
		return "", err
	}
	defer func() { _ = sdb.Close() }()

	dbFiles, err := sdb.GetAllFiles()
	if err != nil {
		return "", err
	}

	currentFiles := collectSourceFiles(targetDir, projectRoot, recursive)

	// Drop cache entries for files that were tracked but no longer exist
	// under targetDir, matching Python's "db_file not in current_files"
	// cleanup loop.
	for dbFile := range dbFiles {
		dbFileAbs := filepath.Join(projectRoot, filepath.FromSlash(dbFile))
		if !isPathUnder(targetDir, dbFileAbs) {
			continue
		}
		if _, stillPresent := currentFiles[dbFile]; stillPresent {
			continue
		}
		if err := sdb.DeleteFileCache(dbFile); err != nil {
			return "", err
		}
	}

	// Differential re-parse: only files whose mtime changed are considered;
	// among those, only a content hash mismatch triggers an actual
	// re-parse (a hash match just refreshes the cached mtime). Errors here
	// are swallowed (best-effort), matching Python's
	// `except Exception: pass` around this block.
	for relPath, info := range currentFiles {
		dbInfo, hasDBInfo := dbFiles[relPath]
		var dbMtime float64
		var dbHash string
		if hasDBInfo {
			dbMtime = dbInfo.Mtime
			dbHash = dbInfo.Hash
		}

		if info.Mtime == dbMtime {
			continue
		}

		hash, err := fileHash(info.AbsPath)
		if err != nil {
			continue
		}

		if hash != dbHash {
			p, err := o.getParser(info.AbsPath)
			if err != nil {
				continue
			}
			symbols, err := p.ParseFile(info.AbsPath)
			if err != nil {
				continue
			}
			if err := sdb.UpdateFileCache(relPath, info.Mtime, hash, info.Language, symbols); err != nil {
				continue
			}
		} else {
			if err := sdb.UpdateFileMtime(relPath, info.Mtime); err != nil {
				continue
			}
		}
	}

	relTargetDir, err := filepath.Rel(projectRoot, targetDir)
	if err != nil {
		relTargetDir = ""
	}
	relTargetDir = filepath.ToSlash(relTargetDir)
	if relTargetDir == "." {
		relTargetDir = ""
	}

	allSymbols, err := sdb.GetSymbolsInDirectory(relTargetDir)
	if err != nil {
		return "", err
	}

	fileSymbols := make(map[string][]parser.SymbolData)
	for _, sym := range allSymbols {
		if sym.FilePath != nil {
			fileSymbols[*sym.FilePath] = append(fileSymbols[*sym.FilePath], sym)
		}
	}

	sortedFiles := make([]string, 0, len(currentFiles))
	for f := range currentFiles {
		sortedFiles = append(sortedFiles, f)
	}
	sort.Strings(sortedFiles)

	isTruncated := false
	if len(sortedFiles) > limit {
		sortedFiles = sortedFiles[:limit]
		isTruncated = true
	}

	baseName := filepath.Base(targetDir)
	if baseName == "" || baseName == "." || baseName == string(filepath.Separator) {
		baseName = "."
	}

	lines := []string{baseName + "/"}
	for _, f := range sortedFiles {
		lines = append(lines, "  "+filepath.Base(f))
		lines = append(lines, symbolOutlineLines(fileSymbols[f], "    ", "      ", "    ")...)
	}

	if isTruncated {
		lines = append(lines, "... (output truncated due to limit)")
		lines = append(lines, "Hint: Use '--limit N' or '--filter' to scope down your search.")
	}

	return strings.Join(lines, "\n"), nil
}

// feedbackLogEntry is the JSON shape of each line written to
// .workspace_cache/feedback.jsonl by WriteFeedbackLog, matching Python's
// `log_entry` dict in `Outliner.write_feedback_log` (field order kept the
// same, though JSON consumers should not rely on it).
type feedbackLogEntry struct {
	Timestamp string `json:"timestamp"`
	Message   string `json:"message"`
}

// WriteFeedbackLog appends a timestamped JSON line recording message to
// .workspace_cache/feedback.jsonl (relative to the current working
// directory / project root), creating the cache directory/file as needed.
// It corresponds to Python's `Outliner.write_feedback_log`.
func (o *Outliner) WriteFeedbackLog(message string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return &apperr.FileAccessError{Err: err}
	}
	projectRoot, err := filepath.Abs(cwd)
	if err != nil {
		return &apperr.FileAccessError{Err: err}
	}

	cacheDir := filepath.Join(projectRoot, ".workspace_cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return &apperr.FileAccessError{Path: cacheDir, Err: err}
	}
	feedbackFile := filepath.Join(cacheDir, "feedback.jsonl")

	entry := feedbackLogEntry{
		Timestamp: time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Message:   message,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("outline: failed to marshal feedback log entry: %w", err)
	}
	data = append(data, '\n')

	f, err := os.OpenFile(feedbackFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return &apperr.FileAccessError{Path: feedbackFile, Err: err}
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(data); err != nil {
		return &apperr.FileAccessError{Path: feedbackFile, Err: err}
	}
	return nil
}

// LocateSymbol resolves symbolPath within filePath and returns its
// definition location: the (possibly project-root-relative) file path it
// was defined in, and its 1-indexed start/end lines. It corresponds to
// Python's `Outliner.locate_symbol`, adapted to design_GOLANG.md's
// lightweight (filePath, startLine, endLine, error) return shape rather
// than returning a full SymbolData.
func (o *Outliner) LocateSymbol(filePath, symbolPath string) (string, int, int, error) {
	p, err := o.getParser(filePath)
	if err != nil {
		return "", 0, 0, err
	}
	sym, err := p.ExtractSymbol(filePath, symbolPath)
	if err != nil {
		return "", 0, 0, err
	}
	resultPath := ""
	if sym.FilePath != nil {
		resultPath = *sym.FilePath
	}
	return resultPath, sym.StartLine, sym.EndLine, nil
}

// FindReferences searches every supported source file under projectRoot
// (excluding directories in excludeDirs) for occurrences of symbolName,
// delegating to each file's Parser.FindReferencesInFile and aggregating the
// results. If projectRoot does not exist, an empty (non-nil) slice is
// returned rather than an error. Errors from parsing/searching an
// individual file are ignored so that one problematic file does not abort
// the project-wide scan. It corresponds to Python's
// `Outliner.find_references`.
func (o *Outliner) FindReferences(projectRoot, symbolName string) ([]parser.ReferenceData, error) {
	rootPath, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, &apperr.FileAccessError{Path: projectRoot, Err: err}
	}
	if _, err := os.Stat(rootPath); err != nil {
		return []parser.ReferenceData{}, nil
	}

	references := []parser.ReferenceData{}

	var walk func(dir string)
	walk = func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			name := entry.Name()
			full := filepath.Join(dir, name)

			if entry.IsDir() {
				if excludeDirs[name] {
					continue
				}
				walk(full)
				continue
			}

			ext := strings.ToLower(filepath.Ext(name))
			if _, ok := extToLang[ext]; !ok {
				continue
			}

			p, err := o.getParser(full)
			if err != nil {
				continue
			}
			refs, err := p.FindReferencesInFile(full, symbolName)
			if err != nil {
				continue
			}
			references = append(references, refs...)
		}
	}
	walk(rootPath)

	return references, nil
}
