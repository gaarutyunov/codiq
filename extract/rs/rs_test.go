package rs_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/extract/rs"
	"github.com/gaarutyunov/codiq/facts"
)

// testCoord is the coordinate the batch would hand the parser for a file in the
// crate codiq-greeter, rooted at the fixture directory.
func testCoord(t *testing.T) coord.Coord {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "greeter"))
	require.NoError(t, err)
	coords, err := coord.Resolve(root)
	require.NoError(t, err)
	c := coords.For("x" + rs.Ext)
	require.Equal(t, coord.RustScheme, c.Scheme, "the fixture must resolve through the cargo resolver")
	return c
}

// parse parses src as the file at name, which is interpreted relative to the
// crate root — the path is not incidental here the way it is for Go, because a
// Rust module's namespace is derived from its path.
func parse(t *testing.T, name, src string) facts.FileFacts {
	t.Helper()
	c := testCoord(t)
	ff := rs.New().Parse(filepath.Join(c.Root, filepath.FromSlash(name)), []byte(src), c)
	require.Empty(t, ff.ParseError)
	return ff
}

const prefix = "scip-rust cargo codiq-greeter 1.0.0"

// --------------------------------------------------------------- definitions --

func TestParseDefinitionDescriptors(t *testing.T) {
	ff := parse(t, "src/shapes.rs", `
use std::fmt::Display;

pub const LEVEL: u32 = 1;
static PLAIN: u32 = 2;

pub struct Loud {
    pub volume: u32,
}

pub enum Mood {
    Happy,
    Sad,
}

pub trait Speaker {
    fn speak(&self) -> String;
}

impl Loud {
    pub fn new(volume: u32) -> Loud {
        let local = volume;
        Loud { volume: local }
    }
}

impl Speaker for Loud {
    fn speak(&self) -> String {
        String::new()
    }
}

pub mod inner {
    pub struct Nested;

    pub fn helper<T: Display>(n: T) -> T {
        n
    }
}

pub fn run(s: &Loud, n: u32) -> u32 {
    for i in 0..n {
        let _ = i;
    }
    s.volume
}

pub type Alias = Loud;
`)

	// The whole set, so that a suffix rule which starts producing an *extra*
	// definition fails here too. Keyed by descriptor rather than by name
	// because `speak` and `n` each name two different symbols.
	want := []string{
		// The module the file is, named by its path rather than by a clause.
		prefix + " shapes/",
		prefix + " shapes/LEVEL.",
		prefix + " shapes/PLAIN.",
		prefix + " shapes/Loud#",
		prefix + " shapes/Loud#volume.",
		prefix + " shapes/Mood#",
		prefix + " shapes/Mood#Happy.",
		prefix + " shapes/Mood#Sad.",
		prefix + " shapes/Speaker#",
		prefix + " shapes/Speaker#speak().",
		// Both `impl` blocks put their members on the *type*, which is what
		// makes `implements` derivable at all: link compares method sets by
		// descriptor suffix, so `Loud#speak().` is what has to line up with
		// `Speaker#speak().`.
		prefix + " shapes/Loud#new().",
		prefix + " shapes/Loud#new().(volume)",
		prefix + " shapes/Loud#new().local.",
		prefix + " shapes/Loud#speak().",
		// An inline module is a real level of the descriptor, not a flattening.
		prefix + " shapes/inner/",
		prefix + " shapes/inner/Nested#",
		prefix + " shapes/inner/helper().",
		prefix + " shapes/inner/helper().(T)",
		prefix + " shapes/inner/helper().(n)",
		prefix + " shapes/run().",
		prefix + " shapes/run().(s)",
		prefix + " shapes/run().(n)",
		prefix + " shapes/run().i.",
		prefix + " shapes/Alias#",
	}
	sort.Strings(want)
	assert.Equal(t, want, definitionDescriptors(ff))
}

