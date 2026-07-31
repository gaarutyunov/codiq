// Package rb is the Ruby stanza: the tree-sitter query in query.scm plus the
// mapper here that turns its captures into core facts (SPEC.md §5).
//
// The package is named for the extension, per SPEC.md §12. It imports facts and
// coord and deliberately not extract: it satisfies extract.Parser structurally,
// which is what keeps the registry free of an import cycle.
//
// The mapper's job, and its limits, are the six stanzas before it (§4.3): it
// builds the descriptor *suffix* from the CST; it assigns role and neutral-core
// symbol kind; and it resolves references whose target definition is in the same
// file. It does no type checking, runs no constant-resolution algorithm and looks
// at no other file. A reference it cannot pin down is still emitted, carrying the
// best descriptor syntax allows, and the link pass decides what it means (§7).
// Where a component is genuinely unknowable file-locally the descriptor writes
// SCIP's "." for it, so it names an unresolved symbol rather than false-matching
// a real one.
//
// # Two units of modularity, and Ruby is the first language here that has both
//
// Go's unit is the directory, TypeScript's the file, Python's the file with
// `__init__.py` collapsing to its directory, Rust's the `mod` tree, Java's the
// `package` clause, C#'s the `namespace` declaration. Every one of those is a
// single answer to "where does this symbol live". Ruby has two answers and they
// are about different things:
//
//   - The **constant namespace** is `module`/`class` nesting, and it is the only
//     thing that names a symbol. `Greeter::Greeter` is a path through it, and it
//     is written the same way at the definition and at every reference.
//   - The **load unit** is the file, named by the path `require` spells. Ruby's
//     constants are one global tree that `require` merely populates; a required
//     file contributes no namespace of its own, and two files in one directory
//     are not thereby related.
//
// So this stanza models both, and keeps them apart. A symbol's descriptor is its
// constant path and nothing else — the file it was written in never appears in it
// — while each file additionally defines a `package` whose descriptor is its own
// path (unitNamespace), which is what a `require` occurrence names and what
// link's `imports` derivation joins on. `imports` is file → file, so a path-keyed
// package descriptor is exactly the right key for it, and the two schemes cannot
// collide because a Ruby constant is capitalised and a Ruby path is not.
//
// Deriving symbol descriptors from the *path* instead was considered and is
// wrong, twice over. `lib/greeter/greeter.rb` idiomatically holds
// `module Greeter; class Greeter`, so the path and the nesting are the same
// information written twice and a path-based namespace would double it. And a
// reference carries no path at all: `Greeter::Greeter` written three directories
// away has to render what the declaration rendered, and the constant path is the
// only thing both sides can compute. That is the test cs.go applies to an
// explicit interface implementation, and it decides this the same way.
//
// The visible cost is stated in features/index_rb.feature: Ruby is the first
// language in this graph that **cannot** be made to collide with the others in
// the mixed-language corpus. Five languages write `greeter/Greeter#greet().`
// because each can be made to declare a namespace spelled `greeter`; Ruby cannot,
// because a module name is a `constant` token by the grammar and `module greeter`
// is a syntax error rather than an unidiomatic choice.
//
// # Reopening, which is Ruby's version of the partial class
//
// `class Foo` in five files declares one class, each adding members. C#'s
// partial classes established (issue #19) that two definition rows sharing one
// descriptor are two *sites of one symbol*, and Ruby is that case turned up: a
// reopening needs no keyword, happens across directories, and is the ordinary way
// a Ruby program is written rather than a code-generator's convention.
//
// The property holds here for the same structural reason and one extra one. The
// descriptor is a coordinate and a constant path and says nothing about which file
// a symbol was written in — and because the namespace is the *constant* nesting
// and not the path, two reopenings in two different directories still render one
// descriptor, which a path-derived namespace would have broken. `resolves_to`
// joins a reference against every definition carrying its descriptor, so a call
// reaches whichever site declares the member; `implements` gathers a method set
// over the whole occurrence table with no file predicate (store/sqlc/query.sql),
// so every site sees the union — though see below for why no Ruby type reaches
// that derivation at all.
//
// Reopening a *core* class is the case C# has no analogue for.
// `class String; def blank?; end; end` renders `String#blank?().` at **this
// repository's** coordinate, and deliberately not at a foreign one. A foreign
// coordinate would be shared by every Ruby repository in an index, so two
// unrelated monkey patches of `String` would render one descriptor and the link
// pass would materialise an edge between them; this coordinate can only ever
// collide with another reopening of `String` in the same repository, which is the
// same class. What it costs is that the patch is a definition nothing references,
// because a call `s.blank?` has a receiver whose type is unknowable file-locally.
//
// # What this stanza cannot represent
//
//   - `define_method`, `Class.new`, `const_set`, `method_missing` and every other
//     metaprogrammed definition. They are the reason Ruby is hard, and a
//     file-local CST reader sees a method call and no member.
//   - `include Foo` as an implementation claim. See implements, below.
//   - A `require` resolved through `$LOAD_PATH`. A non-relative require is read as
//     repo-root-relative, so a gem's `require "greeter/version"` — which Bundler
//     resolves under `lib/` — joins nothing. That is py.go's src-layout note
//     verbatim, and the fix is the same one: which directory is the load root is
//     manifest knowledge, so it belongs in coord.
//   - `def <=>`, `def []`, `def +`. An operator is reached by writing the operator,
//     which names no member, so a descriptor for it is one only the definition side
//     could compute.
//   - A constant that is a class and a constant that is an integer.
//     `MAX = 3`, `Point = Struct.new(:x)` and `class Foo` are one statement shape,
//     so all three render `#` and the `symbol_kind` column carries what little more
//     is known.
//   - Refinements (`refine String do … end`). Lexically scoped monkey patches, and
//     nothing in the descriptor can say "only inside this file's `using`".
//
// # implements, and why Ruby does not take part
//
// `include Foo` is Ruby's interface story, and the Go rule — a non-interface type
// implements an interface when its method suffixes contain the interface's — is
// not merely unhelpful here, it is backwards.
//
// A Ruby module's method set is what it *gives*, not what it demands.
// `Comparable` provides `<`, `<=`, `>`, `>=`, `==` and `between?` and demands
// `<=>`; a class that includes it declares only `<=>`. Containment therefore fails
// exactly where the include is real, and the one place it would fire is a class
// that happens to have written `each` and `map` and never included anything —
// which is duck typing producing a claim nobody made.
//
// So this stanza emits `interface` for nothing, and a module is a `package` whose
// descriptor ends `/`, which link's `type_def` CTE (`right(descriptor, 1) = '#'`)
// does not select. Ruby derives no `implements` edges, by construction rather than
// by omission. What `include Foo` does derive is `imports`: it is recorded as what
// it structurally is, a reference to the module's package descriptor, so the file
// that includes a mixin imports the file that declares it.
package rb

