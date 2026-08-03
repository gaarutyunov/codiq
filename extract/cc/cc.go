// Package cc is the C and C++ stanza: the tree-sitter queries in query_c.scm
// and query_cpp.scm plus the mapper here that turns their captures into core
// facts (SPEC.md §5).
//
// It imports facts and coord and deliberately not extract: it satisfies
// extract.Parser structurally, which is what keeps the registry free of an
// import cycle.
//
// It is named for no single extension, because it owns eight and none of them
// names the pair. `cc` is one of the eight, is the traditional name of the C
// compiler, and is the shortest thing that means "C and C++ together" — which
// is what this stanza is, and, as coord/cmake.go argues, what it has to be.
//
// # The eight stanzas before this one had two things C does not have
//
// Every language so far arrived with a manifest that named the package and a
// module system the compiler resolved semantically. C has neither, C++ has half
// of the second, and the honest thing is to say what that costs rather than to
// dress the gaps up.
//
//   - **`#include` is textual.** It pastes a file in. There is no import of a
//     *name*, no alias table, nothing analogous to PHP's `use` or Rust's `use`
//     or C#'s `using`; and the path is resolved by the *build system's* `-I`
//     search, which is not in the source at all. What this stanza does about
//     that is below.
//   - **The preprocessor can rewrite anything.** tree-sitter parses the
//     unexpanded text, so a macro that declares a function, renames a type or
//     hides a declaration behind `#ifdef` is invisible: `DECLARE_GETTER(int,
//     count)` yields no `get_count` and no `set_count`, and both arms of an
//     `#if`/`#else` are read as if both were compiled. Measured on a
//     macro-hostile header, the C grammar produced 2 MISSING nodes and the C++
//     grammar 1 ERROR, and *neither* recovered a single one of the declarations
//     the macros generate. This is not a fidelity gap that a better query
//     closes; it is the difference between reading C and compiling it.
//   - **Declaration and definition are separate, and idiomatically in different
//     files.** That is new. In all eight predecessors a declaration site and a
//     definition site were the same site. See below — it is the central design
//     question of this stanza.
//   - **There is no standard manifest.** coord/cmake.go takes that one, states
//     what CMakeLists.txt can and cannot be read as, and says loudly what a
//     repository with no manifest at all costs.
//
// # `void greet();` in greeter.h, `void greet() { … }` in greeter.c
//
// The core model has two roles, `definition` and `reference`, and every derived
// edge is a join between them (§4.4, store/sqlc/query.sql). So the question is
// not philosophical: whichever role the header's prototype gets decides what
// `resolves_to` and `calls` contain for every C program ever indexed here.
//
// **The rule is: a declarator with a body is a definition; a declarator without
// one is a reference carrying the byte-identical descriptor.** A class member
// declaration is the exception and is a definition; the argument for that is
// two paragraphs down.
//
// Consider what the alternative does. If the prototype were also a definition,
// then every cross-file call in a C program would `resolves_to` **two** rows —
// the prototype and the body — and `calls` would carry both, half of its edges
// pointing at a declaration that calls nothing. A C project declares every
// external function exactly once and defines it exactly once, so that is not a
// corner case; it is a doubling of the entire call graph. And the two rows would
// never be joined to each other, because a definition never joins a definition,
// so the graph would hold the header and the source as two unrelated symbols
// that merely happen to share a name.
//
// With the rule as written, all three joins are the ones an agent wants:
//
//	greeter.h:  void greet();          reference  →─┐
//	main.c:     greet(&g);             reference  →─┤   resolves_to
//	greeter.c:  void greet() { … }     definition ←─┘
//
// The header resolves *into* the source, which is exactly "go to definition"
// run on a declaration; the call resolves into the source and nowhere else; and
// `calls` holds one edge, from `main` to `greet`. Nothing is lost, because the
// header's occurrence is still there and still navigable — it changed role, not
// existence.
//
// The rule falls out of the CST rather than being imposed on it: `void greet();`
// is a `declaration` and `void greet() { … }` is a `function_definition`, two
// different node kinds; a `struct Greeter;` is a `struct_specifier` with no
// `body` field and `struct Greeter { … };` is one with a body; an `extern int
// verbose;` carries an `extern` storage class and `int verbose = 0;` does not.
// Every case is decided by asking the tree a question it already answers.
//
// **The exception, and why it is not an exception.** A member declared inside a
// class body — `class Greeter { std::string greet(); };` — is a *definition*
// here, even though it has no body and the body is in the .cpp. The reason is
// that the class body **is** the definition of what the class has: nothing else
// in the program states that `Greeter` has a `greet`, and an out-of-line
// definition is only legal *because* the class declared it. Making it a
// reference would have one further consequence that settles the matter on its
// own: a C++ abstract base class consists entirely of pure virtual functions,
// which are declared and never defined, so its method set would be empty and
// link's `implements` derivation — which gathers a method set from
// *definitions* by descriptor prefix (store/sqlc/query.sql) — would never fire
// for any C++ interface at all. The price is paid where C++ splits a member
// across two files: the in-class declaration and the out-of-line definition are
// both definitions, so a call to such a member resolves to two rows. That is
// the doubling described above, confined to the one construct that earns it.
//
// # There is no namespace in C, and that is load-bearing
//
// Go's namespace component is the directory, TypeScript's and Python's the
// file, Rust's the `mod` tree, Java's, C#'s and PHP's a declared namespace,
// Ruby's the `module`/`class` nesting. **C's is empty.** Every symbol with
// external linkage lives in one flat namespace shared by the whole linked
// program; two files defining `greet` at file scope is a link error, not two
// symbols.
//
// So this stanza never calls coord.Coord.Namespace, and it is the first that
// does not. That is not a shortcut — it is the thing that makes the header/
// source join possible at all. `include/greeter.h` and `src/greeter.c` are in
// different directories; a directory-derived namespace would render
// `include/greet().` and `src/greet().`, and the declaration and the definition
// of one symbol would never match. Modelling C as if it had per-directory
// packages would break the one join the language cares about.
//
// C++ *does* have namespaces, and they are read from the source exactly as
// PHP's and C#'s are: `namespace greeter { }` contributes `greeter/`, and an
// out-of-line definition writes the qualifier itself. The directory still
// contributes nothing, in either dialect.
//
// Two things fill the vacuum the missing namespace leaves.
//
// **Internal linkage is keyed on the file.** `static void helper(void)` in a.c
// and `static void helper(void)` in b.c are two different functions that
// happen to share a name, and with an empty namespace they would render one
// descriptor and produce a cross-file `resolves_to` edge between two unrelated
// files — a wrong edge, which is the thing this project most avoids. So a
// symbol this file gives internal linkage — `static` at file scope, or anything
// inside an unnamed `namespace { … }` — is descriptored under the file's own
// repo-relative path: `src/util.c/helper().`.
//
// That is a path-keyed descriptor, which php.go considered and rejected because
// a PHP namespace is unconstrained by case and so a path-keyed package could
// collide with a namespace-keyed one. C and C++ can make the argument PHP could
// not, and it is exact rather than statistical: **the last segment of a C or C++
// file path always contains a `.`**, and `.` is not a character any C or C++
// identifier may contain, so a path-keyed descriptor can never be spelled by a
// namespace-keyed one. It is rb.go's "a Ruby constant is capitalised and a Ruby
// path is not" with the extension dot doing the work instead of the case.
//
// **`#include` is approximated by path suffix.** The include path is resolved
// by the build system, and the only file-local approximation is to offer every
// resolution the join could want and let the descriptor match do what `-I`
// would have done. So a file at `include/greeter/greeter.h` emits a `package`
// definition for *each suffix of its own path* — `greeter.h/`,
// `greeter/greeter.h/`, `include/greeter/greeter.h/` — and an
// `#include "greeter/greeter.h"` anywhere emits a `package` reference for the
// path as written. link's `imports` derivation joins the two with no change
// (store/sqlc/query.sql), and it works from any `-I` configuration without
// knowing any of them.
//
// The cost is stated rather than hidden: two files whose paths share a suffix
// equal to a written include — `a/util.h` and `b/util.h` under
// `#include "util.h"` — both match, and one of the two edges is wrong. That is
// bounded by basename collisions and is the price of the only mechanism C has.
// A system include with no extension (`#include <string>`) mints nothing, which
// is both correct — no repository file is named `string` with no extension —
// and necessary, since `string/` *could* be spelled by a `namespace string`.
//
// # C++'s `A::B`, which is Ruby's question in a different alphabet
//
// `std::string Greeter::greet() const { … }` in greeter.cpp has to render
// `greeter/Greeter#greet().` to join the header. But the grammar labels the
// scope of a `qualified_identifier` `namespace_identifier` whether it names a
// namespace or a class, and both `void ns::freefunc() { }` and
// `void Cls::method() { }` are legal and identical in shape. Nothing file-local
// settles it — the class is in the header, which §2.5 forbids reading.
//
// The rule is: **in a declarator's qualified name, the innermost qualifier is a
// type and every qualifier before it is a namespace, unless this file declares a
// namespace of that name, in which case it is a namespace.** It is chosen
// because it is right for both of the two idioms that actually occur:
//
//	namespace greeter { std::string Greeter::greet() … }  → greeter/Greeter#greet().
//	std::string greeter::Greeter::greet() …               → greeter/Greeter#greet().
//
// and wrong for the one that mostly does not: `void greeter::c_greet() { }`, a
// namespace-scope function defined out of line without a namespace block, which
// renders `greeter#c_greet().` and joins nothing. The `unless this file declares
// a namespace of that name` clause rescues the mixed file — one that opens
// `namespace greeter { }` somewhere and also writes `int greeter::verbose = 0;`
// — and a file that does neither gets the documented gap. A nested class defined
// out of line (`Outer::Inner::m`) reads `Outer` as a namespace and is wrong the
// same way.
//
// # Overloads, templates and `extern "C"`
//
// **Overloads collide, deliberately.** C++ overloads on parameter types, and
// java.go settled the shape of the answer: a descriptor only one side can
// compute is worse than one both sides compute the same way. A declaration
// writes its parameter types; a *call site* does not — `greet(x)` needs the type
// of `x` to pick the overload, which is semantic analysis and not a CST read. So
// no parameter component is emitted, `greet()` and `greet(int)` both render
// `Greeter#greet().`, and a call to either resolves to whichever definition is
// emitted first. Only one is: a second definition with a descriptor this file
// already defined is dropped, so an overload set contributes one definition row
// rather than an arbitrary number of indistinguishable ones.
//
// **Templates collide with their specializations.** `template <typename T> class
// Buffer` renders `Buffer#`, and so does `template <> class Buffer<bool>`, and
// so does every use `Buffer<int>`. The type parameters are not emitted and the
// arguments are not read. SCIP has a `[T]` descriptor for a type parameter and
// it is deliberately unused, for java.go's reason again: a use site writes
// *arguments*, not parameters, so a descriptor carrying the parameter is one the
// use can never reproduce. `template_declaration` is therefore transparent in the
// container walk, which also means a member of a class template is descriptored
// exactly as a member of a plain class.
//
// **`extern "C"` resets the namespace to the global one.** `extern "C" void
// greet(const Greeter *);` written inside `namespace greeter` is, to C++ name
// lookup, `greeter::greet` — but it exists precisely so that a C translation
// unit can call it, and C has one namespace. Rendering it `greeter/greet().`
// would guarantee it never joined the `void greet(const Greeter *) { … }` in the
// .c file it was written to reach. So a `linkage_specification` terminates the
// container walk at the global namespace, and a C++ declaration of a C function
// renders the byte-identical descriptor the C definition does. That is the one
// place this stanza knowingly departs from C++'s own name lookup, and it is the
// interop case the construct exists for.
//
// # What this stanza cannot represent
//
//   - **Anything the preprocessor generates.** Argued above. A macro that
//     declares functions, a token-pasted name, a type hidden behind `#ifdef`,
//     and the unselected arm of an `#if` are all read as the text they are, not
//     as the program they become.
//   - **An object-like macro *use*.** `#define MAXN 8` is emitted as a constant
//     definition, but `int x = MAXN;` is a bare `identifier` in an expression
//     and is not captured, exactly as php.go declines to capture a bare
//     `(name)`. A *function-like* macro use is a `call_expression` and does
//     resolve, which is an asymmetry the preprocessor hands us rather than one
//     chosen here.
//   - **`type_defines`, in either dialect, ever.** link derives it from
//     adjacency: a variable definition immediately followed by its type's
//     reference occurrence (store/sqlc/query.sql). C and C++ write the type
//     *before* the declarator — `Greeter g;` is [type reference][variable
//     definition] — so the predecessor of the type reference is never the
//     declaration it types, and the derivation yields nothing. It under-derives
//     rather than inventing, which is how that approximation is designed to
//     fail; fixing it is a `declared_type` column, which is a core-schema change
//     an additional-language task must not make.
//   - **Which language a `.h` is.** Nothing in the file says. It is parsed with
//     the C++ grammar because a C header measures ERROR=0 under it while a C++
//     header measures 31 ERROR under the C grammar, and `file.lang` is `cc` for
//     every extension this stanza owns *because* of that: tagging `.h` as one
//     language and `.c` as another would make a pure-C project's header/source
//     edge a cross-language edge, which is the one invariant the mixed corpus
//     exists to hold. The residue is that C code using `new` as an identifier —
//     legal C, not legal C++ — parses with one ERROR node.
//   - **Overload sets, template specializations and the `static`/instance split
//     on members.** All collapse to one descriptor; argued above.
//   - **A `const` object at namespace scope having internal linkage in C++ and
//     external linkage in C.** The rule differs between the two languages and a
//     `.h` does not say which it is, so neither is applied: a `const int kMax =
//     16;` in a header is descriptored globally in both dialects.
//   - **`#include` cycles, include guards and `#pragma once`.** They are
//     preprocessor state, and a file-local reader sees a `#define` and an
//     `#ifndef` with nothing joining them.
//   - **Anything about linking.** Which translation units are compiled into
//     which binary, which library a symbol resolves to at link time, and whether
//     two definitions of one external name are a link error are all facts about
//     a build, and there is no build here.
//   - **A multi-project monorepo.** `coord.Resolve` reads the manifests of one
//     directory, so every subproject's files carry the root CMakeLists.txt's
//     coordinate rather than their own. That is coord's boundary and not this
//     stanza's, and it is Rust's Cargo-workspace note verbatim.
package cc

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	sitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/facts"
)

