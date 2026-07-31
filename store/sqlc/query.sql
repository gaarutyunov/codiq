-- CodiQ loader + linker queries (SPEC.md §6 reduce phase, §7 link pass).
--
-- Two groups: `store` uses the file-scoped queries (advisory lock, file upsert,
-- delete-by-file, `:copyfrom`); `link` uses the RebuildAll group at the bottom.
-- They share one generated package because they share one catalog.


-- ===========================================================================
-- File identity
--
-- `file.path` is the natural key and `file.id` is a surrogate, so the loader
-- resolves on path and *reuses* the existing id. Keeping the id stable across
-- re-indexes is what stops re-indexing one file from invalidating every other
-- file's `imports` edges (whose endpoints are file ids), and it is what makes
-- `ReplaceFile` idempotent rather than merely repeatable.
--
-- There is no unique constraint on `path` (only the btree index the SDL
-- declares), so `ON CONFLICT` is unavailable and the resolve is a
-- select-then-insert. The advisory lock below makes that race-free without a
-- schema change: it is keyed on the path, held to the end of the enclosing
-- transaction, and so serializes only concurrent loads of the *same* file --
-- exactly the collision select-then-insert has -- while leaving loads of
-- different files fully parallel (SPEC.md §14 M2 loads files on goroutines).
-- ===========================================================================

-- name: LockFilePath :exec
SELECT pg_advisory_xact_lock(hashtextextended(@path::text, 0));

-- name: FileIDByPath :one
SELECT id FROM file WHERE path = @path;

-- name: InsertFile :one
INSERT INTO file (path, lang, pkg_scheme, pkg_manager, pkg_name, pkg_version)
VALUES (@path, @lang, @pkg_scheme, @pkg_manager, @pkg_name, @pkg_version)
RETURNING id;

-- name: UpdateFile :exec
UPDATE file
SET lang = @lang,
    pkg_scheme = @pkg_scheme,
    pkg_manager = @pkg_manager,
    pkg_name = @pkg_name,
    pkg_version = @pkg_version
WHERE id = @id;


