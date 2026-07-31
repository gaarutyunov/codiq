// The Java feature suite: features/index_java.feature against the real stack
// (SPEC.md §13, §14 M9+).
//
// The file is named m9java_test.go rather than m9_test.go for the reason
// m9rs_test.go gives: §14 M9+ is not one milestone but a bag of independent
// language tasks with no ordering between them, so C# will want m9cs_test.go
// beside this one rather than to grow it.
//
// There is no new harness and no new stack — for the fifth time, which is the
// point rather than a convenience. m1_test.go's startStack brings up the one
// composition the package shares; m2_test.go's m2State carries the module copy,
// the `codiq` invocation, the graph reset and M1's MCP handshake; and
// m9rs_test.go is embedded whole, so M6's "no derived edge joins …" checks and
// every generalisation the three suites before this one made are called rather
// than reimplemented. A suite that had to build its own indexing machinery to
// test Java would have quietly disproved the claim the task exists to make.
//
// What this file adds is two corpora and four steps, and three of the four are
// the Rust suite's generalised from four languages to five. That is the whole
// diff, and it is the measurement: the fifth language cost a sub-package, a
// query, a resolver and a `byExt` entry, and no core package changed.
//
// The claims split the way they have in every suite before this one. Every
// navigation claim goes over MCP. The rest are SQL, because the GraphQL surface
// cannot express them: `imports` is file → file and File is not selectable by
// path (schema/codiq.graphql says so explicitly, and says why); "definitions
// exist in five languages" and "Greeter is defined once in each" are facts about
// the whole `file` table; and "no derived edge joins …" is a statement about the
// absence of a row anywhere, which a traversal can never return.
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
)

// javaGreeterFixture is the Java corpus, relative to the repository root: the
// artifact extract/java/testdata/greeter/ the unit tests already use. Copied,
// never indexed in place — two scenarios add files to it.
var javaGreeterFixture = filepath.Join("extract", "java", "testdata", "greeter")

// TestM9JavaFeatures is the godog entry point for the Java task, scoped to its
// own feature file so that each milestone's suite owns the scenarios it has
// steps for.
func TestM9JavaFeatures(t *testing.T) {
	ctx := context.Background()
	repoRoot = mustRepoRoot(t)

	startStack(t, ctx)

	suite := godog.TestSuite{
		Name:                "m9java",
		ScenarioInitializer: InitializeM9JavaScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{filepath.Join(repoRoot, "features", "index_java.feature")},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("m9java feature scenarios failed")
	}
}

// javaCorpusFiles is the fifth language's half of the mixed corpus: a pom.xml
// beside the four manifests already there, a `greeter` package declaring the
// type, and an `app` package that builds one and calls it.
//
// It is written to collide as hard as the four before it. The package clause
// says `greeter` outright — Java is the one language here that states its
// namespace rather than deriving it — so the suffix is `greeter/Greeter#greet().`
// byte for byte with TypeScript's, Python's and Rust's, and only the coordinate
// separates the four.
//
// The caller assigns a public field instead of passing a constructor argument.
// That is the Rust corpus's struct-literal choice for the same reason: a
// declared constructor is a member of its type *named after it*, so `Greeter`
// would name two definitions in one file and "defined once in each language"
// would stop meaning what it says.
var javaCorpusFiles = map[string]string{
	"pom.xml": `<project><groupId>com.codiq</groupId><artifactId>mixed-java</artifactId><version>5.0.0</version></project>` + "\n",

	"src/main/java/greeter/Greeter.java": `package greeter;

public class Greeter {
    public String name;

    public String greet() {
        return "hello, " + this.name;
    }
}
`,
	"src/main/java/app/Launcher.java": `package app;

import greeter.Greeter;

public final class Launcher {
    public static String launch() {
        Greeter g = new Greeter();
        g.name = "world";
        return g.greet();
    }
}
`,
}

// fiveLanguageCorpus is the Rust task's four-language corpus with a fifth
// language added, and it is assembled rather than rewritten precisely because
// that is the claim: the four earlier languages' files are byte-for-byte the
// ones m9rs_test.go already indexes, so anything this scenario finds that the
// Rust one did not is Java's doing.
var fiveLanguageCorpus = func() map[string]string {
	out := make(map[string]string, len(fourLanguageCorpus)+len(javaCorpusFiles))
	maps.Copy(out, fourLanguageCorpus)
	maps.Copy(out, javaCorpusFiles)
	return out
}()

