package coord_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
)

func writeCMake(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.CMakeFile), []byte(body), 0o600))
	return dir
}

func TestFromCMake(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantName    string
		wantVersion string
	}{
		{
			name:        "name and version",
			body:        "cmake_minimum_required(VERSION 3.20)\nproject(greeter VERSION 1.2.3 LANGUAGES C CXX)\n",
			wantName:    "greeter",
			wantVersion: "1.2.3",
		},
		{
			name: "no version is Unknown, never invented",
			// A version that renders is a version the link pass joins on, so
			// defaulting one would make every unversioned CMake project in an
			// index render one coordinate and match every other (SPEC.md §4.3).
			body:        "project(greeter LANGUAGES C)\n",
			wantName:    "greeter",
			wantVersion: coord.Unknown,
		},
		{
			name:        "the name is positional and the version keyed",
			body:        "project(my_lib DESCRIPTION \"a thing\" VERSION 0.1.0)\n",
			wantName:    "my_lib",
			wantVersion: "0.1.0",
		},
		{
			name:        "quotes are CMake syntax and not part of the value",
			body:        "project(\"greeter\" VERSION \"1.0.0\")\n",
			wantName:    "greeter",
			wantVersion: "1.0.0",
		},
		{
			name:        "the command name is case insensitive, as CMake's are",
			body:        "PROJECT(greeter VERSION 2.0)\n",
			wantName:    "greeter",
			wantVersion: "2.0",
		},
		{
			name: "a variable reference states its identity elsewhere",
			// coord/nuget.go's msbuildValue rule in CMake's spelling: the text
			// is a pointer rather than the thing pointed at.
			body:        "set(NAME greeter)\nproject(${NAME} VERSION ${VER})\n",
			wantName:    coord.Unknown,
			wantVersion: coord.Unknown,
		},
		{
			name:        "a commented-out project call is not a project call",
			body:        "# project(wrong VERSION 9.9.9)\nproject(greeter VERSION 1.0.0)\n",
			wantName:    "greeter",
			wantVersion: "1.0.0",
		},
		{
			name: "a listfile with no project call states nothing",
			// Not an error. A CMakeLists.txt that only adds subdirectories is
			// ordinary, and Unknown can never false-match.
			body:        "cmake_minimum_required(VERSION 3.20)\nadd_subdirectory(src)\n",
			wantName:    coord.Unknown,
			wantVersion: coord.Unknown,
		},
		{
			name: "a call wrapped over several lines is declined rather than guessed at",
			body: "project(\n  greeter\n  VERSION 1.0.0\n)\n",
			// The scanner reads a command closed on one line and nothing else;
			// a partial CMake interpreter would fail silently instead.
			wantName:    coord.Unknown,
			wantVersion: coord.Unknown,
		},
		{
			name:        "only the first project call wins",
			body:        "project(outer VERSION 1.0.0)\nproject(inner VERSION 2.0.0)\n",
			wantName:    "outer",
			wantVersion: "1.0.0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := coord.FromCMake(writeCMake(t, tt.body))
			require.NoError(t, err)

			assert.Equal(t, coord.CCScheme, got.Scheme)
			assert.Equal(t, coord.CMakeManager, got.Manager)
			assert.Equal(t, tt.wantName, got.Name)
			assert.Equal(t, tt.wantVersion, got.Version)
		})
	}
}

// TestFromCMakeNeedsAReadableFile is the line every resolver since go.mod has
// drawn: Resolve stat'd the manifest, so failing to open it means something is
// wrong with the tree rather than with the manifest's contents.
func TestFromCMakeNeedsAReadableFile(t *testing.T) {
	_, err := coord.FromCMake(t.TempDir())
	require.Error(t, err)
}

// TestCMakeOwnsEightExtensions pins the widest extension claim any ecosystem
// here makes. It is spelled out rather than referenced so that adding a ninth
// is a deliberate edit — and so that the two exclusions with a real case for
// them, `.C` and `.inl`, stay excluded on purpose.
func TestCMakeOwnsEightExtensions(t *testing.T) {
	assert.Equal(t, []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx"}, coord.CCExts)
	assert.NotContains(t, coord.CCExts, ".C", "filepath.Ext is case-sensitive and macOS filesystems are not")
	assert.NotContains(t, coord.CCExts, ".inl", "an inline fragment is not a translation unit")
	assert.NotContains(t, coord.CCExts, ".hs", "Haskell is the walk tripwire's canary")
}

// TestTheCCCoordinateResolvesFromTheFixture is the end-to-end check: the
// extract/cc fixture is a CMake project, and the coordinate it resolves to is
// the one every descriptor in that stanza's tests carries.
func TestTheCCCoordinateResolvesFromTheFixture(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "extract", "cc", "testdata", "greeter"))
	require.NoError(t, err)

	set, err := coord.Resolve(root)
	require.NoError(t, err)

	c := set.For("src/greeter.c")
	assert.Equal(t, coord.CCScheme, c.Scheme)
	assert.Equal(t, coord.CMakeManager, c.Manager)
	assert.Equal(t, "greeter", c.Name)
	assert.Equal(t, "1.0.0", c.Version)
	assert.Equal(t, root, c.Root)

	// Every extension the stanza owns lands on the same coordinate, which is
	// what makes a header and a source name one symbol.
	for _, ext := range coord.CCExts {
		assert.Equal(t, c, set.For("x"+ext), ext)
	}
}

// TestNoManifestAtAllIsStillAFailedRun is the cost of C having no manifest,
// pinned rather than described.
//
// Resolve walks up looking for *any* registered manifest and fails the whole run
// when it finds none. For eight ecosystems that is an edge case; for C it is the
// ordinary state of a Makefile-only, autotools, Meson or Bazel project, and
// nothing about the failure names C as the reason. coord/cmake.go says why the
// fix belongs to Resolve rather than to an additional-language task.
func TestNoManifestAtAllIsStillAFailedRun(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Makefile"), []byte("all:\n\tcc -o hello main.c\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.c"), []byte("int main(void){return 0;}\n"), 0o600))

	_, err := coord.Resolve(dir)
	require.ErrorIs(t, err, coord.ErrNoManifest)
}