-- ===========================================================================
-- Delete-by-file (SPEC.md §6: "DELETE FROM <table> WHERE file_id = $1")
--
-- Only the three vertex tables actually have a `file_id` column. An edge table
-- is exactly `(source_id, target_id)` -- the SDL's edge form admits no extra
-- columns -- so for edges "by file" is a join through the endpoints, not a
-- predicate on the edge table at all.
--
-- Each edge table is therefore cleared from *both* endpoint sides, and by two
-- separate statements rather than one with an OR. The split is not cosmetic. A
-- single `source_id IN (...) OR target_id IN (...)` gives PostgreSQL no usable
-- index -- it hashes both sub-selects and sequentially scans the whole edge
-- table -- so a per-file delete would cost O(all edges in the corpus) instead of
-- O(this file's edges). Measured on 200k occurrences / 195k `calls` rows,
-- deleting one file's 40 occurrences' worth: the OR form seq-scans 195,000 rows
-- in 37 ms; the two split statements use `calls_pkey` and `calls_target_idx` and
-- take 0.6 ms and 0.5 ms. Multiplied by every file in a repo, that is the
-- difference between a loader that finishes and one that does not.
--
-- The two sides also mean different things, which the split names say out loud:
--
--   ...BySourceFile is *ownership* (SPEC.md §7): an intra-file edge is owned by
--   the file its endpoints share, a cross-file edge by its referencing file,
--   i.e. by source_id's file_id.
--
--   ...ByTargetFile is *referential integrity*: the endpoint FKs are not
--   ON DELETE CASCADE, so an inbound edge from another file does not dangle --
--   it refuses the occurrence delete outright. Such an edge is invalid
--   regardless, since the occurrence ids it points at are about to be replaced,
--   and the full link rebuild (§7, and §14 M2's only link mode) restores it.
--
-- Those two properties together are also what made concurrent re-indexing
-- deadlock, which is what the Lock...ByFile queries below exist to stop. See
-- their banner.
-- ===========================================================================


-- ===========================================================================
-- Ordered lock acquisition for the derived edge tables
--
-- The split delete above is correct per file and was a lock cycle across files.
-- A derived row (a -> b) with a in file A and b in file B is reached by A's
-- *source*-side delete and by B's *target*-side delete; the mirror row (b -> a)
-- is reached by B's source side and A's target side. Both transactions run
-- source-side first, so A takes (a -> b) then reaches for (b -> a) while B takes
-- (b -> a) then reaches for (a -> b) -- opposite acquisition order over the same
-- two rows, i.e. SQLSTATE 40P01 deadlock_detected, and PostgreSQL picks a
-- victim. It cannot happen on a first index, because there are no derived rows to
-- delete yet, which is why it took a re-index to surface.
--
-- The fix is the textbook one: give every transaction the *same* acquisition
-- order. PostgreSQL locks rows in the order the plan emits them, and `LockRows`
-- is planned above any Sort, so `... ORDER BY source_id, target_id FOR UPDATE`
-- locks the rows in primary-key order. Two transactions ordering their
-- acquisitions by one global key cannot hold-and-wait in a cycle: a cycle needs
-- r1 < r2 for one and r2 < r1 for the other. Held to commit, so the subsequent
-- deletes only re-lock rows this transaction already owns.
--
-- Only the four *derived* tables need this, and that is a property of the schema
-- rather than an optimization. Every other edge a file owns is written by
-- store.flatten out of one self-contained facts.FileFacts, so both endpoints are
-- always occurrences (or scopes) of that same file: two transactions for two
-- different files have disjoint row sets in `references_local`, `contains_*` and
-- `defines`, and disjoint sets cannot collide, let alone cycle. `imports` is
-- file -> file and cleared from its source side only, so likewise. The four
-- below are the only tables link.RebuildAll fills with rows whose two endpoints
-- belong to *different* files.
--
-- Ordering within a table is enough only because the tables are also visited in
-- a fixed order (store.deleteFile's step list), which rules out a cycle that
-- runs through two tables: a transaction reaching for a `calls` row has already
-- finished with `resolves_to` and so is not waiting on one.
--
-- The union is what keeps this index-driven, and index-driven is the whole
-- reason the delete was split in two in the first place. It is the same two
-- index scans the split delete uses -- `..._pkey` on the source side,
-- `..._target_idx` on the target side -- joined back on the edge's key so that
-- `FOR UPDATE` has a real table to lock; nothing here is allowed to become the
-- whole-table Seq Scan an `OR` would force.
--
-- It is not free. Measured at the scale of the note above (200k occurrences,
-- 200k `calls` rows), per file, averaged over 200 files: the two split deletes
-- alone 0.42 ms, the lock plus the same two deletes 1.27 ms. So ordering costs
-- ~0.85 ms per derived table per file -- ~3.4 ms across the four -- and it buys
-- back a class of failure a retry can only paper over. For scale, the `OR` form
-- this carefully does not reintroduce costs 51 ms per table per file.
-- ===========================================================================

-- name: LockResolvesToByFile :exec
WITH victim AS (
    SELECT source_id, target_id FROM resolves_to
    WHERE source_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = @owner_file_id)
    UNION
    SELECT source_id, target_id FROM resolves_to
    WHERE target_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = @owner_file_id)
)
SELECT e.source_id FROM resolves_to e
JOIN victim v ON v.source_id = e.source_id AND v.target_id = e.target_id
ORDER BY e.source_id, e.target_id
FOR UPDATE OF e;

-- name: LockCallsByFile :exec
WITH victim AS (
    SELECT source_id, target_id FROM calls
    WHERE source_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = @owner_file_id)
    UNION
    SELECT source_id, target_id FROM calls
    WHERE target_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = @owner_file_id)
)
SELECT e.source_id FROM calls e
JOIN victim v ON v.source_id = e.source_id AND v.target_id = e.target_id
ORDER BY e.source_id, e.target_id
FOR UPDATE OF e;

-- name: LockImplementsByFile :exec
WITH victim AS (
    SELECT source_id, target_id FROM implements
    WHERE source_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = @owner_file_id)
    UNION
    SELECT source_id, target_id FROM implements
    WHERE target_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = @owner_file_id)
)
SELECT e.source_id FROM implements e
JOIN victim v ON v.source_id = e.source_id AND v.target_id = e.target_id
ORDER BY e.source_id, e.target_id
FOR UPDATE OF e;

-- name: LockTypeDefinesByFile :exec
WITH victim AS (
    SELECT source_id, target_id FROM type_defines
    WHERE source_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = @owner_file_id)
    UNION
    SELECT source_id, target_id FROM type_defines
    WHERE target_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = @owner_file_id)
)
SELECT e.source_id FROM type_defines e
JOIN victim v ON v.source_id = e.source_id AND v.target_id = e.target_id
ORDER BY e.source_id, e.target_id
FOR UPDATE OF e;

