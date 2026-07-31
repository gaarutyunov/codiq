// Package ts is the TypeScript stanza: the tree-sitter query in query.scm plus
// the mapper here that turns its captures into core facts (SPEC.md §5).
//
// The package is named for the extension, per SPEC.md §12. It imports facts and
// coord and deliberately not extract: it satisfies extract.Parser structurally,
// which is what keeps the registry free of an import cycle.
//
// The mapper's job, and its limits, are the Go stanza's (extract/golang): it
// builds the descriptor *suffix* from the CST and the module namespace the
// coordinate supplies (§4.3); it assigns role and neutral-core symbol kind; and
// it resolves references whose target definition is in the same file. It does no
// type checking, runs no module resolution algorithm and looks at no other file.
// A reference it cannot pin down is still emitted, carrying the best descriptor
// syntax allows, and the link pass decides what it means (§7). Where a component
// is genuinely unknowable file-locally the descriptor writes SCIP's "." for it,
// so it names an unresolved symbol rather than false-matching a real one.
//
// One thing differs from Go, and it is the unit of modularity. A Go package is a
// directory and is named by a `package` clause the CST carries; a TypeScript
// module is a *file* and has no clause naming it. So the namespace here is
// derived from the file's own path rather than read out of the source, and the
// module's definition occurrence is emitted for the whole file rather than for a
// declaration inside it (moduleNamespace, collectModule).
package ts

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
const Lang = "ts"

// Ext is the file extension this stanza is registered under.
const Ext = ".ts"

//go:embed query.scm
var queryScheme string

// Parser is the TypeScript stanza. Safe for concurrent use: the grammar and
// compiled query are immutable after the first Parse, and each parse checks a
// gotreesitter parser out of a pool.
type Parser struct {
	once    sync.Once
	lang    *sitter.Language
	pool    *sitter.ParserPool
	query   *sitter.Query
	initErr error
}

// New returns the TypeScript parser. It is cheap: the grammar is loaded and
// query.scm compiled on the first Parse, so a binary that never parses
// TypeScript never decompresses the TypeScript grammar.
func New() *Parser { return &Parser{} }

func (p *Parser) init() {
	p.once.Do(func() {
		p.lang = grammars.TypescriptLanguage()
		if p.lang == nil {
			p.initErr = errors.New("ts: gotreesitter has no TypeScript grammar")
			return
		}
		q, err := sitter.NewQuery(queryScheme, p.lang)
		if err != nil {
			p.initErr = fmt.Errorf("ts: compile query.scm: %w", err)
			return
		}
		p.query = q
		p.pool = sitter.NewParserPool(p.lang)
	})
}

// Parse extracts one TypeScript file's facts. It never returns an error: a
// failure is reported in FileFacts.ParseError with File still populated, so the
// caller can tell "this file has no facts" from "this file was never seen".
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
		dir:        path.Dir(rel),
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
	typeName string // syntactic type of a variable/parameter/field; "" when unknown
}

// moduleRec is a module a specifier named: the coordinate that owns it and the
// namespace descriptor of the module within that coordinate.
type moduleRec struct {
	coord coord.Coord
	ns    string
}

// bindingRec is a name an import clause bound to one *symbol* of another module.
// TypeScript's named imports have no counterpart in Go, where an import binds a
// package and every use is qualified: here `import { Greeter } from "./greeter"`
// puts a bare `Greeter` in scope, so the mapper has to remember which module it
// came from and what it is called *there* — an aliased import is used under one
// name and defined under another, and the descriptor must carry the latter.
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
	// dir is the file's directory, repo-relative and slash-separated, against
	// which a relative import specifier is resolved.
	dir string

	out       facts.FileFacts
	nextScope facts.LocalID
	nextOcc   facts.LocalID

	scopes    []scopeRec
	scopeByID map[facts.LocalID]scopeRec

	// namespaces holds qualifiers that name a whole module (`import * as fs`),
	// bindings the ones that name a single symbol in one (`import { x }`).
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

// ------------------------------------------------------------------ module ---

