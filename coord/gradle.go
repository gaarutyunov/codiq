package coord

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// KotlinScheme and GradleManager are the SCIP scheme/manager pair for Kotlin
// modules (SPEC.md §4.3), the tenth of the pairs beside GoScheme/GoManager,
// TSScheme/NPMManager, PyScheme/PipManager, RustScheme/CargoManager,
// JavaScheme/MavenManager, CSharpScheme/NuGetManager, RubyScheme/GemManager,
// PHPScheme/ComposerManager and CCScheme/CMakeManager.
//
// **The scheme names Kotlin and not the JVM.** Kotlin and Java compile to one
// bytecode and share one classpath, which is the argument cmake.go used to put C
// and C++ behind a single `scip-cc` — and here it gives the opposite answer,
// because the test cs.go stated is not "do they share a runtime" but "does this
// stanza read them". A `scip-jvm` would claim Java, Kotlin, Scala, Groovy and
// Clojure, and `extract/kotlin` parses exactly one of them. It would also merge
// two coordinate spaces that this indexer resolves from two different manifests,
// so a Kotlin `com/example/Greeter#` and a Java one would render the same prefix
// and the link pass — which joins on the descriptor string and nothing else (§7)
// — would materialize edges between files it has never compared.
//
// That is a real cost and it is worth naming: a Kotlin class calling a Java class
// in the same repository is a call this index will not resolve, because the two
// carry different schemes. It is the honest trade. The alternative is not "the
// call resolves"; it is "every same-named symbol in the two languages resolves
// into every other", which is the phantom-edge defect M6 found, reintroduced
// deliberately.
//
// **The manager is a build system, because Kotlin has no package manager of its
// own.** Kotlin publishes to Maven repositories and consumes them through Gradle
// or Maven; there is no `kotlinpm`. `maven` was the issue's suggestion and is
// declined for a plain reason: the file this resolver actually reads is Gradle's,
// and cmake.go's rule — the manager names the thing that was read — is what keeps
// the coordinate honest documentation of where it came from. Only the
// *uniqueness* of the pair is load-bearing, and `scip-kotlin` already gives that.
const (
	KotlinScheme  = "scip-kotlin"
	GradleManager = "gradle"
)

// SettingsFile is the manifest FromGradleSettings reads.
//
// `Ecosystem.Manifest` does two jobs — it is the filename `Resolve` stats to find
// the repository root, and it is the file `From` reads for an identity — and in
// the Gradle world those two jobs point at different files. `settings.gradle.kts`
// is chosen because it is the only candidate that does both:
//
//   - It **marks the root**, which is its entire purpose: Gradle defines a build
//     as the directory holding the settings file, and every subproject is named
//     from there. `build.gradle.kts` is the other candidate and marks nothing —
//     every subproject has one, so the topmost is found by accident rather than
//     by meaning.
//   - It **states a name**. `rootProject.name = "greeter"` is a literal
//     assignment, the one fact Gradle requires a settings file to state, and it
//     is an artifact name rather than a namespace. `build.gradle.kts` states
//     `group` and `version`, and a group (`com.example`) names an organisation,
//     not a package: two repositories of one organisation would render the same
//     coordinate.
//
// It is Kotlin's own DSL and not Groovy's. `settings.gradle` is the other half of
// the Gradle world and this resolver does not read it, for the reason `Register`
// forces: one manifest per ecosystem, so one had to be chosen, and a Kotlin
// project's settings file is overwhelmingly the Kotlin one. A Groovy-DSL Kotlin
// project is not an error and not a special case — it has no
// `settings.gradle.kts`, so `Resolve` hands its `.kt` files `Ecosystem.unknown`:
// the right scheme and manager, `.` for the name and the version, and the
// repository root for namespacing, which is exactly what §4.3 reserves `.` for.
//
// # What this costs, stated where it is felt
//
// The version is **always** Unknown, because a settings file does not state one.
// Gradle puts `version` in `build.gradle.kts`, a different file, and reading a
// second file would make `Manifests()` — which is what `Resolve` stats to find
// the root — a lie about which file this ecosystem depends on. An unversioned
// coordinate names its package perfectly well and can never false-match (§4.3);
// an invented one would join.
//
// And, as cmake.go recorded for C: a Kotlin repository with neither a
// `settings.gradle.kts` nor any other language's manifest resolves to
// ErrNoManifest and does not index at all. The fix is a last-resort coordinate in
// the shared `Resolve`, which is a change to the core §14 M9+ says an
// additional-language task must not make. It is recorded here so the next reader
// finds it stated rather than discovers it as a bug.
const SettingsFile = "settings.gradle.kts"

