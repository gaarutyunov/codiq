package link

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/facts"
	"github.com/gaarutyunov/codiq/store"
)

// The link pass is tested against a real postgres:19beta2 running the real
// committed migrations, and its inputs are loaded through the real store — the
// derivations are SQL, so a fake database would test nothing at all.
//
// The corpus is deploy/seed/seed.sql's three-file package, restated as facts.
// That is deliberate: M1 hand-wrote the derived edges that corpus should have,
// with a comment explaining each, and M2's job is to compute them. Reusing it
// means the expectations here were written by someone reasoning about the data
// model rather than by reading this package's SQL back to itself.

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()

	container, err := postgres.Run(ctx, "postgres:19beta2",
		postgres.WithDatabase("codiq"),
		postgres.WithUsername("codiq"),
		postgres.WithPassword("codiq"),
		tc.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(3*time.Minute),
		),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start postgres:19beta2: %v\n", err)
		os.Exit(1)
	}

	code := func() int {
		defer func() { _ = container.Terminate(context.Background()) }()

		dsn, err := container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			fmt.Fprintf(os.Stderr, "connection string: %v\n", err)
			return 1
		}
		pool, err = pgxpool.New(ctx, dsn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "open pool: %v\n", err)
			return 1
		}
		defer pool.Close()

		if err := applyMigrations(ctx, pool); err != nil {
			fmt.Fprintf(os.Stderr, "apply migrations: %v\n", err)
			return 1
		}
		return m.Run()
	}()
	os.Exit(code)
}

func applyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	paths, err := filepath.Glob(filepath.Join("..", "schema", "migrations", "*.sql"))
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("no migrations found")
	}
	sort.Strings(paths)
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		up := string(body)
		if i := strings.Index(up, "-- +goose Down"); i >= 0 {
			up = up[:i]
		}
		if _, err := pool.Exec(ctx, strings.Replace(up, "-- +goose Up", "", 1)); err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// The corpus: cmd/codiq/main.go -> internal/graph/{store.go,iface.go}
//
//	main()          calls        Store.Put()   (cross-file)
//	Store           implements   Storer        (cross-file)
//	store (var)     type_defines Store         (cross-file)
//	graph.Store ref resolves_to  Store def     (cross-file)
//	s.db ref        references_local db def    (same file, extracted)
//
// Each file also carries the `package` definition the extractor emits from its
// package clause, and main.go carries the matching `package` *reference* for its
// import. Those four rows are what `imports` joins on -- a corpus without them
// could not exercise that derivation at all -- and deploy/seed/seed.sql carries
// them too, because a seed whose `imports` rows do not follow from its own base
// rows loses them to the first rebuild. TestRebuildAllIsAFixedPointOfTheM1Seed
// is what keeps the two corpora from drifting apart again.
// ---------------------------------------------------------------------------

var testCoord = coord.Coord{
	Scheme:  "scip-go",
	Manager: "gomod",
	Name:    "github.com/gaarutyunov/codiq",
	Version: "v0.1.0",
}

func desc(suffix string) facts.Descriptor {
	return facts.Descriptor{Prefix: testCoord, Suffix: suffix}
}

func ifaceFacts() facts.FileFacts {
	return facts.FileFacts{
		File:   facts.File{Path: "internal/graph/iface.go", Lang: "go", Coord: testCoord},
		Scopes: []facts.Scope{{ID: 1, Kind: facts.ScopeFile, RangeStart: 0, RangeEnd: 190}},
		Occurrences: []facts.Occurrence{
			{ID: 1, Descriptor: desc("internal/graph/Storer#"), Role: facts.RoleDefinition,
				SymbolKind: facts.KindInterface, Name: "Storer", RangeStart: 45, RangeEnd: 51, Scope: 1},
			{ID: 2, Descriptor: desc("internal/graph/Storer#Put()."), Role: facts.RoleDefinition,
				SymbolKind: facts.KindMethod, Name: "Put", RangeStart: 72, RangeEnd: 75, Scope: 1},
			{ID: 3, Descriptor: desc("internal/graph/"), Role: facts.RoleDefinition,
				SymbolKind: facts.KindPackage, Name: "graph", RangeStart: 8, RangeEnd: 13, Scope: 1},
		},
		Edges: []facts.Edge{
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(1)},
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(2)},
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(3)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(1), Target: facts.OccurrenceRef(1)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(1), Target: facts.OccurrenceRef(2)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(1), Target: facts.OccurrenceRef(3)},
		},
	}
}

