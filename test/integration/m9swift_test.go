// The Swift feature suite: features/index_swift.feature against the real stack
// (SPEC.md §13, §14 M9+).
//
// The file is named m9swift_test.go rather than m9_test.go for the reason
// m9rs_test.go gives and the six suites after it repeat: §14 M9+ is not one
// milestone but a bag of independent language tasks with no ordering between
// them, so each wants its own file rather than to grow a shared one.
//
// There is no new harness and no new stack — for the eleventh time, which is the
// point rather than a convenience. m1_test.go's startStack brings up the one
// composition the package shares; m2_test.go's m2State carries the module copy,
// the `codiq` invocation, the graph reset and M1's MCP handshake; and
// m9kt_test.go is embedded whole, so M6's "no derived edge joins …" checks and
// every generalisation the nine suites before it made are called rather than
// reimplemented.
//
// What this file adds is two corpora and four steps. Three of the four are the
// Kotlin suite's generalised from ten languages to eleven; the fourth is Swift's
// own, and it is the claim `Package.swift` forces: two manifests declare two
// symbols. That is the whole diff, and it is the measurement: the eleventh
// language cost a sub-package, a query, a resolver and one `byExt` entry, and no
// core package changed.
//
// The claims split the way they have in every suite before this one. Every
// navigation claim goes over MCP. The rest are SQL, because the GraphQL surface
// cannot express them: `imports` is file → file and File is not selectable by
// path; "definitions exist in eleven languages" and "Greeter is defined once in
// each" are facts about the whole `file` table; and "these two manifests declare
// two symbols" is a statement about two rows that must not be one.
package integration

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/cucumber/godog"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
)

// swiftGreeterFixture is the Swift corpus, relative to the repository root: the
// SwiftPM package extract/swift/testdata/greeter/ the unit tests already use.
// Copied, never indexed in place — three scenarios add files to it.
var swiftGreeterFixture = filepath.Join("extract", "swift", "testdata", "greeter")

// TestM9SwiftFeatures is the godog entry point for the Swift task, scoped to its
// own feature file so that each milestone's suite owns the scenarios it has
// steps for.
func TestM9SwiftFeatures(t *testing.T) {
	ctx := context.Background()
	repoRoot = mustRepoRoot(t)

	startStack(t, ctx)

	suite := godog.TestSuite{
		Name:                "m9swift",
		ScenarioInitializer: InitializeM9SwiftScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{filepath.Join(repoRoot, "features", "index_swift.feature")},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("m9swift feature scenarios failed")
	}
}

// swiftCorpusFiles is the eleventh language's half of the mixed corpus: a
// `Package.swift` beside the ten manifests already there, and one target holding
// the type and the caller.
//
// **One target, not two**, and that is the corpus stating Swift's central limit
// rather than working around it. Every other language's half of this corpus puts
// the caller in a second namespace and reaches the first through an import;
// Swift's import is a whole-module wildcard, so a cross-module call cannot be
// resolved file-locally and this stanza refuses to guess. Both files are
// therefore in `Sources/greeter/`, which is where a Swift author would put two
// files of one module anyway.
//
// The directory is lowercase, which the API Design Guidelines disown and SwiftPM
// permits, and that is what it costs Swift to join the `greeter/Greeter#greet().`
// collision at all: the module is a directory name, so colliding means naming
// the directory against the language's conventions. PHP and C# had to write
// against theirs too; Kotlin was the one language it cost nothing.
//
// The caller assigns a property instead of passing an initialiser argument,
// which is the Java, Rust, C#, PHP, C++ and Kotlin corpora's choice for the same
// reason — a declared initialiser would be a member of its type — and it costs
// nothing here because a Swift property write is a `field` reference, which
// link's `calls` derivation (`symbol_kind IN ('function', 'method')`) does not
// select. So the traversal below still matches exactly one row.
//
// The `Package.swift` is a Swift file as well as a manifest, and it is indexed
// as one. That is the manifest decision showing through in the corpus rather
// than only in the unit tests: it carries `file.lang = "swift"`, it declares
// `package` into a container named for the file, and it belongs to no module —
// so it contributes no namespace anything else could collide with.
var swiftCorpusFiles = map[string]string{
	"Package.swift": `// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "mixed-swift",
    targets: [.target(name: "greeter")]
)
`,

	"Sources/greeter/Greeter.swift": `struct Greeter {
    var name: String = ""

    func greet() -> String {
        return "hello, " + name
    }
}
`,

	"Sources/greeter/Bootstrap.swift": `func advance() -> String {
    var g = Greeter()
    g.name = "world"

    return g.greet()
}
`,
}

