package java_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/extract/java"
	"github.com/gaarutyunov/codiq/facts"
)

// testCoord is the coordinate the batch would hand the parser for a file in the
// artifact com.example:codiq-greeter, rooted at the fixture directory.
func testCoord(t *testing.T) coord.Coord {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "greeter"))
	require.NoError(t, err)
	coords, err := coord.Resolve(root, "greeter")
	require.NoError(t, err)
	c := coords.For("X" + java.Ext)
	require.Equal(t, coord.JavaScheme, c.Scheme, "the fixture must resolve through the Maven resolver")
	return c
}

// parse parses src as the file at name, which is interpreted relative to the
// artifact root. Unlike Rust's, the path is *not* what names the file's
// namespace — the `package` clause is — so it is incidental here, and one test
// below is about exactly that.
func parse(t *testing.T, name, src string) facts.FileFacts {
	t.Helper()
	c := testCoord(t)
	ff := java.New().Parse(filepath.Join(c.Root, filepath.FromSlash(name)), []byte(src), c)
	require.Empty(t, ff.ParseError)
	return ff
}

const prefix = "scip-java maven com.example:codiq-greeter 1.0.0"

// jdk is the coordinate references to the Java platform carry: foreign, so
// nothing under it can ever match a definition this index owns.
const jdk = "scip-java maven java ."

// --------------------------------------------------------------- definitions --

func TestParseDefinitionDescriptors(t *testing.T) {
	ff := parse(t, "src/main/java/com/example/shapes/Shapes.java", `
package com.example.shapes;

public class Shapes {
    public static final int LEVEL = 1;
    private String label;

    public Shapes(String label) {
        this.label = label;
    }

    public String describe(int depth) {
        String local = this.label;
        return local;
    }

    public interface Speaker {
        String speak();
    }

    public enum Mood { HAPPY, SAD }

    public record Point(int x, int y) {}

    public static class Inner {
        void ping() {}
    }

    public <T> T identity(T value) {
        return value;
    }
}
`)

	// The whole set, so that a suffix rule which starts producing an *extra*
	// definition fails here too. Keyed by descriptor rather than by name
	// because `Shapes` names both the class and its constructor.
	want := []string{
		// The package the file declares, which is where Java differs from
		// TypeScript, Python and Rust: the name is written down rather than
		// derived from the path.
		prefix + " com/example/shapes/",
		prefix + " com/example/shapes/Shapes#",
		prefix + " com/example/shapes/Shapes#LEVEL.",
		prefix + " com/example/shapes/Shapes#label.",
		// A constructor is a member of its type named after it. It shares the
		// `().` shape of every other callable, so `implements` reads it as one
		// more entry in the class's method set — which is harmless, since an
		// interface never has one to match it.
		prefix + " com/example/shapes/Shapes#Shapes().",
		prefix + " com/example/shapes/Shapes#Shapes().(label)",
		prefix + " com/example/shapes/Shapes#describe().",
		prefix + " com/example/shapes/Shapes#describe().(depth)",
		prefix + " com/example/shapes/Shapes#describe().local.",
		// Nested types are a real level of the descriptor, not a flattening —
		// the answer no earlier language in this graph had to give.
		prefix + " com/example/shapes/Shapes#Speaker#",
		prefix + " com/example/shapes/Shapes#Speaker#speak().",
		prefix + " com/example/shapes/Shapes#Mood#",
		prefix + " com/example/shapes/Shapes#Mood#HAPPY.",
		prefix + " com/example/shapes/Shapes#Mood#SAD.",
		prefix + " com/example/shapes/Shapes#Point#",
		// A record's components are state and accessors, not arguments.
		prefix + " com/example/shapes/Shapes#Point#x.",
		prefix + " com/example/shapes/Shapes#Point#y.",
		prefix + " com/example/shapes/Shapes#Inner#",
		prefix + " com/example/shapes/Shapes#Inner#ping().",
		prefix + " com/example/shapes/Shapes#identity().",
		prefix + " com/example/shapes/Shapes#identity().(T)",
		prefix + " com/example/shapes/Shapes#identity().(value)",
	}
	sort.Strings(want)
	assert.Equal(t, want, definitionDescriptors(ff))
}

