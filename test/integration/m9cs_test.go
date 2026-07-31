// The C# feature suite: features/index_cs.feature against the real stack
// (SPEC.md §13, §14 M9+).
//
// The file is named m9cs_test.go rather than m9_test.go for the reason
// m9rs_test.go gives and m9java_test.go repeats: §14 M9+ is not one milestone
// but a bag of independent language tasks with no ordering between them, so Ruby
// will want m9rb_test.go beside this one rather than to grow it.
//
// There is no new harness and no new stack — for the sixth time, which is the
// point rather than a convenience. m1_test.go's startStack brings up the one
// composition the package shares; m2_test.go's m2State carries the module copy,
// the `codiq` invocation, the graph reset and M1's MCP handshake; and
// m9java_test.go is embedded whole, so M6's "no derived edge joins …" checks and
// every generalisation the four suites before this one made are called rather
// than reimplemented. A suite that had to build its own indexing machinery to
// test C# would have quietly disproved the claim the task exists to make.
//
// What this file adds is two corpora and five steps, and three of the five are
// the Java suite's generalised from five languages to six. The other two are
// C#'s own, and they are the only steps in this package that exist because a
// language can declare one type in two files. That is the whole diff, and it is
// the measurement: the sixth language cost a sub-package, a query, a resolver
// and a `byExt` entry, and no core package changed.
//
// The claims split the way they have in every suite before this one. Every
// navigation claim goes over MCP. The rest are SQL, because the GraphQL surface
// cannot express them: `imports` is file → file and File is not selectable by
// path (schema/codiq.graphql says so explicitly, and says why); "definitions
// exist in six languages" and "Greeter is defined once in each" are facts about
// the whole `file` table; "no derived edge joins …" is a statement about the
// absence of a row anywhere; and a partial class's two definition rows share a
// descriptor, so a traversal reaching them returns them in an order the GraphQL
// layer does not promise.
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

// csGreeterFixture is the C# corpus, relative to the repository root: the
// artifact extract/cs/testdata/greeter/ the unit tests already use. Copied,
// never indexed in place — three scenarios add files to it.
var csGreeterFixture = filepath.Join("extract", "cs", "testdata", "greeter")

// TestM9CSFeatures is the godog entry point for the C# task, scoped to its own
// feature file so that each milestone's suite owns the scenarios it has steps
// for.
func TestM9CSFeatures(t *testing.T) {
	ctx := context.Background()
	repoRoot = mustRepoRoot(t)

	startStack(t, ctx)

	suite := godog.TestSuite{
		Name:                "m9cs",
		ScenarioInitializer: InitializeM9CSScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{filepath.Join(repoRoot, "features", "index_cs.feature")},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("m9cs feature scenarios failed")
	}
}

// csCorpusFiles is the sixth language's half of the mixed corpus: a
// Directory.Build.props beside the five manifests already there, a `greeter`
// namespace declaring the type, and an `app` namespace that builds one and calls
// it.
//
// It is written to collide as hard as the five before it, and it is the one half
// of this corpus written against its own language's conventions rather than with
// them. .NET names a namespace and a method in PascalCase; this writes
// `namespace greeter;` and `greet()` outright, so the suffix is
// `greeter/Greeter#greet().` byte for byte with TypeScript's, Python's, Rust's
// and Java's and only the coordinate separates the five. Both spellings are
// legal C# and neither is idiomatic, and that is deliberate: the collision is
// the fixture, and a `Greet()` that could not collide would have tested nothing.
//
// The caller assigns a public field instead of passing a constructor argument.
// That is the Java and Rust corpora's choice for the same reason: a declared
// constructor is a member of its type *named after it*, so `Greeter` would name
// two definitions in one file and "defined once in each language" would stop
// meaning what it says.
var csCorpusFiles = map[string]string{
	"Directory.Build.props": `<Project><PropertyGroup><PackageId>Codiq.Mixed</PackageId><Version>6.0.0</Version></PropertyGroup></Project>` + "\n",

	"src/greeter/Greeter.cs": `namespace greeter;

public class Greeter
{
    public string name;

    public string greet()
    {
        return "hello, " + this.name;
    }
}
`,
	"src/app/Bootstrap.cs": `using greeter;

namespace app;

public static class Bootstrap
{
    public static string begin()
    {
        Greeter g = new Greeter();
        g.name = "world";
        return g.greet();
    }
}
`,
}

