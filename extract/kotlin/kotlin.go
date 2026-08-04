// Package kotlin is the Kotlin stanza: the tree-sitter query in query.scm plus
// the mapper here that turns its captures into core facts (SPEC.md §5).
//
// The package is named for the language rather than for the extension, as
// `golang` is: `kt` reads as an abbreviation in an import path where the file
// tag it produces does not. It imports facts and
// coord and deliberately not extract: it satisfies extract.Parser structurally,
// which is what keeps the registry free of an import cycle.
//
// The mapper's job, and its limits, are every earlier stanza's (§4.3): it builds
// the descriptor *suffix* from the CST and the namespace the file declares; it
// assigns role and neutral-core symbol kind; and it resolves references whose
// target definition is in the same file. It does no type checking, runs no name
// resolution algorithm and looks at no other file. A reference it cannot pin down
// is still emitted, carrying the best descriptor syntax allows, and the link pass
// decides what it means (§7). Where a component is genuinely unknowable
// file-locally the descriptor writes SCIP's "." for it, so it names an unresolved
// symbol rather than false-matching a real one.
//
// # The unit of modularity
//
// Go's is the directory, TypeScript's the file, Python's the file with
// `__init__.py` collapsing to its directory, Rust's the `mod` tree, Java's the
// `package` clause. Kotlin's is Java's: `package com.example.greeter` at the top
// of the file, and the namespace is that name with its dots turned into slashes.
//
// The declaration is preferred over the path, always, and in Kotlin the two
// disagree *more* than they do in Java rather than less. Java at least
// conventionally mirrors the package in the directory tree below
// `src/main/java/`; Kotlin explicitly drops that convention — the official style
// guide tells you to omit the common root package from the directory layout — so
// `src/main/kotlin/greeter/Greeter.kt` declaring `package com.example.greeter` is
// the *recommended* shape rather than a mistake. A path-derived namespace would
// disagree with every `import` in the repository, and the two sides of an import
// have to derive the same namespace or no `imports` edge exists at all.
//
// What it cannot represent is Java's list, for Java's reasons: two source roots
// declaring one package collide, a multi-module Gradle build gives every module
// the root build's coordinate (`coord.Resolve` reads one directory's manifests),
// and a file with no `package` clause is in the root package and renders
// `Greeter#`, colliding with every other root-package `Greeter` exactly as
// kotlinc's own name resolution would.
//
// # A script is not a package
//
// `.kts` is the one place this stanza departs from the "namespace is the package
// clause" rule, and it has to. A Kotlin script is a compilation unit whose
// top-level declarations are members of a class the compiler synthesizes from the
// *file name* — `build.gradle.kts` becomes `Build_gradle` — so two scripts each
// declaring `val logger` declare two unrelated things. Treating them as package
// members would render one descriptor for both, and the link pass would join
// them: a phantom edge, which is worse than a missing one.
//
// So a script's declarations hang off a container named for its file. The name
// approximates kotlinc's mangling rather than reproducing it, and that is sound
// here for a reason specific to scripts: nothing outside a script can reference
// its top-level declarations, so the component has to *separate*, not to be
// reconstructible by another file.
//
// # What one dot hides
//
// Kotlin inherits Java's ambiguity — `a.b` is a member of a value, a member of a
// type, or a segment of a package name, with nothing in the syntax to say which —
// and the mapper resolves it the same way: by what the left half resolves to, a
// binding in scope, an imported name, a platform root, or none of those. The
// split between the package part and the type part of a dotted name is Kotlin's
// naming convention and not its grammar (lowercase segments are the package, the
// first uppercase one begins the type), and being wrong costs a descriptor that
// matches nothing rather than one that matches the wrong thing.
//
// One thing is genuinely Kotlin's own. Java can only import a type, so a dotted
// path ending in a lowercase segment is a package; Kotlin has top-level
// declarations, so `import kotlin.math.max` ends in a *function*. splitImport
// says so, and it is the difference between binding `max` and inventing a package
// called `max`.
//
// # What is inferred, and the one thing that is
//
// `val g = Greeter()` is the idiomatic spelling and it writes no type, so a
// stanza that only read declared types would resolve nothing through `g`. This
// one reads the initialiser in exactly one shape: a call whose callee is an
// uppercase name is a constructor invocation, and Kotlin has no `new` to mark it
// with, so the callee *is* the type. That is syntax and not inference — the
// program says `Greeter` — and it is the whole of what is read back. `val x =
// compute()` yields no type, and its members land on ".".
//
// The same fact is why constructors are not definitions here. Kotlin never names
// one, so there is no identifier to build a component from; a constructor call
// resolves to the type's own descriptor, which is a definition that exists.
//
// # What an overload hides
//
// Kotlin overloads on the parameter list as Java does, and the descriptor's
// callable component carries the name and not the signature, so `greet(String)`
// and `greet(Int)` both render `greet().` and collide. java.go argues the case at
// length and the argument is unchanged: the definition side could number its
// overloads from one file's CST and the reference side could not, and a
// descriptor only one side can compute is worse than a coarse one both sides
// compute the same way.
//
// Kotlin adds one shape Java has no counterpart for: an extension function.
// `fun String.shout()` is declared here, called on a `String`, and belongs to
// neither — it is a static function of the file's package that takes a receiver.
// It is descriptored where it is declared, which is what makes its *definition*
// findable; a call to it through a `String` resolves to the receiver's type and
// therefore does not join it. That is a knowing gap rather than an oversight: the
// alternative is to descriptor every extension under the type it extends, which
// would put a definition this repository owns inside a namespace it does not.
package kotlin

