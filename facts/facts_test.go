package facts_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/facts"
)

func TestDescriptorString(t *testing.T) {
	c := coord.Coord{
		Scheme: "scip-go", Manager: "gomod",
		Name: "github.com/foo/bar", Version: "v1",
	}

	tests := []struct {
		name string
		desc facts.Descriptor
		want string
	}{
		{
			name: "the SPEC 4.3 example",
			desc: facts.Descriptor{Prefix: c, Suffix: "pkg/Type#method()."},
			want: "scip-go gomod github.com/foo/bar v1 pkg/Type#method().",
		},
		{
			name: "an empty suffix names the package itself",
			desc: facts.Descriptor{Prefix: c},
			want: "scip-go gomod github.com/foo/bar v1",
		},
		{
			name: "an unknown component keeps the prefix parseable",
			desc: facts.Descriptor{Prefix: coord.Foreign("scip-go", "gomod", "fmt"), Suffix: "Println()."},
			want: "scip-go gomod fmt . Println().",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.desc.String())
		})
	}
}

// TestDescriptorStringIsTheJoinKey pins the property the link pass rests on:
// two descriptors built from the same coordinate and suffix are the same string,
// and a differing coordinate makes them differ even when the suffix matches.
func TestDescriptorStringIsTheJoinKey(t *testing.T) {
	ours := coord.Coord{Scheme: "scip-go", Manager: "gomod", Name: "github.com/foo/bar", Version: coord.Unknown}
	theirs := coord.Foreign("scip-go", "gomod", "github.com/other/thing")

	a := facts.Descriptor{Prefix: ours, Suffix: "Greeter#Greet()."}
	b := facts.Descriptor{Prefix: ours, Suffix: "Greeter#Greet()."}
	c := facts.Descriptor{Prefix: theirs, Suffix: "Greeter#Greet()."}

	assert.Equal(t, a.String(), b.String())
	assert.NotEqual(t, a.String(), c.String())
}

func TestRefs(t *testing.T) {
	tests := []struct {
		name string
		ref  facts.Ref
		want facts.Ref
	}{
		{
			name: "the file endpoint carries no id",
			ref:  facts.FileRef(),
			want: facts.Ref{Vertex: facts.VertexFile, ID: facts.NoID},
		},
		{
			name: "a scope endpoint",
			ref:  facts.ScopeRef(7),
			want: facts.Ref{Vertex: facts.VertexScope, ID: 7},
		},
		{
			name: "an occurrence endpoint",
			ref:  facts.OccurrenceRef(7),
			want: facts.Ref{Vertex: facts.VertexOccurrence, ID: 7},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.ref)
		})
	}

	// Scope and occurrence ids live in separate spaces, so the vertex kind is
	// what distinguishes them — the store needs two id maps, not one.
	assert.NotEqual(t, facts.ScopeRef(7), facts.OccurrenceRef(7))
}

func TestZeroValues(t *testing.T) {
	assert.Equal(t, facts.LocalID(0), facts.NoID, "NoID must be the zero value so an unset parent means absent")

	var ff facts.FileFacts
	assert.Empty(t, ff.Scopes)
	assert.Empty(t, ff.Occurrences)
	assert.Empty(t, ff.Edges)
	assert.Empty(t, ff.ParseError)
}