// sixLanguageCorpus is the Java task's five-language corpus with a sixth
// language added, and it is assembled rather than rewritten precisely because
// that is the claim: the five earlier languages' files are byte-for-byte the ones
// m9java_test.go already indexes, so anything this scenario finds that the Java
// one did not is C#'s doing.
var sixLanguageCorpus = func() map[string]string {
	out := make(map[string]string, len(fiveLanguageCorpus)+len(csCorpusFiles))
	maps.Copy(out, fiveLanguageCorpus)
	maps.Copy(out, csCorpusFiles)
	return out
}()

// m9csState is one scenario's state: the Java task's, which is the Rust task's,
// which is M7's, which is M6's, which is M2's plus the second-tree machinery.
// Nothing is added — the corpora are on disk and the assertions are stateless —
// and that is itself part of what the task claims.
type m9csState struct {
	m9javaState
}

func InitializeM9CSScenario(sc *godog.ScenarioContext) {
	st := &m9csState{}

	// The same real handshake every suite does, once per scenario.
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "codiq-m9cs-tests", Version: "0.1.0"}, nil)
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
	sc.Step(`^the two-file C# artifact$`, st.theCSArtifact)
	sc.Step(`^the artifact also holds "([^"]*)":$`, st.packageAlsoHolds)
	sc.Step(`^the artifact is indexed$`, st.moduleIndexed)
	sc.Step(`^the mixed-language repository of six languages$`, st.theSixLanguageRepository)
	sc.Step(`^the repository is indexed$`, st.moduleIndexed)
	sc.Step(`^"([^"]*)" imports "([^"]*)"$`, st.fileImports)
	sc.Step(`^the "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)" definitions of "([^"]*)" differ only in their coordinate$`, st.fiveDifferOnlyInCoordinate)
	sc.Step(`^"([^"]*)" is defined once in each of "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)"$`, st.definedOnceInEachOfSix)
	sc.Step(`^the graph holds definitions written in all of "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)"$`, st.sixLanguagesPresent)
	sc.Step(`^no derived edge joins two package schemes$`, st.noCrossSchemeEdges)
	sc.Step(`^no derived edge joins two languages$`, st.noCrossLanguageEdges)

	// C#'s own two, and the only steps in this package that exist because a
	// language can declare one type across several files.
	sc.Step(`^"([^"]*)" is declared in (\d+) files under one descriptor$`, st.declaredInFilesUnderOneDescriptor)
	sc.Step(`^"([^"]*)" is implemented from every file "([^"]*)" is declared in$`, st.implementedFromEveryDeclaringFile)

	// M1's steps, reused as written: the MCP tool call and the JSON comparison.
	sc.Step(`^an agent asks over MCP:$`, st.agentAsks)
	sc.Step(`^the answer is:$`, st.answerIs)
}

// --- given -----------------------------------------------------------------

