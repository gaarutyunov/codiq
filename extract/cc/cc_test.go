package cc_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/extract/cc"
	"github.com/gaarutyunov/codiq/facts"
)

// testCoord is the coordinate the batch would hand the parser for a file in the
// CMake project `greeter`, rooted at the fixture directory.
func testCoord(t *testing.T) coord.Coord {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "greeter"))
	require.NoError(t, err)
	coords, err := coord.Resolve(root, "greeter")
	require.NoError(t, err)
	c := coords.For("x.c")
	require.Equal(t, coord.CCScheme, c.Scheme, "the fixture must resolve through the CMake resolver")
	return c
}

// parse parses src as the file at name, which is interpreted relative to the
// package root.
//
// Unlike every stanza before this one, the path is *load-bearing* here — but
// only for the two things C keys on a file rather than on a name: internal
// linkage, and `#include`. A symbol with external linkage carries no directory
// component at all, which is what TestNoNamespaceComesFromTheDirectory asserts
// and what makes a header and a source in different directories join.
func parse(t *testing.T, name, src string) facts.FileFacts {
	t.Helper()
	c := testCoord(t)
	ff := cc.New().Parse(filepath.Join(c.Root, filepath.FromSlash(name)), []byte(src), c)
	require.Empty(t, ff.ParseError)
	return ff
}

const prefix = "scip-cc cmake greeter 1.0.0"

// descriptors renders every occurrence of the given role as its descriptor.
func descriptors(ff facts.FileFacts, role facts.Role) []string {
	out := []string{}
	for _, o := range ff.Occurrences {
		if o.Role == role {
			out = append(out, o.Descriptor.String())
		}
	}
	sort.Strings(out)
	return out
}

// kindOf returns the symbol kind of the one occurrence with the given
// descriptor and role.
func kindOf(t *testing.T, ff facts.FileFacts, role facts.Role, descriptor string) string {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == role && o.Descriptor.String() == descriptor {
			return o.SymbolKind
		}
	}
	t.Fatalf("no %s occurrence with descriptor %q", role, descriptor)
	return ""
}

// ------------------------------------------------- the header/source split ---

// TestTheDeclarationIsAReferenceAndTheDefinitionIsTheDefinition is the central
// claim of this stanza and the one every derived edge in a C corpus rests on.
//
// `void greet();` in a header and `void greet() { … }` in a source are the same
// symbol written twice, and the core model has exactly one role for the site a
// join lands on. The prototype is a *reference* carrying the byte-identical
// descriptor, so the header resolves into the source — which is "go to
// definition" run on a declaration — instead of competing with it for every
// caller in the corpus. cc.go's package comment argues the alternative.
func TestTheDeclarationIsAReferenceAndTheDefinitionIsTheDefinition(t *testing.T) {
	header := parse(t, "include/greeter.h", `
void greet(const char *name);
extern int verbose;
struct Greeter;
`)
	source := parse(t, "src/greeter.c", `
#include "greeter.h"

int verbose = 0;

struct Greeter {
    const char *name;
};

void greet(const char *name) { (void)name; }
`)

	// The header declares three symbols and defines none of them.
	assert.Subset(t, descriptors(header, facts.RoleReference), []string{
		prefix + " greet().",
		prefix + " verbose.",
		prefix + " Greeter#",
	})
	assert.NotContains(t, descriptors(header, facts.RoleDefinition), prefix+" greet().")
	assert.NotContains(t, descriptors(header, facts.RoleDefinition), prefix+" verbose.")
	assert.NotContains(t, descriptors(header, facts.RoleDefinition), prefix+" Greeter#")

	// The source defines all three, under descriptors the header wrote byte for
	// byte. The link pass joins on that string and nothing else (SPEC.md §7).
	assert.Subset(t, descriptors(source, facts.RoleDefinition), []string{
		prefix + " greet().",
		prefix + " verbose.",
		prefix + " Greeter#",
	})
}

