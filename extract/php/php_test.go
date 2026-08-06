package php_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/extract/php"
	"github.com/gaarutyunov/codiq/facts"
)

// testCoord is the coordinate the batch would hand the parser for a file in the
// Composer package codiq/greeter, rooted at the fixture directory.
func testCoord(t *testing.T) coord.Coord {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "greeter"))
	require.NoError(t, err)
	coords, err := coord.Resolve(root, "greeter")
	require.NoError(t, err)
	c := coords.For("x" + php.Ext)
	require.Equal(t, coord.PHPScheme, c.Scheme, "the fixture must resolve through the Composer resolver")
	return c
}

// parse parses src as the file at name, which is interpreted relative to the
// package root.
//
// The path is very nearly irrelevant here, and that is the point: PHP's namespace
// is written in the file, so nothing below depends on where the file sits — which
// is what TestTheNamespaceIsWrittenDownAndNotDerivedFromThePath asserts outright.
func parse(t *testing.T, name, src string) facts.FileFacts {
	t.Helper()
	c := testCoord(t)
	ff := php.New().Parse(filepath.Join(c.Root, filepath.FromSlash(name)), []byte(src), c)
	require.Empty(t, ff.ParseError)
	return ff
}

const prefix = "scip-php composer codiq/greeter 1.0.0"

// --------------------------------------------------------------- definitions --

func TestParseDefinitionDescriptors(t *testing.T) {
	ff := parse(t, "src/Shapes/Shapes.php", `<?php

namespace Com\Example\Shapes;

const LEVEL = 1;

interface Drawable
{
    public function draw(): string;
}

trait Outlined
{
    public function outline(): string
    {
        return '';
    }
}

class Shapes implements Drawable
{
    use Outlined;

    public const MAX = 3;

    private string $label;
    private static int $made = 0;

    public function __construct(string $label)
    {
        $this->label = $label;
    }

    public function draw(): string
    {
        $local = $this->label;

        return $local;
    }

    public static function build(string $label): self
    {
        return new Shapes($label);
    }
}

function loose(int $a): int
{
    return $a;
}
`)

	for _, want := range []string{
		prefix + " Com/Example/Shapes/",
		prefix + " Com/Example/Shapes/LEVEL.",
		prefix + " Com/Example/Shapes/Drawable#",
		prefix + " Com/Example/Shapes/Drawable#draw().",
		prefix + " Com/Example/Shapes/Outlined#",
		prefix + " Com/Example/Shapes/Outlined#outline().",
		prefix + " Com/Example/Shapes/Shapes#",
		prefix + " Com/Example/Shapes/Shapes#MAX.",
		prefix + " Com/Example/Shapes/Shapes#label.",
		prefix + " Com/Example/Shapes/Shapes#made.",
		prefix + " Com/Example/Shapes/Shapes#__construct().",
		prefix + " Com/Example/Shapes/Shapes#__construct().(label)",
		prefix + " Com/Example/Shapes/Shapes#draw().",
		prefix + " Com/Example/Shapes/Shapes#draw().local.",
		prefix + " Com/Example/Shapes/Shapes#build().",
		prefix + " Com/Example/Shapes/Shapes#build().(label)",
		prefix + " Com/Example/Shapes/loose().",
		prefix + " Com/Example/Shapes/loose().(a)",
	} {
		assertHasDefinition(t, ff, want)
	}
}

func TestParseDefinitionKinds(t *testing.T) {
	ff := parse(t, "src/Kinds/Kinds.php", `<?php

namespace Kinds;

const TOP = 1;

interface Marker
{
}

trait Helper
{
}

enum Suit: string
{
    case Hearts = 'H';
}

class Thing
{
    public const MAX = 3;

    private string $label = '';

    public function describe(int $times): string
    {
        $local = '';

        return $local;
    }
}

function loose(int $a): int
{
    return $a;
}
`)

	kinds := map[string]string{}
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition {
			kinds[o.Descriptor.Suffix] = o.SymbolKind
		}
	}

	assert.Equal(t, map[string]string{
		"Kinds/":                         facts.KindPackage,
		"Kinds/TOP.":                     facts.KindConstant,
		"Kinds/Marker#":                  facts.KindInterface,
		"Kinds/Helper#":                  facts.KindType,
		"Kinds/Suit#":                    facts.KindType,
		"Kinds/Suit#Hearts.":             facts.KindConstant,
		"Kinds/Thing#":                   facts.KindType,
		"Kinds/Thing#MAX.":               facts.KindConstant,
		"Kinds/Thing#label.":             facts.KindField,
		"Kinds/Thing#describe().":        facts.KindMethod,
		"Kinds/Thing#describe().(times)": facts.KindParameter,
		"Kinds/Thing#describe().local.":  facts.KindVariable,
		"Kinds/loose().":                 facts.KindFunction,
		"Kinds/loose().(a)":              facts.KindParameter,
	}, kinds)
}

