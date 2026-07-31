package coord

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// RubyScheme and GemManager are the SCIP scheme/manager pair for Ruby gems
// (SPEC.md §4.3), the seventh of the pairs beside GoScheme/GoManager,
// TSScheme/NPMManager, PyScheme/PipManager, RustScheme/CargoManager,
// JavaScheme/MavenManager and CSharpScheme/NuGetManager.
//
// The scheme is named for the language and the manager for the ecosystem's
// package manager, which is RubyGems — reached through Bundler, but Bundler is a
// resolver over RubyGems and not a registry of its own.
//
// Only one property of the pair is load-bearing — no coordinate of one ecosystem
// may render a prefix another can render, since the link pass joins on the
// descriptor string and nothing else (§7). Seven distinct schemes give that for
// free, and the manager is then documentation.
const (
	RubyScheme = "scip-ruby"
	GemManager = "gem"
)

// GemfileName is the manifest FromGemfile is registered under, and LockName is
// the file it actually reads. The two being different is this resolver's one
// oddity, and it is forced by the shape of the Ruby tree rather than chosen.
//
// `Ecosystem.Manifest` does two jobs: it is the filename `Resolve` stats to find
// the repository root, and it is the file `From` reads for an identity. In Ruby
// no single file does both.
//
//   - `*.gemspec` states the identity — name, version, and nothing else does —
//     and cannot be a manifest. It is glob-named, exactly as `Greeter.csproj` is
//     (coord/nuget.go says why a glob is not a filename), and it is worse than
//     the C# case besides: a gemspec is *executable Ruby*, and the idiomatic one
//     reads its version out of a `lib/<gem>/version.rb` constant, so even given
//     its name there is no version in it to read.
//   - `Gemfile` is fixed-name and near-universal — Bundler requires it, so a
//     Rails application, a gem under development and a bare script directory
//     with any dependency at all all have one — but it describes *dependencies*
//     and not identity. The Gemfile of a gem is usually two lines: a `source`
//     and the word `gemspec`.
//   - `Gemfile.lock` is fixed-name and does state identity, in the one case that
//     matters: when the tree being indexed is itself a gem, Bundler writes a
//     `PATH` section whose `remote:` is `.` and whose `specs:` names the gem and
//     its version. It is generated, machine-readable and exact. But it is not
//     always present — a library's Gemfile.lock is conventionally *not*
//     committed — so it cannot be what `Resolve` looks for.
//
// So the manifest is the `Gemfile`, which answers "is this a Ruby tree, and
// where does it begin", and `FromGemfile` reads the lock beside it for "what is
// this tree called". A tree with a Gemfile and no lock, or with a lock that has
// no `PATH remote: .` section — which is every application, since an application
// is not a package — resolves to Unknown for both components. That is the C#
// `Directory.Build.props`-absent case one ecosystem on, and Unknown is right for
// the same reason: a version that renders is a version the link pass joins on,
// so inventing one would make every unversioned Ruby tree in an index collide
// with every other. Unknown does not join (§4.3).
const (
	GemfileName = "Gemfile"
	LockName    = "Gemfile.lock"
)

// RubyExt is the file extension the Ruby ecosystem owns here. It repeats
// extract/rb.Ext, for the reason coord.GoExt gives.
//
// `.rb` and nothing else. `.rake`, `.gemspec`, `Rakefile` and `config.ru` are all
// Ruby the language and are deliberately absent: an extension this owns but no
// parser reads is a registration nothing consults, and the extension-less ones
// are not extensions at all — `extract.ParserFor` keys on `filepath.Ext`, which
// is "" for a `Rakefile`, and "" is not a key any ecosystem may own.
const RubyExt = ".rb"

func init() {
	Register(Ecosystem{
		Manifest: GemfileName,
		Scheme:   RubyScheme,
		Manager:  GemManager,
		Exts:     []string{RubyExt},
		From:     FromGemfile,
	})
}

