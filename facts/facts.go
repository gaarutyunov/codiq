// Package facts is the extractor's output contract: plain structs describing
// one file's rows in the core vertex/edge tables (SPEC.md §4.4, §5).
//
// One invariant governs every type here: a FileFacts is *wholly self-contained*
// (SPEC.md §2.5). Nothing in it may depend on, or refer to, another file's
// facts. That is what makes the reduce phase's per-file delete-by-file +
// CopyFrom (§6) safe to run on one file without reading any other, and it is
// why cross-file structure is derived by the link pass (§7) instead of
// extracted: `resolves_to`, `imports`, `calls`, `implements` and `type_defines`
// never appear here.
//
// Self-containment shows up concretely in two places:
//
//   - Identity inside a FileFacts is a LocalID — an ordinal the extractor
//     assigns, meaningful only within that FileFacts. Database identity is a
//     uuid the store assigns at insert; the store maps LocalID → uuid as it
//     writes, and that mapping never leaves one file's load.
//   - A reference whose target lives in another file is still emitted, as an
//     Occurrence with RoleReference carrying the target's Descriptor
//     unresolved. It gets no Edge. The link pass matches it by descriptor
//     string (§4.3, §7).
package facts

import "github.com/gaarutyunov/codiq/coord"

// LocalID identifies a Scope or an Occurrence within a single FileFacts.
//
// Scopes and Occurrences are numbered independently, starting at 1; a Ref says
// which space an id belongs to. NoID means "none" — an unset parent scope or
// enclosing scope, which is the file's top level.
type LocalID int32

// NoID is the zero LocalID: absent, not "the first row".
const NoID LocalID = 0

// VertexKind names one of the three base vertex tables (SPEC.md §4.4).
type VertexKind string

const (
	VertexFile       VertexKind = "file"
	VertexScope      VertexKind = "scope"
	VertexOccurrence VertexKind = "occurrence"
)

// Ref is an edge endpoint: which vertex table, and which row in it.
//
// The vertex kind is carried explicitly rather than inferred from EdgeKind
// because `contains` is one graph label over two physical tables —
// contains_scope and contains_occurrence (schema/codiq.graphql). Facts model
// containment once, as EdgeContains, and the store picks the table from
// Target.Vertex. A gopgql edge table is exactly (source_id, target_id), so once
// the store has resolved both Refs to uuids there is nothing else to write.
type Ref struct {
	Vertex VertexKind
	ID     LocalID
}

// FileRef refers to the one file a FileFacts describes. It carries no id: a
// FileFacts has exactly one file, so there is nothing to disambiguate.
func FileRef() Ref { return Ref{Vertex: VertexFile} }

// ScopeRef refers to a Scope in this file.
func ScopeRef(id LocalID) Ref { return Ref{Vertex: VertexScope, ID: id} }

// OccurrenceRef refers to an Occurrence in this file.
func OccurrenceRef(id LocalID) Ref { return Ref{Vertex: VertexOccurrence, ID: id} }

// Role is what an Occurrence does with its symbol. Closed set: the `occurrence`
// table CHECKs it (schema/codiq.graphql), so a value outside these two is a
// PostgreSQL error at COPY time.
type Role string

const (
	RoleDefinition Role = "definition"
	RoleReference  Role = "reference"
)

// EdgeKind names an extracted, intra-file edge. Closed set: these are the only
// edges a file-local extractor can know (SPEC.md §4.4 "Base edge tables"). The
// five derived kinds are the link pass's to write, never an extractor's.
type EdgeKind string

const (
	// EdgeDefines is file → occurrence(definition).
	EdgeDefines EdgeKind = "defines"
	// EdgeContains is scope → scope | occurrence. One kind, two tables.
	EdgeContains EdgeKind = "contains"
	// EdgeReferencesLocal is occurrence(reference) → occurrence(definition),
	// same file, resolved during extraction because the target definition is in
	// the same CST (SPEC.md §4.3).
	EdgeReferencesLocal EdgeKind = "references_local"
)

// Neutral-core symbol kinds (SPEC.md §4.2, §4.4). Deliberately an open
// vocabulary of plain strings rather than a closed type: a new language
// normalises *to* the core and may need a kind these constants do not name, and
// the column is text. Role and EdgeKind are typed because they are closed —
// one by a CHECK constraint, the other by the set of tables that exist.
const (
	KindFunction = "function"
	KindMethod   = "method"
	KindType     = "type"
	// KindInterface is a type that declares behaviour rather than data. It is a
	// distinct kind, not a flavour of KindType, because the link pass keys
	// `implements` off it — deploy/seed/seed.sql, the hand-extracted M1 corpus
	// this contract has to reproduce, writes `interface` for exactly this.
	KindInterface = "interface"
	KindField     = "field"
	KindVariable  = "variable"
	KindParameter = "parameter"
	KindConstant  = "constant"
	KindPackage   = "package"
	KindModule    = "module"
)