// TestNoDefinitionIsEmittedAsAModule guards the one kind that would be invisible
// to the link pass. `imports` joins on `symbol_kind = 'package'`
// (store/sqlc/query.sql), so anything emitted with `facts.KindModule` derives no
// import edge at all and fails *silently* — TypeScript has that defect today and
// Ruby was one line from it. §5's own capture list still names `module`; this is
// the reason not to follow it.
//
// PHP has three tempting candidates and none of them is a module: a `namespace`
// is a `package`, a `trait` is a `type`, and there is nothing else.
func TestNoDefinitionIsEmittedAsAModule(t *testing.T) {
	ff := parse(t, "src/Every/Every.php", `<?php

namespace Every\Kind;

const K = 1;

interface I
{
    public function m(): void;
}

trait T
{
    public function t(): void
    {
    }
}

enum E: string
{
    case A = 'a';
}

class C implements I
{
    use T;

    public const X = 1;
    public int $f = 0;

    public function m(): void
    {
        $v = 1;
    }
}

function g(int $p): void
{
}
`)

	require.NotEmpty(t, ff.Occurrences)
	for _, o := range ff.Occurrences {
		assert.NotEqual(t, facts.KindModule, o.SymbolKind,
			"%s was emitted as a module; link's imports derivation joins on 'package' and would skip it silently",
			o.Descriptor.String())
	}
	assertHasDefinition(t, ff, prefix+" Every/Kind/")
}

// TestATraitIsATypeAndNotAPackage is the decision that separates this stanza from
// Ruby's, and the two are opposite because the languages are.
//
// Ruby's `module` is genuinely a namespace — `Foo::Bar` names a constant inside
// it — so rb.go emits it as a `package` ending `/`. A PHP trait is not: nothing
// may be written `Loudly\x`, a trait lives *in* a namespace exactly as a class
// does, and its members are reached only through a class that uses it. So it is a
// `type` ending `#`, which is also what puts it in link's `type_def` CTE
// (`right(descriptor, 1) = '#'`) where its members can be gathered at all.
func TestATraitIsATypeAndNotAPackage(t *testing.T) {
	ff := parse(t, "src/Loud/Loudly.php", `<?php

namespace Com\Example\Greeting;

trait Loudly
{
    public function shout(): string
    {
        return '';
    }
}
`)

	assertHasDefinition(t, ff, prefix+" Com/Example/Greeting/Loudly#")
	assertHasDefinition(t, ff, prefix+" Com/Example/Greeting/Loudly#shout().")
	assertNoDefinition(t, ff, prefix+" Com/Example/Greeting/Loudly/")

	for _, o := range ff.Occurrences {
		if o.Name == "Loudly" && o.Role == facts.RoleDefinition {
			assert.Equal(t, facts.KindType, o.SymbolKind)
		}
	}
}

// TestATraitUseIsATypeReferenceAndNotAnImport is the other half of the same
// decision. Ruby's `include Foo` derives an `imports` edge because a module *is*
// a package and the reference carries a package descriptor; PHP's `use Loudly;`
// inside a class body derives none, because a trait is a type.
//
// That is not a loss: PHP already has an import mechanism, and it is the top-level
// `use` — which the next test covers.
func TestATraitUseIsATypeReferenceAndNotAnImport(t *testing.T) {
	ff := parse(t, "src/Loud/Speaker.php", `<?php

namespace Com\Example\Greeting;

class Speaker
{
    use Loudly;
}
`)

	ref := referenceNamed(t, ff, "Loudly")
	assert.Equal(t, facts.KindType, ref.SymbolKind)
	assert.Equal(t, prefix+" Com/Example/Greeting/Loudly#", ref.Descriptor.String())

	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference && o.SymbolKind == facts.KindPackage {
			t.Fatalf("a trait use produced a package reference %q; only a top-level `use` may", o.Descriptor.String())
		}
	}
}