func storeGoFacts() facts.FileFacts {
	return facts.FileFacts{
		File: facts.File{Path: "internal/graph/store.go", Lang: "go", Coord: testCoord},
		Scopes: []facts.Scope{
			{ID: 1, Kind: facts.ScopeFile, RangeStart: 0, RangeEnd: 420},
			{ID: 2, Kind: facts.ScopeFunction, RangeStart: 210, RangeEnd: 415, Parent: 1},
		},
		Occurrences: []facts.Occurrence{
			{ID: 1, Descriptor: desc("internal/graph/Store#"), Role: facts.RoleDefinition,
				SymbolKind: facts.KindType, Name: "Store", RangeStart: 45, RangeEnd: 50, Scope: 1},
			{ID: 2, Descriptor: desc("internal/graph/Store#db."), Role: facts.RoleDefinition,
				SymbolKind: facts.KindField, Name: "db", RangeStart: 66, RangeEnd: 68, Scope: 1},
			{ID: 3, Descriptor: desc("internal/graph/Store#"), Role: facts.RoleReference,
				SymbolKind: facts.KindType, Name: "Store", RangeStart: 222, RangeEnd: 227, Scope: 1},
			{ID: 4, Descriptor: desc("internal/graph/Store#Put()."), Role: facts.RoleDefinition,
				SymbolKind: facts.KindMethod, Name: "Put", RangeStart: 232, RangeEnd: 235, Scope: 1},
			{ID: 5, Descriptor: desc("internal/graph/Store#db."), Role: facts.RoleReference,
				SymbolKind: facts.KindField, Name: "db", RangeStart: 300, RangeEnd: 302, Scope: 2},
			{ID: 6, Descriptor: desc("internal/graph/"), Role: facts.RoleDefinition,
				SymbolKind: facts.KindPackage, Name: "graph", RangeStart: 8, RangeEnd: 13, Scope: 1},
		},
		Edges: []facts.Edge{
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(1)},
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(2)},
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(4)},
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(6)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(1), Target: facts.OccurrenceRef(6)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(1), Target: facts.ScopeRef(2)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(1), Target: facts.OccurrenceRef(1)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(1), Target: facts.OccurrenceRef(2)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(1), Target: facts.OccurrenceRef(3)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(1), Target: facts.OccurrenceRef(4)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(2), Target: facts.OccurrenceRef(5)},
			{Kind: facts.EdgeReferencesLocal, Source: facts.OccurrenceRef(3), Target: facts.OccurrenceRef(1)},
			{Kind: facts.EdgeReferencesLocal, Source: facts.OccurrenceRef(5), Target: facts.OccurrenceRef(2)},
		},
	}
}

// mainFacts is the *referencing* file. Its two references carry the target
// descriptors unresolved — nothing here says which file defines Store, because a
// file-local extractor cannot know (SPEC.md §4.3). Every derived edge in this
// corpus originates from these two rows.
func mainFacts() facts.FileFacts {
	return facts.FileFacts{
		File: facts.File{Path: "cmd/codiq/main.go", Lang: "go", Coord: testCoord},
		Scopes: []facts.Scope{
			{ID: 1, Kind: facts.ScopeFile, RangeStart: 0, RangeEnd: 310},
			{ID: 2, Kind: facts.ScopeFunction, RangeStart: 120, RangeEnd: 305, Parent: 1},
		},
		Occurrences: []facts.Occurrence{
			{ID: 1, Descriptor: desc("cmd/codiq/main()."), Role: facts.RoleDefinition,
				SymbolKind: facts.KindFunction, Name: "main", RangeStart: 125, RangeEnd: 129, Scope: 1},
			{ID: 2, Descriptor: desc("cmd/codiq/main().store."), Role: facts.RoleDefinition,
				SymbolKind: facts.KindVariable, Name: "store", RangeStart: 140, RangeEnd: 145, Scope: 2},
			{ID: 3, Descriptor: desc("internal/graph/Store#"), Role: facts.RoleReference,
				SymbolKind: facts.KindType, Name: "Store", RangeStart: 155, RangeEnd: 166, Scope: 2},
			{ID: 4, Descriptor: desc("internal/graph/Store#Put()."), Role: facts.RoleReference,
				SymbolKind: facts.KindMethod, Name: "Put", RangeStart: 250, RangeEnd: 253, Scope: 2},
			{ID: 5, Descriptor: desc("cmd/codiq/"), Role: facts.RoleDefinition,
				SymbolKind: facts.KindPackage, Name: "main", RangeStart: 8, RangeEnd: 12, Scope: 1},
			// `import "github.com/gaarutyunov/codiq/internal/graph"` — the package
			// reference whose descriptor is byte-identical to the package
			// definition each file of that package contributes.
			{ID: 6, Descriptor: desc("internal/graph/"), Role: facts.RoleReference,
				SymbolKind: facts.KindPackage, Name: "graph", RangeStart: 30, RangeEnd: 45, Scope: 1},
		},
		Edges: []facts.Edge{
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(1)},
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(2)},
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(5)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(1), Target: facts.OccurrenceRef(5)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(1), Target: facts.OccurrenceRef(6)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(1), Target: facts.ScopeRef(2)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(1), Target: facts.OccurrenceRef(1)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(2), Target: facts.OccurrenceRef(2)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(2), Target: facts.OccurrenceRef(3)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(2), Target: facts.OccurrenceRef(4)},
		},
	}
}

