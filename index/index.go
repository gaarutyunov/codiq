// Package index is CodiQ's M2 loader: it turns a repository on disk into rows
// in the core tables (SPEC.md §14 M2).
//
// The whole pipeline is four steps, in order: resolve the repository's package
// coordinate once (coord), walk the tree for files a parser is registered for
// (extract), parse and load each of those files independently (extract +
// store), then materialize the cross-file edges once at the end (link). Nothing
// here derives anything itself — it is wiring, and every rule it enforces
// belongs to one of those four packages.
//
// Two properties of the design are worth stating because the rest of the
// package is shaped by them:
//
//   - Per-file work is embarrassingly parallel. A FileFacts is self-contained
//     (facts) and store.ReplaceFile takes a transaction-scoped advisory lock on
//     the path, so files need no coordination beyond a worker limit. The link
//     pass is the one global step and runs alone, after the last file.
//   - A file that cannot be parsed is skipped, never fatal (SPEC.md §5: "a
//     poison file is flagged and skipped, never blocking the batch"). SPEC.md
//     §14 M2's skeleton returns store.ReplaceFile's error straight into the
//     errgroup, which would abort the whole walk on the first unparseable file
//     — the opposite of §5. store.ErrParseFailed is therefore filtered out here
//     and reported in Result instead.
//
// M2 scope: this is the monolithic loader. No DBOS, no batching, no protobuf,
// no disk — M3, M4 and M5 respectively.
package index

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/sync/errgroup"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/extract"
	"github.com/gaarutyunov/codiq/facts"
	"github.com/gaarutyunov/codiq/link"
	"github.com/gaarutyunov/codiq/store"
)

// DB is what a run needs of a database handle: the ability to start a
// transaction. *pgxpool.Pool satisfies it, and so does pgx.Tx.
//
// It is deliberately the same shape as store.DB and link.DB rather than an
// alias for either: this package hands the same handle to both, and stating the
// requirement once locally is what lets it do that without importing one of
// them for a type name.
type DB interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Skip is one file the run did not load: the extractor could not parse it, so
// store wrote nothing and whatever it had loaded before is still in place.
type Skip struct {
	// Path is repo-relative, as it would appear in the `file` table.
	Path string
	// Err is the parse failure, wrapping store.ErrParseFailed.
	Err error
}

// Result is what a run did. It is returned even when the run fails, so a caller
// can report the files that were already loaded.
type Result struct {
	// Coord is the package coordinate resolved for the repository.
	Coord coord.Coord
	// Files is how many files the walk selected — those with a registered
	// parser, after the ignore rules.
	Files int
	// Loaded is how many of those files were written to the database.
	Loaded int
	// Skipped lists the unparseable files, sorted by path. Loaded + len(Skipped)
	// == Files on a successful run.
	Skipped []Skip
	// Retries is how many per-file loads were retried after a transient
	// serialization failure. Nonzero is normal on a re-index and not an error;
	// growing with the size of the tree is worth looking at.
	Retries int
	// Concurrency is the worker limit the run used.
	Concurrency int
}

// DefaultConcurrency is the number of files Run parses and loads at once.
//
// GOMAXPROCS is the natural size. The expensive half of per-file work is
// gotreesitter parsing, which is CPU-bound, so more workers than cores buys
// nothing; the other half is one short transaction per file, and keeping the
// number of in-flight transactions at GOMAXPROCS keeps a default-sized pgx pool
// from becoming the thing workers wait on. It is exported so a caller can size
// its connection pool to match.
func DefaultConcurrency() int { return max(runtime.GOMAXPROCS(0), 1) }

// Run indexes the repository rooted at repo (SPEC.md §14 M2).
//
// It is idempotent: store.ReplaceFile replaces a file's rows rather than merging
// them and keeps the file's id, and link.RebuildAll recomputes the derived
// tables from scratch, so running Run twice over an unchanged tree leaves the
// same rows behind.
//
// An unparseable file is skipped and reported in Result.Skipped; any other
// failure aborts the run, and the link pass does not run. That asymmetry is the
// point: a bad file is data, and a failed insert is not.
func Run(ctx context.Context, db DB, repo string) (Result, error) {
	return defaultLoader().run(ctx, db, repo)
}

