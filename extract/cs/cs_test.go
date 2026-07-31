package cs_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/extract/cs"
	"github.com/gaarutyunov/codiq/facts"
)

// testCoord is the coordinate the batch would hand the parser for a file in the
// artifact Codiq.Greeter, rooted at the fixture directory.
func testCoord(t *testing.T) coord.Coord {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "greeter"))
	require.NoError(t, err)
	coords, err := coord.Resolve(root)
	require.NoError(t, err)
	c := coords.For("X" + cs.Ext)
	require.Equal(t, coord.CSharpScheme, c.Scheme, "the fixture must resolve through the NuGet resolver")
	return c
}

// parse parses src as the file at name, which is interpreted relative to the
// artifact root. As in Java and unlike Rust, the path is *not* what names the
// file's namespace — the declaration is — so it is incidental here, and one test
// below is about exactly that.
func parse(t *testing.T, name, src string) facts.FileFacts {
	t.Helper()
	c := testCoord(t)
	ff := cs.New().Parse(filepath.Join(c.Root, filepath.FromSlash(name)), []byte(src), c)
	require.Empty(t, ff.ParseError)
	return ff
}

const prefix = "scip-csharp nuget Codiq.Greeter 1.0.0"

// platform is the coordinate references to .NET carry: foreign, so nothing under
// it can ever match a definition this index owns.
const platform = "scip-csharp nuget System ."

// --------------------------------------------------------------- definitions --

func TestParseDefinitionDescriptors(t *testing.T) {
	ff := parse(t, "src/Shapes/Shapes.cs", `
namespace Com.Example.Shapes;

public class Shapes
{
    public const int Level = 1;
    private string _label;
    public string Label { get; set; }

    public Shapes(string label)
    {
        _label = label;
    }

    public string Describe(int times)
    {
        string local = _label;
        return local;
    }
}

public interface IShape
{
    double Area();
}

public enum Kind { Round, Square }

public record Point(int X, int Y);

public struct Vec { public int X; }

public delegate void Handler(int code);
`)

	want := []string{
		prefix + " Com/Example/Shapes/",
		prefix + " Com/Example/Shapes/Handler#",
		prefix + " Com/Example/Shapes/Handler#(code)",
		prefix + " Com/Example/Shapes/IShape#",
		prefix + " Com/Example/Shapes/IShape#Area().",
		prefix + " Com/Example/Shapes/Kind#",
		prefix + " Com/Example/Shapes/Kind#Round.",
		prefix + " Com/Example/Shapes/Kind#Square.",
		prefix + " Com/Example/Shapes/Point#",
		prefix + " Com/Example/Shapes/Point#X.",
		prefix + " Com/Example/Shapes/Point#Y.",
		prefix + " Com/Example/Shapes/Shapes#",
		prefix + " Com/Example/Shapes/Shapes#Describe().",
		prefix + " Com/Example/Shapes/Shapes#Describe().(times)",
		prefix + " Com/Example/Shapes/Shapes#Describe().local.",
		prefix + " Com/Example/Shapes/Shapes#Label.",
		prefix + " Com/Example/Shapes/Shapes#Level.",
		prefix + " Com/Example/Shapes/Shapes#Shapes().",
		prefix + " Com/Example/Shapes/Shapes#Shapes().(label)",
		prefix + " Com/Example/Shapes/Shapes#_label.",
		prefix + " Com/Example/Shapes/Vec#",
		prefix + " Com/Example/Shapes/Vec#X.",
	}
	assert.Equal(t, want, definitionDescriptors(ff))
}

func TestParseDefinitionKinds(t *testing.T) {
	tests := []struct {
		name string
		src  string
		kind map[string]string
	}{
		{
			name: "a class is data and an interface declares behaviour",
			src:  "public class C { }\npublic interface I { }",
			kind: map[string]string{"C": facts.KindType, "I": facts.KindInterface},
		},
		{
			name: "a struct, a record, an enum and a delegate are all types",
			src:  "public struct S { }\npublic record R(int A);\npublic enum E { X }\npublic delegate void D();",
			kind: map[string]string{
				"S": facts.KindType, "R": facts.KindType,
				"E": facts.KindType, "D": facts.KindType,
			},
		},
		{
			// A property and an event are state a type holds, which is what the
			// neutral core's `field` kind means. The accessors C# generates
			// around them are not what a caller names.
			name: "a property and an event are fields",
			src:  "using System;\npublic class C { public int P { get; set; } public event EventHandler? Ev; }",
			kind: map[string]string{"P": facts.KindField, "Ev": facts.KindField},
		},
		{
			// C# has no distinct node for a constant, so the modifier is the
			// only thing that says so.
			name: "a const field is a constant and a plain one is a field",
			src:  "public class C { public const int K = 1; private int f; }",
			kind: map[string]string{"K": facts.KindConstant, "f": facts.KindField},
		},
		{
			// `record Point(int X, int Y)` declares state and an accessor for
			// it, not an argument — so a positional component is a field.
			name: "a record's positional component is a field",
			src:  "public record Point(int X);",
			kind: map[string]string{"X": facts.KindField},
		},
		{
			// A class's primary constructor is a constructor: C# generates no
			// member for its parameters, so they stay parameters.
			name: "a class's primary constructor parameter stays a parameter",
			src:  "public class C(int seed) { }",
			kind: map[string]string{"seed": facts.KindParameter},
		},
		{
			// A method belongs to a type; a local function does not, which is
			// the whole of the distinction the two kinds carry.
			name: "a method is a method and a local function is a function",
			src:  "public class C { void M() { void inner() { } } }",
			kind: map[string]string{"M": facts.KindMethod, "inner": facts.KindFunction},
		},
		{
			name: "a type parameter is a parameter",
			src:  "public class C<T> { }",
			kind: map[string]string{"T": facts.KindParameter},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ff := parse(t, "src/App/Main.cs", "namespace Com.Example.App;\n\n"+tc.src+"\n")
			defs := definitionsByName(ff)
			for name, kind := range tc.kind {
				require.Contains(t, defs, name)
				assert.Equal(t, kind, defs[name].SymbolKind, "kind of %s", name)
			}
		})
	}
}

