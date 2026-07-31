# M8 — incremental re-link and the full-rebuild backstop (SPEC.md §7, §14 M8).
#
# Every milestone before this one added something to the graph. M8 changes an
# answer that was already correct, which makes it the first one whose failure is
# silent: `link.RebuildAll` is a pure function of the base facts, so the derived
# layer has been right since M2 for a reason no amount of change could erode —
# there is no state to drift. An incremental re-link introduces some. A
# neighbourhood computed too small leaves a stale edge behind, and a stale edge
# does not look like a bug; it looks like an answer.
#
# So the claim every scenario here makes is the same one, and it is not "the
# incremental path works":
#
#   **after any change, the graph is the graph a full rebuild would have built.**
#
# That is what "the graph is what a full re-link would produce" means below. It
# reads the derived edges, runs the backstop over the same base facts, reads them
# again, and requires the two to be identical — rendered as endpoint descriptors
# rather than counted, because a re-link that keeps the row count while moving an
# edge to a different symbol is exactly the defect this milestone can introduce.
#
# ## What this file deliberately does not assert
#
# SPEC.md §14 M8 words its test as "change one file → only its neighbourhood's
# edges change". The first half is here; the second half **cannot be observed
# through the binary**, and pretending otherwise would be the worst kind of green
# test. `codiq <repo>` walks the whole tree and reduces every file it finds, and
# store.flatten mints fresh occurrence uuids on every load — so on any run of the
# real command *every* file is a changed file, the neighbourhood is the whole
# corpus by construction, and there are no untouched rows left to be untouched.
#
# The mechanism is nonetheless right, and the scoping is tested where a batch can
# actually be a subset of the corpus: link/incremental_test.go drives
# link.Batch directly, over six change shapes, and asserts both that the answer
# equals the rebuild's and that rows outside the neighbourhood keep their uuids.
# What is left for this file is the end-to-end half — the real binary, the real
# workflow, the real database — plus the two claims that are only true of the
# deployed system: that the backstop repairs drift, and that it does not collide
# with an index in flight.

Feature: Re-linking incrementally without changing the answer
  As an agent navigating a codebase that keeps changing
  I want the cross-file edges recomputed from the neighbourhood of what changed
  So that indexing stays cheap without the graph quietly going stale

  Background:
    Given an empty CodiQ graph

  # The ordinary case: a file changes what it points at, and the graph follows.
  #
  # The module gains a third file so the corpus has something to be wrong about.
  # `Speaker` is an interface `Greeter` satisfies structurally, which puts rows in
  # `implements` — the one derivation that has no neighbourhood at all and falls
  # back to the full rebuild inside the incremental path (link.Batch.Relink). A
  # corpus without it would leave that fallback untested end to end.
  Scenario: A changed reference is followed, and the graph still equals a rebuild
    Given the two-file Go module
    And the module also holds "speaker.go":
      """
      package main

      // Speaker is satisfied by anything that can greet.
      type Speaker interface {
              Greet() string
      }
      """
    When the module is indexed
    And "main.go" is rewritten as:
      """
      package main

      import "fmt"

      func main() {
              var s Speaker = &Greeter{Name: "world"}
              fmt.Println(s.Greet())
      }
      """
    And the module is indexed again
    Then the graph is what a full re-link would produce
    And an agent asks over MCP:
      """
      {
        occurrence(name: "Speaker", role: "definition") {
          symbolKind
          implementedBy {
            name
            definedIn {
              path
            }
          }
        }
      }
      """
    And the answer is:
      """
      {
        "occurrence": [
          {
            "symbolKind": "interface",
            "implementedBy": [
              {
                "name": "Greeter",
                "definedIn": [
                  { "path": "greeter.go" }
                ]
              }
            ]
          }
        ]
      }
      """

  # The shape SPEC.md §7 names by itself: "a definition move triggers re-link of
  # referencing files via the descriptor index". `Greet` leaves greeter.go for a
  # file that did not exist when main.go was indexed, and main.go — whose own text
  # did not change at all — has to end up pointing at the new home.
  #
  # It is the change that distinguishes a re-link from a no-op most sharply. The
  # edge is not merely rebuilt with new ids: its far endpoint is in a different
  # file, so a neighbourhood that missed main.go would leave `main` calling
  # nothing, and the traversal below would come back empty rather than wrong —
  # which is why it is asserted over MCP as well as against the rebuild.
  Scenario: A definition that moved to another file is followed
    Given the two-file Go module
    When the module is indexed
    And "greeter.go" is rewritten as:
      """
      package main

      // Greeter greets by name.
      type Greeter struct {
              Name string
      }
      """
    And the module also holds "greet.go":
      """
      package main

      // Greet returns this Greeter's greeting.
      func (g *Greeter) Greet() string {
              return "hello, " + g.Name
      }
      """
    And the module is indexed again
    Then the graph is what a full re-link would produce
    And an agent asks over MCP:
      """
      {
        occurrence(name: "main", role: "definition") {
          name
          calls {
            name
            definedIn {
              path
            }
          }
        }
      }
      """
    And the answer is:
      """
      {
        "occurrence": [
          {
            "name": "main",
            "calls": [
              {
                "name": "Greet",
                "definedIn": [
                  { "path": "greet.go" }
                ]
              }
            ]
          }
        ]
      }
      """

  # SPEC.md §14 M8's second stated test, and the reason Decision 17 keeps the full
  # rebuild after M8 has an incremental path: "corrupt an edge → backstop restores
  # it".
  #
  # The corruption is deliberately of both kinds, because they fail differently
  # and only one of them is visible to a count. A deleted row is a navigation that
  # silently returns nothing; an invented row is a navigation that silently
  # returns the wrong symbol, and it is the one an incremental-invalidation bug
  # actually produces. The backstop has to undo both, and "the graph is exactly
  # what it was" is the whole assertion — every row, every uuid, every rendered
  # edge.
  Scenario: The backstop repairs a graph that has drifted
    Given the indexed two-file Go module
    When a derived edge is deleted and a false one invented
    Then the graph is not what it was
    When the link backstop runs on demand
    Then the graph is exactly what it was before

  # The hazard the schedule creates, which is the part most likely to become a
  # rare and confusing production failure.
  #
  # A full rebuild empties every derived table, and the derived tables' endpoints
  # are plain foreign keys into `occurrence` rows a loader may be replacing at
  # that moment — so the two transactions cannot both win, and one of them loses
  # with SQLSTATE 23503. That is a failure two concurrent indexers already have;
  # what Decision 17 adds is a timer that reproduces it unattended, at 03:00, on
  # whichever night an index happens to be running.
  #
  # The interlock is a reader/writer advisory lock: every writer of the base facts
  # takes it shared, the rebuild takes it exclusive. Both halves are asserted,
  # because only asserting the first would be satisfied by an interlock that
  # serializes every index in the system and nothing here would notice.
  Scenario: The backstop waits for an index in flight, and loaders do not wait for each other
    Given the indexed two-file Go module
    When an index holds its transaction open
    Then the link backstop blocks
    And a second index does not block
    When the index in flight finishes
    Then the link backstop runs to completion
    And the graph is exactly what it was before
