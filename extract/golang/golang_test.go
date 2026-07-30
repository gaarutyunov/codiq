package golang_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/extract/golang"
	"github.com/gaarutyunov/codiq/facts"
)

// testCoord is the coordinate the batch would hand the parser for a file in the
// root package of module github.com/foo/bar.
func testCoord(t *testing.T) coord.Coord {
	t.Helper()
	return coord.Coord{
		Scheme:  coord.GoScheme,
		Manager: coord.GoManager,
		Name:    "github.com/foo/bar",
		Version: coord.Unknown,
		Root:    filepath.FromSlash("/repo"),
	}
}

func parse(t *testing.T, path, src string) facts.FileFacts {
	t.Helper()
	ff := golang.New().Parse(path, []byte(src), testCoord(t))
	require.Empty(t, ff.ParseError, "query.scm must compile and the source must parse")
	return ff
}

const prefix = "scip-go gomod github.com/foo/bar ."

// --------------------------------------------------------------- definitions --

func TestParseDefinitionDescriptors(t *testing.T) {
	const src = `package pkg

import "fmt"

type Greeter struct {
	Name string
}

func (g *Greeter) Greet() string {
	return "hi " + g.Name
}

type Speaker interface {
	Greet() string
}

type Alias = Greeter

const Version = "v1"

var Global = 3

func Run[T any](in T) error {
	local := in
	_ = local
	return fmt.Errorf("x")
}
`
	ff := parse(t, filepath.FromSlash("/repo/pkg/greet.go"), src)

	// A file one directory down carries that directory as its namespace, which
	// the coordinate supplies — the stanza cannot know it from the CST. The
	// expectation is the exact set, so an extra or missing definition fails too.
	want := []string{
		prefix + " pkg/",                    // the package clause
		prefix + " pkg/Greeter#",            // type
		prefix + " pkg/Greeter#Name.",       // field, hung off its struct
		prefix + " pkg/Greeter#Greet().",    // method, hung off its receiver type
		prefix + " pkg/Greeter#Greet().(g)", // receiver, a SCIP parameter
		prefix + " pkg/Speaker#",            // interface type
		prefix + " pkg/Speaker#Greet().",    // interface method, not the struct's
		prefix + " pkg/Alias#",              // type alias
		prefix + " pkg/Version.",            // constant
		prefix + " pkg/Global.",             // package-level variable
		prefix + " pkg/Run().",              // generic function
		prefix + " pkg/Run().[T]",           // type parameter, in SCIP brackets
		prefix + " pkg/Run().(in)",          // parameter
		prefix + " pkg/Run().local.",        // local, scoped under its function
	}
	sort.Strings(want)

	var got []string
	for _, occ := range ff.Occurrences {
		if occ.Role == facts.RoleDefinition {
			got = append(got, occ.Descriptor.String())
		}
	}
	sort.Strings(got)

	assert.Equal(t, want, got)
}

func TestParseDefinitionKinds(t *testing.T) {
	const src = `package main

type T struct{ F int }

func (t T) M() {}

type I interface{ M() }

const C = 1

var V = 2

func F(p int) { l := p; _ = l }
`
	ff := parse(t, filepath.FromSlash("/repo/main.go"), src)

	kinds := map[string]string{}
	for _, occ := range ff.Occurrences {
		if occ.Role == facts.RoleDefinition {
			kinds[occ.Descriptor.Suffix] = occ.SymbolKind
		}
	}

	tests := []struct {
		suffix string
		want   string
	}{
		{"T#", facts.KindType},
		{"T#F.", facts.KindField},
		{"T#M().", facts.KindMethod},
		// An interface is its own kind, not a flavour of `type`: the link pass
		// keys `implements` off it, and seed.sql writes it this way.
		{"I#", facts.KindInterface},
		{"I#M().", facts.KindMethod},
		{"C.", facts.KindConstant},
		{"V.", facts.KindVariable},
		{"F().", facts.KindFunction},
		{"F().(p)", facts.KindParameter},
		{"F().l.", facts.KindVariable},
	}
	for _, tt := range tests {
		t.Run(tt.suffix, func(t *testing.T) {
			assert.Equal(t, tt.want, kinds[tt.suffix])
		})
	}
}

