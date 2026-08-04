package swift_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/extract/swift"
	"github.com/gaarutyunov/codiq/facts"
)

// testCoord is the coordinate the batch would hand the parser for a file in the
// SwiftPM package `greeter`, rooted at the fixture directory.
func testCoord(t *testing.T) coord.Coord {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "greeter"))
	require.NoError(t, err)
	coords, err := coord.Resolve(root)
	require.NoError(t, err)
	c := coords.For("X.swift")
	require.Equal(t, coord.SwiftScheme, c.Scheme, "the fixture must resolve through the SwiftPM resolver")
	return c
}

// parse parses src as the file at name, which is interpreted relative to the
// package root. Unlike every other stanza's, the path here really does name the
// file's namespace: Swift declares none, so the module is derived from
// SwiftPM's `Sources/<Module>/` rule and the tests below are largely about that.
func parse(t *testing.T, name, src string) facts.FileFacts {
	t.Helper()
	c := testCoord(t)
	ff := swift.New().Parse(filepath.Join(c.Root, filepath.FromSlash(name)), []byte(src), c)
	require.Empty(t, ff.ParseError)
	return ff
}

const prefix = "scip-swift swiftpm greeter ."

// stdlib is the coordinate references to the Swift standard library carry:
// foreign, so nothing under it can ever match a definition this index owns.
const stdlib = "scip-swift swiftpm Swift ."

// --------------------------------------------------------------- namespaces --

// The central claim of this stanza, and the one thing about Swift that no
// earlier language forced: the namespace is the SwiftPM target, the target is
// declared in a file §2.5 forbids reading, and the path rule SwiftPM *enforces*
// is what stands in for it.
func TestTheModuleComesFromTheSourcesPathRule(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "the layout SwiftPM requires of a target with no explicit path",
			path: "Sources/Greeter/Greeter.swift",
			want: prefix + " Greeter/Greeter#",
		},
		{
			// A Swift module is flat: a subdirectory is a file-system
			// convenience and creates no namespace, which is why every segment
			// after the second is dropped.
			name: "a subdirectory of a target is still that target",
			path: "Sources/Greeter/Internal/Deep/Greeter.swift",
			want: prefix + " Greeter/Greeter#",
		},
		{
			name: "a test target is a module like any other",
			path: "Tests/GreeterTests/Greeter.swift",
			want: prefix + " GreeterTests/Greeter#",
		},
		{
			name: "the lowercase source roots SwiftPM also accepts",
			path: "src/Greeter/Greeter.swift",
			want: prefix + " Greeter/Greeter#",
		},
		{
			// Not a target layout at all, so the top-level directory is the
			// namespace. It separates, which is all a namespace has to do.
			name: "a file outside any source root falls back to its own directory",
			path: "swift/Greeter.swift",
			want: prefix + " swift/Greeter#",
		},
		{
			name: "a file at the package root is in the unnamed root module",
			path: "Greeter.swift",
			want: prefix + " Greeter#",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertHasDefinition(t, parse(t, tt.path, "struct Greeter {}\n"), tt.want)
		})
	}
}

// Two files of one module derive the same namespace from two different paths,
// which is the whole reason a module namespace is worth having: a Swift module
// is multi-file and flat, and a reference from one of its files to another has
// nothing but this to join on.
func TestTwoFilesOfOneModuleShareItsNamespace(t *testing.T) {
	one := moduleDefinition(t, parse(t, "Sources/Greeter/A.swift", "struct A {}\n"))
	two := moduleDefinition(t, parse(t, "Sources/Greeter/Deep/B.swift", "struct B {}\n"))
	assert.Equal(t, one.Descriptor.String(), two.Descriptor.String())
	assert.Equal(t, prefix+" Greeter/", one.Descriptor.String())
}

// Two *modules* do not, which is the phantom this namespace exists to prevent:
// a `Logger` in `Core` and a `Logger` in `Utils` are two types, and one
// descriptor for both is an edge the link pass would materialize as fact.
func TestTwoModulesDoNotCollide(t *testing.T) {
	core := parse(t, "Sources/Core/Logger.swift", "struct Logger {}\n")
	utils := parse(t, "Sources/Utils/Logger.swift", "struct Logger {}\n")
	assertHasDefinition(t, core, prefix+" Core/Logger#")
	assertHasDefinition(t, utils, prefix+" Utils/Logger#")
	assert.NotEqual(t, definitionDescriptors(core), definitionDescriptors(utils))
}

