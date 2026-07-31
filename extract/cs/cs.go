// Package cs is the C# stanza: the tree-sitter query in query.scm plus the
// mapper here that turns its captures into core facts (SPEC.md §5).
//
// The package is named for the extension, per SPEC.md §12. It imports facts and
// coord and deliberately not extract: it satisfies extract.Parser structurally,
// which is what keeps the registry free of an import cycle.
//
// The mapper's job, and its limits, are the Go, TypeScript, Python, Rust and
// Java stanzas' (§4.3): it builds the descriptor *suffix* from the CST and the
// namespace the file declares; it assigns role and neutral-core symbol kind; and
// it resolves references whose target definition is in the same file. It does no
// type checking, runs no name resolution algorithm and looks at no other file. A
// reference it cannot pin down is still emitted, carrying the best descriptor
// syntax allows, and the link pass decides what it means (§7). Where a component
// is genuinely unknowable file-locally the descriptor writes SCIP's "." for it,
// so it names an unresolved symbol rather than false-matching a real one.
//
// # The unit of modularity
//
// Go's is the directory, TypeScript's the file, Python's the file with
// `__init__.py` collapsing to its directory, Rust's the `mod` tree, Java's the
// `package` clause. C#'s is Java's — a namespace the file writes down — with two
// twists Java does not have, and they are why this stanza does not simply have a
// `b.ns` and stop there.
//
// The first is that C# spells a namespace two ways. `namespace Foo.Bar;` is the
// file-scoped form (C# 10+) and behaves exactly as Java's clause does: it is
// written once at the top and everything after it is a sibling of it in the CST,
// so it can only be read as a property of the file. `namespace Foo.Bar { … }` is
// the block form, and it is a real lexical region — it nests, and a file may
// hold several side by side.
//
// The second follows: a C# file's namespace is therefore not a single fact about
// the file. So the namespace is *not* a file-level constant here. It is built by
// the same ancestor walk that produces every other container component:
// containerSuffix contributes one namespace component per enclosing
// `namespace … { }` on the way out, and the file-scoped declaration is the base
// the walk terminates at. `namespace A { namespace B { class C } }` renders
// `A/B/C#` with no special case, and two namespace blocks in one file give their
// types two different prefixes, because they are in two different namespaces.
//
// What it cannot represent:
//
//   - `global using`. The directive means "for every file in this assembly", and
//     the files it affects do not write it down. It is read here exactly as the
//     plain `using` it is written like, which is right for the file that declares
//     it and silent for every other file in the assembly.
//   - `extern alias` and `global::`. Both name a thing by which *assembly* it
//     came from, and an assembly is not a component this descriptor has.
//     `alias_qualified_name` therefore resolves to nothing and its members land
//     on ".".
//   - A namespace alias. `using C = System.Collections;` is legal and aliases a
//     namespace; `using B = System.Text.StringBuilder;` aliases a type, and the
//     two have identical syntax. The type reading is taken, because it is what
//     an alias is nearly always for, and being wrong yields a descriptor ending
//     `#` where one ending `/` was meant — which matches nothing rather than
//     matching the wrong thing.
//   - A multi-project solution. `coord.Resolve` reads the manifests of one
//     directory — the repository root — so every project's files carry the root
//     property sheet's coordinate rather than their own. That is coord's
//     boundary and not this stanza's, and it is Rust's Cargo-workspace note and
//     Java's multi-module-Maven note verbatim.
//
// # Partial classes, and why the shared descriptor is the point
//
// `partial class Greeter` in two files declares *one* type, and both files
// render `Ns/Greeter#` for it. No earlier language in this graph can do that,
// and it is worth being explicit that it is what the link pass wants rather than
// something it has to survive.
//
// The descriptor is a coordinate and a structural path, and by construction it
// says nothing about which file a symbol was written in. Two definition rows
// with one descriptor are therefore two *sites* of one symbol, which is exactly
// what a partial class is. Every derivation reads it correctly and none of them
// needed to know:
//
//   - `resolves_to` joins a reference against every definition with its
//     descriptor, so a reference to `Greeter` resolves to both halves. That is
//     the right answer — the type genuinely is declared in both — and it is the
//     same shape as a Java overload, where two definitions share one descriptor
//     and a call reaches both.
//   - `implements` is the interesting one. Its method set is gathered by
//     `starts_with(member.descriptor, type.descriptor)` over the whole
//     occurrence table with no file predicate (store/sqlc/query.sql), so both
//     halves of a partial class see the *union* of the members declared in
//     either. A class that implements `IFoo.Bar()` in one file and `IFoo.Baz()`
//     in another satisfies `IFoo` — which is what the compiler says, and which a
//     per-file method set would have got wrong.
//
// The one thing it costs is counting: "Greeter is defined once" is false of a
// partial class, in the graph as in the language.
//
// # What one dot hides, and what C# takes away that Java had
//
// C# has one member operator, as Java does, and `a.b` is a member of a value, a
// member of a type, or a segment of a namespace with nothing in the syntax to
// say which. Java splits the three on its naming convention — lowercase segments
// are the package, the first uppercase one begins the type — and **that
// convention does not exist in C#**. `System.Text.StringBuilder` is PascalCase
// end to end, namespace segments and type alike, so the trick the Java stanza
// turns on is simply unavailable here.
//
// What replaces it is knowledge rather than convention, and it is still
// file-local. A segment is a namespace when this file has been *told* it is one:
// by a `using` directive, which in C# can name a namespace and nothing else; by
// the file's own namespace declaration; or by being one of the two roots the
// platform reserves. splitPath then takes the longest such prefix, and what
// follows it is types. For a path rooted at a platform namespace the reading is
// "everything but the last segment", because a coordinate this index does not own
// can never match anything in it anyway, so the legible reading is free.
//
// Where nothing says, the leading segment is resolved as a simple type name and
// the rest nest inside it — which is what makes `Registry.Entry` name a nested
// type in *this* file's namespace rather than a type `Entry` in a namespace
// `Registry` that does not exist.
//
// # What an overload hides, and what an explicit implementation does
//
// C# overloads on the parameter list exactly as Java does, and the descriptor's
// callable component carries the name and not the signature, so `Greet(string)`
// and `Greet(int)` both render `Greet().` and a reference to either resolves to
// both. java.go's reasoning applies unchanged: the definition side could number
// its overloads from one file's CST and the reference side could not, and a
// descriptor only one side can compute is worse than one both sides compute the
// same way.
//
// C# adds a second shape to the same trade. An explicit interface implementation
// — `string ISpeaker.Greet()` — is a member whose declaration names the interface
// it satisfies. It is descriptored as an ordinary member of its declaring type,
// `Greeter#Greet().`, with the qualifier dropped, for two reasons. It is what
// makes link's `implements` derivation see it at all: the derivation is method-set
// containment over suffixes, so a member spelled `Greeter#ISpeaker.Greet().`
// would not contain `ISpeaker#Greet().` and a class that implements an interface
// explicitly would implement nothing. And the alternative descriptor is one no
// reference site could reconstruct — an explicit implementation is only ever
// *called* through the interface, so the call writes `ISpeaker#Greet().` and
// never the qualified form — which is the same objection as numbering overloads.
// The cost is that a type declaring both `public void Greet()` and
// `void ISpeaker.Greet()` renders one descriptor for two members, which is the
// overload collision again and no worse.
package cs

import (
	_ "embed"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	sitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/facts"
)

// Lang is the value written to file.lang for the files this stanza handles.
const Lang = "cs"

// Ext is the file extension this stanza is registered under.
const Ext = ".cs"

//go:embed query.scm
var queryScheme string

// Parser is the C# stanza. Safe for concurrent use: the grammar and compiled
// query are immutable after the first Parse, and each parse checks a
// gotreesitter parser out of a pool.
type Parser struct {
	once    sync.Once
	lang    *sitter.Language
	pool    *sitter.ParserPool
	query   *sitter.Query
	initErr error
}

// New returns the C# parser. It is cheap: the grammar is loaded and query.scm
// compiled on the first Parse, so a binary that never parses C# never
// decompresses the C# grammar — and a query that fails to compile lands in
// ParseError rather than panicking at init.
func New() *Parser { return &Parser{} }

