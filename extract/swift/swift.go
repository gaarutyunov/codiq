// Package swift is the Swift stanza: the tree-sitter query in query.scm plus
// the mapper here that turns its captures into core facts (SPEC.md §5).
//
// It imports facts and coord and deliberately not extract: it satisfies
// extract.Parser structurally, which is what keeps the registry free of an
// import cycle.
//
// The mapper's job, and its limits, are every earlier stanza's (§4.3): it builds
// the descriptor *suffix* from the CST and the namespace the file belongs to; it
// assigns role and neutral-core symbol kind; and it resolves references whose
// target definition is in the same file. It does no type checking, runs no name
// resolution algorithm and looks at no other file. A reference it cannot pin
// down is still emitted, carrying the best descriptor syntax allows, and the
// link pass decides what it means (§7). Where a component is genuinely
// unknowable file-locally the descriptor writes SCIP's "." for it, so it names
// an unresolved symbol rather than false-matching a real one.
//
// # The unit of modularity, and the first language that writes it nowhere
//
// Go's is the directory, TypeScript's the file, Python's the file with
// `__init__.py` collapsing to its directory, Rust's the `mod` tree, Java's and
// Kotlin's the `package` clause, C#'s the `namespace` declaration, PHP's the
// `namespace` statement. Swift's is the **module**, and Swift is the first
// language in this graph whose source says nothing at all about which module it
// is in. A SwiftPM module is a *target*, and a target is declared in
// `Package.swift` — one file, listing every module in the package — which §2.5
// forbids this extractor from reading.
//
// So the namespace is derived from the path, and specifically from the one path
// rule SwiftPM enforces rather than merely suggests: a target with no explicit
// `path:` **must** live at `Sources/<TargetName>/`, and the manager refuses to
// build the package otherwise. `Sources/Greeter/Greeter.swift` is therefore in
// module `Greeter` by the manager's own rule, and the derivation reads the
// convention rather than guessing at it. Everything below `Sources/Greeter/` is
// in that one module however deeply nested, because a Swift module is *flat*: a
// subdirectory is a file-system convenience and creates no namespace, which is
// exactly what dropping every segment after the second does.
//
// What it cannot represent, stated rather than discovered:
//
//   - A target that sets `path:` explicitly takes its module name from
//     `Package.swift` and its namespace here from the directory, and the two
//     disagree. Every reference into such a module from another one fails to
//     join — a missing edge, never a wrong one.
//   - A file outside any recognised source root falls back to its top-level
//     directory, and one at the repository root to the unnamed root module.
//     Both are namespaces that separate; neither claims to be a target name.
//
// # What a wildcard import costs
//
// This is the stanza's central limit and it is the language's, not the
// implementation's. **Every Swift import is a whole-module wildcard.** `import
// Greeter` brings every public declaration of `Greeter` into scope under its
// simple name, and the statement names not one of them. So a file-local
// extractor reading `Greeter()` in a module that imports three others cannot say
// which module the name came from — the information is in the other modules'
// sources, which §2.5 forbids reading.
//
// The answer is the one this repository keeps giving: resolve what the file
// states and refuse to guess the rest. A simple name is resolved against a
// declaration import, then the types this file declares, then the standard
// library, and a name none of those explains lands in *this file's own module's*
// namespace — a descriptor that matches nothing rather than one that matches the
// wrong thing. Guessing "the one non-platform module this file imports" would
// resolve most of them and would manufacture a phantom edge the first time it
// was wrong, and a phantom edge is worse than a missing one because nothing
// downstream can tell it is false.
//
// What that leaves working is more than it sounds. A Swift module is flat and
// multi-file, so the overwhelming majority of a package's references are
// *within* one module — and those resolve, because both sides derive the same
// namespace from the same path rule. Across modules, two spellings still
// resolve, and they are the two Swift gives for saying which module you mean:
// a qualified reference (`Greeter.Greeter`) and a declaration import
// (`import struct Greeter.Greeter`). And `imports` itself derives for every
// module import, wildcard or not, because the statement names the module even
// when it names nothing in it.
//
// # A manifest that is also a compilation unit
//
// `Package.swift` is written in Swift, so `byExt` hands it to this stanza as a
// source file — the first time a manifest and an indexable source have been the
// same file. Nothing in the registry can exclude it without a core change, and
// nothing should: it is Swift, this grammar reads it, and it is one of the two
// files in a Swift repository a human is most likely to be looking for.
//
// What it is not is part of a module. SwiftPM compiles each manifest on its own,
// and its `let package = Package(…)` is not a declaration any target's code can
// reference. So a manifest's declarations hang off a container named for the
// file, exactly as extract/kotlin does for a `.kts` and for the same reason: a
// repository with a root `Package.swift` and a second one under `examples/`
// declares `package` twice, and giving both the root module's namespace would
// render one descriptor for both — which the link pass, joining on the
// descriptor string and nothing else, would resolve into each other.
//
// The container is the file name with its separators flattened
// (`Package.swift` → `Package_swift#`), and it keeps the `swift` because a
// bare `Package#` is a type name somebody really writes. It is *namespaced by
// the manifest's own directory* rather than by the module rule, which is what
// separates two manifests in one repository — the case the module rule cannot
// see, since a manifest is in no module to be separated by.
//
// # An extension is a container and not a definition
//
// `extension String { func shout() -> String }` really does add a member to
// `String`: it is callable as `s.shout()` on any `String` anywhere in the
// module, and the reference site writes nothing that says where it was declared.
// So an extension's members are descriptored **under the type they extend** —
// `String#shout().` — which is the descriptor a call site renders once it has
// resolved its receiver, and therefore the one that joins.
//
// That is the opposite of the call extract/kotlin makes, and the two are both
// right because the languages differ. Kotlin's `fun String.shout()` is a static
// function of the *file's package* that takes a receiver; it is not a member of
// `String` and a call through a `String` does not reach it, so kotlin.go
// descriptors it where it is declared. Swift's is a member. Modelling either one
// as the other would be modelling the language as something it is not.
//
// The extension itself defines nothing. It writes no name of its own — the
// `user_type` in its header is the type it extends, which some other file
// declares — so capturing it as a definition would put a definition inside
// `String`'s namespace and would list a file that merely extends `String` in
// that type's `definedIn`. What it costs, named because it is real: two modules
// of one package each writing `extension String { func shout() }` render one
// descriptor and the link pass joins them. That is legal Swift, it is rare, and
// the alternative — no cross-file resolution of extension members at all — would
// lose the common case to protect the rare one.
//
// # A protocol, and the requirement its default implementation shares
//
// A `protocol` carries the `interface` kind, which is what link's `implements`
// derivation keys off, and conformance derives from method-set containment with
// nothing added: Swift writes the conformance in a `:` list, but the derivation
// never reads it, so a conformance declared in an extension in a third file
// derives exactly as well as one declared inline.
//
// A protocol extension — `extension Speaker { func greet() … }`, Swift's default
// implementation — descriptors its member at `Speaker#greet().`, which is byte
// for byte the requirement's own descriptor. Two definitions, one string. That
// is not a defect to be papered over but the honest reading: a call site writes
// `speaker.greet()` and names one thing, and which body runs is Swift's decision
// at dispatch time and not a fact this index could carry. Both definitions are
// the answer to "where is this", and both are shown.
//
// # An enum case is written two ways, and is descriptored the way it is written
//
// `case square` is read (`Shape.square`); `case circle(radius: Double)` is
// *called* (`Shape.circle(radius: 1)`), because a case with associated values is
// a function from those values to the enum. The declaration says which, so the
// descriptor follows the declaration: an associated-value case takes the
// callable component and a plain one takes the field component, and each matches
// the only spelling its own use sites can have.
//
// Two costs. A leading-dot form (`case .circle(let r)`, `.square`) infers its
// type from context and this stanza does no inference, so a pattern match joins
// nothing — which is true of both readings and is not what the choice decides.
// And an associated-value case now carries a `()` descriptor, so link's
// `implements` counts it toward its enum's method set: an enum whose case names
// happen to cover a protocol's whole method set would be derived as conforming
// to it. Case names are lowercase nouns and protocol requirements are verbs, so
// the collision needs to be written on purpose.
//
// # What one dot hides, and the receiver that arrives as a type
//
// Swift inherits the ambiguity every language with a member operator has — `a.b`
// is a member of a value, a member of a type, or a segment of a qualified name —
// and the mapper resolves it the same way every stanza here does: by what the
// left half resolves to. Swift's naming convention does the disambiguating that
// the grammar cannot (types UpperCamelCase, values lowerCamelCase), and being
// wrong costs a descriptor that matches nothing rather than one that matches the
// wrong thing.
//
// One thing is this grammar's own rather than the language's. A bare identifier
// in receiver position parses as a **`user_type`**: `g.greet()` wraps `g` in the
// node a type reference wears, whatever `g` is. Taken at face value that would
// emit every receiver in the file as a type reference. So a `user_type` sitting
// as the left half of a navigation is demoted to a read before anything else
// looks at it — the CST's position says what the node type does not.
//
// # What is inferred, and the one thing that is
//
// `let g = Greeter(name: "world")` is the idiomatic spelling and it writes no
// type, so a stanza that only read declared types would resolve nothing through
// `g`. This one reads the initialiser in exactly one shape: a construction,
// whose callee *is* the type, because Swift has no `new` to mark it with. That
// is syntax and not inference — the program says `Greeter` — and it is the whole
// of what is read back. `let x = compute()` yields no type, and the members
// reached through it land on ".".
//
// # What an overload hides
//
// Swift overloads on the parameter list *and* on the argument labels, and the
// descriptor's callable component carries the name alone, so `greet(name:)` and
// `greet(id:)` both render `greet().` and collide. java.go argues the case at
// length and the argument is unchanged, with one Swift-specific aggravation
// worth naming: an argument label is part of the name in Swift in a way it is
// not in Java, so `move(to:)` and `move(from:)` are two functions a reader would
// call differently-named. The definition side could render them apart from one
// file's CST; the reference side, which sees `move(to: p)` written as a callee
// plus a labelled argument list, could be made to as well — but only for a call,
// never for a function reference (`let f = move`), so the two sides would
// disagree. A descriptor both sides compute the same way beats a precise one
// only one side can.
//
// # An if that does not parse
//
// The pinned grammar (gotreesitter v0.47.1) cannot parse `if let x = y { … }`,
// Swift's most common statement: an optional binding in an `if` condition yields
// an ERROR node, while the same binding in a `guard` or a `while` parses. §14
// M9+ forbids changing go.mod, so the grammar is what it is, and the recovery is
// read rather than fought — tree-sitter keeps an ERROR node's children, and the
// children it keeps are the same shape the `guard` form writes. query.scm's last
// definition pattern extracts the binding the parse lost, anchored so it claims
// the binding and nothing else in the failed region. What is still lost is the
// `if` body's scope: its bindings land in the enclosing scope, which resolves
// one level too widely and never to the wrong thing.
package swift

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

