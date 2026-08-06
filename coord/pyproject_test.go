package coord_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
)

func writePyProject(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.PyProjectFile), []byte(body), 0o600))
	return dir
}

func TestFromPyProject(t *testing.T) {
	tests := []struct {
		name string
		body string
		want coord.Coord
	}{
		{
			name: "name and version",
			body: "[project]\nname = \"codiq-greeter\"\nversion = \"1.2.3\"\n",
			want: coord.Coord{Name: "codiq-greeter", Version: "1.2.3"},
		},
		{
			name: "single-quoted strings are TOML too",
			body: "[project]\nname = 'greeter'\nversion = '0.1.0'\n",
			want: coord.Coord{Name: "greeter", Version: "0.1.0"},
		},
		{
			name: "comments and blank lines",
			body: "# a build file\n\n[project]  # identity\nname = \"greeter\"  # the name\nversion = \"1.0.0\"\n",
			want: coord.Coord{Name: "greeter", Version: "1.0.0"},
		},
		{
			// The commonest real shape: a name, and a version the build backend
			// computes. An unknown component is written rather than guessed, and
			// SCIP's marker can never collide with a real one (§4.3).
			name: "a dynamic version is unknown",
			body: "[project]\nname = \"greeter\"\ndynamic = [\"version\"]\n",
			want: coord.Coord{Name: "greeter", Version: coord.Unknown},
		},
		{
			// A Poetry-1.x manifest, or an application that is not packaged at
			// all. It still owns its files, so it still resolves.
			name: "no [project] table",
			body: "[tool.poetry]\nname = \"legacy\"\nversion = \"9.9.9\"\n",
			want: coord.Coord{Name: coord.Unknown, Version: coord.Unknown},
		},
		{
			name: "an empty manifest",
			body: "",
			want: coord.Coord{Name: coord.Unknown, Version: coord.Unknown},
		},
		{
			// `[project.optional-dependencies]` is a different table and its
			// keys are not the package's identity.
			name: "a subtable of project is not the project table",
			body: "[project]\nname = \"greeter\"\n\n[project.urls]\nname = \"not-the-name\"\n",
			want: coord.Coord{Name: "greeter", Version: coord.Unknown},
		},
		{
			// A later table's keys must not overwrite the project's, which is
			// the whole reason the scanner tracks which table it is in.
			name: "a later table does not overwrite",
			body: "[project]\nname = \"greeter\"\nversion = \"1.0.0\"\n\n[tool.ruff]\nname = \"nope\"\nversion = \"nope\"\n",
			want: coord.Coord{Name: "greeter", Version: "1.0.0"},
		},
		{
			// A multi-line string can hold anything, including a line that
			// looks like a table header. Trusting line boundaries here would
			// read the readme as configuration.
			name: "a multi-line string is not configuration",
			body: "[project]\nname = \"greeter\"\nreadme = \"\"\"\n[project]\nname = \"evil\"\n\"\"\"\nversion = \"1.0.0\"\n",
			want: coord.Coord{Name: "greeter", Version: "1.0.0"},
		},
		{
			// A value that is not a plain string is not a name.
			name: "a non-string value is ignored",
			body: "[project]\nname = [\"greeter\"]\nversion = 3\n",
			want: coord.Coord{Name: coord.Unknown, Version: coord.Unknown},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := writePyProject(t, tc.body)
			got, err := coord.FromPyProject(dir)
			require.NoError(t, err)

			want := tc.want
			want.Scheme, want.Manager, want.Root = coord.PyScheme, coord.PipManager, dir
			assert.Equal(t, want, got)
		})
	}
}

func TestFromPyProjectMissingFile(t *testing.T) {
	_, err := coord.FromPyProject(t.TempDir())
	require.ErrorIs(t, err, os.ErrNotExist)
}