import (
	_ "embed"
	"errors"
	"fmt"
	"path/filepath"
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

// Lang is the value written to file.lang for the files this stanza handles. It
// is the tag SPEC.md §14 M9+ names for this task and the extension without its
// dot, which is what every language here but C/C++ carries — and C/C++ departed
// only because two languages shared one stanza.
//
// A `.kts` carries it too. A script is Kotlin, and tagging it apart would make a
// Gradle build's own files a second language in every Kotlin repository indexed —
// and would turn any edge between a script and a source into a cross-language
// edge, which is the invariant test/integration/m6_test.go's noCrossLanguageEdges
// asserts.
const Lang = "kt"

// Exts are the file extensions this stanza is registered under. A script is
// Kotlin and is read with the same grammar; what separates it from a source file
// is where its declarations hang, not whether they are read. They repeat
// coord.KotlinExts, for the reason coord.GoExt gives.
var Exts = []string{".kt", ".kts"}

// scriptExt is the extension that makes a file a script rather than a
// compilation unit of its package.
const scriptExt = ".kts"

//go:embed query.scm
var queryScheme string

// Parser is the Kotlin stanza. Safe for concurrent use: the grammar and compiled
// query are immutable after the first Parse, and each parse checks a
// gotreesitter parser out of a pool.
type Parser struct {
	once    sync.Once
	lang    *sitter.Language
	pool    *sitter.ParserPool
	query   *sitter.Query
	initErr error
}

// New returns the Kotlin parser. It is cheap: the grammar is loaded and query.scm
// compiled on the first Parse, so a binary that never parses Kotlin never
// decompresses the Kotlin grammar — and a query that fails to compile lands in
// ParseError rather than panicking at init.
func New() *Parser { return &Parser{} }

func (p *Parser) init() {
	p.once.Do(func() {
		p.lang = grammars.KotlinLanguage()
		if p.lang == nil {
			p.initErr = errors.New("kotlin: gotreesitter has no Kotlin grammar")
			return
		}
		q, err := sitter.NewQuery(queryScheme, p.lang)
		if err != nil {
			p.initErr = fmt.Errorf("kotlin: compile query.scm: %w", err)
			return
		}
		p.query = q
		p.pool = sitter.NewParserPool(p.lang)
	})
}

// Parse extracts one Kotlin file's facts. It never returns an error: a failure is
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
		script:     scriptContainer(filePath),
		out:        facts.FileFacts{File: file},
		scopeByID:  map[facts.LocalID]scopeRec{},
		types:      map[string]pathTarget{},
		members:    map[string]memberTarget{},
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
	typeName string // syntactic type of a binding/parameter/property; "" when unknown
}

// pathTarget is what a dotted path resolved to: the coordinate and descriptor
// suffix its members hang off.
type pathTarget struct {
	coord  coord.Coord
	suffix string
}

// memberTarget is what an import bound to a single declaration: the container it
// lives in, plus the name it is declared under — which is not always the name it
// was bound to, since `import kotlin.math.max as maxOf` renames it. The
// terminator is missing on purpose: only the use site knows whether the
// declaration is called or read.
type memberTarget struct {
	pathTarget
	name string
}

type builder struct {
	lang  *sitter.Language
	src   []byte
	coord coord.Coord
	// ns is this file's package namespace, read off the `package` clause: the
	// descriptor prefix of everything it declares. "" for the root package.
	ns string
	// script is the descriptor component a `.kts` file's declarations hang off,
	// or "" for an ordinary source file. See the package comment.
	script string

	out       facts.FileFacts
	nextScope facts.LocalID
	nextOcc   facts.LocalID

	scopes    []scopeRec
	scopeByID map[facts.LocalID]scopeRec

	// types holds simple names an import bound to a type (`import a.b.C` binds
	// `C`); members holds names bound to one declaration of a package or a type
	// (`import kotlin.math.max` binds `max`).
	types   map[string]pathTarget
	members map[string]memberTarget

	// claimed holds identifier ranges a definition or an import already owns, so
	// a reference pattern matching the same identifier is dropped.
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
// Nesting is pure byte containment: sorted by (start ascending, end descending),
// a stack of open scopes yields each scope's parent with no language knowledge at
// all.
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
// collectScopes sorts by (start ascending, end descending) and the `source_file`
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
// `(package_header (identifier) @name)` when the file has a `package` clause, and
// `(source_file)` when it does not.
//
// The descriptor is the namespace and not the script container, even for a
// `.kts`: a script *is* in a package (the root one, unless it says otherwise),
// and what the script container separates is the declarations inside it.
func (b *builder) collectPackage(matches []sitter.QueryMatch) {
	var decl *sitter.Node
	found := false
	for _, m := range matches {
		root, name, ok := roots(m, "definition.package")
		if !ok {
			continue
		}
		found = true
		if root.Node.Type(b.lang) == "package_header" && name != nil {
			decl = name.Node
		}
	}
	if !found {
		return
	}

	start, end := uint32(0), uint32(0)
	if decl != nil {
		b.ns = namespaceOf(b.pathSegments(decl))
		start, end = decl.StartByte(), decl.EndByte()
		b.claimSubtree(decl)
	}

	desc := facts.Descriptor{Prefix: b.coord, Suffix: b.ns}
	// Explicitly the file scope: the package clause is the first thing in the
	// file and the innermost scope containing a zero-width point at byte 0 would
	// still be the file, but saying so keeps the root-package case honest.
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
// Every identifier the statement consumed is claimed, so the reference patterns —
// which match happily inside an import — do not emit a second, weaker occurrence
// over the same bytes.
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
		b.claimSubtree(cd.stmt)
		b.importDeclaration(cd.stmt, cd.name)
	}
}

// importDeclaration resolves one import and records what it binds.
//
// A wildcard import binds nothing nameable, which is the on-demand form's whole
// problem: the names it brings into scope are the *other* file's to state. It
// still names a package, so the occurrence is emitted and the `imports` edge
// derives from it exactly as a specific import's does.
func (b *builder) importDeclaration(stmt, path *sitter.Node) {
	nodes := namedChildrenOfType(path, "simple_identifier", b.lang)
	segs := b.textsOf(nodes)
	if len(segs) == 0 {
		return
	}

	if hasChildOfType(stmt, "wildcard_import", b.lang) {
		c, ns := b.namespaceTarget(segs)
		b.addOccurrence(
			facts.Descriptor{Prefix: c, Suffix: ns},
			facts.RoleReference, facts.KindPackage, segs[len(segs)-1],
			nodes[len(nodes)-1].StartByte(), nodes[len(nodes)-1].EndByte(),
		)
		return
	}

	pkg, types, members := splitImport(segs)

	// The package occurrence, over the last segment of the package name. It is
	// the row link's `imports` derivation joins against.
	c, ns := b.namespaceTarget(pkg)
	if len(pkg) > 0 {
		b.addOccurrence(
			facts.Descriptor{Prefix: c, Suffix: ns},
			facts.RoleReference, facts.KindPackage, pkg[len(pkg)-1],
			nodes[len(pkg)-1].StartByte(), nodes[len(pkg)-1].EndByte(),
		)
	}

	// The type the path names, and the occurrence that makes the import itself
	// navigable to the class it imports.
	target := pathTarget{coord: c, suffix: ns}
	for _, t := range types {
		target = pathTarget{coord: target.coord, suffix: target.suffix + t + "#"}
	}
	if len(types) > 0 {
		typeEnd := len(pkg) + len(types) - 1
		b.addOccurrence(
			facts.Descriptor{Prefix: target.coord, Suffix: target.suffix},
			facts.RoleReference, facts.KindType, types[len(types)-1],
			nodes[typeEnd].StartByte(), nodes[typeEnd].EndByte(),
		)
	}

	// What the statement brought into scope, under the name it brought it in as.
	// A member's own descriptor needs a terminator only the use site can choose,
	// so the container and the declared name are what is recorded.
	local := segs[len(segs)-1]
	if alias := b.importAlias(stmt); alias != "" {
		local = alias
	}
	switch {
	case len(members) > 0:
		b.members[local] = memberTarget{pathTarget: target, name: members[len(members)-1]}
	case len(types) > 0:
		b.types[local] = target
	}
}

