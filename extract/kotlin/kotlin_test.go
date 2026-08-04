package kotlin_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/extract/kotlin"
	"github.com/gaarutyunov/codiq/facts"
)

// testCoord is the coordinate the batch would hand the parser for a file in the
// Gradle build `greeter`, rooted at the fixture directory.
func testCoord(t *testing.T) coord.Coord {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "greeter"))
	require.NoError(t, err)
	coords, err := coord.Resolve(root)
	require.NoError(t, err)
	c := coords.For("X.kt")
	require.Equal(t, coord.KotlinScheme, c.Scheme, "the fixture must resolve through the Gradle resolver")
	return c
}

// parse parses src as the file at name, which is interpreted relative to the
// build root. The path does not name the file's namespace — the `package` clause
// does — except for a `.kts`, and two tests below are about exactly that.
func parse(t *testing.T, name, src string) facts.FileFacts {
	t.Helper()
	c := testCoord(t)
	ff := kotlin.New().Parse(filepath.Join(c.Root, filepath.FromSlash(name)), []byte(src), c)
	require.Empty(t, ff.ParseError)
	return ff
}

const prefix = "scip-kotlin gradle greeter ."

// stdlib is the coordinate references to the Kotlin standard library carry:
// foreign, so nothing under it can ever match a definition this index owns.
const stdlib = "scip-kotlin gradle kotlin ."

// --------------------------------------------------------------- definitions --

func TestParseDefinitionDescriptors(t *testing.T) {
	ff := parse(t, "src/main/kotlin/shapes/Shapes.kt", `
package com.example.shapes

const val LEVEL = 1

typealias Label = String

interface Speaker {
    fun speak(): String
}

enum class Mood { HAPPY, SAD }

data class Point(val x: Int, val y: Int)

object Registry {
    fun register(name: String) {}
}

class Shapes(private val label: String) : Speaker {
    companion object {
        const val DEFAULT = "none"

        fun of(label: String): Shapes = Shapes(label)
    }

    override fun speak(): String {
        val local = label
        return local
    }

    class Inner {
        fun ping() {}
    }
}

fun <T> identity(value: T): T = value

fun String.shout(): String = uppercase()
`)

	// The whole set, so that a suffix rule which starts producing an *extra*
	// definition fails here too.
	want := []string{
		// The package the file declares, which is written down as Go's and
		// Java's are and unlike TypeScript's, Python's and Rust's.
		prefix + " com/example/shapes/",
		prefix + " com/example/shapes/LEVEL.",
		prefix + " com/example/shapes/Label#",
		prefix + " com/example/shapes/Speaker#",
		prefix + " com/example/shapes/Speaker#speak().",
		prefix + " com/example/shapes/Mood#",
		prefix + " com/example/shapes/Mood#HAPPY.",
		prefix + " com/example/shapes/Mood#SAD.",
		prefix + " com/example/shapes/Point#",
		// A `val` in a primary constructor is state, not an argument.
		prefix + " com/example/shapes/Point#x.",
		prefix + " com/example/shapes/Point#y.",
		prefix + " com/example/shapes/Registry#",
		prefix + " com/example/shapes/Registry#register().",
		prefix + " com/example/shapes/Registry#register().(name)",
		prefix + " com/example/shapes/Shapes#",
		prefix + " com/example/shapes/Shapes#label.",
		// The companion's members are the class's: no `Companion#` level, so
		// that `Shapes.of(…)` written in another file renders this string.
		prefix + " com/example/shapes/Shapes#DEFAULT.",
		prefix + " com/example/shapes/Shapes#of().",
		prefix + " com/example/shapes/Shapes#of().(label)",
		prefix + " com/example/shapes/Shapes#speak().",
		prefix + " com/example/shapes/Shapes#speak().local.",
		// Nested types are a real level of the descriptor.
		prefix + " com/example/shapes/Shapes#Inner#",
		prefix + " com/example/shapes/Shapes#Inner#ping().",
		// A top-level function is a member of the package and not of any type,
		// which is the shape Java has no counterpart for.
		prefix + " com/example/shapes/identity().",
		prefix + " com/example/shapes/identity().(T)",
		prefix + " com/example/shapes/identity().(value)",
		// An extension function is declared where it is written, not under the
		// type it extends.
		prefix + " com/example/shapes/shout().",
	}
	sort.Strings(want)
	assert.Equal(t, want, definitionDescriptors(ff))
}

