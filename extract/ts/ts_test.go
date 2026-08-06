package ts_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/extract/ts"
	"github.com/gaarutyunov/codiq/facts"
)

// testCoord is the coordinate the batch would hand the parser for a file in the
// package @codiq/greeter, rooted at the fixture directory.
func testCoord(t *testing.T) coord.Coord {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "greeter"))
	require.NoError(t, err)
	coords, err := coord.Resolve(root, "greeter")
	require.NoError(t, err)
	c := coords.For("x" + ts.Ext)
	require.Equal(t, coord.TSScheme, c.Scheme, "the fixture must resolve through the npm resolver")
	return c
}

// parse parses src as the file at path, which is interpreted relative to the
// fixture root — the path is not incidental here the way it is for Go, because
// a TypeScript module's namespace *is* its path.
func parse(t *testing.T, name, src string) facts.FileFacts {
	t.Helper()
	c := testCoord(t)
	return ts.New().Parse(filepath.Join(c.Root, name), []byte(src), c)
}

const prefix = "scip-typescript npm @codiq/greeter 1.0.0"

// --------------------------------------------------------------- definitions --

func TestParseDefinitionDescriptors(t *testing.T) {
	ff := parse(t, "shapes.ts", `
export class Loud {
  private prefix: string;
  constructor(prefix: string) {}
  speak(loud: boolean): string { return ""; }
}

export interface Speaker {
  speak(loud: boolean): string;
}

export type Name = string;
export enum Volume { Low, High = 2 }
export namespace Cfg { export const level = 1; }

export function run<T>(s: Speaker, opt?: number): void {
  const local = 1;
  let mutable = 2;
}
`)
	require.Empty(t, ff.ParseError)

	// The whole set, so that a suffix rule which starts producing an *extra*
	// definition fails here too. Keyed by descriptor rather than by name
	// because `speak` and `prefix` each name two different symbols.
	want := []string{
		// The module the file is, named by its path rather than by a clause.
		prefix + " shapes/",
		prefix + " shapes/Loud#",
		prefix + " shapes/Loud#prefix.",
		prefix + " shapes/Loud#constructor().",
		prefix + " shapes/Loud#constructor().(prefix)",
		prefix + " shapes/Loud#speak().",
		prefix + " shapes/Loud#speak().(loud)",
		prefix + " shapes/Speaker#",
		prefix + " shapes/Speaker#speak().",
		prefix + " shapes/Speaker#speak().(loud)",
		prefix + " shapes/Name#",
		prefix + " shapes/Volume#",
		prefix + " shapes/Volume#Low.",
		prefix + " shapes/Volume#High.",
		prefix + " shapes/Cfg/",
		prefix + " shapes/Cfg/level.",
		prefix + " shapes/run().",
		prefix + " shapes/run().[T]",
		prefix + " shapes/run().(s)",
		prefix + " shapes/run().(opt)",
		prefix + " shapes/run().local.",
		prefix + " shapes/run().mutable.",
	}
	sort.Strings(want)
	assert.Equal(t, want, definitionDescriptors(ff))
}

func TestParseDefinitionKinds(t *testing.T) {
	ff := parse(t, "kinds.ts", `
export class C { f: number = 1; m(): void {} }
export interface I { m(): void; f: number; }
export type A = string;
export enum E { X }
export namespace N { }
export function fn(p: string): void {}
const c = 1;
let v = 2;
`)
	require.Empty(t, ff.ParseError)

	tests := []struct {
		name string
		want string
	}{
		{"C", facts.KindType},
		{"I", facts.KindInterface},
		{"A", facts.KindType},
		{"E", facts.KindType},
		{"N", facts.KindModule},
		{"fn", facts.KindFunction},
		{"m", facts.KindMethod},
		{"p", facts.KindParameter},
		{"c", facts.KindConstant},
		{"v", facts.KindVariable},
		{"X", facts.KindField},
	}
	got := definitionsByName(ff)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			occ, ok := got[tc.name]
			require.Truef(t, ok, "no definition named %q", tc.name)
			assert.Equal(t, tc.want, occ.SymbolKind)
		})
	}
}

