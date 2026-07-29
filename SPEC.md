# CodiQ — Ingestion Specification

- **Spec:** `SPEC.md`
- **Version:** v1.0
- **Status:** Accepted — all decisions closed, no open questions
- **Date:** 2026-07-28
- **Consolidates:** `INGESTION.md` v1.6 (architecture & decisions) + `DESIGN.md` v3.2 (per-milestone program design), merged so each milestone carries its repo diff, key code, data flow, and test.
- **Supersedes:** the prior CodiQ graph/workflow stack (Apache AGE openCypher; Temporal + second CNPG cluster), displaced by gopgql (schema + queries) and DBOS (orchestration).
- **Depends on:** gopgql (ghcr image) · DBOS Transact (Go) · PostgreSQL 19 (SQL/PGQ, `postgres:19beta2`) · gotreesitter · sqlc · goose · pgx · buf · Docker Compose (local) · godog · testcontainers-go
- **Sequencing:** CodiQ is built **first**, as the reference architecture example that gopgql and other components serve.
- **Delivery:** plain Go — packages grouped by what they do, wired in `main`; interfaces only where they earn their keep. No layered/hexagonal architecture.

-----

## 1. Purpose & Scope

CodiQ is a distributed code-intelligence platform (Kubernetes-targeted; run locally via Docker Compose for now), exposed to agents over MCP. This spec defines the **ingestion architecture**: how source code becomes a queryable property graph in PostgreSQL.

**In scope:** the extract → transform → load → link pipeline; the fact contract; the neutral-core graph model; the stanza contract; the load path; incrementality; orchestration; the read/MCP boundary; storage; deployment; testing; milestones.

**Out of scope (owned elsewhere):** the query compiler and MCP surface (owned by gopgql); per-language semantic overlays beyond the base structural extractor; the individual per-language `.scm`/mapper implementations (built per milestone against the fixed contract in §5).

-----

## 2. Architectural Principles

1. **Model is the single source of truth.** The GraphQL SDL is authoritative. Every downstream artifact is generated from it: DDL + goose migrations, protobuf fact types, sqlc models/queries, and (via gopgql) the SQL/PGQ view and the GraphQL/MCP surface. No consumer is exempt.
1. **Dumb extractor / smart schema+query.** The extractor emits file-local, structural facts only. All derivation, cross-file resolution, and transitive reasoning live behind the schema/query layer. *(Lineage: Kythe entries, Glean facts, SCIP transmission format.)*
1. **Read/write separation.** gopgql owns reads (GraphQL → SQL/PGQ, MCP). A generated loader owns writes. The two never mix; writes are never agent-triggered.
1. **The property graph is a read-only view.** `CREATE PROPERTY GRAPH` (owned by gopgql) is a view over plain vertex/edge tables. All mutation is ordinary DML on those base tables. The mutation path is genuinely separate from the query path.
1. **File-disjoint incrementality.** Base facts are file-owned; nothing about one file’s rows depends on another file. Cross-file structure is *derived*, not extracted. *(Lineage: GitHub stack-graphs — each file a disjoint subgraph.)*
1. **Neutral core.** One language-neutral core vocabulary; every language’s stanzas normalize to it. Language-specific or deeper-analysis **overlays are possible later but out of current scope** (§16).
1. **Pure Go, no cgo.** Cross-compilation to any GOOS/GOARCH is a hard constraint. Every dependency in the ingestion path is pure Go.
1. **Vertical slices, optimized in place.** Every milestone lands a working, integration-tested, deployed feature — never a shim. The target architecture below (§5–§9: DBOS map-reduce, protobuf artifacts, disk offload, incremental link) is the **end-state**; milestones reach it by progressively optimizing an already-working slice, not by building it big-bang. Features (godog scenarios) land **with their milestone**, not up front.

-----

## 3. System Overview

```mermaid
flowchart LR
    T[Trigger: push / manual / watch] --> BW[DBOS batch workflow]
    subgraph MAP[Map — per file, durable queue]
        E1[extract file A] --> A1[(protobuf artifact\nshared volume)]
        E2[extract file B] --> A2[(protobuf artifact)]
        E3[extract file N] --> A3[(protobuf artifact)]
    end
    BW --> MAP
    MAP --> RD
    subgraph RD[Reduce — one transaction, successful subset]
        L1[delete-by-file + CopyFrom\ninto vertex/edge tables] --> L2[incremental re-link\ncross-file edges]
    end
    RD --> PG[(PostgreSQL 19\nvertex/edge tables)]
    PG --> GQ[gopgql\nSQL/PGQ view + GraphQL/MCP]
    GQ --> AG[Agents / clients]
```

