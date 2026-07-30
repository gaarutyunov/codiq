// M3's feature suite: features/durable_index.feature against the real stack
// (SPEC.md §13, §14 M3).
//
// Nothing here is a new harness. m1_test.go's startStack builds the gopgql
// image, runs postgres:19beta2 with the committed initdb script — which is also
// what creates `codiq_dbos`, so the checkpoint database this milestone needs
// arrives with the stack rather than being conjured for the tests — applies the
// migrations and serves gopgql-mcp; this file calls it, embeds M1's
// scenarioState for the MCP handshake, and reuses M2's snapshot, coreTables,
// check and reportField.
//
// Two things it does add, because M3 has no way to assert its claims without
// them:
//
//   - A second pool, on the DBOS system database. A workflow's status and its
//     step ledger are the only places "this run died rather than finished" and
//     "this file's work is durable" are written down, and neither is in the
//     graph or reachable over MCP. Every navigation claim below still goes
//     through MCP; only the durability claims read dbos.workflow_status and
//     dbos.operation_outputs.
//   - A real process to kill. The DBOS worker is in-process, so there is no
//     separate thing to signal: the test execs the built cmd/codiq binary and
//     signals *that*. The binary is built once, in TestMain, so that the process
//     which resumes a run is byte for byte the one that started it — belt and
//     braces, since index.WorkflowVersion pins the application version recovery
//     matches on, but the alternative is a suite whose meaning depends on that.
//
// The corpus is generated. M2's fixture indexes in milliseconds and there is
// nothing there to interrupt; see the feature file's header for the shape of
// the module and why it is the size it is.
package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/jackc/pgx/v5/pgxpool"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/index"
)

// dbosDBName is DBOS's system database: a separate database on the same
// instance (SPEC.md §9 "Isolation"), created by deploy/initdb/01-dbos.sql.
const dbosDBName = "codiq_dbos"

const (
	// checkpointPoll is how often the checkpoint ledger is read while waiting
	// for a run to get far enough to be worth interrupting. Short, because the
	// window between "far enough" and "finished" is the corpus's whole runtime
	// and the point of polling at all is to land inside it.
	checkpointPoll = 20 * time.Millisecond
	// checkpointWait bounds that wait.
	checkpointWait = 3 * time.Minute
	// indexWait bounds a run that is expected to finish on its own.
	indexWait = 5 * time.Minute
	// exitWait is how long a signalled process is given to go away. Generous
	// for SIGTERM's sake: that path runs dbos.Shutdown on the way out, which
	// has a timeout of its own (cmd/codiq shutdownTimeout).
	exitWait = time.Minute
)

// m3Bin is cmd/codiq, built once for the package in TestMain.
var m3Bin string

// dbosPool is a pool on dbosDBName, opened by TestM3Features.
var dbosPool *pgxpool.Pool

// m3Log is the suite's *testing.T, so that the durability steps can record what
// they saw and not only whether they liked it.
//
// A step that asserts a workflow was recovered and prints nothing leaves a
// failure in CI with no way to tell a crash that was not survived from a crash
// that never happened. The status a run was left in, the id that finished it and
// the size of the replayed ledger are three lines, and they are the three lines
// that make this suite's central claim checkable by a person.
var m3Log interface {
	Helper()
	Logf(string, ...any)
}

func m3Logf(format string, args ...any) {
	if m3Log == nil {
		return
	}
	// So the line the log is attributed to is the step that recorded it rather
	// than this function, which is the same reason any test helper does it.
	m3Log.Helper()
	m3Log.Logf(format, args...)
}