// TestParsePackageDefinitionMatchesImportReference pins what makes the link
// pass's `imports` derivation a plain descriptor join: the descriptor a file's
// package clause defines is byte-identical to the one an importer's @import
// reference carries for that package.
func TestParsePackageDefinitionMatchesImportReference(t *testing.T) {
	imported := parse(t, filepath.FromSlash("/repo/internal/pretty/pretty.go"), "package pretty\n")
	importer := parse(t, filepath.FromSlash("/repo/main.go"), `package main

import "github.com/foo/bar/internal/pretty"
`)

	def, ok := findOccurrence(imported, facts.RoleDefinition, "pretty")
	require.True(t, ok, "the package clause is a definition")
	assert.Equal(t, facts.KindPackage, def.SymbolKind)
	assert.Equal(t, prefix+" internal/pretty/", def.Descriptor.String())

	ref, ok := findOccurrence(importer, facts.RoleReference, "pretty")
	require.True(t, ok, "the import spec is a reference")
	assert.Equal(t, def.Descriptor.String(), ref.Descriptor.String())

	rootPkg, ok := findOccurrence(importer, facts.RoleDefinition, "main")
	require.True(t, ok)
	assert.Equal(t, prefix, rootPkg.Descriptor.String(),
		"a file in the module root defines the package with an empty namespace")
}

// TestParseMatchesSeedEncoding checks the extractor against
// deploy/seed/seed.sql — the hand-extracted M1 corpus, which is the shipped
// statement of what these rows are supposed to look like. The source below is
// the seed's internal/graph package; the expectations are its literal
// descriptor, role and symbol_kind values.
func TestParseMatchesSeedEncoding(t *testing.T) {
	seedCoord := coord.Coord{
		Scheme: coord.GoScheme, Manager: coord.GoManager,
		Name: "github.com/gaarutyunov/codiq", Version: "v0.1.0",
		Root: filepath.FromSlash("/repo"),
	}
	const seedPrefix = "scip-go gomod github.com/gaarutyunov/codiq v0.1.0"

	iface := golang.New().Parse(filepath.FromSlash("/repo/internal/graph/iface.go"), []byte(`package graph

type Storer interface {
	Put(k string) error
}
`), seedCoord)
	require.Empty(t, iface.ParseError)

	store := golang.New().Parse(filepath.FromSlash("/repo/internal/graph/store.go"), []byte(`package graph

type Store struct {
	db *DB
}

func (s *Store) Put(k string) error {
	return s.db.Write(k)
}
`), seedCoord)
	require.Empty(t, store.ParseError)

	type row struct {
		role       facts.Role
		symbolKind string
		descriptor string
	}
	rows := func(ff facts.FileFacts, name string) []row {
		var out []row
		for _, occ := range ff.Occurrences {
			if occ.Name == name {
				out = append(out, row{occ.Role, occ.SymbolKind, occ.Descriptor.String()})
			}
		}
		return out
	}

	tests := []struct {
		name  string
		facts facts.FileFacts
		ident string
		want  row
	}{
		{
			name: "an interface definition", facts: iface, ident: "Storer",
			want: row{facts.RoleDefinition, facts.KindInterface, seedPrefix + " internal/graph/Storer#"},
		},
		{
			name: "an interface method", facts: iface, ident: "Put",
			want: row{facts.RoleDefinition, facts.KindMethod, seedPrefix + " internal/graph/Storer#Put()."},
		},
		{
			name: "a struct definition", facts: store, ident: "Store",
			want: row{facts.RoleDefinition, facts.KindType, seedPrefix + " internal/graph/Store#"},
		},
		{
			name: "a field definition", facts: store, ident: "db",
			want: row{facts.RoleDefinition, facts.KindField, seedPrefix + " internal/graph/Store#db."},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Contains(t, rows(tt.facts, tt.ident), tt.want)
		})
	}

	// The receiver's type name is a reference in the same file as its
	// definition, so extraction resolves it — seed.sql row c13.
	assert.Contains(t, rows(store, "Store"),
		row{facts.RoleReference, facts.KindType, seedPrefix + " internal/graph/Store#"})

	// `s.db` inside the method body — seed.sql row c14.
	assert.Contains(t, rows(store, "db"),
		row{facts.RoleReference, facts.KindField, seedPrefix + " internal/graph/Store#db."})

	// A method definition hangs off its receiver type, not off the file.
	assert.Contains(t, rows(store, "Put"),
		row{facts.RoleDefinition, facts.KindMethod, seedPrefix + " internal/graph/Store#Put()."})

	// Every role the store writes is one of the two the CHECK constraint allows.
	for _, ff := range []facts.FileFacts{iface, store} {
		for _, occ := range ff.Occurrences {
			assert.Contains(t, []facts.Role{facts.RoleDefinition, facts.RoleReference}, occ.Role)
		}
	}
}

