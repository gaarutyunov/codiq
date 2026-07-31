# M7 — the Python extractor (SPEC.md §14 M7).
#
# M6 tested a claim about M1-M5's design — "a new language is one sub-package in
# `extract` + a query + a resolver; no other change" — and found one thing that
# was not true: a coordinate is a property of (repository, ecosystem) and index
# resolved one per repository. M7 tests the same claim with that fixed, and a
# third language is where the answer stops being a coincidence. Every scenario
# below runs against the schema, the store, the link pass and the MCP surface
# exactly as the Go and TypeScript milestones left them.
#
# The corpus is extract/py/testdata/greeter/, reused rather than duplicated and
# deliberately the same shape as the Go and TypeScript ones: a `pyproject.toml`
# naming codiq-greeter, a greeter.py declaring the class and its method, and a
# main.py that builds one and calls it. Two files, one call across the boundary
# between them. Scenarios that need more add to a copy, so the fixture stays the
# fixture.
#
# Three things about Python that the descriptors below only make sense against:
#
#   * A module is a *file*, as in TypeScript — but Python looks like it has two
#     units of modularity, since a directory holding `__init__.py` is a package.
#     It has one: `__init__.py` *is* the module its directory is, so it collapses
#     to the directory, exactly as TypeScript's stanza drops a trailing `index`
#     segment. One rule, both cases, and no filesystem probe either way (§2.5).
#   * A relative import is path arithmetic against the file's own package: one
#     dot is that package, each further dot is one directory up. It is the one
#     import form Python leaves no room to guess at, and the fourth scenario is
#     about it.
#   * The coordinate is `scip-python pip <name> <version>`, from pyproject.toml's
#     `[project]` table (coord/pyproject.go). Nothing it can produce shares a
#     prefix with `scip-go gomod …` or `scip-typescript npm …`, which is the
#     whole of why three ecosystems can share one `occurrence` table.
#
# THE MILESTONE'S STATED TEST is the third scenario, and it is a strictly harder
# corpus than M6's. There, the two languages collided on the namespace and the
# type name but not on the member: `Greet` and `greet` are different strings, so
# a bug in the coordinate would still have left the two methods distinguishable.
# Here a TypeScript `greet()` and a Python `greet()` render descriptor suffixes
# that are byte-identical — `greeter/Greeter#greet().` on both sides — and the
# coordinate prefix is the *only* thing in the entire string keeping them apart.

