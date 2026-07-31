// Command protogen emits the fact artifact's protobuf schema from the SDL
// (SPEC.md §4.1, Decision 3).
//
// §4.1 makes schema/codiq.graphql the single source of truth: it already
// generates the goose migrations, the sqlc catalog and the property-graph view,
// and Decision 3 adds the protobuf fact types to that list — "SDL generates
// .proto, buf generates Go". So schema/proto/ is written by this program and
// never by hand, for the same reason schema/migrations/ is: a .proto edited
// beside the SDL is a second source of truth, and the first thing it does is
// disagree.
//
// Run it from the repository root:
//
//	go run ./artifact/protogen
//	buf generate
//
// # What the SDL does and does not decide
//
// Everything about the *rows* comes from the SDL and nothing here restates it:
// which vertex types exist, which columns each has, their names, their types
// and their order are all read out of schema/codiq.graphql, and the field
// numbers are the SDL's own declaration order.
//
// Three things the SDL cannot decide, because they belong to the extractor
// contract (§5) rather than to the schema, are policy in this file and are
// stated as constants below so that a change to either side is visible:
//
//   - Identity. A row's `id: ID!` is a database uuid the store assigns at
//     insert (§4.4); inside an artifact identity is a facts.LocalID, an ordinal
//     meaningful only within one file. So the surrogate key becomes
//     `int32 local_id`, and so does every intra-file reference to one
//     (`scope_id`, `parent_scope_id`).
//   - Ownership. Every base row carries `file_id` (§2.5). An artifact *is* one
//     file's facts, so the column has nothing to say and is dropped.
//   - Which edges are extracted. §4.4 splits the edge tables into base
//     (extracted, intra-file) and derived (written by the link pass, §7), and
//     nothing in the SDL distinguishes them — both are plain @relationship
//     fields. extractedEdges below is that split, and it is checked against the
//     SDL on every run, so renaming a relationship breaks generation loudly
//     instead of quietly emitting an artifact the reduce cannot load.
//
// The one column that is not a scalar is `occurrence.descriptor`. In the
// database it is the joined string the link pass matches on; in an artifact it
// is still the two halves §4.3 keeps apart — a package coordinate the batch
// resolved and a structural suffix the stanza built — because an artifact is
// written before anything joins them. It is therefore emitted as a Descriptor
// message over the Coord the `file` row's four pkg_* columns already describe.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The generated schema's coordinates. protoPackage is also the directory the
// file is written under, which is what buf's `paths=source_relative` and the
// go_package option below agree on.
const (
	protoPackage = "codiq.facts.v1"
	goPackage    = "github.com/gaarutyunov/codiq/artifact/proto/codiq/facts/v1;factsv1"
)

// extractedEdges are the relationship types a file-local extractor may emit —
// §4.4's "base edge tables", and exactly the set facts.EdgeKind names.
//
// The order is the order the enum is emitted in, so it is fixed rather than
// alphabetical: an enum value's number is part of the wire format, and §4.6's
// encoding versioning is about not having to re-encode when the schema moves.
//
// Every name here must exist in the SDL; generation fails if one does not.
var extractedEdges = []string{"defines", "contains", "references_local"}

// vertexTypes are the SDL @node types an edge endpoint may name, in the order
// facts.VertexKind names them. Also validated against the SDL.
var vertexTypes = []string{"file", "scope", "occurrence"}

// fileOwnerColumn is the column every non-file base row carries to say which
// file owns it (§2.5). An artifact describes one file, so it is dropped.
const fileOwnerColumn = "file_id"

// ownerTable is the vertex table that owns every other base row (§2.5). Its own
// surrogate key is dropped for the same reason fileOwnerColumn is: an artifact
// describes exactly one file, so there is no second file to tell it apart from
// and nothing an id could name. facts.FileRef carries no id for this reason.
const ownerTable = "file"

// coordPrefix marks the `file` columns that together are the descriptor prefix
// (§4.3). They are emitted as one Coord message rather than four strings,
// because Coord is what the extractor is handed and what coord.Coord.Prefix
// renders.
const coordPrefix = "pkg_"

// descriptorColumn is the column whose value is a SCIP descriptor (§4.3), and
// so is emitted as the two-part Descriptor message rather than as a string.
const descriptorColumn = "descriptor"

