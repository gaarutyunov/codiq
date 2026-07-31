// The Ruby feature suite: features/index_rb.feature against the real stack
// (SPEC.md §13, §14 M9+).
//
// The file is named m9rb_test.go rather than m9_test.go for the reason
// m9rs_test.go gives and the two suites after it repeat: §14 M9+ is not one
// milestone but a bag of independent language tasks with no ordering between
// them, so PHP will want m9php_test.go beside this one rather than to grow it.
//
// There is no new harness and no new stack — for the seventh time, which is the
// point rather than a convenience. m1_test.go's startStack brings up the one
// composition the package shares; m2_test.go's m2State carries the module copy,
// the `codiq` invocation, the graph reset and M1's MCP handshake; and
// m9cs_test.go is embedded whole, so M6's "no derived edge joins …" checks, C#'s
// two shared-descriptor steps and every generalisation the five suites before it
// made are called rather than reimplemented.
//
// What this file adds is two corpora and four steps. Three of the four are the
// C# suite's generalised from six languages to seven; the fourth is Ruby's own
// and is the first step in this package that asserts a derivation produces
// *nothing*, which is what `implements` does here and why. That is the whole
// diff, and it is the measurement: the seventh language cost a sub-package, a
// query, a resolver and a `byExt` entry, and no core package changed.
//
// The claims split the way they have in every suite before this one. Every
// navigation claim goes over MCP. The rest are SQL, because the GraphQL surface
// cannot express them: `imports` is file → file and File is not selectable by
// path; "definitions exist in seven languages" and "Greeter is defined once in
// each" are facts about the whole `file` table; "no derived edge joins …" and "no
// implements edge has a Ruby endpoint" are statements about the absence of a row
// anywhere; and a reopened class's two definition rows share a descriptor, so a
// traversal reaching them returns them in an order the GraphQL layer does not
// promise.
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

// rbGreeterFixture is the Ruby corpus, relative to the repository root: the gem
// extract/rb/testdata/greeter/ the unit tests already use. Copied, never indexed
// in place — two scenarios add files to it.
var rbGreeterFixture = filepath.Join("extract", "rb", "testdata", "greeter")

// TestM9RBFeatures is the godog entry point for the Ruby task, scoped to its own
// feature file so that each milestone's suite owns the scenarios it has steps
// for.
func TestM9RBFeatures(t *testing.T) {
	ctx := context.Background()
	repoRoot = mustRepoRoot(t)

	startStack(t, ctx)

	suite := godog.TestSuite{
		Name:                "m9rb",
		ScenarioInitializer: InitializeM9RBScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{filepath.Join(repoRoot, "features", "index_rb.feature")},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("m9rb feature scenarios failed")
	}
}

// rbCorpusFiles is the seventh language's half of the mixed corpus: a Gemfile
// with the lock beside it, a `Greeting` module declaring the type, and an `App`
// module that builds one and calls it.
//
// It is the one half of this corpus that is *not* written to collide, and that is
// forced rather than chosen. The other six each declare a namespace spelled
// `greeter` — two of them against their own language's conventions, which
// index_cs.feature says so in as many words — and Ruby has no such declaration to
// write: `module greeter` is a syntax error, because a module name is a
// `constant` token by the grammar. So the suffix is `Greeting/Greeter#greet().`
// and only the four leading words of the coordinate would have had to separate it
// from the other six anyway.
//
// The caller passes a constructor argument rather than assigning through an
// accessor, which is the opposite of the Java, Rust and C# corpora's choice and
// for the mirror-image reason. Those three avoided a constructor because it is a
// member named after its type; Ruby's is called `initialize` and collides with
// nothing, while `attr_accessor :name` would generate a `name=` *method* that the
// caller's `g.name = "world"` would reach — putting a second entry in `calls` and
// making the traversal below match two rows in an order nothing promises.
var rbCorpusFiles = map[string]string{
	"Gemfile": "source \"https://rubygems.org\"\n\ngemspec\n",
	"Gemfile.lock": `PATH
  remote: .
  specs:
    codiq-mixed-rb (7.0.0)

GEM
  remote: https://rubygems.org/
  specs:

PLATFORMS
  ruby

DEPENDENCIES
  codiq-mixed-rb!

BUNDLED WITH
   2.5.9
`,

	"greeter.rb": `module Greeting
  class Greeter
    def initialize(name)
      @name = name
    end

    def greet
      "hello, #{@name}"
    end
  end
end
`,
	"bootstrap.rb": `require_relative "greeter"

module App
  class Bootstrap
    def self.commence
      g = Greeting::Greeter.new("world")
      g.greet
    end
  end
end
`,
}

