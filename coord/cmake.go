package coord

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// CCScheme and CMakeManager are the SCIP scheme/manager pair for C and C++
// translation units (SPEC.md §4.3), the ninth of the pairs beside
// GoScheme/GoManager, TSScheme/NPMManager, PyScheme/PipManager,
// RustScheme/CargoManager, JavaScheme/MavenManager, CSharpScheme/NuGetManager,
// RubyScheme/GemManager and PHPScheme/ComposerManager.
//
// Both halves of the pair break the naming rule the eight before them followed,
// and each breaks it for a reason that is a fact about C rather than a
// preference.
//
// **The scheme names two languages, because they are one linkage namespace.**
// Every predecessor named the scheme for *the* language it read, and cs.go
// argued the case explicitly: `scip-dotnet` was rejected because .NET is also
// F# and Visual Basic and that stanza reads neither. Here the same test gives
// the opposite answer. C and C++ share one flat namespace of external symbols —
// a C++ translation unit reaches a C function through `extern "C"`, and the C
// header that declares it and the C file that defines it are ordinary members
// of the same program — so a `scip-c` and a `scip-cpp` coordinate would put the
// declaration and the definition of one symbol behind two prefixes that the
// link pass, which joins on the descriptor string and nothing else (§7), can
// never match. Splitting the scheme would not describe two languages; it would
// break the one join this ecosystem exists to make. `scip-clang` was the other
// candidate and loses on cs.go's own test: clang also reads Objective-C,
// Objective-C++ and CUDA, and this stanza reads none of them. `scip-cc` names
// the pair, is the traditional name of the C compiler, and names nothing else.
//
// **The manager is a build system, because the ecosystem has no package
// manager.** Every predecessor could name one — gomod, npm, pip, cargo, maven,
// nuget, gem, composer. C and C++ have no such thing: there is Conan, there is
// vcpkg, there are distribution packages, there is vendoring, and none of them
// is what a majority of C projects use, let alone what one can be read out of a
// tree. `cmake` names the file this resolver actually reads (CMakeFile), which
// is the honest documentation of where a C coordinate came from — and, per the
// note every ecosystem file before this one carries, only the *uniqueness* of
// the pair is load-bearing.
const (
	CCScheme     = "scip-cc"
	CMakeManager = "cmake"
)

// CMakeFile is the manifest FromCMake reads, and choosing it is the least
// comfortable decision in this ecosystem. It is worth setting out in full,
// because the honest summary is "C has no manifest, and this is the closest
// approximation to one that exists".
//
// `Ecosystem.Manifest` does two jobs: it is the filename `Resolve` stats to
// find the repository root, and it is the file `From` reads for an identity.
// The C and C++ world offers no file that does either job well:
//
//   - **`CMakeLists.txt`** is fixed-name, sits at the root of a CMake project by
//     definition, and its `project()` command — which CMake *requires* a
//     top-level listfile to call — states a name and, optionally, a version.
//     But it is a *Turing-complete script*, so `project(foo VERSION 1.2.3)` is a
//     call whose arguments are pattern-matched, not a field that is read.
//   - **`Makefile`**, **`configure.ac`**, **`meson.build`**, **`BUILD.bazel`**
//     and **`MODULE.bazel`** each cover part of the remainder, and `Register`
//     admits exactly one manifest per ecosystem and exactly one ecosystem per
//     extension — so registering several of them is not merely more work, it is
//     a panic (`coord: ".c" is already owned by …`). One had to be chosen.
//   - **Nothing at all** is what most C repositories that are not CMake
//     projects have.
//
// CMakeLists.txt is chosen because it is the only candidate that is both
// fixed-name and states an identity a human wrote down. The reading is
// deliberately narrow, and it is a reading rather than an invention — which is
// the line coord/nuget.go drew and every resolver since has kept. It takes the
// literal first argument of a top-level `project()` call as the name and the
// literal token after `VERSION` as the version; a name that is a `${…}`
// variable reference states its identity somewhere this cannot see and reduces
// to Unknown, exactly as msbuildValue and Maven's `stated` do; an absent
// `VERSION` reduces to Unknown, because a version that renders is a version the
// link pass joins on (§4.3) and defaulting one would make every unversioned C
// repository in an index collide with every other.
//
// # What this used to cost, and what it costs now
//
// `Resolve` used to walk up from a directory looking for *any* registered
// manifest and fail the whole run when it reached the filesystem root having
// found none, so the repository did not index at all. For the eight ecosystems
// before this one that was an edge case: a Go tree has a go.mod, a Rust tree
// has a Cargo.toml, and a tree with neither is not really a project of that
// language. For C it was **the ordinary case**. A Makefile-only project, an
// autotools project, a Meson project, a Bazel project, or a bare directory of
// `.c` files has no registered manifest, and nothing about that failure named C
// as the reason.
//
// The corpus is the last-resort coordinate that answers it. Such a repository
// now resolves to `c-cmake <corpus> .` rooted at its own directory: no version,
// no CMake package name, but a name that is unique in the database and a Root
// that separates one directory from another. What is still missing is only what
// the manifest would have added — the declared package name and version — so a
// Makefile-only C project indexes, and its symbols simply carry the corpus for
// a package name.
//
// That was coord's boundary and not this file's, which is why the fix landed in
// the shared `Resolve` rather than here — a change §14 M9+ says an
// additional-language task must not make, and one made deliberately by the
// corpus milestone instead.
const CMakeFile = "CMakeLists.txt"