// collectModule emits the definition of the module the file *is*.
//
// Go gets this from a `package` clause; TypeScript has no such clause, so the
// query captures the whole file (`(program) @definition.package`) and the name
// comes from the path. Everything else about it is the Go stanza's package
// definition: neutral-core `package` kind, descriptor equal to the namespace,
// and a `defines` edge from the file — which together are exactly what link's
// `imports` derivation joins an importer's @import reference against.
//
// The occurrence is zero-width at the start of the file. There is no identifier
// to point at, and a range spanning the whole file would claim every scope and
// every other occurrence sits inside the module's *name*, which is not a thing.
func (b *builder) collectModule(matches []sitter.QueryMatch) {
	for _, m := range matches {
		root, _, ok := roots(m, "definition.package")
		if !ok || root.Node.Type(b.lang) != "program" {
			continue
		}
		desc := facts.Descriptor{Prefix: b.coord, Suffix: b.ns}
		occ := b.addOccurrence(desc, facts.RoleDefinition, facts.KindPackage, moduleName(b.coord, b.ns), 0, 0)
		b.edge(facts.EdgeDefines, facts.FileRef(), facts.OccurrenceRef(occ))
		if _, dup := b.descIndex[desc.String()]; !dup {
			b.descIndex[desc.String()] = occ
		}
		return
	}
}

// ----------------------------------------------------------------- imports ---

// collectImports records what an import statement binds and emits one module
// occurrence per statement.
//
// Import *edges* are cross-file and therefore the link pass's (§4.4); what is
// extracted is the occurrence, whose descriptor names the imported module. For a
// specifier that resolves inside this package that descriptor is byte-identical
// to the one the imported file's own module definition produces, which is what
// lets link derive `imports` by descriptor join.
func (b *builder) collectImports(matches []sitter.QueryMatch) {
	type cand struct {
		stmt   *sitter.Node
		source *sitter.Node
	}
	var cands []cand
	for _, m := range matches {
		if root, name, ok := roots(m, "import"); ok && name != nil {
			cands = append(cands, cand{stmt: root.Node, source: name.Node})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		return cands[i].stmt.StartByte() < cands[j].stmt.StartByte()
	})

	for _, cd := range cands {
		spec := strings.Trim(b.text(cd.source), "\"'`")
		if spec == "" {
			continue
		}
		var mod moduleRec
		mod.coord, mod.ns = b.resolveSpecifier(spec)
		b.bindClause(cd.stmt, mod)

		b.addOccurrence(
			facts.Descriptor{Prefix: mod.coord, Suffix: mod.ns},
			facts.RoleReference, facts.KindPackage, moduleName(mod.coord, mod.ns),
			cd.stmt.StartByte(), cd.stmt.EndByte(),
		)
	}
}

// bindClause records the names an import statement puts in scope. A statement
// with no clause (`import "./polyfill"`) binds nothing and is only an edge.
func (b *builder) bindClause(stmt *sitter.Node, mod moduleRec) {
	clause := b.namedChildOfType(stmt, "import_clause")
	if clause == nil {
		return
	}
	for i := 0; i < clause.NamedChildCount(); i++ {
		child := clause.NamedChild(i)
		switch child.Type(b.lang) {
		case "identifier":
			// `import def from "m"`: the local name is bound to the module's
			// default export, which is what that export is called there.
			b.bindings[b.text(child)] = bindingRec{moduleRec: mod, orig: "default"}
		case "namespace_import":
			// `import * as ns from "m"`: a qualifier for the whole module, the
			// one TypeScript form that behaves like a Go import.
			if id := firstNamedChild(child); id != nil {
				b.namespaces[b.text(id)] = mod
			}
		case "named_imports":
			for j := 0; j < child.NamedChildCount(); j++ {
				spec := child.NamedChild(j)
				if spec.Type(b.lang) != "import_specifier" {
					continue
				}
				orig := b.fieldText(spec, "name")
				if orig == "" {
					continue
				}
				local := b.fieldText(spec, "alias")
				if local == "" {
					local = orig
				}
				b.bindings[local] = bindingRec{moduleRec: mod, orig: orig}
			}
		}
	}
}