func TestParseDefinitionKinds(t *testing.T) {
	ff := parse(t, "src/main/kotlin/shapes/Kinds.kt", `
package com.example.shapes

const val LEVEL = 1
val mutableTop = 2

typealias Label = String

interface Speaker {
    fun speak(): String
}

enum class Mood { HAPPY }

object Registry

class Kinds(val field: String, other: String) {
    var counter = 0

    fun run(arg: String) {
        val local = arg
        for (item in listOf(local)) {
            println(item)
        }
    }
}
`)
	defs := definitionsByName(ff)

	// `interface` is its own kind because link keys `implements` off it, and the
	// grammar spells a class and an interface with one node type — so this is the
	// assertion that the keyword is read.
	assert.Equal(t, facts.KindInterface, defs["Speaker"].SymbolKind)
	assert.Equal(t, facts.KindMethod, defs["speak"].SymbolKind)

	// An `object`, an `enum class`, a `class` and a `typealias` are all types.
	for _, name := range []string{"Registry", "Mood", "Kinds", "Label"} {
		assert.Equal(t, facts.KindType, defs[name].SymbolKind, name)
	}
	assert.Equal(t, facts.KindField, defs["HAPPY"].SymbolKind)

	// A property's kind is decided by where it is written and how: `const` makes
	// a constant, a type body makes a field, a function body makes a local.
	assert.Equal(t, facts.KindConstant, defs["LEVEL"].SymbolKind)
	assert.Equal(t, facts.KindVariable, defs["mutableTop"].SymbolKind)
	assert.Equal(t, facts.KindField, defs["counter"].SymbolKind)
	assert.Equal(t, facts.KindVariable, defs["local"].SymbolKind)
	assert.Equal(t, facts.KindVariable, defs["item"].SymbolKind)

	// A primary-constructor parameter is state when it carries `val`/`var` and an
	// argument when it does not, which is the one distinction the CST makes and
	// the capture name cannot.
	assert.Equal(t, facts.KindField, defs["field"].SymbolKind)
	assert.Equal(t, facts.KindParameter, defs["other"].SymbolKind)
	assert.Equal(t, facts.KindParameter, defs["arg"].SymbolKind)

	// A top-level function is a function; a member is a method.
	assert.Equal(t, facts.KindMethod, defs["run"].SymbolKind)
}

// A constructor is written with no name at all, in either of its two spellings,
// so there is nothing to build a descriptor component from — and nothing needs
// one, because a constructor call is written as the type's own name.
func TestConstructorsAreNotDefinitions(t *testing.T) {
	ff := parse(t, "src/main/kotlin/shapes/Ctor.kt", `
package com.example.shapes

class Ctor(val name: String) {
    constructor() : this("anonymous")
}
`)
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition {
			assert.NotEqual(t, "constructor", o.Name)
			assert.NotEqual(t, prefix+" com/example/shapes/Ctor#Ctor().", o.Descriptor.String())
		}
	}
	assertHasDefinition(t, ff, prefix+" com/example/shapes/Ctor#")
	assertHasDefinition(t, ff, prefix+" com/example/shapes/Ctor#name.")
}

// --------------------------------------------------------------- namespaces --

// The namespace is the `package` clause and not the path, and Kotlin is the
// language where insisting on that matters most: the official style guide tells
// you to omit the common root package from the directory layout, so this fixture
// — `src/main/kotlin/greeter/` declaring `package com.example.greeter` — is the
// recommended shape rather than a mistake.
func TestNamespaceComesFromTheDeclarationAndNotThePath(t *testing.T) {
	ff := parse(t, "src/main/kotlin/greeter/Greeter.kt", `
package com.example.greeter

class Greeter {
    fun greet(): String = ""
}
`)
	assertHasDefinition(t, ff, prefix+" com/example/greeter/Greeter#greet().")

	// The same source under a path that agrees with nothing renders the same
	// descriptors, because the path was never read.
	other := parse(t, "elsewhere/Greeter.kt", `
package com.example.greeter

class Greeter {
    fun greet(): String = ""
}
`)
	assert.Equal(t, definitionDescriptors(ff), definitionDescriptors(other))
}

