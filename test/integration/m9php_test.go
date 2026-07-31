// The PHP feature suite: features/index_php.feature against the real stack
// (SPEC.md §13, §14 M9+).
//
// The file is named m9php_test.go rather than m9_test.go for the reason
// m9rs_test.go gives and the three suites after it repeat: §14 M9+ is not one
// milestone but a bag of independent language tasks with no ordering between
// them, so Kotlin (#23) and Swift (#24) will want their own files beside this one
// rather than to grow it.
//
// There is no new harness and no new stack — for the eighth time, which is the
// point rather than a convenience. m1_test.go's startStack brings up the one
// composition the package shares; m2_test.go's m2State carries the module copy,
// the `codiq` invocation, the graph reset and M1's MCP handshake; and
// m9rb_test.go is embedded whole, so M6's "no derived edge joins …" checks and
// every generalisation the six suites before it made are called rather than
// reimplemented.
//
// What this file adds is two corpora and five steps. Three of the five are the
// Ruby suite's generalised from seven languages to eight; the other two are PHP's
// own, and they are a matched pair — one asserts that a class satisfying an
// interface only through a trait implements *nothing*, the other that the trait
// `use` that got it there is not an import either. Together they are the stanza's
// one deliberate false negative and the decision behind it, pinned so that
// neither is rediscovered as a bug. That is the whole diff, and it is the
// measurement: the eighth language cost a sub-package, a query, a resolver and a
// `byExt` entry, and no core package changed.
//
// The claims split the way they have in every suite before this one. Every
// navigation claim goes over MCP. The rest are SQL, because the GraphQL surface
// cannot express them: `imports` is file → file and File is not selectable by
// path; "definitions exist in eight languages" and "Greeter is defined once in
// each" are facts about the whole `file` table; and "no derived edge joins …",
// "Counter implements nothing" and "this file does not import that one" are all
// statements about the absence of a row anywhere.
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

// phpGreeterFixture is the PHP corpus, relative to the repository root: the
// Composer package extract/php/testdata/greeter/ the unit tests already use.
// Copied, never indexed in place — two scenarios add files to it.
var phpGreeterFixture = filepath.Join("extract", "php", "testdata", "greeter")

// TestM9PHPFeatures is the godog entry point for the PHP task, scoped to its own
// feature file so that each milestone's suite owns the scenarios it has steps
// for.
func TestM9PHPFeatures(t *testing.T) {
	ctx := context.Background()
	repoRoot = mustRepoRoot(t)

	startStack(t, ctx)

	suite := godog.TestSuite{
		Name:                "m9php",
		ScenarioInitializer: InitializeM9PHPScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{filepath.Join(repoRoot, "features", "index_php.feature")},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("m9php feature scenarios failed")
	}
}

// phpCorpusFiles is the eighth language's half of the mixed corpus: a
// `composer.json` beside the seven manifests already there, a `greeter` namespace
// declaring the type, and an `app` namespace that builds one and calls it.
//
// It is written to collide as hard as the six that share a suffix, and it is the
// second half of this corpus written against its own language's conventions
// rather than with them — PSR-1 says a namespace is StudlyCaps and a method is
// camelCase, and this writes `namespace greeter;` and `greet()` outright, so the
// suffix is `greeter/Greeter#greet().` byte for byte with TypeScript's, Python's,
// Rust's, Java's and C#'s and only the coordinate separates the six. Both
// spellings are legal PHP and neither is idiomatic, and that is deliberate: the
// collision is the fixture, and a `Greet()` that could not collide would have
// tested nothing.
//
// This is also exactly what Ruby could not do, and the difference is one line of
// each grammar: a Ruby module name is a `constant` token, so `module greeter` is
// a syntax error; a PHP namespace segment is a plain `name`, so
// `namespace greeter;` merely looks wrong.
//
// The caller assigns a public property instead of passing a constructor argument.
// That is the Java, Rust and C# corpora's choice for the same reason — a declared
// constructor would be a member of its type — and it costs nothing here because
// a PHP property write is a `field` reference, which link's `calls` derivation
// (`symbol_kind IN ('function', 'method')`) does not select. So the traversal
// below still matches exactly one row.
var phpCorpusFiles = map[string]string{
	"composer.json": `{"name": "codiq/mixed-php", "version": "8.0.0"}` + "\n",

	"php/Greeter.php": `<?php

namespace greeter;

class Greeter
{
    public string $name = '';

    public function greet(): string
    {
        return 'hello, ' . $this->name;
    }
}
`,
	"php/Bootstrap.php": `<?php

namespace app;

use greeter\Greeter;

class Bootstrap
{
    public static function initiate(): string
    {
        $g = new Greeter();
        $g->name = 'world';

        return $g->greet();
    }
}
`,
}

