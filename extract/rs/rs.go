// Package rs is the Rust stanza: the tree-sitter query in query.scm plus the
// mapper here that turns its captures into core facts (SPEC.md §5).
//
// The package is named for the extension, per SPEC.md §12. It imports facts and
// coord and deliberately not extract: it satisfies extract.Parser structurally,
// which is what keeps the registry free of an import cycle.
//
// The mapper's job, and its limits, are the Go, TypeScript and Python stanzas'
// (§4.3): it builds the descriptor *suffix* from the CST and the module
// namespace the coordinate supplies; it assigns role and neutral-core symbol
// kind; and it resolves references whose target definition is in the same file.
// It does no type checking, runs no name resolution algorithm and looks at no
// other file. A reference it cannot pin down is still emitted, carrying the best
// descriptor syntax allows, and the link pass decides what it means (§7). Where
// a component is genuinely unknowable file-locally the descriptor writes SCIP's
// "." for it, so it names an unresolved symbol rather than false-matching a real
// one.
//
// # The unit of modularity
//
// Go's is the directory, TypeScript's the file, Python's the file with
// `__init__.py` collapsing to its directory. Rust's is none of the three: a
// crate's module tree is built out of `mod` items, which *need not* follow the
// filesystem — `mod foo { … }` nests a whole module inside the file it is
// written in, and `mod foo;` says the module is a sibling file.
//
// The choice here is that the tree has two producers and the mapper honours
// both:
//
//   - A file's own module is its path, with `src/` dropped, the `.rs` dropped,
//     a trailing `mod` segment collapsed to its directory, and `lib` or `main`
//     collapsed when it is the whole of what is left (moduleNamespace). Those
//     are the module-root file names Cargo defines — `mod.rs` at any depth, a
//     crate root only at the top — and collapsing them is the same rule Python's
//     `__init__.py` and TypeScript's `index` get, for the same reason: the file
//     and the directory have to render one namespace so that the two sides of an
//     import agree.
//   - An inline `mod foo { … }` appends `foo/` to everything inside it, so a
//     module below the file is a real level of the descriptor and not a
//     flattening (moduleSuffix, containerSuffix).
//
// Neither reads the disk (§2.5 forbids it): both are functions of names — the
// path's, and the `mod` item's. `mod foo;` in a file whose module is `a/`
// derives `a/foo/`, and `src/a/foo.rs` derives `a/foo/` from its path, so the
// two agree without either file having seen the other. That is what makes an
// `imports` edge between two Rust files derivable at all.
//
// What it gets right: the standard Cargo layout in every shape — `src/lib.rs`,
// `src/main.rs`, `src/a.rs`, `src/a/mod.rs`, `src/a/b.rs` — inline modules to
// any depth, `crate::` / `self::` / `super::` paths, `use` in all its forms, and
// `mod` declarations in both directions. What it cannot represent:
//
//   - `#[path = "…"]`, which points a `mod` item at an arbitrary file. It is the
//     one construct that makes the module tree genuinely independent of the
//     filesystem, and the declaring file and the target file would have to agree
//     on a name neither of them derives the same way.
//   - A `[lib] path` or `[[bin]] path` override in Cargo.toml. The `src/` prefix
//     is dropped by name, so a crate rooted elsewhere derives namespaces one
//     level off. The fix is not the stanza's: which directory is the crate root
//     is manifest knowledge, so it belongs in coord beside the name and the
//     version — exactly where the Python stanza's src-layout note puts it.
//   - `src/lib.rs` and `src/main.rs` in one package. They are two crate roots of
//     one package and both collapse to the package's root namespace, so two
//     top-level items of the same name — one per file — would render one
//     descriptor. Cargo makes them separate crates; the coordinate has one
//     package per manifest and cannot say which crate a file belongs to.
//   - Which of two same-named top-level path segments is meant. Rust 2018's
//     uniform paths make `use foo::Bar` legal both for an extern crate `foo` and
//     for a local module `foo`, so a top-level name is an extern crate when it
//     is one of rustExternCrates' and this crate's otherwise — which is Rust's
//     own resolution order with the dependency graph taken on trust. This is
//     Python's `import os` versus `import mypkg` problem verbatim.
//
// # What a macro hides
//
// One limit is Rust's alone and is worth stating plainly: a macro invocation's
// arguments are an unparsed token tree. `println!("{}", g.greet())` contains no
// `call_expression` — the grammar sees `identifier g`, `identifier greet` and a
// token tree — so no reference is emitted for anything written inside a macro.
// The stanza does not guess at those tokens: a reference invented from an
// unparsed token stream is exactly the kind of wrong edge §7's descriptor join
// would then materialise. Expanding macros needs the compiler, which is the
// overlay of §4.2 and not a file-local extractor.
package rs

import (
	_ "embed"
	"errors"
	"fmt"
	"path"
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

// Lang is the value written to file.lang for the files this stanza handles.
const Lang = "rs"

// Ext is the file extension this stanza is registered under.
const Ext = ".rs"

//go:embed query.scm
var queryScheme string

// Parser is the Rust stanza. Safe for concurrent use: the grammar and compiled
// query are immutable after the first Parse, and each parse checks a
// gotreesitter parser out of a pool.
type Parser struct {
	once    sync.Once
	lang    *sitter.Language
	pool    *sitter.ParserPool
	query   *sitter.Query
	initErr error
}

// New returns the Rust parser. It is cheap: the grammar is loaded and query.scm
// compiled on the first Parse, so a binary that never parses Rust never
// decompresses the Rust grammar — and a query that fails to compile lands in
// ParseError rather than panicking at init.
func New() *Parser { return &Parser{} }

func (p *Parser) init() {
	p.once.Do(func() {
		p.lang = grammars.RustLanguage()
		if p.lang == nil {
			p.initErr = errors.New("rs: gotreesitter has no Rust grammar")
			return
		}
		q, err := sitter.NewQuery(queryScheme, p.lang)
		if err != nil {
			p.initErr = fmt.Errorf("rs: compile query.scm: %w", err)
			return
		}
		p.query = q
		p.pool = sitter.NewParserPool(p.lang)
	})
}

// Parse extracts one Rust file's facts. It never returns an error: a failure is
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
		ns:         moduleNamespace(repoRelative(c.Root, filePath)),
		out:        facts.FileFacts{File: file},
		scopeByID:  map[facts.LocalID]scopeRec{},
		namespaces: map[string]moduleRec{},
		bindings:   map[string]bindingRec{},
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
	typeName string // syntactic type of a binding/parameter; "" when unknown
}