// --------------------------------------------------------------- the manifest --

// `Package.swift` is a manifest and a compilation unit at once — the first file
// in this repository that is both — and what keeps that sound is that its
// declarations hang off a container named for the file rather than off a module
// it is not in.
func TestAManifestDeclaresIntoItsOwnContainer(t *testing.T) {
	ff := parse(t, "Package.swift", "let package = Package(name: \"greeter\")\n")
	assertHasDefinition(t, ff, prefix+" Package_swift#package.")

	for _, o := range ff.Occurrences {
		assert.NotEqual(t, facts.KindPackage, o.SymbolKind,
			"a manifest is in no module, so it defines and references none of its own")
	}
}

// The claim the container exists for. A repository with a root manifest and a
// second one under `examples/` declares `package` twice, and the two are
// unrelated: SwiftPM compiles each manifest on its own.
func TestTwoManifestsDeclareTwoSymbols(t *testing.T) {
	src := "let package = Package(name: \"greeter\")\n"
	root := parse(t, "Package.swift", src)
	nested := parse(t, "examples/demo/Package.swift", src)
	assert.NotEqual(t, definitionDescriptors(root), definitionDescriptors(nested),
		"one descriptor for two manifests is an edge that does not exist")
}

// A tools-version-specific manifest is the same file under another name, and
// SwiftPM reads it in preference to `Package.swift`.
func TestAVersionedManifestIsAManifest(t *testing.T) {
	ff := parse(t, "Package@swift-5.9.swift", "let package = Package(name: \"greeter\")\n")
	assertHasDefinition(t, ff, prefix+" Package_swift_5_9_swift#package.")
}

// A source file that merely *looks* like a manifest is not one: the name is the
// whole of the rule, and `Packages.swift` in a module is ordinary Swift.
func TestASourceFileHasNoManifestContainer(t *testing.T) {
	ff := parse(t, "Sources/Greeter/Packages.swift", "let package = 1\n")
	assertHasDefinition(t, ff, prefix+" Greeter/package.")
}

// --------------------------------------------------------------- definitions --

func TestParseDefinitionDescriptors(t *testing.T) {
	ff := parse(t, "Sources/Shapes/Shapes.swift", `
let level = 1

typealias Label = String

protocol Speaker {
    func speak() -> String
}

enum Mood {
    case happy
    case sad
}

struct Point {
    var x: Int
    var y: Int
}

enum Registry {
    static func register(name: String) {}
}

final class Shapes: Speaker {
    static let fallback = "none"

    private var label: String

    func speak() -> String {
        let local = label
        return local
    }

    struct Nested {
        var depth: Int
    }
}
`)
	want := []string{
		prefix + " Shapes/",
		prefix + " Shapes/Label#",
		prefix + " Shapes/Mood#",
		prefix + " Shapes/Mood#happy.",
		prefix + " Shapes/Mood#sad.",
		prefix + " Shapes/Point#",
		prefix + " Shapes/Point#x.",
		prefix + " Shapes/Point#y.",
		prefix + " Shapes/Registry#",
		prefix + " Shapes/Registry#register().",
		prefix + " Shapes/Registry#register().(name)",
		prefix + " Shapes/Shapes#",
		prefix + " Shapes/Shapes#Nested#",
		prefix + " Shapes/Shapes#Nested#depth.",
		prefix + " Shapes/Shapes#fallback.",
		prefix + " Shapes/Shapes#label.",
		prefix + " Shapes/Shapes#speak().",
		prefix + " Shapes/Shapes#speak().local.",
		prefix + " Shapes/Speaker#",
		prefix + " Shapes/Speaker#speak().",
		prefix + " Shapes/level.",
	}
	assert.Equal(t, want, definitionDescriptors(ff))
}

