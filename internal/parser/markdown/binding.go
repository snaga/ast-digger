package markdown

// #cgo CFLAGS: -std=c11
// #include "../../../../../go/pkg/mod/github.com/tree-sitter-grammars/tree-sitter-markdown@v0.5.3/tree-sitter-markdown/src/parser.c"
// #include "../../../../../go/pkg/mod/github.com/tree-sitter-grammars/tree-sitter-markdown@v0.5.3/tree-sitter-markdown/src/scanner.c"
import "C"

import "unsafe"

// Language returns the tree-sitter Language for Markdown grammar.
func Language() unsafe.Pointer {
	return unsafe.Pointer(C.tree_sitter_markdown())
}
