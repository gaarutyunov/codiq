# M1 — the data model is the product (SPEC.md §14 M1).
#
# There is no ingestion pipeline yet. What M1 claims is that the SDL compiles to
# a real schema, that the schema carries a real property graph, and that an
# agent can read a symbol back out of it over MCP. Each scenario below is one of
# those claims, checked against the real thing: postgres:19beta2, the migrations
# in schema/migrations/, gopgql serving over HTTP, and deploy/seed/seed.sql for
# the corpus. Nothing here is stubbed (SPEC.md §13, "no shims").

Feature: The CodiQ core model, served over MCP
  As an agent navigating a codebase
  I want the SCIP-style occurrences and their edges exposed as a graph
  So that I can answer "who calls this, and where is it defined" in one query

  Background:
    Given the CodiQ stack is running

  # The schema is the deliverable, so its shape is asserted directly rather than
  # inferred from a query succeeding. "Exactly" matters: gopgql generates the
  # tables from the SDL, and a table nobody named in schema/codiq.graphql
  # appearing here would mean the SDL had stopped being the source of truth.
  Scenario: The core model materialises from the SDL
    Then the database holds exactly these tables:
      | table               |
      | file                |
      | occurrence          |
      | scope               |
      | defines             |
      | contains_scope      |
      | contains_occurrence |
      | references_local    |
      | resolves_to         |
      | imports             |
      | calls               |
      | implements          |
      | type_defines        |
    And the migration history is recorded
    And the property graph "app_graph" is queryable

  # SPEC.md §14 M1's literal acceptance: "assert an MCP query returns a seeded
  # descriptor". The descriptor is the whole identity model (§4.3) — a
  # human-readable string that resolution is a match on — so reading one back
  # intact is what proves the model survived the round trip.
  Scenario: An agent reads a seeded symbol back over MCP
    Given the demo corpus is seeded
    When an agent asks over MCP:
      """
      {
        occurrence(name: "Store", role: "definition") {
          descriptor
          symbolKind
          name
          rangeStart
        }
      }
      """
    Then the answer is:
      """
      {
        "occurrence": [
          {
            "descriptor": "scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Store#",
            "symbolKind": "type",
            "name": "Store",
            "rangeStart": 45
          }
        ]
      }
      """

  # The point of materialising cross-file edges (§7) is that navigation is a
  # read, not a resolution: one query walks from a call site in cmd/codiq to the
  # file that declares the callee, without the agent ever matching a descriptor
  # itself.
  Scenario: A call is traversable across files to the file that defines the callee
    Given the demo corpus is seeded
    When an agent asks over MCP:
      """
      {
        occurrence(name: "main", role: "definition") {
          name
          calls {
            name
            descriptor
            definedIn {
              path
            }
          }
        }
      }
      """
    Then the answer is:
      """
      {
        "occurrence": [
          {
            "name": "main",
            "calls": [
              {
                "name": "Put",
                "descriptor": "scip-go gomod github.com/gaarutyunov/codiq v0.1.0 internal/graph/Store#Put().",
                "definedIn": [
                  { "path": "internal/graph/store.go" }
                ]
              }
            ]
          }
        ]
      }
      """

  # §4.4 gives `contains` one relationship with two possible destination types.
  # gopgql cannot express that in one edge table, so schema/codiq.graphql splits
  # it into contains_scope and contains_occurrence carrying the same label — and
  # that split is only harmless if a match on the label still spans both. If it
  # ever stops spanning both, half the containment skeleton disappears silently.
  Scenario: Lexical containment is one relationship spanning two tables
    Given the demo corpus is seeded
    When the graph is matched on the "contains" relationship
    Then it yields 13 edges
    And 2 of them lead to a scope
    And 11 of them lead to an occurrence

  # A traversal is an inner join, not an outer one: an occurrence with no
  # outgoing call is absent from the result rather than present with an empty
  # list. Seven definitions are seeded and exactly one of them calls anything, so
  # a single row coming back is the whole behaviour. Recorded because it is the
  # trap in every query written against this model — "no results" can mean the
  # deepest hop is missing, not the shallowest.
  Scenario: A traversal returns only the paths that exist all the way down
    Given the demo corpus is seeded
    When an agent asks over MCP:
      """
      {
        occurrence(role: "definition") {
          name
          calls {
            name
          }
        }
      }
      """
    Then the answer is:
      """
      {
        "occurrence": [
          {
            "name": "main",
            "calls": [
              { "name": "Put" }
            ]
          }
        ]
      }
      """

  # Everything above reads the database through gopgql, which reasons from the
  # SDL and so cannot notice that the database stopped agreeing with it. This is
  # the check that reads the property graph back out of PostgreSQL and compares.
  Scenario: The live property graph still conforms to the SDL
    When the database is checked against the SDL
    Then no drift is reported
