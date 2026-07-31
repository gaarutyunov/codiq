// M6's feature suite: features/index_ts.feature against the real stack
// (SPEC.md §13, §14 M6).
//
// There is no new harness and no new stack. m1_test.go's startStack brings up
// the one composition the package shares — postgres:19beta2, the committed
// migrations applied by the real `gopgql migrate`, and `gopgql-mcp` over
// streamable HTTP — and m2_test.go's m2State is *embedded* below, so the module
// copy, the `codiq` invocation, the graph reset and M1's MCP handshake are
// literally M2's code running against a TypeScript corpus.
//
// That reuse is the point rather than a convenience. M6 asserts that a second
// language needed nothing new (SPEC.md §14 M6), and a suite that had to build
// its own indexing machinery to test TypeScript would have quietly disproved it.
// What this file adds is two corpora and a handful of assertions; if any of it
// had needed a second loader, a second store or a second query path, that would
// be the milestone's answer. One thing did need changing, and it is named where
// it belongs — in features/index_ts.feature's header and in coord's doc comment:
// a coordinate is a property of (repository, ecosystem), and index used to
// resolve one per repository.
//
// Five steps are SQL rather than MCP, and each is a claim the GraphQL surface
// cannot express: `imports` is file → file and File is not selectable by path
// (schema/codiq.graphql says so explicitly, and says why); "definitions exist in
// both languages" and "Greeter is defined once in each" are facts about the
// whole `file` table; and the two "no derived edge joins …" steps are statements
// about the absence of a row anywhere, which a traversal can never return. Every
// navigation claim goes over MCP.
package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cucumber/godog"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
)

// tsGreeterFixture is the TypeScript corpus, relative to the repository root:
// the package extract/ts/testdata/greeter/ the unit tests already use. Copied,
// never indexed in place — one scenario adds a file to it.
var tsGreeterFixture = filepath.Join("extract", "ts", "testdata", "greeter")

// TestM6Features is the godog entry point for M6, scoped to its own feature file
// so that each milestone's suite owns the scenarios it has steps for.
func TestM6Features(t *testing.T) {
	ctx := context.Background()
	repoRoot = mustRepoRoot(t)

	startStack(t, ctx)

	suite := godog.TestSuite{
		Name:                "m6",
		ScenarioInitializer: InitializeM6Scenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{filepath.Join(repoRoot, "features", "index_ts.feature")},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("m6 feature scenarios failed")
	}
}

// mixedCorpus is SPEC.md §14 M6's literal test corpus: one repository holding a
// go.mod and a package.json, a Go package in greeter/ and a greeter.ts beside
// it, each declaring a type called Greeter and each called from a main of its
// own language.
//
// Written out here rather than copied from a testdata directory, because the
// collision *is* the fixture: `greeter.ts` and the Go package `greeter/` derive
// the same namespace, so everything in the two descriptors after the coordinate
// is byte-identical, and a corpus assembled from the two languages' separate
// fixtures could not arrange that. It also keeps extract/*/testdata as what the
// unit tests use, which is the rule the TypeScript fixture already follows.
var mixedCorpus = map[string]string{
	"go.mod":       "module github.com/foo/bar\n\ngo 1.25.0\n",
	"package.json": `{"name": "@codiq/mixed", "version": "2.0.0"}` + "\n",
	"greeter/greeter.go": `package greeter

type Greeter struct {
	Name string
}

func (g *Greeter) Greet() string { return "hello, " + g.Name }
`,
	"main.go": `package main

import (
	"fmt"

	"github.com/foo/bar/greeter"
)

func main() {
	g := &greeter.Greeter{Name: "world"}
	fmt.Println(g.Greet())
}
`,
	"greeter.ts": `export class Greeter {
  constructor(private readonly name: string) {}

  greet(): string {
    return "hello, " + this.name;
  }
}
`,
	"main.ts": `import { Greeter } from "./greeter";

export function main(): string {
  const g = new Greeter("world");
  return g.greet();
}
`,
}

// m6State is one scenario's state: M2's, plus the second tree the two-repository
// scenario indexes.
//
// The second tree is not a workaround for the mixed-language case any more —
// index resolves one coordinate per ecosystem since M6, and the mixed scenario
// indexes a single repository holding both manifests. It is kept because two
// runs over two roots is a different claim: that is how a graph spanning several
// repositories is built.
type m6State struct {
	m2State

	// other is a second tree this scenario indexes into the same graph.
	other string
}