// Lang is the value written to file.lang for the files this stanza handles: the
// extension without its dot, which is what every language here but C/C++ carries
// — and C/C++ departed only because two languages shared one stanza.
//
// A `Package.swift` carries it too. A manifest is Swift, this grammar reads it,
// and tagging it apart would make every Swift repository's own manifest a second
// language in the graph — and would turn any edge between a manifest and a
// source into a cross-language edge, which is the invariant
// test/integration/m6_test.go's noCrossLanguageEdges asserts.
const Lang = "swift"

// Ext is the file extension this stanza is registered under. It repeats
// coord.SwiftExt, for the reason coord.GoExt gives.
const Ext = ".swift"

//go:embed query.scm
var queryScheme string

// Parser is the Swift stanza. Safe for concurrent use: the grammar and compiled
// query are immutable after the first Parse, and each parse checks a
// gotreesitter parser out of a pool.
type Parser struct {
	once    sync.Once
	lang    *sitter.Language
	pool    *sitter.ParserPool
	query   *sitter.Query
	initErr error
}

// New returns the Swift parser. It is cheap: the grammar is loaded and query.scm
// compiled on the first Parse, so a binary that never parses Swift never
// decompresses the Swift grammar — and a query that fails to compile lands in
// ParseError rather than panicking at init.
func New() *Parser { return &Parser{} }

func (p *Parser) init() {
	p.once.Do(func() {
		p.lang = grammars.SwiftLanguage()
		if p.lang == nil {
			p.initErr = errors.New("swift: gotreesitter has no Swift grammar")
			return
		}
		q, err := sitter.NewQuery(queryScheme, p.lang)
		if err != nil {
			p.initErr = fmt.Errorf("swift: compile query.scm: %w", err)
			return
		}
		p.query = q
		p.pool = sitter.NewParserPool(p.lang)
	})
}

// Parse extracts one Swift file's facts. It never returns an error: a failure is
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
		modules:    map[string]pathTarget{},
		types:      map[string]pathTarget{},
		members:    map[string]memberTarget{},
		claimed:    map[span]bool{},
		descIndex:  map[string]facts.LocalID{},
		defsByName: map[string][]defRec{},
	}
	b.ns, b.manifest = fileBase(c, filePath)
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

// pathTarget is what a qualified name resolved to: the coordinate and descriptor
// suffix its members hang off.
type pathTarget struct {
	coord  coord.Coord
	suffix string
}

// memberTarget is what a declaration import bound: the container the declaration
// lives in, plus the name it is declared under. The terminator is missing on
// purpose — only the use site knows whether the declaration is called or read.
type memberTarget struct {
	pathTarget
	name string
}

