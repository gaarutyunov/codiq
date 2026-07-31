// M4's feature suite: features/mapreduce.feature against the real stack
// (SPEC.md §13, §14 M4).
//
// There is no new harness here, and deliberately so — this is the fourth
// milestone and the third file to drive the same stack. m1_test.go's startStack
// builds the gopgql image, runs postgres:19beta2 with the committed initdb
// script and serves gopgql-mcp; m2_test.go's snapshot, coreTables, check and
// reportField say what the graph looks like; m3_test.go's m3State carries the
// generated corpus, the indexer process, the kill machinery and the two pools,
// and is *embedded* below rather than reimplemented, so the crash scenario runs
// M3's own code against M4's workflow.
//
// What M4 adds to that is three things it has no way to assert without:
//
//   - A run that is expected to fail. Every earlier suite runs the binary and
//     requires exit zero. Two scenarios here require the opposite, and require
//     the graph to be untouched afterwards, so the exit status is an assertion
//     rather than an error.
//   - A file the process cannot read. It is the only trigger for §5's poison
//     skip that a real repository can contain (features/mapreduce.feature says
//     why), and it does not work at all under a user who bypasses file
//     permissions — so the step that creates one proves the file is unreadable
//     *by this process* before any scenario is allowed to depend on it.
//   - A read of the child workflows' checkpoints. M3 could look at one ledger
//     because the per-file work was a step of the parent. At M4 it is a
//     workflow of its own with a ledger of its own, and whether a resumed run
//     re-extracted a file is written down only there.
//
// Navigation claims go over MCP, as in every suite before this one. The
// durability and whole-graph claims are SQL, for the reason m2_test.go's
// graphUnchanged gives: GraphQL exposes traversals, not table contents, and
// says nothing at all about dbos.workflow_status.
package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cucumber/godog"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"

	"github.com/gaarutyunov/codiq/index"
)

// refusalConstraint is the name of the CHECK constraint the atomicity scenarios
// add to `occurrence`, and drop again when the scenario ends.
//
// A constraint rather than a trigger because it is the smaller lie: there is no
// procedure, no test-only code path, nothing the indexer can observe about it
// beyond PostgreSQL refusing a row — which is the collaborator failure
// index/reduce.go's transaction exists to survive. Row-level constraints are
// enforced against binary COPY exactly as they are against INSERT, which is
// what makes it reachable at all (store loads six of seven tables by COPY).
const refusalConstraint = "codiq_test_refuses_type"

// TestM4Features is the godog entry point for M4, scoped to its own feature file
// so that each milestone's suite owns the scenarios it has steps for.
//
// The stack it runs against is the package's (m1_test.go's startStack), brought
// up by whichever suite ran first. The whole graph is inside several assertions
// below, so no other suite's corpus may be in the database when they run — which
// is what the Background is for, and was true of the scenario before this one
// long before it was true of the suite before this one.
func TestM4Features(t *testing.T) {
	ctx := context.Background()
	repoRoot = mustRepoRoot(t)
	m3Log = t

	startStack(t, ctx)

	suite := godog.TestSuite{
		Name:                "m4",
		ScenarioInitializer: InitializeM4Scenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{filepath.Join(repoRoot, "features", "mapreduce.feature")},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("m4 feature scenarios failed")
	}
}

// m4State is one scenario's state: everything M3 needed, plus whether this
// scenario has told the database to refuse a write.
//
// m3State is embedded by value, so every step it implements — the generated
// corpus, "the module is being indexed", the kill, "it is the same run that
// finished" — is available here as written, and the fields those steps read
// (repo, report, log, runID, before) are the same fields the steps below write.
type m4State struct {
	m3State

	// refused is the type name the database is currently refusing, empty when
	// it is refusing nothing. The After hook drops the constraint on the way
	// out, since it survives the Background's TRUNCATE.
	refused string

	// extracted is every `extract:` checkpoint the interrupted run's map tasks
	// had written, read before anything resumed them. m3State.ledger is the
	// parent's; this is the children's, which is where the per-file work lives
	// since M4 and the only place a re-extraction would be visible.
	extracted []extractCheckpoint
}