func (p *Parser) init() {
	p.once.Do(func() {
		p.lang = grammars.CSharpLanguage()
		if p.lang == nil {
			p.initErr = errors.New("cs: gotreesitter has no C# grammar")
			return
		}
		q, err := sitter.NewQuery(queryScheme, p.lang)
		if err != nil {
			p.initErr = fmt.Errorf("cs: compile query.scm: %w", err)
			return
		}
		p.query = q
		p.pool = sitter.NewParserPool(p.lang)
	})
}

// Parse extracts one C# file's facts. It never returns an error: a failure is
// reported in FileFacts.ParseError with File still populated, so the caller can
// tell "this file has no facts" from "this file was never seen".
func (p *Parser) Parse(filePath string, src []byte, c coord.Coord) facts.FileFacts {
	file := facts.File{Path: filePath, Lang: Lang, Coord: c}

	p.init()
	if p.initErr != nil {
		return facts.FileFacts{File: file, ParseError: p.initErr.Error()}
	}

	tree, err := p.pool.Parse(src)
	if err != nil {
		return facts.FileFacts{File: file, ParseError: err.Error()}
	}
	defer tree.Release()

	b := &builder{
		lang:       p.lang,
		src:        src,
		coord:      c,
		out:        facts.FileFacts{File: file},
		scopeByID:  map[facts.LocalID]scopeRec{},
		namespaces: map[string]bool{},
		types:      map[string]pathTarget{},
		claimed:    map[span]bool{},
		descIndex:  map[string]facts.LocalID{},
		defsByName: map[string][]defRec{},
	}
	return b.build(p.query.Execute(tree))
}

// span is an identifier's byte range, used to key the dedupe of overlapping
// captures.
type span struct{ start, end uint32 }

type scopeRec struct {
	id    facts.LocalID
	start uint32
	end   uint32
}

// defRec is a definition the mapper may need to look up again while resolving
// references: by name, within the scope it was declared in.
type defRec struct {
	occ      facts.LocalID
	scope    facts.LocalID
	start    uint32
	typeName string // syntactic type of a binding/parameter/field; "" when unknown
}

type builder struct {
	lang  *sitter.Language
	src   []byte
	coord coord.Coord
	// ns is the namespace a *file-scoped* declaration puts this whole file in:
	// the descriptor prefix the ancestor walk terminates at. "" for the global
	// namespace, and "" for a file that uses block namespaces instead — there the
	// namespace is contributed by containerSuffix, one component per block.
	ns string

	out       facts.FileFacts
	nextScope facts.LocalID
	nextOcc   facts.LocalID

	scopes    []scopeRec
	scopeByID map[facts.LocalID]scopeRec

	// namespaces holds every dotted path this file has been told is a namespace,
	// and every prefix of one: the namespace it declares, and the target of each
	// `using`. It is what stands in for Java's naming convention, which C# does
	// not have — see splitPath.
	namespaces map[string]bool
	// types holds simple names an alias bound to a type
	// (`using B = System.Text.StringBuilder;` binds `B`).
	types map[string]pathTarget
	// projectUsings holds the namespace suffixes of the plain `using`
	// directives that name a namespace of *this* artifact rather than the
	// platform's. It is what resolveTypeName falls back to; see there for why a
	// list of exactly one is the only length that can be used.
	projectUsings []string

	// claimed holds identifier ranges a definition or a using directive already
	// owns, so a reference pattern matching the same identifier is dropped.
	claimed map[span]bool
	// descIndex maps a definition's full descriptor to its occurrence — the
	// same-file half of what the link pass does across files.
	descIndex  map[string]facts.LocalID
	defsByName map[string][]defRec
}

func (b *builder) build(matches []sitter.QueryMatch) facts.FileFacts {
	b.collectScopes(matches)
	b.collectNamespaces(matches)
	b.collectImports(matches)
	b.collectDefinitions(matches)
	b.collectReferences(matches)

	sort.SliceStable(b.out.Edges, func(i, j int) bool {
		a, c := b.out.Edges[i], b.out.Edges[j]
		if a.Kind != c.Kind {
			return a.Kind < c.Kind
		}
		if a.Source != c.Source {
			if a.Source.Vertex != c.Source.Vertex {
				return a.Source.Vertex < c.Source.Vertex
			}
			return a.Source.ID < c.Source.ID
		}
		if a.Target.Vertex != c.Target.Vertex {
			return a.Target.Vertex < c.Target.Vertex
		}
		return a.Target.ID < c.Target.ID
	})
	return b.out
}

// ------------------------------------------------------------------ scopes ---

// collectScopes turns @scope.* captures into the file's containment skeleton.
// Nesting is pure byte containment: sorted by (start ascending, end
// descending), a stack of open scopes yields each scope's parent with no
// language knowledge at all.
func (b *builder) collectScopes(matches []sitter.QueryMatch) {
	type cand struct {
		kind string
		node *sitter.Node
	}
	var cands []cand
	for _, m := range matches {
		if root, _, ok := roots(m, "scope."); ok {
			cands = append(cands, cand{kind: suffixAfter(root.Name, "scope."), node: root.Node})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, c := cands[i].node, cands[j].node
		if a.StartByte() != c.StartByte() {
			return a.StartByte() < c.StartByte()
		}
		if a.EndByte() != c.EndByte() {
			return a.EndByte() > c.EndByte()
		}
		return cands[i].kind < cands[j].kind
	})

	var stack []scopeRec
	seen := map[span]bool{}
	for _, cd := range cands {
		s := span{cd.node.StartByte(), cd.node.EndByte()}
		if seen[s] {
			continue // two patterns matched the same node; the first kind wins
		}
		seen[s] = true

		for len(stack) > 0 && stack[len(stack)-1].end <= s.start {
			stack = stack[:len(stack)-1]
		}
		parent := facts.NoID
		if len(stack) > 0 {
			parent = stack[len(stack)-1].id
		}

		b.nextScope++
		rec := scopeRec{id: b.nextScope, start: s.start, end: s.end}
		b.out.Scopes = append(b.out.Scopes, facts.Scope{
			ID:         rec.id,
			Kind:       cd.kind,
			RangeStart: int(s.start),
			RangeEnd:   int(s.end),
			Parent:     parent,
		})
		if parent != facts.NoID {
			b.edge(facts.EdgeContains, facts.ScopeRef(parent), facts.ScopeRef(rec.id))
		}
		b.scopes = append(b.scopes, rec)
		b.scopeByID[rec.id] = rec
		stack = append(stack, rec)
	}
}

// enclosingScope returns the innermost scope containing [start, end).
func (b *builder) enclosingScope(start, end uint32) facts.LocalID {
	best := facts.NoID
	var bestStart uint32
	bestEnd := ^uint32(0)
	for _, s := range b.scopes {
		if s.start > start || s.end < end {
			continue
		}
		if best == facts.NoID || s.start > bestStart || (s.start == bestStart && s.end < bestEnd) {
			best, bestStart, bestEnd = s.id, s.start, s.end
		}
	}
	return best
}

func (b *builder) scopeRange(id facts.LocalID) (uint32, uint32, bool) {
	s, ok := b.scopeByID[id]
	return s.start, s.end, ok
}

// fileScope is the scope the whole file is, or NoID for an empty file.
// collectScopes sorts by (start ascending, end descending) and the
// `compilation_unit` node spans everything, so it is always the first scope
// emitted.
func (b *builder) fileScope() facts.LocalID {
	if len(b.scopes) == 0 {
		return facts.NoID
	}
	return b.scopes[0].id
}

// -------------------------------------------------------------- namespaces ---

