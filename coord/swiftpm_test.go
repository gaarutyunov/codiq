package coord_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
)

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.PackageManifest), []byte(body), 0o600))
	return dir
}

func TestFromSwiftPackage(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantName string
	}{
		{
			name:     "the argument SwiftPM requires",
			body:     "let package = Package(name: \"greeter\")\n",
			wantName: "greeter",
		},
		{
			name:     "spacing is not part of the syntax",
			body:     "let package = Package(name:\"greeter\")\n",
			wantName: "greeter",
		},
		{
			name: "the argument on its own line, which is how a real manifest is written",
			body: "// swift-tools-version:5.9\nimport PackageDescription\n\n" +
				"let package = Package(\n    name: \"greeter\",\n    targets: [.target(name: \"Greeter\")]\n)\n",
			wantName: "greeter",
		},
		{
			// The one that makes the scan correct rather than merely shallow:
			// `name:` is Package's first parameter and Swift requires arguments
			// in declaration order, so a target's name is always written after
			// the package's and can never be reached first.
			name: "a target named before the package is not the package",
			body: "let package = Package(\n    name: \"greeter\",\n" +
				"    products: [.library(name: \"GreeterLib\", targets: [\"Greeter\"])],\n" +
				"    targets: [.target(name: \"Greeter\")]\n)\n",
			wantName: "greeter",
		},
		{
			name:     "a commented-out initialiser names nothing",
			body:     "// let package = Package(name: \"wrong\")\nlet package = Package(name: \"greeter\")\n",
			wantName: "greeter",
		},
		{
			// A manifest with no `Package(` at all does not build; one whose
			// name is computed states nothing this may read. Both are Unknown
			// rather than the directory name, which is a fact about the
			// filesystem and not about this file (SPEC.md §2.5).
			name:     "no initialiser at all is Unknown, never the directory",
			body:     "import PackageDescription\n",
			wantName: coord.Unknown,
		},
		{
			name:     "a computed name is not a name",
			body:     "let package = Package(name: packageName)\n",
			wantName: coord.Unknown,
		},
		{
			// An interpolation names a value defined elsewhere, so the text is a
			// pointer rather than the thing pointed at.
			name:     "an interpolation states nothing here",
			body:     "let package = Package(name: \"\\(prefix)-greeter\")\n",
			wantName: coord.Unknown,
		},
		{
			// Swift has no single-quoted string; `'greeter'` does not compile.
			name:     "single quotes are not Swift's string syntax",
			body:     "let package = Package(name: 'greeter')\n",
			wantName: coord.Unknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := coord.FromSwiftPackage(writeManifest(t, tt.body))
			require.NoError(t, err)

			assert.Equal(t, coord.SwiftScheme, got.Scheme)
			assert.Equal(t, coord.SwiftPMManager, got.Manager)
			assert.Equal(t, tt.wantName, got.Name)
			// Always: a SwiftPM package's version is a git tag, not a manifest
			// field, so there is no file to read one from. §4.3 reserves "." for
			// exactly that rather than for a default anyone could invent.
			assert.Equal(t, coord.Unknown, got.Version)
		})
	}
}

func TestFromSwiftPackageNeedsAReadableFile(t *testing.T) {
	_, err := coord.FromSwiftPackage(t.TempDir())
	assert.Error(t, err, "Resolve stat'd the file, so failing to open it is a broken tree")
}

// TestTheSwiftManifestIsAlsoASwiftSource is the registration fact that no
// earlier ecosystem produced: the manifest this resolver reads carries the
// extension this ecosystem owns, so extract's registry hands `Package.swift` to
// the Swift stanza as a compilation unit. Neither package can prevent that and
// neither should; what keeps it sound is where the stanza puts the manifest's
// declarations (extract/swift).
func TestTheSwiftManifestIsAlsoASwiftSource(t *testing.T) {
	assert.Equal(t, coord.SwiftExt, filepath.Ext(coord.PackageManifest))
	assert.Contains(t, coord.Manifests(), coord.PackageManifest)
	assert.Contains(t, coord.Extensions(), coord.SwiftExt)
}

