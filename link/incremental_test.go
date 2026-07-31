package link

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/facts"
	"github.com/gaarutyunov/codiq/store"
)

// The equality demonstration (SPEC.md §14 M8).
//
// M8's whole risk is that an incremental re-link is *almost* right: it produces
// a graph that answers queries, plausibly, out of a row that should have been
// recomputed and was not. So the bar is not "the incremental path works", it is
// **equality with the full rebuild, demonstrated over real changes**, and every
// test below is the same shape:
//
//	apply a change through the incremental path  -> read the derived edges
//	run link.RebuildAll over the same base facts -> read them again
//	the two must be identical
//
// The comparison is over *rendered* edges — endpoints by natural key, from
// rebuild_test.go's dump queries — and not over row counts. A re-link that keeps
// the count while moving an edge to a different symbol is exactly the failure
// this milestone can produce, and a count comparison is blind to it.
//
// The change shapes are the ones that break different halves of the
// neighbourhood: a file added, a file's facts removed, a reference changed, a
// definition others depend on changed, a definition moved to another file, and a
// symbol renamed. The fifth is the one that motivates the whole design — see
// TestPriorNeighbourhoodIsLoadBearing, which asserts that dropping half of
// link.Batch.Touch's contract produces a *different* graph, so the argument for
// that half is checked rather than merely written down.

// ---------------------------------------------------------------------------
// A four-file corpus. rebuild_test.go's three files plus cmd/other/other.go,
// which exists to be *outside* the neighbourhood: it owns derived edges of its
// own and references nothing that cmd/codiq/main.go defines, so a change to
// main.go must leave its rows untouched down to the uuid. That is SPEC.md §14
// M8's stated test — "change one file → only its neighbourhood's edges change"
// — and it needs a file that could have been disturbed and was not.
// ---------------------------------------------------------------------------

// occ is one occurrence, flat: the corpora below are about descriptors and
// adjacency, and a literal per row would bury both under bookkeeping.
type occ struct {
	desc  string
	role  facts.Role
	kind  string
	name  string
	start int
}

// corpusFile builds a FileFacts with every occurrence in the file scope, ids
// assigned in the order given.
//
// One scope for the whole file is enough for what these tests exercise and is
// not a simplification that hides anything: `type_defines` requires the
// declaration and its type reference to share a scope, and putting them all in
// one satisfies that for every pair — so the derivation is exercised, and the
// same-scope guard is left to rebuild_test.go's richer corpus, which has two.
func corpusFile(path string, occs []occ) facts.FileFacts {
	ff := facts.FileFacts{
		File:   facts.File{Path: path, Lang: "go", Coord: testCoord},
		Scopes: []facts.Scope{{ID: 1, Kind: facts.ScopeFile, RangeStart: 0, RangeEnd: 10000}},
	}
	for i, o := range occs {
		id := facts.LocalID(i + 1)
		ff.Occurrences = append(ff.Occurrences, facts.Occurrence{
			ID: id, Descriptor: desc(o.desc), Role: o.role, SymbolKind: o.kind,
			Name: o.name, RangeStart: o.start, RangeEnd: o.start + len(o.name), Scope: 1,
		})
		if o.role == facts.RoleDefinition {
			ff.Edges = append(ff.Edges, facts.Edge{
				Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(id)})
		}
		ff.Edges = append(ff.Edges, facts.Edge{
			Kind: facts.EdgeContains, Source: facts.ScopeRef(1), Target: facts.OccurrenceRef(id)})
	}
	return ff
}

const (
	pathIface = "internal/graph/iface.go"
	pathStore = "internal/graph/store.go"
	pathMain  = "cmd/codiq/main.go"
	pathOther = "cmd/other/other.go"
	pathMoved = "internal/graph/store_moved.go"
)

func ifaceFile() facts.FileFacts {
	return corpusFile(pathIface, []occ{
		{"internal/graph/", facts.RoleDefinition, facts.KindPackage, "graph", 8},
		{"internal/graph/Storer#", facts.RoleDefinition, facts.KindInterface, "Storer", 45},
		{"internal/graph/Storer#Put().", facts.RoleDefinition, facts.KindMethod, "Put", 72},
	})
}

func storeFile() facts.FileFacts {
	return corpusFile(pathStore, []occ{
		{"internal/graph/", facts.RoleDefinition, facts.KindPackage, "graph", 8},
		{"internal/graph/Store#", facts.RoleDefinition, facts.KindType, "Store", 45},
		{"internal/graph/Store#db.", facts.RoleDefinition, facts.KindField, "db", 66},
		{"internal/graph/Store#Put().", facts.RoleDefinition, facts.KindMethod, "Put", 232},
	})
}

func mainFile() facts.FileFacts {
	return corpusFile(pathMain, []occ{
		{"cmd/codiq/", facts.RoleDefinition, facts.KindPackage, "main", 8},
		{"internal/graph/", facts.RoleReference, facts.KindPackage, "graph", 30},
		{"cmd/codiq/main().", facts.RoleDefinition, facts.KindFunction, "main", 125},
		{"cmd/codiq/main().store.", facts.RoleDefinition, facts.KindVariable, "store", 140},
		{"internal/graph/Store#", facts.RoleReference, facts.KindType, "Store", 155},
		{"internal/graph/Store#Put().", facts.RoleReference, facts.KindMethod, "Put", 250},
	})
}

