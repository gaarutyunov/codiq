// Command codiq indexes a repository into the CodiQ graph (SPEC.md §14 M2, M3,
// M5).
//
// It is a one-shot program: it walks the tree, parses every file it has a
// parser for, replaces that file's rows, rebuilds the cross-file edges, and
// exits. Re-running it over the same tree is idempotent, so it is safe to run
// on every push, and it is what the `codiq` service in deploy/docker-compose.yml
// runs.
//
// Since M3 the run is a DBOS workflow (index/dbos.go), so it is also
// crash-resumable: every stage is checkpointed into a second database, and a
// process that dies mid-index is finished rather than redone by the next one.
// That makes this file responsible for two databases and one lifecycle —
// NewDBOSContext, Register, Launch, run, Shutdown — and for the distinction
// between a run whose process was killed and one this program stopped on
// purpose, which leave the workflow in different states and are picked up again
// by different calls (see durable and start).
//
// Since M5 the map phase spills each file's facts to a shared volume as a
// protobuf artifact and checkpoints only the key (SPEC.md §5, §10), so this
// file is also responsible for the third resource a run needs: the artifact
// directory.
//
//	codiq [-dsn URL] [-dbos-dsn URL] [-artifact-dir DIR] [-v] [repo]
//
// Wiring lives here and only here (SPEC.md §12: plain Go, packages grouped by
// what they do, wired in main). The flag package is the whole CLI surface — one
// command with a handful of flags does not need a command framework, and the
// spec names none.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gaarutyunov/codiq/artifact"
	"github.com/gaarutyunov/codiq/index"
)

// dsnEnv is the connection string's environment variable: CodiQ's own name for
// it, established by deploy/docker-compose.yml and SPEC.md §11.1. gopgql's
// binaries read GOPGQL_DSN for the same database; the two names are deliberately
// distinct because the two programs are (read/write separation, SPEC.md §2.3).
const dsnEnv = "CODIQ_DATABASE_URL"

// dbosDSNEnv is the *other* connection string: DBOS's own system database
// (SPEC.md §9, §10). It is a separate database on the same instance —
// `codiq_dbos`, created by deploy/initdb/01-dbos.sql — so that checkpointing
// every step of a run never contends with the bulk writes that run is making
// into the graph tables. Nothing but DBOS ever opens it.
const dbosDSNEnv = "DBOS_DATABASE_URL"

// artifactDirEnv is the shared volume the map phase spills facts to (SPEC.md
// §5, §10, §11.1, §14 M5). deploy/docker-compose.yml mounts the `artifacts`
// volume there.
const artifactDirEnv = "CODIQ_ARTIFACT_DIR"

// defaultArtifactDir is where artifacts go when nothing says otherwise.
//
// Unlike the two connection strings this program refuses to start without, an
// artifact directory has a correct default: it is scratch space, and every
// machine has somewhere to put scratch. Refusing to run for want of one would
// break `codiq /repo` for every caller outside Compose, which is a worse failure
// than picking a directory.
//
// What it must not be is a fresh one. The whole point of the artifacts is that a
// process which dies after the map phase leaves them for its successor to
// consume (§6, §14 M5), so a MkdirTemp per process would turn every resume into
// a re-extraction — the exact behaviour the milestone removes. A fixed name
// under the system temp directory is stable across processes on one machine,
// which is the same scope §10's local volume has.
var defaultArtifactDir = filepath.Join(os.TempDir(), "codiq-artifacts")

// shutdownTimeout is how long Shutdown is given to wind the workflow down before
// the process stops waiting for it.
//
// A step here is one file's parse and one short transaction, so this is orders
// of magnitude more than the common case needs; the size is for the pathological
// one — a step blocked on a lock — where the right answer is still to stop and
// let the next run continue from the checkpoints that did land.
const shutdownTimeout = 30 * time.Second

