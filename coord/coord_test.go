package coord_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
)

func writeGoMod(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.GoModFile), []byte(body), 0o600))
	return dir
}

func TestFromGoMod(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantName    string
		wantVersion string
		wantErr     bool
	}{
		{
			name:        "plain module",
			body:        "module github.com/foo/bar\n\ngo 1.25.0\n",
			wantName:    "github.com/foo/bar",
			wantVersion: coord.Unknown,
		},
		{
			name:        "major version suffix becomes the version",
			body:        "module github.com/foo/bar/v3\n\ngo 1.25.0\n",
			wantName:    "github.com/foo/bar/v3",
			wantVersion: "v3",
		},
		{
			name:        "v1 suffix is not a major version",
			body:        "module github.com/foo/bar/v1\n",
			wantName:    "github.com/foo/bar/v1",
			wantVersion: coord.Unknown,
		},
		{
			name:        "quoted module path",
			body:        "module \"github.com/foo/bar\"\n",
			wantName:    "github.com/foo/bar",
			wantVersion: coord.Unknown,
		},
		{
			name:        "trailing comment is stripped",
			body:        "module github.com/foo/bar // deprecated\n",
			wantName:    "github.com/foo/bar",
			wantVersion: coord.Unknown,
		},
		{
			name:        "directive keyword needs whitespace",
			body:        "modulefoo\nmodule github.com/foo/bar\n",
			wantName:    "github.com/foo/bar",
			wantVersion: coord.Unknown,
		},
		{
			name:    "no module directive",
			body:    "go 1.25.0\n",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeGoMod(t, tt.body)

			got, err := coord.FromGoMod(dir)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			assert.Equal(t, coord.GoScheme, got.Scheme)
			assert.Equal(t, coord.GoManager, got.Manager)
			assert.Equal(t, tt.wantName, got.Name)
			assert.Equal(t, tt.wantVersion, got.Version)
			assert.Equal(t, dir, got.Root)
		})
	}
}

func TestFromGoModMissingFile(t *testing.T) {
	_, err := coord.FromGoMod(t.TempDir())
	require.Error(t, err)
}

func TestPrefix(t *testing.T) {
	tests := []struct {
		name  string
		coord coord.Coord
		want  string
	}{
		{
			name:  "fully determined",
			coord: coord.Coord{Scheme: "scip-go", Manager: "gomod", Name: "github.com/foo/bar", Version: "v2"},
			want:  "scip-go gomod github.com/foo/bar v2",
		},
		{
			name:  "unknown version renders as a dot",
			coord: coord.Coord{Scheme: "scip-go", Manager: "gomod", Name: "github.com/foo/bar", Version: coord.Unknown},
			want:  "scip-go gomod github.com/foo/bar .",
		},
		{
			name:  "empty components render as dots so the prefix keeps four fields",
			coord: coord.Coord{},
			want:  ". . . .",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.coord.Prefix())
		})
	}
}

func TestNamespace(t *testing.T) {
	c := coord.Coord{Name: "github.com/foo/bar", Root: filepath.FromSlash("/repo")}

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "package root", path: "/repo/main.go", want: ""},
		{name: "one level down", path: "/repo/pkg/util.go", want: "pkg/"},
		{name: "nested", path: "/repo/internal/a/b/c.go", want: "internal/a/b/"},
		{name: "outside the root", path: "/elsewhere/x.go", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, c.Namespace(filepath.FromSlash(tt.path)))
		})
	}
}

func TestNamespaceWithoutRoot(t *testing.T) {
	c := coord.Coord{Name: "github.com/foo/bar"}
	assert.Empty(t, c.Namespace(filepath.FromSlash("/repo/pkg/util.go")))
}

func TestImport(t *testing.T) {
	c := coord.Coord{
		Scheme: coord.GoScheme, Manager: coord.GoManager,
		Name: "github.com/foo/bar", Version: "v2", Root: "/repo",
	}

	tests := []struct {
		name          string
		importPath    string
		wantCoord     coord.Coord
		wantNamespace string
	}{
		{
			name:          "the module itself",
			importPath:    "github.com/foo/bar",
			wantCoord:     c,
			wantNamespace: "",
		},
		{
			name:          "a package inside the module keeps the coordinate",
			importPath:    "github.com/foo/bar/internal/pretty",
			wantCoord:     c,
			wantNamespace: "internal/pretty/",
		},
		{
			name:          "another module becomes foreign with an unknown version",
			importPath:    "github.com/other/thing",
			wantCoord:     coord.Foreign(coord.GoScheme, coord.GoManager, "github.com/other/thing"),
			wantNamespace: "",
		},
		{
			name:          "the standard library is foreign too",
			importPath:    "fmt",
			wantCoord:     coord.Foreign(coord.GoScheme, coord.GoManager, "fmt"),
			wantNamespace: "",
		},
		{
			name:          "a prefix that is not a path boundary is not inside the module",
			importPath:    "github.com/foo/barbaz",
			wantCoord:     coord.Foreign(coord.GoScheme, coord.GoManager, "github.com/foo/barbaz"),
			wantNamespace: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCoord, gotNamespace := c.Import(tt.importPath)
			assert.Equal(t, tt.wantCoord, gotCoord)
			assert.Equal(t, tt.wantNamespace, gotNamespace)
		})
	}
}

// TestImportRoundTripsToNamespace pins the property the link pass depends on:
// resolving an in-module import must produce the same namespace that the
// imported package's own files produce for themselves, so descriptors match on
// the nose.
func TestImportRoundTripsToNamespace(t *testing.T) {
	root := t.TempDir()
	c := coord.Coord{
		Scheme: coord.GoScheme, Manager: coord.GoManager,
		Name: "github.com/foo/bar", Version: coord.Unknown, Root: root,
	}

	importedCoord, importedNamespace := c.Import("github.com/foo/bar/internal/pretty")
	ownNamespace := c.Namespace(filepath.Join(root, "internal", "pretty", "pretty.go"))

	assert.Equal(t, c, importedCoord)
	assert.Equal(t, ownNamespace, importedNamespace)
}

func TestForeign(t *testing.T) {
	got := coord.Foreign("scip-go", "gomod", "fmt")

	assert.Equal(t, "scip-go gomod fmt .", got.Prefix())
	assert.Empty(t, got.Root, "a foreign coordinate owns no files in this repo")
}

func TestManifestsIncludesGoMod(t *testing.T) {
	assert.Contains(t, coord.Manifests(), coord.GoModFile)
}

func TestResolve(t *testing.T) {
	root := writeGoMod(t, "module github.com/foo/bar\n")
	nested := filepath.Join(root, "internal", "deep")
	require.NoError(t, os.MkdirAll(nested, 0o750))

	t.Run("finds the manifest above the directory", func(t *testing.T) {
		got, err := coord.Resolve(nested)
		require.NoError(t, err)
		assert.Equal(t, "github.com/foo/bar", got.Name)
		// Root is the manifest's directory, not the directory asked about, so
		// namespaces are relative to the module.
		assert.Equal(t, "internal/deep/", got.Namespace(filepath.Join(nested, "x.go")))
	})

	t.Run("reports no manifest", func(t *testing.T) {
		_, err := coord.Resolve(t.TempDir())
		require.ErrorIs(t, err, coord.ErrNoManifest)
	})
}
