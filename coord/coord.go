// Package coord resolves package coordinates: the `scheme manager package
// version` prefix of a SCIP-style descriptor (SPEC.md §4.3).
//
// The split this package exists to serve: a stanza emits the *structural*
// descriptor suffix from the CST alone, because that is all a file-local
// extractor may know. The package coordinate is project-wide knowledge — it
// comes from a manifest (go.mod, package.json, pyproject.toml) that lives
// outside the file being parsed — so it is resolved by the batch and handed to
// every parser. Keeping it here is what lets the extractor stay file-local
// (SPEC.md §2.2, §2.5).
//
// A coordinate is a property of *(repository, ecosystem)* and not of a
// repository. A repository holding a go.mod beside a package.json has two of
// them, and a file belongs to the one its own language declares. Resolving a
// single coordinate per repository stamps every TypeScript file with the Go
// module's scheme, manager, name and version — which makes a TypeScript class
// and a Go type in a same-named directory render the byte-identical descriptor,
// and the link pass (§7) joins on the descriptor and nothing else, so it
// materializes a cross-language edge that does not exist. Resolve therefore
// returns a Set: one coordinate per ecosystem, keyed by the file extensions
// that ecosystem owns.
//
// The same defect has a second, larger form, and it is the one the corpus
// exists for: a *repository* bound. Resolve used to walk upward until it found
// a manifest, with nothing to stop it at the repository. A tree with no
// manifest of its own therefore inherited whichever one happened to sit above
// it on that machine — measured, twenty repositories under one directory took
// a single package.json in the operator's home directory, with Root set there,
// so Namespace rendered every file's directory relative to *home* and two
// same-named symbols in two unrelated repositories produced byte-identical
// descriptors. One database holding many repositories would have linked them
// together. So the directory Resolve is given *is* the repository: it reads
// that directory's manifests and no ancestor's, and an ecosystem that declares
// none there is named after the corpus instead of after somebody else's
// package.
//
// One Ecosystem per language; go.mod was the only one M2 shipped (gomod.go).
// M6 adds npm.go beside it and M7 pyproject.go, each registering itself here.
package coord

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Unknown is SCIP's marker for a descriptor component that could not be
// determined. Writing it is deliberate: a coordinate with an unknown version
// still names a package, and `.` can never collide with a real component, so
// the link pass (§7) will not false-match on it.
//
// It means that for a *version* and no longer for a *name*, and the two used to
// be the same value for the same reason. A version legitimately cannot be
// determined: a go.mod names a module and not its own release. A name always
// can be, because the corpus names it — so an ecosystem with no manifest is
// stamped `<scheme> <manager> <corpus> .` and never `. . . .`. A name of `.`
// would put every manifest-less repository in one namespace, which is the
// collision Resolve's repository bound exists to stop.
const Unknown = "."

// ErrNoCorpus is returned when Resolve is called without a corpus name.
//
// It is a caller error rather than a repository one. The corpus is what names
// an ecosystem that declares no manifest, so resolving without it would hand
// back the `. . . .` coordinate the bound exists to prevent — silently, and one
// whole repository at a time.
var ErrNoCorpus = errors.New("coord: no corpus name")

// Coord is a package coordinate — the descriptor prefix of every symbol the
// package owns, plus the directory it was resolved from.
//
// The four descriptor components are stored apart rather than pre-joined
// because the `file` table keeps them in four columns (pkg_scheme, pkg_manager,
// pkg_name, pkg_version); joining is a read concern, not a storage one.
type Coord struct {
	// Scheme is the indexer scheme, e.g. "scip-go".
	Scheme string
	// Manager is the package manager, e.g. "gomod".
	Manager string
	// Name is the package (module) name, e.g. "github.com/foo/bar".
	Name string
	// Version is the package version, or Unknown when the manifest does not
	// carry one.
	Version string
	// Root is the directory the manifest was read from. Namespace resolves file
	// paths against it. Empty for a foreign coordinate, which owns no files in
	// this repo.
	Root string
}