// TestAnInlineDefinitionInAHeaderIsADefinition is the other half of the rule:
// what decides the role is the presence of a body, not the extension. A
// `static inline` helper is defined where it is written, and so is a struct.
func TestAnInlineDefinitionInAHeaderIsADefinition(t *testing.T) {
	ff := parse(t, "include/util.h", `
typedef struct Point { int x; int y; } Point;

static inline int point_x(const Point *p) { return p->x; }
`)
	defs := descriptors(ff, facts.RoleDefinition)
	assert.Contains(t, defs, prefix+" Point#")
	assert.Contains(t, defs, prefix+" Point#x.")
	assert.Contains(t, defs, prefix+" include/util.h/point_x().")
}

// TestNoNamespaceComesFromTheDirectory is the decision that makes the split
// work at all, stated as a fact.
//
// Every stanza before this one derived a namespace from somewhere — Go from the
// directory, Python from the file, Java from a `package` clause. C has none: all
// external symbols share one flat namespace, and two files defining `greet` at
// file scope is a link error rather than two symbols. So the descriptor carries
// no directory, and `include/greeter.h` and `src/greeter.c` — which are not even
// in the same directory — render the same string.
func TestNoNamespaceComesFromTheDirectory(t *testing.T) {
	deep := parse(t, "src/very/deep/greeter.c", "void greet(void) {}\n")
	shallow := parse(t, "greeter.c", "void greet(void) {}\n")

	assert.Contains(t, descriptors(deep, facts.RoleDefinition), prefix+" greet().")
	assert.Contains(t, descriptors(shallow, facts.RoleDefinition), prefix+" greet().")
}

// TestInternalLinkageIsKeyedOnTheFile is the exception that the absence of a
// namespace forces.
//
// `static void helper(void)` in two files is two functions that share a name,
// and with an empty namespace they would render one descriptor and derive a
// cross-file `resolves_to` edge between two unrelated files. So an
// internal-linkage symbol hangs off the file's own path — which cannot collide
// with a namespace-keyed descriptor, because the last segment of a C path always
// contains a `.` and no C identifier may.
func TestInternalLinkageIsKeyedOnTheFile(t *testing.T) {
	a := parse(t, "src/a.c", `
static void helper(void) {}
void run_a(void) { helper(); }
`)
	b := parse(t, "src/b.c", `
static void helper(void) {}
void run_b(void) { helper(); }
`)

	assert.Contains(t, descriptors(a, facts.RoleDefinition), prefix+" src/a.c/helper().")
	assert.Contains(t, descriptors(b, facts.RoleDefinition), prefix+" src/b.c/helper().")

	// Each file's call reaches its own helper, and neither descriptor can ever
	// match the other's.
	assert.Contains(t, descriptors(a, facts.RoleReference), prefix+" src/a.c/helper().")
	assert.NotContains(t, descriptors(a, facts.RoleReference), prefix+" src/b.c/helper().")
}

// TestAStaticDefinedWithoutTheKeywordStillGetsInternalLinkage is why the
// linkage scan is a pre-pass rather than a per-site question. C lets an earlier
// declaration confer internal linkage on a later definition that does not repeat
// the keyword, and only the declaration says so.
func TestAStaticDefinedWithoutTheKeywordStillGetsInternalLinkage(t *testing.T) {
	ff := parse(t, "src/a.c", `
static void trim(char *s);

void trim(char *s) { (void)s; }
`)
	assert.Contains(t, descriptors(ff, facts.RoleDefinition), prefix+" src/a.c/trim().")
	assert.NotContains(t, descriptors(ff, facts.RoleDefinition), prefix+" trim().")
}

// ------------------------------------------------------------ #include -------

// TestIncludeJoinsByPathSuffix is the whole of C's import story.
//
// `#include` names a path the *build system* resolves through its `-I` search,
// and a file-local reader has no access to that. What it can do is offer every
// resolution the join could want: the included file emits a `package`
// definition per suffix of its own path, the including file emits a `package`
// reference for the path as written, and link's `imports` derivation matches
// them with no change.
func TestIncludeJoinsByPathSuffix(t *testing.T) {
	header := parse(t, "include/greeter/greeter.h", "void greet(void);\n")
	source := parse(t, "src/main.c", `
#include "greeter/greeter.h"
#include "greeter.h"
#include <stdio.h>
`)

	assert.Subset(t, descriptors(header, facts.RoleDefinition), []string{
		prefix + " greeter.h/",
		prefix + " greeter/greeter.h/",
		prefix + " include/greeter/greeter.h/",
	})
	// Both spellings of the include match a suffix the header emitted, which is
	// what makes the join independent of the `-I` configuration.
	assert.Subset(t, descriptors(source, facts.RoleReference), []string{
		prefix + " greeter/greeter.h/",
		prefix + " greeter.h/",
	})
	assert.Equal(t, facts.KindPackage, kindOf(t, source, facts.RoleReference, prefix+" greeter.h/"))
}