// TestSwiftPrefixIsDistinguishableFromEveryOtherEcosystem is M6's defect stated
// as an invariant: the link pass joins on the descriptor string and nothing else
// (§7), so two ecosystems that render one prefix resolve every same-named symbol
// in each into the other.
func TestSwiftPrefixIsDistinguishableFromEveryOtherEcosystem(t *testing.T) {
	name, version := "greeter", "1.0.0"
	sw := coord.Coord{Scheme: coord.SwiftScheme, Manager: coord.SwiftPMManager, Name: name, Version: version}
	others := map[string]coord.Coord{
		"go":     {Scheme: coord.GoScheme, Manager: coord.GoManager, Name: name, Version: version},
		"kotlin": {Scheme: coord.KotlinScheme, Manager: coord.GradleManager, Name: name, Version: version},
		"cc":     {Scheme: coord.CCScheme, Manager: coord.CMakeManager, Name: name, Version: version},
		"rust":   {Scheme: coord.RustScheme, Manager: coord.CargoManager, Name: name, Version: version},
	}
	for lang, other := range others {
		assert.NotEqual(t, other.Prefix(), sw.Prefix(),
			"a Swift coordinate must not render the %s one's prefix", lang)
	}
}

// TestResolveStampsSwiftFilesWithTheSwiftPMCoordinate is the M6 defect's test in
// an eleventh language: a repository holding a go.mod beside a Package.swift
// gives its `.swift` files the package's coordinate and its `.go` files the
// module's, and the two prefixes differ.
func TestResolveStampsSwiftFilesWithTheSwiftPMCoordinate(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.GoModFile),
		[]byte("module github.com/foo/bar\n\ngo 1.25.0\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.PackageManifest),
		[]byte("let package = Package(name: \"corpus\")\n"), 0o600))

	set, err := coord.Resolve(dir, "greeter")
	require.NoError(t, err)

	sw := set.For("Sources/Greeter/Greeter" + coord.SwiftExt)
	assert.Equal(t, coord.SwiftScheme, sw.Scheme)
	assert.Equal(t, "corpus", sw.Name)
	assert.Equal(t, dir, sw.Root)
	assert.Equal(t, sw, set.For(coord.PackageManifest),
		"the manifest belongs to the ecosystem it declares")
	assert.NotEqual(t, set.For("main"+coord.GoExt).Prefix(), sw.Prefix())
}

// TestResolveStampsXcodeBuiltSwiftWithoutAManifest is the decision this resolver
// made, stated as a test: only `Package.swift` is read, so a Swift project built
// by Xcode alone — whose `.xcodeproj` is a directory of XML nothing here parses —
// gets the Swift scheme and manager with `.` for the name and the version, so
// long as *some* other ecosystem's manifest marks the root.
//
// That is the whole of what "Package.swift only" costs. The files still index,
// their descriptors still separate correctly from every other language's, and
// the one thing missing is the one thing no file this reads states.
func TestResolveStampsXcodeBuiltSwiftWithoutAManifest(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.GoModFile),
		[]byte("module github.com/foo/bar\n\ngo 1.25.0\n"), 0o600))

	set, err := coord.Resolve(dir, "greeter")
	require.NoError(t, err)

	sw := set.For("Sources/Greeter/Greeter" + coord.SwiftExt)
	assert.Equal(t, coord.SwiftScheme, sw.Scheme)
	assert.Equal(t, coord.SwiftPMManager, sw.Manager)
	assert.Equal(t, "greeter", sw.Name,
		"the corpus names an ecosystem the repository declares no manifest for")
	assert.Equal(t, coord.Unknown, sw.Version)
	assert.Equal(t, dir, sw.Root, "namespaces still separate one module from another")
	assert.NotEqual(t, set.For("main"+coord.GoExt).Prefix(), sw.Prefix())
}

// TestTheSwiftCoordinateResolvesFromTheFixture is the end-to-end half: the
// fixture extract/swift's own tests parse resolves through this resolver.
func TestTheSwiftCoordinateResolvesFromTheFixture(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "extract", "swift", "testdata", "greeter"))
	require.NoError(t, err)

	set, err := coord.Resolve(root, "greeter")
	require.NoError(t, err)

	sw := set.For("Sources/Greeter/Greeter" + coord.SwiftExt)
	assert.Equal(t, "scip-swift swiftpm greeter .", sw.Prefix())
	assert.Equal(t, root, sw.Root)
}