func TestParseDefinitionKinds(t *testing.T) {
	ff := parse(t, "Sources/Shapes/Kinds.swift", `
let topLevel = 1

protocol Speaker {
    func speak() -> String
}

enum Mood {
    case happy
    case tinted(shade: Int)
}

struct Point {
    var x: Int

    func moved(by delta: Int) -> Point {
        let next = x + delta
        return Point(x: next)
    }
}

func free() {}
`)
	defs := definitionsByName(ff)
	tests := map[string]string{
		"topLevel": facts.KindConstant,
		"Speaker":  facts.KindInterface,
		"speak":    facts.KindMethod,
		"Mood":     facts.KindType,
		"happy":    facts.KindField,
		// An enum case with associated values is a function from them to the
		// enum, and every use site writes it as a call.
		"tinted": facts.KindMethod,
		"Point":  facts.KindType,
		"x":      facts.KindField,
		"moved":  facts.KindMethod,
		"delta":  facts.KindParameter,
		// A `let` inside a body is an immutable local, not a constant of the
		// module: it is the reading kotlin.go gives a local `val`.
		"next": facts.KindVariable,
		"free": facts.KindFunction,
	}
	for name, kind := range tests {
		require.Contains(t, defs, name)
		assert.Equal(t, kind, defs[name].SymbolKind, "the kind of %q", name)
	}
}

// The enum-case decision, stated as the two descriptors it produces. Each
// matches the only spelling its own use sites can have — a plain case is read,
// an associated-value case is called — and the reference side renders the same
// string, which is what makes both joinable.
func TestAnEnumCaseIsDescriptoredTheWayItIsWritten(t *testing.T) {
	ff := parse(t, "Sources/Shapes/Shape.swift", `
enum Shape {
    case square
    case circle(radius: Double)
}

func build() -> Shape {
    let a = Shape.square
    let b = Shape.circle(radius: 2.0)
    _ = a
    return b
}
`)
	assertHasDefinition(t, ff, prefix+" Shapes/Shape#square.")
	assertHasDefinition(t, ff, prefix+" Shapes/Shape#circle().")
	assertResolvesLocally(t, ff, "square", prefix+" Shapes/Shape#square.")
	assertResolvesLocally(t, ff, "circle", prefix+" Shapes/Shape#circle().")

	// The associated value's label names an argument slot, not a symbol.
	for _, o := range ff.Occurrences {
		assert.NotEqual(t, "radius", o.Name, "an argument label is not an occurrence")
	}
}

// An initialiser writes no name in either of its spellings, so it is not a
// definition — and it needs none. Swift has no `new`, so `Greeter(name:)` is a
// call whose callee is the *type*, which resolves to a definition that exists
// and that another file renders identically.
func TestInitialisersAreNotDefinitions(t *testing.T) {
	ff := parse(t, "Sources/Greeter/Greeter.swift", `
struct Greeter {
    var name: String

    init(name: String) {
        self.name = name
    }

    init() {
        self.init(name: "world")
    }
}
`)
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition {
			assert.NotEqual(t, "init", o.Name, "an initialiser declares no identifier")
		}
	}
	assertHasDefinition(t, ff, prefix+" Greeter/Greeter#")
}

// ---------------------------------------------------------------- extensions --

// The extension decision, and the one place this stanza deliberately answers
// differently from extract/kotlin. A Swift extension really does add a member to
// the type it extends — `s.shout()` reaches it from anywhere in the module — so
// the member is descriptored under that type, which is the descriptor a call
// site renders once it has resolved its receiver.
func TestAnExtensionsMembersBelongToTheTypeItExtends(t *testing.T) {
	ff := parse(t, "Sources/Greeter/Shout.swift", `
extension String {
    func shout() -> String {
        return self.uppercased()
    }
}
`)
	assertHasDefinition(t, ff, stdlib+" String#shout().")
}

// And an extension defines nothing of its own. It writes no name — the
// `user_type` in its header is a type some other file declares — so capturing it
// as a definition would list a file that merely extends `String` in that type's
// `definedIn`.
func TestAnExtensionDefinesNoType(t *testing.T) {
	ff := parse(t, "Sources/Greeter/Shout.swift", "extension String {\n    func shout() {}\n}\n")
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition {
			assert.NotEqual(t, "String", o.Name, "an extension declares no type")
		}
	}
}

