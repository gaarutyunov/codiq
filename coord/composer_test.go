package coord_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
)

func writeComposerJSON(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.ComposerFile), []byte(body), 0o600))
	return dir
}

func TestFromComposer(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    coord.Coord
		wantErr bool
	}{
		{
			name: "name and version",
			body: `{"name": "codiq/greeter", "version": "1.2.3"}`,
			want: coord.Coord{
				Scheme:  coord.PHPScheme,
				Manager: coord.ComposerManager,
				Name:    "codiq/greeter",
				Version: "1.2.3",
			},
		},
		{
			// The recommended state of a library's manifest, and so the case that
			// matters most. Composer's own documentation tells package authors to
			// omit `version` because Packagist derives it from the git tag, so a
			// name with no version is what a correct library looks like — not a
			// defect, and not a licence to invent `1.0.0` or `dev-main`. A version
			// that renders is a version the link pass joins on.
			name: "a library states its name and deliberately omits its version",
			body: `{
				"name": "codiq/greeter",
				"description": "A greeter",
				"require": {"php": ">=8.1"},
				"autoload": {"psr-4": {"Com\\Example\\Greeting\\": "src/"}}
			}`,
			want: coord.Coord{
				Scheme:  coord.PHPScheme,
				Manager: coord.ComposerManager,
				Name:    "codiq/greeter",
				Version: coord.Unknown,
			},
		},
		{
			// An application's root manifest: dependencies and nothing else. It
			// still owns its files, so it still resolves.
			name: "an application states neither",
			body: `{"require": {"monolog/monolog": "^3.0"}, "config": {"sort-packages": true}}`,
			want: coord.Coord{
				Scheme:  coord.PHPScheme,
				Manager: coord.ComposerManager,
				Name:    coord.Unknown,
				Version: coord.Unknown,
			},
		},
		{
			// autoload.psr-4 is the field this deliberately does not read. It maps a
			// namespace prefix to a directory, and extract/php reads the namespace
			// out of the file's own `namespace` statement — which PHP requires and
			// which is authoritative, since PSR-4 governs where the autoloader looks
			// for a file and not what the symbols in it are called.
			name: "the autoload map is not identity and is ignored",
			body: `{
				"name": "codiq/greeter",
				"version": "2.0.0",
				"autoload": {"psr-4": {"Com\\Example\\": "src/", "Com\\Example\\Tests\\": "tests/"}}
			}`,
			want: coord.Coord{
				Scheme:  coord.PHPScheme,
				Manager: coord.ComposerManager,
				Name:    "codiq/greeter",
				Version: "2.0.0",
			},
		},
		{
			name: "whitespace around a value states the value",
			body: `{"name": "  codiq/greeter  ", "version": " 1.0.0 "}`,
			want: coord.Coord{
				Scheme:  coord.PHPScheme,
				Manager: coord.ComposerManager,
				Name:    "codiq/greeter",
				Version: "1.0.0",
			},
		},
		{
			// A composer.json is validated by every `composer` invocation and
			// rewritten by `composer require`, so one that does not parse is a broken
			// tree rather than an incomplete manifest — which is why this is an error
			// and a half-valid MSBuild property sheet is not.
			name:    "not JSON at all",
			body:    "this is not a manifest",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeComposerJSON(t, tc.body)
			got, err := coord.FromComposer(dir)
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

func TestFromComposerMissingFile(t *testing.T) {
	_, err := coord.FromComposer(t.TempDir())
	require.ErrorIs(t, err, os.ErrNotExist)
}

// TestComposerPrefixIsDistinguishableFromTheOtherSeven is the property eight
// ecosystems in one `occurrence` table rest on: the link pass joins on the
// rendered descriptor and nothing else (§7), so nothing may keep two languages
// apart except that their prefixes cannot collide.
//
// The name is deliberately the same in all eight, which is the adversarial case
// — a repository really can hold a Go module, an npm package, a PyPI
// distribution, a crate, a Maven artifact, a NuGet package, a gem and a Composer
// package all called `greeter`.
func TestComposerPrefixIsDistinguishableFromTheOtherSeven(t *testing.T) {
	pairs := []struct{ scheme, manager string }{
		{coord.GoScheme, coord.GoManager},
		{coord.TSScheme, coord.NPMManager},
		{coord.PyScheme, coord.PipManager},
		{coord.RustScheme, coord.CargoManager},
		{coord.JavaScheme, coord.MavenManager},
		{coord.CSharpScheme, coord.NuGetManager},
		{coord.RubyScheme, coord.GemManager},
		{coord.PHPScheme, coord.ComposerManager},
	}

	seen := map[string]bool{}
	for _, p := range pairs {
		prefix := coord.Coord{Scheme: p.scheme, Manager: p.manager, Name: "greeter", Version: "1.0.0"}.Prefix()
		assert.False(t, seen[prefix], "two ecosystems render the prefix %q", prefix)
		seen[prefix] = true
	}
	assert.Len(t, seen, len(pairs))

	php := coord.Coord{Scheme: coord.PHPScheme, Manager: coord.ComposerManager, Name: "codiq/greeter", Version: "1.0.0"}
	assert.Equal(t, "scip-php composer codiq/greeter 1.0.0", php.Prefix())
}

func TestManifestsIncludesComposerJSON(t *testing.T) {
	assert.Contains(t, coord.Manifests(), coord.ComposerFile)
}

func TestResolveFindsComposerJSON(t *testing.T) {
	root := writeComposerJSON(t, `{"name": "codiq/greeter", "version": "1.0.0"}`)
	nested := filepath.Join(root, "src", "Greeter")
	require.NoError(t, os.MkdirAll(nested, 0o750))

	set, err := coord.Resolve(nested)
	require.NoError(t, err)
	got := set.For("x" + coord.PHPExt)
	assert.Equal(t, coord.PHPScheme, got.Scheme)
	assert.Equal(t, coord.ComposerManager, got.Manager)
	assert.Equal(t, "codiq/greeter", got.Name)
	assert.Equal(t, "1.0.0", got.Version)
	assert.Equal(t, root, got.Root)
}

// TestResolveGivesPHPItsOwnCoordinateBesideNPM is the M6 defect stated for the
// pair most likely to reproduce it. A `composer.json` and a `package.json` share
// a directory in essentially every PHP web application, and both are fixed-name
// JSON manifests carrying `name` and `version` — so a resolver that returned one
// coordinate per repository would stamp a PHP class and a TypeScript class in
// same-named directories with the same four leading words and let the link pass
// join them.
func TestResolveGivesPHPItsOwnCoordinateBesideNPM(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.ComposerFile),
		[]byte(`{"name": "codiq/mixed", "version": "8.0.0"}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.PackageJSONFile),
		[]byte(`{"name": "codiq/mixed", "version": "8.0.0"}`), 0o600))

	set, err := coord.Resolve(dir)
	require.NoError(t, err)

	assert.Equal(t, "scip-php composer codiq/mixed 8.0.0", set.For("Greeter.php").Prefix())
	assert.Equal(t, "scip-typescript npm codiq/mixed 8.0.0", set.For("greeter.ts").Prefix())
	assert.NotEqual(t, set.For("Greeter.php").Prefix(), set.For("greeter.ts").Prefix(),
		"a PHP class and a TypeScript class in one repository must not render the same descriptor")

	assert.Equal(t, dir, set.For("Greeter.php").Root)
	assert.Equal(t, dir, set.For("greeter.ts").Root)
}

// TestPHPFilesGetAPHPCoordinateWithNoComposerJSON is the Gradle case
// (coord/maven.go) and the no-Directory.Build.props case (coord/nuget.go) one
// ecosystem on, and PHP has its own version of it: a legacy tree with no
// Composer at all, or a repository whose composer.json sits in a subdirectory.
// Its `.php` files still index, still carry a PHP scheme and manager that
// separate them from every other language, and are still namespaced against the
// repository root — only the two components nothing in the tree states are
// Unknown.
func TestPHPFilesGetAPHPCoordinateWithNoComposerJSON(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.GoModFile),
		[]byte("module github.com/foo/bar\n"), 0o600))

	set, err := coord.Resolve(dir)
	require.NoError(t, err)

	php := set.For("Greeter.php")
	assert.Equal(t, coord.PHPScheme, php.Scheme)
	assert.Equal(t, coord.ComposerManager, php.Manager)
	assert.Equal(t, coord.Unknown, php.Name)
	assert.Equal(t, coord.Unknown, php.Version)
	assert.Equal(t, dir, php.Root, "namespacing still separates one directory from another")
	assert.NotEqual(t, set.For("main.go").Prefix(), php.Prefix())
}
