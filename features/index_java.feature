# M9 — the Java extractor (SPEC.md §14 M9+, an additional-language task and the
# fifth language in this graph).
#
# M6 tested a claim about M1-M5's design — "a new language is one sub-package in
# `extract` + a query + a resolver; no other change" — and found one thing that
# was not true: a coordinate is a property of (repository, ecosystem) and index
# resolved one per repository. M7, M9-Rust and this task have tested the same
# claim with that fixed. Every scenario below runs against the schema, the store,
# the link pass and the MCP surface exactly as the Rust task left them.
#
# The corpus is extract/java/testdata/greeter/, reused rather than duplicated and
# deliberately the same shape as the Go, TypeScript, Python and Rust ones: a
# `pom.xml` naming com.example:codiq-greeter, a `Greeter.java` declaring the type
# and its methods, and a `Main.java` that builds one and calls it. Two files, one
# call across the boundary between them. Scenarios that need more add to a copy,
# so the fixture stays the fixture.
#
# Five things about Java that the descriptors below only make sense against:
#
#   * A file's namespace is the one it *declares*. `package com.example.greeter;`
#     is written at the top of the file, and the namespace is that name with its
#     dots turned into slashes. Java is the first language since Go whose file
#     says which package it is in — TypeScript, Python and Rust all derive it
#     from the path — and the declaration is preferred over the path precisely
#     because the two disagree: the fixture is laid out the way Maven requires,
#     under `src/main/java/`, and no `import` anywhere writes that prefix. Nothing
#     probes the filesystem either way (§2.5).
#   * A type declared inside a type is another `#` level of the descriptor. No
#     earlier language in this graph has nested types at all, and the fourth
#     scenario is about them.
#   * Java has one member operator where Rust has two, and `a.b` is a field of a
#     value, a member of a type, or a segment of a package name with nothing in
#     the syntax to say which. The mapper decides by what the left half resolves
#     to, and where it cannot the descriptor writes SCIP's "." for the type.
#   * Java declares `implements` explicitly, unlike Go's implicit satisfaction.
#     The last scenario is about that clause needing no new edge kind.
#   * The coordinate is `scip-java maven <groupId>:<artifactId> <version>`, from
#     pom.xml (coord/maven.go). Nothing it can produce shares a prefix with
#     `scip-go gomod …`, `scip-typescript npm …`, `scip-python pip …` or
#     `scip-rust cargo …`, which is the whole of why five ecosystems can share
#     one `occurrence` table.
#
# THE MILESTONE'S STATED TEST is the third scenario, and it is a strictly harder
# corpus than the Rust task's. There, three of four languages rendered
# byte-identical suffixes for `greet` and the coordinate was the only thing
# keeping them apart. Here *four* of the five do.

