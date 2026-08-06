package coord_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
)

func writeCargo(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.CargoFile), []byte(body), 0o600))
	return dir
}

func TestFromCargo(t *testing.T) {
	tests := []struct {
		name string
		body string
		want coord.Coord
	}{
		{
			name: "name and version",
			body: "[package]\nname = \"codiq-greeter\"\nversion = \"1.2.3\"\n",
			want: coord.Coord{Name: "codiq-greeter", Version: "1.2.3"},
		},
		{
			name: "single-quoted strings are TOML too",
			body: "[package]\nname = 'greeter'\nversion = '0.1.0'\n",
			want: coord.Coord{Name: "greeter", Version: "0.1.0"},
		},
		{
			name: "comments, blank lines and the rest of a real manifest",
			body: "# a crate\n\n[package]  # identity\nname = \"greeter\"  # the name\nversion = \"1.0.0\"\nedition = \"2021\"\n\n[dependencies]\nserde = \"1\"\n",
			want: coord.Coord{Name: "greeter", Version: "1.0.0"},
		},
		{
			// A workspace root's virtual manifest, which has no `[package]`
			// table at all. It still owns the tree it roots, so it still
			// resolves — with both components unknown, which can never
			// false-match (§4.3).
			name: "a virtual manifest has no package",
			body: "[workspace]\nmembers = [\"crates/*\"]\n",
			want: coord.Coord{Name: coord.Unknown, Version: coord.Unknown},
		},
		{
			// The commonest real shape in a workspace member: the name is the
			// crate's own and the version is inherited from the root. Cargo
			// forbids inheriting `name`, so only the version can land here.
			name: "an inherited version is unknown",
			body: "[package]\nname = \"member\"\nversion.workspace = true\n",
			want: coord.Coord{Name: "member", Version: coord.Unknown},
		},
		{
			name: "an inherited version written as an inline table",
			body: "[package]\nname = \"member\"\nversion = { workspace = true }\n",
			want: coord.Coord{Name: "member", Version: coord.Unknown},
		},
		{
			name: "an empty manifest",
			body: "",
			want: coord.Coord{Name: coord.Unknown, Version: coord.Unknown},
		},
		{
			// `[package.metadata]` is a different table and its keys are not the
			// crate's identity.
			name: "a subtable of package is not the package table",
			body: "[package]\nname = \"greeter\"\n\n[package.metadata.docs.rs]\nname = \"not-the-name\"\n",
			want: coord.Coord{Name: "greeter", Version: coord.Unknown},
		},
		{
			// A later table's keys must not overwrite the package's, which is
			// the whole reason the scanner tracks which table it is in. This is
			// not hypothetical for Cargo: `[dependencies]` entries and
			// `[[bin]]` sections both carry `name` and `version` keys.
			name: "a later table does not overwrite",
			body: "[package]\nname = \"greeter\"\nversion = \"1.0.0\"\n\n[[bin]]\nname = \"nope\"\n\n[dependencies.serde]\nversion = \"nope\"\n",
			want: coord.Coord{Name: "greeter", Version: "1.0.0"},
		},
		{
			// A multi-line string can hold anything, including a line that
			// looks like a table header. Trusting line boundaries here would
			// read the description as configuration.
			name: "a multi-line string is not configuration",
			body: "[package]\nname = \"greeter\"\ndescription = \"\"\"\n[package]\nname = \"evil\"\n\"\"\"\nversion = \"1.0.0\"\n",
			want: coord.Coord{Name: "greeter", Version: "1.0.0"},
		},
		{
			name: "a non-string value is not a name",
			body: "[package]\nname = [\"greeter\"]\nversion = 3\n",
			want: coord.Coord{Name: coord.Unknown, Version: coord.Unknown},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeCargo(t, tc.body)
			got, err := coord.FromCargo(dir)
			require.NoError(t, err)

			want := tc.want
			want.Scheme, want.Manager, want.Root = coord.RustScheme, coord.CargoManager, dir
			assert.Equal(t, want, got)
		})
	}
}