// m9javaState is one scenario's state: the Rust task's, which is M7's, which is
// M6's, which is M2's plus the second-tree machinery. Nothing is added — the
// corpora are on disk and the assertions are stateless — and that is itself part
// of what the task claims.
type m9javaState struct {
	m9rsState
}

func InitializeM9JavaScenario(sc *godog.ScenarioContext) {
	st := &m9javaState{}

	// The same real handshake every suite does, once per scenario.
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "codiq-m9java-tests", Version: "0.1.0"}, nil)
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
	sc.Step(`^the two-file Java artifact$`, st.theJavaArtifact)
	sc.Step(`^the artifact also holds "([^"]*)":$`, st.packageAlsoHolds)
	sc.Step(`^the artifact is indexed$`, st.moduleIndexed)
	sc.Step(`^the mixed-language repository of five languages$`, st.theFiveLanguageRepository)
	sc.Step(`^the repository is indexed$`, st.moduleIndexed)
	sc.Step(`^"([^"]*)" imports "([^"]*)"$`, st.fileImports)
	sc.Step(`^the "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)" definitions of "([^"]*)" differ only in their coordinate$`, st.fourDifferOnlyInCoordinate)
	sc.Step(`^"([^"]*)" is defined once in each of "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)"$`, st.definedOnceInEachOfFive)
	sc.Step(`^the graph holds definitions written in all of "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)"$`, st.fiveLanguagesPresent)
	sc.Step(`^no derived edge joins two package schemes$`, st.noCrossSchemeEdges)
	sc.Step(`^no derived edge joins two languages$`, st.noCrossLanguageEdges)

	// M1's steps, reused as written: the MCP tool call and the JSON comparison.
	sc.Step(`^an agent asks over MCP:$`, st.agentAsks)
	sc.Step(`^the answer is:$`, st.answerIs)
}

// --- given -----------------------------------------------------------------

// theJavaArtifact copies the Java fixture into a temp directory, the way
// m2State.theModule copies the Go one and for the same reason: two scenarios add
// files to it, and extract/java/testdata is the unit tests' fixture.
func (st *m9javaState) theJavaArtifact() error {
	dir, err := os.MkdirTemp("", "codiq-m9java-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	st.repo = filepath.Join(dir, "greeter")
	return copyTree(filepath.Join(repoRoot, javaGreeterFixture), st.repo)
}

// theFiveLanguageRepository writes the five-language corpus into a temp
// directory, the way the other Given step copies a fixture into one — the same
// tmp root, so the After hook cleans up both the same way.
func (st *m9javaState) theFiveLanguageRepository() error {
	dir, err := os.MkdirTemp("", "codiq-m9java-mixed-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	st.repo = filepath.Join(dir, "mixed")
	for rel, body := range fiveLanguageCorpus {
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

// fourDifferOnlyInCoordinate is m9rsState.threeDifferOnlyInCoordinate over the
// four languages that now share a suffix, which is what makes the mixed
// scenario's other assertions mean anything. The Rust task could state it of
// three; here `greet` is written by TypeScript, Python, Rust and Java and all
// four render the same descriptor after the coordinate, so the pairwise claim
// has to hold along the whole chain.
func (st *m9javaState) fourDifferOnlyInCoordinate(ctx context.Context, first, second, third, fourth, name string) error {
	if err := st.threeDifferOnlyInCoordinate(ctx, first, second, third, name); err != nil {
		return err
	}
	return st.differOnlyInCoordinate(ctx, third, fourth, name)
}

// definedOnceInEachOfFive is m9rsState.definedOnceInEachOfFour with a fifth
// language, which is the collision the five-language corpus is built around.
func (st *m9javaState) definedOnceInEachOfFive(ctx context.Context, name, first, second, third, fourth, fifth string) error {
	if err := st.definedOnceInEachOfFour(ctx, name, first, second, third, fourth); err != nil {
		return err
	}
	return st.definedOnceInEachLanguage(ctx, name, fourth, fifth)
}

// fiveLanguagesPresent is "one graph, five languages" stated over the whole
// `file` table: all five tags are there, and all five actually contributed
// definitions rather than merely being registered as files.
func (st *m9javaState) fiveLanguagesPresent(ctx context.Context, first, second, third, fourth, fifth string) error {
	if err := st.fourLanguagesPresent(ctx, first, second, third, fourth); err != nil {
		return err
	}
	return st.bothLanguagesPresent(ctx, fourth, fifth)
}
