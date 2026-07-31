// M8's feature suite: features/incremental.feature against the real stack
// (SPEC.md §13, §14 M8).
//
// No new harness and no new stack, for the seventh time: m1_test.go's startStack
// brings up the one composition the package shares, and m2_test.go's m2State
// carries the module copy, the `codiq` invocation, the graph reset, the graph
// snapshot and M1's MCP handshake. M8 changes how the reduce re-links and adds a
// backstop beside the workflow; it changes nothing about how a repository is
// indexed, so a suite that had to build its own indexing machinery to test it
// would be evidence against the milestone rather than for it.
//
// What this file adds is four steps, and each is a claim the unit tests cannot
// make because it is about the deployed system rather than about a package:
//
//   - "the graph is what a full re-link would produce" — the equality obligation,
//     end to end, through the real binary and the real workflow. It reads the
//     derived edges, runs the backstop over the same base facts, reads them
//     again, and requires the two to be identical. link/incremental_test.go
//     proves this over six change shapes with a batch that is a *subset* of the
//     corpus; here the batch is always the whole tree (the feature file says
//     why), so what is added is the rest of the stack rather than another shape.
//   - the corruption and the repair — SPEC.md §14 M8's second stated test.
//   - the interlock, in both directions — the hazard the nightly schedule
//     creates, which only exists between transactions and so cannot be shown
//     anywhere but against a real database.
//
// The backstop is reached through index.RebuildLinks rather than through DBOS.
// That is the on-demand half of Decision 17 and it is what an operator with a
// connection string has; standing up a second DBOS executor inside the test
// process would recover the workflows the M3 and M4 scenarios deliberately leave
// PENDING, which is a much worse thing to do to a shared stack than to test the
// scheduled path's cron expression in index/schedule_test.go instead.
package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/jackc/pgx/v5"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"

	"github.com/gaarutyunov/codiq/index"
)

// blockWait is how long a step waits before calling a lock contended.
//
// It has to be long enough that a slow-but-unblocked call is not mistaken for a
// blocked one, and short enough that four scenarios do not spend a minute
// waiting. Two seconds is two orders of magnitude above the rebuild this corpus
// needs (measured at single-digit milliseconds), so a timeout here means the
// lock and not the machine.
const blockWait = 2 * time.Second

// TestM8Features is the godog entry point for M8, scoped to its own feature file
// so that each milestone's suite owns the scenarios it has steps for.
func TestM8Features(t *testing.T) {
	ctx := context.Background()
	repoRoot = mustRepoRoot(t)

	startStack(t, ctx)

	suite := godog.TestSuite{
		Name:                "m8",
		ScenarioInitializer: InitializeM8Scenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{filepath.Join(repoRoot, "features", "incremental.feature")},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("m8 feature scenarios failed")
	}
}

// m8State is one scenario's state: M2's, plus the transaction the interlock
// scenario holds open.
type m8State struct {
	m2State

	// inflight is an index's transaction, held open by a step so that the next
	// step can find the backstop blocked behind it. It is a real transaction
	// holding the real lock the loader takes, not a stand-in: the step opens it
	// the way index/reduce.go does.
	inflight pgx.Tx
}