// TestMain builds the binary the M3 scenarios exec.
//
// In TestMain rather than at the top of the suite so that the same executable
// backs every scenario, including the ones that kill one process and expect
// another to pick up what it left. DBOS recovers a workflow only for a matching
// application version, and index.WorkflowVersion is a constant rather than the
// default hash of the running binary — so a rebuild between the two halves is
// in fact harmless, and this is here to keep the suite from depending on that
// being true.
func TestMain(m *testing.M) {
	root, err := repoRootFromWD()
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration: %v\n", err)
		os.Exit(1)
	}
	dir, err := os.MkdirTemp("", "codiq-bin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration: %v\n", err)
		os.Exit(1)
	}
	m3Bin = filepath.Join(dir, "codiq")
	build := exec.Command("go", "build", "-o", m3Bin, "./cmd/codiq")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "integration: go build ./cmd/codiq: %v\n%s", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// TestM3Features is the godog entry point for M3, scoped to its own feature file
// so that each milestone's suite owns the scenarios it has steps for.
func TestM3Features(t *testing.T) {
	ctx := context.Background()
	repoRoot = mustRepoRoot(t)
	m3Log = t

	startStack(t, ctx)

	dbosDSN, err := dbosConnString(connString)
	require.NoError(t, err)
	dbosPool, err = pgxpool.New(ctx, dbosDSN)
	require.NoError(t, err, "open %s pool", dbosDBName)
	t.Cleanup(dbosPool.Close)
	// The database exists because the initdb script the stack mounts creates it.
	// Proving that here, once, turns "M3's whole premise is missing" into one
	// clear failure instead of a puzzling one inside a scenario.
	var one int
	require.NoError(t, dbosPool.QueryRow(ctx, `SELECT 1`).Scan(&one),
		"%s is not reachable; deploy/initdb/01-dbos.sql is what creates it", dbosDBName)

	suite := godog.TestSuite{
		Name:                "m3",
		ScenarioInitializer: InitializeM3Scenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{filepath.Join(repoRoot, "features", "durable_index.feature")},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("m3 feature scenarios failed")
	}
}

// dbosConnString is a graph DSN pointed at the DBOS system database instead.
//
// Derived rather than built, because the two databases are on one instance by
// design (SPEC.md §9) and the host port the stack maps is only known at run
// time. It lives in this file because M3 is what introduced the second database
// — m2_test.go calls it for the same reason cmd/codiq now demands it.
func dbosConnString(graphDSN string) (string, error) {
	u, err := url.Parse(graphDSN)
	if err != nil {
		return "", fmt.Errorf("parse %q: %w", graphDSN, err)
	}
	u.Path = "/" + dbosDBName
	return u.String(), nil
}

// repoRootFromWD is mustRepoRoot without a *testing.T, for TestMain.
func repoRootFromWD() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(filepath.Join(wd, "..", ".."))
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(root, "schema", "codiq.graphql")); err != nil {
		return "", fmt.Errorf("%s is not the repository root: %w", root, err)
	}
	return root, nil
}

// m3State is one scenario's state: M1's MCP session, the generated module, the
// indexer process, and what the last run left behind in both databases.
type m3State struct {
	scenarioState

	// tmp is the directory the generated module lives in, removed when the
	// scenario ends.
	tmp string
	// repo is the module's root, and — being absolute already — the target
	// cmd/codiq derives its workflow IDs from.
	repo string

	// proc is the running indexer, nil when none is running.
	proc *exec.Cmd
	// exit carries proc's wait status exactly once.
	exit chan error
	// stdout and stderr collect the running process's output; report and log
	// are what it had written by the time it ended.
	stdout, stderr bytes.Buffer
	report, log    string

	// runID is the workflow the scenario interrupted.
	runID string
	// ledger is runID's checkpoints as the interrupted process left them.
	ledger []checkpoint

	// before is the graph as it was when the scenario last remembered it.
	before *graphSnapshot
}

// checkpoint is one row of a workflow's step ledger — a step's identity and the
// output recorded for it, which is what a replay reads instead of running the
// step again.
type checkpoint struct {
	FunctionID int
	Name       string
	Output     string
	Err        string
}

