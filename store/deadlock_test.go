package store

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/gaarutyunov/codiq/facts"
	"github.com/gaarutyunov/codiq/store/sqlc"
)

// ---------------------------------------------------------------------------
// Re-indexing two files at once must not deadlock (SPEC.md §6, §14 M2).
//
// This is a regression test for a defect the rest of the package cannot reach,
// because reproducing it needs all three of the conditions below at the same
// time and every other test here has at most two:
//
//   - Two *different* files. TestReplaceFileIsSafeUnderConcurrentLoads races 16
//     goroutines, but over the same two paths, and same-path loads are
//     serialized by the advisory lock in resolveFile — so they queue rather than
//     interleave.
//   - Derived rows already present, i.e. the *second* index. A first index has
//     no cross-file edges to delete yet, so deleteFile's derived-table steps all
//     match zero rows and take no locks at all.
//   - Cross-file references in *both* directions. One file referencing another
//     gives derived rows that only ever point one way, and two transactions that
//     reach for the same rows in the same order merely wait for each other.
//
// With all three, deleteFile clears each derived table from both endpoint sides
// — the source side because the file owns those rows, the target side because
// they reference occurrences it is about to delete — and the two transactions
// walk the shared rows in opposite orders. That is a lock cycle, and PostgreSQL
// reports it as SQLSTATE 40P01.
// ---------------------------------------------------------------------------

// mutualFacts is one half of a pair of files that reference each other's
// definitions: it defines `self`0..N and references `peer`0..N, all functions so
// that both RebuildResolvesTo and RebuildCalls derive rows from them.
//
// Mutual cross-file reference is ordinary — two files in one package calling
// each other's helpers is the commonest shape in a Go corpus — and it is the
// only shape that puts derived rows both into and out of the same file. The
// count is deliberately not 1: every additional symbol is another shared row for
// the two transactions to disagree about the order of, which is what makes the
// race land in a bounded number of rounds instead of eventually.
func mutualFacts(path, self, peer string) facts.FileFacts {
	const symbols = 25

	ff := facts.FileFacts{
		File:   facts.File{Path: path, Lang: "go", Coord: testCoord},
		Scopes: []facts.Scope{{ID: 1, Kind: facts.ScopeFile, RangeStart: 0, RangeEnd: symbols * 100}},
	}
	for i := range symbols {
		def, ref := facts.LocalID(1+i), facts.LocalID(1+symbols+i)
		ff.Occurrences = append(ff.Occurrences,
			facts.Occurrence{
				ID:         def,
				Descriptor: desc(fmt.Sprintf("mutual/%s%d().", self, i)),
				Role:       facts.RoleDefinition, SymbolKind: facts.KindFunction,
				Name: fmt.Sprintf("%s%d", self, i), RangeStart: i * 100, RangeEnd: i*100 + 10, Scope: 1,
			},
			// Cross-file, so it carries the peer's descriptor unresolved and the
			// link pass is what turns it into an edge (SPEC.md §7).
			facts.Occurrence{
				ID:         ref,
				Descriptor: desc(fmt.Sprintf("mutual/%s%d().", peer, i)),
				Role:       facts.RoleReference, SymbolKind: facts.KindFunction,
				Name: fmt.Sprintf("%s%d", peer, i), RangeStart: i*100 + 20, RangeEnd: i*100 + 30, Scope: 1,
			},
		)
		ff.Edges = append(ff.Edges,
			facts.Edge{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(def)},
			facts.Edge{Kind: facts.EdgeContains, Source: facts.ScopeRef(1), Target: facts.OccurrenceRef(def)},
			facts.Edge{Kind: facts.EdgeContains, Source: facts.ScopeRef(1), Target: facts.OccurrenceRef(ref)},
		)
	}
	return ff
}

func TestReplaceFileDoesNotDeadlockOnConcurrentReindex(t *testing.T) {
	reset(t)
	ctx := context.Background()

	a := mutualFacts("mutual/a.go", "A", "B")
	b := mutualFacts("mutual/b.go", "B", "A")

	// The derived rows are built with the real link queries rather than by hand,
	// so what the deletes collide over is exactly what a linked corpus holds.
	// link.RebuildAll itself is unreachable from here — link imports store — but
	// both packages drive the same generated queries.
	q := sqlc.New(pool)
	relink := func(t *testing.T) {
		t.Helper()
		require.NoError(t, q.DeleteAllResolvesTo(ctx))
		require.NoError(t, q.RebuildResolvesTo(ctx))
		require.NoError(t, q.DeleteAllCalls(ctx))
		require.NoError(t, q.RebuildCalls(ctx))
	}

	// Round 0 is the *first* index: load both files, then link. Every round after
	// it is a re-index over a corpus that already has derived rows, which is the
	// only state in which the cycle exists.
	const rounds = 25
	for round := range rounds {
		var start sync.WaitGroup
		start.Add(1)
		var g errgroup.Group
		for _, ff := range []facts.FileFacts{a, b} {
			g.Go(func() error {
				start.Wait() // both transactions open together, not one after the other
				return ReplaceFile(ctx, pool, ff)
			})
		}
		start.Done()
		require.NoError(t, g.Wait(), "concurrent re-index of two mutually referencing files, round %d", round)

		relink(t)
		if round == 0 {
			// Guard the guard: a round that links nothing proves nothing, so
			// state the preconditions rather than assume them.
			require.NotZero(t, count(t, "resolves_to"), "the fixtures must derive resolves_to rows in both directions")
			require.NotZero(t, count(t, "calls"), "the fixtures must derive calls rows in both directions")
			require.Equal(t, 2, crossFileDirections(t, "resolves_to"),
				"derived rows must run both a.go -> b.go and b.go -> a.go, or there is no cycle to break")
		}
	}

	// The graph must also still be right, not merely un-deadlocked: the last
	// round's re-index replaced both files, so the base rows are the fixtures'
	// and nothing was lost to a rolled-back victim.
	require.Equal(t, 2, count(t, "file"))
	require.Equal(t, 2*25*2, count(t, "occurrence"))
}

// crossFileDirections counts the distinct (source file, target file) orderings a
// derived table holds, so a test can assert that its rows really do run both
// ways.
func crossFileDirections(t *testing.T, table string) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT count(*) FROM (
			SELECT DISTINCT s.file_id, d.file_id FROM `+table+` e
			JOIN occurrence s ON s.id = e.source_id
			JOIN occurrence d ON d.id = e.target_id
			WHERE s.file_id <> d.file_id
		) x`).Scan(&n))
	return n
}
