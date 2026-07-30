// DBOS orchestration for the loader (SPEC.md §9, §14 M3).
//
// index.go is M2: one process walks a tree, loads every file, rebuilds the
// edges, and if it dies halfway the next run starts from nothing. This file is
// the same pipeline expressed as a DBOS workflow, so every stage is
// checkpointed and a run that dies halfway resumes from its last checkpoint
// instead. Nothing about *what* is loaded changes — IndexRepo calls the same
// walk, the same extract, the same loadWithRetry and the same relink that
// loader.run does, in the same order.
//
// Three properties of DBOS shape the code, and each is load-bearing rather than
// stylistic:
//
//   - A workflow is identified by its function's code pointer, so IndexRepo has
//     to be a top-level named function. A closure or a method value would
//     register under a name that changes between builds, and registering twice
//     panics with a conflicting-registration error.
//   - Steps are replayed by *step ID*, and a step ID is handed out in the order
//     the step is started. Starting steps from raw goroutines therefore numbers
//     them in goroutine-scheduling order, which differs on the next run, and
//     the replay fails with UnexpectedStep — permanently, since that failure is
//     terminal. dbos.Go exists for exactly this: it takes the next step ID
//     *before* it spawns. See runFiles.
//   - Steps are at-least-once, not exactly-once. A step whose side effect
//     committed but whose checkpoint did not is re-run on recovery. That is
//     safe here only because store.ReplaceFile replaces a file's rows rather
//     than merging them, so loading the same file twice is loading it once;
//     that idempotence is now a durability requirement and not merely a nice
//     property (index.go's Run doc, SPEC.md §6).
//
// What DBOS deliberately does *not* take over is the transaction.
// store.ReplaceFile keeps its own `Begin` seam and its own pgx.Tx, because the
// reduce phase writes six tables with CopyFrom and dbos.RunAsTransaction hands
// out a handle that has no CopyFrom. DBOS checkpoints the step; store owns the
// transaction (SPEC.md §9 "Isolation", §10).
package index

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/google/uuid"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/store"
)

// WorkflowName is the name IndexRepo is registered and recorded under.
//
// DBOS defaults to the function's fully-qualified Go name, which would put
// `github.com/gaarutyunov/codiq/index.IndexRepo` in every checkpoint row and
// break every in-flight workflow the day the package moves. Naming it
// explicitly makes the recorded name a deliberate, stable identifier — and one
// a caller can filter `dbos.ListWorkflows` on without reproducing DBOS's own
// name-mangling rules.
const WorkflowName = "codiq.IndexRepo"

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
// everything else. An operator can still override it per-process with
// DBOS__APPVERSION.
const WorkflowVersion = "codiq-index-1"

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

// registered is what IndexRepo runs against.
//
// It is package state rather than a parameter because it has to be. DBOS
// serializes a workflow's input so it can replay the workflow later, and on
// recovery it calls IndexRepo itself with nothing but that recorded input — so
// a database pool cannot be an argument, and anything a step needs beyond the
// repository path has to be reachable from the package. Register is the only
// writer, and an atomic pointer rather than a plain variable so that a test that
// registers while another goroutine runs a workflow is not a data race.
var registered atomic.Pointer[registration]

// registration is the collaborators IndexRepo's steps use: the same loader Run
// builds, plus the handle to load through.
type registration struct {
	db DB
	l  loader
}

// Register binds IndexRepo to a database and registers it with DBOS.
//
// The two happen together on purpose: a workflow registered without a handle to
// load through is one that panics on recovery instead of at startup, and the
// window between the two is exactly the window dbos.Launch recovers in.
//
// Call it once, before dbos.Launch — DBOS registers a workflow by code pointer
// and panics on a second registration of the same function.
func Register(ctx dbos.DBOSContext, db DB) {
	registered.Store(&registration{db: db, l: defaultLoader()})
	dbos.RegisterWorkflow(ctx, IndexRepo, dbos.WithWorkflowName(WorkflowName))
}