func InitializeM3Scenario(sc *godog.ScenarioContext) {
	st := &m3State{}

	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "codiq-m3-tests", Version: "0.1.0"}, nil)
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
		// A scenario that failed between starting an indexer and signalling it
		// would otherwise leave one running against the next scenario's
		// database — and its workflow PENDING, for the next Launch to recover.
		st.stopIndexer()
		if st.tmp != "" {
			_ = os.RemoveAll(st.tmp)
		}
		st.tmp, st.repo, st.report, st.log, st.runID, st.ledger, st.before = "", "", "", "", "", nil, nil
		return ctx, nil
	})

	sc.Step(`^an empty CodiQ graph and no checkpoints$`, st.emptyGraphAndCheckpoints)
	sc.Step(`^a Go module of (\d+) files$`, st.aModuleOf)
	sc.Step(`^the module is being indexed$`, st.moduleBeingIndexed)
	sc.Step(`^the module has been indexed$`, st.moduleHasBeenIndexed)
	sc.Step(`^the indexer is killed once (\d+) files are checkpointed$`, st.killedAfter)
	sc.Step(`^the indexer is interrupted once (\d+) files are checkpointed$`, st.interruptedAfter)
	sc.Step(`^the module is indexed again$`, st.moduleIndexedAgain)
	sc.Step(`^the run is left pending$`, st.runLeftPending)
	sc.Step(`^the run is left cancelled$`, st.runLeftCancelled)
	sc.Step(`^the process said the index continues on the next run$`, st.saidItContinues)
	sc.Step(`^it is the same run that finished$`, st.sameRunFinished)
	sc.Step(`^it is not a continuation$`, st.notAContinuation)
	sc.Step(`^(\d+) runs are recorded, all successful$`, st.runsRecorded)
	sc.Step(`^the report accounts for all (\d+) files$`, st.reportAccountsFor)
	sc.Step(`^every checkpoint the killed run wrote is still exactly what it wrote$`, st.checkpointsSurvived)
	sc.Step(`^each of the (\d+) files was checkpointed exactly once$`, st.oneCheckpointPerFile)
	sc.Step(`^the graph is exactly what an uninterrupted index builds$`, st.graphMatchesCleanRun)
	sc.Step(`^the graph is exactly what it was before$`, st.graphUnchanged)
	sc.Step(`^every file kept the identity it was first given$`, st.filesKeptIdentity)

	// M1's steps, reused as written: the MCP tool call and the JSON comparison.
	sc.Step(`^an agent asks over MCP:$`, st.agentAsks)
	sc.Step(`^the answer is:$`, st.answerIs)
}

// --- given -----------------------------------------------------------------

// emptyGraphAndCheckpoints is the Background. Both databases, because a
// scenario's claims are about one run of one module: a workflow left by the
// previous scenario would be recovered by the next Launch, and its graph would
// be inside every whole-graph assertion made here.
func (st *m3State) emptyGraphAndCheckpoints(ctx context.Context) error {
	if st.session == nil {
		return errors.New("no MCP session")
	}
	if _, err := pool.Exec(ctx, `TRUNCATE `+strings.Join(coreTables, ", ")); err != nil {
		return fmt.Errorf("reset graph: %w", err)
	}
	ready, err := dbosMigrated(ctx)
	if err != nil {
		return err
	}
	if !ready {
		// Nothing has been checkpointed because nothing has run: DBOS creates
		// its own schema on the first NewDBOSContext, so on a fresh stack the
		// first scenario finds an empty database rather than empty tables.
		return nil
	}
	// CASCADE, because every other DBOS table references workflow_status.
	if _, err := dbosPool.Exec(ctx, `TRUNCATE dbos.workflow_status CASCADE`); err != nil {
		return fmt.Errorf("reset checkpoints: %w", err)
	}
	return nil
}

// dbosMigrated reports whether DBOS has created its system schema yet.
//
// It has not until the first cmd/codiq process runs its migrations, so
// everything that reads the checkpoint tables has to cope with them not being
// there — including the poll that waits for the very first run to record
// something, which starts before that run has finished migrating.
func dbosMigrated(ctx context.Context) (bool, error) {
	var ready bool
	if err := dbosPool.QueryRow(ctx,
		`SELECT to_regclass('dbos.operation_outputs') IS NOT NULL`).Scan(&ready); err != nil {
		return false, fmt.Errorf("look for the DBOS schema: %w", err)
	}
	return ready, nil
}