func TestParseDefinitionKinds(t *testing.T) {
	ff := parse(t, "src/main/java/com/example/kinds/Kinds.java", `
package com.example.kinds;

public class Kinds {
    static final int MAX = 3;
    private String attr;

    void method(int arg) {
        int local = arg;
    }

    interface Speaker { String speak(); }

    @interface Marker {}

    enum E { ONE }

    record R(int component) {}

    static class Nested {}
}
`)

	defs := definitionsByName(ff)
	tests := []struct {
		name string
		want string
	}{
		{"kinds", facts.KindPackage},
		{"Kinds", facts.KindType},
		{"MAX", facts.KindField},
		{"attr", facts.KindField},
		// Every Java callable is a member of a type: the language has no free
		// function, so `function` never appears in this stanza's output.
		{"method", facts.KindMethod},
		{"arg", facts.KindParameter},
		{"local", facts.KindVariable},
		// `interface` is a keyword and means exactly one thing, which is why
		// Java needs none of the base inspection the Python stanza does.
		{"Speaker", facts.KindInterface},
		{"speak", facts.KindMethod},
		// An annotation type is an interface in the JLS, and is one here.
		{"Marker", facts.KindInterface},
		{"E", facts.KindType},
		{"ONE", facts.KindField},
		{"R", facts.KindType},
		{"component", facts.KindField},
		{"Nested", facts.KindType},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			def, ok := defs[tc.name]
			require.True(t, ok, "no definition named %q", tc.name)
			assert.Equal(t, tc.want, def.SymbolKind)
		})
	}
}

// TestAConstructorIsAMethodNamedAfterItsType is kept apart from the kinds table
// above because it is the one definition whose name collides with another in the
// same file: a class and its constructor share an identifier, so a lookup by
// name cannot distinguish them and only the descriptor can.
func TestAConstructorIsAMethodNamedAfterItsType(t *testing.T) {
	ff := parse(t, "src/main/java/com/example/app/Main.java", `
package com.example.app;

class Main {
    Main(int seed) {}
}
`)
	var ctor, class facts.Occurrence
	for _, o := range ff.Occurrences {
		if o.Role != facts.RoleDefinition {
			continue
		}
		switch o.Descriptor.String() {
		case prefix + " com/example/app/Main#Main().":
			ctor = o
		case prefix + " com/example/app/Main#":
			class = o
		}
	}
	require.NotZero(t, ctor.ID, "no constructor definition")
	require.NotZero(t, class.ID, "no class definition")
	assert.Equal(t, facts.KindMethod, ctor.SymbolKind)
	assert.Equal(t, facts.KindType, class.SymbolKind)
	assert.Equal(t, class.Name, ctor.Name, "they share an identifier and not a descriptor")
}

// TestTheRarerDeclarationFormsAreCaptured is a tripwire on the query itself.
//
// A pattern naming a node type the grammar does not have is not a compile error
// in every tree-sitter binding — it can simply match nothing, forever, silently.
// These five forms are the ones no other test in this file reaches, so without
// this they would be five patterns nobody had ever proved fire.
func TestTheRarerDeclarationFormsAreCaptured(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			// An interface's field is `constant_declaration`, not
			// `field_declaration`: it is implicitly static final and the
			// grammar gives it its own node.
			name: "an interface constant",
			src:  "interface Q { int LEVEL = 1; }",
			want: prefix + " com/example/app/Q#LEVEL.",
		},
		{
			name: "a try-with-resources binding",
			src:  "class C { void f() throws Exception { try (AutoCloseable r = null) {} } }",
			want: prefix + " com/example/app/C#f().r.",
		},
		{
			// A record's compact constructor, which has no parameter list at
			// all and is still a callable member of its type.
			name: "a compact constructor",
			src:  "record R(int x) { R { } }",
			want: prefix + " com/example/app/R#R().",
		},
		{
			name: "an annotation type element",
			src:  "@interface Ann { String value(); }",
			want: prefix + " com/example/app/Ann#value.",
		},
		{
			name: "a varargs parameter",
			src:  "class C { void f(String... parts) {} }",
			want: prefix + " com/example/app/C#f().(parts)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ff := parse(t, "src/main/java/com/example/app/Main.java", "package com.example.app;\n\n"+tc.src+"\n")
			assertHasDefinition(t, ff, tc.want)
		})
	}
}