func TestParseDefinitionKinds(t *testing.T) {
	ff := parse(t, "src/kinds.rs", `
pub const MAX: u32 = 3;
static LOOSE: u32 = 4;

pub struct S {
    pub attr: u32,
}

pub enum E { One }

pub union U { a: u32 }

pub trait T {
    fn required(&self) -> u32;
    fn provided(&self) -> u32 { 0 }
}

impl S {
    pub fn method(&self, arg: u32) -> u32 {
        let local = arg;
        local
    }
}

pub fn free(p: u32) -> u32 { p }

pub mod m {
    pub fn also_free() {}
}
`)

	defs := definitionsByName(ff)
	tests := []struct {
		name string
		want string
	}{
		{"MAX", facts.KindConstant},
		{"LOOSE", facts.KindConstant},
		{"S", facts.KindType},
		{"attr", facts.KindField},
		{"E", facts.KindType},
		{"One", facts.KindField},
		{"U", facts.KindType},
		// `trait` is a keyword and means exactly one thing, which is why Rust
		// needs none of the base-class inspection the Python stanza does.
		{"T", facts.KindInterface},
		// A trait's required method has no body and is still a method.
		{"required", facts.KindMethod},
		{"provided", facts.KindMethod},
		{"method", facts.KindMethod},
		{"arg", facts.KindParameter},
		{"local", facts.KindVariable},
		{"free", facts.KindFunction},
		{"m", facts.KindPackage},
		// Inside a `mod` and not inside an `impl`, so a function.
		{"also_free", facts.KindFunction},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			def, ok := defs[tc.name]
			require.True(t, ok, "no definition named %q", tc.name)
			assert.Equal(t, tc.want, def.SymbolKind)
		})
	}
}

// The same `fn` node is a method or a free function depending on nothing but
// what encloses it, which is the one distinction Rust's grammar does not draw
// for the mapper.
func TestParseKindDependsOnTheEnclosingContainer(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"at the top level", "pub fn f() {}\n", facts.KindFunction},
		{"in a module", "pub mod m {\n    pub fn f() {}\n}\n", facts.KindFunction},
		{"in an inherent impl", "pub struct S;\nimpl S {\n    pub fn f(&self) {}\n}\n", facts.KindMethod},
		{"in a trait impl", "pub struct S;\npub trait T { fn f(&self); }\nimpl T for S {\n    fn f(&self) {}\n}\n", facts.KindMethod},
		{"in a trait", "pub trait T {\n    fn f(&self);\n}\n", facts.KindMethod},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ff := parse(t, "src/kind.rs", tc.src)
			assert.Equal(t, tc.want, definitionNamed(t, ff, "f").SymbolKind)
		})
	}
}

// --------------------------------------------------------------- namespaces --

// The namespace decision, stated as a table. Rust has three module-root file
// names where Python has one and TypeScript has one, and each collapses to its
// directory for the same reason: the file and the directory have to render one
// namespace or the two sides of a `mod` declaration cannot agree.
func TestModuleNamespaceFollowsTheCargoLayout(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"a crate root library", "src/lib.rs", prefix},
		{"a crate root binary", "src/main.rs", prefix},
		{"a top-level module", "src/greeter.rs", prefix + " greeter/"},
		{"a nested module", "src/util/text.rs", prefix + " util/text/"},
		{"a directory module", "src/util/mod.rs", prefix + " util/"},
		{"a deeply nested directory module", "src/a/b/mod.rs", prefix + " a/b/"},
		// `mod.rs` makes a directory a module at any depth; a crate root is one
		// only at the top. Collapsing this would hand it the namespace
		// `src/util.rs` is entitled to.
		{"a crate root name below the top", "src/util/main.rs", prefix + " util/main/"},
		// `src/` is Cargo's layout and not a level of the module tree, so a
		// crate whose files sit at the root derives the same namespaces.
		{"no src directory", "greeter.rs", prefix + " greeter/"},
		{"a crate root without src", "lib.rs", prefix},
		// Outside `src/` there is no crate root convention, so nothing
		// collapses and the path is the namespace.
		{"an integration test", "tests/it.rs", prefix + " tests/it/"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ff := parse(t, tc.path, "pub const X: u32 = 1;\n")
			assert.Equal(t, tc.want, moduleDefinition(t, ff).Descriptor.String())
		})
	}
}