// TestParseSatisfiesLinkDerivationPredicates encodes the predicates link's
// RebuildAll selects on, so that a change to this mapper's symbol_kind
// assignment fails here rather than silently emptying a derived edge table.
//
// The three predicates, as link states them:
//
//	calls        reference occurrence with symbol_kind in ('function','method')
//	type_defines reference occurrence with symbol_kind = 'type'
//	implements   interface definition with symbol_kind = 'interface'
//
// Each is checked by finding a reference this extractor emits whose descriptor
// matches a definition it emits for the same symbol — which is the join link
// performs, run here across two independently extracted files.
func TestParseSatisfiesLinkDerivationPredicates(t *testing.T) {
	c := coord.Coord{
		Scheme: coord.GoScheme, Manager: coord.GoManager,
		Name: "github.com/foo/bar", Version: coord.Unknown, Root: filepath.FromSlash("/repo"),
	}
	p := golang.New()

	defs := p.Parse(filepath.FromSlash("/repo/greeter.go"), []byte(`package main

type Speaker interface {
	Greet() string
}

type Greeter struct{ Name string }

func (g *Greeter) Greet() string { return g.Name }

func Free() {}
`), c)
	require.Empty(t, defs.ParseError)

	uses := p.Parse(filepath.FromSlash("/repo/main.go"), []byte(`package main

func main() {
	g := &Greeter{Name: "x"}
	_ = g.Greet()
	Free()
}
`), c)
	require.Empty(t, uses.ParseError)

	// definitionKind indexes the *other* file's definitions by descriptor, the
	// way link's self-join sees them.
	definitionKind := map[string]string{}
	for _, occ := range defs.Occurrences {
		if occ.Role == facts.RoleDefinition {
			definitionKind[occ.Descriptor.String()] = occ.SymbolKind
		}
	}

	// joined reports the kind link would see on a cross-file reference, plus the
	// kind of the definition it resolves to.
	joined := func(name string) (refKind, defKind string, ok bool) {
		for _, occ := range uses.Occurrences {
			if occ.Role != facts.RoleReference || occ.Name != name {
				continue
			}
			if dk, found := definitionKind[occ.Descriptor.String()]; found {
				return occ.SymbolKind, dk, true
			}
		}
		return "", "", false
	}

	tests := []struct {
		name        string
		ident       string
		wantRefKind []string
		wantDefKind string
		derives     string
	}{
		{
			name: "a method call", ident: "Greet",
			wantRefKind: []string{facts.KindFunction, facts.KindMethod},
			wantDefKind: facts.KindMethod, derives: "calls",
		},
		{
			name: "a package-level function call", ident: "Free",
			wantRefKind: []string{facts.KindFunction, facts.KindMethod},
			wantDefKind: facts.KindFunction, derives: "calls",
		},
		{
			name: "a type reference", ident: "Greeter",
			wantRefKind: []string{facts.KindType},
			wantDefKind: facts.KindType, derives: "type_defines",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refKind, defKind, ok := joined(tt.ident)
			require.True(t, ok,
				"%q must resolve across files by descriptor, or link derives no %s row", tt.ident, tt.derives)
			assert.Contains(t, tt.wantRefKind, refKind,
				"link selects %s on the reference's symbol_kind", tt.derives)
			assert.Equal(t, tt.wantDefKind, defKind)
		})
	}

	// implements: link needs the interface definition distinguishable from the
	// struct that satisfies it.
	assert.Equal(t, facts.KindInterface,
		definitionKind["scip-go gomod github.com/foo/bar . Speaker#"],
		"link keys implements off the interface definition's symbol_kind")
	assert.Equal(t, facts.KindType,
		definitionKind["scip-go gomod github.com/foo/bar . Greeter#"],
		"the struct satisfying it stays a plain type")

	// And the method-set containment link compares is over descriptor suffixes,
	// so the interface method and the struct method must agree on their tails.
	assert.Contains(t, definitionKind, "scip-go gomod github.com/foo/bar . Speaker#Greet().")
	assert.Contains(t, definitionKind, "scip-go gomod github.com/foo/bar . Greeter#Greet().")
}

