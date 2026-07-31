package coord

import (
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CSharpScheme and NuGetManager are the SCIP scheme/manager pair for C#
// artifacts (SPEC.md §4.3), the sixth of the pairs beside GoScheme/GoManager,
// TSScheme/NPMManager, PyScheme/PipManager, RustScheme/CargoManager and
// JavaScheme/MavenManager.
//
// The scheme is named for the *language*, as the five before it are, and not
// for the runtime: `scip-dotnet` is what Sourcegraph's own indexer is called,
// but .NET is also F# and Visual Basic and this stanza reads neither. The
// manager names the ecosystem's package manager, which is NuGet.
//
// Only one property of the pair is load-bearing — no coordinate of one ecosystem
// may render a prefix another can render, since the link pass joins on the
// descriptor string and nothing else (§7). Six distinct schemes give that for
// free, and the manager is then documentation.
const (
	CSharpScheme = "scip-csharp"
	NuGetManager = "nuget"
)

// PropsFile is the manifest FromProps reads: MSBuild's repository-wide property
// sheet, imported automatically by every project at or below the directory that
// holds it.
//
// It is the only fixed-name file in the .NET tree that states an artifact's
// identity, and that is the whole of why it is the manifest. `Ecosystem.Manifest`
// is a filename that `Resolve` stats, and every other candidate is either named
// after the project it describes — `Greeter.csproj`, `Greeter.sln`, one per
// project and none at a name known in advance — or states no identity at all:
// `global.json` pins an SDK version, `nuget.config` and `Directory.Packages.props`
// describe where dependencies come from, and none of the three says what *this*
// tree is.
//
// Reading `*.csproj` was considered and is not done, on two counts.
//
// The first is structural and is not this resolver's to fix: a glob is not a
// filename, so a `.csproj` cannot be a registered manifest without changing how
// `Resolve` finds one — and a repository holds one `.csproj` per project, so
// even then something would have to choose which project's identity is the
// repository's, which nothing in the tree states.
//
// The second is that the interesting half of a `.csproj` is usually not written
// down. MSBuild defaults `AssemblyName` to the project *file's* name and
// `PackageId` to `AssemblyName`, and `Version` to `1.0.0` — so a project that
// states neither is not stating `Greeter` and `1.0.0`, it is stating nothing,
// and the tool that fills them in is the one this deliberately is not. Deriving
// the name from the filename would be right exactly when the file does not
// override it, and inventing `1.0.0` would be worse than that: a version that
// renders is a version the link pass joins on, so every unversioned C#
// repository in an index would render the same coordinate and match every other
// one. Unknown does not join (§4.3).
//
// A repository with no Directory.Build.props is therefore not an error and not a
// special case — it is the Gradle case from coord/maven.go, one ecosystem on.
// It has no registered manifest of its own, so `Resolve` hands its `.cs` files
// Ecosystem.unknown: the right scheme and manager, `.` for the name and the
// version, and the repository root for namespacing. Its files index, its symbols
// carry C# descriptors that separate correctly from every other language, and
// the only thing missing is the one thing nothing in the tree states.
const PropsFile = "Directory.Build.props"

// CSharpExt is the file extension the .NET ecosystem owns here. It repeats
// extract/cs.Ext, for the reason coord.GoExt gives.
//
// `.cs` and not also `.csx` or `.razor`: a C# script and a Razor component are
// read by different grammars, and an extension this owns but no parser reads is
// a registration nothing consults — which is exactly what
// TestExtensionsMatchTheParserRegistry exists to catch.
const CSharpExt = ".cs"

func init() {
	Register(Ecosystem{
		Manifest: PropsFile,
		Scheme:   CSharpScheme,
		Manager:  NuGetManager,
		Exts:     []string{CSharpExt},
		From:     FromProps,
	})
}

// props is the fragment of a Directory.Build.props a coordinate is made of: the
// `<PropertyGroup>` elements, and from them the four properties that name a NuGet
// package and its version.
//
// encoding/xml matches a bare tag against *immediate* children only, which is
// what makes the struct safe over a real property sheet, and it is the property
// coord/maven.go leans on for the same reason one level down. `<PackageId>` is
// meaningful as a child of `<PropertyGroup>` and nowhere else; an `<ItemGroup>`
// full of `<PackageReference>` elements names other people's packages, and a
// `<Target>` may hold a `<PropertyGroup>` of its own that runs at build time and
// is not identity. Matching the path `Project > PropertyGroup > PackageId` and
// nothing deeper is what keeps those out.
//
// Parsed rather than scanned, which is where C# joins Java and parts company
// with Python and Rust. Those two hand-scanned their manifests to keep a TOML
// parser out of go.mod (coord/cargo.go says so); Go's standard library already
// ships an XML parser, so the dependency surface stays exactly where it was.
type props struct {
	Groups []propertyGroup `xml:"PropertyGroup"`
}

// propertyGroup is one `<PropertyGroup>`'s identity properties.
//
// The precedence between them is NuGet's own, not an invention: `PackageId`
// defaults to `AssemblyName`, and `Version` is the assembled version that
// `VersionPrefix` contributes to. Reading both members of each pair is what lets
// a property sheet that states only the more general one still name something.
type propertyGroup struct {
	PackageID     string `xml:"PackageId"`
	AssemblyName  string `xml:"AssemblyName"`
	Version       string `xml:"Version"`
	VersionPrefix string `xml:"VersionPrefix"`
}

// FromProps reads dir/Directory.Build.props and returns the C# artifact's
// coordinate.
//
// A property sheet may hold any number of `<PropertyGroup>` elements, most of
// them conditioned on a configuration or a target framework, so the value taken
// is the first one *stated* in document order — which is MSBuild's own last-write
// -wins evaluated the other way round, and the difference only shows up in a file
// that sets the same property twice unconditionally, which is a file that does
// not mean anything definite anyway.
//
// Two things reduce to Unknown rather than to an error, both for the reason
// FromPOM gives. A value carrying a `$(…)` MSBuild property reference states its
// identity somewhere this cannot see — another property, an imported sheet, or
// the command line — so it is a pointer and not a value. And a property sheet
// that does not parse yields Unknown too: refusing would fail an entire run over
// a manifest defect, leaving a whole C# tree unindexable, when the files below it
// are perfectly readable and Unknown can never false-match (§4.3). Only an
// unreadable *file* is an error, which is the same line every other resolver
// draws.
func FromProps(dir string) (Coord, error) {
	path := filepath.Join(dir, PropsFile)
	f, err := os.Open(path) //nolint:gosec // dir is the repository root the batch resolved.
	if err != nil {
		return Coord{}, err
	}
	defer func() { _ = f.Close() }()

	p := decodeProps(f)

	return Coord{
		Scheme:  CSharpScheme,
		Manager: NuGetManager,
		Name:    or(p.first(func(g propertyGroup) []string { return []string{g.PackageID, g.AssemblyName} }), Unknown),
		Version: or(p.first(func(g propertyGroup) []string { return []string{g.Version, g.VersionPrefix} }), Unknown),
		Root:    dir,
	}, nil
}

// first returns the first value pick reports for any property group, in document
// order, that states something. "" means no group stated any of them.
func (p props) first(pick func(propertyGroup) []string) string {
	for _, g := range p.Groups {
		for _, v := range pick(g) {
			if v := msbuildValue(v); v != "" {
				return v
			}
		}
	}
	return ""
}

// decodeProps reads the property groups out of a property sheet, or returns the
// zero value when the document cannot be parsed. See FromProps for why that is
// not an error.
func decodeProps(r io.Reader) props {
	var p props
	if err := xml.NewDecoder(r).Decode(&p); err != nil && !errors.Is(err, io.EOF) {
		return props{}
	}
	return p
}

// msbuildValue reduces an MSBuild property to what it actually states. A `$(…)`
// property reference names a value defined elsewhere, so the text is a pointer
// rather than the thing pointed at, and the honest reading of it is "not stated
// here" — which is `stated`'s reading of Maven's `${…}`, in MSBuild's spelling.
func msbuildValue(v string) string {
	v = strings.TrimSpace(v)
	if strings.Contains(v, "$(") {
		return ""
	}
	return v
}