// TestModuleDefinitionMatchesModDeclaration pins what makes the link pass's
// `imports` derivation a plain descriptor join: the descriptor a file defines
// for itself as a module is byte-identical to the one the declaring file's
// `mod` or `use` reference carries for it.
//
// It matters more here than it does for Go, because neither side is written
// down anywhere. Go reads the importer's side out of an import path and the
// definition's side out of a `package` clause; both sides of this are *derived*
// — one from the file's own path, one from a `mod` name or a `::` path resolved
// against the declaring file's module — so nothing but this test says they
// agree.
func TestModuleDefinitionMatchesModDeclaration(t *testing.T) {
	tests := []struct {
		name     string
		defined  string // the file that is the module
		importer string // the file that names it
		stmt     string // the statement it names it by
	}{
		{"a sibling module", "src/greeter.rs", "src/lib.rs", "mod greeter;"},
		{"a sibling of a binary crate root", "src/greeter.rs", "src/main.rs", "mod greeter;"},
		{"an absolute use", "src/greeter.rs", "src/main.rs", "use crate::greeter::Greeter;"},
		{"a submodule of a directory module", "src/util/text.rs", "src/util/mod.rs", "mod text;"},
		{"a directory module from the crate root", "src/util/mod.rs", "src/lib.rs", "mod util;"},
		{"a nested absolute use", "src/util/text.rs", "src/main.rs", "use crate::util::text::Mood;"},
		{"a relative use up one level", "src/util/mod.rs", "src/util/text.rs", "use super::LEVEL;"},
		{"a relative use of this module", "src/util/mod.rs", "src/util/mod.rs", "use self::LEVEL;"},
		{"a uniform-path use", "src/greeter.rs", "src/lib.rs", "use greeter::Greeter;"},
		{"a glob", "src/greeter.rs", "src/lib.rs", "use crate::greeter::*;"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defined := parse(t, tc.defined, "pub const X: u32 = 1;\n")
			importer := parse(t, tc.importer, tc.stmt+"\n")

			assert.Equal(t,
				moduleDefinition(t, defined).Descriptor.String(),
				moduleReference(t, importer).Descriptor.String(),
				"%s naming %s by %q", tc.importer, tc.defined, tc.stmt)
		})
	}
}

// The toolchain's crates are not this one, and the only thing that says so is
// knowing what they are called: since the 2018 edition's uniform paths,
// `use std::fmt::Display` and `use greeter::Greeter` are the same statement. A
// toolchain crate therefore gets a foreign coordinate it cannot match anything
// indexed here from, and everything else is assumed to be this crate's —
// Rust's own resolution order with the dependency graph taken on trust.
func TestImportCoordinates(t *testing.T) {
	tests := []struct {
		name string
		stmt string
		want string
	}{
		{"the standard library", "use std::fmt::Display;", "scip-rust cargo std . fmt/"},
		{"core", "use core::mem::swap;", "scip-rust cargo core . mem/"},
		{"an extern crate declaration", "extern crate legacy;", "scip-rust cargo legacy ."},
		{"this crate, by crate::", "use crate::greeter::Greeter;", prefix + " greeter/"},
		{"this crate, by a uniform path", "use greeter::Greeter;", prefix + " greeter/"},
		// A third-party dependency is indistinguishable from a module of this
		// crate, so it lands in this crate's namespace — where nothing defines
		// it. An unresolved reference, never a wrong edge.
		{"a third-party crate", "use serde::Serialize;", prefix + " serde/"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ff := parse(t, "src/main.rs", tc.stmt+"\n")
			assert.Equal(t, tc.want, moduleReference(t, ff).Descriptor.String())
		})
	}
}

// ------------------------------------------------------------------- traits --

