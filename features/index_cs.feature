# M9 — the C# extractor (SPEC.md §14 M9+, an additional-language task and the
# sixth language in this graph).
#
# M6 tested a claim about M1-M5's design — "a new language is one sub-package in
# `extract` + a query + a resolver; no other change" — and found one thing that
# was not true: a coordinate is a property of (repository, ecosystem) and index
# resolved one per repository. M7, M9-Rust, M9-Java and this task have tested the
# same claim with that fixed. Every scenario below runs against the schema, the
# store, the link pass and the MCP surface exactly as the Java task left them.
#
# The corpus is extract/cs/testdata/greeter/, reused rather than duplicated and
# deliberately the same shape as the Go, TypeScript, Python, Rust and Java ones:
# a `Directory.Build.props` naming Codiq.Greeter, a `Greeter.cs` declaring the
# type and its methods, and a `Program.cs` that builds one and calls it. Two
# files, one call across the boundary between them. Scenarios that need more add
# to a copy, so the fixture stays the fixture.
#
# Five things about C# that the descriptors below only make sense against:
#
#   * A file's namespace is the one it *declares*, as Java's is — but C# spells
#     it two ways and both are here. `namespace Com.Example.Greeting;` is the
#     file-scoped form: everything after it is its sibling, so it can only be a
#     property of the file. `namespace Com.Example.Greeting { … }` is a block:
#     it nests, and one file may hold several. The stanza reads the second with
#     the same ancestor walk that gives a nested type its `#` levels, which is
#     why the fourth scenario can write one file each way and get byte-identical
#     descriptors out. The declaration is preferred over the path, always: the
#     fixture is laid out the way a .NET solution is, under `src/<Project>/`,
#     and no `using` anywhere writes that prefix.
#   * A type declared inside a type is another `#` level of the descriptor. C#
#     is the second language in this graph that can do it at all, and the rule
#     is Java's unchanged.
#   * C# has one member operator where Rust has two, and `a.b` is a member of a
#     value, a member of a type, or a segment of a namespace with nothing in the
#     syntax to say which — and, unlike Java, **no naming convention to split
#     them on**: `System.Text.StringBuilder` is PascalCase end to end. The
#     stanza splits on what the file has been *told* instead, by a `using`, by
#     its own declaration, or by the two roots the platform reserves.
#   * C# declares interface satisfaction explicitly, as Java does, and it also
#     has a shape Java does not: an explicit implementation, `string
#     ISpeaker.Greet()`. The fifth scenario is about the Go rule surviving both.
#   * The coordinate is `scip-csharp nuget <PackageId> <Version>`, from
#     Directory.Build.props (coord/nuget.go). Nothing it can produce shares a
#     prefix with `scip-go gomod …`, `scip-typescript npm …`, `scip-python pip
#     …`, `scip-rust cargo …` or `scip-java maven …`, which is the whole of why
#     six ecosystems can share one `occurrence` table.
#
# THE MILESTONE'S STATED TEST is the third scenario, and it is a strictly harder
# corpus than the Java task's. There, four of five languages rendered
# byte-identical suffixes for `greet` and the coordinate was the only thing
# keeping them apart. Here *five* of the six do.