// TestAnExtensionlessSystemIncludeMintsNothing is the guard on the argument
// that makes path-keyed descriptors safe.
//
// A path-keyed package descriptor cannot collide with a namespace-keyed one
// because the last segment of a C file path contains a `.` and no C++ namespace
// name may. `#include <string>` has no such segment — and `namespace string` is
// legal C++ — so it mints no descriptor at all. Nothing is lost: no repository
// file is named `string` with no extension.
func TestAnExtensionlessSystemIncludeMintsNothing(t *testing.T) {
	ff := parse(t, "src/main.cpp", `
#include <string>
#include <vector>
#include <sys/types.h>
`)
	refs := descriptors(ff, facts.RoleReference)
	assert.NotContains(t, refs, prefix+" string/")
	assert.NotContains(t, refs, prefix+" vector/")
	assert.Contains(t, refs, prefix+" sys/types.h/")
}

// --------------------------------------------------------------------- C++ ---

// TestCppNamespacesAreReadFromTheSource is C++'s half of the namespace story:
// unlike C it has one, and it is written down exactly as PHP's and C#'s are.
func TestCppNamespacesAreReadFromTheSource(t *testing.T) {
	ff := parse(t, "include/greeter.hpp", `
namespace greeter {
namespace detail {
struct Tag {};
}

class Greeter {
public:
    explicit Greeter(const char *name);
    const char *greet() const;

private:
    const char *name_;
};

enum class Mood { Calm, Loud };
}
`)
	assert.Subset(t, descriptors(ff, facts.RoleDefinition), []string{
		prefix + " greeter/",
		prefix + " greeter/detail/",
		prefix + " greeter/detail/Tag#",
		prefix + " greeter/Greeter#",
		prefix + " greeter/Greeter#Greeter().",
		prefix + " greeter/Greeter#greet().",
		prefix + " greeter/Greeter#name_.",
		prefix + " greeter/Mood#",
		prefix + " greeter/Mood#Calm.",
	})
	assert.Equal(t, facts.KindPackage, kindOf(t, ff, facts.RoleDefinition, prefix+" greeter/"))
}

// TestAnOutOfLineDefinitionAgreesWithTheClassBody is the C++ shape of the
// header/source split, and the place this stanza inherits Ruby's unanswerable
// `A::B` question.
//
// The grammar labels the scope of a qualified declarator `namespace_identifier`
// whether it names a namespace or a class. The rule — innermost qualifier is a
// type, everything before it a namespace — is what makes `Greeter::greet` in the
// source render the descriptor `class Greeter { … greet(); }` in the header
// rendered, in both of the two idioms that actually occur.
func TestAnOutOfLineDefinitionAgreesWithTheClassBody(t *testing.T) {
	inBlock := parse(t, "src/greeter.cpp", `
#include "greeter.hpp"

namespace greeter {
const char *Greeter::greet() const { return name_; }
}
`)
	fullyQualified := parse(t, "src/other.cpp", `
#include "greeter.hpp"

const char *greeter::Greeter::greet() const { return name_; }
`)

	assert.Contains(t, descriptors(inBlock, facts.RoleDefinition), prefix+" greeter/Greeter#greet().")
	assert.Contains(t, descriptors(fullyQualified, facts.RoleDefinition), prefix+" greeter/Greeter#greet().")

	// The qualifier is a reference to the class itself, which is what takes an
	// out-of-line member back to the body that declared it.
	assert.Contains(t, descriptors(inBlock, facts.RoleReference), prefix+" greeter/Greeter#")
}

