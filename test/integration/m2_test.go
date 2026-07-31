// M2's feature suite: features/index_go.feature against the real stack
// (SPEC.md §13, §14 M2).
//
// The infrastructure is not re-invented here. m1_test.go's startStack builds the
// gopgql image, runs postgres:19beta2 with the committed initdb script, applies
// the committed migrations with the real `gopgql migrate`, and serves
// `gopgql-mcp` over streamable HTTP; this file calls it and inherits all of it,
// along with its helpers (check, toolText, mustRepoRoot, …) and M1's
// scenarioState, which is embedded so that the MCP handshake and the
// "asks over MCP" / "the answer is" steps are literally M1's code.
//
// It does not share the database's *contents*: deploy/seed/seed.sql's corpus
// would otherwise be inside every whole-graph assertion M2 makes. Up to M4 that
// was arranged by standing up a second stack; since M5 it is arranged by the
// Background, which truncates before each scenario and had to exist anyway to
// isolate the scenarios from each other (startStack says why the stacks were
// collapsed into one).
//
// The indexer is driven as the `cmd/codiq` binary rather than by calling
// index.Run, because the binary is part of what M2 ships: its walk, its flag
// handling and its report are otherwise never exercised end to end, and the
// report is where "a skipped file is visible" is actually visible. It is built
// once for the package (m3_test.go's TestMain) and run against the host-mapped
// DSN, which is the same program the compose `codiq` service runs with the same
// arguments.
package integration

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
)

// coreTables is every table the model has (schema/codiq.graphql), base first
// then derived. It is the reset list and the snapshot list: one TRUNCATE over
// all of them needs no ordering, because every foreign key in the schema points
// inside the set.
var coreTables = []string{
	"file", "occurrence", "scope",
	"defines", "contains_scope", "contains_occurrence", "references_local",
	"resolves_to", "imports", "calls", "implements", "type_defines",
}

// greeterFixture is the corpus, relative to the repository root: the two-file Go
// module extract/golang/testdata/greeter/ that the unit tests already use. It is
// copied, never indexed in place — three of the scenarios below add to or
// rewrite the module.
var greeterFixture = filepath.Join("extract", "golang", "testdata", "greeter")

// TestM2Features is the godog entry point for M2, scoped to its own feature file
// so that each milestone's suite owns the scenarios it has steps for.
func TestM2Features(t *testing.T) {
	ctx := context.Background()
	repoRoot = mustRepoRoot(t)

	startStack(t, ctx)

	suite := godog.TestSuite{
		Name:                "m2",
		ScenarioInitializer: InitializeM2Scenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{filepath.Join(repoRoot, "features", "index_go.feature")},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("m2 feature scenarios failed")
	}
}

// m2State is one scenario's state. It embeds M1's scenarioState for the MCP
// session and the agentAsks / answerIs steps, and adds the module on disk, the
// indexer's output, and the graph snapshot the idempotency scenario compares.
type m2State struct {
	scenarioState

	// repo is the copy of the fixture this scenario indexes.
	repo string
	// tmp is the directory repo lives in, removed when the scenario ends.
	tmp string
	// report is the stdout of the last `codiq` run.
	report string
	// before is the graph as it was when the scenario last remembered it.
	before *graphSnapshot
}

// graphSnapshot is enough of the graph to notice a second index changing it:
// how many rows every table holds, which uuid each path was given, and the
// derived edges rendered as descriptor pairs — the last so that a rebuild that
// kept the row count while moving an edge still fails.
type graphSnapshot struct {
	counts  map[string]int
	fileIDs map[string]string
	derived map[string][]string
}