-- name: DeleteResolvesToBySourceFile :exec
DELETE FROM resolves_to
WHERE source_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = @owner_file_id);

-- name: DeleteResolvesToByTargetFile :exec
DELETE FROM resolves_to
WHERE target_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = @owner_file_id);

-- name: DeleteCallsBySourceFile :exec
DELETE FROM calls
WHERE source_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = @owner_file_id);

-- name: DeleteCallsByTargetFile :exec
DELETE FROM calls
WHERE target_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = @owner_file_id);

-- name: DeleteImplementsBySourceFile :exec
DELETE FROM implements
WHERE source_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = @owner_file_id);

-- name: DeleteImplementsByTargetFile :exec
DELETE FROM implements
WHERE target_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = @owner_file_id);

-- name: DeleteTypeDefinesBySourceFile :exec
DELETE FROM type_defines
WHERE source_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = @owner_file_id);

-- name: DeleteTypeDefinesByTargetFile :exec
DELETE FROM type_defines
WHERE target_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = @owner_file_id);

-- name: DeleteReferencesLocalBySourceFile :exec
DELETE FROM references_local
WHERE source_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = @owner_file_id);

-- name: DeleteReferencesLocalByTargetFile :exec
DELETE FROM references_local
WHERE target_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = @owner_file_id);

-- name: DeleteContainsOccurrenceBySourceFile :exec
DELETE FROM contains_occurrence
WHERE source_id IN (SELECT s.id FROM scope s WHERE s.file_id = @owner_file_id);

-- name: DeleteContainsOccurrenceByTargetFile :exec
DELETE FROM contains_occurrence
WHERE target_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = @owner_file_id);

-- name: DeleteContainsScopeBySourceFile :exec
DELETE FROM contains_scope
WHERE source_id IN (SELECT s.id FROM scope s WHERE s.file_id = @owner_file_id);

-- name: DeleteContainsScopeByTargetFile :exec
DELETE FROM contains_scope
WHERE target_id IN (SELECT s.id FROM scope s WHERE s.file_id = @owner_file_id);

-- `defines` starts at the file row itself, so its owning side is a plain
-- equality on the leading column of `defines_pkey`.
-- name: DeleteDefinesBySourceFile :exec
DELETE FROM defines WHERE source_id = @owner_file_id;

-- name: DeleteDefinesByTargetFile :exec
DELETE FROM defines
WHERE target_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = @owner_file_id);

-- `imports` is file -> file and derived, so it is owned by the referencing
-- (source) file only. Inbound imports are left alone: the file row and its id
-- survive a ReplaceFile, so an edge pointing at this file is still valid.
-- name: DeleteImportsByFile :exec
DELETE FROM imports WHERE source_id = @owner_file_id;

-- name: DeleteOccurrencesByFile :exec
DELETE FROM occurrence WHERE file_id = @owner_file_id;

-- name: DeleteScopesByFile :exec
DELETE FROM scope WHERE file_id = @owner_file_id;


-- ===========================================================================
-- Bulk insert (SPEC.md §6: `CopyFrom` straight into the target tables; no
-- staging, no merge, no ON CONFLICT -- delete-first guarantees no collision)
-- ===========================================================================