type builder struct {
	lang  *sitter.Language
	src   []byte
	coord coord.Coord
	// ns is this file's module namespace — the descriptor prefix of everything
	// it declares — or, for a manifest, the manifest's own directory. "" for the
	// root module.
	ns string
	// manifest is the descriptor component a `Package.swift`'s declarations hang
	// off, or "" for an ordinary source file. See the package comment.
	manifest string

	out       facts.FileFacts
	nextScope facts.LocalID
	nextOcc   facts.LocalID

	scopes    []scopeRec
	scopeByID map[facts.LocalID]scopeRec

	// modules holds the module names this file imported, so that a qualified
	// `Greeter.Greeter` resolves. types and members hold what a *declaration*
	// import bound — `import struct Greeter.Greeter` binds the type `Greeter`,
	// `import func Greeter.build` the function `build` — which is the only
	// import form that binds a name at all.
	modules map[string]pathTarget
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
	b.collectModule(matches)
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
// a stack of open scopes yields each scope's parent with no language knowledge
// at all.
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

// ------------------------------------------------------------------ module ---

// collectModule emits the definition of the module this file belongs to.
//
// There is nothing in the CST to read: Swift declares no namespace, so the
// capture is the `source_file` itself and the name comes from the path rule (see
// fileBase). The occurrence carries a zero-width range at byte 0 for that
// reason — no bytes of the file say this, which is the honest range for a fact
// the file does not state.
//
// A manifest emits none. `Package.swift` is in no module — SwiftPM compiles it
// on its own — so a module definition for it would be a claim about a namespace
// nothing imports, and `imports` would derive an edge into a build script.
func (b *builder) collectModule(matches []sitter.QueryMatch) {
	found := false
	for _, m := range matches {
		if _, _, ok := roots(m, "definition.package"); ok {
			found = true
			break
		}
	}
	if !found || b.manifest != "" {
		return
	}

	desc := facts.Descriptor{Prefix: b.coord, Suffix: b.ns}
	occ := b.addOccurrenceIn(b.fileScope(), desc, facts.RoleDefinition, facts.KindPackage, moduleName(b.coord, b.ns), 0, 0)
	b.edge(facts.EdgeDefines, facts.FileRef(), facts.OccurrenceRef(occ))
	if _, dup := b.descIndex[desc.String()]; !dup {
		b.descIndex[desc.String()] = occ
	}
}

// ----------------------------------------------------------------- imports ---

// collectImports records what each `import` binds, and emits one occurrence per
// module named.
//
// Import *edges* are cross-file and therefore the link pass's (§4.4); what is
// extracted is the occurrence, whose descriptor names the imported module. That
// descriptor is byte-identical to the one the target module's own files derive
// from their path — both are the module name with a trailing slash — which is
// what lets link derive `imports` by descriptor join.
//
// Every identifier the statement consumed is claimed, so the reference patterns
// — which match happily inside an import — do not emit a second, weaker
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
		b.claimSubtree(cd.stmt)
		b.importDeclaration(cd.stmt, cd.name)
	}
}

// importDeclaration resolves one import and records what it binds.
//
// A module import binds nothing nameable, which is Swift's whole-module wildcard
// and the stanza's central limit — see the package comment. It still names a
// module, so the occurrence is emitted and the `imports` edge derives from it
// exactly as a declaration import's does.
//
// A declaration import (`import struct Greeter.Greeter`) is the one form that
// binds a name, and the kind keyword is how it is told apart: without one, every
// segment is a module path (`import Foundation.NSString` names a submodule);
// with one, the last segment is a declaration of the module before it.
func (b *builder) importDeclaration(stmt, path *sitter.Node) {
	nodes := namedChildrenOfType(path, "simple_identifier", b.lang)
	segs := b.textsOf(nodes)
	if len(segs) == 0 {
		return
	}

	kind, declaration := importKind(stmt, b.lang)
	mod := segs
	var decl string
	if declaration && len(segs) > 1 {
		mod, decl = segs[:len(segs)-1], segs[len(segs)-1]
	}

	c, ns := b.moduleTarget(mod)
	b.addOccurrence(
		facts.Descriptor{Prefix: c, Suffix: ns},
		facts.RoleReference, facts.KindPackage, mod[len(mod)-1],
		nodes[len(mod)-1].StartByte(), nodes[len(mod)-1].EndByte(),
	)
	if !b.moduleIsForeign(mod) {
		b.modules[mod[0]] = pathTarget{coord: c, suffix: namespaceOf(mod[:1])}
	}
	if decl == "" {
		return
	}

	target := pathTarget{coord: c, suffix: ns}
	last := nodes[len(nodes)-1]
	if kind == "typealias" || isTypeName(decl) {
		b.types[decl] = pathTarget{coord: c, suffix: ns + decl + "#"}
		b.addOccurrence(
			facts.Descriptor{Prefix: c, Suffix: ns + decl + "#"},
			facts.RoleReference, facts.KindType, decl, last.StartByte(), last.EndByte(),
		)
		return
	}
	// A `func`, `var` or `let` import: the container and the declared name are
	// what is recorded, because the terminator is the use site's to choose.
	b.members[decl] = memberTarget{pathTarget: target, name: decl}
	b.addOccurrence(
		facts.Descriptor{Prefix: c, Suffix: ns + decl + "."},
		facts.RoleReference, facts.KindVariable, decl, last.StartByte(), last.EndByte(),
	)
}

// importKind returns the declaration kind an import named, and whether it named
// one at all. The keyword is an anonymous child of the statement — the grammar
// gives it no node type of its own — so this reads the token text.
func importKind(stmt *sitter.Node, lang *sitter.Language) (string, bool) {
	for i := 0; i < stmt.ChildCount(); i++ {
		c := stmt.Child(i)
		if c.IsNamed() {
			continue
		}
		if declarationImportKinds[c.Type(lang)] {
			return c.Type(lang), true
		}
	}
	return "", false
}

// declarationImportKinds are the keywords that make `import X.y` name a
// declaration of module `X` rather than a submodule of it.
var declarationImportKinds = map[string]bool{
	"struct": true, "class": true, "enum": true, "protocol": true,
	"typealias": true, "func": true, "var": true, "let": true,
}

// ------------------------------------------------------------------- paths ---

// moduleTarget renders a module path as a coordinate and namespace descriptor.
//
// There is no third case beyond "a module of the platform" and "a module of
// whatever this repository is". A Swift module name carries no artifact identity
// at all — `Greeter` says nothing about which package it comes from — so the
// rule is "the platform if the root says so, this coordinate otherwise", and a
// third-party dependency lands at a namespace of this coordinate that holds no
// definitions, which is an unresolved reference and not a wrong edge. That is
// java.go's problem and kotlin.go's, in Swift's spelling.
func (b *builder) moduleTarget(mod []string) (coord.Coord, string) {
	if len(mod) == 0 {
		return b.coord, ""
	}
	if b.moduleIsForeign(mod) {
		return coord.Foreign(b.coord.Scheme, b.coord.Manager, mod[0]), namespaceOf(mod[1:])
	}
	return b.coord, namespaceOf(mod)
}

