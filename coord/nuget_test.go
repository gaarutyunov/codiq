package coord_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
)

func writeProps(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.PropsFile), []byte(body), 0o600))
	return dir
}

func TestFromProps(t *testing.T) {
	tests := []struct {
		name string
		body string
		want coord.Coord
	}{
		{
			name: "the two identity properties",
			body: `<Project><PropertyGroup><PackageId>Codiq.Greeter</PackageId><Version>1.2.3</Version></PropertyGroup></Project>`,
			want: coord.Coord{Name: "Codiq.Greeter", Version: "1.2.3"},
		},
		{
			// The shape a real property sheet has: several groups, most of them
			// conditioned, and the identity properties among a dozen others.
			name: "a real property sheet with conditions and other properties",
			body: `<?xml version="1.0" encoding="utf-8"?>
<Project>
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
    <Nullable>enable</Nullable>
    <LangVersion>latest</LangVersion>
  </PropertyGroup>
  <PropertyGroup Condition="'$(Configuration)' == 'Release'">
    <Optimize>true</Optimize>
  </PropertyGroup>
  <PropertyGroup>
    <PackageId>Codiq.Greeter</PackageId>
    <Version>1.0.0</Version>
    <Authors>codiq</Authors>
  </PropertyGroup>
</Project>`,
			want: coord.Coord{Name: "Codiq.Greeter", Version: "1.0.0"},
		},
		{
			// The reason the struct matches the path `Project > PropertyGroup >
			// PackageId` and nothing deeper. A sheet naming its dependencies
			// carries `PackageReference` elements whose own children a scanner
			// looking for a *tag* would read as the project's identity.
			name: "a dependency's identity is not the project's",
			body: `<Project>
  <PropertyGroup>
    <PackageId>Codiq.Greeter</PackageId>
    <Version>1.0.0</Version>
  </PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Serilog">
      <PackageId>Serilog</PackageId>
      <Version>3.1.1</Version>
    </PackageReference>
  </ItemGroup>
  <Target Name="Stamp">
    <PropertyGroup>
      <Version>9.9.9</Version>
    </PropertyGroup>
  </Target>
</Project>`,
			want: coord.Coord{Name: "Codiq.Greeter", Version: "1.0.0"},
		},
		{
			// NuGet's own precedence: PackageId defaults to AssemblyName, so a
			// sheet stating only the more general one still names something.
			name: "AssemblyName is the name when PackageId is absent",
			body: `<Project><PropertyGroup><AssemblyName>Codiq.Greeter</AssemblyName><Version>1.0.0</Version></PropertyGroup></Project>`,
			want: coord.Coord{Name: "Codiq.Greeter", Version: "1.0.0"},
		},
		{
			name: "PackageId wins over AssemblyName",
			body: `<Project><PropertyGroup><AssemblyName>Internal.Name</AssemblyName><PackageId>Codiq.Greeter</PackageId><Version>1.0.0</Version></PropertyGroup></Project>`,
			want: coord.Coord{Name: "Codiq.Greeter", Version: "1.0.0"},
		},
		{
			name: "VersionPrefix is the version when Version is absent",
			body: `<Project><PropertyGroup><PackageId>Codiq.Greeter</PackageId><VersionPrefix>2.0.0</VersionPrefix></PropertyGroup></Project>`,
			want: coord.Coord{Name: "Codiq.Greeter", Version: "2.0.0"},
		},
		{
			// `$(…)` names a value defined somewhere this cannot see — another
			// property, an imported sheet, or the command line — so it is a
			// pointer and not a value. Unknown is the honest reading, and it can
			// never false-match (§4.3).
			name: "an MSBuild property reference states nothing here",
			body: `<Project><PropertyGroup><PackageId>Codiq.Greeter</PackageId><Version>$(BuildVersion)</Version></PropertyGroup></Project>`,
			want: coord.Coord{Name: "Codiq.Greeter", Version: coord.Unknown},
		},
		{
			// A sheet that says nothing about identity is the common case, and it
			// is not an error: the version is what MSBuild would default to
			// `1.0.0` and inventing that would render a coordinate the link pass
			// joins on.
			name: "a sheet with no identity properties states nothing",
			body: `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`,
			want: coord.Coord{Name: coord.Unknown, Version: coord.Unknown},
		},
		{
			name: "an empty manifest",
			body: "",
			want: coord.Coord{Name: coord.Unknown, Version: coord.Unknown},
		},
		{
			// A sheet that does not parse is Unknown rather than an error, for
			// the reason FromProps gives: the C# files below it are perfectly
			// readable, and failing the run would leave a whole tree unindexable
			// over a manifest defect.
			name: "a malformed sheet is unknown rather than fatal",
			body: `<Project><PropertyGroup><PackageId>Codiq.Greeter`,
			want: coord.Coord{Name: coord.Unknown, Version: coord.Unknown},
		},
		{
			name: "whitespace around a value is not part of it",
			body: "<Project>\n  <PropertyGroup>\n    <PackageId>\n      Codiq.Greeter\n    </PackageId>\n    <Version>1.0.0</Version>\n  </PropertyGroup>\n</Project>",
			want: coord.Coord{Name: "Codiq.Greeter", Version: "1.0.0"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeProps(t, tc.body)
			got, err := coord.FromProps(dir)
			require.NoError(t, err)

			want := tc.want
			want.Scheme, want.Manager, want.Root = coord.CSharpScheme, coord.NuGetManager, dir
			assert.Equal(t, want, got)
		})
	}
}

