# M2 — the Go extractor and the monolithic loader (SPEC.md §14 M2).
#
# M1 proved the data model. M2 is the first milestone where a *repository* goes
# in: goroutines parse Go files, each file's facts replace that file's rows, and
# one link pass materialises the cross-file edges. Nobody inserts rows by hand
# any more, and nothing here is seeded — every assertion below is about a graph
# that the real indexer built out of real files on disk.
#
# The corpus is extract/golang/testdata/greeter/, reused rather than duplicated:
# a `go.mod` naming module github.com/foo/bar, a greeter.go declaring the type
# and its method, and a main.go that builds one and calls it. Two files, one
# call across the boundary between them — the smallest tree that has anything to
# link. Scenarios that change the module work on a copy, so the fixture stays
# the fixture.
#
# Everything runs against postgres:19beta2, the committed migrations and real
# gopgql, and the indexer is driven as `cmd/codiq` — the same binary the `codiq`
# service in deploy/docker-compose.yml runs (SPEC.md §13, "no shims").

Feature: Indexing a Go module
  As an agent navigating a codebase
  I want a repository on disk turned into the CodiQ graph
  So that a call I can only see half of in one file becomes a hop I can walk

  Background:
    Given an empty CodiQ graph

  # SPEC.md §14 M2's literal acceptance: "index a 2-file Go module; assert
  # `main` → `Greeter` cross-file edge via MCP".
  #
  # This is the whole milestone in one query. Neither file's extraction ever saw
  # the other — that is §5's file-local contract — so at the end of extraction
  # main.go held a reference it could not resolve and greeter.go held a
  # definition nobody had asked for. The edge below exists because the link pass
  # (§7) matched them, and the agent reads it rather than deriving it (§8).
  Scenario: A call is traversable across files to the definition it names
    Given the two-file Go module
    When the module is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "main", role: "definition") {
          name
          calls {
            name
            symbolKind
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
                "name": "Greet",
                "symbolKind": "method",
                "descriptor": "scip-go gomod github.com/foo/bar . Greeter#Greet().",
                "definedIn": [
                  { "path": "greeter.go" }
                ]
              }
            ]
          }
        ]
      }
      """

  # What the previous scenario does not show is *what the join was on*. §4.3
  # makes identity a string: the reference in main.go and the definition in
  # greeter.go each rendered the same descriptor from their own file alone, and
  # the link pass is a self-join on that column with no id lookup and no
  # project-wide symbol table anywhere in it. So the descriptor is asserted on
  # both sides of the edge, byte for byte, because the day they stop agreeing is
  # the day cross-file navigation silently returns nothing.
  Scenario: The cross-file join is on the descriptor and nothing else
    Given the two-file Go module
    When the module is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "Greet", role: "definition") {
          descriptor
          definedIn {
            path
          }
          resolvedFrom {
            role
            descriptor
          }
        }
      }
      """
    Then the answer is:
      """
      {
        "occurrence": [
          {
            "descriptor": "scip-go gomod github.com/foo/bar . Greeter#Greet().",
            "definedIn": [
              { "path": "greeter.go" }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "descriptor": "scip-go gomod github.com/foo/bar . Greeter#Greet()."
              }
            ]
          }
        ]
      }
      """

  # The other half of §5's contract: a use whose target is in the *same* file
  # never needed the link pass, because the extractor could see both ends in one
  # CST and emitted the edge itself. `Greeter.Name` is used in both files, so one
  # symbol carries both mechanisms at once — the greeter.go use arrived as
  # `references_local` at extraction, the main.go use as `resolves_to` after the
  # rebuild. That split is not an optimisation: if extraction ever emitted an
  # edge across a file boundary it would have read another file's facts, and
  # per-file incrementality (§2.5) would be gone. Hence the invariant, stated
  # over every edge in the graph rather than this one.
  Scenario: A same-file use is an edge at extraction, a cross-file use only after linking
    Given the two-file Go module
    When the module is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "Name", role: "definition") {
          descriptor
          definedIn {
            path
          }
          referencedLocallyBy {
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
            "descriptor": "scip-go gomod github.com/foo/bar . Greeter#Name.",
            "definedIn": [
              { "path": "greeter.go" }
            ],
            "referencedLocallyBy": [
              { "name": "Name" }
            ]
          }
        ]
      }
      """
    And no extracted edge crosses a file boundary
    And every derived edge crosses one

  # Indexing is something that runs on every push, so running it twice has to be
  # the same as running it once. §6's reduce phase replaces a file's rows rather
  # than merging them and keeps the file's id; §7's rebuild empties the derived
  # tables and recomputes them from base facts. Neither is conflict handling, so
  # neither can drift — and M4 splits this loop into map and reduce phases on the
  # assumption that it cannot, which is why the property is pinned here first.
  Scenario: Re-indexing an unchanged module changes nothing
    Given the indexed two-file Go module
    When the module is indexed again
    Then the graph is exactly what it was before
    And every file kept the identity it was first given

  # §5: "a poison file is flagged and skipped, never blocking the batch". The
  # skeleton in SPEC.md §14 M2 returns the loader's error straight into the
  # errgroup, which aborts the walk on the first bad file — the opposite — so
  # this is the regression for a bug the spec itself shipped.
  #
  # What a bad file can actually be is narrower than it sounds. tree-sitter is
  # error-tolerant: no file *content* makes the Go extractor fail, so a file of
  # prose parses to a tree with nothing in it rather than to an error. That is
  # the honest end-to-end shape of this requirement, and it is what is asserted.
  # The `ErrParseFailed` skip path — reachable only from a broken grammar or a
  # runtime failure — is covered by index/index_test.go, because reaching it from
  # here would mean injecting a parse error, and §13 allows no shims.
  Scenario: A file that is not Go at all does not stop the module from indexing
    Given the two-file Go module
    And the module also holds "notes.go":
      """
      This file is prose, not Go ((( ]]] }}} and it parses to nothing.
      """
    When the module is indexed
    Then the run indexed 3 files and loaded 3
    And "notes.go" defines nothing
    And an agent asks over MCP:
      """
      {
        occurrence(name: "main", role: "definition") {
          calls {
            name
          }
        }
      }
      """
    And the answer is:
      """
      {
        "occurrence": [
          {
            "calls": [
              { "name": "Greet" }
            ]
          }
        ]
      }
      """

  # A file's rows are deleted and rewritten, never added to (§6). The visible
  # consequence is that deleting code deletes graph: drop the method and the
  # definition is gone, the call that reached it is gone with it, and the
  # reference in the file that still calls it is left dangling — which is
  # correct, because that file did change and its own facts still say it calls
  # something. The file keeps its id throughout, since ids are the one thing a
  # replace must not churn.
  Scenario: A changed file's facts are replaced, not added to
    Given the indexed two-file Go module
    When "greeter.go" is rewritten as:
      """
      package main

      // Greeter no longer greets.
      type Greeter struct {
        Name string
      }
      """
    And the module is indexed again
    Then "greeter.go" kept the identity it was first given
    And an agent asks over MCP:
      """
      {
        occurrence(name: "Greet") {
          role
          descriptor
        }
      }
      """
    And the answer is:
      """
      {
        "occurrence": [
          {
            "role": "reference",
            "descriptor": "scip-go gomod github.com/foo/bar . Greeter#Greet()."
          }
        ]
      }
      """
    And nothing calls anything any more