// TestParseModuleDefinitionMatchesImportReference pins what makes the link
// pass's `imports` derivation a plain descriptor join: the descriptor a file
// defines for itself as a module is byte-identical to the one an importer's
// @import reference carries for it.
//
// It matters more here than it does for Go, because neither side is written
// down anywhere. Go reads the importer's side out of an import path and the
// definition's side out of a `package` clause; both sides of this are *derived*
// from paths — one from the file's own, one from a specifier resolved against
// the importing file's directory — so nothing but this test says they agree.
func TestParseModuleDefinitionMatchesImportReference(t *testing.T) {
	tests := []struct {
		name     string
		defined  string // the file that defines the module
		importer string // the file that imports it
		spec     string // the specifier it imports it by
	}{
		{"sibling", "greeter.ts", "main.ts", "./greeter"},
		{"sibling with the emitted extension", "greeter.ts", "main.ts", "./greeter.js"},
		{"sibling with the source extension", "greeter.ts", "main.ts", "./greeter.ts"},
		{"down a directory", "lib/greeter.ts", "main.ts", "./lib/greeter"},
		{"up a directory", "greeter.ts", "cmd/main.ts", "../greeter"},
		{"a directory's index", "lib/index.ts", "main.ts", "./lib"},
		{"a directory's index, spelled out", "lib/index.ts", "main.ts", "./lib/index"},
		{"the package's own name", "lib/greeter.ts", "main.ts", "@codiq/greeter/lib/greeter"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defined := parse(t, tc.defined, "export const x = 1;\n")
			importer := parse(t, tc.importer, "import { x } from \""+tc.spec+"\";\n")

			var moduleDef string
			for _, o := range defined.Occurrences {
				if o.Role == facts.RoleDefinition && o.SymbolKind == facts.KindPackage {
					moduleDef = o.Descriptor.String()
				}
			}
			require.NotEmpty(t, moduleDef, "%s defines no module", tc.defined)

			var importRef string
			for _, o := range importer.Occurrences {
				if o.Role == facts.RoleReference && o.SymbolKind == facts.KindPackage {
					importRef = o.Descriptor.String()
				}
			}
			require.NotEmpty(t, importRef, "%s imports nothing", tc.importer)

			assert.Equal(t, moduleDef, importRef)
		})
	}
}

// TestParseSatisfiesLinkDerivationPredicates encodes the predicates link's
// RebuildAll selects on, so that a change to this mapper's symbol_kind
// assignment fails here rather than silently emptying a derived edge table.
//
// The four predicates, as link states them (store/sqlc/query.sql):
//
//	resolves_to  a reference whose descriptor equals a definition's
//	imports      a `package` definition matched by a `package` reference
//	calls        reference occurrence with symbol_kind in ('function','method')
//	implements   interface definition with symbol_kind = 'interface', matched by
//	             method-set containment over `#`-terminated descriptors
//
// Each is checked across two independently extracted files, which is the join
// link performs.
func TestParseSatisfiesLinkDerivationPredicates(t *testing.T) {
	speaker := parse(t, "speaker.ts", `
export interface Speaker {
  speak(): string;
}
`)
	loud := parse(t, "loud.ts", `
import { Speaker } from "./speaker";

export class Loud implements Speaker {
  speak(): string { return "!"; }
}

export function run(): void {
  const l = new Loud();
  l.speak();
}
`)
	require.Empty(t, speaker.ParseError)
	require.Empty(t, loud.ParseError)

	t.Run("imports joins a package reference to a package definition", func(t *testing.T) {
		def := definitionWithKind(t, speaker, facts.KindPackage)
		ref := referenceWithKind(t, loud, facts.KindPackage)
		assert.Equal(t, prefix+" speaker/", def.Descriptor.String())
		assert.Equal(t, def.Descriptor.String(), ref.Descriptor.String())
	})

	t.Run("calls joins a method reference to a method definition", func(t *testing.T) {
		// `l.speak()` in loud.ts names Loud#speak(), which loud.ts also defines,
		// so the same-file resolution below carries it; the cross-file shape is
		// the interface method, reached through the Speaker type annotation.
		def := definitionNamed(t, speaker, "speak")
		assert.Equal(t, facts.KindMethod, def.SymbolKind)
		assert.Equal(t, prefix+" speaker/Speaker#speak().", def.Descriptor.String())

		ref := referenceNamed(t, loud, "speak")
		assert.Contains(t, []string{facts.KindFunction, facts.KindMethod}, ref.SymbolKind)
	})

	t.Run("implements needs the interface kind and a shared method suffix", func(t *testing.T) {
		iface := definitionNamed(t, speaker, "Speaker")
		require.Equal(t, facts.KindInterface, iface.SymbolKind)
		require.True(t, strings.HasSuffix(iface.Descriptor.String(), "#"))

		class := definitionNamed(t, loud, "Loud")
		require.NotEqual(t, facts.KindInterface, class.SymbolKind)
		require.True(t, strings.HasSuffix(class.Descriptor.String(), "#"))

		// Method-set containment is over the suffix past the type's descriptor.
		assert.Equal(t, "speak().",
			strings.TrimPrefix(definitionNamed(t, speaker, "speak").Descriptor.String(), iface.Descriptor.String()))
		assert.Equal(t, "speak().",
			strings.TrimPrefix(definitionNamed(t, loud, "speak").Descriptor.String(), class.Descriptor.String()))
	})

	t.Run("resolves_to joins a type reference to the interface it names", func(t *testing.T) {
		iface := definitionNamed(t, speaker, "Speaker")
		ref := referenceNamed(t, loud, "Speaker")
		assert.Equal(t, facts.KindType, ref.SymbolKind)
		assert.Equal(t, iface.Descriptor.String(), ref.Descriptor.String())
	})
}

