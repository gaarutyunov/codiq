// The C/C++ feature suite: features/index_cc.feature against the real stack
// (SPEC.md §13, §14 M9+).
//
// The file is named m9cc_test.go rather than m9_test.go for the reason
// m9rs_test.go gives and the four suites after it repeat: §14 M9+ is not one
// milestone but a bag of independent language tasks with no ordering between
// them, so Kotlin (#23) and Swift (#24) will want their own files beside this
// one rather than to grow it.
//
// There is no new harness and no new stack — for the ninth time, which is the
// point rather than a convenience. m1_test.go's startStack brings up the one
// composition the package shares; m2_test.go's m2State carries the module copy,
// the `codiq` invocation, the graph reset and M1's MCP handshake; and
// m9php_test.go is embedded whole, so M6's "no derived edge joins …" checks and
// every generalisation the seven suites before it made are called rather than
// reimplemented.
//
// What this file adds is two corpora and six steps. Three of the six are the PHP
// suite's generalised from eight languages to nine; the other three are C's own
// and they are the three claims no language before this one could make:
//
//   - a *declaration* in one file resolving into a *definition* in another,
//   - a `static` symbol that no other file can reach,
//   - an `#include` target offered as every suffix of its own path, because the
//     `-I` search that would have resolved it is not in the source.
//
// That is the whole diff, and it is the measurement: the ninth language cost a
// sub-package, two queries, a resolver and eight `byExt` entries, and no core
// package changed.
//
// The claims split the way they have in every suite before this one. Every
// navigation claim goes over MCP. The rest are SQL, because the GraphQL surface
// cannot express them: `imports` is file → file and File is not selectable by
// path; "definitions exist in nine languages" and "Greeter is defined once in
// each" are facts about the whole `file` table; and "nothing outside this file
// resolves to that symbol" is a statement about the absence of a row anywhere.
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

// ccGreeterFixture is the C corpus, relative to the repository root: the CMake
// project extract/cc/testdata/greeter/ the unit tests already use. Copied, never
// indexed in place — two scenarios add files to it.
//
// It is four files rather than the two every suite before it used, and it has to
// be: C is the first language here whose declaration and definition are
// different files, so the smallest corpus that exercises the split is a header,
// the source that defines what it declares, and a caller that includes it.
var ccGreeterFixture = filepath.Join("extract", "cc", "testdata", "greeter")

// TestM9CCFeatures is the godog entry point for the C/C++ task, scoped to its
// own feature file so that each milestone's suite owns the scenarios it has
// steps for.
func TestM9CCFeatures(t *testing.T) {
	ctx := context.Background()
	repoRoot = mustRepoRoot(t)

	startStack(t, ctx)

	suite := godog.TestSuite{
		Name:                "m9cc",
		ScenarioInitializer: InitializeM9CCScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{filepath.Join(repoRoot, "features", "index_cc.feature")},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("m9cc feature scenarios failed")
	}
}