func InitializeM4Scenario(sc *godog.ScenarioContext) {
	st := &m4State{}

	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "codiq-m4-tests", Version: "0.1.0"}, nil)
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
		// A scenario that failed between starting an indexer and killing it
		// would otherwise leave one running against the next scenario's
		// database, and its workflow PENDING for the next Launch to recover.
		st.stopIndexer()
		// The constraint outlives a TRUNCATE, so dropping it is not optional
		// and not conditional: a scenario that failed before its own cleanup
		// step would poison every later one.
		if err := stopRefusing(ctx); err != nil {
			return ctx, err
		}
		st.refused, st.extracted = "", nil
		if st.tmp != "" {
			// The unreadable file is mode 0000; removing it needs write
			// permission on the directory, which is the temp directory's, so
			// RemoveAll copes without chmod.
			_ = os.RemoveAll(st.tmp)
		}
		st.tmp, st.repo, st.report, st.log, st.runID, st.ledger, st.before = "", "", "", "", "", nil, nil
		return ctx, nil
	})

	// M3's steps, reused as written: the corpus, the running indexer, the kill,
	// and the claim that the run which finished is the run that was killed.
	sc.Step(`^an empty CodiQ graph and no checkpoints$`, st.emptyGraphAndCheckpoints)
	sc.Step(`^a Go module of (\d+) files$`, st.aModuleOf)
	sc.Step(`^the module is being indexed$`, st.moduleBeingIndexed)
	sc.Step(`^the indexer is killed once (\d+) files are checkpointed$`, st.killedAfter)
	sc.Step(`^it is the same run that finished$`, st.sameRunFinished)
	sc.Step(`^it is not a continuation$`, st.notAContinuation)
	sc.Step(`^the graph is exactly what it was before$`, st.graphUnchanged)
	sc.Step(`^every file kept the identity it was first given$`, st.filesKeptIdentity)

	// M4's own.
	sc.Step(`^the indexed Go module of (\d+) files$`, st.indexedModuleOf)
	sc.Step(`^"([^"]*)" cannot be read$`, st.cannotBeRead)
	sc.Step(`^the database refuses to store the type "([^"]*)"$`, st.refuseType)
	sc.Step(`^the database stops refusing$`, st.stopRefusingType)
	sc.Step(`^"([^"]*)" is added, defining "([^"]*)"$`, st.addTypeFile)
	sc.Step(`^the module is indexed$`, st.moduleIndexed)
	sc.Step(`^the module is indexed again$`, st.moduleIndexed)
	sc.Step(`^the module is indexed and fails$`, st.moduleIndexedFailing)
	sc.Step(`^the run indexed (\d+) files and loaded (\d+)$`, st.runCounted)
	sc.Step(`^the report names "([^"]*)" as skipped$`, st.reportNamesSkipped)
	sc.Step(`^the reason given is the read failure and not a parse error$`, st.reasonIsTheReadFailure)
	sc.Step(`^"([^"]*)" is not in the graph$`, st.fileNotInGraph)
	sc.Step(`^"([^"]*)" is nowhere in the graph$`, st.nameNowhereInGraph)
	sc.Step(`^the runs recorded are, oldest first: (.+)$`, st.runsRecordedAre)
	sc.Step(`^every extraction the killed run had checkpointed is still exactly what it wrote$`, st.extractionsSurvived)
	sc.Step(`^exactly (\d+) map tasks recorded an extraction$`, st.mapTasksRecorded)

	// M1's steps, reused as written: the MCP tool call and the JSON comparison.
	sc.Step(`^an agent asks over MCP:$`, st.agentAsks)
	sc.Step(`^the answer is:$`, st.answerIs)
}

// --- given -----------------------------------------------------------------

