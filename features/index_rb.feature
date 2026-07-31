# M9 — the Ruby extractor (SPEC.md §14 M9+, an additional-language task and the
# seventh language in this graph).
#
# M6 tested a claim about M1-M5's design — "a new language is one sub-package in
# `extract` + a query + a resolver; no other change" — and found one thing that
# was not true: a coordinate is a property of (repository, ecosystem) and index
# resolved one per repository. M7, M9-Rust, M9-Java, M9-C# and this task have
# tested the same claim with that fixed. Every scenario below runs against the
# schema, the store, the link pass and the MCP surface exactly as the C# task left
# them.
#
# The corpus is extract/rb/testdata/greeter/, reused rather than duplicated and
# deliberately the same shape as the six before it: a `Gemfile` with the lock
# beside it naming codiq-greeter, a `greeter/greeter.rb` declaring the type and
# its methods, and an `app/program.rb` that builds one and calls it. Two files,
# one call across the boundary between them. Scenarios that need more add to a
# copy, so the fixture stays the fixture.
#
# Five things about Ruby that the descriptors below only make sense against.
#
#   * **Ruby has two units of modularity and they are about different things.**
#     The constant namespace is `module`/`class` nesting and is the only thing
#     that names a symbol; the load unit is the file, named by the path `require`
#     spells. `require` populates the one global constant tree and contributes no
#     namespace of its own, so `greeter/greeter.rb` declaring
#     `Com::Example::Greeting::Greeter` is not a contradiction — it is two facts.
#     The stanza models both and keeps them apart, and the second scenario asserts
#     each of them separately.
#   * **A symbol's namespace is never its path.** Every earlier stanza but Java's
#     and C#'s derives the namespace from the file's location; Ruby cannot,
#     because a reference carries no path. `Com::Example::Greeting::Greeter`
#     written three directories away has to render what the declaration rendered,
#     and the constant path is the only thing both sides can compute.
#   * **Reopening is the normal idiom**, not an edge case. `class Foo` in five
#     files declares one class, needs no keyword, and happens across directories.
#     The fourth scenario is that, and it is C#'s partial class turned up.
#   * **`def foo` and `def self.foo` are different methods of one class**, which
#     C# forbids and Ruby does not, so the descriptor carries a `self.` component
#     for the second. It may, because a reference site can reconstruct it:
#     `Greeter.build` has a constant for a receiver and `g.build` has a value.
#   * The coordinate is `scip-ruby gem <name> <version>`, read from the `PATH`
#     section of the Gemfile.lock beside the Gemfile (coord/gemfile.go). Nothing
#     it can produce shares a prefix with `scip-go gomod …`,
#     `scip-typescript npm …`, `scip-python pip …`, `scip-rust cargo …`,
#     `scip-java maven …` or `scip-csharp nuget …`, which is the whole of why
#     seven ecosystems can share one `occurrence` table.
#
# THE MILESTONE'S STATED TEST is the third scenario, and it is the first one in
# this suite that had to be weakened rather than strengthened. Read its comment:
# Ruby is the first language here that cannot be made to collide with the others,
# and the reason is a fact about the grammar rather than a choice this stanza
# made.

