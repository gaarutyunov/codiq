package extract_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/extract"
	"github.com/gaarutyunov/codiq/extract/golang"
	"github.com/gaarutyunov/codiq/extract/py"
	"github.com/gaarutyunov/codiq/extract/rs"
	"github.com/gaarutyunov/codiq/extract/ts"
)

func TestParserFor(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "a Go file", path: filepath.FromSlash("/repo/main.go"), want: true},
		{name: "a Go file in a subdirectory", path: filepath.FromSlash("/repo/pkg/a/b.go"), want: true},
		{name: "a TypeScript file", path: filepath.FromSlash("/repo/src/main.ts"), want: true},
		{name: "a TSX file, which needs its own grammar", path: filepath.FromSlash("/repo/src/app.tsx"), want: false},
		{name: "a Python file", path: filepath.FromSlash("/repo/main.py"), want: true},
		{name: "a Python file in a package", path: filepath.FromSlash("/repo/pkg/__init__.py"), want: true},
		{name: "a stub, which the stanza can read but nothing registers", path: filepath.FromSlash("/repo/pkg/mod.pyi"), want: false},
		{name: "a Python file", path: filepath.FromSlash("/repo/main.py"), want: true},
		{name: "a Python package's __init__", path: filepath.FromSlash("/repo/pkg/__init__.py"), want: true},
		{name: "a type stub, which no ecosystem owns", path: filepath.FromSlash("/repo/pkg/mod.pyi"), want: false},
		{name: "a Rust crate root", path: filepath.FromSlash("/repo/src/main.rs"), want: true},
		{name: "a Rust module in a directory", path: filepath.FromSlash("/repo/src/util/mod.rs"), want: true},
		{name: "no extension", path: filepath.FromSlash("/repo/Makefile"), want: false},
		{name: "an unregistered extension", path: filepath.FromSlash("/repo/App.java"), want: false},
		{name: "the extension is case sensitive", path: filepath.FromSlash("/repo/main.GO"), want: false},
		{name: "not the whole name", path: filepath.FromSlash("/repo/go"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := extract.ParserFor(tt.path)
			assert.Equal(t, tt.want, ok)
			assert.Equal(t, tt.want, extract.Supported(tt.path))
			if tt.want {
				require.NotNil(t, p)
			} else {
				assert.Nil(t, p)
			}
		})
	}
}

// TestExtensions is a tripwire: adding a language has to be a deliberate edit
// here, because the set of extensions the registry answers for is what the walk
// filters on (index) and what every ecosystem has to own a coordinate for
// (coord.Extensions). Extensions() returns them sorted.
func TestExtensions(t *testing.T) {
	assert.Equal(t, []string{golang.Ext, py.Ext, rs.Ext, ts.Ext}, extract.Extensions())
}

// TestRegisteredParserParses is the end-to-end check on the registry: the entry
// for ".go" is a working parser, not just a non-nil interface value.
func TestRegisteredParserParses(t *testing.T) {
	p, ok := extract.ParserFor(filepath.FromSlash("/repo/main.go"))
	require.True(t, ok)

	c := coord.Coord{
		Scheme: coord.GoScheme, Manager: coord.GoManager,
		Name: "github.com/foo/bar", Version: coord.Unknown, Root: filepath.FromSlash("/repo"),
	}
	ff := p.Parse(filepath.FromSlash("/repo/main.go"), []byte("package main\n\nfunc main() {}\n"), c)

	require.Empty(t, ff.ParseError)
	assert.Equal(t, golang.Lang, ff.File.Lang)
	assert.Equal(t, c, ff.File.Coord)

	descriptors := make([]string, 0, len(ff.Occurrences))
	for _, occ := range ff.Occurrences {
		descriptors = append(descriptors, occ.Descriptor.String())
	}
	assert.Contains(t, descriptors, "scip-go gomod github.com/foo/bar . main().")
}