// sevenLanguageCorpus is the C# task's six-language corpus with a seventh
// language added, and it is assembled rather than rewritten precisely because
// that is the claim: the six earlier languages' files are byte-for-byte the ones
// m9cs_test.go already indexes, so anything this scenario finds that the C# one
// did not is Ruby's doing.
var sevenLanguageCorpus = func() map[string]string {
	out := make(map[string]string, len(sixLanguageCorpus)+len(rbCorpusFiles))
	maps.Copy(out, sixLanguageCorpus)
	maps.Copy(out, rbCorpusFiles)
	return out
}()

// m9rbState is one scenario's state: the C# task's, which is the Java task's,
// which is the Rust task's, which is M7's, which is M6's, which is M2's plus the
// second-tree machinery. Nothing is added — the corpora are on disk and the
// assertions are stateless — and that is itself part of what the task claims.
type m9rbState struct {
	m9csState
}

func InitializeM9RBScenario(sc *godog.ScenarioContext) {
	st := &m9rbState{}

	// The same real handshake every suite does, once per scenario.
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "codiq-m9rb-tests", Version: "0.1.0"}, nil)
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
	sc.Step(`^the two-file Ruby gem$`, st.theRubyGem)
	sc.Step(`^the artifact also holds "([^"]*)":$`, st.packageAlsoHolds)
	sc.Step(`^the artifact is indexed$`, st.moduleIndexed)
	sc.Step(`^the mixed-language repository of seven languages$`, st.theSevenLanguageRepository)
	sc.Step(`^the repository is indexed$`, st.moduleIndexed)
	sc.Step(`^"([^"]*)" imports "([^"]*)"$`, st.fileImports)
	sc.Step(`^the "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)" definitions of "([^"]*)" differ only in their coordinate$`, st.fiveDifferOnlyInCoordinate)
	sc.Step(`^"([^"]*)" is defined once in each of "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)"$`, st.definedOnceInEachOfSeven)
	sc.Step(`^the graph holds definitions written in all of "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)"$`, st.sevenLanguagesPresent)
	sc.Step(`^no derived edge joins two package schemes$`, st.noCrossSchemeEdges)
	sc.Step(`^no derived edge joins two languages$`, st.noCrossLanguageEdges)

	// C#'s, reused as written: a reopened Ruby class is a partial class with the
	// guard rails taken off, and the claim about it is the same claim.
	sc.Step(`^"([^"]*)" is declared in (\d+) files under one descriptor$`, st.declaredInFilesUnderOneDescriptor)

	// Ruby's own, and the first step in this package that asserts a derivation
	// produces nothing.
	sc.Step(`^no "([^"]*)" definition takes part in an implements edge$`, st.noImplementsEdgesFor)

	// M1's steps, reused as written: the MCP tool call and the JSON comparison.
	sc.Step(`^an agent asks over MCP:$`, st.agentAsks)
	sc.Step(`^the answer is:$`, st.answerIs)
}

// --- given -----------------------------------------------------------------