// TestNoDefinitionIsEmittedAsAModule guards the one kind that would be invisible
// to the link pass. `imports` joins on `symbol_kind = 'package'`, so a stanza
// that emitted `module` — the natural word for what a Java file belongs to —
// would derive no import edges at all and fail silently.
func TestNoDefinitionIsEmittedAsAModule(t *testing.T) {
	ff := parse(t, "src/main/java/com/example/greeter/Greeter.java", `
package com.example.greeter;

import com.example.util.Text;

public class Greeter {}
`)
	for _, o := range ff.Occurrences {
		assert.NotEqual(t, facts.KindModule, o.SymbolKind,
			"%s was emitted as a module and link's imports derivation joins on package", o.Descriptor)
	}
	assert.Equal(t, facts.KindPackage, moduleDefinition(t, ff).SymbolKind)
}

// --------------------------------------------------------------- namespaces --

// TestNamespaceComesFromTheDeclarationAndNotThePath is the namespace decision,
// stated as the thing that has to remain true of it.
//
// Every case here writes one package clause and puts the file somewhere the path
// would disagree with it. Maven's own layout is the disagreement that matters:
// `src/main/java/` is build configuration, and an `import` in another file writes
// `com.example.greeter` and nothing else — so the declaration is the only reading
// under which the two sides of an import can agree.
func TestNamespaceComesFromTheDeclarationAndNotThePath(t *testing.T) {
	tests := []struct {
		name string
		path string
		src  string
		want string
	}{
		{
			name: "Maven's layout is not part of the namespace",
			path: "src/main/java/com/example/greeter/Greeter.java",
			src:  "package com.example.greeter;\npublic class Greeter {}\n",
			want: prefix + " com/example/greeter/Greeter#",
		},
		{
			name: "a Gradle test source root is not either",
			path: "src/test/java/com/example/greeter/Greeter.java",
			src:  "package com.example.greeter;\npublic class Greeter {}\n",
			want: prefix + " com/example/greeter/Greeter#",
		},
		{
			// The declaration wins even when it contradicts the directory,
			// which javac permits and no build tool does.
			name: "a declaration that contradicts the path still wins",
			path: "src/main/java/wherever/Greeter.java",
			src:  "package com.example.greeter;\npublic class Greeter {}\n",
			want: prefix + " com/example/greeter/Greeter#",
		},
		{
			// The default package is a real namespace and not a missing one.
			name: "no clause is the default package",
			path: "Greeter.java",
			src:  "public class Greeter {}\n",
			want: prefix + " Greeter#",
		},
		{
			name: "a single-segment package",
			path: "src/main/java/greeter/Greeter.java",
			src:  "package greeter;\npublic class Greeter {}\n",
			want: prefix + " greeter/Greeter#",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertHasDefinition(t, parse(t, tc.path, tc.src), tc.want)
		})
	}
}

// TestPackageDefinitionMatchesAnImport is what an `imports` edge between two
// Java files is derived from, checked on both sides: the file that declares the
// package and the file that imports from it write the same descriptor, and
// neither has seen the other.
func TestPackageDefinitionMatchesAnImport(t *testing.T) {
	declaring := parse(t, "src/main/java/com/example/greeter/Greeter.java", `
package com.example.greeter;

public class Greeter {}
`)
	importing := parse(t, "src/main/java/com/example/app/Main.java", `
package com.example.app;

import com.example.greeter.Greeter;

public class Main {}
`)

	def := moduleDefinition(t, declaring)
	assert.Equal(t, prefix+" com/example/greeter/", def.Descriptor.String())
	assert.Equal(t, "greeter", def.Name)

	ref := moduleReference(t, importing)
	assert.Equal(t, def.Descriptor.String(), ref.Descriptor.String(),
		"the two sides of an import must derive one descriptor")
	assert.Equal(t, facts.RoleReference, ref.Role)
}

// TestPackageOccurrencePointsAtTheDeclaration is a small thing Java gets that
// the three languages before it could not: there is an identifier to point at,
// so the package occurrence has a real range rather than a zero-width point at
// byte 0.
func TestPackageOccurrencePointsAtTheDeclaration(t *testing.T) {
	src := "package com.example.greeter;\n\npublic class Greeter {}\n"
	ff := parse(t, "src/main/java/com/example/greeter/Greeter.java", src)

	def := moduleDefinition(t, ff)
	assert.Equal(t, "com.example.greeter", src[def.RangeStart:def.RangeEnd])
}