func TestFromPropsMissingFile(t *testing.T) {
	_, err := coord.FromProps(t.TempDir())
	require.ErrorIs(t, err, os.ErrNotExist)
}

// TestNuGetPrefixIsDistinguishableFromTheOthers is the whole of what makes a
// sixth language safe in one graph: the link pass joins on the rendered
// descriptor and nothing else, so the only thing keeping a C# symbol from
// matching a Go, TypeScript, Python, Rust or Java one is that their prefixes
// cannot collide — and here the *suffixes* genuinely do, because a C# namespace,
// a Java package, a Rust module, a TypeScript module and a Python module can all
// render `greeter/`.
func TestNuGetPrefixIsDistinguishableFromTheOthers(t *testing.T) {
	name, version := "greeter", "1.0.0"
	nuget := coord.Coord{Scheme: coord.CSharpScheme, Manager: coord.NuGetManager, Name: name, Version: version}
	others := map[string]coord.Coord{
		"java":       {Scheme: coord.JavaScheme, Manager: coord.MavenManager, Name: name, Version: version},
		"rust":       {Scheme: coord.RustScheme, Manager: coord.CargoManager, Name: name, Version: version},
		"python":     {Scheme: coord.PyScheme, Manager: coord.PipManager, Name: name, Version: version},
		"typescript": {Scheme: coord.TSScheme, Manager: coord.NPMManager, Name: name, Version: version},
		"go":         {Scheme: coord.GoScheme, Manager: coord.GoManager, Name: name, Version: version},
	}
	for lang, other := range others {
		assert.NotEqual(t, other.Prefix(), nuget.Prefix(),
			"a C# coordinate must not render the %s one's prefix", lang)
	}
}

// TestNuGetIsRegistered is the registration half: the resolver has to be in the
// registry for a repository holding a Directory.Build.props to resolve at all,
// and `.cs` has to be owned by it for a C# file to be stamped with a NuGet
// coordinate rather than with whatever other manifest the repository also holds.
func TestNuGetIsRegistered(t *testing.T) {
	assert.Contains(t, coord.Manifests(), coord.PropsFile)
	assert.Contains(t, coord.Extensions(), coord.CSharpExt)
}

// TestResolveStampsCSharpFilesWithTheNuGetCoordinate is that claim over a real
// mixed tree: a repository holding a go.mod beside a Directory.Build.props gives
// its `.cs` files the artifact's coordinate and its `.go` files the module's,
// and the two prefixes differ.
func TestResolveStampsCSharpFilesWithTheNuGetCoordinate(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.GoModFile),
		[]byte("module github.com/foo/bar\n\ngo 1.25.0\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.PropsFile),
		[]byte(`<Project><PropertyGroup><PackageId>Codiq.Corpus</PackageId><Version>0.2.0</Version></PropertyGroup></Project>`), 0o600))

	set, err := coord.Resolve(dir)
	require.NoError(t, err)

	csharp := set.For("src/App/Program" + coord.CSharpExt)
	assert.Equal(t, coord.CSharpScheme, csharp.Scheme)
	assert.Equal(t, "Codiq.Corpus", csharp.Name)
	assert.Equal(t, "0.2.0", csharp.Version)
	assert.Equal(t, dir, csharp.Root)
	assert.NotEqual(t, set.For("main"+coord.GoExt).Prefix(), csharp.Prefix())
}

// TestResolveStampsAPropslessProjectWithoutAManifest is the decision this
// resolver made, stated as a test, and it is the Gradle case from
// coord/maven.go one ecosystem on: a `.csproj` is named after its project rather
// than after a name a registry can stat, and it usually states neither an
// identity nor a version anyway — so a repository with no Directory.Build.props
// gets the C# scheme and manager with `.` for the name and the version.
//
// That is the whole of what "Directory.Build.props only" costs. The files still
// index, their descriptors still separate correctly from every other language's,
// and the one thing missing is the one thing no fixed-name file in the tree
// states.
func TestResolveStampsAPropslessProjectWithoutAManifest(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Greeter.csproj"),
		[]byte(`<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.GoModFile),
		[]byte("module github.com/foo/bar\n\ngo 1.25.0\n"), 0o600))

	set, err := coord.Resolve(dir)
	require.NoError(t, err)

	csharp := set.For("src/App/Program" + coord.CSharpExt)
	assert.Equal(t, coord.CSharpScheme, csharp.Scheme)
	assert.Equal(t, coord.NuGetManager, csharp.Manager)
	assert.Equal(t, coord.Unknown, csharp.Name)
	assert.Equal(t, coord.Unknown, csharp.Version)
	assert.Equal(t, dir, csharp.Root)
	assert.NotEqual(t, set.For("main"+coord.GoExt).Prefix(), csharp.Prefix())
}
