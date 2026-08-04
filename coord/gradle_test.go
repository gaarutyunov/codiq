package coord_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
)

func writeSettings(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.SettingsFile), []byte(body), 0o600))
	return dir
}

func TestFromGradleSettings(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantName string
	}{
		{
			name:     "the assignment Gradle requires",
			body:     "rootProject.name = \"greeter\"\n",
			wantName: "greeter",
		},
		{
			name:     "spacing is not part of the syntax",
			body:     "rootProject.name=\"greeter\"\n",
			wantName: "greeter",
		},
		{
			name:     "an include block below it changes nothing",
			body:     "rootProject.name = \"greeter\"\n\ninclude(\":app\")\ninclude(\":lib\")\n",
			wantName: "greeter",
		},
		{
			name:     "a plugin management block above it changes nothing",
			body:     "pluginManagement {\n    repositories { gradlePluginPortal() }\n}\n\nrootProject.name = \"greeter\"\n",
			wantName: "greeter",
		},
		{
			name:     "a commented-out assignment assigns nothing",
			body:     "// rootProject.name = \"wrong\"\nrootProject.name = \"greeter\"\n",
			wantName: "greeter",
		},
		{
			// A settings file that never names the project is legal and common
			// enough; Gradle falls back to the directory name, which is a fact
			// about the filesystem rather than about this file (SPEC.md §2.5).
			name:     "no assignment at all is Unknown, never the directory",
			body:     "include(\":app\")\n",
			wantName: coord.Unknown,
		},
		{
			// A template names a value defined elsewhere, so the text is a
			// pointer rather than the thing pointed at.
			name:     "a template expression states nothing here",
			body:     "rootProject.name = \"${prefix}-greeter\"\n",
			wantName: coord.Unknown,
		},
		{
			// `'greeter'` is a Char literal in Kotlin and does not compile; a
			// Groovy settings file is a different manifest this does not read.
			name:     "single quotes are not Kotlin's string syntax",
			body:     "rootProject.name = 'greeter'\n",
			wantName: coord.Unknown,
		},
		{
			name:     "a computed name is not a name",
			body:     "rootProject.name = providers.gradleProperty(\"artifact\").get()\n",
			wantName: coord.Unknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := coord.FromGradleSettings(writeSettings(t, tt.body))
			require.NoError(t, err)

			assert.Equal(t, coord.KotlinScheme, got.Scheme)
			assert.Equal(t, coord.GradleManager, got.Manager)
			assert.Equal(t, tt.wantName, got.Name)
			// Always: a settings file states no version, and §4.3 reserves "."
			// for exactly that rather than for a default anyone could invent.
			assert.Equal(t, coord.Unknown, got.Version)
		})
	}
}

func TestFromGradleSettingsNeedsAReadableFile(t *testing.T) {
	_, err := coord.FromGradleSettings(t.TempDir())
	assert.Error(t, err, "Resolve stat'd the file, so failing to open it is a broken tree")
}

// TestKotlinOwnsTwoExtensions is the registration half. `.kt` and `.kts` are one
// language and one parser; what separates a script is where its declarations hang
// (extract/kotlin), which is not a fact the registry has an opinion about.
func TestKotlinOwnsTwoExtensions(t *testing.T) {
	assert.Equal(t, []string{".kt", ".kts"}, coord.KotlinExts)
	assert.Contains(t, coord.Manifests(), coord.SettingsFile)
	for _, ext := range coord.KotlinExts {
		assert.Contains(t, coord.Extensions(), ext)
	}
}

// TestKotlinPrefixIsDistinguishableFromJava is the one that matters most here,
// and the reason coord/gradle.go declines `scip-jvm`. Kotlin and Java compile to
// one bytecode and share one classpath, so if they shared a prefix the link pass
// — which joins on the descriptor string and nothing else (§7) — would resolve
// every same-named symbol in one language into the other's.
func TestKotlinPrefixIsDistinguishableFromJava(t *testing.T) {
	name, version := "greeter", "1.0.0"
	kt := coord.Coord{Scheme: coord.KotlinScheme, Manager: coord.GradleManager, Name: name, Version: version}
	others := map[string]coord.Coord{
		"java": {Scheme: coord.JavaScheme, Manager: coord.MavenManager, Name: name, Version: version},
		"cc":   {Scheme: coord.CCScheme, Manager: coord.CMakeManager, Name: name, Version: version},
		"go":   {Scheme: coord.GoScheme, Manager: coord.GoManager, Name: name, Version: version},
	}
	for lang, other := range others {
		assert.NotEqual(t, other.Prefix(), kt.Prefix(),
			"a Kotlin coordinate must not render the %s one's prefix", lang)
	}
}

// TestResolveStampsKotlinFilesWithTheGradleCoordinate is the M6 defect's test in
// a tenth language: a repository holding a go.mod beside a settings.gradle.kts
// gives its `.kt` files the Gradle build's coordinate and its `.go` files the
// module's, and the two prefixes differ.
func TestResolveStampsKotlinFilesWithTheGradleCoordinate(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.GoModFile),
		[]byte("module github.com/foo/bar\n\ngo 1.25.0\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.SettingsFile),
		[]byte("rootProject.name = \"corpus\"\n"), 0o600))

	set, err := coord.Resolve(dir)
	require.NoError(t, err)

	kt := set.For("src/main/kotlin/App.kt")
	assert.Equal(t, coord.KotlinScheme, kt.Scheme)
	assert.Equal(t, "corpus", kt.Name)
	assert.Equal(t, dir, kt.Root)
	assert.Equal(t, kt, set.For("build.gradle.kts"), "a script belongs to the same ecosystem as a source")
	assert.NotEqual(t, set.For("main"+coord.GoExt).Prefix(), kt.Prefix())
}

// TestResolveStampsMavenBuiltKotlinWithoutAManifest is the decision this resolver
// made, stated as a test: only the Kotlin-DSL settings file is read, so a Kotlin
// project built with Maven — or with the Groovy DSL — gets the Kotlin scheme and
// manager with `.` for the name and the version.
//
// That is the whole of what "settings.gradle.kts only" costs. The files still
// index, their descriptors still separate correctly from every other language's,
// and the one thing missing is the one thing no file this reads states.
func TestResolveStampsMavenBuiltKotlinWithoutAManifest(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, coord.POMFile),
		[]byte(`<project><groupId>com.example</groupId><artifactId>corpus</artifactId><version>2.0.0</version></project>`), 0o600))

	set, err := coord.Resolve(dir)
	require.NoError(t, err)

	kt := set.For("src/main/kotlin/App.kt")
	assert.Equal(t, coord.KotlinScheme, kt.Scheme)
	assert.Equal(t, coord.GradleManager, kt.Manager)
	assert.Equal(t, coord.Unknown, kt.Name)
	assert.Equal(t, coord.Unknown, kt.Version)
	assert.Equal(t, dir, kt.Root, "namespaces still separate one directory from another")
	assert.NotEqual(t, set.For("src/main/java/App"+coord.JavaExt).Prefix(), kt.Prefix())
}

// TestTheKotlinCoordinateResolvesFromTheFixture is the end-to-end half: the
// fixture extract/kotlin's own tests parse resolves through this resolver.
func TestTheKotlinCoordinateResolvesFromTheFixture(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "extract", "kotlin", "testdata", "greeter"))
	require.NoError(t, err)

	set, err := coord.Resolve(root)
	require.NoError(t, err)

	kt := set.For("src/main/kotlin/greeter/Greeter.kt")
	assert.Equal(t, "scip-kotlin gradle greeter .", kt.Prefix())
	assert.Equal(t, root, kt.Root)
}
