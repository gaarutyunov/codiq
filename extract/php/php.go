// Package php is the PHP stanza: the tree-sitter query in query.scm plus the
// mapper here that turns its captures into core facts (SPEC.md §5).
//
// The package is named for the extension, per SPEC.md §12. It imports facts and
// coord and deliberately not extract: it satisfies extract.Parser structurally,
// which is what keeps the registry free of an import cycle.
//
// The mapper's job, and its limits, are the seven stanzas before it (§4.3): it
// builds the descriptor *suffix* from the CST and the namespace the file
// declares; it assigns role and neutral-core symbol kind; and it resolves
// references whose target definition is in the same file. It does no type
// checking, runs no name resolution algorithm of its own and looks at no other
// file. A reference it cannot pin down is still emitted, carrying the best
// descriptor syntax allows, and the link pass decides what it means (§7). Where a
// component is genuinely unknowable file-locally the descriptor writes SCIP's "."
// for it, so it names an unresolved symbol rather than false-matching a real one.
//
// # The unit of modularity, and the first language here whose rule is *exactly*
// file-local
//
// Go's unit is the directory, TypeScript's the file, Python's the file with
// `__init__.py` collapsing to its directory, Rust's the `mod` tree, Java's the
// `package` clause, C#'s the `namespace` declaration, Ruby's the `module`/`class`
// nesting beside a path-keyed load unit. PHP's is C#'s — a namespace the file
// writes down, in the same two spellings, with the same consequence that a file's
// namespace is not a single fact about the file:
//
//   - `namespace Foo\Bar;` is written once and everything after it is its sibling
//     in the CST, so it can only be read as a property of the file.
//   - `namespace Foo\Bar { … }` is a real lexical region, and a file may hold
//     several side by side — including an unnamed `namespace { … }`, which is the
//     global namespace written explicitly. The grammar gives both spellings one
//     node kind, told apart by whether it has a `body`, so containerSuffix
//     contributes one namespace component per enclosing block and the file-scoped
//     declaration is the base the walk terminates at. PHP forbids *nesting* one
//     namespace inside another, so the walk contributes at most one — which is
//     one fewer case than C# has, not one more.
//
// What is new, and what makes this stanza shorter than C#'s and Java's rather
// than longer, is that **PHP's name resolution is entirely file-local by
// language design**. The rules are in the manual under "Name resolution rules"
// and they read like a specification of what a stanza here is allowed to do:
//
//	\A\B\C     fully qualified — resolved as written, and nothing may change it.
//	namespace\C relative — the current namespace, then the rest.
//	A\B\C      qualified — if `A` is a `use` alias, substitute it; otherwise
//	           prepend the current namespace. There is no third case.
//	C          unqualified — if `C` is a `use` alias, substitute it; otherwise
//	           prepend the current namespace.
//
// Every input to that is written in the file. Compare what the four namespaced
// predecessors had to do instead: Java splits a dotted path on a *naming
// convention* the JLS merely recommends; C# cannot even do that (namespaces and
// types are both PascalCase) and falls back to "the file has been told this is a
// namespace", plus a `projectUsings` rule that fires only when there is exactly
// one candidate and knowingly inverts C#'s own search order; Ruby has to guess
// whether a path segment is a module or a class because `::` does not say. PHP
// needs none of it. `use` is unambiguous — it always imports a *name*, never a
// namespace on demand, in all five of its spellings — so the alias table is
// exact, and a path with more than one segment cannot be a namespace-plus-type
// split to guess at, because the last segment is the name and every segment
// before it is namespace, always.
//
// The one place the language does *not* stay file-local is the global fallback:
// an unqualified **function** or **constant** that the current namespace does not
// define falls back to the global namespace at runtime, which is why `strlen()`
// works inside `namespace App;`. Whether `App\strlen` exists is a fact about
// another file, so the fallback is approximated the way rb.go approximates
// `$LOAD_PATH` and cs.go approximates `System`: a name this file declares wins, a
// name in phpBuiltins is the language's, and anything else is the current
// namespace's — which yields a descriptor with nothing behind it rather than a
// wrong edge. There is no such fallback for *class* names, so a type reference is
// exact.
//
// # Traits, and why PHP emits `implements` where Ruby could not
//
// Ruby established (issue #20) that a mixin must not participate in `implements`,
// because a module's method set is what it *gives* and not what it demands, so
// method-set containment fails exactly where the `include` is real. PHP has both
// halves of that argument and they land differently, so the conclusion is
// different too.
//
// PHP has **real interfaces**. `interface Speaker { public function greet(): string; }`
// declares behaviour and nothing else, `class Greeter implements Speaker` states
// satisfaction explicitly, and the compiler checks it. That is Java's and C#'s
// shape exactly, so an interface is emitted with the neutral core's `interface`
// kind, the `implements` clause is recorded as a reference to the interface's
// type, and link's method-set containment derivation says what it has said for
// six languages. PHP is not Ruby here.
//
// PHP **also** has traits, and a trait is not the thing that looks like it.
// `use Loudly;` inside a class body is horizontal reuse: at compile time the
// trait's members are *flattened into* the using class, so `Greeter::greet()`
// really is a method of `Greeter` when `Loudly` declares `greet` and `Greeter`
// uses `Loudly`. That is stronger than Ruby's `include`, which inserts the module
// into the ancestor chain and leaves the method the module's. And it has a
// consequence worth stating in as many words, because it is the answer to the
// obvious question:
//
//	class Counter implements Countable { use CountableTrait; }
//
// **does** satisfy `Countable` in PHP, and **does not** get an `implements` edge
// in this graph. Nothing declares `Counter#count().`; the only definition row is
// `CountableTrait#count().`, and link gathers a method set by descriptor prefix
// (`starts_with(member.descriptor, type.descriptor)`, store/sqlc/query.sql), so
// `Counter`'s method set is empty and containment fails.
//
// That is a false negative and it is chosen. The fix would be to flatten — to
// emit `Counter#count().` for every member of every trait the class uses — and
// the members of the trait are in another file, which §2.5 forbids reading. The
// half-measure of flattening only when the trait happens to be declared in the
// same file is worse than the consistent gap: it would make a derivation's answer
// depend on how the source is laid out, so the same program split across two
// files and one file would produce two different graphs. A gap that is the same
// everywhere can be documented; one that moves cannot.
//
// The mirror case is a trait that *does* get an edge. A trait is emitted as a
// `type` — it is a named container of members, it is not a namespace (nothing may
// be written `Loudly\x`, which is why Ruby's `package` reading of a module is
// wrong here), and it is not an interface (it demands nothing and cannot be
// type-hinted). So a trait whose members happen to contain an interface's method
// set will be derived as implementing it, having declared nothing. That is the
// duck-typing false positive `implements` has carried since Go, where implicit
// satisfaction makes it the correct answer; here it is noise of exactly the same
// shape and no worse.
//
// # Static and instance members are one namespace, and `::` does not say otherwise
//
// Ruby gave a singleton method a `self.` descriptor component on two grounds:
// every reference site distinguishes them by syntax, and Ruby permits one class to
// declare both `foo` and `self.foo`. PHP has the syntax that looks like the first
// and neither ground holds.
//
// PHP forbids the collision outright — a class declaring a static and an instance
// method, or a static and an instance property, of one name is a fatal
// redeclaration — so there are never two members to tell apart. And the syntax
// that appears to tell them apart does not:
//
//   - `parent::__construct()` calls an *instance* method through `::`, and is
//     the single commonest `::` in all of PHP. `self::helper()` inside a class
//     reaches an instance method just as happily.
//   - `$obj->staticMethod()` reaches a *static* method through `->`, which is
//     legal and does not even warn.
//
// `::` and `->` distinguish the **binding** — late static binding and the class
// scope on one side, an object context on the other — and not the member
// namespace. So a `static.` component keyed off `::` would render
// `Greeter#static.__construct().` at a call site and `Base#__construct().` at the
// declaration, and the two would never join: a distinction the reference site
// only *appears* able to reconstruct is the same defect as one it cannot, which
// is cs.go's test for an explicit interface implementation applied to a case that
// rhymes with Ruby's and answers the other way. One member namespace per class,
// and `Foo::bar()` and `$obj->bar()` render the same descriptor.
//
// # What this stanza cannot represent
//
//   - **A trait's members counting toward the using class.** Argued above: the
//     one deliberate false negative, and it is `implements` and nothing else —
//     a *call* to `$g->shout()` still resolves, because it lands on
//     `Greeter#shout().` which no definition carries, exactly as any other
//     unresolved reference does.
//   - **`require` / `include` as imports.** They name a path, and PHP — unlike
//     Ruby — has no second, path-keyed namespace for them to join against.
//     Giving them one was considered and rejected: rb.go could key a load unit on
//     the path because "a Ruby constant is capitalised and a Ruby path is not",
//     so its two schemes cannot collide. PHP namespaces are unconstrained by
//     case, which is precisely why this stanza *can* be made to collide with the
//     other six in the mixed-language corpus — and the same property means a
//     path-keyed package descriptor would collide with a namespace-keyed one
//     inside a single language. Beyond that, the idiomatic require is
//     `require __DIR__ . '/x.php'`, a concatenation with no literal path in it,
//     and modern PHP requires `vendor/autoload.php` once and reaches everything
//     else through PSR-4 autoloading and `use`. So `use` is the import mechanism
//     and `require` derives nothing.
//   - **`composer.json`'s `autoload.psr-4` map.** It maps a namespace prefix to a
//     directory, and this stanza reads the namespace out of the file's own
//     `namespace` statement, which PHP requires and which is authoritative — PSR-4
//     governs where the *autoloader looks for a file*, not what the symbols in it
//     are called, and a file whose declaration disagrees with the map is a file
//     the autoloader cannot find rather than a file whose classes are named
//     differently. The one thing the map states that syntax does not is *which
//     namespace prefixes this package owns*, which is C#'s `foreignRoots` problem
//     with a real answer in the tree — and it is still not worth taking, because
//     a reference into an unowned prefix lands at a namespace of this coordinate
//     that holds no definitions, which is an unresolved reference and not a wrong
//     edge, while carrying the map would put a per-file list inside `Coord`, which
//     index checkpoints and a recovering process reads back (index/dbos.go). A
//     core-schema change is the one thing §14 M9+ claims an additional language
//     must not cost.
//   - **Variable variables, `$$x`, `call_user_func`, `__call`, `__get`,
//     `eval`.** PHP's metaprogramming surface is Ruby's problem in a different
//     spelling, and a file-local CST reader sees an expression and no member.
//   - **A bare `(name)` in an expression.** `echo MAX;` reads a constant and
//     `$x instanceof Foo` names a class, and both are a bare `name` node with
//     nothing around it to say which. Neither is captured, so a global constant
//     read and an `instanceof` resolve to nothing rather than to a guess.
//   - **A class constant and a property of one name.** `const X` and `public $X`
//     are separate tables in PHP and may coexist; both render `T#X.` here, which
//     is the overload collision cs.go describes with a rarer trigger.
//   - **An anonymous class's members.** `new class { … }` declares a type with no
//     name, so its members hang off `.#` — a container that names nothing and
//     therefore matches nothing, which is right, rather than off the enclosing
//     class, which would be wrong.
//   - **A multi-package monorepo.** `coord.Resolve` reads the manifests of one
//     directory, so every package's files carry the root `composer.json`'s
//     coordinate rather than their own. That is coord's boundary and not this
//     stanza's, and it is Rust's Cargo-workspace note verbatim.
package php

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
const Lang = "php"

