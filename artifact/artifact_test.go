package artifact

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/facts"
)

// sample is a FileFacts with every field of every type set to something that is
// not its zero value, including the ones a codec can plausibly lose: a nested
// Descriptor whose prefix is a struct, both roles, all three edge kinds, a
// NoID parent and a non-NoID one.
//
// Hand-built rather than extracted, on purpose: an extractor fixture proves the
// codec carries what today's mapper emits, and this proves it carries what the
// type can hold. The extracted case is covered separately in codec_test.go.
func sample() facts.FileFacts {
	c := coord.Coord{Scheme: "scip-go", Manager: "gomod", Name: "github.com/foo/bar", Version: "v1.2.3"}
	foreign := coord.Foreign("scip-go", "gomod", "fmt")
	return facts.FileFacts{
		File: facts.File{Path: "pkg/a.go", Lang: "go", Coord: c},
		Scopes: []facts.Scope{
			{ID: 1, Kind: facts.ScopeFile, RangeStart: 0, RangeEnd: 400, Parent: facts.NoID},
			{ID: 2, Kind: facts.ScopeFunction, RangeStart: 20, RangeEnd: 90, Parent: 1},
		},
		Occurrences: []facts.Occurrence{
			{
				ID:         1,
				Descriptor: facts.Descriptor{Prefix: c, Suffix: "pkg/Type#method()."},
				Role:       facts.RoleDefinition,
				SymbolKind: facts.KindMethod,
				Name:       "method",
				RangeStart: 7, RangeEnd: 13,
				Scope: 2,
			},
			{
				// A reference into another module: its prefix is a *foreign*
				// coordinate, not the file's, which is why the codec cannot
				// reconstruct a descriptor prefix from the file row.
				ID:         2,
				Descriptor: facts.Descriptor{Prefix: foreign, Suffix: "Println()."},
				Role:       facts.RoleReference,
				SymbolKind: facts.KindFunction,
				Name:       "Println",
				RangeStart: 40, RangeEnd: 47,
				Scope: facts.NoID,
			},
		},
		Edges: []facts.Edge{
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(1)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(1), Target: facts.ScopeRef(2)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(2), Target: facts.OccurrenceRef(1)},
			{Kind: facts.EdgeReferencesLocal, Source: facts.OccurrenceRef(2), Target: facts.OccurrenceRef(1)},
		},
	}
}

func store(t *testing.T) *Store {
	t.Helper()
	// SPEC.md §13: "from M5, artifacts use a temp dir in tests (no object
	// store)". t.TempDir is removed for us at the end of the test, which is why
	// nothing here has to think about GC.
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	return s
}

// TestWriteReadRoundTrip is the whole contract in one assertion: what the
// reduce reads back is what the map task wrote.
//
// This is the claim M5 rests on. The facts no longer cross the checkpoint, so
// the artifact is the *only* durable form they have; a field this loses is a
// column the graph silently ends up without.
func TestWriteReadRoundTrip(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	in := sample()
	key := Key("/repo", "pkg/a.go")

	require.NoError(t, s.Write(ctx, key, in))
	out, err := s.Read(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, in, out)
}

// TestRoundTripDropsOnlyCoordRoot pins the one documented lossy field.
//
// coord.Coord.Root is extraction-time state and the generated schema does not
// carry it (schema/proto's Coord comment, codec.go's package comment). Asserting
// it here rather than leaving it to the doc means a future decision to carry it
// — or to start reading it after extraction — fails a test instead of surprising
// somebody.
func TestRoundTripDropsOnlyCoordRoot(t *testing.T) {
	s := store(t)
	ctx := context.Background()

	in := sample()
	in.File.Coord.Root = "/repo"
	in.Occurrences[0].Descriptor.Prefix.Root = "/repo"

	key := Key("/repo", "pkg/a.go")
	require.NoError(t, s.Write(ctx, key, in))
	out, err := s.Read(ctx, key)
	require.NoError(t, err)

	assert.Empty(t, out.File.Coord.Root)
	assert.Empty(t, out.Occurrences[0].Descriptor.Prefix.Root)

	// Everything the rest of the pipeline reads is intact: the four descriptor
	// components store.resolveFile writes, and the joined descriptor the link
	// pass matches on (SPEC.md §7).
	want, got := in, out
	want.File.Coord.Root = ""
	want.Occurrences[0].Descriptor.Prefix.Root = ""
	assert.Equal(t, want, got)
	assert.Equal(t, in.Occurrences[0].Descriptor.String(), out.Occurrences[0].Descriptor.String(),
		"the descriptor is the link pass's only join key")
}