import (
	_ "embed"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	sitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/facts"
)

// Lang is the value written to file.lang for the files this stanza handles.
const Lang = "rb"

// Ext is the file extension this stanza is registered under.
const Ext = ".rb"

// singletonComponent is the descriptor component that separates a class's
// singleton (class-level) members from its instance members.
//
// It exists because Ruby is the first language in this graph where one type may
// declare `def foo` and `def self.foo` at once, and they are two different
// methods. C# renders a static and an instance member alike and can afford to:
// the language forbids the collision. Ruby does not, so the descriptor has to
// carry the distinction — and cs.go's test says it may, since a reference site
// *can* reconstruct it. `Greeter.build` has a constant for a receiver, which
// names the class object, while `g.build` has a value; the two are told apart by
// the syntax and nothing else.
//
// The cost is a constant that holds an instance rather than a class —
// `CONFIG = Config.new; CONFIG.load` — whose call is read as a singleton send and
// lands on a descriptor with nothing behind it. An unresolved reference, not a
// wrong edge.
const singletonComponent = "self."

//go:embed query.scm
var queryScheme string

// Parser is the Ruby stanza. Safe for concurrent use: the grammar and compiled
// query are immutable after the first Parse, and each parse checks a
// gotreesitter parser out of a pool.
type Parser struct {
	once    sync.Once
	lang    *sitter.Language
	pool    *sitter.ParserPool
	query   *sitter.Query
	initErr error
}

// New returns the Ruby parser. It is cheap: the grammar is loaded and query.scm
// compiled on the first Parse, so a binary that never parses Ruby never
// decompresses the Ruby grammar — and a query that fails to compile lands in
// ParseError rather than panicking at init.
func New() *Parser { return &Parser{} }

func (p *Parser) init() {
	p.once.Do(func() {
		p.lang = grammars.RubyLanguage()
		if p.lang == nil {
			p.initErr = errors.New("rb: gotreesitter has no Ruby grammar")
			return
		}
		q, err := sitter.NewQuery(queryScheme, p.lang)
		if err != nil {
			p.initErr = fmt.Errorf("rb: compile query.scm: %w", err)
			return
		}
		p.query = q
		p.pool = sitter.NewParserPool(p.lang)
	})
}

// Parse extracts one Ruby file's facts. It never returns an error: a failure is
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

	rel := repoRelative(c.Root, filePath)
	b := &builder{
		lang:       p.lang,
		src:        src,
		coord:      c,
		unit:       unitNamespace(rel),
		dir:        path.Dir("/" + rel)[1:],
		out:        facts.FileFacts{File: file},
		scopeByID:  map[facts.LocalID]scopeRec{},
		claimed:    map[span]bool{},
		descIndex:  map[string]facts.LocalID{},
		defsByName: map[string][]defRec{},
		ivarTypes:  map[string]string{},
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
	typeName string // constant path a binding was initialised from; "" when unknown
}

// pathTarget is what a receiver resolved to: the coordinate and descriptor suffix
// its members hang off.
type pathTarget struct {
	coord  coord.Coord
	suffix string
}

type builder struct {
	lang  *sitter.Language
	src   []byte
	coord coord.Coord
	// unit is this file's load-unit namespace: its own path, which is what a
	// `require` of it names. It is deliberately *not* the namespace of the
	// symbols the file declares; see the package comment.
	unit string
	// dir is this file's directory, repo-relative and slash-separated, and the
	// base `require_relative` counts from.
	dir string

	out       facts.FileFacts
	nextScope facts.LocalID
	nextOcc   facts.LocalID

	scopes    []scopeRec
	scopeByID map[facts.LocalID]scopeRec

	// claimed holds identifier ranges a definition already owns, so a reference
	// pattern matching the same identifier is dropped.
	claimed map[span]bool
	// descIndex maps a definition's full descriptor to its occurrence — the
	// same-file half of what the link pass does across files.
	descIndex  map[string]facts.LocalID
	defsByName map[string][]defRec
	// ivarTypes maps an instance or class variable to the constant path it was
	// last initialised from. It is file-wide rather than scoped because an
	// instance variable belongs to the class and not to the method that first
	// assigned it — `@name` set in `initialize` is the same member `greet` reads.
	ivarTypes map[string]string
}