// TestATraitDoesNotSatisfyAnInterfaceInThisGraph is the stanza's one deliberate
// false negative, stated as a fact about the output rather than as a paragraph.
//
// `class Counter implements Countable { use CountableTrait; }` **does** satisfy
// `Countable` in PHP — a trait is flattened into the using class at compile time,
// so `Counter::count()` really is a method of `Counter`. In this graph it does
// not: link gathers a method set by descriptor prefix with no file predicate
// (store/sqlc/query.sql), and the only definition row is
// `CountableTrait#count().`, so `Counter`'s method set is empty.
//
// The fix would be to flatten, which needs the trait's file and so is forbidden by
// §2.5 — and flattening only when the trait happens to share this file would make
// a derivation's answer depend on source layout, which is worse than a gap that is
// the same everywhere. This test is what stops that gap from being rediscovered as
// a bug.
func TestATraitDoesNotSatisfyAnInterfaceInThisGraph(t *testing.T) {
	ff := parse(t, "src/Count/Counter.php", `<?php

namespace Com\Example\Counting;

trait CountableTrait
{
    public function count(): int
    {
        return 0;
    }
}

class Counter implements \Countable
{
    use CountableTrait;
}
`)

	// The trait declares the member; the class does not, in the graph.
	assertHasDefinition(t, ff, prefix+" Com/Example/Counting/CountableTrait#count().")
	assertNoDefinition(t, ff, prefix+" Com/Example/Counting/Counter#count().")

	// And the class is still a `type` with a recorded reference to the interface,
	// so the day a semantic overlay (§4.2) flattens traits, nothing else changes.
	//
	// `\Countable` is written fully qualified because that is what correct PHP
	// writes: there is no global fallback for a class name, so a bare `Countable`
	// inside a namespace really does mean that namespace's `Countable` — in the
	// language and here. Getting that wrong is a runtime error in PHP and an
	// unresolved reference in this graph, which is the pair of outcomes a stanza
	// should have.
	assertHasDefinition(t, ff, prefix+" Com/Example/Counting/Counter#")
	assert.Equal(t, "scip-php composer php . Countable#", referenceNamed(t, ff, "Countable").Descriptor.String(),
		"an interface the language itself declares carries the language's coordinate, not this package's")
}

// TestAnInterfaceIsAnInterface is the claim Ruby could not make and PHP can. PHP
// has real interfaces with explicit satisfaction, so link's `implements`
// derivation applies unchanged — it is keyed off `symbol_kind = 'interface'`, and
// this is what puts a row there.
func TestAnInterfaceIsAnInterface(t *testing.T) {
	ff := parse(t, "src/Speak/Speaker.php", `<?php

namespace Com\Example\Greeting;

interface Speaker
{
    public function greet(): string;
}

class Greeter implements Speaker
{
    public function greet(): string
    {
        return '';
    }
}
`)

	kinds := definitionKinds(ff)
	assert.Equal(t, facts.KindInterface, kinds[prefix+" Com/Example/Greeting/Speaker#"])
	assert.Equal(t, facts.KindType, kinds[prefix+" Com/Example/Greeting/Greeter#"])

	// Method-set containment is what link joins on, so the two suffixes have to
	// agree exactly. They are asserted apart from the kinds because it is the
	// suffix and not the kind that makes an `implements` row appear.
	assertHasDefinition(t, ff, prefix+" Com/Example/Greeting/Speaker#greet().")
	assertHasDefinition(t, ff, prefix+" Com/Example/Greeting/Greeter#greet().")
}

// -------------------------------------------------------- static vs instance --

// TestStaticAndInstanceMembersShareOneNamespace is the descriptor decision PHP
// forced, and it is the *opposite* of Ruby's for reasons that are PHP's and not a
// change of mind.
//
// Ruby gives `def self.foo` a `self.` component on two grounds: every reference
// site tells the two apart syntactically, and one Ruby class may declare both
// `foo` and `self.foo`. PHP has the syntax that looks like the first and neither
// ground holds. It forbids the collision outright — a static and an instance
// member of one name is a fatal redeclaration — and `::` does not mean "static":
// `parent::__construct()` calls an instance method and is the commonest `::` in
// the language, while `$obj->staticMethod()` reaches a static one without a
// warning. A component keyed off an operator that does not partition the member
// namespace is one the two sides could not agree on.
func TestStaticAndInstanceMembersShareOneNamespace(t *testing.T) {
	ff := parse(t, "src/Both/Both.php", `<?php

namespace Both;

class Counter
{
    private static int $made = 0;

    public static function build(): self
    {
        self::$made++;

        return new Counter();
    }

    public function total(): int
    {
        return self::$made;
    }
}
`)

	// One descriptor for the static property, written `self::$made` at two
	// reference sites and `private static int $made` at the declaration.
	assertHasDefinition(t, ff, prefix+" Both/Counter#made.")
	assertHasDefinition(t, ff, prefix+" Both/Counter#build().")

	made := 0
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference && o.Descriptor.String() == prefix+" Both/Counter#made." {
			made++
		}
	}
	assert.Equal(t, 2, made, "both `self::$made` sites must render the declaration's descriptor")

	for _, o := range ff.Occurrences {
		assert.NotContains(t, o.Descriptor.Suffix, "static.",
			"%s carries a static qualifier; `::` distinguishes the binding, not the member namespace", o.Descriptor.String())
		assert.NotContains(t, o.Descriptor.Suffix, "self.",
			"%s carries Ruby's singleton component; PHP forbids the collision it exists to resolve", o.Descriptor.String())
	}
}