A trigger starts a **per-batch DBOS workflow**. The **map** phase enqueues one extract task per changed file on a durable queue; each runs `gotreesitter`, emits a protobuf fact artifact to a shared volume, and checkpoints only the artifact key. The **reduce** phase runs once over the successful subset, in a single transaction: per-file `delete-by-file` + `CopyFrom` into the base tables, then an incremental cross-file re-link. gopgql exposes the graph read-only over GraphQL/MCP.

-----

## 4. Data Model & Schema

### 4.1 SDL as the source

The GraphQL SDL defines vertices, edges, properties, and endpoint identity — semantics that cannot be recovered from DDL alone. From the SDL, CodiQ generates: **goose migrations** (versioned, delta, prior state folded analytically — no sidecar); **protobuf fact types** (§5): the SDL generates `.proto` files, which **buf** compiles to Go (buf.gen.yaml), with buf lint + breaking-change detection guarding schema evolution; **sqlc models + queries** (§6, sqlc reads the goose DDL); and, via gopgql, the **SQL/PGQ view + GraphQL/MCP surface** (§8).

### 4.2 Vocabulary strategy: neutral core + overlays

A single **language-neutral core** vocabulary; every language’s stanzas normalize *to* it. Language-specific or deeper-analysis facts are added as **derived overlay tables** (e.g. a Rust-trait overlay; a `go/types` precise-call overlay; CFG/PDG) that reference the core by symbol identity and never reshape it. Adding a language touches no core schema; adding fidelity is an additive overlay. *(Lineage: SCIP universal-symbol model for the core; Joern overlays for the escape hatch.)*

**Overlay governance (deferred — §16; recorded for when overlays return):** an overlay is an **additive SDL module** — its own tables, keyed by the core SCIP descriptor and **file-owned** like the core (so delete-by-file + descriptor-join machinery is reused for free), generating its own migrations/protobuf/sqlc. **Overlay producers run as additional steps in the batch reduce** (§6), after base load and core link, so overlays are always consistent with the core in a single pipeline.

### 4.3 Symbol identity: SCIP-style descriptors

Every symbol is named by a **SCIP-style structured descriptor** — `scheme manager package version descriptor-path` (e.g. `scip-go gomod github.com/foo/bar v1 pkg/Type#method().`). Identity is a single human-readable string; resolution is a string match, not an opaque ID.

Split of responsibility (keeps extraction file-local):

- **Stanzas emit the structural descriptor suffix** (`pkg/Type#method().`) from the CST — no project-wide knowledge required.
- **The batch supplies the package coordinate prefix** (`scip-go gomod github.com/foo/bar v1`) via **per-ecosystem manifest resolvers**: a small resolver reads the project manifest (go.mod → module path + version first; package.json, etc. later) and maps each file path to its coordinate. Runs in the batch, so the extractor stays file-local.
- **Same-file references** are resolved at extraction (the target definition is in the same CST).
- **Cross-file references** carry the target descriptor **unresolved**; the link pass matches `reference.descriptor == definition.descriptor` (§7).

### 4.4 Core model (Navigation + structural skeleton)

Structural only — everything below is derivable from the CST plus the link pass. No CFG/PDG (those are overlays).

**Vertex tables (base, file-owned — every row carries `file_id`):**

|Table       |Key columns                                                                                                                         |Notes                                                                |
|------------|------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------|
|`file`      |`file_id` PK; `path`, `lang`; `pkg_scheme`, `pkg_manager`, `pkg_name`, `pkg_version`                                                |package coordinate = descriptor prefix source                        |
|`occurrence`|`id` PK; `file_id`; `descriptor`; `role` (`definition` | `reference`); `symbol_kind`; `name`; `range_start`, `range_end`; `scope_id`|unifies definitions & references as SCIP-style occurrences with roles|
|`scope`     |`id` PK; `file_id`; `kind`; `range_start`, `range_end`; `parent_scope_id`                                                           |lexical containment skeleton                                         |

**Base edge tables (intra-file, extracted):**

|Table             |Endpoints                                         |Meaning                              |
|------------------|--------------------------------------------------|-------------------------------------|
|`contains`        |`scope` → `scope` | `occurrence`                  |lexical containment                  |
|`defines`         |`file` → `occurrence`(definition)                 |file declares a definition           |
|`references_local`|`occurrence`(reference) → `occurrence`(definition)|same-file use, resolved at extraction|

**Derived edge tables (cross-file, materialized by the link pass — §7):**

|Table         |Endpoints                                          |Meaning                                                 |
|--------------|---------------------------------------------------|--------------------------------------------------------|
|`resolves_to` |`occurrence`(reference) → `occurrence`(definition) |cross-file use, matched by descriptor                   |
|`imports`     |`file` → `file`                                    |import edges (by module/file descriptor)                |
|`calls`       |`occurrence`(definition) → `occurrence`(definition)|structural call graph (approximate; refined by overlays)|
|`implements`  |`occurrence`(definition) → `occurrence`(definition)|SCIP implementation relationship                        |
|`type_defines`|`occurrence` → `occurrence`(definition)            |SCIP type-definition relationship                       |