// ccCorpusFiles is the ninth language's half of the mixed corpus: a
// `CMakeLists.txt` beside the eight manifests already there, a `greeter`
// namespace declaring the type, and an `app` namespace that builds one and calls
// it.
//
// It is **C++ and not C**, and that is the one place this corpus records a
// language's limits rather than its conventions. Six languages already write
// `greeter/Greeter#greet().` byte for byte, and C++ joins them by writing
// `namespace greeter` outright — unidiomatic (the convention is `Greeter`) and
// legal, exactly as the C# and PHP halves are. C cannot join, and not because
// of a convention: C has no namespace at all, so a C `greet` is `greet().` with
// nothing in front of it. Manufacturing a `greeter/` from the directory would
// break the one join C actually has — a header in `include/` and a source in
// `src/` naming one symbol — so the collision is C++'s to make and C's to sit
// out. Both halves carry `file.lang = "cc"`, because a `.h` does not say which
// of the two it is.
//
// The class is written header-only, with the body inline in the class. That is
// deliberate rather than idiomatic: split across a `.hpp` and a `.cpp`, `greet`
// would be defined *twice* — the class body declares what the class has and the
// out-of-line definition supplies it, and both are definitions — and the
// "defined once in each language" step would find two. The header/source split
// this stanza exists to model is exercised by its own fixture, where it can be
// asserted rather than merely survived.
//
// The caller assigns a public data member instead of passing a constructor
// argument. That is the Java, Rust, C# and PHP corpora's choice for the same
// reason — a declared constructor would be a member of its type — and it costs
// nothing here because a C++ member write is a `field` reference, which link's
// `calls` derivation (`symbol_kind IN ('function', 'method')`) does not select.
// So the traversal below still matches exactly one row.
var ccCorpusFiles = map[string]string{
	"CMakeLists.txt": `cmake_minimum_required(VERSION 3.20)
project(mixed-cc VERSION 9.0.0 LANGUAGES CXX)
`,

	"cc/greeter.hpp": `#pragma once

namespace greeter {

class Greeter {
public:
    const char *name = "";

    const char *greet() const { return name; }
};

}  // namespace greeter
`,

	"cc/app.cpp": `#include "greeter.hpp"

namespace app {

const char *embark() {
    greeter::Greeter g;
    g.name = "world";

    return g.greet();
}

}  // namespace app
`,
}

// nineLanguageCorpus is the PHP task's eight-language corpus with a ninth
// language added, and it is assembled rather than rewritten precisely because
// that is the claim: the eight earlier languages' files are byte-for-byte the
// ones m9php_test.go already indexes, so anything this scenario finds that the
// PHP one did not is C++'s doing.
var nineLanguageCorpus = func() map[string]string {
	out := make(map[string]string, len(eightLanguageCorpus)+len(ccCorpusFiles))
	maps.Copy(out, eightLanguageCorpus)
	maps.Copy(out, ccCorpusFiles)
	return out
}()

// m9ccState is one scenario's state: the PHP task's, which is the Ruby task's,
// which is the C# task's, … back to M2's. Nothing is added — the corpora are on
// disk and the assertions are stateless — and that is itself part of what the
// task claims.
type m9ccState struct {
	m9phpState
}

func InitializeM9CCScenario(sc *godog.ScenarioContext) {
	st := &m9ccState{}

	// The same real handshake every suite does, once per scenario.
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "codiq-m9cc-tests", Version: "0.1.0"}, nil)
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
	sc.Step(`^the C project$`, st.theCProject)
	sc.Step(`^the artifact also holds "([^"]*)":$`, st.packageAlsoHolds)
	sc.Step(`^the artifact is indexed$`, st.moduleIndexed)
	sc.Step(`^the mixed-language repository of nine languages$`, st.theNineLanguageRepository)
	sc.Step(`^the repository is indexed$`, st.moduleIndexed)
	sc.Step(`^"([^"]*)" imports "([^"]*)"$`, st.fileImports)
	sc.Step(`^the "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)" definitions of "([^"]*)" differ only in their coordinate$`, st.sevenDifferOnlyInCoordinate)
	sc.Step(`^"([^"]*)" is defined once in each of "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)"$`, st.definedOnceInEachOfNine)
	sc.Step(`^the graph holds definitions written in all of "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)", "([^"]*)" and "([^"]*)"$`, st.nineLanguagesPresent)
	sc.Step(`^no derived edge joins two package schemes$`, st.noCrossSchemeEdges)
	sc.Step(`^no derived edge joins two languages$`, st.noCrossLanguageEdges)

	// C's own three, and each is a claim no language before this one could make.
	sc.Step(`^"([^"]*)" resolves into "([^"]*)"$`, st.fileResolvesInto)
	sc.Step(`^"([^"]*)" is defined once$`, st.definedOnce)
	sc.Step(`^no reference outside "([^"]*)" resolves to "([^"]*)"$`, st.noReferenceOutsideResolvesTo)
	sc.Step(`^"([^"]*)" offers the include targets "([^"]*)" and "([^"]*)"$`, st.offersIncludeTargets)

	// M1's steps, reused as written: the MCP tool call and the JSON comparison.
	sc.Step(`^an agent asks over MCP:$`, st.agentAsks)
	sc.Step(`^the answer is:$`, st.answerIs)
}