// TestAStaticCallAndAnInstanceCallRenderOneDescriptor is the claim above stated
// from the reference side, which is where it has to hold: link joins the two on a
// string, so if `::` and `->` rendered differently a call through one would never
// reach a definition reached through the other.
func TestAStaticCallAndAnInstanceCallRenderOneDescriptor(t *testing.T) {
	ff := parse(t, "src/Both/Call.php", `<?php

namespace Both;

class Caller
{
    public function run(): void
    {
        $c = new Counter();
        $c->build();
        Counter::build();
    }
}
`)

	seen := 0
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference && o.Name == "build" {
			assert.Equal(t, prefix+" Both/Counter#build().", o.Descriptor.String())
			seen++
		}
	}
	assert.Equal(t, 2, seen, "`$c->build()` and `Counter::build()` must both be recorded")
}

// ------------------------------------------------------------------ resolving --

// TestNameResolutionFollowsThePHPRules is the property that makes this stanza
// shorter than C#'s and Java's rather than longer: PHP's name resolution is
// entirely file-local by language design, so every case below is a transcription
// of the manual and not an approximation of it.
//
// Java splits a dotted path on a naming convention; C# cannot even do that and
// falls back to "the file has been told this is a namespace" plus a
// fires-only-when-unambiguous `using` rule that knowingly inverts C#'s own search
// order; Ruby has to guess whether a path segment is a module or a class. None of
// that is needed here.
func TestNameResolutionFollowsThePHPRules(t *testing.T) {
	ff := parse(t, "src/Resolve/Resolve.php", `<?php

namespace App\Domain;

use Vendor\Lib\Client;
use Vendor\Lib\Service as Svc;
use Vendor\Deep;

class Resolver
{
    public function run(): void
    {
        $a = new Client();
        $b = new Svc();
        $c = new Deep\Nested();
        $d = new Local();
        $e = new Sub\Local();
        $f = new \Root\Thing();
        $g = new namespace\Sibling();
    }
}
`)

	for name, want := range map[string]string{
		// Unqualified, imported: the alias table is exact, because a `use` always
		// imports a name and never a namespace on demand.
		"Client": prefix + " Vendor/Lib/Client#",
		// Unqualified, imported under another name.
		"Svc": prefix + " Vendor/Lib/Service#",
		// Qualified through an alias: `use Vendor\Deep;` makes `Deep\Nested` mean
		// `\Vendor\Deep\Nested`.
		"Nested": prefix + " Vendor/Deep/Nested#",
		// Unqualified, not imported: the current namespace, with no fallback —
		// PHP has a global fallback for functions and constants and none at all
		// for class names, which is why this cannot be `Local#`.
		"Local": prefix + " App/Domain/Local#",
		// Fully qualified: as written, always.
		"Thing": prefix + " Root/Thing#",
		// Relative: the current namespace, then the rest.
		"Sibling": prefix + " App/Domain/Sibling#",
	} {
		assert.Equal(t, want, referenceNamed(t, ff, name).Descriptor.String(), "resolving %s", name)
	}

	// Qualified without an alias: the current namespace, prepended whole.
	// Asserted apart because `Local` appears twice and referenceNamed takes the
	// first, which is the unqualified one above.
	assertHasReference(t, ff, prefix+" App/Domain/Sub/Local#")
}