// indexedModuleOf is M3's generated corpus plus one successful index plus a
// snapshot — the starting point for the scenarios about what the *next* batch
// does to a graph that already exists.
//
// The snapshot is the whole point of indexing first. A reduce that fails over
// an empty graph could only prove that nothing was written; over a populated
// one it proves that nothing was *un*written, which is the stronger half of
// SPEC.md §6's atomicity and the half a per-file transaction would fail.
func (st *m4State) indexedModuleOf(ctx context.Context, files int) error {
	if err := st.aModuleOf(files); err != nil {
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

// cannotBeRead writes a real Go file into the module and takes every permission
// off it, which is the one poison SPEC.md §5 describes that a repository can
// actually hold: `walk` selects it (extract.Supported reads the extension, not
// the file) and os.ReadFile refuses it.
//
// Then it proves the file is unreadable, and that is not belt and braces. Mode
// 0000 does not stop a process running as root, and it does not stop anything
// holding CAP_DAC_OVERRIDE — under either, the indexer reads the file happily,
// every assertion in the scenario passes, and the scenario proves nothing at
// all. That is worse than not having it, so the failure is made loud here,
// where the diagnosis is one sentence, rather than left to a count that happens
// to come out right.
//
// It fails rather than skips on purpose. A skipped scenario in a suite that
// exists to demonstrate a milestone's acceptance criterion is a silent hole;
// the honest report of "this cannot be tested as root" is a red suite.
func (st *m4State) cannotBeRead(name string) error {
	if st.repo == "" {
		return errors.New("no module; the Given step did not run")
	}
	path := filepath.Join(st.repo, name)
	body := "package main\n\ntype Unreadable struct{}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o000); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		return fmt.Errorf("this scenario cannot be run as root: mode 0000 does not stop uid 0, "+
			"so %s would be indexed and the poison-file claim would pass vacuously", name)
	}
	if _, err := os.ReadFile(path); err == nil {
		return fmt.Errorf("%s is still readable with mode 0000, so the map task will not fail "+
			"and this scenario would prove nothing", name)
	}
	return nil
}

// refuseType tells the database to reject any occurrence with this name.
//
// A CHECK constraint on the real `occurrence` table: PostgreSQL enforces it
// against the binary COPY store uses exactly as it would against an INSERT, so
// the reduce's transaction fails on a real write to a real table, which is the
// failure index/reduce.go documents as the one it is defending against.
//
// The constraint is added rather than the row made to violate an existing one
// because there is nothing about a Go type CodiQ's schema forbids — every
// column the extractor fills is a plain string. Inventing a forbidden value is
// the smallest arrangement that produces a genuine "the database refused this
// write", and it touches no product code.
func (st *m4State) refuseType(ctx context.Context, name string) error {
	if _, err := pool.Exec(ctx, fmt.Sprintf(
		`ALTER TABLE occurrence ADD CONSTRAINT %s CHECK (name <> %s)`,
		refusalConstraint, quoteLiteral(name))); err != nil {
		return fmt.Errorf("refuse %q: %w", name, err)
	}
	st.refused = name
	return nil
}

// addTypeFile puts one more file in the module, declaring the type the database
// is refusing.
//
// The name is what makes it the *last* file of the batch, and that is the whole
// design of the atomicity scenario: the walk is sorted, so a file whose name
// sorts after every other one is loaded after every other one, and the refusal
// lands on a transaction that has already rewritten all of them.
func (st *m4State) addTypeFile(name, typeName string) error {
	if st.repo == "" {
		return errors.New("no module; the Given step did not run")
	}
	body := fmt.Sprintf("package main\n\ntype %s struct {\n\tName string\n}\n", typeName)
	if err := os.WriteFile(filepath.Join(st.repo, name), []byte(body), 0o644); err != nil {
		return err
	}
	if paths, err := lastPath(st.repo); err != nil {
		return err
	} else if paths != name {
		return fmt.Errorf("%s is not the last file of the batch (%s is); "+
			"the atomicity claim is about a failure after every other file has been written", name, paths)
	}
	return nil
}

// --- when ------------------------------------------------------------------