func loadCorpus(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		TRUNCATE calls, implements, type_defines, resolves_to, references_local,
		         contains_occurrence, contains_scope, defines, imports,
		         occurrence, scope, file`)
	require.NoError(t, err)
	for _, ff := range []facts.FileFacts{ifaceFacts(), storeGoFacts(), mainFacts()} {
		require.NoError(t, store.ReplaceFile(ctx, pool, ff))
	}
}

// ---------------------------------------------------------------------------
// Derived-edge projections, rendered through the endpoints' natural keys.
// ---------------------------------------------------------------------------

const (
	dumpResolvesTo = `SELECT format('%s#%s -> %s#%s', sf.path, a.name, tf.path, b.name)
	                  FROM resolves_to e JOIN occurrence a ON a.id = e.source_id
	                  JOIN occurrence b ON b.id = e.target_id
	                  JOIN file sf ON sf.id = a.file_id JOIN file tf ON tf.id = b.file_id`

	dumpImports = `SELECT format('%s -> %s', sf.path, tf.path) FROM imports e
	               JOIN file sf ON sf.id = e.source_id JOIN file tf ON tf.id = e.target_id`

	dumpCalls = `SELECT format('%s#%s -> %s#%s', sf.path, a.name, tf.path, b.name)
	             FROM calls e JOIN occurrence a ON a.id = e.source_id
	             JOIN occurrence b ON b.id = e.target_id
	             JOIN file sf ON sf.id = a.file_id JOIN file tf ON tf.id = b.file_id`

	dumpImplements = `SELECT format('%s -> %s', a.descriptor, b.descriptor)
	                  FROM implements e JOIN occurrence a ON a.id = e.source_id
	                  JOIN occurrence b ON b.id = e.target_id`

	dumpTypeDefines = `SELECT format('%s -> %s', a.descriptor, b.descriptor)
	                   FROM type_defines e JOIN occurrence a ON a.id = e.source_id
	                   JOIN occurrence b ON b.id = e.target_id`
)

func derived(t *testing.T) map[string][]string {
	t.Helper()
	return map[string][]string{
		"resolves_to":  lines(t, dumpResolvesTo),
		"imports":      lines(t, dumpImports),
		"calls":        lines(t, dumpCalls),
		"implements":   lines(t, dumpImplements),
		"type_defines": lines(t, dumpTypeDefines),
	}
}

func lines(t *testing.T, query string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), query)
	require.NoError(t, err)
	defer rows.Close()
	got := []string{}
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		got = append(got, line)
	}
	require.NoError(t, rows.Err())
	sort.Strings(got)
	return got
}

const pfx = "scip-go gomod github.com/gaarutyunov/codiq v0.1.0 "

// wantDerived is deploy/seed/seed.sql's derived block, restated. Every row here
// has a comment in that file explaining why the corpus should have it.
func wantDerived() map[string][]string {
	return map[string][]string{
		// `graph.Store{}` and `store.Put(...)` in main.go, matched by descriptor
		// against the definitions in store.go. The two `graph` rows are the import
		// itself resolving to the package definition each file of that package
		// contributes — a cross-file use matched by descriptor like any other, and
		// the rows `imports` is then derived from.
		"resolves_to": {
			"cmd/codiq/main.go#Put -> internal/graph/store.go#Put",
			"cmd/codiq/main.go#Store -> internal/graph/store.go#Store",
			"cmd/codiq/main.go#graph -> internal/graph/iface.go#graph",
			"cmd/codiq/main.go#graph -> internal/graph/store.go#graph",
		},
		// One `import "internal/graph"` reaching both files of that package —
		// including iface.go, which main.go references nothing in directly.
		"imports": {
			"cmd/codiq/main.go -> internal/graph/iface.go",
			"cmd/codiq/main.go -> internal/graph/store.go",
		},
		// main() is the enclosing callable of the `store.Put(...)` reference.
		"calls": {"cmd/codiq/main.go#main -> internal/graph/store.go#Put"},
		// Store's method set contains Storer's, at the type and the member level.
		"implements": {
			pfx + "internal/graph/Store# -> " + pfx + "internal/graph/Storer#",
			pfx + "internal/graph/Store#Put(). -> " + pfx + "internal/graph/Storer#Put().",
		},
		// The local `store` in main() has type Store, defined in another file.
		"type_defines": {pfx + "cmd/codiq/main().store. -> " + pfx + "internal/graph/Store#"},
	}
}

// ---------------------------------------------------------------------------
// Proof 4 — RebuildAll materializes the cross-file edges.
// ---------------------------------------------------------------------------

func TestRebuildAllMaterializesCrossFileEdges(t *testing.T) {
	loadCorpus(t)
	ctx := context.Background()

	// Nothing extracted is a cross-file edge: the store loaded three
	// self-contained files and every derived table is empty until the link pass
	// runs. That is the premise the whole §7 design rests on, so assert it.
	for table, got := range derived(t) {
		assert.Empty(t, got, "%s must be empty before the link pass", table)
	}

	require.NoError(t, RebuildAll(ctx, pool))

	got := derived(t)
	for _, table := range []string{"resolves_to", "imports", "calls", "implements", "type_defines"} {
		for _, line := range got[table] {
			t.Logf("%-13s %s", table, line)
		}
	}
	for table, want := range wantDerived() {
		assert.Equal(t, want, got[table], "derived %s", table)
	}
}

// ---------------------------------------------------------------------------
// Proof 5 — RebuildAll twice is idempotent.
//
// It is also §7's nightly backstop, so it runs over an *already linked* graph
// far more often than over an empty one.
// ---------------------------------------------------------------------------

func TestRebuildAllIsIdempotent(t *testing.T) {
	loadCorpus(t)
	ctx := context.Background()

	require.NoError(t, RebuildAll(ctx, pool))
	first := derived(t)
	firstCounts := counts(t)

	require.NoError(t, RebuildAll(ctx, pool))
	require.NoError(t, RebuildAll(ctx, pool))

	assert.Equal(t, first, derived(t), "the derived layer is a pure function of the base facts")
	assert.Equal(t, firstCounts, counts(t), "no row is duplicated and none is dropped")

	// And the base facts the derivation reads are untouched by it — a rebuild
	// that consumed its own input would only look idempotent.
	assert.Equal(t, map[string]int{
		"file": 3, "scope": 5, "occurrence": 15,
		"defines": 10, "contains_scope": 2, "contains_occurrence": 15,
		"references_local": 2,
	}, baseCounts(t))
}

// ---------------------------------------------------------------------------
// The other half of idempotency: a rebuild after a file is re-indexed must
// track the new rows rather than the ones the store deleted.
// ---------------------------------------------------------------------------

func TestRebuildAllFollowsAReindexedFile(t *testing.T) {
	loadCorpus(t)
	ctx := context.Background()
	require.NoError(t, RebuildAll(ctx, pool))
	require.Len(t, derived(t)["resolves_to"], 4)

	// Re-index store.go unchanged: fresh occurrence uuids, so every derived edge
	// that pointed at it was deleted by the store.
	require.NoError(t, store.ReplaceFile(ctx, pool, storeGoFacts()))
	assert.Empty(t, derived(t)["calls"], "the store cleared the edges into the replaced file")

	require.NoError(t, RebuildAll(ctx, pool))
	for table, want := range wantDerived() {
		assert.Equal(t, want, derived(t)[table], "derived %s restored after a re-index", table)
	}

	// Now re-index main.go with the `store.Put(...)` reference removed (local id
	// 4). Its dependent derived edges must disappear and not come back.
	stripped := mainFacts()
	stripped.Occurrences = slices.DeleteFunc(stripped.Occurrences,
		func(o facts.Occurrence) bool { return o.ID == 4 })
	stripped.Edges = slices.DeleteFunc(stripped.Edges, func(e facts.Edge) bool {
		return e.Target.Vertex == facts.VertexOccurrence && e.Target.ID == 4
	})
	require.NoError(t, store.ReplaceFile(ctx, pool, stripped))
	require.NoError(t, RebuildAll(ctx, pool))

	got := derived(t)
	assert.Equal(t, []string{
		"cmd/codiq/main.go#Store -> internal/graph/store.go#Store",
		"cmd/codiq/main.go#graph -> internal/graph/iface.go#graph",
		"cmd/codiq/main.go#graph -> internal/graph/store.go#graph",
	}, got["resolves_to"])
	assert.Empty(t, got["calls"], "the call went with the reference that implied it")
	assert.Equal(t, wantDerived()["imports"], got["imports"],
		"the import is still there, so the imports edges are")
	assert.Equal(t, wantDerived()["implements"], got["implements"],
		"implements is derived from method sets, not from the removed reference")
}

// ---------------------------------------------------------------------------
// Regression: two files in the *same* Go package reference each other's symbols
// with no import at all, so no imports edge may be invented for them.
//
// This is the case an earlier version of RebuildImports got wrong. It derived
// imports from any cross-file reference, reading a package key out of the
// descriptor, which made every same-package cross-file reference look like an
// import. deploy/seed/seed.sql cannot catch it — every cross-file reference in
// that corpus also crosses a package boundary — so it needs its own corpus.
// ---------------------------------------------------------------------------

func TestRebuildAllInventsNoImportWithinAPackage(t *testing.T) {
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		TRUNCATE calls, implements, type_defines, resolves_to, references_local,
		         contains_occurrence, contains_scope, defines, imports,
		         occurrence, scope, file`)
	require.NoError(t, err)

	// Both files are package internal/graph, so both contribute the same package
	// definition and neither carries a package *reference*.
	alpha := facts.FileFacts{
		File:   facts.File{Path: "internal/graph/alpha.go", Lang: "go", Coord: testCoord},
		Scopes: []facts.Scope{{ID: 1, Kind: facts.ScopeFile, RangeEnd: 100}},
		Occurrences: []facts.Occurrence{
			{ID: 1, Descriptor: desc("internal/graph/"), Role: facts.RoleDefinition,
				SymbolKind: facts.KindPackage, Name: "graph", RangeStart: 8, RangeEnd: 13, Scope: 1},
			{ID: 2, Descriptor: desc("internal/graph/Alpha#"), Role: facts.RoleDefinition,
				SymbolKind: facts.KindType, Name: "Alpha", RangeStart: 25, RangeEnd: 30, Scope: 1},
		},
		Edges: []facts.Edge{
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(1)},
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(2)},
		},
	}
	// beta.go uses Alpha. Same package, so there is no import statement to emit.
	beta := facts.FileFacts{
		File:   facts.File{Path: "internal/graph/beta.go", Lang: "go", Coord: testCoord},
		Scopes: []facts.Scope{{ID: 1, Kind: facts.ScopeFile, RangeEnd: 100}},
		Occurrences: []facts.Occurrence{
			{ID: 1, Descriptor: desc("internal/graph/"), Role: facts.RoleDefinition,
				SymbolKind: facts.KindPackage, Name: "graph", RangeStart: 8, RangeEnd: 13, Scope: 1},
			{ID: 2, Descriptor: desc("internal/graph/Beta#"), Role: facts.RoleDefinition,
				SymbolKind: facts.KindType, Name: "Beta", RangeStart: 25, RangeEnd: 29, Scope: 1},
			{ID: 3, Descriptor: desc("internal/graph/Beta#a."), Role: facts.RoleDefinition,
				SymbolKind: facts.KindField, Name: "a", RangeStart: 40, RangeEnd: 41, Scope: 1},
			{ID: 4, Descriptor: desc("internal/graph/Alpha#"), Role: facts.RoleReference,
				SymbolKind: facts.KindType, Name: "Alpha", RangeStart: 42, RangeEnd: 47, Scope: 1},
		},
		Edges: []facts.Edge{
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(1)},
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(2)},
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(3)},
		},
	}
	require.NoError(t, store.ReplaceFile(ctx, pool, alpha))
	require.NoError(t, store.ReplaceFile(ctx, pool, beta))
	require.NoError(t, RebuildAll(ctx, pool))

	got := derived(t)
	assert.Empty(t, got["imports"],
		"same-package files import nothing; an imports edge here would be invented")

	// The cross-file *reference* is still real and still resolves — that is what
	// distinguishes the two derivations.
	assert.Equal(t, []string{"internal/graph/beta.go#Alpha -> internal/graph/alpha.go#Alpha"},
		got["resolves_to"])
	// And the field's type, one file over, is still picked up by adjacency.
	assert.Equal(t, []string{pfx + "internal/graph/Beta#a. -> " + pfx + "internal/graph/Alpha#"},
		got["type_defines"])
}