// -------------------------------------------------------------------- scopes --

func TestParseScopes(t *testing.T) {
	ff := parse(t, "scopes.ts", `
export class C {
  m(): void {
    if (true) { }
  }
}
export function f(): void {
  const g = () => { };
}
export namespace N { }
export interface I { }
export enum E { }
`)
	require.Empty(t, ff.ParseError)

	kinds := map[string]int{}
	for _, s := range ff.Scopes {
		kinds[s.Kind]++
	}
	assert.Equal(t, 1, kinds[facts.ScopeFile], "exactly one file scope")
	assert.Equal(t, 3, kinds[facts.ScopeType], "class, interface, enum")
	assert.Equal(t, 1, kinds[facts.ScopePackage], "the namespace declaration")
	assert.Positive(t, kinds[facts.ScopeFunction])
	assert.Positive(t, kinds[facts.ScopeBlock])

	// The file scope is the root, and every other scope hangs off something.
	for _, s := range ff.Scopes {
		if s.Kind == facts.ScopeFile {
			assert.Equal(t, facts.NoID, s.Parent, "the file scope has no parent")
			continue
		}
		assert.NotEqual(t, facts.NoID, s.Parent, "%s scope has no parent", s.Kind)
	}
}

func TestParseContainsEdgesCoverEveryRow(t *testing.T) {
	ff := parse(t, "cover.ts", `
import { x } from "./other";
export class C { f = 1; m(): void { const local = x; } }
`)
	require.Empty(t, ff.ParseError)

	containedScopes := map[facts.LocalID]bool{}
	containedOccurrences := map[facts.LocalID]bool{}
	for _, e := range ff.Edges {
		if e.Kind != facts.EdgeContains {
			continue
		}
		assert.Equal(t, facts.VertexScope, e.Source.Vertex, "a contains edge starts at a scope")
		switch e.Target.Vertex {
		case facts.VertexScope:
			containedScopes[e.Target.ID] = true
		case facts.VertexOccurrence:
			containedOccurrences[e.Target.ID] = true
		default:
			t.Fatalf("contains edge to a %s", e.Target.Vertex)
		}
	}

	for _, s := range ff.Scopes {
		if s.Parent == facts.NoID {
			continue
		}
		assert.Truef(t, containedScopes[s.ID], "scope %d has a parent but no contains edge", s.ID)
	}
	for _, o := range ff.Occurrences {
		if o.Scope == facts.NoID {
			continue
		}
		assert.Truef(t, containedOccurrences[o.ID], "occurrence %q has a scope but no contains edge", o.Name)
	}
}

// ---------------------------------------------------------------- edge kinds --

func TestParseEmitsOnlyExtractedEdgeKinds(t *testing.T) {
	ff := parse(t, "edges.ts", `
import { Greeter } from "./greeter";
export function main(): void {
  const g = new Greeter("world");
  g.greet();
}
`)
	require.Empty(t, ff.ParseError)
	require.NotEmpty(t, ff.Edges)

	extracted := map[facts.EdgeKind]bool{
		facts.EdgeDefines:         true,
		facts.EdgeContains:        true,
		facts.EdgeReferencesLocal: true,
	}
	for _, e := range ff.Edges {
		assert.Truef(t, extracted[e.Kind], "extractor emitted a derived edge kind %q", e.Kind)
	}
}

func TestParseDefinesEdgesStartAtTheFile(t *testing.T) {
	ff := parse(t, "defines.ts", "export class C { m(): void {} }\n")
	require.Empty(t, ff.ParseError)

	definitions := map[facts.LocalID]bool{}
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition {
			definitions[o.ID] = true
		}
	}
	require.NotEmpty(t, definitions)

	defined := map[facts.LocalID]bool{}
	for _, e := range ff.Edges {
		if e.Kind != facts.EdgeDefines {
			continue
		}
		assert.Equal(t, facts.FileRef(), e.Source, "a defines edge starts at the file")
		assert.Equal(t, facts.VertexOccurrence, e.Target.Vertex)
		assert.Truef(t, definitions[e.Target.ID], "defines edge to occurrence %d, which is not a definition", e.Target.ID)
		defined[e.Target.ID] = true
	}
	assert.Equal(t, definitions, defined, "every definition has exactly one defines edge")
}