func main() {
	sdlPath := flag.String("sdl", "schema/codiq.graphql", "path to the SDL")
	out := flag.String("out", "schema/proto/codiq/facts/v1/facts.proto", "path to write")
	flag.Parse()

	if err := run(*sdlPath, *out); err != nil {
		fmt.Fprintf(os.Stderr, "protogen: %v\n", err)
		os.Exit(1)
	}
}

func run(sdlPath, out string) error {
	src, err := os.ReadFile(sdlPath)
	if err != nil {
		return err
	}
	schema, err := parseSDL(string(src))
	if err != nil {
		return fmt.Errorf("%s: %w", sdlPath, err)
	}
	proto, err := render(schema, filepath.ToSlash(sdlPath))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	return os.WriteFile(out, proto, 0o644)
}

// --- the SDL, as much of it as a fact artifact needs -------------------------

// node is one @node type: a vertex table and the columns it declares.
type node struct {
	// Name is the GraphQL type name, e.g. "Occurrence".
	Name string
	// Table is the physical table from @node(table:), e.g. "occurrence". It is
	// what @relationship endpoints and facts.VertexKind are named by.
	Table string
	// Columns are the scalar fields in declaration order, relationships
	// excluded.
	Columns []column
}

// column is one scalar field of a @node type.
type column struct {
	// Name is the column name — @column(name:) when present, else the field
	// name in snake_case, which is gopgql's own default.
	Name string
	// Type is the GraphQL type with its nullability marker stripped: "String",
	// "Int" or "ID".
	Type string
	// Surrogate is true for the type's own `id: ID!`, the key the store
	// assigns.
	Surrogate bool
}

// schema is the whole SDL, reduced to what a fact artifact is shaped by.
type schema struct {
	Nodes []node
	// Relationships is every distinct @relationship(type:) in the document.
	// Used only to check extractedEdges against reality.
	Relationships map[string]bool
}

// parseSDL reads the subset of GraphQL SDL schema/codiq.graphql is written in:
// @node type declarations, their fields, and the directives that rename or
// relate them.
//
// It is deliberately narrow and deliberately strict. Narrow because the
// alternative is a GraphQL parser dependency in a module whose only other
// generator — sqlc — is pinned by a documented install command precisely so its
// build tree stays out of the module graph (store/sqlc/sqlc.yaml). Strict
// because a lenient reader of a source of truth is worse than no reader: every
// construct it does not understand is an error naming the line, so an SDL that
// grows past this parser stops the build instead of silently emitting a .proto
// with a column missing.
func parseSDL(src string) (schema, error) {
	s := schema{Relationships: map[string]bool{}}
	lines := strings.Split(src, "\n")

	var cur *node
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(stripComment(lines[i]))
		if line == "" {
			continue
		}

		if cur == nil {
			if !strings.HasPrefix(line, "type ") {
				continue
			}
			// A type declaration may wrap over several lines; gather it up to
			// the opening brace so the directives are all in one string.
			decl := line
			for !strings.Contains(decl, "{") && i+1 < len(lines) {
				i++
				decl += " " + strings.TrimSpace(stripComment(lines[i]))
			}
			name := strings.Fields(strings.TrimPrefix(decl, "type "))[0]
			table, ok := directiveArg(decl, "node", "table")
			if !ok {
				// Not a vertex table: the SDL's non-@node types (there are none
				// today) are not part of the artifact.
				continue
			}
			s.Nodes = append(s.Nodes, node{Name: name, Table: table})
			cur = &s.Nodes[len(s.Nodes)-1]
			continue
		}

		if line == "}" {
			cur = nil
			continue
		}

		// A field is `name: Type` possibly followed by directives, which may
		// continue on the following lines. Gather until the next field or the
		// closing brace.
		field := line
		for i+1 < len(lines) {
			next := strings.TrimSpace(stripComment(lines[i+1]))
			if next == "" {
				i++
				continue
			}
			if next == "}" || !strings.HasPrefix(next, "@") {
				break
			}
			i++
			field += " " + next
		}

		name, gqlType, ok := splitField(field)
		if !ok {
			return schema{}, fmt.Errorf("type %s: cannot read field %q", cur.Name, field)
		}
		if rel, ok := directiveArg(field, "relationship", "type"); ok {
			s.Relationships[rel] = true
			continue
		}
		bare := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(gqlType, "["), "]!"), "!")
		switch bare {
		case "String", "Int", "ID":
		default:
			return schema{}, fmt.Errorf("type %s: field %s has unsupported type %q", cur.Name, name, gqlType)
		}
		col := column{Name: snake(name), Type: bare, Surrogate: name == "id"}
		if renamed, ok := directiveArg(field, "column", "name"); ok {
			col.Name = renamed
		}
		cur.Columns = append(cur.Columns, col)
	}
	if cur != nil {
		return schema{}, fmt.Errorf("type %s: unterminated declaration", cur.Name)
	}
	if len(s.Nodes) == 0 {
		return schema{}, fmt.Errorf("no @node types found")
	}
	return s, nil
}

