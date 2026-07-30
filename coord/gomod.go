package coord

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// GoScheme and GoManager are the SCIP scheme/manager pair for Go modules
// (SPEC.md §4.3: `scip-go gomod github.com/foo/bar v1 …`).
const (
	GoScheme  = "scip-go"
	GoManager = "gomod"
)

// GoModFile is the manifest FromGoMod reads.
const GoModFile = "go.mod"

func init() { Register(GoModFile, FromGoMod) }

// majorSuffix matches a Go module path's major-version suffix.
var majorSuffix = regexp.MustCompile(`/(v[2-9][0-9]*)$`)

// FromGoMod reads dir/go.mod and returns the Go module's coordinate.
//
// Version: go.mod does not state the module's own version — that lives in the
// VCS tag — so the only version information the manifest carries is the major
// suffix Go requires on v2+ module paths. When present it is used verbatim
// ("github.com/foo/bar/v2" → "v2"); otherwise the version is Unknown. The batch
// can pin a precise version later without any change here, because Coord is a
// plain struct.
func FromGoMod(dir string) (Coord, error) {
	path := filepath.Join(dir, GoModFile)
	f, err := os.Open(path)
	if err != nil {
		return Coord{}, err
	}
	defer func() { _ = f.Close() }()

	module, err := scanModulePath(f)
	if err != nil {
		return Coord{}, fmt.Errorf("%s: %w", path, err)
	}

	version := Unknown
	if m := majorSuffix.FindStringSubmatch(module); m != nil {
		version = m[1]
	}
	return Coord{
		Scheme:  GoScheme,
		Manager: GoManager,
		Name:    module,
		Version: version,
		Root:    dir,
	}, nil
}

// scanModulePath pulls the module path out of a go.mod. Only the `module`
// directive is read: the rest of the file is irrelevant to a coordinate, and a
// line scan keeps the dependency surface at the standard library.
func scanModulePath(f *os.File) (string, error) {
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		rest, ok := cutDirective(line, "module")
		if !ok {
			continue
		}
		if module := strings.Trim(rest, "\"`"); module != "" {
			return module, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no module directive")
}

// cutDirective reports whether line starts with the given go.mod directive and
// returns the remainder. It requires real whitespace after the keyword so
// "modulefoo" is not mistaken for "module foo".
func cutDirective(line, directive string) (string, bool) {
	rest, ok := strings.CutPrefix(line, directive)
	if !ok || rest == "" || !isSpace(rest[0]) {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' }