// resolveSpecifier turns a module specifier into the coordinate and namespace
// that name the module it refers to.
//
// A relative specifier stays inside this package and becomes a namespace, so a
// reference through it produces byte-identical descriptors to the definitions in
// the imported file — which is exactly what the link pass joins on. A bare
// specifier that is not this package's own name becomes a foreign coordinate
// with an unknown version: it cannot match anything indexed here, and it must
// not pretend to.
//
// No module resolution algorithm runs. Node's is a filesystem search over
// extensions, `main` fields, `exports` maps and tsconfig paths, and every one of
// those reads a file other than the one being parsed (§2.5). What is done
// instead is the purely syntactic normalisation in moduleNamespace, chosen so
// that the two sides of an import agree without either consulting the disk.
func (b *builder) resolveSpecifier(spec string) (coord.Coord, string) {
	if isRelative(spec) {
		joined := path.Join(b.dir, spec)
		if joined == ".." || strings.HasPrefix(joined, "../") {
			// Escapes the package root: nothing indexed here owns it.
			return coord.Foreign(b.coord.Scheme, b.coord.Manager, spec), ""
		}
		return b.coord, moduleNamespace(joined)
	}

	name, sub := splitPackage(spec)
	if b.coord.Name != "" && b.coord.Name != coord.Unknown && name == b.coord.Name {
		return b.coord, moduleNamespace(sub)
	}
	return coord.Foreign(b.coord.Scheme, b.coord.Manager, name), moduleNamespace(sub)
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
		b.claimed[s] = true

		name := b.text(cd.name)
		if name == "" || name == "_" {
			continue
		}
		desc := facts.Descriptor{
			Prefix: b.coord,
			Suffix: b.definitionSuffix(cd.kind, cd.node, name),
		}
		kind := b.refineKind(cd.kind, cd.node)
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
// capture name does not. `@definition.variable` covers every declarator, but a
// `const` is the neutral core's `constant` — the same distinction the Go stanza
// gets for free from `const_spec` versus `var_spec`.
func (b *builder) refineKind(kind string, node *sitter.Node) string {
	if kind != facts.KindVariable {
		return kind
	}
	p := node.Parent()
	if p == nil || p.Type(b.lang) != "lexical_declaration" {
		return kind
	}
	for i := 0; i < p.ChildCount(); i++ {
		if c := p.Child(i); c != nil && c.Type(b.lang) == "const" {
			return facts.KindConstant
		}
	}
	return kind
}

// definitionSuffix builds the SCIP descriptor suffix for a definition from its
// capture hierarchy (SPEC.md §5).
func (b *builder) definitionSuffix(kind string, node *sitter.Node, name string) string {
	container := b.containerSuffix(node)
	switch kind {
	case facts.KindFunction, facts.KindMethod:
		return container + name + "()."
	case facts.KindType, facts.KindInterface:
		return container + name + "#"
	case facts.KindModule:
		// A `namespace` declaration is a nested namespace, so it extends the
		// descriptor path the way a directory does rather than naming a symbol.
		return container + name + "/"
	case facts.KindParameter:
		if node.Type(b.lang) == "type_parameter" {
			return container + "[" + name + "]"
		}
		return container + "(" + name + ")"
	default: // field, variable, constant
		return container + name + "."
	}
}

// containerSuffix returns the descriptor suffix of the nearest enclosing named
// container of n — a class, interface, enum, type alias, function, method or
// namespace — or the module namespace when there is none. A function expression
// and an arrow function are transparent: an anonymous function has no descriptor
// of its own, so names inside it belong to the enclosing one.
func (b *builder) containerSuffix(n *sitter.Node) string {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "class_declaration", "abstract_class_declaration", "interface_declaration",
			"type_alias_declaration", "enum_declaration":
			if name := b.fieldText(p, "name"); name != "" {
				return b.containerSuffix(p) + name + "#"
			}
		case "function_declaration", "generator_function_declaration",
			"method_definition", "method_signature", "abstract_method_signature":
			if name := b.fieldText(p, "name"); name != "" {
				return b.containerSuffix(p) + name + "()."
			}
		case "internal_module":
			if name := b.fieldText(p, "name"); name != "" {
				return b.containerSuffix(p) + name + "/"
			}
		}
	}
	return b.ns
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
		return b.typeReferenceDescriptor(r, name)
	default:
		return b.readDescriptor(r, name)
	}
}