// moduleRec is a module a path named: the coordinate that owns it and the
// namespace descriptor of the module within that coordinate.
type moduleRec struct {
	coord coord.Coord
	ns    string
}

// bindingRec is a name a `use` declaration bound to one *item* of another
// module. It is TypeScript's and Python's named-import problem verbatim: the
// local name and the name the exporting module knows the item by differ under an
// `as` alias, and the descriptor must carry the latter.
type bindingRec struct {
	moduleRec
	// orig is the name the defining module knows the item by, which is the name
	// its own definition's descriptor was built from.
	orig string
}

type builder struct {
	lang  *sitter.Language
	src   []byte
	coord coord.Coord
	// ns is this file's module namespace: the descriptor prefix of every symbol
	// it defines at the file's top level. An inline `mod` adds to it.
	ns string

	out       facts.FileFacts
	nextScope facts.LocalID
	nextOcc   facts.LocalID

	scopes    []scopeRec
	scopeByID map[facts.LocalID]scopeRec

	// namespaces holds path segments that name a whole module (`mod foo;`,
	// `use a::b;`), bindings the ones that name a single item in one
	// (`use a::B;`).
	namespaces map[string]moduleRec
	bindings   map[string]bindingRec

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
// collectScopes sorts by (start ascending, end descending) and the `source_file`
// node spans everything, so it is always the first scope emitted.
func (b *builder) fileScope() facts.LocalID {
	if len(b.scopes) == 0 {
		return facts.NoID
	}
	return b.scopes[0].id
}

// ------------------------------------------------------------------ module ---

// collectModule emits the definition of the module the file *is*.
//
// Go gets this from a `package` clause; TypeScript, Python and Rust have none,
// so the query captures the whole file (`(source_file) @definition.package`) and
// the name comes from the path. Everything else about it is the Go stanza's
// package definition: neutral-core `package` kind, descriptor equal to the
// namespace, and a `defines` edge from the file — which together are exactly
// what link's `imports` derivation joins a `mod` or `use` reference against.
//
// The occurrence is zero-width at the start of the file, for the reason the
// TypeScript stanza gives: there is no identifier to point at, and a range
// spanning the whole file would claim every other occurrence sits inside the
// module's *name*.
func (b *builder) collectModule(matches []sitter.QueryMatch) {
	for _, m := range matches {
		root, _, ok := roots(m, "definition.package")
		if !ok || root.Node.Type(b.lang) != "source_file" {
			continue
		}
		// Explicitly the file scope, not the innermost scope containing [0, 0).
		// A Rust file may open with `mod inner {` on its first byte, and then
		// the module scope also starts at 0 and is the innermost — which would
		// put the file's own module definition lexically inside one of its
		// submodules.
		desc := facts.Descriptor{Prefix: b.coord, Suffix: b.ns}
		occ := b.addOccurrenceIn(b.fileScope(), desc, facts.RoleDefinition, facts.KindPackage, moduleName(b.coord, b.ns), 0, 0)
		b.edge(facts.EdgeDefines, facts.FileRef(), facts.OccurrenceRef(occ))
		if _, dup := b.descIndex[desc.String()]; !dup {
			b.descIndex[desc.String()] = occ
		}
		return
	}
}

// ----------------------------------------------------------------- imports ---

// collectImports records what `use`, `mod` and `extern crate` bind, and emits
// one module occurrence per module named.
//
// Import *edges* are cross-file and therefore the link pass's (§4.4); what is
// extracted is the occurrence, whose descriptor names the imported module. For a
// module that resolves inside this crate that descriptor is byte-identical to
// the one the target file's own module definition produces, which is what lets
// link derive `imports` by descriptor join.
//
// Every identifier the statement consumed is claimed, so the `::` reference
// patterns — which match happily inside a `use` — do not emit a second, weaker
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
		switch cd.stmt.Type(b.lang) {
		case "use_declaration":
			b.claimSubtree(cd.name)
			b.useTree(cd.name, nil)
		case "mod_item":
			// The inline form is a definition, not an import; it is captured by
			// the pattern with a `body` field and handled in
			// collectDefinitions.
			if cd.stmt.ChildByFieldName("body", b.lang) != nil {
				continue
			}
			b.claimSubtree(cd.name)
			b.modDeclaration(cd.stmt, cd.name)
		case "extern_crate_declaration":
			b.claimSubtree(cd.name)
			b.externCrate(cd.name)
		}
	}
}

// modDeclaration handles `mod foo;`: the module lives in another file, and the
// occurrence emitted here is what an `imports` edge to that file is derived
// from. The module is a child of *this* file's module, which is what makes the
// two sides agree — `src/a/b.rs` derives `a/b/` from its path, and `mod b;`
// written in `src/a.rs` derives `a/` + `b/`.
func (b *builder) modDeclaration(stmt, name *sitter.Node) {
	local := b.text(name)
	if local == "" {
		return
	}
	ns := b.moduleSuffix(stmt) + local + "/"
	b.namespaces[local] = moduleRec{coord: b.coord, ns: ns}
	b.addOccurrence(
		facts.Descriptor{Prefix: b.coord, Suffix: ns},
		facts.RoleReference, facts.KindPackage, local,
		name.StartByte(), name.EndByte(),
	)
}