// TestTheGlobalFallbackAppliesToFunctionsAndNotToClasses is the one place PHP's
// rules are *not* file-local, and the one approximation this stanza makes.
//
// An unqualified function or constant the current namespace does not define falls
// back to the global namespace at runtime, which is why `strtoupper($s)` works
// inside `namespace App;`. Whether `App\strtoupper` exists is a fact about another
// file, so the fallback is approximated with a set of the language's own names —
// rb.go's `$LOAD_PATH` approximation and cs.go's `System` one, a third time. There
// is no such fallback for a class, so `new Exception()` inside a namespace really
// does name that namespace's `Exception` and is rendered as one.
func TestTheGlobalFallbackAppliesToFunctionsAndNotToClasses(t *testing.T) {
	ff := parse(t, "src/Fallback/Fallback.php", `<?php

namespace App;

function localHelper(string $s): string
{
    return $s;
}

class Fallback
{
    public function run(string $s): void
    {
        strtoupper($s);
        localHelper($s);
        mysteryHelper($s);
        $e = new Exception();
        $f = new \Exception();
    }
}
`)

	// A name PHP itself declares: the language's, at a foreign coordinate.
	assert.Equal(t, "scip-php composer php . strtoupper().",
		referenceNamed(t, ff, "strtoupper").Descriptor.String())
	// A name this file declares: this file's, and the fallback never fires.
	assert.Equal(t, prefix+" App/localHelper().",
		referenceNamed(t, ff, "localHelper").Descriptor.String())
	// A name nothing here knows: the current namespace, which is what PHP tries
	// first. It matches nothing, which is an unresolved reference and not a wrong
	// edge.
	assert.Equal(t, prefix+" App/mysteryHelper().",
		referenceNamed(t, ff, "mysteryHelper").Descriptor.String())

	// No fallback for a class name: unqualified means the current namespace, and
	// only the fully qualified spelling reaches the language's.
	assertHasReference(t, ff, prefix+" App/Exception#")
	assertHasReference(t, ff, "scip-php composer php . Exception#")
}

// TestUseTablesAreKeptApartByNameSpace is why there are three alias tables and
// not one. PHP keeps class, function and constant imports in separate name
// spaces, so `use A\thing;` and `use function B\thing;` may both appear in one
// file and bind different symbols — and a call site and a type position have to
// ask different tables.
func TestUseTablesAreKeptApartByNameSpace(t *testing.T) {
	ff := parse(t, "src/Tables/Tables.php", `<?php

namespace App;

use Vendor\Thing;
use function Other\thing;
use const Third\THING;

class Tables
{
    public function run(): void
    {
        $t = new Thing();
        thing();
        echo THING;
    }
}
`)

	assertHasReference(t, ff, prefix+" Vendor/Thing#")
	assertHasReference(t, ff, prefix+" Other/thing().")
	// The constant's *import* occurrence. A bare `THING` in an expression is an
	// unadorned `(name)` node that this stanza deliberately does not capture, so
	// the read itself is not recorded; the declaration that binds it is.
	assertHasReference(t, ff, prefix+" Third/THING.")
}

// TestAGroupUseBindsEveryName is the fifth spelling of `use`, and the only one
// whose prefix is written outside the clause that consumes it.
func TestAGroupUseBindsEveryName(t *testing.T) {
	ff := parse(t, "src/Group/Group.php", `<?php

namespace App;

use Vendor\Lib\{Alpha, Beta as B};

class Group
{
    public function run(): void
    {
        $a = new Alpha();
        $b = new B();
    }
}
`)

	assert.Equal(t, prefix+" Vendor/Lib/Alpha#", referenceNamed(t, ff, "Alpha").Descriptor.String())
	assert.Equal(t, prefix+" Vendor/Lib/Beta#", referenceNamed(t, ff, "Beta").Descriptor.String())
	assert.Equal(t, prefix+" Vendor/Lib/Beta#", referenceNamed(t, ff, "B").Descriptor.String())

	// One package reference for the shared prefix, and exactly one: it is written
	// once, so emitting it per clause would describe the same bytes twice.
	pkgs := 0
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference && o.SymbolKind == facts.KindPackage {
			assert.Equal(t, prefix+" Vendor/Lib/", o.Descriptor.String())
			pkgs++
		}
	}
	assert.Equal(t, 1, pkgs)
}

// ------------------------------------------------------------------ namespace --

// TestATopLevelUseIsAPackageReference is what makes link's `imports` derivation
// fire for PHP. The reference's descriptor is the imported namespace, which is
// byte-identical to what the declaring file's own `namespace` statement renders —
// and that identity is the whole of the join (§7).
func TestATopLevelUseIsAPackageReference(t *testing.T) {
	importer := parse(t, "src/App/Program.php", `<?php

namespace Com\Example\App;

use Com\Example\Greeting\Greeter;

class Program
{
}
`)
	declarer := parse(t, "src/Greeter/Greeter.php", `<?php

namespace Com\Example\Greeting;

class Greeter
{
}
`)

	ref := referenceNamed(t, importer, "Greeting")
	assert.Equal(t, facts.KindPackage, ref.SymbolKind)
	assert.Equal(t, prefix+" Com/Example/Greeting/", ref.Descriptor.String())
	assertHasDefinition(t, declarer, ref.Descriptor.String())
}

