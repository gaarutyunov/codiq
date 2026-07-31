# M9 — the PHP extractor (SPEC.md §14 M9+, an additional-language task and the
# eighth language in this graph).
#
# M6 tested a claim about M1-M5's design — "a new language is one sub-package in
# `extract` + a query + a resolver; no other change" — and found one thing that
# was not true: a coordinate is a property of (repository, ecosystem) and index
# resolved one per repository. M7, M9-Rust, M9-Java, M9-C#, M9-Ruby and this task
# have tested the same claim with that fixed. Every scenario below runs against
# the schema, the store, the link pass and the MCP surface exactly as the Ruby
# task left them.
#
# The corpus is extract/php/testdata/greeter/, reused rather than duplicated and
# deliberately the same shape as the seven before it: a `composer.json`, a
# `src/Greeter/Greeter.php` declaring the type and its members, and an
# `src/App/Program.php` that builds one and calls it. Two files, one call across
# the boundary between them. Scenarios that need more add to a copy, so the
# fixture stays the fixture.
#
# Five things about PHP that the descriptors below only make sense against.
#
#   * **PHP's name resolution is entirely file-local by language design**, and it
#     is the first language in this graph of which that is true. The manual's
#     "Name resolution rules" are four cases with no fifth — fully qualified,
#     relative, qualified, unqualified — and every input to them is written in the
#     file: the `namespace` statement and the `use` declarations. Java splits a
#     dotted path on a naming convention, C# cannot even do that and falls back to
#     "the file has been told this is a namespace", Ruby has to guess whether a
#     path segment is a module or a class. None of it is needed here, and the
#     seventh scenario is what checks it.
#   * **`use` is the import mechanism and `require` is not.** A top-level `use`
#     names a symbol in another namespace, and the namespace half of it is
#     recorded as a `package` reference — byte-identical to what the declaring
#     file's own `namespace` renders, which is what `imports` joins on. `require`
#     names a *path*, and PHP — unlike Ruby — has no second path-keyed namespace
#     for it to join against; extract/php/php.go says why giving it one would
#     reintroduce exactly the collision the mixed corpus exists to detect.
#   * **PHP has real interfaces, so `implements` fires.** This is the claim Ruby
#     could not make. `interface Speaker` declares behaviour, `class Greeter
#     implements Speaker` states satisfaction, and link's method-set containment
#     derivation reads it unmodified — the fourth scenario.
#   * **A trait is not an interface and not a namespace**, and a class satisfying
#     an interface *only* through a trait gets no `implements` edge. That is a
#     false negative, it is chosen, and the fifth scenario is where it is written
#     down rather than discovered later as a bug.
#   * The coordinate is `scip-php composer <name> <version>`, read from
#     `composer.json` (coord/composer.go) — the first manifest since `go.mod` that
#     is fixed-name, machine-readable and states identity outright. Nothing it can
#     produce shares a prefix with `scip-go gomod …`, `scip-typescript npm …`,
#     `scip-python pip …`, `scip-rust cargo …`, `scip-java maven …`,
#     `scip-csharp nuget …` or `scip-ruby gem …`, which is the whole of why eight
#     ecosystems can share one `occurrence` table.
#
# THE MILESTONE'S STATED TEST is the third scenario. Ruby was the one language
# that could not be made to collide with the others; PHP can, and does — a PHP
# namespace is unconstrained by case, so `namespace greeter;` is legal and the
# suffix is `greeter/Greeter#greet().` byte for byte with five others.

