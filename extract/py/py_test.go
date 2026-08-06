package py_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/extract/py"
	"github.com/gaarutyunov/codiq/facts"
)

// testCoord is the coordinate the batch would hand the parser for a file in the
// package codiq-greeter, rooted at the fixture directory.
func testCoord(t *testing.T) coord.Coord {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "greeter"))
	require.NoError(t, err)
	coords, err := coord.Resolve(root, "greeter")
	require.NoError(t, err)
	c := coords.For("x" + py.Ext)
	require.Equal(t, coord.PyScheme, c.Scheme, "the fixture must resolve through the pyproject resolver")
	return c
}

// parse parses src as the file at name, which is interpreted relative to the
// fixture root — the path is not incidental here the way it is for Go, because
// a Python module's namespace *is* its path.
func parse(t *testing.T, name, src string) facts.FileFacts {
	t.Helper()
	c := testCoord(t)
	ff := py.New().Parse(filepath.Join(c.Root, filepath.FromSlash(name)), []byte(src), c)
	require.Empty(t, ff.ParseError)
	return ff
}

const prefix = "scip-python pip codiq-greeter 1.0.0"

// --------------------------------------------------------------- definitions --

func TestParseDefinitionDescriptors(t *testing.T) {
	ff := parse(t, "shapes.py", `
from typing import Final, Protocol

LEVEL: Final = 1
plain = 2


class Loud:
    volume = 1

    def __init__(self, prefix: str) -> None:
        self.prefix = prefix

    def speak(self, loud: bool = False, *rest, **opts) -> str:
        local = 1
        return local


class Speaker(Protocol):
    def speak(self, loud: bool) -> str: ...


def run(s: Speaker, n: int) -> None:
    for i in range(n):
        pass
    with open("f") as fh:
        pass
`)

	// The whole set, so that a suffix rule which starts producing an *extra*
	// definition fails here too. Keyed by descriptor rather than by name
	// because `speak` and `loud` each name two different symbols.
	want := []string{
		// The module the file is, named by its path rather than by a clause.
		prefix + " shapes/",
		prefix + " shapes/LEVEL.",
		prefix + " shapes/plain.",
		prefix + " shapes/Loud#",
		prefix + " shapes/Loud#volume.",
		prefix + " shapes/Loud#__init__().",
		prefix + " shapes/Loud#__init__().(self)",
		prefix + " shapes/Loud#__init__().(prefix)",
		// The instance attribute, hung off the class and not off __init__.
		prefix + " shapes/Loud#prefix.",
		prefix + " shapes/Loud#speak().",
		prefix + " shapes/Loud#speak().(self)",
		prefix + " shapes/Loud#speak().(loud)",
		prefix + " shapes/Loud#speak().(rest)",
		prefix + " shapes/Loud#speak().(opts)",
		prefix + " shapes/Loud#speak().local.",
		prefix + " shapes/Speaker#",
		prefix + " shapes/Speaker#speak().",
		prefix + " shapes/Speaker#speak().(self)",
		prefix + " shapes/Speaker#speak().(loud)",
		prefix + " shapes/run().",
		prefix + " shapes/run().(s)",
		prefix + " shapes/run().(n)",
		prefix + " shapes/run().i.",
		prefix + " shapes/run().fh.",
	}
	sort.Strings(want)
	assert.Equal(t, want, definitionDescriptors(ff))
}

func TestParseDefinitionKinds(t *testing.T) {
	ff := parse(t, "kinds.py", `
from typing import Final, Protocol
from abc import ABC

MAX: Final = 3
loose = 4


class C:
    attr = 1

    def m(self, p: str) -> None:
        local = 1


class I(Protocol):
    pass


class A(ABC):
    pass


def fn() -> None:
    pass
`)

	tests := []struct {
		name string
		want string
	}{
		{"kinds", facts.KindPackage},
		{"MAX", facts.KindConstant},
		{"loose", facts.KindVariable},
		{"C", facts.KindType},
		{"attr", facts.KindField},
		{"m", facts.KindMethod},
		{"p", facts.KindParameter},
		{"local", facts.KindVariable},
		{"I", facts.KindInterface},
		{"A", facts.KindInterface},
		{"fn", facts.KindFunction},
	}
	got := definitionsByName(ff)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			occ, ok := got[tc.name]
			require.Truef(t, ok, "no definition named %q", tc.name)
			assert.Equal(t, tc.want, occ.SymbolKind)
		})
	}
}

