# The corpus: one database, many repositories, no false edge between them.
#
# codiq had never held two repositories at once, and the defect was worse than a
# missing column. Two things were keyed on a path alone:
#
#   1. `file` rows. `FileIDByPath` was `SELECT id FROM file WHERE path = @path`,
#      and `src/greeter/greeter.go` is a path most repositories have. Indexing
#      the second repository resolved to the first's row and replaced its
#      occurrences — a repository that emptied itself whenever a sibling was
#      indexed.
#
#   2. Descriptors, which is the half that matters. `coord.Resolve` walked
#      *upward* from the indexed directory with no repository bound, so a tree
#      with no manifest of its own inherited whichever manifest happened to sit
#      above it on that machine — with `Root` set there, so namespaces were
#      rendered relative to somebody else's directory. Measured on the machine
#      this was written for: twenty trees under `projects/` had no manifest any
#      resolver reads, and the nearest one above them was a stray
#      `package.json` in the operator's home directory. The link pass joins on
#      `occurrence.descriptor` and on nothing else (§7), so two unrelated
#      symbols rendering the same descriptor become a `resolves_to` or a `calls`
#      edge between two repositories that have never heard of each other.
#
# The fixture is built to reproduce (2) exactly, and the first scenario asserts
# the trap is *present* before asserting it is sprung — otherwise "the two
# coordinates differ" could pass for reasons having nothing to do with the
# bound, and a gate that cannot fail proves nothing.
#
# What makes the descriptors collide, spelled out because it is subtle: each
# repository is a directory called `repo` sitting under a parent that holds a
# nameless `package.json` and nothing else. Unbounded resolution finds that
# parent, stamps the Go files `scip-go gomod . .` — the ecosystem has no
# manifest, so it used to get Unknown for a name — and roots them at the
# parent. The namespace of `<parent>/repo/src/greeter/greeter.go` is then
# `repo/src/greeter/` in *both* trees, and both prefixes are `scip-go gomod . .`,
# so the two `Greeter` types render byte-identical descriptors. Bounding
# resolution at the indexed directory replaces both halves at once: the prefix
# becomes `scip-go gomod <corpus> .` and the namespace becomes `src/greeter/`.
#
# Two steps are SQL rather than MCP, for the reason m6_test.go gives: "no
# derived edge joins two corpora" is a statement about the absence of a row
# anywhere, which a traversal can never return, and `File` is not selectable by
# path over GraphQL. Every navigation claim goes over MCP.

Feature: One database holds many repositories

  Background:
    Given an empty CodiQ graph

  # 1.19 — the milestone's reason for existing.
  Scenario: Two repositories sharing a path keep their own rows and their own edges
    Given two repositories that share a path, a directory and a symbol name
    Then the two repositories really are indistinguishable by path
    When both are indexed under their own corpus
    Then "src/greeter/greeter.go" exists once in "alpha" and once in "beta"
    And each corpus has its own occurrences of "Greeter"
    And no derived edge joins two corpora

  # 1.20 — the coordinate half, stated on its own so a failure says which half
  # broke. A repository with no manifest, under a directory that has one.
  Scenario: A repository with no manifest is named after its corpus
    Given two repositories that share a path, a directory and a symbol name
    When both are indexed under their own corpus
    Then every file in "alpha" carries the package name "alpha"
    And every file in "beta" carries the package name "beta"
    And no descriptor in "alpha" appears in "beta"

  # 1.21 — the property that makes a shared database usable rather than merely
  # correct: indexing one repository is not an event in another's life.
  Scenario: Re-indexing one corpus leaves the others untouched
    Given two repositories that share a path, a directory and a symbol name
    When both are indexed under their own corpus
    And "alpha" is indexed again
    Then "beta" is exactly what it was before
    And every file in "alpha" kept the identity it was first given