Feature: Indexing a C# artifact
  As an agent navigating a polyglot codebase
  I want C# in the same graph, under the same model, as Go, TypeScript, Python, Rust and Java
  So that one query language reaches every symbol regardless of what wrote it

  Background:
    Given an empty CodiQ graph

  # The M2 scenario in a sixth language, with not one line of the pipeline
  # between them changed. Neither file's extraction ever saw the other — §5's
  # file-local contract holds per language, not per product — so at the end of
  # extraction Program.cs held a reference it could not resolve and Greeter.cs
  # held a definition nobody had asked for, and the edge below exists because the
  # link pass (§7) matched their descriptor strings.
  #
  # What made the match possible is two readings, and C# needs both where Java
  # needed one. `g.Greet()` says nothing about what `g` is, so the mapper reads
  # the type off the declaration — `Greeter g = …`, where C# writes the type down
  # and only `var` makes it inferable. And the simple name `Greeter` says nothing
  # about which namespace it came from, because a `using` is on-demand and brings
  # in every name of a namespace while writing none of them down; the file has
  # exactly one `using` naming a namespace of this artifact, so there is exactly
  # one namespace it can have come from. Together those give
  # `Com/Example/Greeting/Greeter#Greet().`, which is what Greeter.cs
  # independently wrote for its own method.
  #
  # `System.Console.WriteLine(message)` is in the same method and produces no
  # second entry, which is the honest half of the same mechanism: it resolves to
  # the platform's coordinate, which owns no file this index reads.
  Scenario: A call is traversable across files to the definition it names
    Given the two-file C# artifact
    When the artifact is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "Main", role: "definition") {
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
            "name": "Main",
            "calls": [
              {
                "name": "Greet",
                "symbolKind": "method",
                "descriptor": "scip-csharp nuget Codiq.Greeter 1.0.0 Com/Example/Greeting/Greeter#Greet().",
                "definedIn": [
                  { "path": "src/Greeter/Greeter.cs" }
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
  # NuGet coordinate, which is the part that has to be right for six ecosystems
  # to share a table — scheme, manager, package name and version, all four read
  # out of Directory.Build.props by a resolver that changed nothing to slot in
  # beside go.mod's, package.json's, pyproject.toml's, Cargo.toml's and
  # pom.xml's.
  #
  # The second read is the same join one level up, and it is the namespace
  # decision made checkable. `Greeter.cs` sits at `src/Greeter/` and declares
  # `namespace Com.Example.Greeting;`; `Program.cs` writes `using
  # Com.Example.Greeting;` and has never seen it. The path would have yielded
  # `src/Greeter/` on one side and the using directive `Com/Example/Greeting/` on
  # the other, and there would be no edge at all. Both entries below are that
  # claim.
  Scenario: The cross-file join is on the descriptor and nothing else
    Given the two-file C# artifact
    When the artifact is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "Greet", role: "definition") {
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
            "descriptor": "scip-csharp nuget Codiq.Greeter 1.0.0 Com/Example/Greeting/Greeter#Greet().",
            "definedIn": [
              {
                "path": "src/Greeter/Greeter.cs",
                "lang": "cs",
                "pkgScheme": "scip-csharp",
                "pkgManager": "nuget",
                "pkgName": "Codiq.Greeter",
                "pkgVersion": "1.0.0"
              }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "descriptor": "scip-csharp nuget Codiq.Greeter 1.0.0 Com/Example/Greeting/Greeter#Greet()."
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
            "descriptor": "scip-csharp nuget Codiq.Greeter 1.0.0 Com/Example/Greeting/",
            "definedIn": [
              { "path": "src/Greeter/Greeter.cs" }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "name": "Greeting",
                "symbolKind": "package",
                "descriptor": "scip-csharp nuget Codiq.Greeter 1.0.0 Com/Example/Greeting/"
              }
            ]
          }
        ]
      }
      """
    And "src/App/Program.cs" imports "src/Greeter/Greeter.cs"

  # §14 M9+'s inherited test — "mixed-language repo; a cross-language query
  # returns both" — with six languages rather than two. One repository, one
  # manifest per ecosystem, one `codiq` run.
  #
  # The corpus is built to collide as hard as six languages can be made to,
  # because a corpus that cannot collide proves nothing. All six derive the
  # namespace `greeter/` — Go from the directory, TypeScript from `greeter.ts`,
  # Python from `greeter.py`, Rust from `src/greeter.rs` with `src/` dropped,
  # Java from a `package greeter;` clause and C# from a `namespace greeter;` one
  # — and all six declare a type called `Greeter`. The Java task made four of the
  # *methods* collide; this makes five, so `greeter/Greeter#greet().` is written
  # byte for byte by TypeScript, Python, Rust, Java and C# and nothing but four
  # leading words separates them.
  #
  # The C# half is the one place in this corpus written against its own
  # language's conventions rather than with them: .NET names a namespace and a
  # method in PascalCase, and this writes `namespace greeter;` and `greet()`
  # outright. Both are legal C# and neither is idiomatic, and that is the point —
  # the collision is the fixture, and a `Greet()` that could not collide would
  # have tested nothing. Everything else about it is ordinary: a
  # Directory.Build.props beside the five manifests already there, a `greeter`
  # namespace declaring the type, and an `app` namespace that builds one and
  # calls it.
  #
  # The C# caller assigns a public field rather than passing a constructor
  # argument, which is the Java and Rust callers' choice for the same reason: a
  # declared constructor is a member of its type named after it, so `Greeter`
  # would be the name of two definitions in one file and "defined once in each
  # language" would stop meaning what it says.
  #
  # Each read below is written to match exactly one row, for the reason
  # index_ts.feature gives — the GraphQL layer emits no ORDER BY, so a query
  # matching a row per language would be a coin flip. The six entry points are
  # therefore named apart (`main`, `boot`, `run`, `start`, `launch`, `begin`)
  # while everything they reach is named together, which is what lets each
  # traversal ask the question that matters: not "is this language present" but
  # "did *this* language's call find its own definition and only it".
  #
  # The last five steps are the half a traversal cannot state. The first of them
  # is what makes the rest non-vacuous — it asserts the collision is real across
  # all five of the languages that share a suffix — and the last is what the
  # scenario exists for: "no derived edge joins two languages" is a claim about
  # the absence of a row anywhere, and it is strictly stronger than the scheme
  # check beside it, because the defect that kept a mixed repository out of M6
  # stamped two languages with the *same* scheme and would have passed a scheme
  # comparison unnoticed.
  Scenario: One mixed repository of six languages keeps all six apart
    Given the mixed-language repository of six languages
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
    And an agent asks over MCP:
      """
      {
        occurrence(name: "begin", role: "definition") {
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
                "descriptor": "scip-csharp nuget Codiq.Mixed 6.0.0 greeter/Greeter#greet().",
                "definedIn": [
                  { "path": "src/greeter/Greeter.cs", "lang": "cs" }
                ]
              }
            ]
          }
        ]
      }
      """
    And the "ts", "py", "rs", "java" and "cs" definitions of "greet" differ only in their coordinate
    And "Greeter" is defined once in each of "go", "ts", "py", "rs", "java" and "cs"
    And the graph holds definitions written in all of "go", "ts", "py", "rs", "java" and "cs"
    And no derived edge joins two package schemes
    And no derived edge joins two languages

  # Nested types and block namespaces, in one scenario because they are one rule.
  #
  # Each named container on the way out of the CST contributes exactly one
  # component, so a `namespace … { }` contributes `A/`, a type contributes `T#`
  # and a method contributes `M().` — and `Registry#Entry#Label().` falls out of
  # the same walk whether the namespace above it was written with braces or with
  # a semicolon. Registry.cs writes the block form and Lookup.cs the file-scoped
  # one, and the descriptors below are the assertion that the two agree: if they
  # did not, there would be no edge.
  #
  # Splitting `Com.Example.Greeting.Registry.Entry` into a namespace and two
  # types is where C# parts company with Java. Java splits on its naming
  # convention — lowercase segments are the package, the first uppercase one
  # begins the type — and C# has no such convention, because the framework
  # guidelines put namespaces and types alike in PascalCase. So the split is on
  # what the file has been told: `using Com.Example.Greeting;` says that much is
  # a namespace, and what follows is types.
  #
  # The `calls` edge is the evidence the two sides agreed: `Describe` reaches a
  # method two `#` levels down in a file it has never seen.
  Scenario: A nested class is a level of the descriptor, and a block namespace is one too
    Given the two-file C# artifact
    And the artifact also holds "src/Greeter/Registry.cs":
      """
      namespace Com.Example.Greeting
      {
          public sealed class Registry
          {
              public sealed class Entry
              {
                  private readonly string _text;

                  public Entry(string text)
                  {
                      _text = text;
                  }

                  public string Label()
                  {
                      return _text;
                  }
              }
          }
      }
      """
    And the artifact also holds "src/App/Lookup.cs":
      """
      using Com.Example.Greeting;

      namespace Com.Example.App;

      public static class Lookup
      {
          public static string Describe()
          {
              Registry.Entry e = new Registry.Entry("first");
              return e.Label();
          }
      }
      """
    When the artifact is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "Label", role: "definition") {
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
            "descriptor": "scip-csharp nuget Codiq.Greeter 1.0.0 Com/Example/Greeting/Registry#Entry#Label().",
            "definedIn": [
              { "path": "src/Greeter/Registry.cs" }
            ],
            "resolvedFrom": [
              {
                "role": "reference",
                "descriptor": "scip-csharp nuget Codiq.Greeter 1.0.0 Com/Example/Greeting/Registry#Entry#Label()."
              }
            ]
          }
        ]
      }
      """
    And an agent asks over MCP:
      """
      {
        occurrence(name: "Describe", role: "definition") {
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
                "name": "Label",
                "descriptor": "scip-csharp nuget Codiq.Greeter 1.0.0 Com/Example/Greeting/Registry#Entry#Label().",
                "definedIn": [
                  { "path": "src/Greeter/Registry.cs" }
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
  # It has now held unmodified for Go, TypeScript, Python, Rust, Java and, here,
  # C#. That is the sixth language and the second one that writes the word down:
  # `class Greeter : ISpeaker` is as explicit as a declaration gets, and the
  # stanza still needs no edge kind for saying so — the clause is recorded as
  # what it structurally is, a reference to the interface's type, so the explicit
  # declaration stays navigable with no schema change at all.
  #
  # The second read is the shape Java has no counterpart for, and it is the one
  # place C# forced a descriptor decision. An *explicit* implementation — `string
  # IAnnouncer.Announce()` — is a member whose declaration names the interface it
  # satisfies, and its descriptor drops that qualifier: `Announcer#Announce().`
  # and not `Announcer#IAnnouncer.Announce().`. Keeping the qualifier would put a
  # suffix in the class's method set that does not contain the interface's, so a
  # class implementing an interface explicitly would implement nothing — and the
  # qualified descriptor is one no reference site could reconstruct anyway, since
  # an explicit implementation is only ever *called* through the interface. The
  # `implementedBy` entry below is that decision, checked.
  #
  # `interface` is a keyword, so recognising the interface needs none of the base
  # inspection the Python stanza does — which is what `symbolKind` below is
  # asserting and what link's derivation keys off. The derivation stays
  # approximate in exactly the way §4.4 says: signatures are not compared, so
  # this asserts structural satisfaction and not C#'s. The constructor `Greeter`
  # sits in the same method set and does not disturb it, because containment is
  # what is asked and an interface has no constructor to be missing.
  #
  # ISpeaker.cs is added rather than being part of the fixture, and Greeter.cs
  # already names it: the base corpus declares `: ISpeaker` against a type no
  # file defines, which is an unresolved reference and exactly what §4.3 says
  # such a thing should be.
  Scenario: An interface is implemented across a file boundary by the Go rule, explicitly or not
    Given the two-file C# artifact
    And the artifact also holds "src/Greeter/ISpeaker.cs":
      """
      namespace Com.Example.Greeting;

      public interface ISpeaker
      {
          string Greet();
      }
      """
    And the artifact also holds "src/Greeter/IAnnouncer.cs":
      """
      namespace Com.Example.Greeting;

      public interface IAnnouncer
      {
          string Announce();
      }
      """
    And the artifact also holds "src/Greeter/Announcer.cs":
      """
      namespace Com.Example.Greeting;

      public sealed class Announcer : IAnnouncer
      {
          string IAnnouncer.Announce()
          {
              return "now speaking";
          }
      }
      """
    When the artifact is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "ISpeaker", role: "definition") {
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
            "descriptor": "scip-csharp nuget Codiq.Greeter 1.0.0 Com/Example/Greeting/ISpeaker#",
            "implementedBy": [
              {
                "name": "Greeter",
                "symbolKind": "type",
                "descriptor": "scip-csharp nuget Codiq.Greeter 1.0.0 Com/Example/Greeting/Greeter#",
                "definedIn": [
                  { "path": "src/Greeter/Greeter.cs" }
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
        occurrence(name: "IAnnouncer", role: "definition") {
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
    And the answer is:
      """
      {
        "occurrence": [
          {
            "symbolKind": "interface",
            "descriptor": "scip-csharp nuget Codiq.Greeter 1.0.0 Com/Example/Greeting/IAnnouncer#",
            "implementedBy": [
              {
                "name": "Announcer",
                "symbolKind": "type",
                "descriptor": "scip-csharp nuget Codiq.Greeter 1.0.0 Com/Example/Greeting/Announcer#",
                "definedIn": [
                  { "path": "src/Greeter/Announcer.cs" }
                ]
              }
            ]
          }
        ]
      }
      """

  # A partial class is C#'s own contribution to this graph, and no earlier
  # language here has anything like it: `partial class Loud` in two files
  # declares *one* type, so two definition rows carry one descriptor.
  #
  # That is what the link pass wants rather than something it has to survive. A
  # descriptor is a coordinate and a structural path and says nothing about which
  # file a symbol was written in, so two rows with one descriptor are two *sites*
  # of one symbol — which is exactly what a partial class is. The two claims
  # below are the two halves of that.
  #
  # The traversal is the first half: a call into the type resolves to the site
  # that declares the member, and only that site. `Whisper` is declared in one of
  # the two files and reached from a third that has seen neither.
  #
  # The last step is the second half, and it is the one that could have gone
  # wrong. `implements` gathers a type's method set with
  # `starts_with(member.descriptor, type.descriptor)` over the whole occurrence
  # table and no file predicate (store/sqlc/query.sql), so both halves of a
  # partial class see the *union* of what either declares — and `ILoud`, which
  # asks for `Shout()` and `Whisper()`, is satisfied even though neither file
  # alone declares both. A per-file method set would have got that wrong, and
  # nothing about the descriptor had to change for it to be right.
  #
  # Both claims are SQL rather than MCP for the reason the other suites give: two
  # definition rows share a descriptor, so a traversal reaching the type returns
  # them in an order the GraphQL layer does not promise, and "declared in two
  # files under one descriptor" is a fact about the rows rather than a path
  # through them.
  Scenario: A partial class is one type declared in two files
    Given the two-file C# artifact
    And the artifact also holds "src/Greeter/ILoud.cs":
      """
      namespace Com.Example.Greeting;

      public interface ILoud
      {
          string Shout();

          string Whisper();
      }
      """
    And the artifact also holds "src/Greeter/Loud.Shout.cs":
      """
      namespace Com.Example.Greeting;

      public partial class Loud : ILoud
      {
          public string Shout()
          {
              return "HELLO";
          }
      }
      """
    And the artifact also holds "src/Greeter/Loud.Whisper.cs":
      """
      namespace Com.Example.Greeting;

      public partial class Loud
      {
          public string Whisper()
          {
              return "hello";
          }
      }
      """
    And the artifact also holds "src/App/Herald.cs":
      """
      using Com.Example.Greeting;

      namespace Com.Example.App;

      public static class Herald
      {
          public static string Proclaim(Loud l)
          {
              return l.Whisper();
          }
      }
      """
    When the artifact is indexed
    And an agent asks over MCP:
      """
      {
        occurrence(name: "Proclaim", role: "definition") {
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
                "name": "Whisper",
                "descriptor": "scip-csharp nuget Codiq.Greeter 1.0.0 Com/Example/Greeting/Loud#Whisper().",
                "definedIn": [
                  { "path": "src/Greeter/Loud.Whisper.cs" }
                ]
              }
            ]
          }
        ]
      }
      """
    And "Loud" is declared in 2 files under one descriptor
    And "ILoud" is implemented from every file "Loud" is declared in
