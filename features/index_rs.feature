# M9 — the Rust extractor (SPEC.md §14 M9+, the first of the additional-language
# tasks).
#
# M6 tested a claim about M1-M5's design — "a new language is one sub-package in
# `extract` + a query + a resolver; no other change" — and found one thing that
# was not true: a coordinate is a property of (repository, ecosystem) and index
# resolved one per repository. M7 tested the same claim with that fixed. This is
# the fourth language, and it is the first one whose *shape* is genuinely unlike
# the three before it. Every scenario below runs against the schema, the store,
# the link pass and the MCP surface exactly as the Python milestone left them.
#
# The corpus is extract/rs/testdata/greeter/, reused rather than duplicated and
# deliberately the same shape as the Go, TypeScript and Python ones: a
# `Cargo.toml` naming codiq-greeter, a `src/greeter.rs` declaring the type and
# its methods, and a `src/main.rs` that builds one and calls it. Two files, one
# call across the boundary between them. Scenarios that need more add to a copy,
# so the fixture stays the fixture.
#
# Four things about Rust that the descriptors below only make sense against:
#
#   * A crate's module tree is built out of `mod` items and need not follow the
#     filesystem. The stanza honours both producers: a file's own module is its
#     path — with `src/` dropped, `.rs` dropped, and a trailing `mod`, `lib` or
#     `main` segment collapsed to its directory — and an inline `mod foo { … }`
#     adds a real level below that. The collapse is the rule Python's
#     `__init__.py` and TypeScript's `index` already get, for the same reason,
#     and no filesystem probe either way (§2.5).
#   * `mod foo;` has no counterpart in any earlier language: nothing in Go,
#     TypeScript or Python has to *declare* that a sibling file exists. It is
#     what an `imports` edge between two Rust files is derived from, and the
#     fourth scenario is about it.
#   * Rust has two member operators and they ask different questions. `::`
#     resolves a path and is exact; `.` reaches a member of a value, so the
#     receiver has to be typed — which the mapper does from an annotation or an
#     initialiser, never by inference.
#   * The coordinate is `scip-rust cargo <name> <version>`, from Cargo.toml's
#     `[package]` table (coord/cargo.go). Nothing it can produce shares a prefix
#     with `scip-go gomod …`, `scip-typescript npm …` or `scip-python pip …`,
#     which is the whole of why four ecosystems can share one `occurrence` table.
#
# THE MILESTONE'S STATED TEST is the third scenario, and it is a strictly harder
# corpus than M7's. There, a TypeScript `greet()` and a Python `greet()` rendered
# byte-identical suffixes and the coordinate was the only thing keeping them
# apart. Here *three* of the four do.

