// The reduce half of the map-reduce loader (SPEC.md §6, §14 M4, M5).
//
// M3 loaded one file per transaction. M4 loads a whole batch in one: every
// file's delete-and-COPY, then the link rebuild, then a single commit. §6 asks
// for exactly that — "reduce sequence: base load → core link → overlay
// producers, all within the one transaction" — and the reason is visible from
// outside the process. The derived cross-file edges are a function of the base
// facts (§7), so between the first file's write and the last file's link pass
// the graph is not merely stale, it is *inconsistent*: `resolves_to` rows point
// at occurrences that the batch has already replaced. One transaction means no
// reader ever sees that window, and a batch that fails leaves the previous
// graph exactly as it was.
//
// §6 names `RunAsTransaction` as the mechanism, and that part is not reachable:
// it hands back a dbos.Tx, which has no CopyFrom, and six of store's seven
// tables are loaded by binary COPY. So the transaction stays store's own pgx.Tx
// and DBOS checkpoints the step around it (the argument is dbos.go's package
// comment, and it is the same one M3 made for the per-file load). What is lost
// is the checkpoint and the commit landing atomically; what that costs is a
// batch that commits and then crashes before its checkpoint, which re-runs the
// reduce on recovery. That is harmless here and only because it is: every
// operation below is a replace.
//
// Widening the transaction from a file to a batch puts a ceiling on how big a
// batch may be, and it is worth writing down because nothing announces it until
// it is hit. store.resolveFile takes a transaction-scoped advisory lock per
// file, and those are held to the commit rather than released per file, so a
// batch of N files holds N of them at once; on the stock postgres:19beta2 image
// (max_locks_per_transaction 128, max_connections 100) one transaction ran out
// of shared memory at 17,409 — measured.
//
// M4 had a second, lower ceiling: it kept every file's facts in RAM from the
// map phase until the reduce. That one is gone. The batch is now a slice of
// artifact keys, ~69 bytes each, and this function reads one artifact at a time
// *inside* the transaction, so the resident set is one FileFacts rather than
// all of them and the workflow's own memory is linear in the file count at a
// few tens of bytes per file. What remains is the advisory-lock ceiling, which
// is now the binding one and is the number above: ~17.4k files per batch on a
// stock image, raisable with max_locks_per_transaction. Chunking the batch
// below that would buy a lower ceiling at the price of running the whole-graph
// link rebuild once per chunk, so it is still not worth doing.
//
// M8 changes the link half of the sequence and nothing else about it. Where the
// reduce used to end in link.RebuildAll -- delete every derived table, recompute
// the whole corpus -- it now accumulates the batch's neighbourhood as it loads
// (link.Batch) and re-links only that. The full rebuild stays, as SPEC.md §7's
// scheduled and on-demand backstop (schedule.go) and as the incremental path's
// own fallback for `implements`, which cannot be scoped to a neighbourhood at
// all (link.Batch.Relink).
//
// What that is worth depends entirely on how much of the corpus a batch is, and
// it is worth being blunt about: `codiq <repo>` walks the whole tree and reduces
// every file it finds, and store.flatten mints fresh occurrence uuids on every
// load, so on a whole-tree run *every* file is a changed file and the
// neighbourhood is the whole corpus. The incremental path then does what the
// rebuild did, plus two indexed queries per file. The saving is real but it
// belongs to a batch that is a *subset* of the corpus -- which the mechanism now
// supports and the walk does not yet produce.
package index

import (
	"context"
	"fmt"

	"github.com/gaarutyunov/codiq/link"
)

// reduce writes a batch of extracted facts and rebuilds the derived edges, in
// one transaction, retrying the whole transaction past a transient
// serialization failure. It reports how many retries that took.
//
// Retrying moved up from the file to the batch, and it had to. M2's
// loadWithRetry re-ran one file's load because each file was its own
// transaction; inside a batch a deadlock aborts the *transaction*, so every
// later statement on it fails with "current transaction is aborted" and
// re-running the one file would achieve nothing. The unit of retry is now the
// unit of atomicity, which is the only thing it can be.
//
// It is still a correct answer and not a pragmatic one, for M2's reason: a
// deadlock victim's transaction is rolled back whole, so there is nothing to
// undo, and the whole batch is a replace, so doing it again is doing it for the
// first time.
//
// From M5 the batch is a list of artifact keys and this function owns the other
// half of Decision 16: **the artifacts are deleted if and only if the
// transaction committed.** §14 M5's skeleton is literally
// `if err == nil { artifact.Delete(ctx, keys...) }`, and the asymmetry is the
// point — a failed batch keeps its artifacts, so the resume consumes them
// without re-extracting a single file (§6: "the step re-runs deterministically
// from its checkpoint over the already-produced artifacts — no re-extraction").
//
// A delete that fails does not fail the reduce, and that is deliberate rather
// than sloppy. The transaction has committed; the batch *is* loaded, and
// returning an error here would make the workflow report a run that wrote
// nothing when it wrote everything. What an undeleted artifact costs is disk,
// and it costs it only until the next index of the same tree, which overwrites
// it — artifact.Key is a pure function of (root, path) precisely so that
// orphans are reclaimable without the sweeper Decision 16 declines to ship. So
// the honest state after a failed delete is "loaded, and one stale artifact",
// which is the same state §6 already defines for a failed batch.
func (reg *registration) reduce(ctx context.Context, keys []string) (int, error) {
	retries, err := withRetry(ctx, func() error { return reg.reduceOnce(ctx, keys) })
	if err != nil {
		return retries, err
	}
	_ = reg.art.Delete(ctx, keys...)
	return retries, nil
}