Feature: Indexing a PHP package
  As an agent navigating a polyglot codebase
  I want PHP in the same graph, under the same model, as Go, TypeScript, Python, Rust, Java, C# and Ruby
  So that one query language reaches every symbol regardless of what wrote it

  Background:
    Given an empty CodiQ graph

  # The M2 scenario in an eighth language, with not one line of the pipeline
  # between them changed. Neither file's extraction ever saw the other — §5's
  # file-local contract holds per language, not per product — so at the end of
  # extraction Program.php held a reference it could not resolve and Greeter.php
  # held a definition nobody had asked for, and the edge below exists because the
  # link pass (§7) matched their descriptor strings.
  #
  # What made the match possible is `new Greeter(…)`, which is the one expression
  # whose type PHP writes down where a `var`-style local is concerned. It is also
  # not the only way: a typed parameter, a typed property and a promoted
  # constructor parameter all carry declared types too, which is why this stanza
  # resolves receivers more often than Ruby's and about as often as C#'s.
  #
  # `strtoupper($this->greet())` inside the trait produces no second entry, which
  # is the honest half of the same mechanism: `strtoupper` is the language's, this
  # index owns no definition of it, and the reference carries a foreign coordinate
  # rather than being guessed at.
  Scenario: A call is traversable across files to the definition it names
    Given the two-file PHP package
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
                "descriptor": "scip-php composer codiq/greeter 1.0.0 Com/Example/Greeting/Greeter#greet().",
                "definedIn": [
                  { "path": "src/Greeter/Greeter.php" }
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
  # Composer coordinate, which is the part that has to be right for eight
  # ecosystems to share a table — scheme, manager, package name and version, all
  # four read out of a `composer.json` by a resolver that changed nothing to slot
  # in beside go.mod's, package.json's, pyproject.toml's, Cargo.toml's, pom.xml's,
  # Directory.Build.props' and Gemfile's.
  #
  # The last two steps are PHP's import mechanism, checked apart. The `Greeting`
  # read is the namespace: `src/Greeter/Greeter.php` sits in a directory called
  # `Greeter` and declares `Com\Example\Greeting` anyway, and `src/App/Program.php`
  # writes the same path in a `use` having never seen the other file. The
  # `imports` step is the edge that falls out of it — and note that no `require`
  # appears anywhere in this corpus, because in PHP with Composer none is needed.
  Scenario: The cross-file join is on the descriptor and nothing else
    Given the two-file PHP package
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
            "descriptor": "scip-php composer codiq/greeter 1.0.0 Com/Example/Greeting/Greeter#greet().",
            "definedIn": [
              {
                "path": "src/Greeter/Greeter.php",
                "lang": "php",
                "pkgScheme": "scip-php",
                "pkgManager": "composer",
                "pkgName": "codiq/greeter",
                "pkgVersion": "1.0.0"
              }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "descriptor": "scip-php composer codiq/greeter 1.0.0 Com/Example/Greeting/Greeter#greet()."
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
            "descriptor": "scip-php composer codiq/greeter 1.0.0 Com/Example/Greeting/",
            "definedIn": [
              { "path": "src/Greeter/Greeter.php" }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "name": "Greeting",
                "symbolKind": "package",
                "descriptor": "scip-php composer codiq/greeter 1.0.0 Com/Example/Greeting/"
              }
            ]
          }
        ]
      }
      """
    And "src/App/Program.php" imports "src/Greeter/Greeter.php"

  # §14 M9+'s inherited test — "mixed-language repo; a cross-language query
  # returns both" — with eight languages rather than two. One repository, one
  # manifest per ecosystem, one `codiq` run.
  #
  # The corpus is built to collide as hard as it can be made to. Seven languages
  # derive the namespace `greeter/` — Go from the directory, TypeScript from
  # `greeter.ts`, Python from `greeter.py`, Rust from `src/greeter.rs`, Java from a
  # `package greeter;` clause, C# from a `namespace greeter;` one and **PHP from a
  # `namespace greeter;` one** — and six of them write `greeter/Greeter#greet().`
  # byte for byte, with nothing but four leading words keeping them apart.
  #
  # PHP joins that collision where Ruby could not, and the reason is precise and
  # worth stating. Ruby's capitalisation rule is *lexical*: a module name is a
  # `constant` token by the grammar, so `module greeter` is a syntax error rather
  # than an unidiomatic choice. PHP has no such rule — a namespace segment is a
  # plain `name`, and `namespace greeter;` is legal, if unidiomatic. That single
  # property is why PHP is back in the collision and, in extract/php/php.go, why
  # PHP could not have Ruby's second path-keyed namespace: unconstrained case cuts
  # both ways, and a path-derived package descriptor would collide with a
  # namespace-derived one inside a single language.
  #
  # Each read below is written to match exactly one row, for the reason
  # index_ts.feature gives — the GraphQL layer emits no ORDER BY, so a query
  # matching a row per language would be a coin flip. The eight entry points are
  # therefore named apart (`main`, `boot`, `run`, `start`, `launch`, `begin`,
  # `commence`, `initiate`) while everything they reach is named together.
  #
  # The last four steps are the half a traversal cannot state. The first of them
  # is what makes the rest non-vacuous — it asserts the collision is real across
  # the six languages that share a suffix — and the last is what the scenario
  # exists for: "no derived edge joins two languages" is a claim about the absence
  # of a row anywhere, and it is strictly stronger than the scheme check beside
  # it, because the defect that kept a mixed repository out of M6 stamped two
  # languages with the *same* scheme and would have passed a scheme comparison
  # unnoticed.
  Scenario: One mixed repository of eight languages keeps all eight apart
    Given the mixed-language repository of eight languages
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
        occurrence(name: "initiate", role: "definition") {
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
                "descriptor": "scip-php composer codiq/mixed-php 8.0.0 greeter/Greeter#greet().",
                "definedIn": [
                  { "path": "php/Greeter.php", "lang": "php" }
                ]
              }
            ]
          }
        ]
      }
      """
    And the "ts", "py", "rs", "java", "cs" and "php" definitions of "greet" differ only in their coordinate
    And "Greeter" is defined once in each of "go", "ts", "py", "rs", "java", "cs", "rb" and "php"
    And the graph holds definitions written in all of "go", "ts", "py", "rs", "java", "cs", "rb" and "php"
    And no derived edge joins two package schemes
    And no derived edge joins two languages

  # `implements` has held unmodified for Go, TypeScript, Python, Rust, Java and
  # C#, and Ruby was the one language that did not take part in it. PHP does, and
  # this scenario is the difference between the two stated as a fact rather than
  # as a paragraph.
  #
  # Ruby's `include Foo` is the *inverse* of an interface declaration: a module's
  # method set is what it gives, so a class including `Comparable` declares the one
  # method the module lacks and none of the six it has, and containment fails
  # exactly where the include is real. PHP does not have that problem, because PHP
  # has the thing itself: `interface Speaker { public function greet(): string; }`
  # declares behaviour and nothing else, `class Greeter implements Speaker` is
  # checked by the compiler, and link's method-set containment derivation reads it
  # with nothing added — the same derivation, keyed off the same
  # `symbol_kind = 'interface'`, that six languages before this one exercised.
  #
  # The interface is added rather than in the base corpus for the reason the C#
  # suite gives: `implements` is cross-file by construction
  # (`c.file_id <> i.file_id`, store/sqlc/query.sql), and a second `greet`
  # definition in the base fixture would make the previous scenario's traversal
  # match two rows in an order nothing promises.
  Scenario: An interface declared in another file is implemented
    Given the two-file PHP package
    And the artifact also holds "src/Greeter/Speaker.php":
      """
      <?php

      declare(strict_types=1);

      namespace Com\Example\Greeting;

      interface Speaker
      {
          public function greet(): string;
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
            "descriptor": "scip-php composer codiq/greeter 1.0.0 Com/Example/Greeting/Speaker#",
            "implementedBy": [
              {
                "name": "Greeter",
                "symbolKind": "type",
                "descriptor": "scip-php composer codiq/greeter 1.0.0 Com/Example/Greeting/Greeter#",
                "definedIn": [
                  { "path": "src/Greeter/Greeter.php" }
                ]
              }
            ]
          }
        ]
      }
      """

  # PHP's traits are the other half of the interface story and they are the
  # stanza's one deliberate false negative. This scenario is where it is written
  # down, because a gap that is documented is a decision and a gap that is
  # discovered is a bug.
  #
  # A trait is horizontal reuse: at compile time its members are *flattened into*
  # the using class, so `class Counter implements \Countable { use CountableTrait; }`
  # really does satisfy `Countable` in PHP — more strongly than Ruby's `include`,
  # which leaves the method the module's. In this graph it does not: link gathers a
  # method set by descriptor prefix over the whole occurrence table
  # (store/sqlc/query.sql), the only definition row is `CountableTrait#count().`,
  # and `Counter`'s method set is empty.
  #
  # The fix would be to flatten, which needs the trait's file and so is forbidden
  # by §2.5 — and flattening only when the trait happens to share the class's file
  # would make a derivation's answer depend on how the source is laid out, so the
  # same program in one file and in two would produce two different graphs. A gap
  # that is the same everywhere can be documented; one that moves cannot.
  #
  # The two steps are the two halves of the claim. A trait `use` derives *no*
  # `implements` edge, which is the gap; and it derives no `imports` edge either,
  # which is the decision behind it — a trait is a `type` ending `#` and not, as
  # Ruby's module is, a `package` ending `/`, because nothing may be written
  # `CountableTrait\x` and a trait lives in a namespace exactly as a class does.
  Scenario: A trait gives methods and never an implementation
    Given the two-file PHP package
    And the artifact also holds "src/Counting/Countable.php":
      """
      <?php

      declare(strict_types=1);

      namespace Com\Example\Counting;

      interface Sized
      {
          public function size(): int;
      }
      """
    And the artifact also holds "src/Counting/SizedTrait.php":
      """
      <?php

      declare(strict_types=1);

      namespace Com\Example\Counting;

      trait SizedTrait
      {
          public function size(): int
          {
              return 0;
          }
      }
      """
    And the artifact also holds "src/Counting/Counter.php":
      """
      <?php

      declare(strict_types=1);

      namespace Com\Example\Counting;

      class Counter implements Sized
      {
          use SizedTrait;
      }
      """
    When the artifact is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "SizedTrait", role: "definition") {
          symbolKind
          descriptor
          definedIn {
            path
          }
        }
      }
      """
    Then the answer is:
      """
      {
        "occurrence": [
          {
            "symbolKind": "type",
            "descriptor": "scip-php composer codiq/greeter 1.0.0 Com/Example/Counting/SizedTrait#",
            "definedIn": [
              { "path": "src/Counting/SizedTrait.php" }
            ]
          }
        ]
      }
      """
    And "Counter" implements nothing
    And "src/Counting/Counter.php" does not import "src/Counting/SizedTrait.php"