// TestAConstructorIsAMethodNamedAfterItsType is kept apart from the kinds table
// because the descriptor is the interesting half: a constructor is a member of
// its type named after it, so it collides with nothing and needs no special
// component.
func TestAConstructorIsAMethodNamedAfterItsType(t *testing.T) {
	ff := parse(t, "src/App/Main.cs", `
namespace Com.Example.App;

public class Widget
{
    public Widget() { }
}
`)
	assertHasDefinition(t, ff, prefix+" Com/Example/App/Widget#")
	assertHasDefinition(t, ff, prefix+" Com/Example/App/Widget#Widget().")
	assert.Equal(t, facts.KindMethod, definitionNamed(t, ff, "Widget", facts.KindMethod).SymbolKind)
}

// TestTheRarerDeclarationFormsAreCaptured is a tripwire on the query itself: the
// forms below are the ones a small corpus never exercises and a grammar bump is
// most likely to rename out from under it.
func TestTheRarerDeclarationFormsAreCaptured(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "an explicit property accessor pair",
			src:  "public class C { public int P { get { return 1; } set { } } }",
			want: prefix + " Com/Example/App/C#P.",
		},
		{
			name: "an event with explicit accessors",
			src:  "using System;\npublic class C { public event EventHandler Ev { add { } remove { } } }",
			want: prefix + " Com/Example/App/C#Ev.",
		},
		{
			name: "a foreach binding",
			src:  "public class C { void M(int[] xs) { foreach (var x in xs) { } } }",
			want: prefix + " Com/Example/App/C#M().x.",
		},
		{
			name: "a using statement's binding",
			src:  "using System;\npublic class C { void M() { using (var d = Make()) { } } IDisposable Make() { return null; } }",
			want: prefix + " Com/Example/App/C#M().d.",
		},
		{
			name: "a catch parameter",
			src:  "using System;\npublic class C { void M() { try { } catch (Exception e) { } } }",
			want: prefix + " Com/Example/App/C#M().(e)",
		},
		{
			name: "a lambda parameter",
			src:  "using System;\npublic class C { void M() { Func<int, int> f = (y) => y; } }",
			want: prefix + " Com/Example/App/C#M().(y)",
		},
		{
			name: "a method's own type parameter",
			src:  "public class C { void M<U>(U u) { } }",
			want: prefix + " Com/Example/App/C#M().(U)",
		},
		{
			name: "an enum member",
			src:  "public enum Color { Red }",
			want: prefix + " Com/Example/App/Color#Red.",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ff := parse(t, "src/App/Main.cs", "namespace Com.Example.App;\n\n"+tc.src+"\n")
			assertHasDefinition(t, ff, tc.want)
		})
	}
}

// TestNoDefinitionIsEmittedAsAModule guards the one kind that would be invisible
// to the link pass. `imports` joins on `symbol_kind = 'package'`, so a stanza
// that emitted `module` — the natural word for what a C# file compiles into —
// would derive no import edges at all and fail silently. §5's own capture list
// still names `module`; this is the reason not to follow it.
func TestNoDefinitionIsEmittedAsAModule(t *testing.T) {
	ff := parse(t, "src/Greeter/Greeter.cs", `
using Com.Example.Util;

namespace Com.Example.Greeting;

public class Greeter { }
`)
	for _, o := range ff.Occurrences {
		assert.NotEqual(t, facts.KindModule, o.SymbolKind,
			"%s was emitted as a module and link's imports derivation joins on package", o.Descriptor)
	}
	assert.Equal(t, facts.KindPackage, namespaceDefinition(t, ff).SymbolKind)
}

// --------------------------------------------------------------- namespaces --

// TestNamespaceComesFromTheDeclarationAndNotThePath is the namespace decision for
// the file-scoped form, which is the one that behaves exactly as Java's clause
// does. The fixture is laid out the way a .NET solution is, under `src/<Project>/`,
// and no `using` anywhere writes that prefix — so the declaration is the only
// reading under which the two sides of a using directive can agree.
func TestNamespaceComesFromTheDeclarationAndNotThePath(t *testing.T) {
	tests := []struct {
		name string
		path string
		src  string
		want string
	}{
		{
			name: "the solution layout is not part of the namespace",
			path: "src/Greeter/Greeter.cs",
			src:  "namespace Com.Example.Greeting;\npublic class Greeter { }\n",
			want: prefix + " Com/Example/Greeting/Greeter#",
		},
		{
			name: "a file in the root still gets its declared namespace",
			path: "Greeter.cs",
			src:  "namespace Com.Example.Greeting;\npublic class Greeter { }\n",
			want: prefix + " Com/Example/Greeting/Greeter#",
		},
		{
			name: "a one-segment namespace",
			path: "src/App/Program.cs",
			src:  "namespace App;\npublic class Program { }\n",
			want: prefix + " App/Program#",
		},
		{
			// C#'s global namespace is a real namespace and not a missing one,
			// exactly as Java's default package is.
			name: "no declaration at all is the global namespace",
			path: "src/App/Loose.cs",
			src:  "public class Loose { }\n",
			want: prefix + " Loose#",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertHasDefinition(t, parse(t, tc.path, tc.src), tc.want)
		})
	}
}