// collectNamespaces records the namespaces this file declares and emits a
// definition for each.
//
// Three patterns produce a @definition.package match: the file-scoped
// declaration, the block declaration, and `(compilation_unit)` for a file that
// has neither. The first two are real declarations with an identifier to hang an
// occurrence on; the third is the global namespace and gets a zero-width point at
// byte 0, which is what every language before Java had to do for all of them.
//
// A file may produce several. Two `namespace A { }` / `namespace B { }` blocks
// declare two namespaces, and that is not an anomaly to be collapsed — the types
// inside them carry different prefixes, and each namespace is a symbol other
// files' `using` directives join against. Everything else about them is the Go
// stanza's package definition: neutral-core `package` kind, descriptor equal to
// the namespace, and a `defines` edge from the file.
func (b *builder) collectNamespaces(matches []sitter.QueryMatch) {
	type cand struct{ decl, name *sitter.Node }
	var cands []cand
	unit := false
	for _, m := range matches {
		root, name, ok := roots(m, "definition.package")
		if !ok {
			continue
		}
		if root.Node.Type(b.lang) == "compilation_unit" {
			unit = true
			continue
		}
		if name != nil {
			cands = append(cands, cand{decl: root.Node, name: name.Node})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		return cands[i].name.StartByte() < cands[j].name.StartByte()
	})

	if len(cands) == 0 {
		if unit {
			b.definePackage(b.fileScope(), "", 0, 0)
		}
		return
	}

	for _, cd := range cands {
		segs, _ := b.pathSegments(cd.name)
		if len(segs) == 0 {
			continue
		}
		b.claimSubtree(cd.name)

		_, outer := b.containerSuffix(cd.decl)
		suffix := outer + namespaceOf(segs)
		if cd.decl.Type(b.lang) == "file_scoped_namespace_declaration" && b.ns == "" {
			// The one form that is a property of the file: everything after it is
			// its sibling in the CST, so the ancestor walk can never find it and
			// it has to be the base the walk terminates at.
			b.ns = suffix
		}
		b.rememberNamespace(suffix)
		b.definePackage(b.enclosingScope(cd.name.StartByte(), cd.name.EndByte()),
			suffix, cd.name.StartByte(), cd.name.EndByte())
	}
}

// definePackage emits one namespace definition and indexes it.
func (b *builder) definePackage(scope facts.LocalID, suffix string, start, end uint32) {
	desc := facts.Descriptor{Prefix: b.coord, Suffix: suffix}
	occ := b.addOccurrenceIn(scope, desc, facts.RoleDefinition, facts.KindPackage,
		packageName(b.coord, suffix), start, end)
	b.edge(facts.EdgeDefines, facts.FileRef(), facts.OccurrenceRef(occ))
	if _, dup := b.descIndex[desc.String()]; !dup {
		b.descIndex[desc.String()] = occ
	}
}

// rememberNamespace records a namespace descriptor suffix, and every prefix of
// it, as a dotted path splitPath may recognise. Prefixes are included because a
// file that declares `A.B.C` has thereby said that `A` and `A.B` are namespaces
// too, which is the only thing that makes a partially qualified name readable.
func (b *builder) rememberNamespace(suffix string) {
	segs := strings.Split(strings.TrimSuffix(suffix, "/"), "/")
	for i := 1; i <= len(segs); i++ {
		if p := strings.Join(segs[:i], "."); p != "" {
			b.namespaces[p] = true
		}
	}
}

// ----------------------------------------------------------------- imports ---

// collectImports records what each `using` binds, and emits one occurrence per
// name the directive reaches.
//
// Import *edges* are cross-file and therefore the link pass's (§4.4); what is
// extracted is the occurrence, whose descriptor names the imported namespace.
// That descriptor is byte-identical to the one the target file's own namespace
// declaration produces — both are the dotted name with slashes — which is what
// lets link derive `imports` by descriptor join.
//
// The plain form is the one that has to be exactly right, and it is the one C#
// makes easy: `using a.b.c;` names a namespace and can name nothing else, so
// there is no package/type split to guess at and no convention to lean on. The
// alias and `using static` forms do have a split, and there it is the last
// segment that is the type — which the language states, since neither form may
// name a namespace's member.
//
// Every identifier the directive consumed is claimed, so the `.` reference
// patterns — which match happily inside a using directive — do not emit a second,
// weaker occurrence over the same bytes.
func (b *builder) collectImports(matches []sitter.QueryMatch) {
	type cand struct{ stmt *sitter.Node }
	var cands []cand
	for _, m := range matches {
		if root, _, ok := roots(m, "import"); ok {
			cands = append(cands, cand{stmt: root.Node})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		return cands[i].stmt.StartByte() < cands[j].stmt.StartByte()
	})
	for _, cd := range cands {
		b.usingDirective(cd.stmt)
	}
}

// usingDirective resolves one `using` and records what it binds.
func (b *builder) usingDirective(stmt *sitter.Node) {
	alias := stmt.ChildByFieldName("name", b.lang)
	path := lastNamedChild(stmt)
	if path == nil || (alias != nil && sameSpan(path, alias)) {
		return
	}
	b.claimSubtree(alias)
	b.claimSubtree(path)

	segs, nodes := b.pathSegments(path)
	if len(segs) == 0 {
		return
	}

	// A plain (or `global`) using names a namespace, whole. An alias or a
	// `using static` names a type, so the namespace is everything before the last
	// segment.
	nsSegs := segs
	if alias != nil || b.hasToken(stmt, "static") {
		nsSegs = segs[:len(segs)-1]
	}
	b.rememberNamespace(namespaceOf(nsSegs))

	c, ns := b.namespaceTarget(nsSegs)
	if len(nsSegs) > 0 {
		b.addOccurrence(
			facts.Descriptor{Prefix: c, Suffix: ns},
			facts.RoleReference, facts.KindPackage, nsSegs[len(nsSegs)-1],
			nodes[len(nsSegs)-1].StartByte(), nodes[len(nsSegs)-1].EndByte(),
		)
	}
	if len(nsSegs) == len(segs) {
		// A namespace imported on demand binds no *name* this file writes down:
		// the simple names it makes available are the other file's to state. What
		// it does record is that this file's unqualified type names may come from
		// there, which is the only written evidence a file-local reader has.
		if c == b.coord {
			b.projectUsings = append(b.projectUsings, ns)
		}
		return
	}

	// The type the path names, and the occurrence that makes the directive itself
	// navigable to it.
	target := pathTarget{coord: c, suffix: ns + segs[len(segs)-1] + "#"}
	b.addOccurrence(
		facts.Descriptor{Prefix: target.coord, Suffix: target.suffix},
		facts.RoleReference, facts.KindType, segs[len(segs)-1],
		nodes[len(segs)-1].StartByte(), nodes[len(segs)-1].EndByte(),
	)
	if alias != nil {
		b.types[b.text(alias)] = target
	}
	// `using static T;` binds T's static members on demand, and — unlike Java's
	// `import static a.b.C.m;` — C# has no form that names one of them. So there
	// is no simple name to record: an unqualified call in this file resolves to
	// the enclosing type, which is right everywhere except in a statically
	// imported call, and being wrong there costs a descriptor the enclosing type
	// has no member for, which matches nothing.
}

// ------------------------------------------------------------------- paths ---

// pathTarget is what a dotted path resolved to: the coordinate and descriptor
// suffix its members hang off.
type pathTarget struct {
	coord  coord.Coord
	suffix string
}

// pathSegments flattens a dotted name into its segments and the nodes that cover
// each prefix of it. `A.B.C` yields ["A","B","C"] and the three nodes spanning
// `A`, `A.B` and `A.B.C`, which is what lets an occurrence be emitted over
// exactly the namespace half of a using directive.
//
// C# writes the same dotted name two ways — `qualified_name` in a type or a using
// directive, `member_access_expression` in an expression — and they ask the same
// question, so both are flattened here. A generic application contributes its
// bare name: `List<string>` is a segment called `List`, because the members
// reached through it are `List`'s.
func (b *builder) pathSegments(n *sitter.Node) (segs []string, nodes []*sitter.Node) {
	if n == nil {
		return nil, nil
	}
	switch n.Type(b.lang) {
	case "qualified_name", "member_access_expression":
		scope := n.ChildByFieldName("qualifier", b.lang)
		if scope == nil {
			scope = n.ChildByFieldName("expression", b.lang)
		}
		name := n.ChildByFieldName("name", b.lang)
		if scope == nil || name == nil {
			return nil, nil
		}
		segs, nodes = b.pathSegments(scope)
		if segs == nil {
			return nil, nil
		}
		text := b.nameText(name)
		if text == "" {
			return nil, nil
		}
		return append(segs, text), append(nodes, n)
	case "identifier", "generic_name":
		if text := b.nameText(n); text != "" {
			return []string{text}, []*sitter.Node{n}
		}
	}
	return nil, nil
}

