// The Rust feature suite: features/index_rs.feature against the real stack
// (SPEC.md §13, §14 M9+).
//
// The file is named m9rs_test.go rather than m9_test.go because §14 M9+ is not
// one milestone: it is a bag of independent language tasks with no ordering
// between them, so Java and C# will want m9java_test.go and m9cs_test.go beside
// this one rather than to grow it.
//
// There is no new harness and no new stack — for the fourth time, which is the
// point rather than a convenience. m1_test.go's startStack brings up the one
// composition the package shares; m2_test.go's m2State carries the module copy,
// the `codiq` invocation, the graph reset and M1's MCP handshake; and m7_test.go
// is embedded whole, so M6's "no derived edge joins …" checks and M7's
// three-language steps are called rather than reimplemented. A suite that had to
// build its own indexing machinery to test Rust would have quietly disproved the
// claim the milestone exists to make.
//
// What this file adds is two corpora and three steps, and all three of the steps
// are M7's generalised from three languages to four. That is the whole diff, and
// it is the measurement: the fourth language cost a sub-package, a query, a
// resolver and a `byExt` entry, and no core package changed.
//
// The claims split the way they have in every suite before this one. Every
// navigation claim goes over MCP. The rest are SQL, because the GraphQL surface
// cannot express them: `imports` is file → file and File is not selectable by
// path (schema/codiq.graphql says so explicitly, and says why); "definitions
// exist in four languages" and "Greeter is defined once in each" are facts about
// the whole `file` table; and "no derived edge joins …" is a statement about the
// absence of a row anywhere, which a traversal can never return.
package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cucumber/godog"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// rsGreeterFixture is the Rust corpus, relative to the repository root: the
// crate extract/rs/testdata/greeter/ the unit tests already use. Copied, never
// indexed in place — three scenarios add files to it.
var rsGreeterFixture = filepath.Join("extract", "rs", "testdata", "greeter")

// TestM9RustFeatures is the godog entry point for the Rust task, scoped to its
// own feature file so that each milestone's suite owns the scenarios it has
// steps for.
func TestM9RustFeatures(t *testing.T) {
	ctx := context.Background()
	repoRoot = mustRepoRoot(t)

	startStack(t, ctx)

	suite := godog.TestSuite{
		Name:                "m9rs",
		ScenarioInitializer: InitializeM9RustScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{filepath.Join(repoRoot, "features", "index_rs.feature")},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("m9rs feature scenarios failed")
	}
}

// fourLanguageCorpus is the inherited §14 M6/M7 test corpus with a fourth
// language, and a strictly harder one than M7's: one repository holding a
// go.mod, a package.json, a pyproject.toml and a Cargo.toml, with a Go package
// in greeter/, a greeter.ts and a greeter.py beside it and a src/greeter.rs
// under them, each declaring a type called Greeter and each called from an entry
// point of its own language.
//
// Written out here rather than assembled from the four testdata directories,
// because the collision *is* the fixture. All four derive the namespace
// `greeter/` — Rust's from `src/greeter.rs`, since `src/` is Cargo's layout and
// not a level of the module tree — so every descriptor of the type is identical
// after the coordinate; and the TypeScript, Python and Rust methods are all
// spelled `greet`, so three of the four render `greeter/Greeter#greet().` byte
// for byte. Four separate fixtures could not arrange that, and it also keeps
// extract/*/testdata as what the unit tests use.
//
// The four entry points are named apart — `main`, `boot`, `run`, `start` — for
// the reason the feature file gives: the GraphQL layer emits no ORDER BY, so
// each MCP read has to match exactly one row, and the traversals start from the
// callers rather than from the colliding callees.
//
// The Rust caller builds its Greeter with a struct literal rather than through
// an associated function, which is the one place the corpus is written for the
// assertion rather than for idiom: `Greeter::new(…)` would resolve too — a `::`
// path is exact — and `start` would then have two cross-file `calls` edges in an
// order the GraphQL layer does not promise.
var fourLanguageCorpus = map[string]string{
	"go.mod":         "module github.com/foo/bar\n\ngo 1.25.0\n",
	"package.json":   `{"name": "@codiq/mixed", "version": "2.0.0"}` + "\n",
	"pyproject.toml": "[project]\nname = \"codiq-mixed\"\nversion = \"3.0.0\"\n",
	"Cargo.toml":     "[package]\nname = \"codiq-mixed-rs\"\nversion = \"4.0.0\"\n",

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

	"src/greeter.rs": `pub struct Greeter {
    pub name: String,
}

impl Greeter {
    pub fn greet(&self) -> String {
        let mut out = String::from("hello, ");
        out.push_str(&self.name);
        out
    }
}
`,
	"src/lib.rs": `mod greeter;

use crate::greeter::Greeter;

pub fn start() -> String {
    let g = Greeter { name: String::from("world") };
    g.greet()
}
`,
}