-- name: CopyScopes :copyfrom
INSERT INTO scope (id, file_id, kind, range_start, range_end, parent_scope_id)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: CopyOccurrences :copyfrom
INSERT INTO occurrence (id, file_id, descriptor, role, symbol_kind, name, range_start, range_end, scope_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: CopyDefines :copyfrom
INSERT INTO defines (source_id, target_id) VALUES ($1, $2);

-- name: CopyContainsScope :copyfrom
INSERT INTO contains_scope (source_id, target_id) VALUES ($1, $2);

-- name: CopyContainsOccurrence :copyfrom
INSERT INTO contains_occurrence (source_id, target_id) VALUES ($1, $2);

-- name: CopyReferencesLocal :copyfrom
INSERT INTO references_local (source_id, target_id) VALUES ($1, $2);


-- ===========================================================================
-- Full link rebuild (SPEC.md §7, §14 M2)
--
-- All five derived tables are recomputed in SQL from the extracted base facts;
-- the join key is the SCIP descriptor (§4.3) and the "symbol index" is the
-- btree `occurrence_descriptor_idx` (§7: "no separate structure").
--
-- Idempotency is by construction: every derived table is emptied and rewritten
-- from base facts that the rebuild never touches, and every INSERT is a
-- DISTINCT set, so the result is a pure function of the base facts.
--
-- DELETE rather than TRUNCATE: TRUNCATE takes ACCESS EXCLUSIVE and would block
-- the gopgql MCP readers (§8) for the duration of the rebuild; DELETE leaves
-- them reading the pre-rebuild snapshot until the transaction commits.
--
-- Every derived edge here is *cross-file* by construction (`d.file_id <>
-- r.file_id`). Intra-file resolution is already extracted as
-- `references_local` (§4.4), so the derived layer adds only what a file-local
-- extractor could not know.
-- ===========================================================================

-- name: DeleteAllResolvesTo :exec
DELETE FROM resolves_to;

-- name: DeleteAllImports :exec
DELETE FROM imports;

-- name: DeleteAllCalls :exec
DELETE FROM calls;

-- name: DeleteAllImplements :exec
DELETE FROM implements;

-- name: DeleteAllTypeDefines :exec
DELETE FROM type_defines;

-- The base derivation, shared in spirit by the four occurrence-level tables: a
-- reference occurrence and the definition its descriptor names, in a different
-- file. This is §7's whole mechanism -- `reference.descriptor ==
-- definition.descriptor` -- and the other tables are this join plus a filter.
-- name: RebuildResolvesTo :exec
INSERT INTO resolves_to (source_id, target_id)
SELECT DISTINCT r.id, d.id
FROM occurrence r
JOIN occurrence d ON d.descriptor = r.descriptor AND d.role = 'definition'
WHERE r.role = 'reference' AND d.file_id <> r.file_id;

-- imports: file -> file, and the one derived table that is a plain descriptor
-- join like `resolves_to` rather than a filter over one.
--
-- The extractor emits a `package` *definition* per file, from its package
-- clause, whose descriptor is the namespace; an import (or a qualifier) in
-- another file emits a `package` *reference* carrying the byte-identical
-- descriptor. So the edge is just those two sides matched on descriptor. The
-- seed's one-import-fans-out-to-every-file-of-the-package behaviour falls out
-- for free, because a Go package spans files and each one contributes its own
-- package definition under the same descriptor. That fan-out is correct.
--
-- Keying on an *import* rather than on any cross-file reference is what makes
-- this correct, not merely simpler. Two files in the same Go package reference
-- each other's symbols with no import at all, so deriving imports from symbol
-- references would invent an import edge for every same-package cross-file
-- reference. (An earlier version of this query did exactly that, by reading a
-- package key out of the descriptor with a regex. deploy/seed/seed.sql could
-- not catch it: every cross-file reference in that corpus also crosses a
-- package.)
--
-- The `symbol_kind` test is on the definition side only. Per the extractor's
-- contract a reference cannot be trusted to know what it points at across a
-- file boundary, whereas a definition always knows what it is -- and only a
-- package reference can carry a package definition's descriptor anyway.
-- name: RebuildImports :exec
INSERT INTO imports (source_id, target_id)
SELECT DISTINCT r.file_id, d.file_id
FROM occurrence r
JOIN occurrence d
  ON d.descriptor = r.descriptor
 AND d.role = 'definition'
 AND d.symbol_kind = 'package'
WHERE r.role = 'reference' AND d.file_id <> r.file_id;

-- calls: definition -> definition. The reference gives the callee; the caller
-- is the reference's enclosing callable, taken as the nearest preceding
-- function/method definition in the same file. That is an approximation, and
-- deliberately so -- §4.4 labels `calls` "approximate; refined by overlays",
-- and a precise caller needs the `go/types` overlay of §4.2, not the CST.
-- name: RebuildCalls :exec
WITH enclosing_callable AS (
    SELECT DISTINCT ON (r.id) r.id AS ref_id, d.id AS def_id
    FROM occurrence r
    JOIN occurrence d
      ON d.file_id = r.file_id
     AND d.role = 'definition'
     AND d.symbol_kind IN ('function', 'method')
     AND d.range_start <= r.range_start
    WHERE r.role = 'reference'
    ORDER BY r.id, d.range_start DESC, d.id
)
INSERT INTO calls (source_id, target_id)
SELECT DISTINCT e.def_id, d.id
FROM occurrence r
JOIN occurrence d ON d.descriptor = r.descriptor AND d.role = 'definition'
JOIN enclosing_callable e ON e.ref_id = r.id
WHERE r.role = 'reference'
  AND d.file_id <> r.file_id
  AND r.symbol_kind IN ('function', 'method')
  AND d.symbol_kind IN ('function', 'method')
  AND e.def_id <> d.id;

-- type_defines: definition -> definition. The weakest of the five, and the only
-- one that is not a descriptor join, because nothing in the schema records a
-- declared type: `occurrence` has no type column, so for "the type of `store`"
-- there is literally nothing to join on. The extractor computes the declared
-- type internally and has nowhere to put it.
--
-- What the facts do carry is the shape of a declaration: a variable/field
-- definition, then *immediately* the type's reference occurrence, in the same
-- scope -- `store := graph.Store{}` is the seed's c21/c22 pair. So this derives
-- from adjacency, and adjacency is the whole of its soundness argument.
--
-- Immediate adjacency, not "the nearest preceding definition", is what makes it
-- defensible. In `var s Foo; g(remote.Bar{})` the nearest preceding *definition*
-- to the `Bar` reference is `s`, which would assert that `s` has type `Bar` --
-- wrong. The occurrence immediately before `Bar` is `g`, not a definition at
-- all, so requiring the immediate predecessor rejects it. `lag` over
-- (file, range_start) is exactly that predecessor.
--
-- Limits, stated rather than papered over: a declaration whose type reference is
-- not its immediate successor (a composite literal with a leading field name, a
-- multi-name `var a, b T`) yields no edge, and the same-scope test is what keeps
-- a declaration at the end of one scope from binding to a type reference opening
-- the next. It under-derives rather than inventing edges, which is the right way
-- for an approximation to fail. A `declared_type` column, or the `go/types`
-- overlay of §4.2, is what would make it exact -- both out of M2.
-- name: RebuildTypeDefines :exec
WITH adjacent AS (
    SELECT id, file_id, scope_id, descriptor, role, symbol_kind,
           lag(id) OVER w AS prev_id,
           lag(role) OVER w AS prev_role,
           lag(symbol_kind) OVER w AS prev_kind,
           lag(scope_id) OVER w AS prev_scope
    FROM occurrence
    WINDOW w AS (PARTITION BY file_id ORDER BY range_start, id)
)
INSERT INTO type_defines (source_id, target_id)
SELECT DISTINCT a.prev_id, d.id
FROM adjacent a
JOIN occurrence d ON d.descriptor = a.descriptor AND d.role = 'definition'
WHERE a.role = 'reference'
  AND a.symbol_kind = 'type'
  AND a.prev_role = 'definition'
  -- Only something that *has* a type: not another type, not a callable.
  AND a.prev_kind IN ('variable', 'field', 'parameter', 'constant')
  AND a.prev_scope IS NOT DISTINCT FROM a.scope_id
  AND d.file_id <> a.file_id
  AND a.prev_id <> d.id;

-- implements: definition -> definition, at both the type and the member level.
--
-- Nothing extracted says "implements": Go's satisfaction is implicit, so a
-- file-local extractor cannot emit it and there is no reference occurrence to
-- join on. What the descriptors *do* carry is method sets -- a member of type
-- `T#` is a definition whose descriptor is `T#` plus a `Method().` suffix -- so
-- the derivation is structural method-set containment: a non-interface type
-- implements an interface when it has every one of the interface's method
-- suffixes. Interfaces with no methods are excluded (otherwise every type in
-- the corpus would implement them), and, like the rest of the derived layer,
-- only cross-file pairs are materialized. Approximate in the same sense as
-- `calls`: signatures are not compared, which a `go/types` overlay would fix.
-- name: RebuildImplements :exec
WITH type_def AS (
    SELECT id, file_id, descriptor, symbol_kind
    FROM occurrence
    WHERE role = 'definition' AND right(descriptor, 1) = '#'
), member AS (
    SELECT t.id AS type_id,
           m.id AS member_id,
           substring(m.descriptor FROM length(t.descriptor) + 1) AS suffix
    FROM type_def t
    JOIN occurrence m
      ON m.role = 'definition'
     AND starts_with(m.descriptor, t.descriptor)
     AND substring(m.descriptor FROM length(t.descriptor) + 1) ~ '^[^#/]+\(\)\.$'
), method_set AS (
    SELECT type_id, array_agg(suffix ORDER BY suffix) AS suffixes
    FROM member
    GROUP BY type_id
), impl AS (
    SELECT c.id AS impl_type, i.id AS iface_type
    FROM type_def i
    JOIN method_set mi ON mi.type_id = i.id
    JOIN type_def c ON c.file_id <> i.file_id AND c.symbol_kind <> 'interface'
    JOIN method_set mc ON mc.type_id = c.id
    WHERE i.symbol_kind = 'interface'
      AND mc.suffixes @> mi.suffixes
)
INSERT INTO implements (source_id, target_id)
SELECT impl_type, iface_type FROM impl
UNION
SELECT mc.member_id, mi.member_id
FROM impl
JOIN member mi ON mi.type_id = impl.iface_type
JOIN member mc ON mc.type_id = impl.impl_type AND mc.suffix = mi.suffix;


-- ===========================================================================
-- Link serialization (SPEC.md §7's backstop, Decision 17)
--
-- The backstop is a *full* re-link: it empties every derived table and rebuilds
-- it from base facts. Run on a nightly timer, that collides with the one thing
-- the graph does concurrently -- an index in flight. The endpoint FKs are plain
-- REFERENCES, so a rebuild inserting `resolves_to (source_id, target_id)` while
-- a loader deletes the occurrences those ids name fails with SQLSTATE 23503
-- (`resolves_to_target_id_fkey`), and the two orderings fail differently: the
-- rebuild can also delete a file's inbound edges out from under a loader that is
-- about to re-create them. Neither is a corruption -- both transactions are
-- atomic -- but both turn a scheduled self-heal into a scheduled failure.
--
-- So the two are given a reader/writer lock, and the asymmetry is the point.
-- Every *writer* of the base facts takes the lock in **shared** mode, so two
-- loaders still run in parallel exactly as they did before; the *full rebuild*
-- takes it **exclusive**, so it waits for every load in flight and holds off
-- every load that starts while it runs. It is an advisory lock rather than a
-- table lock because what has to be excluded is a pair of *transactions*, not a
-- pair of statements, and because a real lock strong enough to do it would also
-- block the gopgql MCP readers (§8) -- which is the same reason the rebuild
-- DELETEs instead of TRUNCATEs.
--
-- Held to the end of the transaction (`_xact_`), so nothing has to remember to
-- release it and a crashed backend releases it by disconnecting. Taken as the
-- first statement of the transaction in both cases, which is what rules out a
-- cycle: the exclusive waiter holds nothing while it waits.
--
-- The key is a hash of a fixed string, in the *single-argument* advisory
-- keyspace shared with LockFilePath's per-path lock. A repository holding a file
-- whose path hashes to the same value would take the two locks against each
-- other; the cost is that that one file's load serializes with the backstop,
-- which is what the lock is for anyway, so the collision is harmless rather than
-- merely improbable.
--
-- What this does *not* claim to fix is two concurrent indexers of one corpus.
-- They take the lock in the same mode and so do not exclude each other, exactly
-- as before this existed.
-- ===========================================================================

-- name: LockLinkShared :exec
SELECT pg_advisory_xact_lock_shared(hashtextextended('codiq:link', 0));

-- name: LockLinkExclusive :exec
SELECT pg_advisory_xact_lock(hashtextextended('codiq:link', 0));


-- ===========================================================================
-- Incremental re-link (SPEC.md §7, §14 M8, Decision 5)
--
-- The full rebuild above is a pure function of the base facts, which is why the
-- graph has been right since M2: there is no state to drift. An incremental
-- re-link is an optimization that has to produce the identical result, so
-- everything below is written to be *equal to* the rebuild restricted to a set
-- of owner files, and never to approximate it.
--
-- **Ownership** (§7): an intra-file edge is owned by the file its endpoints
-- share; a cross-file edge is owned by its *referencing* file. Every derived
-- table but `implements` is reference-driven, so "the referencing file" is a
-- column: `resolves_to`, `calls` and `type_defines` put an occurrence of the
-- referencing file in `source_id`, and `imports` puts the referencing file
-- itself there. Re-linking an owner is therefore: delete every row whose
-- `source_id` belongs to it, and re-run the derivation restricted to it. The
-- *other* side of each join stays unrestricted -- a re-linked file matches
-- definitions anywhere in the corpus, which is what makes a definition that
-- moved between files land on its new home.
--
-- **The owner set** is RelinkOwners below, evaluated once per changed file
-- *before* its rows are replaced and once *after*. Both halves are needed and
-- neither is a superset of the other:
--
--   * after: a file that references a descriptor the changed file *now* defines
--     owns an edge that did not exist before and must be created.
--   * before: a file that references a descriptor the changed file *used to*
--     define owns an edge that store.deleteFile has just removed from the target
--     side (it pointed at occurrence ids that no longer exist). If the
--     definition moved to a third file, that owner's edge has to be recomputed
--     against the new home; if it vanished, the edge must stay gone. Either way
--     the owner is only reachable from the pre-replacement state, which is why
--     this is asked before the load and not after.
--
-- Every changed file is in the set by construction (the first branch), and it
-- has to be: store.flatten mints fresh uuids for a file's occurrences on every
-- load, so *all* of a re-loaded file's derived edges are new rows.
--
-- Why that set is exactly right, and not merely conservative: an edge owned by
-- file U is a function of U's own rows and of the definitions matching the
-- descriptors U references. If U was not changed its own rows did not move, so
-- the edge can only change if the definition set of some descriptor U references
-- changed -- and a definition set changes only where a changed file gained or
-- lost a definition, which is precisely the union of the two evaluations above.
-- Too small a neighbourhood leaves stale edges and too large is merely slow, so
-- the argument is worth having in both directions.
--
-- `implements` is the one derivation this cannot express; see the note above
-- RebuildImplements' incremental counterpart at the end of this section.
-- ===========================================================================

-- RelinkOwners is the neighbourhood one file contributes: the file itself, plus
-- every file holding a reference occurrence whose descriptor the file defines.
-- The join is the descriptor btree and nothing else, which is §7's "found by
-- querying base facts on `descriptor`".
--
-- name: RelinkOwners :many
SELECT f.id FROM file f WHERE f.path = @path
UNION
SELECT DISTINCT r.file_id
FROM file f
JOIN occurrence d ON d.file_id = f.id AND d.role = 'definition'
JOIN occurrence r ON r.descriptor = d.descriptor AND r.role = 'reference'
WHERE f.path = @path;


-- The owner-scoped locks. Same argument as the per-file locks above -- take the
-- rows in one global order so two transactions cannot hold-and-wait in a cycle
-- -- and needed for the same reason, now that two batches' owner sets can
-- overlap even when their changed files do not. Source side only, because the
-- delete below is source side only: ownership is what an incremental re-link
-- recomputes, and an inbound edge from a file outside the set is that file's.

-- name: LockResolvesToByOwners :exec
SELECT e.source_id FROM resolves_to e
WHERE e.source_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = ANY(@owner_file_ids::uuid[]))
ORDER BY e.source_id, e.target_id
FOR UPDATE OF e;

-- name: LockCallsByOwners :exec
SELECT e.source_id FROM calls e
WHERE e.source_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = ANY(@owner_file_ids::uuid[]))
ORDER BY e.source_id, e.target_id
FOR UPDATE OF e;