// -------------------------------------------------------------------- scopes --

func TestParseScopes(t *testing.T) {
	const src = `package main

func F() {
	if true {
		x := 1
		_ = x
	}
}
`
	ff := parse(t, filepath.FromSlash("/repo/main.go"), src)

	require.NotEmpty(t, ff.Scopes)
	root := ff.Scopes[0]
	assert.Equal(t, facts.ScopeFile, root.Kind)
	assert.Equal(t, facts.NoID, root.Parent, "the file scope has no parent")
	assert.Equal(t, 0, root.RangeStart)
	assert.Equal(t, len(src), root.RangeEnd)

	kinds := make([]string, 0, len(ff.Scopes))
	for _, s := range ff.Scopes {
		kinds = append(kinds, s.Kind)
	}
	assert.Equal(t, []string{
		facts.ScopeFile, facts.ScopeFunction, facts.ScopeBlock, facts.ScopeBlock,
	}, kinds, "a function body block nests inside the function scope")

	// Every non-root scope is nested, and every scope's range is inside its
	// parent's: containment is the only thing that decides nesting.
	byID := map[facts.LocalID]facts.Scope{}
	for _, s := range ff.Scopes {
		byID[s.ID] = s
	}
	for _, s := range ff.Scopes[1:] {
		parent, ok := byID[s.Parent]
		require.True(t, ok, "scope %d has a parent", s.ID)
		assert.LessOrEqual(t, parent.RangeStart, s.RangeStart)
		assert.GreaterOrEqual(t, parent.RangeEnd, s.RangeEnd)
	}
}

func TestParseContainsEdgesCoverEveryRow(t *testing.T) {
	const src = `package main

type T struct{ F int }

func F() { x := 1; _ = x }
`
	ff := parse(t, filepath.FromSlash("/repo/main.go"), src)

	containedScopes := map[facts.LocalID]bool{}
	containedOccs := map[facts.LocalID]bool{}
	for _, e := range ff.Edges {
		if e.Kind != facts.EdgeContains {
			continue
		}
		require.Equal(t, facts.VertexScope, e.Source.Vertex, "contains always starts at a scope")
		switch e.Target.Vertex {
		case facts.VertexScope:
			containedScopes[e.Target.ID] = true
		case facts.VertexOccurrence:
			containedOccs[e.Target.ID] = true
		default:
			t.Fatalf("unexpected contains target vertex %q", e.Target.Vertex)
		}
	}

	for _, s := range ff.Scopes {
		if s.Parent == facts.NoID {
			continue
		}
		assert.True(t, containedScopes[s.ID], "scope %d is contained", s.ID)
	}
	for _, occ := range ff.Occurrences {
		if occ.Scope == facts.NoID {
			continue
		}
		assert.True(t, containedOccs[occ.ID], "occurrence %q is contained", occ.Name)
	}
}

// ---------------------------------------------------------------- edge kinds --