func (b *builder) build(matches []sitter.QueryMatch) facts.FileFacts {
	b.collectScopes(matches)
	b.collectUnit()
	b.collectDefinitions(matches)
	b.collectAttrs(matches)
	b.collectImports(matches)
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

// -------------------------------------------------------------- load unit ---

// collectUnit emits the definition of the load unit the file *is*.
//
// This is the one place a Ruby file's *path* enters a descriptor, and it is the
// only place it may: a `require` names a path and nothing else, so the thing a
// require refers to has to be keyed by path. Everything else about the occurrence
// is the Go stanza's package definition — neutral-core `package` kind, descriptor
// equal to the namespace, a `defines` edge from the file — which together are what
// link's `imports` derivation joins an importer's @import reference against.
//
// The occurrence is zero-width at the start of the file, for the reason the
// TypeScript and Python stanzas give: there is no identifier to point at, and a
// range spanning the whole file would claim every other occurrence sits inside
// the unit's *name*.
func (b *builder) collectUnit() {
	if b.unit == "" {
		return
	}
	desc := facts.Descriptor{Prefix: b.coord, Suffix: b.unit}
	occ := b.addOccurrenceIn(b.fileScope(), desc, facts.RoleDefinition, facts.KindPackage,
		lastSegment(strings.TrimSuffix(b.unit, "/")), 0, 0)
	b.edge(facts.EdgeDefines, facts.FileRef(), facts.OccurrenceRef(occ))
	if _, dup := b.descIndex[desc.String()]; !dup {
		b.descIndex[desc.String()] = occ
	}
}

// ----------------------------------------------------------------- imports ---

// collectImports emits one occurrence per `require` or `require_relative`,
// naming the load unit it reaches.
//
// Import *edges* are cross-file and therefore the link pass's (§4.4); what is
// extracted is the occurrence, whose descriptor is the required path rendered as
// a namespace — byte-identical to the load-unit definition collectUnit emits for
// the required file, which is what lets link derive `imports` by descriptor join.
func (b *builder) collectImports(matches []sitter.QueryMatch) {
	type cand struct{ call, name *sitter.Node }
	var cands []cand
	for _, m := range matches {
		root, name, ok := roots(m, "import")
		if !ok || name == nil {
			continue
		}
		cands = append(cands, cand{call: root.Node, name: name.Node})
	}
	sort.SliceStable(cands, func(i, j int) bool {
		return cands[i].name.StartByte() < cands[j].name.StartByte()
	})

	for _, cd := range cands {
		spec := b.text(cd.name)
		if spec == "" {
			continue
		}
		c, ns := b.resolveRequire(spec, b.fieldText(cd.call, "method") == "require_relative")
		// The name is the last segment of the *spec* and not of the namespace,
		// so that a standard-library require — whose namespace is empty because
		// the whole path is the foreign coordinate's name — is still called
		// something a reader can find it by.
		b.addOccurrence(
			facts.Descriptor{Prefix: c, Suffix: ns},
			facts.RoleReference, facts.KindPackage,
			lastSegment(strings.TrimSuffix(strings.TrimSuffix(spec, Ext), "/")),
			cd.name.StartByte(), cd.name.EndByte(),
		)
	}
}

// resolveRequire turns a require's path into the coordinate and namespace that
// name the load unit it reaches.
//
// `require_relative` is pure path arithmetic against this file's directory, and
// it is the one require form Ruby makes unambiguous. A plain `require` is
// resolved through `$LOAD_PATH`, which is neither a file nor file-local (§2.5),
// so it is read as repo-root-relative — Rails' rule and a script's, and not a
// gem's, whose `lib/` root is manifest knowledge. A require that climbs out of
// the repository, or names a standard-library feature, becomes foreign rather
// than landing on a namespace of this coordinate that could false-match a file.
func (b *builder) resolveRequire(spec string, relative bool) (coord.Coord, string) {
	spec = strings.TrimSuffix(spec, Ext)
	if spec == "" {
		return b.coord, ""
	}
	if relative {
		joined := path.Join(b.dir, spec)
		if joined == ".." || strings.HasPrefix(joined, "../") {
			return coord.Foreign(b.coord.Scheme, b.coord.Manager, spec), ""
		}
		return b.coord, unitNamespace(joined)
	}
	top, rest, _ := strings.Cut(spec, "/")
	if rbStdlib[top] {
		return coord.Foreign(b.coord.Scheme, b.coord.Manager, top), unitNamespace(rest)
	}
	return b.coord, unitNamespace(spec)
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
		if !ok || name == nil || root.Name == "definition.attr" {
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

		name := b.definitionName(cd.name)
		if name == "" || name == "_" {
			continue
		}
		kind := b.refineKind(cd.kind, cd.node)
		suffix := b.definitionSuffix(kind, cd.node, cd.name, name)
		b.claimSubtree(cd.name)

		desc := facts.Descriptor{Prefix: b.coord, Suffix: suffix}
		occ := b.addOccurrence(desc, facts.RoleDefinition, kind, name, s.start, s.end)
		b.edge(facts.EdgeDefines, facts.FileRef(), facts.OccurrenceRef(occ))

		if _, dup := b.descIndex[desc.String()]; !dup {
			b.descIndex[desc.String()] = occ
		}
		typeName := b.declaredType(cd.node)
		b.defsByName[name] = append(b.defsByName[name], defRec{
			occ:      occ,
			scope:    b.occurrence(occ).Scope,
			start:    s.start,
			typeName: typeName,
		})
		if typeName != "" && isVariableSigil(cd.name.Type(b.lang)) {
			b.ivarTypes[name] = typeName
		}
	}
}

// collectAttrs turns `attr_reader`, `attr_writer` and `attr_accessor` into the
// members they declare.
//
// They are method calls, and treating them as such would be a defensible reading
// of the syntax and a useless reading of the language: Ruby has no field access
// at all, so `attr_accessor :name` is how a class declares that `g.name` and
// `g.name = v` are things one may write, and the majority of Ruby classes declare
// their state this way and no other. The members are `method`s because that is
// what they are — `g.name` is a send — and the writer's descriptor carries the
// `=`, which the reference side reconstructs from the shape of an assignment.
func (b *builder) collectAttrs(matches []sitter.QueryMatch) {
	type cand struct{ call, sym *sitter.Node }
	var cands []cand
	for _, m := range matches {
		root, name, ok := roots(m, "definition.attr")
		if !ok || name == nil {
			continue
		}
		cands = append(cands, cand{call: root.Node, sym: name.Node})
	}
	sort.SliceStable(cands, func(i, j int) bool {
		return cands[i].sym.StartByte() < cands[j].sym.StartByte()
	})

	for _, cd := range cands {
		name := strings.TrimPrefix(b.text(cd.sym), ":")
		if name == "" {
			continue
		}
		container := b.containerSuffix(cd.call)
		start, end := cd.sym.StartByte(), cd.sym.EndByte()
		switch b.fieldText(cd.call, "method") {
		case "attr_reader":
			b.defineAttr(container, name, start, end)
		case "attr_writer":
			b.defineAttr(container, name+"=", start, end)
		case "attr_accessor":
			b.defineAttr(container, name, start, end)
			b.defineAttr(container, name+"=", start, end)
		}
	}
}

// defineAttr emits one generated accessor.
func (b *builder) defineAttr(container, name string, start, end uint32) {
	desc := facts.Descriptor{Prefix: b.coord, Suffix: container + name + "()."}
	if _, dup := b.descIndex[desc.String()]; dup {
		return
	}
	occ := b.addOccurrence(desc, facts.RoleDefinition, facts.KindMethod, name, start, end)
	b.edge(facts.EdgeDefines, facts.FileRef(), facts.OccurrenceRef(occ))
	b.descIndex[desc.String()] = occ
}

// refineKind narrows a capture's kind where the CST carries a distinction the
// capture name does not.
//
// Ruby needs it in one place: `def` is one node type wherever it is written, and
// a `def` outside every `class` and `module` is not a method of anything. It is a
// private method of `Object`, which is the language's way of saying "a
// procedure", so it is the neutral core's `function` — py.go's rule with `module`
// added, because a Ruby module holds methods and a Python module holds functions.
func (b *builder) refineKind(kind string, node *sitter.Node) string {
	if kind == facts.KindMethod && !b.hasEnclosingType(node) {
		return facts.KindFunction
	}
	return kind
}

func (b *builder) hasEnclosingType(n *sitter.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "class", "module", "singleton_class":
			return true
		}
	}
	return false
}