func InitializeM2Scenario(sc *godog.ScenarioContext) {
	st := &m2State{}

	// The same real handshake M1 does, once per scenario: initialize,
	// notifications/initialized, and the Mcp-Session-Id carried on every later
	// request by the transport.
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "codiq-m2-tests", Version: "0.1.0"}, nil)
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
		// The scenario's copy of the module goes with it. godog gives a hook no
		// *testing.T to hang a Cleanup on, so this is where it happens.
		if st.tmp != "" {
			_ = os.RemoveAll(st.tmp)
		}
		st.repo, st.tmp, st.report, st.before = "", "", "", nil
		return ctx, nil
	})

	sc.Step(`^an empty CodiQ graph$`, st.emptyGraph)
	sc.Step(`^the two-file Go module$`, st.theModule)
	sc.Step(`^the indexed two-file Go module$`, st.theIndexedModule)
	sc.Step(`^the module also holds "([^"]*)":$`, st.moduleAlsoHolds)
	sc.Step(`^"([^"]*)" is rewritten as:$`, st.fileRewritten)
	sc.Step(`^the module is indexed$`, st.moduleIndexed)
	sc.Step(`^the module is indexed again$`, st.moduleIndexed)
	sc.Step(`^the run indexed (\d+) files and loaded (\d+)$`, st.runCounted)
	sc.Step(`^"([^"]*)" defines nothing$`, st.definesNothing)
	sc.Step(`^the graph is exactly what it was before$`, st.graphUnchanged)
	sc.Step(`^every file kept the identity it was first given$`, st.filesKeptIdentity)
	sc.Step(`^"([^"]*)" kept the identity it was first given$`, st.fileKeptIdentity)
	sc.Step(`^no extracted edge crosses a file boundary$`, st.extractedEdgesAreLocal)
	sc.Step(`^every derived edge crosses one$`, st.derivedEdgesCross)
	sc.Step(`^nothing calls anything any more$`, st.nothingCalls)

	// M1's steps, reused as written: the MCP tool call and the JSON comparison.
	sc.Step(`^an agent asks over MCP:$`, st.agentAsks)
	sc.Step(`^the answer is:$`, st.answerIs)
}

// --- given -----------------------------------------------------------------

// emptyGraph is the Background. Every scenario indexes a module of its own, so
// each starts from an empty graph — otherwise a scenario's whole-graph
// assertions would be about the previous scenario's corpus as well as its own.
//
// TRUNCATE rather than a fresh database: standing up another postgres per
// scenario would cost more than the whole suite, and the model's foreign keys
// all point inside coreTables, so one statement empties it consistently.
func (st *m2State) emptyGraph(ctx context.Context) error {
	if st.session == nil {
		return errors.New("no MCP session")
	}
	if _, err := pool.Exec(ctx, `TRUNCATE `+strings.Join(coreTables, ", ")); err != nil {
		return fmt.Errorf("reset graph: %w", err)
	}
	return nil
}