// TestTheNamespaceIsWrittenDownAndNotDerivedFromThePath is the decision every
// namespaced stanza has had to make, and PHP is the second language (after Java
// and C#) where the source states it outright — which is why `composer.json`'s
// `autoload.psr-4` map buys nothing.
//
// PSR-4 maps a namespace prefix to a directory, but it governs where the
// *autoloader looks for a file*, not what the symbols in it are called: a file
// whose `namespace` disagrees with the map is a file the autoloader cannot find,
// and its classes are still named by the declaration. So the same source at three
// paths renders one descriptor, and reading the map would be redundant at best.
func TestTheNamespaceIsWrittenDownAndNotDerivedFromThePath(t *testing.T) {
	const src = `<?php

namespace Com\Example\Greeting;

class Greeter
{
    public function greet(): string
    {
        return '';
    }
}
`
	for _, path := range []string{
		"src/Greeter/Greeter.php",
		"lib/deeply/nested/Greeter.php",
		"Greeter.php",
	} {
		ff := parse(t, path, src)
		assertHasDefinition(t, ff, prefix+" Com/Example/Greeting/Greeter#greet().")
		assertHasDefinition(t, ff, prefix+" Com/Example/Greeting/")
	}
}

// TestBlockNamespacesAreEachTheirOwn is the C# block-namespace case two languages
// on. A file may hold several `namespace … { }` blocks side by side, so the
// namespace is a property of a declaration and not of the file — and one of them
// may be the global namespace, written `namespace { … }` with no name at all.
func TestBlockNamespacesAreEachTheirOwn(t *testing.T) {
	ff := parse(t, "src/Blocks/Blocks.php", `<?php

namespace First {
    class Alpha
    {
        public function m(): void
        {
        }
    }
}

namespace Second {
    class Beta
    {
        public function m(): void
        {
        }
    }
}
`)

	assertHasDefinition(t, ff, prefix+" First/")
	assertHasDefinition(t, ff, prefix+" Second/")
	assertHasDefinition(t, ff, prefix+" First/Alpha#m().")
	assertHasDefinition(t, ff, prefix+" Second/Beta#m().")
	assertNoDefinition(t, ff, prefix+" First/Beta#m().")
}

// TestAFileWithNoNamespaceDeclaresTheGlobalOne is what every stanza before this
// one had to do for *all* of its files: there is no identifier to point at, so
// the occurrence is a zero-width point at byte 0. A legacy PHP file is exactly
// this, and it is the majority of PHP that predates namespaces.
func TestAFileWithNoNamespaceDeclaresTheGlobalOne(t *testing.T) {
	ff := parse(t, "legacy/util.php", `<?php

class Util
{
    public function run(): void
    {
    }
}
`)

	assertHasDefinition(t, ff, prefix)
	assertHasDefinition(t, ff, prefix+" Util#run().")

	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition && o.SymbolKind == facts.KindPackage {
			assert.Equal(t, 0, o.RangeStart)
			assert.Equal(t, 0, o.RangeEnd)
		}
	}
}

// ----------------------------------------------------------------- receivers --

// TestAReceiverTypeIsReadOffADeclarationAndNeverInferred is what makes a
// cross-file call resolve at all, and PHP writes the type down more often than
// Ruby does: a parameter, a property and a promoted constructor parameter all
// carry declared types, and a local's type comes from an initialising `new`.
//
// `Greeter::build()` and every other factory are deliberately not read. A name is
// not a contract, and a wrong answer would name a member of a type that does not
// have it — which is a descriptor pointing at the wrong symbol rather than at no
// symbol.
func TestAReceiverTypeIsReadOffADeclarationAndNeverInferred(t *testing.T) {
	ff := parse(t, "src/Recv/Recv.php", `<?php

namespace App;

class Recv
{
    private Greeter $held;

    public function __construct(private Greeter $promoted)
    {
    }

    public function run(Greeter $param): void
    {
        $made = new Greeter();
        $made->greet();
        $param->greet();
        $this->held->greet();
        $this->promoted->greet();

        $guessed = Greeter::build();
        $guessed->greet();
    }
}
`)

	resolved, unresolved := 0, 0
	for _, o := range ff.Occurrences {
		if o.Role != facts.RoleReference || o.Name != "greet" {
			continue
		}
		switch o.Descriptor.String() {
		case prefix + " App/Greeter#greet().":
			resolved++
		case prefix + " .#greet().":
			unresolved++
		default:
			t.Fatalf("unexpected greet descriptor %q", o.Descriptor.String())
		}
	}
	assert.Equal(t, 4, resolved, "a `new`, a parameter and two typed properties all name their type")
	assert.Equal(t, 1, unresolved, "a factory's return type is not written down, so it stays SCIP's \".\"")
}

