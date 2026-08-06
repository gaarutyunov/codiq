// Package store is CodiQ's reduce phase: it writes one file's extracted facts
// into the core tables (SPEC.md §6).
//
// The whole package is one operation, ReplaceFile, and one idea: a file's rows
// are *replaced*, never merged. Delete everything the file owns, then COPY the
// new rows straight into the target tables — no staging table, no upsert, no
// ON CONFLICT, because delete-first means there is nothing left to collide
// with. That is what makes re-indexing a file safe to do at any time, and it is
// the property M4's batch reduce is built on top of.
//
// The one thing the schema does not give away for free is delete-by-file for
// *edges*. Vertex tables carry file_id; an edge table is exactly
// (source_id, target_id) — the SDL's edge form admits no third column — so an
// edge row cannot say which file owns it. Ownership is therefore recovered by
// joining through the endpoints, which is what the DeleteXxxByFile queries in
// store/sqlc do. See query.sql for the ownership rules that shape them.
//
// M2 scope: this is the monolithic loader (SPEC.md §14 M2). One transaction per
// file, driven directly by pgx. No DBOS, no batching, no protobuf, no disk —
// M3, M4 and M5 respectively. When DBOS arrives the transaction boundary widens
// from per-file to per-batch (§6's "reduce sequence, all within the one
// transaction"); nothing else here changes, because DB is an interface pgx.Tx
// already satisfies.
package store

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gaarutyunov/codiq/facts"
	"github.com/gaarutyunov/codiq/store/sqlc"
)

// DB is what the loader needs of a database handle: the ability to start a
// transaction. *pgxpool.Pool, *pgx.Conn and pgx.Tx all satisfy it.
//
// Taking an interface rather than a *pgxpool.Pool is what lets M3 nest the load
// inside DBOS's per-batch transaction without touching this package: pgx.Tx's
// Begin opens a savepoint, so ReplaceFile keeps its atomicity either way.
type DB interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// ErrParseFailed reports that the facts describe a file the extractor could not
// parse, so ReplaceFile wrote nothing.
//
// Loading them would be actively destructive: a FileFacts with a ParseError has
// empty row slices (facts.FileFacts), so replacing the file with it would
// delete a previously-good graph for the file and leave nothing behind. A
// transient parse failure must not cost a file its facts. Skipping instead
// leaves the last good load in place, which is also what SPEC.md §5 asks for —
// a poison file is flagged and skipped, never allowed to block the batch — so
// callers walking a repo should treat this error as a per-file skip and carry
// on rather than abandoning the walk.
var ErrParseFailed = errors.New("store: facts carry a parse error")

