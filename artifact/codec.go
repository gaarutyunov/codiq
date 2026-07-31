// facts.FileFacts ↔ protobuf (SPEC.md §5, §14 M5).
//
// The two shapes describe the same thing and differ only where Go and protobuf
// differ, so this file is mechanical on purpose: every field is named on both
// sides, nothing is inferred, and a field added to either side without the
// other fails to compile. The one place judgement was needed is stated at each
// site rather than here.
//
// Round trip: decode(encode(ff)) == ff for every FileFacts an extractor
// produces, with one documented exception — coord.Coord.Root, which the
// generated schema deliberately does not carry (schema/proto's Coord comment).
// Root is extraction-time state: Coord.Namespace resolves file paths against it
// while a stanza is building descriptor suffixes, and by the time an artifact
// exists there are no more suffixes to build. Nothing downstream of extraction
// reads it — store.ReplaceFile takes the four descriptor components, and
// Descriptor.String renders exactly those four (coord.Coord.Prefix).
package artifact

import (
	"fmt"

	factsv1 "github.com/gaarutyunov/codiq/artifact/proto/codiq/facts/v1"
	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/facts"
)

// vertexKinds and edgeKinds map the generated enums to the facts vocabulary.
//
// Tables rather than a switch because the mapping has to be total in both
// directions and a table makes the inverse free — and because the enum is
// generated from the SDL, so what these have to be checked against is the
// generated file, which is easier to do against a literal than against two
// switch statements that have drifted apart.
var (
	vertexKinds = map[facts.VertexKind]factsv1.VertexKind{
		facts.VertexFile:       factsv1.VertexKind_VERTEX_KIND_FILE,
		facts.VertexScope:      factsv1.VertexKind_VERTEX_KIND_SCOPE,
		facts.VertexOccurrence: factsv1.VertexKind_VERTEX_KIND_OCCURRENCE,
	}
	edgeKinds = map[facts.EdgeKind]factsv1.EdgeKind{
		facts.EdgeDefines:         factsv1.EdgeKind_EDGE_KIND_DEFINES,
		facts.EdgeContains:        factsv1.EdgeKind_EDGE_KIND_CONTAINS,
		facts.EdgeReferencesLocal: factsv1.EdgeKind_EDGE_KIND_REFERENCES_LOCAL,
	}
)

// encode builds the protobuf message for one file's facts.
//
// Ranges are narrowed to int32 here rather than at the store, and checked
// rather than cast: the schema's offset columns are int32 (store.span makes the
// same conversion for the same reason), and an artifact that silently wrapped
// an offset would produce a plausible-looking row two stages later, with
// nothing left to trace it to.
func encode(ff facts.FileFacts) (*factsv1.FileFacts, error) {
	msg := &factsv1.FileFacts{
		File: &factsv1.File{
			Path:  ff.File.Path,
			Lang:  ff.File.Lang,
			Coord: encodeCoord(ff.File.Coord),
		},
		Scopes:      make([]*factsv1.Scope, 0, len(ff.Scopes)),
		Occurrences: make([]*factsv1.Occurrence, 0, len(ff.Occurrences)),
		Edges:       make([]*factsv1.Edge, 0, len(ff.Edges)),
		ParseError:  ff.ParseError,
	}

	for _, s := range ff.Scopes {
		start, end, err := span(s.RangeStart, s.RangeEnd)
		if err != nil {
			return nil, fmt.Errorf("scope %d: %w", s.ID, err)
		}
		msg.Scopes = append(msg.Scopes, &factsv1.Scope{
			LocalId:       int32(s.ID),
			Kind:          s.Kind,
			RangeStart:    start,
			RangeEnd:      end,
			ParentScopeId: int32(s.Parent),
		})
	}

	for _, o := range ff.Occurrences {
		start, end, err := span(o.RangeStart, o.RangeEnd)
		if err != nil {
			return nil, fmt.Errorf("occurrence %d (%s): %w", o.ID, o.Name, err)
		}
		msg.Occurrences = append(msg.Occurrences, &factsv1.Occurrence{
			LocalId: int32(o.ID),
			Descriptor_: &factsv1.Descriptor{
				Prefix: encodeCoord(o.Descriptor.Prefix),
				Suffix: o.Descriptor.Suffix,
			},
			Role:       string(o.Role),
			SymbolKind: o.SymbolKind,
			Name:       o.Name,
			RangeStart: start,
			RangeEnd:   end,
			ScopeId:    int32(o.Scope),
		})
	}

	for i, e := range ff.Edges {
		kind, ok := edgeKinds[e.Kind]
		if !ok {
			// Only reachable from a stanza emitting a derived edge kind, which
			// §4.4 reserves for the link pass. Refused here rather than at the
			// store, because an artifact that carried one would put a cross-file
			// claim into a file-local unit.
			return nil, fmt.Errorf("edge %d: %q is not an extracted edge kind", i, e.Kind)
		}
		source, err := encodeRef(e.Source)
		if err != nil {
			return nil, fmt.Errorf("edge %d (%s) source: %w", i, e.Kind, err)
		}
		target, err := encodeRef(e.Target)
		if err != nil {
			return nil, fmt.Errorf("edge %d (%s) target: %w", i, e.Kind, err)
		}
		msg.Edges = append(msg.Edges, &factsv1.Edge{Kind: kind, Source: source, Target: target})
	}

	return msg, nil
}

