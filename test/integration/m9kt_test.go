// The Kotlin feature suite: features/index_kotlin.feature against the real stack
// (SPEC.md §13, §14 M9+).
//
// The file is named m9kt_test.go rather than m9_test.go for the reason
// m9rs_test.go gives and the five suites after it repeat: §14 M9+ is not one
// milestone but a bag of independent language tasks with no ordering between
// them, so Swift (#24) will want its own file beside this one rather than to grow
// it.
//
// There is no new harness and no new stack — for the tenth time, which is the
// point rather than a convenience. m1_test.go's startStack brings up the one
// composition the package shares; m2_test.go's m2State carries the module copy,
// the `codiq` invocation, the graph reset and M1's MCP handshake; and
// m9cc_test.go is embedded whole, so M6's "no derived edge joins …" checks and
// every generalisation the eight suites before it made are called rather than
// reimplemented.
//
// What this file adds is two corpora and four steps. Three of the four are the
// C/C++ suite's generalised from nine languages to ten; the fourth is Kotlin's
// own, and it is the claim `.kts` forces: two scripts declaring one name declare
// two symbols. That is the whole diff, and it is the measurement: the tenth
// language cost a sub-package, a query, a resolver and two `byExt` entries, and
// no core package changed.
//
// The claims split the way they have in every suite before this one. Every
// navigation claim goes over MCP. The rest are SQL, because the GraphQL surface
// cannot express them: `imports` is file → file and File is not selectable by
// path; "definitions exist in ten languages" and "Greeter is defined once in
// each" are facts about the whole `file` table; and "these two scripts declare
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

// ktGreeterFixture is the Kotlin corpus, relative to the repository root: the
// Gradle build extract/kotlin/testdata/greeter/ the unit tests already use.
// Copied, never indexed in place — three scenarios add files to it.
var ktGreeterFixture = filepath.Join("extract", "kotlin", "testdata", "greeter")

// TestM9KotlinFeatures is the godog entry point for the Kotlin task, scoped to
// its own feature file so that each milestone's suite owns the scenarios it has
// steps for.
func TestM9KotlinFeatures(t *testing.T) {
	ctx := context.Background()
	repoRoot = mustRepoRoot(t)

	startStack(t, ctx)

	suite := godog.TestSuite{
		Name:                "m9kt",
		ScenarioInitializer: InitializeM9KotlinScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{filepath.Join(repoRoot, "features", "index_kotlin.feature")},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("m9kt feature scenarios failed")
	}
}

// ktCorpusFiles is the tenth language's half of the mixed corpus: a
// `settings.gradle.kts` beside the nine manifests already there, a `greeter`
// package declaring the type, and an `app` package that builds one and calls it.
//
// It collides as hard as the seven that already share a suffix, and here that
// costs nothing at all: `package greeter` and `fun greet()` are what a Kotlin
// author would write anyway, since Kotlin's conventions are Java's for packages
// and members. The PHP and C# halves had to be written *against* their own style
// guides to reach the same collision; this one is simply idiomatic.
//
// The caller assigns a property instead of passing a constructor argument, which
// is the Java, Rust, C#, PHP and C++ corpora's choice for the same reason — a
// declared constructor would be a member of its type — and it costs nothing here
// because a Kotlin property write is a `field` reference, which link's `calls`
// derivation (`symbol_kind IN ('function', 'method')`) does not select. So the
// traversal below still matches exactly one row.
//
// The `settings.gradle.kts` is a Kotlin file as well as a manifest, and it is
// indexed as one. That is the `.kts` decision showing through in the corpus
// rather than only in the unit tests: it carries `file.lang = "kt"`, its two
// unresolved reads name nothing, and its declarations — it has none — would have
// hung off a container named for the file.
var ktCorpusFiles = map[string]string{
	"settings.gradle.kts": `rootProject.name = "mixed-kotlin"
`,

	"kotlin/Greeter.kt": `package greeter

class Greeter {
    var name: String = ""

    fun greet(): String = "hello, " + name
}
`,

	"kotlin/Bootstrap.kt": `package app

import greeter.Greeter

fun proceed(): String {
    val g = Greeter()
    g.name = "world"

    return g.greet()
}
`,
}

// tenLanguageCorpus is the C/C++ task's nine-language corpus with a tenth
// language added, and it is assembled rather than rewritten precisely because
// that is the claim: the nine earlier languages' files are byte-for-byte the ones
// m9cc_test.go already indexes, so anything this scenario finds that the C/C++
// one did not is Kotlin's doing.
var tenLanguageCorpus = func() map[string]string {
	out := make(map[string]string, len(nineLanguageCorpus)+len(ktCorpusFiles))
	maps.Copy(out, nineLanguageCorpus)
	maps.Copy(out, ktCorpusFiles)
	return out
}()

// m9ktState is one scenario's state: the C/C++ task's, which is the PHP task's,
// which is the Ruby task's, … back to M2's. Nothing is added — the corpora are on
// disk and the assertions are stateless — and that is itself part of what the
// task claims.
type m9ktState struct {
	m9ccState
}

