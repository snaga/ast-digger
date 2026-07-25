package subpkg

import "../mathutils"

// UseMath calls into the mathutils package via a relative import path, to
// exercise GolangParser.ResolveImports' relative-import resolution branch.
// (Real Go module builds do not support relative import paths like this;
// this file exists purely as tree-sitter-parsed test fixture data and is
// never compiled -- it lives under a "testdata" directory, which the go
// tool ignores for package discovery.)
func UseMath() int {
	return mathutils.Add(3, 4)
}
