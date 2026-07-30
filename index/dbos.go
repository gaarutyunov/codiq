// DBOS orchestration for the loader (SPEC.md §9, §14 M4).
//
// index.go is M2: one process walks a tree, loads every file in its own
// transaction, rebuilds the edges, and if it dies halfway the next run starts
// from nothing. This file is the same pipeline expressed as SPEC.md §9's
// map-reduce batch: a parent workflow freezes the file list, enqueues one
// *extract workflow per file* on the durable "extract" queue, gathers the facts
// they produce, and hands the successful subset to a single reduce step that
// writes the whole batch in one transaction (reduce.go). Nothing about *what*
// is loaded changes — the same walk, the same extract, the same ReplaceFile and
// the same RebuildAll, in the same order.
//
// Four properties of DBOS shape the code, and each is load-bearing rather than
// stylistic:
//
//   - A workflow is identified by its function's code pointer, so IndexRepo and
//     extractFile have to be top-level named functions. A closure or a method
//     value would register under a name that changes between builds, and
//     registering twice panics with a conflicting-registration error.
//   - A queue carries *workflows*, not steps: dbos.WithQueue is a
//     WorkflowOption. The per-file map task is therefore a registered workflow
//     of its own, not a dbos.RunAsStep, and that is why M4 is a bigger change to
//     this file than §14 M4's skeleton makes it look.
//   - Steps are replayed by *step ID*, and a step ID is handed out in the order
//     the step is started. M3 had to reach for dbos.Go to keep that order stable
//     while running files concurrently. M4 does not: enqueueing is a plain
//     sequential loop and the concurrency lives in the queue's workers. See
//     mapFiles.
//   - Steps are at-least-once, not exactly-once. A step whose side effect
//     committed but whose checkpoint did not is re-run on recovery. That is safe
//     here only because store.ReplaceFile replaces a file's rows rather than
//     merging them and link.RebuildAll recomputes the derived layer from
//     scratch, so running the reduce twice is running it once; that idempotence
//     is a durability requirement and not merely a nice property (index.go's Run
//     doc, SPEC.md §6).
//
// What DBOS deliberately does *not* take over is the transaction; reduce.go's
// comment has that argument in full.
package index

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/google/uuid"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/facts"
	"github.com/gaarutyunov/codiq/store"
)

// WorkflowName is the name IndexRepo is registered and recorded under, and
// ExtractWorkflowName the name of the per-file map task.
//
// DBOS defaults to the function's fully-qualified Go name, which would put
// `github.com/gaarutyunov/codiq/index.IndexRepo` in every checkpoint row and
// break every in-flight workflow the day the package moves. Naming them
// explicitly makes the recorded name a deliberate, stable identifier — and one
// a caller can filter `dbos.ListWorkflows` on without reproducing DBOS's own
// name-mangling rules.
const (
	WorkflowName        = "codiq.IndexRepo"
	ExtractWorkflowName = "codiq.ExtractFile"
)

// QueueName is the durable queue the per-file map tasks run on (SPEC.md §9,
// §14 M4's `queue("extract")`).
//
// It is one queue for the whole program, not one per run: a queue is a
// concurrency budget over a Postgres-backed work table, and what it has to bound
// is how many files this executor parses at once, which is a property of the
// machine rather than of the repository being indexed.
const QueueName = "extract"

// queuePollInterval is how often the queue's worker looks for tasks to start.
//
// It has to be set, and it is the one number in this file that was chosen by
// measurement rather than by argument. DBOS starts at most WorkerConcurrency
// tasks per poll, so the rate at which a batch drains is the concurrency divided
// by this interval — and the default interval is one second, which is sized for
// queues whose tasks take seconds. A CodiQ map task is one file read and one
// tree-sitter parse. Leaving the default makes the queue's cadence, not the
// machine, the thing that decides how long an index takes: over a generated
// 400-file module, with the checkpoint database's commit latency taken out of
// the picture so the cadence is what is being measured,
//
//	workers=4  poll=1s     1m48s
//	workers=4  poll=100ms  20.6s
//	workers=4  poll=20ms   12.1s
//	workers=4  poll=10ms    8.8s   <- the floor: enqueues plus the reduce
//	workers=64 poll=100ms   8.7s   <- the same floor, reached the other way
//
// 10ms is where the curve flattens, and it is the right order of magnitude for
// the same reason the default is the wrong one: a polling interval should track
// the length of a task. Raising the concurrency instead reaches the same floor
// (last row) but buys it by admitting more work than the machine has cores for,
// which is a worse thing to claim in Result.Concurrency.
//
// What it costs is an idle probe every 10ms while the queue is empty — during
// startup and during the reduce. That is a cheap indexed UPDATE against a
// database that exists for nothing else, and codiq is a one-shot program, so the
// idle window is seconds rather than the life of a service.
const queuePollInterval = 10 * time.Millisecond