// TestBlockNamespacesNestAndCoexist is the half Java has no counterpart for.
//
// A block namespace is an ancestor of what it holds, so it is contributed by the
// same walk that produces a nested type's `#` levels — which is why nesting and
// multiplicity both work with no rule of their own. Two blocks side by side give
// their types two different prefixes, because they are in two different
// namespaces and a file-level `ns` could only have named one of them.
func TestBlockNamespacesNestAndCoexist(t *testing.T) {
	ff := parse(t, "src/App/Many.cs", `
namespace A.B
{
    public class Outer { }

    namespace Deep
    {
        public class Far { }
    }
}

namespace Other
{
    public class Away { }
}
`)
	for _, want := range []string{
		prefix + " A/B/",
		prefix + " A/B/Deep/",
		prefix + " Other/",
		prefix + " A/B/Outer#",
		prefix + " A/B/Deep/Far#",
		prefix + " Other/Away#",
	} {
		assertHasDefinition(t, ff, want)
	}
}

// TestABlockNamespaceIsAScope records the one scope kind this query has that
// Java's does not. A Java package is declared once at the top of a file and is
// never a lexical region; a C# block namespace has braces and nests, so it is.
func TestABlockNamespaceIsAScope(t *testing.T) {
	ff := parse(t, "src/App/Many.cs", "namespace A { public class C { } }\n")
	kinds := map[string]int{}
	for _, s := range ff.Scopes {
		kinds[s.Kind]++
	}
	assert.Equal(t, 1, kinds[facts.ScopePackage], "a block namespace is a scope")
	assert.Equal(t, 1, kinds[facts.ScopeFile])
	assert.Equal(t, 1, kinds[facts.ScopeType])
}

// TestAFileScopedNamespaceIsNotAScope is the other half of the same decision: it
// delimits nothing, because everything after it is its sibling in the CST.
func TestAFileScopedNamespaceIsNotAScope(t *testing.T) {
	ff := parse(t, "src/App/One.cs", "namespace A;\npublic class C { }\n")
	for _, s := range ff.Scopes {
		assert.NotEqual(t, facts.ScopePackage, s.Kind,
			"a file-scoped namespace delimits nothing and must not be a scope")
	}
	assertHasDefinition(t, ff, prefix+" A/")
}

// TestNamespaceDefinitionMatchesAUsingDirective is what a `imports` edge between
// two files is made of: the descriptor the declaring file writes for its own
// namespace, and the descriptor the using file writes for the namespace it
// names, are the same string.
func TestNamespaceDefinitionMatchesAUsingDirective(t *testing.T) {
	decl := parse(t, "src/Greeter/Greeter.cs", "namespace Com.Example.Greeting;\npublic class Greeter { }\n")
	user := parse(t, "src/App/Program.cs", "using Com.Example.Greeting;\n\nnamespace Com.Example.App;\npublic class Program { }\n")

	def := namespaceDefinition(t, decl)
	ref := namespaceReference(t, user)

	assert.Equal(t, prefix+" Com/Example/Greeting/", def.Descriptor.String())
	assert.Equal(t, def.Descriptor.String(), ref.Descriptor.String(),
		"the two sides of a using directive have to derive the same descriptor or there is no imports edge")
	assert.Equal(t, facts.KindPackage, def.SymbolKind)
	assert.Equal(t, facts.KindPackage, ref.SymbolKind)
}

// TestNamespaceOccurrencePointsAtTheDeclaration is a small thing C# gets that
// TypeScript, Python and Rust cannot: the namespace is written down, so the
// occurrence covers a real identifier instead of a zero-width point.
func TestNamespaceOccurrencePointsAtTheDeclaration(t *testing.T) {
	src := "namespace Com.Example.Greeting;\npublic class Greeter { }\n"
	occ := namespaceDefinition(t, parse(t, "src/Greeter/Greeter.cs", src))
	assert.Equal(t, "Com.Example.Greeting", src[occ.RangeStart:occ.RangeEnd])
}

func TestGlobalNamespaceOccurrenceIsZeroWidth(t *testing.T) {
	occ := namespaceDefinition(t, parse(t, "src/App/Loose.cs", "public class Loose { }\n"))
	assert.Equal(t, 0, occ.RangeStart)
	assert.Equal(t, 0, occ.RangeEnd)
	assert.Equal(t, prefix, occ.Descriptor.String())
}

// ------------------------------------------------------------------ imports --