// m9rsState is one scenario's state: M7's, which is M6's, which is M2's plus the
// second-tree machinery. Nothing is added — the corpora are on disk and the
// assertions are stateless — and that is itself part of what the task claims.
type m9rsState struct {
	m7State
}

func InitializeM9RustScenario(sc *godog.ScenarioContext) {
	st := &m9rsState{}

	// The same real handshake every suite does, once per scenario.
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "codiq-m9rs-tests", Version: "0.1.0"}, nil)
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
	sc.Step(`^the two-file Rust crate$`, st.theRustCrate)
	sc.Step(`^the crate also holds "([^"]*)":$`, st.packageAlsoHolds)
	sc.Step(`^the crate is indexed$`, st.moduleIndexed)
	sc.Step(`^the mixed-language repository of four languages$`, st.theFourLanguageRepository)
	sc.Step(`^the repository is indexed$`, st.moduleIndexed)
	sc.Step(`^"([^"]*)" imports "([^"]*)"$`, st.fileImports)
	sc.Step(`^the "([^"]*)", "([^"]*)" and "([^"]*)" definitions of "([^"]*)" differ only in their coordinate$`, st.threeDifferOnlyInCoordinate)
	sc.Step(`^"([^"]*)" is defined once in each of "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)"$`, st.definedOnceInEachOfFour)
	sc.Step(`^the graph holds definitions written in all of "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)"$`, st.fourLanguagesPresent)
	sc.Step(`^no derived edge joins two package schemes$`, st.noCrossSchemeEdges)
	sc.Step(`^no derived edge joins two languages$`, st.noCrossLanguageEdges)

	// M1's steps, reused as written: the MCP tool call and the JSON comparison.
	sc.Step(`^an agent asks over MCP:$`, st.agentAsks)
	sc.Step(`^the answer is:$`, st.answerIs)
}

// --- given -----------------------------------------------------------------

// theRustCrate copies the Rust fixture into a temp directory, the way
// m2State.theModule copies the Go one and for the same reason: three scenarios
// add files to it, and extract/rs/testdata is the unit tests' fixture.
func (st *m9rsState) theRustCrate() error {
	dir, err := os.MkdirTemp("", "codiq-m9rs-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	st.repo = filepath.Join(dir, "greeter")
	return copyTree(filepath.Join(repoRoot, rsGreeterFixture), st.repo)
}

// theFourLanguageRepository writes the four-language corpus into a temp
// directory, the way the other Given step copies a fixture into one — the same
// tmp root, so the After hook cleans up both the same way.
func (st *m9rsState) theFourLanguageRepository() error {
	dir, err := os.MkdirTemp("", "codiq-m9rs-mixed-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	st.repo = filepath.Join(dir, "mixed")
	for rel, body := range fourLanguageCorpus {
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

// threeDifferOnlyInCoordinate is m7State.differOnlyInCoordinate over the three
// languages that share a suffix, which is what makes the mixed scenario's other
// assertions mean anything. M7 could state it of two; here `greet` is written by
// TypeScript, Python and Rust and all three render the same descriptor after the
// coordinate, so the pairwise claim has to hold across the chain.
func (st *m9rsState) threeDifferOnlyInCoordinate(ctx context.Context, first, second, third, name string) error {
	if err := st.differOnlyInCoordinate(ctx, first, second, name); err != nil {
		return err
	}
	return st.differOnlyInCoordinate(ctx, second, third, name)
}

// definedOnceInEachOfFour is m7State.definedOnceInEachOfThree with a fourth
// language, which is the collision the four-language corpus is built around.
func (st *m9rsState) definedOnceInEachOfFour(ctx context.Context, name, first, second, third, fourth string) error {
	if err := st.definedOnceInEachOfThree(ctx, name, first, second, third); err != nil {
		return err
	}
	return st.definedOnceInEachLanguage(ctx, name, third, fourth)
}

// fourLanguagesPresent is "one graph, four languages" stated over the whole
// `file` table: all four tags are there, and all four actually contributed
// definitions rather than merely being registered as files.
func (st *m9rsState) fourLanguagesPresent(ctx context.Context, first, second, third, fourth string) error {
	if err := st.threeLanguagesPresent(ctx, first, second, third); err != nil {
		return err
	}
	return st.bothLanguagesPresent(ctx, third, fourth)
}
