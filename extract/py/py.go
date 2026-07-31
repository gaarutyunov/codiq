// Package py is the Python stanza: the tree-sitter query in query.scm plus the
// mapper here that turns its captures into core facts (SPEC.md §5).
//
// The package is named for the extension, per SPEC.md §12. It imports facts and
// coord and deliberately not extract: it satisfies extract.Parser structurally,
// which is what keeps the registry free of an import cycle.
//
// The mapper's job, and its limits, are the Go and TypeScript stanzas' (§4.3):
// it builds the descriptor *suffix* from the CST and the module namespace the
// coordinate supplies; it assigns role and neutral-core symbol kind; and it
// resolves references whose target definition is in the same file. It does no
// type checking, runs no import resolution algorithm and looks at no other
// file. A reference it cannot pin down is still emitted, carrying the best
// descriptor syntax allows, and the link pass decides what it means (§7). Where
// a component is genuinely unknowable file-locally the descriptor writes SCIP's
// "." for it, so it names an unresolved symbol rather than false-matching a
// real one.
//
// # The unit of modularity
//
// Go's is the directory; TypeScript's is the file. Python appears to have both
// — a package is a directory holding `__init__.py`, a module is a file — and
// the choice here is that it does not: **a Python module is a file, and a
// package is the module whose file is named `__init__.py`, which therefore
// collapses to its directory** (moduleNamespace). One rule covers both, and it
// is the rule Python's own import machinery uses: `import a.b` reads
// `a/b.py`, or `a/b/__init__.py`, and both are the module `a.b`.
//
// That is not a filesystem probe (§2.5 forbids one): the collapse is a function
// of the *name* `__init__`, exactly as TypeScript's stanza drops a trailing
// `index` segment, and both sides of an import derive it without either reading
// the disk. Python is in fact easier than TypeScript here — an import states the
// module's dotted path and the path maps to the file path one segment per
// segment, where a Node specifier omits the extension and may or may not mean a
// directory.
//
// What it gets right: absolute imports inside the tree, relative imports at any
// level, `__init__.py` packages, and a `.pyi` stub sitting beside the module it
// describes. What it cannot represent:
//
//   - A src-layout (`src/mypkg/…` with the manifest at the repository root).
//     File paths are resolved against the coordinate's Root, so `src/mypkg/m.py`
//     derives `src/mypkg/m/` while `import mypkg.m` derives `mypkg/m/`, and the
//     two never join. The fix is not the stanza's: which directory is the
//     import root is manifest knowledge (`[tool.setuptools] package-dir` and its
//     equivalents), so it belongs in coord beside the name and the version.
//   - `from . import sibling`, where `sibling` may be a submodule or may be a
//     name the package's `__init__` exports. Only the filesystem distinguishes
//     them, so the binding is recorded as a symbol; a submodule reached that way
//     goes unresolved.
//   - Which of two same-named top-level modules an absolute import means. There
//     is no syntax separating `import os` from `import mypkg`, so a top-level
//     name is stdlib when it is one of pyStdlib's and this package's otherwise —
//     which is Python's own rule with sys.path taken on trust.
package py

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
const Lang = "py"

// Ext is the file extension this stanza is registered under.
const Ext = ".py"

//go:embed query.scm
var queryScheme string

// Parser is the Python stanza. Safe for concurrent use: the grammar and
// compiled query are immutable after the first Parse, and each parse checks a
// gotreesitter parser out of a pool.
type Parser struct {
	once    sync.Once
	lang    *sitter.Language
	pool    *sitter.ParserPool
	query   *sitter.Query
	initErr error
}

// New returns the Python parser. It is cheap: the grammar is loaded and
// query.scm compiled on the first Parse, so a binary that never parses Python
// never decompresses the Python grammar — and a query that fails to compile
// lands in ParseError rather than panicking at init.
func New() *Parser { return &Parser{} }

func (p *Parser) init() {
	p.once.Do(func() {
		p.lang = grammars.PythonLanguage()
		if p.lang == nil {
			p.initErr = errors.New("py: gotreesitter has no Python grammar")
			return
		}
		q, err := sitter.NewQuery(queryScheme, p.lang)
		if err != nil {
			p.initErr = fmt.Errorf("py: compile query.scm: %w", err)
			return
		}
		p.query = q
		p.pool = sitter.NewParserPool(p.lang)
	})
}

// Parse extracts one Python file's facts. It never returns an error: a failure
// is reported in FileFacts.ParseError with File still populated, so the caller
// can tell "this file has no facts" from "this file was never seen".
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
		ns:         moduleNamespace(rel),
		dir:        packageDir(rel),
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
	typeName string // syntactic type of a variable/parameter; "" when unknown
}