// Lang is the value written to file.lang for every file this stanza handles,
// and it is one value for two languages on purpose.
//
// A `.h` does not say whether it is C or C++, so a per-extension tag would be a
// claim the stanza cannot make; worse, it would make the header/source edge of
// a pure-C project — `greeter.h` resolving into `greeter.c` — an edge joining
// two languages, which is exactly what the mixed-language corpus asserts never
// happens (test/integration/m6_test.go's noCrossLanguageEdges). One tag names
// what this reader actually is: one stanza spanning one linkage namespace.
const Lang = "cc"

// libcNamespace and stdNamespace are the names of the two foreign coordinates
// this stanza mints: the C standard library, which is reached by unqualified
// name, and the C++ standard library, which is reached through `std::`. Neither
// ships in a package this index can hold, and both are given a foreign
// coordinate so that a reference to one can never match a definition in the
// indexed repository.
const (
	libcNamespace = "libc"
	stdNamespace  = "std"
)

// Exts are the file extensions this stanza is registered under. They repeat
// coord.CCExts, which says why these eight and not the others.
var Exts = []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx"}

// cppExts are the extensions read with the C++ grammar: everything but `.c`,
// including `.h`. See the package comment.
var cppExts = map[string]bool{
	".cc": true, ".cpp": true, ".cxx": true,
	".h": true, ".hh": true, ".hpp": true, ".hxx": true,
}

//go:embed query_c.scm
var cQueryScheme string

//go:embed query_cpp.scm
var cppQueryScheme string

// dialect is one grammar and its compiled query. Two of them, because a
// tree-sitter query is compiled against one *sitter.Language and a query naming
// `namespace_definition` cannot compile against the C grammar.
type dialect struct {
	once    sync.Once
	load    func() *sitter.Language
	scheme  string
	name    string
	lang    *sitter.Language
	pool    *sitter.ParserPool
	query   *sitter.Query
	initErr error
}

func (d *dialect) init() {
	d.once.Do(func() {
		d.lang = d.load()
		if d.lang == nil {
			d.initErr = fmt.Errorf("cc: gotreesitter has no %s grammar", d.name)
			return
		}
		q, err := sitter.NewQuery(d.scheme, d.lang)
		if err != nil {
			d.initErr = fmt.Errorf("cc: compile query_%s.scm: %w", d.name, err)
			return
		}
		d.query = q
		d.pool = sitter.NewParserPool(d.lang)
	})
}

// Parser is the C/C++ stanza. Safe for concurrent use: each dialect's grammar
// and compiled query are immutable after its first Parse, and each parse checks
// a gotreesitter parser out of a pool.
type Parser struct {
	c   dialect
	cpp dialect
}

// New returns the C/C++ parser. It is cheap: a grammar is loaded and its query
// compiled on the first Parse that needs it, so a binary that never parses C++
// never decompresses the C++ grammar — and a query that fails to compile lands
// in ParseError rather than panicking at init.
func New() *Parser {
	return &Parser{
		c:   dialect{load: grammars.CLanguage, scheme: cQueryScheme, name: "c"},
		cpp: dialect{load: grammars.CppLanguage, scheme: cppQueryScheme, name: "cpp"},
	}
}

// Parse extracts one C or C++ file's facts. It never returns an error: a
// failure is reported in FileFacts.ParseError with File still populated, so the
// caller can tell "this file has no facts" from "this file was never seen".
func (p *Parser) Parse(filePath string, src []byte, c coord.Coord) facts.FileFacts {
	file := facts.File{Path: filePath, Lang: Lang, Coord: c}

	cpp := cppExts[strings.ToLower(filepath.Ext(filePath))]
	d := &p.c
	if cpp {
		d = &p.cpp
	}
	d.init()
	if d.initErr != nil {
		return facts.FileFacts{File: file, ParseError: d.initErr.Error()}
	}

	tree, err := d.pool.Parse(src)
	if err != nil {
		return facts.FileFacts{File: file, ParseError: err.Error()}
	}
	defer tree.Release()

	rel := relPath(c.Root, filePath)
	b := &builder{
		lang:       d.lang,
		cpp:        cpp,
		src:        src,
		coord:      c,
		rel:        rel,
		fileKey:    rel + "/",
		out:        facts.FileFacts{File: file},
		scopeByID:  map[facts.LocalID]scopeRec{},
		claimed:    map[span]bool{},
		descIndex:  map[string]facts.LocalID{},
		siteIndex:  map[string]bool{},
		defined:    map[string]bool{},
		defsByName: map[string][]defRec{},
		fieldTypes: map[string]*sitter.Node{},
		internal:   map[string]bool{},
		namespaces: map[string]bool{},
		typeNames:  map[string]bool{},
		aliases:    map[string]pathTarget{},
	}
	return b.build(d.query.Execute(tree))
}