// Ext is the file extension this stanza is registered under.
const Ext = ".php"

// builtinNamespace is the name of the foreign coordinate PHP's own global
// namespace is given: the language's built-in classes, interfaces, functions and
// constants, which ship with the runtime and belong to no Composer package.
//
// It is `php` rather than a package name because there is no package. A
// dependency's symbols, by contrast, are *not* given a foreign coordinate: a
// namespace name carries no package identity in PHP any more than it does in C#,
// so `Symfony\Component\HttpFoundation\Request` lands at this coordinate's
// `Symfony/Component/HttpFoundation/Request#`, which holds no definitions — an
// unresolved reference rather than a wrong edge.
const builtinNamespace = "php"

//go:embed query.scm
var queryScheme string

// Parser is the PHP stanza. Safe for concurrent use: the grammar and compiled
// query are immutable after the first Parse, and each parse checks a
// gotreesitter parser out of a pool.
type Parser struct {
	once    sync.Once
	lang    *sitter.Language
	pool    *sitter.ParserPool
	query   *sitter.Query
	initErr error
}

// New returns the PHP parser. It is cheap: the grammar is loaded and query.scm
// compiled on the first Parse, so a binary that never parses PHP never
// decompresses the PHP grammar — and a query that fails to compile lands in
// ParseError rather than panicking at init.
func New() *Parser { return &Parser{} }

func (p *Parser) init() {
	p.once.Do(func() {
		p.lang = grammars.PhpLanguage()
		if p.lang == nil {
			p.initErr = errors.New("php: gotreesitter has no PHP grammar")
			return
		}
		q, err := sitter.NewQuery(queryScheme, p.lang)
		if err != nil {
			p.initErr = fmt.Errorf("php: compile query.scm: %w", err)
			return
		}
		p.query = q
		p.pool = sitter.NewParserPool(p.lang)
	})
}

// Parse extracts one PHP file's facts. It never returns an error: a failure is
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
		funcs:      map[string]pathTarget{},
		consts:     map[string]pathTarget{},
		claimed:    map[span]bool{},
		descIndex:  map[string]facts.LocalID{},
		defsByName: map[string][]defRec{},
		propTypes:  map[string]*sitter.Node{},
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
	occ   facts.LocalID
	scope facts.LocalID
	start uint32
	// typeNode is the type expression a binding was declared or initialised
	// with, or nil when nothing says. It is kept as a node rather than resolved
	// eagerly because resolution consults descIndex, which collectDefinitions is
	// still filling in — a parameter typed with a class declared further down the
	// file would otherwise resolve against an index that did not have it yet.
	typeNode *sitter.Node
}

