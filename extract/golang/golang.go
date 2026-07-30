// Package golang is the Go stanza: the tree-sitter query in query.scm plus the
// mapper here that turns its captures into core facts (SPEC.md §5).
//
// The package is named for the language, not the extension, because ".go" is a
// Go keyword — the one exception to the package-name-is-the-extension rule in
// SPEC.md §12. It imports facts and coord and deliberately not extract: it
// satisfies extract.Parser structurally, which is what keeps the registry free
// of an import cycle.
//
// The mapper's job, and its limits. It builds the descriptor *suffix* from the
// CST and the package namespace the coordinate supplies (§4.3); it assigns role
// and neutral-core symbol kind; and it resolves references whose target
// definition is in the same file. It does no type checking and looks at no other
// file. A reference it cannot pin down is still emitted, carrying the best
// descriptor syntax allows, and the link pass decides what it means (§7). Where
// a component is genuinely unknowable file-locally — the type of a receiver the
// mapper cannot infer — the descriptor writes SCIP's "." for that component, so
// it names an unresolved symbol rather than false-matching a real one.
package golang

import (
	_ "embed"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	ts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/facts"
)

// Lang is the value written to file.lang for the files this stanza handles.
const Lang = "go"

// Ext is the file extension this stanza is registered under.
const Ext = ".go"

//go:embed query.scm
var queryScheme string

// Parser is the Go stanza. Safe for concurrent use: the grammar and compiled
// query are immutable after the first Parse, and each parse checks a
// gotreesitter parser out of a pool.
type Parser struct {
	once    sync.Once
	lang    *ts.Language
	pool    *ts.ParserPool
	query   *ts.Query
	initErr error
}

// New returns the Go parser. It is cheap: the grammar is loaded and query.scm
// compiled on the first Parse, so a binary that never parses Go never
// decompresses the Go grammar.
func New() *Parser { return &Parser{} }

func (p *Parser) init() {
	p.once.Do(func() {
		p.lang = grammars.GoLanguage()
		if p.lang == nil {
			p.initErr = errors.New("golang: gotreesitter has no Go grammar")
			return
		}
		q, err := ts.NewQuery(queryScheme, p.lang)
		if err != nil {
			p.initErr = fmt.Errorf("golang: compile query.scm: %w", err)
			return
		}
		p.query = q
		p.pool = ts.NewParserPool(p.lang)
	})
}