// relPath is filePath relative to the package root, slash-separated. It is the
// key internal-linkage symbols and `#include` targets hang off, and it is
// deliberately the *file* path rather than coord.Coord.Namespace's directory:
// C's namespace is empty, and the only per-file scope the language has is the
// translation unit.
//
// A path outside Root, or a coordinate with no Root, falls back to the base
// name. That is the worst case of a fallback that only has to be unique enough
// to keep two static functions apart, and a file with no resolvable root has no
// siblings to be confused with.
func relPath(root, filePath string) string {
	if root != "" {
		if rel, err := filepath.Rel(root, filePath); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(filePath)
}

// span is an identifier's byte range, used to key the dedupe of overlapping
// captures.
type span struct{ start, end uint32 }

type scopeRec struct {
	id    facts.LocalID
	start uint32
	end   uint32
}

// defRec is a binding the mapper may need to look up again while resolving
// references: by name, within the scope it was declared in.
type defRec struct {
	occ   facts.LocalID
	scope facts.LocalID
	start uint32
	// typeNode is the type expression the binding was declared with, or nil
	// when the type names nothing a descriptor can reach (a primitive, `auto`,
	// a function pointer). Kept as a node rather than resolved eagerly because
	// resolution consults descIndex, which collectDefinitions is still filling.
	typeNode *sitter.Node
}

// pathTarget is what a written name resolved to: the coordinate and descriptor
// suffix its members hang off.
type pathTarget struct {
	coord  coord.Coord
	suffix string
}

type builder struct {
	lang    *sitter.Language
	cpp     bool
	src     []byte
	coord   coord.Coord
	rel     string
	fileKey string

	out       facts.FileFacts
	nextScope facts.LocalID
	nextOcc   facts.LocalID

	scopes    []scopeRec
	scopeByID map[facts.LocalID]scopeRec

	// claimed holds identifier ranges a definition or an import already owns, so
	// a reference pattern matching the same identifier is dropped.
	claimed map[span]bool
	// descIndex maps a *definition's* full descriptor to its occurrence — the
	// same-file half of what the link pass does across files.
	descIndex map[string]facts.LocalID
	// siteIndex holds every descriptor this file wrote down at all, definition
	// or prototype. Unqualified name resolution consults it rather than
	// descIndex, because a C file calling a function it only *declares* — which
	// is what a header include amounts to once the header is textual — still
	// knows which namespace the name is in.
	siteIndex map[string]bool
	// defined guards against emitting two definition rows with one descriptor:
	// a C `typedef struct X { … } X;` states a tag and an alias, and a C++
	// overload set states one name several times. Two rows the graph cannot
	// tell apart are one symbol as far as every join is concerned.
	defined    map[string]bool
	defsByName map[string][]defRec
	// fieldTypes maps a member name to the type it was declared with. File-wide
	// rather than scoped, because a member belongs to the class and not to the
	// method that reads it.
	fieldTypes map[string]*sitter.Node
	// internal holds the names this file gives internal linkage — `static` at
	// file scope, or a member of an unnamed namespace. They are descriptored
	// under fileKey; see the package comment.
	internal map[string]bool
	// namespaces holds every namespace name this file opens, and typeNames every
	// name it writes in a type position. They are the two pieces of file-local
	// evidence the qualifier rule runs on; see qualifierIsType.
	namespaces map[string]bool
	typeNames  map[string]bool
	// aliases holds what a C++ `using` or namespace alias bound.
	aliases map[string]pathTarget
}

func (b *builder) build(matches []sitter.QueryMatch) facts.FileFacts {
	b.collectScopes(matches)
	b.collectNamespaces(matches)
	b.collectLinkage(matches)
	b.collectTypeNames(matches)
	b.collectImports(matches)
	b.collectDefinitions(matches)
	b.collectReferences(matches)

	sort.SliceStable(b.out.Edges, func(i, j int) bool {
		a, c := b.out.Edges[i], b.out.Edges[j]
		if a.Kind != c.Kind {
			return a.Kind < c.Kind
		}
		if a.Source != c.Source {
			if a.Source.Vertex != c.Source.Vertex {
				return a.Source.Vertex < c.Source.Vertex
			}
			return a.Source.ID < c.Source.ID
		}
		if a.Target.Vertex != c.Target.Vertex {
			return a.Target.Vertex < c.Target.Vertex
		}
		return a.Target.ID < c.Target.ID
	})
	return b.out
}

// ------------------------------------------------------------------ scopes ---

// collectScopes turns @scope.* captures into the file's containment skeleton.
// Nesting is pure byte containment: sorted by (start ascending, end
// descending), a stack of open scopes yields each scope's parent with no
// language knowledge at all.
func (b *builder) collectScopes(matches []sitter.QueryMatch) {
	type cand struct {
		kind string
		node *sitter.Node
	}
	var cands []cand
	for _, m := range matches {
		if root, _, ok := roots(m, "scope."); ok {
			cands = append(cands, cand{kind: suffixAfter(root.Name, "scope."), node: root.Node})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, c := cands[i].node, cands[j].node
		if a.StartByte() != c.StartByte() {
			return a.StartByte() < c.StartByte()
		}
		if a.EndByte() != c.EndByte() {
			return a.EndByte() > c.EndByte()
		}
		return cands[i].kind < cands[j].kind
	})

	var stack []scopeRec
	seen := map[span]bool{}
	for _, cd := range cands {
		s := span{cd.node.StartByte(), cd.node.EndByte()}
		if seen[s] {
			continue // two patterns matched the same node; the first kind wins
		}
		seen[s] = true

		for len(stack) > 0 && stack[len(stack)-1].end <= s.start {
			stack = stack[:len(stack)-1]
		}
		parent := facts.NoID
		if len(stack) > 0 {
			parent = stack[len(stack)-1].id
		}

		b.nextScope++
		rec := scopeRec{id: b.nextScope, start: s.start, end: s.end}
		b.out.Scopes = append(b.out.Scopes, facts.Scope{
			ID:         rec.id,
			Kind:       cd.kind,
			RangeStart: int(s.start),
			RangeEnd:   int(s.end),
			Parent:     parent,
		})
		if parent != facts.NoID {
			b.edge(facts.EdgeContains, facts.ScopeRef(parent), facts.ScopeRef(rec.id))
		}
		b.scopes = append(b.scopes, rec)
		b.scopeByID[rec.id] = rec
		stack = append(stack, rec)
	}
}

// enclosingScope returns the innermost scope containing [start, end).
func (b *builder) enclosingScope(start, end uint32) facts.LocalID {
	best := facts.NoID
	var bestStart uint32
	bestEnd := ^uint32(0)
	for _, s := range b.scopes {
		if s.start > start || s.end < end {
			continue
		}
		if best == facts.NoID || s.start > bestStart || (s.start == bestStart && s.end < bestEnd) {
			best, bestStart, bestEnd = s.id, s.start, s.end
		}
	}
	return best
}

func (b *builder) scopeRange(id facts.LocalID) (uint32, uint32, bool) {
	s, ok := b.scopeByID[id]
	return s.start, s.end, ok
}

// fileScope is the scope the whole file is, or NoID for an empty file.
func (b *builder) fileScope() facts.LocalID {
	if len(b.scopes) == 0 {
		return facts.NoID
	}
	return b.scopes[0].id
}

// -------------------------------------------------------------- namespaces ---

// collectNamespaces emits this file's `package` definitions. There are two
// families and they answer two different questions.
//
// The *namespace* family is what a C++ `namespace X { }` declares, plus — for
// every file, in both dialects — the global namespace with an empty suffix,
// which is the only namespace C has. It is what a qualified reference and a
// `using` join against.
//
// The *path* family is one definition per suffix of this file's own path, and
// it exists because `#include` names a path that the build system resolves. The
// package comment argues it; here it is simply emitted, with the zero-width
// point at byte 0 that every language before Java had to use for a package no
// syntax names.
func (b *builder) collectNamespaces(matches []sitter.QueryMatch) {
	type cand struct{ decl, name *sitter.Node }
	var cands []cand
	program := false
	for _, m := range matches {
		root, name, ok := roots(m, "definition.package")
		if !ok {
			continue
		}
		if root.Node.Type(b.lang) == "translation_unit" {
			program = true
			continue
		}
		if name != nil {
			cands = append(cands, cand{decl: root.Node, name: name.Node})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		return cands[i].name.StartByte() < cands[j].name.StartByte()
	})

	if program {
		b.definePackage(b.fileScope(), "", "", 0, 0)
		b.definePathPackages()
	}
	for _, cd := range cands {
		suffix := b.namespaceOf(cd.name)
		if suffix == "" {
			continue
		}
		b.claimSubtree(cd.name)
		for _, seg := range b.nameSegments(cd.name) {
			b.namespaces[seg] = true
		}
		b.definePackage(b.enclosingScope(cd.name.StartByte(), cd.name.EndByte()),
			b.containerSuffix(cd.decl)+suffix, lastSegment(strings.TrimSuffix(suffix, "/")),
			cd.name.StartByte(), cd.name.EndByte())
	}
}

// definePathPackages emits one `package` definition per suffix of this file's
// path — the file-local half of the `-I` search this stanza cannot run.
func (b *builder) definePathPackages() {
	segs := strings.Split(b.rel, "/")
	if len(segs) == 0 || !strings.Contains(segs[len(segs)-1], ".") {
		// A path whose last segment has no extension could be spelled by a C++
		// namespace, and a descriptor that two schemes can render is one the
		// link pass will join wrongly.
		return
	}
	name := segs[len(segs)-1]
	for i := len(segs) - 1; i >= 0; i-- {
		b.definePackage(b.fileScope(), strings.Join(segs[i:], "/")+"/", name, 0, 0)
	}
}

// definePackage emits one namespace definition and indexes it.
func (b *builder) definePackage(scope facts.LocalID, suffix, name string, start, end uint32) {
	desc := facts.Descriptor{Prefix: b.coord, Suffix: suffix}
	if b.defined[desc.String()] {
		return
	}
	b.defined[desc.String()] = true
	if name == "" {
		name = packageName(b.coord, suffix)
	}
	occ := b.addOccurrenceIn(scope, desc, facts.RoleDefinition, facts.KindPackage, name, start, end)
	b.edge(facts.EdgeDefines, facts.FileRef(), facts.OccurrenceRef(occ))
	if _, dup := b.descIndex[desc.String()]; !dup {
		b.descIndex[desc.String()] = occ
	}
	b.siteIndex[desc.String()] = true
}

// namespaceSuffix is the namespace descriptor in effect at n: the enclosing
// `namespace … { }` blocks, or "" — the global namespace — when there are none,
// which is every position in every C file.
func (b *builder) namespaceSuffix(n *sitter.Node) string {
	for p := n; p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "linkage_specification":
			// `extern "C"`: the entity is reachable from C, and C has one
			// namespace. See the package comment.
			return ""
		case "namespace_definition":
			name := p.ChildByFieldName("name", b.lang)
			if name == nil {
				return b.fileKey
			}
			return b.namespaceSuffix(p.Parent()) + b.namespaceOf(name)
		}
	}
	return ""
}

// namespaceOf renders a namespace name node — a plain identifier or a C++17
// `a::b` nested specifier — as a slash-separated, slash-terminated descriptor.
func (b *builder) namespaceOf(n *sitter.Node) string {
	segs := b.nameSegments(n)
	if len(segs) == 0 {
		return ""
	}
	return strings.Join(segs, "/") + "/"
}

