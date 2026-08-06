package coord_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/extract"
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

	t.Run("reads the manifest of the directory it is given", func(t *testing.T) {
		set, err := coord.Resolve(root, "bar")
		require.NoError(t, err)
		got := set.For("x" + coord.GoExt)
		assert.Equal(t, "github.com/foo/bar", got.Name)
		// Root is the directory asked about, which is also the manifest's, so
		// namespaces are relative to the repository.
		assert.Equal(t, "internal/deep/", got.Namespace(filepath.Join(nested, "x.go")))
	})

	t.Run("names the one ecosystem it found", func(t *testing.T) {
		set, err := coord.Resolve(root, "bar")
		require.NoError(t, err)
		// A repository with a single ecosystem has exactly the coordinate it
		// had before coordinates became per-ecosystem, and Primary is where a
		// report with room for one still finds it.
		assert.Equal(t, "scip-go gomod github.com/foo/bar .", set.Primary.Prefix())
	})

	t.Run("an ecosystem with no manifest is not another ecosystem's", func(t *testing.T) {
		set, err := coord.Resolve(root, "bar")
		require.NoError(t, err)
		// There is no package.json in this tree. A .ts file in it still needs a
		// coordinate, and the one thing it must not be given is the Go module's:
		// that is what made a TypeScript class and a Go type render the same
		// descriptor.
		ts := set.For("x" + coord.TSExt)
		assert.Equal(t, "scip-typescript npm bar .", ts.Prefix())
		assert.NotEqual(t, set.For("x"+coord.GoExt).Prefix(), ts.Prefix())
		assert.Equal(t, "internal/deep/", ts.Namespace(filepath.Join(nested, "x.ts")),
			"a corpus-named package still separates one directory from another")
	})

	t.Run("does not read a manifest above the directory", func(t *testing.T) {
		// The subdirectory *is* the repository as far as this run is concerned,
		// so it does not inherit the module above it. Before the repository
		// bound this returned the Go module's coordinate with Root at the
		// module — paths measured from the subdirectory, namespaces from the
		// module, which is why narrowing this is a correction rather than a
		// regression.
		set, err := coord.Resolve(nested, "deep")
		require.NoError(t, err)
		assert.Equal(t, "scip-go gomod deep .", set.For("x"+coord.GoExt).Prefix())
		assert.Equal(t, nested, set.For("x"+coord.GoExt).Root)
	})

	t.Run("a repository with no manifest at all still resolves", func(t *testing.T) {
		dir := t.TempDir()
		set, err := coord.Resolve(dir, "solo")
		require.NoError(t, err)
		assert.Equal(t, "scip-go gomod solo .", set.For("x"+coord.GoExt).Prefix())
		assert.Equal(t, ". . solo .", set.Primary.Prefix(),
			"a repository that declared nothing is still named, and by the one name it has")
		assert.False(t, set.Primary.IsZero(), "a resolved run must not look like an unresolved one")
	})

	t.Run("refuses to resolve without a corpus", func(t *testing.T) {
		_, err := coord.Resolve(root, "")
		require.ErrorIs(t, err, coord.ErrNoCorpus)
	})
}

// TestResolveKeepsTwoManifestlessRepositoriesApart is the defect the corpus
// exists for, reproduced at the size it actually occurred.
//
// Two repositories with no manifest of their own sit under one parent that has
// one. That is not a contrived shape: it is `projects/<repo>` under a home
// directory holding a stray package.json, which is how twenty trees on one
// machine came to share a single coordinate rooted at `$HOME`. Under that
// coordinate, `src/greeter.go` in either repository rendered `src/` for a
// namespace and the same four leading words for a prefix — so two unrelated
// symbols produced byte-identical descriptors, and the link pass joins on the
// descriptor and nothing else (§7).
//
// The test asserts the trap is present before asserting it is sprung: the two
// files really are at the same repo-relative path under a parent that really
// does hold a manifest. Without that, "the two coordinates differ" could pass
// for reasons having nothing to do with the bound.
func TestResolveKeepsTwoManifestlessRepositoriesApart(t *testing.T) {
	parent := writeGoMod(t, "module github.com/foo/parent\n")
	alpha := filepath.Join(parent, "alpha")
	beta := filepath.Join(parent, "beta")
	for _, repo := range []string{alpha, beta} {
		require.NoError(t, os.MkdirAll(filepath.Join(repo, "src"), 0o750))
	}

	// The trap: one manifest above, none inside either, and the same
	// repo-relative path in both.
	require.FileExists(t, filepath.Join(parent, coord.GoModFile))
	require.NoFileExists(t, filepath.Join(alpha, coord.GoModFile))
	require.NoFileExists(t, filepath.Join(beta, coord.GoModFile))

	setA, err := coord.Resolve(alpha, "alpha")
	require.NoError(t, err)
	setB, err := coord.Resolve(beta, "beta")
	require.NoError(t, err)

	a, b := setA.For("x"+coord.GoExt), setB.For("x"+coord.GoExt)

	assert.NotEqual(t, a.Prefix(), b.Prefix(),
		"two repositories under one manifest must not share a coordinate")
	assert.Equal(t, "scip-go gomod alpha .", a.Prefix())
	assert.Equal(t, "scip-go gomod beta .", b.Prefix())

	// And neither took the parent's, which is the specific wrong answer.
	parentSet, err := coord.Resolve(parent, "parent")
	require.NoError(t, err)
	parentCoord := parentSet.For("x" + coord.GoExt)
	assert.Equal(t, "github.com/foo/parent", parentCoord.Name)
	assert.NotEqual(t, parentCoord.Prefix(), a.Prefix())
	assert.NotEqual(t, parentCoord.Prefix(), b.Prefix())

	// The descriptors the link pass would join on, rendered in full. Same path,
	// same directory, same symbol name, different descriptor — which is the whole
	// property, and the one that was false before the bound.
	same := filepath.Join("src", "greeter.go")
	descA := a.Prefix() + " " + a.Namespace(filepath.Join(alpha, same)) + "Greeter#"
	descB := b.Prefix() + " " + b.Namespace(filepath.Join(beta, same)) + "Greeter#"
	assert.Equal(t, "src/", a.Namespace(filepath.Join(alpha, same)),
		"the namespaces are identical, so the prefix is the only thing keeping them apart")
	assert.Equal(t, "src/", b.Namespace(filepath.Join(beta, same)))
	assert.NotEqual(t, descA, descB)
}

// TestExtensionsMatchTheParserRegistry is the check coord cannot make of itself.
//
// Every extension a parser reads has to be owned by an ecosystem, or files with
// that extension are stamped with no scheme at all; every extension an ecosystem
// owns has to have a parser, or the registration is one nothing consults. coord
// cannot assert this from non-test code — extract imports coord, and the
// dependency may not run the other way — so the external test package does it.
func TestExtensionsMatchTheParserRegistry(t *testing.T) {
	assert.Equal(t, extract.Extensions(), coord.Extensions(),
		"every parsed extension needs an ecosystem to give it a coordinate, and vice versa")
}