### 4.5 Property-graph view (read-only, gopgql)

gopgql compiles the SDL to a `CREATE PROPERTY GRAPH` view over these base + derived tables, mapping edges to `occurrence`/`file` identity via `SOURCE KEY … REFERENCES … / DESTINATION KEY … REFERENCES …`. `GRAPH_TABLE(… MATCH …)` reads rewrite to relational joins. CodiQ writes never touch the view.

### 4.6 Migration / encoding versioning

Row encodings carry a schema version alongside the goose migration version, so an SDL change that reshapes a table is reconciled by an out-of-band re-encode rather than a destructive rebuild. *(Lineage: Sourcegraph `*_schema_versions` companions.)*

-----

## 5. Extraction & the Stanza Contract (Map Phase)

- **Engine:** `gotreesitter` — a pure-Go tree-sitter runtime (no cgo; cross-compiles to any target; embedded grammar registry; GLR parser, incremental reparsing, query engine). One engine spans all languages.
- **Stanza authoring model:** per language, **a `.scm` tree-sitter query file + a Go mapper**. The query captures CST nodes using a **shared, standardized capture vocabulary**; the Go mapper turns captures into core facts and builds SCIP descriptor suffixes.

**Standard capture vocabulary (shared across all languages — this is the normalization to the neutral core):**

- `@definition.<kind>` — a definition occurrence (`function`, `method`, `type`, `field`, `variable`, `module`, …).
- `@reference.<role>` — a reference occurrence (`call`, `read`, `write`, `type`).
- `@scope.<kind>` — a lexical scope.
- `@import` — an import occurrence.
- `@name` — the identifier node used for the descriptor and `name` column.

**Go mapper responsibilities (per language):** build the descriptor **suffix** from the capture hierarchy; assign `symbol_kind`/`role`; resolve same-file references to local definitions; emit `occurrence`/`scope`/`contains`/`defines`/`references_local` facts. Anything requiring another file is emitted as an **unresolved** reference carrying its target descriptor.

**Output & durability:**

- Facts are serialized to a **protobuf artifact** whose message types are generated from the SDL (SDL → `.proto` → **buf** → Go); written to the shared volume (§10); the map task checkpoints the artifact **key**, never the blob.
- One DBOS task per file on a durable, Postgres-backed queue with concurrency limits; a failing file is retried in isolation; a poison file is flagged and **skipped**, never blocking the batch.
- **Depth:** structural only. A `go/types`-style semantic producer may later populate overlay tables (e.g. precise `calls`, `implements`) without changing this contract.

**First language:** Go (M3), using gotreesitter’s Go grammar; the `go/types` overlay is its natural later refinement.

-----

## 6. Loading (Reduce Phase)

- **Granularity:** one reduce step per batch, a single DBOS transaction over the successful map subset.
- **Per file:** `DELETE FROM <table> WHERE file_id = $1`, then `CopyFrom` the file’s new rows straight into the **target** tables. No staging, no merge, no `ON CONFLICT` — delete-first guarantees no key collision.
- **sqlc shape:** generated `:copyfrom` (binary COPY into target) + generated `:exec` `delete-by-file`.
- **Load order:** vertices before edges within each file; intra-file edge endpoints are co-loaded.
- **Reduce sequence:** base load → core link (§7) → overlay producers (§4.2), all within the one transaction.
- **Atomicity & idempotency:** delete + COPY run inside one DBOS transaction (`RunAsTransaction`); on reduce failure the step re-runs deterministically from its checkpoint over the already-produced artifacts — no re-extraction.
- **Artifact GC:** on reduce success the batch’s artifacts are deleted from the shared volume; a **failed** batch keeps its artifacts for inspection/retry (they must persist until reduce succeeds).
- **Driver:** pgx.

-----

## 7. Linking (Materialized Cross-file Edges)

- **Model:** cross-file edges (`resolves_to`, `imports`, `calls`, `implements`, `type_defines`) are a **derived** layer written after load, not extracted. *(Lineage: Kythe/Glean derived predicates; Joern overlays.)*
- **Symbol index:** the join key is the SCIP `descriptor` already on base rows; the “index” is a btree on `descriptor` — no separate structure.
- **Incremental re-link:** on change, only the affected neighborhood is recomputed — files referencing the changed file’s definitions, plus definitions the changed file references — found by querying base facts on `descriptor`. *(Lineage: Glean incremental ownership, ~one unit per file.)*
- **Ownership & deletion:** a cross-file edge is owned by its **referencing** file; re-linking a file deletes its cross-file edges and recomputes them; a definition move triggers re-link of referencing files via the descriptor index.
- **Placement:** runs as a reduce step, once per batch over the union of affected neighborhoods.
- **Backstop:** a **full re-link** (delete all cross-file edges, recompute from base facts — the full-rebuild path) runs both on a **schedule** (nightly DBOS scheduled workflow) and **on demand** (manual trigger), to self-heal any drift from an incremental-invalidation bug.