// nameSegments flattens a name node into its segments.
func (b *builder) nameSegments(n *sitter.Node) []string {
	if n == nil {
		return nil
	}
	switch n.Type(b.lang) {
	case "namespace_identifier", "identifier", "type_identifier", "field_identifier",
		"destructor_name", "operator_name", "primitive_type":
		if t := strings.TrimSpace(b.text(n)); t != "" {
			return []string{t}
		}
	case "template_type":
		return b.nameSegments(n.ChildByFieldName("name", b.lang))
	case "nested_namespace_specifier", "qualified_identifier":
		var segs []string
		for i := 0; i < n.NamedChildCount(); i++ {
			segs = append(segs, b.nameSegments(n.NamedChild(i))...)
		}
		return segs
	}
	return nil
}

// ----------------------------------------------------------------- linkage ---

// collectLinkage pre-scans for the names this file gives internal linkage, so
// that every later descriptor — the definition's, the prototype's and every
// reference's — agrees on the container.
//
// The pre-scan is needed rather than a per-site decision because C lets the two
// disagree: `static void trim(char *);` at the top of a file followed by
// `void trim(char *s) { … }` with no `static` gives `trim` internal linkage all
// the same, and only the earlier declaration says so.
func (b *builder) collectLinkage(matches []sitter.QueryMatch) {
	for _, m := range matches {
		root, _, ok := roots(m, "definition.function")
		if !ok {
			root, _, ok = roots(m, "declaration")
		}
		if !ok {
			continue
		}
		n := root.Node
		if !b.isFileScope(n) {
			continue
		}
		if !b.hasStorageClass(n, "static") && !b.inUnnamedNamespace(n) {
			continue
		}
		for _, d := range b.declaratorsOf(n) {
			if nm := b.declaratorNameText(d); nm != "" {
				b.internal[nm] = true
			}
		}
	}
	// Everything inside an unnamed namespace, whatever its shape.
	for _, m := range matches {
		root, _, ok := roots(m, "definition.")
		if !ok || !b.inUnnamedNamespace(root.Node) {
			continue
		}
		for _, d := range b.declaratorsOf(root.Node) {
			if nm := b.declaratorNameText(d); nm != "" {
				b.internal[nm] = true
			}
		}
	}
}

// collectTypeNames records every name this file writes in a type position.
//
// The evidence is the grammar's own: tree-sitter labels an identifier
// `type_identifier` where a type is written and `namespace_identifier` where a
// namespace is, so a name that appears as the first is one this file has seen
// used as a type. It is what lets `Mood::Calm` be read as a member of a type
// rather than as a symbol in a namespace, in a file that also writes `Mood m;`
// — and it is evidence rather than a convention, which is the test Java's
// stanza set for this kind of rule.
func (b *builder) collectTypeNames(matches []sitter.QueryMatch) {
	var mark func(n *sitter.Node, depth int)
	mark = func(n *sitter.Node, depth int) {
		if n == nil || depth > 8 {
			return
		}
		if n.Type(b.lang) == "type_identifier" {
			if t := strings.TrimSpace(b.text(n)); t != "" {
				b.typeNames[t] = true
			}
			return
		}
		for i := 0; i < n.NamedChildCount(); i++ {
			mark(n.NamedChild(i), depth+1)
		}
	}
	for _, m := range matches {
		for i := range m.Captures {
			c := &m.Captures[i]
			if c.Name == "name" || c.Name == "reference.type" || strings.HasPrefix(c.Name, "definition.type") {
				mark(c.Node, 0)
			}
		}
	}
}

// isFileScope reports whether n sits at translation-unit or namespace level —
// never inside a class body or a function body, where `static` means storage
// duration or "no `this`" rather than internal linkage.
func (b *builder) isFileScope(n *sitter.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "translation_unit", "declaration_list", "linkage_specification",
			"namespace_definition", "template_declaration":
		case "field_declaration_list", "compound_statement", "function_definition",
			"class_specifier", "struct_specifier", "union_specifier":
			return false
		default:
			return false
		}
	}
	return true
}

func (b *builder) inUnnamedNamespace(n *sitter.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Type(b.lang) == "namespace_definition" {
			return p.ChildByFieldName("name", b.lang) == nil
		}
	}
	return false
}

// hasStorageClass reports whether n carries the given storage-class specifier.
func (b *builder) hasStorageClass(n *sitter.Node, want string) bool {
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c.Type(b.lang) == "storage_class_specifier" && b.text(c) == want {
			return true
		}
	}
	return false
}

// ----------------------------------------------------------------- imports ---

// collectImports records `#include` paths and C++ `using` declarations.
//
// Import *edges* are cross-file and therefore the link pass's (§4.4); what is
// extracted is the occurrence, whose descriptor names the imported package.
func (b *builder) collectImports(matches []sitter.QueryMatch) {
	var decls []*sitter.Node
	for _, m := range matches {
		if root, _, ok := roots(m, "import"); ok {
			decls = append(decls, root.Node)
		}
	}
	sort.SliceStable(decls, func(i, j int) bool { return decls[i].StartByte() < decls[j].StartByte() })
	for _, decl := range decls {
		switch decl.Type(b.lang) {
		case "preproc_include":
			b.includeDirective(decl)
		case "using_declaration":
			b.usingDeclaration(decl)
		case "namespace_alias_definition":
			b.namespaceAlias(decl)
		}
	}
}

// includeDirective emits the `package` reference an `#include` amounts to: the
// path as written, rendered as a namespace descriptor so it joins the path
// definitions the included file emits for itself.
func (b *builder) includeDirective(decl *sitter.Node) {
	path := decl.ChildByFieldName("path", b.lang)
	if path == nil {
		return
	}
	raw := strings.Trim(b.text(path), `"<>`)
	raw = strings.TrimPrefix(filepath.ToSlash(raw), "./")
	if raw == "" {
		return
	}
	segs := strings.Split(raw, "/")
	last := segs[len(segs)-1]
	if !strings.Contains(last, ".") {
		// `#include <string>` and friends: no repository file is spelled that
		// way, and a dotless path-keyed descriptor could be spelled by a C++
		// namespace.
		return
	}
	b.claimed[span{path.StartByte(), path.EndByte()}] = true
	b.addOccurrence(facts.Descriptor{Prefix: b.coord, Suffix: raw + "/"},
		facts.RoleReference, facts.KindPackage, last, path.StartByte(), path.EndByte())
}

// usingDeclaration handles `using namespace X;` — a package reference — and
// `using X::y;`, which binds a name and is recorded in the alias table.
func (b *builder) usingDeclaration(decl *sitter.Node) {
	inner := firstNamedChild(decl)
	if inner == nil {
		return
	}
	switch inner.Type(b.lang) {
	case "identifier", "namespace_identifier":
		name := b.text(inner)
		b.claimed[span{inner.StartByte(), inner.EndByte()}] = true
		b.addOccurrence(facts.Descriptor{Prefix: b.coord, Suffix: name + "/"},
			facts.RoleReference, facts.KindPackage, name, inner.StartByte(), inner.EndByte())
	case "qualified_identifier":
		segs := b.nameSegments(inner)
		if len(segs) < 2 {
			return
		}
		name := segs[len(segs)-1]
		ns := strings.Join(segs[:len(segs)-1], "/") + "/"
		b.claimSubtree(inner)
		b.addOccurrence(facts.Descriptor{Prefix: b.coord, Suffix: ns},
			facts.RoleReference, facts.KindPackage, segs[len(segs)-2],
			inner.StartByte(), inner.StartByte()+uint32(len(segs[len(segs)-2])))
		b.aliases[name] = pathTarget{coord: b.coord, suffix: ns + name + "#"}
	}
}

// namespaceAlias records `namespace ns = a::b;`, which renames a namespace and
// nothing else.
func (b *builder) namespaceAlias(decl *sitter.Node) {
	name := b.fieldText(decl, "name")
	target := lastNamedChild(decl)
	if name == "" || target == nil {
		return
	}
	segs := b.nameSegments(target)
	if len(segs) == 0 {
		return
	}
	ns := strings.Join(segs, "/") + "/"
	b.claimSubtree(decl)
	b.addOccurrence(facts.Descriptor{Prefix: b.coord, Suffix: ns},
		facts.RoleReference, facts.KindPackage, segs[len(segs)-1],
		target.StartByte(), target.EndByte())
	b.aliases[name] = pathTarget{coord: b.coord, suffix: ns}
}

// ------------------------------------------------------------- definitions ---

// declCand is one declarator site: the node the query matched, the declarator
// inside it, and the identifier that declarator bottoms out in.
type declCand struct {
	capture string
	node    *sitter.Node
	decl    *sitter.Node
	name    *sitter.Node
}