// moduleIndexed runs the binary over the module and requires it to succeed.
//
// Its own runner rather than m3State.runIndexer for one reason: -v. The report
// lists a skipped file either way, but only -v prints *why* it was skipped, and
// the first scenario's central claim is about the reason rather than the count
// (index.Skip, cmd/codiq report).
func (st *m4State) moduleIndexed(ctx context.Context) error {
	err := st.codiq(ctx)
	if err != nil {
		return fmt.Errorf("codiq %s: %w\n%s", st.repo, err, st.log)
	}
	return nil
}

// moduleIndexedFailing is the same run with the opposite expectation: the batch
// is refused, so the process must exit non-zero. A run that succeeded here would
// mean the arrangement never took effect, which is a different failure from the
// one the Then steps are looking for and deserves to be said so.
func (st *m4State) moduleIndexedFailing(ctx context.Context) error {
	if err := st.codiq(ctx); err == nil {
		return fmt.Errorf("codiq %s succeeded; the database was supposed to refuse the batch:\n%s",
			st.repo, st.log)
	}
	return nil
}

// codiq runs the real binary to completion, keeping its report and its log, and
// reports what it exited with.
//
// Foreground and exec.CommandContext, unlike m3State.startIndexer, because
// nothing here signals the process: these scenarios are about what a run does
// when it is left alone. stdout and stderr are captured apart rather than
// combined so that the report can be parsed while the log is searched for what
// the process said about resuming (m3State.notAContinuation).
func (st *m4State) codiq(ctx context.Context) error {
	if st.repo == "" {
		return errors.New("no module; the Given step did not run")
	}
	dbosDSN, err := dbosConnString(connString)
	if err != nil {
		return err
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, codiqBin, "-dsn", connString, "-dbos-dsn", dbosDSN, "-v", st.repo)
	cmd.Env = codiqEnv()
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	st.report, st.log = stdout.String(), stderr.String()
	return runErr
}

// killedAfter is M3's kill with one thing added: the map tasks' own checkpoints,
// read while the killed process's workflow is still exactly as it left it.
//
// It has to happen here and not in a Then step. m3State.signalAfter already
// captures the *parent's* ledger at this moment for the same reason — the
// baseline for a replay comparison can only be taken before anything replays —
// and at M4 the per-file work is not in that ledger at all. The next step in
// every scenario that kills is the resume, so this is the last moment the
// killed run's children can be seen unresumed.
func (st *m4State) killedAfter(ctx context.Context, files int) error {
	if err := st.m3State.killedAfter(ctx, files); err != nil {
		return err
	}
	byWorkflow, err := extractCheckpoints(ctx, index.RunIDPrefix(st.repo)+"%")
	if err != nil {
		return err
	}
	for id, cs := range byWorkflow {
		for _, c := range cs {
			st.extracted = append(st.extracted, extractCheckpoint{checkpoint: c, Workflow: id})
		}
	}
	slices.SortFunc(st.extracted, func(a, b extractCheckpoint) int {
		return strings.Compare(extractKey(a.Workflow, a.checkpoint), extractKey(b.Workflow, b.checkpoint))
	})
	if len(st.extracted) == 0 {
		return fmt.Errorf("the killed run had checkpointed no extractions, so there is nothing "+
			"to prove it did not repeat; %d files were meant to be done", files)
	}
	m3Logf("the killed run left %d extraction(s) checkpointed across %d map task(s)",
		len(st.extracted), len(byWorkflow))
	return nil
}

// stopRefusingType lifts the constraint, so the next run can write what the
// previous one could not.
func (st *m4State) stopRefusingType(ctx context.Context) error {
	if st.refused == "" {
		return errors.New("the database was not refusing anything; the Given step did not run")
	}
	if err := stopRefusing(ctx); err != nil {
		return err
	}
	st.refused = ""
	return nil
}

// --- then ------------------------------------------------------------------