Feature: Indexing a Python package
  As an agent navigating a polyglot codebase
  I want Python in the same graph, under the same model, as Go and TypeScript
  So that one query language reaches every symbol regardless of what wrote it

  Background:
    Given an empty CodiQ graph

  # The M2 scenario in a third language, with not one line of the pipeline
  # between them changed. Neither file's extraction ever saw the other — §5's
  # file-local contract holds per language, not per product — so at the end of
  # extraction main.py held a reference it could not resolve and greeter.py held
  # a definition nobody had asked for, and the edge below exists because the link
  # pass (§7) matched their descriptor strings.
  #
  # What made the match possible is worth naming, because Python gives the mapper
  # less than either language before it. `g.greet()` says nothing about what `g`
  # is, and `Greeter("world")` does not even say it is a construction — Python
  # has no `new`, so a class instantiation and a function call are the same
  # syntax. The mapper reads the type off the callee's name and writes
  # `greeter/Greeter#greet().`, which is what greeter.py independently wrote for
  # its own method. Being wrong about that is safe rather than merely unlikely: a
  # class descriptor ends `#` and a callable's ends `().`, so a guess in either
  # direction matches no definition instead of the wrong one.
  Scenario: A call is traversable across files to the definition it names
    Given the two-file Python package
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
                "descriptor": "scip-python pip codiq-greeter 1.0.0 greeter/Greeter#greet().",
                "definedIn": [
                  { "path": "greeter.py" }
                ]
              }
            ]
          }
        ]
      }
      """

  # The descriptor is asserted on both sides of the edge, byte for byte, for the
  # reason index_go.feature gives: the day the two stop agreeing is the day
  # cross-file navigation silently returns nothing. Here it also carries the pip
  # coordinate, which is the part that has to be right for three ecosystems to
  # share a table — scheme, manager, package name and version, all four read out
  # of pyproject.toml by a resolver that changed nothing to slot in beside
  # go.mod's and package.json's.
  #
  # The second read is the same join one level up. A Go file's `package` clause
  # is what an import edge lands on; Python writes no such clause, so the stanza
  # emits a `package` definition for the file *as a module*, whose descriptor is
  # its namespace. That is the only reason `imports` derives at all for this
  # language — and it needed no change to link, whose query still asks for a
  # `package` definition matched by a `package` reference.
  Scenario: The cross-file join is on the descriptor and nothing else
    Given the two-file Python package
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
            "descriptor": "scip-python pip codiq-greeter 1.0.0 greeter/Greeter#greet().",
            "definedIn": [
              {
                "path": "greeter.py",
                "lang": "py",
                "pkgScheme": "scip-python",
                "pkgManager": "pip",
                "pkgName": "codiq-greeter",
                "pkgVersion": "1.0.0"
              }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "descriptor": "scip-python pip codiq-greeter 1.0.0 greeter/Greeter#greet()."
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
            "descriptor": "scip-python pip codiq-greeter 1.0.0 greeter/",
            "definedIn": [
              { "path": "greeter.py" }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "name": "greeter",
                "symbolKind": "package",
                "descriptor": "scip-python pip codiq-greeter 1.0.0 greeter/"
              }
            ]
          }
        ]
      }
      """
    And "main.py" imports "greeter.py"

  # §14 M7's literal test: "mixed-language repo; a cross-language query returns
  # both" — with three languages rather than two. One repository, one manifest
  # per ecosystem, one `codiq` run.
  #
  # The corpus is built to collide as hard as three languages can be made to,
  # because a corpus that cannot collide proves nothing. All three derive the
  # namespace `greeter/` — Go from the directory, TypeScript from `greeter.ts`,
  # Python from `greeter.py` — and all three declare a type called `Greeter`. So
  # every Go/TypeScript/Python descriptor of that type is identical after the
  # coordinate, which is where M6 stopped. This goes one further: the TypeScript
  # and Python *methods* are both called `greet`, so two of the three render
  # `greeter/Greeter#greet().` byte for byte and nothing but four leading words
  # separates them. That is not contrived: a Go service with a TypeScript client
  # and a Python client beside it is the ordinary repository this milestone is
  # about, and lowercase `greet` is what both of those languages would call the
  # method.
  #
  # Each read below is written to match exactly one row, for the reason
  # index_ts.feature gives — the GraphQL layer emits no ORDER BY, so a query
  # matching a row per language would be a coin flip. The two entry points are
  # therefore named apart (`boot`, `run`) while everything they reach is named
  # together, which is what lets each traversal ask the question that matters:
  # not "is this language present" but "did *this* language's call find its own
  # definition and only it".
  #
  # The last five steps are the half a traversal cannot state. The first of them
  # is what makes the rest non-vacuous — it asserts the collision is real, that
  # the TypeScript and Python descriptors differ in their coordinate and nowhere
  # else — and the last is what the scenario exists for: "no derived edge joins
  # two languages" is a claim about the absence of a row anywhere, and it is
  # strictly stronger than the scheme check beside it, because the defect that
  # kept a mixed repository out of M6 stamped two languages with the *same*
  # scheme and would have passed a scheme comparison unnoticed.
  Scenario: One mixed repository of three languages keeps all three apart
    Given the mixed-language repository of three languages
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
        occurrence(name: "boot", role: "definition") {
          calls {
            name
            descriptor
            definedIn {
              path
              lang
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
            "calls": [
              {
                "name": "greet",
                "descriptor": "scip-typescript npm @codiq/mixed 2.0.0 greeter/Greeter#greet().",
                "definedIn": [
                  { "path": "greeter.ts", "lang": "ts" }
                ]
              }
            ]
          }
        ]
      }
      """
    And an agent asks over MCP:
      """
      {
        occurrence(name: "run", role: "definition") {
          calls {
            name
            descriptor
            definedIn {
              path
              lang
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
            "calls": [
              {
                "name": "greet",
                "descriptor": "scip-python pip codiq-mixed 3.0.0 greeter/Greeter#greet().",
                "definedIn": [
                  { "path": "greeter.py", "lang": "py" }
                ]
              }
            ]
          }
        ]
      }
      """
    And the "ts" and "py" definitions of "greet" differ only in their coordinate
    And "Greeter" is defined once in each of "go", "ts" and "py"
    And the graph holds definitions written in all of "go", "ts" and "py"
    And no derived edge joins two package schemes
    And no derived edge joins two languages

  # The namespace decision, end to end, and the one scenario in this file with no
  # counterpart in any other language's.
  #
  # Python's two apparent units of modularity are one: `pkg/__init__.py` is the
  # module `pkg`, so the file's namespace is its directory's and an `import pkg`
  # lands on it. Nothing probes the filesystem to decide that — the collapse is a
  # function of the name `__init__` and of nothing else — which is what keeps
  # extraction file-local while still letting the two sides of an import agree.
  #
  # The import below is relative, which is the form that makes the claim
  # checkable in both directions at once: `from . import LEVEL` in pkg/loud.py
  # resolves to `pkg/` by counting one dot from the file's own package, and
  # `from ..greeter import Greeter` climbs out of `pkg` to the root and lands on
  # greeter.py's `greeter/`. Neither namespace is written down anywhere; both
  # sides derive theirs, and the `imports` edges are the evidence they agreed.
  Scenario: A package is the module its __init__.py is
    Given the two-file Python package
    And the package also holds "pkg/__init__.py":
      """
      LEVEL = 1
      """
    And the package also holds "pkg/loud.py":
      """
      from . import LEVEL
      from ..greeter import Greeter


      class Loud(Greeter):
          pass
      """
    When the package is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "pkg", role: "definition") {
          symbolKind
          descriptor
          definedIn {
            path
          }
          resolvedFrom {
            role
            symbolKind
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
            "symbolKind": "package",
            "descriptor": "scip-python pip codiq-greeter 1.0.0 pkg/",
            "definedIn": [
              { "path": "pkg/__init__.py" }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "symbolKind": "package",
                "descriptor": "scip-python pip codiq-greeter 1.0.0 pkg/"
              }
            ]
          }
        ]
      }
      """
    And "pkg/loud.py" imports "pkg/__init__.py"
    And "pkg/loud.py" imports "greeter.py"

  # `implements` is the derivation that looks most like it was written for Go:
  # Go's interface satisfaction is implicit, so nothing extracted can say
  # "implements" and link derives it structurally — a non-interface type
  # implements an interface when its method suffixes contain the interface's.
  #
  # Python is the language where that stops being a reasonable approximation and
  # starts being the definition. `typing.Protocol` is structural typing: a class
  # satisfies a Protocol by having the methods, never by declaring it, and
  # greeter.py is the untouched fixture and knows nothing about Speaker. So the
  # edge below is not the Go rule applied to a language it was not written for —
  # it is the rule Python's own type system states, computed by a query written
  # for Go, against a schema that has no column for either language's notion of
  # the relationship.
  #
  # `interface` is not a Python keyword, so the stanza has to recognise the
  # declaration: a class whose bases include Protocol or ABC is one, which is
  # what `symbolKind` below is asserting and what link's derivation keys off. It
  # is also approximate in exactly the way §4.4 says: signatures are not
  # compared, so this asserts structural satisfaction and not Python's.
  Scenario: A protocol is implemented across a file boundary by the Go rule
    Given the two-file Python package
    And the package also holds "speaker.py":
      """
      from typing import Protocol


      class Speaker(Protocol):
          def greet(self) -> str: ...
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
            "descriptor": "scip-python pip codiq-greeter 1.0.0 speaker/Speaker#",
            "implementedBy": [
              {
                "name": "Greeter",
                "symbolKind": "type",
                "descriptor": "scip-python pip codiq-greeter 1.0.0 greeter/Greeter#",
                "definedIn": [
                  { "path": "greeter.py" }
                ]
              }
            ]
          }
        ]
      }
      """