// importAlias returns the name an `import … as Name` bound the declaration to.
// The grammar spells the alias as a `type_identifier` whatever the declaration
// is, so a renamed function arrives here looking like a type; only the text is
// read, so it does not matter.
func (b *builder) importAlias(stmt *sitter.Node) string {
	alias := firstChildOfType(stmt, "import_alias", b.lang)
	if alias == nil {
		return ""
	}
	return b.text(firstNamedChild(alias))
}

// ------------------------------------------------------------------- paths ---

// splitPath divides a dotted name into its package, type and member parts on
// Kotlin's naming convention: lowercase segments are the package, the first
// uppercase one begins the type, and a lowercase segment after a type is a member
// of it.
//
// The convention is doing real work here and the grammar cannot. `a.b.C.D` names
// a nested type in package `a.b`; `a.b.c.D` names a top-level type in package
// `a.b.c`; and the two parse to the same shape. Kotlin's own convention —
// packages lowercase, types UpperCamelCase — is the coding-conventions document's
// and is followed near-universally. Being wrong costs a descriptor that matches
// nothing, never one that matches something else.
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

// splitImport is splitPath for the one context where an all-lowercase path does
// not name a package: an import.
//
// Java can only import a type, so `import a.b.c;` is a package and a compile
// error. Kotlin imports *declarations* — `import kotlin.math.max` names a
// top-level function, `import com.example.VERSION` a top-level property — so a
// path with no uppercase segment ends in a member of the package before it. Not
// applying this would bind nothing and invent a package called `max`.
func splitImport(segs []string) (pkg, types, members []string) {
	pkg, types, members = splitPath(segs)
	if len(types) == 0 && len(members) == 0 && len(pkg) > 1 {
		return pkg[:len(pkg)-1], nil, pkg[len(pkg)-1:]
	}
	return pkg, types, members
}

// namespaceTarget renders a package path as a coordinate and namespace
// descriptor.
//
// The first segment decides the coordinate, and there is no third case beyond "a
// package of the platform" and "a package of whatever this repository is". Kotlin
// package names carry no artifact identity at all — `com.example.util` says
// nothing about which jar it comes from — so the rule is "the platform if the
// root says so, this coordinate otherwise", and a third-party dependency lands at
// a namespace of this coordinate that holds no definitions, which is an
// unresolved reference and not a wrong edge. This is java.go's problem verbatim,
// with a longer list of roots because Kotlin's platform is the Kotlin standard
// library *and* the JDK it runs on.
func (b *builder) namespaceTarget(pkg []string) (coord.Coord, string) {
	if len(pkg) == 0 {
		return b.coord, ""
	}
	if platformRoots[pkg[0]] {
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
		kind := b.refineKind(cd.kind, cd.node, cd.name)
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
			typeName: b.declaredType(cd.name),
		})
	}
}

// refineKind narrows a capture's kind where the CST carries a distinction the
// capture name does not. Kotlin needs it in three places, and all three exist
// because the grammar spells with one node type what other languages spell with
// several.
func (b *builder) refineKind(kind string, node, name *sitter.Node) string {
	switch kind {
	case facts.KindType:
		// `class`, `interface`, `enum class` and `annotation class` are all
		// `class_declaration`; only the keyword says which, and link keys
		// `implements` off the interface kind.
		if node.Type(b.lang) == "class_declaration" && hasKeyword(node, "interface", b.lang) {
			return facts.KindInterface
		}
	case facts.KindFunction:
		// Kotlin has free functions and members and one node type for both.
		if b.enclosingType(name) != nil {
			return facts.KindMethod
		}
	case facts.KindVariable:
		if node.Type(b.lang) == "property_declaration" {
			switch {
			case hasModifier(node, "const", b.lang):
				return facts.KindConstant
			case isTypeBody(node.Parent(), b.lang):
				return facts.KindField
			}
		}
	case facts.KindParameter:
		// `class Person(val name: String)` writes a property where a parameter is
		// written: `val`/`var` is what makes it state rather than an argument.
		if node.Type(b.lang) == "class_parameter" && hasChildOfType(node, "binding_pattern_kind", b.lang) {
			return facts.KindField
		}
	}
	return kind
}

// definitionDescriptor builds the SCIP descriptor for a definition from its
// capture hierarchy (SPEC.md §5).
//
// The prefix is a return value for symmetry with the other stanzas; a Kotlin
// declaration is always written inside what owns it, so it is always this file's
// coordinate's.
//
// The *declaration* is what the container is computed from and not the name it
// declares: containerSuffix starts at its argument's parent, and a name's parent
// is the very declaration it names — which would give a class `Greeter#Greeter#`.
func (b *builder) definitionDescriptor(kind string, decl *sitter.Node, text string) (coord.Coord, string) {
	c, container := b.containerSuffix(decl)
	switch kind {
	case facts.KindFunction, facts.KindMethod:
		return c, container + text + "()."
	case facts.KindType, facts.KindInterface:
		return c, container + text + "#"
	case facts.KindParameter:
		return c, container + "(" + text + ")"
	case facts.KindPackage:
		return c, container + text + "/"
	default: // field, variable, constant
		return c, container + text + "."
	}
}

// containerSuffix returns the coordinate and descriptor suffix of the nearest
// enclosing named container of n — a type or a function — or this file's own base
// when there is none. A block, a body, a lambda, an `init` and a property
// accessor are transparent: none has a name, so none has a descriptor of its own
// and what is inside belongs to the enclosing container.
//
// Nested and inner classes need no special case: each named container on the way
// out contributes exactly one component, so `Outer#Inner#` and `Outer#run().Local#`
// fall out of the same walk that produces `Greeter#greet().`.
//
// A companion object is **transparent**, and that is a knowing departure from
// the language rather than a simplification. Kotlin reaches a companion's members
// through the class — `Greeter.create()`, `Greeter.DEFAULT` — and only rarely
// through `Greeter.Companion.create()`, so a `Companion#` component would be a
// component the *definition* side can compute and the *reference* side cannot:
// every cross-file call to a factory method in a Kotlin codebase would fail to
// join. That is java.go's overload argument with the answer it gives — a
// descriptor both sides compute the same way beats a precise one only one side
// can — and cc.go's `extern "C"` decision in another language's spelling. What it
// costs is a member of a class and a member of its companion sharing one
// descriptor when they share a name, which is legal Kotlin and vanishingly rare.
func (b *builder) containerSuffix(n *sitter.Node) (coord.Coord, string) {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "class_declaration", "object_declaration":
			if name := b.typeName(p); name != "" {
				c, s := b.containerSuffix(p)
				return c, s + name + "#"
			}
		case "function_declaration":
			if name := b.functionName(p); name != "" {
				c, s := b.containerSuffix(p)
				return c, s + name + "()."
			}
		}
	}
	return b.coord, b.base()
}

