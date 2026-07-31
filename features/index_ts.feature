# M6 — the TypeScript extractor (SPEC.md §14 M6).
#
# M2 proved that a repository on disk becomes a graph. M6 is the first milestone
# that tests a *claim about M1-M5's design* rather than adding a capability:
# "a new language is one sub-package in `extract` + a query + a resolver; no
# other change", and "cross-language queries work with no schema change". Every
# scenario below runs against the schema, the store, the link pass and the MCP
# surface exactly as the Go milestones left them — nothing in this file could
# pass if a second language had needed any of them changed.
#
# The corpus is extract/ts/testdata/greeter/, reused rather than duplicated and
# deliberately the same shape as the Go one: a `package.json` naming
# @codiq/greeter, a greeter.ts declaring the class and its method, and a main.ts
# that builds one and calls it. Two files, one call across the boundary between
# them. Scenarios that need more add to a copy, so the fixture stays the fixture.
#
# Two things about TypeScript that the descriptors below only make sense against:
#
#   * A module is a *file*, not a directory, and no clause in the source names
#     it. So a module's namespace is derived from its path — extension removed, a
#     trailing `index` segment dropped — and both sides of an import derive it
#     the same way without either reading the disk (extract/ts, moduleNamespace).
#   * The coordinate is `scip-typescript npm <name> <version>`, from package.json
#     (coord/npm.go). Nothing it can produce shares a prefix with `scip-go gomod
#     …`, which is the whole of why two ecosystems can share one `occurrence`
#     table.
#
# THE MILESTONE'S STATED TEST is the fourth scenario: one mixed-language
# repository, one index run, both languages queryable and nothing joining them.
# It could not be written when M6 first landed, and the reason is worth keeping
# because it is the one place the milestone's "no other change" claim turned out
# to be false. CodiQ resolved ONE coordinate per repository — index/index.go and
# index/dbos.go both called coord.Resolve(root) once and handed the result to
# every parser — and coord.Resolve tried the registered manifests in sorted
# order, so a repository holding both go.mod and package.json resolved to the Go
# coordinate and every TypeScript file in it was stamped `scip-go gomod …`.
# Worse than cosmetic: a Go package in `greeter/` and a TypeScript `greeter.ts`
# derive the same namespace, so a type called Greeter in each rendered the
# byte-identical descriptor, and the link pass joined them into a cross-language
# edge that was not real. A coordinate is a property of (repository, ecosystem)
# and not of a repository, so coord.Resolve now returns one per ecosystem
# (coord.Set) and index stamps each file with its own language's — a change to
# `index`, which is precisely the "no other change" §14 M6 claims a new language
# does not need. That is the milestone's answer to its own claim: everything
# below the loader held; the arity of the coordinate did not.