// pathTarget is what a written name resolved to: the coordinate and descriptor
// suffix its members hang off.
type pathTarget struct {
	coord  coord.Coord
	suffix string
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

	// types, funcs and consts are what this file's `use` declarations bound, one
	// table per name space PHP keeps separate. Three tables and not one because
	// PHP genuinely has three: `use A\f;` and `use function A\f;` may both appear
	// in one file and bind different symbols, and a call site and a type
	// position ask different tables.
	types  map[string]pathTarget
	funcs  map[string]pathTarget
	consts map[string]pathTarget

	// claimed holds identifier ranges a definition or a `use` declaration already
	// owns, so a reference pattern matching the same identifier is dropped.
	claimed map[span]bool
	// descIndex maps a definition's full descriptor to its occurrence — the
	// same-file half of what the link pass does across files.
	descIndex  map[string]facts.LocalID
	defsByName map[string][]defRec
	// propTypes maps a property name to the type expression it was declared
	// with. It is file-wide rather than scoped because a property belongs to the
	// class and not to the method that reads it — `$this->name` in `greet()` is
	// the member `public string $name` declares.
	propTypes map[string]*sitter.Node
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
// collectScopes sorts by (start ascending, end descending) and the `program`
// node spans everything, so it is always the first scope emitted.
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
// Two patterns produce a @definition.package match: the namespace declaration in
// either spelling, and `(program)` for a file that declares none. The first is a
// real declaration with a name to hang an occurrence on; the second is the global
// namespace and gets a zero-width point at byte 0, which is what every language
// before Java had to do for all of them.
//
// A file may produce several, since PHP permits sibling `namespace … { }` blocks
// — and that is not an anomaly to be collapsed: the types inside them carry
// different prefixes, and each namespace is a symbol other files' `use`
// declarations join against.
func (b *builder) collectNamespaces(matches []sitter.QueryMatch) {
	type cand struct{ decl, name *sitter.Node }
	var cands []cand
	program := false
	for _, m := range matches {
		root, name, ok := roots(m, "definition.package")
		if !ok {
			continue
		}
		if root.Node.Type(b.lang) == "program" {
			program = true
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
		if program {
			b.definePackage(b.fileScope(), "", 0, 0)
		}
		return
	}

	for _, cd := range cands {
		segs := b.nameSegments(cd.name)
		if len(segs) == 0 {
			continue
		}
		b.claimSubtree(cd.name)

		suffix := namespaceOf(segs)
		if cd.decl.ChildByFieldName("body", b.lang) == nil && b.ns == "" {
			// `namespace Foo\Bar;` — the one form that is a property of the file:
			// everything after it is its sibling in the CST, so the ancestor walk
			// can never find it and it has to be the base the walk terminates at.
			b.ns = suffix
		}
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

// namespaceSuffix is the namespace descriptor in effect at n: the enclosing
// `namespace … { }` block, or the file-scoped declaration when there is none.
//
// It is containerSuffix with the types and callables left out, and the two are
// deliberately different walks — cs.go draws the same distinction for the same
// reason. A type name written inside `class Greeter` does not name a member of
// `Greeter`: PHP resolves an unqualified name against the *current namespace*,
// never against the enclosing type's descriptor path.
func (b *builder) namespaceSuffix(n *sitter.Node) string {
	for p := n; p != nil; p = p.Parent() {
		if p.Type(b.lang) != "namespace_definition" {
			continue
		}
		if segs := b.nameSegments(p.ChildByFieldName("name", b.lang)); len(segs) > 0 {
			return namespaceOf(segs)
		}
		// `namespace { … }` with no name is the global namespace, written out.
		return ""
	}
	return b.ns
}

// ----------------------------------------------------------------- imports ---

// collectImports records what each top-level `use` binds, and emits one
// occurrence per name the declaration reaches.
//
// Import *edges* are cross-file and therefore the link pass's (§4.4); what is
// extracted is the occurrence, whose descriptor names the imported namespace.
// That descriptor is byte-identical to the one the target file's own namespace
// declaration produces — both are the backslashed name with slashes — which is
// what lets link derive `imports` by descriptor join.
//
// The split has to be exactly right and PHP makes it free: a `use` always imports
// a *name*, in every one of its five spellings, so the last segment is the name
// and everything before it is the namespace. There is no on-demand form to tell
// apart from an alias form (C#'s problem), and no convention to lean on (Java's).
//
// Every identifier the declaration consumed is claimed, so the reference patterns
// do not emit a second, weaker occurrence over the same bytes.
func (b *builder) collectImports(matches []sitter.QueryMatch) {
	var decls []*sitter.Node
	for _, m := range matches {
		if root, _, ok := roots(m, "import"); ok {
			decls = append(decls, root.Node)
		}
	}
	sort.SliceStable(decls, func(i, j int) bool {
		return decls[i].StartByte() < decls[j].StartByte()
	})
	for _, decl := range decls {
		b.useDeclaration(decl)
	}
}

// useDeclaration resolves one top-level `use` and records what it binds.
//
// The group form (`use A\B\{C, D};`) writes the shared prefix as a sibling of the
// group rather than as part of each clause, so the prefix is read once and
// prepended; the flat forms carry the whole path in the clause. Both end in the
// same place, which is why there is one loop.
func (b *builder) useDeclaration(decl *sitter.Node) {
	b.claimSubtree(decl)

	var prefix []string
	if p := b.directChild(decl, "namespace_name"); p != nil {
		prefix = b.nameSegments(p)
		if len(prefix) > 0 {
			// One occurrence for the group's shared prefix, rather than one per
			// clause over the same bytes.
			c, ns := b.namespaceTarget(prefix)
			b.addOccurrence(facts.Descriptor{Prefix: c, Suffix: ns},
				facts.RoleReference, facts.KindPackage, prefix[len(prefix)-1],
				p.StartByte(), p.EndByte())
		}
	}

	declKind := b.fieldText(decl, "type")
	for _, clause := range b.clausesOf(decl) {
		kind := b.fieldText(clause, "type")
		if kind == "" {
			kind = declKind
		}
		path := b.usePathOf(clause)
		if path == nil {
			continue
		}
		segs := append(append([]string{}, prefix...), b.nameSegments(path)...)
		if len(segs) == 0 {
			continue
		}
		name := segs[len(segs)-1]
		nsSegs := segs[:len(segs)-1]

		c, ns := b.namespaceTarget(nsSegs)
		if len(prefix) == 0 && len(nsSegs) > 0 {
			// A flat `use A\B\C;` — the namespace half is written inside the
			// clause, so the occurrence covers exactly those bytes.
			if q := b.qualifierOf(path); q != nil {
				b.addOccurrence(facts.Descriptor{Prefix: c, Suffix: ns},
					facts.RoleReference, facts.KindPackage, nsSegs[len(nsSegs)-1],
					q.StartByte(), q.EndByte())
			}
		}

		term, symKind := "#", facts.KindType
		table := b.types
		switch kind {
		case "function":
			term, symKind, table = "().", facts.KindFunction, b.funcs
		case "const":
			term, symKind, table = ".", facts.KindConstant, b.consts
		}
		if c == b.coord && len(nsSegs) == 0 {
			// A root-namespace import of a name the language owns.
			c = b.builtinFor(name, term)
		}

		target := pathTarget{coord: c, suffix: ns + name + term}
		if nameNode := b.pathNameNode(path); nameNode != nil {
			b.addOccurrence(facts.Descriptor{Prefix: target.coord, Suffix: target.suffix},
				facts.RoleReference, symKind, name, nameNode.StartByte(), nameNode.EndByte())
		}

		bound := name
		if alias := clause.ChildByFieldName("alias", b.lang); alias != nil {
			bound = b.text(alias)
		}
		if bound != "" {
			table[bound] = target
		}
	}
}

// clausesOf returns a use declaration's clauses, whether they are written flat or
// inside a group.
func (b *builder) clausesOf(decl *sitter.Node) []*sitter.Node {
	var out []*sitter.Node
	for i := 0; i < decl.NamedChildCount(); i++ {
		c := decl.NamedChild(i)
		switch c.Type(b.lang) {
		case "namespace_use_clause":
			out = append(out, c)
		case "namespace_use_group":
			for j := 0; j < c.NamedChildCount(); j++ {
				if g := c.NamedChild(j); g.Type(b.lang) == "namespace_use_clause" {
					out = append(out, g)
				}
			}
		}
	}
	return out
}

// usePathOf is the path a clause imports, which is its first named child that is
// a name of some kind — never the `alias:` child, which names nothing outside
// this file.
func (b *builder) usePathOf(clause *sitter.Node) *sitter.Node {
	alias := clause.ChildByFieldName("alias", b.lang)
	for i := 0; i < clause.NamedChildCount(); i++ {
		c := clause.NamedChild(i)
		if sameSpan(c, alias) {
			continue
		}
		switch c.Type(b.lang) {
		case "name", "qualified_name", "relative_name":
			return c
		}
	}
	return nil
}

// ------------------------------------------------------------------- paths ---

// nameSegments flattens a name of any shape into its segments. `A\B\C` yields
// ["A","B","C"]; a leading `\` contributes nothing, since absoluteness is a
// property of the path and not a segment of it.
func (b *builder) nameSegments(n *sitter.Node) []string {
	if n == nil {
		return nil
	}
	switch n.Type(b.lang) {
	case "name":
		if t := b.text(n); t != "" {
			return []string{t}
		}
	case "namespace_name", "qualified_name", "relative_name":
		var segs []string
		for i := 0; i < n.NamedChildCount(); i++ {
			segs = append(segs, b.nameSegments(n.NamedChild(i))...)
		}
		return segs
	}
	return nil
}

// pathNameNode is the identifier a path node's *last* segment is spelled with —
// the bytes an occurrence over the path should cover. `A\B\C` yields the node
// spanning `C`.
func (b *builder) pathNameNode(n *sitter.Node) *sitter.Node {
	if n == nil {
		return nil
	}
	switch n.Type(b.lang) {
	case "name":
		return n
	case "qualified_name", "relative_name", "namespace_name":
		for i := n.NamedChildCount() - 1; i >= 0; i-- {
			if c := n.NamedChild(i); c.Type(b.lang) == "name" {
				return c
			}
		}
	}
	return nil
}

// qualifierOf is the namespace half of a qualified name, as a node — the
// `namespace_name` a `qualified_name` writes its prefix in. nil when the path has
// no namespace half.
func (b *builder) qualifierOf(n *sitter.Node) *sitter.Node {
	if n == nil {
		return nil
	}
	return b.directChild(n, "namespace_name")
}

// pathMode is how PHP reads a written name, which is decided by its shape alone
// (the manual's "Name resolution rules"). The four cases are exhaustive and there
// is no fifth to guess at, which is the property that makes this stanza's
// resolution exact where Java's, C#'s and Ruby's had to approximate.
type pathMode int

const (
	// modeUnqualified is a single segment: `C`. An alias wins; otherwise the
	// current namespace.
	modeUnqualified pathMode = iota
	// modeQualified is several segments with no leading `\`: `A\B\C`. An alias
	// for the *first* segment wins; otherwise the current namespace.
	modeQualified
	// modeAbsolute is a leading `\`: `\A\B\C`. Resolved as written, always.
	modeAbsolute
	// modeRelative is a leading `namespace\`: the current namespace, then the
	// rest, and no alias may intervene.
	modeRelative
)

// classify reads a path node's segments and which of PHP's four resolution modes
// applies to it.
func (b *builder) classify(n *sitter.Node) ([]string, pathMode) {
	segs := b.nameSegments(n)
	if len(segs) == 0 {
		return nil, modeUnqualified
	}
	switch {
	case n.Type(b.lang) == "relative_name":
		return segs, modeRelative
	case strings.HasPrefix(b.text(n), `\`):
		return segs, modeAbsolute
	case len(segs) > 1:
		return segs, modeQualified
	default:
		return segs, modeUnqualified
	}
}

// namespaceTarget renders namespace segments as a coordinate and namespace
// descriptor. A namespace name carries no package identity in PHP — `Symfony\…`
// says nothing about which package it ships in — so the coordinate is always this
// repository's and a dependency's namespace holds no definitions: an unresolved
// reference rather than a wrong edge. That is cs.go's `namespaceTarget` with the
// platform case removed, because PHP's built-ins live in the *global* namespace
// and so are reached by name rather than by root.
func (b *builder) namespaceTarget(segs []string) (coord.Coord, string) {
	return b.coord, namespaceOf(segs)
}

// builtinFor returns the coordinate a root-namespace name of the given
// terminator belongs to: PHP's own when the language declares it, this
// repository's otherwise.
func (b *builder) builtinFor(name, term string) coord.Coord {
	var known bool
	switch term {
	case "#":
		known = phpBuiltinTypes[name]
	case "().":
		known = phpBuiltinFuncs[name]
	default:
		known = phpBuiltinConsts[name]
	}
	if known {
		return coord.Foreign(b.coord.Scheme, b.coord.Manager, builtinNamespace)
	}
	return b.coord
}

// resolvePath applies PHP's name resolution rules to a written path, for the name
// space the terminator identifies.
//
// This is the whole of the stanza's resolution, and it is a transcription rather
// than a heuristic: every branch below is a line of the manual. The only
// approximation is the global fallback for functions and constants, which is a
// fact about another file (see the package comment).
func (b *builder) resolvePath(n *sitter.Node, term string) pathTarget {
	segs, mode := b.classify(n)
	if len(segs) == 0 {
		return pathTarget{coord: b.coord, suffix: coord.Unknown + term}
	}
	table := b.tableFor(term)

	switch mode {
	case modeAbsolute:
		return b.rootTarget(segs, term)
	case modeRelative:
		return pathTarget{coord: b.coord, suffix: b.namespaceSuffix(n) + joinPath(segs, term)}
	case modeQualified:
		if t, ok := table[segs[0]]; ok {
			// `use X\Y as A;` makes `A\B\C` mean `\X\Y\B\C`. The alias always binds
			// a *name*, so its own terminator is replaced by a namespace separator.
			return pathTarget{coord: t.coord, suffix: reterminate(t.suffix) + joinPath(segs[1:], term)}
		}
		return pathTarget{coord: b.coord, suffix: b.namespaceSuffix(n) + joinPath(segs, term)}
	default:
		if t, ok := table[segs[0]]; ok {
			return t
		}
		if s, ok := b.localDeclared(n, segs, term); ok {
			return pathTarget{coord: b.coord, suffix: s}
		}
		ns := b.namespaceSuffix(n)
		if ns == "" {
			return b.rootTarget(segs, term)
		}
		if term != "#" && b.builtinFor(segs[0], term) != b.coord {
			// PHP's global fallback: an unqualified function or constant the
			// current namespace does not define resolves to the global one. There
			// is no such fallback for a class name, which is why `term == "#"` is
			// excluded rather than merely unlikely to match.
			return b.rootTarget(segs, term)
		}
		return pathTarget{coord: b.coord, suffix: ns + joinPath(segs, term)}
	}
}

// rootTarget names a path read from the global namespace, which is the one place
// PHP's own built-ins can be reached.
func (b *builder) rootTarget(segs []string, term string) pathTarget {
	c := b.coord
	if len(segs) == 1 {
		c = b.builtinFor(segs[0], term)
	}
	return pathTarget{coord: c, suffix: joinPath(segs, term)}
}

// tableFor is the alias table a terminator's name space uses. PHP keeps class,
// function and constant imports apart, and so does this.
func (b *builder) tableFor(term string) map[string]pathTarget {
	switch term {
	case "().":
		return b.funcs
	case "#":
		return b.types
	default:
		return b.consts
	}
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

		name := b.bareName(cd.name)
		if name == "" || name == "_" {
			continue
		}
		kind := b.refineKind(cd.kind, cd.node)
		suffix := b.definitionSuffix(kind, cd.node, name)
		b.claimed[s] = true

		desc := facts.Descriptor{Prefix: b.coord, Suffix: suffix}
		occ := b.addOccurrence(desc, facts.RoleDefinition, kind, name, s.start, s.end)
		b.edge(facts.EdgeDefines, facts.FileRef(), facts.OccurrenceRef(occ))

		if _, dup := b.descIndex[desc.String()]; !dup {
			b.descIndex[desc.String()] = occ
		}
		typeNode := b.declaredTypeNode(cd.node)
		b.defsByName[name] = append(b.defsByName[name], defRec{
			occ:      occ,
			scope:    b.occurrence(occ).Scope,
			start:    s.start,
			typeNode: typeNode,
		})
		if typeNode != nil && kind == facts.KindField {
			b.propTypes[name] = typeNode
		}
	}
}

// refineKind narrows a capture's kind where the CST carries a distinction the
// capture name does not. PHP needs it in one place.
//
// A constructor's *promoted* parameter is written where a parameter is written
// and is not only one: `public function __construct(private string $name)`
// declares a property, and `$this->name` is what reaches it. So it is the neutral
// core's `field`, which is the promotion java.go applies to a record component
// and cs.go to a positional one, for the third time and the same reason.
func (b *builder) refineKind(kind string, node *sitter.Node) string {
	if kind == facts.KindParameter && node.Type(b.lang) == "property_promotion_parameter" {
		return facts.KindField
	}
	return kind
}

// definitionSuffix builds the SCIP descriptor suffix for a definition from its
// capture hierarchy (SPEC.md §5).
func (b *builder) definitionSuffix(kind string, node *sitter.Node, name string) string {
	if node.Type(b.lang) == "property_promotion_parameter" {
		// Promoted, so a member of the *class* and not of the constructor whose
		// parameter list it is written in. The ancestor walk would give it the
		// constructor's `().` component, which is the one place the walk is wrong.
		return b.typeSuffix(node) + name + "."
	}

	container := b.containerSuffix(node)
	switch kind {
	case facts.KindFunction, facts.KindMethod:
		return container + name + "()."
	case facts.KindType, facts.KindInterface:
		return container + name + "#"
	case facts.KindParameter:
		return container + "(" + name + ")"
	case facts.KindPackage:
		return container + name + "/"
	default: // field, variable, constant
		return container + name + "."
	}
}

// containerSuffix returns the descriptor suffix of the nearest enclosing named
// container of n — a namespace block, a type or a callable — or this file's
// file-scoped namespace when there is none. A body, a block, a closure and an
// arrow function are transparent: none has a name, so none has a descriptor of
// its own and what is inside belongs to the enclosing container.
//
// An anonymous class is the one container that is neither named nor transparent.
// It has members, and they are not the enclosing class's, so it contributes
// SCIP's "." — a container that names nothing and therefore matches nothing.
func (b *builder) containerSuffix(n *sitter.Node) string {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "namespace_definition":
			if segs := b.nameSegments(p.ChildByFieldName("name", b.lang)); len(segs) > 0 {
				return namespaceOf(segs)
			}
			return "" // `namespace { … }`: the global namespace, written out.
		case "class_declaration", "interface_declaration", "trait_declaration", "enum_declaration":
			if name := b.fieldText(p, "name"); name != "" {
				return b.containerSuffix(p) + name + "#"
			}
		case "anonymous_class":
			return b.containerSuffix(p) + coord.Unknown + "#"
		case "function_definition", "method_declaration":
			if name := b.fieldText(p, "name"); name != "" {
				return b.containerSuffix(p) + name + "()."
			}
		}
	}
	return b.ns
}

// typeSuffix is the descriptor suffix of the type declaration n sits innermost
// inside, or "" at the file's top level. It is what resolves `$this`, `self`,
// `static` and a promoted constructor parameter.
func (b *builder) typeSuffix(n *sitter.Node) string {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "class_declaration", "interface_declaration", "trait_declaration", "enum_declaration":
			if name := b.fieldText(p, "name"); name != "" {
				return b.containerSuffix(p) + name + "#"
			}
		case "anonymous_class":
			return b.containerSuffix(p) + coord.Unknown + "#"
		}
	}
	return ""
}

// selfTarget is what `$this`, `self` and `static` name: the type the expression
// is written inside. All three are one answer here, because the difference
// between them is late static binding — which class the call *dispatches* to at
// runtime — and a descriptor names a declaration site rather than a dispatch.
func (b *builder) selfTarget(n *sitter.Node) (pathTarget, bool) {
	if s := b.typeSuffix(n); s != "" {
		return pathTarget{coord: b.coord, suffix: s}, true
	}
	return pathTarget{}, false
}

// parentTarget is what `parent::` names: the class the enclosing type extends.
// PHP writes the base class and the interfaces in two different clauses and marks
// both, so — unlike C#, which has to take the first entry of one list — there is
// nothing to guess. A type with no `extends` has no parent, and the honest answer
// is that the call reaches nothing.
func (b *builder) parentTarget(n *sitter.Node) (pathTarget, bool) {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "class_declaration", "interface_declaration", "anonymous_class":
			base := b.directChild(p, "base_clause")
			if base == nil {
				return pathTarget{}, false
			}
			for i := 0; i < base.NamedChildCount(); i++ {
				switch c := base.NamedChild(i); c.Type(b.lang) {
				case "name", "qualified_name", "relative_name":
					return b.resolvePath(c, "#"), true
				}
			}
			return pathTarget{}, false
		}
	}
	return pathTarget{}, false
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
	case "type", "classconst":
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
	add := func(r refRec) {
		if r.nameNode == nil {
			return
		}
		s := span{r.nameNode.StartByte(), r.nameNode.EndByte()}
		if b.claimed[s] {
			return // a definition or a `use` declaration already owns this identifier
		}
		if prev, dup := best[s]; dup && !b.moreSpecific(r, prev) {
			return
		}
		best[s] = r
	}

	for _, m := range matches {
		root, name, ok := roots(m, "reference.")
		if !ok {
			continue
		}
		role := suffixAfter(root.Name, "reference.")
		switch role {
		case "classconst":
			// `Foo::CONST`, whose two halves are both bare `(name)` with no field
			// to tell them apart, so they are split positionally here.
			scope, member := firstNamedChild(root.Node), lastNamedChild(root.Node)
			if scope == nil || member == nil || sameSpan(scope, member) {
				continue
			}
			add(refRec{role: "classconst", node: root.Node, nameNode: member})
			switch scope.Type(b.lang) {
			case "name", "qualified_name", "relative_name":
				add(refRec{role: "type", node: scope, nameNode: b.pathNameNode(scope)})
			}
		case "type":
			// The type patterns capture the path expression, because what decides
			// the descriptor is the whole path and what the occurrence covers is the
			// identifier inside it.
			if name == nil {
				continue
			}
			add(refRec{role: role, node: name.Node, nameNode: b.pathNameNode(name.Node)})
		default:
			nameNode := root.Node
			if name != nil {
				nameNode = name.Node
			}
			switch nameNode.Type(b.lang) {
			case "qualified_name", "relative_name":
				// `\strlen(…)` and `namespace\helper(…)`: the call's `function`
				// field is the whole path, and the occurrence covers the identifier
				// it ends in — the descriptor already carries the rest.
				nameNode = b.pathNameNode(nameNode)
			}
			add(refRec{role: role, node: root.Node, nameNode: nameNode})
		}
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
		name := b.bareName(r.nameNode)
		if name == "" || name == "_" {
			continue
		}
		if r.role == "read" && name == "this" && r.nameNode.Type(b.lang) == "variable_name" {
			// `$this` names the enclosing object rather than a symbol. Every use of
			// it is already described by the member it reaches, and an occurrence
			// over the keyword would describe the same bytes more weakly.
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
		return b.typeReferenceDescriptor(r)
	case "classconst":
		return b.classConstDescriptor(r, name)
	default:
		return b.readDescriptor(r, name)
	}
}

// callDescriptor names the target of a call.
//
// The three shapes are the three the grammar has, and each says exactly what its
// receiver is. `f()` is a free function, resolved by PHP's rules including the
// global fallback; `$g->m()` is a member of whatever `$g` was declared or
// initialised as; `Foo::m()` is a member of a class named right there. Where the
// receiver is unknowable file-locally the type component is SCIP's ".", so the
// descriptor cannot false-match a real definition.
func (b *builder) callDescriptor(r refRec, name string) (facts.Descriptor, string) {
	switch r.node.Type(b.lang) {
	case "function_call_expression":
		// A free function, always: PHP has no receiverless method call, so a
		// method reached without a receiver does not exist and the kind is not a
		// question the descriptor has to answer.
		t := b.resolvePath(r.node.ChildByFieldName("function", b.lang), "().")
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix}, facts.KindFunction
	case "scoped_call_expression":
		t, ok := b.scopeTarget(r.node.ChildByFieldName("scope", b.lang), r.nameNode.StartByte())
		if !ok {
			return facts.Descriptor{Prefix: b.coord, Suffix: coord.Unknown + "#" + name + "()."}, facts.KindMethod
		}
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "()."}, facts.KindMethod
	default: // member_call_expression, nullsafe_member_call_expression
		t, ok := b.valueTarget(r.node.ChildByFieldName("object", b.lang), r.nameNode.StartByte())
		if !ok {
			return facts.Descriptor{Prefix: b.coord, Suffix: coord.Unknown + "#" + name + "()."}, facts.KindMethod
		}
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "()."}, facts.KindMethod
	}
}

// readDescriptor names a plain read: a property of some object, a static
// property of some class, or a local variable.
func (b *builder) readDescriptor(r refRec, name string) (facts.Descriptor, string) {
	switch r.node.Type(b.lang) {
	case "member_access_expression", "nullsafe_member_access_expression":
		t, ok := b.valueTarget(r.node.ChildByFieldName("object", b.lang), r.nameNode.StartByte())
		if !ok {
			return facts.Descriptor{Prefix: b.coord, Suffix: coord.Unknown + "#" + name + "."}, facts.KindField
		}
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "."}, facts.KindField
	case "scoped_property_access_expression":
		t, ok := b.scopeTarget(r.node.ChildByFieldName("scope", b.lang), r.nameNode.StartByte())
		if !ok {
			return facts.Descriptor{Prefix: b.coord, Suffix: coord.Unknown + "#" + name + "."}, facts.KindField
		}
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "."}, facts.KindField
	}

	// A bare `$x`: a local of the innermost enclosing function scope. PHP's
	// function scopes are hard — a function body sees no enclosing local without
	// `global` or a `use (…)` clause — so a name that is not bound here is bound
	// nowhere this file can see, and it lands on the enclosing container.
	if def, ok := b.lookup(name, r.nameNode.StartByte()); ok {
		return b.occurrence(def.occ).Descriptor, facts.KindVariable
	}
	return facts.Descriptor{Prefix: b.coord, Suffix: b.containerSuffix(r.nameNode) + name + "."}, facts.KindVariable
}

// typeReferenceDescriptor names a class, interface, trait or enum written in a
// type position — an annotation, an `extends`, an `implements`, a `new`, a trait
// `use`, an attribute, or the scope half of a `::`.
func (b *builder) typeReferenceDescriptor(r refRec) (facts.Descriptor, string) {
	if t, ok := b.relativeType(r.node); ok {
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix}, facts.KindType
	}
	t := b.resolvePath(r.node, "#")
	return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix}, facts.KindType
}

// classConstDescriptor names `Foo::CONST` and `Suit::Hearts`, which are one
// syntax and one member namespace: PHP reaches an enum case exactly as it reaches
// a class constant, and nothing in the source says which it is.
//
// The exception is a trait adaptation — `TraitA::foo insteadof TraitB;` — where
// the identical syntax names a *method*. That is the one place the enclosing node
// settles what the expression itself cannot, and it is settled here rather than
// left to render a constant that does not exist.
func (b *builder) classConstDescriptor(r refRec, name string) (facts.Descriptor, string) {
	term, kind := ".", facts.KindConstant
	if b.isTraitAdaptation(r.node) {
		term, kind = "().", facts.KindMethod
	}
	t, ok := b.scopeTarget(firstNamedChild(r.node), r.nameNode.StartByte())
	if !ok {
		return facts.Descriptor{Prefix: b.coord, Suffix: coord.Unknown + "#" + name + term}, kind
	}
	return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + term}, kind
}

// isTraitAdaptation reports whether a `Foo::bar` sits in the `{ … }` block of a
// trait `use`, where PHP writes a method reference with class-constant syntax.
func (b *builder) isTraitAdaptation(n *sitter.Node) bool {
	p := n.Parent()
	if p == nil {
		return false
	}
	switch p.Type(b.lang) {
	case "use_instead_of_clause", "use_as_clause":
		return true
	}
	return false
}

// relativeType resolves `self`, `static` and `parent` written where a type name
// is written — which PHP allows in a return type and nowhere else that matters
// here. They are `named_type`s wrapping an ordinary `name`, so the grammar gives
// no hint and the three keywords are checked by spelling.
func (b *builder) relativeType(n *sitter.Node) (pathTarget, bool) {
	if n == nil || n.Type(b.lang) != "name" {
		return pathTarget{}, false
	}
	switch b.text(n) {
	case "self", "static", "$this":
		return b.selfTarget(n)
	case "parent":
		return b.parentTarget(n)
	}
	return pathTarget{}, false
}

// ------------------------------------------------------------ local lookup ---

// scopeTarget resolves the left half of a `::` to the class its members hang off.
// PHP puts a class name there and nothing else — there is no namespace on the
// left of a `::`, which is the question C# needed a whole `splitPath` to answer —
// so the only cases are the three relative keywords and a written name.
func (b *builder) scopeTarget(scope *sitter.Node, pos uint32) (pathTarget, bool) {
	if scope == nil {
		return pathTarget{}, false
	}
	switch scope.Type(b.lang) {
	case "relative_scope":
		switch b.text(scope) {
		case "parent":
			return b.parentTarget(scope)
		default: // self, static
			return b.selfTarget(scope)
		}
	case "name", "qualified_name", "relative_name":
		if t, ok := b.relativeType(scope); ok {
			return t, true
		}
		return b.resolvePath(scope, "#"), true
	case "variable_name":
		// `$class::method()` — a class named by a value, which is a string at
		// runtime and nothing at all file-locally.
		return b.valueTarget(scope, pos)
	}
	return pathTarget{}, false
}

// valueTarget resolves the object half of a `->` to the class its members hang
// off. It is the one place a type has to be recovered rather than read off the
// syntax — and PHP writes it down more often than Ruby does, because a parameter,
// a property and a return type all carry declared types.
func (b *builder) valueTarget(value *sitter.Node, pos uint32) (pathTarget, bool) {
	if value == nil {
		return pathTarget{}, false
	}
	switch value.Type(b.lang) {
	case "parenthesized_expression":
		return b.valueTarget(firstNamedChild(value), pos)
	case "object_creation_expression":
		// `(new Greeter())->greet()` — the type is written right there, which
		// makes it the one expression form worth reading back.
		if path := b.creationType(value); path != nil {
			return b.resolvePath(path, "#"), true
		}
	case "variable_name":
		if b.bareName(value) == "this" {
			return b.selfTarget(value)
		}
		if def, ok := b.lookup(b.bareName(value), pos); ok && def.typeNode != nil {
			return b.resolvePath(def.typeNode, "#"), true
		}
	case "member_access_expression", "nullsafe_member_access_expression":
		// `$this->greeter->greet()` — a property whose declared type is recorded
		// file-wide, because a property belongs to the class and not to the method
		// that reads it.
		if name := b.bareName(value.ChildByFieldName("name", b.lang)); name != "" {
			if t, ok := b.propTypes[name]; ok && t != nil {
				return b.resolvePath(t, "#"), true
			}
		}
	}
	return pathTarget{}, false
}

// creationType is the class a `new` names, or nil for `new class { … }` and for
// `new $c`, neither of which names one.
func (b *builder) creationType(n *sitter.Node) *sitter.Node {
	for i := 0; i < n.NamedChildCount(); i++ {
		switch c := n.NamedChild(i); c.Type(b.lang) {
		case "name", "qualified_name", "relative_name":
			return c
		}
	}
	return nil
}

// lookup finds the definition named name that is visible at byte offset pos:
// among definitions whose declaring scope contains pos, the one in the innermost
// such scope, declared no later than pos.
//
// The "no later than pos" rule is a local's and PHP's function scopes make it
// exact: a body sees no enclosing local at all, so there is no outer binding to
// be shadowed by a later inner one. A *property* is the exception and is not
// looked up here at all — propTypes is keyed by name and file-wide, because a
// property declared below a method is still the member that method reads.
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

// localDeclared resolves an unqualified name against the definitions this file
// declares, innermost container first.
//
// It is what makes a class declared in this file resolvable by its simple name
// from anywhere in it, and what keeps a nested function's call on the enclosing
// callable's `().` component. Only names this file already defined, which is why
// it consults descIndex: collectDefinitions runs first, so every type, callable
// and constant declared here is in it, and a name that is not is not this file's
// to claim.
func (b *builder) localDeclared(n *sitter.Node, segs []string, term string) (string, bool) {
	try := func(prefix string) (string, bool) {
		cand := facts.Descriptor{Prefix: b.coord, Suffix: prefix + joinPath(segs, term)}
		if _, ok := b.descIndex[cand.String()]; ok {
			return prefix + joinPath(segs, term), true
		}
		return "", false
	}
	for p := n; p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "namespace_definition", "class_declaration", "interface_declaration",
			"trait_declaration", "enum_declaration", "function_definition", "method_declaration":
		default:
			continue
		}
		if s, ok := try(b.containerOf(p)); ok {
			return s, true
		}
	}
	return try(b.ns)
}

// containerOf is the descriptor suffix of the container p itself is, which is
// containerSuffix run one level out and then closed with p's own component.
func (b *builder) containerOf(p *sitter.Node) string {
	switch p.Type(b.lang) {
	case "namespace_definition":
		if segs := b.nameSegments(p.ChildByFieldName("name", b.lang)); len(segs) > 0 {
			return namespaceOf(segs)
		}
		return ""
	case "function_definition", "method_declaration":
		if name := b.fieldText(p, "name"); name != "" {
			return b.containerSuffix(p) + name + "()."
		}
	default:
		if name := b.fieldText(p, "name"); name != "" {
			return b.containerSuffix(p) + name + "#"
		}
	}
	return b.containerSuffix(p)
}

// declaredTypeNode recovers the type expression a binding was declared or
// initialised with — enough to name the members reached through it, with no type
// inference at all. nil means unknown, which downstream becomes SCIP's "." rather
// than a guess.
//
// PHP writes the type down at three of the four sites, which puts this stanza
// between C#'s (where it is written everywhere) and Ruby's (where it is written
// nowhere). The fourth is a local, whose type comes from an initialising `new`
// and from nothing else: `Foo::build()` and every other factory are deliberately
// not read, because a name is not a contract and a wrong answer would name a
// member of a type that does not have it.
func (b *builder) declaredTypeNode(node *sitter.Node) *sitter.Node {
	switch node.Type(b.lang) {
	case "simple_parameter", "variadic_parameter", "property_promotion_parameter":
		return b.unwrapType(node.ChildByFieldName("type", b.lang))
	case "property_declaration":
		// The capture's root is the declaration and not the element, because one
		// `private Greeter $a, $b;` declares two properties of one written type.
		return b.unwrapType(node.ChildByFieldName("type", b.lang))
	case "assignment_expression":
		return b.initialiserType(node.ChildByFieldName("right", b.lang))
	case "static_variable_declaration":
		return b.initialiserType(node.ChildByFieldName("value", b.lang))
	case "catch_clause":
		if t := node.ChildByFieldName("type", b.lang); t != nil {
			return b.unwrapType(firstNamedChild(t))
		}
	}
	return nil
}

// initialiserType reads a type off an initialising expression, and only from the
// one expression whose type PHP writes down: `new C`.
func (b *builder) initialiserType(expr *sitter.Node) *sitter.Node {
	for depth := 0; expr != nil && depth < 16; depth++ {
		switch expr.Type(b.lang) {
		case "parenthesized_expression":
			expr = firstNamedChild(expr)
		case "object_creation_expression":
			return b.creationType(expr)
		default:
			return nil
		}
	}
	return nil
}

// unwrapType reduces a type expression to the single path node that names a
// class, or nil when it names none. A nullable type is transparent; a primitive,
// a union and an intersection name nothing a descriptor could reach, and a union
// deliberately so — `Foo|Bar` is two types and the members reached through it are
// whichever the value happens to hold.
func (b *builder) unwrapType(t *sitter.Node) *sitter.Node {
	for depth := 0; t != nil && depth < 16; depth++ {
		switch t.Type(b.lang) {
		case "optional_type":
			t = firstNamedChild(t)
		case "named_type":
			return firstNamedChild(t)
		case "name", "qualified_name", "relative_name":
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

// claimSubtree marks every identifier under n as owned, so the reference
// patterns — which match inside a `use` declaration or a namespace declaration as
// readily as anywhere else — do not emit a second occurrence over bytes already
// described.
func (b *builder) claimSubtree(n *sitter.Node) {
	if n == nil {
		return
	}
	switch n.Type(b.lang) {
	case "name", "variable_name":
		b.claimed[span{n.StartByte(), n.EndByte()}] = true
	}
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

// bareName is the identifier a node spells, with PHP's `$` sigil removed.
//
// Dropping it is the one naming decision this stanza makes that Ruby's inverts,
// and the two are right for the same test. Ruby keeps the `@` of `@name` because
// `@name` is what is written at *every* site the member appears. PHP does not:
// the property is declared `public string $name` and read `$this->name`, so the
// `$` appears on one side and not the other, and a descriptor carrying it would
// be one the two sides could not agree on. It is a sigil marking a variable and
// not a letter of the name, and a static property proves it — `self::$count` and
// `$obj->count` reach the same member.
func (b *builder) bareName(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	if n.Type(b.lang) == "variable_name" {
		if inner := firstNamedChild(n); inner != nil {
			return b.text(inner)
		}
	}
	return strings.TrimPrefix(b.text(n), "$")
}

// directChild returns n's first named child of the given type, or nil.
func (b *builder) directChild(n *sitter.Node, nodeType string) *sitter.Node {
	if n == nil {
		return nil
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); c.Type(b.lang) == nodeType {
			return c
		}
	}
	return nil
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

// namespaceOf renders namespace segments as a SCIP namespace descriptor:
// slash-separated and slash-terminated. The empty namespace renders "", which is
// PHP's global namespace and is a real namespace rather than a missing one.
func namespaceOf(segs []string) string {
	if len(segs) == 0 {
		return ""
	}
	return strings.Join(segs, "/") + "/"
}

// joinPath renders path segments as a descriptor suffix: every segment but the
// last is a namespace, and the last carries term.
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

// reterminate turns a name's descriptor suffix into a namespace prefix, which is
// what an alias becomes when a longer path is written through it: `use X\Y as A;`
// binds `A` to `X/Y#`, and `A\B` means `X/Y/B`.
func reterminate(suffix string) string {
	for _, term := range []string{"().", "#", "."} {
		if strings.HasSuffix(suffix, term) {
			return strings.TrimSuffix(suffix, term) + "/"
		}
	}
	return suffix
}

// packageName is what a namespace occurrence is called in the `name` column: the
// last segment of its namespace, or — for the global namespace, which has no
// namespace at all — the last segment of the package's name.
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

// phpBuiltinTypes is the commonly used subset of the classes and interfaces PHP
// itself declares in the global namespace.
//
// It is what rootTarget needs and what PHP's syntax cannot supply: a global-
// namespace class name says nothing about whether it is the language's or this
// repository's. Everything in this set carries a foreign coordinate and so can
// never pollute descriptor matching within the indexed package; an omission costs
// one reference landing in this package's root namespace with nothing to match,
// which is what an unrecognised name gets anyway.
var phpBuiltinTypes = map[string]bool{
	"stdClass": true, "Closure": true, "Generator": true, "Fiber": true,
	"WeakMap": true, "WeakReference": true, "SplStack": true, "SplQueue": true,
	"SplObjectStorage": true, "SplFixedArray": true, "ArrayObject": true,
	"ArrayIterator": true, "SplFileObject": true, "SplFileInfo": true,
	"DateTime": true, "DateTimeImmutable": true, "DateTimeInterface": true,
	"DateInterval": true, "DateTimeZone": true, "DatePeriod": true,
	"PDO": true, "PDOStatement": true, "PDOException": true,
	"Throwable": true, "Exception": true, "Error": true, "TypeError": true,
	"ValueError": true, "ArgumentCountError": true, "ArithmeticError": true,
	"DivisionByZeroError": true, "RuntimeException": true, "LogicException": true,
	"InvalidArgumentException": true, "OutOfRangeException": true,
	"OutOfBoundsException": true, "LengthException": true, "DomainException": true,
	"RangeException": true, "OverflowException": true, "UnderflowException": true,
	"UnexpectedValueException": true, "BadFunctionCallException": true,
	"BadMethodCallException": true, "JsonException": true, "ErrorException": true,
	"Traversable": true, "Iterator": true, "IteratorAggregate": true,
	"ArrayAccess": true, "Countable": true, "Serializable": true,
	"JsonSerializable": true, "Stringable": true, "UnitEnum": true,
	"BackedEnum": true, "Attribute": true, "Reflection": true,
	"ReflectionClass": true, "ReflectionMethod": true, "ReflectionProperty": true,
	"ReflectionFunction": true, "ReflectionNamedType": true,
}

// phpBuiltinFuncs is the commonly used subset of PHP's global functions. It is
// what makes the language's global fallback readable: `strlen($s)` written inside
// `namespace App;` calls `\strlen`, and only a set like this can say so
// file-locally. The same omission rule applies as for phpBuiltinTypes.
var phpBuiltinFuncs = map[string]bool{
	"strlen": true, "strtolower": true, "strtoupper": true, "ucfirst": true,
	"lcfirst": true, "ucwords": true, "trim": true, "ltrim": true, "rtrim": true,
	"substr": true, "strpos": true, "stripos": true, "strrpos": true,
	"str_replace": true, "str_repeat": true, "str_split": true, "str_pad": true,
	"str_contains": true, "str_starts_with": true, "str_ends_with": true,
	"sprintf": true, "printf": true, "vsprintf": true, "number_format": true,
	"implode": true, "explode": true, "join": true, "nl2br": true,
	"htmlspecialchars": true, "strip_tags": true, "preg_match": true,
	"preg_match_all": true, "preg_replace": true, "preg_split": true,
	"preg_quote": true, "json_encode": true, "json_decode": true,
	"count": true, "array_map": true, "array_filter": true, "array_reduce": true,
	"array_merge": true, "array_keys": true, "array_values": true,
	"array_key_exists": true, "array_search": true, "array_slice": true,
	"array_splice": true, "array_reverse": true, "array_unique": true,
	"array_combine": true, "array_fill": true, "array_sum": true,
	"in_array": true, "sort": true, "usort": true, "uasort": true, "uksort": true,
	"ksort": true, "asort": true, "range": true, "compact": true, "extract": true,
	"is_array": true, "is_string": true, "is_int": true, "is_float": true,
	"is_bool": true, "is_null": true, "is_numeric": true, "is_callable": true,
	"is_object": true, "isset": true, "empty": true, "gettype": true,
	"intval": true, "floatval": true, "strval": true, "boolval": true,
	"var_dump": true, "print_r": true, "var_export": true,
	"abs": true, "max": true, "min": true, "round": true, "floor": true,
	"ceil": true, "intdiv": true, "pow": true, "sqrt": true, "rand": true,
	"mt_rand": true, "random_int": true,
	"file_get_contents": true, "file_put_contents": true, "fopen": true,
	"fclose": true, "fwrite": true, "fread": true, "file_exists": true,
	"is_dir": true, "mkdir": true, "unlink": true, "dirname": true,
	"basename": true, "realpath": true, "glob": true, "scandir": true,
	"date": true, "time": true, "mktime": true, "strtotime": true,
	"microtime": true, "sleep": true, "usleep": true,
	"function_exists": true, "class_exists": true, "method_exists": true,
	"property_exists": true, "get_class": true, "get_object_vars": true,
	"call_user_func": true, "call_user_func_array": true, "func_get_args": true,
	"iterator_to_array": true, "spl_autoload_register": true,
	"str_word_count": true, "md5": true, "sha1": true, "hash": true,
	"base64_encode": true, "base64_decode": true, "urlencode": true,
	"urldecode": true, "uniqid": true, "serialize": true, "unserialize": true,
}

// phpBuiltinConsts is the commonly used subset of PHP's global constants, for the
// same reason and with the same omission rule.
var phpBuiltinConsts = map[string]bool{
	"PHP_EOL": true, "PHP_INT_MAX": true, "PHP_INT_MIN": true, "PHP_INT_SIZE": true,
	"PHP_FLOAT_EPSILON": true, "PHP_FLOAT_MAX": true, "PHP_FLOAT_MIN": true,
	"PHP_VERSION": true, "PHP_MAJOR_VERSION": true, "PHP_MINOR_VERSION": true,
	"PHP_OS": true, "PHP_OS_FAMILY": true, "DIRECTORY_SEPARATOR": true,
	"PATH_SEPARATOR": true, "E_ALL": true, "E_ERROR": true, "E_WARNING": true,
	"E_NOTICE": true, "E_STRICT": true, "E_DEPRECATED": true,
	"JSON_THROW_ON_ERROR": true, "JSON_PRETTY_PRINT": true,
	"JSON_UNESCAPED_SLASHES": true, "JSON_UNESCAPED_UNICODE": true,
	"SORT_REGULAR": true, "SORT_NUMERIC": true, "SORT_STRING": true,
	"COUNT_RECURSIVE": true, "ARRAY_FILTER_USE_KEY": true,
	"ARRAY_FILTER_USE_BOTH": true, "M_PI": true, "M_E": true, "NAN": true,
	"INF": true,
}