// A `def` is a method or a function depending on nothing but where it is, and
// a name bound by `=` is a field, a constant or a variable on the same
// evidence. Python has one node type for each pair, so these are the
// distinctions the mapper has to draw that the query cannot.
func TestParseKindDependsOnTheEnclosingContainer(t *testing.T) {
	ff := parse(t, "nesting.py", `
class Outer:
    def method(self):
        def inner():
            pass
        return inner


def free():
    class Local:
        pass
    return Local
`)

	got := definitionsByName(ff)
	assert.Equal(t, facts.KindMethod, got["method"].SymbolKind)
	assert.Equal(t, facts.KindFunction, got["inner"].SymbolKind,
		"a def inside a method is a function, not a second method")
	assert.Equal(t, facts.KindFunction, got["free"].SymbolKind)
	assert.Equal(t, facts.KindType, got["Local"].SymbolKind)

	assertHasDefinition(t, ff, prefix+" nesting/Outer#method().inner().")
	assertHasDefinition(t, ff, prefix+" nesting/free().Local#")
}

// `self.x = …` is the only way Python declares an instance attribute, so the
// assignment is the definition — and it belongs to the class rather than to the
// method it is written in. An assignment through anything else is not a
// declaration at all and must stay a reference, or `other.x = 1` would invent a
// member of a class this file may not even define.
func TestParseInstanceAttributes(t *testing.T) {
	ff := parse(t, "attrs.py", `
class Box:
    def __init__(self, other):
        self.value = 1
        cls.shared = 2
        other.value = 3
`)

	assertHasDefinition(t, ff, prefix+" attrs/Box#value.")
	assertHasDefinition(t, ff, prefix+" attrs/Box#shared.")

	// `other.value = 3` declares nothing: one definition named `value`, not two.
	var defined int
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition && o.Name == "value" {
			defined++
		}
	}
	assert.Equal(t, 1, defined, "an assignment through a non-receiver was taken for a declaration")

	// It is still emitted — as the field reference it is, against a receiver
	// the stanza cannot name.
	assert.Equal(t, prefix+" attrs/.#value.",
		referenceNamed(t, ff, "value").Descriptor.String())
}

// ------------------------------------------------------------------ modules --

// TestModuleNamespaceCollapsesInit is the Python half of the namespace
// decision: a module is a file, and `__init__.py` is the module its directory
// *is*. Both facts are read off the path and nothing else, which is what keeps
// extraction file-local (§2.5) — there is no probe for a sibling `__init__.py`
// anywhere in this stanza.
func TestModuleNamespaceCollapsesInit(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"a module at the root", "greeter.py", prefix + " greeter/"},
		{"a module in a package", "pkg/mod.py", prefix + " pkg/mod/"},
		{"a package's __init__", "pkg/__init__.py", prefix + " pkg/"},
		{"a nested package's __init__", "pkg/sub/__init__.py", prefix + " pkg/sub/"},
		{"the root __init__", "__init__.py", prefix},
		{"a stub beside its module", "pkg/mod.pyi", prefix + " pkg/mod/"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ff := parse(t, tc.path, "x = 1\n")
			assert.Equal(t, tc.want, moduleDefinition(t, ff).Descriptor.String())
		})
	}
}