Feature: Indexing a Rust crate
  As an agent navigating a polyglot codebase
  I want Rust in the same graph, under the same model, as Go, TypeScript and Python
  So that one query language reaches every symbol regardless of what wrote it

  Background:
    Given an empty CodiQ graph

  # The M2 scenario in a fourth language, with not one line of the pipeline
  # between them changed. Neither file's extraction ever saw the other — §5's
  # file-local contract holds per language, not per product — so at the end of
  # extraction main.rs held a reference it could not resolve and greeter.rs held
  # a definition nobody had asked for, and the edge below exists because the link
  # pass (§7) matched their descriptor strings.
  #
  # What made the match possible is the `.` operator's one hard requirement:
  # `g.greet()` says nothing about what `g` is, so the mapper reads the type off
  # the binding's initialiser — here a struct literal, whose type is written in
  # the source — and writes `greeter/Greeter#greet().`, which is what greeter.rs
  # independently wrote for its own method. Being wrong about that is safe rather
  # than merely unlikely: a type's descriptor ends `#` and a callable's ends
  # `().`, so a guess in either direction matches no definition instead of the
  # wrong one.
  Scenario: A call is traversable across files to the definition it names
    Given the two-file Rust crate
    When the crate is indexed
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
                "descriptor": "scip-rust cargo codiq-greeter 1.0.0 greeter/Greeter#greet().",
                "definedIn": [
                  { "path": "src/greeter.rs" }
                ]
              }
            ]
          }
        ]
      }
      """

  # The descriptor is asserted on both sides of the edge, byte for byte, for the
  # reason index_go.feature gives: the day the two stop agreeing is the day
  # cross-file navigation silently returns nothing. Here it also carries the
  # cargo coordinate, which is the part that has to be right for four ecosystems
  # to share a table — scheme, manager, package name and version, all four read
  # out of Cargo.toml by a resolver that changed nothing to slot in beside
  # go.mod's, package.json's and pyproject.toml's.
  #
  # The second read is the same join one level up, and it is where Rust differs
  # from every language before it. A Go file's `package` clause is what an import
  # edge lands on; TypeScript and Python write no such clause, so the stanza
  # emits a `package` definition for the file *as a module*. Rust writes no
  # clause either — but it does write the other side twice, once as `mod
  # greeter;` and once as `use crate::greeter::Greeter;`, and both have to derive
  # the same namespace as the file they name. Both entries below are that claim.
  Scenario: The cross-file join is on the descriptor and nothing else
    Given the two-file Rust crate
    When the crate is indexed
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
            "descriptor": "scip-rust cargo codiq-greeter 1.0.0 greeter/Greeter#greet().",
            "definedIn": [
              {
                "path": "src/greeter.rs",
                "lang": "rs",
                "pkgScheme": "scip-rust",
                "pkgManager": "cargo",
                "pkgName": "codiq-greeter",
                "pkgVersion": "1.0.0"
              }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "descriptor": "scip-rust cargo codiq-greeter 1.0.0 greeter/Greeter#greet()."
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
            "descriptor": "scip-rust cargo codiq-greeter 1.0.0 greeter/",
            "definedIn": [
              { "path": "src/greeter.rs" }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "name": "greeter",
                "symbolKind": "package",
                "descriptor": "scip-rust cargo codiq-greeter 1.0.0 greeter/"
              },
              {
                "role": "reference",
                "name": "greeter",
                "symbolKind": "package",
                "descriptor": "scip-rust cargo codiq-greeter 1.0.0 greeter/"
              }
            ]
          }
        ]
      }
      """
    And "src/main.rs" imports "src/greeter.rs"

  # §14 M9+'s inherited test — "mixed-language repo; a cross-language query
  # returns both" — with four languages rather than two. One repository, one
  # manifest per ecosystem, one `codiq` run.
  #
  # The corpus is built to collide as hard as four languages can be made to,
  # because a corpus that cannot collide proves nothing. All four derive the
  # namespace `greeter/` — Go from the directory, TypeScript from `greeter.ts`,
  # Python from `greeter.py`, Rust from `src/greeter.rs` with `src/` dropped —
  # and all four declare a type called `Greeter`. M7 went one further than M6 by
  # making two of the *methods* collide; this makes three, so
  # `greeter/Greeter#greet().` is written byte for byte by TypeScript, Python and
  # Rust and nothing but four leading words separates them. That is not
  # contrived: a Go service with a TypeScript client, a Python client and a Rust
  # client beside it is the ordinary repository this milestone is about, and
  # lowercase `greet` is what all three of those languages would call the method.
  #
  # Each read below is written to match exactly one row, for the reason
  # index_ts.feature gives — the GraphQL layer emits no ORDER BY, so a query
  # matching a row per language would be a coin flip. The four entry points are
  # therefore named apart (`main`, `boot`, `run`, `start`) while everything they
  # reach is named together, which is what lets each traversal ask the question
  # that matters: not "is this language present" but "did *this* language's call
  # find its own definition and only it".
  #
  # The last five steps are the half a traversal cannot state. The first of them
  # is what makes the rest non-vacuous — it asserts the collision is real across
  # all three of the languages that share a suffix — and the last is what the
  # scenario exists for: "no derived edge joins two languages" is a claim about
  # the absence of a row anywhere, and it is strictly stronger than the scheme
  # check beside it, because the defect that kept a mixed repository out of M6
  # stamped two languages with the *same* scheme and would have passed a scheme
  # comparison unnoticed.
  Scenario: One mixed repository of four languages keeps all four apart
    Given the mixed-language repository of four languages
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
    And an agent asks over MCP:
      """
      {
        occurrence(name: "start", role: "definition") {
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
                "descriptor": "scip-rust cargo codiq-mixed-rs 4.0.0 greeter/Greeter#greet().",
                "definedIn": [
                  { "path": "src/greeter.rs", "lang": "rs" }
                ]
              }
            ]
          }
        ]
      }
      """
    And the "ts", "py" and "rs" definitions of "greet" differ only in their coordinate
    And "Greeter" is defined once in each of "go", "ts", "py" and "rs"
    And the graph holds definitions written in all of "go", "ts", "py" and "rs"
    And no derived edge joins two package schemes
    And no derived edge joins two languages

  # The namespace decision, end to end, and the scenario in this file with no
  # counterpart in any other language's — because no other language in this graph
  # writes down its module tree at all.
  #
  # `src/util/mod.rs` *is* the module `util`, so the file's namespace is its
  # directory's, exactly as `pkg/__init__.py` collapses to `pkg`. Nothing probes
  # the filesystem to decide that — the collapse is a function of the name `mod`
  # and of nothing else — which is what keeps extraction file-local while still
  # letting the two sides of a `mod` declaration agree.
  #
  # Four import edges make the claim checkable in every direction at once.
  # `mod util;` in the crate root resolves *down* to a directory module;
  # `pub mod text;` inside it resolves down again; `use super::LEVEL;` in
  # text.rs climbs back *up* by counting one level from the file's own module;
  # and `use crate::greeter::Greeter;` reaches across from an absolute path.
  # None of those namespaces is written down anywhere. Both sides derive theirs,
  # and the edges are the evidence they agreed.
  Scenario: A directory module is the module its mod.rs is
    Given the two-file Rust crate
    And the crate also holds "src/main.rs":
      """
      mod greeter;
      mod util;

      use crate::greeter::Greeter;

      fn main() {
          let g = Greeter { name: String::from("world") };
          let message = g.greet();
          println!("{}", message);
      }
      """
    And the crate also holds "src/util/mod.rs":
      """
      pub mod text;

      pub const LEVEL: u32 = 1;
      """
    And the crate also holds "src/util/text.rs":
      """
      use super::LEVEL;
      use crate::greeter::Greeter;

      pub fn shout(g: &Greeter) -> String {
          let _ = LEVEL;
          g.greet()
      }
      """
    When the crate is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "util", role: "definition") {
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
            "descriptor": "scip-rust cargo codiq-greeter 1.0.0 util/",
            "definedIn": [
              { "path": "src/util/mod.rs" }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "symbolKind": "package",
                "descriptor": "scip-rust cargo codiq-greeter 1.0.0 util/"
              },
              {
                "role": "reference",
                "symbolKind": "package",
                "descriptor": "scip-rust cargo codiq-greeter 1.0.0 util/"
              }
            ]
          }
        ]
      }
      """
    And "src/main.rs" imports "src/util/mod.rs"
    And "src/util/mod.rs" imports "src/util/text.rs"
    And "src/util/text.rs" imports "src/util/mod.rs"
    And "src/util/text.rs" imports "src/greeter.rs"

  # `implements` is the derivation that looks most like it was written for Go:
  # Go's interface satisfaction is implicit, so nothing extracted can say
  # "implements" and link derives it structurally — a non-interface type
  # implements an interface when its method suffixes contain the interface's.
  #
  # Rust is the language where that looks least likely to survive, because Rust's
  # `impl Trait for Type` is *explicit* and syntactically present: unlike Go's
  # implicit satisfaction and unlike Python's structural Protocol, a Rust type
  # says which traits it implements, and one would expect a stanza to want an
  # edge kind for saying so. It does not need one. An `impl` block's members are
  # members of the type it is for, so `Greeter#greet().` is what the mapper
  # writes and the Go rule reads it unmodified — and the `impl … for …` clause
  # itself is recorded as what it structurally is, a type reference to the trait,
  # so the explicit declaration stays navigable with no schema change at all.
  #
  # `trait` is a keyword, so recognising the interface needs none of the base
  # inspection the Python stanza does — which is what `symbolKind` below is
  # asserting and what link's derivation keys off. The derivation stays
  # approximate in exactly the way §4.4 says: signatures are not compared, so
  # this asserts structural satisfaction and not Rust's.
  #
  # speaker.rs is added without a `mod speaker;` declaration anywhere, and that
  # is deliberate: the unit CodiQ indexes is the file, and `implements` reads
  # method sets rather than imports, so the edge below owes nothing to the module
  # tree.
  Scenario: A trait is implemented across a file boundary by the Go rule
    Given the two-file Rust crate
    And the crate also holds "src/speaker.rs":
      """
      pub trait Speaker {
          fn greet(&self) -> String;
      }
      """
    When the crate is indexed
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
            "descriptor": "scip-rust cargo codiq-greeter 1.0.0 speaker/Speaker#",
            "implementedBy": [
              {
                "name": "Greeter",
                "symbolKind": "type",
                "descriptor": "scip-rust cargo codiq-greeter 1.0.0 greeter/Greeter#",
                "definedIn": [
                  { "path": "src/greeter.rs" }
                ]
              }
            ]
          }
        ]
      }
      """