// An extension of this module's own type lands in this module's namespace, which
// is what makes a type spread across files navigable: the extension and the
// declaration derive the same namespace from the same path rule.
func TestAnExtensionOfALocalTypeStaysInTheModule(t *testing.T) {
	ff := parse(t, "Sources/Greeter/Extras.swift", `
extension Greeter {
    func loud() -> String {
        return self.greet()
    }
}
`)
	assertHasDefinition(t, ff, prefix+" Greeter/Greeter#loud().")
	assert.Contains(t, referenceDescriptors(ff), prefix+" Greeter/Greeter#greet().",
		"`self` inside an extension is the extended type")
}

// A protocol extension is Swift's default implementation, and it renders the
// requirement's own descriptor. Two definitions, one string — deliberately: a
// call site writes `speaker.greet()` and names one thing, and which body runs is
// a dispatch-time decision this index could not carry.
func TestAProtocolExtensionSharesTheRequirementsDescriptor(t *testing.T) {
	ff := parse(t, "Sources/Greeter/Speaker.swift", `
protocol Speaker {
    func greet() -> String
}

extension Speaker {
    func greet() -> String {
        return "default"
    }
}
`)
	assert.Equal(t, 2, count(definitionDescriptors(ff), prefix+" Greeter/Speaker#greet()."),
		"the requirement and its default implementation are both answers to `where is this`")
}

// ------------------------------------------------------------------ imports ---

// A module import binds nothing nameable — that is Swift's whole-module wildcard
// — but it names the module, and the occurrence it emits is what link's
// `imports` derivation joins against the other module's own files.
func TestAModuleImportNamesTheModule(t *testing.T) {
	ff := parse(t, "Sources/App/Main.swift", "import Greeter\n")
	ref := moduleReference(t, ff)
	assert.Equal(t, prefix+" Greeter/", ref.Descriptor.String())
	assert.Equal(t, facts.KindPackage, ref.SymbolKind)

	// Byte-identical to what a file of that module derives for itself, which is
	// the whole of what makes the edge derivable.
	assert.Equal(t, ref.Descriptor.String(),
		moduleDefinition(t, parse(t, "Sources/Greeter/Greeter.swift", "struct Greeter {}\n")).Descriptor.String())
}

// A platform module is foreign: it belongs to no artifact this index owns, so
// nothing under it can ever match a definition here.
func TestAPlatformImportIsForeign(t *testing.T) {
	ff := parse(t, "Sources/App/Main.swift", "import Foundation\n")
	assert.Equal(t, "scip-swift swiftpm Foundation .", moduleReference(t, ff).Descriptor.String())
}

// The one import form that binds a name, and the escape hatch from the wildcard:
// a declaration import says which module a name came from, so the reference
// resolves across modules where a bare `import` cannot.
func TestADeclarationImportBindsAName(t *testing.T) {
	ff := parse(t, "Sources/App/Main.swift", `
import struct Greeter.Greeter

func build() -> Greeter {
    return Greeter(name: "world")
}
`)
	refs := referenceDescriptors(ff)
	assert.Equal(t, 3, count(refs, prefix+" Greeter/Greeter#"),
		"the import, the return type and the construction all name the imported type")
}

// A module-qualified reference is the other spelling that survives the wildcard,
// and it is the one Swift offers when two imports collide.
func TestAQualifiedReferenceReachesAnotherModule(t *testing.T) {
	ff := parse(t, "Sources/App/Main.swift", `
import Greeter

func build() -> Greeter.Greeter {
    return Greeter.Greeter(name: "world")
}
`)
	assert.Contains(t, referenceDescriptors(ff), prefix+" Greeter/Greeter#")
}

// And the cost, stated as a test so that nobody mistakes it for a bug. A bare
// name brought in by a wildcard import cannot be attributed to the module it
// came from, so it lands in *this* module's namespace — a descriptor that
// matches nothing rather than one that matches the wrong thing.
func TestAWildcardImportDoesNotResolveASimpleName(t *testing.T) {
	ff := parse(t, "Sources/App/Main.swift", `
import Greeter

func build() -> Greeter {
    return Greeter(name: "world")
}
`)
	refs := referenceDescriptors(ff)
	assert.Contains(t, refs, prefix+" App/Greeter#",
		"an unattributable name is this module's, and matches nothing")
	assert.NotContains(t, refs, prefix+" Greeter/Greeter#",
		"guessing the module would be a phantom edge, which is worse than a missing one")
}

