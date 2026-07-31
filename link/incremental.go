// Incremental re-link (SPEC.md §7, §14 M8, Decision 5).
//
// Every milestone before this one *added* something. This one changes an answer
// that was already correct: RebuildAll is a pure function of the base facts, and
// that total recompute is exactly why the derived layer has been right since M2
// — there is no state to drift. So an incremental re-link is not a feature, it
// is an optimization under an equality obligation: for any change, the rows it
// leaves behind must be the rows a full rebuild would have left behind. Its
// failure mode is a graph that looks entirely plausible and is quietly wrong; a
// missing edge is invisible, and a stale one is worse.
//
// The whole design is therefore shaped to make that equality *checkable rather
// than asserted*, in three ways:
//
//   - Each scoped derivation in query.sql is its Rebuild… counterpart with one
//     conjunct added. Set the owner list to every file in the corpus and the
//     conjunct is a tautology, so the two are the same query. That is the
//     equality argument by reading.
//   - The neighbourhood is a precise set rather than a heuristic one, and the
//     proof that it is neither too small nor too large is in query.sql's
//     banner, in both directions.
//   - incremental_test.go runs the two paths against each other over a series of
//     change shapes — add a file, delete a file, change a reference, change a
//     definition others depend on, rename a symbol — and compares *rendered
//     edges*, endpoints by natural key. A re-link that keeps the row count while
//     moving an edge fails it.
//
// What this package cannot do incrementally is `implements`, and Relink says so
// by falling back to the full rebuild for that one table rather than
// approximating it. The reason is in Relink's own comment.
package link

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gaarutyunov/codiq/store/sqlc"
)

// Batch is the neighbourhood of one reduce batch, accumulated while the batch's
// files are loaded and re-linked once at the end (SPEC.md §7: "runs as a reduce
// step, once per batch over the union of affected neighborhoods").
//
// It holds an open transaction rather than a handle that could open one,
// deliberately. The rows an incremental re-link derives from are the ones the
// batch has written and not yet committed, so a re-link that opened a
// transaction of its own would derive the graph as it was before the batch —
// the one case where being wrong is completely silent. That is also why this is
// pgx.Tx and not link.DB.
type Batch struct {
	q *sqlc.Queries
	// owners is the set of files whose owned cross-file edges the batch may
	// have invalidated. A set rather than a slice because Touch is called twice
	// per file and neighbourhoods overlap heavily — two files of one package
	// share every referencing file.
	owners map[uuid.UUID]struct{}
}

// NewBatch starts an incremental re-link over tx and takes the link lock in
// shared mode.
//
// The lock is what keeps the scheduled backstop (Decision 17) off a batch that
// is in flight; query.sql's banner has the argument. It is taken here — before
// the caller has read an artifact, let alone written a row — because the
// exclusive waiter must never find a loader holding rows it needs, and a waiter
// that holds nothing cannot be half of a cycle.
func NewBatch(ctx context.Context, tx pgx.Tx) (*Batch, error) {
	q := sqlc.New(tx)
	if err := q.LockLinkShared(ctx); err != nil {
		return nil, fmt.Errorf("link: lock: %w", err)
	}
	return &Batch{q: q, owners: map[uuid.UUID]struct{}{}}, nil
}

// Touch records the neighbourhood of one changed file.
//
// **Call it immediately before the file's rows are replaced and again
// immediately after.** The "after" call finds the files that reference what this
// file now defines; the "before" call finds the files that referenced what it
// *used to* define, which after the load is unrecoverable — which is the whole
// reason this is not one call taking a list of paths.
//
// The pre-replacement half is the one that looks redundant, so it is worth
// saying exactly what it buys. Most of what it appears to cover is covered
// twice: an edge into a definition that was re-loaded is deleted by
// store.deleteFile's *target*-side delete and re-derived because the descriptor
// comes back, and an edge into a definition that moved is found by the post-load
// call on whichever file in the batch received it. `imports` is where that stops
// being true. It is file → file, both endpoints survive a ReplaceFile, and so
// store.deleteFile clears it from the source side only (query.sql says why) —
// an inbound import edge is the one derived row nothing deletes. When a file
// stops declaring the package another file imports, the pre-batch neighbourhood
// is the only thing that can notice.
//
// It also buys independence. Without it, the correctness of an incremental
// re-link would rest on store.deleteFile continuing to delete inbound edges from
// the other four tables — a coupling to another package's implementation detail,
// invisible from here, and exactly the kind that gets broken by a schema change
// that looks unrelated. With it, the neighbourhood is sufficient on its own.
// link/incremental_test.go's TestPriorNeighbourhoodIsLoadBearing runs the
// version without it and requires the answer to be wrong.
//
// Calling it more often than that is harmless — the set absorbs duplicates and
// a file that is in the neighbourhood without needing to be costs a recompute,
// not a wrong answer — which is what makes the reduce's at-least-once step
// semantics safe (index/reduce.go).
func (b *Batch) Touch(ctx context.Context, path string) error {
	ids, err := b.q.RelinkOwners(ctx, path)
	if err != nil {
		return fmt.Errorf("link: neighbourhood of %s: %w", path, err)
	}
	for _, id := range ids {
		b.owners[id] = struct{}{}
	}
	return nil
}