func TestUsingDirectiveCoordinates(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			// A namespace of this artifact keeps this coordinate, which is what
			// lets the reference match the other file's declaration.
			name: "a namespace of this artifact",
			src:  "using Com.Example.Greeting;",
			want: []string{prefix + " Com/Example/Greeting/"},
		},
		{
			// The platform's, which owns no file this index reads.
			name: "a platform namespace is foreign",
			src:  "using System.Text;",
			want: []string{platform + " Text/"},
		},
		{
			// A third-party package has no assembly identity in its namespace,
			// so it lands in this artifact's namespace with nothing to match:
			// an unresolved reference rather than a wrong edge.
			name: "a third-party namespace is this artifact's, and matches nothing",
			src:  "using Newtonsoft.Json;",
			want: []string{prefix + " Newtonsoft/Json/"},
		},
		{
			name: "an alias names the namespace and the type apart",
			src:  "using B = System.Text.StringBuilder;",
			want: []string{platform + " Text/", platform + " Text/StringBuilder#"},
		},
		{
			name: "using static names the type whose statics come in",
			src:  "using static System.Console;",
			want: []string{platform, platform + " Console#"},
		},
		{
			// `global using` affects every file in the assembly and the files it
			// affects do not write it down. It is read as the plain using it is
			// written like: right for this file, silent for the rest.
			name: "a global using reads as the plain using it looks like",
			src:  "global using System.Linq;",
			want: []string{platform + " Linq/"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ff := parse(t, "src/App/Program.cs", tc.src+"\n\nnamespace Com.Example.App;\npublic class P { }\n")
			for _, want := range tc.want {
				assert.Contains(t, referenceDescriptors(ff), want)
			}
		})
	}
}

// TestAnAliasBindsASimpleName is the one using form that brings a name into
// scope this file writes down, so it is the one the mapper can bind.
func TestAnAliasBindsASimpleName(t *testing.T) {
	ff := parse(t, "src/App/Program.cs", `
using B = System.Text.StringBuilder;

namespace Com.Example.App;

public class P
{
    public void M()
    {
        B b = new B();
        b.Append("x");
    }
}
`)
	assert.Contains(t, referenceDescriptors(ff), platform+" Text/StringBuilder#")
	assert.Contains(t, referenceDescriptors(ff), platform+" Text/StringBuilder#Append().")
}

// TestUsingDirectivesClaimTheirIdentifiers is the dedupe that keeps a using from
// being described twice: the `.` reference patterns match inside one as readily
// as anywhere else.
func TestUsingDirectivesClaimTheirIdentifiers(t *testing.T) {
	ff := parse(t, "src/App/Program.cs", "using System.Text;\n\nnamespace Com.Example.App;\npublic class P { }\n")

	count := 0
	for _, o := range ff.Occurrences {
		if o.RangeStart >= 6 && o.RangeEnd <= 17 {
			count++
		}
	}
	assert.Equal(t, 1, count, "one occurrence over the using's path, not one per segment: %v", referenceDescriptors(ff))
}

// -------------------------------------------------------------------- types --

// TestNestedTypesAreADescriptorLevel is the rule C# shares with Java and with no
// other language in this graph: each named container contributes exactly one
// component, so a type inside a type is another `#` level and needs no rule of
// its own.
func TestNestedTypesAreADescriptorLevel(t *testing.T) {
	ff := parse(t, "src/Greeter/Registry.cs", `
namespace Com.Example.Greeting;

public sealed class Registry
{
    public sealed class Entry
    {
        public string Label() { return ""; }
    }
}
`)
	assertHasDefinition(t, ff, prefix+" Com/Example/Greeting/Registry#")
	assertHasDefinition(t, ff, prefix+" Com/Example/Greeting/Registry#Entry#")
	assertHasDefinition(t, ff, prefix+" Com/Example/Greeting/Registry#Entry#Label().")
}

// TestALocalFunctionCarriesItsMethodsComponent falls out of the same walk: a
// method is a named container, so what is declared inside it is named through it.
func TestALocalFunctionCarriesItsMethodsComponent(t *testing.T) {
	ff := parse(t, "src/App/Program.cs", `
namespace Com.Example.App;

public class P
{
    public void Run()
    {
        void inner() { }
        inner();
    }
}
`)
	assertHasDefinition(t, ff, prefix+" Com/Example/App/P#Run().inner().")
	assertResolvesLocally(t, ff, "inner", prefix+" Com/Example/App/P#Run().inner().")
}

// TestPartialClassesRenderOneDescriptor is C#'s own contribution to this graph,
// and the descriptor answer is that the collision is the point rather than a
// problem.
//
// Two files declaring `partial class Greeter` declare one type, and the
// descriptor — a coordinate and a structural path — says nothing about which file
// a symbol was written in, so both render the same string. That is what makes
// link's `implements` see the union of the members: its method set is gathered by
// descriptor prefix over the whole occurrence table with no file predicate, so a
// class that implements half an interface in each file satisfies it.
func TestPartialClassesRenderOneDescriptor(t *testing.T) {
	one := parse(t, "src/Greeter/Greeter.Speak.cs", `
namespace Com.Example.Greeting;

public partial class Greeter
{
    public string Greet() { return ""; }
}
`)
	two := parse(t, "src/Greeter/Greeter.Name.cs", `
namespace Com.Example.Greeting;

public partial class Greeter
{
    public string Name() { return ""; }
}
`)
	assertHasDefinition(t, one, prefix+" Com/Example/Greeting/Greeter#")
	assertHasDefinition(t, two, prefix+" Com/Example/Greeting/Greeter#")
	assertHasDefinition(t, one, prefix+" Com/Example/Greeting/Greeter#Greet().")
	assertHasDefinition(t, two, prefix+" Com/Example/Greeting/Greeter#Name().")
}