// eightLanguageCorpus is the Ruby task's seven-language corpus with an eighth
// language added, and it is assembled rather than rewritten precisely because
// that is the claim: the seven earlier languages' files are byte-for-byte the
// ones m9rb_test.go already indexes, so anything this scenario finds that the
// Ruby one did not is PHP's doing.
var eightLanguageCorpus = func() map[string]string {
	out := make(map[string]string, len(sevenLanguageCorpus)+len(phpCorpusFiles))
	maps.Copy(out, sevenLanguageCorpus)
	maps.Copy(out, phpCorpusFiles)
	return out
}()

// m9phpState is one scenario's state: the Ruby task's, which is the C# task's,
// which is the Java task's, which is the Rust task's, which is M7's, which is
// M6's, which is M2's plus the second-tree machinery. Nothing is added — the
// corpora are on disk and the assertions are stateless — and that is itself part
// of what the task claims.
type m9phpState struct {
	m9rbState
}

func InitializeM9PHPScenario(sc *godog.ScenarioContext) {
	st := &m9phpState{}

	// The same real handshake every suite does, once per scenario.
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "codiq-m9php-tests", Version: "0.1.0"}, nil)
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
	sc.Step(`^the two-file PHP package$`, st.thePHPPackage)
	sc.Step(`^the artifact also holds "([^"]*)":$`, st.packageAlsoHolds)
	sc.Step(`^the artifact is indexed$`, st.moduleIndexed)
	sc.Step(`^the mixed-language repository of eight languages$`, st.theEightLanguageRepository)
	sc.Step(`^the repository is indexed$`, st.moduleIndexed)
	sc.Step(`^"([^"]*)" imports "([^"]*)"$`, st.fileImports)
	sc.Step(`^the "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)" definitions of "([^"]*)" differ only in their coordinate$`, st.sixDifferOnlyInCoordinate)
	sc.Step(`^"([^"]*)" is defined once in each of "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)"$`, st.definedOnceInEachOfEight)
	sc.Step(`^the graph holds definitions written in all of "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)"$`, st.eightLanguagesPresent)
	sc.Step(`^no derived edge joins two package schemes$`, st.noCrossSchemeEdges)
	sc.Step(`^no derived edge joins two languages$`, st.noCrossLanguageEdges)

	// PHP's own two, and they are a matched pair: the trait false negative, and
	// the decision that produces it.
	sc.Step(`^"([^"]*)" implements nothing$`, st.implementsNothing)
	sc.Step(`^"([^"]*)" does not import "([^"]*)"$`, st.fileDoesNotImport)

	// M1's steps, reused as written: the MCP tool call and the JSON comparison.
	sc.Step(`^an agent asks over MCP:$`, st.agentAsks)
	sc.Step(`^the answer is:$`, st.answerIs)
}

// --- given -----------------------------------------------------------------

