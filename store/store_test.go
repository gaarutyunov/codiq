package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/sync/errgroup"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/facts"
)

// The loader is tested against a real postgres:19beta2 running the real
// committed migrations (SPEC.md §13: no shims, no in-memory fake). Delete-by-file
// for edges is a join through the endpoints and the file row's identity rests on
// an advisory lock — neither of which a mock would exercise at all.

// One container for the package; each test gets its own schema-fresh state by
// truncating, which is far cheaper than a container per test.
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

// applyMigrations runs the committed goose migrations in order — the same DDL
// gopgql emits from the SDL and the compose deploys, so the tables under test
// are the shipped tables and not a copy that can drift.
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
		// Only the Up section; the Down section's DROPs are goose's business.
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

func reset(t *testing.T) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		TRUNCATE calls, implements, type_defines, resolves_to, references_local,
		         contains_occurrence, contains_scope, defines, imports,
		         occurrence, scope, file`)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Fixtures
//
// A two-file Go package, shaped exactly like deploy/seed/seed.sql's corpus so
// the loader is fed the descriptors §4.3 actually specifies rather than
// placeholders. iface.go declares an interface; store.go declares the type that
// satisfies it and holds the one same-file reference pair, so every extracted
// edge table gets rows.
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

// ifaceFacts is internal/graph/iface.go: `type Storer interface { Put(...) }`.
func ifaceFacts() facts.FileFacts {
	return facts.FileFacts{
		File: facts.File{Path: "internal/graph/iface.go", Lang: "go", Coord: testCoord},
		Scopes: []facts.Scope{
			{ID: 1, Kind: facts.ScopeFile, RangeStart: 0, RangeEnd: 190},
		},
		Occurrences: []facts.Occurrence{
			{ID: 1, Descriptor: desc("internal/graph/Storer#"), Role: facts.RoleDefinition,
				SymbolKind: facts.KindInterface, Name: "Storer", RangeStart: 45, RangeEnd: 51, Scope: 1},
			{ID: 2, Descriptor: desc("internal/graph/Storer#Put()."), Role: facts.RoleDefinition,
				SymbolKind: facts.KindMethod, Name: "Put", RangeStart: 72, RangeEnd: 75, Scope: 1},
		},
		Edges: []facts.Edge{
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(1)},
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(2)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(1), Target: facts.OccurrenceRef(1)},
			{Kind: facts.EdgeContains, Source: facts.ScopeRef(1), Target: facts.OccurrenceRef(2)},
		},
	}
}

// storeFacts is internal/graph/store.go: the Store type, its db field, its Put
// method, the receiver's type reference and the `s.db` read. The last two are
// same-file references, resolved at extraction (§4.3), so this file is the one
// that exercises references_local and the nested-scope containment.
func storeFacts() facts.FileFacts {
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
		},
		Edges: []facts.Edge{
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(1)},
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(2)},
			{Kind: facts.EdgeDefines, Source: facts.FileRef(), Target: facts.OccurrenceRef(4)},
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

// ---------------------------------------------------------------------------
// Snapshots
//
// Every assertion below compares *id-free* projections. Row ids are freshly
// generated on each load by design, so comparing them would only ever prove
// that uuid.New works; what has to hold is that the descriptors, ranges and
// edge endpoints come back identical. Each edge is rendered through its
// endpoints' natural keys, which is also the only way to see that an edge
// still points where it did.
// ---------------------------------------------------------------------------

const (
	dumpFiles = `SELECT format('%s|%s|%s|%s', path, lang, pkg_name, pkg_version) FROM file`

	dumpScopes = `SELECT format('%s|%s|[%s,%s)|parent=%s', f.path, s.kind, s.range_start, s.range_end,
	                     coalesce(p.kind || '@' || p.range_start, '-'))
	              FROM scope s JOIN file f ON f.id = s.file_id
	              LEFT JOIN scope p ON p.id = s.parent_scope_id`

	dumpOccurrences = `SELECT format('%s|%s|%s|%s|[%s,%s)|scope=%s', f.path, o.descriptor, o.role, o.name,
	                          o.range_start, o.range_end, coalesce(s.kind || '@' || s.range_start, '-'))
	                   FROM occurrence o JOIN file f ON f.id = o.file_id
	                   LEFT JOIN scope s ON s.id = o.scope_id`

	dumpDefines = `SELECT format('%s -> %s@%s', f.path, o.descriptor, o.range_start)
	               FROM defines d JOIN file f ON f.id = d.source_id
	               JOIN occurrence o ON o.id = d.target_id`

	dumpContainsScope = `SELECT format('%s|%s@%s -> %s@%s', f.path, a.kind, a.range_start, b.kind, b.range_start)
	                     FROM contains_scope c JOIN scope a ON a.id = c.source_id
	                     JOIN scope b ON b.id = c.target_id JOIN file f ON f.id = a.file_id`

	dumpContainsOccurrence = `SELECT format('%s|%s@%s -> %s@%s', f.path, s.kind, s.range_start, o.descriptor, o.range_start)
	                          FROM contains_occurrence c JOIN scope s ON s.id = c.source_id
	                          JOIN occurrence o ON o.id = c.target_id JOIN file f ON f.id = s.file_id`

	dumpReferencesLocal = `SELECT format('%s|%s@%s -> %s@%s', f.path, a.descriptor, a.range_start, b.descriptor, b.range_start)
	                       FROM references_local r JOIN occurrence a ON a.id = r.source_id
	                       JOIN occurrence b ON b.id = r.target_id JOIN file f ON f.id = a.file_id`

	dumpResolvesTo = `SELECT format('%s@%s -> %s@%s', sf.path, a.range_start, tf.path, b.range_start)
	                  FROM resolves_to r JOIN occurrence a ON a.id = r.source_id
	                  JOIN occurrence b ON b.id = r.target_id
	                  JOIN file sf ON sf.id = a.file_id JOIN file tf ON tf.id = b.file_id`

	dumpImports = `SELECT format('%s -> %s', sf.path, tf.path) FROM imports i
	               JOIN file sf ON sf.id = i.source_id JOIN file tf ON tf.id = i.target_id`
)

// snapshot renders the whole graph as sorted, id-free lines per table.
func snapshot(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for table, query := range map[string]string{
		"file":                dumpFiles,
		"scope":               dumpScopes,
		"occurrence":          dumpOccurrences,
		"defines":             dumpDefines,
		"contains_scope":      dumpContainsScope,
		"contains_occurrence": dumpContainsOccurrence,
		"references_local":    dumpReferencesLocal,
		"resolves_to":         dumpResolvesTo,
		"imports":             dumpImports,
	} {
		out[table] = lines(t, query)
	}
	return out
}

func lines(t *testing.T, query string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), query)
	require.NoError(t, err)
	defer rows.Close()
	var got []string
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		got = append(got, line)
	}
	require.NoError(t, rows.Err())
	sort.Strings(got)
	return got
}

func count(t *testing.T, table string) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n))
	return n
}

func fileID(t *testing.T, path string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT id FROM file WHERE path = $1", path).Scan(&id))
	return id
}

// ---------------------------------------------------------------------------
// Proof 1 — ReplaceFile inserts a file's facts.
// ---------------------------------------------------------------------------

func TestReplaceFileInsertsFacts(t *testing.T) {
	reset(t)
	ctx := context.Background()

	require.NoError(t, ReplaceFile(ctx, pool, storeFacts()))

	for _, table := range []string{"file", "scope", "occurrence", "defines",
		"contains_scope", "contains_occurrence", "references_local"} {
		t.Logf("%-20s %d rows", table, count(t, table))
	}

	assert.Equal(t, []string{
		"internal/graph/store.go|go|github.com/gaarutyunov/codiq|v0.1.0",
	}, lines(t, dumpFiles))

	assert.Equal(t, []string{
		"internal/graph/store.go|file|[0,420)|parent=-",
		"internal/graph/store.go|function|[210,415)|parent=file@0",
	}, lines(t, dumpScopes), "scopes land with their parent link")

	assert.Equal(t, []string{
		"internal/graph/store.go|scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Store#Put().|definition|Put|[232,235)|scope=file@0",
		"internal/graph/store.go|scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Store#db.|definition|db|[66,68)|scope=file@0",
		"internal/graph/store.go|scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Store#db.|reference|db|[300,302)|scope=function@210",
		"internal/graph/store.go|scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Store#|definition|Store|[45,50)|scope=file@0",
		"internal/graph/store.go|scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Store#|reference|Store|[222,227)|scope=file@0",
	}, lines(t, dumpOccurrences), "definitions and references land as occurrences with roles")

	// contains routed across both physical tables from one facts.EdgeContains.
	assert.Equal(t, []string{
		"internal/graph/store.go|file@0 -> function@210",
	}, lines(t, dumpContainsScope))
	assert.Len(t, lines(t, dumpContainsOccurrence), 5)

	assert.Equal(t, []string{
		"internal/graph/store.go|scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Store#@222 -> scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Store#@45",
		"internal/graph/store.go|scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Store#db.@300 -> scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Store#db.@66",
	}, lines(t, dumpReferencesLocal), "same-file references resolved at extraction")

	assert.Len(t, lines(t, dumpDefines), 3)
}

// ---------------------------------------------------------------------------
// Proof 2 — ReplaceFile twice on the same file is idempotent.
//
// This is the property M4's reduce phase rests on: a re-run of a batch must not
// double a file's rows, so the loader has to be safe to repeat.
// ---------------------------------------------------------------------------

func TestReplaceFileIsIdempotent(t *testing.T) {
	reset(t)
	ctx := context.Background()

	require.NoError(t, ReplaceFile(ctx, pool, ifaceFacts()))
	require.NoError(t, ReplaceFile(ctx, pool, storeFacts()))
	first := snapshot(t)
	firstID := fileID(t, "internal/graph/store.go")

	// Reload both files, twice more, in the reverse order for good measure.
	require.NoError(t, ReplaceFile(ctx, pool, storeFacts()))
	require.NoError(t, ReplaceFile(ctx, pool, ifaceFacts()))
	require.NoError(t, ReplaceFile(ctx, pool, storeFacts()))

	assert.Equal(t, first, snapshot(t), "reloading a file must not change the graph")
	assert.Equal(t, firstID, fileID(t, "internal/graph/store.go"),
		"file.id is a surrogate over the path and must survive a reload, or every imports edge into this file dies with it")

	// The snapshots are sets, so state the row counts too — a duplicated row
	// would otherwise collapse into an equal set.
	for table, want := range map[string]int{
		"file": 2, "scope": 3, "occurrence": 7,
		"defines": 5, "contains_scope": 1, "contains_occurrence": 7,
		"references_local": 2,
	} {
		got := count(t, table)
		t.Logf("%-20s %d rows after 1 load, %d after 4 (want %d)", table, want, got, want)
		assert.Equal(t, want, got, "row count for %s", table)
	}
}

// ---------------------------------------------------------------------------
// Proof 3 — delete-by-file removes the file's edges too, via the endpoint join,
// and leaves every other file's rows untouched.
//
// The interesting case is the edges, because an edge table is exactly
// (source_id, target_id) and cannot say which file owns a row. Cross-file edges
// are inserted here directly rather than through link.RebuildAll: what is under
// test is the store's deletion reach, and hand-writing the edges pins the
// endpoints the test then looks for.
// ---------------------------------------------------------------------------

func TestReplaceFileDeletesEdgesByEndpointJoin(t *testing.T) {
	reset(t)
	ctx := context.Background()

	require.NoError(t, ReplaceFile(ctx, pool, ifaceFacts()))
	require.NoError(t, ReplaceFile(ctx, pool, storeFacts()))

	ifaceID := fileID(t, "internal/graph/iface.go")
	storeID := fileID(t, "internal/graph/store.go")

	// Outbound: store.go's Store implements iface.go's Storer.
	// Inbound: iface.go's Storer.Put is type-defined by store.go's Store.
	// Both directions matter: outbound is what §7's ownership rule says store.go
	// owns, inbound is what the FKs force it to clear anyway.
	mustExec(t, `
		INSERT INTO implements (source_id, target_id)
		SELECT s.id, i.id FROM occurrence s, occurrence i
		WHERE s.file_id = $1 AND s.descriptor LIKE '%Store#' AND s.role = 'definition'
		  AND i.file_id = $2 AND i.descriptor LIKE '%Storer#' AND i.role = 'definition'`, storeID, ifaceID)
	mustExec(t, `
		INSERT INTO type_defines (source_id, target_id)
		SELECT i.id, s.id FROM occurrence i, occurrence s
		WHERE i.file_id = $1 AND i.descriptor LIKE '%Storer#Put().' AND i.role = 'definition'
		  AND s.file_id = $2 AND s.descriptor LIKE '%Store#' AND s.role = 'definition'`, ifaceID, storeID)
	mustExec(t, `INSERT INTO imports (source_id, target_id) VALUES ($1, $2), ($2, $1)`, storeID, ifaceID)

	require.Equal(t, 1, count(t, "implements"))
	require.Equal(t, 1, count(t, "type_defines"))
	require.Equal(t, 2, count(t, "imports"))
	t.Logf("before: implements=%d type_defines=%d imports=%d references_local=%d contains_scope=%d occurrence=%d scope=%d defines=%d",
		count(t, "implements"), count(t, "type_defines"), count(t, "imports"),
		count(t, "references_local"), count(t, "contains_scope"),
		count(t, "occurrence"), count(t, "scope"), count(t, "defines"))

	// Now replace store.go with facts that declare nothing at all. Every row it
	// owns must go, including the two occurrence-level cross-file edges — one of
	// which store.go is only the *target* of.
	empty := facts.FileFacts{File: facts.File{Path: "internal/graph/store.go", Lang: "go", Coord: testCoord}}
	require.NoError(t, ReplaceFile(ctx, pool, empty))

	t.Logf("after:  implements=%d type_defines=%d imports=%d references_local=%d contains_scope=%d occurrence=%d scope=%d defines=%d",
		count(t, "implements"), count(t, "type_defines"), count(t, "imports"),
		count(t, "references_local"), count(t, "contains_scope"),
		count(t, "occurrence"), count(t, "scope"), count(t, "defines"))

	assert.Zero(t, count(t, "implements"), "outbound cross-file edge deleted with its owning file")
	assert.Zero(t, count(t, "type_defines"),
		"inbound cross-file edge deleted too: its target occurrences are gone, and the FK is not ON DELETE CASCADE")
	assert.Zero(t, count(t, "references_local"))
	assert.Zero(t, count(t, "contains_scope"))

	// iface.go is untouched, and store.go's file row survives with its id.
	assert.Equal(t, []string{
		"internal/graph/iface.go|go|github.com/gaarutyunov/codiq|v0.1.0",
		"internal/graph/store.go|go|github.com/gaarutyunov/codiq|v0.1.0",
	}, lines(t, dumpFiles))
	assert.Equal(t, storeID, fileID(t, "internal/graph/store.go"))
	assert.Equal(t, 2, count(t, "occurrence"), "only iface.go's two definitions remain")
	assert.Equal(t, 1, count(t, "scope"), "only iface.go's file scope remains")
	assert.Equal(t, 2, count(t, "defines"), "iface.go still defines its two symbols")

	// imports is file -> file and owned by the referencing file only, so
	// store.go's outbound import went and iface.go's inbound one stayed: the
	// file row's id survived a ReplaceFile, so that edge still points somewhere
	// real (which is exactly why the id has to be stable).
	assert.Equal(t, []string{"internal/graph/iface.go -> internal/graph/store.go"}, lines(t, dumpImports))
}

// ---------------------------------------------------------------------------
// Failure modes: a defect in the facts must be reported, not written.
// ---------------------------------------------------------------------------

func TestReplaceFileRejectsBadFacts(t *testing.T) {
	reset(t)
	ctx := context.Background()

	t.Run("a parse error is a skip, not an erasure", func(t *testing.T) {
		require.NoError(t, ReplaceFile(ctx, pool, storeFacts()))
		before := snapshot(t)

		broken := facts.FileFacts{
			File:       facts.File{Path: "internal/graph/store.go", Lang: "go", Coord: testCoord},
			ParseError: "unexpected token at 41",
		}
		err := ReplaceFile(ctx, pool, broken)
		require.ErrorIs(t, err, ErrParseFailed)
		assert.Equal(t, before, snapshot(t), "the last good load must survive a failed parse")
	})

	t.Run("an edge into a nonexistent local id is rejected", func(t *testing.T) {
		ff := ifaceFacts()
		ff.Edges = append(ff.Edges, facts.Edge{
			Kind: facts.EdgeReferencesLocal, Source: facts.OccurrenceRef(1), Target: facts.OccurrenceRef(99),
		})
		err := ReplaceFile(ctx, pool, ff)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no occurrence with local id 99")
	})

	t.Run("an edge with the wrong endpoint table is rejected", func(t *testing.T) {
		ff := ifaceFacts()
		ff.Edges = append(ff.Edges, facts.Edge{
			Kind: facts.EdgeReferencesLocal, Source: facts.ScopeRef(1), Target: facts.OccurrenceRef(1),
		})
		err := ReplaceFile(ctx, pool, ff)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected a occurrence endpoint, got \"scope\"")
	})

	t.Run("a repeated edge is a repeated fact, not a key violation", func(t *testing.T) {
		reset(t)
		ff := ifaceFacts()
		ff.Edges = append(ff.Edges, ff.Edges[0])
		require.NoError(t, ReplaceFile(ctx, pool, ff))
		assert.Equal(t, 2, count(t, "defines"))
	})

	t.Run("an inverted range is rejected", func(t *testing.T) {
		ff := ifaceFacts()
		ff.Occurrences[0].RangeStart, ff.Occurrences[0].RangeEnd = 51, 45
		err := ReplaceFile(ctx, pool, ff)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "inverted range")
	})
}

// ---------------------------------------------------------------------------
// The advisory lock: SPEC.md §14 M2 loads files on goroutines, and `file.path`
// carries no unique constraint, so concurrent loads of the same path are the one
// case that could duplicate a file row. Two concurrent loads of the same file
// should not happen in a well-formed walk, but "should not" is not a guarantee
// the loader gets to rely on.
// ---------------------------------------------------------------------------

func TestReplaceFileIsSafeUnderConcurrentLoads(t *testing.T) {
	reset(t)
	ctx := context.Background()

	var g errgroup.Group
	for i := 0; i < 8; i++ {
		g.Go(func() error { return ReplaceFile(ctx, pool, storeFacts()) })
		g.Go(func() error { return ReplaceFile(ctx, pool, ifaceFacts()) })
	}
	require.NoError(t, g.Wait())

	assert.Equal(t, 2, count(t, "file"), "one row per path, however many loads raced for it")
	assert.Equal(t, 7, count(t, "occurrence"))
	assert.Equal(t, 5, count(t, "defines"))
}

func mustExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	_, err := pool.Exec(context.Background(), sql, args...)
	require.NoError(t, err)
}
