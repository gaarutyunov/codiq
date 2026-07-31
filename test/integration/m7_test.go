// M7's feature suite: features/index_py.feature against the real stack
// (SPEC.md §13, §14 M7).
//
// There is no new harness and no new stack — for the third time, which is the
// point rather than a convenience. m1_test.go's startStack brings up the one
// composition the package shares; m2_test.go's m2State carries the module copy,
// the `codiq` invocation, the graph reset and M1's MCP handshake; m6_test.go's
// steps for "definitions exist in both languages", "defined once in each" and
// the two "no derived edge joins …" checks are called directly rather than
// reimplemented. M7 asserts that a *third* language needed nothing new
// (SPEC.md §14 M7), and a suite that had to build its own indexing machinery to
// test Python would have quietly disproved it.
//
// What this file adds is two corpora and three steps. Two of the steps are M6's
// generalised from two languages to three; the third is new and is the one that
// makes the mixed scenario non-vacuous — it asserts that the TypeScript and
// Python descriptors of `greet` really are identical after the coordinate, so
// that "nothing joined them" is a statement about a collision that exists.
//
// The claims split the way they have in every suite before this one. Every
// navigation claim goes over MCP. The rest are SQL, because the GraphQL surface
// cannot express them: `imports` is file → file and File is not selectable by
// path (schema/codiq.graphql says so explicitly, and says why); "definitions
// exist in three languages" and "Greeter is defined once in each" are facts
// about the whole `file` table; and "no derived edge joins …" is a statement
// about the absence of a row anywhere, which a traversal can never return.
package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
)

// pyGreeterFixture is the Python corpus, relative to the repository root: the
// package extract/py/testdata/greeter/ the unit tests already use. Copied,
// never indexed in place — three scenarios add files to it.
var pyGreeterFixture = filepath.Join("extract", "py", "testdata", "greeter")

// TestM7Features is the godog entry point for M7, scoped to its own feature file
// so that each milestone's suite owns the scenarios it has steps for.
func TestM7Features(t *testing.T) {
	ctx := context.Background()
	repoRoot = mustRepoRoot(t)

	startStack(t, ctx)

	suite := godog.TestSuite{
		Name:                "m7",
		ScenarioInitializer: InitializeM7Scenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{filepath.Join(repoRoot, "features", "index_py.feature")},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("m7 feature scenarios failed")
	}
}

// threeLanguageCorpus is SPEC.md §14 M7's test corpus, and a strictly harder
// one than M6's: one repository holding a go.mod, a package.json and a
// pyproject.toml, with a Go package in greeter/ and a greeter.ts and a
// greeter.py beside it, each declaring a type called Greeter and each called
// from an entry point of its own language.
//
// Written out here rather than assembled from the three testdata directories,
// because the collision *is* the fixture. All three derive the namespace
// `greeter/`, so every descriptor of the type is identical after the
// coordinate; and the TypeScript and Python methods are both spelled `greet`,
// so two of the three render `greeter/Greeter#greet().` byte for byte. Three
// separate fixtures could not arrange that, and it also keeps extract/*/testdata
// as what the unit tests use.
//
// The three entry points are named apart — `main`, `boot`, `run` — for the
// reason the feature file gives: the GraphQL layer emits no ORDER BY, so each
// MCP read has to match exactly one row, and the traversals start from the
// callers rather than from the colliding callees.
var threeLanguageCorpus = map[string]string{
	"go.mod":         "module github.com/foo/bar\n\ngo 1.25.0\n",
	"package.json":   `{"name": "@codiq/mixed", "version": "2.0.0"}` + "\n",
	"pyproject.toml": "[project]\nname = \"codiq-mixed\"\nversion = \"3.0.0\"\n",

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

export function boot(): string {
  const g = new Greeter("world");
  return g.greet();
}
`,

	"greeter.py": `class Greeter:
    def __init__(self, name: str) -> None:
        self.name = name

    def greet(self) -> str:
        return "hello, " + self.name
`,
	"main.py": `from greeter import Greeter


def run() -> None:
    g = Greeter("world")
    print(g.greet())
`,
}

// m7State is one scenario's state: M6's, which is M2's plus the second-tree
// machinery. Nothing is added — the corpora are on disk and the assertions are
// stateless — and that is itself part of what the milestone claims.
type m7State struct {
	m6State
}

func InitializeM7Scenario(sc *godog.ScenarioContext) {
	st := &m7State{}

	// The same real handshake every suite does, once per scenario.
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "codiq-m7-tests", Version: "0.1.0"}, nil)
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
	sc.Step(`^the two-file Python package$`, st.thePythonPackage)
	sc.Step(`^the package also holds "([^"]*)":$`, st.packageAlsoHolds)
	sc.Step(`^the package is indexed$`, st.moduleIndexed)
	sc.Step(`^the mixed-language repository of three languages$`, st.theThreeLanguageRepository)
	sc.Step(`^the repository is indexed$`, st.moduleIndexed)
	sc.Step(`^"([^"]*)" imports "([^"]*)"$`, st.fileImports)
	sc.Step(`^the "([^"]*)" and "([^"]*)" definitions of "([^"]*)" differ only in their coordinate$`, st.differOnlyInCoordinate)
	sc.Step(`^"([^"]*)" is defined once in each of "([^"]*)", "([^"]*)" and "([^"]*)"$`, st.definedOnceInEachOfThree)
	sc.Step(`^the graph holds definitions written in all of "([^"]*)", "([^"]*)" and "([^"]*)"$`, st.threeLanguagesPresent)
	sc.Step(`^no derived edge joins two package schemes$`, st.noCrossSchemeEdges)
	sc.Step(`^no derived edge joins two languages$`, st.noCrossLanguageEdges)

	// M1's steps, reused as written: the MCP tool call and the JSON comparison.
	sc.Step(`^an agent asks over MCP:$`, st.agentAsks)
	sc.Step(`^the answer is:$`, st.answerIs)
}

