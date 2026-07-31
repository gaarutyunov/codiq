// Package extract dispatches a file to the parser for its language.
//
// The registry is the only place that knows the set of supported languages
// (SPEC.md §12): adding one is a new sub-package plus a line in byExt, and
// nothing else changes.
//
// Direction of dependency matters here. Each language sub-package satisfies
// Parser *structurally* — it imports facts and coord and never extract — so
// extract can import the sub-packages to fill the registry without a cycle.
// Nothing in a language sub-package may reference this package.
package extract

import (
	"path/filepath"
	"sort"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/extract/golang"
	"github.com/gaarutyunov/codiq/extract/java"
	"github.com/gaarutyunov/codiq/extract/py"
	"github.com/gaarutyunov/codiq/extract/rs"
	"github.com/gaarutyunov/codiq/extract/ts"
	"github.com/gaarutyunov/codiq/facts"
)

// Parser turns one file's source into that file's facts (SPEC.md §14 M2).
//
// Parse returns no error: a parse failure is a property of the file, not of the
// call, so it travels in facts.FileFacts.ParseError and the caller decides
// whether to skip the file (§5 poison-file handling). Implementations must be
// safe for concurrent use — the M2 loader parses files on goroutines.
type Parser interface {
	Parse(path string, src []byte, coord coord.Coord) facts.FileFacts
}

// byExt maps a file extension to its parser. Each language sub-package's Parser
// satisfies Parser structurally; the map literal is the compile-time check that
// they all still do.
var byExt = map[string]Parser{
	golang.Ext: golang.New(),
	java.Ext:   java.New(),
	py.Ext:     py.New(),
	rs.Ext:     rs.New(),
	ts.Ext:     ts.New(),
}

// ParserFor returns the parser registered for path's extension.
func ParserFor(path string) (Parser, bool) {
	p, ok := byExt[filepath.Ext(path)]
	return p, ok
}

// Supported reports whether path has a registered parser. It is the predicate
// a repo walk filters on.
func Supported(path string) bool {
	_, ok := byExt[filepath.Ext(path)]
	return ok
}

// Extensions returns the registered extensions, sorted, each with its leading
// dot.
func Extensions() []string {
	out := make([]string, 0, len(byExt))
	for ext := range byExt {
		out = append(out, ext)
	}
	sort.Strings(out)
	return out
}
