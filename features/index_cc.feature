# M9 — the C and C++ extractor (SPEC.md §14 M9+, an additional-language task and
# the ninth language in this graph).
#
# M6 tested a claim about M1-M5's design — "a new language is one sub-package in
# `extract` + a query + a resolver; no other change" — and found one thing that
# was not true. M7, M9-Rust, M9-Java, M9-C#, M9-Ruby and M9-PHP tested it again
# with that fixed. This task tests it against the first language family that
# arrives with **neither a manifest nor a module system**, and the honest report
# is that the claim holds for the pipeline and buckles once at the edge of it.
#
# Six things about C that the descriptors below only make sense against.
#
#   * **A declaration and a definition are different files**, and this is the
#     first language here of which that is true. `void greet();` in a header and
#     `void greet() { … }` in a source are one symbol written twice, and the core
#     model has two roles for the site a join lands on. The prototype is a
#     **reference** carrying the byte-identical descriptor, so the header
#     resolves *into* the source rather than competing with it for every caller
#     in the corpus — the second scenario. A class member is the exception,
#     because a C++ class body is the definition of what the class has.
#   * **There is no namespace in C at all**, and that is load-bearing rather
#     than a gap. Every external symbol is in one flat namespace shared by the
#     linked program, so the descriptor carries no directory — which is the only
#     reason `include/greeter.h` and `src/greeter.c` can name one symbol.
#     Internal linkage is the exception and is keyed on the file, because
#     `static void helper(void)` in two files is two functions.
#   * **`#include` is textual**, and the path is resolved by the build system's
#     `-I` search rather than by anything in the source. The file-local
#     approximation is to offer every resolution the join could want: an included
#     file emits a `package` definition per suffix of its own path, an `#include`
#     emits a `package` reference for the path as written, and link's `imports`
#     derivation matches them unchanged — the third scenario.
#   * **The preprocessor is invisible.** tree-sitter parses unexpanded text, so a
#     macro that declares a function, a token-pasted name and the unselected arm
#     of an `#ifdef` are all read as the text they are and not as the program
#     they become. That is not a fidelity gap a better query closes; it is the
#     difference between reading C and compiling it.
#   * **C++ has interfaces without the keyword.** A class whose member functions
#     are all pure virtual and which declares no data members is what every C++
#     codebase writes where Java writes `interface`, and promoting it to
#     `symbol_kind = 'interface'` is what makes link's method-set containment
#     derivation fire at all here — the fifth scenario. Destructors are emitted
#     nowhere, because C++ declares one for every class whether the author writes
#     it or not and it is named after its class, so an interface's method set
#     containing `~Speaker().` would be one no implementer could ever satisfy.
#   * The coordinate is `scip-cc cmake <name> <version>`, read from a
#     `CMakeLists.txt` (coord/cmake.go) — the first manifest here that is a
#     *program* rather than a document, and the first ecosystem with no package
#     manager at all. `project(greeter VERSION 1.0.0)` is a call whose literal
#     arguments are pattern-matched; anything computed reduces to Unknown.
#     Nothing it can produce shares a prefix with the eight schemes before it,
#     which is the whole of why nine ecosystems can share one `occurrence` table.
#     What it costs is stated where it is felt: a Makefile-only, autotools, Meson
#     or Bazel C project has no registered manifest, and `coord.Resolve` fails a
#     whole run rather than indexing it — which for C is the ordinary case rather
#     than an edge case.
#
# THE MILESTONE'S STATED TEST is the fourth scenario. C++ collides with the six
# languages that already share a suffix; C **cannot**, and the reason is exact
# rather than a shortfall: C has no namespace at all, so a C `greet` renders
# `greet().` with nothing in front of it and there is no `greeter/` for it to
# carry. The corpus therefore contributes C++, which has namespaces and writes
# `namespace greeter` outright — and the pair share one `file.lang`, which is
# itself the point: a `.h` does not say whether it is C or C++, and tagging them
# apart would make a pure-C project's header-into-source edge a cross-language
# edge.