// ReplaceFile replaces everything the file owns with the given facts, in one
// transaction (SPEC.md §6).
//
// It is idempotent: calling it twice with the same facts leaves the database in
// the same state as calling it once — same rows, same row counts, and the same
// `file.id`, because the file row is resolved on its path rather than
// re-created. Row ids below the file are freshly generated on each load, since
// nothing outside the file may hold one (facts.FileFacts is self-contained), and
// cross-file edges that pointed into the file are the link pass's to restore.
func ReplaceFile(ctx context.Context, db DB, ff facts.FileFacts) error {
	if ff.ParseError != "" {
		return fmt.Errorf("%w (%s): %s", ErrParseFailed, ff.File.Path, ff.ParseError)
	}
	if ff.File.Path == "" {
		return errors.New("store: facts have no file path")
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin %s: %w", ff.File.Path, err)
	}
	// Rollback on any early return; a no-op once Commit has succeeded.
	defer func() { _ = tx.Rollback(ctx) }()

	q := sqlc.New(tx)

	// The link lock, shared, and the first statement of the transaction
	// (query.sql's banner, and link.RebuildAll's counterpart to it). This
	// function is the only thing that deletes occurrences, and a full re-link
	// inserting derived edges that point at them is the one concurrent writer it
	// cannot survive: the endpoint FKs are plain REFERENCES, so one of the two
	// transactions loses with SQLSTATE 23503. Shared mode is what keeps that
	// from costing anything -- two loads still run fully in parallel, and the
	// only transaction excluded is the exclusive one a full rebuild takes.
	//
	// Nested inside the batch reduce (index/reduce.go) this is a second
	// acquisition of a lock the enclosing transaction's session already holds,
	// which advisory locks count rather than conflict on. It is taken here as
	// well so that the standalone per-file path -- index.Run, and any direct
	// caller -- is covered by the same rule instead of by its caller's
	// diligence.
	if err := q.LockLinkShared(ctx); err != nil {
		return fmt.Errorf("store: lock %s: %w", ff.File.Path, err)
	}

	fileID, err := resolveFile(ctx, q, ff.File)
	if err != nil {
		return fmt.Errorf("store: resolve %s: %w", ff.File.Path, err)
	}

	rows, err := flatten(ff, fileID)
	if err != nil {
		return fmt.Errorf("store: %s: %w", ff.File.Path, err)
	}

	if err := deleteFile(ctx, q, fileID); err != nil {
		return fmt.Errorf("store: delete %s: %w", ff.File.Path, err)
	}
	if err := insertRows(ctx, q, rows); err != nil {
		return fmt.Errorf("store: load %s: %w", ff.File.Path, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit %s: %w", ff.File.Path, err)
	}
	return nil
}

// resolveFile returns the file row's uuid, creating the row if this
// (corpus, path) has never been loaded and refreshing its properties if it has.
//
// The id must survive a reload: `imports` endpoints are file ids, so minting a
// new one would invalidate every other file's import edges into this one, and
// `ReplaceFile` would be merely repeatable rather than idempotent. So the pair
// is the natural key and the uuid is a surrogate that is looked up, not
// regenerated.
//
// The pair, and not the path. One database holds many repositories, and
// `src/main.go` is a path most of them have — so resolving on the path alone
// would hand one repository's file the row of another's, and the delete-then-
// insert below would empty the first repository's file every time the second
// was indexed. That is the storage half of the corpus; the descriptor half is
// coord.Resolve's, and both are needed, because the link pass joins on
// descriptors rather than on file rows.
//
// The advisory lock closes the select-then-insert race. It is needed because
// the pair carries only btree indexes and not a unique constraint (the SDL
// declares no unique keys, and this package does not get to change the schema),
// so ON CONFLICT is unavailable. Keyed on the pair and held to the end of the
// transaction, it serializes only concurrent loads of the *same* file — which is
// the only case that can collide — and leaves M2's per-file goroutines running
// in parallel otherwise, including across corpora.
func resolveFile(ctx context.Context, q *sqlc.Queries, f facts.File) (uuid.UUID, error) {
	if err := q.LockFile(ctx, sqlc.LockFileParams{Corpus: f.Corpus, Path: f.Path}); err != nil {
		return uuid.Nil, fmt.Errorf("lock file: %w", err)
	}

	fileID, err := q.FileIDByCorpusPath(ctx, sqlc.FileIDByCorpusPathParams{Corpus: f.Corpus, Path: f.Path})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		fileID, err = q.InsertFile(ctx, sqlc.InsertFileParams{
			Corpus:     f.Corpus,
			Path:       f.Path,
			Lang:       f.Lang,
			PkgScheme:  f.Coord.Scheme,
			PkgManager: f.Coord.Manager,
			PkgName:    f.Coord.Name,
			PkgVersion: f.Coord.Version,
		})
		if err != nil {
			return uuid.Nil, fmt.Errorf("insert file: %w", err)
		}
		return fileID, nil
	case err != nil:
		return uuid.Nil, fmt.Errorf("lookup file: %w", err)
	}

	// The file was loaded before. Its coordinate can still have moved — a go.mod
	// version bump changes pkg_version for every file in the module — so the
	// properties are refreshed even though the id is kept. The corpus is not
	// among them: it is half of what selected this row, so it cannot have
	// changed without this being a different row.
	if err := q.UpdateFile(ctx, sqlc.UpdateFileParams{
		ID:         fileID,
		Lang:       f.Lang,
		PkgScheme:  f.Coord.Scheme,
		PkgManager: f.Coord.Manager,
		PkgName:    f.Coord.Name,
		PkgVersion: f.Coord.Version,
	}); err != nil {
		return uuid.Nil, fmt.Errorf("update file: %w", err)
	}
	return fileID, nil
}