func InitializeM6Scenario(sc *godog.ScenarioContext) {
	st := &m6State{}

	// The same real handshake every suite does, once per scenario.
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "codiq-m6-tests", Version: "0.1.0"}, nil)
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
	sc.Step(`^the two-file TypeScript package$`, st.thePackage)
	sc.Step(`^the package also holds "([^"]*)":$`, st.moduleAlsoHolds)
	sc.Step(`^the package is indexed$`, st.moduleIndexed)
	sc.Step(`^the Go module and the TypeScript package$`, st.bothTrees)
	sc.Step(`^both are indexed into the same graph$`, st.bothIndexed)
	sc.Step(`^the mixed-language repository$`, st.theMixedRepository)
	sc.Step(`^the repository is indexed$`, st.moduleIndexed)
	sc.Step(`^"([^"]*)" imports "([^"]*)"$`, st.fileImports)
	sc.Step(`^"([^"]*)" is defined once in "([^"]*)" and once in "([^"]*)"$`, st.definedOnceInEachLanguage)
	sc.Step(`^the graph holds definitions written in both "([^"]*)" and "([^"]*)"$`, st.bothLanguagesPresent)
	sc.Step(`^no derived edge joins two package schemes$`, st.noCrossSchemeEdges)
	sc.Step(`^no derived edge joins two languages$`, st.noCrossLanguageEdges)

	// M1's steps, reused as written: the MCP tool call and the JSON comparison.
	sc.Step(`^an agent asks over MCP:$`, st.agentAsks)
	sc.Step(`^the answer is:$`, st.answerIs)
}

// --- given -----------------------------------------------------------------