-- name: LockTypeDefinesByOwners :exec
SELECT e.source_id FROM type_defines e
WHERE e.source_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = ANY(@owner_file_ids::uuid[]))
ORDER BY e.source_id, e.target_id
FOR UPDATE OF e;

-- name: LockImportsByOwners :exec
SELECT e.source_id FROM imports e
WHERE e.source_id = ANY(@owner_file_ids::uuid[])
ORDER BY e.source_id, e.target_id
FOR UPDATE OF e;


-- name: DeleteResolvesToByOwners :exec
DELETE FROM resolves_to
WHERE source_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = ANY(@owner_file_ids::uuid[]));

-- name: DeleteCallsByOwners :exec
DELETE FROM calls
WHERE source_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = ANY(@owner_file_ids::uuid[]));

-- name: DeleteTypeDefinesByOwners :exec
DELETE FROM type_defines
WHERE source_id IN (SELECT o.id FROM occurrence o WHERE o.file_id = ANY(@owner_file_ids::uuid[]));

-- name: DeleteImportsByOwners :exec
DELETE FROM imports WHERE source_id = ANY(@owner_file_ids::uuid[]);


-- The four scoped derivations. Each is its Rebuild… counterpart with one
-- conjunct added, restricting the *referencing* side to the owner set; nothing
-- else about any of them moves. That is what makes the equality claim checkable
-- by reading: with the owner set equal to every file in the corpus, the added
-- conjunct is a tautology and each query below is its Rebuild… twin.