// externCrate handles `extern crate legacy;`, the 2015-edition way to name a
// dependency. The crate is outside this coordinate by construction, so it gets a
// foreign one and can never match anything indexed here.
func (b *builder) externCrate(name *sitter.Node) {
	local := b.text(name)
	if local == "" {
		return
	}
	c := coord.Foreign(b.coord.Scheme, b.coord.Manager, local)
	b.namespaces[local] = moduleRec{coord: c}
	b.addOccurrence(
		facts.Descriptor{Prefix: c, Suffix: ""},
		facts.RoleReference, facts.KindPackage, local,
		name.StartByte(), name.EndByte(),
	)
}

// useTree walks a `use` declaration's argument, which is a tree rather than a
// path: `use a::{b::C, d::*};` names three things in one statement. base is the
// module the enclosing `{…}` list resolved to, or nil at the top of the tree.
func (b *builder) useTree(n *sitter.Node, base *pathTarget) {
	if n == nil {
		return
	}
	switch n.Type(b.lang) {
	case "scoped_identifier":
		p := n.ChildByFieldName("path", b.lang)
		local := b.fieldText(n, "name")
		mod, ok := b.pathIn(p, base)
		if !ok || local == "" {
			return
		}
		b.emitModule(p, mod)
		b.bindUse(local, local, mod)

	case "use_as_clause":
		p := n.ChildByFieldName("path", b.lang)
		alias := b.fieldText(n, "alias")
		orig, mod, ok := b.useItem(p, base)
		if !ok || alias == "" {
			return
		}
		b.bindUse(alias, orig, mod)

	case "scoped_use_list":
		p := n.ChildByFieldName("path", b.lang)
		mod, ok := b.pathIn(p, base)
		if !ok {
			return
		}
		b.emitModule(p, mod)
		b.useList(n.ChildByFieldName("list", b.lang), &mod)

	case "use_list":
		b.useList(n, base)

	case "use_wildcard":
		// `use a::b::*;` names the module and binds nothing nameable, which is
		// the glob import's whole problem: the names it brings into scope are
		// the *other* file's to state.
		if child := firstNamedChild(n); child != nil {
			if mod, ok := b.pathIn(child, base); ok {
				b.emitModule(child, mod)
			}
		}

	case "identifier":
		local := b.text(n)
		if local == "" {
			return
		}
		if base == nil {
			// `use foo;` — a whole crate or top-level module, named and bound.
			mod, ok := b.pathIn(n, nil)
			if !ok {
				return
			}
			b.emitModule(n, mod)
			if mod.module {
				b.namespaces[local] = moduleRec{coord: mod.coord, ns: mod.suffix}
			}
			return
		}
		b.bindUse(local, local, *base)

	case "self":
		// `use a::b::{self, C};` binds the module `a::b` under its own last
		// name.
		if base != nil {
			if last := lastSegment(strings.TrimSuffix(base.suffix, "/")); last != "" {
				b.namespaces[last] = moduleRec{coord: base.coord, ns: base.suffix}
			}
		}
	}
}

func (b *builder) useList(list *sitter.Node, base *pathTarget) {
	if list == nil {
		return
	}
	for i := 0; i < list.NamedChildCount(); i++ {
		b.useTree(list.NamedChild(i), base)
	}
}

// useItem resolves the path half of a `… as alias` clause to the item it names
// and the module holding it.
func (b *builder) useItem(n *sitter.Node, base *pathTarget) (orig string, mod pathTarget, ok bool) {
	if n == nil {
		return "", pathTarget{}, false
	}
	switch n.Type(b.lang) {
	case "scoped_identifier":
		p := n.ChildByFieldName("path", b.lang)
		orig = b.fieldText(n, "name")
		mod, ok = b.pathIn(p, base)
		if ok && orig != "" {
			b.emitModule(p, mod)
			return orig, mod, true
		}
	case "identifier":
		orig = b.text(n)
		if base != nil && orig != "" {
			return orig, *base, true
		}
	}
	return "", pathTarget{}, false
}

// bindUse records one name a `use` brought into scope. A lowercase name is also
// recorded as a module: Rust's naming convention — enforced by the compiler's
// own `non_snake_case` and `non_camel_case_types` lints — is the only thing that
// separates `use a::b;` naming a module from `use a::B;` naming a type, since
// the syntax is identical. Taking it on trust is the same trade the Python
// stanza makes for `self`/`cls`, and the cost of being wrong is a descriptor
// that matches nothing rather than one that matches the wrong thing.
func (b *builder) bindUse(local, orig string, mod pathTarget) {
	b.bindings[local] = bindingRec{moduleRec: moduleRec{coord: mod.coord, ns: mod.suffix}, orig: orig}
	if !isTypeName(orig) {
		b.namespaces[local] = moduleRec{coord: mod.coord, ns: mod.suffix + orig + "/"}
	}
}

// emitModule writes the `package` reference occurrence a path's module half
// produces — the row link's `imports` derivation joins against.
func (b *builder) emitModule(n *sitter.Node, mod pathTarget) {
	if n == nil || !mod.module {
		return
	}
	b.addOccurrence(
		facts.Descriptor{Prefix: mod.coord, Suffix: mod.suffix},
		facts.RoleReference, facts.KindPackage, moduleName(mod.coord, mod.suffix),
		n.StartByte(), n.EndByte(),
	)
}

// ------------------------------------------------------------------- paths ---