// TestSelfStaticAndParentAreResolved covers the three keywords PHP writes where a
// class name goes. `self` and `static` are one answer here — the difference
// between them is late static binding, which is which class the call *dispatches*
// to at runtime, and a descriptor names a declaration site. `parent` is read off
// the `extends` clause, which PHP writes separately from `implements`, so unlike
// C# there is nothing to guess.
func TestSelfStaticAndParentAreResolved(t *testing.T) {
	ff := parse(t, "src/Rel/Child.php", `<?php

namespace App;

class Child extends Base implements Marker
{
    public const K = 1;

    public function run(): void
    {
        self::helper();
        static::helper();
        parent::helper();
        echo self::K;
    }
}
`)

	helpers := map[string]int{}
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference && o.Name == "helper" {
			helpers[o.Descriptor.String()]++
		}
	}
	assert.Equal(t, map[string]int{
		prefix + " App/Child#helper().": 2,
		prefix + " App/Base#helper().":  1,
	}, helpers, "`self` and `static` name the declaring class; `parent` names what it extends")

	// `implements Marker` is not the parent, which is the case C#'s single base
	// list forces it to guess at and PHP's two clauses settle.
	assertHasReference(t, ff, prefix+" App/Marker#")
	assertHasReference(t, ff, prefix+" App/Child#K.")
}

// TestAPromotedConstructorParameterIsAField is the promotion java.go applies to a
// record component and cs.go to a positional one, for the third time and the same
// reason: `$this->name` is what reaches it, so a parameter descriptor hanging off
// the constructor is not what any reference site writes.
func TestAPromotedConstructorParameterIsAField(t *testing.T) {
	ff := parse(t, "src/Promo/Promo.php", `<?php

namespace App;

class Promo
{
    public function __construct(private readonly string $name)
    {
    }

    public function greet(): string
    {
        return $this->name;
    }
}
`)

	assertHasDefinition(t, ff, prefix+" App/Promo#name.")
	assertNoDefinition(t, ff, prefix+" App/Promo#__construct().(name)")
	assert.Equal(t, facts.KindField, definitionKinds(ff)[prefix+" App/Promo#name."])
	assertHasReference(t, ff, prefix+" App/Promo#name.")
}

// TestTheSigilIsNotPartOfTheName is the naming decision that Ruby's inverts, and
// the two are right for the same test.
//
// Ruby keeps the `@` of `@name` because `@name` is what is written at every site
// the member appears. PHP does not: the property is declared `public string $name`
// and read `$this->name`, so the `$` appears on one side and not the other — a
// descriptor carrying it would be one the two sides could not agree on. A static
// property proves the point from the other direction: `self::$count` and
// `$obj->count` reach the same member and are written with and without it.
func TestTheSigilIsNotPartOfTheName(t *testing.T) {
	ff := parse(t, "src/Sigil/Sigil.php", `<?php

namespace App;

class Sigil
{
    public string $name = '';

    public function read(): string
    {
        return $this->name;
    }
}
`)

	for _, o := range ff.Occurrences {
		assert.NotContains(t, o.Descriptor.Suffix, "$", "%s carries a `$`", o.Descriptor.String())
		assert.NotContains(t, o.Name, "$", "the name column carries a `$`: %q", o.Name)
	}
	assertHasDefinition(t, ff, prefix+" App/Sigil#name.")
	assertHasReference(t, ff, prefix+" App/Sigil#name.")
}

// ---------------------------------------------------------------- mechanics ---

// TestScopesAreFunctionsAndTypesAndNotBlocks is the restraint py.go and rb.go
// showed, and PHP needs it for the same reason both did: PHP has **no block
// scoping at all**, so a variable assigned inside an `if` or a `foreach` is
// visible after it in the enclosing function. Capturing a block as a scope would
// model something the language does not have, and same-file reference resolution
// asks which scope a definition was declared in.
func TestScopesAreFunctionsAndTypesAndNotBlocks(t *testing.T) {
	ff := parse(t, "src/Scopes/Scopes.php", `<?php

namespace App;

class Scopes
{
    public function run(array $xs): int
    {
        $total = 0;
        foreach ($xs as $x) {
            if ($x > 0) {
                $seen = $x;
                $total = $total + $seen;
            }
        }

        return $total;
    }
}
`)

	kinds := map[string]int{}
	for _, s := range ff.Scopes {
		kinds[s.Kind]++
	}
	assert.Equal(t, map[string]int{
		facts.ScopeFile:     1,
		facts.ScopeType:     1,
		facts.ScopeFunction: 1,
	}, kinds, "no block scopes: PHP has none")

	// `$seen`, assigned two blocks deep, is a local of the method — which is what
	// the language says and what the descriptor has to say too.
	assertHasDefinition(t, ff, prefix+" App/Scopes#run().seen.")
	assertHasDefinition(t, ff, prefix+" App/Scopes#run().total.")
}