-----

## 8. Query & MCP Surface

- **Owned by gopgql.** gopgql compiles the SDL to SQL/PGQ `GRAPH_TABLE` queries and ships a built-in **GraphQL-over-MCP** surface. CodiQ builds **no MCP layer of its own**.
- **Read-only.** The surface never writes; ingestion is the only writer and is never agent-exposed.
- **Cross-file navigation** is served from the materialized derived edges (fast reads), not resolved at query time.

-----

## 9. Orchestration (DBOS Transact, Go)

- **Pattern:** per-batch map-reduce. The batch workflow enqueues per-file extract tasks on a durable queue and collects results; each task is checkpointed, so recovery never re-runs completed work.
- **Failure handling:** per-task retry/timeout/flag; the batch proceeds over the successful subset. `waitFirst` is available; default is wait-for-all.
- **Enqueue atomicity:** the transactional outbox may make an enqueue atomic with a triggering DB write.
- **Isolation:** DBOS system tables live in a **separate database** on the same Postgres instance (`codiq_dbos`) from the graph tables, so checkpoints don’t contend with bulk `CopyFrom`.
- **No extra infra:** DBOS is an embedded library over the Postgres CodiQ already runs — no separate orchestration server.

-----

## 10. Storage & Infrastructure

- **Database:** plain **PostgreSQL 19** with SQL/PGQ, pinned to the **`postgres:19beta2`** tag (the same tag gopgql targets). No Kubernetes/CloudNativePG for now — that returns later (§16).
- **Extensions:** the core navigation graph needs only the stock image. Search extensions (pgvector HNSW, pg_search BM25) are an **overlay** concern (§4.2) requiring a custom image; deferred with the search overlays.
- **DBOS database:** a second database (`codiq_dbos`) on the same instance (§9).
- **Map artifacts:** a **shared local volume** (a Docker Compose named volume / bind mount) that the map writers and the reduce reader both mount. Artifacts are short-lived: survive until the batch’s reduce completes, then GC’d; the reduce reads them by key. *(Under Kubernetes later this becomes a ReadWriteMany PVC — the “shared filesystem” contract is unchanged.)*
- **Driver:** pgx.

-----

## 11. Deployment

### 11.1 Local runtime (Docker Compose)

Easy local deployment. The composition grows with the milestones: **M1** is `postgres` + `gopgql` (pulled from ghcr — it owns the schema/migrations and serves GraphQL/MCP); the **`codiq`** ingestion service is added at **M2** and the `artifacts` volume becomes relevant at **M5** (protobuf + disk offload).

```yaml
services:
  postgres:
    image: postgres:19beta2          # pinned, same tag as gopgql
    environment:
      POSTGRES_USER: codiq
      POSTGRES_PASSWORD: codiq
      POSTGRES_DB: codiq
    ports:
      - "5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
      - ./deploy/initdb:/docker-entrypoint-initdb.d:ro   # creates the codiq_dbos database
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U codiq"]
      interval: 5s
      timeout: 3s
      retries: 10

  gopgql:                            # M1 — schema/migrations + GraphQL/MCP, from ghcr
    image: ghcr.io/gaarutyunov/gopgql:<pinned>   # pin the tag; installed from ghcr
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      GOPGQL_DATABASE_URL: postgres://codiq:codiq@postgres:5432/codiq?sslmode=disable
    volumes:
      - ./schema:/schema:ro          # the CodiQ SDL, source of truth
    ports:
      - "8080:8080"                  # GraphQL/MCP surface

  codiq:                             # M2+ — ingestion (goroutine loader → DBOS → map-reduce → protobuf)
    build: .
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      CODIQ_DATABASE_URL: postgres://codiq:codiq@postgres:5432/codiq?sslmode=disable
      DBOS_DATABASE_URL:  postgres://codiq:codiq@postgres:5432/codiq_dbos?sslmode=disable   # from M3
      CODIQ_ARTIFACT_DIR: /artifacts                                                        # from M5
    volumes:
      - artifacts:/artifacts         # shared artifact volume (local stand-in for the RWX PVC), M5+

volumes:
  pgdata:
  artifacts:
```

`./deploy/initdb/01-dbos.sql` runs `CREATE DATABASE codiq_dbos;` (needed once DBOS lands at M3). The SDL under `./schema` is the single source of truth; gopgql applies migrations on startup.

### 11.2 Docs, landing & demo surface (GitHub Pages)