// defaultLoader is the real thing: the real extractor registry, the real store,
// the real link pass. Run and the DBOS workflow (dbos.go) both need it, and a
// second literal is a second place for the two to drift apart.
func defaultLoader() loader {
	return loader{
		parserFor: extract.ParserFor,
		load:      store.ReplaceFile,
		relink:    link.RebuildAll,
		limit:     DefaultConcurrency(),
	}
}

// loader is Run's dependencies, named so a test can substitute them.
//
// The three fields are exactly the three collaborators that need a database or
// a tree-sitter grammar to do anything real; everything else in this package is
// a pure function over paths and can be tested directly. Concrete function
// values rather than interfaces because each is one function, not a role.
type loader struct {
	parserFor func(path string) (extract.Parser, bool)
	load      func(ctx context.Context, db store.DB, ff facts.FileFacts) error
	relink    func(ctx context.Context, db link.DB) error
	limit     int
}

func (l loader) run(ctx context.Context, db DB, repo string) (Result, error) {
	// Absolute throughout. coord.Resolve resolves its argument, and
	// coord.Coord.Namespace resolves a file path against the coordinate's root,
	// so the walk has to produce paths in the same form the coordinate was
	// resolved in or every namespace comes out empty.
	root, err := filepath.Abs(repo)
	if err != nil {
		return Result{}, fmt.Errorf("index: %s: %w", repo, err)
	}

	// Once per repository, not once per file: the coordinate comes from a
	// manifest outside the file being parsed, which is exactly why it is not the
	// extractor's to resolve (SPEC.md §4.3).
	c, err := coord.Resolve(root)
	if err != nil {
		return Result{}, fmt.Errorf("index: %w", err)
	}

	paths, err := walk(root)
	if err != nil {
		return Result{}, fmt.Errorf("index: walk %s: %w", repo, err)
	}

	res := Result{Coord: c, Files: len(paths), Concurrency: l.limit}

	// WithContext so the first real failure cancels the workers still running
	// instead of letting them finish writing into a load that is already lost.
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(l.limit)

	var mu sync.Mutex // guards res.Loaded and res.Skipped
	for _, path := range paths {
		g.Go(func() error {
			ff, err := l.extract(root, path, c)
			if err != nil {
				return err
			}
			retries, err := l.loadWithRetry(gctx, db, ff)
			switch {
			case err == nil:
				mu.Lock()
				res.Loaded++
				res.Retries += retries
				mu.Unlock()
				return nil
			case errors.Is(err, store.ErrParseFailed):
				// SPEC.md §5: flagged and skipped, never blocking the batch. The
				// file keeps the facts of its last good load, so the graph is
				// stale for this one file rather than missing it entirely.
				mu.Lock()
				res.Skipped = append(res.Skipped, Skip{Path: ff.File.Path, Err: err})
				mu.Unlock()
				return nil
			default:
				return err
			}
		})
	}
	waitErr := g.Wait()

	slices.SortFunc(res.Skipped, func(a, b Skip) int { return strings.Compare(a.Path, b.Path) })
	if waitErr != nil {
		return res, waitErr
	}

	// One rebuild, after the last file. Cross-file edges are a function of the
	// base facts (SPEC.md §7), so they can only be computed once every file's
	// facts are in — and doing it per file would recompute the whole derived
	// layer N times.
	if err := l.relink(ctx, db); err != nil {
		return res, fmt.Errorf("index: %w", err)
	}
	return res, nil
}

// maxLoadAttempts is how many times one file's load is tried before its
// serialization failure is treated as real. Two is usually enough — the point of
// a retry is only to break a tie — and the cap exists to turn a pathological
// case into an error rather than a hang.
const maxLoadAttempts = 5