// base is the descriptor suffix everything this file declares hangs off: the
// package namespace, plus the script container for a `.kts`.
func (b *builder) base() string { return b.ns + b.script }

// enclosingType returns the type declaration n sits innermost inside, or nil when
// n is at the file's top level. It is what resolves `this` and what tells a free
// function from a method.
//
// A companion object is walked through rather than returned, for containerSuffix's
// reason: its members are the class's, and `this` inside it names the class.
func (b *builder) enclosingType(n *sitter.Node) *sitter.Node {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "class_declaration", "object_declaration":
			return p
		}
	}
	return nil
}

// thisTarget is the coordinate and suffix `this` names.
//
// Usually the type the expression is written inside — but not always, and the
// exception is Kotlin's alone. Inside `fun String.shout()`, `this` is the
// *receiver*: an extension function is written outside the type it extends, so
// the enclosing type is either something else or nothing at all. The receiver is
// declared right there in the signature, so it is read rather than inferred, and
// a receiver_type met on the way out wins over any type further out.
func (b *builder) thisTarget(n *sitter.Node) (pathTarget, bool) {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Type(b.lang) != "function_declaration" {
			continue
		}
		recv := firstChildOfType(p, "receiver_type", b.lang)
		if recv == nil {
			continue
		}
		if name := b.unwrapType(recv); name != "" {
			c, s := b.resolveTypeName(p, name)
			return pathTarget{coord: c, suffix: s}, true
		}
	}

	decl := b.enclosingType(n)
	if decl == nil {
		return pathTarget{}, false
	}
	name := b.typeName(decl)
	if name == "" {
		return pathTarget{}, false
	}
	c, s := b.containerSuffix(decl)
	return pathTarget{coord: c, suffix: s + name + "#"}, true
}

// superTarget is what `super` names: the first supertype the enclosing class
// declares. Kotlin writes the supertype list after a `:` and does not say which
// entry is the class and which are interfaces — only a resolver with the other
// files could — so the first is taken, which is where Kotlin requires the
// superclass to be written when there is one.
//
// A class with no supertype list extends `kotlin.Any`, which is the platform's
// and is named as such rather than left unknown.
func (b *builder) superTarget(n *sitter.Node) (pathTarget, bool) {
	decl := b.enclosingType(n)
	if decl == nil {
		return pathTarget{}, false
	}
	if spec := firstChildOfType(decl, "delegation_specifier", b.lang); spec != nil {
		if name := b.unwrapType(delegationType(spec, b.lang)); name != "" {
			c, s := b.resolveTypeName(n, name)
			return pathTarget{coord: c, suffix: s}, true
		}
	}
	return pathTarget{coord: b.builtin(), suffix: "Any#"}, true
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

// moreSpecific decides which of two references over the same identifier survives.
// Role decides it where the roles differ; where they do not, the wider structural
// node wins, because the wider node is the one that saw the qualifier and
// therefore knows strictly more about the identifier than the narrower match
// does. That is what makes the bare `(simple_identifier)` pattern safe: it always
// loses to a navigation that covers it.
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
		if nameNode.Type(b.lang) == "user_type" {
			// The bare `(user_type)` pattern names its own last segment; the
			// descriptor is built from the whole path.
			last := lastChildOfType(nameNode, "type_identifier", b.lang)
			if last == nil {
				continue
			}
			nameNode = last
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
		if name == backingField && b.inAccessor(r.nameNode) {
			// `field` inside a getter or a setter is the property's backing field,
			// which is a compiler-synthesized binding with no declaration
			// anywhere. Emitting it would invent a symbol called `field` in this
			// container, one per accessor.
			continue
		}
		desc, kind := b.referenceDescriptor(r, name)
		occ := b.addOccurrence(desc, facts.RoleReference, kind, name, s.start, s.end)

		// Same-file resolution (§4.3): the target definition is in this CST, so
		// the edge is extracted rather than left to the link pass. The match is on
		// the descriptor string, exactly as link's cross-file join is.
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

// callDescriptor names the target of a call.
//
// Three shapes, and the first is the one with no counterpart in Java. Kotlin has
// no `new`, so `Greeter("x")` is a call whose callee is a *type* — the
// constructor is unnamed and unnameable — and the honest descriptor is the type's
// own, which is a definition that exists and which a caller in another file
// renders identically.
//
// A bare `f()` is a member of the enclosing type, a top-level function of this
// file's package, or an imported declaration. A receiver call `g.f()` is a member
// of whatever `g` resolves to, read off a declaration and never inferred. Where
// the receiver is unknowable file-locally the type component is SCIP's ".", so
// the descriptor cannot false-match a real definition.
func (b *builder) callDescriptor(r refRec, name string) (facts.Descriptor, string) {
	receiver := b.callReceiver(r)
	if receiver == nil {
		if isTypeName(name) {
			// A constructor invocation, or an `object`'s `invoke`. Either way the
			// name is a type and the type is what it resolves to.
			c, s := b.resolveTypeName(r.nameNode, name)
			return facts.Descriptor{Prefix: c, Suffix: s}, facts.KindType
		}
		if t, ok := b.members[name]; ok {
			return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + t.name + "()."}, facts.KindMethod
		}
		return b.unqualifiedCall(r.nameNode, name)
	}
	if t, ok := b.valueTarget(receiver, r.nameNode.StartByte()); ok {
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "()."}, facts.KindMethod
	}
	return facts.Descriptor{Prefix: b.coord, Suffix: b.base() + coord.Unknown + "#" + name + "()."}, facts.KindMethod
}

// unqualifiedCall names a call written with no receiver, which in Kotlin is
// three different things: a member of the enclosing type, a member of that type's
// companion object, and a top-level function of this file's package.
//
// The enclosing type wins, as it does in Java — but only if this file actually
// declares that member, and that qualification is what Kotlin needs and Java does
// not: Kotlin has top-level functions, so `println(x)` written inside a method is
// not a method. The question is answerable file-locally for the reason localType's
// is: a Kotlin class is written in one file, so a member the enclosing type has is
// a member collectDefinitions has already indexed.
//
// The last resort is the package-level form rather than the member one, and the
// choice matters. A bare call the enclosing type does not declare is either a
// top-level function — possibly one declared in another file of this package,
// which `pkg/name().` joins — or a method inherited from a supertype, whose
// descriptor carries the *supertype's* name and which therefore joins nothing
// whichever form is written. One of the two can resolve; the other cannot.
func (b *builder) unqualifiedCall(n *sitter.Node, name string) (facts.Descriptor, string) {
	if t, ok := b.thisTarget(n); ok {
		member := facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "()."}
		if _, ok := b.descIndex[member.String()]; ok {
			return member, facts.KindMethod
		}
	}
	if d, ok := b.enclosingMember(n, name, "()."); ok {
		return d, facts.KindMethod
	}
	top := facts.Descriptor{Prefix: b.coord, Suffix: b.base() + name + "()."}
	if _, ok := b.descIndex[top.String()]; ok {
		return top, facts.KindFunction
	}
	if def, ok := b.lookup(name, n.StartByte()); ok {
		// `block()` where `block` is a parameter is `block.invoke()`: a call
		// written on a *value* rather than on a function. The value is what it
		// names, and a function of the same name would have won above — which is
		// also kotlinc's order.
		return b.occurrence(def.occ).Descriptor, b.occurrence(def.occ).SymbolKind
	}
	return top, facts.KindFunction
}