Feature: Indexing a TypeScript package
  As an agent navigating a polyglot codebase
  I want TypeScript in the same graph, under the same model, as Go
  So that one query language reaches every symbol regardless of what wrote it

  Background:
    Given an empty CodiQ graph

  # The M2 scenario, in another language and with not one line of the pipeline
  # between them changed. Neither file's extraction ever saw the other — §5's
  # file-local contract holds per language, not per product — so at the end of
  # extraction main.ts held a reference it could not resolve and greeter.ts held
  # a definition nobody had asked for, and the edge below exists because the link
  # pass (§7) matched their descriptor strings.
  #
  # What made the match possible is worth naming, because TypeScript gives the
  # mapper less than Go does. `g.greet()` says nothing about what `g` is; the
  # mapper reads the type off `new Greeter("world")` — syntax, not inference —
  # and that is enough to write `greeter/Greeter#greet().`, which is what
  # greeter.ts independently wrote for its own method.
  Scenario: A call is traversable across files to the definition it names
    Given the two-file TypeScript package
    When the package is indexed
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
                "name": "greet",
                "symbolKind": "method",
                "descriptor": "scip-typescript npm @codiq/greeter 1.0.0 greeter/Greeter#greet().",
                "definedIn": [
                  { "path": "greeter.ts" }
                ]
              }
            ]
          }
        ]
      }
      """

  # The descriptor is asserted on both sides of the edge, byte for byte, for the
  # reason index_go.feature gives: the day the two stop agreeing is the day
  # cross-file navigation silently returns nothing. Here it also carries the npm
  # coordinate, which is the part that has to be right for two ecosystems to
  # share a table — scheme, manager, package name and version, all four read out
  # of package.json by a resolver that changed nothing to slot in beside go.mod's.
  #
  # The second read is the same join one level up. A Go file's `package` clause
  # is what an import edge lands on; TypeScript writes no such clause, so the
  # stanza emits a `package` definition for the file *as a module*, whose
  # descriptor is its namespace. That is the only reason `imports` derives at all
  # for this language — and it needed no change to link, whose query still asks
  # for a `package` definition matched by a `package` reference.
  #
  # It does not ask which file the reference is in, and cannot: `definedIn` is
  # the inverse of `defines`, which runs file → *definition*, so hanging it off a
  # reference is an inner join on an edge that by construction never exists and
  # silently empties the list above it (schema_mcp.feature says the same thing
  # about traversals generally). The file-level claim is the step after it.
  Scenario: The cross-file join is on the descriptor and nothing else
    Given the two-file TypeScript package
    When the package is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "greet", role: "definition") {
          descriptor
          definedIn {
            path
            lang
            pkgScheme
            pkgManager
            pkgName
            pkgVersion
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
            "descriptor": "scip-typescript npm @codiq/greeter 1.0.0 greeter/Greeter#greet().",
            "definedIn": [
              {
                "path": "greeter.ts",
                "lang": "ts",
                "pkgScheme": "scip-typescript",
                "pkgManager": "npm",
                "pkgName": "@codiq/greeter",
                "pkgVersion": "1.0.0"
              }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "descriptor": "scip-typescript npm @codiq/greeter 1.0.0 greeter/Greeter#greet()."
              }
            ]
          }
        ]
      }
      """
    And an agent asks over MCP:
      """
      {
        occurrence(name: "greeter", role: "definition") {
          symbolKind
          descriptor
          definedIn {
            path
          }
          resolvedFrom {
            role
            name
            symbolKind
            descriptor
          }
        }
      }
      """
    And the answer is:
      """
      {
        "occurrence": [
          {
            "symbolKind": "package",
            "descriptor": "scip-typescript npm @codiq/greeter 1.0.0 greeter/",
            "definedIn": [
              { "path": "greeter.ts" }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "name": "greeter",
                "symbolKind": "package",
                "descriptor": "scip-typescript npm @codiq/greeter 1.0.0 greeter/"
              }
            ]
          }
        ]
      }
      """
    And "main.ts" imports "greeter.ts"

  # §14 M6's other claim: "cross-language queries work with no schema change",
  # over two repositories.
  #
  # Two trees go in, one Go and one TypeScript, indexed by two runs of the same
  # binary into the same database. Afterwards one `occurrence` table holds both,
  # under the same columns and the same roles, and either language's symbol is
  # reachable by the query an agent would already know how to write. No migration
  # ran between the runs and none exists for TypeScript.
  #
  # This used to stand in for the mixed-repository scenario that follows it,
  # which could not be written while a repository had exactly one coordinate. It
  # is kept because it is not the same claim: two runs over two roots is how a
  # graph spanning several repositories is built, and nothing else in the suite
  # covers a second run writing into a graph that is already populated.
  #
  # It is two reads rather than one, and the reason is the surface rather than
  # the claim: the GraphQL layer emits no ORDER BY, so a query matching a row per
  # language returns them in an order nothing guarantees, and an assertion on it
  # would be a coin flip. Each read below matches exactly one row.
  #
  # The last two steps are the half a traversal cannot state. "Both languages are
  # in one graph" and "nothing joined them" are claims about every row in every
  # table, and the second is the one that matters: the descriptor is the only
  # thing the link pass looks at, so if two ecosystems could ever render the same
  # string, cross-language navigation would return edges that are not real.
  Scenario: One graph holds two languages, and neither leaks into the other
    Given the Go module and the TypeScript package
    When both are indexed into the same graph
    And an agent asks over MCP:
      """
      {
        occurrence(name: "Greet", role: "definition") {
          descriptor
          definedIn {
            path
            lang
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
              { "path": "greeter.go", "lang": "go" }
            ]
          }
        ]
      }
      """
    And an agent asks over MCP:
      """
      {
        occurrence(name: "greet", role: "definition") {
          descriptor
          definedIn {
            path
            lang
          }
        }
      }
      """
    And the answer is:
      """
      {
        "occurrence": [
          {
            "descriptor": "scip-typescript npm @codiq/greeter 1.0.0 greeter/Greeter#greet().",
            "definedIn": [
              { "path": "greeter.ts", "lang": "ts" }
            ]
          }
        ]
      }
      """
    And the graph holds definitions written in both "go" and "ts"
    And no derived edge joins two package schemes

  # §14 M6's literal test: "mixed-language repo; a cross-language query returns
  # both". One repository, one manifest per ecosystem, one `codiq` run.
  #
  # The corpus is built to collide, because a corpus that cannot collide proves
  # nothing. A TypeScript module's namespace is its path with the extension
  # removed, so `greeter.ts` derives `greeter/` — exactly what the Go package in
  # `greeter/` derives — and both declare a type called Greeter. Everything in
  # the descriptor after the coordinate is therefore byte-identical between the
  # two languages, and the coordinate prefix is the only thing left keeping them
  # apart. That is not a contrived shape: a Go service with a TypeScript client
  # beside it is the ordinary repository this milestone is about.
  #
  # Two reads rather than one, for the reason the scenario above gives: the
  # GraphQL layer emits no ORDER BY, so each read below is written to match
  # exactly one row. Each also asks for `resolvedFrom`, so what is asserted is
  # not merely that both languages are present but that each one's reference
  # found its own definition and only it.
  #
  # The last four steps are the half a traversal cannot state, and the last of
  # them is what this scenario exists for. "No derived edge joins two languages"
  # is a claim about the absence of a row anywhere, and it is strictly stronger
  # than the scheme check beside it: the defect that kept this scenario out of M6
  # stamped both languages with the *same* scheme, so the phantom edge it
  # produced — main.go's reference to the Go Greeter resolving to the TypeScript
  # class as well — would have passed a scheme comparison unnoticed.
  Scenario: One mixed repository, indexed once, keeps its two languages apart
    Given the mixed-language repository
    When the repository is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "Greet", role: "definition") {
          descriptor
          definedIn {
            path
            lang
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
            "descriptor": "scip-go gomod github.com/foo/bar . greeter/Greeter#Greet().",
            "definedIn": [
              { "path": "greeter/greeter.go", "lang": "go" }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "descriptor": "scip-go gomod github.com/foo/bar . greeter/Greeter#Greet()."
              }
            ]
          }
        ]
      }
      """
    And an agent asks over MCP:
      """
      {
        occurrence(name: "greet", role: "definition") {
          descriptor
          definedIn {
            path
            lang
          }
          resolvedFrom {
            role
            descriptor
          }
        }
      }
      """
    And the answer is:
      """
      {
        "occurrence": [
          {
            "descriptor": "scip-typescript npm @codiq/mixed 2.0.0 greeter/Greeter#greet().",
            "definedIn": [
              { "path": "greeter.ts", "lang": "ts" }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "descriptor": "scip-typescript npm @codiq/mixed 2.0.0 greeter/Greeter#greet()."
              }
            ]
          }
        ]
      }
      """
    And "Greeter" is defined once in "go" and once in "ts"
    And the graph holds definitions written in both "go" and "ts"
    And no derived edge joins two package schemes
    And no derived edge joins two languages

  # `implements` is the derivation that looks most like it was written for Go:
  # Go's interface satisfaction is implicit, so nothing extracted can say
  # "implements" and link derives it structurally — a non-interface type
  # implements an interface when its method suffixes contain the interface's.
  #
  # TypeScript writes `implements` explicitly, and this scenario deliberately
  # does not use the clause: greeter.ts is the untouched fixture and knows
  # nothing about Speaker. The edge below exists because `Greeter#` has a
  # `greet().` member and `Speaker#` has one too, which is the same rule, run
  # over a language it was never written for and against a schema that has no
  # column for either language's notion of the relationship.
  #
  # It is also approximate in exactly the way §4.4 says: signatures are not
  # compared, so this asserts structural satisfaction and not TypeScript's.
  Scenario: An interface is implemented across a file boundary by the Go rule
    Given the two-file TypeScript package
    And the package also holds "speaker.ts":
      """
      export interface Speaker {
        greet(): string;
      }
      """
    When the package is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "Speaker", role: "definition") {
          symbolKind
          descriptor
          implementedBy {
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
            "symbolKind": "interface",
            "descriptor": "scip-typescript npm @codiq/greeter 1.0.0 speaker/Speaker#",
            "implementedBy": [
              {
                "name": "Greeter",
                "symbolKind": "type",
                "descriptor": "scip-typescript npm @codiq/greeter 1.0.0 greeter/Greeter#",
                "definedIn": [
                  { "path": "greeter.ts" }
                ]
              }
            ]
          }
        ]
      }
      """