// elevenLanguageCorpus is the Kotlin task's ten-language corpus with an
// eleventh language added, and it is assembled rather than rewritten precisely
// because that is the claim: the ten earlier languages' files are byte-for-byte
// the ones m9kt_test.go already indexes, so anything this scenario finds that
// the Kotlin one did not is Swift's doing.
var elevenLanguageCorpus = func() map[string]string {
	out := make(map[string]string, len(tenLanguageCorpus)+len(swiftCorpusFiles))
	maps.Copy(out, tenLanguageCorpus)
	maps.Copy(out, swiftCorpusFiles)
	return out
}()

// m9swiftState is one scenario's state: the Kotlin task's, which is the C/C++
// task's, which is the PHP task's, … back to M2's. Nothing is added — the
// corpora are on disk and the assertions are stateless — and that is itself part
// of what the task claims.
type m9swiftState struct {
	m9ktState
}

func InitializeM9SwiftScenario(sc *godog.ScenarioContext) {
	st := &m9swiftState{}

	// The same real handshake every suite does, once per scenario.
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "codiq-m9swift-tests", Version: "0.1.0"}, nil)
		session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: mcpURL}, nil)
		if err != nil {
			return ctx, fmt.Errorf("mcp handshake with %s: %w", mcpURL, err)
		}
		st.session = session
		return ctx, nil
	})

	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if st.session != nil {
			_ = st.session.Close()
			st.session = nil
		}
		if st.tmp != "" {
			_ = os.RemoveAll(st.tmp)
		}
		st.repo, st.tmp, st.report, st.before, st.other = "", "", "", nil, ""
		return ctx, nil
	})

	sc.Step(`^an empty CodiQ graph$`, st.emptyGraph)
	sc.Step(`^the Swift package$`, st.theSwiftPackage)
	sc.Step(`^the artifact also holds "([^"]*)":$`, st.packageAlsoHolds)
	sc.Step(`^the artifact is indexed$`, st.moduleIndexed)
	sc.Step(`^the mixed-language repository of eleven languages$`, st.theElevenLanguageRepository)
	sc.Step(`^the repository is indexed$`, st.moduleIndexed)
	sc.Step(`^"([^"]*)" imports "([^"]*)"$`, st.fileImports)
	sc.Step(`^the "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)" definitions of "([^"]*)" differ only in their coordinate$`, st.nineDifferOnlyInCoordinate)
	sc.Step(`^"([^"]*)" is defined once in each of "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)"$`, st.definedOnceInEachOfEleven)
	sc.Step(`^the graph holds definitions written in all of "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)"$`, st.elevenLanguagesPresent)

	// M6's two invariants, bound to the *m6State methods that first stated them
	// rather than restated here. The second is the one this task has the most to
	// say about: Swift's `Package.swift` is a manifest that is also a source, so
	// a repository indexes a build file as a compilation unit — and an edge from
	// one to anything else would be a cross-language edge if the manifest were
	// tagged as anything but Swift.
	sc.Step(`^no derived edge joins two package schemes$`, st.noCrossSchemeEdges)
	sc.Step(`^no derived edge joins two languages$`, st.noCrossLanguageEdges)

	// Swift's own, and the claim `Package.swift` forces.
	sc.Step(`^"([^"]*)" and "([^"]*)" declare "([^"]*)" as two symbols$`, st.manifestsDeclareTwoSymbols)

	// M1's steps, reused as written: the MCP tool call and the JSON comparison.
	sc.Step(`^an agent asks over MCP:$`, st.agentAsks)
	sc.Step(`^the answer is:$`, st.answerIs)
}

// --- given -----------------------------------------------------------------