// theModule copies the fixture module into a temp directory. A copy, because
// scenarios add files to it and rewrite them, and extract/golang/testdata is
// the unit tests' fixture.
func (st *m2State) theModule() error {
	dir, err := os.MkdirTemp("", "codiq-m2-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	// Nested one level down so the module's own directory is named `greeter`
	// rather than after the temp directory: coord resolves the module path from
	// go.mod, but the directory name is what shows up in a failure message.
	st.repo = filepath.Join(dir, "greeter")
	return copyTree(filepath.Join(repoRoot, greeterFixture), st.repo)
}

// theIndexedModule is "the two-file Go module" plus one index plus a snapshot —
// the starting point for the scenarios that assert about a *second* run.
func (st *m2State) theIndexedModule(ctx context.Context) error {
	if err := st.theModule(); err != nil {
		return err
	}
	if err := st.moduleIndexed(ctx); err != nil {
		return err
	}
	snap, err := snapshot(ctx)
	if err != nil {
		return err
	}
	st.before = snap
	return nil
}

func (st *m2State) moduleAlsoHolds(name string, body *godog.DocString) error {
	return st.write(name, body.Content)
}

func (st *m2State) fileRewritten(name string, body *godog.DocString) error {
	if _, err := os.Stat(filepath.Join(st.repo, name)); err != nil {
		return fmt.Errorf("%s is not in the module to rewrite: %w", name, err)
	}
	return st.write(name, body.Content)
}

func (st *m2State) write(name, content string) error {
	if st.repo == "" {
		return errors.New("no module; the Given step did not run")
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return os.WriteFile(filepath.Join(st.repo, name), []byte(content), 0o644)
}

// --- when ------------------------------------------------------------------

// moduleIndexed runs the real binary over the module, against the same database
// gopgql is serving. Its stdout is kept, because the report is the only place
// the run's own account of what it did is visible.
func (st *m2State) moduleIndexed(ctx context.Context) error {
	if st.repo == "" {
		return errors.New("no module; the Given step did not run")
	}
	// -dbos-dsn as well as -dsn since M3: the binary is a DBOS workflow now and
	// refuses to run without a checkpoint database. The database itself is
	// already there — deploy/initdb/01-dbos.sql creates it and startStack mounts
	// that script — so this is the flag and nothing else (m3_test.go).
	dbosDSN, err := dbosConnString(connString)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, codiqBin, "-dsn", connString, "-dbos-dsn", dbosDSN, "-v", st.repo)
	cmd.Env = codiqEnv()
	out, err := cmd.CombinedOutput()
	st.report = string(out)
	if err != nil {
		return fmt.Errorf("codiq %s: %w\n%s", st.repo, err, out)
	}
	return nil
}

// --- then ------------------------------------------------------------------

// runCounted reads the counts back out of the report the binary printed
// (cmd/codiq report), so the assertion is about what the run says it did rather
// than about rows that could have been left by an earlier scenario.
func (st *m2State) runCounted(files, loaded int) error {
	gotFiles, err := reportField(st.report, "files")
	if err != nil {
		return err
	}
	gotLoaded, err := reportField(st.report, "loaded")
	if err != nil {
		return err
	}
	return check(func(t assert.TestingT) {
		assert.Equal(t, files, gotFiles, "files the walk selected:\n%s", st.report)
		assert.Equal(t, loaded, gotLoaded, "files loaded:\n%s", st.report)
	})
}

// definesNothing asserts a file is in the graph and contributes no definitions:
// the difference between "indexed and empty" and "never seen", which is the
// distinction §5 turns on. Read in SQL rather than over MCP because MCP cannot
// express it — `{ file(path:) { defines } }` is an inner join, so a file with no
// definitions is absent from the answer for the same reason a file that was
// never indexed is, and the two cases are exactly what this has to tell apart.
func (st *m2State) definesNothing(ctx context.Context, path string) error {
	var defines int
	err := pool.QueryRow(ctx, `
		SELECT count(d.target_id)
		FROM file f LEFT JOIN defines d ON d.source_id = f.id
		WHERE f.path = $1
		GROUP BY f.id`, path).Scan(&defines)
	if err != nil {
		return fmt.Errorf("%s is not in the graph at all: %w", path, err)
	}
	return check(func(t assert.TestingT) {
		assert.Zero(t, defines, "definitions extracted from %s", path)
	})
}

// graphUnchanged compares the whole graph against the snapshot taken before the
// second run. SQL, not MCP: the claim is about every row in every table, and the
// GraphQL surface exposes traversals rather than table contents.
func (st *m2State) graphUnchanged(ctx context.Context) error {
	if st.before == nil {
		return errors.New("nothing remembered; the Given step did not snapshot")
	}
	now, err := snapshot(ctx)
	if err != nil {
		return err
	}
	return check(func(t assert.TestingT) {
		assert.Equal(t, st.before.counts, now.counts, "rows per table after re-indexing")
		assert.Equal(t, st.before.derived, now.derived, "cross-file edges after re-indexing")
	})
}

func (st *m2State) filesKeptIdentity(ctx context.Context) error {
	if st.before == nil {
		return errors.New("nothing remembered; the Given step did not snapshot")
	}
	now, err := snapshot(ctx)
	if err != nil {
		return err
	}
	return check(func(t assert.TestingT) {
		assert.Equal(t, st.before.fileIDs, now.fileIDs, "file ids after re-indexing")
	})
}

func (st *m2State) fileKeptIdentity(ctx context.Context, path string) error {
	if st.before == nil {
		return errors.New("nothing remembered; the Given step did not snapshot")
	}
	was, ok := st.before.fileIDs[path]
	if !ok {
		return fmt.Errorf("%s was not in the graph before", path)
	}
	var now string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM file WHERE path = $1`, path).Scan(&now); err != nil {
		return fmt.Errorf("read %s id: %w", path, err)
	}
	return check(func(t assert.TestingT) {
		assert.Equal(t, was, now, "%s kept its id across a re-index", path)
	})
}

// extractedEdgesAreLocal is §2.5 stated over the whole graph: an extracted edge
// whose endpoints sit in different files would mean extraction had read another
// file's facts, and per-file incrementality would be gone. Only SQL can say
// "no edge anywhere", so this is SQL.
func (st *m2State) extractedEdgesAreLocal(ctx context.Context) error {
	crossing, err := crossFileEdges(ctx, "references_local")
	if err != nil {
		return err
	}
	return check(func(t assert.TestingT) {
		assert.Zero(t, crossing, "references_local edges spanning two files")
	})
}

// derivedEdgesCross is the converse, and the reason the derived layer exists at
// all: every one of its rows is a fact no single file could have known. link's
// queries all carry `d.file_id <> r.file_id`, so a derived edge inside one file
// would mean a derivation had stopped filtering.
func (st *m2State) derivedEdgesCross(ctx context.Context) error {
	var total, crossing int
	for _, table := range []string{"resolves_to", "calls"} {
		n, err := crossFileEdges(ctx, table)
		if err != nil {
			return err
		}
		var all int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&all); err != nil {
			return fmt.Errorf("count %s: %w", table, err)
		}
		crossing += n
		total += all
	}
	return check(func(t assert.TestingT) {
		assert.Positive(t, total, "derived edges to check")
		assert.Equal(t, total, crossing, "derived edges spanning two files")
	})
}

// nothingCalls is the visible consequence of a replace: the definition the call
// pointed at is gone, so the edge is gone too. Asserted on the table rather
// than over MCP because "no calls at all" is the absence of every row, and a
// traversal cannot return an absence (m1_test.go's inner-join scenario).
func (st *m2State) nothingCalls(ctx context.Context) error {
	var calls int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM calls`).Scan(&calls); err != nil {
		return fmt.Errorf("count calls: %w", err)
	}
	return check(func(t assert.TestingT) {
		assert.Zero(t, calls, "call edges left after the callee was deleted")
	})
}