func InitializeM9KotlinScenario(sc *godog.ScenarioContext) {
	st := &m9ktState{}

	// The same real handshake every suite does, once per scenario.
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "codiq-m9kt-tests", Version: "0.1.0"}, nil)
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
	sc.Step(`^the Kotlin project$`, st.theKotlinProject)
	sc.Step(`^the artifact also holds "([^"]*)":$`, st.packageAlsoHolds)
	sc.Step(`^the artifact is indexed$`, st.moduleIndexed)
	sc.Step(`^the mixed-language repository of ten languages$`, st.theTenLanguageRepository)
	sc.Step(`^the repository is indexed$`, st.moduleIndexed)
	sc.Step(`^"([^"]*)" imports "([^"]*)"$`, st.fileImports)
	sc.Step(`^the "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)" definitions of "([^"]*)" differ only in their coordinate$`, st.eightDifferOnlyInCoordinate)
	sc.Step(`^"([^"]*)" is defined once in each of "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)"$`, st.definedOnceInEachOfTen)
	sc.Step(`^the graph holds definitions written in all of "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)"$`, st.tenLanguagesPresent)
	sc.Step(`^no derived edge joins two package schemes$`, st.noCrossSchemeEdges)
	sc.Step(`^no derived edge joins two languages$`, st.noCrossLanguageEdges)

	// Kotlin's own, and the claim `.kts` forces.
	sc.Step(`^"([^"]*)" and "([^"]*)" declare "([^"]*)" as two symbols$`, st.scriptsDeclareTwoSymbols)

	// M1's steps, reused as written: the MCP tool call and the JSON comparison.
	sc.Step(`^an agent asks over MCP:$`, st.agentAsks)
	sc.Step(`^the answer is:$`, st.answerIs)
}

// --- given -----------------------------------------------------------------

// theKotlinProject copies the Kotlin fixture into a temp directory, the way
// m2State.theModule copies the Go one and for the same reason: three scenarios
// add files to it, and extract/kotlin/testdata is the unit tests' fixture.
func (st *m9ktState) theKotlinProject() error {
	dir, err := os.MkdirTemp("", "codiq-m9kt-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	st.repo = filepath.Join(dir, "greeter")
	return copyTree(filepath.Join(repoRoot, ktGreeterFixture), st.repo)
}

// theTenLanguageRepository writes the ten-language corpus into a temp directory,
// the way the other Given step copies a fixture into one — the same tmp root, so
// the After hook cleans up both the same way.
func (st *m9ktState) theTenLanguageRepository() error {
	dir, err := os.MkdirTemp("", "codiq-m9kt-mixed-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	st.repo = filepath.Join(dir, "mixed")
	for rel, body := range tenLanguageCorpus {
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

// eightDifferOnlyInCoordinate is m9ccState.sevenDifferOnlyInCoordinate over the
// eight languages that now share a suffix. Kotlin extends the chain, and it is
// the first to do so without writing anything its own style guide disowns.
func (st *m9ktState) eightDifferOnlyInCoordinate(ctx context.Context, first, second, third, fourth, fifth, sixth, seventh, eighth, name string) error {
	if err := st.sevenDifferOnlyInCoordinate(ctx, first, second, third, fourth, fifth, sixth, seventh, name); err != nil {
		return err
	}
	return st.differOnlyInCoordinate(ctx, seventh, eighth, name)
}

// definedOnceInEachOfTen is m9ccState.definedOnceInEachOfNine with a tenth
// language: the name is declared once in each, and no language's definition
// swallowed another's.
func (st *m9ktState) definedOnceInEachOfTen(ctx context.Context, name, first, second, third, fourth, fifth, sixth, seventh, eighth, ninth, tenth string) error {
	if err := st.definedOnceInEachOfNine(ctx, name, first, second, third, fourth, fifth, sixth, seventh, eighth, ninth); err != nil {
		return err
	}
	return st.definedOnceInEachLanguage(ctx, name, ninth, tenth)
}

// tenLanguagesPresent is "one graph, ten languages" stated over the whole `file`
// table: all ten tags are there, and all ten actually contributed definitions
// rather than merely being registered as files.
func (st *m9ktState) tenLanguagesPresent(ctx context.Context, first, second, third, fourth, fifth, sixth, seventh, eighth, ninth, tenth string) error {
	if err := st.nineLanguagesPresent(ctx, first, second, third, fourth, fifth, sixth, seventh, eighth, ninth); err != nil {
		return err
	}
	return st.bothLanguagesPresent(ctx, ninth, tenth)
}

// scriptsDeclareTwoSymbols is the claim `.kts` forces, and the reason a Kotlin
// script does not simply inherit its package's namespace.
//
// kotlinc compiles each script into a class named after the *file*, so two
// scripts declaring `val logger` declare two unrelated things — and a stanza that
// descriptored both as members of the root package would render one string for
// both, which the link pass joins. That is a phantom edge, and nothing
// downstream can tell it is false.
//
// Both files must also be Kotlin: a script that indexed as another language, or
// as nothing, would make this pass for the wrong reason.
func (st *m9ktState) scriptsDeclareTwoSymbols(ctx context.Context, first, second, name string) error {
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
		assert.Len(t, byPath, 2, "each script must declare %s exactly once; found %v", name, byPath)
		assert.NotEqual(t, byPath[first], byPath[second],
			"two scripts declaring %s declare two symbols; one descriptor for both is an edge that does not exist", name)
		for path, lang := range langs {
			assert.Equal(t, "kt", lang, "%s is a Kotlin script and has to index as one", path)
		}
	})
}