// aModuleOf generates the corpus: M2's two-file module, padded to files with
// independent ones.
//
// Generated rather than committed because its only purpose is to take long
// enough to interrupt, and a number of files that does is not a fixture anybody
// should have to read. The padding defines types and methods and uses fields,
// so each file is real parse and load work, and none of it calls anything —
// leaving main -> Greet the only call edge in the graph, exactly as in M2.
func (st *m3State) aModuleOf(files int) error {
	if files < 3 {
		return fmt.Errorf("a module of %d files has nothing to interrupt", files)
	}
	dir, err := os.MkdirTemp("", "codiq-m3-*")
	if err != nil {
		return err
	}
	st.tmp = dir
	// Nested one level down so the module's directory is named after the module
	// rather than after the temp directory, which is what shows up in a failure
	// message. The path is already absolute, so it is also the target cmd/codiq
	// hashes into the workflow ID.
	st.repo = filepath.Join(dir, "durable")
	if err := os.MkdirAll(st.repo, 0o755); err != nil {
		return err
	}

	write := func(name, body string) error {
		return os.WriteFile(filepath.Join(st.repo, name), []byte(body), 0o644)
	}
	if err := write("go.mod", "module github.com/foo/durable\n\ngo 1.24\n"); err != nil {
		return err
	}
	if err := write("greeter.go", `package main

type Greeter struct {
	Name string
}

func (g Greeter) Greet() string {
	return "hello, " + g.Name
}
`); err != nil {
		return err
	}
	if err := write("main.go", `package main

func main() {
	g := Greeter{Name: "world"}
	_ = g.Greet()
}
`); err != nil {
		return err
	}

	for i := range files - 2 {
		var b strings.Builder
		fmt.Fprintf(&b, "package main\n\ntype Filler%03d struct {\n\tName string\n\tKind string\n}\n\n", i)
		for j := range 8 {
			fmt.Fprintf(&b, "func (f Filler%03d) Field%d() string {\n"+
				"\tif f.Kind == \"\" {\n\t\treturn f.Name\n\t}\n\treturn f.Name + f.Kind\n}\n\n", i, j)
		}
		if err := write(fmt.Sprintf("filler_%03d.go", i), b.String()); err != nil {
			return err
		}
	}
	return nil
}

// moduleBeingIndexed starts the indexer and returns while it is still running.
func (st *m3State) moduleBeingIndexed() error {
	return st.startIndexer()
}

// moduleHasBeenIndexed indexes the module to completion and remembers the graph
// — the starting point for the scenario about what a *second* run does.
func (st *m3State) moduleHasBeenIndexed(ctx context.Context) error {
	if err := st.runIndexer(ctx); err != nil {
		return err
	}
	snap, err := snapshot(ctx)
	if err != nil {
		return err
	}
	st.before = snap
	return nil
}

// --- when ------------------------------------------------------------------

// killedAfter is the crash: SIGKILL, which the process cannot trap.
//
// It has to be SIGKILL and not SIGTERM. cmd/codiq turns SIGINT and SIGTERM into
// a graceful cancel, which ends the workflow CANCELLED — a different state
// reached by a different code path, resumed by a different call, and covered by
// its own scenario. Only an untrappable signal leaves the workflow PENDING,
// which is what a crashed executor looks like and what the durability claim is
// about.
func (st *m3State) killedAfter(ctx context.Context, files int) error {
	return st.signalAfter(ctx, files, syscall.SIGKILL)
}

// interruptedAfter is Ctrl-C: SIGTERM, which cmd/codiq traps.
func (st *m3State) interruptedAfter(ctx context.Context, files int) error {
	return st.signalAfter(ctx, files, syscall.SIGTERM)
}