// TestAnAnonymousTypeHasNoDescriptorOfItsOwn is the one shape with no answer:
// `new { A = 1 }` declares a type the source never names, so there is no
// identifier to build a component from.
func TestAnAnonymousTypeHasNoDescriptorOfItsOwn(t *testing.T) {
	ff := parse(t, "src/App/Program.cs", `
namespace Com.Example.App;

public class P
{
    public void Run()
    {
        var q = new { A = 1 };
    }
}
`)
	assertHasDefinition(t, ff, prefix+" Com/Example/App/P#Run().q.")
	for _, o := range ff.Occurrences {
		assert.NotContains(t, o.Descriptor.Suffix, "$", "no positional name for an anonymous type")
	}
}

// ------------------------------------------------------------------ implements

// TestInterfaceMembersLineUpWithAnImplementation is what makes `implements`
// derivable with no change at all: link's rule is method-set containment over
// descriptor suffixes, and a C# type's members are written inside the type, so
// the implementing class's suffixes contain the interface's.
//
// This is the sixth language the unmodified rule holds for.
func TestInterfaceMembersLineUpWithAnImplementation(t *testing.T) {
	iface := parse(t, "src/Greeter/ISpeaker.cs", `
namespace Com.Example.Greeting;

public interface ISpeaker
{
    string Greet();
}
`)
	impl := parse(t, "src/Greeter/Greeter.cs", `
namespace Com.Example.Greeting;

public class Greeter : ISpeaker
{
    public string Greet() { return ""; }
}
`)
	assert.Equal(t, facts.KindInterface, definitionNamed(t, iface, "ISpeaker", facts.KindInterface).SymbolKind)
	assert.Equal(t, facts.KindType, definitionNamed(t, impl, "Greeter", facts.KindType).SymbolKind)

	// The suffix after the type's own descriptor is what link compares, and the
	// two are the same string.
	assertHasDefinition(t, iface, prefix+" Com/Example/Greeting/ISpeaker#Greet().")
	assertHasDefinition(t, impl, prefix+" Com/Example/Greeting/Greeter#Greet().")
}

// TestAnExplicitImplementationIsAnOrdinaryMember is the descriptor decision C#
// forces and no earlier language here does.
//
// `string ISpeaker.Greet()` drops its qualifier, for two reasons. Keeping it
// would put `ISpeaker.Greet().` in the class's method set, which does not contain
// the interface's `Greet().`, so a class implementing an interface explicitly
// would implement nothing. And the qualified descriptor is one no reference site
// could reconstruct: an explicit implementation is only ever called through the
// interface, so the call writes the interface's descriptor and never this one.
func TestAnExplicitImplementationIsAnOrdinaryMember(t *testing.T) {
	ff := parse(t, "src/Greeter/Greeter.cs", `
namespace Com.Example.Greeting;

public class Greeter : ISpeaker
{
    string ISpeaker.Greet() { return ""; }
}
`)
	assertHasDefinition(t, ff, prefix+" Com/Example/Greeting/Greeter#Greet().")
	for _, d := range definitionDescriptors(ff) {
		assert.NotContains(t, d, "ISpeaker.Greet",
			"an explicit implementation must not carry its qualifier into the descriptor")
	}
	// The interface it names stays navigable: the qualifier is recorded as what
	// it structurally is, a reference to the interface's type.
	assert.Contains(t, referenceDescriptors(ff), prefix+" Com/Example/Greeting/ISpeaker#")
}

// TestABaseListEntryIsATypeReference is the explicit declaration C# writes and Go
// does not, recorded with no edge kind the schema lacks.
func TestABaseListEntryIsATypeReference(t *testing.T) {
	ff := parse(t, "src/Greeter/Greeter.cs", `
namespace Com.Example.Greeting;

public class Greeter : Base, ISpeaker { }
`)
	descriptors := referenceDescriptors(ff)
	assert.Contains(t, descriptors, prefix+" Com/Example/Greeting/Base#")
	assert.Contains(t, descriptors, prefix+" Com/Example/Greeting/ISpeaker#")
}

// ------------------------------------------------------------------ references

func TestParseReferenceDescriptors(t *testing.T) {
	ff := parse(t, "src/App/Program.cs", `
using Com.Example.Greeting;

namespace Com.Example.App;

public static class Program
{
    public static void Main()
    {
        Greeter g = new Greeter("world");
        string message = g.Greet();
        System.Console.WriteLine(message);
    }
}
`)
	descriptors := referenceDescriptors(ff)
	for _, want := range []string{
		prefix + " Com/Example/Greeting/",                 // the using directive
		prefix + " Com/Example/Greeting/Greeter#",         // the declared type, and the `new`
		prefix + " Com/Example/Greeting/Greeter#Greet().", // the call, through g's declared type
		platform,               // `System`
		platform + " Console#", // `System.Console`
		platform + " Console#WriteLine().",
	} {
		assert.Contains(t, descriptors, want)
	}
}