func (b *builder) collectDefinitions(matches []sitter.QueryMatch) {
	var cands []declCand
	for _, m := range matches {
		root, name, ok := roots(m, "definition.")
		capture := ""
		if ok {
			capture = suffixAfter(root.Name, "definition.")
			if capture == "package" {
				continue
			}
		} else if root, name, ok = roots(m, "declaration"); ok {
			capture = "declaration"
		} else {
			continue
		}
		if name != nil {
			cands = append(cands, declCand{capture: capture, node: root.Node, name: name.Node})
			continue
		}
		for _, d := range b.declaratorsOf(root.Node) {
			if nm := b.declaratorName(d); nm != nil {
				cands = append(cands, declCand{capture: capture, node: root.Node, decl: d, name: nm})
			}
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, c := cands[i].name, cands[j].name
		if a.StartByte() != c.StartByte() {
			return a.StartByte() < c.StartByte()
		}
		return cands[i].capture < cands[j].capture
	})

	for _, cd := range cands {
		s := span{cd.name.StartByte(), cd.name.EndByte()}
		if b.claimed[s] {
			continue
		}
		name := strings.TrimSpace(b.text(cd.name))
		if name == "" || cd.name.Type(b.lang) == "destructor_name" {
			// A destructor is not emitted at all, and the reason is link's
			// `implements` derivation rather than tidiness. C++ declares a
			// destructor for *every* class whether the author writes one or not,
			// and it is named after its class — so `~Speaker().` in an abstract
			// base's method set is a member no implementer can ever have, and
			// method-set containment (store/sqlc/query.sql) would fail for every
			// C++ interface that declares the virtual destructor good C++ always
			// declares. Emitting a member whose presence depends on whether the
			// author wrote a line the compiler writes for them would make the
			// derivation ask a question about C++ that has no answer. The cost
			// is that `~Speaker()` is not navigable, which no call site writes
			// anyway.
			continue
		}
		kind, role := b.classify(cd)
		if kind == "" {
			continue
		}
		suffix := b.declaratorSuffix(kind, cd, name)
		b.claimed[s] = true

		desc := facts.Descriptor{Prefix: b.coord, Suffix: suffix}
		if role == facts.RoleDefinition && b.defined[desc.String()] {
			// A second definition row with a descriptor this file already
			// defines: a typedef repeating its struct tag, or another member of
			// an overload set. One symbol, as far as every join is concerned.
			continue
		}
		occ := b.addOccurrence(desc, role, kind, name, s.start, s.end)
		b.siteIndex[desc.String()] = true

		if role == facts.RoleDefinition {
			b.defined[desc.String()] = true
			b.edge(facts.EdgeDefines, facts.FileRef(), facts.OccurrenceRef(occ))
			if _, dup := b.descIndex[desc.String()]; !dup {
				b.descIndex[desc.String()] = occ
			}
		}

		typeNode := b.declaredTypeNode(cd)
		b.defsByName[name] = append(b.defsByName[name], defRec{
			occ: occ, scope: b.occurrence(occ).Scope, start: s.start, typeNode: typeNode,
		})
		if typeNode != nil && kind == facts.KindField {
			b.fieldTypes[name] = typeNode
		}
	}
}

// classify decides a declarator site's neutral-core kind and its role. It is
// the header/source rule of the package comment, applied.
func (b *builder) classify(cd declCand) (kind string, role facts.Role) {
	switch cd.capture {
	case "type":
		switch cd.node.Type(b.lang) {
		case "class_specifier", "struct_specifier", "union_specifier", "enum_specifier":
			if cd.node.ChildByFieldName("body", b.lang) == nil {
				// `struct Greeter;` — a forward declaration names a type it does
				// not define.
				return facts.KindType, facts.RoleReference
			}
			if b.isAbstract(cd.node) {
				return facts.KindInterface, facts.RoleDefinition
			}
			return facts.KindType, facts.RoleDefinition
		}
		return facts.KindType, facts.RoleDefinition
	case "constant":
		return facts.KindConstant, facts.RoleDefinition
	case "parameter":
		if !b.isBindingParameter(cd.node) {
			// A parameter of a *prototype*. C explicitly permits a declaration's
			// parameter names to be omitted, or to differ from the definition's,
			// so they bind nothing and name nothing the program can refer to.
			// Emitting them would put a `(g)` occurrence at the global namespace
			// of every header in the corpus, all with one descriptor.
			return "", facts.RoleDefinition
		}
		return facts.KindParameter, facts.RoleDefinition
	case "field":
		// A class member. `int a;` is a field and `void m();` is a method, and
		// the declarator chain is what says which.
		if b.isFunctionDeclarator(cd.decl) {
			return facts.KindMethod, facts.RoleDefinition
		}
		return facts.KindField, facts.RoleDefinition
	case "function":
		if b.isMember(cd) {
			return facts.KindMethod, facts.RoleDefinition
		}
		return facts.KindFunction, facts.RoleDefinition
	case "declaration":
		member := b.inClassBody(cd.node)
		switch {
		case b.isFunctionDeclarator(cd.decl):
			if member {
				// A member declared in the class body: the body *is* the
				// definition of what the class has.
				return facts.KindMethod, facts.RoleDefinition
			}
			if b.isMember(cd) {
				return facts.KindMethod, facts.RoleReference
			}
			// A prototype: it declares a function defined elsewhere.
			return facts.KindFunction, facts.RoleReference
		case member:
			return facts.KindField, facts.RoleDefinition
		case b.hasStorageClass(cd.node, "extern") && cd.decl != nil &&
			cd.decl.Type(b.lang) != "init_declarator":
			// `extern int verbose;` declares a variable defined elsewhere.
			return facts.KindVariable, facts.RoleReference
		default:
			return facts.KindVariable, facts.RoleDefinition
		}
	}
	return "", facts.RoleDefinition
}

// isMember reports whether a declarator site names a member of a type: either
// it sits in a class body, or its declarator is qualified (`Greeter::greet`).
func (b *builder) isMember(cd declCand) bool {
	if b.inClassBody(cd.node) {
		return true
	}
	q := b.qualifiedOf(cd.decl)
	return q != nil && len(b.qualifierSegments(q)) > 0
}

// isBindingParameter reports whether a parameter declaration is part of a
// declarator that actually binds it — a function definition or a lambda — as
// opposed to a prototype, whose parameter names the compiler discards. The
// first enclosing declarator settles it, so a function-pointer parameter's own
// parameters stop at their `parameter_declaration` and bind nothing either.
func (b *builder) isBindingParameter(n *sitter.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "function_definition", "lambda_expression":
			return true
		case "declaration", "field_declaration", "parameter_declaration",
			"type_definition", "alias_declaration", "translation_unit":
			return false
		}
	}
	return false
}

func (b *builder) inClassBody(n *sitter.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "field_declaration_list":
			return true
		case "compound_statement", "translation_unit", "namespace_definition":
			return false
		}
	}
	return false
}

// isAbstract reports whether a class body declares at least one pure virtual
// member function and no data members — C++'s spelling of an interface, and the
// only shape link's `implements` derivation can key off.
func (b *builder) isAbstract(n *sitter.Node) bool {
	body := n.ChildByFieldName("body", b.lang)
	if body == nil {
		return false
	}
	pure := false
	for i := 0; i < body.NamedChildCount(); i++ {
		c := body.NamedChild(i)
		switch c.Type(b.lang) {
		case "field_declaration":
			decl := c.ChildByFieldName("declarator", b.lang)
			if !b.isFunctionDeclarator(decl) {
				return false // a data member: not an interface
			}
			if v := c.ChildByFieldName("default_value", b.lang); v != nil && b.text(v) == "0" {
				pure = true
			}
		case "function_definition", "declaration", "access_specifier", "comment",
			"alias_declaration", "type_definition", "using_declaration":
		default:
			return false
		}
	}
	return pure
}

// declaratorSuffix builds the SCIP descriptor suffix for a declarator site.
func (b *builder) declaratorSuffix(kind string, cd declCand, name string) string {
	if q := b.qualifiedOf(cd.decl); q != nil && len(b.qualifierSegments(q)) > 0 {
		// An out-of-line definition writes its own container. The rule is the
		// package comment's: innermost qualifier is a type, the rest namespaces.
		return b.namespaceSuffix(cd.node) + b.qualifierSuffix(q) + name + terminatorFor(kind)
	}
	container := b.containerSuffix(cd.node)
	if b.internal[name] && b.isFileScope(cd.node) {
		container = b.fileKey
	}
	if kind == facts.KindParameter {
		return container + "(" + name + ")"
	}
	return container + name + terminatorFor(kind)
}

func terminatorFor(kind string) string {
	switch kind {
	case facts.KindFunction, facts.KindMethod:
		return "()."
	case facts.KindType, facts.KindInterface:
		return "#"
	case facts.KindPackage:
		return "/"
	default:
		return "."
	}
}

// qualifierSuffix renders the scope half of a qualified declarator. See the
// package comment for why the innermost segment is a type.
func (b *builder) qualifierSuffix(q *sitter.Node) string {
	return b.joinScope(b.qualifierSegments(q), true)
}

// containerSuffix returns the descriptor suffix of the nearest enclosing named
// container of n — a namespace, a type or a callable — or "" for the global
// namespace. A block, a declaration list, a template header and an
// `extern "C"` are transparent.
func (b *builder) containerSuffix(n *sitter.Node) string {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "linkage_specification":
			return ""
		case "namespace_definition":
			name := p.ChildByFieldName("name", b.lang)
			if name == nil {
				return b.fileKey
			}
			return b.containerSuffix(p) + b.namespaceOf(name)
		case "class_specifier", "struct_specifier", "union_specifier", "enum_specifier":
			if nm := b.fieldText(p, "name"); nm != "" {
				return b.containerSuffix(p) + nm + "#"
			}
			return b.containerSuffix(p) + coord.Unknown + "#"
		case "function_definition":
			return b.ownFunctionSuffix(p)
		case "lambda_expression":
			return b.containerSuffix(p) + coord.Unknown + "()."
		}
	}
	return ""
}

// ownFunctionSuffix is the descriptor suffix of the function definition p is,
// which is what its locals and parameters hang off.
func (b *builder) ownFunctionSuffix(p *sitter.Node) string {
	decl := p.ChildByFieldName("declarator", b.lang)
	name := b.declaratorNameText(p)
	if name == "" {
		return b.containerSuffix(p)
	}
	if q := b.qualifiedOf(decl); q != nil && len(b.qualifierSegments(q)) > 0 {
		return b.namespaceSuffix(p) + b.qualifierSuffix(q) + name + "()."
	}
	container := b.containerSuffix(p)
	if b.internal[name] && b.isFileScope(p) {
		container = b.fileKey
	}
	return container + name + "()."
}

// typeSuffix is the descriptor suffix of the type declaration n sits innermost
// inside — what resolves `this`, and what an in-class member's siblings hang
// off. For an out-of-line member definition it is the qualifier instead, which
// is the only place the enclosing type is written down.
func (b *builder) typeSuffix(n *sitter.Node) string {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type(b.lang) {
		case "class_specifier", "struct_specifier", "union_specifier":
			if nm := b.fieldText(p, "name"); nm != "" {
				return b.containerSuffix(p) + nm + "#"
			}
			return b.containerSuffix(p) + coord.Unknown + "#"
		case "function_definition":
			q := b.qualifiedOf(p.ChildByFieldName("declarator", b.lang))
			if q != nil && len(b.qualifierSegments(q)) > 0 {
				return b.namespaceSuffix(p) + b.qualifierSuffix(q)
			}
		}
	}
	return ""
}

// ------------------------------------------------------------- declarators ---