// TestGolangParserSatisfiesParserStructurally documents the SPEC 12 constraint
// the registry depends on: the assignment below compiles, and it compiles
// because golang.Parser has the right method set — golang imports facts and
// coord and never extract, so there is no cycle to break.
func TestGolangParserSatisfiesParserStructurally(t *testing.T) {
	var p extract.Parser = golang.New()
	assert.NotNil(t, p)
}

// TestTSParserSatisfiesParserStructurally is the same constraint for the second
// language, and it is the one that turns SPEC 12's claim into something
// checked rather than asserted: two independent sub-packages now satisfy Parser
// without either importing extract.
func TestTSParserSatisfiesParserStructurally(t *testing.T) {
	var p extract.Parser = ts.New()
	assert.NotNil(t, p)
}

// TestPyParserSatisfiesParserStructurally is the third, which is where the
// claim stops being a coincidence: three sub-packages written independently
// satisfy Parser, none of them imports extract, and the byExt literal in
// extract.go is the whole of the compile-time check that they do.
func TestPyParserSatisfiesParserStructurally(t *testing.T) {
	var p extract.Parser = py.New()
	assert.NotNil(t, p)
}

// TestRSParserSatisfiesParserStructurally is the fourth, and it is the one that
// makes the claim about the *registry* rather than about any of the languages
// in it: four sub-packages written independently satisfy Parser, none of them
// imports extract, and the byExt literal in extract.go is the whole of the
// compile-time check that they do.
func TestRSParserSatisfiesParserStructurally(t *testing.T) {
	var p extract.Parser = rs.New()
	assert.NotNil(t, p)
}

// TestRegisteredPythonParserParses is TestRegisteredParserParses for Python:
// the entry for ".py" is a working parser and not just a non-nil interface
// value, and the descriptor it produces carries the Python coordinate.
func TestRegisteredPythonParserParses(t *testing.T) {
	p, ok := extract.ParserFor(filepath.FromSlash("/repo/greeter.py"))
	require.True(t, ok)

	c := coord.Coord{
		Scheme: coord.PyScheme, Manager: coord.PipManager,
		Name: "greeter", Version: "1.0.0", Root: filepath.FromSlash("/repo"),
	}
	ff := p.Parse(filepath.FromSlash("/repo/greeter.py"), []byte("def greet():\n    pass\n"), c)

	require.Empty(t, ff.ParseError)
	assert.Equal(t, py.Lang, ff.File.Lang)
	assert.Equal(t, c, ff.File.Coord)

	descriptors := make([]string, 0, len(ff.Occurrences))
	for _, occ := range ff.Occurrences {
		descriptors = append(descriptors, occ.Descriptor.String())
	}
	assert.Contains(t, descriptors, "scip-python pip greeter 1.0.0 greeter/greet().")
}

// TestRegisteredRustParserParses is TestRegisteredParserParses for Rust: the
// entry for ".rs" is a working parser and not just a non-nil interface value,
// and the descriptor it produces carries the cargo coordinate — with `src/`
// dropped from the namespace, which is the one part of Rust's module rule that
// shows up in a two-line file.
func TestRegisteredRustParserParses(t *testing.T) {
	p, ok := extract.ParserFor(filepath.FromSlash("/repo/src/greeter.rs"))
	require.True(t, ok)

	c := coord.Coord{
		Scheme: coord.RustScheme, Manager: coord.CargoManager,
		Name: "greeter", Version: "1.0.0", Root: filepath.FromSlash("/repo"),
	}
	ff := p.Parse(filepath.FromSlash("/repo/src/greeter.rs"), []byte("pub fn greet() {}\n"), c)

	require.Empty(t, ff.ParseError)
	assert.Equal(t, rs.Lang, ff.File.Lang)
	assert.Equal(t, c, ff.File.Coord)

	descriptors := make([]string, 0, len(ff.Occurrences))
	for _, occ := range ff.Occurrences {
		descriptors = append(descriptors, occ.Descriptor.String())
	}
	assert.Contains(t, descriptors, "scip-rust cargo greeter 1.0.0 greeter/greet().")
}