func TestParseResolvesAReceiverThroughItsDeclaredType(t *testing.T) {
	tests := []struct {
		name string
		src  string
		call string
		want string
	}{
		{
			name: "a local's declared type",
			src:  "public class P { void M() { Greeter g = null; g.Greet(); } }",
			call: "Greet",
			want: prefix + " Com/Example/App/Greeter#Greet().",
		},
		{
			name: "a parameter's declared type",
			src:  "public class P { void M(Greeter g) { g.Greet(); } }",
			call: "Greet",
			want: prefix + " Com/Example/App/Greeter#Greet().",
		},
		{
			name: "a field's declared type, used before it is declared",
			src:  "public class P { void M() { _g.Greet(); } private Greeter _g; }",
			call: "Greet",
			want: prefix + " Com/Example/App/Greeter#Greet().",
		},
		{
			name: "a property's declared type",
			src:  "public class P { Greeter G { get; set; } void M() { G.Greet(); } }",
			call: "Greet",
			want: prefix + " Com/Example/App/Greeter#Greet().",
		},
		{
			name: "a generic application is named by its own name",
			src:  "public class P { void M() { Box<int> b = null; b.Open(); } }",
			call: "Open",
			want: prefix + " Com/Example/App/Box#Open().",
		},
		{
			name: "an array is named by its element type",
			src:  "public class P { void M() { Greeter[] gs = null; gs[0].Greet(); } }",
			call: "Greet",
			want: prefix + " Com/Example/App/" + coord.Unknown + "#Greet().",
		},
		{
			name: "a nullable is transparent",
			src:  "public class P { void M() { Greeter? g = null; g.Greet(); } }",
			call: "Greet",
			want: prefix + " Com/Example/App/Greeter#Greet().",
		},
		{
			// The receiver's type is written right there, which makes it the one
			// expression form worth reading back.
			name: "a new expression states its own type",
			src:  "public class P { void M() { new Greeter().Greet(); } }",
			call: "Greet",
			want: prefix + " Com/Example/App/Greeter#Greet().",
		},
		{
			// `var` names no type: the type is in the initialiser's *type*, which
			// is a type checker's answer and not a syntactic one.
			name: "an inferred local's members land on the unknown type",
			src:  "public class P { void M() { var g = Make(); g.Greet(); } Greeter Make() { return null; } }",
			call: "Greet",
			want: prefix + " Com/Example/App/" + coord.Unknown + "#Greet().",
		},
		{
			name: "this names the enclosing type",
			src:  "public class P { void M() { this.Other(); } void Other() { } }",
			call: "Other",
			want: prefix + " Com/Example/App/P#Other().",
		},
		{
			name: "base names the first entry of the base list",
			src:  "public class P : Root { void M() { base.Ping(); } }",
			call: "Ping",
			want: prefix + " Com/Example/App/Root#Ping().",
		},
		{
			// A type with no base list derives from System.Object, which is the
			// platform's and is named as such rather than left unknown.
			name: "base with no base list is System.Object",
			src:  "public class P { void M() { base.ToString(); } }",
			call: "ToString",
			want: platform + " Object#ToString().",
		},
		{
			name: "a bare call is a member of the enclosing type",
			src:  "public class P { void M() { Other(); } void Other() { } }",
			call: "Other",
			want: prefix + " Com/Example/App/P#Other().",
		},
		{
			// The type name is exact, so a static call is too.
			name: "a static call on a type declared here",
			src:  "public class P { void M() { Helper.Go(); } }",
			call: "Go",
			want: prefix + " Com/Example/App/Helper#Go().",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ff := parse(t, "src/App/Program.cs", "namespace Com.Example.App;\n\n"+tc.src+"\n")
			assert.Equal(t, tc.want, referenceNamed(t, ff, tc.call).Descriptor.String())
		})
	}
}

// TestParseResolvesAQualifiedPlatformName is the reading a foreign-rooted path
// gets: everything but the last segment is the namespace. A coordinate this index
// does not own can never match a definition in it, so the legible reading costs
// nothing — and the alternative would render `System/Collections#Generic#List#`.
func TestParseResolvesAQualifiedPlatformName(t *testing.T) {
	ff := parse(t, "src/App/Program.cs", `
namespace Com.Example.App;

public class P
{
    void M()
    {
        System.Collections.Generic.List<string> l = null;
    }
}
`)
	descriptors := referenceDescriptors(ff)
	assert.Contains(t, descriptors, platform+" Collections/")
	assert.Contains(t, descriptors, platform+" Collections/Generic/")
	assert.Contains(t, descriptors, platform+" Collections/Generic/List#")
}

// TestASimpleTypeNameResolvesThroughTheOneProjectUsing is C#'s namespace
// ambiguity, stated as the rule that resolves it.
//
// A `using` is on-demand: it brings in every name of a namespace and writes none
// of them down, so a simple type name this file does not declare could have come
// from any of them. With exactly one naming a namespace of this artifact, there
// is exactly one candidate; with more than one there is no evidence, and the file's
// own namespace is used instead, which matches nothing rather than something else.
func TestASimpleTypeNameResolvesThroughTheOneProjectUsing(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "no project using: the file's own namespace",
			src:  "using System;\npublic class P { void M(Greeter g) { } }",
			want: prefix + " Com/Example/App/Greeter#",
		},
		{
			name: "one project using: that namespace",
			src:  "using System;\nusing Com.Example.Greeting;\npublic class P { void M(Greeter g) { } }",
			want: prefix + " Com/Example/Greeting/Greeter#",
		},
		{
			name: "two project usings: no evidence, so the file's own namespace",
			src:  "using Com.Example.Greeting;\nusing Com.Example.Util;\npublic class P { void M(Greeter g) { } }",
			want: prefix + " Com/Example/App/Greeter#",
		},
		{
			// A type this file declares wins over every using, which is the
			// compiler's order too.
			name: "a type declared here beats the using",
			src:  "using Com.Example.Greeting;\npublic class Greeter { }\npublic class P { void M(Greeter g) { } }",
			want: prefix + " Com/Example/App/Greeter#",
		},
		{
			// So does an alias, which names one type outright.
			name: "an alias beats the using",
			src:  "using Com.Example.Greeting;\nusing Greeter = Com.Example.Other.Greeter;\npublic class P { void M(Greeter g) { } }",
			want: prefix + " Com/Example/Other/Greeter#",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ff := parse(t, "src/App/Program.cs", insertNamespace(tc.src))
			assert.Contains(t, referenceDescriptors(ff), tc.want)
		})
	}
}