// pathTarget is what a `::`-separated path resolved to: the coordinate and
// descriptor suffix its members hang off, and whether it is a whole module
// rather than a type — the difference between `std::fmt::format` naming a
// function of a module and `Greeter::new` naming an associated function of a
// type.
type pathTarget struct {
	coord  coord.Coord
	suffix string
	module bool
}

// pathIn resolves a path node to the module or type it names. base is the
// module an enclosing `use` list already resolved, or nil when the path starts
// from nothing.
func (b *builder) pathIn(n *sitter.Node, base *pathTarget) (pathTarget, bool) {
	if n == nil {
		if base != nil {
			return *base, true
		}
		return pathTarget{}, false
	}
	switch n.Type(b.lang) {
	case "scoped_identifier", "scoped_type_identifier":
		parent, ok := b.pathIn(n.ChildByFieldName("path", b.lang), base)
		if !ok {
			return pathTarget{}, false
		}
		seg := b.fieldText(n, "name")
		if seg == "" {
			return pathTarget{}, false
		}
		return extend(parent, seg), true

	case "identifier", "type_identifier":
		seg := b.text(n)
		if seg == "" {
			return pathTarget{}, false
		}
		if base != nil {
			return extend(*base, seg), true
		}
		return b.firstSegment(n, seg), true

	case "crate":
		// The crate root, which is this coordinate's own namespace root.
		return pathTarget{coord: b.coord, module: true}, true

	case "self":
		return pathTarget{coord: b.coord, suffix: b.moduleSuffix(n), module: true}, true

	case "super":
		return pathTarget{coord: b.coord, suffix: parentNamespace(b.moduleSuffix(n)), module: true}, true

	case "generic_type", "bracketed_type":
		return b.pathIn(n.ChildByFieldName("type", b.lang), base)
	}
	return pathTarget{}, false
}

// firstSegment resolves the leading segment of a path, which is the only one
// whose meaning is not fixed by what came before it.
//
// There is no third case beyond "a dependency" and "this crate", and no syntax
// to build one from. Rust 2018's uniform paths make `use foo::Bar` legal whether
// `foo` is an extern crate or a module of this one; only Cargo.toml's dependency
// table tells them apart, and reading it here would be neither file-local (§2.5)
// nor enough — a dependency is renameable. So the rule is "a dependency if the
// name is a known one, this crate otherwise", and an unrecognised third-party
// crate lands at a namespace of this crate that holds no definitions, which is
// an unresolved reference and not a wrong edge.
func (b *builder) firstSegment(n *sitter.Node, name string) pathTarget {
	if mod, ok := b.namespaces[name]; ok {
		return pathTarget{coord: mod.coord, suffix: mod.ns, module: true}
	}
	if bnd, ok := b.bindings[name]; ok {
		return pathTarget{coord: bnd.coord, suffix: bnd.ns + bnd.orig + "#"}
	}
	if rustExternCrates[name] {
		return pathTarget{coord: coord.Foreign(b.coord.Scheme, b.coord.Manager, name), module: true}
	}
	if rustPrelude[name] {
		return pathTarget{coord: b.builtin(), suffix: name + "#"}
	}
	if isTypeName(name) {
		return pathTarget{coord: b.coord, suffix: b.moduleSuffix(n) + name + "#"}
	}
	return pathTarget{coord: b.coord, suffix: b.moduleSuffix(n) + name + "/", module: true}
}

// extend appends one path segment to an already-resolved prefix. A segment of a
// module is a module or an item of it, by the same naming convention bindUse
// reads; a segment of a type is an associated item.
func extend(base pathTarget, seg string) pathTarget {
	if !base.module {
		return pathTarget{coord: base.coord, suffix: base.suffix + seg + "#"}
	}
	if isTypeName(seg) {
		return pathTarget{coord: base.coord, suffix: base.suffix + seg + "#"}
	}
	return pathTarget{coord: base.coord, suffix: base.suffix + seg + "/", module: true}
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
		if !ok || name == nil {
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
		if kind == facts.KindPackage {
			b.namespaces[name] = moduleRec{coord: prefix, ns: suffix}
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
// capture name does not. Rust needs it in exactly two places, and both are
// easier than the Python stanza's counterparts: one node type covers a free
// function and a method, and `trait` — unlike a Python class, which had to be
// inspected for a Protocol base — is unambiguous.
func (b *builder) refineKind(kind string, node *sitter.Node) string {
	switch kind {
	case facts.KindFunction:
		// A `fn` directly inside an `impl` or a `trait` body is a method.
		// Nothing in the node says so; the enclosing container does.
		switch b.nearestContainer(node) {
		case "impl", "trait":
			return facts.KindMethod
		}
	case facts.KindType:
		// A trait declares behaviour rather than data, which is what the
		// neutral core's `interface` kind means and what link's `implements`
		// derivation keys off.
		if node.Type(b.lang) == "trait_item" {
			return facts.KindInterface
		}
	}
	return kind
}

// nearestContainer says which kind of named container n sits innermost inside,
// or "" when it is at the file's top level. It is what tells a method from a
// free function.
func (b *builder) nearestContainer(n *sitter.Node) string {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "impl_item":
			return "impl"
		case "trait_item":
			return "trait"
		case "struct_item", "enum_item", "union_item":
			return "type"
		case "function_item", "function_signature_item", "closure_expression":
			return "function"
		case "mod_item":
			return "mod"
		}
	}
	return ""
}

// definitionDescriptor builds the SCIP descriptor for a definition from its
// capture hierarchy (SPEC.md §5).
//
// The *prefix* is a return value and not always this file's coordinate, which is
// the one place Rust's model is wider than Go's. `impl Display for Greeter`
// defines a method on a local type, but `impl MyTrait for Vec<T>` defines one on
// a type this crate does not own — Rust lets a crate add members to a foreign
// type, which neither Go nor TypeScript nor Python can do — and naming that
// method under this crate's coordinate would claim a symbol that is not this
// crate's to claim. The descriptor follows the type, which is what makes it join
// with the members the owning crate declares.
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
// enclosing named container of n — a module, a type, a trait, an `impl` or a
// function — or this file's module namespace when there is none. A block and a
// closure are transparent: neither has a name, so neither has a descriptor of
// its own and what is inside belongs to the enclosing container.
func (b *builder) containerSuffix(n *sitter.Node) (coord.Coord, string) {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "impl_item":
			// An `impl` has no name of its own: its members are the members of
			// the type it is for, which is what makes `implements` derivable
			// (link's method-set containment reads exactly these suffixes).
			return b.implTarget(p, 0)
		case "trait_item", "struct_item", "enum_item", "union_item", "type_item":
			if name := b.fieldText(p, "name"); name != "" {
				c, s := b.containerSuffix(p)
				return c, s + name + "#"
			}
		case "function_item", "function_signature_item":
			if name := b.fieldText(p, "name"); name != "" {
				c, s := b.containerSuffix(p)
				return c, s + name + "()."
			}
		case "mod_item":
			if name := b.fieldText(p, "name"); name != "" && p.ChildByFieldName("body", b.lang) != nil {
				c, s := b.containerSuffix(p)
				return c, s + name + "/"
			}
		}
	}
	return b.coord, b.ns
}

