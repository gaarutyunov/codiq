package index

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/artifact"
	"github.com/gaarutyunov/codiq/coord"
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

// TestExtractedSurvivesACheckpoint is M5's load-bearing serialization claim, and
// it replaced M4's.
//
// At M4 the map task's output *was* the facts, so the claim was that a whole
// facts.FileFacts round-trips through encoding/json. At M5 the facts do not
// cross at all (SPEC.md §5: "the map task checkpoints the artifact key, never
// the blob") — what crosses is this, and the equivalent claim about the facts
// is now a claim about the artifact, asserted in artifact/codec_test.go against
// the file the reduce actually reads.
//
// Both fields matter and they fail differently. A lost Key is a file the reduce
// never loads; a lost ParseError is a poison file promoted to a good one, which
// mapFiles would then let into the batch to delete its own graph.
func TestExtractedSurvivesACheckpoint(t *testing.T) {
	for _, in := range []extracted{
		{Key: "ab/" + strings.Repeat("ab", 32) + ".pb"},
		{ParseError: "syntax error at 12:4"},
		{},
	} {
		assert.Equal(t, in, checkpoint(t, in))
	}
}

// TestExtractedCarriesNoFacts is the milestone itself, stated as a test.
//
// M5 exists so that the map phase's output is bounded and small rather than one
// serialized parse tree per file. A field added to `extracted` that carried
// facts — the occurrences "just for the reduce", the source "just for
// debugging" — would undo it silently, since everything would still work and
// only `codiq_dbos` would grow. So the wire form is pinned: exactly two string
// fields, and a payload the size of a key.
func TestExtractedCarriesNoFacts(t *testing.T) {
	typ := reflect.TypeOf(extracted{})
	require.Equal(t, 2, typ.NumField(), "extracted is a key and a reason; nothing else may cross")
	for i := range typ.NumField() {
		assert.Equal(t, reflect.String, typ.Field(i).Type.Kind(),
			"%s must stay a scalar", typ.Field(i).Name)
	}

	encoded, err := json.Marshal(any(extracted{Key: artifact.Key("/some/repo/root", "internal/pkg/file.go")}))
	require.NoError(t, err)
	assert.Less(t, len(encoded), 128,
		"a checkpointed map-task output is a key, not a blob: %s", encoded)
}

// TestFactsAreNotWhatCrossesTheCheckpoint measures the substitution M5 made,
// over the real extractor rather than over a literal.
//
// The number is the point. What used to be recorded twice per file — once as the
// extract step's output and once as the map task's — is the JSON on the left;
// what is recorded twice per file now is the JSON on the right. The assertion is
// deliberately loose (an order of magnitude) because the ratio depends on the
// file, and deliberately present because "smaller" is the whole milestone and a
// regression to blob-passing would otherwise be invisible.
func TestFactsAreNotWhatCrossesTheCheckpoint(t *testing.T) {
	root := tree(t, repo)
	coords, err := coord.Resolve(root, "greeter")
	require.NoError(t, err)
	l := defaultLoader()

	for _, rel := range []string{"main.go", "greeter.go", "internal/store/store.go"} {
		t.Run(rel, func(t *testing.T) {
			ff, err := l.extract(root, filepath.Join(root, filepath.FromSlash(rel)), "greeter", coords.For(rel))
			require.NoError(t, err)
			require.NotEmpty(t, ff.Occurrences, "the fixture has to exercise the shapes")

			m4, err := json.Marshal(any(ff))
			require.NoError(t, err)
			m5, err := json.Marshal(any(extracted{Key: artifact.Key(root, rel)}))
			require.NoError(t, err)

			t.Logf("checkpoint payload: M4 %d bytes of facts, M5 %d bytes of key", len(m4), len(m5))
			assert.Less(t, len(m5)*10, len(m4),
				"the key has to be an order of magnitude smaller than the facts it replaced")
		})
	}
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
//
// A two-ecosystem set and not a single coordinate, because that is what the step
// records from M6 (coord.Set) and because the map inside it is the one part of
// the shape a JSON round trip could plausibly lose — which would hand every file
// the zero Coord and index a whole repository under `. . . .`.
func TestSiteSurvivesACheckpoint(t *testing.T) {
	gomod := coord.Coord{Scheme: coord.GoScheme, Manager: coord.GoManager, Name: "github.com/foo/bar", Version: "v0.1.0", Root: "/repo"}
	npm := coord.Coord{Scheme: coord.TSScheme, Manager: coord.NPMManager, Name: "@codiq/mixed", Version: "2.0.0", Root: "/repo"}
	in := site{
		Root: "/repo",
		Coords: coord.Set{
			ByExt:   map[string]coord.Coord{coord.GoExt: gomod, coord.TSExt: npm},
			Primary: gomod,
		},
	}

	out := checkpoint(t, in)

	assert.Equal(t, in, out)
	assert.Equal(t, gomod, out.Coords.For("pkg/a.go"), "a Go file still finds the go.mod coordinate")
	assert.Equal(t, npm, out.Coords.For("src/a.ts"), "a TypeScript file still finds the package.json one")
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