func (b *builder) callDescriptor(r refRec, name string) (facts.Descriptor, string) {
	// A bare identifier call is either an imported binding, an ambient global,
	// or a function declared in this module.
	if r.nameNode.Type(b.lang) == "identifier" {
		if bnd, ok := b.bindings[name]; ok {
			return facts.Descriptor{Prefix: bnd.coord, Suffix: bnd.ns + bnd.orig + "()."}, facts.KindFunction
		}
		if tsGlobals[name] {
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
	// The object side of a member expression: a module qualifier, an imported
	// binding, or a value in scope. The node type is enough to tell the two
	// sides apart — `a.b` puts an `identifier` in the object slot and a
	// `property_identifier` in the property slot, and computed access (`a[b]`)
	// is a subscript_expression this query never matches.
	if r.nameNode.Type(b.lang) == "identifier" {
		if mod, ok := b.namespaces[name]; ok {
			return facts.Descriptor{Prefix: mod.coord, Suffix: mod.ns}, facts.KindPackage
		}
		if bnd, ok := b.bindings[name]; ok {
			return facts.Descriptor{Prefix: bnd.coord, Suffix: bnd.ns + bnd.orig + "."}, facts.KindVariable
		}
		if def, ok := b.lookup(name, r.nameNode.StartByte()); ok {
			return b.occurrence(def.occ).Descriptor, facts.KindVariable
		}
		if tsGlobals[name] {
			return facts.Descriptor{Prefix: b.builtin(), Suffix: name + "."}, facts.KindVariable
		}
		return facts.Descriptor{Prefix: b.coord, Suffix: b.ns + name + "."}, facts.KindVariable
	}

	// The property side of a member expression.
	if q, ok := b.qualifierOf(r.node, r.nameNode.StartByte()); ok {
		if q.module {
			return facts.Descriptor{Prefix: q.coord, Suffix: q.suffix + name + "."}, facts.KindVariable
		}
		return facts.Descriptor{Prefix: q.coord, Suffix: q.suffix + name + "."}, facts.KindField
	}
	return facts.Descriptor{Prefix: b.coord, Suffix: b.ns + coord.Unknown + "#" + name + "."}, facts.KindField
}

func (b *builder) typeReferenceDescriptor(r refRec, name string) (facts.Descriptor, string) {
	// `ns.T` parses as a nested_type_identifier whose name half is the captured
	// type_identifier; resolve the module half through the namespace qualifiers.
	if parent := r.nameNode.Parent(); parent != nil && parent.Type(b.lang) == "nested_type_identifier" {
		if qualifier := firstNamedChild(parent); qualifier != nil && qualifier != r.nameNode {
			if mod, ok := b.namespaces[b.text(qualifier)]; ok {
				return facts.Descriptor{Prefix: mod.coord, Suffix: mod.ns + name + "#"}, facts.KindType
			}
		}
		return facts.Descriptor{Prefix: b.coord, Suffix: b.ns + coord.Unknown + "#"}, facts.KindType
	}
	c, suffix := b.typeSuffix(name)
	return facts.Descriptor{Prefix: c, Suffix: suffix}, facts.KindType
}

// typeSuffix names a type by the coordinate and descriptor suffix it lives at.
// The name may be qualified ("ns.T") when it came from a declared type.
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
	if tsGlobalTypes[typeName] {
		return b.builtin(), typeName + "#"
	}
	return b.coord, b.ns + typeName + "#"
}

// ------------------------------------------------------------ local lookup ---

// qualifier is what a member expression's object resolved to: the coordinate and
// descriptor suffix its members hang off, and whether it is a whole module
// rather than a value — the difference between `fs.readFile()` naming a function
// of a module and `greeter.greet()` naming a method of an object.
type qualifier struct {
	coord  coord.Coord
	suffix string
	module bool
}

// qualifierOf resolves the object half of the member expression at or under n.
func (b *builder) qualifierOf(n *sitter.Node, pos uint32) (qualifier, bool) {
	object := b.memberObject(n)
	if object == nil {
		return qualifier{}, false
	}
	switch object.Type(b.lang) {
	case "this":
		// `this.x` inside a class body names a member of that class, and the
		// class is in this file, so this is the one receiver a stanza can always
		// resolve — the TypeScript counterpart of a Go method's receiver.
		if suffix, ok := b.enclosingTypeSuffix(object); ok {
			return qualifier{coord: b.coord, suffix: suffix}, true
		}
	case "identifier":
		name := b.text(object)
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
		if tsGlobals[name] {
			return qualifier{coord: b.builtin(), suffix: name + "#"}, true
		}
	}
	return qualifier{}, false
}

// enclosingTypeSuffix returns the descriptor suffix of the class or interface n
// sits inside.
func (b *builder) enclosingTypeSuffix(n *sitter.Node) (string, bool) {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "class_declaration", "abstract_class_declaration", "interface_declaration":
			if name := b.fieldText(p, "name"); name != "" {
				return b.containerSuffix(p) + name + "#", true
			}
		}
	}
	return "", false
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