// moduleSuffix returns the namespace of the module n is written in: this file's,
// plus one segment per inline `mod` between the file and n.
func (b *builder) moduleSuffix(n *sitter.Node) string {
	var segs []string
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Type(b.lang) != "mod_item" || p.ChildByFieldName("body", b.lang) == nil {
			continue
		}
		if name := b.fieldText(p, "name"); name != "" {
			segs = append(segs, name)
		}
	}
	if len(segs) == 0 {
		return b.ns
	}
	var sb strings.Builder
	sb.WriteString(b.ns)
	for i := len(segs) - 1; i >= 0; i-- {
		sb.WriteString(segs[i])
		sb.WriteString("/")
	}
	return sb.String()
}

// implTarget names the type an `impl` block is for. depth guards the one shape
// that could recurse, `impl Self`, which is not legal Rust but is cheap to
// refuse.
func (b *builder) implTarget(impl *sitter.Node, depth int) (coord.Coord, string) {
	name := b.unwrapType(impl.ChildByFieldName("type", b.lang))
	if name == "" || depth > 4 {
		return b.coord, b.moduleSuffix(impl) + coord.Unknown + "#"
	}
	return b.resolveTypeName(impl, name, depth+1)
}

// enclosingImpl returns the `impl` block n sits inside, if any. It is what
// resolves `Self` and the receiver `self`.
func (b *builder) enclosingImpl(n *sitter.Node) *sitter.Node {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "impl_item":
			return p
		case "trait_item":
			// Inside a trait, `Self` is the implementing type — unknowable
			// here — but the trait itself is the nearest thing a descriptor can
			// name, and its members are what a `self.` call reaches.
			return p
		}
	}
	return nil
}