// IndexRepo indexes the repository rooted at repo as a durable workflow
// (SPEC.md §14 M3). It is index.Run's behaviour, checkpointed.
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
	// here the sorted list is also *frozen*, so a file added to the tree while
	// the run is down is not half-indexed by the resumed half.
	paths, err := dbos.RunAsStep(ctx, func(context.Context) ([]string, error) {
		return walkRelative(s.Root)
	}, dbos.WithStepName("walk"))
	if err != nil {
		return Result{}, fmt.Errorf("index: walk %s: %w", repo, err)
	}

	res := Result{Coord: s.Coord, Files: len(paths), Concurrency: reg.l.limit}
	if err := reg.runFiles(ctx, s, paths, &res); err != nil {
		return res, err
	}

	// One rebuild, after the last file, for index.go's reason — and now also a
	// checkpoint, so a crash between the last file and the rebuild resumes at
	// the rebuild rather than re-loading the tree.
	if _, err := dbos.RunAsStep(ctx, func(sctx context.Context) (struct{}, error) {
		return struct{}{}, reg.l.relink(sctx, reg.db)
	}, dbos.WithStepName("link")); err != nil {
		return res, fmt.Errorf("index: %w", err)
	}
	return res, nil
}

// runFiles loads every file, up to loader.limit at a time, and folds the
// outcomes into res.
//
// This is where M2's errgroup went, and it could not stay. errgroup starts each
// worker as a bare goroutine, so the steps inside them would take their step IDs
// in whatever order the scheduler ran them; the next run would number the same
// steps differently and the replay would fail with UnexpectedStep, which is
// terminal. dbos.Go is the sanctioned replacement: it takes the next step ID on
// the calling goroutine, synchronously, and only then spawns.
//
// So the ordering rule this function has to keep is narrow and easy to state:
// **start the steps in path order.** It does, and it drains each batch fully
// before starting the next, which means path i is always the i-th step started
// no matter what the batch size is. That last part matters more than it looks —
// the batch size is GOMAXPROCS, a recovering process may well have a different
// one, and step IDs still line up because they never depended on where the batch
// boundaries fell.
//
// Draining is in slice order rather than first-ready (dbos.Select) for the same
// reason index.go takes a mutex: the fold has to produce the same Result every
// time, and reading the outcomes in path order is the cheapest way to guarantee
// it. There is nothing to wait first for at this milestone anyway — every file
// has to be loaded before the link pass runs.
func (reg *registration) runFiles(ctx dbos.DBOSContext, s site, paths []string, res *Result) error {
	defer func() {
		slices.SortFunc(res.Skipped, func(a, b Skip) int { return strings.Compare(a.Path, b.Path) })
	}()

	for batch := range slices.Chunk(paths, max(reg.l.limit, 1)) {
		outcomes := make([]<-chan dbos.StepOutcome[fileOutcome], 0, len(batch))
		var spawnErr error
		for _, rel := range batch {
			ch, err := dbos.Go(ctx, reg.loadStep(s, rel), dbos.WithStepName("load:"+rel))
			if err != nil {
				spawnErr = fmt.Errorf("index: load %s: %w", rel, err)
				break
			}
			outcomes = append(outcomes, ch)
		}

		// Every step that was started is drained, even once the batch is known
		// to have failed. The alternative is returning while a checkpoint is
		// still being written, which is how a workflow ends up with a step
		// recorded after it finished. M2 cancelled its stragglers through the
		// errgroup's context; here they are short enough to simply finish.
		var batchErr error
		for _, ch := range outcomes {
			out := <-ch
			switch {
			case out.Err != nil:
				batchErr = errors.Join(batchErr, out.Err)
			case out.Result.Skip != nil:
				// SPEC.md §5, and see loadStep: the skip is data, not failure.
				res.Skipped = append(res.Skipped, *out.Result.Skip)
			default:
				res.Loaded++
				res.Retries += out.Result.Retries
			}
		}
		if err := errors.Join(spawnErr, batchErr); err != nil {
			return err
		}
	}
	return nil
}