// runCounted reads the counts back out of the report the binary printed, so the
// assertion is about the run's own account of what it did rather than about rows
// an earlier scenario could have left (m2_test.go's step of the same name).
func (st *m4State) runCounted(files, loaded int) error {
	gotFiles, err := reportField(st.report, "files")
	if err != nil {
		return err
	}
	gotLoaded, err := reportField(st.report, "loaded")
	if err != nil {
		return err
	}
	gotSkipped, err := reportField(st.report, "skipped")
	if err != nil {
		return err
	}
	return check(func(t assert.TestingT) {
		assert.Equal(t, files, gotFiles, "files the walk selected:\n%s", st.report)
		assert.Equal(t, loaded, gotLoaded, "files loaded:\n%s", st.report)
		assert.Equal(t, files-loaded, gotSkipped, "files skipped:\n%s", st.report)
	})
}

// reportNamesSkipped is SPEC.md §5's "flagged", read literally. cmd/codiq lists
// every skipped file rather than counting them because a file silently missing
// from the graph is indistinguishable from a file with nothing in it — so the
// path has to be in the report, on the line under the skipped count.
func (st *m4State) reportNamesSkipped(path string) error {
	listed := skippedLines(st.report)
	m3Logf("the run reported %d skipped file(s): %v", len(listed), listed)
	return check(func(t assert.TestingT) {
		assert.Equal(t, []string{path}, listed, "the files the run named as skipped:\n%s", st.report)
	})
}

// reasonIsTheReadFailure is the discrimination M4 introduced and index.Skip
// documents: a map task that failed and a file the extractor could not parse
// are both skips, and they are not the same finding.
//
// This one is the first — the file was never read, so it was never parsed, so
// calling it a parse failure would be a false statement about the tree. The
// evidence is the reason `-v` prints, which is index.Skip's error rendered:
// mapFiles wraps a failed task as `index: extract <path>: …` and mints
// store.ErrParseFailed's message only for the other case, so the presence of
// one and the absence of the other is exactly the distinction.
func (st *m4State) reasonIsTheReadFailure() error {
	reasons := skippedReasons(st.report)
	m3Logf("the run gave %d reason(s) for its skips: %v", len(reasons), reasons)
	joined := strings.Join(reasons, "\n")
	return check(func(t assert.TestingT) {
		assert.Len(t, reasons, 1, "reasons printed under -v:\n%s", st.report)
		assert.Contains(t, joined, "index: extract ", "the failed map task, named as one")
		assert.Contains(t, joined, "permission denied", "what the operating system said")
		assert.NotContains(t, joined, "parse error", "a file that was never read was never parsed")
	})
}

// fileNotInGraph is the other half of a skip: nothing was written for the file,
// so it is absent rather than present and empty.
//
// SQL, not MCP, for m2_test.go's definesNothing reason in reverse — `{ file }`
// would answer the same for a file with no rows as for a file that is not there
// — and because the claim is the absence of a row, which a traversal cannot
// return.
func (st *m4State) fileNotInGraph(ctx context.Context, path string) error {
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM file WHERE path = $1`, path).Scan(&n); err != nil {
		return fmt.Errorf("look for %s: %w", path, err)
	}
	return check(func(t assert.TestingT) {
		assert.Zero(t, n, "rows in `file` for the skipped %s", path)
	})
}

// nameNowhereInGraph is the shallow half of the atomicity claim — the row the
// database refused is not there — stated separately from graphUnchanged because
// the two fail for different reasons and a reader deserves to know which.
func (st *m4State) nameNowhereInGraph(ctx context.Context, name string) error {
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM occurrence WHERE name = $1`, name).Scan(&n); err != nil {
		return fmt.Errorf("look for %s: %w", name, err)
	}
	return check(func(t assert.TestingT) {
		assert.Zero(t, n, "occurrences named %s after the batch was refused", name)
	})
}