func InitializeM8Scenario(sc *godog.ScenarioContext) {
	st := &m8State{}

	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "codiq-m8-tests", Version: "0.1.0"}, nil)
		session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: mcpURL}, nil)
		if err != nil {
			return ctx, fmt.Errorf("mcp handshake with %s: %w", mcpURL, err)
		}
		st.session = session
		return ctx, nil
	})

	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		// The held transaction goes first: it owns a lock every other scenario
		// would then wait on, and a scenario that failed halfway is exactly when
		// that matters.
		if st.inflight != nil {
			_ = st.inflight.Rollback(ctx)
			st.inflight = nil
		}
		if st.session != nil {
			_ = st.session.Close()
			st.session = nil
		}
		if st.tmp != "" {
			_ = os.RemoveAll(st.tmp)
		}
		st.repo, st.tmp, st.report, st.before = "", "", "", nil
		return ctx, nil
	})

	// M2's steps, reused as written: the corpus, the binary, the snapshot.
	sc.Step(`^an empty CodiQ graph$`, st.emptyGraph)
	sc.Step(`^the two-file Go module$`, st.theModule)
	sc.Step(`^the indexed two-file Go module$`, st.theIndexedModule)
	sc.Step(`^the module also holds "([^"]*)":$`, st.moduleAlsoHolds)
	sc.Step(`^"([^"]*)" is rewritten as:$`, st.fileRewritten)
	sc.Step(`^the module is indexed$`, st.moduleIndexed)
	sc.Step(`^the module is indexed again$`, st.moduleIndexed)
	sc.Step(`^the graph is exactly what it was before$`, st.graphUnchanged)

	// M8's own.
	sc.Step(`^the graph is what a full re-link would produce$`, st.equalsAFullRebuild)
	sc.Step(`^a derived edge is deleted and a false one invented$`, st.corruptAnEdge)
	sc.Step(`^the graph is not what it was$`, st.graphDrifted)
	sc.Step(`^the link backstop runs on demand$`, st.backstopRuns)
	sc.Step(`^an index holds its transaction open$`, st.indexInFlight)
	sc.Step(`^the link backstop blocks$`, st.backstopBlocks)
	sc.Step(`^a second index does not block$`, st.secondIndexDoesNotBlock)
	sc.Step(`^the index in flight finishes$`, st.inflightFinishes)
	sc.Step(`^the link backstop runs to completion$`, st.backstopRuns)

	// M1's steps, reused as written: the MCP tool call and the JSON comparison.
	sc.Step(`^an agent asks over MCP:$`, st.agentAsks)
	sc.Step(`^the answer is:$`, st.answerIs)
}

// --- then ------------------------------------------------------------------

// equalsAFullRebuild is the milestone's obligation, stated as one step.
//
// The derived edges as the incremental re-link left them, then the same edges
// after a full rebuild over base facts the rebuild does not touch. They must be
// identical — and identical as *rendered* edges, endpoint descriptors on both
// sides, because graphSnapshot.derived is built that way precisely so that a
// rebuild which keeps the row count while moving an edge fails the comparison
// (m2_test.go).
//
// Running the rebuild is not a side effect to be apologised for: it is the
// reference implementation, it is a pure function of the base facts, and if the
// two agree then leaving the graph in the rebuild's hands leaves it exactly
// where the incremental path had it.
func (st *m8State) equalsAFullRebuild(ctx context.Context) error {
	incremental, err := snapshot(ctx)
	if err != nil {
		return err
	}
	if err := index.RebuildLinks(ctx, pool); err != nil {
		return fmt.Errorf("full re-link: %w", err)
	}
	full, err := snapshot(ctx)
	if err != nil {
		return err
	}
	return check(func(t assert.TestingT) {
		for table, want := range full.derived {
			assert.Equal(t, want, incremental.derived[table],
				"%s: the incremental re-link and the full rebuild disagree", table)
		}
		for _, table := range coreTables {
			assert.Equal(t, full.counts[table], incremental.counts[table],
				"%s row count", table)
		}
		// The base facts are the rebuild's input, so a rebuild that changed one
		// of them would make the comparison above meaningless.
		assert.Equal(t, full.fileIDs, incremental.fileIDs, "a re-link moved a file identity")
	})
}