The public site — **landing page + docs + a live demo** (the demo mirrors gopgql’s: real SDL, generated schema, MCP querying inserted test data) — publishes to **GitHub Pages in branch mode** (`gh-pages`), with **a live preview on every PR**. This ships in **M1** and every milestone updates it.

- **Site:** Astro + **gaarutyunov/ui-kit** (landing + docs + demo).
- **Publish:** `peaceiris/actions-gh-pages` (branch mode).
- **PR previews:** `rossjrw/pr-preview-action` (`action: auto`).

```yaml
name: docs
on:
  push:
    branches: [main]
  pull_request:
    types: [opened, synchronize, reopened, closed]

permissions:
  contents: write
  pull-requests: write

jobs:
  build-deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with: { node-version: 20 }
      - name: Build docs (Astro + gaarutyunov/ui-kit)
        run: |
          npm ci
          npm run build   # outputs ./dist

      - name: Deploy to gh-pages
        if: github.event_name == 'push'
        uses: peaceiris/actions-gh-pages@v4
        with:
          github_token: ${{ secrets.GITHUB_TOKEN }}
          publish_branch: gh-pages
          publish_dir: ./dist

      - name: PR preview
        if: github.event_name == 'pull_request'
        uses: rossjrw/pr-preview-action@v1
        with:
          source-dir: ./dist
          action: auto
```

-----

## 12. Program structure (packages)

Plain Go: packages grouped by what they do, wired in `main`. Concrete types by default; an interface only where there are multiple implementations (per-language parsers behind `extract.Parser`) or to fake a dependency in a test. No ports/adapters/domain/driver layers, no arch linter.

```
codiq/
  cmd/codiq/            # main: flags, wiring, run
  facts/               # plain structs: File, Occurrence, Scope, Edge, Descriptor, FileFacts
  extract/             # dispatch by extension; one sub-package per language (package name = ext)
    extract.go         #   Parser interface + registry (map[".ext"]Parser)
    golang/            #   package golang  (".go" is a keyword — only exception to the ext rule)
      golang.go        #     mapper (captures -> facts)
      query.scm        #     tree-sitter query, embedded via //go:embed
    ts/ py/ …          #   package ts, py, … (each: <ext>.go + query.scm), added as languages land
  coord/               # package-coordinate resolvers: gomod.go, npm.go, pyproject.go
  store/               # postgres access (pgx + sqlc): insert, delete-by-file; sqlc/
  link/                # cross-file edges: rebuild.go, incremental.go
  artifact/            # protobuf write/read to disk (M5); proto/
  index/               # orchestration: walk + extract + store + link; dbos.go (M3+)
  schema/              # codiq.graphql, migrations/, proto/
  deploy/ docs/ features/ test/
```

Each language sub-package’s parser satisfies `extract.Parser` **structurally**, so it imports `facts`/`coord` but not `extract` (no import cycle); `extract` imports the language sub-packages to fill its registry. If a client (e.g. a pgx pool helper) becomes reusable, pull it to `pkg/postgres`; until then it lives in `store`. gopgql (ghcr) owns the schema + GraphQL/MCP; sqlc reads `schema/migrations`; DBOS lives in `index`; protobuf lives in `artifact`.

-----

## 13. Testing (BDD)

- **Harness:** godog + testcontainers-go, integrated with `go test` (JUnit output).
- **Containers:** `postgres:19beta2` (SQL/PGQ), plus the **gopgql ghcr image** for MCP-facing tests; from M5, artifacts use a temp dir in tests (no object store).
- **Feature files are canonical and single-source:** godog consumes `features/*.feature` directly — never duplicated, paraphrased, or re-transcribed.
- **No shims.** Every milestone lands with a **real, end-to-end integration test** that spins up the real dependencies (Postgres, gopgql) and exercises the actual slice — not a stub.
- **Features land with their milestone.** Each milestone adds its own `*.feature` scenarios; the suite grows with the product rather than being authored up front. Within a milestone, scenario-by-scenario, **each scenario a separate commit**.

-----

## 14. Milestones

Each milestone is a **working, integration-tested, deployed vertical slice**. The product works from M1; later milestones optimize the ingestion internals (§5–§9 are the end-state they converge on) without regressing the working feature. Every milestone updates the landing/docs/demo site (§11.2) and lands its own godog features (§13). Diff legend: `+` new · `~` changed · `-` removed.

### M1 — SDL + database + MCP (the data model is the product)

Author the first CodiQ SDL (the §4.4 core: nav + structural skeleton). gopgql (from **ghcr**) generates the full schema into `postgres:19beta2` and serves **GraphQL/MCP**. Ship a **demo mirroring gopgql’s** over **inserted test data**. No ingestion pipeline yet — anyone can insert rows directly; the value is the **data model + the MCP**.

