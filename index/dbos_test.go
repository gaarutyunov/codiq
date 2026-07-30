package index

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/facts"
	"github.com/gaarutyunov/codiq/store"
)

// The tests in this file are about the one thing M4 added that has no
// counterpart on the M2 path: values crossing a checkpoint. Everything the map
// phase produces is written to the DBOS database and read back by a different
// process than the one that wrote it, so a type that does not survive
// encoding/json is a bug that only a resumed run would ever show — and a
// resumed run is precisely the case there is no second chance to get right.
//
// The workflow structure itself is not tested here. It needs a real DBOS
// context, which needs Postgres; the durability claims belong to the integration
// suite, which drives the real binary against real containers (SPEC.md §13).

// checkpoint round-trips a value the way DBOS's default serializer does.
//
// Faithfully, and the details matter. DBOS encodes through a Serializer[any] —
// so the value is marshalled as an interface, which is what a step's or a
// workflow's output always is — base64s the JSON, and decodes with a
// Serializer[R] typed to the destination (dbos/serialization.go, v0.20.0).
// Marshalling the concrete type directly would be a weaker test than the thing
// the runtime actually does.
func checkpoint[T any](t *testing.T, in T) T {
	t.Helper()
	encoded, err := json.Marshal(any(in))
	require.NoError(t, err, "encode")
	wire := base64.StdEncoding.EncodeToString(encoded)

	raw, err := base64.StdEncoding.DecodeString(wire)
	require.NoError(t, err, "decode base64")
	var out T
	require.NoError(t, json.Unmarshal(raw, &out), "decode")
	return out
}

// TestFileFactsSurvivesACheckpoint is M4's load-bearing serialization claim.
//
// At M3 the per-file step returned an error and nothing else, so facts never
// crossed a checkpoint. At M4 they are the map phase's whole output: the reduce
// loads what the extract tasks recorded, and if any of it decodes differently
// than it was encoded then a resumed run writes a different graph than an
// uninterrupted one — silently, since every field below is a plain value that
// would come back as its zero rather than as an error.
//
// The facts are the real extractor's over real Go source, not a literal, so the
// claim covers whatever shapes the mapper actually emits — nested Descriptors
// with their coord.Coord prefix, both roles, all three edge kinds, LocalIDs
// including NoID.
func TestFileFactsSurvivesACheckpoint(t *testing.T) {
	root := tree(t, repo)
	c, err := coord.Resolve(root)
	require.NoError(t, err)

	l := defaultLoader()
	for _, rel := range []string{"main.go", "greeter.go", "internal/store/store.go"} {
		t.Run(rel, func(t *testing.T) {
			ff, err := l.extract(root, filepath.Join(root, filepath.FromSlash(rel)), c)
			require.NoError(t, err)
			require.NotEmpty(t, ff.Occurrences, "the fixture has to exercise the shapes")
			require.NotEmpty(t, ff.Edges)

			assert.Equal(t, ff, checkpoint(t, ff),
				"facts.FileFacts must decode exactly as it was encoded")
		})
	}
}

// TestFileFactsCheckpointsEveryFieldItHas guards the claim above against the
// fixture rather than against the type: a field the Go mapper never populates
// would round-trip vacuously.
//
// So this one hand-builds a FileFacts with every field set to something that is
// not its zero value — including the ones the round trip could plausibly lose:
// a Descriptor, whose Prefix is a struct rather than a string; a Role and an
// EdgeKind, which are defined types over string; LocalIDs, which are int32; and
// ParseError, which is how a poison file reports itself.
func TestFileFactsCheckpointsEveryFieldItHas(t *testing.T) {
	c := coord.Coord{
		Scheme:  "scip-go",
		Manager: "gomod",
		Name:    "github.com/foo/bar",
		Version: "v1.2.3",
		Root:    "/repo",
	}
	ff := facts.FileFacts{
		File:   facts.File{Path: "pkg/a.go", Lang: "go", Coord: c},
		Scopes: []facts.Scope{{ID: 1, Kind: facts.ScopeFile, RangeStart: 0, RangeEnd: 42, Parent: facts.NoID}},
		Occurrences: []facts.Occurrence{{
			ID:         1,
			Descriptor: facts.Descriptor{Prefix: c, Suffix: "pkg/Type#method()."},
			Role:       facts.RoleDefinition,
			SymbolKind: facts.KindMethod,
			Name:       "method",
			RangeStart: 7,
			RangeEnd:   13,
			Scope:      1,
		}},
		Edges: []facts.Edge{
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(1)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(1), Target: facts.OccurrenceRef(1)},
			{Kind: facts.EdgeReferencesLocal, Source: facts.OccurrenceRef(1), Target: facts.OccurrenceRef(1)},
		},
		ParseError: "parse error at 1:1",
	}

	got := checkpoint(t, ff)
	assert.Equal(t, ff, got)
	// Stated separately because it is the one value the reduce branches on, and
	// an empty-string ParseError would silently turn a poison file into a good
	// one that deletes its own graph (mapFiles).
	assert.Equal(t, ff.ParseError, got.ParseError)
	assert.Equal(t, ff.Occurrences[0].Descriptor.String(), got.Occurrences[0].Descriptor.String(),
		"the descriptor is the link pass's only join key")
}