// declaredType recovers the syntactic type of a variable, parameter or field
// definition — enough to name the methods and fields reached through it, without
// any type checking. "" means unknown, which downstream becomes SCIP's "."
// rather than a guess.
func (b *builder) declaredType(node *sitter.Node) string {
	switch node.Type(b.lang) {
	case "required_parameter", "optional_parameter", "public_field_definition",
		"property_signature", "variable_declarator":
		if t := node.ChildByFieldName("type", b.lang); t != nil {
			return b.unwrapType(t)
		}
		if v := node.ChildByFieldName("value", b.lang); v != nil {
			return b.inferredType(v)
		}
	}
	return ""
}

// unwrapType reduces a type expression to the bare name a descriptor can use,
// possibly qualified. Composite types (union, array, function, object literal)
// have no single name and yield "".
func (b *builder) unwrapType(t *sitter.Node) string {
	for depth := 0; t != nil && depth < 16; depth++ {
		switch t.Type(b.lang) {
		case "type_annotation", "parenthesized_type":
			t = firstNamedChild(t)
		case "type_identifier", "nested_type_identifier", "predefined_type":
			return b.text(t)
		case "generic_type":
			t = t.ChildByFieldName("name", b.lang)
		default:
			return ""
		}
	}
	return ""
}

// inferredType reads a type off an initialising expression. Only the one form
// whose type is written in the source is handled — `new T()`. Anything else is
// unknown; this is a stanza, not a type checker.
func (b *builder) inferredType(expr *sitter.Node) string {
	for depth := 0; expr != nil && depth < 16; depth++ {
		switch expr.Type(b.lang) {
		case "parenthesized_expression":
			expr = firstNamedChild(expr)
		case "new_expression":
			ctor := expr.ChildByFieldName("constructor", b.lang)
			if ctor == nil {
				return ""
			}
			switch ctor.Type(b.lang) {
			case "identifier", "member_expression", "nested_identifier":
				return b.text(ctor)
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
	scope := b.enclosingScope(start, end)
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

func (b *builder) builtin() coord.Coord {
	return coord.Foreign(b.coord.Scheme, b.coord.Manager, "builtin")
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

// memberObject returns the object half of the member expression at n, unwrapping
// a call expression to the member it calls.
func (b *builder) memberObject(n *sitter.Node) *sitter.Node {
	member := n
	if member.Type(b.lang) == "call_expression" {
		member = member.ChildByFieldName("function", b.lang)
	}
	if member == nil || member.Type(b.lang) != "member_expression" {
		return nil
	}
	return member.ChildByFieldName("object", b.lang)
}

func (b *builder) namedChildOfType(n *sitter.Node, want string) *sitter.Node {
	if n == nil {
		return nil
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); c.Type(b.lang) == want {
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

// moduleExts are the extensions a TypeScript module path may carry, longest
// first so that `.d.ts` is stripped whole rather than leaving a stray `.d`.
var moduleExts = []string{".d.ts", ".tsx", ".mts", ".cts", ".ts", ".js", ".mjs", ".cjs", ".jsx"}

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
// removed and a trailing `index` segment dropped.
//
// Both normalisations exist so that the two sides of an import agree without
// either reading the disk. The extension goes because a specifier omits it —
// `./greeter` and `greeter.ts` have to render the same namespace — and the
// `.js` forms are stripped alongside the `.ts` ones because ESM-correct
// TypeScript imports the *emitted* name. `index` goes because `./lib` and
// `lib/index.ts` are the same module and only a filesystem probe could tell.
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
	p = strings.TrimSuffix(p, "/index")
	if p == "" || p == "." || p == "index" {
		return ""
	}
	return p + "/"
}

// moduleName is what a module occurrence is called in the `name` column: the
// last segment of its namespace, or — for the module at a package's root, which
// has no namespace of its own — the last segment of the package's name. A bare
// `import * as util from "node:util"` would otherwise land in the graph nameless.
func moduleName(c coord.Coord, ns string) string {
	if trimmed := strings.TrimSuffix(ns, "/"); trimmed != "" {
		return lastSegment(trimmed)
	}
	name := c.Name
	if name == "" || name == coord.Unknown {
		return ""
	}
	// `node:util` and `@scope/pkg` both name themselves by their last part.
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

// isRelative reports whether a specifier names a module by path rather than by
// package name. Node's rule exactly: `.` or `..`, alone or as a first segment.
func isRelative(spec string) bool {
	return spec == "." || spec == ".." ||
		strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../")
}

// splitPackage cuts a bare specifier into the npm package it names and the
// subpath within it. A leading `@` marks a scoped package, whose name is two
// segments rather than one.
func splitPackage(spec string) (name, sub string) {
	segments := strings.Split(spec, "/")
	if strings.HasPrefix(spec, "@") {
		if len(segments) < 2 {
			return spec, ""
		}
		return segments[0] + "/" + segments[1], strings.Join(segments[2:], "/")
	}
	return segments[0], strings.Join(segments[1:], "/")
}

// tsGlobals and tsGlobalTypes are TypeScript's ambient identifiers — the ones
// declared by the standard library rather than by any package in the tree. They
// belong to no npm package, so references to them carry a "builtin" coordinate
// and never pollute descriptor matching within the indexed package. The lists
// are the commonly used subset rather than the whole of lib.d.ts: an omission
// costs one unresolvable reference, which is what an unrecognised name gets
// anyway.
var tsGlobals = map[string]bool{
	"console": true, "process": true, "globalThis": true, "Math": true,
	"JSON": true, "Object": true, "Array": true, "String": true,
	"Number": true, "Boolean": true, "Symbol": true, "BigInt": true,
	"Promise": true, "Map": true, "Set": true, "WeakMap": true,
	"WeakSet": true, "Date": true, "RegExp": true, "Error": true,
	"require": true, "parseInt": true, "parseFloat": true, "isNaN": true,
	"isFinite": true, "setTimeout": true, "setInterval": true,
	"clearTimeout": true, "clearInterval": true, "fetch": true,
	"structuredClone": true, "encodeURIComponent": true,
	"decodeURIComponent": true,
}

var tsGlobalTypes = map[string]bool{
	"Array": true, "ReadonlyArray": true, "Promise": true, "Map": true,
	"Set": true, "WeakMap": true, "WeakSet": true, "Date": true,
	"RegExp": true, "Error": true, "Object": true, "Function": true,
	"Partial": true, "Required": true, "Readonly": true, "Record": true,
	"Pick": true, "Omit": true, "Exclude": true, "Extract": true,
	"NonNullable": true, "ReturnType": true, "Awaited": true,
	"string": true, "number": true, "boolean": true, "void": true,
	"any": true, "unknown": true, "never": true, "object": true,
	"symbol": true, "bigint": true, "undefined": true, "null": true,
}