// stripComment removes a `#` comment. The SDL contains no `#` inside a string
// literal — every directive argument is an identifier or a table name — so a
// plain cut is exact here and an error if that ever stops being true would show
// up as a parse failure rather than as a silently mangled field.
func stripComment(line string) string {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		return line[:i]
	}
	return line
}

// splitField reads `name: Type` off the head of a field declaration.
func splitField(field string) (name, gqlType string, ok bool) {
	colon := strings.IndexByte(field, ':')
	if colon < 0 {
		return "", "", false
	}
	name = strings.TrimSpace(field[:colon])
	rest := strings.TrimSpace(field[colon+1:])
	if at := strings.IndexByte(rest, '@'); at >= 0 {
		rest = strings.TrimSpace(rest[:at])
	}
	if name == "" || rest == "" || strings.ContainsAny(name, " \t") {
		return "", "", false
	}
	return name, rest, true
}

// directiveArg reads a string argument off a directive: given `@node(label:
// "file", table: "file")`, ("node", "table") yields "file".
func directiveArg(s, directive, arg string) (string, bool) {
	at := strings.Index(s, "@"+directive+"(")
	if at < 0 {
		return "", false
	}
	open := at + len("@"+directive)
	depth, end := 0, -1
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return "", false
	}
	body := s[open+1 : end]
	key := arg + ":"
	k := strings.Index(body, key)
	if k < 0 {
		return "", false
	}
	rest := body[k+len(key):]
	q := strings.IndexByte(rest, '"')
	if q < 0 {
		return "", false
	}
	rest = rest[q+1:]
	q = strings.IndexByte(rest, '"')
	if q < 0 {
		return "", false
	}
	return rest[:q], true
}

