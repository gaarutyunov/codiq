package extract_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/extract"
	"github.com/gaarutyunov/codiq/extract/golang"
)

func TestParserFor(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "a Go file", path: filepath.FromSlash("/repo/main.go"), want: true},
		{name: "a Go file in a subdirectory", path: filepath.FromSlash("/repo/pkg/a/b.go"), want: true},
		{name: "no extension", path: filepath.FromSlash("/repo/Makefile"), want: false},
		{name: "an unregistered extension", path: filepath.FromSlash("/repo/main.rs"), want: false},
		{name: "the extension is case sensitive", path: filepath.FromSlash("/repo/main.GO"), want: false},
		{name: "not the whole name", path: filepath.FromSlash("/repo/go"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := extract.ParserFor(tt.path)
			assert.Equal(t, tt.want, ok)
			assert.Equal(t, tt.want, extract.Supported(tt.path))
			if tt.want {
				require.NotNil(t, p)
			} else {
				assert.Nil(t, p)
			}
		})
	}
}

func TestExtensions(t *testing.T) {
	assert.Equal(t, []string{golang.Ext}, extract.Extensions())
}

// TestRegisteredParserParses is the end-to-end check on the registry: the entry
// for ".go" is a working parser, not just a non-nil interface value.
func TestRegisteredParserParses(t *testing.T) {
	p, ok := extract.ParserFor(filepath.FromSlash("/repo/main.go"))
	require.True(t, ok)

	c := coord.Coord{
		Scheme: coord.GoScheme, Manager: coord.GoManager,
		Name: "github.com/foo/bar", Version: coord.Unknown, Root: filepath.FromSlash("/repo"),
	}
	ff := p.Parse(filepath.FromSlash("/repo/main.go"), []byte("package main\n\nfunc main() {}\n"), c)

	require.Empty(t, ff.ParseError)
	assert.Equal(t, golang.Lang, ff.File.Lang)
	assert.Equal(t, c, ff.File.Coord)

	descriptors := make([]string, 0, len(ff.Occurrences))
	for _, occ := range ff.Occurrences {
		descriptors = append(descriptors, occ.Descriptor.String())
	}
	assert.Contains(t, descriptors, "scip-go gomod github.com/foo/bar . main().")
}

// TestGolangParserSatisfiesParserStructurally documents the SPEC 12 constraint
// the registry depends on: the assignment below compiles, and it compiles
// because golang.Parser has the right method set — golang imports facts and
// coord and never extract, so there is no cycle to break.
func TestGolangParserSatisfiesParserStructurally(t *testing.T) {
	var p extract.Parser = golang.New()
	assert.NotNil(t, p)
}