// The whole of what makes `implements` derive for Rust, and the reason no new
// edge kind was needed: an `impl` block's members hang off the *type*, so the
// method-set containment link already computes for Go's implicit interfaces
// sees `Loud#speak().` against `Speaker#speak().` and matches them.
//
// The `impl Trait for Type` syntax is recorded too, but as what it structurally
// is — a type reference to the trait — so the explicit declaration is navigable
// without the schema growing a column for it.
func TestImplMembersHangOffTheType(t *testing.T) {
	ff := parse(t, "src/loud.rs", `
pub trait Speaker {
    fn speak(&self) -> u32;
}

pub struct Loud;

impl Speaker for Loud {
    fn speak(&self) -> u32 { 1 }
}
`)

	assertHasDefinition(t, ff, prefix+" loud/Speaker#speak().")
	assertHasDefinition(t, ff, prefix+" loud/Loud#speak().")
	assert.Equal(t, facts.KindInterface, definitionNamed(t, ff, "Speaker").SymbolKind,
		"link's implements derivation keys off the interface kind")
	assert.Equal(t, facts.KindType, definitionNamed(t, ff, "Loud").SymbolKind)

	// The trait named in `impl … for …` is a type reference, and it resolves
	// to the trait's own definition inside this one file.
	trait := definitionNamed(t, ff, "Speaker")
	var resolved int
	for _, e := range ff.Edges {
		if e.Kind == facts.EdgeReferencesLocal && e.Target.ID == trait.ID {
			resolved++
		}
	}
	assert.Equal(t, 1, resolved, "the `impl Speaker for Loud` clause references the trait")
}

// A crate may add members to a type it does not own, which no earlier language
// in this graph can do. The descriptor follows the type rather than the file, so
// the member joins with the ones the owning crate declares instead of claiming
// a symbol this crate has no right to.
func TestImplOnAForeignTypeCarriesThatTypesCoordinate(t *testing.T) {
	ff := parse(t, "src/ext.rs", `
pub trait Loudly { fn shout(&self) -> u32; }

impl Loudly for String {
    fn shout(&self) -> u32 { 1 }
}
`)
	assertHasDefinition(t, ff, "scip-rust cargo std . String#shout().")
	assertHasDefinition(t, ff, prefix+" ext/Loudly#shout().")
}

// ---------------------------------------------------------------- references --

func TestParseReferenceDescriptors(t *testing.T) {
	ff := parse(t, "src/main.rs", `
mod greeter;

use crate::greeter::Greeter;
use std::fmt::Display;

pub enum Mood { Happy }

fn run(d: &dyn Display) -> String {
    let g = Greeter::new("world");
    let m = Mood::Happy;
    let _ = m;
    let _ = d;
    g.greet()
}
`)

	tests := []struct {
		name string
		of   string
		want string
	}{
		// `mod greeter;` names the module a sibling file is, which is what an
		// `imports` edge is derived from.
		{"a mod declaration", "greeter", prefix + " greeter/"},
		// `Greeter::new(…)` is a path, and a path is resolvable syntactically —
		// so unlike Python's `Greeter("world")`, this one is exact.
		{"an associated function", "new", prefix + " greeter/Greeter#new()."},
		// The receiver's type is read off the binding's initialiser.
		{"a method through an inferred type", "greet", prefix + " greeter/Greeter#greet()."},
		// An enum variant is reached by `::` and is a member of the type.
		// `src/main.rs` is a crate root, so its module namespace is empty.
		{"an enum variant", "Happy", prefix + " Mood#Happy."},
		// A trait from another crate keeps that crate's coordinate.
		{"a foreign type", "Display", "scip-rust cargo std . fmt/Display#"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, referenceNamed(t, ff, tc.of).Descriptor.String())
		})
	}
}

