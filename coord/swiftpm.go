package coord

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// SwiftScheme and SwiftPMManager are the SCIP scheme/manager pair for Swift
// packages (SPEC.md §4.3), the eleventh of the pairs beside GoScheme/GoManager,
// TSScheme/NPMManager, PyScheme/PipManager, RustScheme/CargoManager,
// JavaScheme/MavenManager, CSharpScheme/NuGetManager, RubyScheme/GemManager,
// PHPScheme/ComposerManager, CCScheme/CMakeManager and KotlinScheme/GradleManager.
//
// Both halves are the issue's suggestion taken as written, which is the first
// time in four language tasks — C/C++ shipped `scip-cc`/`cmake` over the
// suggested `scip-clang`, Kotlin `gradle` over the suggested `maven` — and it is
// worth saying why the suggestion holds here when it did not there.
//
// The manager names the thing that was read: cmake.go's rule, and gradle.go's
// reason for declining `maven`. For Swift the thing that was read *is* SwiftPM's
// — `Package.swift` is the Swift Package Manager's manifest, there is no second
// candidate the way `settings.gradle.kts` and `pom.xml` are two candidates for a
// JVM project, and Swift genuinely has a package manager of its own where Kotlin
// has only build systems. `swiftpm` is therefore the honest name rather than the
// convenient one.
//
// The scheme names Swift and only Swift. Swift and Objective-C share a runtime
// and interoperate through generated headers, which is close to the argument
// that put C and C++ behind a single `scip-cc` — and it gives the opposite
// answer for cs.go's reason and gradle.go's: the test is not "do they share a
// runtime" but "does this stanza read them", and extract/swift parses exactly
// one of the two. What that costs is stated where it is felt: a Swift class
// calling into an Objective-C one is a call this index does not resolve. The
// alternative is not "the call resolves" — nothing here reads `.m` at all — it
// is a scheme that claims a language nothing extracts.
const (
	SwiftScheme    = "scip-swift"
	SwiftPMManager = "swiftpm"
)

// PackageManifest is the manifest FromSwiftPackage reads.
//
// `Ecosystem.Manifest` does two jobs — it is the filename `Resolve` stats to
// find the repository root, and it is the file `From` reads for an identity —
// and `Package.swift` is the rare manifest that does both without qualification.
// SwiftPM *defines* a package as the directory holding it, so it marks the root
// by construction rather than by convention, and `Package(name: "greeter")` is
// the one argument the manifest is required to state.
//
// # A manifest that is also a compilation unit
//
// `Package.swift` is written in Swift, which no earlier ecosystem's manifest
// was: a `go.mod` is not Go, a `package.json` is not TypeScript, a
// `CMakeLists.txt` is not C. `settings.gradle.kts` came closest — it is Kotlin —
// but Kotlin's own `.kts` extension marked it as a script, and Swift has no such
// marker: a manifest is a `.swift` like any other, so extract's `byExt` hands it
// to the Swift stanza as a source file and nothing in the registry can say
// otherwise without a core change.
//
// That is not worked around, it is modelled: extract/swift descriptors a
// manifest's declarations under a container named for the file, exactly as
// extract/kotlin does for a `.kts`, and for exactly the same reason — SwiftPM
// compiles each manifest as its own module, so two manifests in one repository
// declaring `let package` declare two unrelated things. See extract/swift's
// package comment.
//
// # What this costs, stated where it is felt
//
// The version is **always** Unknown, and unlike Gradle's case there is no other
// file to read: a SwiftPM package's version is a *git tag*, not a manifest
// field, so `Package.swift` states no version because the format has none to
// state. An unversioned coordinate names its package perfectly well and can
// never false-match (§4.3); an invented one would join.
//
// And, as cmake.go recorded for C and gradle.go for Kotlin: a Swift repository
// with neither a `Package.swift` nor any other language's manifest resolves to
// ErrNoManifest and does not index at all. An Xcode project is exactly that
// repository — `.xcodeproj` is a directory of XML this resolver does not read —
// so an app that has never adopted SwiftPM is out of scope. The fix is a
// last-resort coordinate in the shared `Resolve`, which is a change to the core
// §14 M9+ says an additional-language task must not make. It is recorded here so
// the next reader finds it stated rather than discovers it as a bug.
const PackageManifest = "Package.swift"