// thePackage copies the TypeScript fixture into a temp directory, the way
// m2State.theModule copies the Go one and for the same reason: one scenario adds
// a file to it, and extract/ts/testdata is the unit tests' fixture.
func (st *m6State) thePackage() error {
	dir, err := os.MkdirTemp("", "codiq-m6-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	st.repo = filepath.Join(dir, "greeter")
	return copyTree(filepath.Join(repoRoot, tsGreeterFixture), st.repo)
}

// bothTrees lays down the two single-language corpora side by side, each with
// its own manifest and so its own coordinate.
func (st *m6State) bothTrees() error {
	if err := st.thePackage(); err != nil {
		return err
	}
	st.other = filepath.Join(st.tmp, "gomod")
	return copyTree(filepath.Join(repoRoot, greeterFixture), st.other)
}

// theMixedRepository writes the mixed corpus into a temp directory, the way the
// other Given steps copy a fixture into one — the same tmp root, so the After
// hook cleans up all three the same way.
func (st *m6State) theMixedRepository() error {
	dir, err := os.MkdirTemp("", "codiq-m6-mixed-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	st.repo = filepath.Join(dir, "mixed")
	for rel, body := range mixedCorpus {
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

// --- when ------------------------------------------------------------------

// bothIndexed runs the same binary over each tree in turn, into the same
// database. Two runs and not one because there are two repository roots — a
// single root holding two manifests is one run, which is what the mixed
// scenario does.
func (st *m6State) bothIndexed(ctx context.Context) error {
	if err := st.moduleIndexed(ctx); err != nil {
		return err
	}
	if st.other == "" {
		return errors.New("no second tree; the Given step did not run")
	}
	st.repo, st.other = st.other, st.repo
	err := st.moduleIndexed(ctx)
	st.repo, st.other = st.other, st.repo
	return err
}

// --- then ------------------------------------------------------------------

// fileImports reads the derived file → file edge. SQL because the surface cannot
// ask it: `File` has no key on `path` (schema/codiq.graphql), so there is no
// query root that selects one file and walks its imports.
func (st *m6State) fileImports(ctx context.Context, from, to string) error {
	var edges int
	err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM imports e
		JOIN file s ON s.id = e.source_id
		JOIN file t ON t.id = e.target_id
		WHERE s.path = $1 AND t.path = $2`, from, to).Scan(&edges)
	if err != nil {
		return fmt.Errorf("read imports %s -> %s: %w", from, to, err)
	}
	return check(func(t assert.TestingT) {
		assert.Equal(t, 1, edges, "import edges from %s to %s", from, to)
	})
}

// bothLanguagesPresent is "one graph, two languages" stated over the whole
// `file` table: both tags are there, and both actually contributed definitions
// rather than merely being registered as files.
func (st *m6State) bothLanguagesPresent(ctx context.Context, first, second string) error {
	counts := map[string]int{}
	for _, lang := range []string{first, second} {
		var defs int
		err := pool.QueryRow(ctx, `
			SELECT count(*)
			FROM occurrence o
			JOIN file f ON f.id = o.file_id
			WHERE f.lang = $1 AND o.role = 'definition'`, lang).Scan(&defs)
		if err != nil {
			return fmt.Errorf("count %s definitions: %w", lang, err)
		}
		counts[lang] = defs
	}
	return check(func(t assert.TestingT) {
		assert.Positive(t, counts[first], "definitions extracted from %s files", first)
		assert.Positive(t, counts[second], "definitions extracted from %s files", second)
	})
}

// definedOnceInEachLanguage is the mixed repository's collision, stated as the
// thing that has to remain true of it: the same name is declared in both
// languages, each declaration is there exactly once, and neither swallowed the
// other.
//
// It counts rather than traverses because the two rows are the point — a query
// root filtered on the name returns both, in an order the GraphQL layer does not
// promise, so the claim is not one an MCP read can make.
func (st *m6State) definedOnceInEachLanguage(ctx context.Context, name, first, second string) error {
	counts := map[string]int{}
	for _, lang := range []string{first, second} {
		var defs int
		err := pool.QueryRow(ctx, `
			SELECT count(*)
			FROM occurrence o
			JOIN file f ON f.id = o.file_id
			WHERE o.name = $1 AND o.role = 'definition' AND f.lang = $2`, name, lang).Scan(&defs)
		if err != nil {
			return fmt.Errorf("count %s definitions of %s: %w", lang, name, err)
		}
		counts[lang] = defs
	}
	return check(func(t assert.TestingT) {
		assert.Equal(t, 1, counts[first], "%s definitions of %s", first, name)
		assert.Equal(t, 1, counts[second], "%s definitions of %s", second, name)
	})
}

// noCrossLanguageEdges is noCrossSchemeEdges's stronger sibling, and the one the
// mixed repository needs.
//
// A scheme comparison catches an edge between two *correctly stamped* languages.
// It cannot catch the defect this scenario exists for: index used to resolve one
// coordinate per repository, so a TypeScript file in a mixed tree was stamped
// `scip-go gomod …` — and the phantom `resolves_to` edge from main.go's
// reference to greeter.ts's class then had the same scheme on both ends and
// would have passed unnoticed. Comparing the *files'* languages asks the
// question the descriptor cannot once the descriptor is already wrong.
//
// The two are kept side by side rather than one replacing the other: they fail
// for different reasons, and a suite that only asked the stronger one would stop
// saying that the coordinate prefix is what separates the ecosystems.
func (st *m6State) noCrossLanguageEdges(ctx context.Context) error {
	crossing := map[string]int{}
	total := 0
	for _, table := range []string{"resolves_to", "calls", "implements", "type_defines"} {
		var n, all int
		// table is one of this file's own constants, never scenario input.
		err := pool.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE sf.lang <> tf.lang), count(*)
			FROM `+table+` e
			JOIN occurrence s ON s.id = e.source_id
			JOIN occurrence t ON t.id = e.target_id
			JOIN file sf ON sf.id = s.file_id
			JOIN file tf ON tf.id = t.file_id`).Scan(&n, &all)
		if err != nil {
			return fmt.Errorf("scan %s for cross-language edges: %w", table, err)
		}
		crossing[table] = n
		total += all
	}

	var importsCrossing int
	err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM imports e
		JOIN file s ON s.id = e.source_id
		JOIN file t ON t.id = e.target_id
		WHERE s.lang <> t.lang`).Scan(&importsCrossing)
	if err != nil {
		return fmt.Errorf("scan imports for cross-language edges: %w", err)
	}
	crossing["imports"] = importsCrossing

	return check(func(t assert.TestingT) {
		assert.Positive(t, total, "derived occurrence edges to check")
		for table, n := range crossing {
			assert.Zerof(t, n, "%s edges joining two languages", table)
		}
	})
}

// noCrossSchemeEdges is the invariant that makes two ecosystems safe in one
// graph. The link pass joins on the rendered descriptor and nothing else, so the
// only thing stopping a TypeScript symbol from resolving to a Go one is that
// their coordinate prefixes cannot collide — and the first field of a descriptor
// is the scheme. A single row here would mean a navigation answer that is not
// real, so the claim is "not one, anywhere", over every derived table at once.
func (st *m6State) noCrossSchemeEdges(ctx context.Context) error {
	crossing := map[string]int{}
	total := 0
	for _, table := range []string{"resolves_to", "calls", "implements", "type_defines"} {
		var n, all int
		// table is one of this file's own constants, never scenario input.
		err := pool.QueryRow(ctx, `
			SELECT
				count(*) FILTER (WHERE split_part(s.descriptor, ' ', 1) <> split_part(t.descriptor, ' ', 1)),
				count(*)
			FROM `+table+` e
			JOIN occurrence s ON s.id = e.source_id
			JOIN occurrence t ON t.id = e.target_id`).Scan(&n, &all)
		if err != nil {
			return fmt.Errorf("scan %s for cross-scheme edges: %w", table, err)
		}
		crossing[table] = n
		total += all
	}

	var importsCrossing int
	err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM imports e
		JOIN file s ON s.id = e.source_id
		JOIN file t ON t.id = e.target_id
		WHERE s.pkg_scheme <> t.pkg_scheme`).Scan(&importsCrossing)
	if err != nil {
		return fmt.Errorf("scan imports for cross-scheme edges: %w", err)
	}
	crossing["imports"] = importsCrossing

	return check(func(t assert.TestingT) {
		assert.Positive(t, total, "derived occurrence edges to check")
		for table, n := range crossing {
			assert.Zerof(t, n, "%s edges joining two package schemes", table)
		}
	})
}