```
+ schema/codiq.graphql             # §4.4 core vocabulary
+ schema/migrations/               # emitted by gopgql from the SDL
+ deploy/docker-compose.yml        # postgres + gopgql(ghcr)
+ deploy/initdb/01-dbos.sql        # CREATE DATABASE codiq_dbos (dormant until M3)
+ deploy/seed/seed.sql             # test data, no pipeline
+ docs/                            # Astro + ui-kit: landing + docs + demo
+ features/schema_mcp.feature
+ test/integration/m1_test.go
+ .github/workflows/{docs.yml,ci.yml}
```

**Data flow:** `SDL → gopgql: migrate → Postgres schema + graph view; gopgql serves MCP; seed.sql → Postgres; agent queries via MCP`.
**Test:** start postgres + gopgql, assert tables exist, seed, assert an MCP query returns a seeded descriptor. Ships docs deploy + landing page.

> **gopgql availability:** the compose pulls gopgql from **ghcr**. If gopgql has no stable release with a Docker image pushed to the registry yet, **install it from source** (build the binary / a local image in the compose) and proceed — do not block M1 on it. Swap back to the pinned ghcr image once it’s published; the rest of M1 is unaffected.

### M2 — Go extractor, monolithic loader

A single program: goroutines parse Go files with `gotreesitter`, insert into the M1 schema, then a full link rebuild materializes cross-file edges. **No DBOS, no batching, no protobuf, no disk.**

```
+ cmd/codiq/main.go
+ facts/facts.go                   # File, Occurrence, Scope, Edge, Descriptor, FileFacts
+ extract/extract.go               # Parser interface + registry (map[".ext"]Parser)
+ extract/golang/golang.go         # package golang — Go mapper (captures -> facts)
+ extract/golang/query.scm         # embedded via //go:embed
+ coord/gomod.go                   # read module path from go.mod
+ store/store.go                   # pgx + sqlc: ReplaceFile (delete-by-file + insert)
+ store/sqlc/                      # sqlc.yaml + generated
+ link/rebuild.go                  # full rebuild of cross-file edges (SQL)
+ index/index.go                   # walk + goroutine extract + store + rebuild
+ features/index_go.feature
+ test/integration/m2_test.go
~ deploy/docker-compose.yml        # + codiq service
```

```go
// extract/extract.go
type Parser interface { Parse(path string, src []byte, coord coord.Coord) facts.FileFacts }
var byExt = map[string]Parser{".go": golang.New()}   // golang.parser satisfies Parser structurally

// index/index.go
func Run(ctx, db, repo):
    c := coord.FromGoMod(repo)
    var wg errgroup.Group; wg.SetLimit(N)
    for path := range walkGo(repo):
        wg.Go(func(): return store.ReplaceFile(ctx, db, byExt[filepath.Ext(path)].Parse(path, read(path), c)))
    wg.Wait()
    return link.RebuildAll(ctx, db)
```

**Data flow:** `repo → (goroutine) extract → store.ReplaceFile → link.RebuildAll → gopgql MCP`.
**Test:** index a 2-file Go module; assert `main` → `Greeter` cross-file edge via MCP.

### M3 — Wrap in DBOS

Move the M2 loop into a DBOS workflow — same behavior, crash-resumable. Adds `codiq_dbos`.

```
+ index/dbos.go                    # IndexRepo workflow: walk -> (extract+store step)* -> link step
~ cmd/codiq/main.go                # start DBOS worker
~ deploy/docker-compose.yml        # + DBOS_DATABASE_URL
+ features/durable_index.feature
+ test/integration/m3_test.go      # M2 assertions + resume-after-crash
```

```go
func IndexRepo(ctx dbos.Context, repo string):
    files := dbos.RunAsStep(ctx,"walk", func(): return walkGo(repo))
    for f := range files:
        dbos.RunAsStep(ctx,"load:"+f, func(): return store.ReplaceFile(ctx, db, byExt[filepath.Ext(f)].Parse(f, read(f), c)))
    dbos.RunAsStep(ctx,"link", func(): return link.RebuildAll(ctx, db))
```

**Data flow:** same as M2; each stage a checkpointed step; crash resumes. DBOS stays in `index`.
**Test:** kill after K files, restart, assert resume + graph == M2.

### M4 — Map-reduce + batching (in-memory)

`index` becomes map (per-file extract on a durable queue) / reduce (batch store + link, one txn). Facts passed **in memory**. Poison-file skip.

```
~ index/dbos.go                    # map-reduce; queue "extract"; gather; reduce step
+ index/reduce.go                  # store batch + link, one txn
+ features/mapreduce.feature
+ test/integration/m4_test.go
```