// WorkflowVersion is the application version IndexRepo's workflows are recorded
// under, and the version a process will recover.
//
// It must be pinned. DBOS defaults it to a hash of the running binary, and
// recovery only ever picks up workflows whose (executor, application version)
// matches the running process — so with the default, rebuilding the binary
// makes every workflow the old one left behind unrecoverable: they stay PENDING
// forever and a handle on them never resolves. That is the opposite of what
// durability is for, since redeploying is the most likely reason a process died
// in the first place.
//
// So the version is a constant, and it names the *step sequence*, not the
// build. Bump it when a change to IndexRepo would make an in-flight workflow
// unreplayable — a step added, removed or reordered — and leave it alone for
// everything else. M4 is exactly such a change: M3's run was resolve, one load
// per file, link; M4's is resolve, walk, one enqueue and one await per file,
// reduce. An M3 workflow left PENDING cannot be replayed against this code and
// must not be, hence `-2`. An operator can still override it per-process with
// DBOS__APPVERSION.
const WorkflowVersion = "codiq-index-2"

// RunIDPrefix is the prefix shared by the workflow IDs of every index of target,
// which must be an absolute path. NewRunID mints one; a caller finds an
// unfinished run of the same tree by listing on the prefix.
//
// Identifying a run by its ID rather than by its recorded input is not a
// stylistic choice. dbos.ListWorkflows can load a workflow's input, but with the
// default serializer it hands back the *raw encoded JSON* — `"\"/repo\""`, quotes
// and all — rather than the string that was passed in (measured against v0.20.0;
// dbos.WorkflowStatus.Input is typed `any`, which reads as though it were
// decoded). Matching on that would be matching on an encoding detail that a
// custom Config.Serializer changes out from under it. A workflow ID is a plain
// text column that DBOS filters with LIKE, and it means the same thing whatever
// the payloads are encoded with.
//
// The path is hashed rather than embedded so that the prefix is unambiguous: a
// separator alone is not enough when the separator can occur in a path, and
// `/a#` is a prefix of `/a#b#` while `/a` is not a prefix of `/a#b`. Fixed-width
// hex has neither problem, and contains no LIKE metacharacter either.
//
// The map tasks fall under the prefix too, and by construction: DBOS names a
// child workflow `<parent id>-<step id>`, so listing on a run's prefix finds the
// run and everything it enqueued.
func RunIDPrefix(target string) string {
	sum := sha256.Sum256([]byte(target))
	return "index-" + hex.EncodeToString(sum[:8]) + "-"
}

// NewRunID returns a fresh workflow ID for indexing target.
//
// Fresh, and not a pure function of the target, because indexing is idempotent
// and meant to be re-run: a stable ID would make the second run return the first
// run's checkpointed result and touch nothing. Resuming is what RunIDPrefix is
// for, and it is a different question — "is there an unfinished one?" — from
// "start one".
func NewRunID(target string) string { return RunIDPrefix(target) + uuid.NewString() }

// registered is what IndexRepo and extractFile run against.
//
// It is package state rather than a parameter because it has to be. DBOS
// serializes a workflow's input so it can replay the workflow later, and on
// recovery it calls the workflow itself with nothing but that recorded input —
// so a database pool cannot be an argument, and anything a step needs beyond the
// repository path has to be reachable from the package. Register is the only
// writer, and an atomic pointer rather than a plain variable so that a test that
// registers while another goroutine runs a workflow is not a data race.
var registered atomic.Pointer[registration]