-- name: RelinkResolvesTo :exec
INSERT INTO resolves_to (source_id, target_id)
SELECT DISTINCT r.id, d.id
FROM occurrence r
JOIN occurrence d ON d.descriptor = r.descriptor AND d.role = 'definition'
WHERE r.role = 'reference' AND d.file_id <> r.file_id
  AND r.file_id = ANY(@owner_file_ids::uuid[]);

-- name: RelinkImports :exec
INSERT INTO imports (source_id, target_id)
SELECT DISTINCT r.file_id, d.file_id
FROM occurrence r
JOIN occurrence d
  ON d.descriptor = r.descriptor
 AND d.role = 'definition'
 AND d.symbol_kind = 'package'
WHERE r.role = 'reference' AND d.file_id <> r.file_id
  AND r.file_id = ANY(@owner_file_ids::uuid[]);

-- The enclosing-callable CTE is restricted rather than the outer query, and it
-- is the same restriction: the CTE already joins `d.file_id = r.file_id`, so the
-- caller it finds is always in the referencing file, which is the file that owns
-- the edge.
-- name: RelinkCalls :exec
WITH enclosing_callable AS (
    SELECT DISTINCT ON (r.id) r.id AS ref_id, d.id AS def_id
    FROM occurrence r
    JOIN occurrence d
      ON d.file_id = r.file_id
     AND d.role = 'definition'
     AND d.symbol_kind IN ('function', 'method')
     AND d.range_start <= r.range_start
    WHERE r.role = 'reference' AND r.file_id = ANY(@owner_file_ids::uuid[])
    ORDER BY r.id, d.range_start DESC, d.id
)
INSERT INTO calls (source_id, target_id)
SELECT DISTINCT e.def_id, d.id
FROM occurrence r
JOIN occurrence d ON d.descriptor = r.descriptor AND d.role = 'definition'
JOIN enclosing_callable e ON e.ref_id = r.id
WHERE r.role = 'reference'
  AND d.file_id <> r.file_id
  AND r.symbol_kind IN ('function', 'method')
  AND d.symbol_kind IN ('function', 'method')
  AND e.def_id <> d.id;