// TestOverloadsRenderOneDescriptor states the limit rather than hiding it, and it
// is Java's verbatim: the descriptor's callable component carries the name and
// not the signature, so two overloads collide and a reference to either resolves
// to both. Encoding the signature is what SCIP's disambiguating suffix is for and
// is deliberately not done: the definition side could number its overloads from
// one CST and the reference side could not.
func TestOverloadsRenderOneDescriptor(t *testing.T) {
	ff := parse(t, "src/App/Program.cs", `
namespace Com.Example.App;

public class P
{
    public string Greet(string name) { return name; }
    public string Greet(int times) { return ""; }
}
`)
	count := 0
	for _, d := range definitionDescriptors(ff) {
		if d == prefix+" Com/Example/App/P#Greet()." {
			count++
		}
	}
	assert.Equal(t, 2, count, "two overloads, one descriptor")
}

// ------------------------------------------------------------------- scopes --

func TestScopesAreCSharpsOwn(t *testing.T) {
	ff := parse(t, "src/App/Program.cs", `
namespace Com.Example.App
{
    public class P
    {
        public int Q { get { return 1; } }

        public void M(int[] xs)
        {
            foreach (var x in xs)
            {
            }
            try { } catch (System.Exception e) { }
            System.Func<int, int> f = (y) => y;
        }
    }
}
`)
	kinds := map[string]int{}
	for _, s := range ff.Scopes {
		kinds[s.Kind]++
	}
	assert.Equal(t, 1, kinds[facts.ScopeFile])
	assert.Equal(t, 1, kinds[facts.ScopePackage])
	assert.Equal(t, 1, kinds[facts.ScopeType])
	assert.Positive(t, kinds[facts.ScopeFunction], "a method, an accessor and a lambda are all function scopes")
	assert.Positive(t, kinds[facts.ScopeBlock])
}

func TestNamespaceDefinitionIsInsideItsOwnScope(t *testing.T) {
	ff := parse(t, "src/App/Program.cs", "namespace A { public class C { } }\n")
	occ := namespaceDefinition(t, ff)
	require.NotEqual(t, facts.NoID, occ.Scope)
	for _, s := range ff.Scopes {
		if s.ID == occ.Scope {
			assert.Equal(t, facts.ScopePackage, s.Kind)
			return
		}
	}
	t.Fatal("the namespace definition is in no scope")
}

// ------------------------------------------------------------------- edges ---

func TestParseResolvesSameFileReferences(t *testing.T) {
	ff := parse(t, "src/App/Program.cs", `
namespace Com.Example.App;

public class P
{
    public string Greet() { return ""; }

    public string Run()
    {
        return this.Greet();
    }
}
`)
	assertResolvesLocally(t, ff, "Greet", prefix+" Com/Example/App/P#Greet().")
}

// TestForwardFieldReferencesResolve is a rule C# shares with Java and no earlier
// language has: a method may use a member declared below it, so a definition
// whose scope is a type is exempt from the "declared no later than here" rule.
func TestForwardFieldReferencesResolve(t *testing.T) {
	ff := parse(t, "src/App/Program.cs", `
namespace Com.Example.App;

public class P
{
    public void M() { _g.Greet(); }

    private Greeter _g;
}
`)
	assert.Equal(t, prefix+" Com/Example/App/Greeter#Greet().",
		referenceNamed(t, ff, "Greet").Descriptor.String())
}

func TestParseEmitsOnlyExtractableEdgeKinds(t *testing.T) {
	ff := parse(t, "src/Greeter/Greeter.cs", `
namespace Com.Example.Greeting;

public class Greeter
{
    public string Greet() { return ""; }
    public string Again() { return this.Greet(); }
}
`)
	allowed := map[facts.EdgeKind]bool{
		facts.EdgeDefines: true, facts.EdgeContains: true, facts.EdgeReferencesLocal: true,
	}
	for _, e := range ff.Edges {
		assert.True(t, allowed[e.Kind], "extractors may not emit %s: it is the link pass's", e.Kind)
	}
}

// -------------------------------------------------------------------- files --

func TestParseFile(t *testing.T) {
	c := testCoord(t)
	path := filepath.Join(c.Root, "src", "App", "Program.cs")
	ff := cs.New().Parse(path, []byte("namespace A;\npublic class P { }\n"), c)

	require.Empty(t, ff.ParseError)
	assert.Equal(t, path, ff.File.Path)
	assert.Equal(t, cs.Lang, ff.File.Lang)
	assert.Equal(t, c, ff.File.Coord)
}