// KotlinExts are the file extensions this ecosystem owns.
//
// `.kts` is a Kotlin *script* — a compilation unit with no `package` clause whose
// top-level declarations belong to a synthesized class rather than to a package —
// and it is owned here rather than excluded because it is Kotlin, the same grammar
// reads it, and a Gradle build's `settings.gradle.kts` and `build.gradle.kts` are
// the two files in a Kotlin repository most likely to be read by a human looking
// for how the project is assembled. extract/kotlin descriptors a script's
// declarations under a container named for the file, which is what keeps two
// scripts' identically-named top-level `val`s apart; see its package comment.
//
// `.kt` and `.kts` and nothing else. `.ktm` was Kotlin's module-info extension,
// removed in Kotlin 1.4, and no current toolchain writes one.
//
// These repeat extract/kotlin.Exts, for the reason coord.GoExt gives.
var KotlinExts = []string{".kt", ".kts"}

func init() {
	Register(Ecosystem{
		Manifest: SettingsFile,
		Scheme:   KotlinScheme,
		Manager:  GradleManager,
		Exts:     KotlinExts,
		From:     FromGradleSettings,
	})
}

// FromGradleSettings reads dir/settings.gradle.kts and returns the Gradle build's
// coordinate.
//
// Only an unreadable file is an error, which is the line every resolver since
// go.mod has drawn: `Resolve` stat'd it, so failing to open it means something is
// wrong with the tree rather than with the manifest's contents. A settings file
// that never assigns `rootProject.name`, or assigns it something computed, yields
// Unknown — never an error, and never a guess.
func FromGradleSettings(dir string) (Coord, error) {
	f, err := os.Open(filepath.Join(dir, SettingsFile)) //nolint:gosec // dir is the repository root the batch resolved.
	if err != nil {
		return Coord{}, err
	}
	defer func() { _ = f.Close() }()

	return Coord{
		Scheme:  KotlinScheme,
		Manager: GradleManager,
		Name:    or(scanRootProjectName(f), Unknown),
		// Unknown, always: see SettingsFile.
		Version: Unknown,
		Root:    dir,
	}, nil
}

// scanRootProjectName finds the first `rootProject.name = "…"` assignment in a
// settings file and returns the string it assigns.
//
// Scanned rather than parsed, for the reason cmake.go gives one ecosystem over,
// and with the same deliberate shallowness. A `settings.gradle.kts` is a Kotlin
// program: the assignment may sit inside an `if`, the value may come from a
// `gradle.properties` entry read three lines earlier, and a settings plugin may
// set it from somewhere this file cannot see. Reading further would be a partial
// Kotlin interpreter whose failures were silent. This reads the literal of the
// first assignment it sees at the start of a line and stops.
//
// Line comments are stripped, because `// rootProject.name = "wrong"` assigns
// nothing. A `/* … */` block comment is not handled and is rare enough to leave;
// the worst it can do is make a commented-out assignment win, which yields a name
// the file does contain.
func scanRootProjectName(f *os.File) string {
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		rest, ok := strings.CutPrefix(line, "rootProject.name")
		if !ok {
			continue
		}
		rest, ok = strings.CutPrefix(strings.TrimSpace(rest), "=")
		if !ok {
			continue
		}
		return kotlinStringLiteral(strings.TrimSpace(rest))
	}
	return ""
}

// kotlinStringLiteral reduces the right-hand side of an assignment to what it
// actually states.
//
// Double quotes and nothing else, because that is the whole of Kotlin's string
// syntax: `'greeter'` is a `Char` literal and does not compile, and a bare
// identifier names a value defined elsewhere. A `$` inside the quotes is a
// template expression — the text is a pointer rather than the thing pointed at —
// so the honest reading is "not stated here", which is cmake.go's `${…}` rule in
// Kotlin's spelling.
func kotlinStringLiteral(v string) string {
	if len(v) < 2 || v[0] != '"' {
		return ""
	}
	end := strings.IndexByte(v[1:], '"')
	if end < 0 {
		return ""
	}
	lit := v[1 : 1+end]
	if strings.Contains(lit, "$") || strings.Contains(lit, `\`) {
		return ""
	}
	return lit
}