func TestParseEmitsOnlyExtractedEdgeKinds(t *testing.T) {
	const src = `package main

import "fmt"

type T struct{ F int }

func (t T) M() int { return t.F }

func main() {
	v := T{F: 1}
	fmt.Println(v.M())
}
`
	ff := parse(t, filepath.FromSlash("/repo/main.go"), src)

	allowed := map[facts.EdgeKind]bool{
		facts.EdgeDefines:         true,
		facts.EdgeContains:        true,
		facts.EdgeReferencesLocal: true,
	}
	for _, e := range ff.Edges {
		assert.True(t, allowed[e.Kind], "edge kind %q is link-derived, not extracted", e.Kind)
	}
}

func TestParseDefinesEdgesStartAtTheFile(t *testing.T) {
	const src = `package main

func A() {}
func B() {}
`
	ff := parse(t, filepath.FromSlash("/repo/main.go"), src)

	defined := map[facts.LocalID]bool{}
	for _, e := range ff.Edges {
		if e.Kind != facts.EdgeDefines {
			continue
		}
		assert.Equal(t, facts.FileRef(), e.Source, "defines starts at the file, which has no local id")
		assert.Equal(t, facts.VertexOccurrence, e.Target.Vertex)
		defined[e.Target.ID] = true
	}

	for _, occ := range ff.Occurrences {
		assert.Equal(t, occ.Role == facts.RoleDefinition, defined[occ.ID],
			"%q: defines covers exactly the definitions", occ.Name)
	}
}

// ---------------------------------------------------------------- references --

func TestParseReferencesLocal(t *testing.T) {
	const src = `package main

type Greeter struct{ Name string }

func (g *Greeter) Greet() string { return g.Name }

func main() {
	g := &Greeter{Name: "x"}
	_ = g.Greet()
}
`
	ff := parse(t, filepath.FromSlash("/repo/main.go"), src)

	byID := map[facts.LocalID]facts.Occurrence{}
	for _, occ := range ff.Occurrences {
		byID[occ.ID] = occ
	}

	var resolved []string
	for _, e := range ff.Edges {
		if e.Kind != facts.EdgeReferencesLocal {
			continue
		}
		src, dst := byID[e.Source.ID], byID[e.Target.ID]
		require.Equal(t, facts.RoleReference, src.Role)
		require.Equal(t, facts.RoleDefinition, dst.Role)
		require.Equal(t, src.Descriptor.String(), dst.Descriptor.String(),
			"references_local is a descriptor match, same as the link pass")
		resolved = append(resolved, fmt.Sprintf("%s -> %s", src.Name, dst.Descriptor.Suffix))
	}
	sort.Strings(resolved)

	// The receiver read, the field read through it, the composite-literal type,
	// and the method call all resolve inside this one file.
	assert.Contains(t, resolved, "Greet -> Greeter#Greet().")
	assert.Contains(t, resolved, "Greeter -> Greeter#")
	assert.Contains(t, resolved, "Name -> Greeter#Name.")
}

