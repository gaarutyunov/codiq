# M9 — the Kotlin extractor (SPEC.md §14 M9+, an additional-language task and the
# tenth language in this graph).
#
# M6 tested a claim about M1-M5's design — "a new language is one sub-package in
# `extract` + a query + a resolver; no other change" — and found one thing that
# was not true. M7 and the six M9 language tasks after it tested it again with
# that fixed. This task tests it against the first language that arrives on a
# runtime another indexed language already owns, and the claim holds: no core
# package changed, and the one decision that had to be argued rather than copied
# is the coordinate.
#
# Six things about Kotlin that the descriptors below only make sense against.
#
#   * **The namespace is the `package` clause and never the path**, and Kotlin is
#     the language where insisting on that matters most. Java at least
#     conventionally mirrors its packages in the directory tree; Kotlin's style
#     guide tells you *not* to, so this project's `src/main/kotlin/greeter/`
#     declaring `package com.example.greeter` is the recommended shape rather
#     than a mistake. A path-derived namespace would disagree with every
#     `import` in the repository, and the two sides of an import have to derive
#     the same namespace or no `imports` edge exists at all.
#   * **Kotlin and Java are one runtime and two coordinates.** They compile to
#     one bytecode and share one classpath, which is the argument that made C and
#     C++ a single `scip-cc` — and here it gives the opposite answer, because
#     this stanza reads one of the two languages and they resolve from two
#     different manifests. What it costs is stated where it is felt: a Kotlin
#     class calling a Java class in the same repository is a call this index does
#     not resolve. The alternative is not "the call resolves"; it is "every
#     same-named symbol in the two languages resolves into every other".
#   * **A constructor has no name**, in either of its spellings, so it is not a
#     definition here. It needs none: Kotlin has no `new`, so `Greeter("world")`
#     is a call whose callee is the *type*, and it resolves to the type's own
#     descriptor — a definition that exists and that both files render
#     identically.
#   * **A companion object is transparent.** Kotlin reaches a companion's members
#     through the class — `Factory.create()` — and only rarely through
#     `Factory.Companion.create()`, so a `Companion#` component would be one the
#     definition side can compute and the reference side cannot, and every
#     cross-file call to a factory method would fail to join. The fifth scenario
#     is that decision, stated as a traversal.
#   * **A `.kts` is its own compilation unit.** kotlinc synthesizes a class per
#     script file, so two scripts declaring one name declare two things — and
#     descriptoring both as members of the root package would render one string
#     for both, which the link pass joins. The last scenario pins that apart.
#   * The coordinate is `scip-kotlin gradle <name> .`, read from a
#     `settings.gradle.kts` (coord/gradle.go). The version is **always** Unknown
#     and that is not a gap: Gradle states a version in `build.gradle.kts`, a
#     different file, and `Ecosystem.Manifest` is the one file `Resolve` stats to
#     find the root. What it costs is the same thing CMake's choice costs C: a
#     Kotlin repository with no `settings.gradle.kts` and no other language's
#     manifest resolves to ErrNoManifest and does not index.
#
# THE MILESTONE'S STATED TEST is the fourth scenario. Eight languages now write
# `greeter/Greeter#greet().` byte for byte, and Kotlin is the first to join that
# collision **without writing anything its own style guide disowns**: the PHP and
# C# halves of this corpus had to spell a namespace and a method against PSR-1
# and against .NET's conventions to collide at all, while `package greeter` and
# `fun greet()` are simply what a Kotlin author writes.