// --- helpers ---------------------------------------------------------------

// snapshot reads the whole graph into a comparable value.
func snapshot(ctx context.Context) (*graphSnapshot, error) {
	snap := &graphSnapshot{
		counts:  map[string]int{},
		fileIDs: map[string]string{},
		derived: map[string][]string{},
	}
	for _, table := range coreTables {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			return nil, fmt.Errorf("count %s: %w", table, err)
		}
		snap.counts[table] = n
	}

	rows, err := pool.Query(ctx, `SELECT path, id::text FROM file`)
	if err != nil {
		return nil, fmt.Errorf("read file ids: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var path, id string
		if err := rows.Scan(&path, &id); err != nil {
			return nil, err
		}
		snap.fileIDs[path] = id
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// The occurrence-level derived tables, rendered as descriptor pairs: a
	// rebuild that recreated the same number of edges between different symbols
	// would pass a count comparison and fail this one. Descriptors rather than
	// ids because ids below the file are regenerated on every load by design.
	for _, table := range []string{"resolves_to", "calls", "implements", "type_defines"} {
		edges, err := edgeDescriptors(ctx, table)
		if err != nil {
			return nil, err
		}
		snap.derived[table] = edges
	}
	return snap, nil
}

func edgeDescriptors(ctx context.Context, table string) ([]string, error) {
	// table is one of this file's own constants, never scenario input.
	rows, err := pool.Query(ctx, `
		SELECT s.descriptor || ' -> ' || t.descriptor
		FROM `+table+` e
		JOIN occurrence s ON s.id = e.source_id
		JOIN occurrence t ON t.id = e.target_id`)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", table, err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var edge string
		if err := rows.Scan(&edge); err != nil {
			return nil, err
		}
		out = append(out, edge)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func crossFileEdges(ctx context.Context, table string) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM `+table+` e
		JOIN occurrence s ON s.id = e.source_id
		JOIN occurrence t ON t.id = e.target_id
		WHERE s.file_id <> t.file_id`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count cross-file %s: %w", table, err)
	}
	return n, nil
}

// reportField pulls one of cmd/codiq's report lines ("  files       2") apart.
func reportField(report, name string) (int, error) {
	re := regexp.MustCompile(`(?m)^\s+` + name + `\s+(\d+)`)
	m := re.FindStringSubmatch(report)
	if m == nil {
		return 0, fmt.Errorf("no %q line in the report:\n%s", name, report)
	}
	return strconv.Atoi(m[1])
}

// copyTree copies a directory's regular files, which is all the fixture has.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, body, 0o644)
	})
}