// TestParseTheFixture is the corpus the feature suite indexes, checked here so
// that a change to either side is caught by a unit test rather than by a
// container.
func TestParseTheFixture(t *testing.T) {
	c := testCoord(t)

	greeterPath := filepath.Join(c.Root, "src", "Greeter", "Greeter.cs")
	greeterSrc, err := os.ReadFile(greeterPath) //nolint:gosec // the fixture.
	require.NoError(t, err)
	greeter := cs.New().Parse(greeterPath, greeterSrc, c)
	require.Empty(t, greeter.ParseError)

	programPath := filepath.Join(c.Root, "src", "App", "Program.cs")
	programSrc, err := os.ReadFile(programPath) //nolint:gosec // the fixture.
	require.NoError(t, err)
	program := cs.New().Parse(programPath, programSrc, c)
	require.Empty(t, program.ParseError)

	assertHasDefinition(t, greeter, prefix+" Com/Example/Greeting/")
	assertHasDefinition(t, greeter, prefix+" Com/Example/Greeting/Greeter#")
	assertHasDefinition(t, greeter, prefix+" Com/Example/Greeting/Greeter#Greet().")
	assertHasDefinition(t, program, prefix+" Com/Example/App/Program#Main().")

	// The cross-file claim, made twice: the call in Program.cs and the method in
	// Greeter.cs render the same string, and so do the using directive and the
	// namespace declaration. Neither file's extraction ever saw the other.
	assert.Equal(t, prefix+" Com/Example/Greeting/Greeter#Greet().",
		referenceNamed(t, program, "Greet").Descriptor.String())
	assert.Equal(t, prefix+" Com/Example/Greeting/",
		namespaceReference(t, program).Descriptor.String())
	assert.Equal(t, prefix+" Com/Example/Greeting/",
		namespaceDefinition(t, greeter).Descriptor.String())
}

func TestParseBrokenSourceStillReturns(t *testing.T) {
	c := testCoord(t)
	ff := cs.New().Parse(filepath.Join(c.Root, "src", "App", "Broken.cs"), []byte("public class {{{"), c)
	assert.Equal(t, cs.Lang, ff.File.Lang)
}

// TestParseEmptyFile records what an empty compilation unit is: a member of the
// global namespace and nothing else.
func TestParseEmptyFile(t *testing.T) {
	ff := parse(t, "src/App/Empty.cs", "")
	assert.Equal(t, prefix, namespaceDefinition(t, ff).Descriptor.String())
	for _, o := range ff.Occurrences {
		assert.Equal(t, facts.KindPackage, o.SymbolKind)
	}
}

// --- helpers ---------------------------------------------------------------

// insertNamespace puts the file's own namespace declaration after the directives,
// which is where C# requires it.
func insertNamespace(src string) string {
	lines := []string{}
	rest := []string{}
	for _, line := range splitLines(src) {
		if len(rest) == 0 && len(line) >= 5 && line[:5] == "using" {
			lines = append(lines, line)
			continue
		}
		rest = append(rest, line)
	}
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	out += "\nnamespace Com.Example.App;\n\n"
	for _, l := range rest {
		out += l + "\n"
	}
	return out
}

func splitLines(s string) []string {
	out := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func definitionsByName(ff facts.FileFacts) map[string]facts.Occurrence {
	out := map[string]facts.Occurrence{}
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition {
			out[o.Name] = o
		}
	}
	return out
}

func definitionDescriptors(ff facts.FileFacts) []string {
	out := []string{}
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition {
			out = append(out, o.Descriptor.String())
		}
	}
	sort.Strings(out)
	return out
}

func referenceDescriptors(ff facts.FileFacts) []string {
	out := []string{}
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference {
			out = append(out, o.Descriptor.String())
		}
	}
	sort.Strings(out)
	return out
}

func assertHasDefinition(t *testing.T, ff facts.FileFacts, descriptor string) {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition && o.Descriptor.String() == descriptor {
			return
		}
	}
	t.Fatalf("no definition with descriptor %q in %v", descriptor, definitionDescriptors(ff))
}

// definitionNamed returns the definition named name whose kind is kind, which is
// how a constructor is told from the type it is named after.
func definitionNamed(t *testing.T, ff facts.FileFacts, name, kind string) facts.Occurrence {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition && o.Name == name && o.SymbolKind == kind {
			return o
		}
	}
	t.Fatalf("no %s definition named %q", kind, name)
	return facts.Occurrence{}
}

func referenceNamed(t *testing.T, ff facts.FileFacts, name string) facts.Occurrence {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference && o.Name == name {
			return o
		}
	}
	t.Fatalf("no reference named %q", name)
	return facts.Occurrence{}
}

func namespaceDefinition(t *testing.T, ff facts.FileFacts) facts.Occurrence {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition && o.SymbolKind == facts.KindPackage {
			return o
		}
	}
	t.Fatal("no namespace definition")
	return facts.Occurrence{}
}

func namespaceReference(t *testing.T, ff facts.FileFacts) facts.Occurrence {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference && o.SymbolKind == facts.KindPackage {
			return o
		}
	}
	t.Fatal("no namespace reference")
	return facts.Occurrence{}
}

// assertResolvesLocally checks that the reference named name carries descriptor
// and has a references_local edge to the definition with the same descriptor.
func assertResolvesLocally(t *testing.T, ff facts.FileFacts, name, descriptor string) {
	t.Helper()
	ref := referenceNamed(t, ff, name)
	assert.Equal(t, descriptor, ref.Descriptor.String())

	var def facts.LocalID
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition && o.Descriptor.String() == descriptor {
			def = o.ID
		}
	}
	require.NotEqual(t, facts.NoID, def, "no definition with descriptor %q", descriptor)

	for _, e := range ff.Edges {
		if e.Kind == facts.EdgeReferencesLocal && e.Source.ID == ref.ID && e.Target.ID == def {
			return
		}
	}
	t.Fatalf("no references_local edge from the reference to %q", descriptor)
}