// TestPyPrefixIsDistinguishableFromTheOthers is the whole of what makes a third
// language safe in one graph (SPEC.md §14 M7): the link pass joins on the
// rendered descriptor and nothing else, so the only thing keeping a Python
// symbol from matching a Go or TypeScript one is that their prefixes cannot
// collide — and here the *suffixes* genuinely do, because a Python module and a
// TypeScript module both derive their namespace from the file's path.
func TestPyPrefixIsDistinguishableFromTheOthers(t *testing.T) {
	name, version := "greeter", "1.0.0"
	py := coord.Coord{Scheme: coord.PyScheme, Manager: coord.PipManager, Name: name, Version: version}
	ts := coord.Coord{Scheme: coord.TSScheme, Manager: coord.NPMManager, Name: name, Version: version}
	gomod := coord.Coord{Scheme: coord.GoScheme, Manager: coord.GoManager, Name: name, Version: version}

	assert.Equal(t, "scip-python pip greeter 1.0.0", py.Prefix())
	prefixes := map[string]bool{}
	for _, c := range []coord.Coord{py, ts, gomod} {
		require.Falsef(t, prefixes[c.Prefix()], "two ecosystems render %q", c.Prefix())
		prefixes[c.Prefix()] = true
	}
}

func TestManifestsIncludesPyProject(t *testing.T) {
	assert.Contains(t, coord.Manifests(), coord.PyProjectFile)
}

func TestResolveFindsPyProject(t *testing.T) {
	root := writePyProject(t, "[project]\nname = \"greeter\"\nversion = \"1.0.0\"\n")

	set, err := coord.Resolve(root, "greeter")
	require.NoError(t, err)
	got := set.For("x" + coord.PyExt)
	assert.Equal(t, "scip-python pip greeter 1.0.0", got.Prefix())
	assert.Equal(t, root, got.Root)
	assert.Equal(t, got, set.Primary, "the one ecosystem it found is the primary one")
}

// TestResolveKeepsThreeEcosystemsApart is TestResolveGivesEachEcosystemItsOwnManifest
// with the third language added, and it is a strictly stronger claim than the
// two-language version: a Go package in `greeter/`, a `greeter.ts` and a
// `greeter.py` all derive the namespace `greeter/`, and the TypeScript and
// Python ones agree on the member name as well — `greet` in both — so
// everything after the coordinate is byte-identical between two of the three.
// The coordinate prefix is the only thing left keeping them apart.
func TestResolveKeepsThreeEcosystemsApart(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.GoModFile),
		[]byte("module github.com/foo/bar\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.PackageJSONFile),
		[]byte(`{"name": "@codiq/mixed", "version": "2.0.0"}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.PyProjectFile),
		[]byte("[project]\nname = \"codiq-mixed\"\nversion = \"3.0.0\"\n"), 0o600))

	set, err := coord.Resolve(dir, "greeter")
	require.NoError(t, err)

	assert.Equal(t, "scip-go gomod github.com/foo/bar .", set.For("greeter.go").Prefix())
	assert.Equal(t, "scip-typescript npm @codiq/mixed 2.0.0", set.For("greeter.ts").Prefix())
	assert.Equal(t, "scip-python pip codiq-mixed 3.0.0", set.For("greeter.py").Prefix())

	seen := map[string]string{}
	for _, ext := range []string{".go", ".ts", ".py"} {
		p := set.For(ext).Prefix()
		require.Emptyf(t, seen[p], "%s and %s render the same coordinate", seen[p], ext)
		seen[p] = ext
		// Every ecosystem is rooted at the directory its manifest was read
		// from, so all three languages' namespaces are relative to the
		// repository and none is empty.
		assert.Equal(t, dir, set.For(ext).Root)
	}

	// Primary is unchanged in kind: the report line still names one coordinate,
	// and manifest-name order is what picks it.
	assert.Equal(t, set.For(".go"), set.Primary)
}

// A repository with no pyproject.toml still has to give its .py files a
// coordinate, and there are two things it must not give them: another language's,
// and a name that every other manifest-less repository also has.
func TestResolveNamesPythonAfterTheCorpus(t *testing.T) {
	root := writeGoMod(t, "module github.com/foo/bar\n")

	set, err := coord.Resolve(root, "greeter")
	require.NoError(t, err)

	py := set.For("x" + coord.PyExt)
	assert.Equal(t, "scip-python pip greeter .", py.Prefix())
	assert.NotEqual(t, set.For("x"+coord.GoExt).Prefix(), py.Prefix())
	assert.Equal(t, root, py.Root, "a corpus-named package still separates one directory from another")

	// The version stays Unknown and the name does not, which is the whole of
	// what the corpus changed about this coordinate: a version genuinely cannot
	// be determined here, and a name now always can.
	assert.Equal(t, coord.Unknown, py.Version)
}