Feature: Indexing a C and C++ project
  As an agent navigating a polyglot codebase
  I want C and C++ in the same graph, under the same model, as Go, TypeScript, Python, Rust, Java, C#, Ruby and PHP
  So that one query language reaches every symbol regardless of what wrote it

  Background:
    Given an empty CodiQ graph

  # The M2 scenario in a ninth language, with not one line of the pipeline
  # between them changed. Neither file's extraction ever saw the other — §5's
  # file-local contract holds per language, not per product — so at the end of
  # extraction main.c held a reference it could not resolve and greeter.c held a
  # definition nobody had asked for, and the edge below exists because the link
  # pass (§7) matched their descriptor strings.
  #
  # There is exactly one `calls` row, and that is the whole point of the role
  # rule. `include/greeter.h` declares `greet` too; had the prototype been a
  # definition, this traversal would return the same callee twice — once at the
  # body and once at a declaration that calls nothing — and every cross-file call
  # in every C program indexed here would be doubled.
  #
  # `printf` produces no entry, which is the honest half of the same mechanism:
  # it is the C standard library's, this index owns no definition of it, and the
  # reference carries a foreign coordinate rather than being guessed at.
  Scenario: A call is traversable across files to the definition it names
    Given the C project
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
                "symbolKind": "function",
                "descriptor": "scip-cc cmake greeter 1.0.0 greet().",
                "definedIn": [
                  { "path": "src/greeter.c" }
                ]
              }
            ]
          }
        ]
      }
      """

  # The milestone's signature scenario, and the one no language before this one
  # could have had: a *declaration* and a *definition* in two files, joined.
  #
  # `greet` is defined once, in `src/greeter.c`, and the descriptor carries no
  # directory — not `src/`, not `include/` — because C has no namespace and
  # inventing one from the tree would put the header and the source behind
  # prefixes that can never match. The declaration in `include/greeter.h` is a
  # reference with the identical string, so the header resolves into the source:
  # "go to definition" run on a declaration, for free, out of the same
  # descriptor join every other language uses.
  #
  # The last step is the exception the empty namespace forces. `fallback` is
  # `static`, so it is descriptored under `src/greeter.c/` and no other file can
  # reach it — which is exactly C's rule, and which is what stops two unrelated
  # files' `static void helper(void)` from resolving into each other.
  Scenario: A declaration is a reference, and it resolves into the definition
    Given the C project
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
            "symbolKind": "function",
            "descriptor": "scip-cc cmake greeter 1.0.0 greet().",
            "definedIn": [
              {
                "path": "src/greeter.c",
                "lang": "cc",
                "pkgScheme": "scip-cc",
                "pkgManager": "cmake",
                "pkgName": "greeter",
                "pkgVersion": "1.0.0"
              }
            ]
          }
        ]
      }
      """
    And "include/greeter.h" resolves into "src/greeter.c"
    And "greet" is defined once
    And no reference outside "src/greeter.c" resolves to "fallback"

  # `#include` is the whole of C's import story and it is not an import of a
  # *name*: it pastes a file in, and which file is a fact about the build
  # system's `-I` search rather than about the source.
  #
  # Both files write `#include "greeter.h"` while the header lives at
  # `include/greeter.h`, so nothing in either file names the target as it sits on
  # disk. The edge exists because the header emitted a `package` definition for
  # *every suffix of its own path* — `greeter.h/` and `include/greeter.h/` — and
  # the join picked the one the include spelled. That is the build system's
  # search, approximated by the only means a file-local reader has.
  #
  # The descriptor is asserted on both sides of the edge, byte for byte, for the
  # reason index_go.feature gives: the day the two stop agreeing is the day
  # cross-file navigation silently returns nothing. Here it also carries the
  # CMake coordinate, which is the part that has to be right for nine ecosystems
  # to share a table — scheme, manager, package name and version, all four read
  # out of a `CMakeLists.txt` by a resolver that changed nothing to slot in
  # beside the eight before it.
  #
  # The path-suffix step is checked apart, because it is a claim about the shape
  # of the definitions rather than about a traversal, and because a query by name
  # would match both suffixes in an order the GraphQL layer does not promise.
  Scenario: The cross-file join is on the descriptor, and #include derives the import
    Given the C project
    When the artifact is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "greeter_name", role: "definition") {
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
            "symbolKind": "function",
            "descriptor": "scip-cc cmake greeter 1.0.0 greeter_name().",
            "definedIn": [
              {
                "path": "src/greeter.c",
                "lang": "cc",
                "pkgScheme": "scip-cc",
                "pkgManager": "cmake",
                "pkgName": "greeter",
                "pkgVersion": "1.0.0"
              }
            ]
          }
        ]
      }
      """
    And "include/greeter.h" offers the include targets "greeter.h/" and "include/greeter.h/"
    And "src/main.c" imports "include/greeter.h"
    And "src/greeter.c" imports "include/greeter.h"

  # §14 M9+'s inherited test — "mixed-language repo; a cross-language query
  # returns both" — with nine languages rather than two. One repository, one
  # manifest per ecosystem, one `codiq` run.
  #
  # Seven languages now write `greeter/Greeter#greet().` byte for byte —
  # TypeScript, Python, Rust, Java, C#, PHP and **C++** — with nothing but four
  # leading words keeping them apart. C++ joins that collision by writing
  # `namespace greeter` outright, which is unidiomatic (the convention is
  # `Greeter`) and legal, exactly as the PHP and C# halves are.
  #
  # **C itself cannot join it, and the reason is the language rather than a
  # shortfall.** Ruby could not collide because `module greeter` is a syntax
  # error; C cannot collide because there is no `module` at all. A C `greet` is
  # `greet().` with nothing in front of it, because every external symbol in a C
  # program is in one flat namespace — so there is no `greeter/` component for it
  # to carry, and manufacturing one from the directory would break the one join C
  # actually has. The corpus therefore contributes C++, and the two share one
  # `file.lang` because a `.h` does not say which of them it is.
  #
  # The ninth entry point is `embark`, after `main`, `boot`, `run`, `start`,
  # `launch`, `begin`, `commence` and `initiate`. It is worth remarking that
  # `main` — taken by the *first* language in this corpus — is also C's own
  # entry point, the one name in the language the standard actually reserves for
  # it. This corpus's C++ half declares none, which is not a contrivance: a
  # translation unit without `main` is an ordinary C++ file, it is simply not the
  # one you link last.
  #
  # The last four steps are the half a traversal cannot state, and the last is
  # what the scenario exists for: "no derived edge joins two languages" is a
  # claim about the absence of a row anywhere, and it is strictly stronger than
  # the scheme check beside it.
  Scenario: One mixed repository of nine languages keeps all nine apart
    Given the mixed-language repository of nine languages
    When the repository is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "embark", role: "definition") {
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
                "descriptor": "scip-cc cmake mixed-cc 9.0.0 greeter/Greeter#greet().",
                "definedIn": [
                  { "path": "cc/greeter.hpp", "lang": "cc" }
                ]
              }
            ]
          }
        ]
      }
      """
    And the "ts", "py", "rs", "java", "cs", "php" and "cc" definitions of "greet" differ only in their coordinate
    And "Greeter" is defined once in each of "go", "ts", "py", "rs", "java", "cs", "rb", "php" and "cc"
    And the graph holds definitions written in all of "go", "ts", "py", "rs", "java", "cs", "rb", "php" and "cc"
    And no derived edge joins two package schemes
    And no derived edge joins two languages

  # `implements` has held unmodified for Go, TypeScript, Python, Rust, Java, C#
  # and PHP; Ruby was the one language that could not take part. C++ takes part,
  # and it has to be *made* to — the language has no `interface` keyword, so the
  # stanza recognises the shape every C++ codebase uses instead: a class whose
  # member functions are all pure virtual and which declares no data members.
  #
  # Two decisions have to be right for this to fire and both are visible here.
  # The promotion to `symbol_kind = 'interface'` is one, since link keys the
  # derivation on it. The other is that **destructors are emitted nowhere**: C++
  # declares one for every class whether it is written or not, and it is named
  # after its class, so `Speaker#~Speaker().` in the interface's method set would
  # be a member `Loud` could never have — and containment would fail for every
  # C++ interface that declares the virtual destructor good C++ always declares.
  #
  # The interface is added rather than in the base corpus for the reason the C#
  # suite gives: `implements` is cross-file by construction
  # (`c.file_id <> i.file_id`, store/sqlc/query.sql).
  Scenario: An abstract base class declared in another file is implemented
    Given the C project
    And the artifact also holds "include/speaker.hpp":
      """
      #pragma once

      namespace greeter {

      class Speaker {
      public:
          virtual ~Speaker() = default;
          virtual const char *greet() const = 0;
      };

      }  // namespace greeter
      """
    And the artifact also holds "include/loud.hpp":
      """
      #pragma once

      namespace greeter {

      class Loud {
      public:
          const char *greet() const { return "HI"; }
      };

      }  // namespace greeter
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
            "descriptor": "scip-cc cmake greeter 1.0.0 greeter/Speaker#",
            "implementedBy": [
              {
                "name": "Loud",
                "symbolKind": "type",
                "descriptor": "scip-cc cmake greeter 1.0.0 greeter/Loud#",
                "definedIn": [
                  { "path": "include/loud.hpp" }
                ]
              }
            ]
          }
        ]
      }
      """

  # `extern "C"` is the construct that makes C and C++ one ecosystem rather than
  # two, and this is the scenario that says so with a row.
  #
  # The declaration below sits inside `namespace greeter`, so to C++'s own name
  # lookup it is `greeter::c_greet`. This stanza renders it `c_greet().` with no
  # namespace at all, which is a knowing departure from C++ and the only reading
  # that works: the construct exists so that a C translation unit can call the
  # function, C has one namespace, and a `greeter/c_greet().` would be guaranteed
  # never to join the `void c_greet(…) { … }` in the .c file it was written to
  # reach.
  #
  # This is also why `scip-c` and `scip-cpp` are not two schemes. The link pass
  # joins on the rendered descriptor and nothing else (§7), so splitting the
  # coordinate would not describe two languages — it would break the one join the
  # pair exists to make.
  Scenario: extern "C" makes a C++ declaration and a C definition one symbol
    Given the C project
    And the artifact also holds "include/bridge.hpp":
      """
      #pragma once

      namespace greeter {

      extern "C" {
      void c_greet(const char *name);
      }

      }  // namespace greeter
      """
    And the artifact also holds "src/bridge.c":
      """
      #include <stdio.h>

      void c_greet(const char *name) {
          printf("%s\n", name);
      }
      """
    When the artifact is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "c_greet", role: "definition") {
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
            "descriptor": "scip-cc cmake greeter 1.0.0 c_greet().",
            "definedIn": [
              { "path": "src/bridge.c", "lang": "cc" }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "descriptor": "scip-cc cmake greeter 1.0.0 c_greet()."
              }
            ]
          }
        ]
      }
      """