// runsRecordedAre pins the whole run history of the module, in order.
//
// The sequence is the assertion and not just its last element. A refused batch
// has to leave an ERROR run behind — that is the state recovery ignores and
// cmd/codiq's start does not list, which is why the next invocation is a new
// run rather than a replay of a recorded failure. And the run *before* it has
// to still be SUCCESS: the failing batch must not have reopened, retried or
// otherwise disturbed the index that was already there, which is the same claim
// graphUnchanged makes about the rows, made here about the ledger.
//
// The feature says "successful" and "refused" rather than DBOS's own words,
// because what a scenario is entitled to talk about is what happened to the
// run; SUCCESS and ERROR are how this database spells it.
func (st *m4State) runsRecordedAre(ctx context.Context, sequence string) error {
	want := make([]string, 0, 3)
	for _, word := range strings.Split(sequence, ",") {
		switch strings.TrimSpace(word) {
		case "successful":
			want = append(want, "SUCCESS")
		case "refused":
			want = append(want, "ERROR")
		default:
			return fmt.Errorf("unknown run outcome %q in %q", strings.TrimSpace(word), sequence)
		}
	}

	ids, err := st.workflowIDs(ctx)
	if err != nil {
		return err
	}
	got := make([]string, 0, len(ids))
	for _, id := range ids {
		s, err := st.statusOf(ctx, id)
		if err != nil {
			return err
		}
		got = append(got, s)
	}
	m3Logf("the %d run(s) recorded for this module are %v", len(got), got)
	return check(func(t assert.TestingT) {
		assert.Equal(t, want, got, "the states this module's runs ended in, oldest first:\n%s", st.log)
	})
}

// extractionsSurvived is the map phase's determinism claim, and the reason it
// is asserted at the child level rather than the parent's.
//
// Every extract checkpoint the killed process had written is captured by
// killedAfter, before anything resumes the run, and compared here against what
// is there once another process has finished it. Identity is the pair (child
// workflow, step id): a file extracted a second time would have been extracted
// under a different child, because a child's id is derived from the parent's
// step id at enqueue time and the parent enqueues in walk order. The recorded
// output is compared too, byte for byte, since that is what a replay reads
// instead of parsing the file again.
func (st *m4State) extractionsSurvived(ctx context.Context) error {
	if len(st.extracted) == 0 {
		return errors.New("no extractions were captured; the When step did not run")
	}
	now, err := extractCheckpoints(ctx, index.RunIDPrefix(st.repo)+"%")
	if err != nil {
		return err
	}
	byKey := make(map[string]extractCheckpoint, len(now))
	for id, cs := range now {
		for _, c := range cs {
			byKey[extractKey(id, c)] = extractCheckpoint{checkpoint: c, Workflow: id}
		}
	}
	m3Logf("%d of the finished run's %d extractions were checkpointed before the kill",
		len(st.extracted), len(byKey))
	return check(func(t assert.TestingT) {
		for _, was := range st.extracted {
			assert.Equal(t, was, byKey[extractKey(was.Workflow, was.checkpoint)],
				"the extraction %s recorded as %s, after the run was resumed", was.Workflow, was.Name)
		}
	})
}

// mapTasksRecorded pins the count from the other side: one child workflow
// recorded an extraction per file, and not one more.
//
// A resume that renumbered the children would leave the originals behind and
// add its own, so the number would be larger than the corpus even though every
// file was in the graph exactly once — the failure this suite exists to catch,
// and the one a graph comparison alone would walk straight past.
func (st *m4State) mapTasksRecorded(ctx context.Context, files int) error {
	now, err := extractCheckpoints(ctx, index.RunIDPrefix(st.repo)+"%")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(now))
	extra := 0
	for _, cs := range now {
		if len(cs) != 1 {
			extra++
		}
		names = append(names, cs[0].Name)
	}
	slices.Sort(names)
	names = slices.Compact(names)
	return check(func(t assert.TestingT) {
		assert.Len(t, now, files, "child workflows that recorded an extraction")
		assert.Zero(t, extra, "child workflows that recorded more than one extraction")
		assert.Len(t, names, files, "distinct files extracted")
	})
}

// --- the checkpoint database -----------------------------------------------

// extractCheckpoint is one child workflow's extract step: m3_test.go's
// checkpoint plus the workflow it belongs to, which at M4 is what identifies
// the file's map task.
type extractCheckpoint struct {
	checkpoint
	Workflow string
}

// extractKey identifies one extraction: the child workflow that recorded it and
// the step id it recorded it under. Both halves are needed — a step id alone
// repeats across children, and a workflow alone would hide a second extraction
// inside the same task.
func extractKey(workflow string, c checkpoint) string {
	return fmt.Sprintf("%s#%d", workflow, c.FunctionID)
}