// nameText is the bare identifier a name node spells, with a generic
// application's type arguments removed.
func (b *builder) nameText(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	switch n.Type(b.lang) {
	case "identifier":
		return b.text(n)
	case "generic_name":
		return b.text(firstNamedChild(n))
	}
	return ""
}

// splitPath divides a dotted name into its namespace part and its type part.
//
// This is where C# parts company with Java, and it is the one place the two
// languages look alike and are not. Java splits on its naming convention:
// packages lowercase, types UpperCamelCase, near-universally observed because
// the JLS recommends it. C# has no such convention — the framework design
// guidelines put namespaces *and* types in PascalCase, so `System.Text` and
// `System.Text.StringBuilder` are the same shape all the way down and no rule
// over the spelling can separate them.
//
// So the split is on what the file has been told rather than on how a name is
// spelled: a prefix is the namespace when a `using` in this file named it, when
// this file declares it, or when it is one of the platform's reserved roots. The
// longest such prefix wins, because `using A.B;` and `using A.B.C;` may both be
// present and the deeper reading is the more specific one.
//
// Two fallbacks, and both are chosen so that being wrong matches nothing rather
// than something else. A path rooted at a platform namespace is read as
// "everything but the last segment", because a foreign coordinate can never
// match a definition this index owns, so the legible reading costs nothing and
// the alternative would render `System/Collections#Generic#List#`. And a path
// nothing has said anything about has no namespace part at all: the leading
// segment resolves as a simple type name and the rest nest inside it, which is
// what makes `Registry.Entry` name a nested type of this file's namespace.
func (b *builder) splitPath(segs []string) (pkg, types []string) {
	if len(segs) < 2 {
		return nil, segs
	}
	if foreignRoots[segs[0]] {
		return segs[:len(segs)-1], segs[len(segs)-1:]
	}
	for i := len(segs) - 1; i > 0; i-- {
		if b.namespaces[strings.Join(segs[:i], ".")] {
			return segs[:i], segs[i:]
		}
	}
	return nil, segs
}

// namespaceTarget renders a namespace path as a coordinate and namespace
// descriptor.
//
// The first segment decides the coordinate, and there is no third case beyond "a
// namespace of the platform" and "a namespace of whatever this repository is".
// C#'s namespaces carry no assembly identity at all — `Contoso.Util` says nothing
// about which package it ships in — so the rule is "the platform if the root says
// so, this coordinate otherwise", and a third-party dependency lands at a
// namespace of this coordinate that holds no definitions, which is an unresolved
// reference and not a wrong edge. This is Java's `jdkRoots` problem, Rust's
// `firstSegment` problem and Python's `import os` problem, one more time.
func (b *builder) namespaceTarget(pkg []string) (coord.Coord, string) {
	if len(pkg) == 0 {
		return b.coord, ""
	}
	if foreignRoots[pkg[0]] {
		return coord.Foreign(b.coord.Scheme, b.coord.Manager, pkg[0]), namespaceOf(pkg[1:])
	}
	return b.coord, namespaceOf(pkg)
}

// typePath names the type a dotted path reaches: the coordinate it lives under
// and the descriptor suffix down to and including it.
func (b *builder) typePath(n *sitter.Node, segs []string) (coord.Coord, string) {
	pkg, types := b.splitPath(segs)
	if len(types) == 0 {
		return b.namespaceTarget(pkg)
	}
	if len(pkg) == 0 {
		// Unqualified: the leading segment resolves the way a bare type name does
		// — through an alias, through this file's own declarations, through the
		// platform, and finally through this file's namespace — and what follows
		// it is nested inside it.
		c, s := b.resolveTypeName(n, types[0])
		for _, t := range types[1:] {
			s += t + "#"
		}
		return c, s
	}
	c, ns := b.namespaceTarget(pkg)
	for _, t := range types {
		ns += t + "#"
	}
	return c, ns
}

// ------------------------------------------------------------- definitions ---

func (b *builder) collectDefinitions(matches []sitter.QueryMatch) {
	type cand struct {
		kind string
		node *sitter.Node
		name *sitter.Node
	}
	var cands []cand
	for _, m := range matches {
		root, name, ok := roots(m, "definition.")
		if !ok || name == nil || root.Name == "definition.package" {
			continue
		}
		cands = append(cands, cand{kind: suffixAfter(root.Name, "definition."), node: root.Node, name: name.Node})
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, c := cands[i].name, cands[j].name
		if a.StartByte() != c.StartByte() {
			return a.StartByte() < c.StartByte()
		}
		return cands[i].kind < cands[j].kind
	})

	for _, cd := range cands {
		s := span{cd.name.StartByte(), cd.name.EndByte()}
		if b.claimed[s] {
			continue
		}

		name := b.text(cd.name)
		if name == "" || name == "_" {
			continue
		}
		kind := b.refineKind(cd.kind, cd.node)
		prefix, suffix := b.definitionDescriptor(kind, cd.node, name)
		b.claimed[s] = true

		desc := facts.Descriptor{Prefix: prefix, Suffix: suffix}
		occ := b.addOccurrence(desc, facts.RoleDefinition, kind, name, s.start, s.end)
		b.edge(facts.EdgeDefines, facts.FileRef(), facts.OccurrenceRef(occ))

		if _, dup := b.descIndex[desc.String()]; !dup {
			b.descIndex[desc.String()] = occ
		}
		b.defsByName[name] = append(b.defsByName[name], defRec{
			occ:      occ,
			scope:    b.occurrence(occ).Scope,
			start:    s.start,
			typeName: b.declaredType(cd.node),
		})
	}
}

// refineKind narrows a capture's kind where the CST carries a distinction the
// capture name does not. C# needs it in exactly two places, and both are places
// the grammar has one node for two meanings.
func (b *builder) refineKind(kind string, node *sitter.Node) string {
	switch {
	case kind == facts.KindParameter && b.isRecordComponent(node):
		// A record's positional component is written where a parameter is written
		// and is not one: `record Point(int X, int Y)` declares state and an
		// accessor for it, which is what the neutral core's `field` kind means.
		// A *class* with a primary constructor is deliberately not this: its
		// parameters are the constructor's and C# generates no member for them.
		return facts.KindField
	case kind == facts.KindField && b.hasToken(node, "const"):
		// C# has no distinct node for a constant — `const int X = 1;` is a field
		// declaration with a modifier — so the modifier is the only thing that
		// says so. The descriptor is the same either way; the kind is what a
		// reader and an overlay see.
		return facts.KindConstant
	}
	return kind
}

// isRecordComponent reports whether a formal parameter is a record's positional
// component rather than a callable's argument, which the CST says by where the
// parameter list hangs.
func (b *builder) isRecordComponent(node *sitter.Node) bool {
	if node.Type(b.lang) != "parameter" {
		return false
	}
	list := node.Parent()
	if list == nil || list.Type(b.lang) != "parameter_list" {
		return false
	}
	owner := list.Parent()
	return owner != nil && owner.Type(b.lang) == "record_declaration"
}

// definitionDescriptor builds the SCIP descriptor for a definition from its
// capture hierarchy (SPEC.md §5).
//
// The prefix is a return value for symmetry with the other stanzas, but C# has
// no counterpart to Rust's `impl MyTrait for Vec<T>`: a C# type's members are
// written inside the type — an extension method is a static method of a static
// class of its own and is named as one — so a definition is always this file's
// coordinate's.
func (b *builder) definitionDescriptor(kind string, node *sitter.Node, name string) (coord.Coord, string) {
	c, container := b.containerSuffix(node)
	switch kind {
	case facts.KindFunction, facts.KindMethod:
		return c, container + name + "()."
	case facts.KindType, facts.KindInterface:
		return c, container + name + "#"
	case facts.KindParameter:
		return c, container + "(" + name + ")"
	case facts.KindPackage:
		return c, container + name + "/"
	default: // field, variable, constant
		return c, container + name + "."
	}
}