// ---------------------------------------------------------------------------
// The M1 seed is a fixed point of the rebuild.
//
// Everything above restates deploy/seed/seed.sql as facts, which checks the
// derivations but not the seed: a restatement is faithful only for as long as
// someone keeps it faithful, and the seed is the corpus `docker compose up`
// actually loads. So this reads that file and runs the real rebuild over it.
//
// The property is RebuildAll's own contract — over a corpus the M1 seed already
// linked, it yields exactly the rows that are already there. M1 hand-wrote those
// derived rows with a comment justifying each; if the rebuild disagrees, one of
// the two is wrong, and either way `docker compose up` ends with a graph nobody
// described.
//
// It is here because that stopped being true. The seed predates the decision to
// model a package clause as an occurrence, so it had no `package` rows and its
// two `imports` edges followed from nothing at all: the first rebuild deleted
// them and no later one put them back, which turned a plain `docker compose up`
// into a demo corpus with an empty `imports` table.
// ---------------------------------------------------------------------------

func TestRebuildAllIsAFixedPointOfTheM1Seed(t *testing.T) {
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		TRUNCATE calls, implements, type_defines, resolves_to, references_local,
		         contains_occurrence, contains_scope, defines, imports,
		         occurrence, scope, file`)
	require.NoError(t, err)

	seed, err := os.ReadFile(filepath.Join("..", "deploy", "seed", "seed.sql"))
	require.NoError(t, err)
	// The file carries its own BEGIN/COMMIT and a temp table, so it goes over the
	// wire as one multi-statement simple query, exactly as psql would send it.
	_, err = pool.Exec(ctx, string(seed))
	require.NoError(t, err)

	seeded, base := derived(t), baseCounts(t)
	for _, table := range []string{"resolves_to", "imports", "calls", "implements", "type_defines"} {
		require.NotEmpty(t, seeded[table],
			"the seed must hand-write %s rows, or this proves nothing about them", table)
		t.Logf("%-13s seeded %d rows", table, len(seeded[table]))
	}

	require.NoError(t, RebuildAll(ctx, pool))
	assert.Equal(t, seeded, derived(t),
		"every derived row the seed hand-wrote must be derivable from the seed's own base rows")

	// Twice, because a rebuild that was a no-op only by luck of the first pass is
	// not a fixed point.
	require.NoError(t, RebuildAll(ctx, pool))
	assert.Equal(t, seeded, derived(t), "and stay derivable")
	assert.Equal(t, base, baseCounts(t), "a rebuild reads the base rows; it never writes them")
}

func counts(t *testing.T) map[string]int {
	t.Helper()
	return tableCounts(t, "resolves_to", "imports", "calls", "implements", "type_defines")
}

func baseCounts(t *testing.T) map[string]int {
	t.Helper()
	return tableCounts(t, "file", "scope", "occurrence", "defines",
		"contains_scope", "contains_occurrence", "references_local")
}

func tableCounts(t *testing.T, tables ...string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, table := range tables {
		var n int
		require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n))
		out[table] = n
	}
	return out
}