// TestAnAbstractClassIsAnInterface is what makes link's `implements` derivation
// fire in a language with no `interface` keyword.
//
// C++ writes an interface as a class whose member functions are all pure virtual
// and which declares no data members. The promotion is keyed on
// `symbol_kind = 'interface'` (store/sqlc/query.sql), and without it C++ would
// derive no `implements` edge at all.
func TestAnAbstractClassIsAnInterface(t *testing.T) {
	ff := parse(t, "include/speaker.hpp", `
namespace greeter {
class Speaker {
public:
    virtual ~Speaker() = default;
    virtual const char *greet() const = 0;
};

class Greeter : public Speaker {
public:
    const char *greet() const override { return ""; }

private:
    const char *name_;
};
}
`)
	assert.Equal(t, facts.KindInterface, kindOf(t, ff, facts.RoleDefinition, prefix+" greeter/Speaker#"))
	assert.Equal(t, facts.KindType, kindOf(t, ff, facts.RoleDefinition, prefix+" greeter/Greeter#"),
		"a class with a data member is not an interface, however many virtuals it has")
	assert.Contains(t, descriptors(ff, facts.RoleReference), prefix+" greeter/Speaker#",
		"the base-class clause is a reference to the interface's type")
}

// TestADestructorIsNotEmitted is the other half of what makes `implements`
// usable in C++, and it is a deliberate omission rather than an oversight.
//
// Every class has a destructor whether one is written or not, and it is named
// after its class — so `Speaker#~Speaker().` in an abstract base's method set is
// a member no implementer can ever have, and containment would fail for every
// interface that declares the virtual destructor good C++ always declares.
func TestADestructorIsNotEmitted(t *testing.T) {
	ff := parse(t, "include/speaker.hpp", `
namespace greeter {
class Speaker {
public:
    virtual ~Speaker() = default;
    virtual const char *greet() const = 0;
};
}
`)
	for _, o := range ff.Occurrences {
		assert.NotContains(t, o.Descriptor.String(), "~", "%s", o.Name)
	}
	assert.Contains(t, descriptors(ff, facts.RoleDefinition), prefix+" greeter/Speaker#greet().")
}

// TestAnInterfacesMethodSetIsContainedByItsImplementer is the shape link's
// `implements` derivation needs, checked at the descriptor level where this
// stanza can check it: an abstract base and an implementing class must render
// the same member suffix under different type prefixes.
func TestAnInterfacesMethodSetIsContainedByItsImplementer(t *testing.T) {
	iface := parse(t, "include/speaker.hpp", `
namespace greeter {
class Speaker {
public:
    virtual ~Speaker() = default;
    virtual const char *greet() const = 0;
};
}
`)
	impl := parse(t, "include/loud.hpp", `
namespace greeter {
class Loud {
public:
    const char *greet() const { return "HI"; }
};
}
`)
	assert.Equal(t, facts.KindInterface, kindOf(t, iface, facts.RoleDefinition, prefix+" greeter/Speaker#"))
	assert.Contains(t, descriptors(iface, facts.RoleDefinition), prefix+" greeter/Speaker#greet().")
	assert.Equal(t, facts.KindType, kindOf(t, impl, facts.RoleDefinition, prefix+" greeter/Loud#"))
	assert.Contains(t, descriptors(impl, facts.RoleDefinition), prefix+" greeter/Loud#greet().")
}

// TestAQualifierIsReadAsANamespaceOutsideADeclarator is the correction the
// qualifier rule needs to stay honest outside the one position it was argued
// for. `greeter::Greeter g;` names a type in a namespace, and reading `greeter`
// as a class would render a descriptor the header never wrote.
func TestAQualifierIsReadAsANamespaceOutsideADeclarator(t *testing.T) {
	ff := parse(t, "src/app.cpp", `
#include "greeter.hpp"

namespace app {
const char *embark() {
    greeter::Greeter g;
    return g.greet();
}
}
`)
	refs := descriptors(ff, facts.RoleReference)
	assert.Contains(t, refs, prefix+" greeter/Greeter#")
	assert.Contains(t, refs, prefix+" greeter/Greeter#greet().")
	assert.NotContains(t, refs, prefix+" greeter#Greeter#")
}