Feature: Indexing a Kotlin project
  As an agent navigating a polyglot codebase
  I want Kotlin in the same graph, under the same model, as Go, TypeScript, Python, Rust, Java, C#, Ruby, PHP and C/C++
  So that one query language reaches every symbol regardless of what wrote it

  Background:
    Given an empty CodiQ graph

  # The M2 scenario in a tenth language, with not one line of the pipeline
  # between them changed. Neither file's extraction ever saw the other — §5's
  # file-local contract holds per language, not per product — so at the end of
  # extraction Main.kt held a reference it could not resolve and Greeter.kt held
  # a definition nobody had asked for, and the edge below exists because the link
  # pass (§7) matched their descriptor strings.
  #
  # There is exactly one `calls` row, and the two things that are *not* in it are
  # the scenario's other half. `Greeter("world")` is a constructor call, which
  # resolves to the type and not to a callable, so it is a `type` reference that
  # link's `calls` derivation (`symbol_kind IN ('function', 'method')`) does not
  # select. And `println` produces no entry at all: it is the Kotlin standard
  # library's, this index owns no definition of it, and the reference carries a
  # foreign coordinate rather than being guessed at.
  Scenario: A call is traversable across files to the definition it names
    Given the Kotlin project
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
                "descriptor": "scip-kotlin gradle greeter . com/example/greeter/Greeter#greet().",
                "definedIn": [
                  { "path": "src/main/kotlin/greeter/Greeter.kt" }
                ]
              }
            ]
          }
        ]
      }
      """

  # The descriptor is asserted on both sides of the edge, byte for byte, for the
  # reason index_go.feature gives: the day the two stop agreeing is the day
  # cross-file navigation silently returns nothing.
  #
  # `com/example/greeter/` is the whole point of the assertion. The file sits at
  # `src/main/kotlin/greeter/Greeter.kt` and declares `package
  # com.example.greeter`, and the two agree about nothing — which is the shape
  # Kotlin's style guide asks for, and the reason this stanza reads the clause
  # and never the path.
  #
  # The coordinate is the other part that has to be right for ten ecosystems to
  # share a table: scheme, manager and package name read out of a
  # `settings.gradle.kts`, and a version that is honestly Unknown because no
  # settings file states one.
  Scenario: The cross-file join is on the descriptor, and the import derives the edge
    Given the Kotlin project
    When the artifact is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "greet", role: "definition") {
          symbolKind
          descriptor
          definedIn {
            path
            lang
            pkgScheme
            pkgManager
            pkgName
            pkgVersion
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
            "descriptor": "scip-kotlin gradle greeter . com/example/greeter/Greeter#greet().",
            "definedIn": [
              {
                "path": "src/main/kotlin/greeter/Greeter.kt",
                "lang": "kt",
                "pkgScheme": "scip-kotlin",
                "pkgManager": "gradle",
                "pkgName": "greeter",
                "pkgVersion": "."
              }
            ]
          }
        ]
      }
      """
    And "src/main/kotlin/app/Main.kt" imports "src/main/kotlin/greeter/Greeter.kt"

  # `implements` has held unmodified for Go, TypeScript, Python, Rust, Java, C#,
  # PHP and C++; Ruby was the one language that could not take part. Kotlin takes
  # part with nothing added at all, which is the least eventful this derivation
  # has been since M6: the language has an `interface` keyword, the grammar spells
  # it with the same node type as a class, and reading the keyword is the whole of
  # what the stanza does to make it fire.
  #
  # The interface is added rather than being in the base fixture for the reason
  # the C# suite gives: `implements` is cross-file by construction
  # (`c.file_id <> i.file_id`, store/sqlc/query.sql). The fixture's Greeter
  # already declares `: Speaker`, so this supplies the other half.
  Scenario: An interface declared in another file is implemented
    Given the Kotlin project
    And the artifact also holds "src/main/kotlin/greeter/Speaker.kt":
      """
      package com.example.greeter

      interface Speaker {
          fun greet(): String
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
            "descriptor": "scip-kotlin gradle greeter . com/example/greeter/Speaker#",
            "implementedBy": [
              {
                "name": "Greeter",
                "symbolKind": "type",
                "descriptor": "scip-kotlin gradle greeter . com/example/greeter/Greeter#",
                "definedIn": [
                  { "path": "src/main/kotlin/greeter/Greeter.kt" }
                ]
              }
            ]
          }
        ]
      }
      """

  # The companion decision, stated as the traversal it exists for.
  #
  # `create` is declared inside `companion object` and its descriptor carries no
  # `Companion#` component, because the *use site* two files away writes
  # `Factory.create()` and has no way to know that a companion is what it
  # reaches. A component only the definition side can compute is a component that
  # makes every cross-file call to a Kotlin factory method fail to join — the
  # same trade java.go argues for overloads, with the same answer.
  #
  # What it costs is a member of a class and a member of its companion sharing
  # one descriptor when they share a name, which is legal Kotlin and vanishingly
  # rare. `resolvedFrom` is what makes the claim: the reference that reached the
  # definition carries the identical string.
  Scenario: A companion object's member is reached through the class that holds it
    Given the Kotlin project
    And the artifact also holds "src/main/kotlin/greeter/Factory.kt":
      """
      package com.example.greeter

      class Factory {
          companion object {
              fun create(): Greeter = Greeter("world")
          }
      }
      """
    And the artifact also holds "src/main/kotlin/app/Build.kt":
      """
      package com.example.app

      import com.example.greeter.Factory

      fun build(): String = Factory.create().greet()
      """
    When the artifact is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "create", role: "definition") {
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
            "descriptor": "scip-kotlin gradle greeter . com/example/greeter/Factory#create().",
            "definedIn": [
              { "path": "src/main/kotlin/greeter/Factory.kt" }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "descriptor": "scip-kotlin gradle greeter . com/example/greeter/Factory#create()."
              }
            ]
          }
        ]
      }
      """

  # §14 M9+'s inherited test — "mixed-language repo; a cross-language query
  # returns both" — with ten languages rather than two. One repository, one
  # manifest per ecosystem, one `codiq` run.
  #
  # Eight languages now write `greeter/Greeter#greet().` byte for byte —
  # TypeScript, Python, Rust, Java, C#, PHP, C++ and **Kotlin** — with nothing but
  # four leading words keeping them apart. Kotlin is the first to join that
  # collision idiomatically: `package greeter` and `fun greet()` are what its own
  # conventions ask for, where PHP had to write against PSR-1 and C# against
  # .NET's to reach the same string.
  #
  # The tenth entry point is `proceed`, after `main`, `boot`, `run`, `start`,
  # `launch`, `begin`, `commence`, `initiate` and `embark`.
  #
  # The last four steps are the half a traversal cannot state, and the last is
  # what the scenario exists for: "no derived edge joins two languages" is a claim
  # about the absence of a row anywhere, and it is strictly stronger than the
  # scheme check beside it. It is also the check with the most to say here, since
  # Kotlin and Java are the first pair in this corpus that a human would expect to
  # be one language.
  Scenario: One mixed repository of ten languages keeps all ten apart
    Given the mixed-language repository of ten languages
    When the repository is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "proceed", role: "definition") {
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
    Then the answer is:
      """
      {
        "occurrence": [
          {
            "calls": [
              {
                "name": "greet",
                "descriptor": "scip-kotlin gradle mixed-kotlin . greeter/Greeter#greet().",
                "definedIn": [
                  { "path": "kotlin/Greeter.kt", "lang": "kt" }
                ]
              }
            ]
          }
        ]
      }
      """
    And the "ts", "py", "rs", "java", "cs", "php", "cc" and "kt" definitions of "greet" differ only in their coordinate
    And "Greeter" is defined once in each of "go", "ts", "py", "rs", "java", "cs", "rb", "php", "cc" and "kt"
    And the graph holds definitions written in all of "go", "ts", "py", "rs", "java", "cs", "rb", "php", "cc" and "kt"
    And no derived edge joins two package schemes
    And no derived edge joins two languages

  # The `.kts` decision, pinned where it would otherwise be discovered as a bug.
  #
  # A Kotlin script is a compilation unit whose top-level declarations are members
  # of a class kotlinc synthesizes from the *file name*, so `tools/report.kts` and
  # `tools/verify.kts` each declaring `val logger` declare two unrelated things.
  # Treating them as members of the root package would give both one descriptor,
  # and the link pass — which joins on the descriptor string and nothing else —
  # would resolve each into the other.
  #
  # Nothing outside a script can reference its top-level declarations, so the
  # container this stanza builds has to *separate* the two rather than be
  # reconstructible by a third file. That is why the step asserts they differ
  # rather than asserting what they are.
  Scenario: Two scripts declaring one name declare two symbols
    Given the Kotlin project
    And the artifact also holds "tools/report.kts":
      """
      val logger = "report"

      println(logger)
      """
    And the artifact also holds "tools/verify.kts":
      """
      val logger = "verify"

      println(logger)
      """
    When the artifact is indexed
    Then "tools/report.kts" and "tools/verify.kts" declare "logger" as two symbols