// CCExts are the file extensions this ecosystem owns, and it owns eight of them
// — more than the eight previous ecosystems own between them. Three properties
// of C and C++ force it, and the third is the one with consequences.
//
// The first is that the family genuinely has no canonical extension: `.cpp`,
// `.cc` and `.cxx` are all ordinary spellings of a C++ source, and `.hpp`,
// `.hh` and `.hxx` of a C++ header, with no convention picking a winner.
//
// The second is that **`.h` is ambiguous and nothing in the file says which
// language it is.** A `.h` may be a C header, a C++ header, or a header written
// to be included from both behind `#ifdef __cplusplus`. extract/cc parses it
// with the C++ grammar and says why; what matters here is that `.h` can belong
// to exactly one ecosystem, so the ambiguity has to be resolved by putting C
// and C++ in the same one.
//
// The third is what is *not* here. `.C` and `.H` — the historical uppercase
// spellings — are excluded because `filepath.Ext` is case-sensitive while macOS
// and Windows filesystems are not, so a repository holding `foo.C` would be
// walked on one machine and not on another. `.inl`, `.ipp`, `.tcc` and `.txx`
// are excluded because they are conventionally `#include`d into the middle of
// a header and are often not well-formed translation units on their own — a
// fragment that opens inside a class body parses as an error, and an extension
// this owns but whose files cannot be read is worse than one it does not own.
// `.c++`/`.h++` are excluded as vanishingly rare.
//
// These repeat extract/cc.Exts, for the reason coord.GoExt gives.
var CCExts = []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx"}

func init() {
	Register(Ecosystem{
		Manifest: CMakeFile,
		Scheme:   CCScheme,
		Manager:  CMakeManager,
		Exts:     CCExts,
		From:     FromCMake,
	})
}

// FromCMake reads dir/CMakeLists.txt and returns the C/C++ project's
// coordinate.
//
// Only an unreadable file is an error, which is the line every resolver since
// go.mod has drawn: `Resolve` stat'd it, so failing to open it means something
// is wrong with the tree rather than with the manifest's contents. A listfile
// with no `project()` call, or one whose arguments are variable references,
// yields Unknown for both components — never an error, and never a guess.
func FromCMake(dir string) (Coord, error) {
	f, err := os.Open(filepath.Join(dir, CMakeFile)) //nolint:gosec // dir is the repository root the batch resolved.
	if err != nil {
		return Coord{}, err
	}
	defer func() { _ = f.Close() }()

	name, version := scanProjectCall(f)

	return Coord{
		Scheme:  CCScheme,
		Manager: CMakeManager,
		Name:    or(name, Unknown),
		Version: or(version, Unknown),
		Root:    dir,
	}, nil
}

// scanProjectCall finds the first `project(...)` command in a listfile and
// returns the name and version it states.
//
// Scanned rather than parsed, for the reason coord/cargo.go gives one ecosystem
// over: two scalars out of one known command is not a parsing problem, and the
// dependency surface stays at the standard library. It is also the only
// defensible depth. A CMake listfile is a program — `project()` may sit inside
// an `if()`, its arguments may be variables set fifty lines earlier or on the
// command line, and a `function()` may wrap the whole thing — and a scanner
// that read further would be a partial CMake interpreter whose failures were
// silent. This one reads the literal arguments of the first `project()` it sees
// at the start of a line and stops; everything else is Unknown.
//
// Comments are stripped, because `# project(wrong)` is not a project call. A
// `#[[ … ]]` bracket comment is not handled and is rare enough to leave; the
// worst it can do is make a commented-out `project()` win, which yields a name
// the listfile does contain.
func scanProjectCall(r *os.File) (name, version string) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		args, ok := commandArgs(line, "project")
		if !ok {
			continue
		}
		return projectIdentity(args)
	}
	return "", ""
}

// commandArgs splits `name(arg arg arg)` into its arguments when the line opens
// the named command, and reports false otherwise.
//
// The call has to be *closed* on the same line. A `project()` whose arguments
// are wrapped over several lines states its identity in a shape this declines
// to read rather than one it guesses at — the same choice indentOf makes in
// coord/gemfile.go — and a wrapped `project()` is unusual enough that the cost
// is one repository resolving to Unknown.
func commandArgs(line, command string) ([]string, bool) {
	lower := strings.ToLower(line)
	if !strings.HasPrefix(lower, command) {
		return nil, false
	}
	rest := strings.TrimLeftFunc(line[len(command):], unicode.IsSpace)
	if !strings.HasPrefix(rest, "(") {
		return nil, false
	}
	end := strings.IndexByte(rest, ')')
	if end < 0 {
		return nil, false
	}
	return strings.Fields(rest[1:end]), true
}

// projectIdentity reads the name and version out of a `project()` call's
// arguments. CMake's own signature is
// `project(<name> [VERSION <v>] [DESCRIPTION …] [LANGUAGES …])`, so the name is
// positional and the version is the token after the `VERSION` keyword.
func projectIdentity(args []string) (name, version string) {
	if len(args) == 0 {
		return "", ""
	}
	name = cmakeValue(args[0])
	for i := 1; i < len(args); i++ {
		if strings.EqualFold(args[i], "VERSION") && i+1 < len(args) {
			return name, cmakeValue(args[i+1])
		}
	}
	return name, ""
}

// cmakeValue reduces one argument to what it actually states. A `${…}` variable
// reference names a value defined elsewhere, so the text is a pointer rather
// than the thing pointed at, and the honest reading is "not stated here" —
// which is coord/nuget.go's msbuildValue in CMake's spelling. Surrounding
// quotes are CMake's syntax and not part of the value.
func cmakeValue(v string) string {
	v = strings.Trim(v, `"`)
	if strings.Contains(v, "${") || strings.Contains(v, "@") {
		return ""
	}
	return v
}