Feature: Indexing a Java artifact
  As an agent navigating a polyglot codebase
  I want Java in the same graph, under the same model, as Go, TypeScript, Python and Rust
  So that one query language reaches every symbol regardless of what wrote it

  Background:
    Given an empty CodiQ graph

  # The M2 scenario in a fifth language, with not one line of the pipeline
  # between them changed. Neither file's extraction ever saw the other — §5's
  # file-local contract holds per language, not per product — so at the end of
  # extraction Main.java held a reference it could not resolve and Greeter.java
  # held a definition nobody had asked for, and the edge below exists because the
  # link pass (§7) matched their descriptor strings.
  #
  # What made the match possible is the one hard requirement of a single member
  # operator: `g.greet()` says nothing about what `g` is, so the mapper reads the
  # type off the declaration — `Greeter g = …`, where Java writes the type down
  # and never makes it inferable — and writes
  # `com/example/greeter/Greeter#greet().`, which is what Greeter.java
  # independently wrote for its own method. Being wrong about that is safe rather
  # than merely unlikely: a type's descriptor ends `#` and a callable's ends
  # `().`, so a guess in either direction matches no definition instead of the
  # wrong one.
  #
  # `System.out.println(message)` is in the same method and produces no second
  # entry, which is the honest half of the same mechanism: `System.out` is a
  # field of a JDK type, its own type is not written anywhere in this file, and a
  # descriptor with "." for the receiver matches nothing rather than guessing.
  Scenario: A call is traversable across files to the definition it names
    Given the two-file Java artifact
    When the artifact is indexed
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
                "descriptor": "scip-java maven com.example:codiq-greeter 1.0.0 com/example/greeter/Greeter#greet().",
                "definedIn": [
                  { "path": "src/main/java/com/example/greeter/Greeter.java" }
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
  # Maven coordinate, which is the part that has to be right for five ecosystems
  # to share a table — scheme, manager, package name and version, all four read
  # out of pom.xml by a resolver that changed nothing to slot in beside go.mod's,
  # package.json's, pyproject.toml's and Cargo.toml's. The package name is
  # Maven's own two-part spelling, `groupId:artifactId`, because that is the
  # string that names an artifact and half of it would name nothing.
  #
  # The second read is the same join one level up, and it is the namespace
  # decision made checkable. `Greeter.java` sits at
  # `src/main/java/com/example/greeter/` and declares `package
  # com.example.greeter;`; `Main.java` writes `import com.example.greeter.Greeter;`
  # and has never seen it. The path would have yielded
  # `src/main/java/com/example/greeter/` on one side and the import would have
  # yielded `com/example/greeter/` on the other, and there would be no edge at
  # all. Both entries below are that claim.
  Scenario: The cross-file join is on the descriptor and nothing else
    Given the two-file Java artifact
    When the artifact is indexed
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
            "descriptor": "scip-java maven com.example:codiq-greeter 1.0.0 com/example/greeter/Greeter#greet().",
            "definedIn": [
              {
                "path": "src/main/java/com/example/greeter/Greeter.java",
                "lang": "java",
                "pkgScheme": "scip-java",
                "pkgManager": "maven",
                "pkgName": "com.example:codiq-greeter",
                "pkgVersion": "1.0.0"
              }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "descriptor": "scip-java maven com.example:codiq-greeter 1.0.0 com/example/greeter/Greeter#greet()."
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
            "descriptor": "scip-java maven com.example:codiq-greeter 1.0.0 com/example/greeter/",
            "definedIn": [
              { "path": "src/main/java/com/example/greeter/Greeter.java" }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "name": "greeter",
                "symbolKind": "package",
                "descriptor": "scip-java maven com.example:codiq-greeter 1.0.0 com/example/greeter/"
              }
            ]
          }
        ]
      }
      """
    And "src/main/java/com/example/app/Main.java" imports "src/main/java/com/example/greeter/Greeter.java"

  # §14 M9+'s inherited test — "mixed-language repo; a cross-language query
  # returns both" — with five languages rather than two. One repository, one
  # manifest per ecosystem, one `codiq` run.
  #
  # The corpus is built to collide as hard as five languages can be made to,
  # because a corpus that cannot collide proves nothing. All five derive the
  # namespace `greeter/` — Go from the directory, TypeScript from `greeter.ts`,
  # Python from `greeter.py`, Rust from `src/greeter.rs` with `src/` dropped, and
  # Java from a `package greeter;` clause that names it outright — and all five
  # declare a type called `Greeter`. The Rust task made three of the *methods*
  # collide; this makes four, so `greeter/Greeter#greet().` is written byte for
  # byte by TypeScript, Python, Rust and Java and nothing but four leading words
  # separates them. That is not contrived: a Go service with a TypeScript client,
  # a Python client, a Rust client and a Java client beside it is the ordinary
  # repository this milestone is about, and lowercase `greet` is what all four of
  # those languages would call the method.
  #
  # The Java caller assigns a public field rather than passing a constructor
  # argument, which is the one place the corpus is written for the assertion
  # rather than for idiom — and it is the Rust caller's struct-literal choice for
  # the same reason. A declared constructor is a member of its type named after
  # it, so `Greeter` would be the name of two definitions in one file and "defined
  # once in each language" would stop meaning what it says.
  #
  # Each read below is written to match exactly one row, for the reason
  # index_ts.feature gives — the GraphQL layer emits no ORDER BY, so a query
  # matching a row per language would be a coin flip. The five entry points are
  # therefore named apart (`main`, `boot`, `run`, `start`, `launch`) while
  # everything they reach is named together, which is what lets each traversal ask
  # the question that matters: not "is this language present" but "did *this*
  # language's call find its own definition and only it".
  #
  # The last five steps are the half a traversal cannot state. The first of them
  # is what makes the rest non-vacuous — it asserts the collision is real across
  # all four of the languages that share a suffix — and the last is what the
  # scenario exists for: "no derived edge joins two languages" is a claim about
  # the absence of a row anywhere, and it is strictly stronger than the scheme
  # check beside it, because the defect that kept a mixed repository out of M6
  # stamped two languages with the *same* scheme and would have passed a scheme
  # comparison unnoticed.
  Scenario: One mixed repository of five languages keeps all five apart
    Given the mixed-language repository of five languages
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
    And an agent asks over MCP:
      """
      {
        occurrence(name: "launch", role: "definition") {
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
                "descriptor": "scip-java maven com.codiq:mixed-java 5.0.0 greeter/Greeter#greet().",
                "definedIn": [
                  { "path": "src/main/java/greeter/Greeter.java", "lang": "java" }
                ]
              }
            ]
          }
        ]
      }
      """
    And the "ts", "py", "rs" and "java" definitions of "greet" differ only in their coordinate
    And "Greeter" is defined once in each of "go", "ts", "py", "rs" and "java"
    And the graph holds definitions written in all of "go", "ts", "py", "rs" and "java"
    And no derived edge joins two package schemes
    And no derived edge joins two languages

  # Nested types, and the scenario in this file with no counterpart in any other
  # language's — because no other language in this graph can declare a type
  # inside a type at all.
  #
  # The rule is that each named container contributes exactly one component, so
  # `Registry#Entry#label().` is what both files derive: Registry.java from
  # walking out of the method through `Entry` and `Registry` to the package, and
  # Lookup.java from splitting `com.example.greeter.Registry.Entry` on Java's own
  # naming convention — lowercase segments are the package, the first uppercase
  # one begins the type chain. Nothing in the grammar distinguishes
  # `a.b.C.D` naming a nested type from `a.b.c.D` naming a top-level one in a
  # deeper package; the convention is the whole of what does, and being wrong
  # costs a descriptor that matches nothing rather than one that matches
  # something else.
  #
  # The `calls` edge is the evidence the two sides agreed: `describe` reaches a
  # method two `#` levels down in a file it has never seen.
  Scenario: A nested class is a level of the descriptor, not a flattening
    Given the two-file Java artifact
    And the artifact also holds "src/main/java/com/example/greeter/Registry.java":
      """
      package com.example.greeter;

      public final class Registry {
          public static final class Entry {
              private final String text;

              public Entry(String text) {
                  this.text = text;
              }

              public String label() {
                  return this.text;
              }
          }
      }
      """
    And the artifact also holds "src/main/java/com/example/app/Lookup.java":
      """
      package com.example.app;

      import com.example.greeter.Registry.Entry;

      public final class Lookup {
          public static String describe() {
              Entry e = new Entry("first");
              return e.label();
          }
      }
      """
    When the artifact is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "label", role: "definition") {
          symbolKind
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
            "symbolKind": "method",
            "descriptor": "scip-java maven com.example:codiq-greeter 1.0.0 com/example/greeter/Registry#Entry#label().",
            "definedIn": [
              { "path": "src/main/java/com/example/greeter/Registry.java" }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "descriptor": "scip-java maven com.example:codiq-greeter 1.0.0 com/example/greeter/Registry#Entry#label()."
              }
            ]
          }
        ]
      }
      """
    And an agent asks over MCP:
      """
      {
        occurrence(name: "describe", role: "definition") {
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
    And the answer is:
      """
      {
        "occurrence": [
          {
            "calls": [
              {
                "name": "label",
                "descriptor": "scip-java maven com.example:codiq-greeter 1.0.0 com/example/greeter/Registry#Entry#label().",
                "definedIn": [
                  { "path": "src/main/java/com/example/greeter/Registry.java" }
                ]
              }
            ]
          }
        ]
      }
      """

  # `implements` is the derivation that looks most like it was written for Go:
  # Go's interface satisfaction is implicit, so nothing extracted can say
  # "implements" and link derives it structurally — a non-interface type
  # implements an interface when its method suffixes contain the interface's.
  #
  # Java is the language where that looks least likely to survive, because Java
  # writes the word down. `class Greeter implements Speaker` is as explicit as a
  # declaration gets — more so than Rust's `impl Trait for Type`, since Java puts
  # it in the class header — and one would expect a stanza to want an edge kind
  # for saying so. It does not need one. A Java class's members are written inside
  # the class, so `Greeter#greet().` is what the mapper writes and the Go rule
  # reads it unmodified — and the `implements` clause itself is recorded as what
  # it structurally is, a type reference to the interface, so the explicit
  # declaration stays navigable with no schema change at all.
  #
  # `interface` is a keyword, so recognising the interface needs none of the base
  # inspection the Python stanza does — which is what `symbolKind` below is
  # asserting and what link's derivation keys off. The derivation stays
  # approximate in exactly the way §4.4 says: signatures are not compared, so this
  # asserts structural satisfaction and not Java's. The constructor `Greeter` sits
  # in the same method set and does not disturb it, because containment is what
  # is asked and an interface has no constructor to be missing.
  #
  # Speaker.java is added rather than being part of the fixture, and Greeter.java
  # already names it: the base corpus declares `implements Speaker` against a
  # type no file defines, which is an unresolved reference and exactly what §4.3
  # says such a thing should be.
  Scenario: An interface is implemented across a file boundary by the Go rule
    Given the two-file Java artifact
    And the artifact also holds "src/main/java/com/example/greeter/Speaker.java":
      """
      package com.example.greeter;

      public interface Speaker {
          String greet();
      }
      """
    When the artifact is indexed
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
            "descriptor": "scip-java maven com.example:codiq-greeter 1.0.0 com/example/greeter/Speaker#",
            "implementedBy": [
              {
                "name": "Greeter",
                "symbolKind": "type",
                "descriptor": "scip-java maven com.example:codiq-greeter 1.0.0 com/example/greeter/Greeter#",
                "definedIn": [
                  { "path": "src/main/java/com/example/greeter/Greeter.java" }
                ]
              }
            ]
          }
        ]
      }
      """