// enclosingMember resolves a bare name against the members of every type it is
// written inside, innermost first.
//
// It exists for the two shapes the scope tree cannot express. A companion's
// members are in scope throughout the class body, but the companion is a
// *sibling* region of the code reading them, so lookup — which walks enclosing
// scopes — never reaches it. And an inner class's code sees the outer class's
// members, which is two type scopes out rather than one.
//
// Consulting descIndex is sound for localType's reason: a Kotlin class is written
// in one file, so a member this could resolve against is one collectDefinitions
// has already indexed.
func (b *builder) enclosingMember(n *sitter.Node, name, terminator string) (facts.Descriptor, bool) {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "class_declaration", "object_declaration":
		default:
			continue
		}
		own := b.typeName(p)
		if own == "" {
			continue
		}
		c, s := b.containerSuffix(p)
		d := facts.Descriptor{Prefix: c, Suffix: s + own + "#" + name + terminator}
		if _, ok := b.descIndex[d.String()]; ok {
			return d, true
		}
	}
	return facts.Descriptor{}, false
}

// callReceiver returns the expression a call was written on, or nil for a bare
// call. `g.greet()` parses as a call whose callee is a navigation, so the
// receiver is the navigation's left half.
func (b *builder) callReceiver(r refRec) *sitter.Node {
	callee := firstNamedChild(r.node)
	if callee == nil || callee.Type(b.lang) != "navigation_expression" {
		return nil
	}
	return firstNamedChild(callee)
}

func (b *builder) readDescriptor(r refRec, name string) (facts.Descriptor, string) {
	switch r.node.Type(b.lang) {
	case "navigation_expression":
		// The left half of `a.b`, which names a value, a type or a package.
		return b.qualifierDescriptor(r, name)
	case "directly_assignable_expression":
		// `a.b = x` — the same left half, one node type over. A bare `a = x`
		// wears the same node and is not a qualifier at all, so the suffix is
		// what is asked rather than the node.
		if hasChildOfType(r.node, "navigation_suffix", b.lang) {
			return b.qualifierDescriptor(r, name)
		}
	case "navigation_suffix":
		// The right half of `a.b`: a member of whatever the left half is.
		return b.memberDescriptor(r, name)
	}

	// A bare identifier used as a value.
	if def, ok := b.lookup(name, r.nameNode.StartByte()); ok {
		return b.occurrence(def.occ).Descriptor, b.occurrence(def.occ).SymbolKind
	}
	if b.isImplicitIt(r.nameNode, name) {
		return b.unknownValue(), facts.KindVariable
	}
	if t, ok := b.members[name]; ok {
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + t.name + "."}, facts.KindField
	}
	if d, ok := b.enclosingMember(r.nameNode, name, "."); ok {
		return d, facts.KindField
	}
	if isTypeName(name) {
		c, s := b.resolveTypeName(r.nameNode, name)
		return facts.Descriptor{Prefix: c, Suffix: s}, facts.KindType
	}
	// A top-level property of this file's package, which is what a lowercase
	// name that resolves to no binding in scope is in Kotlin. It renders a
	// descriptor that matches only if such a property is really declared.
	return facts.Descriptor{Prefix: b.coord, Suffix: b.base() + name + "."}, facts.KindVariable
}

// memberDescriptor names the right half of a `.`.
func (b *builder) memberDescriptor(r refRec, name string) (facts.Descriptor, string) {
	receiver := firstNamedChild(r.node.Parent())
	if t, ok := b.valueTarget(receiver, r.nameNode.StartByte()); ok {
		if isNestedTypeName(name) {
			// `Shape.Empty` — an object or a nested class reached through the type
			// that holds it, which Kotlin writes exactly as it writes a member
			// read. The two are told apart by the one convention that separates
			// them: a nested type is UpperCamelCase and a constant is ALL_CAPS, so
			// `Color.RED` stays a field and `Shape.Empty` becomes the type it is.
			return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "#"}, facts.KindType
		}
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "."}, facts.KindField
	}
	if t, ok := b.packageTarget(receiver); ok {
		// `a.b.C` read as an expression: the segment after a package is a type.
		kind := facts.KindPackage
		suffix := t.suffix + name + "/"
		if isTypeName(name) {
			kind, suffix = facts.KindType, t.suffix+name+"#"
		}
		return facts.Descriptor{Prefix: t.coord, Suffix: suffix}, kind
	}
	return facts.Descriptor{Prefix: b.coord, Suffix: b.base() + coord.Unknown + "#" + name + "."}, facts.KindField
}

// qualifierDescriptor names the left half of a `.`, which is the one place
// Kotlin's single member operator has to be disambiguated rather than read.
func (b *builder) qualifierDescriptor(r refRec, name string) (facts.Descriptor, string) {
	// A binding in scope wins: a local named `list` shadows a package named
	// `list`, which is also what kotlinc does.
	if def, ok := b.lookup(name, r.nameNode.StartByte()); ok {
		return b.occurrence(def.occ).Descriptor, b.occurrence(def.occ).SymbolKind
	}
	if b.isImplicitIt(r.nameNode, name) {
		return b.unknownValue(), facts.KindVariable
	}
	if t, ok := b.types[name]; ok {
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix}, facts.KindType
	}
	if isTypeName(name) {
		c, s := b.resolveTypeName(r.nameNode, name)
		return facts.Descriptor{Prefix: c, Suffix: s}, facts.KindType
	}
	// A lowercase leading segment is the root of a fully qualified name, which in
	// Kotlin is always absolute — there are no relative package references.
	c, ns := b.namespaceTarget([]string{name})
	return facts.Descriptor{Prefix: c, Suffix: ns}, facts.KindPackage
}

// typeReferenceDescriptor names a type reference. The captured node is the whole
// `user_type`, so a qualified name is resolved as a path and a simple one against
// what this file can see.
func (b *builder) typeReferenceDescriptor(r refRec, name string) (facts.Descriptor, string) {
	segs := b.textsOf(namedChildrenOfType(r.node, "type_identifier", b.lang))
	if len(segs) > 1 {
		pkg, types, _ := splitPath(segs)
		c, ns := b.typePath(r.nameNode, pkg, types)
		kind := facts.KindType
		if len(types) == 0 {
			kind = facts.KindPackage
		}
		return facts.Descriptor{Prefix: c, Suffix: ns}, kind
	}
	c, suffix := b.resolveTypeName(r.nameNode, name)
	return facts.Descriptor{Prefix: c, Suffix: suffix}, facts.KindType
}