func TestDefaultPackageOccurrenceIsZeroWidth(t *testing.T) {
	ff := parse(t, "Greeter.java", "public class Greeter {}\n")

	def := moduleDefinition(t, ff)
	assert.Equal(t, 0, def.RangeStart)
	assert.Equal(t, 0, def.RangeEnd)
	assert.Equal(t, prefix, def.Descriptor.String())
}

// ------------------------------------------------------------------ imports --

func TestImportCoordinates(t *testing.T) {
	ff := parse(t, "src/main/java/com/example/app/Main.java", `
package com.example.app;

import com.example.greeter.Greeter;
import com.example.util.Text.Inner;
import java.util.List;
import java.util.*;
import static com.example.util.Text.upper;

public class Main {}
`)

	refs := referenceDescriptors(ff)
	// A package of this repository keeps this coordinate, so a reference
	// through it produces byte-identical descriptors to the definitions in that
	// package. A package of the JDK gets a foreign one and can never match
	// anything indexed here.
	assert.Contains(t, refs, prefix+" com/example/greeter/")
	assert.Contains(t, refs, prefix+" com/example/greeter/Greeter#")
	// A nested type import: the convention splits `…util.Text.Inner` into the
	// package `com.example.util` and the type chain `Text.Inner`, which the
	// grammar alone cannot do.
	assert.Contains(t, refs, prefix+" com/example/util/")
	assert.Contains(t, refs, prefix+" com/example/util/Text#Inner#")
	assert.Contains(t, refs, jdk+" util/")
	assert.Contains(t, refs, jdk+" util/List#")
	assert.Contains(t, refs, prefix+" com/example/util/Text#")
}

func TestImportsBindSimpleNames(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a single-type import binds the type",
			src:  "import com.example.util.Text;\n\nclass Main { void f() { Text t = null; } }",
			want: prefix + " com/example/util/Text#",
		},
		{
			name: "a nested-type import binds the innermost name",
			src:  "import com.example.util.Text.Inner;\n\nclass Main { void f() { Inner i = null; } }",
			want: prefix + " com/example/util/Text#Inner#",
		},
		{
			// A static import binds a member, and only the use site knows
			// whether it is called or read — so what is recorded is the owning
			// type and the terminator is chosen here.
			name: "a static import binds a member of its type",
			src:  "import static com.example.util.Text.upper;\n\nclass Main { void f() { upper(\"x\"); } }",
			want: prefix + " com/example/util/Text#upper().",
		},
		{
			// An on-demand import binds nothing nameable: the simple names it
			// makes available are the other file's to state, and guessing at
			// them would invent symbols this file never wrote.
			name: "an on-demand import binds nothing",
			src:  "import com.example.util.*;\n\nclass Main { void f() { Text t = null; } }",
			want: prefix + " com/example/app/Text#",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ff := parse(t, "src/main/java/com/example/app/Main.java", "package com.example.app;\n\n"+tc.src+"\n")
			assert.Contains(t, referenceDescriptors(ff), tc.want)
		})
	}
}

// TestImportsClaimTheirIdentifiers is the dedupe that keeps an import from being
// described twice: the `.` reference patterns match inside an import statement
// as readily as anywhere else, and the weaker match must not survive beside the
// one that knew it was an import.
func TestImportsClaimTheirIdentifiers(t *testing.T) {
	ff := parse(t, "src/main/java/com/example/app/Main.java", `
package com.example.app;

import com.example.greeter.Greeter;

public class Main {}
`)
	for _, o := range ff.Occurrences {
		assert.NotContains(t, o.Descriptor.Suffix, "com/example/app/com/",
			"an import's segments were re-read as members of this package")
	}
	assert.Len(t, referenceDescriptors(ff), 2, "one package and one type, not a segment each")
}

// ------------------------------------------------------------------ nesting --