// registration is the collaborators the workflows use: the same loader Run
// builds, the handle to load through, and the context they were registered
// against.
//
// root is that context: the executor's own DBOSContext, the one Register is
// given before dbos.Launch, with no workflow state on it. Keeping it is what
// lets mapFiles ask DBOS a question about a map task without the question
// becoming a step of the parent workflow — every dbos.* call finds the workflow
// it is running inside by looking in the context it is handed, and checkpoints
// itself when it finds one. The same call on root is a plain query against the
// system database. reviveCancelled is why that distinction is needed.
type registration struct {
	db   DB
	l    loader
	root dbos.DBOSContext
}

// Register binds the workflows to a database and registers them, and the
// "extract" queue, with DBOS.
//
// The three happen together on purpose: a workflow registered without a handle
// to load through is one that panics on recovery instead of at startup, and the
// window between them is exactly the window dbos.Launch recovers in.
//
// Call it once, before dbos.Launch — DBOS registers a workflow by code pointer
// and panics on a second registration of the same function, and Launch is what
// starts the queue's workers and recovers this executor's unfinished runs.
func Register(ctx dbos.DBOSContext, db DB) error {
	l := defaultLoader()
	registered.Store(&registration{db: db, l: l, root: ctx})
	dbos.RegisterWorkflow(ctx, IndexRepo, dbos.WithWorkflowName(WorkflowName))
	dbos.RegisterWorkflow(ctx, extractFile, dbos.WithWorkflowName(ExtractWorkflowName))

	// Per-executor rather than global: there is one executor, and what the limit
	// bounds is how many files this machine parses at once. It is no longer
	// bounding database connections the way M2's errgroup limit was — a map task
	// touches no database but DBOS's own, and the reduce is a single transaction
	// on a single connection — but GOMAXPROCS is still the right number, because
	// what a map task spends its time on is a CPU-bound tree-sitter parse.
	if _, err := dbos.RegisterQueue(ctx, QueueName,
		dbos.WithWorkerConcurrency(l.limit),
		dbos.WithQueueBasePollingInterval(queuePollInterval),
	); err != nil {
		return fmt.Errorf("index: register queue %q: %w", QueueName, err)
	}
	return nil
}

// IndexRepo indexes the repository rooted at repo as a durable map-reduce batch
// (SPEC.md §14 M4). It is index.Run's behaviour, checkpointed and batched.
//
// The result is the same Result Run returns, and is itself checkpointed, so a
// caller that attaches to a workflow it did not start gets the same report as
// the caller that started it.
//
// It must stay a top-level function: DBOS identifies a workflow by its code
// pointer (see this file's package comment).
func IndexRepo(ctx dbos.DBOSContext, repo string) (Result, error) {
	reg := registered.Load()
	if reg == nil {
		// Reachable only from a caller that registered the workflow by hand
		// instead of through Register, which is the whole reason Register does
		// both halves.
		return Result{}, errors.New("index: IndexRepo ran before index.Register")
	}
	return reg.indexRepo(ctx, repo)
}

// site is what resolving the repository establishes once, before any file is
// read: where the tree is and what coordinate its files belong to.
//
// It is a step's output, so it is checkpointed — which is the point. Root is
// derived from the process's working directory, and a recovering process need
// not have the same one; taking Root from the checkpoint rather than resolving
// it again means the resumed half of a run indexes the same tree as the first
// half, under the same coordinate, or fails loudly because the tree is gone.
type site struct {
	Root  string      `json:"root"`
	Coord coord.Coord `json:"coord"`
}