// declaratorsOf returns the declarator children of a declaration site. One
// declaration may hold several — `int a, b;` — and each names a symbol.
func (b *builder) declaratorsOf(n *sitter.Node) []*sitter.Node {
	if n == nil {
		return nil
	}
	var out []*sitter.Node
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil || !c.IsNamed() {
			continue
		}
		if n.FieldNameForChild(i, b.lang) == "declarator" {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		if d := n.ChildByFieldName("declarator", b.lang); d != nil {
			out = append(out, d)
		}
	}
	return out
}

// declaratorName walks a declarator chain to the identifier it bottoms out in.
//
// The walk is what query.scm cannot do: C nests the name arbitrarily deep
// inside pointer, array, reference, function and parenthesised declarators —
// `char *(*table[4])(void)` — so there is no field path a pattern can name and
// no bounded set of patterns that covers it.
func (b *builder) declaratorName(n *sitter.Node) *sitter.Node {
	for depth := 0; n != nil && depth < 32; depth++ {
		switch n.Type(b.lang) {
		case "identifier", "field_identifier", "type_identifier", "namespace_identifier",
			"destructor_name", "operator_name":
			return n
		case "qualified_identifier":
			n = n.ChildByFieldName("name", b.lang)
		case "template_type", "template_function":
			n = n.ChildByFieldName("name", b.lang)
		default:
			next := n.ChildByFieldName("declarator", b.lang)
			if next == nil {
				next = b.firstDeclaratorChild(n)
			}
			if next == nil {
				return nil
			}
			n = next
		}
	}
	return nil
}

// firstDeclaratorChild is the fallback for the declarator nodes that carry
// their inner declarator as an unlabelled child — `reference_declarator` is the
// one the C++ grammar spells that way.
func (b *builder) firstDeclaratorChild(n *sitter.Node) *sitter.Node {
	for i := 0; i < n.NamedChildCount(); i++ {
		switch c := n.NamedChild(i); c.Type(b.lang) {
		case "identifier", "field_identifier", "type_identifier", "destructor_name",
			"operator_name", "qualified_identifier", "function_declarator",
			"pointer_declarator", "reference_declarator", "array_declarator",
			"parenthesized_declarator", "init_declarator", "template_type":
			return c
		}
	}
	return nil
}

func (b *builder) declaratorNameText(n *sitter.Node) string {
	for _, d := range b.declaratorsOf(n) {
		if nm := b.declaratorName(d); nm != nil {
			return strings.TrimSpace(b.text(nm))
		}
	}
	return ""
}

// isFunctionDeclarator reports whether a declarator chain passes through a
// function declarator, which is what separates a prototype from a variable.
func (b *builder) isFunctionDeclarator(n *sitter.Node) bool {
	for depth := 0; n != nil && depth < 32; depth++ {
		if n.Type(b.lang) == "function_declarator" {
			return true
		}
		next := n.ChildByFieldName("declarator", b.lang)
		if next == nil {
			next = b.firstDeclaratorChild(n)
		}
		n = next
	}
	return false
}

// qualifiedOf is the qualified name a declarator writes — `greeter::Greeter::greet`
// — or nil when the declarator names an unqualified symbol.
//
// The whole node is returned rather than its `scope` child because the grammar
// nests `a::b::c` to the *right*: the outermost node's scope is `a` alone, and
// the qualifier is every segment but the last.
func (b *builder) qualifiedOf(n *sitter.Node) *sitter.Node {
	for depth := 0; n != nil && depth < 32; depth++ {
		if n.Type(b.lang) == "qualified_identifier" {
			return n
		}
		next := n.ChildByFieldName("declarator", b.lang)
		if next == nil {
			next = b.firstDeclaratorChild(n)
		}
		n = next
	}
	return nil
}

// qualifierSegments is every segment of a qualified name but the last.
func (b *builder) qualifierSegments(q *sitter.Node) []string {
	segs := b.nameSegments(q)
	if len(segs) < 2 {
		return nil
	}
	return segs[:len(segs)-1]
}

// innermostQualifier is the node spelling the last segment of a qualified
// name's scope — the bytes a `type` reference to the enclosing class covers.
func (b *builder) innermostQualifier(q *sitter.Node) *sitter.Node {
	var scope *sitter.Node
	for depth := 0; q != nil && depth < 32; depth++ {
		if q.Type(b.lang) != "qualified_identifier" {
			break
		}
		scope = q.ChildByFieldName("scope", b.lang)
		q = q.ChildByFieldName("name", b.lang)
	}
	return b.leafName(scope)
}

// declaredTypeNode recovers the type expression a binding was declared with —
// enough to name the members reached through it, with no type inference at all.
// nil means "names nothing a descriptor can reach", which downstream becomes
// SCIP's "." rather than a guess.
func (b *builder) declaredTypeNode(cd declCand) *sitter.Node {
	return b.unwrapType(cd.node.ChildByFieldName("type", b.lang))
}

// unwrapType reduces a type expression to the single node that names a type, or
// nil when it names none. A primitive, `auto`, a union of qualifiers and a
// function-pointer type all name nothing a descriptor can reach.
func (b *builder) unwrapType(t *sitter.Node) *sitter.Node {
	for depth := 0; t != nil && depth < 16; depth++ {
		switch t.Type(b.lang) {
		case "type_identifier", "qualified_identifier":
			return t
		case "template_type":
			return t.ChildByFieldName("name", b.lang)
		case "struct_specifier", "union_specifier", "class_specifier", "enum_specifier":
			return t.ChildByFieldName("name", b.lang)
		case "type_descriptor", "sized_type_specifier":
			t = t.ChildByFieldName("type", b.lang)
		default:
			return nil
		}
	}
	return nil
}

// -------------------------------------------------------------- references ---

type refRec struct {
	role     string
	node     *sitter.Node
	nameNode *sitter.Node
}

// refPriority ranks the roles that can capture the same identifier.
func refPriority(role string) int {
	switch role {
	case "call":
		return 3
	case "type", "scoped":
		return 2
	default:
		return 1
	}
}

func (b *builder) moreSpecific(cand, prev refRec) bool {
	if p, c := refPriority(prev.role), refPriority(cand.role); p != c {
		return c > p
	}
	pw := prev.node.EndByte() - prev.node.StartByte()
	cw := cand.node.EndByte() - cand.node.StartByte()
	if pw != cw {
		return cw > pw
	}
	return cand.role < prev.role
}

func (b *builder) collectReferences(matches []sitter.QueryMatch) {
	best := map[span]refRec{}
	add := func(r refRec) {
		if r.nameNode == nil {
			return
		}
		s := span{r.nameNode.StartByte(), r.nameNode.EndByte()}
		if b.claimed[s] {
			return
		}
		if prev, dup := best[s]; dup && !b.moreSpecific(r, prev) {
			return
		}
		best[s] = r
	}

	for _, m := range matches {
		root, name, ok := roots(m, "reference.")
		if !ok {
			continue
		}
		role := suffixAfter(root.Name, "reference.")
		switch role {
		case "scoped":
			// A qualified name, in one of two positions that look identical.
			//
			// In a *declarator* — `std::string Greeter::greet() const { … }` —
			// the leaf is the definition's own name and collectDefinitions has
			// already claimed it. What is left is the qualifier, and it names
			// the enclosing type: a `type` reference, which is the edge that
			// takes an out-of-line member back to the class body that declared
			// it. Reading it as a namespace instead would mint a `package`
			// reference for a class.
			//
			// Everywhere else the scope really is a namespace (or the standard
			// library) and the leaf is a symbol in it.
			scope := root.Node.ChildByFieldName("scope", b.lang)
			leaf := b.leafName(root.Node.ChildByFieldName("name", b.lang))
			if leaf != nil && b.claimed[span{leaf.StartByte(), leaf.EndByte()}] && scope != nil {
				add(refRec{role: "qualifier", node: root.Node, nameNode: b.innermostQualifier(root.Node)})
				continue
			}
			if segs := b.nameSegments(scope); len(segs) > 0 && !b.qualifierIsType(segs[len(segs)-1], false) {
				// The scope names a namespace. When it names a *type* — an enum
				// case, a static member — there is no package to reference and
				// the `type` half of the name is the qualifier reference below.
				add(refRec{role: "package", node: scope, nameNode: scope})
			}
			add(refRec{role: "scoped", node: root.Node, nameNode: leaf})
		case "type":
			nn := root.Node
			if name != nil {
				nn = name.Node
			}
			add(refRec{role: role, node: nn, nameNode: b.leafName(nn)})
		default:
			nn := root.Node
			if name != nil {
				nn = name.Node
			}
			add(refRec{role: role, node: root.Node, nameNode: b.leafName(nn)})
		}
	}

	spans := make([]span, 0, len(best))
	for s := range best {
		spans = append(spans, s)
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end < spans[j].end
	})

	for _, s := range spans {
		r := best[s]
		name := strings.TrimSpace(b.text(r.nameNode))
		if name == "" {
			continue
		}
		desc, kind := b.referenceDescriptor(r, name)
		occ := b.addOccurrence(desc, facts.RoleReference, kind, name, s.start, s.end)

		// Same-file resolution (§4.3): the target definition is in this CST, so
		// the edge is extracted rather than left to the link pass.
		if def, ok := b.descIndex[desc.String()]; ok && def != occ {
			b.edge(facts.EdgeReferencesLocal, facts.OccurrenceRef(occ), facts.OccurrenceRef(def))
		}
	}
}

// leafName reduces a name node to the identifier an occurrence should cover.
func (b *builder) leafName(n *sitter.Node) *sitter.Node {
	for depth := 0; n != nil && depth < 16; depth++ {
		switch n.Type(b.lang) {
		case "qualified_identifier":
			n = n.ChildByFieldName("name", b.lang)
		case "template_type", "template_function":
			n = n.ChildByFieldName("name", b.lang)
		default:
			return n
		}
	}
	return nil
}