func (st *m3State) signalAfter(ctx context.Context, files int, sig os.Signal) error {
	if st.proc == nil {
		return errors.New("no indexer is running; the Given step did not run")
	}
	if err := st.waitForCheckpoints(ctx, files); err != nil {
		return err
	}
	id, err := st.workflowID(ctx)
	if err != nil {
		return err
	}
	st.runID = id

	if err := st.proc.Process.Signal(sig); err != nil {
		return fmt.Errorf("signal %v: %w", sig, err)
	}
	select {
	case <-st.exit:
	case <-time.After(exitWait):
		return fmt.Errorf("the indexer was still running %s after %v", exitWait, sig)
	}
	// Safe to read only now: Wait does not return until the pipes are drained.
	st.proc, st.report, st.log = nil, st.stdout.String(), st.stderr.String()

	// The ledger as the dying process left it, read before anything resumes it.
	// This is the baseline the replay is later compared against, so it has to be
	// taken here and not reconstructed afterwards.
	st.ledger, err = ledgerOf(ctx, id)
	return err
}

// moduleIndexedAgain runs the binary over the same module, in a new process, and
// waits for it to finish.
func (st *m3State) moduleIndexedAgain(ctx context.Context) error {
	return st.runIndexer(ctx)
}

// --- then ------------------------------------------------------------------

func (st *m3State) runLeftPending(ctx context.Context) error {
	return st.runLeftIn(ctx, "PENDING")
}

func (st *m3State) runLeftCancelled(ctx context.Context) error {
	return st.runLeftIn(ctx, "CANCELLED")
}

func (st *m3State) runLeftIn(ctx context.Context, want string) error {
	if st.runID == "" {
		return errors.New("no run was interrupted; the When step did not run")
	}
	got, err := st.statusOf(ctx, st.runID)
	if err != nil {
		return err
	}
	loaded, err := loadCheckpoints(ctx, st.runID)
	if err != nil {
		return err
	}
	m3Logf("the stopped run %s is %s with %d of its files checkpointed", st.runID, got, loaded)
	return check(func(t assert.TestingT) {
		assert.Equal(t, want, got, "the status %s was left in:\n%s", st.runID, st.log)
	})
}

// saidItContinues is the interrupted process's own account of what it did: it
// stopped waiting, and said where the work went. A run that reported failure
// and nothing else would leave an operator with no reason to think re-running
// is the fix.
func (st *m3State) saidItContinues() error {
	return check(func(t assert.TestingT) {
		assert.Contains(t, st.log, "is checkpointed and continues on the next run",
			"what the interrupted process said")
	})
}

// sameRunFinished is the milestone's claim. Three things have to hold at once
// and each of them can fail on its own: the workflow that was interrupted is
// the one now SUCCESS, the process that finished it said it was continuing
// rather than starting, and no second run was created beside it. The last is
// not pedantry — two indexers over one corpus collide in the link pass, so a
// resume that started a rival would be a bug that this scenario would otherwise
// pass straight over.
func (st *m3State) sameRunFinished(ctx context.Context) error {
	if st.runID == "" {
		return errors.New("no run was interrupted; the When step did not run")
	}
	ids, err := st.workflowIDs(ctx)
	if err != nil {
		return err
	}
	status, err := st.statusOf(ctx, st.runID)
	if err != nil {
		return err
	}
	m3Logf("the same run %s is now %s; %d run(s) recorded for this module", st.runID, status, len(ids))
	for _, line := range strings.Split(st.log, "\n") {
		// The two lines that say a process picked up somebody else's work:
		// DBOS's own recovery notice and cmd/codiq's report of what it attached
		// to. Between them they are the difference between resuming and
		// restarting, which is the whole scenario.
		if strings.Contains(line, "Recovered pending workflows") || strings.Contains(line, "continuing unfinished index") {
			m3Logf("  %s", strings.TrimSpace(line))
		}
	}
	return check(func(t assert.TestingT) {
		assert.Equal(t, []string{st.runID}, ids, "the runs recorded for this module")
		assert.Equal(t, "SUCCESS", status, "the state the resumed run ended in")
		assert.Contains(t, st.log, "continuing unfinished index "+st.runID,
			"what the resuming process said")
	})
}

