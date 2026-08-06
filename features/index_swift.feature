# M9 — the Swift extractor (SPEC.md §14 M9+, an additional-language task and the
# eleventh language in this graph).
#
# M6 tested a claim about M1-M5's design — "a new language is one sub-package in
# `extract` + a query + a resolver; no other change" — and found one thing that
# was not true. M7 and the seven M9 language tasks after it tested it again with
# that fixed. This task tests it against the first language that **writes its
# namespace nowhere** and whose **manifest is one of its own source files**, and
# the claim holds: no core package changed, and the decisions that had to be
# argued rather than copied are all inside `extract/swift` and `coord/swiftpm.go`.
#
# Six things about Swift that the descriptors below only make sense against.
#
#   * **The namespace is the module, and Swift declares no module.** Go, Java,
#     Kotlin, C# and PHP write a namespace in the source; TypeScript, Python and
#     Rust derive one from the file's own position. Swift's namespace is the
#     SwiftPM *target*, which is declared in `Package.swift` — one file, listing
#     every module in the package — and §2.5 forbids the extractor from reading
#     another file. So the module is derived from the path rule SwiftPM
#     *enforces*: a target with no explicit `path:` must live at
#     `Sources/<TargetName>/`, and everything below that directory is in that one
#     module however deeply nested, because a Swift module is flat.
#   * **Every Swift import is a whole-module wildcard**, and that is this
#     stanza's central limit. `import Greeter` brings every public declaration of
#     the module into scope under its simple name and names not one of them, so a
#     file-local extractor cannot say which module a bare `Greeter` came from.
#     It refuses to guess: an unattributable name lands in *this* module's
#     namespace, matching nothing. The first scenario's call therefore resolves
#     because both files are in one module — which is where the overwhelming
#     majority of a Swift package's references live, since a module is flat and
#     multi-file — while `Sources/App/main.swift`'s call to `run()` does not, and
#     is not made to.
#   * **`Package.swift` is a manifest and a compilation unit at once**, the first
#     file in this repository that is both: it is Swift, so `byExt` hands it to
#     the Swift stanza, and nothing in the registry can say otherwise. It is in
#     no module — SwiftPM compiles each manifest on its own — so its declarations
#     hang off a container named for the file. The last scenario is that
#     decision, stated as the phantom it prevents.
#   * **An extension is a container and not a definition.** `extension Greeter {
#     func loud() }` really does add a member to `Greeter`, reachable as
#     `g.loud()` from anywhere in the module, so the member is descriptored under
#     the type it extends — which is the descriptor a call site renders once it
#     has resolved its receiver. That is the *opposite* of the call
#     extract/kotlin makes, and both are right: Kotlin's `fun String.shout()` is
#     a static function of the file's package that takes a receiver, and Swift's
#     is a member. The fourth scenario is that decision, stated as a traversal.
#   * **An initialiser has no name**, so it is not a definition here. It needs
#     none: Swift has no `new`, so `Greeter(name: "world")` is a call whose
#     callee is the *type*, and it resolves to the type's own descriptor — a
#     definition that exists and that both files render identically.
#   * The coordinate is `scip-swift swiftpm <name> .`, read from a
#     `Package.swift` (coord/swiftpm.go). The version is **always** Unknown and
#     that is not a gap: a SwiftPM package's version is a git tag, so the
#     manifest format has no version field to read. What it costs is what CMake's
#     choice costs C and Gradle's costs Kotlin: a Swift repository with no
#     `Package.swift` and no other language's manifest used to fail the run
#     outright. Since the corpus milestone it is named
#     `scip-swift swiftpm <corpus> .` instead and indexes, so an Xcode-only
#     project -- exactly that repository -- is no longer out of reach.
#
# THE MILESTONE'S STATED TEST is the fifth scenario. Nine languages now write
# `greeter/Greeter#greet().` byte for byte, and Swift joins that collision the
# way PHP and C# did rather than the way Kotlin did: a lowercase module name is
# something SwiftPM permits and the API Design Guidelines disown, so the corpus
# has to be written slightly against the language's own conventions to collide at
# all. That it *can* be written that way is the point — the four leading words
# are the only thing keeping eleven languages apart, and they are enough.