// thePHPPackage copies the PHP fixture into a temp directory, the way
// m2State.theModule copies the Go one and for the same reason: two scenarios add
// files to it, and extract/php/testdata is the unit tests' fixture.
func (st *m9phpState) thePHPPackage() error {
	dir, err := os.MkdirTemp("", "codiq-m9php-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	st.repo = filepath.Join(dir, "greeter")
	return copyTree(filepath.Join(repoRoot, phpGreeterFixture), st.repo)
}

// theEightLanguageRepository writes the eight-language corpus into a temp
// directory, the way the other Given step copies a fixture into one — the same
// tmp root, so the After hook cleans up both the same way.
func (st *m9phpState) theEightLanguageRepository() error {
	dir, err := os.MkdirTemp("", "codiq-m9php-mixed-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	st.repo = filepath.Join(dir, "mixed")
	for rel, body := range eightLanguageCorpus {
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

// sixDifferOnlyInCoordinate is m9csState.fiveDifferOnlyInCoordinate over the six
// languages that now share a suffix, which is what makes the mixed scenario's
// other assertions mean anything. Ruby broke the chain at five because it could
// not join it; PHP restores it at six, so the pairwise claim has to hold along
// the whole chain again.
func (st *m9phpState) sixDifferOnlyInCoordinate(ctx context.Context, first, second, third, fourth, fifth, sixth, name string) error {
	if err := st.fiveDifferOnlyInCoordinate(ctx, first, second, third, fourth, fifth, name); err != nil {
		return err
	}
	return st.differOnlyInCoordinate(ctx, fifth, sixth, name)
}

// definedOnceInEachOfEight is m9rbState.definedOnceInEachOfSeven with an eighth
// language. PHP rejoins the suffix collision Ruby could not, so this is the claim
// the mixed corpus makes of all eight in full: the name is declared once in each,
// and no language's definition swallowed another's.
func (st *m9phpState) definedOnceInEachOfEight(ctx context.Context, name, first, second, third, fourth, fifth, sixth, seventh, eighth string) error {
	if err := st.definedOnceInEachOfSeven(ctx, name, first, second, third, fourth, fifth, sixth, seventh); err != nil {
		return err
	}
	return st.definedOnceInEachLanguage(ctx, name, seventh, eighth)
}

// eightLanguagesPresent is "one graph, eight languages" stated over the whole
// `file` table: all eight tags are there, and all eight actually contributed
// definitions rather than merely being registered as files.
func (st *m9phpState) eightLanguagesPresent(ctx context.Context, first, second, third, fourth, fifth, sixth, seventh, eighth string) error {
	if err := st.sevenLanguagesPresent(ctx, first, second, third, fourth, fifth, sixth, seventh); err != nil {
		return err
	}
	return st.bothLanguagesPresent(ctx, seventh, eighth)
}

// implementsNothing is the PHP stanza's one deliberate false negative, pinned.
//
// `class Counter implements Sized { use SizedTrait; }` satisfies `Sized` in PHP,
// because a trait's members are flattened into the using class at compile time.
// It does not here: `implements` gathers a method set by descriptor prefix
// (store/sqlc/query.sql), and nothing declares `Counter#size().` — the only
// definition row is `SizedTrait#size().`.
//
// Flattening would need the trait's file, which §2.5 forbids reading, and
// flattening only when the trait shares the class's file would make the
// derivation's answer depend on source layout. So the gap is chosen, and this is
// what stops it from being rediscovered as a bug rather than read as a decision.
//
// The step checks that the graph derived `implements` edges *somewhere* first: an
// emptiness claim in a graph with no implements rows at all would be true for the
// wrong reason, and the very next scenario in this file is the one that puts a
// real PHP `implements` row there.
func (st *m9phpState) implementsNothing(ctx context.Context, name string) error {
	var total, mine int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM implements`).Scan(&total); err != nil {
		return fmt.Errorf("count implements edges: %w", err)
	}
	err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM implements e
		JOIN occurrence o ON o.id = e.source_id
		WHERE o.name = $1 AND o.role = 'definition'`, name).Scan(&mine)
	if err != nil {
		return fmt.Errorf("count the implements edges of %s: %w", name, err)
	}
	return check(func(t assert.TestingT) {
		assert.Positive(t, total,
			"the graph derived no implements edges at all, so the claim about %s would be vacuous", name)
		assert.Zero(t, mine,
			"%s implements something; a trait's members are flattened by PHP and not by this graph", name)
	})
}

// fileDoesNotImport is the decision behind the gap above, and it is the one place
// this stanza and Ruby's part company on the same syntax.
//
// Ruby's `include Foo` derives an `imports` edge, because a Ruby module *is* a
// namespace and the mixin reference carries its package descriptor. PHP's
// `use SizedTrait;` inside a class body derives none, because a trait is not a
// namespace — nothing may be written `SizedTrait\x`, and a trait lives in a
// namespace exactly as a class does. So it is a `type` ending `#`, and only a
// top-level `use` produces the `package` reference `imports` joins on.
func (st *m9phpState) fileDoesNotImport(ctx context.Context, from, to string) error {
	var edges, outbound int
	err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM imports e
		JOIN file s ON s.id = e.source_id
		JOIN file t ON t.id = e.target_id
		WHERE s.path = $1 AND t.path = $2`, from, to).Scan(&edges)
	if err != nil {
		return fmt.Errorf("read imports %s -> %s: %w", from, to, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM imports`).Scan(&outbound); err != nil {
		return fmt.Errorf("count imports edges: %w", err)
	}
	return check(func(t assert.TestingT) {
		assert.Positive(t, outbound,
			"the graph derived no imports edges at all, so the claim about %s would be vacuous", from)
		assert.Zero(t, edges,
			"%s imports %s; a trait use is a type reference, and only a top-level `use` is an import", from, to)
	})
}
