package coord

import (
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// JavaScheme and MavenManager are the SCIP scheme/manager pair for Java
// artifacts (SPEC.md §4.3), the fifth of the pairs beside GoScheme/GoManager,
// TSScheme/NPMManager, PyScheme/PipManager and RustScheme/CargoManager.
//
// The manager names the ecosystem's package manager, as `gomod`, `npm`, `pip`
// and `cargo` do: the one this reads a manifest for is Maven, over Maven
// Central. Only one property of the pair is load-bearing — no coordinate of one
// ecosystem may render a prefix another can render, since the link pass joins on
// the descriptor string and nothing else (§7). Five distinct schemes give that
// for free, and the manager is then documentation.
const (
	JavaScheme   = "scip-java"
	MavenManager = "maven"
)

// POMFile is the manifest FromPOM reads.
//
// Maven's, and deliberately only Maven's. Gradle is the other half of the Java
// world and this resolver does not read it: `build.gradle` is a Groovy program
// and `build.gradle.kts` a Kotlin one, and neither states a coordinate — both
// *compute* one, from assignments that may sit behind a function call, a
// convention plugin or an `ext` block in a different file. A line scan over
// either is a guess dressed as a fact, and a wrong package name is worse than no
// package name: it renders a prefix, and a prefix that renders is a prefix the
// link pass will join on.
//
// A Gradle project is therefore not an error and not a special case. It has no
// pom.xml, so Resolve hands its .java files Ecosystem.unknown — the right scheme
// and manager, `.` for the name and the version, and the repository root for
// namespacing — which is exactly the "cannot be determined" §4.3 reserves `.`
// for. Its files index, its symbols carry Java descriptors that separate
// correctly from every other language, and the only thing missing is the one
// thing nothing in the tree states.
const POMFile = "pom.xml"

// JavaExt is the file extension the Java ecosystem owns. It repeats
// extract/java.Ext, for the reason coord.GoExt gives.
const JavaExt = ".java"

func init() {
	Register(Ecosystem{
		Manifest: POMFile,
		Scheme:   JavaScheme,
		Manager:  MavenManager,
		Exts:     []string{JavaExt},
		From:     FromPOM,
	})
}

// pom is the fragment of a Maven POM a coordinate is made of: the project's own
// `groupId`/`artifactId`/`version`, and the `<parent>` coordinate the first and
// third are inherited from when the project omits them.
//
// encoding/xml matches a bare tag against *immediate* children only, which is
// what makes the struct safe over a real POM: a `<dependency>` has a `groupId`
// too, and several hundred of them, but none is a child of `<project>`.
//
// Parsed rather than scanned, which is where Java parts company with Python and
// Rust. Those two hand-scanned their manifests to keep a TOML parser out of
// go.mod (coord/cargo.go says so); Go's standard library already ships an XML
// parser, so the dependency surface stays exactly where it was and the reading
// is a real parse instead of a line-shaped approximation of one.
type pom struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
	Parent     struct {
		GroupID string `xml:"groupId"`
		Version string `xml:"version"`
	} `xml:"parent"`
}

// FromPOM reads dir/pom.xml and returns the Java artifact's coordinate.
//
// Only `<project>`'s own identity is read: `groupId`, `artifactId`, `version`,
// and the `<parent>` element the first and third fall back to. That is where a
// Maven project states what it is, and it is the whole of what a coordinate is
// made of; the rest of a POM is dependencies, plugins and build configuration.
//
// The parent fallback is Maven's own inheritance rule and not a guess: a child
// module that omits `groupId` and `version` inherits exactly those two from
// `<parent>`, and `artifactId` is never inherited — which is why it has no
// fallback here. The fallback is also *file-local*, which is the point: the
// parent's coordinate is written down in the child's own POM, so reading it
// needs no second file and no directory walk (§2.5).
//
// Two things reduce to Unknown rather than to an error, both for the reason
// FromCargo gives for a virtual manifest. A value carrying a `${…}` property
// reference states its identity somewhere this cannot see — a `<properties>`
// block, a profile, the parent POM, or the command line — so it is not a value
// at all. And a pom.xml that does not parse yields Unknown too: refusing would
// fail an entire run over a manifest defect, leaving a whole Java tree
// unindexable, when the files below it are perfectly readable and Unknown can
// never false-match (§4.3). Only an unreadable *file* is an error, which is the
// same line every other resolver draws.
func FromPOM(dir string) (Coord, error) {
	path := filepath.Join(dir, POMFile)
	f, err := os.Open(path) //nolint:gosec // dir is the repository root the batch resolved.
	if err != nil {
		return Coord{}, err
	}
	defer func() { _ = f.Close() }()

	p := decodePOM(f)

	group := stated(or(p.GroupID, p.Parent.GroupID))
	artifact := stated(p.ArtifactID)
	version := stated(or(p.Version, p.Parent.Version))

	return Coord{
		Scheme:  JavaScheme,
		Manager: MavenManager,
		Name:    mavenName(group, artifact),
		Version: or(version, Unknown),
		Root:    dir,
	}, nil
}

// decodePOM reads the identity fields out of a POM, or returns the zero value
// when the document cannot be parsed. See FromPOM for why that is not an error.
func decodePOM(r io.Reader) pom {
	var p pom
	if err := xml.NewDecoder(r).Decode(&p); err != nil && !errors.Is(err, io.EOF) {
		return pom{}
	}
	return p
}

// mavenName renders the package half of the coordinate, which for this
// ecosystem is Maven's own two-part name: `groupId:artifactId`, the string every
// Maven user already writes to name an artifact.
//
// Both halves or neither. A POM missing either one is not a POM Maven itself
// would build — `artifactId` is mandatory and never inherited, `groupId` is
// mandatory and inheritable — so half a name would be a coordinate that renders,
// and a coordinate that renders is one the link pass joins on. Unknown does not
// join.
func mavenName(group, artifact string) string {
	if group == "" || artifact == "" {
		return Unknown
	}
	return group + ":" + artifact
}

// stated reduces a POM value to what it actually states. A `${…}` property
// reference names a value defined elsewhere, so the text is a pointer rather
// than the thing pointed at, and the honest reading of it is "not stated here".
func stated(v string) string {
	v = strings.TrimSpace(v)
	if strings.Contains(v, "${") {
		return ""
	}
	return v
}