// containerSuffix returns the coordinate and descriptor suffix of the nearest
// enclosing named container of n — a namespace block, a type, a callable or a
// property — or this file's file-scoped namespace when there is none. A block, a
// body, a lambda, an accessor and an anonymous type are transparent: none has a
// name, so none has a descriptor of its own and what is inside belongs to the
// enclosing container.
//
// This is where nested types *and* nested namespaces get their descriptors, and
// it is why neither needed a special case: each named container on the way out
// contributes exactly one component, so `A/B/`, `Outer#Inner#` and
// `Outer#Run().Local#` all fall out of the same walk that produces
// `Greeter#Greet().`.
func (b *builder) containerSuffix(n *sitter.Node) (coord.Coord, string) {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "namespace_declaration":
			if segs, _ := b.pathSegments(p.ChildByFieldName("name", b.lang)); len(segs) > 0 {
				c, s := b.containerSuffix(p)
				return c, s + namespaceOf(segs)
			}
		case "class_declaration", "struct_declaration", "record_declaration",
			"interface_declaration", "enum_declaration", "delegate_declaration":
			if name := b.fieldText(p, "name"); name != "" {
				c, s := b.containerSuffix(p)
				return c, s + name + "#"
			}
		case "method_declaration", "constructor_declaration", "destructor_declaration",
			"operator_declaration", "conversion_operator_declaration", "local_function_statement":
			if name := b.fieldText(p, "name"); name != "" {
				c, s := b.containerSuffix(p)
				return c, s + name + "()."
			}
		case "property_declaration":
			if name := b.fieldText(p, "name"); name != "" {
				c, s := b.containerSuffix(p)
				return c, s + name + "."
			}
		}
	}
	return b.coord, b.ns
}

// enclosingType returns the type declaration n sits innermost inside, or nil at
// the file's top level. It is what resolves `this`.
func (b *builder) enclosingType(n *sitter.Node) *sitter.Node {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "class_declaration", "struct_declaration", "record_declaration",
			"interface_declaration", "enum_declaration":
			return p
		}
	}
	return nil
}

// thisTarget is the coordinate and suffix `this` names: the type the expression
// is written inside.
func (b *builder) thisTarget(n *sitter.Node) (pathTarget, bool) {
	decl := b.enclosingType(n)
	if decl == nil {
		return pathTarget{}, false
	}
	name := b.fieldText(decl, "name")
	if name == "" {
		return pathTarget{}, false
	}
	c, s := b.containerSuffix(decl)
	return pathTarget{coord: c, suffix: s + name + "#"}, true
}

// baseTarget is what `base` names: the first entry of the enclosing type's base
// list. C# writes the base class and the interfaces in one list and marks
// neither, so the first entry is taken — which is the language's own rule, since
// a base class must come first when there is one. A type with no base list
// derives from `System.Object`, which is the platform's and is named as such
// rather than left unknown.
func (b *builder) baseTarget(n *sitter.Node) (pathTarget, bool) {
	decl := b.enclosingType(n)
	if decl == nil {
		return pathTarget{}, false
	}
	for i := 0; decl != nil && i < decl.NamedChildCount(); i++ {
		child := decl.NamedChild(i)
		if child.Type(b.lang) != "base_list" {
			continue
		}
		if first := firstNamedChild(child); first != nil {
			if segs := b.typeSegments(first); len(segs) > 0 {
				c, s := b.typePath(first, segs)
				return pathTarget{coord: c, suffix: s}, true
			}
		}
	}
	return pathTarget{coord: b.builtin(), suffix: "Object#"}, true
}

// -------------------------------------------------------------- references ---

type refRec struct {
	role     string
	node     *sitter.Node
	nameNode *sitter.Node
}

// refPriority ranks the roles that can capture the same identifier. A call is
// more specific than a read of the same member, and a type reference is more
// specific than a read.
func refPriority(role string) int {
	switch role {
	case "call":
		return 3
	case "type":
		return 2
	default:
		return 1
	}
}

// moreSpecific decides which of two references over the same identifier
// survives. Role decides it where the roles differ; where they do not, the wider
// structural node wins, because the wider node is the one that saw the qualifier
// and therefore knows strictly more about the identifier than the narrower match
// does.
func (b *builder) moreSpecific(cand, prev refRec) bool {
	if p, c := refPriority(prev.role), refPriority(cand.role); p != c {
		return c > p
	}
	pw := prev.node.EndByte() - prev.node.StartByte()
	cw := cand.node.EndByte() - cand.node.StartByte()
	if pw != cw {
		return cw > pw
	}
	return cand.role < prev.role
}

func (b *builder) collectReferences(matches []sitter.QueryMatch) {
	best := map[span]refRec{}
	for _, m := range matches {
		root, name, ok := roots(m, "reference.")
		if !ok {
			continue
		}
		role := suffixAfter(root.Name, "reference.")
		node, nameNode := root.Node, root.Node
		if name != nil {
			nameNode = name.Node
		}
		if role == "type" {
			// The type patterns capture the type expression whole, because C# has
			// no node kind that means "a type name". The descriptor is built from
			// the whole expression and the occurrence covers the identifier inside
			// it; `int`, `var` and a tuple reduce to nothing and are dropped.
			if name == nil {
				continue
			}
			node = name.Node
			nameNode = b.typeNameNode(node)
			if nameNode == nil {
				continue
			}
		}
		s := span{nameNode.StartByte(), nameNode.EndByte()}
		if b.claimed[s] {
			continue // a definition or a using directive already owns this identifier
		}
		cand := refRec{role: role, node: node, nameNode: nameNode}
		if prev, dup := best[s]; dup && !b.moreSpecific(cand, prev) {
			continue
		}
		best[s] = cand
	}

	spans := make([]span, 0, len(best))
	for s := range best {
		spans = append(spans, s)
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end < spans[j].end
	})

	for _, s := range spans {
		r := best[s]
		name := b.text(r.nameNode)
		if name == "" || name == "_" {
			continue
		}
		desc, kind := b.referenceDescriptor(r, name)
		occ := b.addOccurrence(desc, facts.RoleReference, kind, name, s.start, s.end)

		// Same-file resolution (§4.3): the target definition is in this CST, so
		// the edge is extracted rather than left to the link pass. The match is
		// on the descriptor string, exactly as link's cross-file join is.
		if def, ok := b.descIndex[desc.String()]; ok && def != occ {
			b.edge(facts.EdgeReferencesLocal, facts.OccurrenceRef(occ), facts.OccurrenceRef(def))
		}
	}
}

func (b *builder) referenceDescriptor(r refRec, name string) (facts.Descriptor, string) {
	switch r.role {
	case "call":
		return b.callDescriptor(r, name)
	case "type":
		return b.typeReferenceDescriptor(r, name)
	default:
		return b.readDescriptor(r, name)
	}
}

// callDescriptor names the target of an invocation.
//
// Nearly every C# callable is a member of a type, so the whole question is which
// type — and the answer is whatever the receiver resolves to. A bare `F()` is a
// local function of an enclosing callable or a member of the enclosing type;
// `g.F()` is a member of whatever `g` was declared as, read off the declaration
// and never inferred; and `T.F()` is exact, because a type name is resolvable
// syntactically. Where the receiver is unknowable file-locally the type component
// is SCIP's ".", so the descriptor cannot false-match a real definition.
func (b *builder) callDescriptor(r refRec, name string) (facts.Descriptor, string) {
	fn := r.node.ChildByFieldName("function", b.lang)
	receiver := (*sitter.Node)(nil)
	if fn != nil && fn.Type(b.lang) == "member_access_expression" {
		receiver = fn.ChildByFieldName("expression", b.lang)
	}

	if receiver == nil {
		// `F()` — a local function of some enclosing callable, or a member of the
		// enclosing type. The first is checked against what this file actually
		// declared, which is what keeps a local function's `().` component on it.
		if c, s, ok := b.localDeclared(r.nameNode, name, "()."); ok {
			return facts.Descriptor{Prefix: c, Suffix: s}, facts.KindMethod
		}
		if t, ok := b.thisTarget(r.nameNode); ok {
			return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "()."}, facts.KindMethod
		}
		return facts.Descriptor{Prefix: b.coord, Suffix: b.ns + name + "()."}, facts.KindMethod
	}
	if t, ok := b.valueTarget(receiver, r.nameNode.StartByte()); ok {
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "()."}, facts.KindMethod
	}
	return facts.Descriptor{Prefix: b.coord, Suffix: b.ns + coord.Unknown + "#" + name + "()."}, facts.KindMethod
}