func (b *builder) moduleIsForeign(mod []string) bool {
	return len(mod) > 0 && platformModules[mod[0]]
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
		cd := cand{kind: suffixAfter(root.Name, "definition."), node: root.Node, name: name.Node}
		if root.Node.Type(b.lang) == "parameter" {
			// `func move(to point: Point)` writes an external label and an
			// internal name, and only the second is a binding. Both arrive as
			// separate matches over one node; both resolve to the same name here
			// and the span dedupe below collapses them.
			if last := lastChildOfType(root.Node, "simple_identifier", b.lang); last != nil {
				cd.name = last
			}
		}
		cands = append(cands, cd)
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
		prefix, suffix := b.definitionDescriptor(kind, cd.node, cd.name, name)
		b.claimed[s] = true
		if cd.node.Type(b.lang) == "parameter" {
			// The external label, now that the binding beside it is emitted: it
			// names an argument slot rather than a symbol, and claiming it is
			// what keeps the bare-identifier reference pattern off it.
			b.claimLabels(cd.node)
		}

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
// capture name does not.
func (b *builder) refineKind(kind string, node, name *sitter.Node) string {
	switch kind {
	case facts.KindType:
		// A protocol is the neutral core's interface, and it is the kind link
		// keys `implements` off.
		if node.Type(b.lang) == "protocol_declaration" {
			return facts.KindInterface
		}
	case facts.KindFunction:
		// Swift has free functions and members and one node type for both; a
		// protocol requirement is a member by construction.
		if node.Type(b.lang) == "protocol_function_declaration" || b.enclosingType(name) != nil {
			return facts.KindMethod
		}
	case facts.KindVariable:
		decl := b.propertyDeclaration(name)
		switch {
		case decl == nil:
			// A `guard let`, a `catch let`, a `for` binding or the recovered
			// `if let`: a local, always.
		case isTypeBody(decl.Parent(), b.lang):
			return facts.KindField
		case decl.Parent() != nil && decl.Parent().Type(b.lang) == "source_file" && hasBinding(decl, "let", b.lang):
			// A top-level `let` is Swift's constant; the language has no
			// `const`, so the binding keyword is the whole of the distinction.
			// A `let` *inside* a body is an immutable local and stays a
			// variable, which is the reading kotlin.go gives a local `val`.
			return facts.KindConstant
		}
	case facts.KindField:
		// An enum case with associated values is a function from them to the
		// enum, and is written as a call at every use site.
		if hasChildOfType(node, "enum_type_parameters", b.lang) {
			return facts.KindMethod
		}
	}
	return kind
}

// definitionDescriptor builds the SCIP descriptor for a definition from its
// capture hierarchy (SPEC.md §5).
//
// The prefix is a return value rather than this file's coordinate because of
// extensions: `extension String` puts the members below it under the standard
// library's coordinate, which is not this artifact's. Everything else a Swift
// file declares is written inside what owns it, so for everything else it is
// this file's.
func (b *builder) definitionDescriptor(kind string, decl, name *sitter.Node, text string) (coord.Coord, string) {
	c, container := b.containerSuffix(decl)
	switch kind {
	case facts.KindFunction, facts.KindMethod:
		return c, container + text + "()."
	case facts.KindType, facts.KindInterface:
		return c, container + text + "#"
	case facts.KindParameter:
		if name.Type(b.lang) == "type_identifier" {
			// A generic parameter is a type, not an argument.
			return c, container + text + "#"
		}
		return c, container + "(" + text + ")"
	case facts.KindPackage:
		return c, container + text + "/"
	default: // field, variable, constant
		return c, container + text + "."
	}
}

// containerSuffix returns the coordinate and descriptor suffix of the nearest
// enclosing named container of n — a type, a protocol, an extension or a
// function — or this file's own base when there is none. A body, a closure, an
// accessor and an observer are transparent: none has a name, so none has a
// descriptor of its own and what is inside belongs to the enclosing container.
//
// Nested types need no special case: each named container on the way out
// contributes exactly one component, so `Outer#Inner#` and `Outer#run().Local#`
// fall out of the same walk that produces `Greeter#greet().`.
//
// An **extension** is where this stops being a walk over declarations and
// becomes a resolution. `extension String` names a type some other file
// declares, so the walk does not continue past it: the extended type's own
// container is wherever it was declared, which resolveTypeName knows and the
// ancestors of this node do not. That is what puts `shout()` at
// `String#shout().` — the descriptor a call site renders — and it is argued in
// the package comment.
func (b *builder) containerSuffix(n *sitter.Node) (coord.Coord, string) {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "class_declaration":
			if name := b.typeName(p); name != "" {
				c, s := b.containerSuffix(p)
				return c, s + name + "#"
			}
			if ext := firstChildOfType(p, "user_type", b.lang); ext != nil {
				if name := b.unwrapType(ext); name != "" {
					return b.resolveTypeName(p, name)
				}
			}
		case "protocol_declaration":
			if name := b.typeName(p); name != "" {
				c, s := b.containerSuffix(p)
				return c, s + name + "#"
			}
		case "function_declaration", "protocol_function_declaration":
			if name := b.functionName(p); name != "" {
				c, s := b.containerSuffix(p)
				return c, s + name + "()."
			}
		}
	}
	return b.coord, b.base()
}

// base is the descriptor suffix everything this file declares hangs off: the
// module namespace, plus the manifest container for a `Package.swift`.
func (b *builder) base() string { return b.ns + b.manifest }

// enclosingType returns the type, protocol or extension declaration n sits
// innermost inside, or nil when n is at the file's top level. It is what
// resolves `self` and what tells a free function from a method.
func (b *builder) enclosingType(n *sitter.Node) *sitter.Node {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "class_declaration", "protocol_declaration":
			return p
		}
	}
	return nil
}

// selfTarget is the coordinate and suffix `self` names: the type, protocol or
// extension the expression is written inside. Swift has no receiver-taking free
// function, so unlike Kotlin's there is only the one case.
func (b *builder) selfTarget(n *sitter.Node) (pathTarget, bool) {
	decl := b.enclosingType(n)
	if decl == nil {
		return pathTarget{}, false
	}
	if name := b.typeName(decl); name != "" {
		c, s := b.containerSuffix(decl)
		return pathTarget{coord: c, suffix: s + name + "#"}, true
	}
	if ext := firstChildOfType(decl, "user_type", b.lang); ext != nil {
		if name := b.unwrapType(ext); name != "" {
			c, s := b.resolveTypeName(decl, name)
			return pathTarget{coord: c, suffix: s}, true
		}
	}
	return pathTarget{}, false
}

