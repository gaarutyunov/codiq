// Package java is the Java stanza: the tree-sitter query in query.scm plus the
// mapper here that turns its captures into core facts (SPEC.md §5).
//
// The package is named for the extension, per SPEC.md §12. It imports facts and
// coord and deliberately not extract: it satisfies extract.Parser structurally,
// which is what keeps the registry free of an import cycle.
//
// The mapper's job, and its limits, are the Go, TypeScript, Python and Rust
// stanzas' (§4.3): it builds the descriptor *suffix* from the CST and the
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
// `__init__.py` collapsing to its directory, Rust's the `mod` tree. Java's is
// the one the language writes down: `package com.example.greeter;` at the top of
// the file, and the namespace is that name with its dots turned into slashes.
//
// The declaration is preferred over the path, always, and the two do disagree in
// practice. Maven puts sources under `src/main/java/`, so the path would yield
// `src/main/java/com/example/greeter/` while every `import` in the repository
// writes `com.example.greeter` — and the two sides of an import have to derive
// the same namespace or no `imports` edge exists at all. It is also the more
// file-local of the two readings (§2.5): the path is a fact about where the file
// sits, the declaration is a fact written *in* the file, and the compiler agrees
// with the second. Nothing here probes the filesystem, and nothing here needs to.
//
// What it cannot represent:
//
//   - Two source roots declaring the same package. `src/main/java/…/Greeter.java`
//     and `src/test/java/…/Greeter.java` both declare `com.example.greeter`, so
//     both derive one namespace and a test class collides with the main class it
//     tests. The path would separate them and the declaration cannot, which is
//     the cost of preferring the declaration; the compiler has the same view and
//     resolves it with a classpath, which is not a file-local fact.
//   - A multi-module Maven build. `coord.Resolve` reads the manifests of one
//     directory — the repository root — so every module's files carry the root
//     POM's coordinate rather than their own. That is coord's boundary and not
//     this stanza's, and it is Rust's Cargo-workspace note verbatim.
//   - The default package. A file with no `package` clause has namespace "", so
//     its top-level types render `Greeter#` — correct, and colliding with every
//     other default-package `Greeter` in the repository, exactly as javac would.
//
// # Nested and inner classes
//
// No earlier language in this graph has them, and the descriptor answer is the
// one SCIP already implies: a type inside a type is another `#` level, so
// `class Outer { class Inner { void ping(); } }` in `com.example` renders
// `com/example/Outer#Inner#ping().`. The rule is not special-cased — it falls out
// of containerSuffix walking ancestors and appending one component per named
// container — which is why a type declared inside a *method* works too:
// `Outer#run().Local#` names a local class, since the method contributes its own
// `().` component on the way past.
//
// Static, inner, local and nested-in-an-interface are all the same shape here.
// Java draws a distinction between them (an inner class captures an enclosing
// instance; a static nested class does not) and the descriptor does not, because
// the distinction is about lifetime rather than about naming, and nothing in the
// core model asks.
//
// An *anonymous* class is the one shape with no answer: `new Runnable() { … }`
// declares a type the source never names, so there is no identifier to build a
// component from and its members land under the enclosing container instead.
// Naming it positionally — SCIP writes `$anon1`, javac writes `Outer$1` — would
// be a descriptor no reference could ever reconstruct, since a caller cannot
// count the anonymous classes in another file.
//
// # What one dot hides
//
// One limit is Java's alone. Rust has two member operators and they ask
// different questions; Java has one, and `a.b` is a field of a value, a member of
// a type, or a segment of a package name with nothing in the syntax to say which.
// The mapper decides by what the left half resolves to — a binding in scope, an
// imported name, a `java.*` root, or a name that is none of those — and where the
// left half is itself unresolved the descriptor writes "." for the type. The
// split between the package part and the type part of a dotted name is Java's
// naming convention and not its grammar: lowercase segments are the package, the
// first uppercase one begins the type. That is the trade the Rust stanza makes
// for `use a::b;` versus `use a::B;`, and the cost of being wrong is the same —
// a descriptor that matches nothing rather than one that matches the wrong thing.
//
// # What an overload hides
//
// The other limit is worth stating plainly, because it is the one place Java asks
// something of the descriptor that no earlier language here does. Java overloads
// on the parameter list: `greet(String)` and `greet(int)` are two methods, and
// both render `greet().`, because the descriptor's callable component carries the
// name and not the signature. So the two definitions collide, and a reference to
// either resolves to both.
//
// Encoding the signature is what SCIP's disambiguating suffix exists for, and it
// is deliberately not done here: the *definition* side could number its overloads
// from one file's CST, but the *reference* side could not — `g.greet(x)` in
// another file would have to know which overload the argument's type selects,
// which is type checking, which is the §4.2 overlay and not a file-local
// extractor. A descriptor only one side can compute is worse than one both sides
// compute the same way, even when the shared answer is coarse.
package java

import (
	_ "embed"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	sitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/facts"
)