func (b *builder) referenceDescriptor(r refRec, name string) (facts.Descriptor, string) {
	switch r.role {
	case "call":
		return b.callDescriptor(r, name)
	case "read":
		return b.readDescriptor(r, name)
	case "package":
		segs := b.nameSegments(r.nameNode)
		if len(segs) == 0 {
			segs = []string{name}
		}
		if segs[0] == stdNamespace && !b.namespaces[stdNamespace] {
			return facts.Descriptor{
				Prefix: coord.Foreign(b.coord.Scheme, b.coord.Manager, stdNamespace),
				Suffix: namespacePath(segs[1:]),
			}, facts.KindPackage
		}
		return facts.Descriptor{Prefix: b.coord, Suffix: namespacePath(segs)}, facts.KindPackage
	case "qualifier":
		// The scope half of an out-of-line member definition, which names the
		// type the member belongs to.
		return facts.Descriptor{
			Prefix: b.coord,
			Suffix: b.namespaceSuffix(r.node) + b.qualifierSuffix(r.node),
		}, facts.KindType
	default: // type, scoped
		return b.typeReferenceDescriptor(r, name)
	}
}

// callDescriptor names the target of a call. The three shapes are the three the
// grammar has, and each says exactly what its receiver is.
func (b *builder) callDescriptor(r refRec, name string) (facts.Descriptor, string) {
	fn := r.node.ChildByFieldName("function", b.lang)
	if fn == nil {
		fn = r.nameNode
	}
	switch fn.Type(b.lang) {
	case "qualified_identifier":
		t := b.resolveQualified(fn, "().")
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix}, b.callKind(fn)
	case "field_expression":
		t, ok := b.valueTarget(fn.ChildByFieldName("argument", b.lang), r.nameNode.StartByte())
		if !ok {
			return facts.Descriptor{Prefix: b.coord, Suffix: coord.Unknown + "#" + name + "()."}, facts.KindMethod
		}
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "()."}, facts.KindMethod
	default:
		// A free function, a function-like macro, or an unqualified call to a
		// member from inside the same class — the last of which the CST spells
		// exactly like the first.
		if s, ok := b.memberTarget(r.nameNode, name, "()."); ok {
			return facts.Descriptor{Prefix: b.coord, Suffix: s}, facts.KindMethod
		}
		t := b.resolveUnqualified(r.nameNode, name, "().")
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix}, facts.KindFunction
	}
}

// callKind is `method` when a qualified call names a member and `function`
// otherwise, decided by the same rule the declarator side uses.
func (b *builder) callKind(fn *sitter.Node) string {
	segs := b.nameSegments(fn.ChildByFieldName("scope", b.lang))
	if len(segs) == 0 {
		return facts.KindFunction
	}
	last := segs[len(segs)-1]
	if len(segs) == 1 && last == stdNamespace && !b.namespaces[stdNamespace] {
		return facts.KindFunction
	}
	if b.qualifierIsType(last, false) {
		return facts.KindMethod
	}
	return facts.KindFunction
}

// readDescriptor names a member read: `g->name`, `g.name`.
func (b *builder) readDescriptor(r refRec, name string) (facts.Descriptor, string) {
	t, ok := b.valueTarget(r.node.ChildByFieldName("argument", b.lang), r.nameNode.StartByte())
	if !ok {
		return facts.Descriptor{Prefix: b.coord, Suffix: coord.Unknown + "#" + name + "."}, facts.KindField
	}
	return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix + name + "."}, facts.KindField
}

// typeReferenceDescriptor names a type written in a type position, or — for a
// qualified name in an expression — whatever the grammar says its leaf is.
//
// `Mood m` and `Mood::Calm` are both `A::b` shapes and the second names an enum
// case rather than a type. The distinction is in the tree: tree-sitter spells
// the leaf `type_identifier` where a type is written and `identifier` where a
// value is, so the terminator is read off the token rather than guessed at.
func (b *builder) typeReferenceDescriptor(r refRec, name string) (facts.Descriptor, string) {
	if r.node.Type(b.lang) == "qualified_identifier" {
		term, kind := "#", facts.KindType
		if r.nameNode != nil && r.nameNode.Type(b.lang) != "type_identifier" {
			term, kind = ".", facts.KindConstant
		}
		t := b.resolveQualified(r.node, term)
		return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix}, kind
	}
	if s, ok := b.memberTarget(r.nameNode, name, "#"); ok {
		return facts.Descriptor{Prefix: b.coord, Suffix: s}, facts.KindType
	}
	t := b.resolveUnqualified(r.nameNode, name, "#")
	return facts.Descriptor{Prefix: t.coord, Suffix: t.suffix}, facts.KindType
}

// resolveQualified renders a written `a::b::c` as a descriptor suffix, applying
// the innermost-qualifier-is-a-type rule and the alias table.
func (b *builder) resolveQualified(n *sitter.Node, term string) pathTarget {
	segs := b.nameSegments(n)
	if len(segs) == 0 {
		return pathTarget{coord: b.coord, suffix: coord.Unknown + term}
	}
	name := segs[len(segs)-1]
	scope := segs[:len(segs)-1]
	if len(scope) == 0 {
		return b.resolveUnqualified(n, name, term)
	}
	if t, ok := b.aliases[scope[0]]; ok {
		return pathTarget{coord: t.coord, suffix: reterminate(t.suffix) + b.joinScope(scope[1:], false) + name + term}
	}
	if scope[0] == stdNamespace && !b.namespaces[stdNamespace] {
		// The C++ standard library, which ships with the toolchain and belongs
		// to no package this index can hold. Its leading `std` is dropped from
		// the suffix because the coordinate already names it.
		return pathTarget{
			coord:  coord.Foreign(b.coord.Scheme, b.coord.Manager, stdNamespace),
			suffix: b.joinScope(scope[1:], false) + name + term,
		}
	}
	return pathTarget{coord: b.coord, suffix: b.joinScope(scope, false) + name + term}
}

// joinScope renders the qualifier segments of a written `a::b::c`.
//
// Only the innermost segment is ever in doubt: `a` in `a::b::c` is a namespace
// whichever `b` turns out to be. qualifierIsType settles the innermost.
func (b *builder) joinScope(segs []string, declarator bool) string {
	out := ""
	for i, seg := range segs {
		if i == len(segs)-1 && b.qualifierIsType(seg, declarator) {
			out += seg + "#"
			continue
		}
		out += seg + "/"
	}
	return out
}

// qualifierIsType decides whether the innermost qualifier of a written `A::b`
// names a type or a namespace — the question the CST cannot answer, because it
// labels the scope `namespace_identifier` either way, and the class is in the
// header §2.5 forbids reading.
//
// Three pieces of file-local evidence, in order:
//
//   - This file opens a namespace of that name: it is a namespace, and the file
//     said so. This is what rescues `int greeter::verbose = 0;` written in a
//     file that also has a `namespace greeter { }` block.
//   - This file writes that name in a type position somewhere: it is a type,
//     and the grammar said so — `type_identifier` and `namespace_identifier`
//     are different tokens. This is what makes `Mood::Calm` a member of `Mood`
//     in a file that also declares a `Mood m;`.
//   - Neither, in a **declarator**: a type. Defining a member out of line is the
//     reason headers and sources exist, and defining a namespace-scope function
//     out of line with a qualified name is legal, discouraged and rare.
//   - Neither, anywhere else: a namespace. `greeter::Greeter g;` names a type in
//     a namespace far more often than a class nested in a class.
//
// What each mistake costs is in the package comment.
func (b *builder) qualifierIsType(seg string, declarator bool) bool {
	switch {
	case b.namespaces[seg]:
		return false
	case b.typeNames[seg]:
		return true
	default:
		return declarator
	}
}

// memberTarget resolves an unqualified name written inside a class body to that
// class's member, when the class declares one of that name. It is what makes
// `bump()` inside `Greeter::greet()` reach `Greeter#bump().` — and it declines
// when the enclosing type declares nothing of the name, rather than inventing a
// member the class does not have.
func (b *builder) memberTarget(n *sitter.Node, name, term string) (string, bool) {
	ts := b.typeSuffix(n)
	if ts == "" {
		return "", false
	}
	cand := facts.Descriptor{Prefix: b.coord, Suffix: ts + name + term}
	if b.siteIndex[cand.String()] {
		return cand.Suffix, true
	}
	return "", false
}

// resolveUnqualified applies C++'s enclosing-namespace search to a bare name,
// and C's absence of one.
//
// Internal linkage wins first: a name this file made `static` is this file's,
// wherever it is written. Then the alias table, then the innermost enclosing
// namespace this file actually wrote the name in — which is what makes an
// `extern "C"` declaration and a call to it agree — and finally the innermost
// namespace, which yields a descriptor with nothing behind it rather than a
// wrong one.
func (b *builder) resolveUnqualified(n *sitter.Node, name, term string) pathTarget {
	if b.internal[name] {
		return pathTarget{coord: b.coord, suffix: b.fileKey + name + term}
	}
	if t, ok := b.aliases[name]; ok {
		return t
	}
	for _, ns := range b.enclosingNamespaces(n) {
		cand := facts.Descriptor{Prefix: b.coord, Suffix: ns + name + term}
		if b.siteIndex[cand.String()] {
			return pathTarget{coord: b.coord, suffix: cand.Suffix}
		}
	}
	if c, ok := b.libcCoord(name, term); ok {
		return pathTarget{coord: c, suffix: name + term}
	}
	return pathTarget{coord: b.coord, suffix: b.namespaceSuffix(n) + name + term}
}

// libcCoord returns the foreign coordinate a name belongs to when it is the C
// standard library's and this file declares nothing of that name.
//
// It is php.go's phpBuiltins and cs.go's platform namespace in C's spelling,
// and it exists for the same reason: `printf` is not this repository's, and
// leaving it at this repository's coordinate would let a project that happens
// to define its own `printf` false-match every call to the real one. Everything
// in the sets below carries a foreign coordinate and so can never pollute
// descriptor matching within the indexed package; an omission costs one
// reference landing at this coordinate with nothing to match, which is what an
// unrecognised name gets anyway.
func (b *builder) libcCoord(name, term string) (coord.Coord, bool) {
	known := libcFuncs[name]
	if term == "#" {
		known = libcTypes[name]
	}
	if !known {
		return coord.Coord{}, false
	}
	return coord.Foreign(b.coord.Scheme, b.coord.Manager, libcNamespace), true
}

