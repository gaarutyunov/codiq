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
  ('00000000-0000-4000-8000-000000000c01', '00000000-0000-4000-8000-000000000f01',
   'scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Storer#',
   'definition', 'interface', 'Storer', 45, 51, '00000000-0000-4000-8000-0000000005c1'),
  ('00000000-0000-4000-8000-000000000c02', '00000000-0000-4000-8000-000000000f01',
   'scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Storer#Put().',
   'definition', 'method', 'Put', 72, 75, '00000000-0000-4000-8000-0000000005c1'),

  -- internal/graph/store.go ------------------------------------------------
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

-- file → occurrence(definition)
INSERT INTO defines (source_id, target_id) VALUES
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

-- scope → occurrence
INSERT INTO contains_occurrence (source_id, target_id) VALUES
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
-- ---------------------------------------------------------------------------

-- file → file. A Go import names a package, and a package is several files, so
-- one `import "internal/graph"` becomes an edge to each file of that package.
INSERT INTO imports (source_id, target_id) VALUES
  ('00000000-0000-4000-8000-000000000f03', '00000000-0000-4000-8000-000000000f01'),
  ('00000000-0000-4000-8000-000000000f03', '00000000-0000-4000-8000-000000000f02');

-- occurrence(reference) → occurrence(definition), across files, by descriptor
INSERT INTO resolves_to (source_id, target_id) VALUES
  ('00000000-0000-4000-8000-000000000c22', '00000000-0000-4000-8000-000000000c10'),
  ('00000000-0000-4000-8000-000000000c23', '00000000-0000-4000-8000-000000000c12');

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