// deleteFile removes everything the file owns.
//
// Order is edges before vertices. The endpoint FKs are plain REFERENCES with no
// ON DELETE CASCADE, so a surviving edge row would not be silently orphaned —
// it would refuse the vertex delete outright. Getting the order wrong is a
// foreign-key error, not a data bug, which is the good failure mode.
//
// Each edge table is cleared from both endpoint sides, in two statements rather
// than one with an OR: the source side is ownership and the target side is
// referential integrity, and — as the note in query.sql measures — the OR form
// would cost a sequential scan of the whole edge table per file. Only `imports`
// has a single side; its inbound edges stay valid because the file row's id
// survives.
//
// Clearing a table from both sides is also what made two files' loads deadlock,
// because it gave them opposite lock-acquisition orders over the same derived
// rows. Each of the four derived tables is therefore preceded by a Lock...ByFile
// that takes those rows in primary-key order first, so every transaction
// acquires in one global order and no cycle can form. query.sql's banner has the
// argument in full; the property this function has to preserve is that the table
// order below stays fixed, since ordering *within* a table only rules out a
// single-table cycle.
//
// The five derived tables are deleted here as well as by link.RebuildAll. They
// have to be: their rows reference this file's occurrences, so they block the
// occurrence delete. That is not a conflict with SPEC.md §7's ownership rule —
// ownership decides which file's re-link *recomputes* an edge, and in M2 the
// recompute is total.
func deleteFile(ctx context.Context, q *sqlc.Queries, fileID uuid.UUID) error {
	steps := []struct {
		name string
		// Not all of these delete: each derived table's rows are locked in a
		// deterministic order first. Same signature, same sequence, one loop.
		run func(context.Context, uuid.UUID) error
	}{
		// Derived cross-file edges: occurrence <-> occurrence. Lock before
		// delete, and never reorder a table ahead of one already locked.
		{"resolves_to (lock)", q.LockResolvesToByFile},
		{"resolves_to (source)", q.DeleteResolvesToBySourceFile},
		{"resolves_to (target)", q.DeleteResolvesToByTargetFile},
		{"calls (lock)", q.LockCallsByFile},
		{"calls (source)", q.DeleteCallsBySourceFile},
		{"calls (target)", q.DeleteCallsByTargetFile},
		{"implements (lock)", q.LockImplementsByFile},
		{"implements (source)", q.DeleteImplementsBySourceFile},
		{"implements (target)", q.DeleteImplementsByTargetFile},
		{"type_defines (lock)", q.LockTypeDefinesByFile},
		{"type_defines (source)", q.DeleteTypeDefinesBySourceFile},
		{"type_defines (target)", q.DeleteTypeDefinesByTargetFile},
		// Extracted intra-file edges, innermost endpoints first. No lock step:
		// flatten resolves both endpoints out of one file's own facts, so two
		// files' row sets here are disjoint and cannot contend at all.
		{"references_local (source)", q.DeleteReferencesLocalBySourceFile},
		{"references_local (target)", q.DeleteReferencesLocalByTargetFile},
		{"contains_occurrence (source)", q.DeleteContainsOccurrenceBySourceFile},
		{"contains_occurrence (target)", q.DeleteContainsOccurrenceByTargetFile},
		{"contains_scope (source)", q.DeleteContainsScopeBySourceFile},
		{"contains_scope (target)", q.DeleteContainsScopeByTargetFile},
		{"defines (source)", q.DeleteDefinesBySourceFile},
		{"defines (target)", q.DeleteDefinesByTargetFile},
		{"imports", q.DeleteImportsByFile},
		// Vertices last, now that nothing references them.
		{"occurrence", q.DeleteOccurrencesByFile},
		{"scope", q.DeleteScopesByFile},
	}
	for _, s := range steps {
		if err := s.run(ctx, fileID); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	return nil
}

// insertRows COPYs the file's rows into the target tables (SPEC.md §6).
//
// Load order is the mirror of deleteFile: vertices before edges, and scopes
// before occurrences. The vertex-before-edge half is enforced by real foreign
// keys. The scope-before-occurrence half is not — `occurrence.scope_id` and
// `scope.parent_scope_id` carry no FK in the shipped schema — but it is the
// order those FKs would demand, so declaring them later needs no change here.
//
// `CopyFrom` covers every table but one. The `file` row is written by a plain
// INSERT/UPDATE instead, and cannot be a COPY: it is a single row, and it needs
// its generated id back (RETURNING) or an existing id refreshed, neither of
// which the COPY protocol can do. Every other table takes many rows per file
// and needs nothing back, which is exactly what binary COPY is for.
func insertRows(ctx context.Context, q *sqlc.Queries, r *fileRows) error {
	// Empty slices are skipped rather than COPYed: a file with no scopes or no
	// local references is ordinary, and an empty COPY is a round trip that buys
	// nothing.
	if len(r.scopes) > 0 {
		if _, err := q.CopyScopes(ctx, r.scopes); err != nil {
			return fmt.Errorf("scope: %w", err)
		}
	}
	if len(r.occurrences) > 0 {
		if _, err := q.CopyOccurrences(ctx, r.occurrences); err != nil {
			return fmt.Errorf("occurrence: %w", err)
		}
	}
	if len(r.defines) > 0 {
		if _, err := q.CopyDefines(ctx, r.defines); err != nil {
			return fmt.Errorf("defines: %w", err)
		}
	}
	if len(r.containsScope) > 0 {
		if _, err := q.CopyContainsScope(ctx, r.containsScope); err != nil {
			return fmt.Errorf("contains_scope: %w", err)
		}
	}
	if len(r.containsOccurrence) > 0 {
		if _, err := q.CopyContainsOccurrence(ctx, r.containsOccurrence); err != nil {
			return fmt.Errorf("contains_occurrence: %w", err)
		}
	}
	if len(r.referencesLocal) > 0 {
		if _, err := q.CopyReferencesLocal(ctx, r.referencesLocal); err != nil {
			return fmt.Errorf("references_local: %w", err)
		}
	}
	return nil
}

// fileRows is one FileFacts flattened into the exact argument shapes the
// generated COPY helpers take: local ids resolved to uuids, and containment
// routed to whichever of the two `contains` tables its target belongs in.
type fileRows struct {
	scopes             []sqlc.CopyScopesParams
	occurrences        []sqlc.CopyOccurrencesParams
	defines            []sqlc.CopyDefinesParams
	containsScope      []sqlc.CopyContainsScopeParams
	containsOccurrence []sqlc.CopyContainsOccurrenceParams
	referencesLocal    []sqlc.CopyReferencesLocalParams
}

// flatten converts facts to rows, minting a uuid for every scope and occurrence
// and translating the facts' file-local ids through it.
//
// Local ids are an extractor-private numbering (facts.LocalID), so the mapping
// is built and discarded inside this one load; nothing outside the file may hold
// a row id, which is precisely why regenerating them on every load is safe.
//
// Every failure mode here is a defect in the facts, not in the database, so
// each is reported as an error naming the offending edge rather than left to
// surface later as an opaque foreign-key or NOT NULL violation from inside a
// COPY.
func flatten(ff facts.FileFacts, fileID uuid.UUID) (*fileRows, error) {
	scopeIDs := make(map[facts.LocalID]uuid.UUID, len(ff.Scopes))
	for _, s := range ff.Scopes {
		if s.ID == facts.NoID {
			return nil, fmt.Errorf("scope at [%d,%d) has no local id", s.RangeStart, s.RangeEnd)
		}
		if _, dup := scopeIDs[s.ID]; dup {
			return nil, fmt.Errorf("scope local id %d declared twice", s.ID)
		}
		scopeIDs[s.ID] = uuid.New()
	}
	occurrenceIDs := make(map[facts.LocalID]uuid.UUID, len(ff.Occurrences))
	for _, o := range ff.Occurrences {
		if o.ID == facts.NoID {
			return nil, fmt.Errorf("occurrence %q has no local id", o.Name)
		}
		if _, dup := occurrenceIDs[o.ID]; dup {
			return nil, fmt.Errorf("occurrence local id %d declared twice", o.ID)
		}
		occurrenceIDs[o.ID] = uuid.New()
	}

	r := &fileRows{
		scopes:      make([]sqlc.CopyScopesParams, 0, len(ff.Scopes)),
		occurrences: make([]sqlc.CopyOccurrencesParams, 0, len(ff.Occurrences)),
	}

	for _, s := range ff.Scopes {
		start, end, err := span(s.RangeStart, s.RangeEnd)
		if err != nil {
			return nil, fmt.Errorf("scope %d: %w", s.ID, err)
		}
		parent, err := optional(scopeIDs, s.Parent, "scope")
		if err != nil {
			return nil, fmt.Errorf("scope %d parent: %w", s.ID, err)
		}
		r.scopes = append(r.scopes, sqlc.CopyScopesParams{
			ID:            scopeIDs[s.ID],
			FileID:        fileID,
			Kind:          s.Kind,
			RangeStart:    start,
			RangeEnd:      end,
			ParentScopeID: parent,
		})
	}

	for _, o := range ff.Occurrences {
		start, end, err := span(o.RangeStart, o.RangeEnd)
		if err != nil {
			return nil, fmt.Errorf("occurrence %d (%s): %w", o.ID, o.Name, err)
		}
		scope, err := optional(scopeIDs, o.Scope, "scope")
		if err != nil {
			return nil, fmt.Errorf("occurrence %d (%s) scope: %w", o.ID, o.Name, err)
		}
		r.occurrences = append(r.occurrences, sqlc.CopyOccurrencesParams{
			ID:         occurrenceIDs[o.ID],
			FileID:     fileID,
			Descriptor: o.Descriptor.String(),
			Role:       string(o.Role),
			SymbolKind: o.SymbolKind,
			Name:       o.Name,
			RangeStart: start,
			RangeEnd:   end,
			ScopeID:    scope,
		})
	}

	// Edge tables are keyed on (source_id, target_id), so an edge is a set
	// member: emitting it twice states the same fact twice. Deduplicating keeps
	// a repeated fact from failing the file's entire COPY on a primary-key
	// violation. This is not the ON CONFLICT that SPEC.md §6 rules out — that
	// would be reconciling against rows already in the table; this normalizes
	// the input before it gets there.
	seen := make(map[[2]uuid.UUID]bool, len(ff.Edges))
	for i, e := range ff.Edges {
		source, target, err := endpoints(e, fileID, scopeIDs, occurrenceIDs)
		if err != nil {
			return nil, fmt.Errorf("edge %d (%s): %w", i, e.Kind, err)
		}
		if seen[[2]uuid.UUID{source, target}] {
			continue
		}
		seen[[2]uuid.UUID{source, target}] = true

		switch {
		case e.Kind == facts.EdgeDefines:
			r.defines = append(r.defines, sqlc.CopyDefinesParams{SourceID: source, TargetID: target})
		case e.Kind == facts.EdgeReferencesLocal:
			r.referencesLocal = append(r.referencesLocal, sqlc.CopyReferencesLocalParams{SourceID: source, TargetID: target})
		// `contains` is one graph label over two physical tables, so the target's
		// vertex kind — not the edge kind — picks the table.
		case e.Kind == facts.EdgeContains && e.Target.Vertex == facts.VertexScope:
			r.containsScope = append(r.containsScope, sqlc.CopyContainsScopeParams{SourceID: source, TargetID: target})
		case e.Kind == facts.EdgeContains && e.Target.Vertex == facts.VertexOccurrence:
			r.containsOccurrence = append(r.containsOccurrence, sqlc.CopyContainsOccurrenceParams{SourceID: source, TargetID: target})
		default:
			return nil, fmt.Errorf("edge %d: no table for kind %q into %q", i, e.Kind, e.Target.Vertex)
		}
	}

	return r, nil
}

// endpoints resolves an edge's two Refs to uuids, checking that each names the
// vertex table its edge table actually references.
func endpoints(
	e facts.Edge,
	fileID uuid.UUID,
	scopeIDs, occurrenceIDs map[facts.LocalID]uuid.UUID,
) (source, target uuid.UUID, err error) {
	// Which vertex table each edge kind's endpoints live in
	// (schema/migrations/0001_core_tables.sql).
	var want [2]facts.VertexKind
	switch e.Kind {
	case facts.EdgeDefines:
		want = [2]facts.VertexKind{facts.VertexFile, facts.VertexOccurrence}
	case facts.EdgeContains:
		// Target is scope or occurrence; the switch in flatten discriminates.
		want = [2]facts.VertexKind{facts.VertexScope, e.Target.Vertex}
	case facts.EdgeReferencesLocal:
		want = [2]facts.VertexKind{facts.VertexOccurrence, facts.VertexOccurrence}
	default:
		return uuid.Nil, uuid.Nil, fmt.Errorf("unknown edge kind %q", e.Kind)
	}

	source, err = resolveRef(e.Source, want[0], fileID, scopeIDs, occurrenceIDs)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("source: %w", err)
	}
	target, err = resolveRef(e.Target, want[1], fileID, scopeIDs, occurrenceIDs)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("target: %w", err)
	}
	return source, target, nil
}