// theSwiftPackage copies the Swift fixture into a temp directory, the way
// m2State.theModule copies the Go one and for the same reason: three scenarios
// add files to it, and extract/swift/testdata is the unit tests' fixture.
func (st *m9swiftState) theSwiftPackage() error {
	dir, err := os.MkdirTemp("", "codiq-m9swift-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	st.repo = filepath.Join(dir, "greeter")
	return copyTree(filepath.Join(repoRoot, swiftGreeterFixture), st.repo)
}

// theElevenLanguageRepository writes the eleven-language corpus into a temp
// directory, the way the other Given step copies a fixture into one — the same
// tmp root, so the After hook cleans up both the same way.
func (st *m9swiftState) theElevenLanguageRepository() error {
	dir, err := os.MkdirTemp("", "codiq-m9swift-mixed-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	st.repo = filepath.Join(dir, "mixed")
	for rel, body := range elevenLanguageCorpus {
		path := filepath.Join(st.repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// --- then ------------------------------------------------------------------

// nineDifferOnlyInCoordinate is m9ktState.eightDifferOnlyInCoordinate over the
// nine languages that now share a suffix. Swift extends the chain, and it is the
// first to do so by naming a *directory* against its own conventions rather than
// by writing a declaration against them.
func (st *m9swiftState) nineDifferOnlyInCoordinate(ctx context.Context, first, second, third, fourth, fifth, sixth, seventh, eighth, ninth, name string) error {
	if err := st.eightDifferOnlyInCoordinate(ctx, first, second, third, fourth, fifth, sixth, seventh, eighth, name); err != nil {
		return err
	}
	return st.differOnlyInCoordinate(ctx, eighth, ninth, name)
}

// definedOnceInEachOfEleven is m9ktState.definedOnceInEachOfTen with an
// eleventh language: the name is declared once in each, and no language's
// definition swallowed another's.
func (st *m9swiftState) definedOnceInEachOfEleven(ctx context.Context, name, first, second, third, fourth, fifth, sixth, seventh, eighth, ninth, tenth, eleventh string) error {
	if err := st.definedOnceInEachOfTen(ctx, name, first, second, third, fourth, fifth, sixth, seventh, eighth, ninth, tenth); err != nil {
		return err
	}
	return st.definedOnceInEachLanguage(ctx, name, tenth, eleventh)
}

// elevenLanguagesPresent is "one graph, eleven languages" stated over the whole
// `file` table: all eleven tags are there, and all eleven actually contributed
// definitions rather than merely being registered as files.
func (st *m9swiftState) elevenLanguagesPresent(ctx context.Context, first, second, third, fourth, fifth, sixth, seventh, eighth, ninth, tenth, eleventh string) error {
	if err := st.tenLanguagesPresent(ctx, first, second, third, fourth, fifth, sixth, seventh, eighth, ninth, tenth); err != nil {
		return err
	}
	return st.bothLanguagesPresent(ctx, tenth, eleventh)
}

// manifestsDeclareTwoSymbols is the claim `Package.swift` forces, and the reason
// a SwiftPM manifest does not simply inherit the root module's namespace.
//
// SwiftPM compiles each manifest on its own, so two of them declaring `let
// package` declare two unrelated things — and a stanza that descriptored both as
// members of the root module would render one string for both, which the link
// pass joins. That is a phantom edge, and nothing downstream can tell it is
// false.
//
// Both files must also be Swift: a manifest that indexed as another language, or
// as nothing, would make this pass for the wrong reason. That is not a
// hypothetical here the way it was for a `.kts` — `Package.swift` carries the
// extension the Swift ecosystem owns, so the registry has no way to hand it
// anywhere else, and this pins that the registry really does hand it here.
func (st *m9swiftState) manifestsDeclareTwoSymbols(ctx context.Context, first, second, name string) error {
	byPath := map[string]string{}
	langs := map[string]string{}
	rows, err := pool.Query(ctx, `
		SELECT f.path, f.lang, o.descriptor
		FROM occurrence o
		JOIN file f ON f.id = o.file_id
		WHERE f.path IN ($1, $2) AND o.name = $3 AND o.role = 'definition'`, first, second, name)
	if err != nil {
		return fmt.Errorf("read the definitions of %s in %s and %s: %w", name, first, second, err)
	}
	defer rows.Close()
	for rows.Next() {
		var path, lang, descriptor string
		if err := rows.Scan(&path, &lang, &descriptor); err != nil {
			return err
		}
		byPath[path], langs[path] = descriptor, lang
	}
	if err := rows.Err(); err != nil {
		return err
	}

	return check(func(t assert.TestingT) {
		assert.Len(t, byPath, 2, "each manifest must declare %s exactly once; found %v", name, byPath)
		assert.NotEqual(t, byPath[first], byPath[second],
			"two manifests declaring %s declare two symbols; one descriptor for both is an edge that does not exist", name)
		for path, lang := range langs {
			assert.Equal(t, "swift", lang, "%s is a Swift source file and has to index as one", path)
		}
	})
}
