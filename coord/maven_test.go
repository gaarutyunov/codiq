package coord_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
)

func writePOM(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.POMFile), []byte(body), 0o600))
	return dir
}

func TestFromPOM(t *testing.T) {
	tests := []struct {
		name string
		body string
		want coord.Coord
	}{
		{
			name: "the three identity fields",
			body: `<project><groupId>com.example</groupId><artifactId>greeter</artifactId><version>1.2.3</version></project>`,
			want: coord.Coord{Name: "com.example:greeter", Version: "1.2.3"},
		},
		{
			// The shape every generated POM has: a namespace, an XML
			// declaration, and the identity fields among a dozen others.
			name: "a real POM with its namespace and preamble",
			body: `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0"
         xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>greeter</artifactId>
  <version>1.0.0</version>
  <packaging>jar</packaging>
  <name>Greeter</name>
</project>`,
			want: coord.Coord{Name: "com.example:greeter", Version: "1.0.0"},
		},
		{
			// The reason the struct matches immediate children only. A real POM
			// carries dozens of `groupId` elements and exactly one of them is
			// the project's; a scanner looking for the *tag* would read the
			// first dependency's identity as the project's.
			name: "a dependency's coordinate is not the project's",
			body: `<project>
  <groupId>com.example</groupId>
  <artifactId>greeter</artifactId>
  <version>1.0.0</version>
  <dependencies>
    <dependency>
      <groupId>org.junit.jupiter</groupId>
      <artifactId>junit-jupiter</artifactId>
      <version>5.10.0</version>
    </dependency>
  </dependencies>
  <build>
    <plugins>
      <plugin>
        <groupId>org.apache.maven.plugins</groupId>
        <artifactId>maven-compiler-plugin</artifactId>
        <version>3.13.0</version>
      </plugin>
    </plugins>
  </build>
</project>`,
			want: coord.Coord{Name: "com.example:greeter", Version: "1.0.0"},
		},
		{
			// Maven's own inheritance rule, and the commonest shape in a
			// multi-module build: the module states only what is its own, and
			// `groupId` and `version` come from the parent. Both are written
			// down in this very file, so reading them is still file-local.
			name: "groupId and version are inherited from the parent",
			body: `<project>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>platform</artifactId>
    <version>4.5.6</version>
  </parent>
  <artifactId>greeter</artifactId>
</project>`,
			want: coord.Coord{Name: "com.example:greeter", Version: "4.5.6"},
		},
		{
			// The project's own values win over the parent's, which is also
			// Maven's rule.
			name: "the project overrides the parent",
			body: `<project>
  <parent>
    <groupId>com.other</groupId>
    <artifactId>platform</artifactId>
    <version>9.9.9</version>
  </parent>
  <groupId>com.example</groupId>
  <artifactId>greeter</artifactId>
  <version>1.0.0</version>
</project>`,
			want: coord.Coord{Name: "com.example:greeter", Version: "1.0.0"},
		},
		{
			// `${…}` names a value defined somewhere this cannot see — a
			// `<properties>` block, a profile, the parent, or the command
			// line — so it is a pointer and not a value. Unknown is the honest
			// reading, and it can never false-match (§4.3).
			name: "a property reference states nothing here",
			body: `<project><groupId>com.example</groupId><artifactId>greeter</artifactId><version>${revision}</version></project>`,
			want: coord.Coord{Name: "com.example:greeter", Version: coord.Unknown},
		},
		{
			// Half a coordinate is not a coordinate: it would render a prefix,
			// and a prefix that renders is one the link pass joins on.
			name: "a missing artifactId is not half a name",
			body: `<project><groupId>com.example</groupId><version>1.0.0</version></project>`,
			want: coord.Coord{Name: coord.Unknown, Version: "1.0.0"},
		},
		{
			name: "a missing groupId is not half a name either",
			body: `<project><artifactId>greeter</artifactId><version>1.0.0</version></project>`,
			want: coord.Coord{Name: coord.Unknown, Version: "1.0.0"},
		},
		{
			name: "an empty manifest",
			body: "",
			want: coord.Coord{Name: coord.Unknown, Version: coord.Unknown},
		},
		{
			// A POM that does not parse is Unknown rather than an error, for
			// the reason FromPOM gives: the Java files below it are perfectly
			// readable, and failing the run would leave a whole tree
			// unindexable over a manifest defect.
			name: "a malformed POM is unknown rather than fatal",
			body: `<project><groupId>com.example</groupId><artifactId>greeter`,
			want: coord.Coord{Name: coord.Unknown, Version: coord.Unknown},
		},
		{
			name: "whitespace around a value is not part of it",
			body: "<project>\n  <groupId>\n    com.example\n  </groupId>\n  <artifactId>greeter</artifactId>\n  <version>1.0.0</version>\n</project>",
			want: coord.Coord{Name: "com.example:greeter", Version: "1.0.0"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := writePOM(t, tc.body)
			got, err := coord.FromPOM(dir)
			require.NoError(t, err)

			want := tc.want
			want.Scheme, want.Manager, want.Root = coord.JavaScheme, coord.MavenManager, dir
			assert.Equal(t, want, got)
		})
	}
}

