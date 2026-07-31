package coord

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PHPScheme and ComposerManager are the SCIP scheme/manager pair for PHP
// packages (SPEC.md §4.3), the eighth of the pairs beside GoScheme/GoManager,
// TSScheme/NPMManager, PyScheme/PipManager, RustScheme/CargoManager,
// JavaScheme/MavenManager, CSharpScheme/NuGetManager and RubyScheme/GemManager.
//
// The scheme is named for the language and the manager for the ecosystem's
// package manager, which is Composer. Composer is a resolver over Packagist
// rather than a registry of its own, exactly as Bundler is over RubyGems — and
// the name written here is the one the tool uses, because `composer.json` is the
// file this reads and `composer` is the word a reader of a descriptor will
// recognise.
//
// Only one property of the pair is load-bearing — no coordinate of one ecosystem
// may render a prefix another can render, since the link pass joins on the
// descriptor string and nothing else (§7). Eight distinct schemes give that for
// free, and the manager is then documentation.
const (
	PHPScheme       = "scip-php"
	ComposerManager = "composer"
)

// ComposerFile is the manifest FromComposer reads, and it is the first manifest
// since `go.mod` that needs no apology.
//
// `Ecosystem.Manifest` does two jobs: it is the filename `Resolve` stats to find
// the repository root, and it is the file `From` reads for an identity. Six of
// the seven ecosystems before this one made at least one of the two awkward —
// `*.csproj` and `*.gemspec` are glob-named and so cannot be a manifest at all
// (coord/nuget.go, coord/gemfile.go), Ruby had to split the two jobs across
// `Gemfile` and `Gemfile.lock`, Maven's identity is inherited from a parent POM
// this cannot follow, and go.mod states no version. `composer.json` is
// fixed-name, is JSON, sits at the root of every Composer project by definition,
// and states `name` outright.
//
// The one thing it does *not* reliably state is `version`, and that is a
// convention rather than an accident: Composer's own documentation tells library
// authors to omit it, because Packagist derives the version from the git tag and
// a hand-written one immediately goes stale. So a library's composer.json
// usually has a name and no version, an application's often has neither, and a
// version is present mainly in packages distributed outside a VCS.
//
// An omitted version renders `Unknown` — SCIP's "." — and nothing else. That is
// the rule coord/nuget.go and coord/gemfile.go established and the reason is
// unchanged and worth restating, because PHP is the ecosystem where the
// temptation is strongest: a version that renders is a version the link pass
// joins on, so defaulting to `1.0.0`, or to `dev-main`, or to the branch alias
// in `extra.branch-alias`, would make every unversioned PHP repository in an
// index render one coordinate and match every other one. `.` cannot collide
// with a real component, so it names an unresolved package rather than the
// wrong package (§4.3).
const ComposerFile = "composer.json"

// PHPExt is the file extension the Composer ecosystem owns here. It repeats
// extract/php.Ext, for the reason coord.GoExt gives.
//
// `.php` and nothing else. `.phtml`, `.php4`, `.php5`, `.phps` and `.inc` are
// all PHP the language and are deliberately absent: an extension this owns but
// no parser reads is a registration nothing consults, and an extension a parser
// reads but no ecosystem owns has no scheme to be stamped with — which is what
// TestExtensionsMatchTheParserRegistry exists to catch. `.phtml` is the one with
// a real argument for it, and it loses on a second count: a template is mostly
// HTML with PHP islands, and this stanza's grammar reads a file that begins
// `<?php`.
const PHPExt = ".php"

func init() {
	Register(Ecosystem{
		Manifest: ComposerFile,
		Scheme:   PHPScheme,
		Manager:  ComposerManager,
		Exts:     []string{PHPExt},
		From:     FromComposer,
	})
}

// composerJSON is the part of a composer.json a coordinate is made of. The file
// is mostly dependency and autoload configuration; two fields of it are
// identity.
//
// `autoload.psr-4` is deliberately not among them. It maps a namespace prefix to
// a directory, and this stanza reads the namespace out of the file's own
// `namespace` statement instead — see extract/php/php.go, which says why the map
// is redundant with a declaration PHP requires anyway.
type composerJSON struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// FromComposer reads dir/composer.json and returns the PHP package's
// coordinate.
//
// Both fields reduce to Unknown when absent rather than to an error, which is
// FromPackageJSON's rule and matters more here: an omitted `version` is the
// *recommended* state of a library's manifest, not a defect, and a root
// `composer.json` that only lists `require` entries — which is every
// application — states no name either. A package with no declared name still
// owns its files, and refusing to resolve would leave a whole PHP tree
// unindexable over a field its authors were told not to write.
//
// Malformed JSON *is* an error, which is where this parts company with
// coord/nuget.go and coord/maven.go and joins coord/npm.go, coord/gomod.go,
// coord/cargo.go and coord/pyproject.go. The two lenient ones read XML property
// sheets, which are edited by hand and are frequently half-valid; a
// composer.json is validated by every `composer` invocation and is regenerated
// by `composer require`, so a file that does not parse is a broken tree rather
// than an incomplete manifest, and saying so beats silently indexing it under
// `. .`.
func FromComposer(dir string) (Coord, error) {
	path := filepath.Join(dir, ComposerFile)
	body, err := os.ReadFile(path) //nolint:gosec // dir is the repository root the batch resolved.
	if err != nil {
		return Coord{}, err
	}

	var pkg composerJSON
	if err := json.Unmarshal(body, &pkg); err != nil {
		return Coord{}, fmt.Errorf("%s: %w", path, err)
	}

	return Coord{
		Scheme:  PHPScheme,
		Manager: ComposerManager,
		Name:    or(strings.TrimSpace(pkg.Name), Unknown),
		Version: or(strings.TrimSpace(pkg.Version), Unknown),
		Root:    dir,
	}, nil
}