// TestAQualifierSeenAsATypeIsReadAsOne is the evidence half of the same rule:
// tree-sitter labels an identifier `type_identifier` in a type position and
// `namespace_identifier` in a namespace one, so a file that writes `Mood m;`
// has told this stanza that `Mood::Calm` reaches a member of a type.
func TestAQualifierSeenAsATypeIsReadAsOne(t *testing.T) {
	ff := parse(t, "src/app.cpp", `
#include "mood.hpp"

int run() {
    Mood m = Mood::Calm;
    return static_cast<int>(m);
}
`)
	assert.Contains(t, descriptors(ff, facts.RoleReference), prefix+" Mood#Calm.")
}

// TestExternCResetsTheNamespace is the one place this stanza knowingly departs
// from C++'s own name lookup, and it is the interop case the construct exists
// for.
//
// `extern "C" void greet(…);` inside `namespace greeter` is, to C++, the name
// `greeter::greet`. But it is written precisely so that a C translation unit can
// call it, and C has one namespace — so rendering it `greeter/greet().` would
// guarantee it never joined the `void greet(…) { … }` in the .c file it exists
// to reach.
func TestExternCResetsTheNamespace(t *testing.T) {
	header := parse(t, "include/bridge.hpp", `
namespace greeter {
extern "C" {
void c_greet(const char *name);
}
}
`)
	source := parse(t, "src/bridge.c", "void c_greet(const char *name) { (void)name; }\n")

	assert.Contains(t, descriptors(header, facts.RoleReference), prefix+" c_greet().")
	assert.NotContains(t, descriptors(header, facts.RoleReference), prefix+" greeter/c_greet().")
	assert.Contains(t, descriptors(source, facts.RoleDefinition), prefix+" c_greet().")
}

// TestAnUnnamedNamespaceIsInternalLinkage is C++'s spelling of C's `static`,
// and it is keyed the same way.
func TestAnUnnamedNamespaceIsInternalLinkage(t *testing.T) {
	ff := parse(t, "src/greeter.cpp", `
namespace greeter {
namespace {
int calls = 0;
void bump() { ++calls; }
}
}
`)
	defs := descriptors(ff, facts.RoleDefinition)
	assert.Contains(t, defs, prefix+" src/greeter.cpp/bump().")
	assert.Contains(t, defs, prefix+" src/greeter.cpp/calls.")
	assert.NotContains(t, defs, prefix+" greeter/bump().")
}

// TestOverloadsCollapseToOneDescriptor pins the collision java.go settled the
// shape of: a descriptor only one side can compute is worse than one both sides
// compute the same way.
//
// A declaration writes its parameter types; a call site does not, because
// picking the overload needs the argument's type and that is semantic analysis.
// So no parameter component is emitted, every overload renders one descriptor,
// and only the first contributes a definition row — one symbol rather than an
// arbitrary number of indistinguishable ones.
func TestOverloadsCollapseToOneDescriptor(t *testing.T) {
	ff := parse(t, "include/greeter.hpp", `
namespace greeter {
class Greeter {
public:
    const char *greet() const;
    const char *greet(int times) const;
    const char *greet(const char *sep, int times) const;
};
}
`)
	n := 0
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition && o.Descriptor.String() == prefix+" greeter/Greeter#greet()." {
			n++
		}
	}
	assert.Equal(t, 1, n, "three overloads, one descriptor, one definition row")
}

// TestATemplateAndItsSpecializationCollapse is the same collision one dimension
// out. SCIP has a `[T]` type-parameter descriptor and it is deliberately unused:
// a use site writes *arguments*, not parameters, so a descriptor carrying the
// parameter is one the use can never reproduce.
func TestATemplateAndItsSpecializationCollapse(t *testing.T) {
	ff := parse(t, "include/buffer.hpp", `
namespace greeter {
template <typename T, int N>
class Buffer {
public:
    T &at(int i);
};

template <>
class Buffer<bool, 4> {
public:
    bool &at(int i);
};
}
`)
	n := 0
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition && o.Descriptor.String() == prefix+" greeter/Buffer#" {
			n++
		}
	}
	assert.Equal(t, 1, n, "the primary template and its specialization render one descriptor")
	assert.Contains(t, descriptors(ff, facts.RoleDefinition), prefix+" greeter/Buffer#at().",
		"a member of a class template is descriptored as a member of a plain class")
}