// enclosingNamespaces lists the namespace prefixes C++ name lookup would search
// from n, innermost first, ending at the global namespace. In C the list is
// always exactly [""].
func (b *builder) enclosingNamespaces(n *sitter.Node) []string {
	out := []string{}
	seen := map[string]bool{}
	for p := n; p != nil; p = p.Parent() {
		if p.Type(b.lang) != "namespace_definition" && p.Type(b.lang) != "linkage_specification" {
			continue
		}
		ns := b.namespaceSuffix(p)
		if !seen[ns] {
			seen[ns] = true
			out = append(out, ns)
		}
	}
	if !seen[""] {
		out = append(out, "")
	}
	return out
}

// valueTarget resolves the object half of a `.` or `->` to the type its members
// hang off. It is the one place a type has to be recovered rather than read off
// the syntax, and C and C++ write it down at every binding site — which puts
// this stanza with C# rather than with Ruby.
func (b *builder) valueTarget(value *sitter.Node, pos uint32) (pathTarget, bool) {
	for depth := 0; value != nil && depth < 16; depth++ {
		switch value.Type(b.lang) {
		case "parenthesized_expression":
			value = firstNamedChild(value)
		case "pointer_expression", "unary_expression":
			value = value.ChildByFieldName("argument", b.lang)
		case "this":
			if s := b.typeSuffix(value); s != "" {
				return pathTarget{coord: b.coord, suffix: s}, true
			}
			return pathTarget{}, false
		case "identifier":
			name := b.text(value)
			if def, ok := b.lookup(name, pos); ok && def.typeNode != nil {
				return b.typeTarget(def.typeNode)
			}
			if t, ok := b.fieldTypes[name]; ok && t != nil {
				return b.typeTarget(t)
			}
			return pathTarget{}, false
		case "field_expression":
			nm := b.text(value.ChildByFieldName("field", b.lang))
			if t, ok := b.fieldTypes[nm]; ok && t != nil {
				return b.typeTarget(t)
			}
			return pathTarget{}, false
		default:
			return pathTarget{}, false
		}
	}
	return pathTarget{}, false
}

// typeTarget renders a recovered type node as the descriptor its members hang
// off.
func (b *builder) typeTarget(t *sitter.Node) (pathTarget, bool) {
	if t == nil {
		return pathTarget{}, false
	}
	if t.Type(b.lang) == "qualified_identifier" {
		return b.resolveQualified(t, "#"), true
	}
	name := strings.TrimSpace(b.text(t))
	if name == "" {
		return pathTarget{}, false
	}
	return b.resolveUnqualified(t, name, "#"), true
}

// lookup finds the binding named name visible at byte offset pos: among
// bindings whose declaring scope contains pos, the one in the innermost such
// scope, declared no later than pos. C's block scoping makes the "no later
// than" rule exact.
func (b *builder) lookup(name string, pos uint32) (defRec, bool) {
	var best defRec
	var bestStart uint32
	bestEnd := ^uint32(0)
	found := false
	for _, d := range b.defsByName[name] {
		start, end, ok := b.scopeRange(d.scope)
		if !ok || pos < start || pos >= end || d.start > pos {
			continue
		}
		if !found || start > bestStart || (start == bestStart && end < bestEnd) {
			best, bestStart, bestEnd, found = d, start, end, true
		}
	}
	return best, found
}

// ------------------------------------------------------------------ emitting --

func (b *builder) addOccurrence(d facts.Descriptor, role facts.Role, kind, name string, start, end uint32) facts.LocalID {
	return b.addOccurrenceIn(b.enclosingScope(start, end), d, role, kind, name, start, end)
}

func (b *builder) addOccurrenceIn(scope facts.LocalID, d facts.Descriptor, role facts.Role, kind, name string, start, end uint32) facts.LocalID {
	b.nextOcc++
	b.out.Occurrences = append(b.out.Occurrences, facts.Occurrence{
		ID:         b.nextOcc,
		Descriptor: d,
		Role:       role,
		SymbolKind: kind,
		Name:       name,
		RangeStart: int(start),
		RangeEnd:   int(end),
		Scope:      scope,
	})
	if scope != facts.NoID {
		b.edge(facts.EdgeContains, facts.ScopeRef(scope), facts.OccurrenceRef(b.nextOcc))
	}
	return b.nextOcc
}

// occurrence looks up an occurrence by its LocalID. addOccurrence is the only
// producer of ids and numbers them densely from 1, so the id is the index.
func (b *builder) occurrence(id facts.LocalID) facts.Occurrence {
	return b.out.Occurrences[id-1]
}

func (b *builder) edge(kind facts.EdgeKind, source, target facts.Ref) {
	b.out.Edges = append(b.out.Edges, facts.Edge{Kind: kind, Source: source, Target: target})
}

// claimSubtree marks every identifier under n as owned, so the reference
// patterns do not emit a second occurrence over bytes already described.
func (b *builder) claimSubtree(n *sitter.Node) {
	if n == nil {
		return
	}
	switch n.Type(b.lang) {
	case "identifier", "field_identifier", "type_identifier", "namespace_identifier":
		b.claimed[span{n.StartByte(), n.EndByte()}] = true
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		b.claimSubtree(n.NamedChild(i))
	}
}

// ------------------------------------------------------------------ helpers ---

func (b *builder) text(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return n.Text(b.src)
}

func (b *builder) fieldText(n *sitter.Node, field string) string {
	if n == nil {
		return ""
	}
	return b.text(n.ChildByFieldName(field, b.lang))
}

// roots splits a match into its structural capture (the one whose name starts
// with prefix) and its @name capture, if any.
func roots(m sitter.QueryMatch, prefix string) (root, name *sitter.QueryCapture, ok bool) {
	for i := range m.Captures {
		c := &m.Captures[i]
		switch {
		case c.Name == "name":
			name = c
		case strings.HasPrefix(c.Name, prefix):
			root = c
		}
	}
	return root, name, root != nil
}

func suffixAfter(capture, prefix string) string {
	return strings.TrimPrefix(capture, prefix)
}

func firstNamedChild(n *sitter.Node) *sitter.Node {
	if n == nil || n.NamedChildCount() == 0 {
		return nil
	}
	return n.NamedChild(0)
}

func lastNamedChild(n *sitter.Node) *sitter.Node {
	if n == nil || n.NamedChildCount() == 0 {
		return nil
	}
	return n.NamedChild(n.NamedChildCount() - 1)
}

// reterminate turns a name's descriptor suffix into a namespace prefix, which
// is what an alias becomes when a longer path is written through it.
func reterminate(suffix string) string {
	for _, term := range []string{"().", "#", "."} {
		if strings.HasSuffix(suffix, term) {
			return strings.TrimSuffix(suffix, term) + "/"
		}
	}
	return suffix
}

// packageName is what a namespace occurrence is called in the `name` column.
func packageName(c coord.Coord, ns string) string {
	if trimmed := strings.TrimSuffix(ns, "/"); trimmed != "" {
		return lastSegment(trimmed)
	}
	name := c.Name
	if name == "" || name == coord.Unknown {
		return ""
	}
	return lastSegment(name)
}

// namespacePath renders namespace segments as a slash-terminated descriptor.
// No segments is the global namespace, which renders "" and is a real namespace
// rather than a missing one.
func namespacePath(segs []string) string {
	if len(segs) == 0 {
		return ""
	}
	return strings.Join(segs, "/") + "/"
}

func lastSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// libcFuncs is the commonly used subset of the C standard library's functions.
// See libcCoord for what the set is for and what an omission costs.
var libcFuncs = map[string]bool{
	"printf": true, "fprintf": true, "sprintf": true, "snprintf": true,
	"vprintf": true, "vfprintf": true, "vsnprintf": true, "puts": true,
	"putchar": true, "scanf": true, "sscanf": true, "fscanf": true,
	"fopen": true, "fclose": true, "fread": true, "fwrite": true,
	"fgets": true, "fputs": true, "fseek": true, "ftell": true, "rewind": true,
	"feof": true, "ferror": true, "fflush": true, "remove": true, "rename": true,
	"malloc": true, "calloc": true, "realloc": true, "free": true,
	"memcpy": true, "memmove": true, "memset": true, "memcmp": true, "memchr": true,
	"strlen": true, "strcpy": true, "strncpy": true, "strcat": true, "strncat": true,
	"strcmp": true, "strncmp": true, "strchr": true, "strrchr": true, "strstr": true,
	"strtok": true, "strdup": true, "strerror": true, "strspn": true, "strcspn": true,
	"atoi": true, "atol": true, "atoll": true, "atof": true, "strtol": true,
	"strtoul": true, "strtoll": true, "strtod": true,
	"abs": true, "labs": true, "llabs": true, "div": true,
	"exit": true, "abort": true, "atexit": true, "getenv": true, "system": true,
	"qsort": true, "bsearch": true, "rand": true, "srand": true,
	"assert": true, "time": true, "clock": true, "difftime": true, "mktime": true,
	"localtime": true, "gmtime": true, "strftime": true,
	"isalpha": true, "isdigit": true, "isalnum": true, "isspace": true,
	"isupper": true, "islower": true, "toupper": true, "tolower": true,
	"sqrt": true, "pow": true, "fabs": true, "floor": true, "ceil": true,
	"round": true, "fmod": true, "exp": true, "log": true, "log2": true,
	"log10": true, "sin": true, "cos": true, "tan": true, "atan2": true,
	"errno": true, "perror": true, "setjmp": true, "longjmp": true,
	"va_start": true, "va_end": true, "va_arg": true, "offsetof": true,
}

// libcTypes is the commonly used subset of the C standard library's type
// names, for the same reason and with the same omission rule.
var libcTypes = map[string]bool{
	"size_t": true, "ssize_t": true, "ptrdiff_t": true, "wchar_t": true,
	"int8_t": true, "int16_t": true, "int32_t": true, "int64_t": true,
	"uint8_t": true, "uint16_t": true, "uint32_t": true, "uint64_t": true,
	"intptr_t": true, "uintptr_t": true, "intmax_t": true, "uintmax_t": true,
	"FILE": true, "va_list": true, "jmp_buf": true, "time_t": true,
	"clock_t": true, "div_t": true, "ldiv_t": true, "fpos_t": true,
	"tm": true, "sig_atomic_t": true, "max_align_t": true, "nullptr_t": true,
}