// ---------------------------------------------------------------- references --

func TestParseReferencesLocal(t *testing.T) {
	ff := parse(t, "local.ts", `
export class Greeter {
  name: string;
  constructor(name: string) { this.name = name; }
  greet(): string { return this.name; }
}
export function make(): Greeter {
  const g = new Greeter("x");
  g.greet();
  return g;
}
`)
	require.Empty(t, ff.ParseError)

	byID := map[facts.LocalID]facts.Occurrence{}
	for _, o := range ff.Occurrences {
		byID[o.ID] = o
	}

	resolved := map[string]string{}
	for _, e := range ff.Edges {
		if e.Kind != facts.EdgeReferencesLocal {
			continue
		}
		source, target := byID[e.Source.ID], byID[e.Target.ID]
		assert.Equal(t, facts.RoleReference, source.Role)
		assert.Equal(t, facts.RoleDefinition, target.Role)
		assert.Equal(t, source.Descriptor.String(), target.Descriptor.String(),
			"a local reference edge is a descriptor match, exactly like the link pass's")
		resolved[source.Name] = target.Descriptor.String()
	}

	// `this.name` is the TypeScript counterpart of a Go method's receiver: the
	// one qualifier a file-local mapper can always resolve.
	assert.Equal(t, prefix+" local/Greeter#name.", resolved["name"])
	// `new Greeter(...)` gives `g` a syntactic type, which is what names
	// `g.greet()` without any type checking.
	assert.Equal(t, prefix+" local/Greeter#greet().", resolved["greet"])
	assert.Equal(t, prefix+" local/make().g.", resolved["g"])
}

func TestParseReferenceDescriptors(t *testing.T) {
	ff := parse(t, "refs.ts", `
import { Greeter, other as alias } from "./greeter";
import * as util from "node:util";
import Def from "@scope/pkg";

export function run(): void {
  const g = new Greeter("x");
  g.greet();
  alias();
  util.format("x");
  util.inspect;
  Def();
  console.log("x");
  unknownThing.method();
}
`)
	require.Empty(t, ff.ParseError)

	byName := map[string][]facts.Occurrence{}
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference {
			byName[o.Name] = append(byName[o.Name], o)
		}
	}

	tests := []struct {
		name       string
		descriptor string
		kind       string
		why        string
	}{
		{"Greeter", prefix + " greeter/Greeter#", facts.KindType,
			"a named import resolves to the exporting module's namespace"},
		{"greet", prefix + " greeter/Greeter#greet().", facts.KindMethod,
			"the receiver's syntactic type names the method"},
		{"alias", prefix + " greeter/other().", facts.KindFunction,
			"an aliased import carries the name the exporting module knows"},
		{"util", "scip-typescript npm node:util .", facts.KindPackage,
			"a namespace import is a module qualifier, like a Go import"},
		{"format", "scip-typescript npm node:util . format().", facts.KindFunction,
			"a call through a module qualifier names a function of that module"},
		{"inspect", "scip-typescript npm node:util . inspect.", facts.KindVariable,
			"a read through a module qualifier names a value of that module"},
		{"Def", "scip-typescript npm @scope/pkg . default().", facts.KindFunction,
			"a default import is bound to the exporting module's `default`"},
		{"console", "scip-typescript npm builtin . console.", facts.KindVariable,
			"an ambient global belongs to no package"},
		{"log", "scip-typescript npm builtin . console#log().", facts.KindMethod,
			"and neither do its members"},
		{"method", prefix + " refs/.#method().", facts.KindMethod,
			"an unresolvable receiver writes SCIP's marker rather than guessing"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			occs, ok := byName[tc.name]
			require.Truef(t, ok, "no reference named %q", tc.name)
			var got []string
			for _, o := range occs {
				got = append(got, o.Descriptor.String())
				if o.Descriptor.String() == tc.descriptor {
					assert.Equal(t, tc.kind, o.SymbolKind, tc.why)
					return
				}
			}
			t.Fatalf("no reference named %q with descriptor %q; got %v", tc.name, tc.descriptor, got)
		})
	}
}