// moduleRec is a module an import named: the coordinate that owns it and the
// namespace descriptor of the module within that coordinate.
type moduleRec struct {
	coord coord.Coord
	ns    string
}

// bindingRec is a name a `from … import …` clause bound to one *symbol* of
// another module. It is TypeScript's named-import problem verbatim: the local
// name and the name the exporting module knows the symbol by differ under an
// alias, and the descriptor must carry the latter.
type bindingRec struct {
	moduleRec
	// orig is the name the exporting module knows the symbol by, which is the
	// name its own definition's descriptor was built from.
	orig string
}

type builder struct {
	lang  *sitter.Language
	src   []byte
	coord coord.Coord
	// ns is this file's module namespace: the descriptor prefix of every symbol
	// it defines.
	ns string
	// dir is the package this file belongs to — its directory, or its own
	// directory when it *is* an `__init__.py` — repo-relative and
	// slash-separated, and the base a relative import counts levels from.
	dir string

	out       facts.FileFacts
	nextScope facts.LocalID
	nextOcc   facts.LocalID

	scopes    []scopeRec
	scopeByID map[facts.LocalID]scopeRec

	// namespaces holds qualifiers that name a whole module (`import os`),
	// bindings the ones that name a single symbol in one (`from m import x`).
	namespaces map[string]moduleRec
	bindings   map[string]bindingRec

	// claimed holds identifier ranges a definition already owns, so a
	// reference pattern matching the same identifier is dropped.
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

// fileScope is the scope the whole module is, or NoID for an empty file.
// collectScopes sorts by (start ascending, end descending) and the `module`
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
// Go gets this from a `package` clause; neither TypeScript nor Python has one,
// so the query captures the whole file (`(module) @definition.package`) and the
// name comes from the path. Everything else about it is the Go stanza's package
// definition: neutral-core `package` kind, descriptor equal to the namespace,
// and a `defines` edge from the file — which together are exactly what link's
// `imports` derivation joins an importer's @import reference against.
//
// The occurrence is zero-width at the start of the file, for the reason the
// TypeScript stanza gives: there is no identifier to point at, and a range
// spanning the whole file would claim every other occurrence sits inside the
// module's *name*.
func (b *builder) collectModule(matches []sitter.QueryMatch) {
	for _, m := range matches {
		root, _, ok := roots(m, "definition.package")
		if !ok || root.Node.Type(b.lang) != "module" {
			continue
		}
		// Explicitly the file scope, not the innermost scope containing [0, 0).
		// A Python file may open with `class C:` on its first byte, and then the
		// class scope also starts at 0 and is the innermost — which would put
		// the module's own definition lexically inside one of its classes.
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

// collectImports records what an import statement binds and emits one module
// occurrence per module named.
//
// Import *edges* are cross-file and therefore the link pass's (§4.4); what is
// extracted is the occurrence, whose descriptor names the imported module. For
// a module that resolves inside this package that descriptor is byte-identical
// to the one the imported file's own module definition produces, which is what
// lets link derive `imports` by descriptor join.
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

	// `from … import …` names one module and binds several symbols, so its
	// clause is read once per statement; `import a, b` names two modules and is
	// one candidate each.
	seenFrom := map[span]bool{}
	for _, cd := range cands {
		switch cd.stmt.Type(b.lang) {
		case "import_statement":
			b.plainImport(cd.name)
		case "import_from_statement":
			s := span{cd.stmt.StartByte(), cd.stmt.EndByte()}
			if seenFrom[s] {
				continue
			}
			seenFrom[s] = true
			b.fromImport(cd.stmt, cd.name)
		}
	}
}

// plainImport handles `import a.b` and `import a.b as x`.
//
// What each form binds is Python's rule and not an approximation of it: the
// alias names the whole dotted module, and a bare `import a.b` binds only `a`
// — which is why `import os.path` lets you write `os.path.join` and not
// `path.join`.
func (b *builder) plainImport(name *sitter.Node) {
	dotted, alias := name, ""
	if name.Type(b.lang) == "aliased_import" {
		dotted = name.ChildByFieldName("name", b.lang)
		alias = b.fieldText(name, "alias")
	}
	spec := b.text(dotted)
	if spec == "" {
		return
	}

	c, ns := b.resolveModule(spec)
	b.addOccurrence(
		facts.Descriptor{Prefix: c, Suffix: ns},
		facts.RoleReference, facts.KindPackage, moduleName(c, ns),
		name.StartByte(), name.EndByte(),
	)

	if alias != "" {
		b.namespaces[alias] = moduleRec{coord: c, ns: ns}
		return
	}
	top, _, _ := strings.Cut(spec, ".")
	topCoord, topNS := b.resolveModule(top)
	b.namespaces[top] = moduleRec{coord: topCoord, ns: topNS}
}

// fromImport handles `from m import x, y as z` and `from .m import x`, in every
// relative level.
func (b *builder) fromImport(stmt, module *sitter.Node) {
	c, ns := b.resolveFromModule(module)
	mod := moduleRec{coord: c, ns: ns}

	b.addOccurrence(
		facts.Descriptor{Prefix: c, Suffix: ns},
		facts.RoleReference, facts.KindPackage, moduleName(c, ns),
		module.StartByte(), module.EndByte(),
	)

	for i := 0; i < stmt.ChildCount(); i++ {
		child := stmt.Child(i)
		if child == nil || stmt.FieldNameForChild(i, b.lang) != "name" {
			continue
		}
		orig, local := "", ""
		switch child.Type(b.lang) {
		case "dotted_name":
			orig = b.text(child)
			local = orig
		case "aliased_import":
			orig = b.fieldText(child, "name")
			local = b.fieldText(child, "alias")
		}
		if orig == "" || local == "" {
			continue // a wildcard import binds nothing nameable
		}
		b.bindings[local] = bindingRec{moduleRec: mod, orig: orig}
	}
}

// resolveModule turns an absolute dotted module path into the coordinate and
// namespace that name it.
//
// A module of this package keeps this coordinate and becomes a namespace, so a
// reference through it produces byte-identical descriptors to the definitions
// in the imported file — which is exactly what the link pass joins on. A
// standard-library module becomes a foreign coordinate with an unknown version:
// it cannot match anything indexed here, and it must not pretend to.
//
// There is no third case, and no syntax to build one from. `import os` and
// `import mypkg` are the same statement; only sys.path tells them apart, and
// reading it is neither file-local (§2.5) nor even a file. So the rule is
// "stdlib if the top-level name is one, this package otherwise", which is
// Python's own resolution order with the local tree trusted to be what it says
// — and a third-party import lands at a namespace of this package that holds no
// definitions, which is an unresolved reference and not a wrong edge.
func (b *builder) resolveModule(dotted string) (coord.Coord, string) {
	top, rest, _ := strings.Cut(dotted, ".")
	if pyStdlib[top] {
		return coord.Foreign(b.coord.Scheme, b.coord.Manager, top), dottedNamespace(rest)
	}
	return b.coord, dottedNamespace(dotted)
}

// resolveFromModule resolves the module half of a `from … import …`, which is
// either an absolute dotted path or a relative one.
//
// A relative import is pure path arithmetic against the file's own package, and
// it is the one import form Python makes unambiguous: one leading dot is this
// package, each further dot is one directory up. An import that climbs past the
// package root escapes what this coordinate owns, so it becomes foreign rather
// than silently wrapping round to the root.
func (b *builder) resolveFromModule(module *sitter.Node) (coord.Coord, string) {
	if module.Type(b.lang) != "relative_import" {
		return b.resolveModule(b.text(module))
	}

	level, tail := 0, ""
	for i := 0; i < module.NamedChildCount(); i++ {
		child := module.NamedChild(i)
		switch child.Type(b.lang) {
		case "import_prefix":
			level = strings.Count(b.text(child), ".")
		case "dotted_name":
			tail = b.text(child)
		}
	}

	base, ok := b.relativeBase(level)
	if !ok {
		return coord.Foreign(b.coord.Scheme, b.coord.Manager, b.text(module)), ""
	}
	return b.coord, moduleNamespace(path.Join(base, dottedPath(tail)))
}

// relativeBase climbs level-1 directories out of this file's package. It
// reports false when that leaves the package root, which no coordinate here
// owns.
func (b *builder) relativeBase(level int) (string, bool) {
	if level < 1 {
		return "", false
	}
	base := b.dir
	for i := 1; i < level; i++ {
		if base == "" {
			return "", false
		}
		base = path.Dir(base)
		if base == "." {
			base = ""
		}
	}
	return base, true
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
		suffix, ok := b.definitionSuffix(kind, cd.node, name)
		if !ok {
			// Not a definition after all — an attribute assignment on something
			// other than the receiver. Left unclaimed so it is emitted as the
			// reference it is.
			continue
		}
		b.claimed[s] = true

		desc := facts.Descriptor{Prefix: b.coord, Suffix: suffix}
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
// capture name does not. Python needs it more than TypeScript does: one node
// type covers both a function and a method, one covers both a class and what
// another language would call an interface, and one covers a local, a module
// global and a class attribute.
func (b *builder) refineKind(kind string, node *sitter.Node) string {
	switch kind {
	case facts.KindFunction:
		// A `def` directly inside a class body is a method. Nothing in the node
		// says so; the enclosing container does.
		if b.nearestContainer(node) == "class" {
			return facts.KindMethod
		}
	case facts.KindType:
		// `interface` is not a Python keyword, but declaring one is: a class
		// whose bases include typing.Protocol or abc.ABC exists to be
		// implemented. link keys `implements` off this kind, so recognising it
		// is what lets a Python protocol take part in the derivation at all.
		if b.isProtocol(node) {
			return facts.KindInterface
		}
	case facts.KindVariable:
		if b.nearestContainer(node) == "class" {
			return facts.KindField
		}
		// `Final` is the only thing in Python that says "constant". The
		// ALL_CAPS convention is not read: it is a convention, and a wrong
		// symbol_kind is worse than a conservative one.
		if isFinal(b.annotationText(node)) {
			return facts.KindConstant
		}
	}
	return kind
}

// isProtocol reports whether a class declares itself an interface, by listing
// Protocol or ABC among its bases. Both the bare and the qualified spelling
// count, since `typing.Protocol` and `abc.ABC` are how they are usually written
// when the module rather than the name was imported.
func (b *builder) isProtocol(node *sitter.Node) bool {
	bases := node.ChildByFieldName("superclasses", b.lang)
	if bases == nil {
		return false
	}
	for i := 0; i < bases.NamedChildCount(); i++ {
		switch bareName(b.text(bases.NamedChild(i))) {
		case "Protocol", "ABC":
			return true
		}
	}
	return false
}

// nearestContainer says whether the innermost named container of n is a class
// or a function, or "" when n is at module level. It is what tells a method
// from a function and a class attribute from a global.
func (b *builder) nearestContainer(n *sitter.Node) string {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "class_definition":
			return "class"
		case "function_definition", "lambda":
			return "function"
		}
	}
	return ""
}

// definitionSuffix builds the SCIP descriptor suffix for a definition from its
// capture hierarchy (SPEC.md §5). It reports false when the capture turns out
// not to name a definition at all.
func (b *builder) definitionSuffix(kind string, node *sitter.Node, name string) (string, bool) {
	// `self.x = …` declares a member of the class, not of the method it is
	// written in, so its container is the class and not the enclosing callable.
	if kind == facts.KindField && b.isAttributeAssignment(node) {
		suffix, ok := b.enclosingTypeSuffix(node)
		if !ok || !b.assignsThroughReceiver(node) {
			return "", false
		}
		return suffix + name + ".", true
	}

	container := b.containerSuffix(node)
	switch kind {
	case facts.KindFunction, facts.KindMethod:
		return container + name + "().", true
	case facts.KindType, facts.KindInterface:
		return container + name + "#", true
	case facts.KindParameter:
		return container + "(" + name + ")", true
	default: // field, variable, constant
		return container + name + ".", true
	}
}

// isAttributeAssignment reports whether node is `something.x = …`.
func (b *builder) isAttributeAssignment(node *sitter.Node) bool {
	if node.Type(b.lang) != "assignment" {
		return false
	}
	left := node.ChildByFieldName("left", b.lang)
	return left != nil && left.Type(b.lang) == "attribute"
}

// assignsThroughReceiver reports whether an attribute assignment writes through
// the method's own receiver — `self.x = …`, or `cls.x = …` in a classmethod.
// Anything else (`other.x = 1`) declares nothing about the class this file is
// in, and is left to be emitted as the reference it is.
//
// The receiver is recognised by name and not by position. Python's receiver is
// a plain first parameter with no syntax marking it, so *something* has to be
// taken on trust; `self`/`cls` is the convention every style guide and linter in
// the ecosystem enforces, and reading the first parameter instead would misfire
// on the one case that matters — a @staticmethod, whose first parameter is an
// ordinary argument.
func (b *builder) assignsThroughReceiver(node *sitter.Node) bool {
	left := node.ChildByFieldName("left", b.lang)
	if left == nil {
		return false
	}
	object := left.ChildByFieldName("object", b.lang)
	return object != nil && isReceiver(b.text(object))
}

// containerSuffix returns the descriptor suffix of the nearest enclosing named
// container of n — a class or a function — or the module namespace when there
// is none. A lambda and a comprehension are transparent: neither has a name, so
// neither has a descriptor of its own and what is inside belongs to the
// enclosing container.
func (b *builder) containerSuffix(n *sitter.Node) string {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "class_definition":
			if name := b.fieldText(p, "name"); name != "" {
				return b.containerSuffix(p) + name + "#"
			}
		case "function_definition":
			if name := b.fieldText(p, "name"); name != "" {
				return b.containerSuffix(p) + name + "()."
			}
		}
	}
	return b.ns
}