// Prefix renders the coordinate as the leading four space-separated components
// of a SCIP descriptor. Empty components render as Unknown so the prefix always
// has exactly four fields and stays parseable.
func (c Coord) Prefix() string {
	return strings.Join([]string{
		or(c.Scheme, Unknown),
		or(c.Manager, Unknown),
		or(c.Name, Unknown),
		or(c.Version, Unknown),
	}, " ")
}

// IsZero reports whether the coordinate names no package at all.
func (c Coord) IsZero() bool { return c == Coord{} }

// Namespace returns the SCIP namespace descriptor of the package that owns
// filePath: the file's directory relative to Root, slash-separated and
// slash-terminated. A file in the package root yields "".
//
// This is the one piece of the descriptor *suffix* that a stanza cannot derive
// from the CST — a Go file's `package` clause names the package but not its
// import path — so it is served from here and prepended by the mapper.
func (c Coord) Namespace(filePath string) string {
	if c.Root == "" {
		return ""
	}
	rel, err := filepath.Rel(c.Root, filepath.Dir(filePath))
	if err != nil || rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.ToSlash(rel) + "/"
}

// Import resolves an import path to the coordinate and namespace that name the
// imported package.
//
// An import that stays inside this module keeps this coordinate and becomes a
// namespace, so a reference through it produces byte-identical descriptors to
// the definitions in that package — which is exactly what the link pass joins
// on. An import that leaves the module becomes a foreign coordinate with an
// unknown version: it cannot match anything indexed here, and it must not
// pretend to.
func (c Coord) Import(importPath string) (Coord, string) {
	switch {
	case c.Name != "" && importPath == c.Name:
		return c, ""
	case c.Name != "" && strings.HasPrefix(importPath, c.Name+"/"):
		return c, strings.TrimPrefix(importPath, c.Name+"/") + "/"
	default:
		return Foreign(c.Scheme, c.Manager, importPath), ""
	}
}

// Foreign builds a coordinate for a package outside the resolved module — an
// import of another module, or a language's predeclared identifiers. Its
// version is Unknown and it owns no Root.
func Foreign(scheme, manager, name string) Coord {
	return Coord{Scheme: scheme, Manager: manager, Name: name, Version: Unknown}
}

// Resolver reads an ecosystem's manifest in dir and returns the coordinate it
// declares.
type Resolver func(dir string) (Coord, error)

// Ecosystem is one language ecosystem's coordinate source: the manifest that
// declares a package, the SCIP scheme/manager pair its symbols carry, and the
// file extensions whose files belong to it.
//
// Exts is what makes the arity right. A registry keyed by manifest alone can
// answer "which manifests exist" but not "which of them owns this file", and
// the second question is the one a mixed repository asks — so a caller with
// only the first has no choice but to pick one coordinate for the whole tree,
// which is the defect described in this package's doc comment.
type Ecosystem struct {
	// Manifest is the filename From reads, e.g. "go.mod".
	Manifest string
	// Scheme and Manager are the SCIP pair every symbol of this ecosystem
	// carries, e.g. "scip-go" and "gomod".
	//
	// They are stated here as well as produced by From because they are what an
	// ecosystem with *no* manifest is stamped with: a .ts file in a repository
	// that has a go.mod and no package.json still needs a coordinate, and the
	// one thing it must not be given is another language's (named).
	Scheme, Manager string
	// Exts are the file extensions this ecosystem owns, leading dot included.
	Exts []string
	// From reads Manifest in a directory and returns the coordinate it declares.
	From Resolver
}