func (reg *registration) indexRepo(ctx dbos.DBOSContext, repo string) (Result, error) {
	s, err := dbos.RunAsStep(ctx, func(context.Context) (site, error) {
		return resolve(repo)
	}, dbos.WithStepName("resolve"))
	if err != nil {
		return Result{}, err
	}

	// Walking is a step so that the set of files a run indexes is fixed at the
	// moment the run starts. loader.run only sorts the walk for determinism;
	// here the sorted list is also *frozen*, which M4 needs for more than
	// tidiness: the i-th map task is identified by its position in this list and
	// by nothing else (see mapFiles), so a file appearing in the tree while the
	// run is down must not renumber the ones already extracted.
	paths, err := dbos.RunAsStep(ctx, func(context.Context) ([]string, error) {
		return walkRelative(s.Root)
	}, dbos.WithStepName("walk"))
	if err != nil {
		return Result{}, fmt.Errorf("index: walk %s: %w", repo, err)
	}

	res := Result{Coord: s.Coord, Files: len(paths), Concurrency: reg.l.limit}

	batch, skipped, err := reg.mapFiles(ctx, s, paths)
	// The skips are reported even when the map phase failed outright: they are
	// the run's findings about the tree, and a caller that has to retry deserves
	// to know which files it will be told about again.
	res.Skipped = skipped
	if err != nil {
		return res, err
	}

	// One reduce, after the last map task, and one transaction inside it
	// (SPEC.md §6). Loaded and Retries are set from the step's own output rather
	// than counted here, so a replay of a finished reduce reports what the
	// original reduce did instead of recomputing it — and so that a *failed*
	// reduce reports nothing loaded, which is exactly true: the transaction
	// rolled back and the graph is as it was.
	retries, err := dbos.RunAsStep(ctx, func(sctx context.Context) (int, error) {
		return reg.reduce(sctx, batch)
	}, dbos.WithStepName("reduce"))
	if err != nil {
		return res, err
	}
	res.Loaded, res.Retries = len(batch), retries
	return res, nil
}

// fileRef is one map task's input: everything extracting a file needs that the
// file itself cannot supply.
//
// It carries the root and the coordinate rather than the repository path,
// because a map task must not re-derive either. Both are the resolve step's
// checkpointed output, and re-resolving them in a worker — possibly in a process
// with a different working directory — is how the two halves of a resumed run
// would come to disagree about which tree they are indexing.
type fileRef struct {
	// Root is the absolute repository root, as the resolve step froze it.
	Root string `json:"root"`
	// Path is repo-relative and slash-separated, as the `file` table stores it.
	Path string `json:"path"`
	// Coord is the package coordinate every symbol in the file is prefixed with.
	Coord coord.Coord `json:"coord"`
}

// extractFile is the map task: parse one file, checkpoint its facts (SPEC.md §5,
// §14 M4).
//
// It is a *workflow* and not a step because a durable queue carries workflows —
// dbos.WithQueue is a WorkflowOption — and §9 puts the per-file tasks on a
// queue. That is not a technicality: it is what gives each file its own
// retry/recovery unit and its own status row, which is what "per-task
// retry/timeout/flag" (§9) means.
//
// It must stay a top-level function: DBOS identifies a workflow by its code
// pointer (see this file's package comment).
func extractFile(ctx dbos.DBOSContext, ref fileRef) (facts.FileFacts, error) {
	reg := registered.Load()
	if reg == nil {
		return facts.FileFacts{}, errors.New("index: extractFile ran before index.Register")
	}
	return reg.extract(ctx, ref)
}

// extract runs the parse inside a step.
//
// The step is not redundant with the workflow around it. Reading and parsing is
// the non-deterministic part — it touches the filesystem — and DBOS's contract
// is that such work lives in a step, so a task that dies after parsing and
// before recording its own output replays from the checkpoint instead of
// re-reading a file that may have changed underneath it.
//
// It does cost a second copy of the facts: they are recorded once as the step's
// output and once as the workflow's. That is the M4 shape and it is temporary by
// design — SPEC.md §5 says "the map task checkpoints the artifact *key*, never
// the blob", which is M5's protobuf-on-a-shared-volume and the whole reason M5
// exists. At M4 the facts travel in memory (§14 M4) and the checkpoint is the
// only place they are durable.
//
// A parse failure is not an error here. facts.FileFacts carries ParseError
// precisely so the failure can travel *with* the facts; returning it as an error
// would make the map task fail, and the parent has to tell "this file is poison"
// apart from "this task broke" to report the first properly (see mapFiles).
func (reg *registration) extract(ctx dbos.DBOSContext, ref fileRef) (facts.FileFacts, error) {
	// The path the extractor sees has to be the walk's own form — absolute,
	// platform-separated — because coord.Coord.Namespace resolves against it;
	// the checkpoint holds the repo-relative form because that is what the
	// `file` table holds and what makes the checkpoint readable.
	path := filepath.Join(ref.Root, filepath.FromSlash(ref.Path))
	return dbos.RunAsStep(ctx, func(context.Context) (facts.FileFacts, error) {
		return reg.l.extract(ref.Root, path, ref.Coord)
	}, dbos.WithStepName("extract:"+ref.Path))
}