// Lang is the value written to file.lang for the files this stanza handles.
const Lang = "java"

// Ext is the file extension this stanza is registered under.
const Ext = ".java"

//go:embed query.scm
var queryScheme string

// Parser is the Java stanza. Safe for concurrent use: the grammar and compiled
// query are immutable after the first Parse, and each parse checks a
// gotreesitter parser out of a pool.
type Parser struct {
	once    sync.Once
	lang    *sitter.Language
	pool    *sitter.ParserPool
	query   *sitter.Query
	initErr error
}

// New returns the Java parser. It is cheap: the grammar is loaded and query.scm
// compiled on the first Parse, so a binary that never parses Java never
// decompresses the Java grammar — and a query that fails to compile lands in
// ParseError rather than panicking at init.
func New() *Parser { return &Parser{} }

func (p *Parser) init() {
	p.once.Do(func() {
		p.lang = grammars.JavaLanguage()
		if p.lang == nil {
			p.initErr = errors.New("java: gotreesitter has no Java grammar")
			return
		}
		q, err := sitter.NewQuery(queryScheme, p.lang)
		if err != nil {
			p.initErr = fmt.Errorf("java: compile query.scm: %w", err)
			return
		}
		p.query = q
		p.pool = sitter.NewParserPool(p.lang)
	})
}

// Parse extracts one Java file's facts. It never returns an error: a failure is
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
		types:      map[string]pathTarget{},
		members:    map[string]pathTarget{},
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
	// ns is this file's package namespace, read off the `package` clause: the
	// descriptor prefix of every type it declares. "" for the default package.
	ns string

	out       facts.FileFacts
	nextScope facts.LocalID
	nextOcc   facts.LocalID

	scopes    []scopeRec
	scopeByID map[facts.LocalID]scopeRec

	// types holds simple names an import bound to a whole type (`import a.b.C;`
	// binds `C`); members holds names bound to one static member of a type
	// (`import static a.b.C.m;` binds `m`), and the target is the *owning
	// type's* suffix, since only the use site knows whether `m` is called or
	// read.
	types   map[string]pathTarget
	members map[string]pathTarget

	// claimed holds identifier ranges a definition or an import already owns,
	// so a reference pattern matching the same identifier is dropped.
	claimed map[span]bool
	// descIndex maps a definition's full descriptor to its occurrence — the
	// same-file half of what the link pass does across files.
	descIndex  map[string]facts.LocalID
	defsByName map[string][]defRec
}