```go
func IndexRepo(ctx, repo):
    files := step("walk", walk)
    q := queue("extract")
    hs := for f in files: q.Enqueue(ctx, extractStep, FileRef{repo,f})   // returns facts.FileFacts
    batch := []
    for h in hs:
        r,err := h.Get(ctx); if err { markPoison(f); continue }          // skip, don't block
        batch = append(batch, r)
    step("reduce", func(): reduce(ctx, db, batch))                        // in-memory facts

func reduce(ctx, db, batch):
    tx(func(): for ff in batch: store.ReplaceFile(tx, ff); return link.RebuildAll(tx))
```

**Test:** repo with one broken file → poison-skip + others indexed + reduce atomicity.

### M5 — Protobuf artifacts + disk offload (target ingestion shape)

`extract` step writes a protobuf artifact to the shared volume and returns a key; reduce reads by key; delete-on-success. Protobuf lives in `artifact` (facts↔proto). Coordinate resolver formalized. Pipeline now equals §5–§9.

```
+ schema/proto/                    # generated .proto from SDL
+ buf.yaml buf.gen.yaml
+ artifact/artifact.go             # Write(key, FileFacts) / Read(key) / Delete(keys)  (fs)
+ artifact/proto/                  # buf-generated Go
+ artifact/codec.go                # facts.FileFacts <-> proto
~ index/dbos.go                    # extract writes artifact, returns key; reduce reads keys
~ deploy/docker-compose.yml        # artifacts volume + CODIQ_ARTIFACT_DIR
+ features/artifacts.feature
+ test/integration/m5_test.go
```

```go
func extractStep(ctx, ref) string:                    // returns key
    ff  := byExt[filepath.Ext(ref.Path)].Parse(ref.Path, read(ref.Path), c)
    key := artifactKey(ref); artifact.Write(ctx, key, ff); return key

func reduce(ctx, db, keys):
    err := tx(func(): for k in keys: store.ReplaceFile(tx, artifact.Read(ctx,k)); return link.RebuildAll(tx))
    if err == nil { artifact.Delete(ctx, keys...) }   // failed batch keeps artifacts
```

**Data flow:** `files → queue → extract → protobuf on volume (+key) → reduce reads by key → store+link (txn) → delete on success`.
**Test:** artifacts written in map; crash after map → reduce consumes them w/o re-extract; deleted on success, kept on failure.

### M6 — TypeScript · M7 — Python

A new language is one sub-package in `extract` + a query + a resolver; no other change. Cross-language queries work with no schema change.

```
+ extract/ts/ts.go  extract/ts/query.scm  coord/npm.go         # M6
+ extract/py/py.go  extract/py/query.scm   coord/pyproject.go   # M7
~ extract/extract.go  # add to byExt (".ts" / ".py");  ~ coord/coord.go  # register resolver
+ features/index_<lang>.feature  test/integration/m{6,7}_test.go
```

**Test:** mixed-language repo; a cross-language query returns both.

### M8 — Incremental link

Add incremental re-link; keep full rebuild as the scheduled + on-demand backstop (Decision 17).

```
+ link/incremental.go              # RelinkNeighborhood(changed) via descriptor btree
~ index/reduce.go                  # link step -> link.RelinkNeighborhood(changed)
+ index/schedule.go                # DBOS nightly link.RebuildAll + on-demand trigger
+ features/incremental.feature
+ test/integration/m8_test.go
```

**Data flow (delta):** reduce re-links only the affected neighborhood; nightly/on-demand `RebuildAll` heals drift.
**Test:** change one file → only its neighborhood’s edges change; corrupt an edge → backstop restores it.

### M9+ — Additional languages (each a separate task)

Overlays are **out of scope** (§16). Beyond Go/TS/Python, every language is an **independent task** — no ordering dependency, no core changes. Same template as M6/M7:

```
+ extract/<ext>/<ext>.go        # package <ext> — mapper (captures -> facts)
+ extract/<ext>/query.scm       # tree-sitter query, embedded
+ coord/<ecosystem>.go          # package-coordinate resolver
~ extract/extract.go            # add ".<ext>" -> parser in byExt
~ coord/coord.go                # register resolver
+ features/index_<lang>.feature  test/integration/<lang>_test.go
```

Steps: wire the grammar; author `query.scm` against the shared capture vocabulary (`@definition.*`, `@reference.*`, `@scope.*`, `@import`, `@name`); write the mapper (descriptor suffix + role + same-file resolution); add the coordinate resolver; register both; land a feature + integration test. **Backlog (any order):** Rust (`rs`) · Java (`java`) · C# (`cs`) · Ruby (`rb`) · PHP (`php`) · C/C++ (`c`/`cpp`) · Kotlin (`kt`) · Swift (`swift`).

*Rationale: a working feature from M1, optimized bit by bit — monolithic loader → DBOS → map-reduce/batching (in-memory) → protobuf + disk offload → more languages (one task each) → incremental link.*

-----

## 15. Decision Log

