// The full re-link backstop (SPEC.md §7, Decision 17, §14 M8).
//
// The incremental re-link of the reduce (link.Batch) is an optimization under an
// equality obligation, and the honest thing to say about any such optimization
// is that it can be wrong in a way nothing notices: an edge that should have
// been recomputed and was not leaves a graph that answers queries, plausibly,
// with a stale row. So §7 keeps the total recompute — link.RebuildAll, which is
// a pure function of the base facts and therefore cannot drift — as a permanent
// backstop, on a nightly schedule and on demand, and Decision 17 makes both
// reuse the same full-rebuild path rather than a second implementation of it.
//
// Two things about that are easy to get wrong, and this file is mostly about
// them.
//
// **A scheduled rebuild is a scheduled collision.** RebuildAll deletes the
// derived tables wholesale, and the derived tables' endpoints are plain FK
// references to `occurrence` rows a loader may be replacing at that moment; the
// two transactions cannot both win, and one loses with SQLSTATE 23503. That is
// the failure two concurrent indexers already have, put on a timer — which is
// worse than the original, because a nightly job that fails one night in ten is
// exactly the kind of thing that gets rediscovered a quarter later. The fix is
// the reader/writer lock in query.sql: every writer of the base facts takes it
// shared (store.ReplaceFile, link.Batch), RebuildAll takes it exclusive, so a
// rebuild waits for the loads in flight and holds off the ones that start while
// it runs, and two loaders are still parallel with each other.
//
// **A scheduled workflow is a separate workflow.** It is registered beside
// IndexRepo rather than folded into it: IndexRepo's step sequence is what
// WorkflowVersion names, and a step added to it would make every in-flight run
// of the previous build unreplayable (dbos.go). Nothing here touches that
// sequence, and nothing here changes what an IndexRepo checkpoint records, which
// is why M8 leaves WorkflowVersion where M6 put it.
package index

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/gaarutyunov/codiq/link"
)

// RebuildWorkflowName is the name the backstop is registered and recorded under,
// for the reason WorkflowName is: DBOS would otherwise record the function's
// fully-qualified Go name and break every scheduled row the day the package
// moves.
const RebuildWorkflowName = "codiq.RebuildLinks"

// RebuildSchedule is when the backstop runs unattended: 03:00 every day.
//
// Six fields, not five. DBOS parses schedules with second precision
// (cron.Second|Minute|Hour|Dom|Month|Dow), so the leading `0` is the second and
// a five-field expression would silently mean something else — `0 3 * * *` would
// be read as "second 0 of minute 3 of every hour", which is 24 full rebuilds a
// day rather than one. schedule_test.go parses this constant with DBOS's own
// parser and asserts the next tick, so the mistake is caught by a test rather
// than by a night of load.
//
// Nightly is what §7 asks for and the right order of magnitude for what this is:
// not a correctness mechanism the graph depends on between runs, but a floor on
// how long an incremental-invalidation bug can go on being invisible. 03:00
// rather than midnight for the ordinary reason — it is the far side of the
// nightly jobs everything else schedules on the hour.
const RebuildSchedule = "0 0 3 * * *"

// Rebuild is what one backstop run reports. It is a workflow's output, so it is
// checkpointed, which is what lets a caller attaching to a run it did not start
// see the same answer as the caller that started it.
type Rebuild struct {
	// ScheduledTime is the tick this run belongs to, as DBOS handed it over. It
	// is the scheduled instant rather than the moment work began: DBOS jitters
	// the start to spread load across executors, and the tick is what identifies
	// the run.
	ScheduledTime time.Time `json:"scheduled_time"`
	// Took is how long the rebuild itself took, measured around the transaction
	// and so including the wait for any index in flight.
	Took time.Duration `json:"took"`
}

// RebuildLinks is Decision 17's on-demand trigger: a full re-link, now, on the
// handle it is given.
//
// It is a plain function rather than a workflow, and that is the whole of its
// design. A full rebuild is one transaction that either commits or does not
// (link.RebuildAll), so there is no partial state for a checkpoint to resume
// from and nothing durability would buy — while a caller that wants the trigger
// is typically an operator with a connection string and no DBOS executor. The
// scheduled path below wraps this in a workflow because a *schedule* needs one,
// not because a rebuild does.
func RebuildLinks(ctx context.Context, db DB) error {
	if err := link.RebuildAll(ctx, db); err != nil {
		return fmt.Errorf("index: rebuild links: %w", err)
	}
	return nil
}

// ScheduledRebuild is the nightly backstop (SPEC.md §7, Decision 17).
//
// Its signature is DBOS's for a scheduled workflow — the tick time is the input
// — and it must stay a top-level named function for the reason IndexRepo must
// (dbos.go: a workflow is identified by its code pointer).
//
// The rebuild is a step so that a run interrupted after the rebuild committed is
// not repeated on recovery. Repeating it would be harmless — RebuildAll is
// idempotent by construction — but a checkpoint is cheaper than a second
// whole-corpus recompute, and "harmless to repeat" is a property worth keeping
// as a safety net rather than spending as a design.
func ScheduledRebuild(ctx dbos.DBOSContext, scheduledTime time.Time) (Rebuild, error) {
	reg := registered.Load()
	if reg == nil {
		// Same reachability as IndexRepo's: a caller that registered the
		// workflow by hand instead of through Register.
		return Rebuild{}, errors.New("index: ScheduledRebuild ran before index.Register")
	}
	return dbos.RunAsStep(ctx, func(sctx context.Context) (Rebuild, error) {
		started := time.Now()
		if err := RebuildLinks(sctx, reg.db); err != nil {
			return Rebuild{}, err
		}
		return Rebuild{ScheduledTime: scheduledTime, Took: time.Since(started)}, nil
	}, dbos.WithStepName("rebuild"))
}

// TriggerRebuild runs the backstop now, through DBOS, and waits for it.
//
// It exists for the caller that already has an executor and wants the scheduled
// path exercised on demand rather than a separate one — the same registered
// workflow, the same step, a workflow id of its own. A caller with only a
// database handle wants RebuildLinks instead.
func TriggerRebuild(ctx dbos.DBOSContext) (Rebuild, error) {
	h, err := dbos.RunWorkflow(ctx, ScheduledRebuild, time.Now().UTC())
	if err != nil {
		return Rebuild{}, fmt.Errorf("index: trigger rebuild: %w", err)
	}
	return h.GetResult()
}