// selfTarget is the coordinate and suffix `Self` and the receiver `self` name.
func (b *builder) selfTarget(n *sitter.Node, depth int) (coord.Coord, string, bool) {
	p := b.enclosingImpl(n)
	if p == nil {
		return coord.Coord{}, "", false
	}
	if p.Type(b.lang) == "trait_item" {
		name := b.fieldText(p, "name")
		if name == "" {
			return coord.Coord{}, "", false
		}
		c, s := b.containerSuffix(p)
		return c, s + name + "#", true
	}
	c, s := b.implTarget(p, depth)
	return c, s, true
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
// survives. Role decides it where the roles differ; where they do not, the
// wider structural node wins.
//
// The tie-break is not cosmetic. `(type_identifier) @reference.type` matches the
// `Debug` inside `std::fmt::Debug` just as readily as the qualified pattern
// does, and the two carry the same role — so without a rule the descriptor would
// be whichever match the query engine happened to yield first, `std . fmt/Debug#`
// on one run and a bare `Debug#` in this crate's namespace on another. The wider
// node is the one that saw the qualifier, and it is always the right answer for
// the same reason: it knows strictly more about the identifier than the narrower
// match does.
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
// Rust's two member operators are two different questions, and the descriptor
// says which was asked. `g.greet()` reaches a method of whatever `g` is, so the
// receiver has to be typed — the mapper reads the type off the binding's
// annotation or its initialiser, and writes SCIP's "." for the type when it
// cannot. `Greeter::new()` names an item of a path, and a path is resolvable
// syntactically, so that one is exact whenever the path's first segment is.
func (b *builder) callDescriptor(r refRec, name string) (facts.Descriptor, string) {
	fn := r.node.ChildByFieldName("function", b.lang)
	if fn == nil {
		return facts.Descriptor{Prefix: b.coord, Suffix: b.moduleSuffix(r.nameNode) + name + "()."}, facts.KindFunction
	}

	switch fn.Type(b.lang) {
	case "identifier":
		if bnd, ok := b.bindings[name]; ok {
			return facts.Descriptor{Prefix: bnd.coord, Suffix: bnd.ns + bnd.orig + "()."}, facts.KindFunction
		}
		if rustPrelude[name] {
			return facts.Descriptor{Prefix: b.builtin(), Suffix: name + "()."}, facts.KindFunction
		}
		return facts.Descriptor{Prefix: b.coord, Suffix: b.moduleSuffix(r.nameNode) + name + "()."}, facts.KindFunction

	case "scoped_identifier":
		if q, ok := b.pathIn(fn.ChildByFieldName("path", b.lang), nil); ok {
			kind := facts.KindMethod
			if q.module {
				kind = facts.KindFunction
			}
			return facts.Descriptor{Prefix: q.coord, Suffix: q.suffix + name + "()."}, kind
		}

	case "field_expression":
		if q, ok := b.valueTarget(fn.ChildByFieldName("value", b.lang), r.nameNode.StartByte()); ok {
			return facts.Descriptor{Prefix: q.coord, Suffix: q.suffix + name + "()."}, facts.KindMethod
		}
	}

	// Receiver unknowable file-locally: name it with SCIP's "." for the type so
	// it cannot false-match a real definition.
	return facts.Descriptor{Prefix: b.coord, Suffix: b.moduleSuffix(r.nameNode) + coord.Unknown + "#" + name + "()."}, facts.KindMethod
}

func (b *builder) readDescriptor(r refRec, name string) (facts.Descriptor, string) {
	switch {
	case b.isFieldValue(r):
		// The value half of `a.b`: a binding in scope, or an item a `use`
		// brought in.
		if def, ok := b.lookup(name, r.nameNode.StartByte()); ok {
			return b.occurrence(def.occ).Descriptor, facts.KindVariable
		}
		if bnd, ok := b.bindings[name]; ok {
			return facts.Descriptor{Prefix: bnd.coord, Suffix: bnd.ns + bnd.orig + "."}, facts.KindVariable
		}
		return facts.Descriptor{Prefix: b.coord, Suffix: b.moduleSuffix(r.nameNode) + name + "."}, facts.KindVariable

	case b.isPathQualifier(r):
		// The left half of `a::b`, which names a module or a type and never a
		// value.
		q := b.firstSegment(r.nameNode, name)
		kind := facts.KindType
		if q.module {
			kind = facts.KindPackage
		}
		return facts.Descriptor{Prefix: q.coord, Suffix: q.suffix}, kind

	case r.node.Type(b.lang) == "scoped_identifier":
		if q, ok := b.pathIn(r.node.ChildByFieldName("path", b.lang), nil); ok {
			// A path segment that is itself the prefix of a longer path names a
			// module or a type — `fmt` in `std::fmt::Debug` — and never a value.
			if b.isPathPrefix(r.node) {
				t := extend(q, name)
				kind := facts.KindType
				if t.module {
					kind = facts.KindPackage
				}
				return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix}, kind
			}
			// The right half of `a::b`: an associated item, an enum variant or
			// a module-level value.
			kind := facts.KindField
			if q.module {
				kind = facts.KindVariable
			}
			return facts.Descriptor{Prefix: q.coord, Suffix: q.suffix + name + "."}, kind
		}

	default:
		// The field half of `a.b`.
		if q, ok := b.valueTarget(r.node.ChildByFieldName("value", b.lang), r.nameNode.StartByte()); ok {
			return facts.Descriptor{Prefix: q.coord, Suffix: q.suffix + name + "."}, facts.KindField
		}
	}
	return facts.Descriptor{Prefix: b.coord, Suffix: b.moduleSuffix(r.nameNode) + coord.Unknown + "#" + name + "."}, facts.KindField
}

func (b *builder) typeReferenceDescriptor(r refRec, name string) (facts.Descriptor, string) {
	// `std::fmt::Debug` is one type reference whose name is `Debug` and whose
	// meaning is in the qualifier, so the qualified form resolves the path
	// rather than the name.
	if r.node.Type(b.lang) == "scoped_type_identifier" {
		if q, ok := b.pathIn(r.node.ChildByFieldName("path", b.lang), nil); ok {
			t := extend(q, name)
			return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix}, facts.KindType
		}
	}
	c, suffix := b.resolveTypeName(r.nameNode, name, 0)
	return facts.Descriptor{Prefix: c, Suffix: suffix}, facts.KindType
}

// resolveTypeName names a type by the coordinate and descriptor suffix it lives
// at. The name may be `::`-qualified when it came from an annotation, and it may
// be `Self`, which is the one type name whose meaning is positional.
func (b *builder) resolveTypeName(n *sitter.Node, typeName string, depth int) (coord.Coord, string) {
	if typeName == selfType && depth <= 4 {
		if c, s, ok := b.selfTarget(n, depth+1); ok {
			return c, s
		}
	}
	if qualifier, bare, qualified := strings.Cut(typeName, "::"); qualified {
		base := b.firstSegment(n, qualifier)
		for {
			seg, rest, more := strings.Cut(bare, "::")
			base = extend(base, seg)
			if !more {
				break
			}
			bare = rest
		}
		return base.coord, base.suffix
	}
	if bnd, ok := b.bindings[typeName]; ok {
		return bnd.coord, bnd.ns + bnd.orig + "#"
	}
	if rustPrelude[typeName] || rustPrimitives[typeName] {
		return b.builtin(), typeName + "#"
	}
	return b.coord, b.moduleSuffix(n) + typeName + "#"
}

// ------------------------------------------------------------ local lookup ---

// valueTarget resolves the value half of a `.` access to the type its members
// hang off. Only `.` reaches a member of a value in Rust — `::` resolves a path,
// and the two are never interchangeable — so this is the one place a *type* has
// to be recovered rather than read off the syntax.
func (b *builder) valueTarget(value *sitter.Node, pos uint32) (pathTarget, bool) {
	if value == nil {
		return pathTarget{}, false
	}
	switch value.Type(b.lang) {
	case "self":
		if c, s, ok := b.selfTarget(value, 0); ok {
			return pathTarget{coord: c, suffix: s}, true
		}
	case "identifier":
		name := b.text(value)
		if typeName, ok := b.localTypeAt(name, pos); ok && typeName != "" {
			c, s := b.resolveTypeName(value, typeName, 0)
			return pathTarget{coord: c, suffix: s}, true
		}
		if bnd, ok := b.bindings[name]; ok && isTypeName(bnd.orig) {
			return pathTarget{coord: bnd.coord, suffix: bnd.ns + bnd.orig + "#"}, true
		}
	}
	return pathTarget{}, false
}

