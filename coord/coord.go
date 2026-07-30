// Package coord resolves package coordinates: the `scheme manager package
// version` prefix of a SCIP-style descriptor (SPEC.md §4.3).
//
// The split this package exists to serve: a stanza emits the *structural*
// descriptor suffix from the CST alone, because that is all a file-local
// extractor may know. The package coordinate is project-wide knowledge — it
// comes from a manifest (go.mod, package.json, pyproject.toml) that lives
// outside the file being parsed — so it is resolved once per repo by the batch
// and handed to every parser. Keeping it here is what lets the extractor stay
// file-local (SPEC.md §2.2, §2.5).
//
// One resolver per ecosystem; go.mod is the only one M2 ships (gomod.go).
// M6/M7 add npm.go and pyproject.go beside it and register them here.
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
const Unknown = "."

// ErrNoManifest is returned when no registered manifest is found at or above a
// directory.
var ErrNoManifest = errors.New("coord: no package manifest found")

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

// resolvers maps a manifest filename to the resolver that reads it. Registered
// from each ecosystem's file (gomod.go now; npm.go, pyproject.go at M6/M7).
var resolvers = map[string]Resolver{}

// Register registers a resolver under the manifest filename it reads.
// It panics on a duplicate registration, which can only be a build defect.
func Register(manifest string, r Resolver) {
	if _, dup := resolvers[manifest]; dup {
		panic(fmt.Sprintf("coord: resolver already registered for %q", manifest))
	}
	resolvers[manifest] = r
}

// Manifests returns the registered manifest filenames, sorted.
func Manifests() []string {
	out := make([]string, 0, len(resolvers))
	for m := range resolvers {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// Resolve finds the nearest registered manifest at or above dir and returns the
// coordinate it declares. It is the entry point the batch calls once per repo.
func Resolve(dir string) (Coord, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Coord{}, err
	}
	for cur := abs; ; {
		for _, manifest := range Manifests() {
			if _, err := os.Stat(filepath.Join(cur, manifest)); err == nil {
				return resolvers[manifest](cur)
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return Coord{}, fmt.Errorf("%w at or above %s", ErrNoManifest, dir)
		}
		cur = parent
	}
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