// typePath renders a dotted name already split into its package and type halves.
//
// A path with no package half is the case that needs saying: `Shape.Circle` names
// a nested type of something in *scope* rather than a top-level `Shape` of no
// package at all, so its first segment is resolved the way a bare type name is —
// against the imports, this file's own declarations and the standard library —
// and the rest are appended to whatever that turned out to be.
func (b *builder) typePath(n *sitter.Node, pkg, types []string) (coord.Coord, string) {
	if len(pkg) == 0 && len(types) > 0 {
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

// resolveTypeName names a type by the coordinate and descriptor suffix it lives
// at. The name may be dotted, when it came from a declaration this stanza read
// the text of rather than the CST.
//
// The order is kotlinc's own, minus the classpath: an import wins, then a type
// declared in this file (which is what makes a nested type resolvable by its
// simple name), then the implicitly imported standard library, then this file's
// own package. The last is Kotlin's implicit same-package visibility and is why a
// two-file package needs no import at all.
func (b *builder) resolveTypeName(n *sitter.Node, typeName string) (coord.Coord, string) {
	if strings.Contains(typeName, ".") {
		pkg, types, _ := splitPath(strings.Split(typeName, "."))
		if len(pkg) > 0 {
			return b.typePath(n, pkg, types)
		}
		// Nested, and resolving it here would recurse on the same function: the
		// outermost segment is the one to resolve, and the rest hang off it.
		c, s := b.resolveTypeName(n, types[0])
		for _, t := range types[1:] {
			s += t + "#"
		}
		return c, s
	}
	if t, ok := b.types[typeName]; ok {
		return t.coord, t.suffix
	}
	if c, s, ok := b.localType(n, typeName); ok {
		return c, s
	}
	if ns, ok := stdlibTypes[typeName]; ok {
		return b.builtin(), ns + typeName + "#"
	}
	return b.coord, b.base() + typeName + "#"
}

// localType resolves a simple type name against the types this file declares,
// innermost container first. It is what makes `Inner` written inside `Outer` name
// `Outer#Inner#` rather than the package-level `Inner#` that does not exist.
//
// Only names this file already defined, which is why it consults descIndex:
// collectDefinitions runs first, so every type declared here is in it, and a name
// that is not is not this file's to claim.
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
		case "class_declaration", "object_declaration":
			component = "#"
		case "function_declaration":
			component = "()."
		default:
			continue
		}
		c, s := b.containerSuffix(p)
		own := b.typeName(p)
		if own == "" {
			own = b.functionName(p)
		}
		if own != "" {
			s += own + component
		}
		if c, s, ok := try(c, s); ok {
			return c, s, true
		}
	}
	return try(b.coord, b.base())
}

// ------------------------------------------------------------ local lookup ---

