package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Paths are relative to this package's directory, which is where `go test`
// runs; repoSDLPath is the same file named the way the generator is invoked
// from the repository root, and it has to be, because it is written into the
// generated file's header.
const (
	sdlPath     = "../../schema/codiq.graphql"
	repoSDLPath = "schema/codiq.graphql"
	protoPath   = "../../schema/proto/codiq/facts/v1/facts.proto"
)

// TestTheCommittedProtoMatchesTheSDL is the drift guard, and it is the direct
// analogue of CI's "migrations match the SDL" job.
//
// schema/proto/ is generated at authoring time and committed, exactly like
// schema/migrations/, for the same reason: the build applies what is in the
// tree rather than regenerating it. That is only safe while an SDL edit that
// was not regenerated *fails* rather than being caught in review — so this
// re-runs the generator over the committed SDL and compares byte for byte.
//
// It is a test rather than a CI step because CI belongs to another wave; the
// job would be the better home for it and this is the part that can live here.
func TestTheCommittedProtoMatchesTheSDL(t *testing.T) {
	src, err := os.ReadFile(sdlPath)
	require.NoError(t, err)
	s, err := parseSDL(string(src))
	require.NoError(t, err)
	want, err := render(s, repoSDLPath)
	require.NoError(t, err)

	got, err := os.ReadFile(protoPath)
	require.NoError(t, err)

	assert.Equal(t, string(want), string(got),
		"schema/proto is stale; run `go run ./artifact/protogen && buf generate`")
}

// TestTheSDLIsReadWholeGuardsAgainstASilentlyNarrowParser.
//
// parseSDL is a focused reader rather than a GraphQL parser (see its doc), and
// the failure mode that buys is not a crash but a *quiet omission* — a field
// shape it does not recognise being skipped, and the column vanishing from the
// artifact. So the parse is checked against what the SDL actually declares:
// every @node type, and every scalar column on each one.
func TestTheSDLIsReadWholeGuardsAgainstASilentlyNarrowParser(t *testing.T) {
	src, err := os.ReadFile(sdlPath)
	require.NoError(t, err)
	s, err := parseSDL(string(src))
	require.NoError(t, err)

	// One node per top-level `type X @node(...)` declaration. Counted off the
	// document rather than listed, so a vertex table added to the SDL and not
	// read by the parser fails here instead of silently missing from the
	// artifact.
	declarations := 0
	for _, line := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(line, "type ") {
			declarations++
		}
	}
	assert.Equal(t, declarations, len(s.Nodes), "every @node type in the SDL has to be read")

	byTable := map[string]node{}
	for _, n := range s.Nodes {
		byTable[n.Table] = n
	}
	for table, want := range map[string][]string{
		"file":       {"id", "path", "lang", "pkg_scheme", "pkg_manager", "pkg_name", "pkg_version"},
		"scope":      {"id", "file_id", "kind", "range_start", "range_end", "parent_scope_id"},
		"occurrence": {"id", "file_id", "descriptor", "role", "symbol_kind", "name", "range_start", "range_end", "scope_id"},
	} {
		n, ok := byTable[table]
		require.True(t, ok, "no @node with table %q", table)
		got := make([]string, len(n.Columns))
		for i, c := range n.Columns {
			got[i] = c.Name
		}
		assert.Equal(t, want, got, "columns of %s, in declaration order", table)
	}
}

// TestGenerationFailsWhenTheSDLMovesUnderIt covers the two allow-lists that are
// policy rather than SDL (extractedEdges, vertexTypes).
//
// They exist because §4.4's split between base and derived edges is not
// expressible in the SDL, and the whole justification for keeping them is that
// they are validated rather than trusted. A rename that made them stale has to
// stop generation, not produce a .proto missing an edge kind.
func TestGenerationFailsWhenTheSDLMovesUnderIt(t *testing.T) {
	src, err := os.ReadFile(sdlPath)
	require.NoError(t, err)

	t.Run("a renamed relationship", func(t *testing.T) {
		s, err := parseSDL(strings.ReplaceAll(string(src), `type: "references_local"`, `type: "refs_local"`))
		require.NoError(t, err)
		_, err = render(s, sdlPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "references_local")
	})

	t.Run("a renamed vertex table", func(t *testing.T) {
		s, err := parseSDL(strings.ReplaceAll(string(src), `@node(label: "scope", table: "scope")`, `@node(label: "scope", table: "scopes")`))
		require.NoError(t, err)
		_, err = render(s, sdlPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "scope")
	})

	t.Run("a column type the reader does not know", func(t *testing.T) {
		_, err := parseSDL(strings.ReplaceAll(string(src), "lang: String!", "lang: Float!"))
		require.Error(t, err, "an unreadable field must stop generation, never be skipped")
	})
}
