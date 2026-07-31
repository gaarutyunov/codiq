// Package link materializes CodiQ's cross-file edges (SPEC.md §7).
//
// Cross-file edges are derived, never extracted: extraction is file-local
// (SPEC.md §5), so a reference whose target lives in another file is emitted
// carrying the target's SCIP descriptor unresolved, and this package is what
// turns those descriptors into edges. The join key is the descriptor already on
// the base rows and the "symbol index" is the plain btree over
// `occurrence.descriptor` — §7 is explicit that there is no separate structure.
//
// The derivation runs in SQL, not in Go loops: the whole point of keeping
// identity a string match (§4.3) is that the join is a join, and the database
// is where a self-join over one indexed column belongs. `RebuildAll` therefore
// moves no rows through the client.
//
// M2 has one link mode: the full rebuild — every derived table emptied and
// recomputed from base facts. It is also §7's permanent backstop (the nightly
// and on-demand re-link that self-heals drift). Incremental re-link is M8 and
// is deliberately absent here.
package link

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/gaarutyunov/codiq/store/sqlc"
)

// DB is what RebuildAll needs of a database handle: the ability to start a
// transaction. *pgxpool.Pool, *pgx.Conn and pgx.Tx all satisfy it, so a caller
// can hand over its pool or nest the rebuild inside a wider transaction —
// which is what SPEC.md §6's single-transaction reduce sequence (base load →
// core link) becomes once DBOS arrives in M3.
type DB interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// RebuildAll recomputes all five derived cross-file edge tables from the
// extracted base facts, in one transaction.
//
// It is idempotent, and by construction rather than by conflict handling: each
// table is emptied and rewritten from base facts the rebuild never modifies,
// and each rewrite inserts a DISTINCT set. So the derived layer is a pure
// function of the base layer, and running RebuildAll twice yields exactly the
// same rows.
//
// Over the M1 seed's corpus it yields the rows already there, which is a claim
// about deploy/seed/seed.sql and not about this function: the seed hand-wrote the
// derived edges M1 had no link pass to compute, and they coincide with the
// rebuild's output only for as long as they follow from the seed's own base rows.
// They did not, once — the seed had no `package` occurrences, so its `imports`
// edges were underivable and the first rebuild dropped them for good.
// TestRebuildAllIsAFixedPointOfTheM1Seed runs the real seed file through the real
// rebuild so that the coincidence is checked rather than assumed.
//
// The whole rebuild is one transaction so readers never observe a half-linked
// graph: the gopgql MCP surface (§8) serves cross-file navigation straight out
// of these tables, and a snapshot missing half its `resolves_to` rows is worse
// than a stale one.
func RebuildAll(ctx context.Context, db DB) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("link: begin: %w", err)
	}
	// Rollback on any early return; a no-op once Commit has succeeded.
	defer func() { _ = tx.Rollback(ctx) }()

	q := sqlc.New(tx)

	// The link lock, exclusive, and the first statement of the transaction
	// (query.sql's banner). A full rebuild empties every derived table, so it
	// cannot run beside a loader replacing the occurrences those tables point
	// at -- the endpoint FKs would refuse one of the two with SQLSTATE 23503.
	// Every writer of the base facts takes the same lock in shared mode, so this
	// waits for the loads in flight and holds off the ones that start while it
	// runs, while two loaders still run in parallel with each other. Taken
	// first, because a waiter that holds nothing cannot be half of a cycle.
	//
	// It is also why nothing below takes ordered row locks the way
	// store.deleteFile and link.Batch.Relink do: holding this exclusively means
	// there is no second transaction to deadlock against.
	if err := q.LockLinkExclusive(ctx); err != nil {
		return fmt.Errorf("link: lock: %w", err)
	}

	// Delete before recompute, in the order the derivations depend on nothing:
	// no derived table is an input to another, so the five are independent and
	// the only ordering constraint is that a table is emptied before it is
	// refilled.
	for _, step := range []struct {
		name  string
		clear func(context.Context) error
		build func(context.Context) error
	}{
		{"resolves_to", q.DeleteAllResolvesTo, q.RebuildResolvesTo},
		{"imports", q.DeleteAllImports, q.RebuildImports},
		{"calls", q.DeleteAllCalls, q.RebuildCalls},
		{"implements", q.DeleteAllImplements, q.RebuildImplements},
		{"type_defines", q.DeleteAllTypeDefines, q.RebuildTypeDefines},
	} {
		if err := step.clear(ctx); err != nil {
			return fmt.Errorf("link: clear %s: %w", step.name, err)
		}
		if err := step.build(ctx); err != nil {
			return fmt.Errorf("link: rebuild %s: %w", step.name, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("link: commit: %w", err)
	}
	return nil
}