// reduceOnce is one attempt: one transaction, one commit.
//
// Both collaborators take an interface whose only method is Begin, and the
// argument here is the batch's own pgx.Tx rather than the pool. That is what
// nests them: pgx.Tx.Begin opens a savepoint, so store.ReplaceFile and
// link.RebuildAll each keep the atomicity they document while the enclosing
// transaction decides whether any of it is kept. Neither package needed a line
// changed for M4, which is what those interfaces were for (store.DB, link.DB).
//
// The order is §6's: every file's base rows first, then the link pass, because
// a cross-file edge is derived from base facts the batch is still writing. The
// order *within* a file — vertices before edges, scopes before occurrences — is
// store's and is unchanged. Files are visited in the batch's order, which is
// the walk's path order, so two runs over one tree take the same locks in the
// same sequence.
//
// Nothing here treats a per-file failure as a skip. A file the extractor could
// not parse never reaches this function — mapFiles flags it and keeps it out of
// the batch — so a failure at this point is the database refusing a write, and
// SPEC.md §6's atomicity is the answer to that, not §5's poison-skip. The
// deferred Rollback is what makes "the batch failed" and "the graph is
// untouched" the same sentence.
//
// Each artifact is read *inside* the transaction, immediately before the file it
// describes is written — §14 M5's `for k in keys: store.ReplaceFile(tx,
// artifact.Read(ctx, k))`, and not an incidental ordering.
// Reading them all up front would put the whole batch's facts back in memory
// and undo the milestone; reading one at a time keeps the resident set at a
// single file's worth, which is what makes the batch size a lock-table question
// rather than a heap question (the package comment). A read failure fails the
// batch: an artifact that is gone or unreadable means the file cannot be loaded,
// and loading the rest of the batch without it would leave the graph missing a
// file the run reported as loaded.
func (reg *registration) reduceOnce(ctx context.Context, keys []string) error {
	tx, err := reg.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("index: reduce: begin: %w", err)
	}
	// Rollback on any early return; a no-op once Commit has succeeded.
	defer func() { _ = tx.Rollback(ctx) }()

	// The batch's neighbourhood, accumulated as its files are loaded and
	// re-linked once at the end (SPEC.md §7: "runs as a reduce step, once per
	// batch over the union of affected neighborhoods"). Opened before the first
	// artifact is read, because it takes the link lock that keeps the scheduled
	// backstop off a batch in flight, and a lock taken after the first write is
	// a lock taken too late.
	batch, err := link.NewBatch(ctx, tx)
	if err != nil {
		return fmt.Errorf("index: reduce: %w", err)
	}

	for _, key := range keys {
		ff, err := reg.art.Read(ctx, key)
		if err != nil {
			return fmt.Errorf("index: reduce: %w", err)
		}
		// Before *and* after, and the load between them is what makes the
		// first call unrepeatable: it is the only chance to see which files
		// referenced what this one used to define. link.Batch.Touch has the
		// argument for why that matters -- an inbound `imports` edge is the one
		// derived row store.deleteFile does not clear -- and a test that runs
		// the version without it and requires a wrong answer.
		if err := batch.Touch(ctx, ff.File.Path); err != nil {
			return fmt.Errorf("index: reduce: %w", err)
		}
		if err := reg.l.load(ctx, tx, ff); err != nil {
			return fmt.Errorf("index: reduce: %w", err)
		}
		if err := batch.Touch(ctx, ff.File.Path); err != nil {
			return fmt.Errorf("index: reduce: %w", err)
		}
	}
	if err := batch.Relink(ctx); err != nil {
		return fmt.Errorf("index: reduce: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("index: reduce: commit: %w", err)
	}
	return nil
}