// Every identifier an import consumed is claimed, so the reference patterns —
// which match inside an import as readily as anywhere else — emit nothing over
// the same bytes.
func TestImportsClaimTheirIdentifiers(t *testing.T) {
	ff := parse(t, "Sources/App/Main.swift", "import struct Greeter.Greeter\n")
	assert.Len(t, ff.Occurrences, 3,
		"the module definition, the module reference and the type the import bound")
}

// ---------------------------------------------------------------- references --

// The grammar parses a bare identifier in receiver position as a `user_type`
// whatever it is, so position and not node type has to decide what `g` is. Taken
// at face value this file would emit `g` as a type reference.
func TestAReceiverIsNotAType(t *testing.T) {
	ff := parse(t, "Sources/Greeter/Use.swift", `
struct Greeter {
    var name: String

    func greet() -> String { return name }
}

func use() -> String {
    let g = Greeter(name: "world")
    return g.greet()
}
`)
	assertResolvesLocally(t, ff, "g", prefix+" Greeter/use().g.")
	assertResolvesLocally(t, ff, "greet", prefix+" Greeter/Greeter#greet().")
}

// A construction is the one expression this stanza reads a type back out of, and
// it is syntax rather than inference: Swift has no `new`, so the callee *is* the
// type. Both spellings — with and without written generic arguments — are read.
func TestAConstructionNamesTheType(t *testing.T) {
	ff := parse(t, "Sources/Greeter/Box.swift", `
struct Box {
    func open() -> Int { return 1 }
}

func plain() -> Int {
    let b = Box()
    return b.open()
}
`)
	assertResolvesLocally(t, ff, "open", prefix+" Greeter/Box#open().")
}

// A value whose type is unknowable file-locally takes SCIP's "." where the type
// would be, so the descriptor cannot false-match a real definition.
func TestAnUnknownReceiverLandsOnADot(t *testing.T) {
	ff := parse(t, "Sources/Greeter/Use.swift", `
func use(compute: () -> Int) -> Int {
    let x = compute()
    return x.magnitude
}
`)
	assert.Contains(t, referenceDescriptors(ff), prefix+" Greeter/.#magnitude.")
}

func TestOptionalChainingResolvesLikeAnOrdinaryCall(t *testing.T) {
	ff := parse(t, "Sources/Greeter/Use.swift", `
struct Greeter {
    func greet() -> String { return "" }
}

func use(g: Greeter?) -> String? {
    return g?.greet()
}
`)
	assertResolvesLocally(t, ff, "greet", prefix+" Greeter/Greeter#greet().")
}

func TestSelfAndSuperNameTheTypesTheyMean(t *testing.T) {
	ff := parse(t, "Sources/Greeter/Derived.swift", `
class Base {
    func run() {}
}

final class Derived: Base {
    func run2() {
        super.run()
        self.run()
    }
}
`)
	refs := referenceDescriptors(ff)
	assert.Equal(t, 1, count(refs, prefix+" Greeter/Base#run()."), "`super` is the first inheritance entry")
	assert.Contains(t, refs, prefix+" Greeter/Derived#run().", "`self` is the enclosing type")
}

// `newValue` and `$0` are the two bindings Swift declares nowhere. Without a
// rule each would fall through to "a lowercase name that resolves to nothing",
// which renders a property of this module: a descriptor that matches nothing,
// but that says something false about what they are.
func TestTheTwoBindingsNothingDeclares(t *testing.T) {
	ff := parse(t, "Sources/Greeter/Observed.swift", `
struct Observed {
    var stored: Int = 0 {
        didSet {
            print(oldValue)
        }
    }

    var computed: Int {
        get { return stored }
        set { stored = newValue }
    }

    func mapped(xs: [Int]) -> [Int] {
        return xs.map { $0 + 1 }
    }
}
`)
	refs := referenceDescriptors(ff)
	assert.NotContains(t, refs, prefix+" Greeter/newValue.")
	assert.NotContains(t, refs, prefix+" Greeter/oldValue.")
	assert.NotContains(t, refs, prefix+" Greeter/$0.")
}