func (b *builder) readDescriptor(r refRec, name string) (facts.Descriptor, string) {
	if b.isQualifier(r) {
		// The left half of `a.b`, which names a value, a type or a namespace.
		return b.qualifierDescriptor(r, name)
	}

	// The right half of `a.b`: a member of whatever the left half is.
	object := r.node.ChildByFieldName("expression", b.lang)
	if object == nil {
		object = r.node.ChildByFieldName("qualifier", b.lang)
	}
	// A namespace is asked about before a value, and the order matters: the left
	// half of `System.Collections.Generic.List` is itself a path that splitPath
	// would happily read as a namespace and a type, so asking "is this a value?"
	// first would make `Generic` a field of a type called `Collections`.
	if t, segs, ok := b.namespaceOfNode(object); ok {
		// `A.B.C` read as a path: the segment after a namespace is a deeper
		// namespace when this file has been told so, and — for a platform-rooted
		// path, where nothing has been told — when it is far enough from the end
		// of the whole name to be one. That is splitPath's "everything but the
		// last segment" stated a segment at a time, with the correction an
		// invocation forces: `System.Console.WriteLine(x)` ends in a *method*, so
		// `Console` is the type and only `System` is the namespace, while
		// `System.Collections.Generic.List<T>` ends in a type and everything
		// before it is namespace.
		trailing, call := b.pathTail(r.node)
		if b.namespaces[strings.Join(append(segs, name), ".")] ||
			(foreignRoots[segs[0]] && (trailing >= 2 || (trailing == 1 && !call))) {
			return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "/"}, facts.KindPackage
		}
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "#"}, facts.KindType
	}
	if t, ok := b.valueTarget(object, r.nameNode.StartByte()); ok {
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "."}, facts.KindField
	}
	return facts.Descriptor{Prefix: b.coord, Suffix: b.ns + coord.Unknown + "#" + name + "."}, facts.KindField
}

// qualifierDescriptor names the left half of a `.`, which is the one place C#'s
// single member operator has to be disambiguated rather than read.
func (b *builder) qualifierDescriptor(r refRec, name string) (facts.Descriptor, string) {
	// A binding in scope wins: a local named `Text` shadows a namespace named
	// `Text`, which is also what the compiler does.
	if def, ok := b.lookup(name, r.nameNode.StartByte()); ok {
		return b.occurrence(def.occ).Descriptor, facts.KindVariable
	}
	if t, ok := b.types[name]; ok {
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix}, facts.KindType
	}
	if b.namespaces[name] || foreignRoots[name] {
		c, ns := b.namespaceTarget([]string{name})
		return facts.Descriptor{Prefix: c, Suffix: ns}, facts.KindPackage
	}
	c, s := b.resolveTypeName(r.nameNode, name)
	return facts.Descriptor{Prefix: c, Suffix: s}, facts.KindType
}

// typeReferenceDescriptor names a type written in a type position. r.node is the
// type expression whole, so a qualified name resolves the path rather than the
// bare name it ends in.
func (b *builder) typeReferenceDescriptor(r refRec, name string) (facts.Descriptor, string) {
	segs := b.typeSegments(r.node)
	if len(segs) > 1 {
		c, s := b.typePath(r.node, segs)
		kind := facts.KindType
		if strings.HasSuffix(s, "/") {
			kind = facts.KindPackage
		}
		return facts.Descriptor{Prefix: c, Suffix: s}, kind
	}
	c, s := b.resolveTypeName(r.nameNode, name)
	return facts.Descriptor{Prefix: c, Suffix: s}, facts.KindType
}

// resolveTypeName names a type by the coordinate and descriptor suffix it lives
// at. The name may be dotted, when it came from a declaration this stanza read
// the text of rather than the CST.
//
// The order is the compiler's own, minus the assembly references: an alias wins,
// then a type declared in this file (which is what makes a nested type resolvable
// by its simple name), then the platform, then this file's own namespace. The
// last is C#'s implicit same-namespace visibility and is why two files in one
// namespace need no `using` at all.
func (b *builder) resolveTypeName(n *sitter.Node, typeName string) (coord.Coord, string) {
	if strings.Contains(typeName, ".") {
		return b.typePath(n, dotted(typeName))
	}
	if t, ok := b.types[typeName]; ok {
		return t.coord, t.suffix
	}
	if c, s, ok := b.localDeclared(n, typeName, "#"); ok {
		return c, s
	}
	if systemTypes[typeName] {
		return b.builtin(), typeName + "#"
	}
	if len(b.projectUsings) == 1 {
		// The name is not this file's and not the platform's, so it came through
		// a `using` — and with exactly one of them naming a namespace of this
		// artifact, there is exactly one namespace it can have come from.
		//
		// This inverts C#'s own order, which searches the enclosing namespaces
		// before the usings, and it does so knowingly. The compiler can ask
		// whether the enclosing namespace holds the name; a file-local reader
		// cannot, because the answer is in another file (§2.5). The `using` is
		// the only *written* evidence in this file that a name comes from
		// somewhere else, and preferring it is what makes a cross-namespace type
		// reference resolve at all.
		//
		// What it costs is the mirror case: a file that both imports one
		// namespace and uses a type of its own from another file resolves that
		// type into the imported namespace, where it matches nothing. That is
		// the trade every stanza here makes — a descriptor that matches nothing
		// rather than one that matches something else — and it is why the rule
		// is "exactly one": two project usings are two candidates with no
		// evidence between them, so neither is taken.
		return b.coord, b.projectUsings[0] + typeName + "#"
	}
	return b.coord, b.namespaceSuffix(n) + typeName + "#"
}

// namespaceSuffix is the namespace descriptor a name written at n falls back to:
// the enclosing `namespace … { }` blocks, or the file-scoped declaration when
// there are none.
//
// It is containerSuffix with the types and callables left out, and the two are
// deliberately different walks. A type name written inside `class Greeter` does
// not name a member of `Greeter` — C# resolves an unqualified type against the
// enclosing *namespaces*, never against the enclosing type's descriptor path —
// so the ancestor walk that builds a definition's container is the wrong one to
// resolve a reference with.
func (b *builder) namespaceSuffix(n *sitter.Node) string {
	for p := n; p != nil; p = p.Parent() {
		if p.Type(b.lang) != "namespace_declaration" {
			continue
		}
		if segs, _ := b.pathSegments(p.ChildByFieldName("name", b.lang)); len(segs) > 0 {
			return b.namespaceSuffix(p.Parent()) + namespaceOf(segs)
		}
	}
	return b.ns
}

// localDeclared resolves a simple name against the definitions this file
// declares, innermost container first. It is what makes `Inner i;` inside
// `Outer` name `Outer#Inner#` rather than the namespace-level `Inner#` that does
// not exist, and what keeps a local function's call on the enclosing method's
// `().` component.
//
// Only names this file already defined, which is why it consults descIndex:
// collectDefinitions runs first, so every type and callable declared here is in
// it, and a name that is not is not this file's to claim.
func (b *builder) localDeclared(n *sitter.Node, name, terminator string) (coord.Coord, string, bool) {
	try := func(c coord.Coord, prefix string) (coord.Coord, string, bool) {
		candidate := facts.Descriptor{Prefix: c, Suffix: prefix + name + terminator}
		if _, ok := b.descIndex[candidate.String()]; ok {
			return c, prefix + name + terminator, true
		}
		return coord.Coord{}, "", false
	}
	for p := n; p != nil; p = p.Parent() {
		var component string
		switch p.Type(b.lang) {
		case "namespace_declaration":
			component = "/"
		case "class_declaration", "struct_declaration", "record_declaration",
			"interface_declaration", "enum_declaration":
			component = "#"
		case "method_declaration", "constructor_declaration", "local_function_statement":
			component = "()."
		default:
			continue
		}
		c, s := b.containerSuffix(p)
		if component == "/" {
			if segs, _ := b.pathSegments(p.ChildByFieldName("name", b.lang)); len(segs) > 0 {
				s += namespaceOf(segs)
			}
		} else if own := b.fieldText(p, "name"); own != "" {
			s += own + component
		}
		if c, s, ok := try(c, s); ok {
			return c, s, true
		}
	}
	return try(b.coord, b.ns)
}

