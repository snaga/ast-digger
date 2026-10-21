// Package parser defines the common data model and interface shared by all
// language-specific AST parsers (Python, TypeScript, Go). It corresponds to
// ast_digger/parsers/base.py in the Python implementation.
package parser

// SymbolData describes a single symbol (class, function, method, struct,
// interface, type, ...) discovered while parsing a source file.
//
// It mirrors the Python `SymbolData` dataclass defined in
// ast_digger/parsers/base.py. Optional fields that may be absent for a given
// symbol/language combination are represented as pointers so that "not set"
// can be distinguished from the zero value.
type SymbolData struct {
	// Name is the symbol name (e.g. "UserManager", "register_user").
	Name string `json:"name"`
	// Kind classifies the symbol, e.g. "class", "function", "method",
	// "interface", "struct", "type".
	Kind string `json:"kind"`
	// Parent is the enclosing symbol name (e.g. the class name for a
	// method), or nil for top-level symbols.
	Parent *string `json:"parent"`
	// FilePath is the path of the file the symbol was defined in.
	FilePath *string `json:"file_path"`
	// StartLine is the 1-indexed line the symbol definition starts on.
	StartLine int `json:"start_line"`
	// EndLine is the 1-indexed line the symbol definition ends on.
	EndLine int `json:"end_line"`
	// Signature holds parameter/type annotation information, if available.
	Signature *string `json:"signature"`
	// Docstring holds the symbol's docstring/doc comment text, if any.
	Docstring *string `json:"docstring"`
	// Code holds the extracted source code for the symbol. It is not
	// persisted to the cache database and is populated on demand by
	// extracting the relevant lines from the source file.
	Code *string `json:"code"`
}

// ReferenceData describes a single place in the source tree where a symbol
// is referenced (e.g. called or otherwise used).
//
// It mirrors the Python `ReferenceData` dataclass defined in
// ast_digger/parsers/base.py.
type ReferenceData struct {
	// FilePath is the path of the file the reference was found in.
	FilePath string `json:"file_path"`
	// LineNumber is the 1-indexed line the reference occurs on.
	LineNumber int `json:"line_number"`
	// LineContent is the source line (or snippet) containing the reference.
	// It is serialized under the "snippet" key to match Python's
	// ReferenceData dataclass field name.
	LineContent string `json:"snippet"`
}

// Parser is the common AST-analysis interface implemented by every
// language-specific parser (PythonParser, TypeScriptParser, GolangParser).
//
// It corresponds to the abstract `BaseParser` class defined in
// ast_digger/parsers/base.py.
type Parser interface {
	// ParseFile parses filePath and returns the list of symbols found in it.
	ParseFile(filePath string) ([]SymbolData, error)

	// ParseBytes parses source byte content for filePath and returns the list
	// of symbols found in it.
	ParseBytes(source []byte, filePath string) ([]SymbolData, error)

	// ExtractSymbol resolves symbolPath (e.g. "MyClass.my_method") within
	// filePath and returns the corresponding SymbolData, including its
	// source code.
	ExtractSymbol(filePath, symbolPath string) (SymbolData, error)

	// ResolveImports resolves the definition location of symbolName as
	// imported by filePath, returning the defining file path and the
	// 1-indexed line the definition starts on.
	ResolveImports(filePath, symbolName string) (defFilePath string, startLine int, err error)

	// FindReferencesInFile searches filePath for occurrences where
	// symbolName is referenced or invoked.
	FindReferencesInFile(filePath, symbolName string) ([]ReferenceData, error)

	// GenerateSkeleton reconstructs filePath's source with every
	// function/method body replaced by "..." (or a language-appropriate
	// equivalent), while preserving signatures, decorators, and
	// docstrings/doc comments verbatim.
	GenerateSkeleton(filePath string) (string, error)
}