func main() {
	// SIGINT/SIGTERM cancel the context rather than killing the process, so an
	// interrupted run stops between transactions instead of inside one. Each
	// file's load is already atomic, so what is on disk stays consistent.
	//
	// Since M3 this context stops the *process*; the run it was in the middle of
	// is checkpointed, and the next invocation continues it from there rather
	// than starting over. durable, await and start are where that is arranged,
	// and why an interrupted run and a crashed one get back in by different
	// doors.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(os.Stderr, "codiq: %v\n", err)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("codiq", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dsn := fs.String("dsn", "", "PostgreSQL connection string (default $"+dsnEnv+")")
	dbosDSN := fs.String("dbos-dsn", "", "DBOS checkpoint connection string (default $"+dbosDSNEnv+")")
	artifactDir := fs.String("artifact-dir", "", "directory for map-phase fact artifacts (default $"+artifactDirEnv+", else "+defaultArtifactDir+")")
	verbose := fs.Bool("v", false, "print the reason behind every skipped file")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(stderr, "usage: codiq [flags] [repo]\n\n"+
			"Index a repository into the CodiQ graph. repo defaults to the working\n"+
			"directory and must be inside a module CodiQ can resolve a package\n"+
			"coordinate for (go.mod).\n\nflags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		fs.Usage()
		return fmt.Errorf("one repository at a time, got %d", fs.NArg())
	}

	repo := fs.Arg(0)
	if repo == "" {
		repo = "."
	}
	if *dsn == "" {
		*dsn = os.Getenv(dsnEnv)
	}
	if *dsn == "" {
		return fmt.Errorf("no database: pass -dsn or set %s", dsnEnv)
	}
	if *dbosDSN == "" {
		*dbosDSN = os.Getenv(dbosDSNEnv)
	}
	if *dbosDSN == "" {
		return fmt.Errorf("no checkpoint database: pass -dbos-dsn or set %s", dbosDSNEnv)
	}
	if *artifactDir == "" {
		*artifactDir = os.Getenv(artifactDirEnv)
	}
	if *artifactDir == "" {
		*artifactDir = defaultArtifactDir
	}

	// The workflow's input is the absolute path, not what was typed. It is what
	// identifies the run in the checkpoint tables, so `codiq .` and `codiq /repo`
	// from the same directory have to be the same run — otherwise resuming an
	// interrupted index depends on how it was spelled the first time. The
	// original spelling is still what gets reported.
	target, err := filepath.Abs(repo)
	if err != nil {
		return fmt.Errorf("%s: %w", repo, err)
	}

	pool, err := open(ctx, *dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Opened before DBOS is launched, so a volume that is missing or not
	// writable is an error at startup rather than one every map task discovers
	// for itself once the queue is already draining.
	art, err := artifact.Open(*artifactDir)
	if err != nil {
		return err
	}

	dctx, err := durable(ctx, *dbosDSN, stderr)
	if err != nil {
		return err
	}
	// Registration and launch are separate for one reason: Launch recovers this
	// executor's unfinished workflows and starts the "extract" queue's workers,
	// so everything they need has to already be registered when it runs.
	if err := index.Register(dctx, pool, art); err != nil {
		return err
	}
	if err := dbos.Launch(dctx); err != nil {
		return fmt.Errorf("dbos: launch: %w", err)
	}
	defer dbos.Shutdown(dctx, shutdownTimeout)

	h, resumed, err := start(dctx, target)
	if err != nil {
		return err
	}
	if resumed {
		_, _ = fmt.Fprintf(stderr, "codiq: continuing unfinished index %s\n", h.GetWorkflowID())
	}

	started := time.Now()
	res, err := await(ctx, h)
	// A run that failed before it resolved a coordinate has nothing to report
	// that its error does not already say.
	if !res.Coord.IsZero() {
		report(stdout, repo, res, time.Since(started), *verbose)
	}
	return err
}

// durable builds the DBOS context the run is orchestrated by.
//
// The context is context.WithoutCancel, so the signal context that stops this
// process does not reach DBOS on its own. That does not spare the workflow —
// dbos.Shutdown cancels the DBOS context as its first act, so the deferred
// Shutdown ends an interrupted run as CANCELLED either way (measured; it is not
// a "let the current step land" operation). What WithoutCancel buys is that the
// cancellation happens *once*, at a known point, after await has stopped waiting
// and reported — instead of racing await the instant Ctrl-C is pressed.
//
// CANCELLED is where the crash path and the interrupt path diverge, and start is
// where they are put back together: a crashed run is left PENDING and Launch
// restarts it by itself, while a cancelled one is invisible to recovery and has
// to be resumed explicitly. Both end up resuming from the same checkpoints.
//
// The logger goes to stderr because stdout is the report, and a caller piping
// one should not have to filter the other out of it.
func durable(ctx context.Context, dsn string, stderr io.Writer) (dbos.DBOSContext, error) {
	dctx, err := dbos.NewDBOSContext(context.WithoutCancel(ctx), dbos.Config{
		AppName:     "codiq",
		DatabaseURL: dsn,
		// Pinned, not the default hash of this binary: see index.WorkflowVersion
		// for why the default makes a redeploy lose every workflow it inherits.
		ApplicationVersion: index.WorkflowVersion,
		Logger:             slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	})
	if err != nil {
		return nil, fmt.Errorf("bad %s: %w", dbosDSNEnv, err)
	}
	return dctx, nil
}

// start picks up this repository's unfinished index if it has one, and starts a
// new one otherwise. The bool reports which happened.
//
// Both halves are necessary and they pull in opposite directions. Indexing is
// idempotent and meant to be re-run — on every push — so the workflow ID cannot
// be a pure function of the repository, or the second run would hand back the
// first run's checkpointed result and index nothing. But a run that did not
// finish has to be *finished*, not redone: starting a second workflow beside it
// would index the same tree twice, concurrently, which is not merely wasteful.
// Two indexers over one corpus race in the link pass — measured, as a foreign
// key violation out of link.RebuildAll — because rebuilding the derived edges is
// a whole-graph operation and the other run is still replacing the rows it reads.
//
// Unfinished comes in two shapes, and they need different verbs:
//
//   - PENDING is a run whose process died. dbos.Launch has already restarted it
//     in the background by the time this is called, so the work here is only to
//     find it and attach to the running thing rather than start a rival.
//   - CANCELLED is a run this program stopped on purpose, on Ctrl-C (see
//     durable). Recovery does not look at cancelled workflows at all, so this one
//     has to be handed back to the runtime explicitly. It restarts from the same
//     checkpoints; only the way it is reached differs.
func start(dctx dbos.DBOSContext, target string) (dbos.WorkflowHandle[index.Result], bool, error) {
	// Listed on the workflow ID rather than the recorded input; index.RunIDPrefix
	// documents why that distinction is load-bearing. The name filter is just as
	// load-bearing: the per-file map tasks are child workflows whose IDs DBOS
	// derives as <parentID>-<stepID>, so they share the run's ID prefix, and
	// without it the match found here is a map task, not the run. The oldest match wins,
	// which is what the default ordering gives: if more than one run was left
	// unfinished, the one that has been waiting longest is the one to finish, and
	// the next invocation picks up the next.
	for _, c := range []struct {
		status dbos.WorkflowStatusType
		pick   func(dbos.DBOSContext, string) (dbos.WorkflowHandle[index.Result], error)
	}{
		{dbos.WorkflowStatusPending, dbos.RetrieveWorkflow[index.Result]},
		{dbos.WorkflowStatusEnqueued, dbos.RetrieveWorkflow[index.Result]},
		{dbos.WorkflowStatusCancelled, func(ctx dbos.DBOSContext, id string) (dbos.WorkflowHandle[index.Result], error) {
			return dbos.ResumeWorkflow[index.Result](ctx, id)
		}},
	} {
		found, err := dbos.ListWorkflows(dctx,
			dbos.WithName(index.WorkflowName),
			dbos.WithWorkflowIDPrefix(index.RunIDPrefix(target)),
			dbos.WithStatus([]dbos.WorkflowStatusType{c.status}),
		)
		if err != nil {
			return nil, false, fmt.Errorf("dbos: list workflows: %w", err)
		}
		if len(found) == 0 {
			continue
		}
		h, err := c.pick(dctx, found[0].ID)
		if err != nil {
			return nil, false, fmt.Errorf("dbos: resume %s: %w", found[0].ID, err)
		}
		return h, true, nil
	}

	h, err := dbos.RunWorkflow(dctx, index.IndexRepo, target, dbos.WithWorkflowID(index.NewRunID(target)))
	if err != nil {
		return nil, false, fmt.Errorf("dbos: %w", err)
	}
	return h, false, nil
}

// await waits for the workflow to finish, or for this process to be interrupted,
// whichever comes first.
//
// GetResult has no context, so the wait is a goroutine and a select. All the
// signal path does here is stop waiting; ending the workflow is the deferred
// Shutdown's job, and it ends it as CANCELLED. That is not a lost run — its
// checkpoints are already on disk and the next invocation resumes it — but it is
// a different door back in from the one a crash uses, so the message says the
// run continues rather than implying it was never stopped. durable and start
// carry the rest of the reasoning.
func await(ctx context.Context, h dbos.WorkflowHandle[index.Result]) (index.Result, error) {
	type outcome struct {
		res index.Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := h.GetResult()
		done <- outcome{res: res, err: err}
	}()

	select {
	case o := <-done:
		return o.res, o.err
	case <-ctx.Done():
		return index.Result{}, fmt.Errorf("interrupted: index %s is checkpointed and continues on the next run", h.GetWorkflowID())
	}
}

// open dials Postgres and proves the connection works before any indexing
// starts, so an unreachable database is one clear error rather than a worker's.
//
// The pool is sized to the loader's worker count: each in-flight file holds one
// connection for the length of its transaction, so a smaller pool would just
// make workers queue on connections. A DSN that asks for more keeps what it
// asked for.
func open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("bad %s: %w", dsnEnv, err)
	}
	if workers := int32(index.DefaultConcurrency()); cfg.MaxConns < workers {
		cfg.MaxConns = workers
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect: %w", err)
	}
	return pool, nil
}