// otherFile is the control. It references only what iface.go declares, so no
// change to main.go or store.go can put it in the neighbourhood, and it owns one
// row in each of the four scoped tables so that "untouched" is a claim with
// something behind it.
func otherFile() facts.FileFacts {
	return corpusFile(pathOther, []occ{
		{"cmd/other/", facts.RoleDefinition, facts.KindPackage, "main", 8},
		{"internal/graph/", facts.RoleReference, facts.KindPackage, "graph", 30},
		{"cmd/other/other().", facts.RoleDefinition, facts.KindFunction, "other", 60},
		{"cmd/other/other().s.", facts.RoleDefinition, facts.KindVariable, "s", 80},
		{"internal/graph/Storer#", facts.RoleReference, facts.KindType, "Storer", 95},
		{"internal/graph/Storer#Put().", facts.RoleReference, facts.KindMethod, "Put", 120},
	})
}

func baseCorpus() []facts.FileFacts {
	return []facts.FileFacts{ifaceFile(), storeFile(), mainFile(), otherFile()}
}

// ---------------------------------------------------------------------------
// Driving the two paths.
// ---------------------------------------------------------------------------

// reduceBatch is index/reduce.go's sequence, in a test: one transaction, the
// batch's files replaced in order, Touch before and after each, one Relink.
//
// It is written out here rather than reached through index because the property
// under test is link's, and a test that went through the workflow would be
// testing the checkpointing as well. What it must not do is drift from the real
// sequence — so it is deliberately the same six lines, in the same order.
func reduceBatch(t *testing.T, ffs ...facts.FileFacts) int {
	t.Helper()
	return reduceBatchWith(t, true, ffs...)
}

// reduceBatchWith is reduceBatch with the pre-replacement half of Touch's
// contract optional, so that TestPriorNeighbourhoodIsLoadBearing can run the
// mistake and show it produces a different graph.
func reduceBatchWith(t *testing.T, prior bool, ffs ...facts.FileFacts) int {
	t.Helper()
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	batch, err := NewBatch(ctx, tx)
	require.NoError(t, err)

	for _, ff := range ffs {
		if prior {
			require.NoError(t, batch.Touch(ctx, ff.File.Path))
		}
		require.NoError(t, store.ReplaceFile(ctx, tx, ff))
		require.NoError(t, batch.Touch(ctx, ff.File.Path))
	}
	owners := batch.Owners()
	require.NoError(t, batch.Relink(ctx))
	require.NoError(t, tx.Commit(ctx))
	return owners
}

// emptied is the closest the pipeline gets to deleting a file: every fact it
// contributed is removed, and the `file` row keeps its id (store.resolveFile).
// A repository that drops a file leaves exactly this behind — see the note on
// TestIncrementalEqualsFullRebuild's "a file's facts are removed" shape.
func emptied(path string) facts.FileFacts {
	return facts.FileFacts{File: facts.File{Path: path, Lang: "go", Coord: testCoord}}
}