// TestModuleDefinitionMatchesImportReference pins what makes the link pass's
// `imports` derivation a plain descriptor join: the descriptor a file defines
// for itself as a module is byte-identical to the one an importer's @import
// reference carries for it.
//
// It matters more here than it does for Go, because neither side is written
// down anywhere. Go reads the importer's side out of an import path and the
// definition's side out of a `package` clause; both sides of this are *derived*
// from paths — one from the file's own, one from a dotted name resolved against
// the importing file's package — so nothing but this test says they agree.
func TestModuleDefinitionMatchesImportReference(t *testing.T) {
	tests := []struct {
		name     string
		defined  string // the file that defines the module
		importer string // the file that imports it
		stmt     string // the statement it imports it by
	}{
		{"absolute, top level", "greeter.py", "main.py", "import greeter"},
		{"absolute, from", "greeter.py", "main.py", "from greeter import Greeter"},
		{"absolute, into a package", "pkg/mod.py", "main.py", "import pkg.mod"},
		{"absolute, aliased", "pkg/mod.py", "main.py", "import pkg.mod as m"},
		{"a package's __init__", "pkg/__init__.py", "main.py", "import pkg"},
		{"relative sibling", "pkg/mod.py", "pkg/main.py", "from .mod import x"},
		{"relative parent package", "pkg/__init__.py", "pkg/sub/main.py", "from .. import x"},
		{"relative up and across", "pkg/other.py", "pkg/sub/main.py", "from ..other import x"},
		{"relative into a subpackage", "pkg/sub/deep.py", "pkg/main.py", "from .sub.deep import x"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defined := parse(t, tc.defined, "x = 1\n")
			importer := parse(t, tc.importer, tc.stmt+"\n")

			assert.Equal(t,
				moduleDefinition(t, defined).Descriptor.String(),
				moduleReference(t, importer).Descriptor.String(),
				"%s importing %s by %q", tc.importer, tc.defined, tc.stmt)
		})
	}
}

// The standard library is not this package, and the only thing that says so is
// knowing what the standard library is called: `import os` and `import greeter`
// are the same statement. A stdlib module therefore gets a foreign coordinate
// it cannot match anything indexed here from, and everything else is assumed to
// be this package's — Python's own resolution order.
func TestImportCoordinates(t *testing.T) {
	tests := []struct {
		name string
		stmt string
		want string
	}{
		{"a module of this package", "import greeter", prefix + " greeter/"},
		{"the standard library", "import os", "scip-python pip os ."},
		{"a standard-library submodule", "import os.path", "scip-python pip os . path/"},
		{"from the standard library", "from typing import Protocol", "scip-python pip typing ."},
		{"a relative import escaping the root", "from ... import x", "scip-python pip ... ."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ff := parse(t, "main.py", tc.stmt+"\n")
			assert.Equal(t, tc.want, moduleReference(t, ff).Descriptor.String())
		})
	}
}

// --------------------------------------------------------------- references --

// The mandatory cross-file claim, made at the level the mapper decides it: a
// method call on a local names the method of the type the local was built from,
// and that descriptor is byte-identical to the one the defining module writes.
func TestParseResolvesAMethodCallThroughAnInferredType(t *testing.T) {
	defined := parse(t, "greeter.py", `
class Greeter:
    def greet(self) -> str:
        return "hi"
`)
	caller := parse(t, "main.py", `
from greeter import Greeter


def main() -> None:
    g = Greeter("world")
    print(g.greet())
`)

	assert.Equal(t,
		definitionNamed(t, defined, "greet").Descriptor.String(),
		referenceNamed(t, caller, "greet").Descriptor.String())
	assert.Equal(t, prefix+" greeter/Greeter#greet().",
		referenceNamed(t, caller, "greet").Descriptor.String())
	assert.Equal(t, facts.KindMethod, referenceNamed(t, caller, "greet").SymbolKind)
}

// The same claim from the annotation instead of the initialiser, which is the
// exact case rather than the inferred one.
func TestParseResolvesThroughAnAnnotation(t *testing.T) {
	ff := parse(t, "main.py", `
from greeter import Greeter


def main(g: Greeter, n: "Greeter") -> None:
    g.greet()
    n.greet()
`)

	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference && o.Name == "greet" {
			assert.Equal(t, prefix+" greeter/Greeter#greet().", o.Descriptor.String())
		}
	}
}

// A construction is a call and nothing else says otherwise, so the reference
// says what the syntax says. What matters is that being wrong is safe: a class
// descriptor ends `#` and a call's ends `().`, so a guess in either direction
// matches no definition rather than the wrong one.
func TestParseCallOnAClassNamesACallable(t *testing.T) {
	ff := parse(t, "main.py", "from greeter import Greeter\n\nx = Greeter()\n")

	ref := referenceNamed(t, ff, "Greeter")
	assert.Equal(t, prefix+" greeter/Greeter().", ref.Descriptor.String())
	assert.Equal(t, facts.KindFunction, ref.SymbolKind)
}