// named is this ecosystem's coordinate in a repository that declares no
// manifest for it: the right scheme and manager, the corpus for a name, no
// version, and the repository root, so that namespaces still separate one
// directory from another.
//
// It is no manifest, not no name. Before the corpus this returned Unknown for
// the name, on the reasoning that a package nobody declared has none — but a
// name is a thing that can always be determined once a repository has one, and
// `. ` for a name means every manifest-less repository in the database shares a
// namespace. Two of them holding `src/main.go` would then render byte-identical
// descriptors for two unrelated symbols, and the link pass joins on the
// descriptor and nothing else (§7). So the caller's corpus name is what fills
// the slot, and Unknown keeps only the version it was always right for.
//
// Deliberately not the zero Coord, for the reason it never was: a zero
// coordinate carries no Root, so Namespace returns "" for every file and two
// same-named symbols in different directories of one repository would collide —
// the same false match Set exists to prevent, one level down.
func (e Ecosystem) named(root, corpus string) Coord {
	return Coord{Scheme: e.Scheme, Manager: e.Manager, Name: corpus, Version: Unknown, Root: root}
}

// ecosystems maps a manifest filename to the ecosystem that reads it; owner
// maps a file extension to that ecosystem's manifest. Registered from each
// ecosystem's file (gomod.go, npm.go; pyproject.go at M7).
var (
	ecosystems = map[string]Ecosystem{}
	owner      = map[string]string{}
)

// Register registers an ecosystem under the manifest filename it reads. It
// panics on a duplicate manifest or a duplicate extension, either of which can
// only be a build defect — and a doubly-owned extension is the one that would
// silently hand a file to whichever ecosystem registered last.
func Register(e Ecosystem) {
	if _, dup := ecosystems[e.Manifest]; dup {
		panic(fmt.Sprintf("coord: resolver already registered for %q", e.Manifest))
	}
	for _, ext := range e.Exts {
		if prev, dup := owner[ext]; dup {
			panic(fmt.Sprintf("coord: %q is already owned by %q", ext, prev))
		}
		owner[ext] = e.Manifest
	}
	ecosystems[e.Manifest] = e
}