func TestParseReferenceDescriptors(t *testing.T) {
	const src = `package main

import (
	"fmt"
	pretty "github.com/foo/bar/internal/pretty"
	other "github.com/other/thing"
)

type Greeter struct{ Name string }

func (g *Greeter) Greet() string { return g.Name }

func Free() {}

func main() {
	g := &Greeter{Name: "x"}
	Free()
	_ = g.Greet()
	fmt.Println(g.Name)
	pretty.Print(g)
	other.Do()
	_ = len("x")
	unknown().Method()
}

func unknown() *Greeter { return nil }
`
	ff := parse(t, filepath.FromSlash("/repo/main.go"), src)

	// Descriptors of references, keyed by the byte offset they occur at, so the
	// same name at different sites stays distinguishable.
	got := map[int]facts.Occurrence{}
	for _, occ := range ff.Occurrences {
		if occ.Role == facts.RoleReference {
			got[occ.RangeStart] = occ
		}
	}
	descAt := func(name string, nth int) (facts.Occurrence, bool) {
		seen := 0
		offsets := make([]int, 0, len(got))
		for off := range got {
			offsets = append(offsets, off)
		}
		sort.Ints(offsets)
		for _, off := range offsets {
			if got[off].Name != name {
				continue
			}
			if seen == nth {
				return got[off], true
			}
			seen++
		}
		return facts.Occurrence{}, false
	}

	tests := []struct {
		name     string
		nth      int
		wantDesc string
		wantKind string
		why      string
	}{
		{
			name: "Free", nth: 0,
			wantDesc: prefix + " Free().", wantKind: facts.KindFunction,
			why: "a bare call resolves in this package",
		},
		{
			name: "Greet", nth: 0,
			wantDesc: prefix + " Greeter#Greet().", wantKind: facts.KindMethod,
			why: "the receiver's type comes from the composite literal it was assigned",
		},
		{
			name: "Println", nth: 0,
			wantDesc: "scip-go gomod fmt . Println().", wantKind: facts.KindFunction,
			why: "a stdlib call takes the import path as a foreign coordinate",
		},
		{
			name: "Print", nth: 0,
			wantDesc: prefix + " internal/pretty/Print().", wantKind: facts.KindFunction,
			why: "an in-module import keeps this coordinate and becomes a namespace",
		},
		{
			name: "Do", nth: 0,
			wantDesc: "scip-go gomod github.com/other/thing . Do().", wantKind: facts.KindFunction,
			why: "another module is foreign with an unknown version",
		},
		{
			name: "len", nth: 0,
			wantDesc: "scip-go gomod builtin . len().", wantKind: facts.KindFunction,
			why: "predeclared identifiers belong to no module",
		},
		{
			name: "Method", nth: 0,
			wantDesc: prefix + " .#Method().", wantKind: facts.KindMethod,
			why: "an uninferable receiver writes SCIP's dot rather than guessing a type",
		},
		{
			name: "fmt", nth: 0,
			wantDesc: "scip-go gomod fmt .", wantKind: facts.KindPackage,
			why: "an import occurrence names the package itself",
		},
		{
			name: "pretty", nth: 0,
			wantDesc: prefix + " internal/pretty/", wantKind: facts.KindPackage,
			why: "an in-module package descriptor equals the namespace its own files use",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			occ, ok := descAt(tt.name, tt.nth)
			require.True(t, ok, "reference %q not extracted", tt.name)
			assert.Equal(t, tt.wantDesc, occ.Descriptor.String(), tt.why)
			assert.Equal(t, tt.wantKind, occ.SymbolKind, tt.why)
		})
	}
}

func TestParseReferencesNeverShadowDefinitions(t *testing.T) {
	const src = `package main

type Greeter struct{ Name string }

func F(g Greeter) string { return g.Name }
`
	ff := parse(t, filepath.FromSlash("/repo/main.go"), src)

	// `(type_identifier) @reference.type` matches `Greeter` in `type Greeter`
	// too; the definition must win at that identifier.
	spans := map[[2]int]facts.Role{}
	for _, occ := range ff.Occurrences {
		key := [2]int{occ.RangeStart, occ.RangeEnd}
		_, dup := spans[key]
		require.False(t, dup, "two occurrences claim bytes %v (%q)", key, occ.Name)
		spans[key] = occ.Role
	}

	declOffset := strings.Index(src, "Greeter")
	assert.Equal(t, facts.RoleDefinition, spans[[2]int{declOffset, declOffset + len("Greeter")}])
}