// corruptAnEdge is drift, injected: one real `resolves_to` row removed and one
// that no derivation could produce put in its place.
//
// Both halves matter and they fail differently. The deletion is the drift an
// invalidation bug leaves when a neighbourhood is computed too small — a
// navigation that returns nothing. The invention is what one leaves when an edge
// is not deleted before being recomputed — a navigation that returns the wrong
// symbol, which is the worse of the two because it looks like an answer. The
// invented row joins two *definitions* across files whose descriptors do not
// match, so nothing in §7's derivation can produce it and the backstop must
// remove it rather than merely add the other back.
func (st *m8State) corruptAnEdge(ctx context.Context) error {
	tag, err := pool.Exec(ctx, `
		DELETE FROM resolves_to
		WHERE (source_id, target_id) IN (
			SELECT e.source_id, e.target_id FROM resolves_to e
			ORDER BY e.source_id, e.target_id
			LIMIT 1)`)
	if err != nil {
		return fmt.Errorf("delete a derived edge: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("no derived edge to delete; the corpus is not linked")
	}

	// A definition in one file pointed at a definition in another, with no
	// descriptor in common — the shape of a stale edge, and not derivable.
	if _, err := pool.Exec(ctx, `
		INSERT INTO resolves_to (source_id, target_id)
		SELECT a.id, b.id
		FROM occurrence a, occurrence b
		WHERE a.role = 'definition' AND b.role = 'definition'
		  AND a.file_id <> b.file_id AND a.descriptor <> b.descriptor
		  AND NOT EXISTS (SELECT 1 FROM resolves_to e WHERE e.source_id = a.id AND e.target_id = b.id)
		ORDER BY a.id, b.id
		LIMIT 1`); err != nil {
		return fmt.Errorf("invent a derived edge: %w", err)
	}
	return nil
}

// graphDrifted is what keeps the repair scenario from passing vacuously: if the
// corruption above ever stops corrupting — because the corpus lost its
// cross-file edges, or because the invented row turned out to be derivable —
// then "the backstop restored it" is a statement about nothing.
func (st *m8State) graphDrifted(ctx context.Context) error {
	if st.before == nil {
		return errors.New("nothing was remembered to compare against")
	}
	now, err := snapshot(ctx)
	if err != nil {
		return err
	}
	return check(func(t assert.TestingT) {
		assert.NotEqual(t, st.before.derived["resolves_to"], now.derived["resolves_to"],
			"the corruption changed nothing, so the repair proves nothing")
	})
}

// backstopRuns is Decision 17's on-demand trigger, called the way an operator
// would: a connection string and one function.
func (st *m8State) backstopRuns(ctx context.Context) error {
	if err := index.RebuildLinks(ctx, pool); err != nil {
		return fmt.Errorf("backstop: %w", err)
	}
	return nil
}

// --- the interlock ---------------------------------------------------------

// indexInFlight opens a transaction holding the lock a batch holds, and leaves
// it open.
//
// It takes the lock by the same statement store.ReplaceFile and link.NewBatch do
// rather than by calling either, which is the point: the two sides of the
// interlock have to agree on a key, and a step that reached for the Go helper
// would still pass if the key ever moved out from under the backstop.
func (st *m8State) indexInFlight(ctx context.Context) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin an index: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended('codiq:link', 0))`); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("take the loader's lock: %w", err)
	}
	st.inflight = tx
	return nil
}

// backstopBlocks is the hazard, prevented. A rebuild started while a batch holds
// its transaction open must wait rather than delete the rows that batch is about
// to point at.
func (st *m8State) backstopBlocks(ctx context.Context) error {
	if st.inflight == nil {
		return errors.New("no index in flight; the When step did not run")
	}
	blocked, cancel := context.WithTimeout(ctx, blockWait)
	defer cancel()
	err := index.RebuildLinks(blocked, pool)
	return check(func(t assert.TestingT) {
		assert.ErrorIs(t, err, context.DeadlineExceeded,
			"the backstop ran straight through an index in flight")
	})
}

// secondIndexDoesNotBlock is the other half, and the one that can fire. An
// interlock built out of a single exclusive lock would satisfy every other
// assertion in this scenario and quietly serialize every index in the system;
// this is what says the loader's half is *shared*.
func (st *m8State) secondIndexDoesNotBlock(ctx context.Context) error {
	if st.inflight == nil {
		return errors.New("no index in flight; the When step did not run")
	}
	quick, cancel := context.WithTimeout(ctx, blockWait)
	defer cancel()

	tx, err := pool.Begin(quick)
	if err != nil {
		return fmt.Errorf("begin a second index: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(quick, `SELECT pg_advisory_xact_lock_shared(hashtextextended('codiq:link', 0))`)
	return check(func(t assert.TestingT) {
		assert.NoError(t, err, "two indexes serialized against each other")
	})
}

// inflightFinishes commits the held transaction, which is the loader releasing
// the lock: the rebuild is only ever waiting, never refused.
func (st *m8State) inflightFinishes(ctx context.Context) error {
	if st.inflight == nil {
		return errors.New("no index in flight; the When step did not run")
	}
	err := st.inflight.Commit(ctx)
	st.inflight = nil
	if err != nil {
		return fmt.Errorf("finish the index in flight: %w", err)
	}
	return nil
}