// enclosingTypeSuffix returns the descriptor suffix of the class n sits inside.
func (b *builder) enclosingTypeSuffix(n *sitter.Node) (string, bool) {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Type(b.lang) != "class_definition" {
			continue
		}
		if name := b.fieldText(p, "name"); name != "" {
			return b.containerSuffix(p) + name + "#", true
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
		if prev, dup := best[s]; dup && refPriority(prev.role) >= refPriority(cand.role) {
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
		return b.typeReferenceDescriptor(name)
	default:
		return b.readDescriptor(r, name)
	}
}

// callDescriptor names the target of a call.
//
// A construction is a call here, and cannot be anything else: Python has no
// `new`, so `Greeter("world")` is syntactically a call to the name `Greeter`
// and the descriptor says exactly that. It then matches nothing, because a
// class's descriptor ends `#` and this one ends `().` — an honest unresolved
// reference rather than a guess. The *type* the construction implies is still
// recovered, by inferredType, and it is what names the members reached through
// the value; a wrong guess there cannot produce a false edge for the same
// reason, since it would have to match a definition of the other shape.
func (b *builder) callDescriptor(r refRec, name string) (facts.Descriptor, string) {
	if fn := r.node.ChildByFieldName("function", b.lang); fn != nil && fn.Type(b.lang) == "identifier" {
		if bnd, ok := b.bindings[name]; ok {
			return facts.Descriptor{Prefix: bnd.coord, Suffix: bnd.ns + bnd.orig + "()."}, facts.KindFunction
		}
		if pyBuiltins[name] {
			return facts.Descriptor{Prefix: b.builtin(), Suffix: name + "()."}, facts.KindFunction
		}
		return facts.Descriptor{Prefix: b.coord, Suffix: b.ns + name + "()."}, facts.KindFunction
	}

	// A member call: resolve the object it hangs off.
	if q, ok := b.qualifierOf(r.node, r.nameNode.StartByte()); ok {
		if q.module {
			return facts.Descriptor{Prefix: q.coord, Suffix: q.suffix + name + "()."}, facts.KindFunction
		}
		return facts.Descriptor{Prefix: q.coord, Suffix: q.suffix + name + "()."}, facts.KindMethod
	}
	// Receiver unknowable file-locally: name it with SCIP's "." for the type so
	// it cannot false-match a real definition.
	return facts.Descriptor{Prefix: b.coord, Suffix: b.ns + coord.Unknown + "#" + name + "()."}, facts.KindMethod
}

func (b *builder) readDescriptor(r refRec, name string) (facts.Descriptor, string) {
	// The object side of an attribute: a module qualifier, an imported binding,
	// or a value in scope. Both halves of `a.b` are plain `identifier` nodes in
	// Python — TypeScript's `property_identifier` has no counterpart — so the
	// two are told apart by which field of the attribute the capture sits in.
	if b.isObjectSide(r) {
		if mod, ok := b.namespaces[name]; ok {
			return facts.Descriptor{Prefix: mod.coord, Suffix: mod.ns}, facts.KindPackage
		}
		if bnd, ok := b.bindings[name]; ok {
			return facts.Descriptor{Prefix: bnd.coord, Suffix: bnd.ns + bnd.orig + "."}, facts.KindVariable
		}
		if def, ok := b.lookup(name, r.nameNode.StartByte()); ok {
			return b.occurrence(def.occ).Descriptor, facts.KindVariable
		}
		if pyBuiltins[name] {
			return facts.Descriptor{Prefix: b.builtin(), Suffix: name + "."}, facts.KindVariable
		}
		return facts.Descriptor{Prefix: b.coord, Suffix: b.ns + name + "."}, facts.KindVariable
	}

	// The attribute side.
	if q, ok := b.qualifierOf(r.node, r.nameNode.StartByte()); ok {
		if q.module {
			return facts.Descriptor{Prefix: q.coord, Suffix: q.suffix + name + "."}, facts.KindVariable
		}
		return facts.Descriptor{Prefix: q.coord, Suffix: q.suffix + name + "."}, facts.KindField
	}
	return facts.Descriptor{Prefix: b.coord, Suffix: b.ns + coord.Unknown + "#" + name + "."}, facts.KindField
}

func (b *builder) typeReferenceDescriptor(name string) (facts.Descriptor, string) {
	c, suffix := b.typeSuffix(name)
	return facts.Descriptor{Prefix: c, Suffix: suffix}, facts.KindType
}

// typeSuffix names a type by the coordinate and descriptor suffix it lives at.
// The name may be qualified ("mod.T") when it came from an annotation.
func (b *builder) typeSuffix(typeName string) (coord.Coord, string) {
	if qualifier, bare, qualified := strings.Cut(typeName, "."); qualified {
		if mod, ok := b.namespaces[qualifier]; ok {
			return mod.coord, mod.ns + bare + "#"
		}
		return b.coord, b.ns + coord.Unknown + "#"
	}
	if bnd, ok := b.bindings[typeName]; ok {
		return bnd.coord, bnd.ns + bnd.orig + "#"
	}
	if pyBuiltinTypes[typeName] {
		return b.builtin(), typeName + "#"
	}
	return b.coord, b.ns + typeName + "#"
}

// ------------------------------------------------------------ local lookup ---

// qualifier is what an attribute's object resolved to: the coordinate and
// descriptor suffix its members hang off, and whether it is a whole module
// rather than a value — the difference between `os.getcwd()` naming a function
// of a module and `g.greet()` naming a method of an object.
type qualifier struct {
	coord  coord.Coord
	suffix string
	module bool
}

// qualifierOf resolves the object half of the attribute at or under n.
func (b *builder) qualifierOf(n *sitter.Node, pos uint32) (qualifier, bool) {
	object := b.attributeObject(n)
	if object == nil || object.Type(b.lang) != "identifier" {
		return qualifier{}, false
	}

	name := b.text(object)
	if isReceiver(name) {
		// `self.x` inside a class body names a member of that class, and the
		// class is in this file, so this is the one receiver a stanza can always
		// resolve — the Python counterpart of a Go method's receiver.
		if suffix, ok := b.enclosingTypeSuffix(object); ok {
			return qualifier{coord: b.coord, suffix: suffix}, true
		}
	}
	if mod, ok := b.namespaces[name]; ok {
		return qualifier{coord: mod.coord, suffix: mod.ns, module: true}, true
	}
	if bnd, ok := b.bindings[name]; ok {
		return qualifier{coord: bnd.coord, suffix: bnd.ns + bnd.orig + "#"}, true
	}
	if typeName, ok := b.localTypeAt(name, pos); ok && typeName != "" {
		c, suffix := b.typeSuffix(typeName)
		return qualifier{coord: c, suffix: suffix}, true
	}
	if pyBuiltins[name] {
		return qualifier{coord: b.builtin(), suffix: name + "#"}, true
	}
	return qualifier{}, false
}

// lookup finds the definition named name that is visible at byte offset pos:
// among definitions whose declaring scope contains pos, the one in the
// innermost such scope, declared no later than pos.
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

// declaredType recovers the syntactic type of a variable, parameter or
// attribute definition — enough to name the methods and fields reached through
// it, without any type checking. "" means unknown, which downstream becomes
// SCIP's "." rather than a guess.
func (b *builder) declaredType(node *sitter.Node) string {
	if t := b.annotationText(node); t != "" {
		return t
	}
	if node.Type(b.lang) == "assignment" {
		return b.inferredType(node.ChildByFieldName("right", b.lang))
	}
	return ""
}

// annotationText reduces a node's annotation to the bare name a descriptor can
// use, possibly qualified. Composite annotations (a union, a tuple, a callable)
// have no single name and yield "".
func (b *builder) annotationText(node *sitter.Node) string {
	switch node.Type(b.lang) {
	case "assignment", "typed_parameter", "typed_default_parameter":
		return b.unwrapType(node.ChildByFieldName("type", b.lang))
	}
	return ""
}

// unwrapType reduces a type expression to its bare name. A forward reference
// ("Greeter" in quotes) is read through, because the quotes are Python's way of
// naming a type that is not defined yet and the name inside them is the type.
func (b *builder) unwrapType(t *sitter.Node) string {
	for depth := 0; t != nil && depth < 16; depth++ {
		switch t.Type(b.lang) {
		case "type":
			t = firstNamedChild(t)
		case "identifier", "attribute":
			return b.text(t)
		case "string":
			return b.stringContent(t)
		case "generic_type":
			// `list[int]` is named by its constructor: the members reached
			// through it are the constructor's.
			t = firstNamedChild(t)
		default:
			return ""
		}
	}
	return ""
}

// inferredType reads a type off an initialising expression. Only the one form
// whose type is written in the source is handled — a call whose callee is a
// name — and Python cannot say whether that name is a class or a function, so
// this is a guess. It is a safe one: a wrong answer produces a descriptor
// naming a member of a type that does not exist, which matches nothing, because
// a class descriptor ends `#` and a function's ends `().`.
func (b *builder) inferredType(expr *sitter.Node) string {
	for depth := 0; expr != nil && depth < 16; depth++ {
		switch expr.Type(b.lang) {
		case "parenthesized_expression":
			expr = firstNamedChild(expr)
		case "call":
			fn := expr.ChildByFieldName("function", b.lang)
			if fn == nil {
				return ""
			}
			switch fn.Type(b.lang) {
			case "identifier", "attribute":
				return b.text(fn)
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

// builtin is the coordinate of Python's predeclared names, which belong to no
// distributed package. `builtins` is the module they actually live in.
func (b *builder) builtin() coord.Coord {
	return coord.Foreign(b.coord.Scheme, b.coord.Manager, "builtins")
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

// stringContent returns what a string literal holds, without its quotes. The
// grammar splits a string into start / content / end, so the content child is
// the answer and there is nothing to trim.
func (b *builder) stringContent(n *sitter.Node) string {
	for i := 0; i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); c.Type(b.lang) == "string_content" {
			return b.text(c)
		}
	}
	return ""
}

// attributeObject returns the object half of the attribute at n, unwrapping a
// call to the attribute it calls.
func (b *builder) attributeObject(n *sitter.Node) *sitter.Node {
	attr := n
	if attr.Type(b.lang) == "call" {
		attr = attr.ChildByFieldName("function", b.lang)
	}
	if attr == nil || attr.Type(b.lang) != "attribute" {
		return nil
	}
	return attr.ChildByFieldName("object", b.lang)
}

// isObjectSide reports whether the captured identifier is the object of the
// attribute rather than the attribute name.
func (b *builder) isObjectSide(r refRec) bool {
	if r.node.Type(b.lang) != "attribute" {
		return false
	}
	object := r.node.ChildByFieldName("object", b.lang)
	return object != nil && object.StartByte() == r.nameNode.StartByte()
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

// isReceiver reports whether a name is a method's receiver by Python's
// universal convention.
func isReceiver(name string) bool { return name == "self" || name == "cls" }

// isFinal reports whether an annotation declares a constant. `Final` and
// `Final[int]` both do, under either spelling of the import.
func isFinal(annotation string) bool { return bareName(annotation) == "Final" }

// bareName drops the module qualifier from a dotted name, so that `Protocol`
// and `typing.Protocol` — the same thing under two spellings of the import —
// are recognised as one.
func bareName(qualified string) string {
	if i := strings.LastIndex(qualified, "."); i >= 0 {
		return qualified[i+1:]
	}
	return qualified
}

// moduleExts are the extensions a Python module file may carry, longest first
// so that a stub is stripped whole.
var moduleExts = []string{".pyi", ".py"}

// initModule is the file name that makes a directory a package. A module by
// that name *is* its directory, which is what collapses Python's two units of
// modularity into one.
const initModule = "__init__"

// repoRelative renders filePath relative to the package root, slash-separated.
// It is "" when there is no root or the file is outside it, which makes every
// namespace below empty — the honest answer when nothing says where the package
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

// moduleNamespace turns a package-relative module path into a SCIP namespace
// descriptor: slash-separated and slash-terminated, with the file extension
// removed and a trailing `__init__` segment dropped.
//
// Both normalisations exist so that the two sides of an import agree without
// either reading the disk. The extension goes because an import states a dotted
// module path and never a file name. `__init__` goes because `pkg/__init__.py`
// *is* the module `pkg` — it is the whole of what makes a directory a package
// — so the file and the directory have to render one namespace, and choosing
// the directory's is what makes `import pkg` land on it.
func moduleNamespace(modulePath string) string {
	if modulePath == "" {
		return ""
	}
	p := path.Clean(modulePath)
	if p == "." || p == "/" {
		return ""
	}
	for _, ext := range moduleExts {
		if trimmed, ok := strings.CutSuffix(p, ext); ok {
			p = trimmed
			break
		}
	}
	p = strings.TrimSuffix(p, "/"+initModule)
	if p == "" || p == "." || p == initModule {
		return ""
	}
	return p + "/"
}

// packageDir is the package a file belongs to: its directory. An `__init__.py`
// is not a special case — the file lives in the directory it is the package of,
// so `path.Dir` is already the right answer for it and for every other module,
// which is what makes a relative import's level arithmetic one rule.
func packageDir(rel string) string {
	if rel == "" {
		return ""
	}
	dir := path.Dir(rel)
	if dir == "." {
		return ""
	}
	return dir
}

// dottedPath turns a dotted module path into a slash-separated one.
func dottedPath(dotted string) string {
	return strings.ReplaceAll(dotted, ".", "/")
}

// dottedNamespace turns a dotted module path into a SCIP namespace descriptor.
func dottedNamespace(dotted string) string {
	return moduleNamespace(dottedPath(dotted))
}

// moduleName is what a module occurrence is called in the `name` column: the
// last segment of its namespace, or — for the module at a package's root, which
// has no namespace of its own — the last segment of the package's name.
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

// pyBuiltins and pyBuiltinTypes are Python's predeclared names — the contents
// of the `builtins` module, which belongs to no distributed package. References
// to them carry a foreign coordinate and so never pollute descriptor matching
// within the indexed package. The lists are the commonly used subset rather
// than the whole of `builtins`: an omission costs one reference landing in this
// package's namespace with nothing to match, which is what an unrecognised name
// gets anyway.
var pyBuiltins = map[string]bool{
	"print": true, "len": true, "range": true, "open": true, "input": true,
	"abs": true, "all": true, "any": true, "ascii": true, "bin": true,
	"callable": true, "chr": true, "dir": true, "divmod": true,
	"enumerate": true, "eval": true, "exec": true, "filter": true,
	"format": true, "getattr": true, "globals": true, "hasattr": true,
	"hash": true, "help": true, "hex": true, "id": true, "isinstance": true,
	"issubclass": true, "iter": true, "locals": true, "map": true,
	"max": true, "min": true, "next": true, "oct": true, "ord": true,
	"pow": true, "repr": true, "reversed": true, "round": true,
	"setattr": true, "sorted": true, "sum": true, "vars": true, "zip": true,
	"super": true, "__import__": true,
	// The types double as constructors, so they are callable names too.
	"bool": true, "bytearray": true, "bytes": true, "complex": true,
	"dict": true, "float": true, "frozenset": true, "int": true,
	"list": true, "object": true, "set": true, "slice": true, "str": true,
	"tuple": true, "type": true,
}

var pyBuiltinTypes = map[string]bool{
	"bool": true, "bytearray": true, "bytes": true, "complex": true,
	"dict": true, "float": true, "frozenset": true, "int": true,
	"list": true, "memoryview": true, "object": true, "set": true,
	"slice": true, "str": true, "tuple": true, "type": true,
	"BaseException": true, "Exception": true, "ValueError": true,
	"TypeError": true, "KeyError": true, "IndexError": true,
	"RuntimeError": true, "StopIteration": true, "NotImplementedError": true,
	"AttributeError": true, "OSError": true,
}

// pyStdlib is the set of top-level standard-library module names.
//
// It is what resolveModule needs and what Python's syntax cannot supply:
// `import os` and `import mypkg` are the same statement, so the only way to
// know that one names a package outside this tree is to know the standard
// library's contents. The set is the stable, commonly imported subset; a module
// missing from it is treated as this package's, which yields a namespace with
// no definitions in it — an unresolved reference rather than a wrong edge.
var pyStdlib = map[string]bool{
	"abc": true, "argparse": true, "array": true, "ast": true,
	"asyncio": true, "base64": true, "binascii": true, "bisect": true,
	"builtins": true, "calendar": true, "collections": true, "colorsys": true,
	"concurrent": true, "configparser": true, "contextlib": true,
	"contextvars": true, "copy": true, "csv": true, "ctypes": true,
	"dataclasses": true, "datetime": true, "decimal": true, "difflib": true,
	"dis": true, "email": true, "enum": true, "errno": true, "faulthandler": true,
	"fnmatch": true, "fractions": true, "functools": true, "gc": true,
	"getpass": true, "glob": true, "graphlib": true, "gzip": true,
	"hashlib": true, "heapq": true, "hmac": true, "html": true, "http": true,
	"importlib": true, "inspect": true, "io": true, "ipaddress": true,
	"itertools": true, "json": true, "keyword": true, "linecache": true,
	"locale": true, "logging": true, "lzma": true, "math": true,
	"mimetypes": true, "multiprocessing": true, "numbers": true,
	"operator": true, "os": true, "pathlib": true, "pickle": true,
	"platform": true, "pprint": true, "queue": true, "random": true,
	"re": true, "readline": true, "reprlib": true, "sched": true,
	"secrets": true, "select": true, "selectors": true, "shlex": true,
	"shutil": true, "signal": true, "site": true, "smtplib": true,
	"socket": true, "socketserver": true, "sqlite3": true, "ssl": true,
	"stat": true, "statistics": true, "string": true, "struct": true,
	"subprocess": true, "sys": true, "sysconfig": true, "tarfile": true,
	"tempfile": true, "textwrap": true, "threading": true, "time": true,
	"timeit": true, "tkinter": true, "token": true, "tokenize": true,
	"tomllib": true, "trace": true, "traceback": true, "types": true,
	"typing": true, "unicodedata": true, "unittest": true, "urllib": true,
	"uuid": true, "venv": true, "warnings": true, "wave": true,
	"weakref": true, "webbrowser": true, "xml": true, "zipfile": true,
	"zipimport": true, "zlib": true, "zoneinfo": true,
}