// A file with no `package` clause is in the root package, which is a real
// namespace and not a missing one — and its top-level names collide with every
// other root-package file's, exactly as kotlinc's own resolution does.
func TestRootPackageHasNoNamespace(t *testing.T) {
	ff := parse(t, "src/main/kotlin/Loose.kt", "class Loose\n")

	assertHasDefinition(t, ff, prefix+" Loose#")
	pkg := moduleDefinition(t, ff)
	assert.Equal(t, prefix, pkg.Descriptor.String())
	assert.Equal(t, 0, pkg.RangeStart)
	assert.Equal(t, 0, pkg.RangeEnd, "with no clause to point at, the package occurrence is zero-width")
}

// The package definition and an import of it render one string, which is the
// whole of how link derives `imports` (§4.4): a descriptor join and nothing else.
func TestPackageDefinitionMatchesAnImport(t *testing.T) {
	defining := parse(t, "src/main/kotlin/greeter/Greeter.kt", `
package com.example.greeter

class Greeter
`)
	importing := parse(t, "src/main/kotlin/app/Main.kt", `
package com.example.app

import com.example.greeter.Greeter

fun main() {
    Greeter()
}
`)
	assert.Equal(t, moduleDefinition(t, defining).Descriptor.String(),
		moduleReference(t, importing).Descriptor.String())
}

// ------------------------------------------------------------------- scripts --

// A `.kts` is a compilation unit of its own: kotlinc synthesizes a class per
// script file, so two scripts declaring the same top-level name declare two
// unrelated things. Rendering one descriptor for both would have the link pass
// join them — a phantom edge, which is worse than a missing one.
func TestTwoScriptsDoNotCollide(t *testing.T) {
	build := parse(t, "build.gradle.kts", "val logger = 1\n")
	settings := parse(t, "settings.gradle.kts", "val logger = 2\n")

	assertHasDefinition(t, build, prefix+" Build_gradle#logger.")
	assertHasDefinition(t, settings, prefix+" Settings_gradle#logger.")
	assert.NotEqual(t, definitionDescriptors(build), definitionDescriptors(settings))
}

// A source file gets no such container, which is the other half of the claim: the
// script container exists for scripts and changes nothing else.
func TestASourceFileHasNoScriptContainer(t *testing.T) {
	ff := parse(t, "src/main/kotlin/Loose.kt", "val logger = 1\n")
	assertHasDefinition(t, ff, prefix+" logger.")
}

// ------------------------------------------------------------------ imports --

func TestImportsBindSimpleNames(t *testing.T) {
	ff := parse(t, "src/main/kotlin/app/Main.kt", `
package com.example.app

import com.example.greeter.Greeter
import com.example.util.formatAll
import kotlin.math.max as biggest
import com.example.legacy.*

fun main() {
    val g = Greeter()
    formatAll("x")
    biggest(1, 2)
}
`)
	refs := referenceDescriptors(ff)

	// A type import binds the simple name to the type's own descriptor.
	assert.Contains(t, refs, prefix+" com/example/greeter/Greeter#")

	// Kotlin imports *declarations*, so an all-lowercase path ends in a
	// top-level function rather than in a deeper package. Java cannot write this
	// and splitImport exists for it.
	assert.Contains(t, refs, prefix+" com/example/util/formatAll().")
	assert.Contains(t, refs, prefix+" com/example/util/")

	// An alias binds the *local* name to the *declared* one.
	assert.Contains(t, refs, stdlib+" math/max().")

	// A wildcard names a package and binds nothing: the names it brings into
	// scope are the other file's to state.
	assert.Contains(t, refs, prefix+" com/example/legacy/")
}

// Every identifier an import consumed is claimed, so the reference patterns —
// which match inside an import as readily as anywhere else — do not emit a
// second, weaker occurrence over the same bytes.
func TestImportsClaimTheirIdentifiers(t *testing.T) {
	ff := parse(t, "src/main/kotlin/app/Main.kt", `
package com.example.app

import com.example.greeter.Greeter
`)
	var greeters int
	for _, o := range ff.Occurrences {
		if o.Name == "Greeter" {
			greeters++
		}
	}
	assert.Equal(t, 1, greeters, "the import emits one occurrence for the type it names")
}