// Neutral-core scope kinds (SPEC.md §4.4). Open vocabulary, same reasoning.
const (
	ScopeFile     = "file"
	ScopePackage  = "package"
	ScopeFunction = "function"
	ScopeBlock    = "block"
	ScopeType     = "type"
)

// Descriptor is a symbol's SCIP-style name (SPEC.md §4.3), kept as the two
// halves that produce it rather than as one pre-joined string.
//
// The split is the §4.3 ownership boundary made structural: Prefix is the
// package coordinate, project-wide knowledge the batch resolves from a manifest
// and hands down; Suffix is the structural descriptor path a stanza builds from
// the CST alone. A stanza receives a Coord and cannot invent one, so the
// boundary is a compile-time fact instead of a convention.
//
// Keeping the halves apart also serves the store: the `file` table wants the
// coordinate in four columns, while `occurrence.descriptor` wants the joined
// string. String is the single canonical joining — the same bytes the link pass
// compares, defined once.
type Descriptor struct {
	// Prefix is the package coordinate: `scheme manager package version`.
	Prefix coord.Coord
	// Suffix is the structural descriptor path, e.g. "pkg/Type#method().".
	// Empty when the descriptor names the package itself.
	Suffix string
}

// String renders the full descriptor — the value of occurrence.descriptor and
// the only key the link pass joins on (SPEC.md §7).
func (d Descriptor) String() string {
	if d.Suffix == "" {
		return d.Prefix.Prefix()
	}
	return d.Prefix.Prefix() + " " + d.Suffix
}

// File is the one file a FileFacts describes: a row in `file`. It carries no
// id — the store assigns the uuid — and its coordinate supplies the four
// pkg_* columns.
type File struct {
	// Path is repo-relative.
	Path string
	// Lang is the extractor language tag, e.g. "go".
	Lang string
	// Coord is the package coordinate: the descriptor prefix of every symbol
	// this file defines.
	Coord coord.Coord
}

// Scope is a row in `scope`: one lexical scope, the containment skeleton this
// file's occurrences hang off (SPEC.md §4.4).
type Scope struct {
	ID LocalID
	// Kind is a neutral-core scope kind; see the Scope* constants.
	Kind string
	// RangeStart and RangeEnd are byte offsets into the file, half-open.
	RangeStart, RangeEnd int
	// Parent is the enclosing scope, or NoID for the file's root scope.
	// Denormalised alongside the EdgeContains edge, matching the schema.
	Parent LocalID
}

// Occurrence is a row in `occurrence`: one place in this file where a symbol is
// defined or used (SPEC.md §4.4). Definitions and references share this shape
// and are told apart by Role, which is what lets the link pass be one self-join
// on Descriptor.
type Occurrence struct {
	ID LocalID
	// Descriptor names the symbol. On a RoleReference whose target is in
	// another file this is the *target's* descriptor, unresolved.
	Descriptor Descriptor
	Role       Role
	// SymbolKind is a neutral-core kind; see the Kind* constants.
	SymbolKind string
	// Name is the identifier as it appears in the source.
	Name string
	// RangeStart and RangeEnd are byte offsets of the identifier itself,
	// half-open — the SCIP convention, and the span an agent jumps to.
	RangeStart, RangeEnd int
	// Scope is the innermost enclosing scope, or NoID at the file's top level.
	// Denormalised alongside the EdgeContains edge, matching the schema.
	Scope LocalID
}

// Edge is a row in one of the extracted intra-file edge tables. Both endpoints
// are in this file, always: an edge that leaves the file is the link pass's.
type Edge struct {
	Kind   EdgeKind
	Source Ref
	Target Ref
}

// FileFacts is everything extraction knows about one file — the unit the map
// phase produces, the reduce phase loads, and M5 serialises to a protobuf
// artifact.
type FileFacts struct {
	File        File
	Scopes      []Scope
	Occurrences []Occurrence
	Edges       []Edge
	// ParseError is non-empty when the file could not be parsed. The Parser
	// contract returns no error (SPEC.md §14 M2), so the failure travels with
	// the facts: File is still populated, the row slices are empty, and the
	// caller decides whether to load nothing or flag the file poison (§5,
	// §14 M4). Kept a string, not an error, because FileFacts becomes a
	// protobuf message at M5.
	ParseError string
}