// Owners is how many files the batch will re-link. It is the size of the work
// the incremental path saved, and the number a caller reports.
func (b *Batch) Owners() int { return len(b.owners) }

// Relink recomputes the neighbourhood's cross-file edges, in the batch's
// transaction.
//
// Four of the five derived tables are scoped to the owner set: their rows are
// deleted from the *source* side, which is ownership (§7), and recomputed by the
// Relink… queries — the full derivation with the referencing side restricted.
// Rows owned by a file outside the set are not touched, because they cannot have
// changed: query.sql's banner is the argument.
//
// **`implements` is rebuilt in full, and cannot be scoped.** It is the one
// derivation with no reference occurrence anywhere in it — Go's interface
// satisfaction is implicit, so the edge is structural method-set containment
// over descriptor suffixes — and that breaks the neighbourhood in two
// independent ways:
//
//   - There is no owning file to scope *to*. A member-level row runs from one
//     type's method to another's, and a method of type `T` may be declared in
//     any file of `T`'s package, so an `implements` row can have a source in a
//     file that holds neither of the two types.
//   - Scoping by the implementing type is unsound in the direction that matters.
//     If an interface *loses* a method, types that already had the rest now
//     satisfy it, so edges must be *created* from files that did not change and
//     reference nothing in the file that did. Nothing in the changed file's
//     descriptors names them.
//
// A conservative neighbourhood that caught that case would have to compute the
// method set of every candidate type in the corpus, which is the expensive half
// of the derivation itself — so scoping it would buy nothing even if it were
// sound. Falling back is therefore the honest answer rather than the lazy one,
// and it is the answer SPEC.md §7 already allows for: the full rebuild is the
// permanent backstop, and this uses it a table at a time.
//
// The table order is store.deleteFile's — resolves_to, calls, implements,
// type_defines, then imports — and it has to stay fixed for the reason that
// function's comment gives: ordering acquisitions *within* a table only rules
// out a single-table cycle, and a fixed order across tables is what rules out
// the rest. Two batches re-linking at once is the case that needs it, since
// their owner sets can overlap even when their changed files do not; `implements`
// keeps its place in the order and takes its rows in the same (source, target)
// order the others do, over the whole table rather than over an owner's rows.
func (b *Batch) Relink(ctx context.Context) error {
	owners := make([]uuid.UUID, 0, len(b.owners))
	for id := range b.owners {
		owners = append(owners, id)
	}
	// `implements` ignores the owner set, and saying so with a closure keeps it
	// in the one list that fixes the table order.
	whole := func(f func(context.Context) error) func(context.Context, []uuid.UUID) error {
		return func(ctx context.Context, _ []uuid.UUID) error { return f(ctx) }
	}

	for _, step := range []struct {
		name  string
		lock  func(context.Context, []uuid.UUID) error
		clear func(context.Context, []uuid.UUID) error
		build func(context.Context, []uuid.UUID) error
	}{
		{"resolves_to", b.q.LockResolvesToByOwners, b.q.DeleteResolvesToByOwners, b.q.RelinkResolvesTo},
		{"calls", b.q.LockCallsByOwners, b.q.DeleteCallsByOwners, b.q.RelinkCalls},
		{"implements", whole(b.q.LockAllImplements), whole(b.q.DeleteAllImplements), whole(b.q.RebuildImplements)},
		{"type_defines", b.q.LockTypeDefinesByOwners, b.q.DeleteTypeDefinesByOwners, b.q.RelinkTypeDefines},
		{"imports", b.q.LockImportsByOwners, b.q.DeleteImportsByOwners, b.q.RelinkImports},
	} {
		if err := step.lock(ctx, owners); err != nil {
			return fmt.Errorf("link: lock %s: %w", step.name, err)
		}
		if err := step.clear(ctx, owners); err != nil {
			return fmt.Errorf("link: clear %s: %w", step.name, err)
		}
		if err := step.build(ctx, owners); err != nil {
			return fmt.Errorf("link: relink %s: %w", step.name, err)
		}
	}
	return nil
}