// valueTarget resolves an expression to the type its members hang off. It is the
// one place a *type* has to be recovered rather than read off the syntax, and it
// is recovered from a declaration or from a constructor call, never inferred.
func (b *builder) valueTarget(value *sitter.Node, pos uint32) (pathTarget, bool) {
	if value == nil {
		return pathTarget{}, false
	}
	switch value.Type(b.lang) {
	case "this_expression":
		return b.thisTarget(value)
	case "super_expression":
		return b.superTarget(value)
	case "parenthesized_expression":
		return b.valueTarget(firstNamedChild(value), pos)
	case "call_expression":
		// `Greeter("x").greet()` — the receiver's type is written right there,
		// which makes it the one expression form worth reading back.
		if callee := firstNamedChild(value); callee != nil && callee.Type(b.lang) == "simple_identifier" {
			if name := b.text(callee); isTypeName(name) {
				c, s := b.resolveTypeName(value, name)
				return pathTarget{coord: c, suffix: s}, true
			}
		}
	case "simple_identifier":
		name := b.text(value)
		if typeName, ok := b.localTypeAt(name, pos); ok && typeName != "" {
			c, s := b.resolveTypeName(value, typeName)
			return pathTarget{coord: c, suffix: s}, true
		}
		if t, ok := b.types[name]; ok {
			return t, true
		}
		if isTypeName(name) {
			// A member reached through a type: a companion's, an `object`'s, or an
			// enum entry's.
			c, s := b.resolveTypeName(value, name)
			return pathTarget{coord: c, suffix: s}, true
		}
	case "navigation_expression":
		segs := b.navigationSegments(value)
		if len(segs) == 0 {
			return pathTarget{}, false
		}
		// A binding shadows a package, so a chain starting at a local is a member
		// access and not a qualified name.
		if _, ok := b.lookup(segs[0], pos); ok {
			return pathTarget{}, false
		}
		pkg, types, members := splitPath(segs)
		if len(types) == 0 || len(members) > 0 {
			return pathTarget{}, false
		}
		c, ns := b.typePath(value, pkg, types)
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
	var segs []string
	switch n.Type(b.lang) {
	case "simple_identifier":
		if b.isImplicitIt(n, b.text(n)) {
			return pathTarget{}, false
		}
		segs = []string{b.text(n)}
	case "navigation_expression":
		segs = b.navigationSegments(n)
	}
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

// navigationSegments flattens a navigation chain into the dotted name it spells,
// or nil when its base is not a plain identifier — `f().g` names no path.
func (b *builder) navigationSegments(n *sitter.Node) []string {
	if n == nil {
		return nil
	}
	switch n.Type(b.lang) {
	case "simple_identifier":
		return []string{b.text(n)}
	case "navigation_expression", "directly_assignable_expression":
		base := b.navigationSegments(firstNamedChild(n))
		if base == nil {
			return nil
		}
		suffix := firstChildOfType(n, "navigation_suffix", b.lang)
		if suffix == nil {
			return nil
		}
		name := b.text(firstChildOfType(suffix, "simple_identifier", b.lang))
		if name == "" {
			return nil
		}
		return append(base, name)
	}
	return nil
}

// lookup finds the definition named name that is visible at byte offset pos:
// among definitions whose declaring scope contains pos, the one in the innermost
// such scope, declared no later than pos.
//
// The "no later than pos" rule is a local's, and a class member breaks it — a
// method may use a property declared below it, which is legal and common. So a
// definition whose scope is a *type* is exempt: its whole scope is its lifetime.
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

// declaredType recovers the syntactic type of the binding a captured name
// declares — enough to name the members reached through it, without any type
// inference. "" means unknown, which downstream becomes SCIP's "." rather than a
// guess.
//
// It is asked of the *name* rather than of the declaration, because the type
// annotation hangs off different ancestors in the shapes Kotlin writes: a
// property's is on the `variable_declaration`, a parameter's on the `parameter`,
// a `catch`'s on the block.
func (b *builder) declaredType(name *sitter.Node) string {
	parent := name.Parent()
	if parent == nil {
		return ""
	}
	switch parent.Type(b.lang) {
	case "variable_declaration":
		if t := b.unwrapType(typeChild(parent, b.lang)); t != "" {
			return t
		}
		// `val g = Greeter()` — the initialiser names the type outright.
		if owner := parent.Parent(); owner != nil && owner.Type(b.lang) == "property_declaration" {
			return b.constructedType(owner)
		}
	case "parameter", "class_parameter", "catch_block", "parameter_with_optional_type":
		return b.unwrapType(typeChild(parent, b.lang))
	}
	return ""
}

// constructedType reads a property's initialiser in the one shape that names a
// type: a call whose callee is an uppercase name. Kotlin has no `new`, so a
// constructor call is spelled exactly like a function call and the callee is the
// type — which makes this syntax rather than inference. `val x = compute()`
// yields "", and the members reached through `x` land on ".".
func (b *builder) constructedType(property *sitter.Node) string {
	call := lastChildOfType(property, "call_expression", b.lang)
	if call == nil {
		return ""
	}
	switch callee := firstNamedChild(call); {
	case callee == nil:
		return ""
	case callee.Type(b.lang) == "simple_identifier":
		if name := b.text(callee); isTypeName(name) {
			return name
		}
	case callee.Type(b.lang) == "navigation_expression":
		segs := b.navigationSegments(callee)
		if len(segs) > 0 && isTypeName(segs[len(segs)-1]) {
			return strings.Join(segs, ".")
		}
	}
	return ""
}

// unwrapType reduces a type expression to the bare (possibly dotted) name a
// descriptor can use. Nullability and generic application are transparent:
// `List<String>?` is named by `List`, because the members reached through it are
// `List`'s. A function type yields "" — its member is `invoke`, which nothing
// writes.
func (b *builder) unwrapType(t *sitter.Node) string {
	for depth := 0; t != nil && depth < 16; depth++ {
		switch t.Type(b.lang) {
		case "nullable_type", "parenthesized_type", "receiver_type", "type_projection":
			t = firstNamedChild(t)
		case "user_type":
			return strings.Join(b.textsOf(namedChildrenOfType(t, "type_identifier", b.lang)), ".")
		case "type_identifier":
			return b.text(t)
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

// claimSubtree marks every identifier under n as owned, so the reference patterns
// — which match inside an import or a package clause as readily as anywhere else
// — do not emit a second occurrence over bytes already described.
func (b *builder) claimSubtree(n *sitter.Node) {
	if n == nil {
		return
	}
	switch n.Type(b.lang) {
	case "simple_identifier", "type_identifier":
		b.claimed[span{n.StartByte(), n.EndByte()}] = true
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		b.claimSubtree(n.NamedChild(i))
	}
}

// builtin is the coordinate of the Kotlin standard library, which belongs to no
// artifact this index owns. `kotlin` is the root package it is reached through,
// and every one of its types is auto-imported into every file.
func (b *builder) builtin() coord.Coord {
	return coord.Foreign(b.coord.Scheme, b.coord.Manager, "kotlin")
}

// unknownValue is the descriptor for a value this file cannot name: the file's
// own base with SCIP's "." where the type would be. Nothing renders it twice for
// two different things, and nothing real ever matches it.
func (b *builder) unknownValue() facts.Descriptor {
	return facts.Descriptor{Prefix: b.coord, Suffix: b.base() + coord.Unknown + "#"}
}

// isImplicitIt reports whether a name is a lambda's implicit parameter — the one
// binding in Kotlin that is declared nowhere.
//
// `items.map { it.length }` binds `it` by the language rather than by the
// program, so there is no declaration for lookup to find and no type for the
// members reached through it. Without this it would fall through to "a lowercase
// name that resolves to nothing", which renders it as a *package* called `it` and
// its members as packages below one — descriptors that match nothing, but that
// say something false about what `it` is. An unknown value says the true thing.
//
// A lambda that names its parameter shadows this: lookup finds the declaration
// first, and this is never reached.
func (b *builder) isImplicitIt(n *sitter.Node, name string) bool {
	if name != implicitParameter {
		return false
	}
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "lambda_literal":
			return true
		case "function_declaration", "class_declaration", "source_file":
			return false
		}
	}
	return false
}

// inAccessor reports whether a node sits inside a property accessor, which is
// where `field` is a compiler-synthesized binding rather than a name.
func (b *builder) inAccessor(n *sitter.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "getter", "setter":
			return true
		case "function_declaration", "class_declaration", "source_file":
			return false
		}
	}
	return false
}

// ------------------------------------------------------------------ helpers ---

func (b *builder) text(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return n.Text(b.src)
}

func (b *builder) textsOf(nodes []*sitter.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, b.text(n))
	}
	return out
}

// typeName is the name a type declaration declares: the grammar names no fields,
// so it is the declaration's own `type_identifier` child. `class Box<T>` has a
// `T` too and it is a grandchild, under `type_parameters`, so the direct-child
// rule is what keeps them apart.
func (b *builder) typeName(n *sitter.Node) string {
	return b.text(firstChildOfType(n, "type_identifier", b.lang))
}

// functionName is the name a function declaration declares. An extension function
// writes its receiver first (`fun String.shout()`), and the receiver is a
// `receiver_type`, so the one direct `simple_identifier` child is still the name.
func (b *builder) functionName(n *sitter.Node) string {
	return b.text(firstChildOfType(n, "simple_identifier", b.lang))
}

// pathSegments reads a dotted `identifier` node — the shape a `package` clause
// and an `import` write a qualified name in — as its segments.
func (b *builder) pathSegments(n *sitter.Node) []string {
	return b.textsOf(namedChildrenOfType(n, "simple_identifier", b.lang))
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

func firstChildOfType(n *sitter.Node, kind string, lang *sitter.Language) *sitter.Node {
	if n == nil {
		return nil
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); c.Type(lang) == kind {
			return c
		}
	}
	return nil
}

func lastChildOfType(n *sitter.Node, kind string, lang *sitter.Language) *sitter.Node {
	if n == nil {
		return nil
	}
	var last *sitter.Node
	for i := 0; i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); c.Type(lang) == kind {
			last = c
		}
	}
	return last
}

func namedChildrenOfType(n *sitter.Node, kind string, lang *sitter.Language) []*sitter.Node {
	if n == nil {
		return nil
	}
	var out []*sitter.Node
	for i := 0; i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); c.Type(lang) == kind {
			out = append(out, c)
		}
	}
	return out
}

func hasChildOfType(n *sitter.Node, kind string, lang *sitter.Language) bool {
	return firstChildOfType(n, kind, lang) != nil
}

// hasKeyword reports whether a declaration was written with a given keyword. The
// grammar keeps `class`, `interface`, `object` and `fun` as anonymous children
// rather than as node types, so this is how `interface Greets` is told from
// `class Greeter`.
func hasKeyword(n *sitter.Node, keyword string, lang *sitter.Language) bool {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if !c.IsNamed() && c.Type(lang) == keyword {
			return true
		}
	}
	return false
}