// definitionSuffix builds the SCIP descriptor suffix for a definition from its
// capture hierarchy (SPEC.md §5).
func (b *builder) definitionSuffix(kind string, node, nameNode *sitter.Node, name string) string {
	switch nameNode.Type(b.lang) {
	case "global_variable":
		// `$x` is global whatever it is written inside, so it has no container.
		return name + "."
	case "instance_variable", "class_variable":
		// A member of the enclosing class or module rather than of the method the
		// assignment sits in — Ruby has no field declaration, so the write is the
		// declaration. `@x` in `def self.build` is the class object's and carries
		// the singleton component; `@@x` is the class's either way.
		s, _ := b.selfSuffix(node, nameNode.Type(b.lang) == "instance_variable")
		return s + name + "."
	}

	container := b.containerSuffix(node)
	switch kind {
	case facts.KindFunction, facts.KindMethod:
		if node.Type(b.lang) == "singleton_method" {
			container += singletonComponent
		}
		return container + name + "()."
	case facts.KindType, facts.KindConstant:
		// A class *is* a constant bound to a Class, and `MAX = 3` is the same
		// statement with a different right-hand side. Both render `#` because the
		// reference side cannot tell them apart either: a bare `Foo` is a
		// `constant` token and nothing more. Rendering `.` for the one and `#` for
		// the other would give a descriptor only the definition side can compute,
		// which is the objection cs.go raises to numbering overloads.
		return container + name + "#"
	case facts.KindPackage:
		return container + name + "/"
	case facts.KindParameter:
		return container + "(" + name + ")"
	default: // variable, field
		return container + name + "."
	}
}

// containerSuffix returns the descriptor suffix of the nearest enclosing named
// container of n — a module, a class, an eigenclass or a callable — or "" at the
// file's top level, which is Ruby's root namespace and a real namespace rather
// than a missing one.
//
// A `body_statement`, a `block`, a conditional and a `begin` are transparent:
// none has a name, so none has a descriptor of its own and what is inside belongs
// to the enclosing container. Nested modules, nested classes and a method inside
// an eigenclass all fall out of the one walk, which is why none of them needed a
// special case.
func (b *builder) containerSuffix(n *sitter.Node) string {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "module":
			if name := b.declaredPath(p); name != "" {
				return b.containerSuffix(p) + name + "/"
			}
		case "class":
			if name := b.declaredPath(p); name != "" {
				return b.containerSuffix(p) + name + "#"
			}
		case "singleton_class":
			return b.containerSuffix(p) + singletonComponent
		case "method":
			if name := b.memberName(p.ChildByFieldName("name", b.lang)); name != "" {
				return b.containerSuffix(p) + name + "()."
			}
		case "singleton_method":
			if name := b.memberName(p.ChildByFieldName("name", b.lang)); name != "" {
				return b.containerSuffix(p) + singletonComponent + name + "()."
			}
		}
	}
	return ""
}

// moduleSuffix is the namespace an unqualified constant written at n falls back
// to: the enclosing `module` declarations, and nothing else.
//
// It is containerSuffix with the classes and callables left out, and the two are
// deliberately different walks — cs.go draws the same distinction for the same
// reason. Ruby resolves an unqualified constant against `Module.nesting` and then
// against the ancestors, and a file-local reader can follow only the first half;
// of that half, the modules are what sibling files share. A class is not: two
// classes in one module are siblings, and a constant written in one of them names
// the module's member far more often than its own.
func (b *builder) moduleSuffix(n *sitter.Node) string {
	for p := n; p != nil; p = p.Parent() {
		if p.Type(b.lang) != "module" {
			continue
		}
		if name := b.declaredPath(p); name != "" {
			return b.moduleSuffix(p.Parent()) + name + "/"
		}
	}
	return ""
}

// selfSuffix returns the descriptor suffix `self` names at n: the enclosing
// class, module or eigenclass, with the singleton component appended when n sits
// inside a `def self.…` and singleton is asked for.
//
// It is what resolves a receiverless send, an explicit `self.foo`, and an
// instance variable — all three of which name a member of the same thing.
func (b *builder) selfSuffix(n *sitter.Node, singleton bool) (string, bool) {
	inSingleton := false
	for p := n; p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "singleton_method":
			inSingleton = true
		case "singleton_class":
			return b.containerSuffix(p) + singletonComponent, true
		case "class", "module":
			name := b.declaredPath(p)
			if name == "" {
				return "", false
			}
			term := "#"
			if p.Type(b.lang) == "module" {
				term = "/"
			}
			s := b.containerSuffix(p) + name + term
			if singleton && inSingleton {
				s += singletonComponent
			}
			return s, true
		}
	}
	return "", false
}

// -------------------------------------------------------------- references ---

