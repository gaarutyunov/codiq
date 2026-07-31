package coord_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
)

// writeBundle writes the two files a Ruby coordinate is read from: the Gemfile,
// which is the registered manifest and marks the tree, and the lock beside it,
// which is where the identity actually lives. An empty lock body writes no lock
// at all, which is the ordinary state of a library's checkout.
func writeBundle(t *testing.T, gemfile, lock string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.GemfileName), []byte(gemfile), 0o600))
	if lock != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, coord.LockName), []byte(lock), 0o600))
	}
	return dir
}

const realisticLock = `PATH
  remote: .
  specs:
    codiq-greeter (1.0.0)
      rake (~> 13.0)

GEM
  remote: https://rubygems.org/
  specs:
    rake (13.2.1)
    rspec (3.13.0)
      rspec-core (~> 3.13.0)

PLATFORMS
  ruby

DEPENDENCIES
  codiq-greeter!
  rspec (~> 3.13)

BUNDLED WITH
   2.5.9
`

func TestFromGemfile(t *testing.T) {
	tests := []struct {
		name string
		lock string
		want coord.Coord
	}{
		{
			name: "the PATH section names the gem this tree is",
			lock: realisticLock,
			want: coord.Coord{Name: "codiq-greeter", Version: "1.0.0"},
		},
		{
			// The minimum a lock can be and still state an identity.
			name: "a bare PATH section",
			lock: "PATH\n  remote: .\n  specs:\n    greeter (0.1.0)\n",
			want: coord.Coord{Name: "greeter", Version: "0.1.0"},
		},
		{
			// An application is not a package. Its lock has no PATH section at
			// all, and inventing a name and a version for it would render a
			// coordinate the link pass joins on — so every unversioned Rails app
			// in one index would collide with every other. Unknown does not join.
			name: "an application's lock states no identity",
			lock: "GEM\n  remote: https://rubygems.org/\n  specs:\n    rails (7.1.3)\n\nPLATFORMS\n  ruby\n\nDEPENDENCIES\n  rails\n",
			want: coord.Coord{Name: coord.Unknown, Version: coord.Unknown},
		},
		{
			// A PATH whose remote is some other checkout describes a gem this
			// repository *depends on*, not the one it is.
			name: "a PATH pointing at another checkout is not this tree",
			lock: "PATH\n  remote: ../sibling\n  specs:\n    sibling (2.0.0)\n\nGEM\n  remote: https://rubygems.org/\n  specs:\n",
			want: coord.Coord{Name: coord.Unknown, Version: coord.Unknown},
		},
		{
			// Both kinds present: the one that is this tree wins wherever it sits.
			name: "the PATH for this tree is found past another one",
			lock: "PATH\n  remote: ../sibling\n  specs:\n    sibling (2.0.0)\n\nPATH\n  remote: .\n  specs:\n    codiq-greeter (1.0.0)\n",
			want: coord.Coord{Name: "codiq-greeter", Version: "1.0.0"},
		},
		{
			// A library conventionally gitignores its lock, so this is the common
			// case for exactly the repositories that *are* gems.
			name: "no lock at all",
			lock: "",
			want: coord.Coord{Name: coord.Unknown, Version: coord.Unknown},
		},
		{
			name: "a lock that is not a lock",
			lock: "this is not a Gemfile.lock\n",
			want: coord.Coord{Name: coord.Unknown, Version: coord.Unknown},
		},
		{
			// The dependency lines under a spec are indented one level further,
			// which is the whole of what separates them from the spec itself.
			name: "a dependency of the gem is not the gem",
			lock: "PATH\n  remote: .\n  specs:\n    codiq-greeter (1.0.0)\n      zeitwerk (~> 2.6)\n      rake (~> 13.0)\n",
			want: coord.Coord{Name: "codiq-greeter", Version: "1.0.0"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeBundle(t, "source \"https://rubygems.org\"\n\ngemspec\n", tc.lock)
			got, err := coord.FromGemfile(dir)
			require.NoError(t, err)

			want := tc.want
			want.Scheme, want.Manager, want.Root = coord.RubyScheme, coord.GemManager, dir
			assert.Equal(t, want, got)
		})
	}
}

// TestFromGemfileMissingFile is the one thing that *is* an error here, and it is
// the line every other resolver draws: Resolve stat'd the Gemfile, so failing to
// open it means the tree is broken rather than that the manifest says nothing.
func TestFromGemfileMissingFile(t *testing.T) {
	_, err := coord.FromGemfile(t.TempDir())
	require.ErrorIs(t, err, os.ErrNotExist)
}