// isFieldValue reports whether the captured identifier is the value half of a
// `.` access rather than the field name.
func (b *builder) isFieldValue(r refRec) bool {
	if r.node.Type(b.lang) != "field_expression" {
		return false
	}
	value := r.node.ChildByFieldName("value", b.lang)
	return value != nil && value.StartByte() == r.nameNode.StartByte()
}

// isPathQualifier reports whether the captured identifier is the left half of a
// `::` path rather than the item it names.
func (b *builder) isPathQualifier(r refRec) bool {
	if r.node.Type(b.lang) != "scoped_identifier" {
		return false
	}
	p := r.node.ChildByFieldName("path", b.lang)
	return p != nil && p.StartByte() == r.nameNode.StartByte()
}

// isPathPrefix reports whether n is itself the qualifier of a longer path, which
// is what makes `std::fmt` in `std::fmt::Debug` a module rather than an item.
func (b *builder) isPathPrefix(n *sitter.Node) bool {
	parent := n.Parent()
	if parent == nil {
		return false
	}
	p := parent.ChildByFieldName("path", b.lang)
	return p != nil && p.StartByte() == n.StartByte() && p.EndByte() == n.EndByte()
}

// lookup finds the definition named name that is visible at byte offset pos:
// among definitions whose declaring scope contains pos, the one in the innermost
// such scope, declared no later than pos.
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

func (b *builder) localTypeAt(name string, pos uint32) (string, bool) {
	d, ok := b.lookup(name, pos)
	if !ok {
		return "", false
	}
	return d.typeName, true
}

// declaredType recovers the syntactic type of a binding, parameter, field or
// constant — enough to name the members reached through it, without any type
// inference. "" means unknown, which downstream becomes SCIP's "." rather than a
// guess.
func (b *builder) declaredType(node *sitter.Node) string {
	switch node.Type(b.lang) {
	case "let_declaration":
		if t := b.unwrapType(node.ChildByFieldName("type", b.lang)); t != "" {
			return t
		}
		return b.inferredType(node.ChildByFieldName("value", b.lang))
	case "parameter", "const_item", "static_item", "field_declaration":
		return b.unwrapType(node.ChildByFieldName("type", b.lang))
	}
	return ""
}

// unwrapType reduces a type expression to the bare (possibly `::`-qualified)
// name a descriptor can use. A reference, a pointer and a generic application
// are all transparent: `&mut Vec<T>` is named by `Vec`, because the members
// reached through it are `Vec`'s. Composite types with no single name — a
// tuple, a slice, a `dyn Trait + Send`, an `impl Trait` — yield "".
func (b *builder) unwrapType(t *sitter.Node) string {
	for depth := 0; t != nil && depth < 16; depth++ {
		switch t.Type(b.lang) {
		case "reference_type", "pointer_type", "generic_type", "bracketed_type":
			t = t.ChildByFieldName("type", b.lang)
		case "type_identifier", "scoped_type_identifier", "primitive_type":
			return b.text(t)
		default:
			return ""
		}
	}
	return ""
}

// inferredType reads a type off an initialising expression. Only the forms whose
// type is written in the source are handled — a struct literal, and an
// associated function called through a type path — and the second is a
// convention rather than a rule: `Greeter::new(…)` returning a `Greeter` is what
// Rust's own API guidelines prescribe, but nothing enforces it.
//
// Being wrong is safe rather than merely unlikely, for the reason the Python
// stanza gives: a type's descriptor ends `#` and a callable's ends `().`, so a
// guess in either direction matches no definition instead of the wrong one.
func (b *builder) inferredType(expr *sitter.Node) string {
	for depth := 0; expr != nil && depth < 16; depth++ {
		switch expr.Type(b.lang) {
		case "reference_expression", "unary_expression", "parenthesized_expression", "try_expression", "await_expression":
			expr = firstNamedChild(expr)
		case "struct_expression":
			return b.fieldText(expr, "name")
		case "call_expression":
			fn := expr.ChildByFieldName("function", b.lang)
			if fn == nil || fn.Type(b.lang) != "scoped_identifier" {
				return ""
			}
			// `Greeter::new(…)` names the type; `helpers::build(…)` names a
			// module and says nothing about the result.
			p := fn.ChildByFieldName("path", b.lang)
			name := b.pathText(p)
			if name == "" || !isTypeName(lastPathSegment(name)) {
				return ""
			}
			return name
		default:
			return ""
		}
	}
	return ""
}