// TestNestedTypesAreADescriptorLevel is Java's own contribution to this graph:
// no earlier language here has a type declared inside a type, and the answer is
// that each named container contributes exactly one component.
func TestNestedTypesAreADescriptorLevel(t *testing.T) {
	ff := parse(t, "src/main/java/com/example/registry/Registry.java", `
package com.example.registry;

public final class Registry {
    public static final class Entry {
        public static final class Deep {
            public String probe() { return ""; }
        }

        public String label() { return ""; }
    }

    public void load() {
        class Local {
            void deep() {}
        }
    }
}
`)

	// Two and three levels down, and — the case with no counterpart anywhere —
	// a class declared inside a method body, whose descriptor carries the
	// method's `().` component on the way past.
	assertHasDefinition(t, ff, prefix+" com/example/registry/Registry#Entry#")
	assertHasDefinition(t, ff, prefix+" com/example/registry/Registry#Entry#label().")
	assertHasDefinition(t, ff, prefix+" com/example/registry/Registry#Entry#Deep#")
	assertHasDefinition(t, ff, prefix+" com/example/registry/Registry#Entry#Deep#probe().")
	assertHasDefinition(t, ff, prefix+" com/example/registry/Registry#load().Local#")
	assertHasDefinition(t, ff, prefix+" com/example/registry/Registry#load().Local#deep().")
}

// TestAnAnonymousClassHasNoDescriptorOfItsOwn is the one nesting shape with no
// answer, stated so that it stays a decision rather than becoming a surprise.
//
// The source never names the type, so there is no component to build. Naming it
// positionally — SCIP writes `$anon1`, javac writes `Outer$1` — would be a
// descriptor no reference could ever reconstruct, since a caller cannot count the
// anonymous classes in another file. Its members therefore land under the
// enclosing container, which is also where an unqualified call inside one
// resolves to, and that second part is *correct* Java: `helper()` written inside
// an anonymous class really does reach the enclosing instance's method.
func TestAnAnonymousClassHasNoDescriptorOfItsOwn(t *testing.T) {
	ff := parse(t, "src/main/java/com/example/app/Main.java", `
package com.example.app;

public class Main {
    void helper() {}

    void f() {
        Runnable r = new Runnable() {
            public void run() { helper(); }
        };
    }
}
`)

	assertHasDefinition(t, ff, prefix+" com/example/app/Main#f().run().")
	assert.Equal(t, prefix+" com/example/app/Main#helper().",
		referenceNamed(t, ff, "helper").Descriptor.String())
}

// ---------------------------------------------------------------- interfaces --

// TestInterfaceMembersLineUpWithAnImplementation is what makes `implements`
// derivable without a new edge kind, and it is the assertion the milestone's
// interface question comes down to.
//
// Java declares `implements` explicitly, unlike Go's implicit satisfaction — and
// the derivation does not read the declaration. It compares method sets by
// descriptor suffix, so what has to line up is `Greeter#greet().` against
// `Speaker#greet().`. The explicit clause is still recorded, as what it
// structurally is: a type reference to the interface.
func TestInterfaceMembersLineUpWithAnImplementation(t *testing.T) {
	iface := parse(t, "src/main/java/com/example/greeter/Speaker.java", `
package com.example.greeter;

public interface Speaker {
    String greet();
}
`)
	impl := parse(t, "src/main/java/com/example/greeter/Greeter.java", `
package com.example.greeter;

public class Greeter implements Speaker {
    public String greet() { return ""; }
}
`)

	assertHasDefinition(t, iface, prefix+" com/example/greeter/Speaker#greet().")
	assertHasDefinition(t, impl, prefix+" com/example/greeter/Greeter#greet().")
	assert.Equal(t, facts.KindInterface, definitionNamed(t, iface, "Speaker").SymbolKind,
		"link's implements derivation keys off this kind")
	assert.Equal(t, facts.KindType, definitionNamed(t, impl, "Greeter").SymbolKind)

	// The `implements` clause itself, navigable with no schema change at all.
	assert.Equal(t, prefix+" com/example/greeter/Speaker#",
		referenceNamed(t, impl, "Speaker").Descriptor.String())
}

func TestAnExtendsClauseIsATypeReference(t *testing.T) {
	ff := parse(t, "src/main/java/com/example/greeter/Loud.java", `
package com.example.greeter;

public class Loud extends Greeter implements Speaker {
    public String greet() { return ""; }
}
`)
	refs := referenceDescriptors(ff)
	assert.Contains(t, refs, prefix+" com/example/greeter/Greeter#")
	assert.Contains(t, refs, prefix+" com/example/greeter/Speaker#")
}

// ---------------------------------------------------------------- references --