// Rust's two member operators ask two different questions, and only one of them
// needs a type. `::` resolves a path and is exact; `.` reaches a member of a
// value, so the mapper has to recover what the value is — from an annotation, or
// from an initialiser whose type is written in the source.
func TestParseResolvesAReceiverThroughItsType(t *testing.T) {
	tests := []struct {
		name string
		src  string
		call string
		want string
	}{
		{
			name: "an annotated binding",
			src:  "use crate::greeter::Greeter;\nfn f() {\n    let g: Greeter = make();\n    g.greet();\n}\n",
			call: "greet",
			want: prefix + " greeter/Greeter#greet().",
		},
		{
			name: "an associated-function initialiser",
			src:  "use crate::greeter::Greeter;\nfn f() {\n    let g = Greeter::new(\"x\");\n    g.greet();\n}\n",
			call: "greet",
			want: prefix + " greeter/Greeter#greet().",
		},
		{
			name: "a struct literal initialiser",
			src:  "use crate::greeter::Greeter;\nfn f() {\n    let g = Greeter { name: 1 };\n    g.greet();\n}\n",
			call: "greet",
			want: prefix + " greeter/Greeter#greet().",
		},
		{
			name: "a reference to a struct literal",
			src:  "use crate::greeter::Greeter;\nfn f() {\n    let g = &Greeter { name: 1 };\n    g.greet();\n}\n",
			call: "greet",
			want: prefix + " greeter/Greeter#greet().",
		},
		{
			name: "an annotated parameter",
			src:  "use crate::greeter::Greeter;\nfn f(g: &Greeter) {\n    g.greet();\n}\n",
			call: "greet",
			want: prefix + " greeter/Greeter#greet().",
		},
		{
			// `self` is a keyword rather than a name in scope, so it is
			// resolved from the enclosing `impl` — the one receiver a stanza
			// can always pin down.
			name: "the receiver inside an impl",
			src:  "pub struct S;\nimpl S {\n    fn a(&self) {}\n    fn b(&self) { self.a(); }\n}\n",
			call: "a",
			want: prefix + " S#a().",
		},
		{
			// Nothing in the source says what a free function returns, so the
			// type is unknowable file-locally and SCIP's "." is written for it —
			// which matches no definition rather than the wrong one.
			name: "an unknowable receiver",
			src:  "fn f() {\n    let g = make();\n    g.greet();\n}\n",
			call: "greet",
			want: prefix + " .#greet().",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ff := parse(t, "src/main.rs", tc.src)
			assert.Equal(t, tc.want, referenceNamed(t, ff, tc.call).Descriptor.String())
		})
	}
}

// An `as` alias renames the item locally, and the descriptor has to carry the
// name the *defining* module knows it by — otherwise the link pass joins on a
// name that exists in one file only.
func TestParseResolvesThroughAnAlias(t *testing.T) {
	ff := parse(t, "src/main.rs", `
use crate::greeter::Greeter as G;

fn f() {
    let g = G::new("x");
    g.greet();
}
`)
	assert.Equal(t, prefix+" greeter/Greeter#new().", referenceNamed(t, ff, "new").Descriptor.String())
	assert.Equal(t, prefix+" greeter/Greeter#greet().", referenceNamed(t, ff, "greet").Descriptor.String())
}

// A `::` path is resolved as a path even when it is several segments long, and
// the qualifier is what decides the coordinate. Resolving `Debug` on its own
// would put a standard-library trait in this crate's namespace.
func TestParseResolvesAQualifiedPath(t *testing.T) {
	ff := parse(t, "src/main.rs", "pub struct S;\n\nimpl std::fmt::Debug for S {}\n")
	assert.Equal(t, "scip-rust cargo std . fmt/Debug#", referenceNamed(t, ff, "Debug").Descriptor.String())
	assert.Equal(t, "scip-rust cargo std . fmt/", referenceNamed(t, ff, "fmt").Descriptor.String())
}

// The one limit that is Rust's alone. A macro invocation's arguments are an
// unparsed token tree, so the call written inside `println!` is not a call as
// far as the grammar is concerned — and the stanza does not guess at the
// tokens, because a reference invented from an unparsed stream is exactly the
// wrong edge §7's descriptor join would then materialise.
func TestMacroArgumentsAreOpaque(t *testing.T) {
	ff := parse(t, "src/main.rs", `
use crate::greeter::Greeter;

fn f() {
    let g = Greeter::new("x");
    println!("{}", g.greet());
}
`)
	for _, o := range ff.Occurrences {
		assert.NotEqual(t, "greet", o.Name, "a macro's token tree is not parsed as code")
	}
}

