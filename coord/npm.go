package coord

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TSScheme and NPMManager are the SCIP scheme/manager pair for npm packages
// (SPEC.md §4.3), the TypeScript counterpart of GoScheme/GoManager.
//
// The pair is what makes a TypeScript symbol distinguishable from a Go one
// without parsing the rest of the descriptor: the link pass joins on the
// rendered string (§7), and `scip-typescript npm …` shares no prefix with
// `scip-go gomod …`, so two ecosystems can occupy one graph without any
// possibility of a false match — which is the whole of "cross-language queries
// work with no schema change".
const (
	TSScheme   = "scip-typescript"
	NPMManager = "npm"
)

// PackageJSONFile is the manifest FromPackageJSON reads.
const PackageJSONFile = "package.json"

// TSExt is the file extension the npm ecosystem owns. It repeats
// extract/ts.Ext, for the reason coord.GoExt gives.
const TSExt = ".ts"

func init() {
	Register(Ecosystem{
		Manifest: PackageJSONFile,
		Scheme:   TSScheme,
		Manager:  NPMManager,
		Exts:     []string{TSExt},
		From:     FromPackageJSON,
	})
}

// packageJSON is the part of a package.json a coordinate is made of. The file
// is mostly build configuration; two fields of it are identity.
type packageJSON struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// FromPackageJSON reads dir/package.json and returns the npm package's
// coordinate.
//
// Unlike go.mod, package.json states the package's own version, so the version
// component is real rather than the major-suffix approximation FromGoMod is
// reduced to. Both fields are optional in practice — a private workspace root
// often has neither — and an absent one becomes Unknown rather than an error: a
// package with no declared name still owns its files, and refusing to resolve
// would leave a whole TypeScript tree unindexable over a missing manifest field.
func FromPackageJSON(dir string) (Coord, error) {
	path := filepath.Join(dir, PackageJSONFile)
	body, err := os.ReadFile(path)
	if err != nil {
		return Coord{}, err
	}

	var pkg packageJSON
	if err := json.Unmarshal(body, &pkg); err != nil {
		return Coord{}, fmt.Errorf("%s: %w", path, err)
	}

	return Coord{
		Scheme:  TSScheme,
		Manager: NPMManager,
		Name:    or(strings.TrimSpace(pkg.Name), Unknown),
		Version: or(strings.TrimSpace(pkg.Version), Unknown),
		Root:    dir,
	}, nil
}