// ------------------------------------------------------------ local lookup ---

// valueTarget resolves the left half of a `.` to the type its members hang off.
// It is the one place a *type* has to be recovered rather than read off the
// syntax, and it is recovered from a declaration and never inferred.
func (b *builder) valueTarget(value *sitter.Node, pos uint32) (pathTarget, bool) {
	if value == nil {
		return pathTarget{}, false
	}
	switch value.Type(b.lang) {
	case "this":
		return b.thisTarget(value)
	case "base":
		return b.baseTarget(value)
	case "parenthesized_expression":
		return b.valueTarget(firstNamedChild(value), pos)
	case "object_creation_expression":
		// `new Greeter("x").Greet()` — the receiver's type is written right
		// there, which makes it the one expression form worth reading back.
		if segs := b.typeSegments(value.ChildByFieldName("type", b.lang)); len(segs) > 0 {
			c, s := b.typePath(value, segs)
			return pathTarget{coord: c, suffix: s}, true
		}
	case "identifier":
		name := b.text(value)
		if def, bound := b.lookup(name, pos); bound {
			// A binding shadows everything. Its declared type is what its members
			// hang off — and when the declaration said `var`, there is no
			// syntactic type at all and the honest answer is "unknown".
			if def.typeName == "" {
				return pathTarget{}, false
			}
			c, s := b.resolveTypeName(value, def.typeName)
			return pathTarget{coord: c, suffix: s}, true
		}
		if t, ok := b.types[name]; ok {
			return t, true
		}
		if b.namespaces[name] || foreignRoots[name] {
			return pathTarget{}, false // a namespace is not a value
		}
		// Not a binding and not a namespace, so a type: a static member access.
		c, s := b.resolveTypeName(value, name)
		return pathTarget{coord: c, suffix: s}, true
	case "member_access_expression", "qualified_name":
		segs, _ := b.pathSegments(value)
		if len(segs) == 0 {
			return pathTarget{}, false
		}
		// A binding shadows a namespace, so a chain starting at a local is a
		// member access and not a qualified name.
		if _, ok := b.lookup(segs[0], pos); ok {
			return pathTarget{}, false
		}
		if _, types := b.splitPath(segs); len(types) == 0 {
			// The whole path is a namespace, and a namespace is not a value.
			return pathTarget{}, false
		}
		c, s := b.typePath(value, segs)
		return pathTarget{coord: c, suffix: s}, true
	}
	return pathTarget{}, false
}

// namespaceOfNode resolves a node to the namespace it names, for the segments of
// a qualified name that are still below the type.
//
// It is only ever asked about the *qualifier* half of a path, which is why a
// platform root is enough on its own: splitPath reads a foreign-rooted path as
// "everything but the last segment is the namespace", so every proper prefix of
// one is a namespace by that same reading.
func (b *builder) namespaceOfNode(n *sitter.Node) (pathTarget, []string, bool) {
	if n == nil {
		return pathTarget{}, nil, false
	}
	segs, _ := b.pathSegments(n)
	if len(segs) == 0 {
		return pathTarget{}, nil, false
	}
	if !b.namespaces[strings.Join(segs, ".")] && !foreignRoots[segs[0]] {
		return pathTarget{}, nil, false
	}
	c, ns := b.namespaceTarget(segs)
	return pathTarget{coord: c, suffix: ns}, segs, true
}

// isQualifier reports whether the captured identifier is the left half of a `.`
// rather than the member it reaches.
func (b *builder) isQualifier(r refRec) bool {
	for _, field := range []string{"expression", "qualifier"} {
		if q := r.node.ChildByFieldName(field, b.lang); q != nil && q.StartByte() == r.nameNode.StartByte() {
			return true
		}
	}
	return false
}

// lookup finds the definition named name that is visible at byte offset pos:
// among definitions whose declaring scope contains pos, the one in the innermost
// such scope, declared no later than pos.
//
// The "no later than pos" rule is a local's, and C# fields break it — a method
// may use a field declared below it, which is legal and common. So a definition
// whose scope is a *type* is exempt: its whole scope is its lifetime.
func (b *builder) lookup(name string, pos uint32) (defRec, bool) {
	var best defRec
	var bestStart uint32
	bestEnd := ^uint32(0)
	found := false
	for _, d := range b.defsByName[name] {
		start, end, ok := b.scopeRange(d.scope)
		if !ok || pos < start || pos >= end {
			continue
		}
		if d.start > pos && !b.isTypeScope(d.scope) {
			continue
		}
		if !found || start > bestStart || (start == bestStart && end < bestEnd) {
			best, bestStart, bestEnd, found = d, start, end, true
		}
	}
	return best, found
}

func (b *builder) isTypeScope(id facts.LocalID) bool {
	for _, s := range b.out.Scopes {
		if s.ID == id {
			return s.Kind == facts.ScopeType
		}
	}
	return false
}

// declaredType recovers the syntactic type of a binding, parameter, field or
// property — enough to name the members reached through it, without any type
// inference. "" means unknown, which downstream becomes SCIP's "." rather than a
// guess.
//
// C# writes the type down at every declaration site, which makes this stanza's
// job as easy as Java's and easier than Rust's or Python's: there is no
// initialiser to read back and no convention to lean on. The one exception is
// `var`, and the grammar is kinder about it than Java's is about the same
// keyword — `implicit_type` is its own node, so an inferred local yields "" with
// no name check at all, and its members land on ".".
func (b *builder) declaredType(node *sitter.Node) string {
	switch node.Type(b.lang) {
	case "local_declaration_statement", "field_declaration", "event_field_declaration",
		"using_statement":
		for i := 0; i < node.NamedChildCount(); i++ {
			if c := node.NamedChild(i); c.Type(b.lang) == "variable_declaration" {
				return b.unwrapType(c.ChildByFieldName("type", b.lang))
			}
		}
	case "parameter", "property_declaration", "catch_declaration", "foreach_statement",
		"local_function_statement", "indexer_declaration":
		return b.unwrapType(node.ChildByFieldName("type", b.lang))
	}
	return ""
}

// unwrapType reduces a type expression to the bare (possibly dotted) name a
// descriptor can use.
func (b *builder) unwrapType(t *sitter.Node) string {
	return strings.Join(b.typeSegments(t), ".")
}

// typeSegments reduces a type expression to the dotted segments that name it.
// An array, a nullable and a generic application are transparent: `List<string>`
// is named by `List`, because the members reached through it are `List`'s. A
// predefined type, a tuple and `var` name nothing a descriptor could reach.
func (b *builder) typeSegments(t *sitter.Node) []string {
	for depth := 0; t != nil && depth < 16; depth++ {
		switch t.Type(b.lang) {
		case "array_type", "nullable_type", "pointer_type", "ref_type":
			t = t.ChildByFieldName("type", b.lang)
		case "qualified_name", "identifier", "generic_name":
			segs, _ := b.pathSegments(t)
			return segs
		default:
			return nil
		}
	}
	return nil
}

// typeNameNode reduces a type expression to the identifier the occurrence covers
// — the type's own name, without its qualifier and without its type arguments.
// nil when the expression names no such thing, which is what drops `int`,
// `string`, `var` and a tuple type before an occurrence is ever emitted.
func (b *builder) typeNameNode(t *sitter.Node) *sitter.Node {
	for depth := 0; t != nil && depth < 16; depth++ {
		switch t.Type(b.lang) {
		case "array_type", "nullable_type", "pointer_type", "ref_type":
			t = t.ChildByFieldName("type", b.lang)
		case "qualified_name":
			t = t.ChildByFieldName("name", b.lang)
		case "generic_name":
			t = firstNamedChild(t)
		case "identifier":
			return t
		default:
			return nil
		}
	}
	return nil
}

// ------------------------------------------------------------------ emitting --

func (b *builder) addOccurrence(d facts.Descriptor, role facts.Role, kind, name string, start, end uint32) facts.LocalID {
	return b.addOccurrenceIn(b.enclosingScope(start, end), d, role, kind, name, start, end)
}