func TestParseNoOccurrenceEscapesTheFile(t *testing.T) {
	const src = `package main

import "fmt"

func main() { fmt.Println("x") }
`
	ff := parse(t, filepath.FromSlash("/repo/main.go"), src)

	scopeIDs := map[facts.LocalID]bool{facts.NoID: true}
	for _, s := range ff.Scopes {
		scopeIDs[s.ID] = true
	}
	occIDs := map[facts.LocalID]bool{}
	for _, occ := range ff.Occurrences {
		occIDs[occ.ID] = true
	}

	for _, s := range ff.Scopes {
		assert.True(t, scopeIDs[s.Parent], "scope parent %d is local", s.Parent)
	}
	for _, occ := range ff.Occurrences {
		assert.True(t, scopeIDs[occ.Scope], "occurrence scope %d is local", occ.Scope)
		assert.GreaterOrEqual(t, occ.RangeEnd, occ.RangeStart)
		assert.LessOrEqual(t, occ.RangeEnd, len(src))
	}
	for _, e := range ff.Edges {
		for _, ref := range []facts.Ref{e.Source, e.Target} {
			switch ref.Vertex {
			case facts.VertexFile:
				assert.Equal(t, facts.NoID, ref.ID)
			case facts.VertexScope:
				assert.True(t, scopeIDs[ref.ID], "edge scope endpoint %d is local", ref.ID)
			case facts.VertexOccurrence:
				assert.True(t, occIDs[ref.ID], "edge occurrence endpoint %d is local", ref.ID)
			default:
				t.Fatalf("unexpected vertex kind %q", ref.Vertex)
			}
		}
	}
}

// ------------------------------------------------------------ file behaviour --

func TestParseSetsFileRow(t *testing.T) {
	c := testCoord(t)
	ff := golang.New().Parse(filepath.FromSlash("/repo/pkg/x.go"), []byte("package pkg\n"), c)

	assert.Equal(t, filepath.FromSlash("/repo/pkg/x.go"), ff.File.Path)
	assert.Equal(t, golang.Lang, ff.File.Lang)
	assert.Equal(t, c, ff.File.Coord)
	assert.Empty(t, ff.ParseError)
}

func TestParseIsDeterministic(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "greeter", "main.go"))
	require.NoError(t, err)

	p := golang.New()
	first := p.Parse(filepath.FromSlash("/repo/main.go"), src, testCoord(t))
	for i := 0; i < 5; i++ {
		assert.Equal(t, first, p.Parse(filepath.FromSlash("/repo/main.go"), src, testCoord(t)))
	}
}

func TestParseBrokenSourceStillYieldsAFileRow(t *testing.T) {
	// tree-sitter recovers rather than failing, so this asserts the weaker but
	// real guarantee: a file that does not parse cleanly still produces a
	// loadable File row and never panics.
	ff := parse(t, filepath.FromSlash("/repo/main.go"), "package main\n\nfunc (((\n")

	assert.Equal(t, filepath.FromSlash("/repo/main.go"), ff.File.Path)
	assert.Equal(t, golang.Lang, ff.File.Lang)
}

func TestParseEmptyFile(t *testing.T) {
	ff := parse(t, filepath.FromSlash("/repo/main.go"), "")

	assert.Empty(t, ff.Occurrences)
	assert.Empty(t, ff.Edges)
}

func TestParseConcurrent(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "greeter", "greeter.go"))
	require.NoError(t, err)

	p := golang.New()
	want := p.Parse(filepath.FromSlash("/repo/greeter.go"), src, testCoord(t))

	// The M2 loader parses on goroutines against one registry entry, so a shared
	// Parser must be safe for concurrent use.
	results := make(chan facts.FileFacts, 8)
	for i := 0; i < 8; i++ {
		go func() { results <- p.Parse(filepath.FromSlash("/repo/greeter.go"), src, testCoord(t)) }()
	}
	for i := 0; i < 8; i++ {
		assert.Equal(t, want, <-results)
	}
}

// ------------------------------------------------- the milestone's real input --