// --- given -----------------------------------------------------------------

// thePythonPackage copies the Python fixture into a temp directory, the way
// m2State.theModule copies the Go one and for the same reason: three scenarios
// add files to it, and extract/py/testdata is the unit tests' fixture.
func (st *m7State) thePythonPackage() error {
	dir, err := os.MkdirTemp("", "codiq-m7-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	st.repo = filepath.Join(dir, "greeter")
	return copyTree(filepath.Join(repoRoot, pyGreeterFixture), st.repo)
}

// packageAlsoHolds is m2State.moduleAlsoHolds with directories created on the
// way. Python needs it and no earlier language did: a package is a directory
// holding `__init__.py`, so the one scenario that is about Python's unit of
// modularity has to be able to write into a subdirectory that is not there yet.
func (st *m7State) packageAlsoHolds(name string, body *godog.DocString) error {
	if st.repo == "" {
		return errors.New("no package; the Given step did not run")
	}
	path := filepath.Join(st.repo, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	content := body.Content
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

// theThreeLanguageRepository writes the three-language corpus into a temp
// directory, the way the other Given step copies a fixture into one — the same
// tmp root, so the After hook cleans up both the same way.
func (st *m7State) theThreeLanguageRepository() error {
	dir, err := os.MkdirTemp("", "codiq-m7-mixed-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	st.repo = filepath.Join(dir, "mixed")
	for rel, body := range threeLanguageCorpus {
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

// differOnlyInCoordinate is what makes the mixed scenario's other assertions
// mean anything.
//
// "No derived edge joins two languages" is trivially true over a corpus whose
// languages could never have collided, so this states the collision as a fact
// about the rows: the two definitions of `greet` carry descriptors that are
// equal in everything after the coordinate — the four leading space-separated
// components — and unequal in the whole string. If a change to a stanza's
// namespace rule ever pulls the two suffixes apart, the corpus stops being a
// test of anything and this step says so, rather than the scenario passing for
// the wrong reason.
func (st *m7State) differOnlyInCoordinate(ctx context.Context, first, second, name string) error {
	descriptors := map[string]string{}
	for _, lang := range []string{first, second} {
		var descriptor string
		err := pool.QueryRow(ctx, `
			SELECT o.descriptor
			FROM occurrence o
			JOIN file f ON f.id = o.file_id
			WHERE o.name = $1 AND o.role = 'definition' AND f.lang = $2`, name, lang).Scan(&descriptor)
		if err != nil {
			return fmt.Errorf("read the %s definition of %s: %w", lang, name, err)
		}
		descriptors[lang] = descriptor
	}

	// The descriptor is `scheme manager package version suffix`, so the suffix
	// is everything from the fifth space-separated field on (§4.3).
	suffix := func(d string) string {
		parts := strings.SplitN(d, " ", 5)
		if len(parts) < 5 {
			return ""
		}
		return parts[4]
	}

	return check(func(t assert.TestingT) {
		assert.Equal(t, suffix(descriptors[first]), suffix(descriptors[second]),
			"the corpus does not collide: %s and %s render different suffixes for %s", first, second, name)
		assert.NotEmpty(t, suffix(descriptors[first]), "a descriptor with no suffix cannot collide")
		assert.NotEqual(t, descriptors[first], descriptors[second],
			"two languages render the same descriptor for %s", name)
	})
}

// definedOnceInEachOfThree is m6State.definedOnceInEachLanguage with a third
// language, which is the collision the three-language corpus is built around.
func (st *m7State) definedOnceInEachOfThree(ctx context.Context, name, first, second, third string) error {
	if err := st.definedOnceInEachLanguage(ctx, name, first, second); err != nil {
		return err
	}
	return st.definedOnceInEachLanguage(ctx, name, second, third)
}

// threeLanguagesPresent is "one graph, three languages" stated over the whole
// `file` table: all three tags are there, and all three actually contributed
// definitions rather than merely being registered as files.
func (st *m7State) threeLanguagesPresent(ctx context.Context, first, second, third string) error {
	if err := st.bothLanguagesPresent(ctx, first, second); err != nil {
		return err
	}
	return st.bothLanguagesPresent(ctx, second, third)
}