// An argument label is part of a callee's declared signature, which the
// descriptor does not carry, so an occurrence for one would name nothing.
func TestArgumentLabelsAreNotOccurrences(t *testing.T) {
	ff := parse(t, "Sources/Greeter/Call.swift", `
func move(to point: Int, from origin: Int) -> Int {
    return point - origin
}

func use() -> Int {
    return move(to: 1, from: 0)
}
`)
	for _, o := range ff.Occurrences {
		assert.NotContains(t, []string{"to", "from"}, o.Name, "a label names a slot, not a symbol")
	}
	assertHasDefinition(t, ff, prefix+" Greeter/move().(point)")
	assertResolvesLocally(t, ff, "move", prefix+" Greeter/move().")
}

// Swift overloads on the argument labels as well as on the parameter types, and
// the callable component carries neither: `move(to:)` and `move(from:)` render
// one descriptor. java.go argues the trade and it is unchanged — a descriptor
// both sides compute the same way beats a precise one only one side can.
func TestOverloadsRenderOneDescriptor(t *testing.T) {
	ff := parse(t, "Sources/Greeter/Overload.swift", `
func move(to point: Int) -> Int { return point }
func move(from origin: Int) -> Int { return origin }
`)
	assert.Equal(t, 2, count(definitionDescriptors(ff), prefix+" Greeter/move()."))
}

// ------------------------------------------------------------------- scopes ---

func TestScopesAreSwiftsOwn(t *testing.T) {
	ff := parse(t, "Sources/Greeter/Scopes.swift", `
struct Scopes {
    func run(xs: [Int]) -> Int {
        var total = 0
        for x in xs {
            total += x
        }
        while total > 0 {
            total -= 1
        }
        do {
            total += 1
        } catch let error {
            print(error)
        }
        return xs.reduce(0) { acc, next in acc + next }
    }
}
`)
	kinds := map[string]int{}
	for _, s := range ff.Scopes {
		kinds[s.Kind]++
	}
	assert.Equal(t, 1, kinds[facts.ScopeFile])
	assert.Equal(t, 1, kinds[facts.ScopeType])
	assert.Equal(t, 2, kinds[facts.ScopeFunction], "the method and the closure")
	assert.Equal(t, 4, kinds[facts.ScopeBlock], "for, while, do, catch")
	assert.Zero(t, kinds[facts.ScopePackage], "Swift writes no namespace declaration to scope")
}

// `guard` is the one statement deliberately left unscoped: it binds for the
// *rest of the enclosing scope*, which is the whole point of writing one, so a
// scope around it would hide the binding from every line that uses it.
func TestAGuardBindingOutlivesItsStatement(t *testing.T) {
	ff := parse(t, "Sources/Greeter/Guard.swift", `
func use(xs: [Int]) -> Int {
    guard let tail = xs.last else { return 0 }
    return tail
}
`)
	assertResolvesLocally(t, ff, "tail", prefix+" Greeter/use().tail.")
}

// The grammar this repository pins cannot parse `if let x = y { … }` — Swift's
// most common statement — while `guard let` and `while let` parse. §14 M9+
// forbids changing go.mod, so the recovery is read rather than fought, and this
// pins what is recovered: the binding survives, and the file still yields facts.
//
// What is lost is recorded here too, so that a later grammar bump is measured
// against it rather than guessed at: the condition's receiver is swallowed by
// the failed parse, so `xs.first` names an unknown type.
func TestAnIfLetIsRecoveredFromTheParseError(t *testing.T) {
	ff := parse(t, "Sources/Greeter/IfLet.swift", `
func use(xs: [Int]) -> Int {
    if let head = xs.first {
        return head
    }
    return 0
}
`)
	assert.Empty(t, ff.ParseError, "tree-sitter recovers; a grammar gap is not a failed call")
	assertResolvesLocally(t, ff, "head", prefix+" Greeter/use().head.")
	assert.Contains(t, referenceDescriptors(ff), stdlib+" Int#first().",
		"the failed parse swallows the receiver's type; recorded so a grammar bump can be measured")
}