Feature: Indexing a Ruby gem
  As an agent navigating a polyglot codebase
  I want Ruby in the same graph, under the same model, as Go, TypeScript, Python, Rust, Java and C#
  So that one query language reaches every symbol regardless of what wrote it

  Background:
    Given an empty CodiQ graph

  # The M2 scenario in a seventh language, with not one line of the pipeline
  # between them changed. Neither file's extraction ever saw the other — §5's
  # file-local contract holds per language, not per product — so at the end of
  # extraction program.rb held a reference it could not resolve and greeter.rb
  # held a definition nobody had asked for, and the edge below exists because the
  # link pass (§7) matched their descriptor strings.
  #
  # What made the match possible is one reading, and it is the only one Ruby
  # offers. `g.greet` says nothing about what `g` is, and Ruby has no annotations
  # to read it off — so the type comes from the construction, `Greeter.new`,
  # which is the single expression whose result the source names. Everything else
  # (`Greeter.build`, `Greeter.for(x)`) is deliberately not read: a name is not a
  # contract.
  #
  # `puts message` is in the same method and produces no second entry, which is
  # the honest half of the same mechanism: `puts` is Kernel's, this index owns no
  # definition of it, and the reference stays unresolved rather than being guessed
  # at. So does `Greeter.new` — `new` is `Class#new` and is not `initialize`, and
  # pretending otherwise would be a descriptor no reference site wrote.
  Scenario: A call is traversable across files to the definition it names
    Given the two-file Ruby gem
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
                "descriptor": "scip-ruby gem codiq-greeter 1.0.0 Com/Example/Greeting/Greeter#greet().",
                "definedIn": [
                  { "path": "greeter/greeter.rb" }
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
  # RubyGems coordinate, which is the part that has to be right for seven
  # ecosystems to share a table — scheme, manager, package name and version, all
  # four read out of a Gemfile.lock by a resolver that changed nothing to slot in
  # beside go.mod's, package.json's, pyproject.toml's, Cargo.toml's, pom.xml's and
  # Directory.Build.props'.
  #
  # The last two steps are Ruby's two units of modularity, checked apart. The
  # `Greeting` read is the constant namespace: `greeter/greeter.rb` sits nowhere
  # near a directory called `Com/Example/Greeting` and declares it anyway, and
  # `app/program.rb` writes the same path in an expression having never seen the
  # other file. The `imports` step is the load unit: `require "greeter/greeter"`
  # names a *path*, and the file it names defines that path as a package for
  # exactly this join. Neither mechanism can stand in for the other — a mixin
  # import has no path and a require has no namespace — and the stanza would be
  # wrong if either were missing.
  Scenario: The cross-file join is on the descriptor and nothing else
    Given the two-file Ruby gem
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
            "descriptor": "scip-ruby gem codiq-greeter 1.0.0 Com/Example/Greeting/Greeter#greet().",
            "definedIn": [
              {
                "path": "greeter/greeter.rb",
                "lang": "rb",
                "pkgScheme": "scip-ruby",
                "pkgManager": "gem",
                "pkgName": "codiq-greeter",
                "pkgVersion": "1.0.0"
              }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "descriptor": "scip-ruby gem codiq-greeter 1.0.0 Com/Example/Greeting/Greeter#greet()."
              }
            ]
          }
        ]
      }
      """
    And an agent asks over MCP:
      """
      {
        occurrence(name: "Greeting", role: "definition") {
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
            "descriptor": "scip-ruby gem codiq-greeter 1.0.0 Com/Example/Greeting/",
            "definedIn": [
              { "path": "greeter/greeter.rb" }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "name": "Greeting",
                "symbolKind": "package",
                "descriptor": "scip-ruby gem codiq-greeter 1.0.0 Com/Example/Greeting/"
              }
            ]
          }
        ]
      }
      """
    And "app/program.rb" imports "greeter/greeter.rb"

  # §14 M9+'s inherited test — "mixed-language repo; a cross-language query
  # returns both" — with seven languages rather than two. One repository, one
  # manifest per ecosystem, one `codiq` run.
  #
  # For six languages this scenario was built to collide as hard as it could be
  # made to: all six derive the namespace `greeter/` — Go from the directory,
  # TypeScript from `greeter.ts`, Python from `greeter.py`, Rust from
  # `src/greeter.rs`, Java from a `package greeter;` clause and C# from a
  # `namespace greeter;` one — and five of them write `greeter/Greeter#greet().`
  # byte for byte, with nothing but four leading words keeping them apart.
  #
  # **Ruby cannot join them, and the reason is worth stating rather than working
  # around.** A namespace descriptor spelled `greeter/` needs a namespace named
  # `greeter`, and Ruby has no such thing to declare: `module greeter` is a syntax
  # error, because Ruby's capitalisation rule is *lexical* — a module name is a
  # `constant` token, decided by the tokeniser — and not the convention Java's and
  # C#'s corpora were written against the grain of. The other route, deriving the
  # namespace from the path, is the one extract/rb/rb.go rejects on its own
  # merits: a reference carries no path, so a path-derived namespace would leave
  # every cross-directory constant reference in real Ruby joining nothing.
  #
  # So Ruby's entry writes `Greeting/Greeter#greet().` and collides with none of
  # the six. What that costs this scenario is nothing it was protecting: the
  # collision exists to keep "no derived edge joins two languages" non-vacuous,
  # and six languages still write one suffix. What Ruby adds instead is the case
  # the other six cannot make — a seventh ecosystem whose *suffixes* are
  # unmistakable and which therefore tests the coordinate is carried at all, on a
  # corpus where an edge to any of the other six would be visible immediately.
  #
  # Each read below is written to match exactly one row, for the reason
  # index_ts.feature gives — the GraphQL layer emits no ORDER BY, so a query
  # matching a row per language would be a coin flip. The seven entry points are
  # therefore named apart (`main`, `boot`, `run`, `start`, `launch`, `begin`,
  # `commence`) while everything they reach is named together.
  #
  # The last four steps are the half a traversal cannot state. The first of them
  # is what makes the rest non-vacuous — it asserts the collision is real across
  # the five languages that share a suffix — and the last is what the scenario
  # exists for: "no derived edge joins two languages" is a claim about the absence
  # of a row anywhere, and it is strictly stronger than the scheme check beside
  # it, because the defect that kept a mixed repository out of M6 stamped two
  # languages with the *same* scheme and would have passed a scheme comparison
  # unnoticed.
  Scenario: One mixed repository of seven languages keeps all seven apart
    Given the mixed-language repository of seven languages
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
        occurrence(name: "commence", role: "definition") {
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
                "descriptor": "scip-ruby gem codiq-mixed-rb 7.0.0 Greeting/Greeter#greet().",
                "definedIn": [
                  { "path": "greeter.rb", "lang": "rb" }
                ]
              }
            ]
          }
        ]
      }
      """
    And the "ts", "py", "rs", "java" and "cs" definitions of "greet" differ only in their coordinate
    And "Greeter" is defined once in each of "go", "ts", "py", "rs", "java", "cs" and "rb"
    And the graph holds definitions written in all of "go", "ts", "py", "rs", "java", "cs" and "rb"
    And no derived edge joins two package schemes
    And no derived edge joins two languages

  # Reopening is Ruby's contribution to this graph and it is C#'s partial class
  # with the guard rails taken off: no keyword marks it, no compiler checks that
  # the two halves agree, it happens across directories, and it is how ordinary
  # Ruby is written rather than a code generator's convention.
  #
  # Two definition rows carrying one descriptor are two *sites of one symbol* —
  # the descriptor is a coordinate and a constant path and says nothing about
  # which file a symbol was written in — and that is what the link pass wants
  # rather than something it has to survive. It is also the property a path-derived
  # namespace would have destroyed: `lib/loud/shout.rb` and `core_ext/loud.rb`
  # would have rendered two namespaces and split one class in two.
  #
  # The traversal is the first half: a call into the type resolves to the site
  # that declares the member, and only that site. `whisper` is declared in one of
  # the two files and reached from a third that has seen neither. `herald.rb`
  # writes `Loud` unqualified at the top level, which is the Rails shape and the
  # commonest thing in Ruby, and it resolves because an unqualified constant in a
  # file that declares no module *is* the root namespace.
  #
  # The last step is the shared-descriptor claim, and it is SQL rather than MCP
  # for the reason the C# suite gives: two definition rows share a descriptor, so
  # a traversal reaching the type returns them in an order the GraphQL layer does
  # not promise.
  Scenario: A class reopened in two directories is one type
    Given the two-file Ruby gem
    And the artifact also holds "lib/loud/shout.rb":
      """
      class Loud
        def shout
          "HELLO"
        end
      end
      """
    And the artifact also holds "core_ext/loud.rb":
      """
      class Loud
        def whisper
          "hello"
        end
      end
      """
    And the artifact also holds "app/herald.rb":
      """
      class Herald
        def self.proclaim
          loud = Loud.new
          loud.whisper
        end
      end
      """
    When the artifact is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "proclaim", role: "definition") {
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
            "calls": [
              {
                "name": "whisper",
                "descriptor": "scip-ruby gem codiq-greeter 1.0.0 Loud#whisper().",
                "definedIn": [
                  { "path": "core_ext/loud.rb" }
                ]
              }
            ]
          }
        ]
      }
      """
    And "Loud" is declared in 2 files under one descriptor

  # `implements` has held unmodified for Go, TypeScript, Python, Rust, Java and
  # C#, and Ruby is the first language that does not take part in it. That is a
  # decision and not an omission, and this scenario is where it is written down.
  #
  # `include Foo` looks like the interface declaration `class Greeter : ISpeaker`
  # is, and it is the inverse of one. A Ruby module's method set is what it
  # *gives*: `Comparable` provides six comparison methods and demands `<=>`, so a
  # class that includes it declares the one method the module does not have and
  # none of the six it does. Method-set containment therefore fails exactly where
  # the include is real — and the only place it could succeed is a class that
  # happens to share a module's method names and included nothing, which is duck
  # typing manufacturing a claim nobody made. Either way the derivation would be
  # saying something the program does not.
  #
  # So a module is emitted as a `package`, whose descriptor ends `/` and which
  # link's `type_def` CTE (`right(descriptor, 1) = '#'`) does not select, and no
  # Ruby definition is ever an `interface`. What `include` derives instead is the
  # thing it structurally is: a reference to the module's package descriptor, and
  # therefore an `imports` edge from the file that includes the mixin to the file
  # that declares it — Ruby's *second* import mechanism, with no path in it
  # anywhere, which is why the second scenario could not have covered this one.
  Scenario: A mixin is an import and never an implementation
    Given the two-file Ruby gem
    And the artifact also holds "greeter/loud.rb":
      """
      module Com
        module Example
          module Greeting
            module Loud
              def shout
                greet.upcase
              end
            end
          end
        end
      end
      """
    And the artifact also holds "greeter/speaker.rb":
      """
      module Com
        module Example
          module Greeting
            class Speaker
              include Loud

              def greet
                "hi"
              end
            end
          end
        end
      end
      """
    When the artifact is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "Loud", role: "definition") {
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
            "descriptor": "scip-ruby gem codiq-greeter 1.0.0 Com/Example/Greeting/Loud/",
            "definedIn": [
              { "path": "greeter/loud.rb" }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "symbolKind": "package",
                "descriptor": "scip-ruby gem codiq-greeter 1.0.0 Com/Example/Greeting/Loud/"
              }
            ]
          }
        ]
      }
      """
    And "greeter/speaker.rb" imports "greeter/loud.rb"
    And no "rb" definition takes part in an implements edge
