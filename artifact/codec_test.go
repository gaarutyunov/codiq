package artifact_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/artifact"
	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/extract"
	"github.com/gaarutyunov/codiq/facts"
)

// This file is the other half of the round-trip claim, and it is an external
// test package on purpose: it drives the *real* extractor, so what it asserts
// the codec carries is what the Go mapper actually emits rather than what the
// facts types can hold. artifact_test.go covers the second reading.
//
// The two together are what make M5's substitution safe. Up to M4 the facts
// crossed a checkpoint as JSON and index/dbos_test.go asserted that round trip;
// the artifact is now the only durable form they have, so the same claim has to
// hold here or a resumed run writes a different graph than an uninterrupted one.

// module is a small but complete corpus: a package clause, a type with a field,
// a method with a receiver and a body, a cross-file call and an import that
// leaves the module. Between them they produce every Role, every EdgeKind, both
// vertex kinds an edge can end at, and descriptors under two different package
// coordinates.
var module = map[string]string{
	"go.mod": "module github.com/foo/bar\n\ngo 1.24\n",
	"greeter.go": `package main

type Greeter struct {
	Name string
}

func (g Greeter) Greet() string {
	return "hello, " + g.Name
}
`,
	"main.go": `package main

import "fmt"

func main() {
	g := Greeter{Name: "world"}
	fmt.Println(g.Greet())
}
`,
}

func TestExtractedFactsSurviveTheArtifact(t *testing.T) {
	root := t.TempDir()
	for path, content := range module {
		require.NoError(t, os.WriteFile(filepath.Join(root, path), []byte(content), 0o644))
	}
	coords, err := coord.Resolve(root, "greeter")
	require.NoError(t, err)

	s, err := artifact.Open(t.TempDir())
	require.NoError(t, err)
	ctx := context.Background()

	for _, rel := range []string{"main.go", "greeter.go"} {
		t.Run(rel, func(t *testing.T) {
			path := filepath.Join(root, rel)
			parser, ok := extract.ParserFor(path)
			require.True(t, ok)
			src, err := os.ReadFile(path)
			require.NoError(t, err)

			in := parser.Parse(path, src, coords.For(path))
			in.File.Path = rel
			require.Empty(t, in.ParseError)
			require.NotEmpty(t, in.Occurrences, "the fixture has to exercise the shapes")
			require.NotEmpty(t, in.Edges)
			require.NotEmpty(t, in.Scopes)

			key := artifact.Key(root, rel)
			require.NoError(t, s.Write(ctx, key, in))
			out, err := s.Read(ctx, key)
			require.NoError(t, err)

			// Root is the documented exception; see artifact/codec.go.
			want := withoutCoordRoot(in)
			assert.Equal(t, want, out)

			// Stated separately because it is the value the whole cross-file
			// layer is derived from (SPEC.md §7): every descriptor has to render
			// to the same bytes on both sides of the volume.
			require.Len(t, out.Occurrences, len(in.Occurrences))
			for i := range in.Occurrences {
				assert.Equal(t, in.Occurrences[i].Descriptor.String(), out.Occurrences[i].Descriptor.String())
			}
		})
	}
}

// TestTheFixtureExercisesEveryShape guards the test above against its own
// corpus: a round trip over facts that never contain a reference, or never
// contain a scope→scope edge, would pass while losing exactly those.
func TestTheFixtureExercisesEveryShape(t *testing.T) {
	root := t.TempDir()
	for path, content := range module {
		require.NoError(t, os.WriteFile(filepath.Join(root, path), []byte(content), 0o644))
	}
	coords, err := coord.Resolve(root, "greeter")
	require.NoError(t, err)

	roles := map[facts.Role]bool{}
	kinds := map[facts.EdgeKind]bool{}
	targets := map[facts.VertexKind]bool{}
	prefixes := map[string]bool{}
	for _, rel := range []string{"main.go", "greeter.go"} {
		path := filepath.Join(root, rel)
		parser, ok := extract.ParserFor(path)
		require.True(t, ok)
		src, err := os.ReadFile(path)
		require.NoError(t, err)
		ff := parser.Parse(path, src, coords.For(path))
		for _, o := range ff.Occurrences {
			roles[o.Role] = true
			prefixes[o.Descriptor.Prefix.Prefix()] = true
		}
		for _, e := range ff.Edges {
			kinds[e.Kind] = true
			targets[e.Target.Vertex] = true
		}
	}

	assert.True(t, roles[facts.RoleDefinition] && roles[facts.RoleReference], "both roles")
	assert.True(t, kinds[facts.EdgeDefines] && kinds[facts.EdgeContains] && kinds[facts.EdgeReferencesLocal],
		"all three extracted edge kinds")
	assert.True(t, targets[facts.VertexScope] && targets[facts.VertexOccurrence],
		"contains ends at both vertex tables")
	assert.Greater(t, len(prefixes), 1,
		"the fixture must import out of the module, so a descriptor prefix is not always the file's coordinate")
}

func withoutCoordRoot(ff facts.FileFacts) facts.FileFacts {
	out := ff
	out.File.Coord.Root = ""
	out.Occurrences = make([]facts.Occurrence, len(ff.Occurrences))
	copy(out.Occurrences, ff.Occurrences)
	for i := range out.Occurrences {
		out.Occurrences[i].Descriptor.Prefix.Root = ""
	}
	return out
}

// TestArtifactSizeAgainstTheSource records what the shared volume actually has
// to hold, which is the number the decision *not* to budget poison files by
// size rests on (see the package comment's note on Decision 16).
//
// Two ratios, and the second is the one that matters. Against the JSON the
// facts used to be checkpointed as, protobuf is the smaller encoding, which is
// Decision 3's "typed, compact" claim measured rather than asserted. Against
// the *source*, an artifact is several times bigger — a fact carries a full
// SCIP descriptor per occurrence, and a descriptor is longer than the
// identifier it names — so a volume has to be sized against the corpus with
// that multiple applied, and an operator who does not know it will size it
// wrong.
func TestArtifactSizeAgainstTheSource(t *testing.T) {
	root := t.TempDir()
	for path, content := range module {
		require.NoError(t, os.WriteFile(filepath.Join(root, path), []byte(content), 0o644))
	}
	coords, err := coord.Resolve(root, "greeter")
	require.NoError(t, err)

	s, err := artifact.Open(t.TempDir())
	require.NoError(t, err)
	ctx := context.Background()

	var source, onDisk, asJSON int
	for _, rel := range []string{"main.go", "greeter.go"} {
		path := filepath.Join(root, rel)
		parser, ok := extract.ParserFor(path)
		require.True(t, ok)
		src, err := os.ReadFile(path)
		require.NoError(t, err)
		ff := parser.Parse(path, src, coords.For(path))
		ff.File.Path = rel

		key := artifact.Key(root, rel)
		require.NoError(t, s.Write(ctx, key, ff))
		info, err := os.Stat(filepath.Join(s.Dir(), filepath.FromSlash(key)))
		require.NoError(t, err)

		encoded, err := json.Marshal(any(ff))
		require.NoError(t, err)

		source += len(src)
		onDisk += int(info.Size())
		asJSON += len(encoded)
	}

	t.Logf("source %d B -> artifact %d B (x%.1f); the same facts as checkpointed JSON: %d B (x%.1f)",
		source, onDisk, float64(onDisk)/float64(source), asJSON, float64(asJSON)/float64(source))
	assert.Less(t, onDisk, asJSON,
		"protobuf has to be the smaller encoding, or Decision 3 bought nothing")
}