// Parse extracts one Go file's facts. It never returns an error: a failure is
// reported in FileFacts.ParseError with File still populated, so the caller can
// tell "this file has no facts" from "this file was never seen".
func (p *Parser) Parse(path string, src []byte, c coord.Coord) facts.FileFacts {
	file := facts.File{Path: path, Lang: Lang, Coord: c}

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
		ns:         c.Namespace(path),
		out:        facts.FileFacts{File: file},
		scopeByID:  map[facts.LocalID]scopeRec{},
		imports:    map[string]importRec{},
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

type importRec struct {
	local  string
	coord  coord.Coord
	ns     string
	path   string
	blank  bool
	dotted bool
}

type builder struct {
	lang  *ts.Language
	src   []byte
	coord coord.Coord
	ns    string

	out       facts.FileFacts
	nextScope facts.LocalID
	nextOcc   facts.LocalID

	scopes    []scopeRec
	scopeByID map[facts.LocalID]scopeRec
	imports   map[string]importRec

	// claimed holds identifier ranges a definition already owns, so a
	// reference pattern matching the same identifier is dropped.
	claimed map[span]bool
	// descIndex maps a definition's full descriptor to its occurrence — the
	// same-file half of what the link pass does across files.
	descIndex  map[string]facts.LocalID
	defsByName map[string][]defRec
}

func (b *builder) build(matches []ts.QueryMatch) facts.FileFacts {
	b.collectScopes(matches)
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
func (b *builder) collectScopes(matches []ts.QueryMatch) {
	type cand struct {
		kind string
		node *ts.Node
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

// ----------------------------------------------------------------- imports ---

// collectImports records the file's imports and emits one package occurrence
// per import spec.
//
// Import *edges* are cross-file and therefore the link pass's (§4.4); what is
// extracted is the occurrence, whose descriptor names the imported package. For
// an import that stays inside this module that descriptor is byte-identical to
// the namespace the imported package's own files produce, which is what lets
// link derive `imports` by descriptor join.
func (b *builder) collectImports(matches []ts.QueryMatch) {
	type cand struct {
		spec *ts.Node
		path *ts.Node
	}
	var cands []cand
	for _, m := range matches {
		if root, name, ok := roots(m, "import"); ok && name != nil {
			cands = append(cands, cand{spec: root.Node, path: name.Node})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		return cands[i].spec.StartByte() < cands[j].spec.StartByte()
	})

	for _, cd := range cands {
		path := strings.Trim(b.text(cd.path), "\"`")
		if path == "" {
			continue
		}
		rec := importRec{path: path}
		rec.coord, rec.ns = b.coord.Import(path)

		alias := cd.spec.ChildByFieldName("name", b.lang)
		switch {
		case alias == nil:
			rec.local = lastSegment(path)
		case b.text(alias) == "_":
			rec.blank = true
			rec.local = lastSegment(path)
		case b.text(alias) == ".":
			rec.dotted = true
			rec.local = lastSegment(path)
		default:
			rec.local = b.text(alias)
		}
		// A blank or dotted import binds no name, so it must not shadow a real
		// qualifier; keep the record only when it can actually be referenced.
		if !rec.blank && !rec.dotted {
			b.imports[rec.local] = rec
		}

		b.addOccurrence(
			facts.Descriptor{Prefix: rec.coord, Suffix: rec.ns},
			facts.RoleReference, facts.KindPackage, rec.local,
			cd.spec.StartByte(), cd.spec.EndByte(),
		)
	}
}

// ------------------------------------------------------------- definitions ---

func (b *builder) collectDefinitions(matches []ts.QueryMatch) {
	type cand struct {
		kind string
		node *ts.Node
		name *ts.Node
	}
	var cands []cand
	for _, m := range matches {
		root, name, ok := roots(m, "definition.")
		if !ok {
			continue
		}
		if name == nil {
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
			typeName: b.declaredType(cd.node, cd.name),
		})
	}
}

// refineKind narrows a capture's kind where the CST carries a distinction the
// capture name does not. `@definition.type` covers every named type, but the
// link pass needs interfaces apart from the rest to derive `implements`, and
// deploy/seed/seed.sql writes `interface` for them.
func (b *builder) refineKind(kind string, node *ts.Node) string {
	if kind != facts.KindType {
		return kind
	}
	if t := node.ChildByFieldName("type", b.lang); t != nil && t.Type(b.lang) == "interface_type" {
		return facts.KindInterface
	}
	return kind
}

// definitionSuffix builds the SCIP descriptor suffix for a definition from its
// capture hierarchy (SPEC.md §5).
func (b *builder) definitionSuffix(kind string, node *ts.Node, name string) string {
	container := b.containerSuffix(node)
	switch kind {
	case facts.KindPackage:
		// A package's descriptor is its namespace; the clause's identifier names
		// it but adds nothing to the path.
		return b.ns
	case facts.KindFunction:
		return container + name + "()."
	case facts.KindMethod:
		if node.Type(b.lang) == "method_declaration" {
			// A Go method's receiver type is always package-level, so the
			// container is the package namespace regardless of nesting.
			return b.ns + notEmpty(b.receiverType(node)) + "#" + name + "()."
		}
		return container + name + "()." // interface method element
	case facts.KindType:
		return container + name + "#"
	case facts.KindParameter:
		if node.Type(b.lang) == "type_parameter_declaration" {
			return container + "[" + name + "]"
		}
		return container + "(" + name + ")"
	default: // field, variable, constant
		return container + name + "."
	}
}

// containerSuffix returns the descriptor suffix of the nearest enclosing named
// container of n — a type, function or method — or the package namespace when
// there is none. A func literal is transparent: an anonymous function has no
// descriptor of its own, so names inside it belong to the enclosing one.
func (b *builder) containerSuffix(n *ts.Node) string {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "type_spec", "type_alias":
			if name := b.fieldText(p, "name"); name != "" {
				return b.containerSuffix(p) + name + "#"
			}
		case "function_declaration":
			if name := b.fieldText(p, "name"); name != "" {
				return b.containerSuffix(p) + name + "()."
			}
		case "method_declaration":
			if name := b.fieldText(p, "name"); name != "" {
				return b.ns + notEmpty(b.receiverType(p)) + "#" + name + "()."
			}
		}
	}
	return b.ns
}

// receiverType returns the bare type name a method is declared on, unwrapping
// the pointer and any type parameters.
func (b *builder) receiverType(methodDecl *ts.Node) string {
	recv := methodDecl.ChildByFieldName("receiver", b.lang)
	if recv == nil {
		return ""
	}
	for i := 0; i < recv.NamedChildCount(); i++ {
		param := recv.NamedChild(i)
		if param.Type(b.lang) != "parameter_declaration" {
			continue
		}
		if t := param.ChildByFieldName("type", b.lang); t != nil {
			return b.unwrapType(t)
		}
	}
	return ""
}

// -------------------------------------------------------------- references ---

type refRec struct {
	role     string
	node     *ts.Node
	nameNode *ts.Node
}

// refPriority ranks the roles that can capture the same identifier. A call is
// more specific than a read of the same selector field, and a type reference is
// more specific than a read.
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

func (b *builder) collectReferences(matches []ts.QueryMatch) {
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
	// A bare identifier call is either a predeclared builtin or a function in
	// this package.
	if r.nameNode.Type(b.lang) == "identifier" {
		if goBuiltinFuncs[name] {
			return facts.Descriptor{Prefix: b.builtin(), Suffix: name + "()."}, facts.KindFunction
		}
		return facts.Descriptor{Prefix: b.coord, Suffix: b.ns + name + "()."}, facts.KindFunction
	}

	// A selector call: resolve the qualifier.
	operand := b.selectorOperand(r.node)
	if operand != nil && operand.Type(b.lang) == "identifier" {
		qualifier := b.text(operand)
		if imp, ok := b.imports[qualifier]; ok {
			return facts.Descriptor{Prefix: imp.coord, Suffix: imp.ns + name + "()."}, facts.KindFunction
		}
		if typeName, ok := b.localTypeAt(qualifier, operand.StartByte()); ok && typeName != "" {
			c, suffix := b.typeSuffix(typeName)
			return facts.Descriptor{Prefix: c, Suffix: suffix + name + "()."}, facts.KindMethod
		}
	}
	// Receiver unknowable file-locally: name it with SCIP's "." for the type so
	// it cannot false-match a real definition.
	return facts.Descriptor{Prefix: b.coord, Suffix: b.ns + coord.Unknown + "#" + name + "()."}, facts.KindMethod
}

func (b *builder) readDescriptor(r refRec, name string) (facts.Descriptor, string) {
	// The qualifier side of a selector: a package, or a value in scope.
	if r.nameNode.Type(b.lang) == "identifier" {
		if imp, ok := b.imports[name]; ok {
			return facts.Descriptor{Prefix: imp.coord, Suffix: imp.ns}, facts.KindPackage
		}
		if def, ok := b.lookup(name, r.nameNode.StartByte()); ok {
			return b.occurrence(def.occ).Descriptor, facts.KindVariable
		}
		return facts.Descriptor{Prefix: b.coord, Suffix: b.ns + name + "."}, facts.KindVariable
	}

	// The field side of a selector.
	operand := b.selectorOperand(r.node)
	if operand != nil && operand.Type(b.lang) == "identifier" {
		qualifier := b.text(operand)
		if imp, ok := b.imports[qualifier]; ok {
			return facts.Descriptor{Prefix: imp.coord, Suffix: imp.ns + name + "."}, facts.KindVariable
		}
		if typeName, ok := b.localTypeAt(qualifier, operand.StartByte()); ok && typeName != "" {
			c, suffix := b.typeSuffix(typeName)
			return facts.Descriptor{Prefix: c, Suffix: suffix + name + "."}, facts.KindField
		}
	}
	return facts.Descriptor{Prefix: b.coord, Suffix: b.ns + coord.Unknown + "#" + name + "."}, facts.KindField
}

func (b *builder) typeReferenceDescriptor(r refRec, name string) (facts.Descriptor, string) {
	// `pkg.T` parses as a qualified_type whose name half is the captured
	// type_identifier; resolve the package half through the imports.
	if parent := r.nameNode.Parent(); parent != nil && parent.Type(b.lang) == "qualified_type" {
		if pkg := parent.ChildByFieldName("package", b.lang); pkg != nil {
			if imp, ok := b.imports[b.text(pkg)]; ok {
				return facts.Descriptor{Prefix: imp.coord, Suffix: imp.ns + name + "#"}, facts.KindType
			}
		}
		return facts.Descriptor{Prefix: b.coord, Suffix: b.ns + coord.Unknown + "#"}, facts.KindType
	}
	c, suffix := b.typeSuffix(name)
	return facts.Descriptor{Prefix: c, Suffix: suffix}, facts.KindType
}

// typeSuffix names a type by the coordinate and descriptor suffix it lives at.
// The name may be qualified ("pkg.T") when it came from a declared type.
func (b *builder) typeSuffix(typeName string) (coord.Coord, string) {
	if pkg, bare, qualified := strings.Cut(typeName, "."); qualified {
		if imp, ok := b.imports[pkg]; ok {
			return imp.coord, imp.ns + bare + "#"
		}
		return b.coord, b.ns + coord.Unknown + "#"
	}
	if goPredeclaredTypes[typeName] {
		return b.builtin(), typeName + "#"
	}
	return b.coord, b.ns + typeName + "#"
}

// ------------------------------------------------------------ local lookup ---

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

// declaredType recovers the syntactic type of a variable or parameter
// definition — enough to name the methods and fields reached through it,
// without any type checking. "" means unknown, which downstream becomes SCIP's
// "." rather than a guess.
func (b *builder) declaredType(node, nameNode *ts.Node) string {
	switch node.Type(b.lang) {
	case "parameter_declaration", "field_declaration", "var_spec", "const_spec":
		if t := node.ChildByFieldName("type", b.lang); t != nil {
			return b.unwrapType(t)
		}
		if v := node.ChildByFieldName("value", b.lang); v != nil {
			return b.inferredType(v)
		}
	case "short_var_declaration":
		left, right := node.ChildByFieldName("left", b.lang), node.ChildByFieldName("right", b.lang)
		if left == nil || right == nil {
			return ""
		}
		i := namedChildIndex(left, nameNode)
		// A multi-value right-hand side (one call, several names) gives no
		// positional correspondence, so infer nothing.
		if i < 0 || left.NamedChildCount() != right.NamedChildCount() {
			return ""
		}
		return b.inferredType(right.NamedChild(i))
	}
	return ""
}

// unwrapType reduces a type expression to the bare name a descriptor can use,
// possibly package-qualified. Composite types (slice, map, chan, func) have no
// single name and yield "".
func (b *builder) unwrapType(t *ts.Node) string {
	for depth := 0; t != nil && depth < 16; depth++ {
		switch t.Type(b.lang) {
		case "type_identifier":
			return b.text(t)
		case "qualified_type":
			pkg, name := t.ChildByFieldName("package", b.lang), t.ChildByFieldName("name", b.lang)
			if pkg == nil || name == nil {
				return ""
			}
			return b.text(pkg) + "." + b.text(name)
		case "pointer_type", "parenthesized_type":
			t = firstNamedChild(t)
		case "generic_type":
			t = t.ChildByFieldName("type", b.lang)
		default:
			return ""
		}
	}
	return ""
}

// inferredType reads a type off an initialising expression. Only the forms whose
// type is written in the source are handled — a composite literal and `new(T)`.
// Anything else is unknown; this is a stanza, not a type checker.
func (b *builder) inferredType(expr *ts.Node) string {
	for depth := 0; expr != nil && depth < 16; depth++ {
		switch expr.Type(b.lang) {
		case "expression_list", "parenthesized_expression":
			expr = firstNamedChild(expr)
		case "unary_expression":
			expr = expr.ChildByFieldName("operand", b.lang)
		case "composite_literal":
			return b.unwrapType(expr.ChildByFieldName("type", b.lang))
		case "call_expression":
			fn := expr.ChildByFieldName("function", b.lang)
			args := expr.ChildByFieldName("arguments", b.lang)
			if fn == nil || args == nil || b.text(fn) != "new" {
				return ""
			}
			return b.unwrapType(firstNamedChild(args))
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

func (b *builder) text(n *ts.Node) string {
	if n == nil {
		return ""
	}
	return n.Text(b.src)
}

func (b *builder) fieldText(n *ts.Node, field string) string {
	if n == nil {
		return ""
	}
	return b.text(n.ChildByFieldName(field, b.lang))
}

func (b *builder) selectorOperand(call *ts.Node) *ts.Node {
	sel := call
	if sel.Type(b.lang) == "call_expression" {
		sel = sel.ChildByFieldName("function", b.lang)
	}
	if sel == nil || sel.Type(b.lang) != "selector_expression" {
		return nil
	}
	return sel.ChildByFieldName("operand", b.lang)
}

// roots splits a match into its structural capture (the one whose name starts
// with prefix) and its @name capture, if any.
func roots(m ts.QueryMatch, prefix string) (root, name *ts.QueryCapture, ok bool) {
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

func firstNamedChild(n *ts.Node) *ts.Node {
	if n == nil || n.NamedChildCount() == 0 {
		return nil
	}
	return n.NamedChild(0)
}

func namedChildIndex(parent, child *ts.Node) int {
	for i := 0; i < parent.NamedChildCount(); i++ {
		c := parent.NamedChild(i)
		if c.StartByte() == child.StartByte() && c.EndByte() == child.EndByte() {
			return i
		}
	}
	return -1
}

// majorOnlySegment matches a module path's major-version segment, which is not
// the imported package's name.
var majorOnlySegment = regexp.MustCompile(`^v[0-9]+$`)

// lastSegment guesses the name an unaliased import binds. It is a guess: the
// real name is in the imported package's `package` clause, which lives in
// another file and is therefore off-limits here (SPEC.md §2.5). The one case
// worth correcting is Go's major-version suffix, where the last segment is
// never the package name.
func lastSegment(importPath string) string {
	segments := strings.Split(importPath, "/")
	last := len(segments) - 1
	if last > 0 && majorOnlySegment.MatchString(segments[last]) {
		last--
	}
	return segments[last]
}

// notEmpty substitutes SCIP's unknown-component marker for a name the mapper
// could not determine.
func notEmpty(s string) string {
	if s == "" {
		return coord.Unknown
	}
	return s
}

// goPredeclaredTypes and goBuiltinFuncs are Go's predeclared identifiers. They
// belong to no module, so references to them carry a "builtin" coordinate and
// never pollute descriptor matching within the indexed module.
var goPredeclaredTypes = map[string]bool{
	"any": true, "bool": true, "byte": true, "comparable": true,
	"complex64": true, "complex128": true, "error": true,
	"float32": true, "float64": true, "int": true, "int8": true,
	"int16": true, "int32": true, "int64": true, "rune": true,
	"string": true, "uint": true, "uint8": true, "uint16": true,
	"uint32": true, "uint64": true, "uintptr": true,
}

var goBuiltinFuncs = map[string]bool{
	"append": true, "cap": true, "clear": true, "close": true,
	"complex": true, "copy": true, "delete": true, "imag": true,
	"len": true, "make": true, "max": true, "min": true, "new": true,
	"panic": true, "print": true, "println": true, "real": true,
	"recover": true,
}