// TestSameFileReferencesAreResolvedDuringExtraction is §4.3's split: a reference
// whose target definition is in the same CST gets an extracted
// `references_local` edge, and one that needs another file is emitted unresolved
// for the link pass.
func TestSameFileReferencesAreResolvedDuringExtraction(t *testing.T) {
	ff := parse(t, "src/Local/Local.php", `<?php

namespace App;

class Local
{
    public function greet(): string
    {
        return $this->build();
    }

    public function build(): string
    {
        return '';
    }
}
`)

	var local int
	for _, e := range ff.Edges {
		if e.Kind == facts.EdgeReferencesLocal {
			local++
		}
	}
	assert.Positive(t, local)

	target := occurrenceWithDescriptor(t, ff, facts.RoleDefinition, prefix+" App/Local#build().")
	source := occurrenceWithDescriptor(t, ff, facts.RoleReference, prefix+" App/Local#build().")
	assert.Contains(t, ff.Edges, facts.Edge{
		Kind:   facts.EdgeReferencesLocal,
		Source: facts.OccurrenceRef(source.ID),
		Target: facts.OccurrenceRef(target.ID),
	})
}

// TestAnUnparseableFileYieldsParseErrorAndNotAPanic is the §5 poison-file
// contract: Parse returns no error, so a failure travels in ParseError with the
// File row still populated and the caller decides whether to skip the file.
func TestAnUnparseableFileYieldsFactsWithoutPanicking(t *testing.T) {
	c := testCoord(t)
	src := []byte("<?php\n\nclass {{{ ->->-> function ((( \n")
	require.NotPanics(t, func() {
		ff := php.New().Parse(filepath.Join(c.Root, "broken.php"), src, c)
		assert.Equal(t, php.Lang, ff.File.Lang)
		assert.Equal(t, c, ff.File.Coord)
	})
}

// TestTheFixtureExtractsTheDescriptorsTheFeatureAsserts pins the corpus the
// integration suite indexes, so a change in this stanza that would break
// features/index_php.feature fails here first — in two seconds rather than in a
// container.
func TestTheFixtureExtractsTheDescriptorsTheFeatureAsserts(t *testing.T) {
	c := testCoord(t)
	p := php.New()

	read := func(rel string) facts.FileFacts {
		t.Helper()
		path := filepath.Join(c.Root, filepath.FromSlash(rel))
		src, err := os.ReadFile(path) //nolint:gosec // the fixture is in this package.
		require.NoError(t, err)
		ff := p.Parse(path, src, c)
		require.Empty(t, ff.ParseError)
		return ff
	}

	greeter := read("src/Greeter/Greeter.php")
	program := read("src/App/Program.php")

	assertHasDefinition(t, greeter, prefix+" Com/Example/Greeting/Greeter#")
	assertHasDefinition(t, greeter, prefix+" Com/Example/Greeting/Greeter#greet().")
	assertHasDefinition(t, greeter, prefix+" Com/Example/Greeting/Loudly#shout().")
	assertHasReference(t, greeter, prefix+" Com/Example/Greeting/Speaker#")

	assertHasDefinition(t, program, prefix+" Com/Example/App/Program#main().")
	// The cross-file join, byte for byte on both sides.
	assertHasReference(t, program, prefix+" Com/Example/Greeting/Greeter#greet().")
	assertHasReference(t, program, prefix+" Com/Example/Greeting/")
}

// ------------------------------------------------------------------- helpers --

func definitionDescriptors(ff facts.FileFacts) []string {
	var out []string
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition {
			out = append(out, o.Descriptor.String())
		}
	}
	sort.Strings(out)
	return out
}

func definitionKinds(ff facts.FileFacts) map[string]string {
	out := map[string]string{}
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition {
			out[o.Descriptor.String()] = o.SymbolKind
		}
	}
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

func assertNoDefinition(t *testing.T, ff facts.FileFacts, descriptor string) {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition && o.Descriptor.String() == descriptor {
			t.Fatalf("unexpected definition with descriptor %q", descriptor)
		}
	}
}

func assertHasReference(t *testing.T, ff facts.FileFacts, descriptor string) {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference && o.Descriptor.String() == descriptor {
			return
		}
	}
	var refs []string
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference {
			refs = append(refs, o.Descriptor.String())
		}
	}
	sort.Strings(refs)
	t.Fatalf("no reference with descriptor %q in %v", descriptor, refs)
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

func occurrenceWithDescriptor(t *testing.T, ff facts.FileFacts, role facts.Role, descriptor string) facts.Occurrence {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == role && o.Descriptor.String() == descriptor {
			return o
		}
	}
	t.Fatalf("no %s with descriptor %q", role, descriptor)
	return facts.Occurrence{}
}