func TestFromCargoMissingFile(t *testing.T) {
	_, err := coord.FromCargo(t.TempDir())
	require.ErrorIs(t, err, os.ErrNotExist)
}

// TestCargoPrefixIsDistinguishableFromTheOthers is the whole of what makes a
// fourth language safe in one graph: the link pass joins on the rendered
// descriptor and nothing else, so the only thing keeping a Rust symbol from
// matching a Go, TypeScript or Python one is that their prefixes cannot
// collide — and here the *suffixes* genuinely do, because a Rust module, a
// TypeScript module and a Python module all derive their namespace from the
// file's path.
func TestCargoPrefixIsDistinguishableFromTheOthers(t *testing.T) {
	name, version := "greeter", "1.0.0"
	cargo := coord.Coord{Scheme: coord.RustScheme, Manager: coord.CargoManager, Name: name, Version: version}
	others := map[string]coord.Coord{
		"python":     {Scheme: coord.PyScheme, Manager: coord.PipManager, Name: name, Version: version},
		"typescript": {Scheme: coord.TSScheme, Manager: coord.NPMManager, Name: name, Version: version},
		"go":         {Scheme: coord.GoScheme, Manager: coord.GoManager, Name: name, Version: version},
	}
	for lang, other := range others {
		assert.NotEqual(t, other.Prefix(), cargo.Prefix(),
			"a Rust coordinate must not render the %s one's prefix", lang)
	}
}

// TestCargoIsRegistered is the registration half: the resolver has to be in the
// registry for a repository holding a Cargo.toml to resolve at all, and `.rs`
// has to be owned by it for a Rust file to be stamped with a cargo coordinate
// rather than with whatever other manifest the repository also holds. That
// second half is the M6 defect, one language on.
func TestCargoIsRegistered(t *testing.T) {
	assert.Contains(t, coord.Manifests(), coord.CargoFile)
	assert.Contains(t, coord.Extensions(), coord.RustExt)
}

// TestResolveStampsRustFilesWithTheCargoCoordinate is that claim over a real
// mixed tree: a repository holding a go.mod beside a Cargo.toml gives its `.rs`
// files the crate's coordinate and its `.go` files the module's, and the two
// prefixes differ. Resolving one coordinate per repository is what made every
// TypeScript file inherit the Go module's — and produced cross-language edges
// the graph then served as fact.
func TestResolveStampsRustFilesWithTheCargoCoordinate(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.GoModFile),
		[]byte("module github.com/foo/bar\n\ngo 1.25.0\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.CargoFile),
		[]byte("[package]\nname = \"corpus\"\nversion = \"0.2.0\"\n"), 0o600))

	set, err := coord.Resolve(dir, "greeter")
	require.NoError(t, err)

	rust := set.For("src/lib" + coord.RustExt)
	assert.Equal(t, coord.RustScheme, rust.Scheme)
	assert.Equal(t, "corpus", rust.Name)
	assert.Equal(t, "0.2.0", rust.Version)
	assert.Equal(t, dir, rust.Root)
	assert.NotEqual(t, set.For("main"+coord.GoExt).Prefix(), rust.Prefix())
}

// TestResolveStampsRustFilesWithoutAManifest is the other half of Set's
// contract: a repository with a go.mod and no Cargo.toml still has to give its
// Rust files a coordinate, and the one thing it must not be given is another
// language's. Scheme and manager come from the Ecosystem, and Root is the
// manifests' directory so that namespaces still separate one module from
// another.
func TestResolveStampsRustFilesWithoutAManifest(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.GoModFile),
		[]byte("module github.com/foo/bar\n\ngo 1.25.0\n"), 0o600))

	set, err := coord.Resolve(dir, "greeter")
	require.NoError(t, err)

	rust := set.For("src/lib" + coord.RustExt)
	assert.Equal(t, coord.RustScheme, rust.Scheme)
	assert.Equal(t, coord.CargoManager, rust.Manager)
	assert.Equal(t, "greeter", rust.Name,
		"the corpus names an ecosystem the repository declares no manifest for")
	assert.Equal(t, dir, rust.Root)
}