type refRec struct {
	role     string
	node     *sitter.Node
	nameNode *sitter.Node
}

// refPriority ranks the roles that can capture the same identifier. A call is
// more specific than a read of the same member, and a constant reference is more
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
// structural node wins, because the wider node is the one that saw the `::` and
// therefore knows strictly more about the identifier than the narrower match
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
		s := span{nameNode.StartByte(), nameNode.EndByte()}
		if b.claimed[s] {
			continue // a definition already owns this identifier
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
		if r.role == "call" && b.isDirective(r.node) {
			// `require`, `include` and `attr_accessor` are method calls and are
			// not read as ones: what each of them means is carried by the path or
			// the module it names, and a second occurrence over the keyword would
			// describe the same bytes more weakly.
			continue
		}
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
		return b.constantReference(r, name)
	default:
		return b.readDescriptor(r, name)
	}
}

// callDescriptor names the target of a send.
//
// Every Ruby callable is a method of something, so the whole question is of what,
// and the answer is whatever the receiver resolves to. A bare `f(x)` is a send to
// `self`; `C.f` is a send to the class object, which is the one receiver Ruby
// writes down exactly; `g.f` is a send to whatever `g` was initialised from, read
// off the assignment and never inferred. Where the receiver is unknowable
// file-locally the type component is SCIP's ".", so the descriptor cannot
// false-match a real definition.
func (b *builder) callDescriptor(r refRec, name string) (facts.Descriptor, string) {
	name = b.sendName(r.node, name)
	receiver := r.node.ChildByFieldName("receiver", b.lang)

	if receiver == nil {
		// A send to self: a method of the enclosing class or module, or — at the
		// file's top level — one of the file's own `def`s.
		if s, ok := b.localDeclared(r.nameNode, []string{name}, "()."); ok {
			return facts.Descriptor{Prefix: b.coord, Suffix: s}, facts.KindMethod
		}
		if s, ok := b.selfSuffix(r.nameNode, true); ok {
			return facts.Descriptor{Prefix: b.coord, Suffix: s + name + "()."}, facts.KindMethod
		}
		return facts.Descriptor{Prefix: b.coord, Suffix: name + "()."}, facts.KindFunction
	}
	if t, ok := b.valueTarget(receiver, r.nameNode.StartByte()); ok {
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "()."}, facts.KindMethod
	}
	return facts.Descriptor{Prefix: b.coord, Suffix: coord.Unknown + "#" + name + "()."}, facts.KindMethod
}

// sendName is the member a call actually names. It is the method identifier,
// except on the left of an assignment, where Ruby's writer syntax hides the `=`:
// `g.name = v` parses as an assignment whose left is a call to `name`, and the
// member it reaches is `name=`. Recovering it is a syntactic fact and not a
// guess, and it is what makes a `def name=(v)` definition reachable from a
// reference at all.
func (b *builder) sendName(call *sitter.Node, name string) string {
	parent := call.Parent()
	if parent == nil {
		return name
	}
	switch parent.Type(b.lang) {
	case "assignment", "operator_assignment":
		if left := parent.ChildByFieldName("left", b.lang); sameSpan(left, call) {
			return name + "="
		}
	}
	return name
}

// readDescriptor names a plain read: a variable of one of Ruby's four flavours,
// or a bare lowercase name that turns out to be a send.
func (b *builder) readDescriptor(r refRec, name string) (facts.Descriptor, string) {
	switch r.nameNode.Type(b.lang) {
	case "instance_variable":
		s, _ := b.selfSuffix(r.nameNode, true)
		return facts.Descriptor{Prefix: b.coord, Suffix: s + name + "."}, facts.KindField
	case "class_variable":
		s, _ := b.selfSuffix(r.nameNode, false)
		return facts.Descriptor{Prefix: b.coord, Suffix: s + name + "."}, facts.KindField
	case "global_variable":
		return facts.Descriptor{Prefix: b.coord, Suffix: name + "."}, facts.KindVariable
	}

	// A bare lowercase name. Ruby's own rule decides it and this stanza applies
	// exactly that rule: a local binding assigned earlier in an enclosing scope
	// wins, and everything else is a send to `self`. The CST cannot tell the two
	// apart — `foo` is an `identifier` either way — which is the one place Ruby's
	// lexical capitalisation rule buys nothing.
	if def, ok := b.lookup(name, r.nameNode.StartByte()); ok {
		return b.occurrence(def.occ).Descriptor, facts.KindVariable
	}
	if s, ok := b.localDeclared(r.nameNode, []string{name}, "()."); ok {
		return facts.Descriptor{Prefix: b.coord, Suffix: s}, facts.KindMethod
	}
	if s, ok := b.selfSuffix(r.nameNode, true); ok {
		return facts.Descriptor{Prefix: b.coord, Suffix: s + name + "()."}, facts.KindMethod
	}
	return facts.Descriptor{Prefix: b.coord, Suffix: name + "()."}, facts.KindFunction
}

// constantReference names a constant written in an expression, which is the whole
// of Ruby's type-reference vocabulary: there are no annotations, so a superclass,
// a rescued exception, a `when` pattern, a mixin argument and the receiver of
// `Foo.new` are the only places a type is ever named.
func (b *builder) constantReference(r refRec, name string) (facts.Descriptor, string) {
	segs, term, absolute := b.referencePath(r, name)
	if len(segs) == 0 {
		return facts.Descriptor{Prefix: b.coord, Suffix: coord.Unknown + "#"}, facts.KindType
	}
	c, suffix := b.constantTarget(r.nameNode, segs, term, absolute)
	kind := facts.KindType
	if term == "/" {
		kind = facts.KindPackage
	}
	return facts.Descriptor{Prefix: c, Suffix: suffix}, kind
}