// snake renders a camelCase SDL field name as the column name gopgql would
// derive from it, for the fields that carry no explicit @column.
func snake(name string) string {
	var b strings.Builder
	for i, r := range name {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// --- rendering ---------------------------------------------------------------

func render(s schema, sdlPath string) ([]byte, error) {
	byTable := map[string]node{}
	for _, n := range s.Nodes {
		byTable[n.Table] = n
	}
	for _, want := range vertexTypes {
		if _, ok := byTable[want]; !ok {
			return nil, fmt.Errorf("the SDL declares no @node with table %q; "+
				"vertexTypes in artifact/protogen is out of date", want)
		}
	}
	for _, want := range extractedEdges {
		if !s.Relationships[want] {
			return nil, fmt.Errorf("the SDL declares no @relationship of type %q; "+
				"extractedEdges in artifact/protogen is out of date", want)
		}
	}

	var b bytes.Buffer
	p := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	p("// Code generated by artifact/protogen from %s. DO NOT EDIT.", sdlPath)
	p("//")
	p("// One file's extracted facts (SPEC.md §5): the map phase's output, written to")
	p("// the shared volume (§10) and read back by the reduce (§6). The message shapes")
	p("// are the SDL's vertex tables; the identity model is the extractor's, so a row")
	p("// is named by a file-local ordinal rather than by the uuid the store assigns.")
	p("")
	p("syntax = \"proto3\";")
	p("")
	p("package %s;", protoPackage)
	p("")
	p("option go_package = \"%s\";", goPackage)
	p("")

	// The extractor-contract preamble: the types §5 needs that the SDL, which
	// describes rows, has no way to state.
	p("// Coord is a package coordinate: the descriptor prefix (SPEC.md §4.3) the")
	p("// batch resolves from a manifest and hands to every stanza. Its four")
	p("// components are the `file` row's %s* columns.", coordPrefix)
	p("//")
	p("// coord.Coord's Root is deliberately absent. Root is extraction-time")
	p("// resolution state — it is what Coord.Namespace resolves a file path")
	p("// against — and by the time an artifact exists every namespace has already")
	p("// been resolved into a descriptor suffix. The descriptor prefix is exactly")
	p("// the four components below.")
	p("message Coord {")
	p("  string scheme = 1;")
	p("  string manager = 2;")
	p("  string name = 3;")
	p("  string version = 4;")
	p("}")
	p("")
	p("// Descriptor is a symbol's SCIP-style name (SPEC.md §4.3) as an artifact")
	p("// carries it: the two halves that produce it, not the joined string the")
	p("// `occurrence.%s` column holds. The split is §4.3's ownership", descriptorColumn)
	p("// boundary — prefix is the batch's, suffix is the stanza's — and an artifact")
	p("// is written before anything joins them.")
	p("message Descriptor {")
	p("  Coord prefix = 1;")
	p("  string suffix = 2;")
	p("}")
	p("")

	p("// VertexKind names one of the SDL's vertex tables: which table an edge")
	p("// endpoint is a row in.")
	p("enum VertexKind {")
	p("  VERTEX_KIND_UNSPECIFIED = 0;")
	for i, t := range vertexTypes {
		p("  VERTEX_KIND_%s = %d;", strings.ToUpper(t), i+1)
	}
	p("}")
	p("")

	p("// EdgeKind names an extracted, intra-file edge — SPEC.md §4.4's base edge")
	p("// tables. The derived cross-file kinds are the link pass's (§7) and never")
	p("// appear in an artifact.")
	p("enum EdgeKind {")
	p("  EDGE_KIND_UNSPECIFIED = 0;")
	for i, e := range extractedEdges {
		p("  EDGE_KIND_%s = %d;", strings.ToUpper(e), i+1)
	}
	p("}")
	p("")

	p("// Ref is an edge endpoint: which vertex table, and which row in it.")
	p("// `contains` is one label over two physical tables, so the endpoint's table")
	p("// is carried rather than inferred from the kind.")
	p("message Ref {")
	p("  VertexKind vertex = 1;")
	p("  int32 local_id = 2;")
	p("}")
	p("")
	p("// Edge is a row in one of the extracted edge tables. Both endpoints are in")
	p("// this file, always: an edge that leaves the file is the link pass's.")
	p("message Edge {")
	p("  EdgeKind kind = 1;")
	p("  Ref source = 2;")
	p("  Ref target = 3;")
	p("}")
	p("")

	// The vertex messages, straight off the SDL.
	for _, table := range vertexTypes {
		n := byTable[table]
		p("// %s is a row in `%s`, as the extractor produces it.", n.Name, n.Table)
		p("message %s {", n.Name)
		field := 0
		emitted := map[string]bool{}
		for _, c := range n.Columns {
			switch {
			case c.Name == fileOwnerColumn:
				// An artifact is one file's facts; the owner column has nothing
				// to distinguish.
				continue
			case c.Surrogate && n.Table == ownerTable:
				// The artifact *is* this row; see ownerTable.
				continue
			case c.Surrogate:
				field++
				p("  // Identity inside this artifact: a facts.LocalID, not the uuid")
				p("  // the store assigns at insert. Numbered from 1; 0 means none.")
				p("  int32 local_id = %d;", field)
			case strings.HasPrefix(c.Name, coordPrefix):
				if emitted["coord"] {
					continue
				}
				emitted["coord"] = true
				field++
				p("  // The %s* columns, as the coordinate they are.", coordPrefix)
				p("  Coord coord = %d;", field)
			case c.Name == descriptorColumn:
				field++
				p("  Descriptor %s = %d;", c.Name, field)
			case c.Type == "ID":
				field++
				p("  // An intra-file reference to another row's local_id; 0 means none.")
				p("  int32 %s = %d;", c.Name, field)
			case c.Type == "Int":
				field++
				p("  int32 %s = %d;", c.Name, field)
			default:
				field++
				p("  string %s = %d;", c.Name, field)
			}
		}
		p("}")
		p("")
	}

	p("// FileFacts is everything extraction knows about one file: the unit the map")
	p("// phase writes to the shared volume and the reduce phase loads (SPEC.md §5,")
	p("// §6). It is wholly self-contained — nothing in it refers to another file.")
	p("message FileFacts {")
	p("  File file = 1;")
	p("  repeated Scope scopes = 2;")
	p("  repeated Occurrence occurrences = 3;")
	p("  repeated Edge edges = 4;")
	p("")
	p("  // Non-empty when the file could not be parsed (SPEC.md §5's poison file).")
	p("  // The Parser contract returns no error, so the failure travels with the")
	p("  // facts: the row lists are empty and the caller decides what to do.")
	p("  string parse_error = 5;")
	p("}")

	return b.Bytes(), nil
}
