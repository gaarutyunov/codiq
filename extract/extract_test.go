package extract_test

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/extract"
	"github.com/gaarutyunov/codiq/extract/cc"
	"github.com/gaarutyunov/codiq/extract/cs"
	"github.com/gaarutyunov/codiq/extract/golang"
	"github.com/gaarutyunov/codiq/extract/java"
	"github.com/gaarutyunov/codiq/extract/kotlin"
	"github.com/gaarutyunov/codiq/extract/php"
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
		{name: "a PHP compilation unit", path: filepath.FromSlash("/repo/src/Greeter/Greeter.php"), want: true},
		{name: "a PHP template, which is mostly HTML and needs its own grammar", path: filepath.FromSlash("/repo/views/index.phtml"), want: false},
		{name: "a legacy PHP extension no ecosystem owns", path: filepath.FromSlash("/repo/legacy/util.php5"), want: false},
		{name: "a Composer manifest, which is a manifest and not a source", path: filepath.FromSlash("/repo/composer.json"), want: false},
		{name: "a C translation unit", path: filepath.FromSlash("/repo/src/greeter.c"), want: true},
		{name: "a C header, which may be C or C++ and is read as C++", path: filepath.FromSlash("/repo/include/greeter.h"), want: true},
		{name: "a C++ translation unit", path: filepath.FromSlash("/repo/src/greeter.cpp"), want: true},
		{name: "the other two C++ source spellings", path: filepath.FromSlash("/repo/src/greeter.cxx"), want: true},
		{name: "a C++ header", path: filepath.FromSlash("/repo/include/greeter.hpp"), want: true},
		{name: "an inline fragment, which is included mid-header and is not a translation unit", path: filepath.FromSlash("/repo/include/greeter.inl"), want: false},
		{name: "the historical uppercase C++ source, which a case-insensitive filesystem cannot be trusted with", path: filepath.FromSlash("/repo/src/greeter.C"), want: false},
		{name: "a CMake listfile, which is a manifest and not a source", path: filepath.FromSlash("/repo/CMakeLists.txt"), want: false},
		{name: "an object file, which is a build output", path: filepath.FromSlash("/repo/src/greeter.o"), want: false},
		{name: "no extension", path: filepath.FromSlash("/repo/Makefile"), want: false},
		{name: "a Kotlin compilation unit", path: filepath.FromSlash("/repo/src/main/kotlin/app/App.kt"), want: true},
		{name: "a Kotlin script, which is Kotlin and is read with the same grammar", path: filepath.FromSlash("/repo/build.gradle.kts"), want: true},
		{name: "a Gradle settings file in the Groovy DSL, which is neither Kotlin nor a manifest this reads", path: filepath.FromSlash("/repo/settings.gradle"), want: false},
		{name: "a Kotlin module file, which the toolchain stopped writing in 1.4", path: filepath.FromSlash("/repo/module.ktm"), want: false},
		{name: "an unregistered extension", path: filepath.FromSlash("/repo/App.hs"), want: false},
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
	want := []string{cs.Ext, golang.Ext, java.Ext, php.Ext, py.Ext, rb.Ext, rs.Ext, ts.Ext}
	want = append(want, cc.Exts...)
	want = append(want, kotlin.Exts...)
	sort.Strings(want)
	assert.Equal(t, want, extract.Extensions())

	// The two stanzas that own more than one extension spell their sets out here
	// rather than only referencing them, so that growing either is a deliberate
	// edit in two places.
	assert.Equal(t, []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx"}, cc.Exts)
	assert.Equal(t, []string{".kt", ".kts"}, kotlin.Exts)
}

// TestCCParserSatisfiesParserStructurally is the ninth, and the claim it makes
// is the registry's rather than any one language's: nine sub-packages written
// independently satisfy Parser, none of them imports extract, and the byExt
// literal in extract.go is the whole of the compile-time check that they do.
func TestCCParserSatisfiesParserStructurally(t *testing.T) {
	var p extract.Parser = cc.New()
	assert.NotNil(t, p)
}

// TestRegisteredCCParserParses is TestRegisteredParserParses for C: the entry
// for ".c" is a working parser and not just a non-nil interface value, and the
// descriptor it produces carries the CMake coordinate — with **no namespace at
// all**, which is the one part of C's rule that shows up in a one-line file and
// the thing that lets a header in `include/` and a source in `src/` name one
// symbol.
func TestRegisteredCCParserParses(t *testing.T) {
	path := filepath.FromSlash("/repo/src/greeter.c")
	p, ok := extract.ParserFor(path)
	require.True(t, ok)

	c := coord.Coord{
		Scheme: coord.CCScheme, Manager: coord.CMakeManager,
		Name: "greeter", Version: "1.0.0", Root: filepath.FromSlash("/repo"),
	}
	ff := p.Parse(path, []byte("void greet(void) {}\n"), c)

	require.Empty(t, ff.ParseError)
	assert.Equal(t, cc.Lang, ff.File.Lang)
	assert.Equal(t, c, ff.File.Coord)

	descriptors := make([]string, 0, len(ff.Occurrences))
	for _, occ := range ff.Occurrences {
		descriptors = append(descriptors, occ.Descriptor.String())
	}
	assert.Contains(t, descriptors, "scip-cc cmake greeter 1.0.0 greet().")
}