func TestParseReferenceDescriptors(t *testing.T) {
	ff := parse(t, "src/main/java/com/example/app/Main.java", `
package com.example.app;

import com.example.greeter.Greeter;

public class Main {
    private String field;

    public void run() {
        Greeter g = new Greeter("world");
        String message = g.greet();
        int n = this.field.length();
    }
}
`)

	refs := referenceDescriptors(ff)
	// The receiver is typed from its declaration, never inferred, and the
	// descriptor it produces is what greeter/Greeter.java independently wrote.
	assert.Contains(t, refs, prefix+" com/example/greeter/Greeter#greet().")
	assert.Contains(t, refs, prefix+" com/example/greeter/Greeter#")
	// `this` resolves to the enclosing type.
	assert.Contains(t, refs, prefix+" com/example/app/Main#field.")
	// A member of a JDK type reached through a declared field.
	assert.Contains(t, refs, jdk+" lang/String#length().")
}

func TestParseResolvesAReceiverThroughItsDeclaredType(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a local's declared type",
			src:  "void f() { Greeter g = null; g.greet(); }",
			want: prefix + " com/example/greeter/Greeter#greet().",
		},
		{
			name: "a parameter's declared type",
			src:  "void f(Greeter g) { g.greet(); }",
			want: prefix + " com/example/greeter/Greeter#greet().",
		},
		{
			name: "a field's declared type",
			src:  "Greeter g; void f() { this.g.greet(); }",
			want: prefix + " com/example/greeter/Greeter#greet().",
		},
		{
			// A generic application is transparent: the members reached
			// through `List<String>` are `List`'s.
			name: "a generic application is named by its raw type",
			src:  "void f(java.util.List<String> xs) { xs.size(); }",
			want: jdk + " util/List#size().",
		},
		{
			name: "an array is named by its element type",
			src:  "void f(Greeter[] gs) { gs[0].greet(); }",
			want: prefix + " com/example/app/.#greet().",
		},
		{
			// The type is written right there, which makes this the one
			// expression form worth reading back.
			name: "a constructor call names its own type",
			src:  "void f() { new Greeter(\"x\").greet(); }",
			want: prefix + " com/example/greeter/Greeter#greet().",
		},
		{
			// A static call on a type is exact: a type name is resolvable
			// syntactically, with no receiver to recover.
			name: "a static call on an imported type",
			src:  "void f() { Greeter.of(); }",
			want: prefix + " com/example/greeter/Greeter#of().",
		},
		{
			// `var` names no type: the type is in the initialiser's type,
			// which is a type checker's answer and not a syntactic one.
			name: "an inferred local reaches an unknown type",
			src:  "void f() { var g = build(); g.greet(); }",
			want: prefix + " com/example/app/.#greet().",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ff := parse(t, "src/main/java/com/example/app/Main.java",
				"package com.example.app;\n\nimport com.example.greeter.Greeter;\n\nclass Main { "+tc.src+" }\n")
			assert.Contains(t, referenceDescriptors(ff), tc.want)
		})
	}
}

// TestParseResolvesAFullyQualifiedName is Java's absolute-path rule, which is
// where it is simpler than Rust: a qualified name always starts at the root, so
// `com.example.util.Text` means the same thing in every file regardless of which
// package the file is in.
func TestParseResolvesAFullyQualifiedName(t *testing.T) {
	ff := parse(t, "src/main/java/com/example/app/Main.java", `
package com.example.app;

class Main {
    void f() {
        com.example.util.Text.upper("x");
    }
}
`)
	assert.Contains(t, referenceDescriptors(ff), prefix+" com/example/util/Text#upper().")
	assert.NotContains(t, referenceDescriptors(ff), prefix+" com/example/app/com/example/util/Text#upper().")
}

// TestVarIsNotAType guards the one contextual keyword the grammar hands back as
// a plain type identifier. Emitting an occurrence for it would invent a symbol
// called `var` in every package that ever declared an inferred local.
func TestVarIsNotAType(t *testing.T) {
	ff := parse(t, "src/main/java/com/example/app/Main.java", `
package com.example.app;

class Main {
    void f() {
        var n = 1;
    }
}
`)
	for _, o := range ff.Occurrences {
		assert.NotEqual(t, "var", o.Name, "`var` names no type")
	}
}