// decode rebuilds the facts from the message.
//
// It is total: every value the encoder can produce decodes, and anything else
// is an error naming the offending row. That matters more here than on the
// encoding side, because what decode is handed is a file somebody else's
// process wrote — possibly a build ago, across a resume (SPEC.md §6).
func decode(msg *factsv1.FileFacts) (facts.FileFacts, error) {
	if msg.GetFile() == nil {
		return facts.FileFacts{}, fmt.Errorf("artifact has no file")
	}
	ff := facts.FileFacts{
		File: facts.File{
			Path:  msg.GetFile().GetPath(),
			Lang:  msg.GetFile().GetLang(),
			Coord: decodeCoord(msg.GetFile().GetCoord()),
		},
		ParseError: msg.GetParseError(),
	}

	if n := len(msg.GetScopes()); n > 0 {
		ff.Scopes = make([]facts.Scope, 0, n)
	}
	for _, s := range msg.GetScopes() {
		ff.Scopes = append(ff.Scopes, facts.Scope{
			ID:         facts.LocalID(s.GetLocalId()),
			Kind:       s.GetKind(),
			RangeStart: int(s.GetRangeStart()),
			RangeEnd:   int(s.GetRangeEnd()),
			Parent:     facts.LocalID(s.GetParentScopeId()),
		})
	}

	if n := len(msg.GetOccurrences()); n > 0 {
		ff.Occurrences = make([]facts.Occurrence, 0, n)
	}
	for _, o := range msg.GetOccurrences() {
		ff.Occurrences = append(ff.Occurrences, facts.Occurrence{
			ID: facts.LocalID(o.GetLocalId()),
			Descriptor: facts.Descriptor{
				Prefix: decodeCoord(o.GetDescriptor_().GetPrefix()),
				Suffix: o.GetDescriptor_().GetSuffix(),
			},
			Role:       facts.Role(o.GetRole()),
			SymbolKind: o.GetSymbolKind(),
			Name:       o.GetName(),
			RangeStart: int(o.GetRangeStart()),
			RangeEnd:   int(o.GetRangeEnd()),
			Scope:      facts.LocalID(o.GetScopeId()),
		})
	}

	if n := len(msg.GetEdges()); n > 0 {
		ff.Edges = make([]facts.Edge, 0, n)
	}
	for i, e := range msg.GetEdges() {
		kind, ok := factsEdgeKind(e.GetKind())
		if !ok {
			return facts.FileFacts{}, fmt.Errorf("edge %d: unknown edge kind %s", i, e.GetKind())
		}
		source, err := decodeRef(e.GetSource())
		if err != nil {
			return facts.FileFacts{}, fmt.Errorf("edge %d (%s) source: %w", i, kind, err)
		}
		target, err := decodeRef(e.GetTarget())
		if err != nil {
			return facts.FileFacts{}, fmt.Errorf("edge %d (%s) target: %w", i, kind, err)
		}
		ff.Edges = append(ff.Edges, facts.Edge{Kind: kind, Source: source, Target: target})
	}

	return ff, nil
}

func encodeCoord(c coord.Coord) *factsv1.Coord {
	return &factsv1.Coord{
		Scheme:  c.Scheme,
		Manager: c.Manager,
		Name:    c.Name,
		Version: c.Version,
	}
}

func decodeCoord(c *factsv1.Coord) coord.Coord {
	return coord.Coord{
		Scheme:  c.GetScheme(),
		Manager: c.GetManager(),
		Name:    c.GetName(),
		Version: c.GetVersion(),
	}
}

func encodeRef(r facts.Ref) (*factsv1.Ref, error) {
	vertex, ok := vertexKinds[r.Vertex]
	if !ok {
		return nil, fmt.Errorf("unknown vertex kind %q", r.Vertex)
	}
	return &factsv1.Ref{Vertex: vertex, LocalId: int32(r.ID)}, nil
}

func decodeRef(r *factsv1.Ref) (facts.Ref, error) {
	for kind, pb := range vertexKinds {
		if pb == r.GetVertex() {
			return facts.Ref{Vertex: kind, ID: facts.LocalID(r.GetLocalId())}, nil
		}
	}
	return facts.Ref{}, fmt.Errorf("unknown vertex kind %s", r.GetVertex())
}

func factsEdgeKind(pb factsv1.EdgeKind) (facts.EdgeKind, bool) {
	for kind, want := range edgeKinds {
		if want == pb {
			return kind, true
		}
	}
	return "", false
}

// span narrows a byte offset pair to the schema's int32 columns, checked.
//
// The same three rules store.span applies, applied a stage earlier so a bad
// offset is caught by the process that produced it rather than by the batch
// that loads it. Half-open, so start == end is a legal empty range.
func span(start, end int) (int32, int32, error) {
	const maxInt32 = 1<<31 - 1
	switch {
	case start < 0 || end < 0:
		return 0, 0, fmt.Errorf("negative range [%d,%d)", start, end)
	case start > maxInt32 || end > maxInt32:
		return 0, 0, fmt.Errorf("range [%d,%d) exceeds the int32 offset columns", start, end)
	case end < start:
		return 0, 0, fmt.Errorf("inverted range [%d,%d)", start, end)
	}
	return int32(start), int32(end), nil
}