// referencePath decides which segments of a constant path the captured
// identifier names, how the last of them terminates, and whether the path is read
// from the root.
//
// A qualified path is absolute and an unqualified one is relative, which is the
// split cs.go draws for the same reason: `::` is written precisely because the
// constant is not the one the enclosing nesting would find, while a bare name is
// written because it is.
//
// The terminator is where the one thing Ruby's grammar cannot say shows up. A
// path segment before a `::` is a module or a class — Ruby permits nesting inside
// either — and the syntax does not say which. It is read as a module, because
// namespacing constants are modules in nearly all Ruby and because `imports`
// joins on the `package` kind, so the module reading is the one that derives an
// edge; a class used as a namespace is caught anyway when this file declares it
// (localDeclared), and otherwise yields a descriptor that matches nothing.
func (b *builder) referencePath(r refRec, name string) (segs []string, term string, absolute bool) {
	if r.node.Type(b.lang) != "scope_resolution" {
		term = "#"
		if b.isMixinArgument(r.node) {
			term = "/"
		}
		return []string{name}, term, false
	}

	all, _ := b.pathSegments(r.node)
	if len(all) == 0 {
		return nil, "#", true
	}
	if scope := r.node.ChildByFieldName("scope", b.lang); sameSpan(scope, r.nameNode) {
		// The qualifier half: a namespace, whole.
		return all[:1], "/", true
	}
	if b.isPathQualifier(r.node) {
		return all, "/", true
	}
	term = "#"
	if b.isMixinArgument(r.node) {
		term = "/"
	}
	return all, term, true
}

// constantTarget renders a constant path as a coordinate and a descriptor suffix.
func (b *builder) constantTarget(n *sitter.Node, segs []string, term string, absolute bool) (coord.Coord, string) {
	if s, ok := b.localDeclared(n, segs, term); ok {
		return b.coord, s
	}
	base := ""
	if !absolute {
		base = b.moduleSuffix(n)
	}
	return b.coord, base + joinPath(segs, term)
}

// localDeclared resolves a constant path against the definitions this file
// declares, innermost container first.
//
// It is what makes a nested class resolvable by its simple name, what keeps a
// receiverless send on the enclosing type, and what rescues the one reading
// referencePath had to guess at — a class used as a namespace is found here when
// the file declares it, and a module wrongly assumed is corrected the same way.
//
// Only names this file already defined, which is why it consults descIndex:
// collectDefinitions runs first, so every module, class, constant and callable
// declared here is in it, and a name that is not is not this file's to claim.
func (b *builder) localDeclared(n *sitter.Node, segs []string, term string) (string, bool) {
	try := func(prefix string) (string, bool) { return b.declaredFrom(prefix, segs, term) }
	for p := n; p != nil; p = p.Parent() {
		var component string
		switch p.Type(b.lang) {
		case "module":
			component = "/"
		case "class":
			component = "#"
		case "singleton_class":
			if s, ok := try(b.containerSuffix(p) + singletonComponent); ok {
				return s, true
			}
			continue
		case "method", "singleton_method":
			if name := b.memberName(p.ChildByFieldName("name", b.lang)); name != "" {
				sing := ""
				if p.Type(b.lang) == "singleton_method" {
					sing = singletonComponent
				}
				if s, ok := try(b.containerSuffix(p) + sing + name + "()."); ok {
					return s, true
				}
			}
			continue
		default:
			continue
		}
		if name := b.declaredPath(p); name != "" {
			if s, ok := try(b.containerSuffix(p) + name + component); ok {
				return s, true
			}
		}
	}
	return try("")
}

// declaredFrom walks a constant path one segment at a time against the
// definitions this file declares, starting at prefix.
//
// Segment at a time rather than as one string, because Ruby's syntax does not say
// whether an intermediate segment is a module or a class and the two render
// different terminators — `Registry::Entry` is `Registry#Entry#` when `Registry`
// is a class and `Registry/Entry#` when it is a module. Only the file that
// declares them knows, and this is where it is asked.
//
// Every segment but the last must be found. The last need not: a path whose
// namespace this file declares still names something once the namespace is
// pinned down, and that is the case `Registry::Missing` is — a reference to a
// member this file does not have, under a class it does.
func (b *builder) declaredFrom(prefix string, segs []string, term string) (string, bool) {
	suffix := prefix
	for i, seg := range segs {
		last := i == len(segs)-1
		want := "/"
		if last {
			want = term
		}
		found := false
		for _, t := range terminators(want) {
			cand := facts.Descriptor{Prefix: b.coord, Suffix: suffix + seg + t}
			if _, ok := b.descIndex[cand.String()]; ok {
				suffix, found = suffix+seg+t, true
				break
			}
		}
		if found {
			continue
		}
		if last && i > 0 {
			// The namespace resolved and the member did not, which is an ordinary
			// cross-file reference under a namespace this file happens to declare.
			return suffix + seg + term, true
		}
		return "", false
	}
	return suffix, true
}

// terminators lists the descriptor terminators a lookup will accept for a
// constant, most likely first. A constant path may end at a module or at a class
// and Ruby's syntax does not say which, so both are tried — and only for a
// constant: a callable's `().` has no alternative reading.
func terminators(term string) []string {
	switch term {
	case "#":
		return []string{"#", "/"}
	case "/":
		return []string{"/", "#"}
	default:
		return []string{term}
	}
}

// ------------------------------------------------------------ local lookup ---

// valueTarget resolves a call's receiver to the descriptor prefix its members
// hang off. It is the one place a type has to be recovered rather than read off
// the syntax, and it is recovered from an initialisation and never inferred.
func (b *builder) valueTarget(value *sitter.Node, pos uint32) (pathTarget, bool) {
	if value == nil {
		return pathTarget{}, false
	}
	switch value.Type(b.lang) {
	case "self":
		if s, ok := b.selfSuffix(value, true); ok {
			return pathTarget{coord: b.coord, suffix: s}, true
		}
	case "parenthesized_statements":
		return b.valueTarget(firstNamedChild(value), pos)
	case "constant", "scope_resolution":
		// The receiver is the class object itself, so what hangs off it is the
		// singleton member namespace and not the instance one.
		segs, _ := b.pathSegments(value)
		if len(segs) == 0 {
			return pathTarget{}, false
		}
		c, s := b.constantTarget(value, segs, "#", len(segs) > 1)
		return pathTarget{coord: c, suffix: s + singletonComponent}, true
	case "identifier":
		if def, ok := b.lookup(b.text(value), pos); ok && def.typeName != "" {
			return b.typeTarget(value, def.typeName)
		}
	case "instance_variable", "class_variable":
		if typeName, ok := b.ivarTypes[b.text(value)]; ok && typeName != "" {
			return b.typeTarget(value, typeName)
		}
	case "call":
		// `Greeter.new.greet` — the receiver is a construction rather than a
		// binding, and it is the same expression declaredType reads off an
		// assignment. Every other chained send stays unknown, for the reason
		// inferredType gives.
		if typeName := b.inferredType(value); typeName != "" {
			return b.typeTarget(value, typeName)
		}
	}
	return pathTarget{}, false
}