// FromGemfile returns the Ruby tree's coordinate, reading dir/Gemfile.lock for
// the identity the Gemfile itself does not state.
//
// Only an *unreadable Gemfile* is an error, which is the line every other
// resolver draws: `Resolve` stat'd it, so failing to open it means something is
// wrong with the tree rather than with the manifest's contents. A missing or
// unparseable lock is not an error — it is the ordinary state of an application
// or of a gem whose lock is gitignored — and it yields Unknown, which can never
// false-match.
func FromGemfile(dir string) (Coord, error) {
	f, err := os.Open(filepath.Join(dir, GemfileName)) //nolint:gosec // dir is the repository root the batch resolved.
	if err != nil {
		return Coord{}, err
	}
	_ = f.Close()

	name, version := lockedGemIdentity(dir)

	return Coord{
		Scheme:  RubyScheme,
		Manager: GemManager,
		Name:    or(name, Unknown),
		Version: or(version, Unknown),
		Root:    dir,
	}, nil
}

// lockedGemIdentity reads the gem this tree *is* out of dir/Gemfile.lock.
//
// A lock file is a sequence of blocks introduced by an unindented keyword, and
// only one of them describes the tree being indexed: `PATH`, whose `remote:` is
// `.`. Every other block — `GEM`, `GIT`, `PLATFORMS`, `DEPENDENCIES`,
// `BUNDLED WITH` — names somebody else's code or no code at all, and a `PATH`
// with any other remote is a sibling checkout that this repository depends on
// rather than is.
//
// Inside the block the first entry under `specs:` is the gem, written
// `name (version)`; the lines below it are its dependencies, indented one level
// further, which is the whole of what separates them. Scanned rather than
// parsed, for the reason coord/cargo.go gives: two scalars out of one known block
// is not a parsing problem, and the dependency surface stays at the standard
// library — which is itself part of what §14 M9+ claims an additional language
// costs.
func lockedGemIdentity(dir string) (name, version string) {
	f, err := os.Open(filepath.Join(dir, LockName)) //nolint:gosec // dir is the repository root the batch resolved.
	if err != nil {
		return "", ""
	}
	defer func() { _ = f.Close() }()
	return scanLockPathSection(f)
}

// scanLockPathSection walks a Gemfile.lock for the first `PATH` block whose
// remote is this directory, and returns the gem it names.
func scanLockPathSection(r io.Reader) (name, version string) {
	const (
		outside = iota
		inPath  // inside a PATH block, remote not yet seen to be "."
		inOurs  // inside a PATH block whose remote is "."
		inSpecs // …and past its `specs:` line
	)
	state := outside
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			// An unindented line opens a new block, which ends whatever was open.
			if trimmed == "PATH" {
				state = inPath
			} else {
				state = outside
			}
			continue
		}
		switch state {
		case inPath:
			switch trimmed {
			case "remote: .":
				state = inOurs
			case "specs:":
				// A PATH block for somebody else's checkout: skip its contents.
				state = outside
			}
		case inOurs:
			if trimmed == "specs:" {
				state = inSpecs
			}
		case inSpecs:
			// `    name (version)` at four spaces is the gem; anything deeper is
			// one of its dependencies.
			if indentOf(line) != 4 {
				continue
			}
			return cutParenthesised(trimmed)
		}
	}
	return "", ""
}

// indentOf counts the leading spaces of a line. Bundler writes the lock with
// spaces only, so a tab is not a case to handle — and a line indented some other
// way is one this scanner declines to read rather than one it guesses at.
func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// cutParenthesised splits `name (version)` into its two halves. A spec line
// always carries both, so a line without the parenthesised half states no
// version and yields "" for it — which becomes Unknown.
func cutParenthesised(s string) (name, version string) {
	open := strings.Index(s, " (")
	if open < 0 || !strings.HasSuffix(s, ")") {
		return s, ""
	}
	return s[:open], s[open+2 : len(s)-1]
}
