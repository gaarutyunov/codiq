package coord

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RustScheme and CargoManager are the SCIP scheme/manager pair for Rust crates
// (SPEC.md §4.3), the fourth of the pairs beside GoScheme/GoManager,
// TSScheme/NPMManager and PyScheme/PipManager.
//
// The manager names the ecosystem's package manager, as `gomod`, `npm` and
// `pip` do: Rust's is cargo, over crates.io. Only one property of the pair is
// load-bearing — no coordinate of one ecosystem may render a prefix another can
// render, since the link pass joins on the descriptor string and nothing else
// (§7). Four distinct schemes give that for free, and the manager is then
// documentation.
const (
	RustScheme   = "scip-rust"
	CargoManager = "cargo"
)

// CargoFile is the manifest FromCargo reads.
const CargoFile = "Cargo.toml"

// RustExt is the file extension the Rust ecosystem owns. It repeats
// extract/rs.Ext, for the reason coord.GoExt gives.
const RustExt = ".rs"

func init() {
	Register(Ecosystem{
		Manifest: CargoFile,
		Scheme:   RustScheme,
		Manager:  CargoManager,
		Exts:     []string{RustExt},
		From:     FromCargo,
	})
}

// FromCargo reads dir/Cargo.toml and returns the Rust crate's coordinate.
//
// Only the `[package]` table is read, and only its `name` and `version`. That is
// where a Cargo package states its identity, and it is the whole of what a
// coordinate is made of; the rest of a Cargo.toml is dependency and build
// configuration.
//
// Both fields are optional here, as they are in FromPackageJSON and
// FromPyProject and for the same reason. A virtual manifest — a Cargo.toml
// holding only `[workspace]`, which is how a Cargo workspace root is written —
// has no `[package]` table at all, and `version.workspace = true` states the
// version somewhere else entirely. Neither is an error: the crates below such a
// root still own their files, and refusing to resolve would leave a whole Rust
// tree unindexable over a manifest field that Cargo itself does not require. An
// absent one becomes Unknown, which can never false-match (§4.3).
//
// A workspace member resolves against the *nearest* manifest at or above it
// (Resolve's walk), so a member crate gets its own `[package]` name and not the
// workspace root's. The one layout this does not reach is a member whose
// manifest inherits `name` from the workspace — which Cargo does not allow for
// `name`, only for `version` and the metadata fields — so the crate is always
// named, and only its version can land on Unknown.
func FromCargo(dir string) (Coord, error) {
	path := filepath.Join(dir, CargoFile)
	f, err := os.Open(path)
	if err != nil {
		return Coord{}, err
	}
	defer func() { _ = f.Close() }()

	name, version, err := scanTomlTable(f, "package")
	if err != nil {
		return Coord{}, fmt.Errorf("%s: %w", path, err)
	}

	return Coord{
		Scheme:  RustScheme,
		Manager: CargoManager,
		Name:    or(name, Unknown),
		Version: or(version, Unknown),
		Root:    dir,
	}, nil
}

// scanTomlTable pulls `name` and `version` out of one named top-level table of a
// TOML file.
//
// A line scan rather than a TOML parser, for the reason scanModulePath and
// scanProjectTable give: two scalar strings out of one known table is not a
// parsing problem, and the dependency surface stays at the standard library —
// which is itself part of what §14 M9+ claims an additional language costs,
// since a fourth language that had to add a module to go.mod would not be "one
// sub-package plus a query plus a resolver".
//
// It is scanProjectTable generalised over the table name, and the three pieces
// of TOML that actually matter are handled the same way: a table header ends the
// previous table, a `#` outside a string starts a comment, and a multi-line
// string can contain anything at all — including a line that looks like a table
// header — so the scanner tracks triple-quote state rather than trusting line
// boundaries. scanProjectTable is left calling its own copy: pyproject.go is
// M7's and rewiring it is not this milestone's to do, but the two should become
// one call site.
func scanTomlTable(f *os.File, table string) (name, version string, err error) {
	header := "[" + table + "]"
	sc := bufio.NewScanner(f)
	inTable := false
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
			// The header exactly: `[package.metadata]` is a different table,
			// and `[[package]]` is not one at all.
			inTable = trimmed == header
			continue
		}
		if opened, d := opensMultiline(trimmed); opened {
			delim = d
			continue
		}
		if !inTable {
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
			// `version.workspace = true` and `version = { workspace = true }`
			// both state the version elsewhere; tomlString reduces a non-string
			// to "", which becomes Unknown.
			version = tomlString(value)
		}
	}
	if err := sc.Err(); err != nil {
		return "", "", err
	}
	return name, version, nil
}