// mapFiles enqueues one extract task per file and gathers what they produced:
// the batch the reduce will load, and the files that will not be in it.
//
// **Both loops run in path order on the workflow's own goroutine, and that is
// the whole determinism argument.** DBOS numbers a child workflow by the step ID
// the parent holds when it enqueues it, and derives the child's ID from that
// number; awaiting a child records a step of its own. So the parent's step
// sequence is enqueue(0..n-1) then await(0..n-1), and a replay reproduces it
// exactly because a sequential loop over a frozen slice is the same sequence
// every time. M3 had to reach for dbos.Go for this — it starts steps from
// goroutines and had to take each step ID synchronously before spawning — and
// M4 does not need it at all: the parent starts nothing concurrently. The
// concurrency moved into the queue, whose workers run the tasks in whatever
// order they like, because a task's identity no longer depends on when it runs.
//
// The corollary is the constraint on this function: **do not gather with
// dbos.Select, and do not enqueue from a goroutine.** Either would make the step
// order depend on scheduling, which is the failure M3 measured — a replay that
// fails with UnexpectedStep, permanently, since that failure is terminal.
// SPEC.md §9 offers `waitFirst`; there is nothing to wait first for while the
// reduce needs every file's facts before it can start.
//
// A task that failed is a skipped file rather than a failed run (SPEC.md §5,
// §9's "the batch proceeds over the successful subset"). §14 M4 writes it as
// `if err { markPoison(f); continue }` and two of the cases below are that
// line: the task errored, or it came back carrying a parse error. Neither is
// allowed to reach the reduce — a FileFacts with a ParseError has empty row
// slices, so loading it would delete a good file's graph and put nothing back
// (store's ErrParseFailed doc) — and neither is allowed to stop the batch.
//
// **A cancelled task is neither, and that is the third case.** Stopping the
// process cancels this workflow *and* the map tasks the queue had in flight,
// and DBOS records both durably. So the next process replays a parent whose
// last few children are sitting in CANCELLED, and awaiting one of those returns
// an error like any other — but the file is not poison, nothing about it
// failed, and flagging it loses it for good: the await's outcome is itself
// checkpointed, so every later run replays the skip instead of the file. That
// is one bug with two faces, a file that silently vanishes from the graph and a
// run that waits on a task nobody will start.
//
// Both are answered before anything is awaited: reviveCancelled puts the
// cancelled tasks back on the queue, so the gather loop meets a task that is
// running rather than one that is over. A cancellation that reaches the loop
// anyway is this workflow being cancelled *now*, and it stops the batch instead
// of flagging a file — DBOS's contract for that error is that the workflow
// returns promptly rather than doing further durable work on its way out, and
// swallowing it is how a replay comes to diverge.
//
// The returned skips are in path order without being sorted, because paths is
// the walk's sorted output and both loops follow it.
func (reg *registration) mapFiles(ctx dbos.DBOSContext, s site, paths []string) ([]facts.FileFacts, []Skip, error) {
	handles := make([]dbos.WorkflowHandle[facts.FileFacts], 0, len(paths))
	for _, rel := range paths {
		h, err := dbos.RunWorkflow(ctx, extractFile,
			fileRef{Root: s.Root, Path: rel, Coord: s.Coord},
			dbos.WithQueue(QueueName))
		if err != nil {
			return nil, nil, fmt.Errorf("index: enqueue %s: %w", rel, err)
		}
		handles = append(handles, h)
	}

	if err := reg.reviveCancelled(handles); err != nil {
		return nil, nil, err
	}

	batch := make([]facts.FileFacts, 0, len(handles))
	var skipped []Skip
	for i, h := range handles {
		rel := paths[i]
		ff, err := h.GetResult()
		switch {
		case cancelled(err):
			// Not a verdict on the file: the run is being stopped. Returning
			// here is what leaves the await uncheckpointed, which is what lets
			// the resumed run ask the same question again.
			return nil, skipped, fmt.Errorf("index: extract %s: %w", rel, err)
		case err != nil:
			// The task itself failed: an unreadable file, a grammar that would
			// not load, a worker that died past its retries. Flagged, not fatal.
			skipped = append(skipped, Skip{Path: rel, Err: fmt.Errorf("index: extract %s: %w", rel, err)})
		case ff.ParseError != "":
			// The task succeeded and reported the file unparseable. The sentinel
			// is minted here rather than carried, because an error does not
			// survive a checkpoint as an error; what crossed was the string on
			// the facts.
			skipped = append(skipped, Skip{Path: rel, Err: fmt.Errorf("%w (%s): %s", store.ErrParseFailed, rel, ff.ParseError)})
		default:
			batch = append(batch, ff)
		}
	}
	return batch, skipped, nil
}