// Manifests returns the registered manifest filenames, sorted.
func Manifests() []string {
	out := make([]string, 0, len(ecosystems))
	for m := range ecosystems {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// Extensions returns the registered file extensions, sorted, each with its
// leading dot.
//
// It is the set of files that can be given a coordinate at all, and extract's
// own registry has to list exactly the same set: a file a parser accepts but no
// ecosystem owns has no scheme to be stamped with, and one an ecosystem owns
// but no parser reads is a registration nothing consults. coord cannot check
// that itself — extract imports coord, not the other way round — so the check
// lives in this package's tests.
func Extensions() []string {
	out := make([]string, 0, len(owner))
	for ext := range owner {
		out = append(out, ext)
	}
	sort.Strings(out)
	return out
}

// Set is a repository's package coordinates: one per registered ecosystem,
// keyed by the file extensions that ecosystem owns.
//
// Every registered extension has an entry, resolved or not, which is what lets
// the set alone decide every file's coordinate. That is more than tidiness:
// index checkpoints this value and a recovering process reads it back rather
// than resolving again (index/dbos.go's site), so a set that needed the
// registry to be interpreted would be a checkpoint that means one thing in the
// build that wrote it and another in the build that replays it.
type Set struct {
	// ByExt maps a file extension, leading dot included, to the coordinate of
	// the ecosystem that owns files with that extension.
	ByExt map[string]Coord `json:"by_ext"`
	// Primary is the repository's headline coordinate: the one a report names
	// when it has room for exactly one. It is the first in manifest-name order
	// among the ecosystems that actually resolved, so a repository with a single
	// ecosystem — a Go module, an npm package — has precisely the coordinate it
	// has always had. A repository with two has one of the two, and ByExt is
	// where the rest of the truth is.
	Primary Coord `json:"primary"`
}

// For returns the coordinate of the ecosystem that owns path, which is decided
// by path's extension and by nothing else about it.
func (s Set) For(path string) Coord { return s.ByExt[filepath.Ext(path)] }

// Resolve reads the manifests dir declares and returns one coordinate per
// ecosystem, all rooted at dir. It is the entry point the batch calls once per
// repository, and corpus is the name that repository is known by in the graph.
//
// **The directory you index is the repository, and Resolve does not look above
// it.** That bound is the whole of what makes one database able to hold many
// repositories. Without it Resolve walked upward until something matched, so a
// tree with no manifest of its own silently took the coordinate of whatever sat
// above it — in the measured case a package.json in the operator's home
// directory, shared by twenty unrelated repositories, with Root set there so
// that every namespace was rendered relative to home. Two of those repositories
// would then render byte-identical descriptors for two unrelated symbols, and
// §7 links on the descriptor and nothing else.
//
// The bound is dir itself rather than a separate argument because there is no
// second candidate for it: both callers already hand this function the
// repository root they were told to index, and CodiQ declines to shell out to
// git (see index.prunedDir on .gitignore), so it has no other way to know where
// a repository ends.
//
// **This narrows one usage, and the narrowing is the point.** `codiq ./subdir`
// inside a module no longer inherits that module's coordinate; it gets a
// corpus-named one, because as far as this run is concerned the subdirectory is
// the repository. That usage never produced a coherent graph anyway — file.path
// was already relative to the indexed subdirectory while Root was the module
// root, so paths and namespaces were measured from different origins. Index the
// module and the coordinate is exactly what it always was.
//
// Every manifest is read from that one directory rather than each being
// searched for on its own. Searching per ecosystem would let a package.json
// three levels above a Go module become that module's TypeScript coordinate,
// and would let a malformed manifest outside the repository fail a run that has
// no file of that language at all.
//
// An ecosystem with no manifest there is not an error and never was: it gets
// Ecosystem.named, so its files are stamped with their own scheme and manager
// and can never collide with another language's. *No* manifest at all is not an
// error either, which is the change — it is an ordinary repository whose every
// ecosystem is named by the corpus, rather than a run that refuses to start.
// The only errors left are a manifest that is present and unreadable, and a
// caller that supplied no corpus.
func Resolve(dir, corpus string) (Set, error) {
	if corpus == "" {
		return Set{}, fmt.Errorf("%w for %s", ErrNoCorpus, dir)
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return Set{}, err
	}
	found := map[string]Coord{}
	for _, manifest := range Manifests() {
		if _, err := os.Stat(filepath.Join(root, manifest)); err != nil {
			continue
		}
		c, err := ecosystems[manifest].From(root)
		if err != nil {
			return Set{}, err
		}
		found[manifest] = c
	}
	return newSet(root, corpus, found), nil
}

// newSet spreads the coordinates read from the repository root over the
// extensions their ecosystems own, filling in the ecosystems that declared no
// manifest there.
//
// Manifest-name order is what makes Primary deterministic, and the iteration is
// over the registry rather than over found so that the two kinds of entry are
// produced by one loop and neither can be forgotten.
//
// A repository that declared nothing at all still has a Primary, and it is the
// corpus with no ecosystem attached — `. . <corpus> .`. Leaving it zero would
// make a report print `. . . .` for a repository whose name is the one thing
// certainly known about it, and would make IsZero — which callers read as "the
// run never got as far as resolving" — true of a run that resolved fine.
func newSet(root, corpus string, found map[string]Coord) Set {
	s := Set{ByExt: make(map[string]Coord, len(owner))}
	for _, manifest := range Manifests() {
		e := ecosystems[manifest]
		c, ok := found[manifest]
		switch {
		case !ok:
			c = e.named(root, corpus)
		case s.Primary.IsZero():
			s.Primary = c
		}
		for _, ext := range e.Exts {
			s.ByExt[ext] = c
		}
	}
	if s.Primary.IsZero() {
		s.Primary = Coord{Name: corpus, Version: Unknown, Root: root}
	}
	return s
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
