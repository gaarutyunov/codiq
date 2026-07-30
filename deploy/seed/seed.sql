-- CodiQ demo corpus — test data only (SPEC.md §14 M1: "no ingestion pipeline
-- yet; anyone can insert rows directly").
--
-- M1's whole claim is that the data model is the product, so this seed is
-- shaped to make that claim checkable: it is a real three-file Go package,
-- hand-extracted the way the M2 extractor will produce it, with every one of
-- §4.4's five *derived* cross-file edge kinds actually present. A corpus of one
-- file could not exercise any of them.
--
--   cmd/codiq/main.go ──imports──▶ internal/graph/store.go ──implements──▶ internal/graph/iface.go
--
--     main()  ──calls──▶ Store.Put()          (cross-file, derived)
--     Store   ──implements──▶ Storer          (cross-file, derived)
--     store   ──type_defines──▶ Store         (cross-file, derived)
--     graph.Store (ref) ──resolves_to──▶ Store (def)   (cross-file, derived)
--     s.db (ref) ──references_local──▶ db (def)        (same file, extracted)
--
-- "Hand-extracted the way the M2 extractor will produce it" is a constraint, not
-- a flourish: M2's link.RebuildAll recomputes all five derived tables from the
-- base rows, so any derived row here that does not follow from this file's own
-- base rows is a row the first rebuild deletes and never puts back. Every file
-- therefore carries the `package` definition the extractor emits from its package
-- clause, and main.go carries the matching `package` reference for its import —
-- without those four rows the `imports` edges below are underivable, and a
-- rebuild over the seeded corpus would silently empty the table. They are the
-- only occurrences here that exist for the link pass rather than for the reader.
--
-- Descriptors follow §4.3 — `scheme manager package version descriptor-path` —
-- with the prefix a go.mod resolver would supply and the suffix a Go stanza
-- would emit from the CST. They are the join key of the link pass (§7), so they
-- are written out in full rather than abbreviated.
--
-- Identifiers are fixed uuids, not gen_random_uuid(): a test asserting on a
-- seeded row needs to be able to name it, and a fixed id is also what makes
-- re-running this file a no-op rather than a duplicate corpus.
--
-- Ranges are byte offsets into the file, half-open [start, end).

BEGIN;

-- Idempotent re-seed, in the shape §6 gives the real reduce phase: delete
-- everything owned by these files, then rewrite it. Edges go first because they
-- carry the foreign keys. Cross-file edges are owned by their *referencing*
-- file (§7), which is why they are deleted via source_id rather than by a
-- file_id column of their own — an edge table has none (see schema/codiq.graphql).
CREATE TEMP TABLE seeded_file (id uuid PRIMARY KEY) ON COMMIT DROP;
INSERT INTO seeded_file (id) VALUES
  ('00000000-0000-4000-8000-000000000f01'),
  ('00000000-0000-4000-8000-000000000f02'),
  ('00000000-0000-4000-8000-000000000f03');

CREATE TEMP TABLE seeded_occurrence (id uuid PRIMARY KEY) ON COMMIT DROP;
INSERT INTO seeded_occurrence (id)
  SELECT id FROM occurrence WHERE file_id IN (SELECT id FROM seeded_file);

DELETE FROM calls             WHERE source_id IN (SELECT id FROM seeded_occurrence);
DELETE FROM implements        WHERE source_id IN (SELECT id FROM seeded_occurrence);
DELETE FROM type_defines      WHERE source_id IN (SELECT id FROM seeded_occurrence);
DELETE FROM resolves_to       WHERE source_id IN (SELECT id FROM seeded_occurrence);
DELETE FROM references_local  WHERE source_id IN (SELECT id FROM seeded_occurrence);
DELETE FROM contains_occurrence WHERE target_id IN (SELECT id FROM seeded_occurrence);
DELETE FROM defines           WHERE source_id IN (SELECT id FROM seeded_file);
DELETE FROM imports           WHERE source_id IN (SELECT id FROM seeded_file);
DELETE FROM contains_scope    WHERE source_id IN (SELECT id FROM scope WHERE file_id IN (SELECT id FROM seeded_file));
DELETE FROM occurrence        WHERE file_id IN (SELECT id FROM seeded_file);
DELETE FROM scope             WHERE file_id IN (SELECT id FROM seeded_file);
DELETE FROM file              WHERE id IN (SELECT id FROM seeded_file);


-- ---------------------------------------------------------------------------
-- Vertices: files
--
-- All three share one package coordinate, because they are one Go package in
-- one module — the coordinate comes from go.mod, not from the file (§4.3).
-- ---------------------------------------------------------------------------
INSERT INTO file (id, path, lang, pkg_scheme, pkg_manager, pkg_name, pkg_version) VALUES
  ('00000000-0000-4000-8000-000000000f01', 'internal/graph/iface.go', 'go',
   'scip-go', 'gomod', 'github.com/gaarutyunov/codiq', 'v0.1.0'),
  ('00000000-0000-4000-8000-000000000f02', 'internal/graph/store.go', 'go',
   'scip-go', 'gomod', 'github.com/gaarutyunov/codiq', 'v0.1.0'),
  ('00000000-0000-4000-8000-000000000f03', 'cmd/codiq/main.go', 'go',
   'scip-go', 'gomod', 'github.com/gaarutyunov/codiq', 'v0.1.0');


-- ---------------------------------------------------------------------------
-- Vertices: scopes — the lexical containment skeleton (§4.4)
-- ---------------------------------------------------------------------------
INSERT INTO scope (id, file_id, kind, range_start, range_end, parent_scope_id) VALUES
  -- iface.go
  ('00000000-0000-4000-8000-0000000005c1', '00000000-0000-4000-8000-000000000f01', 'file',     0, 190, NULL),
  -- store.go
  ('00000000-0000-4000-8000-0000000005c2', '00000000-0000-4000-8000-000000000f02', 'file',     0, 420, NULL),
  ('00000000-0000-4000-8000-0000000005c3', '00000000-0000-4000-8000-000000000f02', 'function', 210, 415,
   '00000000-0000-4000-8000-0000000005c2'),
  -- main.go
  ('00000000-0000-4000-8000-0000000005c4', '00000000-0000-4000-8000-000000000f03', 'file',     0, 310, NULL),
  ('00000000-0000-4000-8000-0000000005c5', '00000000-0000-4000-8000-000000000f03', 'function', 120, 305,
   '00000000-0000-4000-8000-0000000005c4');


-- ---------------------------------------------------------------------------
-- Vertices: occurrences
--
-- `role` distinguishes a definition from a use; both are the same row shape, so
-- the link pass is one self-join on `descriptor` (§4.3, §7). A reference whose
-- target lives in another file carries that target's descriptor here, still
-- unresolved — the `resolves_to` rows further down are what the link pass makes
-- of it.
-- ---------------------------------------------------------------------------
INSERT INTO occurrence
  (id, file_id, descriptor, role, symbol_kind, name, range_start, range_end, scope_id) VALUES

  -- internal/graph/iface.go ------------------------------------------------
  -- `package graph` — the namespace descriptor, ending in `/` because §4.3 makes
  -- a package a namespace rather than a symbol. Every file of the package emits
  -- its own, all byte-identical, which is what makes one import fan out to every
  -- file of the package (the two `imports` rows at the bottom).
  ('00000000-0000-4000-8000-000000000c03', '00000000-0000-4000-8000-000000000f01',
   'scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/',
   'definition', 'package', 'graph', 8, 13, '00000000-0000-4000-8000-0000000005c1'),
  ('00000000-0000-4000-8000-000000000c01', '00000000-0000-4000-8000-000000000f01',
   'scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Storer#',
   'definition', 'interface', 'Storer', 45, 51, '00000000-0000-4000-8000-0000000005c1'),
  ('00000000-0000-4000-8000-000000000c02', '00000000-0000-4000-8000-000000000f01',
   'scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Storer#Put().',
   'definition', 'method', 'Put', 72, 75, '00000000-0000-4000-8000-0000000005c1'),

  -- internal/graph/store.go ------------------------------------------------
  -- The second file of package `graph`, so the same package descriptor again.
  ('00000000-0000-4000-8000-000000000c15', '00000000-0000-4000-8000-000000000f02',
   'scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/',
   'definition', 'package', 'graph', 8, 13, '00000000-0000-4000-8000-0000000005c2'),
  ('00000000-0000-4000-8000-000000000c10', '00000000-0000-4000-8000-000000000f02',
   'scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Store#',
   'definition', 'type', 'Store', 45, 50, '00000000-0000-4000-8000-0000000005c2'),
  ('00000000-0000-4000-8000-000000000c11', '00000000-0000-4000-8000-000000000f02',
   'scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Store#db.',
   'definition', 'field', 'db', 66, 68, '00000000-0000-4000-8000-0000000005c2'),
  ('00000000-0000-4000-8000-000000000c12', '00000000-0000-4000-8000-000000000f02',
   'scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Store#Put().',
   'definition', 'method', 'Put', 232, 235, '00000000-0000-4000-8000-0000000005c2'),
  -- The receiver's type name: a *reference*, in the same file as its definition,
  -- so extraction already resolved it (§4.3 "same-file references").
  ('00000000-0000-4000-8000-000000000c13', '00000000-0000-4000-8000-000000000f02',
   'scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Store#',
   'reference', 'type', 'Store', 222, 227, '00000000-0000-4000-8000-0000000005c2'),
  -- `s.db` inside the method body — same file, resolved at extraction.
  ('00000000-0000-4000-8000-000000000c14', '00000000-0000-4000-8000-000000000f02',
   'scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Store#db.',
   'reference', 'field', 'db', 300, 302, '00000000-0000-4000-8000-0000000005c3'),

  -- cmd/codiq/main.go ------------------------------------------------------
  -- `package main` — nothing imports it, so it derives no edge. It is here
  -- because the extractor emits one per file unconditionally, and a seed that
  -- omitted it would be describing a corpus the extractor cannot produce.
  ('00000000-0000-4000-8000-000000000c24', '00000000-0000-4000-8000-000000000f03',
   'scip-go gomod github.com/gaarutyunov/codiq v0.1.0 cmd/codiq/',
   'definition', 'package', 'main', 8, 12, '00000000-0000-4000-8000-0000000005c4'),
  -- `import "github.com/gaarutyunov/codiq/internal/graph"` — the package
  -- *reference*, carrying the same descriptor the two definitions above do. This
  -- one row is the entire input to the `imports` derivation: §7 keys that table
  -- on an import and not on any cross-file reference, because two files of one
  -- package reference each other with no import at all.
  ('00000000-0000-4000-8000-000000000c25', '00000000-0000-4000-8000-000000000f03',
   'scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/',
   'reference', 'package', 'graph', 30, 45, '00000000-0000-4000-8000-0000000005c4'),
  ('00000000-0000-4000-8000-000000000c20', '00000000-0000-4000-8000-000000000f03',
   'scip-go gomod github.com/gaarutyunov/codiq v0.1.0 cmd/codiq/main().',
   'definition', 'function', 'main', 125, 129, '00000000-0000-4000-8000-0000000005c4'),
  -- The local variable. Its *type* is defined in another file, which is what the
  -- `type_defines` edge below records.
  ('00000000-0000-4000-8000-000000000c21', '00000000-0000-4000-8000-000000000f03',
   'scip-go gomod github.com/gaarutyunov/codiq v0.1.0 cmd/codiq/main().store.',
   'definition', 'variable', 'store', 140, 145, '00000000-0000-4000-8000-0000000005c5'),
  -- `graph.Store{...}` — cross-file, so it carries the target descriptor
  -- unresolved until the link pass matches it.
  ('00000000-0000-4000-8000-000000000c22', '00000000-0000-4000-8000-000000000f03',
   'scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Store#',
   'reference', 'type', 'Store', 155, 166, '00000000-0000-4000-8000-0000000005c5'),
  -- `store.Put(...)` — likewise cross-file.
  ('00000000-0000-4000-8000-000000000c23', '00000000-0000-4000-8000-000000000f03',
   'scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Store#Put().',
   'reference', 'method', 'Put', 250, 253, '00000000-0000-4000-8000-0000000005c5');


-- ---------------------------------------------------------------------------
-- Extracted edges (intra-file). Everything below this line up to the next
-- banner is what a file-local extractor can emit on its own (§2.2, §5).
-- ---------------------------------------------------------------------------

-- file → occurrence(definition). The package clause is a definition like any
-- other, so each file defines its own; main.go's package *reference* is not one
-- and so has no row here.
INSERT INTO defines (source_id, target_id) VALUES
  ('00000000-0000-4000-8000-000000000f01', '00000000-0000-4000-8000-000000000c03'),
  ('00000000-0000-4000-8000-000000000f02', '00000000-0000-4000-8000-000000000c15'),
  ('00000000-0000-4000-8000-000000000f03', '00000000-0000-4000-8000-000000000c24'),
  ('00000000-0000-4000-8000-000000000f01', '00000000-0000-4000-8000-000000000c01'),
  ('00000000-0000-4000-8000-000000000f01', '00000000-0000-4000-8000-000000000c02'),
  ('00000000-0000-4000-8000-000000000f02', '00000000-0000-4000-8000-000000000c10'),
  ('00000000-0000-4000-8000-000000000f02', '00000000-0000-4000-8000-000000000c11'),
  ('00000000-0000-4000-8000-000000000f02', '00000000-0000-4000-8000-000000000c12'),
  ('00000000-0000-4000-8000-000000000f03', '00000000-0000-4000-8000-000000000c20'),
  ('00000000-0000-4000-8000-000000000f03', '00000000-0000-4000-8000-000000000c21');

-- scope → scope
INSERT INTO contains_scope (source_id, target_id) VALUES
  ('00000000-0000-4000-8000-0000000005c2', '00000000-0000-4000-8000-0000000005c3'),
  ('00000000-0000-4000-8000-0000000005c4', '00000000-0000-4000-8000-0000000005c5');

-- scope → occurrence. The package clause and the import both sit at the file's
-- top level, so the file scope contains them.
INSERT INTO contains_occurrence (source_id, target_id) VALUES
  ('00000000-0000-4000-8000-0000000005c1', '00000000-0000-4000-8000-000000000c03'),
  ('00000000-0000-4000-8000-0000000005c2', '00000000-0000-4000-8000-000000000c15'),
  ('00000000-0000-4000-8000-0000000005c4', '00000000-0000-4000-8000-000000000c24'),
  ('00000000-0000-4000-8000-0000000005c4', '00000000-0000-4000-8000-000000000c25'),
  ('00000000-0000-4000-8000-0000000005c1', '00000000-0000-4000-8000-000000000c01'),
  ('00000000-0000-4000-8000-0000000005c1', '00000000-0000-4000-8000-000000000c02'),
  ('00000000-0000-4000-8000-0000000005c2', '00000000-0000-4000-8000-000000000c10'),
  ('00000000-0000-4000-8000-0000000005c2', '00000000-0000-4000-8000-000000000c11'),
  ('00000000-0000-4000-8000-0000000005c2', '00000000-0000-4000-8000-000000000c12'),
  ('00000000-0000-4000-8000-0000000005c2', '00000000-0000-4000-8000-000000000c13'),
  ('00000000-0000-4000-8000-0000000005c3', '00000000-0000-4000-8000-000000000c14'),
  ('00000000-0000-4000-8000-0000000005c4', '00000000-0000-4000-8000-000000000c20'),
  ('00000000-0000-4000-8000-0000000005c5', '00000000-0000-4000-8000-000000000c21'),
  ('00000000-0000-4000-8000-0000000005c5', '00000000-0000-4000-8000-000000000c22'),
  ('00000000-0000-4000-8000-0000000005c5', '00000000-0000-4000-8000-000000000c23');

-- occurrence(reference) → occurrence(definition), same file
INSERT INTO references_local (source_id, target_id) VALUES
  ('00000000-0000-4000-8000-000000000c13', '00000000-0000-4000-8000-000000000c10'),
  ('00000000-0000-4000-8000-000000000c14', '00000000-0000-4000-8000-000000000c11');


-- ---------------------------------------------------------------------------
-- Derived edges (cross-file). Nothing below this line is extracted: the link
-- pass (§7) computes all of it by matching `reference.descriptor` against
-- `definition.descriptor`. It is written out here because M1 has no link pass
-- yet — M2 replaces these rows with a real full rebuild.
--
-- Every row below is therefore also an assertion about M2: it must be exactly
-- what link.RebuildAll derives from the base rows above, because a rebuild
-- empties these five tables and refills them from those rows alone. Adding a row
-- here that nothing above implies does not extend the demo corpus, it schedules
-- its own deletion. link/rebuild_test.go checks the two agree.
-- ---------------------------------------------------------------------------

-- file → file. A Go import names a package, and a package is several files, so
-- one `import "internal/graph"` becomes an edge to each file of that package.
-- Derived from main.go's single package reference (c25) joined to the package
-- definition each of the two `graph` files carries (c03, c15).
INSERT INTO imports (source_id, target_id) VALUES
  ('00000000-0000-4000-8000-000000000f03', '00000000-0000-4000-8000-000000000f01'),
  ('00000000-0000-4000-8000-000000000f03', '00000000-0000-4000-8000-000000000f02');

-- occurrence(reference) → occurrence(definition), across files, by descriptor.
-- The package reference resolves like any other reference, to both definitions
-- that share its descriptor — the same two rows `imports` is derived from, one
-- level down.
INSERT INTO resolves_to (source_id, target_id) VALUES
  ('00000000-0000-4000-8000-000000000c22', '00000000-0000-4000-8000-000000000c10'),
  ('00000000-0000-4000-8000-000000000c23', '00000000-0000-4000-8000-000000000c12'),
  ('00000000-0000-4000-8000-000000000c25', '00000000-0000-4000-8000-000000000c03'),
  ('00000000-0000-4000-8000-000000000c25', '00000000-0000-4000-8000-000000000c15');

-- occurrence(definition) → occurrence(definition): main() calls Store.Put().
-- Approximate by construction — it is syntax plus a descriptor match, with no
-- type resolution behind it (§4.4).
INSERT INTO calls (source_id, target_id) VALUES
  ('00000000-0000-4000-8000-000000000c20', '00000000-0000-4000-8000-000000000c12');

-- Store implements Storer, and Store.Put implements Storer.Put.
INSERT INTO implements (source_id, target_id) VALUES
  ('00000000-0000-4000-8000-000000000c10', '00000000-0000-4000-8000-000000000c01'),
  ('00000000-0000-4000-8000-000000000c12', '00000000-0000-4000-8000-000000000c02');

-- The local `store` in main() has type Store, defined in another file.
INSERT INTO type_defines (source_id, target_id) VALUES
  ('00000000-0000-4000-8000-000000000c21', '00000000-0000-4000-8000-000000000c10');

COMMIT;