// TestSkipSurvivesACheckpoint pins what a Skip is allowed to lose.
//
// Skip.Err is an `error`, which encoding/json marshals to `{}` and decodes to
// nil, so the message needs custom marshalling to survive at all. The message is
// not the whole requirement though: Skip.Err is documented as answering
// errors.Is(store.ErrParseFailed), and M4 introduced a second kind of skip — a
// map task that failed — which must answer that question with *no*. A single
// message string cannot carry the difference, so the wire form records it.
func TestSkipSurvivesACheckpoint(t *testing.T) {
	t.Run("a parse failure keeps its sentinel", func(t *testing.T) {
		in := Skip{Path: "a.go", Err: errors.New("store: facts carry a parse error (a.go): boom")}
		in.Err = &wrapped{msg: in.Err.Error(), err: store.ErrParseFailed}
		require.ErrorIs(t, in.Err, store.ErrParseFailed)

		out := checkpoint(t, in)
		assert.Equal(t, in.Path, out.Path)
		assert.Equal(t, in.Err.Error(), out.Err.Error(), "the message a -v run prints")
		assert.ErrorIs(t, out.Err, store.ErrParseFailed, "and the sentinel a caller tests")
	})

	t.Run("a failed map task does not acquire one", func(t *testing.T) {
		in := Skip{Path: "b.go", Err: errors.New("index: extract b.go: open b.go: permission denied")}

		out := checkpoint(t, in)
		assert.Equal(t, in.Path, out.Path)
		assert.Equal(t, in.Err.Error(), out.Err.Error())
		assert.NotErrorIs(t, out.Err, store.ErrParseFailed,
			"a file the extractor never saw was never a parse failure")
	})

	t.Run("no error at all", func(t *testing.T) {
		out := checkpoint(t, Skip{Path: "c.go"})
		assert.Equal(t, "c.go", out.Path)
		assert.NoError(t, out.Err)
	})
}

// TestResultSurvivesACheckpoint covers the workflow's own output, which is what
// a caller attaching to a run it did not start reads.
func TestResultSurvivesACheckpoint(t *testing.T) {
	in := Result{
		Coord:       coord.Coord{Scheme: "scip-go", Manager: "gomod", Name: "github.com/foo/bar", Version: coord.Unknown, Root: "/repo"},
		Files:       3,
		Loaded:      1,
		Skipped:     []Skip{{Path: "a.go", Err: &wrapped{msg: "nope", err: store.ErrParseFailed}}, {Path: "b.go", Err: errors.New("permission denied")}},
		Retries:     2,
		Concurrency: 8,
	}
	out := checkpoint(t, in)

	assert.Equal(t, in.Coord, out.Coord)
	assert.Equal(t, in.Files, out.Files)
	assert.Equal(t, in.Loaded, out.Loaded)
	assert.Equal(t, in.Retries, out.Retries)
	assert.Equal(t, in.Concurrency, out.Concurrency)
	require.Len(t, out.Skipped, 2)
	assert.ErrorIs(t, out.Skipped[0].Err, store.ErrParseFailed)
	assert.NotErrorIs(t, out.Skipped[1].Err, store.ErrParseFailed)
}

// TestFileRefSurvivesACheckpoint covers the map task's input, which DBOS records
// so it can re-run the task in another process.
func TestFileRefSurvivesACheckpoint(t *testing.T) {
	in := fileRef{
		Root:  "/repo",
		Path:  "pkg/a.go",
		Coord: coord.Coord{Scheme: "scip-go", Manager: "gomod", Name: "github.com/foo/bar", Version: coord.Unknown, Root: "/repo"},
	}
	assert.Equal(t, in, checkpoint(t, in))
}

// TestSiteSurvivesACheckpoint covers the resolve step's output, which every map
// task is handed a copy of.
func TestSiteSurvivesACheckpoint(t *testing.T) {
	in := site{Root: "/repo", Coord: coord.Coord{Scheme: "scip-go", Manager: "gomod", Name: "github.com/foo/bar", Version: "v0.1.0", Root: "/repo"}}
	assert.Equal(t, in, checkpoint(t, in))
}

// wrapped is an error that wraps a sentinel while keeping an arbitrary message,
// which is the shape mapFiles builds and the shape the round trip has to
// reproduce.
type wrapped struct {
	msg string
	err error
}

func (w *wrapped) Error() string { return w.msg }
func (w *wrapped) Unwrap() error { return w.err }
