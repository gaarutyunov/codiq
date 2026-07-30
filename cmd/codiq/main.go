// Command codiq indexes a repository into the CodiQ graph (SPEC.md §14 M2).
//
// It is a one-shot program: it walks the tree, parses every file it has a
// parser for, replaces that file's rows, rebuilds the cross-file edges, and
// exits. Re-running it over the same tree is idempotent, so it is safe to run
// on every push, and it is what the `codiq` service in deploy/docker-compose.yml
// runs.
//
//	codiq [-dsn URL] [-v] [repo]
//
// Wiring lives here and only here (SPEC.md §12: plain Go, packages grouped by
// what they do, wired in main). The flag package is the whole CLI surface — one
// command with two flags does not need a command framework, and the spec names
// none.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gaarutyunov/codiq/index"
)

// dsnEnv is the connection string's environment variable: CodiQ's own name for
// it, established by deploy/docker-compose.yml and SPEC.md §11.1. gopgql's
// binaries read GOPGQL_DSN for the same database; the two names are deliberately
// distinct because the two programs are (read/write separation, SPEC.md §2.3).
const dsnEnv = "CODIQ_DATABASE_URL"

func main() {
	// SIGINT/SIGTERM cancel the context rather than killing the process, so an
	// interrupted run stops between transactions instead of inside one. Each
	// file's load is already atomic, so what is on disk stays consistent; the
	// cross-file edges are simply not rebuilt, which the next run does.
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
	verbose := fs.Bool("v", false, "print the parse error behind every skipped file")
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

	pool, err := open(ctx, *dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	started := time.Now()
	res, err := index.Run(ctx, pool, repo)
	// A run that failed before it resolved a coordinate has nothing to report
	// that its error does not already say.
	if !res.Coord.IsZero() {
		report(stdout, repo, res, time.Since(started), *verbose)
	}
	return err
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
// visible instead. -v adds the parse error behind each one.
func report(w io.Writer, repo string, res index.Result, elapsed time.Duration, verbose bool) {
	_, _ = fmt.Fprintf(w, "codiq: %s\n", repo)
	_, _ = fmt.Fprintf(w, "  coordinate  %s\n", res.Coord.Prefix())
	_, _ = fmt.Fprintf(w, "  workers     %d\n", res.Concurrency)
	_, _ = fmt.Fprintf(w, "  files       %d\n", res.Files)
	_, _ = fmt.Fprintf(w, "  loaded      %d in %s\n", res.Loaded, elapsed.Round(time.Millisecond))
	if res.Retries > 0 {
		// Not a warning: two files that reference each other contend on the
		// cross-file edges they share, and one of them backs off and wins.
		_, _ = fmt.Fprintf(w, "  retried     %d file loads after a serialization failure\n", res.Retries)
	}

	if len(res.Skipped) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "  skipped     %d unparseable (previous facts left in place)\n", len(res.Skipped))
	for _, s := range res.Skipped {
		if verbose {
			_, _ = fmt.Fprintf(w, "                %s: %v\n", s.Path, s.Err)
			continue
		}
		_, _ = fmt.Fprintf(w, "                %s\n", s.Path)
	}
	if !verbose {
		_, _ = fmt.Fprintf(w, "              re-run with -v for the parse errors\n")
	}
}