// notAContinuation is the converse: nothing was picked up, so nothing was said
// about continuing.
func (st *m3State) notAContinuation() error {
	return check(func(t assert.TestingT) {
		assert.NotContains(t, st.log, "continuing unfinished index",
			"the second run treated a finished run as unfinished")
	})
}

func (st *m3State) runsRecorded(ctx context.Context, want int) error {
	ids, err := st.workflowIDs(ctx)
	if err != nil {
		return err
	}
	statuses := make([]string, 0, len(ids))
	for _, id := range ids {
		s, err := st.statusOf(ctx, id)
		if err != nil {
			return err
		}
		statuses = append(statuses, s)
	}
	wantStatuses := slices.Repeat([]string{"SUCCESS"}, len(ids))
	return check(func(t assert.TestingT) {
		assert.Len(t, ids, want, "runs recorded for this module")
		assert.Equal(t, wantStatuses, statuses, "the states those runs ended in")
	})
}

// reportAccountsFor reads the finishing process's report. The Result a workflow
// returns is itself checkpointed and folded across every step, so a process that
// only ran the second half of a run still reports the whole of it — which is
// what makes the report usable at all once resuming is possible.
func (st *m3State) reportAccountsFor(files int) error {
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
		assert.Equal(t, files, gotLoaded, "files loaded, across both halves of the run:\n%s", st.report)
	})
}

// checkpointsSurvived compares the ledger the killed process left against the
// ledger the finished run has. A replayed step reads its recorded output; a
// re-run step writes a new one. So every row is required to be there, under the
// same step id and name, with the same bytes.
func (st *m3State) checkpointsSurvived(ctx context.Context) error {
	if len(st.ledger) == 0 {
		return errors.New("no checkpoints were captured; the When step did not run")
	}
	now, err := ledgerOf(ctx, st.runID)
	if err != nil {
		return err
	}
	byID := make(map[int]checkpoint, len(now))
	for _, c := range now {
		byID[c.FunctionID] = c
	}
	m3Logf("%d of the finished run's %d checkpoints were written before the kill and replayed after it",
		len(st.ledger), len(now))
	return check(func(t assert.TestingT) {
		for _, was := range st.ledger {
			assert.Equal(t, was, byID[was.FunctionID],
				"checkpoint %d (%s) after the run was resumed", was.FunctionID, was.Name)
		}
	})
}

// oneCheckpointPerFile is the same claim from the other side: the finished
// ledger holds one step per file and no more, so no file was started twice.
//
// It is also where the shape of a run is pinned — resolve, walk, one load per
// file, link — which is the sequence index.WorkflowVersion names and the thing
// a replay is replaying.
func (st *m3State) oneCheckpointPerFile(ctx context.Context, files int) error {
	ledger, err := ledgerOf(ctx, st.runID)
	if err != nil {
		return err
	}
	names := map[string]int{}
	loads := 0
	for _, c := range ledger {
		names[c.Name]++
		if strings.HasPrefix(c.Name, "load:") {
			loads++
		}
	}
	return check(func(t assert.TestingT) {
		assert.Equal(t, files, loads, "load steps checkpointed")
		assert.Len(t, names, files+3, "distinct steps: one per file, plus resolve, walk and link")
		assert.Equal(t, 1, names["resolve"], "resolve steps")
		assert.Equal(t, 1, names["walk"], "walk steps")
		assert.Equal(t, 1, names["link"], "link steps")
	})
}