// --------------------------------------------------------------------- edges --

func TestParseResolvesSameFileReferences(t *testing.T) {
	ff := parse(t, "Sources/Greeter/Local.swift", `
struct Local {
    private var name: String = "x"

    func greet() -> String { return decorate(value: name) }

    func decorate(value: String) -> String { return value }
}
`)
	assertResolvesLocally(t, ff, "name", prefix+" Greeter/Local#name.")
	assertResolvesLocally(t, ff, "decorate", prefix+" Greeter/Local#decorate().")
}

// Only the four kinds §5 says a file-local extractor can know. The other five
// are link's, and emitting one here would be a claim this stanza cannot make.
func TestParseEmitsOnlyExtractableEdgeKinds(t *testing.T) {
	ff := parse(t, "Sources/Greeter/Local.swift", `
struct Local {
    var name: String = "x"

    func greet() -> String { return name }
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
	path := filepath.Join(c.Root, filepath.FromSlash("Sources/App/main.swift"))
	ff := swift.New().Parse(path, []byte("import Greeter\n"), c)

	assert.Equal(t, path, ff.File.Path)
	assert.Equal(t, swift.Lang, ff.File.Lang)
	assert.Equal(t, c, ff.File.Coord)
	assert.Empty(t, ff.ParseError)
}

// The fixture, parsed as the batch would: two files of one module, one
// cross-file call, and the descriptor that has to match on both sides for it to
// be traversable.
func TestParseTheFixture(t *testing.T) {
	c := testCoord(t)
	p := swift.New()

	read := func(rel string) facts.FileFacts {
		t.Helper()
		path := filepath.Join(c.Root, filepath.FromSlash(rel))
		src, err := os.ReadFile(path) //nolint:gosec // the fixture is in the repository.
		require.NoError(t, err)
		ff := p.Parse(path, src, c)
		require.Empty(t, ff.ParseError)
		return ff
	}

	greeter := read("Sources/Greeter/Greeter.swift")
	runner := read("Sources/Greeter/Runner.swift")
	main := read("Sources/App/main.swift")
	manifest := read("Package.swift")

	assertHasDefinition(t, greeter, prefix+" Greeter/Greeter#greet().")
	assert.Equal(t, prefix+" Greeter/Greeter#greet().",
		referenceNamed(t, runner, "greet").Descriptor.String(),
		"the call and the definition have to render one string or nothing joins them")
	assert.Equal(t, moduleDefinition(t, greeter).Descriptor.String(),
		moduleReference(t, main).Descriptor.String(),
		"the import and the module definition have to render one string or no imports edge exists")
	assertHasDefinition(t, manifest, prefix+" Package_swift#package.")
}

// A file that does not parse is a property of the file and not of the call: the
// facts are whatever the error-tolerant tree yielded, and the caller decides.
func TestParseBrokenSourceStillReturns(t *testing.T) {
	c := testCoord(t)
	ff := swift.New().Parse(filepath.Join(c.Root, "Broken.swift"),
		[]byte("struct Broken {\n    func ping( {\n"), c)

	assert.Empty(t, ff.ParseError, "tree-sitter recovers; a broken file is not a failed call")
	assert.Equal(t, swift.Lang, ff.File.Lang)
}

// TestParseEmptyFile records what an empty Swift file is: a member of its module
// and nothing else — which is what every other Swift file in that directory says
// too, since none of them says anything about it at all.
func TestParseEmptyFile(t *testing.T) {
	ff := parse(t, "Sources/Greeter/Empty.swift", "")

	require.Len(t, ff.Occurrences, 1)
	assert.Equal(t, facts.KindPackage, ff.Occurrences[0].SymbolKind)
	assert.Equal(t, prefix+" Greeter/", ff.Occurrences[0].Descriptor.String())
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
	t.Fatal("no module definition")
	return facts.Occurrence{}
}

func moduleReference(t *testing.T, ff facts.FileFacts) facts.Occurrence {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference && o.SymbolKind == facts.KindPackage {
			return o
		}
	}
	t.Fatal("no module reference")
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