// --- given -----------------------------------------------------------------

// theCProject copies the C fixture into a temp directory, the way
// m2State.theModule copies the Go one and for the same reason: two scenarios add
// files to it, and extract/cc/testdata is the unit tests' fixture.
func (st *m9ccState) theCProject() error {
	dir, err := os.MkdirTemp("", "codiq-m9cc-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	st.repo = filepath.Join(dir, "greeter")
	return copyTree(filepath.Join(repoRoot, ccGreeterFixture), st.repo)
}

// theNineLanguageRepository writes the nine-language corpus into a temp
// directory, the way the other Given step copies a fixture into one — the same
// tmp root, so the After hook cleans up both the same way.
func (st *m9ccState) theNineLanguageRepository() error {
	dir, err := os.MkdirTemp("", "codiq-m9cc-mixed-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	st.repo = filepath.Join(dir, "mixed")
	for rel, body := range nineLanguageCorpus {
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

// sevenDifferOnlyInCoordinate is m9phpState.sixDifferOnlyInCoordinate over the
// seven languages that now share a suffix. C++ extends the chain; C sits it out,
// for the reason ccCorpusFiles gives.
func (st *m9ccState) sevenDifferOnlyInCoordinate(ctx context.Context, first, second, third, fourth, fifth, sixth, seventh, name string) error {
	if err := st.sixDifferOnlyInCoordinate(ctx, first, second, third, fourth, fifth, sixth, name); err != nil {
		return err
	}
	return st.differOnlyInCoordinate(ctx, sixth, seventh, name)
}

// definedOnceInEachOfNine is m9phpState.definedOnceInEachOfEight with a ninth
// language: the name is declared once in each, and no language's definition
// swallowed another's.
func (st *m9ccState) definedOnceInEachOfNine(ctx context.Context, name, first, second, third, fourth, fifth, sixth, seventh, eighth, ninth string) error {
	if err := st.definedOnceInEachOfEight(ctx, name, first, second, third, fourth, fifth, sixth, seventh, eighth); err != nil {
		return err
	}
	return st.definedOnceInEachLanguage(ctx, name, eighth, ninth)
}

// nineLanguagesPresent is "one graph, nine languages" stated over the whole
// `file` table: all nine tags are there, and all nine actually contributed
// definitions rather than merely being registered as files.
func (st *m9ccState) nineLanguagesPresent(ctx context.Context, first, second, third, fourth, fifth, sixth, seventh, eighth, ninth string) error {
	if err := st.eightLanguagesPresent(ctx, first, second, third, fourth, fifth, sixth, seventh, eighth); err != nil {
		return err
	}
	return st.bothLanguagesPresent(ctx, eighth, ninth)
}

// fileResolvesInto is the claim the whole stanza turns on, and the first time in
// nine languages that a *declaration* and a *definition* are two files.
//
// `void greet();` in the header is emitted as a reference carrying the same
// descriptor the definition carries, so the header resolves into the source —
// which is "go to definition" run on a declaration, derived for free out of the
// descriptor join every other language uses. Had the prototype been a
// definition instead, this edge would not exist at all: a definition never joins
// a definition, and the two would sit in the graph as unrelated symbols that
// happen to share a name.
func (st *m9ccState) fileResolvesInto(ctx context.Context, from, to string) error {
	var edges int
	err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM resolves_to e
		JOIN occurrence s ON s.id = e.source_id
		JOIN occurrence t ON t.id = e.target_id
		JOIN file sf ON sf.id = s.file_id
		JOIN file tf ON tf.id = t.file_id
		WHERE sf.path = $1 AND tf.path = $2`, from, to).Scan(&edges)
	if err != nil {
		return fmt.Errorf("read resolves_to %s -> %s: %w", from, to, err)
	}
	return check(func(t assert.TestingT) {
		assert.Positive(t, edges,
			"%s does not resolve into %s; a header's declaration must carry the definition's descriptor", from, to)
	})
}

// definedOnce is the other half of the role rule, stated where it is cheapest to
// check. A prototype that had been emitted as a definition would put a second
// row here, and every cross-file call to the symbol would be doubled.
func (st *m9ccState) definedOnce(ctx context.Context, name string) error {
	var n int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FROM occurrence WHERE name = $1 AND role = 'definition'`, name).Scan(&n)
	if err != nil {
		return fmt.Errorf("count definitions of %s: %w", name, err)
	}
	return check(func(t assert.TestingT) {
		assert.Equal(t, 1, n, "%s is declared in a header and defined in a source; only the body defines it", name)
	})
}

// noReferenceOutsideResolvesTo is C's internal linkage, pinned.
//
// `static void fallback(void)` is descriptored under the file's own path, so no
// other file can render a descriptor that matches it. Without that key, every
// `static` helper in a corpus would share one descriptor with every other of the
// same name and the graph would carry a `resolves_to` edge between two unrelated
// files — a wrong edge, which is worse than a missing one.
//
// The step checks that the graph resolved something *somewhere* first: an
// emptiness claim in a graph with no resolves_to rows at all would be true for
// the wrong reason.
func (st *m9ccState) noReferenceOutsideResolvesTo(ctx context.Context, owner, name string) error {
	var total, escaping int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM resolves_to`).Scan(&total); err != nil {
		return fmt.Errorf("count resolves_to edges: %w", err)
	}
	err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM resolves_to e
		JOIN occurrence s ON s.id = e.source_id
		JOIN occurrence t ON t.id = e.target_id
		JOIN file sf ON sf.id = s.file_id
		WHERE t.name = $1 AND sf.path <> $2`, name, owner).Scan(&escaping)
	if err != nil {
		return fmt.Errorf("count references to %s outside %s: %w", name, owner, err)
	}
	return check(func(t assert.TestingT) {
		assert.Positive(t, total,
			"the graph resolved nothing at all, so the claim about %s would be vacuous", name)
		assert.Zero(t, escaping,
			"%s has internal linkage; no file but %s may reach it", name, owner)
	})
}

// offersIncludeTargets is the file-local approximation of the `-I` search,
// checked as the shape it produces.
//
// `#include "greeter.h"` names a path the build system resolves, and the header
// actually lives at `include/greeter.h`. Nothing in either file says so, so the
// header offers *every suffix of its own path* as a `package` definition and the
// join picks whichever one the include spelled. It is the only mechanism a
// file-local reader has, and its cost — two files whose paths share a suffix
// both match — is bounded by basename collisions.
func (st *m9ccState) offersIncludeTargets(ctx context.Context, path, short, full string) error {
	found := map[string]bool{}
	rows, err := pool.Query(ctx, `
		SELECT o.descriptor
		FROM occurrence o
		JOIN file f ON f.id = o.file_id
		WHERE f.path = $1 AND o.role = 'definition' AND o.symbol_kind = 'package'`, path)
	if err != nil {
		return fmt.Errorf("read the package definitions of %s: %w", path, err)
	}
	defer rows.Close()
	for rows.Next() {
		var descriptor string
		if err := rows.Scan(&descriptor); err != nil {
			return err
		}
		found[descriptor] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return check(func(t assert.TestingT) {
		for _, suffix := range []string{short, full} {
			assert.True(t, found["scip-cc cmake greeter 1.0.0 "+suffix],
				"%s must offer %q as an include target; found %v", path, suffix, found)
		}
	})
}