// graphMatchesCleanRun is SPEC.md §14 M3's "graph == M2", stated as an
// experiment rather than as a constant: the graph the interrupted-and-resumed
// run left is remembered, both databases are emptied, the same module is indexed
// by one uninterrupted process, and the two graphs are compared.
//
// Comparing against a run rather than against a literal is what keeps the claim
// honest — it stays true of whatever M2's extractor and link pass produce, and
// it fails if crash recovery changes any of it. Row ids below the file are
// regenerated on every load by design (M2), so the comparison is over row
// counts, the set of file paths, and the derived edges rendered as descriptor
// pairs, which is the same reduction M2's own re-index scenario compares.
//
// SQL, not MCP: the claim is about every row in every table, and the GraphQL
// surface exposes traversals rather than table contents.
func (st *m3State) graphMatchesCleanRun(ctx context.Context) error {
	durable, err := snapshot(ctx)
	if err != nil {
		return err
	}
	if err := st.emptyGraphAndCheckpoints(ctx); err != nil {
		return err
	}
	if err := st.runIndexer(ctx); err != nil {
		return fmt.Errorf("uninterrupted reference index: %w", err)
	}
	clean, err := snapshot(ctx)
	if err != nil {
		return err
	}
	m3Logf("resumed-after-a-kill graph %v", sortedCounts(durable))
	m3Logf("uninterrupted graph        %v", sortedCounts(clean))
	return check(func(t assert.TestingT) {
		assert.Equal(t, clean.counts, durable.counts, "rows per table")
		assert.Equal(t, paths(clean), paths(durable), "files in the graph")
		assert.Equal(t, clean.derived, durable.derived, "cross-file edges")
	})
}