// typeTarget resolves a recorded constant path — the text of a `X.new` receiver —
// to the descriptor prefix its instance members hang off.
func (b *builder) typeTarget(n *sitter.Node, typeName string) (pathTarget, bool) {
	segs := splitConstantPath(typeName)
	if len(segs) == 0 {
		return pathTarget{}, false
	}
	c, s := b.constantTarget(n, segs, "#", len(segs) > 1)
	return pathTarget{coord: c, suffix: s}, true
}

// lookup finds the definition named name that is visible at byte offset pos:
// among definitions whose declaring scope contains pos, the one in the innermost
// such scope, declared no later than pos.
//
// "No later than pos" is Ruby's rule and not an approximation of it: a local
// exists from the point the parser sees an assignment to it, which is why
// `x = x` assigns nil and why a bare name before its assignment is a send rather
// than a variable.
func (b *builder) lookup(name string, pos uint32) (defRec, bool) {
	var best defRec
	var bestStart uint32
	bestEnd := ^uint32(0)
	found := false
	for _, d := range b.defsByName[name] {
		start, end, ok := b.scopeRange(d.scope)
		if !ok || pos < start || pos >= end || d.start > pos {
			continue
		}
		if !found || start > bestStart || (start == bestStart && end < bestEnd) {
			best, bestStart, bestEnd, found = d, start, end, true
		}
	}
	return best, found
}

// declaredType recovers the constant path a binding was initialised from — enough
// to name the members reached through it, without any type inference.
func (b *builder) declaredType(node *sitter.Node) string {
	switch node.Type(b.lang) {
	case "assignment", "operator_assignment":
		return b.inferredType(node.ChildByFieldName("right", b.lang))
	}
	return ""
}

// inferredType reads a type off an initialising expression, and only from the one
// expression whose type Ruby writes down: `C.new`. Ruby has no annotations and no
// `new` keyword, so a construction is a plain send and `C.new` is the only send
// whose result is named by its receiver.
//
// Everything else yields "", which downstream becomes SCIP's "." rather than a
// guess. `Foo.build`, `Foo.for(x)` and every other factory are deliberately not
// read: a name is not a contract, and a wrong answer would produce a descriptor
// naming a member of a type that does not have it.
func (b *builder) inferredType(expr *sitter.Node) string {
	for depth := 0; expr != nil && depth < 16; depth++ {
		switch expr.Type(b.lang) {
		case "parenthesized_statements":
			expr = firstNamedChild(expr)
		case "call":
			if b.fieldText(expr, "method") != "new" {
				return ""
			}
			receiver := expr.ChildByFieldName("receiver", b.lang)
			if receiver == nil {
				return ""
			}
			switch receiver.Type(b.lang) {
			case "constant", "scope_resolution":
				return b.text(receiver)
			}
			return ""
		default:
			return ""
		}
	}
	return ""
}

// ------------------------------------------------------------------- paths ---

// pathSegments flattens a constant path into its segments and the nodes covering
// each prefix of it. `A::B::C` yields ["A","B","C"] and the three nodes spanning
// `A`, `A::B` and `A::B::C`, which is what lets an occurrence be emitted over
// exactly the namespace half of a path.
//
// A leading `::` contributes no segment: `::Top` and `Top` name the same constant
// once the path is read from the root, and this stanza reads a qualified path
// from the root either way.
func (b *builder) pathSegments(n *sitter.Node) (segs []string, nodes []*sitter.Node) {
	if n == nil {
		return nil, nil
	}
	switch n.Type(b.lang) {
	case "scope_resolution":
		name := n.ChildByFieldName("name", b.lang)
		if name == nil {
			return nil, nil
		}
		scope := n.ChildByFieldName("scope", b.lang)
		if scope == nil {
			return []string{b.text(name)}, []*sitter.Node{n}
		}
		segs, nodes = b.pathSegments(scope)
		if segs == nil {
			return nil, nil
		}
		return append(segs, b.text(name)), append(nodes, n)
	case "constant":
		if text := b.text(n); text != "" {
			return []string{text}, []*sitter.Node{n}
		}
	}
	return nil, nil
}

// isPathQualifier reports whether a scope_resolution is itself the qualifier of a
// longer path, which is what makes `A::B` in `A::B::C` a namespace rather than a
// type.
func (b *builder) isPathQualifier(n *sitter.Node) bool {
	parent := n.Parent()
	if parent == nil || parent.Type(b.lang) != "scope_resolution" {
		return false
	}
	return sameSpan(parent.ChildByFieldName("scope", b.lang), n)
}

// isMixinArgument reports whether a constant path is the argument of an
// `include`, `extend` or `prepend`, which names a module and can name nothing
// else. It is the one place Ruby's syntax settles the module/class question that
// `::` leaves open, and it is why a mixin derives an `imports` edge.
func (b *builder) isMixinArgument(n *sitter.Node) bool {
	args := n.Parent()
	if args == nil || args.Type(b.lang) != "argument_list" {
		return false
	}
	call := args.Parent()
	if call == nil || call.Type(b.lang) != "call" || call.ChildByFieldName("receiver", b.lang) != nil {
		return false
	}
	switch b.fieldText(call, "method") {
	case "include", "extend", "prepend":
		return true
	}
	return false
}