// fileOutcome is one file's step result: what the fold needs, and nothing a
// checkpoint cannot hold.
//
// Skip is a pointer so that "loaded" and "skipped" are distinguishable after a
// round trip through JSON without a second boolean to keep in sync with it.
type fileOutcome struct {
	// Skip is set when the file was unparseable and therefore not loaded.
	Skip *Skip `json:"skip,omitempty"`
	// Retries is how many times the load was retried past a transient
	// serialization failure, as loadWithRetry counts them.
	Retries int `json:"retries,omitempty"`
}

// loadStep is the per-file step: extract, then load, with the poison-file rule
// applied inside.
//
// The rule has to be applied *here*, not by the caller, and that is the one
// place M3 could quietly have broken M2. DBOS records a step that returns an
// error as a failed step and fails the workflow with it — so returning
// store.ErrParseFailed would turn "this file could not be parsed" into "this
// index run is over", which is exactly what SPEC.md §5 forbids and exactly what
// index.go's comment says the spec's own skeleton got wrong. The skip is
// therefore swallowed the way loader.run swallows it and carried out as data.
//
// loadWithRetry stays inside the step too, rather than being handed to DBOS's
// WithStepMaxRetries. DBOS could retry the step, but it surfaces no per-step
// retry count, and Result.Retries is part of what this package reports (and what
// M2's tests assert). Keeping the retry loop inside one step keeps the count and
// costs nothing: the retry is for a deadlock between two files' loads, which is
// resolved in milliseconds and has no reason to reach a checkpoint.
func (reg *registration) loadStep(s site, rel string) dbos.Step[fileOutcome] {
	// The path the extractor sees has to be the walk's own form — absolute,
	// platform-separated — because coord.Coord.Namespace resolves against it;
	// the checkpoint holds the repo-relative form because that is what the
	// `file` table holds and what makes the checkpoint readable.
	path := filepath.Join(s.Root, filepath.FromSlash(rel))

	return func(sctx context.Context) (fileOutcome, error) {
		ff, err := reg.l.extract(s.Root, path, s.Coord)
		if err != nil {
			return fileOutcome{}, err
		}
		// sctx, not a captured outer context: DBOS hands the step the context
		// it wants the step's work cancelled by.
		retries, err := reg.l.loadWithRetry(sctx, reg.db, ff)
		switch {
		case err == nil:
			return fileOutcome{Retries: retries}, nil
		case errors.Is(err, store.ErrParseFailed):
			return fileOutcome{Skip: &Skip{Path: ff.File.Path, Err: err}}, nil
		default:
			return fileOutcome{}, err
		}
	}
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
// it is what a `load:` step name should read as, and loadStep rejoins it against
// the root the resolve step already froze.
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
type skipJSON struct {
	Path string `json:"path"`
	Err  string `json:"err,omitempty"`
}

// MarshalJSON and UnmarshalJSON let a Skip survive a checkpoint.
//
// A Result is a workflow's output and every fileOutcome is a step's, so both
// pass through encoding/json — and Skip.Err is an `error`, which marshals to
// `{}` and comes back nil. Left alone, a resumed run would report the same
// skipped files as the original with their parse errors silently gone, which is
// precisely what `codiq -v` exists to print.
//
// The methods live in this file rather than next to the type because the type is
// M2's and the requirement is M3's.
func (s Skip) MarshalJSON() ([]byte, error) {
	w := skipJSON{Path: s.Path}
	if s.Err != nil {
		w.Err = s.Err.Error()
	}
	return json.Marshal(w)
}

func (s *Skip) UnmarshalJSON(b []byte) error {
	var w skipJSON
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	s.Path, s.Err = w.Path, nil
	if w.Err != "" {
		s.Err = parseFailure(w.Err)
	}
	return nil
}

// parseFailure is a Skip.Err rebuilt from a checkpoint: the original message,
// still recognisable by the sentinel it was recognised by.
//
// Unwrap rather than a comparison, so errors.Is(skip.Err, store.ErrParseFailed)
// answers the same before and after a round trip — that is what index.go
// documents Skip.Err to be, and what M2's tests assert of it.
type parseFailure string

func (e parseFailure) Error() string { return string(e) }
func (e parseFailure) Unwrap() error { return store.ErrParseFailed }