func TestParseReferenceDescriptors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		ref  string
		want string
	}{
		{
			name: "a call on a module qualifier",
			src:  "import os\n\nos.getcwd()\n",
			ref:  "getcwd",
			want: "scip-python pip os . getcwd().",
		},
		{
			name: "a read through a module qualifier",
			src:  "import os\n\nx = os.sep\n",
			ref:  "sep",
			want: "scip-python pip os . sep.",
		},
		{
			name: "a builtin call",
			src:  "print(1)\n",
			ref:  "print",
			want: "scip-python pip builtins . print().",
		},
		{
			name: "a call on an unknowable receiver",
			src:  "def f(x):\n    x.go()\n",
			ref:  "go",
			want: prefix + " main/.#go().",
		},
		{
			name: "a function of this module",
			src:  "def f():\n    pass\n\n\ndef g():\n    f()\n",
			ref:  "f",
			want: prefix + " main/f().",
		},
		{
			name: "an aliased import binds the exporting module's name",
			src:  "from greeter import Greeter as G\n\ndef f(g: G):\n    g.greet()\n",
			ref:  "greet",
			want: prefix + " greeter/Greeter#greet().",
		},
		{
			name: "a receiver names a member of its own class",
			src:  "class C:\n    def a(self):\n        self.b()\n",
			ref:  "b",
			want: prefix + " main/C#b().",
		},
		{
			name: "a base class is a type reference",
			src:  "from greeter import Greeter\n\n\nclass Loud(Greeter):\n    pass\n",
			ref:  "Greeter",
			want: prefix + " greeter/Greeter#",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ff := parse(t, "main.py", tc.src)
			assert.Equal(t, tc.want, referenceNamed(t, ff, tc.ref).Descriptor.String())
		})
	}
}

// ------------------------------------------------------------------- scopes --

// Python has no block scoping, and the stanza models exactly the scopes the
// language has. A name bound inside an `if` is a name of the enclosing
// function, so a later read of it resolves — which it could not if the `if`'s
// body were a scope the way a TypeScript block is.
func TestScopesAreOnlyPythonsOwn(t *testing.T) {
	ff := parse(t, "flow.py", `
def f(cond):
    if cond:
        chosen = "a"
    else:
        chosen = "b"
    return chosen.strip()
`)

	kinds := map[string]int{}
	for _, s := range ff.Scopes {
		kinds[s.Kind]++
	}
	assert.Equal(t, map[string]int{facts.ScopeFile: 1, facts.ScopeFunction: 1}, kinds,
		"a Python `if` body is not a scope")

	// Both branches bind the same name, in the function and not in a branch, so
	// the read after the `if` finds it — which is what a block scope would have
	// broken.
	fn := ff.Scopes[1]
	require.Equal(t, facts.ScopeFunction, fn.Kind)
	assert.Equal(t, fn.ID, definitionNamed(t, ff, "chosen").Scope)
	assert.Equal(t, definitionNamed(t, ff, "chosen").Descriptor.String(),
		referenceNamed(t, ff, "chosen").Descriptor.String())
}

// A comprehension *is* a scope since Python 3, so its loop variable does not
// leak into the enclosing function.
func TestComprehensionsAreScopes(t *testing.T) {
	ff := parse(t, "comp.py", "def f(ys):\n    return [z for z in ys]\n")

	var comp facts.Scope
	for _, s := range ff.Scopes {
		if s.Kind == facts.ScopeFunction && s.RangeStart > 10 {
			comp = s
		}
	}
	require.NotZero(t, comp.ID, "no comprehension scope")
	assert.Equal(t, comp.ID, definitionNamed(t, ff, "z").Scope)
}