// loadWithRetry loads one file, retrying a transient serialization failure, and
// reports how many retries it took (SPEC.md §5: "a failing file is retried in
// isolation").
//
// The failure this exists for is a deadlock between two files' loads.
// store.ReplaceFile deletes a file's cross-file edges from both endpoint sides
// — the source side because the file owns those rows, the target side because
// they reference rows it is about to delete — so two files that reference each
// other's definitions take locks on the same derived rows in opposite orders,
// and PostgreSQL picks a victim. It only happens from the second index onwards,
// since the first has no derived rows to delete yet, which is exactly the kind
// of bug a re-run finds and a first run does not.
//
// Retrying is a correct answer and not merely a pragmatic one: the victim's
// transaction is rolled back whole, so there is nothing to undo, and the whole
// operation is a replace, so doing it again is doing it for the first time. A
// short staggered backoff is what stops the two from colliding again, since the
// victim is chosen at random rather than by age.
func (l loader) loadWithRetry(ctx context.Context, db DB, ff facts.FileFacts) (int, error) {
	for attempt := 1; ; attempt++ {
		err := l.load(ctx, db, ff)
		if err == nil || attempt == maxLoadAttempts || !serializationFailure(err) {
			return attempt - 1, err
		}
		select {
		case <-ctx.Done():
			return attempt - 1, ctx.Err()
		case <-time.After(time.Duration(attempt) * 20 * time.Millisecond):
		}
	}
}

// serializationFailure reports whether err is PostgreSQL telling the caller to
// try the transaction again.
//
// The two codes are spelled out rather than taken from jackc/pgerrcode: that is
// a separate module, and a dependency for two constants that are frozen by the
// SQL standard's class 40 is not worth the go.mod line.
func serializationFailure(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	switch pgErr.Code {
	case "40P01", "40001": // deadlock_detected, serialization_failure
		return true
	}
	return false
}

// extract reads one file and maps it to facts.
//
// The parser is handed the walk path, because that is what the coordinate's
// namespace resolves against; the resulting File.Path is then rewritten
// repo-relative, because that is what the `file` table holds (facts.File). This
// package is the only one that knows both forms, so it is the only one that can
// do the translation.
func (l loader) extract(root, path string, c coord.Coord) (facts.FileFacts, error) {
	parser, ok := l.parserFor(path)
	if !ok {
		// Unreachable: walk selects on the same registry. Reported rather than
		// ignored so a future divergence between the two is loud.
		return facts.FileFacts{}, fmt.Errorf("index: no parser registered for %s", path)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		// Not a parse failure and not a skip: the extractor never saw the bytes,
		// so nothing is known about the file either way, and silently indexing a
		// partial repository is worse than failing the run.
		return facts.FileFacts{}, fmt.Errorf("index: %w", err)
	}
	ff := parser.Parse(path, src, c)
	ff.File.Path = relative(root, path)
	return ff, nil
}

// relative renders path as root-relative and slash-separated, the form the
// `file` table stores so that a path means the same thing on every platform.
func relative(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// walk returns every file under root that has a registered parser, sorted.
//
// extract.Supported is the whole selection rule: the registry is the single
// place that knows the set of supported languages, so a walk that filtered on
// its own extension list would be a second one to keep in sync.
//
// Sorted because the order files are loaded in must not change what ends up in
// the database, and a deterministic order is the cheapest way to notice if it
// ever does.
//
// Root is walked even when its own name is one that would be pruned, so
// pointing the loader straight at a directory called `testdata` still indexes
// it. Symlinks and other non-regular entries are ignored: filepath.WalkDir does
// not follow them, and a symlink into a directory already walked would load the
// same file under two paths.
func walk(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && prunedDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !extract.Supported(path) {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(out)
	return out, nil
}

// prunedDir reports whether a directory is not part of the corpus.
//
// The rule is go/build's own: directories beginning with `.` or `_` are
// invisible to the Go tool, and so are `testdata` and `vendor`. Reusing it
// rather than inventing one means `.git`, `.worktrees` and every editor cache
// are covered by the rule that already covers them for the compiler, and that
// what CodiQ indexes is what `go build ./...` compiles. `node_modules` is the
// same idea for the ecosystem M6 adds.
//
// .gitignore is deliberately not consulted. Honouring it means either a
// gitignore implementation or a `git` subprocess per repository, and neither
// earns its keep here: build output is not a supported extension, and the
// generated trees that do contain source (`node_modules`, dot-directories) are
// already pruned above.
func prunedDir(name string) bool {
	switch name {
	case "testdata", "vendor", "node_modules":
		return true
	}
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}