func resolveRef(
	ref facts.Ref,
	want facts.VertexKind,
	fileID uuid.UUID,
	scopeIDs, occurrenceIDs map[facts.LocalID]uuid.UUID,
) (uuid.UUID, error) {
	if ref.Vertex != want {
		return uuid.Nil, fmt.Errorf("expected a %s endpoint, got %q", want, ref.Vertex)
	}
	switch ref.Vertex {
	case facts.VertexFile:
		// A FileFacts describes exactly one file, so a file Ref carries no id.
		return fileID, nil
	case facts.VertexScope:
		id, ok := scopeIDs[ref.ID]
		if !ok {
			return uuid.Nil, fmt.Errorf("no scope with local id %d", ref.ID)
		}
		return id, nil
	case facts.VertexOccurrence:
		id, ok := occurrenceIDs[ref.ID]
		if !ok {
			return uuid.Nil, fmt.Errorf("no occurrence with local id %d", ref.ID)
		}
		return id, nil
	default:
		return uuid.Nil, fmt.Errorf("unknown vertex kind %q", ref.Vertex)
	}
}

// optional resolves a LocalID that is allowed to be absent — an unset parent
// scope or an occurrence at the file's top level — into a nullable column.
func optional(ids map[facts.LocalID]uuid.UUID, id facts.LocalID, kind string) (*uuid.UUID, error) {
	if id == facts.NoID {
		return nil, nil
	}
	resolved, ok := ids[id]
	if !ok {
		return nil, fmt.Errorf("no %s with local id %d", kind, id)
	}
	return &resolved, nil
}

// span narrows a facts byte offset pair to the schema's integer columns.
//
// The conversion is checked rather than assumed: an int32 cast of a bad offset
// would wrap into a plausible-looking row, and a range that silently moves is
// worse than a file that refuses to load. Half-open, so start == end is a legal
// empty range.
func span(start, end int) (int32, int32, error) {
	if start < 0 || end < 0 {
		return 0, 0, fmt.Errorf("negative range [%d,%d)", start, end)
	}
	if start > math.MaxInt32 || end > math.MaxInt32 {
		return 0, 0, fmt.Errorf("range [%d,%d) exceeds the int32 offset columns", start, end)
	}
	if end < start {
		return 0, 0, fmt.Errorf("inverted range [%d,%d)", start, end)
	}
	return int32(start), int32(end), nil
}