// cancelled reports whether awaiting a map task failed because something was
// cancelled rather than because the task did.
//
// Both DBOS codes belong here and they are different events. WorkflowCancelled
// is *this* workflow: the await was interrupted because the parent is being
// stopped, and DBOS deliberately leaves that outcome uncheckpointed so a resume
// re-runs it. AwaitedWorkflowCancelled is the *child*: it settled in CANCELLED
// before the parent got to it — the race reviveCancelled cannot close, since a
// task can be cancelled after the revive and before its turn in the loop. The
// bare context errors are the same event seen one layer down, and are matched
// for the same reason DBOS's own cancellation check matches them.
//
// What is deliberately *not* here is the dead-letter error a task earns by
// failing its way past its retries. That is a task that failed, and §5 says a
// failed task is a skipped file.
func cancelled(err error) bool {
	return errors.Is(err, &dbos.DBOSError{Code: dbos.WorkflowCancelled}) ||
		errors.Is(err, &dbos.DBOSError{Code: dbos.AwaitedWorkflowCancelled}) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

// reviveCancelled puts back on the "extract" queue every map task of this run
// that a shutdown left in CANCELLED.
//
// It is the counterpart of what cmd/codiq does for the run itself. DBOS's own
// recovery restarts a PENDING workflow — one whose executor simply vanished —
// and ignores CANCELLED ones on purpose, because a cancel is normally somebody's
// decision. Nothing in codiq ever cancels one file: a map task is CANCELLED
// only because the process it was running in was asked to stop, and the same
// signal cancelled the parent. So "CANCELLED map task" and "work interrupted by
// a shutdown" are the same thing here, and re-driving it is the recovery DBOS
// already performs for the PENDING case. A task resumes from its own last
// checkpoint, so one that had finished parsing replays its `extract:` step
// rather than reading the file again.
//
// **It runs on root, and that is the whole reason registration keeps root.**
// dbos.ListWorkflows and dbos.ResumeWorkflows both checkpoint themselves as
// steps when they are handed a context that is inside a workflow, and a step
// recorded here would be fatal twice over: it would change the parent's step
// sequence (the constraint mapFiles exists to protect), and — worse — it would
// be *replayed*. The answer this function needs is the one the database has
// now, not the one it had during the run that got killed, and a checkpointed
// question can only ever return the latter. On the executor context both calls
// are plain queries, so this re-asks on every attempt and adds nothing to the
// ledger.
//
// A run with nothing cancelled pays one indexed query over the run's own
// workflow IDs, which is the cost of not having to know whether it is a resume.
func (reg *registration) reviveCancelled(handles []dbos.WorkflowHandle[facts.FileFacts]) error {
	if len(handles) == 0 {
		return nil
	}
	ids := make([]string, len(handles))
	for i, h := range handles {
		ids[i] = h.GetWorkflowID()
	}
	// Neither payload is wanted: the inputs are this loop's own fileRefs and the
	// outputs are a cancelled task's, which is to say absent. Asking for them
	// would drag the whole run's facts through this query.
	stalled, err := dbos.ListWorkflows(reg.root,
		dbos.WithWorkflowIDs(ids),
		dbos.WithStatus([]dbos.WorkflowStatusType{dbos.WorkflowStatusCancelled}),
		dbos.WithLoadInput(false),
		dbos.WithLoadOutput(false))
	if err != nil {
		return fmt.Errorf("index: list cancelled map tasks: %w", err)
	}
	if len(stalled) == 0 {
		return nil
	}
	revive := make([]string, len(stalled))
	for i, w := range stalled {
		revive[i] = w.ID
	}
	// Back onto "extract" rather than DBOS's internal queue, so a resumed task
	// is admitted under the same concurrency budget as every other map task and
	// Result.Concurrency keeps saying something true.
	if _, err := dbos.ResumeWorkflows[facts.FileFacts](reg.root, revive,
		dbos.WithResumeQueue(QueueName)); err != nil {
		return fmt.Errorf("index: resume %d cancelled map tasks: %w", len(revive), err)
	}
	return nil
}

// resolve establishes the tree's root and coordinate, once per run.
//
// It is loader.run's first two operations verbatim, lifted out so they can be a
// single step: the coordinate comes from a manifest outside any file being
// parsed (SPEC.md §4.3), so resolving it once is both the correct thing and the
// cheap thing.
func resolve(repo string) (site, error) {
	root, err := filepath.Abs(repo)
	if err != nil {
		return site{}, fmt.Errorf("index: %s: %w", repo, err)
	}
	c, err := coord.Resolve(root)
	if err != nil {
		return site{}, fmt.Errorf("index: %w", err)
	}
	return site{Root: root, Coord: c}, nil
}

// walkRelative is walk, with its paths rendered the way the `file` table renders
// them.
//
// walk yields absolute, platform-separated paths because that is what the
// extractor needs. A checkpoint outlives the process that wrote it, so what goes
// into one is the repo-relative slash form instead: it is stable across hosts,
// it is what an `extract:` step name should read as, and the map task rejoins it
// against the root the resolve step already froze.
func walkRelative(root string) ([]string, error) {
	paths, err := walk(root)
	if err != nil {
		return nil, err
	}
	rels := make([]string, len(paths))
	for i, p := range paths {
		rels[i] = relative(root, p)
	}
	return rels, nil
}

// skipJSON is the wire form of a Skip.
//
// Parse records whether the error was the parse-failure sentinel, because that
// is the one thing about it a caller is documented to be able to ask
// (index.go's Skip.Err) and the one thing a message cannot carry.
type skipJSON struct {
	Path  string `json:"path"`
	Err   string `json:"err,omitempty"`
	Parse bool   `json:"parse,omitempty"`
}

// MarshalJSON and UnmarshalJSON let a Skip survive a checkpoint.
//
// A Result is a workflow's output, so it passes through encoding/json — and
// Skip.Err is an `error`, which marshals to `{}` and comes back nil. Left alone,
// a resumed run would report the same skipped files as the original with their
// reasons silently gone, which is precisely what `codiq -v` exists to print.
//
// The methods live in this file rather than next to the type because the type is
// M2's and the requirement is M3's.
func (s Skip) MarshalJSON() ([]byte, error) {
	w := skipJSON{Path: s.Path}
	if s.Err != nil {
		w.Err = s.Err.Error()
		w.Parse = errors.Is(s.Err, store.ErrParseFailed)
	}
	return json.Marshal(w)
}

func (s *Skip) UnmarshalJSON(b []byte) error {
	var w skipJSON
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	s.Path, s.Err = w.Path, nil
	switch {
	case w.Err == "":
	case w.Parse:
		s.Err = parseFailure(w.Err)
	default:
		s.Err = errors.New(w.Err)
	}
	return nil
}

// parseFailure is a Skip.Err rebuilt from a checkpoint: the original message,
// still recognisable by the sentinel it was recognised by.
//
// Unwrap rather than a comparison, so errors.Is(skip.Err, store.ErrParseFailed)
// answers the same before and after a round trip — that is what index.go
// documents Skip.Err to be, and what M2's tests assert of it. A skip for any
// other reason comes back as a plain error, because it was never that sentinel
// and must not start claiming to be.
type parseFailure string

func (e parseFailure) Error() string { return string(e) }
func (e parseFailure) Unwrap() error { return store.ErrParseFailed }