// TestExtractRealTwoFileModule runs the extractor over the two-file Go module
// the M2 acceptance test uses — a `main` calling a `Greeter` defined in another
// file — and prints every fact it produces. Run with -v to read the dump.
//
// The assertions pin the one thing the milestone turns on: the two files are
// extracted wholly independently, and `main.go`'s reference to Greet carries
// byte-for-byte the descriptor that `greeter.go`'s definition of Greet carries,
// so the link pass can join them with no extractor cooperation at all.
func TestExtractRealTwoFileModule(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("testdata", "greeter"))
	require.NoError(t, err)

	c, err := coord.FromGoMod(root)
	require.NoError(t, err)
	t.Logf("coordinate: %s  (root %s)", c.Prefix(), root)

	p := golang.New()
	extracted := map[string]facts.FileFacts{}
	for _, name := range []string{"greeter.go", "main.go"} {
		path := filepath.Join(root, name)
		src, err := os.ReadFile(path)
		require.NoError(t, err)

		ff := p.Parse(path, src, c)
		require.Empty(t, ff.ParseError)
		extracted[name] = ff
		t.Log("\n" + dump(name, ff))
	}

	greeter, main := extracted["greeter.go"], extracted["main.go"]

	// greeter.go defines the method.
	defDescriptor, ok := findOccurrence(greeter, facts.RoleDefinition, "Greet")
	require.True(t, ok, "greeter.go defines Greet")
	assert.Equal(t, "scip-go gomod github.com/foo/bar . Greeter#Greet().", defDescriptor.Descriptor.String())

	// main.go references it, with the identical descriptor and no edge — the
	// target is in another file, so resolving it is the link pass's job.
	refDescriptor, ok := findOccurrence(main, facts.RoleReference, "Greet")
	require.True(t, ok, "main.go references Greet")
	assert.Equal(t, defDescriptor.Descriptor.String(), refDescriptor.Descriptor.String(),
		"the cross-file join key matches without the extractor seeing both files")

	for _, e := range main.Edges {
		if e.Kind == facts.EdgeReferencesLocal && e.Source.ID == refDescriptor.ID {
			t.Errorf("main.go emitted a references_local edge for a cross-file target")
		}
	}

	// Same for the type itself: main.go's composite literal names Greeter.
	typeRef, ok := findOccurrence(main, facts.RoleReference, "Greeter")
	require.True(t, ok)
	typeDef, ok := findOccurrence(greeter, facts.RoleDefinition, "Greeter")
	require.True(t, ok)
	assert.Equal(t, typeDef.Descriptor.String(), typeRef.Descriptor.String())
}

func findOccurrence(ff facts.FileFacts, role facts.Role, name string) (facts.Occurrence, bool) {
	for _, occ := range ff.Occurrences {
		if occ.Role == role && occ.Name == name {
			return occ, true
		}
	}
	return facts.Occurrence{}, false
}

// dump renders a FileFacts the way the tables will hold it.
func dump(name string, ff facts.FileFacts) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "=== %s ===\n", name)
	fmt.Fprintf(&sb, "file      path=%s lang=%s pkg=(%s)\n\n", ff.File.Path, ff.File.Lang, ff.File.Coord.Prefix())

	fmt.Fprintf(&sb, "scopes (%d)\n", len(ff.Scopes))
	fmt.Fprintf(&sb, "  %-4s %-10s %-12s %s\n", "id", "kind", "range", "parent")
	for _, s := range ff.Scopes {
		fmt.Fprintf(&sb, "  %-4d %-10s %-12s %d\n", s.ID, s.Kind, rng(s.RangeStart, s.RangeEnd), s.Parent)
	}

	fmt.Fprintf(&sb, "\noccurrences (%d)\n", len(ff.Occurrences))
	fmt.Fprintf(&sb, "  %-4s %-10s %-9s %-9s %-10s %-8s %s\n", "id", "role", "kind", "name", "range", "scope", "descriptor")
	for _, o := range ff.Occurrences {
		fmt.Fprintf(&sb, "  %-4d %-10s %-9s %-9s %-10s %-8d %s\n",
			o.ID, o.Role, o.SymbolKind, o.Name, rng(o.RangeStart, o.RangeEnd), o.Scope, o.Descriptor.String())
	}

	fmt.Fprintf(&sb, "\nedges (%d)\n", len(ff.Edges))
	for _, e := range ff.Edges {
		fmt.Fprintf(&sb, "  %-16s %-14s -> %s\n", e.Kind, ref(e.Source), ref(e.Target))
	}
	return sb.String()
}

func rng(start, end int) string { return fmt.Sprintf("[%d,%d)", start, end) }

func ref(r facts.Ref) string {
	if r.Vertex == facts.VertexFile {
		return string(r.Vertex)
	}
	return fmt.Sprintf("%s#%d", r.Vertex, r.ID)
}