// TestOverloadsRenderOneDescriptor states the limit rather than hiding it: the
// descriptor's callable component carries the name and not the signature, so two
// overloads collide and a reference to either resolves to both.
//
// Encoding the signature is what SCIP's disambiguating suffix exists for, and it
// is deliberately not done: the definition side could number its overloads from
// one file's CST, but the reference side could not without type checking, and a
// descriptor only one side can compute is worse than a coarse one both sides
// compute the same way.
func TestOverloadsRenderOneDescriptor(t *testing.T) {
	ff := parse(t, "src/main/java/com/example/app/Main.java", `
package com.example.app;

class Main {
    void greet(String s) {}
    void greet(int n) {}
}
`)
	var seen int
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition && o.Descriptor.String() == prefix+" com/example/app/Main#greet()." {
			seen++
		}
	}
	assert.Equal(t, 2, seen, "two overloads, one descriptor")
}

// ---------------------------------------------------------------------- scopes --

func TestScopesAreJavasOwn(t *testing.T) {
	ff := parse(t, "src/main/java/com/example/app/Main.java", `
package com.example.app;

class Main {
    void f(int n) {
        if (n > 0) {
            int inner = n;
        }
        for (int i = 0; i < n; i++) {}
        try { g(); } catch (RuntimeException e) {}
        Runnable r = () -> {};
    }
    void g() {}
    static { int s = 0; }
    interface Q {}
}
`)

	kinds := map[string]int{}
	for _, s := range ff.Scopes {
		kinds[s.Kind]++
	}
	// The file, and no package scope: a Java package is declared once at the
	// top of a file and is never a lexical region below it, which is Go's shape
	// and not Rust's.
	assert.Equal(t, 1, kinds[facts.ScopeFile])
	assert.Zero(t, kinds[facts.ScopePackage])
	// The class and the nested interface.
	assert.Equal(t, 2, kinds[facts.ScopeType])
	// Two methods, a static initialiser and a lambda.
	assert.Equal(t, 4, kinds[facts.ScopeFunction])
	// Blocks really do scope their declarations here, unlike Python's.
	assert.Positive(t, kinds[facts.ScopeBlock])

	// The file scope is the outermost one and parents everything.
	require.NotEmpty(t, ff.Scopes)
	assert.Equal(t, facts.ScopeFile, ff.Scopes[0].Kind)
	assert.Equal(t, facts.NoID, ff.Scopes[0].Parent)
}

func TestPackageDefinitionIsInTheFileScope(t *testing.T) {
	ff := parse(t, "src/main/java/com/example/app/Main.java", `
package com.example.app;

class Main {}
`)
	require.NotEmpty(t, ff.Scopes)
	assert.Equal(t, ff.Scopes[0].ID, moduleDefinition(t, ff).Scope)
}

// --------------------------------------------------------------------- edges --

func TestParseResolvesSameFileReferences(t *testing.T) {
	ff := parse(t, "src/main/java/com/example/app/Main.java", `
package com.example.app;

class Main {
    private String label;

    void f() {
        String local = this.label;
        g();
    }

    void g() {}
}
`)

	// A field read through `this`, a local read, and an unqualified call: all
	// three targets are in this CST, so the edges are extracted rather than
	// left to the link pass (§4.3).
	assertResolvesLocally(t, ff, "label", prefix+" com/example/app/Main#label.")
	assertResolvesLocally(t, ff, "g", prefix+" com/example/app/Main#g().")
}

// TestForwardFieldReferencesResolve is a rule Java has and no earlier language
// here does: a method may use a field declared *below* it, which is legal and
// common. A lookup that only ever walked backwards would miss it.
func TestForwardFieldReferencesResolve(t *testing.T) {
	ff := parse(t, "src/main/java/com/example/app/Main.java", `
package com.example.app;

class Main {
    void f() {
        String local = this.label;
    }

    private String label;
}
`)
	assertResolvesLocally(t, ff, "label", prefix+" com/example/app/Main#label.")
}

func TestParseEmitsOnlyExtractableEdgeKinds(t *testing.T) {
	ff := parse(t, "src/main/java/com/example/app/Main.java", `
package com.example.app;

import com.example.greeter.Greeter;

class Main {
    void f(Greeter g) { g.greet(); }
}
`)
	allowed := map[facts.EdgeKind]bool{
		facts.EdgeDefines: true, facts.EdgeContains: true, facts.EdgeReferencesLocal: true,
	}
	for _, e := range ff.Edges {
		assert.True(t, allowed[e.Kind], "extracted a derived edge kind: %s", e.Kind)
	}
}