// TestTheStandardLibraryCarriesAForeignCoordinate keeps `printf` and
// `std::string` out of this repository's coordinate, so that a project defining
// its own `printf` cannot false-match every call to the real one.
func TestTheStandardLibraryCarriesAForeignCoordinate(t *testing.T) {
	c := parse(t, "src/main.c", `
#include <stdio.h>
void run(void) { printf("hi"); }
`)
	assert.Contains(t, descriptors(c, facts.RoleReference), "scip-cc cmake libc . printf().")

	cpp := parse(t, "src/main.cpp", `
#include <string>
namespace app {
std::string name() { return std::string(); }
}
`)
	assert.Contains(t, descriptors(cpp, facts.RoleReference), "scip-cc cmake std . string#")
}

// ------------------------------------------------------------ same file ------

// TestASameFileReferenceIsResolvedAtExtraction is §4.3's split: the target
// definition is in this CST, so the edge is extracted rather than left to the
// link pass.
func TestASameFileReferenceIsResolvedAtExtraction(t *testing.T) {
	ff := parse(t, "src/greeter.c", `
static const char *fallback(void) { return "world"; }

const char *greeter_name(const char *name) {
    if (name == 0) {
        return fallback();
    }
    return name;
}
`)
	var local int
	for _, e := range ff.Edges {
		if e.Kind == facts.EdgeReferencesLocal {
			local++
		}
	}
	assert.Positive(t, local, "the call to fallback() resolves inside the file")
}

// TestAMemberReadIsTypedByItsDeclaration is the receiver recovery every stanza
// with declared types does, and C and C++ write the type down at every binding
// site — which puts this stanza with C# rather than with Ruby.
func TestAMemberReadIsTypedByItsDeclaration(t *testing.T) {
	ff := parse(t, "src/main.c", `
struct Greeter { const char *name; };

const char *run(void) {
    struct Greeter g;
    return g.name;
}
`)
	assert.Contains(t, descriptors(ff, facts.RoleReference), prefix+" Greeter#name.")
}

// ---------------------------------------------------------------- scopes -----

// TestScopesNestByByteContainment checks the containment skeleton, and C is the
// first language in this graph since Go whose braces are a real scope: a name
// declared inside `{ }` is invisible outside it.
func TestScopesNestByByteContainment(t *testing.T) {
	ff := parse(t, "src/main.c", `
int run(int n) {
    if (n > 0) {
        int inner = n;
        return inner;
    }
    return 0;
}
`)
	require.NotEmpty(t, ff.Scopes)
	assert.Equal(t, facts.ScopeFile, ff.Scopes[0].Kind)
	assert.Equal(t, facts.NoID, ff.Scopes[0].Parent)

	kinds := map[string]int{}
	for _, s := range ff.Scopes {
		kinds[s.Kind]++
		if s.ID != ff.Scopes[0].ID {
			assert.NotEqual(t, facts.NoID, s.Parent, "every scope but the file's has a parent")
		}
	}
	assert.Equal(t, 1, kinds[facts.ScopeFunction])
	assert.GreaterOrEqual(t, kinds[facts.ScopeBlock], 2, "the function body and the if body")
}

// --------------------------------------------------------------- tripwires ---