// theCSArtifact copies the C# fixture into a temp directory, the way
// m2State.theModule copies the Go one and for the same reason: three scenarios
// add files to it, and extract/cs/testdata is the unit tests' fixture.
func (st *m9csState) theCSArtifact() error {
	dir, err := os.MkdirTemp("", "codiq-m9cs-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	st.repo = filepath.Join(dir, "greeter")
	return copyTree(filepath.Join(repoRoot, csGreeterFixture), st.repo)
}

// theSixLanguageRepository writes the six-language corpus into a temp directory,
// the way the other Given step copies a fixture into one — the same tmp root, so
// the After hook cleans up both the same way.
func (st *m9csState) theSixLanguageRepository() error {
	dir, err := os.MkdirTemp("", "codiq-m9cs-mixed-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	st.repo = filepath.Join(dir, "mixed")
	for rel, body := range sixLanguageCorpus {
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

// fiveDifferOnlyInCoordinate is m9javaState.fourDifferOnlyInCoordinate over the
// five languages that now share a suffix, which is what makes the mixed
// scenario's other assertions mean anything. The Java task could state it of
// four; here `greet` is written by TypeScript, Python, Rust, Java and C# and all
// five render the same descriptor after the coordinate, so the pairwise claim
// has to hold along the whole chain.
func (st *m9csState) fiveDifferOnlyInCoordinate(ctx context.Context, first, second, third, fourth, fifth, name string) error {
	if err := st.fourDifferOnlyInCoordinate(ctx, first, second, third, fourth, name); err != nil {
		return err
	}
	return st.differOnlyInCoordinate(ctx, fourth, fifth, name)
}

// definedOnceInEachOfSix is m9javaState.definedOnceInEachOfFive with a sixth
// language, which is the collision the six-language corpus is built around.
func (st *m9csState) definedOnceInEachOfSix(ctx context.Context, name, first, second, third, fourth, fifth, sixth string) error {
	if err := st.definedOnceInEachOfFive(ctx, name, first, second, third, fourth, fifth); err != nil {
		return err
	}
	return st.definedOnceInEachLanguage(ctx, name, fifth, sixth)
}

// sixLanguagesPresent is "one graph, six languages" stated over the whole `file`
// table: all six tags are there, and all six actually contributed definitions
// rather than merely being registered as files.
func (st *m9csState) sixLanguagesPresent(ctx context.Context, first, second, third, fourth, fifth, sixth string) error {
	if err := st.fiveLanguagesPresent(ctx, first, second, third, fourth, fifth); err != nil {
		return err
	}
	return st.bothLanguagesPresent(ctx, fifth, sixth)
}

// declaredInFilesUnderOneDescriptor is the partial-class claim, stated as the
// thing that has to remain true of it: `partial class Loud` in two files is two
// definition rows in two files carrying *one* descriptor.
//
// The descriptor is a coordinate and a structural path and says nothing about
// which file a symbol was written in, so the collision is not something the link
// pass survives — it is what makes two sites of one type read as one symbol. If
// the stanza ever started disambiguating them, this step is what would say so.
//
// SQL because a traversal cannot ask it: two definitions share a descriptor, so
// a query root filtered on the name returns both in an order the GraphQL layer
// does not promise.
func (st *m9csState) declaredInFilesUnderOneDescriptor(ctx context.Context, name string, want int) error {
	var rows, descriptors, files int
	err := pool.QueryRow(ctx, `
		SELECT count(*), count(DISTINCT o.descriptor), count(DISTINCT o.file_id)
		FROM occurrence o
		WHERE o.name = $1 AND o.role = 'definition' AND right(o.descriptor, 1) = '#'`, name).
		Scan(&rows, &descriptors, &files)
	if err != nil {
		return fmt.Errorf("count the declarations of %s: %w", name, err)
	}
	return check(func(t assert.TestingT) {
		assert.Equal(t, want, rows, "type definitions named %s", name)
		assert.Equal(t, want, files, "files declaring %s", name)
		assert.Equal(t, 1, descriptors, "%s is one type and must render one descriptor", name)
	})
}

// implementedFromEveryDeclaringFile is the half of the partial-class decision
// that could have gone wrong, and the reason the shared descriptor is what link
// wants rather than something it tolerates.
//
// `implements` gathers a type's method set by descriptor prefix over the whole
// occurrence table with no file predicate (store/sqlc/query.sql), so both halves
// of a partial class see the union of what either declares. `ILoud` asks for
// `Shout()` and `Whisper()` and neither file declares both — so an `implements`
// edge from every declaring file is the evidence the union was taken, and a
// per-file method set would have produced none at all.
func (st *m9csState) implementedFromEveryDeclaringFile(ctx context.Context, iface, impl string) error {
	var declaring, implementing int
	err := pool.QueryRow(ctx, `
		SELECT count(DISTINCT o.file_id)
		FROM occurrence o
		WHERE o.name = $1 AND o.role = 'definition' AND right(o.descriptor, 1) = '#'`, impl).Scan(&declaring)
	if err != nil {
		return fmt.Errorf("count the files declaring %s: %w", impl, err)
	}
	err = pool.QueryRow(ctx, `
		SELECT count(DISTINCT s.file_id)
		FROM implements e
		JOIN occurrence s ON s.id = e.source_id
		JOIN occurrence t ON t.id = e.target_id
		WHERE s.name = $1 AND t.name = $2 AND t.role = 'definition'`, impl, iface).Scan(&implementing)
	if err != nil {
		return fmt.Errorf("count the files implementing %s: %w", iface, err)
	}
	return check(func(t assert.TestingT) {
		assert.Positive(t, declaring, "files declaring %s", impl)
		assert.Equal(t, declaring, implementing,
			"%s must implement %s from every file it is declared in: the method set is the union of both", impl, iface)
	})
}