// pathText renders a path node back to its source text, which is what
// resolveTypeName consumes.
func (b *builder) pathText(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	switch n.Type(b.lang) {
	case "identifier", "type_identifier", "scoped_identifier", "scoped_type_identifier":
		return b.text(n)
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

// claimSubtree marks every identifier under n as owned, so the `::` and `.`
// reference patterns — which match inside a `use` declaration as readily as
// anywhere else — do not emit a second occurrence over bytes an import already
// described.
func (b *builder) claimSubtree(n *sitter.Node) {
	if n == nil {
		return
	}
	switch n.Type(b.lang) {
	case "identifier", "type_identifier", "field_identifier":
		b.claimed[span{n.StartByte(), n.EndByte()}] = true
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		b.claimSubtree(n.NamedChild(i))
	}
}

// builtin is the coordinate of Rust's prelude, which belongs to no crate this
// index owns. `std` is the crate the prelude is reached through.
func (b *builder) builtin() coord.Coord {
	return coord.Foreign(b.coord.Scheme, b.coord.Manager, "std")
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

// selfType is Rust's positional type name: inside an `impl`, it is the type
// being implemented.
const selfType = "Self"

// isTypeName reports whether a path segment names a type rather than a module or
// a function, by Rust's naming convention: types are UpperCamelCase and
// everything else is snake_case. The compiler's own `non_camel_case_types` and
// `non_snake_case` lints are what make this reliable enough to build a
// descriptor on; see bindUse.
func isTypeName(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r)
}

func lastPathSegment(p string) string {
	if i := strings.LastIndex(p, "::"); i >= 0 {
		return p[i+2:]
	}
	return p
}

// srcDir is Cargo's source directory, the one path segment that is layout and
// not module structure.
const srcDir = "src"

// rootModules are the file names Cargo gives a module root: a crate root
// (`lib.rs`, `main.rs`) or a directory module (`mod.rs`). Each *is* the module
// its directory is, so it collapses to the directory — the same rule Python's
// `__init__.py` and TypeScript's `index` get.
const (
	modModule  = "mod"
	libModule  = "lib"
	mainModule = "main"
)

// repoRelative renders filePath relative to the package root, slash-separated.
// It is "" when there is no root or the file is outside it, which makes every
// namespace below empty — the honest answer when nothing says where the crate
// begins.
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

// moduleNamespace turns a package-relative file path into a SCIP namespace
// descriptor: slash-separated and slash-terminated.
//
// Three normalisations, and each exists so that the two sides of a `mod`
// declaration agree without either reading the disk. The `.rs` goes because a
// `mod` item names a module and never a file name. A leading `src/` goes because
// it is Cargo's layout rather than a level of the module tree — `mod a;` in
// `src/lib.rs` means the module `a`, not `src::a`. And a module-root file name
// goes because such a file *is* the module its directory is, so the file and the
// directory have to render one namespace.
//
// The last one is two rules rather than one, because Cargo's are: `mod.rs` makes
// a directory a module at any depth, while `lib.rs` and `main.rs` are crate
// roots and only at the top. So `src/a/mod.rs` collapses to `a/` and
// `src/a/main.rs` does not collapse at all — it is not a module root, and
// pretending it were would hand it the namespace `a/` that `src/a.rs` or
// `src/a/mod.rs` is entitled to.
func moduleNamespace(filePath string) string {
	if filePath == "" {
		return ""
	}
	p := path.Clean(filePath)
	if p == "." || p == "/" {
		return ""
	}
	p = strings.TrimSuffix(p, Ext)
	p = strings.TrimPrefix(p, srcDir+"/")
	p = strings.TrimSuffix(p, "/"+modModule)
	switch p {
	case "", ".", modModule, libModule, mainModule:
		return ""
	}
	return p + "/"
}

// parentNamespace climbs one module out of a namespace descriptor, which is what
// `super::` does. The crate root has no parent and stays put — `super` there is
// not legal Rust, and inventing a level above the root would name a module
// outside this coordinate.
func parentNamespace(ns string) string {
	trimmed := strings.TrimSuffix(ns, "/")
	if trimmed == "" {
		return ""
	}
	dir := path.Dir(trimmed)
	if dir == "." || dir == "/" {
		return ""
	}
	return dir + "/"
}

// moduleName is what a module occurrence is called in the `name` column: the
// last segment of its namespace, or — for the crate root, which has no namespace
// of its own — the last segment of the crate's name.
func moduleName(c coord.Coord, ns string) string {
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

// rustExternCrates is the set of crate names that are never this crate.
//
// It is what firstSegment needs and what Rust's syntax cannot supply: since the
// 2018 edition's uniform paths, `use foo::Bar` is legal whether `foo` is a
// dependency or a module of this crate, so the only way to know that a leading
// segment leaves the crate is to know the name. The set is the toolchain's own
// crates, which are the ones every Rust file may name without declaring them; a
// third-party dependency missing from it is treated as this crate's, which
// yields a namespace with no definitions in it — an unresolved reference rather
// than a wrong edge.
var rustExternCrates = map[string]bool{
	"std": true, "core": true, "alloc": true, "proc_macro": true, "test": true,
}

// rustPrelude is the commonly used subset of the names Rust's prelude puts in
// every scope. References to them carry a foreign coordinate and so never
// pollute descriptor matching within the indexed crate. An omission costs one
// reference landing in this crate's namespace with nothing to match, which is
// what an unrecognised name gets anyway.
var rustPrelude = map[string]bool{
	"Option": true, "Some": true, "None": true,
	"Result": true, "Ok": true, "Err": true,
	"String": true, "Vec": true, "Box": true, "Rc": true, "Arc": true,
	"Cow": true, "ToString": true, "ToOwned": true, "Into": true, "From": true,
	"TryInto": true, "TryFrom": true, "Iterator": true, "IntoIterator": true,
	"DoubleEndedIterator": true, "ExactSizeIterator": true, "Extend": true,
	"Clone": true, "Copy": true, "Default": true, "Drop": true,
	"Eq": true, "PartialEq": true, "Ord": true, "PartialOrd": true,
	"Hash": true, "Send": true, "Sync": true, "Sized": true, "Unpin": true,
	"Fn": true, "FnMut": true, "FnOnce": true, "AsRef": true, "AsMut": true,
	"HashMap": true, "HashSet": true, "BTreeMap": true, "BTreeSet": true,
	"drop": true, "print": true, "println": true, "format": true,
}

// rustPrimitives are the built-in types. The grammar gives them their own node
// type, so they are never captured as a type *reference*; they reach a
// descriptor only through an annotation the mapper read for a binding's type.
var rustPrimitives = map[string]bool{
	"bool": true, "char": true, "str": true,
	"u8": true, "u16": true, "u32": true, "u64": true, "u128": true, "usize": true,
	"i8": true, "i16": true, "i32": true, "i64": true, "i128": true, "isize": true,
	"f32": true, "f64": true,
}