-- The adjacency window is `PARTITION BY file_id`, and the restriction is on
-- whole files, so every partition the scoped query builds is byte-identical to
-- the one the full rebuild builds. Filtering rows rather than files would move
-- the `lag` and silently change the answer, which is why the predicate is in the
-- CTE and is a file-level one.
-- name: RelinkTypeDefines :exec
WITH adjacent AS (
    SELECT id, file_id, scope_id, descriptor, role, symbol_kind,
           lag(id) OVER w AS prev_id,
           lag(role) OVER w AS prev_role,
           lag(symbol_kind) OVER w AS prev_kind,
           lag(scope_id) OVER w AS prev_scope
    FROM occurrence
    WHERE file_id = ANY(@owner_file_ids::uuid[])
    WINDOW w AS (PARTITION BY file_id ORDER BY range_start, id)
)
INSERT INTO type_defines (source_id, target_id)
SELECT DISTINCT a.prev_id, d.id
FROM adjacent a
JOIN occurrence d ON d.descriptor = a.descriptor AND d.role = 'definition'
WHERE a.role = 'reference'
  AND a.symbol_kind = 'type'
  AND a.prev_role = 'definition'
  AND a.prev_kind IN ('variable', 'field', 'parameter', 'constant')
  AND a.prev_scope IS NOT DISTINCT FROM a.scope_id
  AND d.file_id <> a.file_id
  AND a.prev_id <> d.id;


-- `implements` is re-linked in full rather than by owner (link.Batch.Relink says
-- why), so its ordered lock is over the whole table rather than over a file's
-- rows. It is needed for the same reason the per-file locks are: two batches
-- re-linking concurrently reach this table in the same position of the same
-- fixed table order, so they must also agree on the order they take its rows in.
-- RebuildAll needs no such lock, because it holds the link lock exclusively and
-- so is not racing anything.
-- name: LockAllImplements :exec
SELECT e.source_id FROM implements e
ORDER BY e.source_id, e.target_id
FOR UPDATE OF e;