func TestFromPOMMissingFile(t *testing.T) {
	_, err := coord.FromPOM(t.TempDir())
	require.ErrorIs(t, err, os.ErrNotExist)
}

// TestMavenPrefixIsDistinguishableFromTheOthers is the whole of what makes a
// fifth language safe in one graph: the link pass joins on the rendered
// descriptor and nothing else, so the only thing keeping a Java symbol from
// matching a Go, TypeScript, Python or Rust one is that their prefixes cannot
// collide — and here the *suffixes* genuinely do, because a Java package, a Rust
// module, a TypeScript module and a Python module can all render `greeter/`.
func TestMavenPrefixIsDistinguishableFromTheOthers(t *testing.T) {
	name, version := "greeter", "1.0.0"
	maven := coord.Coord{Scheme: coord.JavaScheme, Manager: coord.MavenManager, Name: name, Version: version}
	others := map[string]coord.Coord{
		"rust":       {Scheme: coord.RustScheme, Manager: coord.CargoManager, Name: name, Version: version},
		"python":     {Scheme: coord.PyScheme, Manager: coord.PipManager, Name: name, Version: version},
		"typescript": {Scheme: coord.TSScheme, Manager: coord.NPMManager, Name: name, Version: version},
		"go":         {Scheme: coord.GoScheme, Manager: coord.GoManager, Name: name, Version: version},
	}
	for lang, other := range others {
		assert.NotEqual(t, other.Prefix(), maven.Prefix(),
			"a Java coordinate must not render the %s one's prefix", lang)
	}
}

// TestMavenIsRegistered is the registration half: the resolver has to be in the
// registry for a repository holding a pom.xml to resolve at all, and `.java` has
// to be owned by it for a Java file to be stamped with a Maven coordinate rather
// than with whatever other manifest the repository also holds. That second half
// is the M6 defect, two languages on.
func TestMavenIsRegistered(t *testing.T) {
	assert.Contains(t, coord.Manifests(), coord.POMFile)
	assert.Contains(t, coord.Extensions(), coord.JavaExt)
}

// TestResolveStampsJavaFilesWithTheMavenCoordinate is that claim over a real
// mixed tree: a repository holding a go.mod beside a pom.xml gives its `.java`
// files the artifact's coordinate and its `.go` files the module's, and the two
// prefixes differ.
func TestResolveStampsJavaFilesWithTheMavenCoordinate(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.GoModFile),
		[]byte("module github.com/foo/bar\n\ngo 1.25.0\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.POMFile),
		[]byte(`<project><groupId>com.example</groupId><artifactId>corpus</artifactId><version>0.2.0</version></project>`), 0o600))

	set, err := coord.Resolve(dir)
	require.NoError(t, err)

	java := set.For("src/main/java/App" + coord.JavaExt)
	assert.Equal(t, coord.JavaScheme, java.Scheme)
	assert.Equal(t, "com.example:corpus", java.Name)
	assert.Equal(t, "0.2.0", java.Version)
	assert.Equal(t, dir, java.Root)
	assert.NotEqual(t, set.For("main"+coord.GoExt).Prefix(), java.Prefix())
}

// TestResolveStampsGradleProjectsWithoutAManifest is the decision this resolver
// made, stated as a test: Gradle is not read, so a Gradle project's Java files
// get the Java scheme and manager with `.` for the name and the version.
//
// That is the whole of what "pom.xml only" costs. The files still index, their
// descriptors still separate correctly from every other language's, and the one
// thing missing is the one thing no file in the tree states in a form this could
// honestly read.
func TestResolveStampsGradleProjectsWithoutAManifest(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "build.gradle.kts"),
		[]byte("group = \"com.example\"\nversion = \"1.0.0\"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.GoModFile),
		[]byte("module github.com/foo/bar\n\ngo 1.25.0\n"), 0o600))

	set, err := coord.Resolve(dir)
	require.NoError(t, err)

	java := set.For("src/main/java/App" + coord.JavaExt)
	assert.Equal(t, coord.JavaScheme, java.Scheme)
	assert.Equal(t, coord.MavenManager, java.Manager)
	assert.Equal(t, coord.Unknown, java.Name)
	assert.Equal(t, coord.Unknown, java.Version)
	assert.Equal(t, dir, java.Root)
	assert.NotEqual(t, set.For("main"+coord.GoExt).Prefix(), java.Prefix())
}
