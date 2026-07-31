package extract_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/extract"
	"github.com/gaarutyunov/codiq/extract/cs"
	"github.com/gaarutyunov/codiq/extract/golang"
	"github.com/gaarutyunov/codiq/extract/java"
	"github.com/gaarutyunov/codiq/extract/py"
	"github.com/gaarutyunov/codiq/extract/rb"
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
		{name: "a Java compilation unit", path: filepath.FromSlash("/repo/src/main/java/com/example/App.java"), want: true},
		{name: "a Java file outside a source root, which the stanza does not care about", path: filepath.FromSlash("/repo/App.java"), want: true},
		{name: "a class file, which is a build output and not a source", path: filepath.FromSlash("/repo/App.class"), want: false},
		{name: "a C# compilation unit", path: filepath.FromSlash("/repo/src/App/Program.cs"), want: true},
		{name: "a C# script, which needs its own grammar", path: filepath.FromSlash("/repo/build.csx"), want: false},
		{name: "a Razor component, which is not C# alone", path: filepath.FromSlash("/repo/Pages/Index.razor"), want: false},
		{name: "a project file, which is a manifest and not a source", path: filepath.FromSlash("/repo/Greeter.csproj"), want: false},
		{name: "a Ruby file", path: filepath.FromSlash("/repo/lib/greeter/greeter.rb"), want: true},
		{name: "a Rake task, which is Ruby but not a .rb", path: filepath.FromSlash("/repo/lib/tasks/build.rake"), want: false},
		{name: "a gemspec, which is Ruby and is a manifest", path: filepath.FromSlash("/repo/greeter.gemspec"), want: false},
		{name: "a Rakefile, which has no extension at all", path: filepath.FromSlash("/repo/Rakefile"), want: false},
		{name: "no extension", path: filepath.FromSlash("/repo/Makefile"), want: false},
		{name: "an unregistered extension", path: filepath.FromSlash("/repo/App.kt"), want: false},
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
	assert.Equal(t, []string{cs.Ext, golang.Ext, java.Ext, py.Ext, rb.Ext, rs.Ext, ts.Ext}, extract.Extensions())
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

// TestJavaParserSatisfiesParserStructurally is the fifth, and by now the claim
// is about the registry and not about any of the languages in it: five
// sub-packages written independently satisfy Parser, none of them imports
// extract, and the byExt literal in extract.go is the whole of the compile-time
// check that they do.
func TestJavaParserSatisfiesParserStructurally(t *testing.T) {
	var p extract.Parser = java.New()
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

// TestRegisteredJavaParserParses is TestRegisteredParserParses for Java: the
// entry for ".java" is a working parser and not just a non-nil interface value,
// and the descriptor it produces carries the Maven coordinate — with the
// namespace read off the `package` clause and not off `src/main/java/`, which is
// the one part of Java's namespace rule that shows up in a three-line file.
func TestRegisteredJavaParserParses(t *testing.T) {
	path := filepath.FromSlash("/repo/src/main/java/greeter/Greeter.java")
	p, ok := extract.ParserFor(path)
	require.True(t, ok)

	c := coord.Coord{
		Scheme: coord.JavaScheme, Manager: coord.MavenManager,
		Name: "com.example:greeter", Version: "1.0.0", Root: filepath.FromSlash("/repo"),
	}
	ff := p.Parse(path, []byte("package greeter;\n\npublic class Greeter {\n    public String greet() { return \"\"; }\n}\n"), c)

	require.Empty(t, ff.ParseError)
	assert.Equal(t, java.Lang, ff.File.Lang)
	assert.Equal(t, c, ff.File.Coord)

	descriptors := make([]string, 0, len(ff.Occurrences))
	for _, occ := range ff.Occurrences {
		descriptors = append(descriptors, occ.Descriptor.String())
	}
	assert.Contains(t, descriptors, "scip-java maven com.example:greeter 1.0.0 greeter/Greeter#greet().")
}

// TestCSParserSatisfiesParserStructurally is the sixth, and the claim it makes is
// the registry's rather than any one language's: six sub-packages written
// independently satisfy Parser, none of them imports extract, and the byExt
// literal in extract.go is the whole of the compile-time check that they do.
func TestCSParserSatisfiesParserStructurally(t *testing.T) {
	var p extract.Parser = cs.New()
	assert.NotNil(t, p)
}

// TestRegisteredCSharpParserParses is TestRegisteredParserParses for C#: the
// entry for ".cs" is a working parser and not just a non-nil interface value,
// and the descriptor it produces carries the NuGet coordinate — with the
// namespace read off the file-scoped declaration and not off `src/Greeter/`,
// which is the one part of C#'s namespace rule that shows up in a four-line file.
func TestRegisteredCSharpParserParses(t *testing.T) {
	path := filepath.FromSlash("/repo/src/Greeter/Greeter.cs")
	p, ok := extract.ParserFor(path)
	require.True(t, ok)

	c := coord.Coord{
		Scheme: coord.CSharpScheme, Manager: coord.NuGetManager,
		Name: "Codiq.Greeter", Version: "1.0.0", Root: filepath.FromSlash("/repo"),
	}
	ff := p.Parse(path, []byte("namespace greeter;\n\npublic class Greeter\n{\n    public string Greet() { return \"\"; }\n}\n"), c)

	require.Empty(t, ff.ParseError)
	assert.Equal(t, cs.Lang, ff.File.Lang)
	assert.Equal(t, c, ff.File.Coord)

	descriptors := make([]string, 0, len(ff.Occurrences))
	for _, occ := range ff.Occurrences {
		descriptors = append(descriptors, occ.Descriptor.String())
	}
	assert.Contains(t, descriptors, "scip-csharp nuget Codiq.Greeter 1.0.0 greeter/Greeter#Greet().")
}

// TestRBParserSatisfiesParserStructurally is the seventh, and the claim it makes
// is the registry's rather than any one language's: seven sub-packages written
// independently satisfy Parser, none of them imports extract, and the byExt
// literal in extract.go is the whole of the compile-time check that they do.
func TestRBParserSatisfiesParserStructurally(t *testing.T) {
	var p extract.Parser = rb.New()
	assert.NotNil(t, p)
}

// TestRegisteredRubyParserParses is TestRegisteredParserParses for Ruby: the
// entry for ".rb" is a working parser and not just a non-nil interface value,
// and the descriptor it produces carries the RubyGems coordinate — with the
// namespace read off the `module`/`class` nesting and not off `lib/greeter/`,
// which is the one part of Ruby's namespace rule that shows up in a five-line
// file.
func TestRegisteredRubyParserParses(t *testing.T) {
	path := filepath.FromSlash("/repo/lib/greeter/greeter.rb")
	p, ok := extract.ParserFor(path)
	require.True(t, ok)

	c := coord.Coord{
		Scheme: coord.RubyScheme, Manager: coord.GemManager,
		Name: "greeter", Version: "1.0.0", Root: filepath.FromSlash("/repo"),
	}
	ff := p.Parse(path, []byte("module Greeter\n  class Greeter\n    def greet\n      \"hi\"\n    end\n  end\nend\n"), c)

	require.Empty(t, ff.ParseError)
	assert.Equal(t, rb.Lang, ff.File.Lang)
	assert.Equal(t, c, ff.File.Coord)

	descriptors := make([]string, 0, len(ff.Occurrences))
	for _, occ := range ff.Occurrences {
		descriptors = append(descriptors, occ.Descriptor.String())
	}
	assert.Contains(t, descriptors, "scip-ruby gem greeter 1.0.0 Greeter/Greeter#greet().")
}