// theRubyGem copies the Ruby fixture into a temp directory, the way
// m2State.theModule copies the Go one and for the same reason: two scenarios add
// files to it, and extract/rb/testdata is the unit tests' fixture.
func (st *m9rbState) theRubyGem() error {
	dir, err := os.MkdirTemp("", "codiq-m9rb-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	st.repo = filepath.Join(dir, "greeter")
	return copyTree(filepath.Join(repoRoot, rbGreeterFixture), st.repo)
}

// theSevenLanguageRepository writes the seven-language corpus into a temp
// directory, the way the other Given step copies a fixture into one — the same
// tmp root, so the After hook cleans up both the same way.
func (st *m9rbState) theSevenLanguageRepository() error {
	dir, err := os.MkdirTemp("", "codiq-m9rb-mixed-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	st.repo = filepath.Join(dir, "mixed")
	for rel, body := range sevenLanguageCorpus {
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

// definedOnceInEachOfSeven is m9csState.definedOnceInEachOfSix with a seventh
// language. Ruby does not join the suffix collision, so this is the claim the
// mixed corpus still makes of it in full: the name is declared once in each of
// the seven, and no language's definition swallowed another's.
func (st *m9rbState) definedOnceInEachOfSeven(ctx context.Context, name, first, second, third, fourth, fifth, sixth, seventh string) error {
	if err := st.definedOnceInEachOfSix(ctx, name, first, second, third, fourth, fifth, sixth); err != nil {
		return err
	}
	return st.definedOnceInEachLanguage(ctx, name, sixth, seventh)
}

// sevenLanguagesPresent is "one graph, seven languages" stated over the whole
// `file` table: all seven tags are there, and all seven actually contributed
// definitions rather than merely being registered as files.
func (st *m9rbState) sevenLanguagesPresent(ctx context.Context, first, second, third, fourth, fifth, sixth, seventh string) error {
	if err := st.sixLanguagesPresent(ctx, first, second, third, fourth, fifth, sixth); err != nil {
		return err
	}
	return st.bothLanguagesPresent(ctx, sixth, seventh)
}

// noImplementsEdgesFor is Ruby's own step, and the first in this package to
// assert that a derivation produces *nothing*.
//
// `implements` is method-set containment keyed off `symbol_kind = 'interface'`,
// and Ruby has no interfaces. The nearest thing — a module included as a mixin —
// is the inverse of one: a module's method set is what it gives, so a class that
// includes `Comparable` declares the `<=>` the module demands and none of the six
// it provides, and containment fails exactly where the include is real. The
// stanza therefore emits `interface` for nothing and leaves a module a `package`
// whose descriptor ends `/`, which link's `type_def` CTE does not select.
//
// The step is written as "no edge with a Ruby endpoint" rather than "the table is
// empty" so that it keeps meaning the same thing in a mixed graph, and it checks
// that Ruby derived *something* first — an emptiness claim over a language that
// contributed no rows at all would be true for the wrong reason.
func (st *m9rbState) noImplementsEdgesFor(ctx context.Context, lang string) error {
	var resolved, implementing int
	err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM resolves_to e
		JOIN occurrence o ON o.id = e.target_id
		JOIN file f ON f.id = o.file_id
		WHERE f.lang = $1`, lang).Scan(&resolved)
	if err != nil {
		return fmt.Errorf("count %s resolves_to edges: %w", lang, err)
	}
	err = pool.QueryRow(ctx, `
		SELECT count(*)
		FROM implements e
		JOIN occurrence o ON o.id IN (e.source_id, e.target_id)
		JOIN file f ON f.id = o.file_id
		WHERE f.lang = $1`, lang).Scan(&implementing)
	if err != nil {
		return fmt.Errorf("count %s implements edges: %w", lang, err)
	}
	return check(func(t assert.TestingT) {
		assert.Positive(t, resolved,
			"%s derived no cross-file edges at all, so the implements claim would be vacuous", lang)
		assert.Zero(t, implementing,
			"%s took part in an implements edge; a module's method set is what it gives, not what it demands", lang)
	})
}