// TestNoDefinitionIsEmittedAsAModule guards the one kind that would be invisible
// to the link pass. `imports` joins on `symbol_kind = 'package'`
// (store/sqlc/query.sql), so anything emitted with `facts.KindModule` derives no
// import edge at all and fails *silently* — TypeScript has that defect today.
// §5's own capture list still names `module`; this is the reason not to follow
// it.
//
// C and C++ have four tempting candidates and none of them is a module: a C++
// `namespace` is a `package`, a translation unit is a `package`, a path-keyed
// include target is a `package`, and a `struct` is a `type`.
func TestNoDefinitionIsEmittedAsAModule(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"include/every.h", `
#define K 1
typedef struct Point { int x; } Point;
enum Mood { CALM };
void greet(void);
int run(void) { return 0; }
`},
		{"include/every.hpp", `
namespace app {
namespace inner {}
class C { public: void m(); int f; };
struct S { int a; };
enum class E { A };
using Alias = int;
template <typename T> class T1 { public: void m(); };
void free_fn();
int run() { return 0; }
}
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ff := parse(t, tc.name, tc.src)
			require.NotEmpty(t, ff.Occurrences)
			for _, o := range ff.Occurrences {
				assert.NotEqual(t, facts.KindModule, o.SymbolKind,
					"%s must not be a module: imports joins on symbol_kind = 'package'", o.Descriptor.String())
			}
		})
	}
}

// TestEveryHeaderExtensionIsReadWithTheCppGrammar pins the `.h` decision. A `.h`
// may be C or C++ and nothing in it says which; measured, a C header under the
// C++ grammar produces the identical tree while a C++ header under the C grammar
// loses every class, template and namespace in it. So the ambiguity is resolved
// towards C++, and `.c` is the only extension the C grammar reads.
func TestEveryHeaderExtensionIsReadWithTheCppGrammar(t *testing.T) {
	const src = `
namespace greeter {
class Greeter { public: const char *greet() const; };
}
`
	for _, ext := range []string{".h", ".hh", ".hpp", ".hxx", ".cc", ".cpp", ".cxx"} {
		t.Run(ext, func(t *testing.T) {
			ff := parse(t, "include/greeter"+ext, src)
			assert.Contains(t, descriptors(ff, facts.RoleDefinition), prefix+" greeter/Greeter#")
		})
	}

	// `.c` is read with the C grammar, which has no namespaces at all — so the
	// same text is not a C++ class there, and the stanza does not pretend it is.
	ff := parse(t, "src/greeter.c", src)
	assert.NotContains(t, descriptors(ff, facts.RoleDefinition), prefix+" greeter/Greeter#")
}

// TestEveryOccurrenceCarriesTheHandedCoordinate is the §4.3 ownership boundary:
// a stanza receives a Coord and may not invent one. The only descriptors that
// leave it are the two foreign coordinates for the standard libraries, which
// name packages this index can never hold.
func TestEveryOccurrenceCarriesTheHandedCoordinate(t *testing.T) {
	c := testCoord(t)
	ff := parse(t, "src/main.c", `
#include "greeter.h"
int main(void) { greet(); return 0; }
`)
	require.NotEmpty(t, ff.Occurrences)
	for _, o := range ff.Occurrences {
		if o.Descriptor.Prefix.Name == "libc" || o.Descriptor.Prefix.Name == "std" {
			continue
		}
		assert.Equal(t, c, o.Descriptor.Prefix, "%s", o.Name)
	}
	assert.Equal(t, cc.Lang, ff.File.Lang)
}

// TestTheFixtureParses is the end-to-end check on the corpus every other suite
// reuses: the four files on disk parse, and the header's declaration and the
// source's definition of `greet` are the same string.
func TestTheFixtureParses(t *testing.T) {
	c := testCoord(t)
	p := cc.New()
	seen := map[facts.Role][]string{}
	for _, rel := range []string{"include/greeter.h", "src/greeter.c", "src/main.c"} {
		path := filepath.Join(c.Root, filepath.FromSlash(rel))
		src, err := os.ReadFile(path) //nolint:gosec // a fixture path this test built.
		require.NoError(t, err)

		ff := p.Parse(path, src, c)
		require.Empty(t, ff.ParseError, rel)
		for _, o := range ff.Occurrences {
			seen[o.Role] = append(seen[o.Role], o.Descriptor.String())
		}
	}
	assert.Contains(t, seen[facts.RoleDefinition], prefix+" greet().")
	assert.Contains(t, seen[facts.RoleReference], prefix+" greet().")
	assert.Contains(t, seen[facts.RoleDefinition], prefix+" src/greeter.c/fallback().")
	assert.Contains(t, seen[facts.RoleReference], prefix+" greeter.h/")
	assert.Contains(t, seen[facts.RoleDefinition], prefix+" greeter.h/")
}