// report prints what the run did.
//
// The skipped files are always listed, never only counted: a file silently
// missing from the graph looks exactly like a file with nothing in it, and the
// whole reason a poison file does not fail the run (SPEC.md §5) is that it is
// visible instead. -v adds the reason behind each one, which from M4 is either
// a parse error or the failure of the file's map task (index.Skip).
func report(w io.Writer, repo string, res index.Result, elapsed time.Duration, verbose bool) {
	_, _ = fmt.Fprintf(w, "codiq: %s\n", repo)
	_, _ = fmt.Fprintf(w, "  coordinate  %s\n", res.Coord.Prefix())
	_, _ = fmt.Fprintf(w, "  workers     %d\n", res.Concurrency)
	_, _ = fmt.Fprintf(w, "  files       %d\n", res.Files)
	_, _ = fmt.Fprintf(w, "  loaded      %d in %s\n", res.Loaded, elapsed.Round(time.Millisecond))
	if res.Retries > 0 {
		// Not a warning: two files that reference each other contend on the
		// cross-file edges they share, and one of them backs off and wins.
		_, _ = fmt.Fprintf(w, "  retried     %d loads after a serialization failure\n", res.Retries)
	}

	if len(res.Skipped) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "  skipped     %d not indexed (previous facts left in place)\n", len(res.Skipped))
	for _, s := range res.Skipped {
		if verbose {
			_, _ = fmt.Fprintf(w, "                %s: %v\n", s.Path, s.Err)
			continue
		}
		_, _ = fmt.Fprintf(w, "                %s\n", s.Path)
	}
	if !verbose {
		_, _ = fmt.Fprintf(w, "              re-run with -v for the reasons\n")
	}
}