|# |Decision                     |Choice                                                                        |Rationale / lineage                                                                                                                                     |
|--|-----------------------------|------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------|
|1 |Batch-insert generation      |**sqlc from goose migrations**                                                |Mature codegen; single source is the migration SQL gopgql emits; gopgql keeps the read view, sqlc reads plain vertex/edge DDL.                          |
|2 |Extractor output contract    |**Serialized fact artifact**                                                  |Dumb, file-local extractor; decoupled retryable stages; emit-then-ingest (Kythe/Glean/SCIP).                                                            |
|3 |Fact encoding                |**Protobuf from SDL, generated with buf**                                     |Typed, compact, language-neutral; SDL generates `.proto`, buf generates Go and enforces lint + breaking-change checks (which reinforce §4.6 versioning).|
|4 |Cross-file edges             |**Materialized link pass**                                                    |Fast reads via pre-resolved edges; extractor stays file-local. Derived-predicate/overlay lineage.                                                       |
|5 |Link invalidation            |**Incremental via symbol index**                                              |Work scales with change; index falls out of base facts (btree on descriptor). Glean lineage.                                                            |
|6 |Load path & ownership        |**Delete-by-file + CopyFrom, one transaction; all file-owned**                |Delete-first removes staging/merge/`ON CONFLICT`; file-disjointness makes deletion a single predicate. stack-graphs lineage.                            |
|7 |Pipeline granularity         |**Per-batch map-reduce**                                                      |Map = per-file extract (durable queue, isolated retry); reduce = batch delete+COPY+link (atomic, once per batch). DBOS-native.                          |
|8 |Read/MCP surface             |**gopgql’s GraphQL-over-MCP**                                                 |gopgql owns queries + MCP; no separate CodiQ layer; SDL-driven.                                                                                         |
|9 |Map-artifact storage         |**Shared volume** (local Compose volume now; RWX PVC under K8s later)         |Simplest; no extra service; artifacts short-lived until reduce completes.                                                                               |
|10|Extractor mechanism          |**gotreesitter (pure-Go runtime) + stanzas**                                  |One pure-Go, cross-compilable engine; broad grammar coverage; structural facts fit the split.                                                           |
|11|Vocabulary strategy          |**Neutral core + overlays later**                                             |One SDL, one loader, cross-language queries; per-language fidelity via additive overlays. SCIP-core / Joern-overlay lineage.                            |
|12|Symbol identity              |**SCIP-style descriptors**                                                    |Stable, debuggable, cross-repo/version aware; string-match resolution; stanzas emit suffix, batch supplies package prefix.                              |
|13|Core model scope             |**Navigation + structural skeleton**                                          |def/ref/impl/type-def + containment + import + call, all structural (CST + link); CFG/PDG deferred to overlays.                                         |
|14|Stanza authoring             |**tree-sitter `.scm` queries + Go mapper**                                    |Uses gotreesitter’s query engine; shared capture vocabulary normalizes to core; Go builds descriptors.                                                  |
|15|Package-coordinate resolution|**Per-ecosystem manifest resolvers**                                          |Accurate, SCIP-aligned; go.mod first; runs in the batch, extractor stays file-local.                                                                    |
|16|Artifact GC                  |**Delete on reduce success**                                                  |Leanest; no sweeper; artifacts persist exactly until reduce succeeds; failed batches retained for inspection.                                           |
|17|Full re-link backstop        |**Scheduled + on-demand**                                                     |Nightly DBOS scheduled full re-link plus a manual trigger, both reusing the full-rebuild path; self-heals incremental drift.                            |
|18|Overlay governance           |**Additive SDL module, producers as in-batch reduce steps** *(deferred — §16)*|Keyed by core descriptor, file-owned (reuses delete/join machinery); always consistent with the core in one pipeline.                                   |

**Reference pipelines surveyed:** Kythe, Glean, Sourcegraph SCIP/LSIF, GitHub stack-graphs, GitHub Semantic, CodeQL, Joern/CPG, Zoekt, rust-analyzer/salsa.

-----

## 16. Deferred / Future Scope

No open questions remain. The following are explicitly out of scope for this spec and will be handled in a future deployment/overlay spec:

- **Kubernetes / CloudNativePG.** When K8s lands: CNPG operator, the shared artifact volume becomes an RWX PVC (storage-class selection: NFS/CephFS/cloud RWX), and the sealed-catalog guaranteed path for CNPG + extensions returns.
- **Overlays.** Language-specific / deeper-analysis derived tables (`go/types` precise calls, CFG/PDG). The neutral core (Decision 11) stays overlay-ready and the intended governance is recorded (§4.2, Decision 18), but overlays are out of scope for the current plan; additional-language tasks are prioritized instead.
- **Search-extension image.** A custom Postgres image bundling pgvector (HNSW) + pg_search (BM25), built alongside the search overlays (§4.2).