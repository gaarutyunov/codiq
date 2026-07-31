package coord_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
)

func writePackageJSON(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.PackageJSONFile), []byte(body), 0o600))
	return dir
}

func TestFromPackageJSON(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    coord.Coord
		wantErr bool
	}{
		{
			name: "name and version",
			body: `{"name": "@codiq/greeter", "version": "1.2.3"}`,
			want: coord.Coord{
				Scheme:  coord.TSScheme,
				Manager: coord.NPMManager,
				Name:    "@codiq/greeter",
				Version: "1.2.3",
			},
		},
		{
			name: "unscoped name",
			body: `{"name": "greeter", "version": "0.1.0"}`,
			want: coord.Coord{
				Scheme:  coord.TSScheme,
				Manager: coord.NPMManager,
				Name:    "greeter",
				Version: "0.1.0",
			},
		},
		{
			// A private workspace root routinely declares neither. It still owns
			// its files, so it still resolves; the components that are genuinely
			// unknown say so with SCIP's marker rather than with a guess.
			name: "no name and no version",
			body: `{"private": true}`,
			want: coord.Coord{
				Scheme:  coord.TSScheme,
				Manager: coord.NPMManager,
				Name:    coord.Unknown,
				Version: coord.Unknown,
			},
		},
		{
			name: "fields the coordinate does not use are ignored",
			body: `{"name": "x", "version": "1.0.0", "scripts": {"build": "tsc"}, "dependencies": {}}`,
			want: coord.Coord{
				Scheme:  coord.TSScheme,
				Manager: coord.NPMManager,
				Name:    "x",
				Version: "1.0.0",
			},
		},
		{
			name:    "not JSON at all",
			body:    "this is not a manifest",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := writePackageJSON(t, tc.body)
			got, err := coord.FromPackageJSON(dir)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			tc.want.Root = dir
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFromPackageJSONMissingFile(t *testing.T) {
	_, err := coord.FromPackageJSON(t.TempDir())
	require.ErrorIs(t, err, os.ErrNotExist)
}

// TestNPMPrefixIsDistinguishableFromGo is the whole of what makes two languages
// safe in one graph (SPEC.md §14 M6, "cross-language queries work with no schema
// change"): the link pass joins on the rendered descriptor and nothing else, so
// the only thing keeping a TypeScript symbol from matching a Go one is that
// their prefixes cannot collide.
func TestNPMPrefixIsDistinguishableFromGo(t *testing.T) {
	npm := coord.Coord{Scheme: coord.TSScheme, Manager: coord.NPMManager, Name: "greeter", Version: "1.0.0"}
	gomod := coord.Coord{Scheme: coord.GoScheme, Manager: coord.GoManager, Name: "greeter", Version: "1.0.0"}

	assert.Equal(t, "scip-typescript npm greeter 1.0.0", npm.Prefix())
	assert.Equal(t, "scip-go gomod greeter 1.0.0", gomod.Prefix())
	assert.NotEqual(t, npm.Prefix(), gomod.Prefix())
}

func TestManifestsIncludesPackageJSON(t *testing.T) {
	assert.Contains(t, coord.Manifests(), coord.PackageJSONFile)
}

func TestResolveFindsPackageJSON(t *testing.T) {
	root := writePackageJSON(t, `{"name": "greeter", "version": "1.0.0"}`)
	nested := filepath.Join(root, "src", "deep")
	require.NoError(t, os.MkdirAll(nested, 0o750))

	set, err := coord.Resolve(nested)
	require.NoError(t, err)
	got := set.For("x" + coord.TSExt)
	assert.Equal(t, coord.TSScheme, got.Scheme)
	assert.Equal(t, coord.NPMManager, got.Manager)
	assert.Equal(t, "greeter", got.Name)
	assert.Equal(t, root, got.Root)
	assert.Equal(t, got, set.Primary, "the one ecosystem it found is the primary one")
}

// TestResolveGivesEachEcosystemItsOwnManifest is this file's central claim, and
// it used to be its opposite.
//
// Until M6 it read TestResolvePrefersGoModInAMixedDirectory and pinned a known
// defect: Resolve returned *one* coordinate for a directory and tried the
// registered manifests in sorted order, so "go.mod" < "package.json" decided a
// repository holding both, and index handed that single coordinate to every
// parser. Every TypeScript file in a mixed repository was therefore stamped with
// the Go module's scheme, manager, name and version — and a TypeScript class in
// greeter.ts rendered the byte-identical descriptor to a Go type in greeter/,
// which the link pass joined into a cross-language edge that was not real.
//
// A coordinate is a property of (repository, ecosystem) and not of a repository,
// so Resolve now returns one per ecosystem and a file gets its own language's.
// The assertion below is the inverse of the one that stood here: the two
// coordinates are read from the same directory and they are different, in every
// component that identifies a package.
func TestResolveGivesEachEcosystemItsOwnManifest(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.GoModFile),
		[]byte("module github.com/foo/bar\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.PackageJSONFile),
		[]byte(`{"name": "@codiq/mixed", "version": "2.0.0"}`), 0o600))

	set, err := coord.Resolve(dir)
	require.NoError(t, err)

	assert.Equal(t, "scip-go gomod github.com/foo/bar .", set.For("greeter.go").Prefix())
	assert.Equal(t, "scip-typescript npm @codiq/mixed 2.0.0", set.For("greeter.ts").Prefix())
	assert.NotEqual(t, set.For("greeter.go").Prefix(), set.For("greeter.ts").Prefix(),
		"a Go type and a TypeScript class in one repository must not render the same descriptor")

	// Both are rooted at the directory their manifest was read from, so both
	// languages' namespaces are relative to the repository and neither is empty.
	assert.Equal(t, dir, set.For("greeter.go").Root)
	assert.Equal(t, dir, set.For("greeter.ts").Root)

	// Primary is unchanged in kind: the report line still names one coordinate,
	// and manifest-name order is what picks it.
	assert.Equal(t, set.For("greeter.go"), set.Primary)
}