// isDirective reports whether a receiverless call is one of the forms this stanza
// reads as a declaration rather than as a send.
func (b *builder) isDirective(call *sitter.Node) bool {
	if call.Type(b.lang) != "call" || call.ChildByFieldName("receiver", b.lang) != nil {
		return false
	}
	switch b.fieldText(call, "method") {
	case "require", "require_relative", "include", "extend", "prepend",
		"attr_reader", "attr_writer", "attr_accessor":
		return call.ChildByFieldName("arguments", b.lang) != nil
	}
	return false
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

// claimSubtree marks every identifier under n as owned, so a reference pattern
// does not emit a second occurrence over bytes a definition already describes.
// The recursion is what a `setter` needs: `def name=(v)` hangs an `identifier`
// spelling `name` under the `setter` node the definition covers, and the two have
// different ranges.
func (b *builder) claimSubtree(n *sitter.Node) {
	if n == nil {
		return
	}
	b.claimed[span{n.StartByte(), n.EndByte()}] = true
	for i := 0; i < n.NamedChildCount(); i++ {
		b.claimSubtree(n.NamedChild(i))
	}
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

// definitionName is the identifier a definition's name node spells. A `setter`
// spells `name=`, which is the member's real name and the one a reference
// reconstructs; an `operator` spells something no reference can name, so it
// yields "" and no definition is emitted for it.
func (b *builder) definitionName(n *sitter.Node) string {
	if n == nil || n.Type(b.lang) == "operator" {
		return ""
	}
	return b.text(n)
}

// memberName is what a callable is called for the purpose of *containing*
// something, which includes the operators no definition is emitted for. A
// parameter of `def <=>(other)` still lives inside that method, and giving the
// walk nothing to contribute would hang the parameter off the enclosing class as
// though the method were not there.
func (b *builder) memberName(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return b.text(n)
}

// declaredPath is the descriptor component a `module` or `class` declaration
// contributes. `class Foo::Bar` is legal and reopens `Bar` inside `Foo`, so its
// path is flattened to the namespace components it names.
func (b *builder) declaredPath(n *sitter.Node) string {
	name := n.ChildByFieldName("name", b.lang)
	if name == nil {
		return ""
	}
	segs, _ := b.pathSegments(name)
	if len(segs) == 0 {
		return ""
	}
	return strings.Join(segs, "/")
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

func sameSpan(a, b *sitter.Node) bool {
	return a != nil && b != nil && a.StartByte() == b.StartByte() && a.EndByte() == b.EndByte()
}

// isVariableSigil reports whether a node type is one of Ruby's sigilled variable
// forms, whose recorded type is file-wide rather than scoped.
func isVariableSigil(nodeType string) bool {
	return nodeType == "instance_variable" || nodeType == "class_variable"
}

// joinPath renders constant path segments as a descriptor suffix: every segment
// but the last is a namespace, and the last carries term.
func joinPath(segs []string, term string) string {
	out := ""
	for i, seg := range segs {
		if i == len(segs)-1 {
			out += seg + term
			continue
		}
		out += seg + "/"
	}
	return out
}

// splitConstantPath splits a Ruby constant path into its segments, dropping the
// leading `::` of a root-anchored one.
func splitConstantPath(p string) []string {
	p = strings.TrimPrefix(p, "::")
	if p == "" {
		return nil
	}
	return strings.Split(p, "::")
}

// repoRelative renders filePath relative to the package root, slash-separated.
// It is "" when there is no root or the file is outside it, which makes the load
// unit empty — the honest answer when nothing says where the tree begins.
func repoRelative(root, filePath string) string {
	if root == "" {
		return ""
	}
	rel, err := filepath.Rel(root, filePath)
	if err != nil {
		return ""
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || strings.HasPrefix(rel, "../") {
		return ""
	}
	return rel
}

// unitNamespace turns a repo-relative path into the SCIP namespace descriptor of
// the load unit it names: slash-separated and slash-terminated, with the `.rb`
// extension removed.
//
// The extension goes because a `require` never writes one — `require "a/b"` loads
// `a/b.rb` — so dropping it is what lets the two sides agree without either
// reading the disk.
func unitNamespace(p string) string {
	if p == "" {
		return ""
	}
	p = path.Clean(p)
	if p == "." || p == "/" {
		return ""
	}
	p = strings.TrimSuffix(p, Ext)
	if p == "" || p == "." {
		return ""
	}
	return p + "/"
}

func lastSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// rbStdlib is the set of top-level names a plain `require` may name that belong
// to Ruby itself rather than to this tree.
//
// It is what resolveRequire needs and what Ruby's syntax cannot supply:
// `require "json"` and `require "greeter/greeter"` are the same statement, and
// only `$LOAD_PATH` separates them. The set is the commonly required subset of
// the default and bundled gems; a feature missing from it is treated as this
// tree's, which yields a load unit no file defines — an unresolved reference
// rather than a wrong edge.
var rbStdlib = map[string]bool{
	"abbrev": true, "base64": true, "benchmark": true, "bigdecimal": true,
	"cgi": true, "coverage": true, "csv": true, "date": true, "delegate": true,
	"digest": true, "drb": true, "English": true, "erb": true, "etc": true,
	"expect": true, "fcntl": true, "fiddle": true, "fileutils": true,
	"find": true, "forwardable": true, "getoptlong": true, "io": true,
	"ipaddr": true, "irb": true, "json": true, "logger": true, "matrix": true,
	"minitest": true, "monitor": true, "mutex_m": true, "net": true,
	"nkf": true, "objspace": true, "observer": true, "open-uri": true,
	"open3": true, "openssl": true, "optparse": true, "ostruct": true,
	"pathname": true, "pp": true, "prettyprint": true, "prime": true,
	"pstore": true, "psych": true, "racc": true, "rdoc": true, "readline": true,
	"resolv": true, "rexml": true, "rinda": true, "ripper": true, "rss": true,
	"rubygems": true, "securerandom": true, "set": true, "shellwords": true,
	"singleton": true, "socket": true, "stringio": true, "strscan": true,
	"tempfile": true, "test": true, "time": true, "timeout": true, "tmpdir": true,
	"tsort": true, "un": true, "uri": true, "weakref": true, "yaml": true,
	"zlib": true,
}