Feature: Indexing a Swift package
  As an agent navigating a polyglot codebase
  I want Swift in the same graph, under the same model, as Go, TypeScript, Python, Rust, Java, C#, Ruby, PHP, C/C++ and Kotlin
  So that one query language reaches every symbol regardless of what wrote it

  Background:
    Given an empty CodiQ graph

  # The M2 scenario in an eleventh language, with not one line of the pipeline
  # between them changed. Neither file's extraction ever saw the other — §5's
  # file-local contract holds per language, not per product — so at the end of
  # extraction Runner.swift held a reference it could not resolve and
  # Greeter.swift held a definition nobody had asked for, and the edge below
  # exists because the link pass (§7) matched their descriptor strings.
  #
  # The two files are in one module and neither writes that down: `Sources/
  # Greeter/` is the whole of what says so, and both sides derive `Greeter/` from
  # it independently. That is Swift's namespace story in one assertion.
  #
  # There is exactly one `calls` row, and the three things that are *not* in it
  # are the scenario's other half. `Greeter(name: "world")` is an initialiser
  # call, which resolves to the type and not to a callable, so it is a `type`
  # reference that link's `calls` derivation (`symbol_kind IN ('function',
  # 'method')`) does not select. `print` is the standard library's and this index
  # owns no definition of it. And `Sources/App/main.swift` calls `run()` across a
  # module boundary through a wildcard import, which does not resolve and is not
  # guessed at — the honest cost, visible here as an absence.
  Scenario: A call is traversable across files to the definition it names
    Given the Swift package
    When the artifact is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "run", role: "definition") {
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
            "name": "run",
            "calls": [
              {
                "name": "greet",
                "symbolKind": "method",
                "descriptor": "scip-swift swiftpm greeter . Greeter/Greeter#greet().",
                "definedIn": [
                  { "path": "Sources/Greeter/Greeter.swift" }
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
  # `Greeter/` is the whole point of the assertion, and it is a namespace no line
  # of either file states. It comes from `Sources/Greeter/`, which is where
  # SwiftPM requires a target's sources to live, and it is the only thing this
  # stanza can read that says which module a Swift file is in.
  #
  # The coordinate is the other part that has to be right for eleven ecosystems
  # to share a table: scheme, manager and package name read out of a
  # `Package.swift`, and a version that is honestly Unknown because a SwiftPM
  # version is a git tag rather than a manifest field.
  #
  # The `imports` edge is the wildcard import's one unambiguous product. `import
  # Greeter` names no declaration, so nothing it brings into scope can be
  # resolved — but it *does* name the module, and the occurrence it emits is
  # byte-identical to the one every file of that module derives for itself.
  Scenario: The cross-file join is on the descriptor, and the import derives the edge
    Given the Swift package
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
            "descriptor": "scip-swift swiftpm greeter . Greeter/Greeter#greet().",
            "definedIn": [
              {
                "path": "Sources/Greeter/Greeter.swift",
                "lang": "swift",
                "pkgScheme": "scip-swift",
                "pkgManager": "swiftpm",
                "pkgName": "greeter",
                "pkgVersion": "."
              }
            ]
          }
        ]
      }
      """
    And "Sources/App/main.swift" imports "Sources/Greeter/Greeter.swift"

  # `implements` has held unmodified for Go, TypeScript, Python, Rust, Java, C#,
  # PHP, C++ and Kotlin; Ruby was the one language that could not take part. Swift
  # takes part with nothing added at all, and one detail is worth stating: the
  # derivation is method-set containment and never reads the conformance clause,
  # so the `: Speaker` written on `Greeter` below contributes nothing. That is
  # what makes it work for Swift at all — a Swift type declares its conformances
  # in the type, in an extension, or in an extension in a third file, and the
  # derivation cannot tell the difference because it never looks.
  #
  # The protocol is added rather than being in the base fixture for the reason
  # the C# suite gives: `implements` is cross-file by construction
  # (`c.file_id <> i.file_id`, store/sqlc/query.sql). The fixture's Greeter
  # already declares `: Speaker`, so this supplies the other half.
  Scenario: A protocol declared in another file is conformed to
    Given the Swift package
    And the artifact also holds "Sources/Greeter/Speaker.swift":
      """
      public protocol Speaker {
          func greet() -> String
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
            "descriptor": "scip-swift swiftpm greeter . Greeter/Speaker#",
            "implementedBy": [
              {
                "name": "Greeter",
                "symbolKind": "type",
                "descriptor": "scip-swift swiftpm greeter . Greeter/Greeter#",
                "definedIn": [
                  { "path": "Sources/Greeter/Greeter.swift" }
                ]
              }
            ]
          }
        ]
      }
      """

  # The extension decision, stated as the traversal it exists for.
  #
  # `loud` is declared in an `extension Greeter` in a file that declares no type
  # at all, and its descriptor is `Greeter/Greeter#loud().` — the extended type's,
  # not the extending file's. That is what the *use site* two files away can
  # compute: `g.loud()` resolves `g` to `Greeter` and renders exactly that
  # string, with no way to know, and no need to know, that an extension is what
  # it reaches.
  #
  # It is the opposite of the call extract/kotlin makes for `fun String.shout()`,
  # and the difference is the languages': a Kotlin extension function is a static
  # function of its file's package that takes a receiver, and a Swift extension's
  # member is a member. `resolvedFrom` is what makes the claim — the reference
  # that reached the definition carries the identical string.
  Scenario: An extension's member is reached through the type it extends
    Given the Swift package
    And the artifact also holds "Sources/Greeter/Loud.swift":
      """
      extension Greeter {
          public func loud() -> String {
              return self.greet()
          }
      }
      """
    And the artifact also holds "Sources/Greeter/Shout.swift":
      """
      public func shout(g: Greeter) -> String {
          return g.loud()
      }
      """
    When the artifact is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "loud", role: "definition") {
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
            "descriptor": "scip-swift swiftpm greeter . Greeter/Greeter#loud().",
            "definedIn": [
              { "path": "Sources/Greeter/Loud.swift" }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "descriptor": "scip-swift swiftpm greeter . Greeter/Greeter#loud()."
              }
            ]
          }
        ]
      }
      """

  # §14 M9+'s inherited test — "mixed-language repo; a cross-language query
  # returns both" — with eleven languages rather than two. One repository, one
  # manifest per ecosystem, one `codiq` run.
  #
  # Nine languages now write `greeter/Greeter#greet().` byte for byte —
  # TypeScript, Python, Rust, Java, C#, PHP, C++, Kotlin and **Swift** — with
  # nothing but four leading words keeping them apart. Swift joins it the way PHP
  # and C# did rather than the way Kotlin did: the module is a directory name, so
  # colliding means writing `Sources/greeter/` where the API Design Guidelines
  # ask for `Sources/Greeter/`. SwiftPM permits it; a Swift author would not
  # write it. That is the honest cost of the collision and it is worth having,
  # because what the assertion tests is that the coordinate is load-bearing.
  #
  # The eleventh entry point is `advance`, after `main`, `boot`, `run`, `start`,
  # `launch`, `begin`, `commence`, `initiate`, `embark` and `proceed`.
  #
  # The last four steps are the half a traversal cannot state, and the last is
  # what the scenario exists for: "no derived edge joins two languages" is a
  # claim about the absence of a row anywhere, and it is strictly stronger than
  # the scheme check beside it — every phantom edge M6 found had the *same*
  # scheme on both ends.
  Scenario: One mixed repository of eleven languages keeps all eleven apart
    Given the mixed-language repository of eleven languages
    When the repository is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "advance", role: "definition") {
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
                "descriptor": "scip-swift swiftpm mixed-swift . greeter/Greeter#greet().",
                "definedIn": [
                  { "path": "Sources/greeter/Greeter.swift", "lang": "swift" }
                ]
              }
            ]
          }
        ]
      }
      """
    And the "ts", "py", "rs", "java", "cs", "php", "cc", "kt" and "swift" definitions of "greet" differ only in their coordinate
    And "Greeter" is defined once in each of "go", "ts", "py", "rs", "java", "cs", "rb", "php", "cc", "kt" and "swift"
    And the graph holds definitions written in all of "go", "ts", "py", "rs", "java", "cs", "rb", "php", "cc", "kt" and "swift"
    And no derived edge joins two package schemes
    And no derived edge joins two languages

  # The `Package.swift` decision, pinned where it would otherwise be discovered
  # as a bug.
  #
  # A SwiftPM manifest is Swift source, so the walk takes it and this stanza
  # parses it — there is no registry entry that could say otherwise, since
  # `Package.swift` carries the extension the Swift ecosystem owns. What it is
  # *not* is part of a module: SwiftPM compiles each manifest on its own, and
  # `let package = Package(…)` is not a declaration any target's code can
  # reference. A repository with a root manifest and a second one under
  # `examples/` therefore declares `package` twice, and descriptoring both as
  # members of the root module would render one string for both — which the link
  # pass, joining on the descriptor string and nothing else, would resolve into
  # each other. That is a phantom edge, and nothing downstream can tell it is
  # false.
  #
  # The step asserts they differ rather than asserting what they are, for the
  # reason index_kotlin.feature gives about a `.kts`: nothing outside a manifest
  # can reference its declarations, so the container has to *separate* the two
  # rather than be reconstructible by a third file.
  Scenario: Two manifests declare two symbols
    Given the Swift package
    And the artifact also holds "examples/demo/Package.swift":
      """
      // swift-tools-version:5.9
      import PackageDescription

      let package = Package(
          name: "demo",
          targets: [.target(name: "Demo")]
      )
      """
    When the artifact is indexed
    Then "Package.swift" and "examples/demo/Package.swift" declare "package" as two symbols