// ---------------------------------------------------------------- references --

// A constructor call is a call whose callee is a *type*, because Kotlin has no
// `new` — so the honest descriptor is the type's own, which is a definition that
// exists and which the class's own file renders identically.
func TestAConstructorCallResolvesToItsType(t *testing.T) {
	ff := parse(t, "src/main/kotlin/app/Main.kt", `
package com.example.app

import com.example.greeter.Greeter

fun main() {
    Greeter("world")
}
`)
	ref := referenceNamed(t, ff, "Greeter")
	assert.Equal(t, facts.KindType, ref.SymbolKind)
	assert.Equal(t, prefix+" com/example/greeter/Greeter#", ref.Descriptor.String())
}

// The receiver's type is read off its declaration where there is one and off the
// constructor call where there is not — which is the idiomatic spelling, and the
// only inference this stanza makes.
func TestParseResolvesAReceiverThroughItsType(t *testing.T) {
	ff := parse(t, "src/main/kotlin/app/Main.kt", `
package com.example.app

import com.example.greeter.Greeter

fun declared(g: Greeter): String = g.greet()

fun inferred(): String {
    val g = Greeter("world")
    return g.greet()
}

fun unknowable(): String {
    val g = build()
    return g.greet()
}
`)
	refs := referenceDescriptors(ff)
	assert.Equal(t, 2, count(refs, prefix+" com/example/greeter/Greeter#greet()."),
		"the declared and the constructed receiver both resolve")

	// `build()` names a type nothing states, so the receiver reduces to SCIP's
	// "." rather than to a guess.
	assert.Contains(t, refs, prefix+" com/example/app/.#greet().")
}

// A safe call is the same call. `?.` lives inside the navigation suffix beside
// the member name, so the shape the query matches is unchanged — which is the
// answer to the grammar's documented `"\?."` display-name quirk: nothing here
// names that token.
func TestSafeCallsResolveLikeOrdinaryOnes(t *testing.T) {
	ff := parse(t, "src/main/kotlin/app/Main.kt", `
package com.example.app

import com.example.greeter.Greeter

fun run(g: Greeter?): String? = g?.greet()
`)
	assert.Equal(t, prefix+" com/example/greeter/Greeter#greet().",
		referenceNamed(t, ff, "greet").Descriptor.String())
}

// A companion's members are reached through the class at every use site, so they
// are descriptored on the class — and this is the test that the two sides agree,
// which is the whole point of the flattening.
func TestCompanionMembersAreReachedThroughTheClass(t *testing.T) {
	defining := parse(t, "src/main/kotlin/greeter/Greeter.kt", `
package com.example.greeter

class Greeter {
    companion object {
        fun create(): Greeter = Greeter()
    }
}
`)
	using := parse(t, "src/main/kotlin/app/Main.kt", `
package com.example.app

import com.example.greeter.Greeter

fun main() {
    Greeter.create()
}
`)
	assertHasDefinition(t, defining, prefix+" com/example/greeter/Greeter#create().")
	assert.Equal(t, prefix+" com/example/greeter/Greeter#create().",
		referenceNamed(t, using, "create").Descriptor.String())
}

// A companion's members are in scope throughout the class body, which the scope
// tree cannot express: the companion is a sibling region of the code reading it.
func TestACompanionsMembersAreVisibleInTheClass(t *testing.T) {
	ff := parse(t, "src/main/kotlin/greeter/Greeter.kt", `
package com.example.greeter

class Greeter(val prefix: String) {
    companion object {
        const val DEFAULT = "hi"

        fun create(): Greeter = Greeter(DEFAULT)
    }

    constructor() : this(DEFAULT)

    fun rebuild(): Greeter = create()
}
`)
	assertResolvesLocally(t, ff, "create", prefix+" com/example/greeter/Greeter#create().")

	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference && o.Name == "DEFAULT" {
			assert.Equal(t, prefix+" com/example/greeter/Greeter#DEFAULT.", o.Descriptor.String())
		}
	}
}