// TestGemPrefixIsDistinguishableFromTheOthers is the whole of what makes a
// seventh language safe in one graph: the link pass joins on the rendered
// descriptor and nothing else, so the only thing keeping a Ruby symbol from
// matching a Go, TypeScript, Python, Rust, Java or C# one is that their prefixes
// cannot collide.
func TestGemPrefixIsDistinguishableFromTheOthers(t *testing.T) {
	name, version := "greeter", "1.0.0"
	gem := coord.Coord{Scheme: coord.RubyScheme, Manager: coord.GemManager, Name: name, Version: version}
	others := map[string]coord.Coord{
		"csharp":     {Scheme: coord.CSharpScheme, Manager: coord.NuGetManager, Name: name, Version: version},
		"java":       {Scheme: coord.JavaScheme, Manager: coord.MavenManager, Name: name, Version: version},
		"rust":       {Scheme: coord.RustScheme, Manager: coord.CargoManager, Name: name, Version: version},
		"python":     {Scheme: coord.PyScheme, Manager: coord.PipManager, Name: name, Version: version},
		"typescript": {Scheme: coord.TSScheme, Manager: coord.NPMManager, Name: name, Version: version},
		"go":         {Scheme: coord.GoScheme, Manager: coord.GoManager, Name: name, Version: version},
	}
	for lang, other := range others {
		assert.NotEqual(t, other.Prefix(), gem.Prefix(),
			"a Ruby coordinate must not render the %s one's prefix", lang)
	}
}

// TestGemIsRegistered is the registration half: the resolver has to be in the
// registry for a repository holding a Gemfile to resolve at all, and `.rb` has to
// be owned by it for a Ruby file to be stamped with a RubyGems coordinate rather
// than with whatever other manifest the repository also holds.
func TestGemIsRegistered(t *testing.T) {
	assert.Contains(t, coord.Manifests(), coord.GemfileName)
	assert.Contains(t, coord.Extensions(), coord.RubyExt)
}

// TestResolveStampsRubyFilesWithTheGemCoordinate is that claim over a real mixed
// tree: a repository holding a go.mod beside a Gemfile gives its `.rb` files the
// gem's coordinate and its `.go` files the module's, and the two prefixes differ.
func TestResolveStampsRubyFilesWithTheGemCoordinate(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.GoModFile),
		[]byte("module github.com/foo/bar\n\ngo 1.25.0\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.GemfileName),
		[]byte("source \"https://rubygems.org\"\n\ngemspec\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.LockName),
		[]byte("PATH\n  remote: .\n  specs:\n    codiq-corpus (0.3.0)\n"), 0o600))

	set, err := coord.Resolve(dir)
	require.NoError(t, err)

	ruby := set.For("lib/greeter/greeter" + coord.RubyExt)
	assert.Equal(t, coord.RubyScheme, ruby.Scheme)
	assert.Equal(t, "codiq-corpus", ruby.Name)
	assert.Equal(t, "0.3.0", ruby.Version)
	assert.Equal(t, dir, ruby.Root)
	assert.NotEqual(t, set.For("main"+coord.GoExt).Prefix(), ruby.Prefix())
}

// TestResolveNeedsAGemfileForARubyTree is the caveat this resolver inherits from
// coord/nuget.go, stated so that it is a decision rather than a surprise:
// Resolve returns ErrNoManifest when *no* registered manifest is found, so a Ruby
// tree carrying only a `.gemspec` — glob-named, and therefore unusable as a
// manifest — fails to index rather than indexing with an unknown coordinate.
//
// Ruby hits that strictly less often than C# does, and the reason is worth
// stating: `Directory.Build.props` is optional and rare, while Bundler *requires*
// a Gemfile, so an application, a gem under development and any script directory
// with a single dependency all have one. What is left is a gem that ships a
// gemspec and no Gemfile, which is a real shape and is the one this cannot read.
func TestResolveNeedsAGemfileForARubyTree(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "greeter.gemspec"),
		[]byte("Gem::Specification.new do |s|\n  s.name = \"greeter\"\nend\n"), 0o600))

	_, err := coord.Resolve(dir)
	require.ErrorIs(t, err, coord.ErrNoManifest)
}