// SwiftExt is the file extension this ecosystem owns.
//
// One extension, and the shortest list since Go's. `.swift` is the whole of the
// language's surface: there is no script extension (a Swift script is a `.swift`
// run through the interpreter), no header (the module interface is generated,
// not written), and no second dialect. `.swiftinterface` is a *compiler output* —
// a textual module interface emitted for a built binary framework — and is
// excluded for the reason `.class` and `.o` are.
//
// It repeats extract/swift.Ext, for the reason coord.GoExt gives.
const SwiftExt = ".swift"

func init() {
	Register(Ecosystem{
		Manifest: PackageManifest,
		Scheme:   SwiftScheme,
		Manager:  SwiftPMManager,
		Exts:     []string{SwiftExt},
		From:     FromSwiftPackage,
	})
}

// FromSwiftPackage reads dir/Package.swift and returns the SwiftPM package's
// coordinate.
//
// Only an unreadable file is an error, which is the line every resolver since
// go.mod has drawn: `Resolve` stat'd it, so failing to open it means something
// is wrong with the tree rather than with the manifest's contents. A manifest
// whose name is computed, or spelled in a way this scan does not reach, yields
// Unknown — never an error, and never a guess.
func FromSwiftPackage(dir string) (Coord, error) {
	f, err := os.Open(filepath.Join(dir, PackageManifest)) //nolint:gosec // dir is the repository root the batch resolved.
	if err != nil {
		return Coord{}, err
	}
	defer func() { _ = f.Close() }()

	return Coord{
		Scheme:  SwiftScheme,
		Manager: SwiftPMManager,
		Name:    or(scanPackageName(f), Unknown),
		// Unknown, always: see PackageManifest.
		Version: Unknown,
		Root:    dir,
	}, nil
}

// scanPackageName finds the `name:` argument of the `Package(…)` initialiser and
// returns the string literal it is given.
//
// Scanned rather than parsed, for the reason cmake.go and gradle.go give and
// with the same deliberate shallowness. A `Package.swift` is a Swift program: it
// may build the name from a constant declared above, may branch on
// `#if os(Linux)`, and — since Swift 5.9 — may be preprocessed by a macro. This
// reads the first `name:` label that follows the first `Package(` and stops.
//
// The first `name:` and not any later one is what makes the scan correct rather
// than merely shallow: `Package(name:)` is the initialiser's first parameter and
// Swift requires arguments in declaration order, so every other `name:` in the
// file — a target's, a product's, a dependency's — is written after it. The
// scan therefore cannot reach a target name unless the package has none of its
// own, which does not compile.
//
// Line comments are stripped, because `// Package(name: "wrong")` declares
// nothing. A `/* … */` block comment is not handled and is rare enough to leave;
// the worst it can do is make a commented-out initialiser win, which yields a
// name the file does contain.
func scanPackageName(f *os.File) string {
	sc := bufio.NewScanner(f)
	seen := false
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		if !seen {
			i := strings.Index(line, "Package(")
			if i < 0 {
				continue
			}
			seen = true
			line = line[i+len("Package("):]
		}
		i := strings.Index(line, "name:")
		if i < 0 {
			continue
		}
		return swiftStringLiteral(strings.TrimSpace(line[i+len("name:"):]))
	}
	return ""
}

// swiftStringLiteral reduces the right-hand side of an argument label to what it
// actually states.
//
// Double quotes and nothing else, because that is the whole of Swift's string
// syntax: `'greeter'` does not compile at all — Swift has no character-literal
// quote — and a bare identifier names a value defined elsewhere. A `\(` inside
// the quotes is a string interpolation — the text is a pointer rather than the
// thing pointed at — so the honest reading is "not stated here", which is
// cmake.go's `${…}` rule and gradle.go's `$` rule in Swift's spelling.
//
// A raw string (`#"greeter"#`) is not handled: it is written to avoid escaping,
// which a package name never needs, so a manifest using one is stating something
// this scan is right to decline.
func swiftStringLiteral(v string) string {
	if len(v) < 2 || v[0] != '"' {
		return ""
	}
	end := strings.IndexByte(v[1:], '"')
	if end < 0 {
		return ""
	}
	lit := v[1 : 1+end]
	if strings.Contains(lit, `\`) {
		return ""
	}
	return lit
}