// Inside an extension function `this` is the *receiver*, which is declared in the
// signature and nowhere else — so an unqualified call in one reaches the type
// being extended rather than the type the function is written next to.
func TestThisIsTheReceiverInAnExtension(t *testing.T) {
	ff := parse(t, "src/main/kotlin/app/Text.kt", `
package com.example.app

fun String.shout(): String = this.uppercase()
`)
	assert.Equal(t, stdlib+" String#uppercase().",
		referenceNamed(t, ff, "uppercase").Descriptor.String())
}

// A nested type reached through the type that holds it is a type, and a constant
// reached the same way is a constant. Kotlin writes both the same and the
// convention is what separates them.
func TestANestedTypeReadThroughItsHolderIsAType(t *testing.T) {
	ff := parse(t, "src/main/kotlin/shapes/Shapes.kt", `
package com.example.shapes

sealed class Shape {
    object Empty : Shape()
}

enum class Mood { HAPPY }

fun describe(): String {
    val a = Shape.Empty
    val b = Mood.HAPPY
    return "" + a + b
}
`)
	assertResolvesLocally(t, ff, "Empty", prefix+" com/example/shapes/Shape#Empty#")

	mood := referenceNamed(t, ff, "HAPPY")
	assert.Equal(t, facts.KindField, mood.SymbolKind)
	assert.Equal(t, prefix+" com/example/shapes/Mood#HAPPY.", mood.Descriptor.String())
}

// `it` is bound by the language rather than by the program, and `field` is the
// compiler's own storage: neither is declared anywhere, so neither may be
// rendered as a name in this package.
func TestTheTwoBindingsNothingDeclares(t *testing.T) {
	ff := parse(t, "src/main/kotlin/app/Main.kt", `
package com.example.app

class Holder {
    var value: String = ""
        set(input) { field = input }

    fun lengths(items: List<String>) = items.map { it.length }
}
`)
	for _, o := range ff.Occurrences {
		assert.NotEqual(t, "field", o.Name, "the backing field is not a symbol")
	}

	// `it` is emitted — it is a real reference — but it names an unknown value
	// rather than a package called `it`.
	assert.Equal(t, prefix+" com/example/app/.#", referenceNamed(t, ff, "it").Descriptor.String())
	assert.Equal(t, prefix+" com/example/app/.#length.", referenceNamed(t, ff, "length").Descriptor.String())
}

// Kotlin overloads on the parameter list and the descriptor carries the name
// only, so two overloads render one string. It is the trade java.go argues:
// the reference side could not compute a disambiguating suffix, and a descriptor
// only one side can compute is worse than a coarse one both compute alike.
func TestOverloadsRenderOneDescriptor(t *testing.T) {
	ff := parse(t, "src/main/kotlin/app/Over.kt", `
package com.example.app

class Over {
    fun greet(who: String): String = who
    fun greet(times: Int): String = ""
}
`)
	assert.Equal(t, 2, count(definitionDescriptors(ff), prefix+" com/example/app/Over#greet()."))
}

// ---------------------------------------------------------------------- scopes --

func TestScopesAreKotlinsOwn(t *testing.T) {
	ff := parse(t, "src/main/kotlin/app/Scopes.kt", `
package com.example.app

class Scopes(val held: String) {
    init { println(held) }

    fun run(items: List<String>) {
        for (item in items) {
            if (item.isEmpty()) {
                println(item)
            }
        }
        items.forEach { println(it) }
        try {
            println("x")
        } catch (e: IllegalStateException) {
            println(e)
        }
    }
}
`)
	kinds := map[string]int{}
	for _, s := range ff.Scopes {
		kinds[s.Kind]++
	}
	assert.Equal(t, 1, kinds[facts.ScopeFile])
	assert.Equal(t, 1, kinds[facts.ScopeType])
	// The function, the `init` block and the lambda.
	assert.Equal(t, 3, kinds[facts.ScopeFunction])
	assert.Positive(t, kinds[facts.ScopeBlock])
}

