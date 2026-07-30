// The reduce half of the map-reduce loader (SPEC.md §6, §14 M4).
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
// of shared memory at 17,409 — measured. Memory is the other ceiling and the
// lower one, since §14 M4 keeps every file's facts in RAM until the reduce. Both
// are what SPEC.md §14 M5 removes by spilling the facts to a shared volume and
// checkpointing keys instead of blobs; neither is worth pre-solving here, and
// chunking the batch instead would buy a lower ceiling at the price of running
// the whole-graph link rebuild once per chunk.
package index

import (
	"context"
	"fmt"

	"github.com/gaarutyunov/codiq/facts"
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
func (reg *registration) reduce(ctx context.Context, batch []facts.FileFacts) (int, error) {
	return withRetry(ctx, func() error { return reg.reduceOnce(ctx, batch) })
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
func (reg *registration) reduceOnce(ctx context.Context, batch []facts.FileFacts) error {
	tx, err := reg.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("index: reduce: begin: %w", err)
	}
	// Rollback on any early return; a no-op once Commit has succeeded.
	defer func() { _ = tx.Rollback(ctx) }()

	for _, ff := range batch {
		if err := reg.l.load(ctx, tx, ff); err != nil {
			return fmt.Errorf("index: reduce: %w", err)
		}
	}
	if err := reg.l.relink(ctx, tx); err != nil {
		return fmt.Errorf("index: reduce: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("index: reduce: commit: %w", err)
	}
	return nil
}
