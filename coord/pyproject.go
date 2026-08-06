package coord

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PyScheme and PipManager are the SCIP scheme/manager pair for Python packages
// (SPEC.md §4.3), the third of the pairs beside GoScheme/GoManager and
// TSScheme/NPMManager.
//
// The manager names the ecosystem's package manager, as `gomod` and `npm` do:
// Python's is pip, over PyPI. Only one property of the pair is load-bearing —
// no coordinate of one ecosystem may render a prefix another can render, since
// the link pass joins on the descriptor string and nothing else (§7). Three
// distinct schemes give that for free, and the manager is then documentation.
const (
	PyScheme   = "scip-python"
	PipManager = "pip"
)

// PyProjectFile is the manifest FromPyProject reads.
const PyProjectFile = "pyproject.toml"

// PyExt is the file extension the Python ecosystem owns. It repeats
// extract/py.Ext, for the reason coord.GoExt gives.
const PyExt = ".py"

func init() {
	Register(Ecosystem{
		Manifest: PyProjectFile,
		Scheme:   PyScheme,
		Manager:  PipManager,
		Exts:     []string{PyExt},
		From:     FromPyProject,
	})
}

// FromPyProject reads dir/pyproject.toml and returns the Python package's
// coordinate.
//
// Only PEP 621's `[project]` table is read, and only its `name` and `version`.
// That is the standard place a Python package states its identity, and it is
// the whole of what a coordinate is made of; the rest of a pyproject.toml is
// build-backend configuration.
//
// Both fields are optional here, as they are in FromPackageJSON and for the
// same reason: a `[tool.poetry]`-only manifest, a `dynamic = ["version"]`
// declaration, or an application that is not packaged at all still owns its
// files, and refusing to resolve would leave a whole Python tree unindexable
// over a missing manifest field. An absent one becomes Unknown, which can never
// false-match (§4.3).
//
// A repository whose only manifest is a setup.py resolves nothing here — the
// registry is keyed by manifest filename and setup.py is not registered. Since
// the corpus milestone that is no longer fatal: such a tree resolves to
// `scip-python pypi <corpus> .` rooted at itself and indexes, with the corpus
// standing in for the distribution name setup.py would have declared. Reading
// setup.py properly is still out of scope, and is the one common Python layout
// whose *declared* name this resolver does not reach.
func FromPyProject(dir string) (Coord, error) {
	path := filepath.Join(dir, PyProjectFile)
	f, err := os.Open(path)
	if err != nil {
		return Coord{}, err
	}
	defer func() { _ = f.Close() }()

	name, version, err := scanProjectTable(f)
	if err != nil {
		return Coord{}, fmt.Errorf("%s: %w", path, err)
	}

	return Coord{
		Scheme:  PyScheme,
		Manager: PipManager,
		Name:    or(name, Unknown),
		Version: or(version, Unknown),
		Root:    dir,
	}, nil
}

// scanProjectTable pulls `name` and `version` out of a pyproject.toml's
// `[project]` table.
//
// A line scan rather than a TOML parser, for the reason scanModulePath gives:
// two scalar strings out of one known table is not a parsing problem, and the
// dependency surface stays at the standard library — which is itself part of
// what §14 M6/M7 claims a new language costs, since a third language that had
// to add a module to go.mod would not be "one sub-package plus a query plus a
// resolver".
//
// Three pieces of TOML actually matter and all three are handled: a table
// header ends the previous table, a `#` outside a string starts a comment, and
// a multi-line string can contain anything at all — including a line that looks
// like `[project]` — so the scanner tracks triple-quote state rather than
// trusting line boundaries.
func scanProjectTable(f *os.File) (name, version string, err error) {
	sc := bufio.NewScanner(f)
	inProject := false
	// delim is the multi-line string terminator currently being sought, or ""
	// when the scanner is not inside one.
	delim := ""
	for sc.Scan() {
		line := sc.Text()

		if delim != "" {
			if _, closed := cutAfter(line, delim); closed {
				delim = ""
			}
			continue
		}

		trimmed := strings.TrimSpace(stripComment(line))
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			// `[project]` exactly: `[project.optional-dependencies]` is a
			// different table, and `[[project]]` is not one at all.
			inProject = trimmed == "[project]"
			continue
		}
		if opened, d := opensMultiline(trimmed); opened {
			delim = d
			continue
		}
		if !inProject {
			continue
		}

		key, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "name":
			name = tomlString(value)
		case "version":
			version = tomlString(value)
		}
	}
	if err := sc.Err(); err != nil {
		return "", "", err
	}
	return name, version, nil
}

// stripComment removes a `#` comment, respecting quoted strings so that a `#`
// inside a name or a version is not mistaken for one.
func stripComment(line string) string {
	quote := byte(0)
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return line[:i]
		}
	}
	return line
}

// opensMultiline reports whether a line starts a multi-line string that it does
// not also close, and returns the delimiter that will close it.
func opensMultiline(line string) (bool, string) {
	for _, d := range []string{`"""`, `'''`} {
		rest, found := cutAfter(line, d)
		if !found {
			continue
		}
		if _, closed := cutAfter(rest, d); closed {
			return false, "" // opened and closed on the one line
		}
		return true, d
	}
	return false, ""
}

// cutAfter returns what follows the first occurrence of sep, and whether sep
// was there at all.
func cutAfter(s, sep string) (string, bool) {
	_, after, found := strings.Cut(s, sep)
	return after, found
}

// tomlString reduces a TOML value to the string it denotes, or "" when it is
// not a plain string — an array, a table or a number is not a name.
func tomlString(value string) string {
	v := strings.TrimSpace(value)
	for _, q := range []string{`"`, `'`} {
		if inner, ok := strings.CutPrefix(v, q); ok {
			if s, ok := strings.CutSuffix(inner, q); ok {
				return strings.TrimSpace(s)
			}
			return ""
		}
	}
	return ""
}