func (b *builder) build(matches []sitter.QueryMatch) facts.FileFacts {
	b.collectScopes(matches)
	b.collectPackage(matches)
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
// collectScopes sorts by (start ascending, end descending) and the `program`
// node spans everything, so it is always the first scope emitted.
func (b *builder) fileScope() facts.LocalID {
	if len(b.scopes) == 0 {
		return facts.NoID
	}
	return b.scopes[0].id
}

// ----------------------------------------------------------------- package ---

// collectPackage sets this file's namespace and emits the definition of the
// package the file belongs to.
//
// Two patterns produce a @definition.package match and the declaration wins:
// `(package_declaration (_) @name)` when the file has a `package` clause, and
// `(program)` when it does not. Java is the first language since Go whose file
// says which package it is in, so this is the first stanza since Go with a real
// identifier to hang the occurrence on rather than a zero-width point at byte 0.
//
// Everything else about it is the Go stanza's package definition: neutral-core
// `package` kind, descriptor equal to the namespace, and a `defines` edge from
// the file — which together are exactly what link's `imports` derivation joins an
// `import` reference against.
func (b *builder) collectPackage(matches []sitter.QueryMatch) {
	var decl *sitter.Node
	found := false
	for _, m := range matches {
		root, name, ok := roots(m, "definition.package")
		if !ok {
			continue
		}
		found = true
		if root.Node.Type(b.lang) == "package_declaration" && name != nil {
			decl = name.Node
		}
	}
	if !found {
		return
	}

	start, end := uint32(0), uint32(0)
	if decl != nil {
		b.ns = namespaceOf(dotted(b.text(decl)))
		start, end = decl.StartByte(), decl.EndByte()
		b.claimSubtree(decl)
	}

	desc := facts.Descriptor{Prefix: b.coord, Suffix: b.ns}
	// Explicitly the file scope: the package clause is the first thing in the
	// file and the innermost scope containing a zero-width point at byte 0 would
	// still be the file, but saying so keeps the default-package case honest.
	occ := b.addOccurrenceIn(b.fileScope(), desc, facts.RoleDefinition, facts.KindPackage, packageName(b.coord, b.ns), start, end)
	b.edge(facts.EdgeDefines, facts.FileRef(), facts.OccurrenceRef(occ))
	if _, dup := b.descIndex[desc.String()]; !dup {
		b.descIndex[desc.String()] = occ
	}
}

// ----------------------------------------------------------------- imports ---

// collectImports records what each `import` binds, and emits one package
// occurrence per package named.
//
// Import *edges* are cross-file and therefore the link pass's (§4.4); what is
// extracted is the occurrence, whose descriptor names the imported package. That
// descriptor is byte-identical to the one the target file's own package
// declaration produces — both are the dotted name with slashes — which is what
// lets link derive `imports` by descriptor join.
//
// Every identifier the statement consumed is claimed, so the `.` reference
// patterns — which match happily inside an import — do not emit a second, weaker
// occurrence over the same bytes.
func (b *builder) collectImports(matches []sitter.QueryMatch) {
	type cand struct{ stmt, name *sitter.Node }
	var cands []cand
	for _, m := range matches {
		root, name, ok := roots(m, "import")
		if !ok || name == nil {
			continue
		}
		cands = append(cands, cand{stmt: root.Node, name: name.Node})
	}
	sort.SliceStable(cands, func(i, j int) bool {
		return cands[i].name.StartByte() < cands[j].name.StartByte()
	})

	for _, cd := range cands {
		b.claimSubtree(cd.name)
		b.importDeclaration(cd.stmt, cd.name)
	}
}

// importDeclaration resolves one import and records what it binds.
//
// The path is split on Java's naming convention (see splitPath), which is the
// only thing that separates `import a.b.C;` from `import a.b.c;` — the grammar
// gives both the same shape. On-demand imports (`import a.b.*;`) bind nothing
// nameable, which is the wildcard's whole problem: the names it brings into
// scope are the *other* file's to state.
func (b *builder) importDeclaration(stmt, path *sitter.Node) {
	segs, nodes := b.pathSegments(path)
	if len(segs) == 0 {
		return
	}
	pkg, types, members := splitPath(segs)

	// The package occurrence, over exactly the bytes the package name occupies.
	// It is the row link's `imports` derivation joins against.
	c, ns := b.namespaceTarget(pkg)
	if len(pkg) > 0 {
		b.addOccurrence(
			facts.Descriptor{Prefix: c, Suffix: ns},
			facts.RoleReference, facts.KindPackage, pkg[len(pkg)-1],
			nodes[len(pkg)-1].StartByte(), nodes[len(pkg)-1].EndByte(),
		)
	}
	if len(types) == 0 {
		return
	}

	// The type the path names, and the occurrence that makes the import itself
	// navigable to the class it imports.
	target := pathTarget{coord: c, suffix: ns}
	for _, t := range types {
		target = pathTarget{coord: target.coord, suffix: target.suffix + t + "#"}
	}
	typeEnd := len(pkg) + len(types) - 1
	b.addOccurrence(
		facts.Descriptor{Prefix: target.coord, Suffix: target.suffix},
		facts.RoleReference, facts.KindType, types[len(types)-1],
		nodes[typeEnd].StartByte(), nodes[typeEnd].EndByte(),
	)

	// What the statement brought into scope. A wildcard binds nothing: the
	// simple names it makes available are not written anywhere in this file.
	if hasAsterisk(stmt, b.lang) {
		return
	}
	switch {
	case len(members) == 0:
		b.types[types[len(types)-1]] = target
	default:
		// `import static a.b.C.m;` — the member's own descriptor needs a
		// terminator only the use site can choose, so the owning type is what is
		// recorded.
		b.members[members[len(members)-1]] = target
	}
}

// ------------------------------------------------------------------- paths ---

// pathTarget is what a dotted path resolved to: the coordinate and descriptor
// suffix its members hang off.
type pathTarget struct {
	coord  coord.Coord
	suffix string
}

// pathSegments flattens a dotted name into its segments and the nodes that cover
// each prefix of it. `a.b.C` yields ["a","b","C"] and the three nodes spanning
// `a`, `a.b` and `a.b.C`, which is what lets an occurrence be emitted over
// exactly the package half of an import.
func (b *builder) pathSegments(n *sitter.Node) (segs []string, nodes []*sitter.Node) {
	if n == nil {
		return nil, nil
	}
	switch n.Type(b.lang) {
	case "scoped_identifier", "scoped_type_identifier", "field_access":
		scope := n.ChildByFieldName("scope", b.lang)
		if scope == nil {
			scope = n.ChildByFieldName("object", b.lang)
		}
		name := n.ChildByFieldName("name", b.lang)
		if name == nil {
			name = n.ChildByFieldName("field", b.lang)
		}
		if scope == nil || name == nil {
			// `scoped_type_identifier` carries no field names in this grammar;
			// its scope is every named child but the last.
			if n.NamedChildCount() < 2 {
				return nil, nil
			}
			scope, name = n.NamedChild(0), n.NamedChild(n.NamedChildCount()-1)
		}
		segs, nodes = b.pathSegments(scope)
		if segs == nil {
			return nil, nil
		}
		return append(segs, b.text(name)), append(nodes, n)
	case "identifier", "type_identifier":
		return []string{b.text(n)}, []*sitter.Node{n}
	case "generic_type":
		return b.pathSegments(firstNamedChild(n))
	}
	return nil, nil
}

// splitPath divides a dotted name into its package, type and member parts on
// Java's naming convention: lowercase segments are the package, the first
// uppercase one begins the type, and a lowercase segment after a type is a
// member of it.
//
// The convention is doing real work here and the grammar cannot. `a.b.C.D` names
// a nested type in package `a.b`; `a.b.c.D` names a top-level type in package
// `a.b.c`; and the two parse to the same shape. Java's own convention — packages
// lowercase, types UpperCamelCase — is the only thing that distinguishes them,
// and it is near-universal because the JLS recommends it and every tool in the
// ecosystem assumes it. Being wrong costs a descriptor that matches nothing,
// never one that matches something else.
func splitPath(segs []string) (pkg, types, members []string) {
	i := 0
	for i < len(segs) && !isTypeName(segs[i]) {
		i++
	}
	j := i
	for j < len(segs) && isTypeName(segs[j]) {
		j++
	}
	return segs[:i], segs[i:j], segs[j:]
}

// namespaceTarget renders a package path as a coordinate and namespace
// descriptor.
//
// The first segment decides the coordinate, and there is no third case beyond "a
// package of the JDK" and "a package of whatever this repository is". Java's
// package names carry no artifact identity at all — `com.example.util` says
// nothing about which jar it comes from — so the rule is "the JDK if the root
// says so, this coordinate otherwise", and a third-party dependency lands at a
// namespace of this coordinate that holds no definitions, which is an unresolved
// reference and not a wrong edge. This is Rust's `firstSegment` problem and
// Python's `import os` versus `import mypkg` problem, one more time.
func (b *builder) namespaceTarget(pkg []string) (coord.Coord, string) {
	if len(pkg) == 0 {
		return b.coord, ""
	}
	if jdkRoots[pkg[0]] {
		return coord.Foreign(b.coord.Scheme, b.coord.Manager, pkg[0]), namespaceOf(pkg[1:])
	}
	return b.coord, namespaceOf(pkg)
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
// capture name does not. Java needs it in exactly one place, and Java's is the
// shortest list of any stanza here: the grammar has a distinct node for a class,
// an interface, an enum, a record, an annotation type, a method and a
// constructor, so nothing else has to be inferred at all.
func (b *builder) refineKind(kind string, node *sitter.Node) string {
	if kind == facts.KindParameter && b.isRecordComponent(node) {
		// A record's component is written where a parameter is written and is
		// not one: `record Point(int x, int y)` declares state and an accessor
		// for it, which is what the neutral core's `field` kind means.
		return facts.KindField
	}
	return kind
}

// isRecordComponent reports whether a formal parameter is a record's component
// rather than a callable's argument, which the CST says by where the parameter
// list hangs.
func (b *builder) isRecordComponent(node *sitter.Node) bool {
	if node.Type(b.lang) != "formal_parameter" {
		return false
	}
	list := node.Parent()
	if list == nil || list.Type(b.lang) != "formal_parameters" {
		return false
	}
	owner := list.Parent()
	return owner != nil && owner.Type(b.lang) == "record_declaration"
}

// definitionDescriptor builds the SCIP descriptor for a definition from its
// capture hierarchy (SPEC.md §5).
//
// The prefix is a return value for symmetry with the other stanzas, but Java has
// no counterpart to Rust's `impl MyTrait for Vec<T>`: a Java class's members are
// written inside the class, so a definition is always this file's coordinate's.
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
// enclosing named container of n — a type or a callable — or this file's package
// namespace when there is none. A block, a body, a lambda and an anonymous class
// are transparent: none has a name, so none has a descriptor of its own and what
// is inside belongs to the enclosing container.
//
// This is where nested and inner classes get their descriptors, and it is why
// they needed no special case: each named container on the way out contributes
// exactly one component, so `Outer#Inner#` and `Outer#run().Local#` fall out of
// the same walk that produces `Greeter#greet().`.
func (b *builder) containerSuffix(n *sitter.Node) (coord.Coord, string) {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "class_declaration", "interface_declaration", "enum_declaration",
			"record_declaration", "annotation_type_declaration":
			if name := b.fieldText(p, "name"); name != "" {
				c, s := b.containerSuffix(p)
				return c, s + name + "#"
			}
		case "method_declaration", "constructor_declaration", "compact_constructor_declaration":
			if name := b.fieldText(p, "name"); name != "" {
				c, s := b.containerSuffix(p)
				return c, s + name + "()."
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
		case "class_declaration", "interface_declaration", "enum_declaration",
			"record_declaration", "annotation_type_declaration":
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

// superTarget is what `super` names: the class the enclosing class extends. A
// class with no `extends` extends `java.lang.Object`, which is the JDK's and is
// named as such rather than left unknown.
func (b *builder) superTarget(n *sitter.Node) (pathTarget, bool) {
	decl := b.enclosingType(n)
	if decl == nil {
		return pathTarget{}, false
	}
	if sc := decl.ChildByFieldName("superclass", b.lang); sc != nil {
		if name := b.unwrapType(firstNamedChild(sc)); name != "" {
			c, s := b.resolveTypeName(n, name)
			return pathTarget{coord: c, suffix: s}, true
		}
	}
	return pathTarget{coord: b.builtin(), suffix: "lang/Object#"}, true
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
		nameNode := root.Node
		if name != nil {
			nameNode = name.Node
		}
		if nameNode.Type(b.lang) == "scoped_type_identifier" {
			// The bare `(scoped_type_identifier)` pattern names its own last
			// segment; the descriptor is built from the whole path.
			if last := lastNamedChild(nameNode); last != nil {
				nameNode = last
			}
		}
		s := span{nameNode.StartByte(), nameNode.EndByte()}
		if b.claimed[s] {
			continue // a definition or an import already owns this identifier
		}
		cand := refRec{role: suffixAfter(root.Name, "reference."), node: root.Node, nameNode: nameNode}
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
		if r.role == "type" && name == varType {
			// `var` is a reserved type *name*, not a type: the grammar hands it
			// back as a plain `type_identifier`, and emitting an occurrence for
			// it would invent a symbol called `var` in this package.
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

// callDescriptor names the target of a method invocation.
//
// Every Java callable is a member of a type, so the whole question is which type
// — and the answer is whatever the receiver resolves to. A bare `f()` is a member
// of the enclosing type or of a statically imported one; `g.f()` is a member of
// whatever `g` was declared as, read off the declaration and never inferred; and
// `Type.f()` is exact, because a type name is resolvable syntactically. Where the
// receiver is unknowable file-locally the type component is SCIP's ".", so the
// descriptor cannot false-match a real definition.
func (b *builder) callDescriptor(r refRec, name string) (facts.Descriptor, string) {
	object := r.node.ChildByFieldName("object", b.lang)
	if object == nil {
		// `f()` — a member of the enclosing type, unless a static import bound
		// the name to another type's.
		if t, ok := b.members[name]; ok {
			return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "()."}, facts.KindMethod
		}
		if t, ok := b.thisTarget(r.nameNode); ok {
			return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "()."}, facts.KindMethod
		}
		return facts.Descriptor{Prefix: b.coord, Suffix: b.ns + name + "()."}, facts.KindMethod
	}
	if t, ok := b.valueTarget(object, r.nameNode.StartByte()); ok {
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "()."}, facts.KindMethod
	}
	return facts.Descriptor{Prefix: b.coord, Suffix: b.ns + coord.Unknown + "#" + name + "()."}, facts.KindMethod
}

func (b *builder) readDescriptor(r refRec, name string) (facts.Descriptor, string) {
	if b.isQualifier(r) {
		// The left half of `a.b`, which names a value, a type or a package.
		return b.qualifierDescriptor(r, name)
	}

	// The right half of `a.b`: a member of whatever the left half is.
	object := r.node.ChildByFieldName("object", b.lang)
	if object == nil {
		object = r.node.ChildByFieldName("scope", b.lang)
	}
	if t, ok := b.valueTarget(object, r.nameNode.StartByte()); ok {
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "."}, facts.KindField
	}
	if t, ok := b.packageTarget(object); ok {
		// `a.b.C` read as an expression: the segment after a package is a type.
		kind := facts.KindPackage
		suffix := t.suffix + name + "/"
		if isTypeName(name) {
			kind, suffix = facts.KindType, t.suffix+name+"#"
		}
		return facts.Descriptor{Prefix: t.coord, Suffix: suffix}, kind
	}
	return facts.Descriptor{Prefix: b.coord, Suffix: b.ns + coord.Unknown + "#" + name + "."}, facts.KindField
}

// qualifierDescriptor names the left half of a `.`, which is the one place
// Java's single member operator has to be disambiguated rather than read.
func (b *builder) qualifierDescriptor(r refRec, name string) (facts.Descriptor, string) {
	// A binding in scope wins: a local named `list` shadows a package named
	// `list`, which is also what javac does.
	if def, ok := b.lookup(name, r.nameNode.StartByte()); ok {
		return b.occurrence(def.occ).Descriptor, facts.KindVariable
	}
	if t, ok := b.types[name]; ok {
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix}, facts.KindType
	}
	if isTypeName(name) {
		c, s := b.resolveTypeName(r.nameNode, name)
		return facts.Descriptor{Prefix: c, Suffix: s}, facts.KindType
	}
	// A lowercase leading segment is the root of a fully qualified name, which
	// in Java is always absolute — there are no relative package references.
	c, ns := b.namespaceTarget([]string{name})
	return facts.Descriptor{Prefix: c, Suffix: ns}, facts.KindPackage
}

func (b *builder) typeReferenceDescriptor(r refRec, name string) (facts.Descriptor, string) {
	if !isTypeName(name) && b.isTypeQualifier(r.nameNode) {
		// The scope half of a qualified type name, spelled lowercase: a package
		// root and not a type. `java` in `java.util.function.Function` is one,
		// and without this it would name a type called `java` in this package.
		c, ns := b.namespaceTarget([]string{name})
		return facts.Descriptor{Prefix: c, Suffix: ns}, facts.KindPackage
	}
	if r.node.Type(b.lang) == "scoped_type_identifier" {
		// `java.util.List` is one type reference whose name is `List` and whose
		// meaning is in the qualifier, so the qualified form resolves the path
		// rather than the name.
		if segs, _ := b.pathSegments(r.node); len(segs) > 0 {
			pkg, types, _ := splitPath(segs)
			c, ns := b.namespaceTarget(pkg)
			for _, t := range types {
				ns += t + "#"
			}
			// The bare pattern matches every prefix of a qualified name as well
			// as the whole of it, so `java.util` inside `java.util.List` arrives
			// here too. It names a package and not a type, and saying so is what
			// stops three phantom types being emitted for one import.
			kind := facts.KindType
			if len(types) == 0 {
				kind = facts.KindPackage
			}
			return facts.Descriptor{Prefix: c, Suffix: ns}, kind
		}
	}
	c, suffix := b.resolveTypeName(r.nameNode, name)
	return facts.Descriptor{Prefix: c, Suffix: suffix}, facts.KindType
}

// resolveTypeName names a type by the coordinate and descriptor suffix it lives
// at. The name may be dotted, when it came from a declaration this stanza read
// the text of rather than the CST.
//
// The order is javac's own, minus the classpath: an import wins, then a type
// declared in this file (which is what makes a nested type resolvable by its
// simple name), then `java.lang`, then this file's own package. The last is
// Java's implicit same-package visibility and is why a two-file package needs no
// import at all.
func (b *builder) resolveTypeName(n *sitter.Node, typeName string) (coord.Coord, string) {
	if strings.Contains(typeName, ".") {
		pkg, types, _ := splitPath(dotted(typeName))
		c, ns := b.namespaceTarget(pkg)
		for _, t := range types {
			ns += t + "#"
		}
		return c, ns
	}
	if t, ok := b.types[typeName]; ok {
		return t.coord, t.suffix
	}
	if c, s, ok := b.localType(n, typeName); ok {
		return c, s
	}
	if javaLang[typeName] {
		return b.builtin(), "lang/" + typeName + "#"
	}
	return b.coord, b.ns + typeName + "#"
}

// localType resolves a simple type name against the types this file declares,
// innermost container first. It is what makes `Inner i;` inside `Outer` name
// `Outer#Inner#` rather than the package-level `Inner#` that does not exist.
//
// Only names this file already defined, which is why it consults descIndex:
// collectDefinitions runs first, so every type declared here is in it, and a
// name that is not is not this file's to claim.
//
// A method is a container too, and has to be: `class Local {}` written inside a
// method body is a real Java declaration and its descriptor carries the method's
// `().` component, so a walk that visited only types would miss it.
func (b *builder) localType(n *sitter.Node, name string) (coord.Coord, string, bool) {
	try := func(c coord.Coord, prefix string) (coord.Coord, string, bool) {
		candidate := facts.Descriptor{Prefix: c, Suffix: prefix + name + "#"}
		if _, ok := b.descIndex[candidate.String()]; ok {
			return c, prefix + name + "#", true
		}
		return coord.Coord{}, "", false
	}
	for p := n; p != nil; p = p.Parent() {
		var component string
		switch p.Type(b.lang) {
		case "class_declaration", "interface_declaration", "enum_declaration",
			"record_declaration", "annotation_type_declaration":
			component = "#"
		case "method_declaration", "constructor_declaration", "compact_constructor_declaration":
			component = "()."
		default:
			continue
		}
		c, s := b.containerSuffix(p)
		if own := b.fieldText(p, "name"); own != "" {
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
	case "super":
		return b.superTarget(value)
	case "parenthesized_expression":
		return b.valueTarget(firstNamedChild(value), pos)
	case "object_creation_expression":
		// `new Greeter("x").greet()` — the receiver's type is written right
		// there, which makes it the one expression form worth reading back.
		if name := b.unwrapType(value.ChildByFieldName("type", b.lang)); name != "" {
			c, s := b.resolveTypeName(value, name)
			return pathTarget{coord: c, suffix: s}, true
		}
	case "identifier":
		name := b.text(value)
		if typeName, ok := b.localTypeAt(name, pos); ok && typeName != "" {
			c, s := b.resolveTypeName(value, typeName)
			return pathTarget{coord: c, suffix: s}, true
		}
		if t, ok := b.types[name]; ok {
			return t, true
		}
		if isTypeName(name) {
			// A static call on a type this file or its package declares.
			c, s := b.resolveTypeName(value, name)
			return pathTarget{coord: c, suffix: s}, true
		}
	case "field_access", "scoped_identifier", "scoped_type_identifier":
		segs, _ := b.pathSegments(value)
		if len(segs) == 0 {
			// Not a qualified name. The one shape worth recovering anyway is
			// `this.field.member()`: the field is declared in this file, so its
			// type is written down and the member it reaches is nameable.
			if obj := value.ChildByFieldName("object", b.lang); obj != nil && obj.Type(b.lang) == "this" {
				field := b.fieldText(value, "field")
				if typeName, ok := b.localTypeAt(field, pos); ok && typeName != "" {
					c, s := b.resolveTypeName(value, typeName)
					return pathTarget{coord: c, suffix: s}, true
				}
			}
			return pathTarget{}, false
		}
		// A binding shadows a package, so a chain starting at a local is a
		// field access and not a qualified name.
		if _, ok := b.lookup(segs[0], pos); ok {
			return pathTarget{}, false
		}
		pkg, types, members := splitPath(segs)
		if len(types) == 0 || len(members) > 0 {
			return pathTarget{}, false
		}
		c, ns := b.namespaceTarget(pkg)
		for _, t := range types {
			ns += t + "#"
		}
		return pathTarget{coord: c, suffix: ns}, true
	}
	return pathTarget{}, false
}

// packageTarget resolves a node to the package it names, for the segments of a
// qualified name that are still below the type.
func (b *builder) packageTarget(n *sitter.Node) (pathTarget, bool) {
	if n == nil {
		return pathTarget{}, false
	}
	segs, _ := b.pathSegments(n)
	if len(segs) == 0 {
		return pathTarget{}, false
	}
	pkg, types, _ := splitPath(segs)
	if len(types) > 0 || len(pkg) == 0 {
		return pathTarget{}, false
	}
	c, ns := b.namespaceTarget(pkg)
	return pathTarget{coord: c, suffix: ns}, true
}

// isTypeQualifier reports whether a node is the scope half of a qualified type
// name — the `java` of `java.util` — rather than the segment it qualifies.
func (b *builder) isTypeQualifier(n *sitter.Node) bool {
	p := n.Parent()
	if p == nil || p.Type(b.lang) != "scoped_type_identifier" {
		return false
	}
	first := firstNamedChild(p)
	return first != nil && first.StartByte() == n.StartByte() && first.EndByte() == n.EndByte()
}

// isQualifier reports whether the captured identifier is the left half of a `.`
// rather than the member it reaches.
func (b *builder) isQualifier(r refRec) bool {
	for _, field := range []string{"object", "scope"} {
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
// The "no later than pos" rule is a local's, and Java fields break it — a method
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

func (b *builder) localTypeAt(name string, pos uint32) (string, bool) {
	d, ok := b.lookup(name, pos)
	if !ok {
		return "", false
	}
	return d.typeName, true
}

// declaredType recovers the syntactic type of a binding, parameter or field —
// enough to name the members reached through it, without any type inference. ""
// means unknown, which downstream becomes SCIP's "." rather than a guess.
//
// Java writes the type down at every declaration site, which makes this stanza's
// job strictly easier than Rust's or Python's: there is no initialiser to read
// back and no convention to lean on. The one exception is `var`, which the
// grammar gives its own node type, so an inferred local yields "" and its members
// land on "." — the honest answer, since the type is in the initialiser's *type*
// and not in its syntax.
func (b *builder) declaredType(node *sitter.Node) string {
	switch node.Type(b.lang) {
	case "local_variable_declaration", "field_declaration", "constant_declaration",
		"formal_parameter", "enhanced_for_statement", "resource":
		return b.unwrapType(node.ChildByFieldName("type", b.lang))
	case "catch_formal_parameter":
		// The grammar gives a catch parameter's type no field name — a
		// multi-catch is a list of alternatives — so it is found by node type.
		for i := 0; i < node.NamedChildCount(); i++ {
			if c := node.NamedChild(i); c.Type(b.lang) == "catch_type" {
				return b.unwrapType(c)
			}
		}
	case "spread_parameter":
		return b.unwrapType(firstNamedChild(node))
	}
	return ""
}

// unwrapType reduces a type expression to the bare (possibly dotted) name a
// descriptor can use. An array and a generic application are transparent:
// `List<String>` is named by `List`, because the members reached through it are
// `List`'s. A primitive, a `var` and `void` yield "" — none of them reaches a
// member a descriptor could name.
func (b *builder) unwrapType(t *sitter.Node) string {
	for depth := 0; t != nil && depth < 16; depth++ {
		switch t.Type(b.lang) {
		case "array_type":
			t = t.ChildByFieldName("element", b.lang)
		case "generic_type", "catch_type":
			// `List<String>` is named by `List`, and a multi-catch by its first
			// alternative: both put the name first and the qualification after.
			t = firstNamedChild(t)
		case "annotated_type":
			// `@NonNull String` puts the annotations first and the type last.
			t = lastNamedChild(t)
		case "type_identifier", "scoped_type_identifier":
			if name := b.text(t); name != varType {
				return name
			}
			return ""
		default:
			return ""
		}
	}
	return ""
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
// patterns — which match inside an import or a package clause as readily as
// anywhere else — do not emit a second occurrence over bytes already described.
func (b *builder) claimSubtree(n *sitter.Node) {
	if n == nil {
		return
	}
	switch n.Type(b.lang) {
	case "identifier", "type_identifier":
		b.claimed[span{n.StartByte(), n.EndByte()}] = true
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		b.claimSubtree(n.NamedChild(i))
	}
}

// builtin is the coordinate of the Java platform, which belongs to no artifact
// this index owns. `java` is the root package the platform is reached through.
func (b *builder) builtin() coord.Coord {
	return coord.Foreign(b.coord.Scheme, b.coord.Manager, "java")
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

// hasAsterisk reports whether an import is an on-demand one. The `*` is a
// sibling of the path rather than part of it, so the statement is what is asked.
func hasAsterisk(stmt *sitter.Node, lang *sitter.Language) bool {
	if stmt == nil {
		return false
	}
	for i := 0; i < stmt.NamedChildCount(); i++ {
		if stmt.NamedChild(i).Type(lang) == "asterisk" {
			return true
		}
	}
	return false
}

// varType is Java's inferred-local marker. It is written where a type is
// written and the grammar parses it as one, but it names no type: the type is in
// the initialiser's *type*, which is a type checker's answer and not a syntactic
// one, so a `var` local yields "" and its members land on SCIP's ".".
const varType = "var"

// isTypeName reports whether a path segment names a type rather than a package
// or a member, by Java's naming convention: types are UpperCamelCase, packages
// and members start lowercase. The JLS recommends it and the whole ecosystem
// follows it, which is what makes it reliable enough to build a descriptor on;
// see splitPath.
func isTypeName(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r)
}

// dotted splits a Java qualified name into its segments.
func dotted(name string) []string {
	if name == "" {
		return nil
	}
	return strings.Split(name, ".")
}

// namespaceOf renders package segments as a SCIP namespace descriptor:
// slash-separated and slash-terminated. The empty package renders "", which is
// the default package and is a real namespace rather than a missing one.
func namespaceOf(segs []string) string {
	if len(segs) == 0 {
		return ""
	}
	return strings.Join(segs, "/") + "/"
}

// packageName is what a package occurrence is called in the `name` column: the
// last segment of its namespace, or — for the default package, which has no
// namespace at all — the last segment of the artifact's name.
func packageName(c coord.Coord, ns string) string {
	if trimmed := strings.TrimSuffix(ns, "/"); trimmed != "" {
		return lastSegment(trimmed)
	}
	name := c.Name
	if name == "" || name == coord.Unknown {
		return ""
	}
	if i := strings.LastIndex(name, ":"); i >= 0 {
		name = name[i+1:]
	}
	return lastSegment(name)
}

func lastSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// jdkRoots are the top-level package names that are never this artifact's.
//
// It is what namespaceTarget needs and what Java's syntax cannot supply: a
// package name carries no artifact identity, so `com.example.util` is this
// repository's or a dependency's with nothing in the source to say which. These
// are the roots the platform reserves — a class under any of them is shipped with
// the JDK or the Jakarta EE API and is never indexed here — and a third-party
// dependency missing from the set is treated as this artifact's, which yields a
// namespace with no definitions in it: an unresolved reference rather than a
// wrong edge.
var jdkRoots = map[string]bool{
	"java": true, "javax": true, "jakarta": true, "jdk": true, "sun": true,
}

// javaLang is the commonly used subset of the types `java.lang` puts in every
// compilation unit without an import. References to them carry a foreign
// coordinate and so never pollute descriptor matching within the indexed
// artifact. An omission costs one reference landing in this artifact's namespace
// with nothing to match, which is what an unrecognised name gets anyway.
var javaLang = map[string]bool{
	"Object": true, "String": true, "StringBuilder": true, "StringBuffer": true,
	"CharSequence": true, "Character": true, "Boolean": true, "Byte": true,
	"Short": true, "Integer": true, "Long": true, "Float": true, "Double": true,
	"Number": true, "Math": true, "System": true, "Class": true, "Enum": true,
	"Record": true, "Iterable": true, "Comparable": true, "Runnable": true,
	"Thread": true, "ThreadLocal": true, "Throwable": true, "Exception": true,
	"RuntimeException": true, "Error": true, "IllegalArgumentException": true,
	"IllegalStateException": true, "NullPointerException": true,
	"UnsupportedOperationException": true, "IndexOutOfBoundsException": true,
	"ClassCastException": true, "ArithmeticException": true,
	"AutoCloseable": true, "Cloneable": true, "Void": true, "Process": true,
	"Override": true, "Deprecated": true, "SuppressWarnings": true,
	"FunctionalInterface": true, "SafeVarargs": true,
}