// A primary constructor is deliberately not a scope: `class Scopes(val held: …)`
// declares a property of the class, so a scope around it would hide it from every
// member below.
func TestAPrimaryConstructorParameterIsVisibleInTheClass(t *testing.T) {
	ff := parse(t, "src/main/kotlin/app/Held.kt", `
package com.example.app

class Held(val held: String) {
    fun show(): String = held
}
`)
	assertResolvesLocally(t, ff, "held", prefix+" com/example/app/Held#held.")
}

// --------------------------------------------------------------------- edges --

func TestParseResolvesSameFileReferences(t *testing.T) {
	ff := parse(t, "src/main/kotlin/app/Local.kt", `
package com.example.app

class Local {
    private val name: String = "x"

    fun greet(): String = decorate(name)

    fun decorate(value: String): String = value
}
`)
	assertResolvesLocally(t, ff, "name", prefix+" com/example/app/Local#name.")
	assertResolvesLocally(t, ff, "decorate", prefix+" com/example/app/Local#decorate().")
}

// Only the four kinds §5 says a file-local extractor can know. The other five are
// link's, and emitting one here would be a claim this stanza cannot make.
func TestParseEmitsOnlyExtractableEdgeKinds(t *testing.T) {
	ff := parse(t, "src/main/kotlin/app/Local.kt", `
package com.example.app

class Local {
    val name: String = "x"

    fun greet(): String = name
}
`)
	allowed := map[facts.EdgeKind]bool{
		facts.EdgeDefines:         true,
		facts.EdgeContains:        true,
		facts.EdgeReferencesLocal: true,
	}
	for _, e := range ff.Edges {
		assert.True(t, allowed[e.Kind], "unexpected edge kind %q", e.Kind)
	}
}

// ---------------------------------------------------------------- the file ---

func TestParseFile(t *testing.T) {
	c := testCoord(t)
	path := filepath.Join(c.Root, filepath.FromSlash("src/main/kotlin/app/Main.kt"))
	ff := kotlin.New().Parse(path, []byte("package com.example.app\n"), c)

	assert.Equal(t, path, ff.File.Path)
	assert.Equal(t, kotlin.Lang, ff.File.Lang)
	assert.Equal(t, c, ff.File.Coord)
	assert.Empty(t, ff.ParseError)
}

// The fixture, parsed as the batch would: two files, one cross-file call, and the
// descriptor that has to match on both sides for it to be traversable.
func TestParseTheFixture(t *testing.T) {
	c := testCoord(t)
	p := kotlin.New()

	read := func(rel string) facts.FileFacts {
		t.Helper()
		path := filepath.Join(c.Root, filepath.FromSlash(rel))
		src, err := os.ReadFile(path) //nolint:gosec // the fixture is in the repository.
		require.NoError(t, err)
		ff := p.Parse(path, src, c)
		require.Empty(t, ff.ParseError)
		return ff
	}

	greeter := read("src/main/kotlin/greeter/Greeter.kt")
	main := read("src/main/kotlin/app/Main.kt")

	assertHasDefinition(t, greeter, prefix+" com/example/greeter/Greeter#greet().")
	assert.Equal(t, prefix+" com/example/greeter/Greeter#greet().",
		referenceNamed(t, main, "greet").Descriptor.String(),
		"the call and the definition have to render one string or nothing joins them")
}

// A file that does not parse is a property of the file and not of the call: the
// facts are whatever the error-tolerant tree yielded, and the caller decides.
func TestParseBrokenSourceStillReturns(t *testing.T) {
	c := testCoord(t)
	ff := kotlin.New().Parse(filepath.Join(c.Root, "Broken.kt"),
		[]byte("package com.example.app\n\nclass Broken {\n    fun ping( {\n"), c)

	assert.Empty(t, ff.ParseError, "tree-sitter recovers; a broken file is not a failed call")
	assert.Equal(t, kotlin.Lang, ff.File.Lang)
}

// TestParseEmptyFile records what an empty Kotlin file is: a member of the root
// package and nothing else — which is what every other clause-less Kotlin file
// says too, exactly as kotlinc sees it.
func TestParseEmptyFile(t *testing.T) {
	ff := parse(t, "src/main/kotlin/Empty.kt", "")

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

func count(all []string, want string) int {
	n := 0
	for _, s := range all {
		if s == want {
			n++
		}
	}
	return n
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