// TestTheHeaderAndTheSourceShareOneParserAndOneLang is the registry-level half
// of the `.h` decision. Eight extensions, one Parser value, one `file.lang` —
// because a `.h` does not say whether it is C or C++, and tagging it as one
// language while its `.c` is another would make a pure-C project's
// header-resolves-into-source edge a cross-language edge (the invariant
// test/integration/m6_test.go's noCrossLanguageEdges asserts).
func TestTheHeaderAndTheSourceShareOneParserAndOneLang(t *testing.T) {
	c := coord.Coord{
		Scheme: coord.CCScheme, Manager: coord.CMakeManager,
		Name: "greeter", Version: "1.0.0", Root: filepath.FromSlash("/repo"),
	}
	for _, rel := range []string{"greeter.h", "greeter.c", "greeter.hpp", "greeter.cpp"} {
		path := filepath.FromSlash("/repo/" + rel)
		p, ok := extract.ParserFor(path)
		require.True(t, ok, rel)
		assert.Equal(t, cc.Lang, p.Parse(path, []byte("void greet(void);\n"), c).File.Lang, rel)
	}
}

// TestKotlinParserSatisfiesParserStructurally is the tenth, and the claim it
// makes is the registry's rather than any one language's: ten sub-packages
// written independently satisfy Parser, none of them imports extract, and the
// byExt literal in extract.go is the whole of the compile-time check that they
// do.
func TestKotlinParserSatisfiesParserStructurally(t *testing.T) {
	var p extract.Parser = kotlin.New()
	assert.NotNil(t, p)
}

// TestRegisteredKotlinParserParses is TestRegisteredParserParses for Kotlin: the
// entry for ".kt" is a working parser, and the descriptor it produces carries the
// namespace the file *declares* — which for Kotlin is the one reading that works,
// since the style guide tells you not to mirror the package in the directory
// tree.
func TestRegisteredKotlinParserParses(t *testing.T) {
	path := filepath.FromSlash("/repo/src/main/kotlin/greeter/Greeter.kt")
	p, ok := extract.ParserFor(path)
	require.True(t, ok)

	c := coord.Coord{
		Scheme: coord.KotlinScheme, Manager: coord.GradleManager,
		Name: "greeter", Version: coord.Unknown, Root: filepath.FromSlash("/repo"),
	}
	src := "package com.example.greeter\n\nclass Greeter {\n    fun greet(): String = \"\"\n}\n"
	ff := p.Parse(path, []byte(src), c)

	require.Empty(t, ff.ParseError)
	assert.Equal(t, kotlin.Lang, ff.File.Lang)
	assert.Equal(t, c, ff.File.Coord)

	descriptors := make([]string, 0, len(ff.Occurrences))
	for _, occ := range ff.Occurrences {
		descriptors = append(descriptors, occ.Descriptor.String())
	}
	assert.Contains(t, descriptors, "scip-kotlin gradle greeter . com/example/greeter/Greeter#greet().")
}

// TestAScriptAndASourceShareOneParserAndOneLang is the registry-level half of the
// `.kts` decision. Two extensions, one Parser value, one `file.lang` — a script
// is Kotlin, and tagging it as something else would make a Gradle build's own
// files a second language in every Kotlin repository indexed.
func TestAScriptAndASourceShareOneParserAndOneLang(t *testing.T) {
	c := coord.Coord{
		Scheme: coord.KotlinScheme, Manager: coord.GradleManager,
		Name: "greeter", Version: coord.Unknown, Root: filepath.FromSlash("/repo"),
	}
	for _, rel := range []string{"App.kt", "build.gradle.kts"} {
		path := filepath.FromSlash("/repo/" + rel)
		p, ok := extract.ParserFor(path)
		require.True(t, ok, rel)
		assert.Equal(t, kotlin.Lang, p.Parse(path, []byte("val x = 1\n"), c).File.Lang, rel)
	}
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

// TestPHPParserSatisfiesParserStructurally is the eighth, and the claim it makes
// is the registry's rather than any one language's: eight sub-packages written
// independently satisfy Parser, none of them imports extract, and the byExt
// literal in extract.go is the whole of the compile-time check that they do.
func TestPHPParserSatisfiesParserStructurally(t *testing.T) {
	var p extract.Parser = php.New()
	assert.NotNil(t, p)
}

// TestRegisteredPHPParserParses is TestRegisteredParserParses for PHP: the entry
// for ".php" is a working parser and not just a non-nil interface value, and the
// descriptor it produces carries the Composer coordinate — with the namespace
// read off the `namespace` statement and not off `src/Greeter/`, which is the one
// part of PHP's namespace rule that shows up in a six-line file.
func TestRegisteredPHPParserParses(t *testing.T) {
	path := filepath.FromSlash("/repo/src/Greeter/Greeter.php")
	p, ok := extract.ParserFor(path)
	require.True(t, ok)

	c := coord.Coord{
		Scheme: coord.PHPScheme, Manager: coord.ComposerManager,
		Name: "codiq/greeter", Version: "1.0.0", Root: filepath.FromSlash("/repo"),
	}
	src := "<?php\n\nnamespace greeter;\n\nclass Greeter\n{\n    public function greet(): string\n    {\n        return \"\";\n    }\n}\n"
	ff := p.Parse(path, []byte(src), c)

	require.Empty(t, ff.ParseError)
	assert.Equal(t, php.Lang, ff.File.Lang)
	assert.Equal(t, c, ff.File.Coord)

	descriptors := make([]string, 0, len(ff.Occurrences))
	for _, occ := range ff.Occurrences {
		descriptors = append(descriptors, occ.Descriptor.String())
	}
	assert.Contains(t, descriptors, "scip-php composer codiq/greeter 1.0.0 greeter/Greeter#greet().")
}