// hasModifier reports whether a declaration carries a modifier — `const`,
// `private`, `data` and the rest live under one `modifiers` node, each as its own
// categorised child.
func hasModifier(n *sitter.Node, modifier string, lang *sitter.Language) bool {
	mods := firstChildOfType(n, "modifiers", lang)
	if mods == nil {
		return false
	}
	for i := 0; i < mods.NamedChildCount(); i++ {
		if c := mods.NamedChild(i); c.ChildCount() > 0 && c.Child(0).Type(lang) == modifier {
			return true
		}
	}
	return false
}

// isTypeBody reports whether a node is the body of a type declaration, which is
// what makes a property declared in it a field rather than a local.
func isTypeBody(n *sitter.Node, lang *sitter.Language) bool {
	if n == nil {
		return false
	}
	switch n.Type(lang) {
	case "class_body", "enum_class_body":
		return true
	}
	return false
}

// typeChild returns the type annotation among a declaration's children, in any of
// the spellings Kotlin writes one.
func typeChild(n *sitter.Node, lang *sitter.Language) *sitter.Node {
	if n == nil {
		return nil
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch c.Type(lang) {
		case "user_type", "nullable_type", "function_type", "parenthesized_type":
			return c
		}
	}
	return nil
}

// delegationType returns the supertype a `delegation_specifier` names. A
// superclass is written as a constructor call (`: Base()`) and an interface as a
// bare type (`: Greets`), so the type sits one level deeper in the first.
func delegationType(spec *sitter.Node, lang *sitter.Language) *sitter.Node {
	if inv := firstChildOfType(spec, "constructor_invocation", lang); inv != nil {
		return firstChildOfType(inv, "user_type", lang)
	}
	return firstChildOfType(spec, "user_type", lang)
}

// scriptContainer is the descriptor component a `.kts` file's declarations hang
// off, or "" for an ordinary source file.
//
// The name approximates kotlinc's own mangling of a script file name into a class
// name — `build.gradle.kts` becomes `Build_gradle` — closely enough to be
// recognisable, and exactness is not required of it: nothing outside a script can
// reference a script's top-level declarations, so this has to separate two
// scripts and nothing more. See the package comment.
func scriptContainer(filePath string) string {
	if !strings.EqualFold(filepath.Ext(filePath), scriptExt) {
		return ""
	}
	base := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	if base == "" {
		return ""
	}
	var sb strings.Builder
	for i, r := range base {
		switch {
		case i == 0:
			sb.WriteRune(unicode.ToUpper(r))
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			sb.WriteRune(r)
		default:
			sb.WriteRune('_')
		}
	}
	return sb.String() + "#"
}

// implicitParameter is the name Kotlin binds a single-parameter lambda's argument
// to when the lambda does not name it.
const implicitParameter = "it"

// backingField is the name a property accessor reaches its own storage through.
// It is a soft keyword: the grammar parses it as an ordinary identifier, and it
// declares nothing anywhere.
const backingField = "field"

// isTypeName reports whether a path segment names a type rather than a package or
// a declaration, by Kotlin's naming convention: types are UpperCamelCase,
// packages and declarations start lowercase. The coding conventions state it and
// the whole ecosystem follows it, which is what makes it reliable enough to build
// a descriptor on; see splitPath.
func isTypeName(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r)
}

// isNestedTypeName reports whether a member name written after a type names a
// nested type rather than a constant of it. Both start uppercase, and Kotlin's
// conventions separate them by what follows: a type is UpperCamelCase and a
// constant is ALL_CAPS with underscores.
func isNestedTypeName(s string) bool {
	return isTypeName(s) && strings.ToUpper(s) != s
}

// namespaceOf renders package segments as a SCIP namespace descriptor:
// slash-separated and slash-terminated. The empty package renders "", which is
// the root package and is a real namespace rather than a missing one.
func namespaceOf(segs []string) string {
	if len(segs) == 0 {
		return ""
	}
	return strings.Join(segs, "/") + "/"
}

// packageName is what a package occurrence is called in the `name` column: the
// last segment of its namespace, or — for the root package, which has no
// namespace at all — the artifact's own name.
func packageName(c coord.Coord, ns string) string {
	if trimmed := strings.TrimSuffix(ns, "/"); trimmed != "" {
		return lastSegment(trimmed)
	}
	if c.Name == "" || c.Name == coord.Unknown {
		return ""
	}
	return lastSegment(c.Name)
}

func lastSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// platformRoots are the top-level package names that are never this artifact's.
//
// It is what namespaceTarget needs and what Kotlin's syntax cannot supply: a
// package name carries no artifact identity, so `com.example.util` is this
// repository's or a dependency's with nothing in the source to say which. These
// are the roots the platform reserves — the Kotlin standard library and its
// official extensions, plus the JDK a Kotlin/JVM program runs on — and a
// third-party dependency missing from the set is treated as this artifact's,
// which yields a namespace with no definitions in it: an unresolved reference
// rather than a wrong edge.
var platformRoots = map[string]bool{
	"kotlin": true, "kotlinx": true,
	"java": true, "javax": true, "jakarta": true, "jdk": true, "sun": true,
}

// stdlibTypes are the standard-library types Kotlin puts in every file without an
// import, mapped to the namespace they live at under the `kotlin` root. References
// to them carry a foreign coordinate and so never pollute descriptor matching
// within the indexed artifact.
//
// The default-import list is `kotlin.*`, `kotlin.collections.*`, `kotlin.io.*`,
// `kotlin.text.*` and four more, and this is the commonly used subset of what
// they bring in. An omission costs one reference landing in this artifact's
// namespace with nothing to match, which is what an unrecognised name gets
// anyway.
var stdlibTypes = map[string]string{
	"Any": "", "Nothing": "", "Unit": "", "Boolean": "", "Byte": "", "Short": "",
	"Int": "", "Long": "", "Float": "", "Double": "", "Char": "", "String": "",
	"CharSequence": "", "Number": "", "Comparable": "", "Iterable": "",
	"Throwable": "", "Exception": "", "RuntimeException": "", "Error": "",
	"IllegalArgumentException": "", "IllegalStateException": "",
	"UnsupportedOperationException": "", "NullPointerException": "",
	"Function": "", "Enum": "", "Annotation": "", "Array": "", "Lazy": "",
	"Result": "", "Pair": "", "Triple": "", "Deprecated": "", "JvmStatic": "",

	"Collection": "collections/", "List": "collections/", "MutableList": "collections/",
	"Set": "collections/", "MutableSet": "collections/", "Map": "collections/",
	"MutableMap": "collections/", "Iterator": "collections/", "Sequence": "sequences/",

	"Regex": "text/", "StringBuilder": "text/", "Charsets": "text/",
}