// extractCheckpoints reads every `extract:` checkpoint under a run prefix,
// grouped by the child workflow that wrote it.
//
// The prefix finds the children because DBOS names one `<parent id>-<step id>`
// — index.RunIDPrefix is written around that property — and the step name
// filter is what keeps the parent's own ledger out: `resolve`, `walk`, the
// enqueues and the reduce all live under the same prefix and are not
// extractions.
func extractCheckpoints(ctx context.Context, idPrefix string) (map[string][]checkpoint, error) {
	ready, err := dbosMigrated(ctx)
	if err != nil || !ready {
		return nil, err
	}
	rows, err := dbosPool.Query(ctx, `
		SELECT workflow_uuid, function_id, function_name, coalesce(output, ''), coalesce(error, '')
		FROM dbos.operation_outputs
		WHERE workflow_uuid LIKE $1 AND function_name LIKE 'extract:%'
		ORDER BY workflow_uuid, function_id`, idPrefix)
	if err != nil {
		return nil, fmt.Errorf("read extract checkpoints: %w", err)
	}
	defer rows.Close()
	out := map[string][]checkpoint{}
	for rows.Next() {
		var id string
		var c checkpoint
		if err := rows.Scan(&id, &c.FunctionID, &c.Name, &c.Output, &c.Err); err != nil {
			return nil, err
		}
		out[id] = append(out[id], c)
	}
	return out, rows.Err()
}

// --- helpers ---------------------------------------------------------------

// stopRefusing drops the constraint if it is there. Unconditional and
// idempotent, because the After hook calls it whether the scenario installed one
// or not and a leftover would fail every scenario after it.
func stopRefusing(ctx context.Context) error {
	if _, err := pool.Exec(ctx,
		`ALTER TABLE occurrence DROP CONSTRAINT IF EXISTS `+refusalConstraint); err != nil {
		return fmt.Errorf("stop refusing: %w", err)
	}
	return nil
}

// quoteLiteral renders a string as a SQL literal. The only value it ever sees is
// a type name out of the feature file, and ALTER TABLE takes no parameters, so
// the quoting is done rather than delegated.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// lastPath reports the name of the file the walk would load last: the largest
// entry, since walk sorts and the module is flat.
func lastPath(repo string) (string, error) {
	entries, err := os.ReadDir(repo)
	if err != nil {
		return "", err
	}
	last := ""
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".go" {
			continue
		}
		if e.Name() > last {
			last = e.Name()
		}
	}
	if last == "" {
		return "", fmt.Errorf("%s holds no Go files", repo)
	}
	return last, nil
}

// skippedLines pulls the paths cmd/codiq listed under its skipped count.
//
// The line is the path alone, or `path: reason` under -v, which this suite
// always passes — so the path is what is in front of the first `: `, and a line
// with no reason on it is a path in full (cmd/codiq report).
func skippedLines(report string) []string {
	var out []string
	for _, line := range skippedBlock(report) {
		path, _, _ := strings.Cut(line, ": ")
		out = append(out, path)
	}
	return out
}

// skippedReasons is the same block read for what -v adds: `path: reason`.
func skippedReasons(report string) []string {
	var out []string
	for _, line := range skippedBlock(report) {
		if _, reason, ok := strings.Cut(strings.TrimSpace(line), ": "); ok {
			out = append(out, reason)
		}
	}
	return out
}

// skippedBlock returns the report's skipped-file lines: everything indented
// under the `skipped` line, up to the closing hint or the end.
func skippedBlock(report string) []string {
	var out []string
	inBlock := false
	for _, line := range strings.Split(report, "\n") {
		switch {
		case strings.HasPrefix(strings.TrimSpace(line), "skipped "):
			inBlock = true
		case !inBlock:
		case strings.HasPrefix(line, "                "):
			out = append(out, strings.TrimSpace(line))
		default:
			inBlock = false
		}
	}
	return out
}