// TestPoisonFactsRoundTrip covers the shape SPEC.md §5 calls a poison file:
// empty row slices and a message.
//
// index does not write an artifact for one (index/dbos.go's `extracted`), but
// the codec has to carry the shape regardless — nothing about a FileFacts stops
// a caller handing one over, and a codec that dropped ParseError would turn a
// poison file into a good one that deletes its own graph.
func TestPoisonFactsRoundTrip(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	in := facts.FileFacts{
		File:       facts.File{Path: "broken.go", Lang: "go"},
		ParseError: "syntax error at 12:4",
	}
	key := Key("/repo", "broken.go")
	require.NoError(t, s.Write(ctx, key, in))

	out, err := s.Read(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, in, out)
	assert.Equal(t, "syntax error at 12:4", out.ParseError)
}

// TestKeyIsDeterministicAndCollisionFree is the key scheme's contract.
//
// Determinism is what makes Write idempotent under at-least-once: a re-run of
// the extract step recomputes the same key and overwrites its own artifact.
// Distinctness across both inputs is what makes one shared volume safe for more
// than one tree (SPEC.md §10).
func TestKeyIsDeterministicAndCollisionFree(t *testing.T) {
	assert.Equal(t, Key("/repo", "a/b.go"), Key("/repo", "a/b.go"), "same input, same key")

	seen := map[string]string{}
	for _, tc := range []struct{ root, path string }{
		{"/repo", "a/b.go"},
		{"/repo", "a/c.go"},
		{"/other", "a/b.go"},
		// The pair is NUL-delimited, so these two cannot collide the way a plain
		// concatenation would.
		{"/a", "b/c.go"},
		{"/a/b", "c.go"},
	} {
		k := Key(tc.root, tc.path)
		if prev, dup := seen[k]; dup {
			t.Fatalf("%s + %s collides with %s", tc.root, tc.path, prev)
		}
		seen[k] = tc.root + " + " + tc.path
		assert.True(t, valid(k), "Key must produce a key path resolves: %q", k)
	}
}

// TestWriteIsIdempotent is the at-least-once requirement (index/dbos.go: a step
// whose side effect landed but whose checkpoint did not is re-run).
func TestWriteIsIdempotent(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	key := Key("/repo", "pkg/a.go")
	in := sample()

	require.NoError(t, s.Write(ctx, key, in))
	require.NoError(t, s.Write(ctx, key, in), "a re-run must not fail on its own artifact")

	out, err := s.Read(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, in, out)

	// And exactly one file, not one per attempt: the temporary is renamed over
	// the name rather than left beside it.
	shard := filepath.Join(s.Dir(), filepath.FromSlash(key))
	entries, err := os.ReadDir(filepath.Dir(shard))
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

// TestDeleteIsTheGC covers SPEC.md §6 / Decision 16 from the store's side: the
// reduce deletes on success, and a delete of something already gone is not a
// failure, because the reduce step is itself at-least-once.
func TestDeleteIsTheGC(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	keys := []string{Key("/repo", "a.go"), Key("/repo", "b.go")}
	for _, k := range keys {
		require.NoError(t, s.Write(ctx, k, sample()))
	}

	require.NoError(t, s.Delete(ctx, keys...))
	for _, k := range keys {
		_, err := s.Read(ctx, k)
		assert.ErrorIs(t, err, ErrNotFound)
	}

	assert.NoError(t, s.Delete(ctx, keys...), "a replayed reduce deletes what it already deleted")
}

// TestReadOfAMissingKeyIsNotAReadFailure is what lets a caller tell "already
// GC'd" from "the volume is broken".
func TestReadOfAMissingKeyIsNotAReadFailure(t *testing.T) {
	_, err := store(t).Read(context.Background(), Key("/repo", "never-written.go"))
	assert.ErrorIs(t, err, ErrNotFound)
}

// TestMalformedKeysAreRefused covers the one place this package takes input it
// did not produce: a key read back off a checkpoint another process wrote.
//
// Every case below would otherwise resolve to a path outside the store, or to a
// name the store did not choose. Refusing on shape means none of them reaches a
// filesystem call at all.
func TestMalformedKeysAreRefused(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	good := Key("/repo", "a.go")

	for _, key := range []string{
		"",
		"..",
		"../../etc/passwd",
		"ab/../../etc/passwd",
		strings.TrimSuffix(good, ".pb"),          // no suffix
		strings.TrimSuffix(good, ".pb") + ".txt", // wrong suffix
		good[3:],                                 // no shard
		"zz" + good[2:],                          // non-hex shard
		"ab/" + strings.Repeat("g", 64) + ".pb",  // non-hex name
		"ab/" + strings.Repeat("a", 64) + ".pb",  // shard does not match the name
	} {
		t.Run(key, func(t *testing.T) {
			assert.Error(t, s.Write(ctx, key, sample()))
			_, err := s.Read(ctx, key)
			assert.Error(t, err)
			assert.Error(t, s.Delete(ctx, key))
		})
	}
}

// TestOpenCreatesTheDirectory covers the first-boot case: §11.1's `artifacts`
// volume is mounted empty.
func TestOpenCreatesTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "artifacts")
	s, err := Open(dir)
	require.NoError(t, err)
	assert.Equal(t, dir, s.Dir())

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	_, err = Open("")
	assert.Error(t, err, "a store with no directory is a configuration error")
}