// superTarget is what `super` names: the first entry of the enclosing type's
// inheritance list.
//
// Swift writes a superclass and a protocol conformance in one comma-separated
// list and does not say which entry is which — only a resolver with the other
// files could — so the first is taken, which is where Swift *requires* the
// superclass to be written when there is one. `super` is only legal in a class
// with a superclass, so the first entry being a protocol means the program does
// not compile, and the descriptor it renders matches a protocol that exists.
func (b *builder) superTarget(n *sitter.Node) (pathTarget, bool) {
	decl := b.enclosingType(n)
	if decl == nil {
		return pathTarget{}, false
	}
	spec := firstChildOfType(decl, "inheritance_specifier", b.lang)
	if spec == nil {
		return pathTarget{}, false
	}
	name := b.unwrapType(firstChildOfType(spec, "user_type", b.lang))
	if name == "" {
		return pathTarget{}, false
	}
	c, s := b.resolveTypeName(n, name)
	return pathTarget{coord: c, suffix: s}, true
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
// does. That is what makes the bare `(simple_identifier)` pattern safe: it
// always loses to a navigation that covers it.
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
		role := suffixAfter(root.Name, "reference.")
		if nameNode.Type(b.lang) == "user_type" {
			// The bare `(user_type)` pattern names its own last segment; the
			// descriptor is built from the whole path.
			last := lastChildOfType(nameNode, "type_identifier", b.lang)
			if last == nil {
				continue
			}
			nameNode = last
			if b.isNavigationQualifier(root.Node) {
				// This grammar parses a bare identifier in receiver position as
				// a `user_type` whatever it is, so position and not node type
				// says what `g` in `g.greet()` is. See the package comment.
				role = "read"
			}
		}
		if b.isLabel(nameNode) {
			// An argument label, an associated-value label or a parameter's
			// external name: syntax that names a slot, not a symbol.
			continue
		}
		s := span{nameNode.StartByte(), nameNode.EndByte()}
		if b.claimed[s] {
			continue // a definition or an import already owns this identifier
		}
		cand := refRec{role: role, node: root.Node, nameNode: nameNode}
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
		if accessorBindings[name] && b.inAccessor(r.nameNode) {
			// `newValue` and `oldValue` inside an accessor or an observer are
			// bindings the compiler synthesizes, with no declaration anywhere.
			// Emitting them would invent a symbol per accessor.
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

// callDescriptor names the target of a call.
//
// Three shapes, and the first is the one an initialiser makes. Swift has no
// `new`, so `Greeter(name: "x")` is a call whose callee is a *type* — the
// initialiser is not named at the use site — and the honest descriptor is the
// type's own, which is a definition that exists and which a caller in another
// file renders identically.
//
// A bare `f()` is a member of the enclosing type, a top-level function of this
// file's module, or a declaration import. A receiver call `g.f()` is a member of
// whatever `g` resolves to, read off a declaration and never inferred. Where the
// receiver is unknowable file-locally the type component is SCIP's ".", so the
// descriptor cannot false-match a real definition.
func (b *builder) callDescriptor(r refRec, name string) (facts.Descriptor, string) {
	receiver := b.callReceiver(r)
	if receiver == nil {
		if isTypeName(name) {
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

// unqualifiedCall names a call written with no receiver, which in Swift is a
// member of the enclosing type or a top-level function of this file's module.
//
// The enclosing type wins, but only if this file actually declares that member,
// and the qualification is what Swift needs for Kotlin's reason: Swift has
// top-level functions, so `print(x)` written inside a method is not a method.
// The question is answerable file-locally in the same way and for a weaker
// reason than Kotlin's — a Swift type may be spread across files by extensions —
// so the fallback matters more here, and it is chosen the same way. A bare call
// the enclosing type does not declare is either a top-level function of this
// module, which `Module/name().` joins, or an inherited or extension member,
// whose descriptor carries a container this file cannot name and which therefore
// joins nothing whichever form is written. One of the two can resolve.
func (b *builder) unqualifiedCall(n *sitter.Node, name string) (facts.Descriptor, string) {
	if t, ok := b.selfTarget(n); ok {
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
		// `block()` where `block` is a closure parameter is a call written on a
		// *value* rather than on a function. The value is what it names, and a
		// function of the same name would have won above.
		return b.occurrence(def.occ).Descriptor, b.occurrence(def.occ).SymbolKind
	}
	return top, facts.KindFunction
}

// enclosingMember resolves a bare name against the members of every type it is
// written inside, innermost first. It exists for the shape the scope tree cannot
// express: a nested type's code sees the outer type's members, which is two type
// scopes out rather than one.
func (b *builder) enclosingMember(n *sitter.Node, name, terminator string) (facts.Descriptor, bool) {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "class_declaration", "protocol_declaration":
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
	case "user_type":
		// The demoted receiver of a navigation: the left half of `a.b`, which
		// names a value, a type or a module.
		return b.qualifierDescriptor(r, name)
	case "navigation_suffix":
		// The right half of `a.b`: a member of whatever the left half is.
		return b.memberDescriptor(r, name)
	}

	// A bare identifier used as a value.
	if def, ok := b.lookup(name, r.nameNode.StartByte()); ok {
		return b.occurrence(def.occ).Descriptor, b.occurrence(def.occ).SymbolKind
	}
	if isImplicitClosureParameter(name) {
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
	// A top-level property of this file's module, which is what a lowercase name
	// resolving to no binding in scope is in Swift. It renders a descriptor that
	// matches only if such a property is really declared.
	return facts.Descriptor{Prefix: b.coord, Suffix: b.base() + name + "."}, facts.KindVariable
}

// memberDescriptor names the right half of a `.`.
func (b *builder) memberDescriptor(r refRec, name string) (facts.Descriptor, string) {
	receiver := firstNamedChild(r.node.Parent())
	if t, ok := b.valueTarget(receiver, r.nameNode.StartByte()); ok {
		if isNestedTypeName(name) {
			// `Shape.Empty` — a nested type reached through the type that holds
			// it, which Swift writes exactly as it writes a member read. The two
			// are told apart by the one convention that separates them: a type
			// is UpperCamelCase and a case or a constant read through its type
			// is lowerCamelCase or ALL_CAPS.
			return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "#"}, facts.KindType
		}
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "."}, facts.KindField
	}
	if t, ok := b.moduleQualifier(receiver); ok {
		// `Greeter.Greeter` — the segment after a module is a declaration of it.
		kind, suffix := facts.KindVariable, t.suffix+name+"."
		if isTypeName(name) {
			kind, suffix = facts.KindType, t.suffix+name+"#"
		}
		return facts.Descriptor{Prefix: t.coord, Suffix: suffix}, kind
	}
	return facts.Descriptor{Prefix: b.coord, Suffix: b.base() + coord.Unknown + "#" + name + "."}, facts.KindField
}

// qualifierDescriptor names the left half of a `.`, which is where Swift's
// single member operator has to be disambiguated rather than read.
func (b *builder) qualifierDescriptor(r refRec, name string) (facts.Descriptor, string) {
	// A binding in scope wins: a local named `greeter` shadows a module named
	// `greeter`, which is also what the compiler does.
	if def, ok := b.lookup(name, r.nameNode.StartByte()); ok {
		return b.occurrence(def.occ).Descriptor, b.occurrence(def.occ).SymbolKind
	}
	if isImplicitClosureParameter(name) {
		return b.unknownValue(), facts.KindVariable
	}
	if t, ok := b.types[name]; ok {
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix}, facts.KindType
	}
	if t, ok := b.modules[name]; ok {
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix}, facts.KindPackage
	}
	if isTypeName(name) {
		c, s := b.resolveTypeName(r.nameNode, name)
		return facts.Descriptor{Prefix: c, Suffix: s}, facts.KindType
	}
	// A lowercase leading segment that names no binding is a top-level property
	// of this module. Swift has no relative module references, so there is
	// nothing else it could be.
	return facts.Descriptor{Prefix: b.coord, Suffix: b.base() + name + "."}, facts.KindVariable
}

// typeReferenceDescriptor names a type reference. The captured node is the whole
// `user_type`, so a qualified name is resolved as a path and a simple one
// against what this file can see.
func (b *builder) typeReferenceDescriptor(r refRec, name string) (facts.Descriptor, string) {
	segs := b.textsOf(namedChildrenOfType(r.node, "type_identifier", b.lang))
	if len(segs) > 1 {
		c, s := b.typePath(r.nameNode, segs)
		return facts.Descriptor{Prefix: c, Suffix: s}, facts.KindType
	}
	c, suffix := b.resolveTypeName(r.nameNode, name)
	return facts.Descriptor{Prefix: c, Suffix: suffix}, facts.KindType
}

// typePath renders a qualified type name — `Greeter.Greeter`, `Shape.Circle`.
//
// The first segment is resolved the way a bare name is and the rest are appended
// to whatever it turned out to be, which is what makes one rule cover both
// spellings Swift writes: a module qualifier and a nested type read through its
// holder are the same syntax, and resolveTypeName already tells a module from a
// type.
func (b *builder) typePath(n *sitter.Node, segs []string) (coord.Coord, string) {
	if len(segs) == 0 {
		return b.coord, b.base()
	}
	c, s := b.resolveTypeName(n, segs[0])
	if t, ok := b.modules[segs[0]]; ok {
		c, s = t.coord, t.suffix
	}
	for _, seg := range segs[1:] {
		s += seg + "#"
	}
	return c, s
}

// resolveTypeName names a type by the coordinate and descriptor suffix it lives
// at. The name may be dotted, when it came from a declaration this stanza read
// the text of rather than the CST.
//
// The order is the compiler's, minus everything the compiler reads other files
// for: a declaration import wins, then a type declared in this file (which is
// what makes a nested type resolvable by its simple name), then the implicitly
// imported standard library, then this file's own module. The last is Swift's
// module-wide visibility and is why a two-file module needs no import at all —
// and it is also where a type that really came from *another* module lands,
// unresolved, for the reason the package comment gives.
func (b *builder) resolveTypeName(n *sitter.Node, typeName string) (coord.Coord, string) {
	if strings.Contains(typeName, ".") {
		return b.typePath(n, strings.Split(typeName, "."))
	}
	if t, ok := b.types[typeName]; ok {
		return t.coord, t.suffix
	}
	if c, s, ok := b.localType(n, typeName); ok {
		return c, s
	}
	if _, ok := stdlibTypes[typeName]; ok {
		return b.builtin(), typeName + "#"
	}
	// A name matching an imported module's is *not* resolved to that module,
	// and the temptation is worth naming: `import Greeter` followed by
	// `Greeter(name:)` looks like it names the module's own type, and in a
	// package whose module and headline type share a name it usually does. It
	// is still a guess — the same guess as "attribute every unresolved name to
	// the one module this file imports" — and the wildcard import gives no
	// ground to make it on. A module-qualified `Greeter.Greeter` says it
	// outright, and typePath reads that.
	return b.coord, b.base() + typeName + "#"
}

// localType resolves a simple type name against the types this file declares,
// innermost container first. It is what makes `Inner` written inside `Outer`
// name `Outer#Inner#` rather than the module-level `Inner#` that does not exist.
//
// Only names this file already defined, which is why it consults descIndex:
// collectDefinitions runs first, so every type declared here is in it, and a
// name that is not is not this file's to claim.
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
		case "class_declaration", "protocol_declaration":
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
// is recovered from a declaration or from a construction, never inferred.
func (b *builder) valueTarget(value *sitter.Node, pos uint32) (pathTarget, bool) {
	if value == nil {
		return pathTarget{}, false
	}
	switch value.Type(b.lang) {
	case "self_expression":
		return b.selfTarget(value)
	case "super_expression":
		return b.superTarget(value)
	case "tuple_expression":
		// A parenthesized expression wears the tuple node when it holds one
		// element, which is how `(g).greet()` is written.
		if value.NamedChildCount() == 1 {
			return b.valueTarget(firstNamedChild(value), pos)
		}
	case "constructor_expression":
		if name := b.unwrapType(firstChildOfType(value, "user_type", b.lang)); name != "" {
			c, s := b.resolveTypeName(value, name)
			return pathTarget{coord: c, suffix: s}, true
		}
	case "call_expression":
		// `Greeter(name: "x").greet()` — the receiver's type is written right
		// there, which makes it the one expression form worth reading back.
		if callee := firstNamedChild(value); callee != nil && callee.Type(b.lang) == "simple_identifier" {
			if name := b.text(callee); isTypeName(name) {
				c, s := b.resolveTypeName(value, name)
				return pathTarget{coord: c, suffix: s}, true
			}
		}
	case "simple_identifier":
		// A receiver reached through `?.`, which is the one navigation whose
		// left half the grammar leaves as a plain identifier.
		return b.simpleValueTarget(value, b.text(value), pos)
	case "user_type":
		// A bare identifier in receiver position, which this grammar wraps in a
		// type node whatever it is.
		segs := b.textsOf(namedChildrenOfType(value, "type_identifier", b.lang))
		if len(segs) == 0 {
			return pathTarget{}, false
		}
		if len(segs) == 1 {
			return b.simpleValueTarget(value, segs[0], pos)
		}
		if _, ok := b.lookup(segs[0], pos); ok {
			return pathTarget{}, false
		}
		c, s := b.typePath(value, segs)
		return pathTarget{coord: c, suffix: s}, true
	case "navigation_expression":
		segs := b.navigationSegments(value)
		if len(segs) == 0 {
			return pathTarget{}, false
		}
		// A binding shadows a module, so a chain starting at a local is a member
		// access and not a qualified name.
		if _, ok := b.lookup(segs[0], pos); ok {
			return pathTarget{}, false
		}
		if _, ok := b.modules[segs[0]]; !ok {
			return pathTarget{}, false
		}
		c, s := b.typePath(value, segs)
		return pathTarget{coord: c, suffix: s}, true
	}
	return pathTarget{}, false
}

// simpleValueTarget resolves a one-segment receiver: a binding whose declared
// type is known, a type imported by name, or a type reached through its own name
// (a static member, or an enum case read through its enum).
func (b *builder) simpleValueTarget(n *sitter.Node, name string, pos uint32) (pathTarget, bool) {
	if typeName, ok := b.localTypeAt(name, pos); ok {
		if typeName == "" {
			return pathTarget{}, false
		}
		c, s := b.resolveTypeName(n, typeName)
		return pathTarget{coord: c, suffix: s}, true
	}
	if t, ok := b.types[name]; ok {
		return t, true
	}
	if _, ok := b.modules[name]; ok {
		return pathTarget{}, false // a module is not a value; moduleQualifier has it
	}
	if isTypeName(name) {
		c, s := b.resolveTypeName(n, name)
		return pathTarget{coord: c, suffix: s}, true
	}
	return pathTarget{}, false
}

// moduleQualifier resolves a node to the module it names, for the leading
// segment of a module-qualified reference.
func (b *builder) moduleQualifier(n *sitter.Node) (pathTarget, bool) {
	if n == nil {
		return pathTarget{}, false
	}
	var segs []string
	switch n.Type(b.lang) {
	case "user_type":
		segs = b.textsOf(namedChildrenOfType(n, "type_identifier", b.lang))
	case "simple_identifier":
		segs = []string{b.text(n)}
	case "navigation_expression":
		segs = b.navigationSegments(n)
	}
	if len(segs) == 0 {
		return pathTarget{}, false
	}
	t, ok := b.modules[segs[0]]
	if !ok {
		return pathTarget{}, false
	}
	s := t.suffix
	for _, seg := range segs[1:] {
		s += seg + "#"
	}
	return pathTarget{coord: t.coord, suffix: s}, true
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
	case "user_type":
		segs := b.textsOf(namedChildrenOfType(n, "type_identifier", b.lang))
		if len(segs) == 0 {
			return nil
		}
		return segs
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
// The "no later than pos" rule is a local's, and a type member breaks it — a
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
// A property writes its type in a `type_annotation` that is a *sibling* of the
// pattern holding the name, which is why this asks the declaration rather than
// the name's own parent; a parameter writes it as a direct child of the
// `parameter` node.
func (b *builder) declaredType(name *sitter.Node) string {
	parent := name.Parent()
	if parent == nil {
		return ""
	}
	switch parent.Type(b.lang) {
	case "parameter", "lambda_parameter":
		return b.unwrapType(typeChild(parent, b.lang))
	}
	decl := b.propertyDeclaration(name)
	if decl == nil {
		return ""
	}
	if ann := firstChildOfType(decl, "type_annotation", b.lang); ann != nil {
		if t := b.unwrapType(firstNamedChild(ann)); t != "" {
			return t
		}
	}
	// `let g = Greeter(name: "x")` — the initialiser names the type outright.
	return b.constructedType(decl)
}

// propertyDeclaration returns the property or protocol-property declaration a
// captured binding name belongs to, or nil when the binding is a local one a
// statement introduced.
func (b *builder) propertyDeclaration(name *sitter.Node) *sitter.Node {
	for p, depth := name.Parent(), 0; p != nil && depth < 4; p, depth = p.Parent(), depth+1 {
		switch p.Type(b.lang) {
		case "property_declaration", "protocol_property_declaration":
			return p
		case "source_file", "statements", "class_body", "function_body":
			return nil
		}
	}
	return nil
}

// constructedType reads a declaration's initialiser in the two shapes that name
// a type: a call whose callee is an uppercase name, and a `constructor_expression`
// — which is the same construction with generic arguments written out. Swift has
// no `new`, so a construction is spelled exactly like a call and the callee *is*
// the type, which makes this syntax rather than inference. `let x = compute()`
// yields "", and the members reached through `x` land on ".".
func (b *builder) constructedType(decl *sitter.Node) string {
	if ctor := lastChildOfType(decl, "constructor_expression", b.lang); ctor != nil {
		return b.unwrapType(firstChildOfType(ctor, "user_type", b.lang))
	}
	call := lastChildOfType(decl, "call_expression", b.lang)
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
// descriptor can use. Optionality, generic application, `some`/`any` and arrays
// are transparent: `[Greeter]?` is named by `Greeter`, because the members
// reached through it are `Greeter`'s. A function type yields "" — its member is
// a call, which nothing writes a name for.
func (b *builder) unwrapType(t *sitter.Node) string {
	for depth := 0; t != nil && depth < 16; depth++ {
		switch t.Type(b.lang) {
		case "optional_type", "opaque_type", "existential_type", "array_type",
			"type_annotation", "attributed_type", "metatype":
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

// claimSubtree marks every identifier under n as owned, so the reference
// patterns — which match inside an import as readily as anywhere else — do not
// emit a second occurrence over bytes already described.
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

// claimLabels marks a parameter's identifiers as owned. `func move(to point:
// Point)` writes an external label and an internal name; only the second is a
// binding, and the first names an argument slot rather than a symbol.
func (b *builder) claimLabels(param *sitter.Node) {
	for _, id := range namedChildrenOfType(param, "simple_identifier", b.lang) {
		b.claimed[span{id.StartByte(), id.EndByte()}] = true
	}
}

// builtin is the coordinate of the Swift standard library, which belongs to no
// artifact this index owns. `Swift` is the module it is reached through, and
// every one of its types is implicitly imported into every file.
func (b *builder) builtin() coord.Coord {
	return coord.Foreign(b.coord.Scheme, b.coord.Manager, swiftModule)
}

// unknownValue is the descriptor for a value this file cannot name: the file's
// own base with SCIP's "." where the type would be. Nothing renders it twice for
// two different things, and nothing real ever matches it.
func (b *builder) unknownValue() facts.Descriptor {
	return facts.Descriptor{Prefix: b.coord, Suffix: b.base() + coord.Unknown + "#"}
}

// isNavigationQualifier reports whether a `user_type` node is the left half of a
// navigation rather than a type in type position. See the package comment.
func (b *builder) isNavigationQualifier(n *sitter.Node) bool {
	p := n.Parent()
	if p == nil {
		return false
	}
	switch p.Type(b.lang) {
	case "navigation_expression", "directly_assignable_expression":
		return firstNamedChild(p) == n
	}
	return false
}

// isLabel reports whether an identifier names an argument slot rather than a
// symbol: `Greeter(name: "x")`'s `name`, and the label of an enum case's
// associated value. Both are part of a *callee's* declared signature, which the
// descriptor does not carry, so an occurrence for either would name nothing.
func (b *builder) isLabel(n *sitter.Node) bool {
	p := n.Parent()
	if p == nil {
		return false
	}
	switch p.Type(b.lang) {
	case "value_argument_label", "enum_type_parameters":
		return true
	}
	return false
}

// inAccessor reports whether a node sits inside a property accessor or observer,
// which is where `newValue` and `oldValue` are compiler-synthesized bindings
// rather than names.
func (b *builder) inAccessor(n *sitter.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "computed_setter", "willset_didset_block":
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
// so it is the declaration's own `type_identifier` child. `struct Box<T>` has a
// `T` too and it is a grandchild, under `type_parameters`, so the direct-child
// rule is what keeps them apart — and an `extension` has none at all, which is
// what makes "" mean "this declares no type of its own".
func (b *builder) typeName(n *sitter.Node) string {
	return b.text(firstChildOfType(n, "type_identifier", b.lang))
}

// functionName is the name a function declaration declares: its one direct
// `simple_identifier` child.
func (b *builder) functionName(n *sitter.Node) string {
	return b.text(firstChildOfType(n, "simple_identifier", b.lang))
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

// hasBinding reports whether a declaration was written with `let` or `var`. The
// grammar wraps the keyword in a `value_binding_pattern` and keeps the keyword
// itself anonymous, so this is how a constant is told from a variable.
func hasBinding(n *sitter.Node, keyword string, lang *sitter.Language) bool {
	pattern := firstChildOfType(n, "value_binding_pattern", lang)
	if pattern == nil {
		return false
	}
	for i := 0; i < pattern.ChildCount(); i++ {
		if c := pattern.Child(i); !c.IsNamed() && c.Type(lang) == keyword {
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
	case "class_body", "enum_class_body", "protocol_body":
		return true
	}
	return false
}

// typeChild returns the type annotation among a declaration's children, in any
// of the spellings Swift writes one.
func typeChild(n *sitter.Node, lang *sitter.Language) *sitter.Node {
	if n == nil {
		return nil
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch c.Type(lang) {
		case "user_type", "optional_type", "opaque_type", "existential_type",
			"array_type", "dictionary_type", "function_type", "tuple_type", "metatype":
			return c
		}
	}
	return nil
}

// ------------------------------------------------------------------- naming ---

// fileBase decides what a file's declarations hang off: its module namespace,
// and — for a manifest — the container that separates it from every other one.
//
// The module is derived from the path, because Swift's source says nothing about
// it and §2.5 forbids reading `Package.swift`. SwiftPM requires a target with no
// explicit `path:` to live at `Sources/<TargetName>/`, so the second segment
// after a recognised source root *is* the module name — and everything below it
// belongs to that one module however deeply nested, because a Swift module is
// flat. See the package comment for what this cannot represent.
//
// A manifest is in no module and takes the file's own directory instead, which
// is what separates a root `Package.swift` from one under `examples/`. The
// module rule cannot separate them: neither is in a target.
func fileBase(c coord.Coord, filePath string) (ns, manifest string) {
	dir := c.Namespace(filePath)
	if container := manifestContainer(filePath); container != "" {
		return dir, container
	}
	if dir == "" {
		return "", ""
	}
	segs := strings.Split(strings.TrimSuffix(dir, "/"), "/")
	if sourceRoots[segs[0]] {
		if len(segs) < 2 {
			return "", ""
		}
		return segs[1] + "/", ""
	}
	return segs[0] + "/", ""
}

// manifestContainer is the descriptor component a SwiftPM manifest's
// declarations hang off, or "" for an ordinary source file.
//
// `Package.swift` and the version-specific `Package@swift-5.9.swift` are the two
// names SwiftPM reads, and nothing else is a manifest. The separators are
// flattened rather than dropped and the `swift` is kept, so the component is
// `Package_swift#` — a bare `Package#` is a type name somebody really writes,
// and this one is not.
func manifestContainer(filePath string) string {
	base := filepath.Base(filePath)
	if base != coordPackageManifest && !strings.HasPrefix(base, versionedManifestPrefix) {
		return ""
	}
	if !strings.HasSuffix(base, Ext) {
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

// coordPackageManifest repeats coord.PackageManifest, which this package could
// import — it already imports coord — but which is spelled here because the two
// mean different things: coord's is the file a resolver reads for an identity,
// and this is the file this stanza refuses to treat as part of a module. They
// happen to be the same name; nothing requires them to stay so.
const coordPackageManifest = "Package.swift"

// versionedManifestPrefix begins the tools-version-specific manifests SwiftPM
// reads in preference to `Package.swift` — `Package@swift-5.9.swift`.
const versionedManifestPrefix = "Package@swift-"

// sourceRoots are the directory names SwiftPM searches for a target's sources.
// They are the manager's own predefined list, and a path that begins with none
// of them falls back to its top-level directory — a namespace that separates,
// which is all a namespace is required to do.
var sourceRoots = map[string]bool{
	"Sources": true, "Source": true, "src": true, "srcs": true, "Tests": true,
}

// swiftModule is the module the standard library is reached through, and the
// name every implicitly-visible type lives under.
const swiftModule = "Swift"

// namespaceOf renders module segments as a SCIP namespace descriptor:
// slash-separated and slash-terminated. No segments renders "", which is the
// root module and is a real namespace rather than a missing one.
func namespaceOf(segs []string) string {
	if len(segs) == 0 {
		return ""
	}
	return strings.Join(segs, "/") + "/"
}

// moduleName is what a module occurrence is called in the `name` column: the
// last segment of its namespace, or — for the root module, which has no
// namespace at all — the package's own name.
func moduleName(c coord.Coord, ns string) string {
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

// isTypeName reports whether a name names a type rather than a value, by Swift's
// naming convention: types UpperCamelCase, everything else lowerCamelCase. The
// API Design Guidelines state it and the whole ecosystem follows it, which is
// what makes it reliable enough to build a descriptor on.
func isTypeName(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r)
}

// isNestedTypeName reports whether a member name written after a type names a
// nested type rather than a constant of it. Both start uppercase, and the
// conventions separate them by what follows: a type is UpperCamelCase and a
// constant is ALL_CAPS with underscores.
func isNestedTypeName(s string) bool {
	return isTypeName(s) && strings.ToUpper(s) != s
}

// isImplicitClosureParameter reports whether a name is a closure's positional
// shorthand — `$0`, `$1` — which is the one binding in Swift that is declared
// nowhere. Without this it would fall through to "a lowercase name that resolves
// to nothing", which renders it as a property of this module: a descriptor that
// matches nothing, but that says something false about what `$0` is.
func isImplicitClosureParameter(s string) bool {
	if len(s) < 2 || s[0] != '$' {
		return false
	}
	for _, r := range s[1:] {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// accessorBindings are the names Swift binds inside a setter or an observer
// without any declaration. They are soft keywords: the grammar parses them as
// ordinary identifiers.
var accessorBindings = map[string]bool{"newValue": true, "oldValue": true}

// platformModules are the module names that are never this artifact's.
//
// It is what moduleTarget needs and what Swift's syntax cannot supply: a module
// name carries no artifact identity, so `Greeter` is this repository's or a
// dependency's with nothing in the source to say which. These are the modules
// the platform ships — the Swift standard library, the corelibs, and the Apple
// frameworks a Swift program most often imports — and a third-party dependency
// missing from the set is treated as this artifact's, which yields a namespace
// with no definitions in it: an unresolved reference rather than a wrong edge.
var platformModules = map[string]bool{
	swiftModule: true,
	// The standard library's siblings and the corelibs.
	"Foundation": true, "FoundationNetworking": true, "FoundationXML": true,
	"Dispatch": true, "ObjectiveC": true, "Darwin": true, "Glibc": true,
	"Musl": true, "WinSDK": true, "os": true, "Observation": true,
	"Synchronization": true, "Distributed": true, "RegexBuilder": true,
	"XCTest": true, "Testing": true, "Combine": true,
	// The frameworks a Swift application imports by name.
	"SwiftUI": true, "UIKit": true, "AppKit": true, "WatchKit": true,
	"CoreData": true, "CoreGraphics": true, "CoreFoundation": true,
	"CoreImage": true, "CoreLocation": true, "CoreML": true, "CoreText": true,
	"QuartzCore": true, "AVFoundation": true, "MapKit": true, "WebKit": true,
	"Network": true, "CryptoKit": true, "Security": true, "Accelerate": true,
	"Metal": true, "MetalKit": true, "SpriteKit": true, "SceneKit": true,
	"GameplayKit": true, "HealthKit": true, "HomeKit": true, "StoreKit": true,
	"Photos": true, "PhotosUI": true, "Contacts": true, "EventKit": true,
	"CloudKit": true, "Vision": true, "NaturalLanguage": true, "ARKit": true,
	"RealityKit": true, "Charts": true, "SwiftData": true,
}

// stdlibTypes are the standard-library types Swift puts in every file without an
// import. References to them carry a foreign coordinate and so never pollute
// descriptor matching within the indexed artifact.
//
// The Swift standard library is one flat module, so unlike Kotlin's these need
// no namespace beside them — the set is a membership test and nothing more. An
// omission costs one reference landing in this artifact's module namespace with
// nothing to match, which is what an unrecognised name gets anyway.
var stdlibTypes = map[string]bool{
	"Any": true, "AnyObject": true, "AnyHashable": true, "Never": true, "Void": true,
	"Bool": true, "Int": true, "Int8": true, "Int16": true, "Int32": true, "Int64": true,
	"UInt": true, "UInt8": true, "UInt16": true, "UInt32": true, "UInt64": true,
	"Float": true, "Double": true, "Character": true, "String": true, "Substring": true,
	"StaticString": true, "Unicode": true,
	"Array": true, "ArraySlice": true, "Dictionary": true, "Set": true, "Optional": true,
	"Range": true, "ClosedRange": true, "Result": true, "Slice": true,
	"Sequence": true, "Collection": true, "IteratorProtocol": true,
	"Equatable": true, "Hashable": true, "Comparable": true, "Identifiable": true,
	"Codable": true, "Encodable": true, "Decodable": true, "Error": true,
	"CustomStringConvertible": true, "CustomDebugStringConvertible": true,
	"RawRepresentable": true, "CaseIterable": true, "Sendable": true,
	"ExpressibleByStringLiteral": true, "ExpressibleByIntegerLiteral": true,
	"Task": true, "MainActor": true, "AsyncSequence": true, "AsyncStream": true,
}