// ---------------------------------------------------------------------- scopes --

// Rust's lexical scopes are the C-family ones, which is the opposite of the
// Python stanza's situation: a `block` really does scope its bindings, so it is
// a scope, and `mod` is one too because a module can sit strictly inside a file.
func TestScopesAreRustsOwn(t *testing.T) {
	ff := parse(t, "src/scopes.rs", `
pub mod m {
    pub struct S {
        pub f: u32,
    }

    impl S {
        pub fn go(&self) -> u32 {
            let c = |x: u32| x;
            {
                let inner = 1;
                let _ = inner;
            }
            c(1)
        }
    }
}
`)
	kinds := map[string]int{}
	for _, s := range ff.Scopes {
		kinds[s.Kind]++
	}
	assert.Equal(t, 1, kinds[facts.ScopeFile])
	assert.Equal(t, 1, kinds[facts.ScopePackage], "an inline module is a scope")
	// The struct, and the impl block.
	assert.Equal(t, 2, kinds[facts.ScopeType])
	// The method, and the closure.
	assert.Equal(t, 2, kinds[facts.ScopeFunction])
	// The method body and the bare block inside it.
	assert.Equal(t, 2, kinds[facts.ScopeBlock])

	// A `mod foo;` declaration is not a scope: it declares that a module is
	// elsewhere and encloses nothing.
	decl := parse(t, "src/decl.rs", "mod other;\n")
	for _, s := range decl.Scopes {
		assert.Equal(t, facts.ScopeFile, s.Kind, "a mod declaration opens no scope")
	}
}

// The file's own module definition belongs to the file scope and not to whatever
// happens to start at byte zero.
func TestModuleDefinitionIsInTheFileScope(t *testing.T) {
	ff := parse(t, "src/first.rs", "pub mod inner {\n    pub const X: u32 = 1;\n}\n")
	require.NotEmpty(t, ff.Scopes)
	file := ff.Scopes[0]
	require.Equal(t, facts.ScopeFile, file.Kind)
	assert.Equal(t, file.ID, moduleDefinition(t, ff).Scope)
}

// --------------------------------------------------------------------- edges --

// Every edge an extractor may emit is intra-file (§2.5), and the only one whose
// endpoints are chosen rather than structural is references_local. Here it is
// the receiver's field, resolved within the one CST.
func TestParseResolvesSameFileReferences(t *testing.T) {
	ff := parse(t, "src/greeter.rs", `
pub struct Greeter {
    pub name: String,
}

impl Greeter {
    pub fn greet(&self) -> u32 {
        self.name
    }
}
`)

	def := definitionWithKind(t, ff, facts.KindField)
	require.Equal(t, prefix+" greeter/Greeter#name.", def.Descriptor.String())

	var resolved int
	for _, e := range ff.Edges {
		if e.Kind != facts.EdgeReferencesLocal {
			continue
		}
		assert.Equal(t, facts.VertexOccurrence, e.Source.Vertex)
		assert.Equal(t, facts.VertexOccurrence, e.Target.Vertex)
		if e.Target.ID == def.ID {
			resolved++
		}
	}
	assert.Equal(t, 1, resolved, "the read of self.name resolves to the field it declares")
}

func TestParseEmitsOnlyExtractableEdgeKinds(t *testing.T) {
	ff := parse(t, "src/greeter.rs", "pub struct S;\nimpl S {\n    fn m(&self) { self.m(); }\n}\n")

	for _, e := range ff.Edges {
		assert.Contains(t,
			[]facts.EdgeKind{facts.EdgeContains, facts.EdgeDefines, facts.EdgeReferencesLocal},
			e.Kind, "a derived edge kind escaped into extraction")
	}
}