func (b *builder) addOccurrenceIn(scope facts.LocalID, d facts.Descriptor, role facts.Role, kind, name string, start, end uint32) facts.LocalID {
	b.nextOcc++
	b.out.Occurrences = append(b.out.Occurrences, facts.Occurrence{
		ID:         b.nextOcc,
		Descriptor: d,
		Role:       role,
		SymbolKind: kind,
		Name:       name,
		RangeStart: int(start),
		RangeEnd:   int(end),
		Scope:      scope,
	})
	if scope != facts.NoID {
		b.edge(facts.EdgeContains, facts.ScopeRef(scope), facts.OccurrenceRef(b.nextOcc))
	}
	return b.nextOcc
}

// occurrence looks up an occurrence by its LocalID. addOccurrence is the only
// producer of ids and numbers them densely from 1, so the id is the index.
func (b *builder) occurrence(id facts.LocalID) facts.Occurrence {
	return b.out.Occurrences[id-1]
}

func (b *builder) edge(kind facts.EdgeKind, source, target facts.Ref) {
	b.out.Edges = append(b.out.Edges, facts.Edge{Kind: kind, Source: source, Target: target})
}

// claimSubtree marks every identifier under n as owned, so the `.` reference
// patterns — which match inside a using directive or a namespace declaration as
// readily as anywhere else — do not emit a second occurrence over bytes already
// described.
func (b *builder) claimSubtree(n *sitter.Node) {
	if n == nil {
		return
	}
	if n.Type(b.lang) == "identifier" {
		b.claimed[span{n.StartByte(), n.EndByte()}] = true
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		b.claimSubtree(n.NamedChild(i))
	}
}

// builtin is the coordinate of the .NET platform, which belongs to no artifact
// this index owns. `System` is the root namespace the platform is reached
// through.
func (b *builder) builtin() coord.Coord {
	return coord.Foreign(b.coord.Scheme, b.coord.Manager, "System")
}

// ------------------------------------------------------------------ helpers ---

func (b *builder) text(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return n.Text(b.src)
}

func (b *builder) fieldText(n *sitter.Node, field string) string {
	if n == nil {
		return ""
	}
	return b.text(n.ChildByFieldName(field, b.lang))
}

// roots splits a match into its structural capture (the one whose name starts
// with prefix) and its @name capture, if any.
func roots(m sitter.QueryMatch, prefix string) (root, name *sitter.QueryCapture, ok bool) {
	for i := range m.Captures {
		c := &m.Captures[i]
		switch {
		case c.Name == "name":
			name = c
		case strings.HasPrefix(c.Name, prefix):
			root = c
		}
	}
	return root, name, root != nil
}

func suffixAfter(capture, prefix string) string {
	return strings.TrimPrefix(capture, prefix)
}

func firstNamedChild(n *sitter.Node) *sitter.Node {
	if n == nil || n.NamedChildCount() == 0 {
		return nil
	}
	return n.NamedChild(0)
}

func lastNamedChild(n *sitter.Node) *sitter.Node {
	if n == nil || n.NamedChildCount() == 0 {
		return nil
	}
	return n.NamedChild(n.NamedChildCount() - 1)
}

func sameSpan(a, b *sitter.Node) bool {
	return a != nil && b != nil && a.StartByte() == b.StartByte() && a.EndByte() == b.EndByte()
}

// hasToken reports whether n has a direct child spelling word — either an
// anonymous keyword token, which is how `using static` writes `static`, or a
// `(modifier)` node, which is how a declaration writes `const`. The grammar
// spells the two differently and the question is the same, so both are asked.
func (b *builder) hasToken(n *sitter.Node, word string) bool {
	if n == nil {
		return false
	}
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c.Type(b.lang) == word {
			return true
		}
		if c.Type(b.lang) == "modifier" && b.text(c) == word {
			return true
		}
	}
	return false
}

// pathTail measures where a path node sits in the dotted name it belongs to:
// how many segments follow it, and whether the whole name is what an invocation
// calls.
//
// It is what tells `System.Console` in `System.Console.WriteLine(x)` from
// `System.Collections` in `System.Collections.Generic.List<T>`. Both are the
// qualifier of a longer platform-rooted path, and one names a type while the
// other names a namespace; the only thing that separates them is how many
// segments come after, and whether the last of them is a call.
func (b *builder) pathTail(n *sitter.Node) (trailing int, call bool) {
	for p := n; ; {
		parent := p.Parent()
		if parent == nil {
			return trailing, false
		}
		switch parent.Type(b.lang) {
		case "qualified_name", "member_access_expression":
			q := parent.ChildByFieldName("qualifier", b.lang)
			if q == nil {
				q = parent.ChildByFieldName("expression", b.lang)
			}
			if !sameSpan(q, p) {
				return trailing, false
			}
			trailing++
			p = parent
		case "invocation_expression":
			return trailing, sameSpan(parent.ChildByFieldName("function", b.lang), p)
		default:
			return trailing, false
		}
	}
}

// dotted splits a C# qualified name into its segments.
func dotted(name string) []string {
	if name == "" {
		return nil
	}
	return strings.Split(name, ".")
}

// namespaceOf renders namespace segments as a SCIP namespace descriptor:
// slash-separated and slash-terminated. The empty namespace renders "", which is
// C#'s global namespace and is a real namespace rather than a missing one.
func namespaceOf(segs []string) string {
	if len(segs) == 0 {
		return ""
	}
	return strings.Join(segs, "/") + "/"
}

// packageName is what a namespace occurrence is called in the `name` column: the
// last segment of its namespace, or — for the global namespace, which has no
// namespace at all — the last segment of the artifact's name.
func packageName(c coord.Coord, ns string) string {
	if trimmed := strings.TrimSuffix(ns, "/"); trimmed != "" {
		return lastSegment(trimmed)
	}
	name := c.Name
	if name == "" || name == coord.Unknown {
		return ""
	}
	return lastSegment(name)
}

func lastSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// foreignRoots are the top-level namespace names that are never this artifact's.
//
// It is what namespaceTarget needs and what C#'s syntax cannot supply: a
// namespace name carries no assembly identity, so `Contoso.Util` is this
// repository's or a dependency's with nothing in the source to say which. These
// are the two roots the platform reserves — a type under either ships with .NET
// and is never indexed here — and a third-party dependency missing from the set
// is treated as this artifact's, which yields a namespace with no definitions in
// it: an unresolved reference rather than a wrong edge.
var foreignRoots = map[string]bool{
	"System": true, "Microsoft": true,
}

// systemTypes is the commonly used subset of the types `System` puts within
// reach of a file that writes `using System;` — which is every file compiled
// with implicit usings on, and so effectively every file. References to them
// carry a foreign coordinate and so never pollute descriptor matching within the
// indexed artifact. An omission costs one reference landing in this artifact's
// namespace with nothing to match, which is what an unrecognised name gets
// anyway.
var systemTypes = map[string]bool{
	"Object": true, "String": true, "Boolean": true, "Char": true, "Byte": true,
	"SByte": true, "Int16": true, "Int32": true, "Int64": true, "UInt16": true,
	"UInt32": true, "UInt64": true, "Single": true, "Double": true, "Decimal": true,
	"Math": true, "Console": true, "Type": true, "Enum": true, "Array": true,
	"Guid": true, "DateTime": true, "TimeSpan": true, "Uri": true, "Version": true,
	"Nullable": true, "Tuple": true, "ValueTuple": true, "Span": true, "Memory": true,
	"IDisposable": true, "IAsyncDisposable": true, "IComparable": true,
	"IEquatable": true, "IFormattable": true, "ICloneable": true,
	"Action": true, "Func": true, "Predicate": true, "Comparison": true,
	"EventHandler": true, "EventArgs": true, "Lazy": true, "Random": true,
	"Exception": true, "SystemException": true, "ApplicationException": true,
	"ArgumentException": true, "ArgumentNullException": true,
	"ArgumentOutOfRangeException": true, "InvalidOperationException": true,
	"NotImplementedException": true, "NotSupportedException": true,
	"NullReferenceException": true, "IndexOutOfRangeException": true,
	"InvalidCastException": true, "FormatException": true, "OverflowException": true,
	"ObjectDisposedException": true, "AggregateException": true,
	"Attribute": true, "AttributeUsageAttribute": true, "ObsoleteAttribute": true,
	"FlagsAttribute": true, "SerializableAttribute": true,
}