// ---------------------------------------------------------------- the file ---

func TestParseFile(t *testing.T) {
	c := testCoord(t)
	path := filepath.Join(c.Root, filepath.FromSlash("src/main/java/com/example/app/Main.java"))
	ff := java.New().Parse(path, []byte("package com.example.app;\n\nclass Main {}\n"), c)

	require.Empty(t, ff.ParseError)
	assert.Equal(t, path, ff.File.Path)
	assert.Equal(t, java.Lang, ff.File.Lang)
	assert.Equal(t, c, ff.File.Coord)
}

// TestParseTheFixture is the corpus the feature suite indexes, checked here so
// that a change to either side is caught by the fast test rather than by a
// container.
func TestParseTheFixture(t *testing.T) {
	c := testCoord(t)
	tests := []struct {
		path string
		want []string
	}{
		{
			path: "src/main/java/com/example/greeter/Greeter.java",
			want: []string{
				prefix + " com/example/greeter/",
				prefix + " com/example/greeter/Greeter#",
				prefix + " com/example/greeter/Greeter#greet().",
			},
		},
		{
			path: "src/main/java/com/example/app/Main.java",
			want: []string{
				prefix + " com/example/app/",
				prefix + " com/example/app/Main#main().",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join(c.Root, filepath.FromSlash(tc.path)))
			require.NoError(t, err)
			ff := java.New().Parse(filepath.Join(c.Root, filepath.FromSlash(tc.path)), src, c)
			require.Empty(t, ff.ParseError)
			for _, want := range tc.want {
				assertHasDefinition(t, ff, want)
			}
		})
	}

	// The claim the first feature scenario rests on: the two files, parsed
	// independently, wrote the same descriptor for `greet`.
	greeterSrc, err := os.ReadFile(filepath.Join(c.Root, filepath.FromSlash("src/main/java/com/example/greeter/Greeter.java")))
	require.NoError(t, err)
	mainSrc, err := os.ReadFile(filepath.Join(c.Root, filepath.FromSlash("src/main/java/com/example/app/Main.java")))
	require.NoError(t, err)

	greeter := java.New().Parse("Greeter.java", greeterSrc, c)
	main := java.New().Parse("Main.java", mainSrc, c)
	assert.Equal(t,
		definitionNamed(t, greeter, "greet").Descriptor.String(),
		referenceNamed(t, main, "greet").Descriptor.String())
}

func TestParseBrokenSourceStillReturns(t *testing.T) {
	c := testCoord(t)
	ff := java.New().Parse("Broken.java", []byte("class {{{ ¬"), c)
	assert.Equal(t, java.Lang, ff.File.Lang)
}

// TestParseEmptyFile records what an empty compilation unit is: a member of the
// default package and nothing else.
//
// Java differs from Rust here, and the difference is the namespace decision
// showing through. A Rust file's module comes from its path, so an empty one
// still names a module; a Java file's package comes from a clause it does not
// have, so the only thing it can honestly say is "the default package" — which
// is what every other clause-less Java file says too, exactly as javac sees it.
func TestParseEmptyFile(t *testing.T) {
	ff := parse(t, "src/main/java/com/example/app/Empty.java", "")

	require.Len(t, ff.Occurrences, 1)
	assert.Equal(t, facts.KindPackage, ff.Occurrences[0].SymbolKind)
	assert.Equal(t, prefix, ff.Occurrences[0].Descriptor.String())
	for _, e := range ff.Edges {
		assert.NotEqual(t, facts.EdgeReferencesLocal, e.Kind, "nothing to resolve in an empty file")
	}
}

// --- helpers ---------------------------------------------------------------

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

func definitionNamed(t *testing.T, ff facts.FileFacts, name string) facts.Occurrence {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition && o.Name == name {
			return o
		}
	}
	t.Fatalf("no definition named %q", name)
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

func moduleDefinition(t *testing.T, ff facts.FileFacts) facts.Occurrence {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition && o.SymbolKind == facts.KindPackage {
			return o
		}
	}
	t.Fatal("no package definition")
	return facts.Occurrence{}
}

func moduleReference(t *testing.T, ff facts.FileFacts) facts.Occurrence {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference && o.SymbolKind == facts.KindPackage {
			return o
		}
	}
	t.Fatal("no package reference")
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