// The module's own definition belongs to the file scope. It is a zero-width
// occurrence at byte 0, and a Python file may open with `class C:` on its first
// byte — so the innermost scope containing it is a class, which is not where a
// module is declared.
func TestModuleDefinitionIsInTheFileScope(t *testing.T) {
	ff := parse(t, "flush.py", "class C:\n    pass\n")

	require.NotEmpty(t, ff.Scopes)
	file := ff.Scopes[0]
	require.Equal(t, facts.ScopeFile, file.Kind)
	assert.Equal(t, file.ID, moduleDefinition(t, ff).Scope)
}

// --------------------------------------------------------------------- edges --

// Every edge an extractor may emit is intra-file (§2.5), and the only one whose
// endpoints are chosen rather than structural is references_local. Here it is
// the receiver's field, resolved within the one CST.
func TestParseResolvesSameFileReferences(t *testing.T) {
	ff := parse(t, "greeter.py", `
class Greeter:
    def __init__(self, name: str) -> None:
        self.name = name

    def greet(self) -> str:
        return "hello, " + self.name
`)

	// The attribute and the parameter it is assigned from share an identifier;
	// the one this is about is the field.
	def := definitionWithKind(t, ff, facts.KindField)
	require.Equal(t, prefix+" greeter/Greeter#name.", def.Descriptor.String())

	var resolved int
	for _, e := range ff.Edges {
		if e.Kind != facts.EdgeReferencesLocal {
			continue
		}
		assert.Equal(t, facts.VertexOccurrence, e.Source.Vertex)
		assert.Equal(t, facts.VertexOccurrence, e.Target.Vertex)
		if e.Target.ID == def.ID {
			resolved++
		}
	}
	assert.Equal(t, 1, resolved, "the read of self.name resolves to the attribute it declares")
}

func TestParseEmitsOnlyExtractableEdgeKinds(t *testing.T) {
	ff := parse(t, "greeter.py", "class C:\n    def m(self):\n        return self.m\n")

	for _, e := range ff.Edges {
		assert.Contains(t,
			[]facts.EdgeKind{facts.EdgeContains, facts.EdgeDefines, facts.EdgeReferencesLocal},
			e.Kind, "a derived edge kind escaped into extraction")
	}
}

// ---------------------------------------------------------------- the file ---

func TestParseFile(t *testing.T) {
	c := testCoord(t)
	path := filepath.Join(c.Root, "greeter.py")
	src, err := os.ReadFile(path)
	require.NoError(t, err)

	ff := py.New().Parse(path, src, c)
	require.Empty(t, ff.ParseError)
	assert.Equal(t, py.Lang, ff.File.Lang)
	assert.Equal(t, path, ff.File.Path)
	assert.Equal(t, c, ff.File.Coord)
}

// Tree-sitter is error-tolerant, so broken Python does not fail the parse — it
// yields whatever the recovered CST holds. What the contract promises is that
// the call still returns, with the file populated, and never panics.
func TestParseBrokenSourceStillReturns(t *testing.T) {
	ff := parse(t, "broken.py", "def f(:\n  return ??\nclass\n")
	assert.Equal(t, py.Lang, ff.File.Lang)
}

func TestParseEmptyFile(t *testing.T) {
	ff := parse(t, "empty.py", "")
	assert.Equal(t, prefix+" empty/", moduleDefinition(t, ff).Descriptor.String())
}

// A file outside the coordinate's root has no namespace to derive, so it gets
// none rather than a wrong one.
func TestParseOutsideTheRoot(t *testing.T) {
	c := testCoord(t)
	ff := py.New().Parse(filepath.FromSlash("/elsewhere/x.py"), []byte("x = 1\n"), c)
	require.Empty(t, ff.ParseError)
	assert.Equal(t, prefix, moduleDefinition(t, ff).Descriptor.String())
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
	return referenceThat(t, ff, func(o facts.Occurrence) bool { return o.Name == name })
}

func definitionWithKind(t *testing.T, ff facts.FileFacts, kind string) facts.Occurrence {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition && o.SymbolKind == kind {
			return o
		}
	}
	t.Fatalf("no %s definition", kind)
	return facts.Occurrence{}
}

func referenceThat(t *testing.T, ff facts.FileFacts, pred func(facts.Occurrence) bool) facts.Occurrence {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference && pred(o) {
			return o
		}
	}
	t.Fatal("no matching reference")
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