// truncate empties the graph. The whole corpus is rebuilt per subtest so that
// one shape's change is never another's starting point.
func truncate(t *testing.T) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		TRUNCATE calls, implements, type_defines, resolves_to, references_local,
		         contains_occurrence, contains_scope, defines, imports,
		         occurrence, scope, file`)
	require.NoError(t, err)
}

// equalsFullRebuild is the assertion the milestone turns on: read the derived
// edges the incremental path left, run the full rebuild over the same base
// facts, read them again, and require the two to be identical.
//
// It logs both sides' shape whether it passes or fails, because the evidence
// this test produces is the point of it and a silent pass is not evidence.
func equalsFullRebuild(t *testing.T, shape string) {
	t.Helper()
	incremental := derived(t)
	require.NoError(t, RebuildAll(context.Background(), pool))
	full := derived(t)

	t.Logf("%-46s %s", shape, render(incremental, full))
	assert.Equal(t, full, incremental,
		"%s: the incremental re-link and the full rebuild disagree", shape)
}

// render is the per-shape line the equality evidence is read off.
func render(incremental, full map[string][]string) string {
	tables := make([]string, 0, len(full))
	for table := range full {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	out := ""
	for _, table := range tables {
		mark := "="
		if !equalStrings(incremental[table], full[table]) {
			mark = "!"
		}
		out += fmt.Sprintf("  %s %s:%d", mark, table, len(full[table]))
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// The demonstration.
// ---------------------------------------------------------------------------

func TestIncrementalEqualsFullRebuild(t *testing.T) {
	// The corpus is rebuilt per subtest rather than shared, so each change is
	// applied to the same known state and a failure names one shape.
	for _, tt := range []struct {
		shape string
		// change is the batch the shape applies on top of the base corpus. It is
		// deliberately *not* the whole corpus: a batch that is a subset is the
		// only case in which an incremental re-link differs from a rebuild at
		// all, so a shape that re-loaded everything would prove nothing.
		change func() []facts.FileFacts
	}{
		{
			// A file appears. Nothing that existed changes, and the new file's
			// references have to find definitions loaded by an earlier batch.
			"a file is added",
			func() []facts.FileFacts {
				return []facts.FileFacts{corpusFile("cmd/third/third.go", []occ{
					{"cmd/third/", facts.RoleDefinition, facts.KindPackage, "main", 8},
					{"internal/graph/", facts.RoleReference, facts.KindPackage, "graph", 30},
					{"cmd/third/third().", facts.RoleDefinition, facts.KindFunction, "third", 60},
					{"cmd/third/third().s.", facts.RoleDefinition, facts.KindVariable, "s", 80},
					{"internal/graph/Store#", facts.RoleReference, facts.KindType, "Store", 95},
					{"internal/graph/Store#Put().", facts.RoleReference, facts.KindMethod, "Put", 120},
				})}
			},
		},
		{
			// A file's facts are removed. Every edge into it must go, and every
			// owner of one of those edges is reachable only from the state
			// *before* the batch replaced it.
			//
			// This is as close to a deleted file as the pipeline gets, and the
			// gap is worth naming: index.walk selects the files that are on
			// disk, so a file removed from the tree is simply never in a batch
			// and its rows stay in the graph for good. That is M2 behaviour and
			// not M8's to change, but it does mean the shape below is what a
			// *future* deletion path would have to call, and this is the test
			// that will already cover it.
			"a file's facts are removed",
			func() []facts.FileFacts { return []facts.FileFacts{emptied(pathStore)} },
		},
		{
			// A reference changes target: main.go stops calling Store.Put and
			// starts calling Storer.Put. Its own edges move; nothing else does.
			"a reference changes target",
			func() []facts.FileFacts {
				return []facts.FileFacts{corpusFile(pathMain, []occ{
					{"cmd/codiq/", facts.RoleDefinition, facts.KindPackage, "main", 8},
					{"internal/graph/", facts.RoleReference, facts.KindPackage, "graph", 30},
					{"cmd/codiq/main().", facts.RoleDefinition, facts.KindFunction, "main", 125},
					{"cmd/codiq/main().store.", facts.RoleDefinition, facts.KindVariable, "store", 140},
					{"internal/graph/Storer#", facts.RoleReference, facts.KindType, "Storer", 155},
					{"internal/graph/Storer#Put().", facts.RoleReference, facts.KindMethod, "Put", 250},
				})}
			},
		},
		{
			// A definition others depend on changes: Store.Put is gone and
			// Store.Store is in its place. main.go is not in the batch, and its
			// `calls` and `resolves_to` rows have to disappear anyway.
			"a depended-on definition changes",
			func() []facts.FileFacts {
				return []facts.FileFacts{corpusFile(pathStore, []occ{
					{"internal/graph/", facts.RoleDefinition, facts.KindPackage, "graph", 8},
					{"internal/graph/Store#", facts.RoleDefinition, facts.KindType, "Store", 45},
					{"internal/graph/Store#db.", facts.RoleDefinition, facts.KindField, "db", 66},
					{"internal/graph/Store#Store().", facts.RoleDefinition, facts.KindMethod, "Store", 232},
				})}
			},
		},
		{
			// A definition *moves* to another file, in a batch holding both ends
			// of the move and neither of the files that reference it. This is
			// the shape SPEC.md §7 calls out by name ("a definition move
			// triggers re-link of referencing files via the descriptor index")
			// and the one that cannot be got right from the post-batch state
			// alone: after the move, nothing about store.go says it used to
			// declare Store, so main.go's re-link is reachable only from the
			// neighbourhood taken before the batch replaced it.
			"a definition moves to another file",
			func() []facts.FileFacts {
				return []facts.FileFacts{
					corpusFile(pathStore, []occ{
						{"internal/graph/", facts.RoleDefinition, facts.KindPackage, "graph", 8},
						{"internal/graph/Store#db.", facts.RoleDefinition, facts.KindField, "db", 66},
					}),
					corpusFile(pathMoved, []occ{
						{"internal/graph/", facts.RoleDefinition, facts.KindPackage, "graph", 8},
						{"internal/graph/Store#", facts.RoleDefinition, facts.KindType, "Store", 45},
						{"internal/graph/Store#Put().", facts.RoleDefinition, facts.KindMethod, "Put", 232},
					}),
				}
			},
		},
		{
			// A symbol is renamed: the interface Storer becomes Reader, so the
			// `implements` pair it anchored has to go — which is the derivation
			// that has no neighbourhood at all and falls back to the full
			// rebuild, exercised here rather than argued about.
			"a symbol is renamed",
			func() []facts.FileFacts {
				return []facts.FileFacts{corpusFile(pathIface, []occ{
					{"internal/graph/", facts.RoleDefinition, facts.KindPackage, "graph", 8},
					{"internal/graph/Reader#", facts.RoleDefinition, facts.KindInterface, "Reader", 45},
					{"internal/graph/Reader#Put().", facts.RoleDefinition, facts.KindMethod, "Put", 72},
				})}
			},
		},
	} {
		t.Run(tt.shape, func(t *testing.T) {
			truncate(t)
			reduceBatch(t, baseCorpus()...)
			// The base corpus itself has to be right before a change to it can
			// mean anything, so the incremental load from empty is checked too.
			equalsFullRebuild(t, "base corpus (incremental load from empty)")

			reduceBatch(t, tt.change()...)
			equalsFullRebuild(t, tt.shape)
		})
	}
}

// TestPriorNeighbourhoodIsLoadBearing runs the mistake.
//
// link.Batch.Touch is documented as "before *and* after", and an argument in a
// comment is not protection — so this drives a change with the
// before-replacement call omitted and requires the result to be **wrong**.
//
// Finding a change that the mistake actually breaks took more than one attempt,
// and the reason is worth recording because it is the shape of the whole
// derived layer. Most of what the pre-batch neighbourhood looks like it is for
// is covered twice over:
//
//   - an edge into a *re-loaded* definition is deleted by store.deleteFile's
//     target-side delete, and re-created because the post-load Touch finds the
//     same descriptor again;
//   - an edge into a definition that *moved* is found by the post-load Touch on
//     whichever file in the batch received it.
//
// `imports` is the exception, and it is an exception by design rather than by
// oversight: it is file → file, its endpoints survive a ReplaceFile, and so
// store.deleteFile clears it from the source side only (query.sql says why). An
// inbound import edge is therefore the one derived row nothing deletes — so when
// a file stops declaring the package another file imports, the only thing that
// can notice is the neighbourhood taken *before* the batch replaced it.
//
// A file emptied of its facts is the smallest change that exhibits it, and is
// also what a deleted file would look like to the loader.
func TestPriorNeighbourhoodIsLoadBearing(t *testing.T) {
	truncate(t)
	reduceBatch(t, baseCorpus()...)

	reduceBatchWith(t, false, emptied(pathStore))
	broken := derived(t)

	require.NoError(t, RebuildAll(context.Background(), pool))
	full := derived(t)

	assert.NotEqual(t, full, broken,
		"omitting the pre-replacement neighbourhood produced the right answer anyway, "+
			"so link.Batch.Touch's before-and-after contract is not what the equality tests are testing")
	// And name the damage, so the failure above is legible when it does fire:
	// two files import a package internal/graph/store.go no longer declares, and
	// nothing deletes an inbound import edge.
	assert.Equal(t, []string{
		"cmd/codiq/main.go -> internal/graph/iface.go",
		"cmd/codiq/main.go -> internal/graph/store.go",
		"cmd/other/other.go -> internal/graph/iface.go",
		"cmd/other/other.go -> internal/graph/store.go",
	}, broken["imports"], "the mistake should leave stale inbound import edges")
	assert.Equal(t, []string{
		"cmd/codiq/main.go -> internal/graph/iface.go",
		"cmd/other/other.go -> internal/graph/iface.go",
	}, full["imports"])
}

// ---------------------------------------------------------------------------
// SPEC.md §14 M8's stated test: change one file → only its neighbourhood's
// edges change.
// ---------------------------------------------------------------------------

// rawEdges reads a derived table as raw uuid pairs, which is what "unchanged"
// has to mean here. The rendered form compares symbols; this compares rows, so
// a re-link that deleted an edge and put an identical one back is still visible
// only if the endpoints' ids moved — and they do not, because store.ReplaceFile
// regenerates ids only for the file it replaces.
func rawEdges(t *testing.T, table string) []string {
	t.Helper()
	return lines(t, `SELECT e.source_id::text || ' ' || e.target_id::text FROM `+table+` e`)
}

// edgesOwnedBy is the same, restricted to the rows one file owns: the rows an
// incremental re-link of that file's neighbourhood is allowed to touch.
func edgesOwnedBy(t *testing.T, table, path string) []string {
	t.Helper()
	return lines(t, `
		SELECT e.source_id::text || ' ' || e.target_id::text
		FROM `+table+` e
		JOIN occurrence o ON o.id = e.source_id
		JOIN file f ON f.id = o.file_id
		WHERE f.path = '`+path+`'`)
}

func TestOnlyTheNeighbourhoodsEdgesChange(t *testing.T) {
	truncate(t)
	reduceBatch(t, baseCorpus()...)

	before := map[string][]string{}
	for _, table := range []string{"resolves_to", "calls", "type_defines"} {
		before[table] = edgesOwnedBy(t, table, pathOther)
	}
	beforeImports := lines(t, `
		SELECT e.source_id::text || ' ' || e.target_id::text
		FROM imports e JOIN file f ON f.id = e.source_id WHERE f.path = '`+pathOther+`'`)
	require.NotEmpty(t, before["resolves_to"], "the control file owns no edges, so it cannot be a control")
	require.NotEmpty(t, before["calls"])
	require.NotEmpty(t, before["type_defines"])
	require.NotEmpty(t, beforeImports)

	// One file changes, and it is one nothing else references.
	owners := reduceBatch(t, corpusFile(pathMain, []occ{
		{"cmd/codiq/", facts.RoleDefinition, facts.KindPackage, "main", 8},
		{"internal/graph/", facts.RoleReference, facts.KindPackage, "graph", 30},
		{"cmd/codiq/main().", facts.RoleDefinition, facts.KindFunction, "main", 125},
		{"cmd/codiq/main().store.", facts.RoleDefinition, facts.KindVariable, "store", 140},
		{"internal/graph/Storer#", facts.RoleReference, facts.KindType, "Storer", 155},
		{"internal/graph/Storer#Put().", facts.RoleReference, facts.KindMethod, "Put", 250},
	}))

	// The neighbourhood is the changed file and nothing else: nobody references
	// what cmd/codiq/main.go declares. Five files are in the graph.
	assert.Equal(t, 1, owners, "the neighbourhood of a file nothing references is that file")

	for _, table := range []string{"resolves_to", "calls", "type_defines"} {
		assert.Equal(t, before[table], edgesOwnedBy(t, table, pathOther),
			"%s rows owned by %s moved, and it is outside the neighbourhood", table, pathOther)
	}
	assert.Equal(t, beforeImports, lines(t, `
		SELECT e.source_id::text || ' ' || e.target_id::text
		FROM imports e JOIN file f ON f.id = e.source_id WHERE f.path = '`+pathOther+`'`),
		"imports rows owned by %s moved, and it is outside the neighbourhood", pathOther)

	// And the changed file's own edges did move, so the assertions above are not
	// passing because nothing happened at all.
	assert.Equal(t, []string{
		"cmd/codiq/main.go#Put -> internal/graph/iface.go#Put",
		"cmd/codiq/main.go#Storer -> internal/graph/iface.go#Storer",
		"cmd/codiq/main.go#graph -> internal/graph/iface.go#graph",
		"cmd/codiq/main.go#graph -> internal/graph/store.go#graph",
	}, lines(t, dumpResolvesTo+` WHERE sf.path = '`+pathMain+`'`))

	equalsFullRebuild(t, "one file changed, neighbourhood of one")
}

// TestNeighbourhoodReachesReferencingFiles is the other half of §7's ownership
// rule, and the case where the neighbourhood has to be *bigger* than the batch:
// a change to a file others reference must pull those others in, or their edges
// are left pointing at occurrence ids store.ReplaceFile has already deleted.
func TestNeighbourhoodReachesReferencingFiles(t *testing.T) {
	truncate(t)
	reduceBatch(t, baseCorpus()...)

	// internal/graph/store.go declares the package descriptor both cmd files
	// import, plus the Store type main.go uses, so re-linking it reaches both of
	// them as well as itself. iface.go stays out, and correctly: it holds no
	// reference occurrence at all, so it owns no cross-file edge that any change
	// anywhere could invalidate.
	owners := reduceBatch(t, storeFile())
	assert.Equal(t, 3, owners, "a re-linked file's neighbourhood must reach every file referencing it")

	equalsFullRebuild(t, "a referenced file re-linked, neighbourhood of four")
}

// TestRelinkOverEveryFileIsTheFullRebuild is the equality argument's base case:
// with the owner set equal to the whole corpus, every scoped derivation's extra
// conjunct is a tautology, so the incremental path *is* the rebuild. If that
// ever stops being true the scoped queries have drifted from their Rebuild…
// counterparts, and every other test in this file is comparing two paths that
// share a bug.
func TestRelinkOverEveryFileIsTheFullRebuild(t *testing.T) {
	truncate(t)
	reduceBatch(t, baseCorpus()...)
	require.NoError(t, RebuildAll(context.Background(), pool))
	full := derived(t)

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	batch, err := NewBatch(ctx, tx)
	require.NoError(t, err)
	// Every file, with no load in between: the derived layer is a function of
	// base facts nothing here changes, so a re-link over all of them has to be a
	// fixed point of itself and of the rebuild.
	for _, ff := range baseCorpus() {
		require.NoError(t, batch.Touch(ctx, ff.File.Path))
	}
	require.Equal(t, 4, batch.Owners())
	require.NoError(t, batch.Relink(ctx))
	require.NoError(t, tx.Commit(ctx))

	assert.Equal(t, full, derived(t), "a re-link over every file is not the full rebuild")
}

// TestRelinkIsIdempotent is the property the reduce's at-least-once step
// semantics rest on (index/reduce.go): a batch whose transaction committed and
// whose checkpoint did not is re-run, and re-running it must be running it once.
func TestRelinkIsIdempotent(t *testing.T) {
	truncate(t)
	reduceBatch(t, baseCorpus()...)
	once := derived(t)

	reduceBatch(t, baseCorpus()...)
	assert.Equal(t, once, derived(t), "a second identical batch changed the derived layer")
}

// TestRelinkWithNoOwnersChangesNothing is the empty case, which a
// `= ANY(ARRAY[]::uuid[])` predicate gets wrong in exactly one direction if it
// is ever rewritten as an `IN` over an empty list or a missing WHERE clause: it
// would delete or rebuild the whole corpus. An empty neighbourhood must be an
// empty amount of work.
func TestRelinkWithNoOwnersChangesNothing(t *testing.T) {
	truncate(t)
	reduceBatch(t, baseCorpus()...)
	before := derived(t)

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	batch, err := NewBatch(ctx, tx)
	require.NoError(t, err)
	require.Zero(t, batch.Owners())
	require.NoError(t, batch.Relink(ctx))
	require.NoError(t, tx.Commit(ctx))

	assert.Equal(t, before, derived(t), "an empty neighbourhood re-linked something")
}

// TestTouchOfAnUnknownPathIsEmpty covers the first batch of a first index: a
// path with no `file` row yet contributes nothing to the neighbourhood, and the
// file itself joins it on the second Touch, after the load.
func TestTouchOfAnUnknownPathIsEmpty(t *testing.T) {
	truncate(t)

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	batch, err := NewBatch(ctx, tx)
	require.NoError(t, err)

	require.NoError(t, batch.Touch(ctx, pathStore))
	assert.Zero(t, batch.Owners(), "an unknown path has a neighbourhood")

	require.NoError(t, store.ReplaceFile(ctx, tx, storeFile()))
	require.NoError(t, batch.Touch(ctx, pathStore))
	assert.Equal(t, 1, batch.Owners(), "a loaded file is not in its own neighbourhood")

	require.NoError(t, batch.Relink(ctx))
	require.NoError(t, tx.Commit(ctx))
}

// TestOwnersAreDistinct guards the set: Touch is called twice per file and
// neighbourhoods overlap heavily, so a slice instead of a set would hand the
// scoped queries an owner list quadratic in the batch size.
func TestOwnersAreDistinct(t *testing.T) {
	truncate(t)
	reduceBatch(t, baseCorpus()...)

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	batch, err := NewBatch(ctx, tx)
	require.NoError(t, err)
	for range 5 {
		for _, ff := range baseCorpus() {
			require.NoError(t, batch.Touch(ctx, ff.File.Path))
		}
	}
	assert.Equal(t, 4, batch.Owners())
}

// TestRawEdgeIdsSurviveAnUnrelatedBatch is the id-level statement the rendered
// comparison cannot make, over the whole graph rather than one control file:
// after a batch that changes one file, every derived row whose source is not in
// that file's neighbourhood is the same row, uuids and all.
func TestRawEdgeIdsSurviveAnUnrelatedBatch(t *testing.T) {
	tables := []string{"resolves_to", "calls", "type_defines"}

	truncate(t)
	reduceBatch(t, baseCorpus()...)

	// The rows *not* owned by the file about to change, before and after. Each
	// side subtracts that file's own rows from its own snapshot, because the
	// uuids on those rows are exactly what a re-load is expected to move.
	rest := func() map[string][]string {
		out := map[string][]string{}
		for _, table := range tables {
			owned := map[string]bool{}
			for _, e := range edgesOwnedBy(t, table, pathMain) {
				owned[e] = true
			}
			kept := []string{}
			for _, e := range rawEdges(t, table) {
				if !owned[e] {
					kept = append(kept, e)
				}
			}
			out[table] = kept
		}
		return out
	}

	before := rest()
	require.NotEmpty(t, before["resolves_to"], "no rows outside the changed file, so nothing to preserve")

	reduceBatch(t, mainFile())

	assert.Equal(t, before, rest(), "derived rows outside the neighbourhood moved")
}

// ---------------------------------------------------------------------------
// The backstop interlock (SPEC.md §7's backstop, Decision 17).
//
// A full re-link empties every derived table, so it cannot run beside a loader
// replacing the occurrences those tables point at: the endpoint FKs are plain
// REFERENCES and one of the two transactions loses with SQLSTATE 23503. Put on a
// nightly timer, that is a scheduled failure. The lock in query.sql is what
// prevents it — shared for every writer of the base facts, exclusive for the
// rebuild — and the three tests below are the three claims that makes.
// ---------------------------------------------------------------------------

// linkLockKey is the key the two locks agree on, restated. Restating it is the
// assertion: if query.sql's key ever moves, the second test below stops timing
// out and says so.
const linkLockKey = `hashtextextended('codiq:link', 0)`

// TestFullRebuildWaitsForALoadInFlight is the hazard itself. A rebuild started
// while a batch holds its transaction open must wait rather than delete the
// rows that batch is about to point at.
func TestFullRebuildWaitsForALoadInFlight(t *testing.T) {
	truncate(t)
	reduceBatch(t, baseCorpus()...)

	ctx := context.Background()
	loading, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = loading.Rollback(ctx) }()
	// A batch in flight: the shared lock is taken and the transaction is not
	// finished, which is the whole of what a reduce looks like from outside.
	_, err = NewBatch(ctx, loading)
	require.NoError(t, err)

	blocked, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	err = RebuildAll(blocked, pool)
	require.Error(t, err, "the backstop ran straight through a batch in flight")
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	// And it is only waiting: once the batch is done, the same call succeeds.
	require.NoError(t, loading.Commit(ctx))
	require.NoError(t, RebuildAll(ctx, pool))
}

// TestALoadWaitsForAFullRebuild is the other direction, and the one the nightly
// schedule needs: a batch that starts while the backstop is running has to wait
// for it, or it deletes occurrences the rebuild is inserting edges to.
func TestALoadWaitsForAFullRebuild(t *testing.T) {
	truncate(t)
	reduceBatch(t, baseCorpus()...)

	ctx := context.Background()
	rebuilding, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = rebuilding.Rollback(ctx) }()
	// A rebuild in flight, held open. RebuildAll commits, so the lock it takes
	// is taken here directly — which also pins the key the two sides share.
	_, err = rebuilding.Exec(ctx, `SELECT pg_advisory_xact_lock(`+linkLockKey+`)`)
	require.NoError(t, err)

	blocked, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	err = store.ReplaceFile(blocked, pool, storeFile())
	require.Error(t, err, "a load ran straight through a full rebuild")
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	require.NoError(t, rebuilding.Commit(ctx))
	require.NoError(t, store.ReplaceFile(ctx, pool, storeFile()))
}

// TestTwoLoadsDoNotWaitForEachOther is what keeps the interlock from being a
// throughput regression dressed up as a fix, and is why the loader's half is
// *shared*. Two batches must still run at once; only the rebuild is exclusive.
//
// It is also the check that can fire. An interlock built out of one exclusive
// lock would pass both tests above and quietly serialize every index in the
// system, and nothing else here would notice.
func TestTwoLoadsDoNotWaitForEachOther(t *testing.T) {
	truncate(t)
	reduceBatch(t, baseCorpus()...)

	ctx := context.Background()
	first, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = first.Rollback(ctx) }()
	_, err = NewBatch(ctx, first)
	require.NoError(t, err)

	// A second batch, while the first is open, on its own connection.
	quick, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	second, err := pool.Begin(quick)
	require.NoError(t, err)
	defer func() { _ = second.Rollback(ctx) }()
	_, err = NewBatch(quick, second)
	require.NoError(t, err, "two loaders serialized against each other")
	require.NoError(t, second.Commit(quick))
	require.NoError(t, first.Commit(ctx))
}

// ---------------------------------------------------------------------------
// What the incremental path actually costs.
// ---------------------------------------------------------------------------

// generatedCorpus is a two-package corpus: `internal/gen` of n files, each
// declaring the package and a type with one method, and `cmd/gen` of m files,
// each importing that package and using one of its types.
//
// The shape is what makes the derivations expensive and is the shape a real Go
// repository has. Every file of `internal/gen` contributes the same package
// definition and every file of `cmd/gen` carries the matching package reference,
// so `imports` and the package half of `resolves_to` are n*m edges — and, more
// to the point here, the package descriptor is what decides how large a
// neighbourhood can get: re-linking any one file of a package reaches every file
// that imports it.
func generatedCorpus(n, m int) []facts.FileFacts {
	out := make([]facts.FileFacts, 0, n+m)
	for i := range n {
		occs := []occ{
			{"internal/gen/", facts.RoleDefinition, facts.KindPackage, "gen", 8},
			{fmt.Sprintf("internal/gen/T%d#", i), facts.RoleDefinition, facts.KindType, fmt.Sprintf("T%d", i), 40},
			{fmt.Sprintf("internal/gen/T%d#Do().", i), facts.RoleDefinition, facts.KindMethod, "Do", 80},
		}
		out = append(out, corpusFile(fmt.Sprintf("internal/gen/f%03d.go", i), occs))
	}
	for j := range m {
		out = append(out, corpusFile(fmt.Sprintf("cmd/gen/c%03d.go", j), []occ{
			{"cmd/gen/", facts.RoleDefinition, facts.KindPackage, "gen", 8},
			{"internal/gen/", facts.RoleReference, facts.KindPackage, "gen", 30},
			{fmt.Sprintf("cmd/gen/use%d().", j), facts.RoleDefinition, facts.KindFunction, fmt.Sprintf("use%d", j), 120},
			{fmt.Sprintf("cmd/gen/use%d().v.", j), facts.RoleDefinition, facts.KindVariable, "v", 160},
			{fmt.Sprintf("internal/gen/T%d#", j%n), facts.RoleReference, facts.KindType, fmt.Sprintf("T%d", j%n), 200},
			{fmt.Sprintf("internal/gen/T%d#Do().", j%n), facts.RoleReference, facts.KindMethod, "Do", 240},
		}))
	}
	return out
}

// manyPackages is the other extreme: p independent two-file packages, each a
// library file and the one file that imports it. Nothing crosses a package
// boundary, so one file's neighbourhood is two files out of 2p however large p
// gets — which is the shape an incremental re-link is actually for, and the
// contrast that makes the single-package measurement above mean something.
func manyPackages(p int) []facts.FileFacts {
	out := make([]facts.FileFacts, 0, 2*p)
	for i := range p {
		out = append(out, corpusFile(fmt.Sprintf("internal/p%03d/lib.go", i), []occ{
			{fmt.Sprintf("internal/p%03d/", i), facts.RoleDefinition, facts.KindPackage, "p", 8},
			{fmt.Sprintf("internal/p%03d/T#", i), facts.RoleDefinition, facts.KindType, "T", 40},
			{fmt.Sprintf("internal/p%03d/T#Do().", i), facts.RoleDefinition, facts.KindMethod, "Do", 80},
		}))
		out = append(out, corpusFile(fmt.Sprintf("cmd/p%03d/main.go", i), []occ{
			{fmt.Sprintf("cmd/p%03d/", i), facts.RoleDefinition, facts.KindPackage, "main", 8},
			{fmt.Sprintf("internal/p%03d/", i), facts.RoleReference, facts.KindPackage, "p", 30},
			{fmt.Sprintf("cmd/p%03d/use().", i), facts.RoleDefinition, facts.KindFunction, "use", 120},
			{fmt.Sprintf("cmd/p%03d/use().v.", i), facts.RoleDefinition, facts.KindVariable, "v", 160},
			{fmt.Sprintf("internal/p%03d/T#", i), facts.RoleReference, facts.KindType, "T", 200},
			{fmt.Sprintf("internal/p%03d/T#Do().", i), facts.RoleReference, facts.KindMethod, "Do", 240},
		}))
	}
	return out
}

// TestRelinkCostAgainstTheRebuild measures the two paths rather than asserting
// about them, because the honest answer depends on something the numbers have to
// show: how much of the corpus a batch is, and how far the corpus fans out.
//
// The equality tests prove the incremental path is *right*. This is the other
// half — whether it is worth having — and it is deliberately not a threshold
// assertion, since a threshold on a shared box measures the box. What it asserts
// is only what is not a timing: the size of each neighbourhood.
//
// Both sides measure the *link phase alone*, with the loads outside the clock.
// That is the only comparison that means anything: the incremental path changes
// what re-linking costs and changes nothing about what loading costs.
func TestRelinkCostAgainstTheRebuild(t *testing.T) {
	ctx := context.Background()

	// One package, fanned out: 100 library files sharing a package descriptor
	// and 20 files importing them. Every importer carries a reference to the
	// descriptor every library file declares, so one file's neighbourhood is
	// already every importer — the worst case for scoping, and the ordinary
	// shape of a Go package.
	const n, m = 100, 20
	fanned := generatedCorpus(n, m)
	truncate(t)
	reduceBatch(t, fanned...)

	started := time.Now()
	require.NoError(t, RebuildAll(ctx, pool))
	fannedRebuild := time.Since(started)
	fannedOne := timeRelink(t, fanned[n/2].File.Path)
	t.Logf("one package fanned out, %d files: full rebuild %v; one file changed: %v",
		n+m, fannedRebuild, fannedOne)
	assert.Equal(t, 1+m, fannedOne.owners,
		"a file of a package is reached by every file importing that package")

	// The whole-corpus batch, which is what `codiq <repo>` actually produces:
	// the tree is walked entire, every file is re-loaded, every file is in the
	// neighbourhood.
	fannedAll := timeRelinkAll(t, fanned)
	t.Logf("one package fanned out, %d files: whole corpus changed: %v", n+m, fannedAll)
	assert.Equal(t, n+m, fannedAll.owners, "every file re-loaded means every file is in the neighbourhood")
	equalsFullRebuild(t, "generated corpus, whole-corpus re-link")

	// Many small packages, nothing crossing a boundary: the shape where scoping
	// is worth what it costs.
	const p = 60
	many := manyPackages(p)
	truncate(t)
	reduceBatch(t, many...)

	started = time.Now()
	require.NoError(t, RebuildAll(ctx, pool))
	manyRebuild := time.Since(started)
	manyOne := timeRelink(t, many[0].File.Path)
	t.Logf("%d independent packages, %d files: full rebuild %v; one file changed: %v",
		p, 2*p, manyRebuild, manyOne)
	assert.Equal(t, 2, manyOne.owners, "a package nothing else imports has a neighbourhood of itself and its one importer")

	equalsFullRebuild(t, "many packages, one file re-linked")
}

// timeRelink re-links a set of paths' neighbourhood and reports how big that
// neighbourhood was and where the time went.
//
// The two durations are reported apart on purpose. `relink` is the work the
// milestone replaces and is what the full rebuild should be compared against;
// `collect` is the work it *adds* — two RelinkOwners queries per changed file,
// which is the price of knowing what the neighbourhood is. An incremental path
// whose collect cost exceeds what its scoping saves is a slower rebuild with
// more ways to be wrong, so the second number is the one that decides whether
// this is worth having on a given corpus.
type relinkCost struct {
	collect time.Duration
	relink  time.Duration
	owners  int
}

func (c relinkCost) String() string {
	return fmt.Sprintf("relink %v + collect %v over %d files", c.relink, c.collect, c.owners)
}

func timeRelink(t *testing.T, paths ...string) relinkCost {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	batch, err := NewBatch(ctx, tx)
	require.NoError(t, err)

	started := time.Now()
	for _, p := range paths {
		// Twice per file, as index/reduce.go calls it.
		require.NoError(t, batch.Touch(ctx, p))
		require.NoError(t, batch.Touch(ctx, p))
	}
	cost := relinkCost{collect: time.Since(started), owners: batch.Owners()}

	started = time.Now()
	require.NoError(t, batch.Relink(ctx))
	cost.relink = time.Since(started)
	require.NoError(t, tx.Commit(ctx))
	return cost
}

func timeRelinkAll(t *testing.T, corpus []facts.FileFacts) relinkCost {
	t.Helper()
	paths := make([]string, 0, len(corpus))
	for _, ff := range corpus {
		paths = append(paths, ff.File.Path)
	}
	return timeRelink(t, paths...)
}