func (st *m3State) graphUnchanged(ctx context.Context) error {
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

func (st *m3State) filesKeptIdentity(ctx context.Context) error {
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

// --- the indexer process ---------------------------------------------------

// startIndexer execs the binary and returns while it is still running.
//
// The DBOS worker is in-process, so the process *is* the executor: there is
// nothing else to kill, and killing this is what a crash means. Which is also
// why it is exec and not index.IndexRepo — a test that called the workflow
// directly could only ever simulate a crash.
func (st *m3State) startIndexer() error {
	if st.proc != nil {
		return errors.New("an indexer is already running")
	}
	if st.repo == "" {
		return errors.New("no module; the Given step did not run")
	}
	dbosDSN, err := dbosConnString(connString)
	if err != nil {
		return err
	}
	st.stdout.Reset()
	st.stderr.Reset()

	// exec.Command and not CommandContext: this process outlives the step that
	// starts it on purpose, and the scenario's own After hook is what cleans it
	// up if a later step never gets to.
	cmd := exec.Command(m3Bin, "-dsn", connString, "-dbos-dsn", dbosDSN, st.repo)
	cmd.Stdout = &st.stdout
	cmd.Stderr = &st.stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start codiq: %w", err)
	}
	st.proc, st.exit = cmd, make(chan error, 1)
	go func(c *exec.Cmd, ch chan<- error) { ch <- c.Wait() }(cmd, st.exit)
	return nil
}

// runIndexer starts the binary and requires it to finish successfully.
func (st *m3State) runIndexer(ctx context.Context) error {
	if err := st.startIndexer(); err != nil {
		return err
	}
	select {
	case err := <-st.exit:
		st.proc, st.report, st.log = nil, st.stdout.String(), st.stderr.String()
		if err != nil {
			return fmt.Errorf("codiq %s: %w\n%s", st.repo, err, st.log)
		}
		return nil
	case <-ctx.Done():
		st.stopIndexer()
		return ctx.Err()
	case <-time.After(indexWait):
		st.stopIndexer()
		return fmt.Errorf("codiq %s did not finish within %s", st.repo, indexWait)
	}
}

// stopIndexer kills whatever is still running, for the After hook.
func (st *m3State) stopIndexer() {
	if st.proc == nil {
		return
	}
	_ = st.proc.Process.Kill()
	<-st.exit
	st.proc = nil
}

// waitForCheckpoints blocks until the running index has durably recorded at
// least files file loads.
//
// This is what makes killing deterministic, and it is deliberately not a sleep.
// A sleep would be a guess about how fast the machine is; the checkpoint count
// is the run's own statement about how far it has got, read from the table the
// resume will read. It also gives the honest failure: if the process exits
// first, the corpus was too small to interrupt, and that is what the message
// says rather than the assertion that would have failed later.
func (st *m3State) waitForCheckpoints(ctx context.Context, files int) error {
	prefix := index.RunIDPrefix(st.repo) + "%"
	deadline := time.Now().Add(checkpointWait)
	for {
		n, err := loadCheckpoints(ctx, prefix)
		if err != nil {
			return err
		}
		if n >= files {
			return nil
		}
		select {
		case err := <-st.exit:
			st.proc, st.exit = nil, nil
			return fmt.Errorf("the indexer finished (%v) with only %d of %d files checkpointed; "+
				"the generated module is too small to interrupt:\n%s", err, n, files, st.stderr.String())
		default:
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("only %d of %d files were checkpointed within %s", n, files, checkpointWait)
		}
		time.Sleep(checkpointPoll)
	}
}

// --- the checkpoint database -----------------------------------------------

// loadCheckpoints counts the file loads durably recorded for runs of one module.
func loadCheckpoints(ctx context.Context, idPrefix string) (int, error) {
	ready, err := dbosMigrated(ctx)
	if err != nil || !ready {
		return 0, err
	}
	var n int
	if err := dbosPool.QueryRow(ctx, `
		SELECT count(*) FROM dbos.operation_outputs
		WHERE workflow_uuid LIKE $1 AND function_name LIKE 'load:%'`, idPrefix).Scan(&n); err != nil {
		return 0, fmt.Errorf("count load checkpoints: %w", err)
	}
	return n, nil
}

// workflowIDs lists this module's runs, oldest first.
//
// Listed by ID prefix, which index.RunIDPrefix documents at length: a workflow's
// recorded input comes back as raw encoded JSON under the default serializer,
// so matching on it silently matches nothing.
func (st *m3State) workflowIDs(ctx context.Context) ([]string, error) {
	rows, err := dbosPool.Query(ctx, `
		SELECT workflow_uuid FROM dbos.workflow_status
		WHERE workflow_uuid LIKE $1 ORDER BY created_at`, index.RunIDPrefix(st.repo)+"%")
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// workflowID is workflowIDs where exactly one is expected.
func (st *m3State) workflowID(ctx context.Context) (string, error) {
	ids, err := st.workflowIDs(ctx)
	if err != nil {
		return "", err
	}
	if len(ids) != 1 {
		return "", fmt.Errorf("expected one run of %s, found %d: %v", st.repo, len(ids), ids)
	}
	return ids[0], nil
}

func (st *m3State) statusOf(ctx context.Context, id string) (string, error) {
	var status string
	err := dbosPool.QueryRow(ctx,
		`SELECT status FROM dbos.workflow_status WHERE workflow_uuid = $1`, id).Scan(&status)
	if err != nil {
		return "", fmt.Errorf("read status of %s: %w", id, err)
	}
	return status, nil
}

// ledgerOf reads a workflow's checkpoints in step order.
func ledgerOf(ctx context.Context, id string) ([]checkpoint, error) {
	rows, err := dbosPool.Query(ctx, `
		SELECT function_id, function_name, coalesce(output, ''), coalesce(error, '')
		FROM dbos.operation_outputs WHERE workflow_uuid = $1 ORDER BY function_id`, id)
	if err != nil {
		return nil, fmt.Errorf("read checkpoints of %s: %w", id, err)
	}
	defer rows.Close()
	var out []checkpoint
	for rows.Next() {
		var c checkpoint
		if err := rows.Scan(&c.FunctionID, &c.Name, &c.Output, &c.Err); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// sortedCounts renders a snapshot's row counts in coreTables order, so the two
// halves of the graph comparison line up when they are read side by side.
func sortedCounts(snap *graphSnapshot) []string {
	out := make([]string, 0, len(coreTables))
	for _, table := range coreTables {
		out = append(out, fmt.Sprintf("%s=%d", table, snap.counts[table]))
	}
	return out
}

// paths is a snapshot's files, sorted — the part of its identity that survives
// a truncate, since the ids do not.
func paths(snap *graphSnapshot) []string {
	out := make([]string, 0, len(snap.fileIDs))
	for path := range snap.fileIDs {
		out = append(out, path)
	}
	slices.Sort(out)
	return out
}