func TestParseReferencesNeverShadowDefinitions(t *testing.T) {
	ff := parse(t, "shadow.ts", `
export class Greeter { greet(): string { return ""; } }
export interface Speaker { speak(): void; }
export function f(p: string): void {}
`)
	require.Empty(t, ff.ParseError)

	definitionSpans := map[[2]int]string{}
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition {
			definitionSpans[[2]int{o.RangeStart, o.RangeEnd}] = o.Name
		}
	}
	for _, o := range ff.Occurrences {
		if o.Role != facts.RoleReference {
			continue
		}
		name, clash := definitionSpans[[2]int{o.RangeStart, o.RangeEnd}]
		assert.Falsef(t, clash, "reference %q re-emits the definition of %q at the same range", o.Name, name)
	}
}

func TestParseNoOccurrenceEscapesTheFile(t *testing.T) {
	src := `
import { Greeter } from "./greeter";
export function main(): void {
  const g = new Greeter("x");
  g.greet();
}
`
	ff := parse(t, "bounds.ts", src)
	require.Empty(t, ff.ParseError)
	require.NotEmpty(t, ff.Occurrences)

	for _, o := range ff.Occurrences {
		assert.GreaterOrEqual(t, o.RangeStart, 0, "%q starts before the file", o.Name)
		assert.LessOrEqual(t, o.RangeEnd, len(src), "%q ends past the file", o.Name)
		assert.LessOrEqual(t, o.RangeStart, o.RangeEnd, "%q has an inverted range", o.Name)
	}
	for _, s := range ff.Scopes {
		assert.GreaterOrEqual(t, s.RangeStart, 0)
		assert.LessOrEqual(t, s.RangeEnd, len(src))
	}
	// Every edge endpoint is a row of this file (facts' self-containment
	// invariant): there is no id space in which it could be anything else.
	for _, e := range ff.Edges {
		for _, ref := range []facts.Ref{e.Source, e.Target} {
			switch ref.Vertex {
			case facts.VertexFile:
				assert.Equal(t, facts.NoID, ref.ID)
			case facts.VertexScope:
				assert.LessOrEqual(t, int(ref.ID), len(ff.Scopes))
			case facts.VertexOccurrence:
				assert.LessOrEqual(t, int(ref.ID), len(ff.Occurrences))
			}
		}
	}
}

// ------------------------------------------------------------ file behaviour --

func TestParseSetsFileRow(t *testing.T) {
	c := testCoord(t)
	path := filepath.Join(c.Root, "src", "main.ts")
	ff := ts.New().Parse(path, []byte("export const x = 1;\n"), c)

	assert.Equal(t, path, ff.File.Path)
	assert.Equal(t, ts.Lang, ff.File.Lang)
	assert.Equal(t, c, ff.File.Coord)
	assert.Empty(t, ff.ParseError)
}

func TestParseIsDeterministic(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "greeter", "greeter.ts"))
	require.NoError(t, err)

	first := parse(t, "greeter.ts", string(src))
	for i := 0; i < 4; i++ {
		assert.Equal(t, first, parse(t, "greeter.ts", string(src)), "parse %d differed", i)
	}
}

func TestParseBrokenSourceStillYieldsAFileRow(t *testing.T) {
	// tree-sitter is error-tolerant, so prose parses to a tree with nothing in
	// it rather than to a failure — the same shape the Go stanza has.
	ff := parse(t, "notes.ts", "This is prose, not TypeScript ((( ]]] }}}\n")
	assert.Equal(t, ts.Lang, ff.File.Lang)
	assert.Empty(t, ff.ParseError)
}

func TestParseEmptyCoordinate(t *testing.T) {
	// No manifest anywhere: the file still parses, and every descriptor says so
	// with SCIP's marker rather than inventing a package.
	ff := ts.New().Parse("/nowhere/x.ts", []byte("export function f(): void {}\n"), coord.Coord{})
	assert.Empty(t, ff.ParseError)
	assertHasDefinition(t, ff, ". . . . f().")
}

func TestParseIsSafeForConcurrentUse(t *testing.T) {
	c := testCoord(t)
	p := ts.New()
	src := []byte("export class C { m(): void {} }\n")

	done := make(chan facts.FileFacts, 8)
	for i := 0; i < cap(done); i++ {
		go func() { done <- p.Parse(filepath.Join(c.Root, "c.ts"), src, c) }()
	}
	first := <-done
	require.Empty(t, first.ParseError)
	for i := 1; i < cap(done); i++ {
		assert.Equal(t, first, <-done)
	}
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
	t.Fatalf("no definition with descriptor %q", descriptor)
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

func referenceWithKind(t *testing.T, ff facts.FileFacts, kind string) facts.Occurrence {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference && o.SymbolKind == kind {
			return o
		}
	}
	t.Fatalf("no %s reference", kind)
	return facts.Occurrence{}
}