// A `use` describes bytes an import already accounted for, so the `::` reference
// patterns must not emit a second, weaker occurrence over the same identifier.
func TestImportsClaimTheirIdentifiers(t *testing.T) {
	ff := parse(t, "src/main.rs", "use crate::greeter::Greeter;\n")

	spans := map[int]int{}
	for _, o := range ff.Occurrences {
		if o.RangeEnd > o.RangeStart {
			spans[o.RangeStart]++
		}
	}
	for start, n := range spans {
		assert.Equal(t, 1, n, "two occurrences at byte %d", start)
	}
}

// ---------------------------------------------------------------- the file ---

func TestParseFile(t *testing.T) {
	c := testCoord(t)
	path := filepath.Join(c.Root, "src", "greeter.rs")
	src, err := os.ReadFile(path)
	require.NoError(t, err)

	ff := rs.New().Parse(path, src, c)
	require.Empty(t, ff.ParseError)
	assert.Equal(t, rs.Lang, ff.File.Lang)
	assert.Equal(t, path, ff.File.Path)
	assert.Equal(t, c, ff.File.Coord)
}

// Tree-sitter is error-tolerant, so broken Rust does not fail the parse — it
// yields whatever the recovered CST holds. What the contract promises is that
// the call still returns, with the file populated, and never panics.
func TestParseBrokenSourceStillReturns(t *testing.T) {
	ff := parse(t, "src/broken.rs", "fn f( {\n    let = ;\nstruct\n")
	assert.Equal(t, rs.Lang, ff.File.Lang)
}

func TestParseEmptyFile(t *testing.T) {
	ff := parse(t, "src/empty.rs", "")
	assert.Equal(t, prefix+" empty/", moduleDefinition(t, ff).Descriptor.String())
}

// A file outside the coordinate's root has no namespace to derive, so it gets
// none rather than a wrong one.
func TestParseOutsideTheRoot(t *testing.T) {
	c := testCoord(t)
	ff := rs.New().Parse(filepath.FromSlash("/elsewhere/x.rs"), []byte("pub fn f() {}\n"), c)
	require.Empty(t, ff.ParseError)
	assert.Equal(t, prefix, moduleDefinition(t, ff).Descriptor.String())
}

// --- helpers ---------------------------------------------------------------

func definitionsByName(ff facts.FileFacts) map[string]facts.Occurrence {
	out := map[string]facts.Occurrence{}
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition {
			out[o.Name] = o
		}
	}
	return out
}

func definitionDescriptors(ff facts.FileFacts) []string {
	out := []string{}
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition {
			out = append(out, o.Descriptor.String())
		}
	}
	sort.Strings(out)
	return out
}

func assertHasDefinition(t *testing.T, ff facts.FileFacts, descriptor string) {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition && o.Descriptor.String() == descriptor {
			return
		}
	}
	t.Fatalf("no definition with descriptor %q in %v", descriptor, definitionDescriptors(ff))
}

func definitionNamed(t *testing.T, ff facts.FileFacts, name string) facts.Occurrence {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition && o.Name == name {
			return o
		}
	}
	t.Fatalf("no definition named %q", name)
	return facts.Occurrence{}
}

func referenceNamed(t *testing.T, ff facts.FileFacts, name string) facts.Occurrence {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference && o.Name == name {
			return o
		}
	}
	t.Fatalf("no reference named %q", name)
	return facts.Occurrence{}
}

func definitionWithKind(t *testing.T, ff facts.FileFacts, kind string) facts.Occurrence {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition && o.SymbolKind == kind {
			return o
		}
	}
	t.Fatalf("no %s definition", kind)
	return facts.Occurrence{}
}

func moduleDefinition(t *testing.T, ff facts.FileFacts) facts.Occurrence {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition && o.SymbolKind == facts.KindPackage {
			return o
		}
	}
	t.Fatal("no module definition")
	return facts.Occurrence{}
}

func moduleReference(t *testing.T, ff facts.FileFacts) facts.Occurrence {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference && o.SymbolKind == facts.KindPackage {
			return o
		}
	}
	t.Fatal("no module reference")
	return facts.Occurrence{}
}
