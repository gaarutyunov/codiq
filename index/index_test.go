package index

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/extract"
	"github.com/gaarutyunov/codiq/facts"
	"github.com/gaarutyunov/codiq/link"
	"github.com/gaarutyunov/codiq/store"
)

// The tests in this file run the real walk and the real extractor over real Go
// source, and stop at the database. What this package contributes beyond its
// collaborators is the walk's selection rules and the decision of which per-file
// error is fatal, and both are settled before any SQL is reached — so both are
// tested against the real store.ErrParseFailed sentinel and a recording load
// rather than against Postgres. store, link and extract each own the tests for
// their own behaviour; the end-to-end assertion that the pipeline produces a
// cross-file edge over MCP is the M2 integration suite's.

// goMod is the manifest coord.Resolve needs at the root of every fixture tree.
const goMod = "module github.com/foo/bar\n\ngo 1.25.0\n"

// tree writes a fixture repository from a path -> contents map and returns its
// root. Paths are slash-separated and relative; directories are created as
// needed.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
	return root
}

func TestWalkSelectsOnlySupportedFiles(t *testing.T) {
	// Guards the assumption every case below rests on: the walk filters on
	// extract's registry, so a case listing a ".ts" file as unsupported is only
	// meaningful while ".ts" really is.
	require.Equal(t, []string{".go"}, extract.Extensions(),
		"the registry grew a language; revisit the unsupported files in these cases")

	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name: "only files with a registered parser",
			files: map[string]string{
				"go.mod":     goMod,
				"main.go":    "package main\n",
				"README.md":  "# nope\n",
				"query.scm":  "(nope)\n",
				"app.ts":     "export {}\n",
				"Makefile":   "all:\n",
				"noext":      "\n",
				"go.mod.bak": goMod,
			},
			want: []string{"main.go"},
		},
		{
			name: "nested packages are walked",
			files: map[string]string{
				"go.mod":                  goMod,
				"main.go":                 "package main\n",
				"internal/store/a.go":     "package store\n",
				"internal/store/b.go":     "package store\n",
				"internal/deep/er/c.go":   "package er\n",
				"internal/deep/notes.txt": "\n",
			},
			want: []string{
				"internal/deep/er/c.go",
				"internal/store/a.go",
				"internal/store/b.go",
				"main.go",
			},
		},
		{
			name: "generated and metadata trees are pruned",
			files: map[string]string{
				"go.mod":                      goMod,
				"main.go":                     "package main\n",
				".git/hooks/pre-commit.go":    "package hooks\n",
				".worktrees/issue-3/main.go":  "package main\n",
				"vendor/example.com/dep/d.go": "package dep\n",
				"extract/testdata/fixture.go": "package fixture\n",
				"node_modules/pkg/index.go":   "package pkg\n",
				"_ignored/x.go":               "package ignored\n",
				"docs/keep.go":                "package docs\n",
			},
			want: []string{"docs/keep.go", "main.go"},
		},
		{
			name:  "a tree with no supported files selects nothing",
			files: map[string]string{"go.mod": goMod, "README.md": "# nope\n"},
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := tree(t, tt.files)

			got, err := walk(root)
			require.NoError(t, err)

			rel := make([]string, 0, len(got))
			for _, p := range got {
				rel = append(rel, relative(root, p))
			}
			assert.Equal(t, tt.want, nilIfEmpty(rel))
			assert.True(t, sort.StringsAreSorted(got), "walk returns sorted paths")
		})
	}
}

// A pruned name at the root is not pruned: the loader has to be able to index a
// directory whose own name happens to be on the list, which is exactly what
// pointing it at a fixture under testdata/ does.
func TestWalkDoesNotPruneItsOwnRoot(t *testing.T) {
	root := tree(t, map[string]string{
		"testdata/greeter/go.mod":  goMod,
		"testdata/greeter/main.go": "package main\n",
	})
	greeter := filepath.Join(root, "testdata", "greeter")

	got, err := walk(greeter)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "main.go", relative(greeter, got[0]))
}

// recorder stands in for the two collaborators that need a database, and
// remembers what it was asked to do. Both are plain functions rather than
// interfaces — store.ReplaceFile and link.RebuildAll are one function each, not
// a role with implementations — so there is no interface here to generate a mock
// for.
type recorder struct {
	mu      sync.Mutex
	loaded  []facts.FileFacts
	relinks int

	// poison names repo-relative paths the store should reject as unparseable.
	// It is how these tests get a poison file at all: tree-sitter is
	// error-tolerant by design, so no Go source makes the real extractor report a
	// parse failure — a real ParseError comes from a broken grammar or a runtime
	// fault, neither of which a fixture can arrange. Injecting it at the store
	// boundary is where the loader actually meets it, and it keeps both the facts
	// and the sentinel real.
	poison map[string]string
	// loadErr names paths whose load fails for a reason that is not a parse
	// failure.
	loadErr map[string]error
	// deadlocks counts down a transient serialization failure per path: while it
	// is positive the load fails the way PostgreSQL fails a deadlock victim.
	deadlocks map[string]int
	// attempts counts every load call per path, retries included.
	attempts map[string]int
}

// load stands in for store.ReplaceFile, and delegates to the real one for a
// poison file so the error the loader has to recognise is produced by the real
// code path: ReplaceFile checks ParseError and returns before it ever touches
// the database, so a nil handle is never dereferenced.
func (r *recorder) load(ctx context.Context, db store.DB, ff facts.FileFacts) error {
	r.mu.Lock()
	if r.attempts == nil {
		r.attempts = map[string]int{}
	}
	r.attempts[ff.File.Path]++
	parseErr, poisoned := r.poison[ff.File.Path]
	err, failed := r.loadErr[ff.File.Path]
	deadlocked := r.deadlocks[ff.File.Path] > 0
	if deadlocked {
		r.deadlocks[ff.File.Path]--
	}
	if !poisoned && !failed && !deadlocked {
		r.loaded = append(r.loaded, ff)
	}
	r.mu.Unlock()

	switch {
	case poisoned:
		ff.ParseError = parseErr
		return store.ReplaceFile(ctx, db, ff)
	case deadlocked:
		// What pgx surfaces when PostgreSQL picks this transaction as the
		// deadlock victim, wrapped the way store.ReplaceFile wraps it.
		return fmt.Errorf("store: delete %s: resolves_to (target): %w",
			ff.File.Path, &pgconn.PgError{Code: "40P01", Message: "deadlock detected"})
	case failed:
		return err
	default:
		return nil
	}
}

func (r *recorder) relink(context.Context, link.DB) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.relinks++
	return nil
}

// paths returns the repo-relative paths that were loaded, sorted.
func (r *recorder) paths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.loaded))
	for _, ff := range r.loaded {
		out = append(out, ff.File.Path)
	}
	sort.Strings(out)
	return nilIfEmpty(out)
}

// facts returns the loaded facts by repo-relative path.
func (r *recorder) facts() map[string]facts.FileFacts {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]facts.FileFacts, len(r.loaded))
	for _, ff := range r.loaded {
		out[ff.File.Path] = ff
	}
	return out
}

// newLoader builds a loader over the real extractor registry and the recorder,
// with the worker limit the run should use.
func newLoader(r *recorder, limit int) loader {
	return loader{
		parserFor: extract.ParserFor,
		load:      r.load,
		relink:    r.relink,
		limit:     limit,
	}
}

// repo is SPEC.md §14 M2's two-file module — main calling a Greeter defined in
// another file — plus a package below it and the trees the walk has to ignore
// around it.
var repo = map[string]string{
	"go.mod": goMod,
	"main.go": `package main

import "fmt"

func main() {
	g := &Greeter{Name: "world"}
	fmt.Println(g.Greet())
}
`,
	"greeter.go": `package main

type Greeter struct {
	Name string
}

func (g *Greeter) Greet() string { return "hello, " + g.Name }
`,
	"internal/store/store.go": `package store

type Store struct{}
`,
	"README.md":                 "# not indexed\n",
	"vendor/example.com/x/x.go": "package x\n",
	"testdata/fixture.go":       "package fixture\n",
}

func TestRun(t *testing.T) {
	tests := []struct {
		name    string
		poison  map[string]string
		loadErr map[string]error
		limit   int

		wantLoaded  []string
		wantFiles   int
		wantSkipped []string
		wantRelinks int
		wantErr     string
	}{
		{
			name:        "every supported file is loaded and the graph is linked once",
			limit:       4,
			wantLoaded:  []string{"greeter.go", "internal/store/store.go", "main.go"},
			wantFiles:   3,
			wantRelinks: 1,
		},
		{
			// The regression test for SPEC.md §14 M2's skeleton, which returns
			// store.ReplaceFile's error straight into the errgroup and so would
			// abort the walk here — losing greeter.go and store.go and never
			// linking — instead of skipping the one bad file (§5).
			name:        "a poison file is skipped, the rest still load, and the link still runs",
			poison:      map[string]string{"main.go": "unexpected token at 12"},
			limit:       4,
			wantLoaded:  []string{"greeter.go", "internal/store/store.go"},
			wantFiles:   3,
			wantSkipped: []string{"main.go"},
			wantRelinks: 1,
		},
		{
			name: "every file being poison is still not a failure",
			poison: map[string]string{
				"main.go":                 "unexpected token at 12",
				"greeter.go":              "unexpected token at 3",
				"internal/store/store.go": "unexpected token at 40",
			},
			limit:       4,
			wantFiles:   3,
			wantSkipped: []string{"greeter.go", "internal/store/store.go", "main.go"},
			wantRelinks: 1,
		},
		{
			// The other half of the asymmetry: a failure that is not a parse
			// failure is fatal, and the derived tables are left alone rather than
			// rebuilt over a load that is known to be incomplete.
			name:    "a load failure aborts the run and skips the link",
			loadErr: map[string]error{"main.go": errors.New("connection reset")},
			// One worker over the walk's sorted order, so which files got as far
			// as the store before the failure is determined rather than raced.
			limit:       1,
			wantLoaded:  []string{"greeter.go", "internal/store/store.go"},
			wantFiles:   3,
			wantRelinks: 0,
			wantErr:     "connection reset",
		},
		{
			// Serialised, to show the skip bookkeeping is not an artefact of the
			// workers happening to interleave.
			name:        "one worker behaves the same as many",
			poison:      map[string]string{"greeter.go": "unexpected token at 3"},
			limit:       1,
			wantLoaded:  []string{"internal/store/store.go", "main.go"},
			wantFiles:   3,
			wantSkipped: []string{"greeter.go"},
			wantRelinks: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := tree(t, repo)
			rec := &recorder{poison: tt.poison, loadErr: tt.loadErr}

			// A nil handle: nothing in this run reaches a database.
			res, err := newLoader(rec, tt.limit).run(t.Context(), nil, root)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.wantFiles, res.Files, "files selected by the walk")
			assert.Equal(t, tt.limit, res.Concurrency)
			assert.Equal(t, tt.wantRelinks, rec.relinks, "link.RebuildAll calls")
			assert.Equal(t, tt.wantLoaded, rec.paths(), "files handed to the store")

			skipped := make([]string, 0, len(res.Skipped))
			for _, s := range res.Skipped {
				skipped = append(skipped, s.Path)
				assert.ErrorIs(t, s.Err, store.ErrParseFailed, "a skip carries the sentinel it was recognised by")
				assert.Contains(t, s.Err.Error(), s.Path, "a skip names its file")
			}
			assert.Equal(t, tt.wantSkipped, nilIfEmpty(skipped), "files reported as skipped")

			if tt.wantErr == "" {
				assert.Equal(t, res.Files, res.Loaded+len(res.Skipped),
					"every selected file is either loaded or reported skipped")
				assert.Equal(t, len(tt.wantLoaded), res.Loaded)
			}
		})
	}
}

// A skipped file costs only itself: the files that did load carry their real
// facts, and — the thing the milestone turns on — the cross-file join key
// survives, so the link pass the run still performs has something to join.
func TestRunSkippingOneFileLeavesTheOthersIntact(t *testing.T) {
	root := tree(t, repo)
	rec := &recorder{poison: map[string]string{"internal/store/store.go": "unexpected token at 40"}}

	res, err := newLoader(rec, 4).run(t.Context(), nil, root)
	require.NoError(t, err)
	require.Len(t, res.Skipped, 1)
	require.Equal(t, 1, rec.relinks)

	loaded := rec.facts()
	require.Contains(t, loaded, "main.go")
	require.Contains(t, loaded, "greeter.go")

	def, ok := occurrence(loaded["greeter.go"], facts.RoleDefinition, "Greet")
	require.True(t, ok, "greeter.go defines Greet")
	ref, ok := occurrence(loaded["main.go"], facts.RoleReference, "Greet")
	require.True(t, ok, "main.go references Greet")
	assert.Equal(t, def.Descriptor.String(), ref.Descriptor.String(),
		"the descriptor the link pass joins on is intact either side of the skip")
}

func occurrence(ff facts.FileFacts, role facts.Role, name string) (facts.Occurrence, bool) {
	for _, o := range ff.Occurrences {
		if o.Role == role && o.Name == name {
			return o, true
		}
	}
	return facts.Occurrence{}, false
}

// Two files that reference each other's definitions delete the same cross-file
// edge rows from opposite endpoint sides, so re-indexing a tree that is already
// linked can deadlock — a failure the first index of a tree cannot produce,
// because there are no derived rows to delete yet. It is transient by
// definition, so the file is retried rather than failed (SPEC.md §5, "a failing
// file is retried in isolation"), and the run is expected to come out whole.
func TestRunRetriesADeadlockedFile(t *testing.T) {
	tests := []struct {
		name         string
		deadlocks    int
		wantErr      bool
		wantAttempts int
		wantRetries  int
		wantLoaded   int
	}{
		{name: "the victim wins on the retry", deadlocks: 1, wantAttempts: 2, wantRetries: 1, wantLoaded: 3},
		{name: "and again if it loses twice", deadlocks: 2, wantAttempts: 3, wantRetries: 2, wantLoaded: 3},
		{
			// The cap is what turns a pathological case into an error instead of
			// a hang. The last attempt's error is the one reported, and it is
			// fatal like any other load failure: no link over a partial load.
			name:      "a file that never gets through is a failure, not a skip",
			deadlocks: maxLoadAttempts, wantErr: true,
			wantAttempts: maxLoadAttempts, wantRetries: 0, wantLoaded: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := tree(t, repo)
			rec := &recorder{deadlocks: map[string]int{"main.go": tt.deadlocks}}

			res, err := newLoader(rec, 4).run(t.Context(), nil, root)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "deadlock detected")
				assert.Equal(t, 0, rec.relinks, "no link over a load that did not finish")
			} else {
				require.NoError(t, err)
				assert.Equal(t, 1, rec.relinks)
				assert.Empty(t, res.Skipped, "a deadlock is not a poison file")
			}
			assert.Equal(t, tt.wantAttempts, rec.attempts["main.go"], "load attempts for the victim")
			assert.Equal(t, tt.wantRetries, res.Retries)
			assert.Len(t, rec.loaded, tt.wantLoaded)
		})
	}
}

// The `file` table holds repo-relative, slash-separated paths (facts.File),
// while the parser is handed the walk path because that is what the
// coordinate's namespace resolves against. Both halves of that translation are
// asserted here: a regression in either is invisible until a path or a
// descriptor lands wrong in the database.
func TestRunNormalizesPathsAndKeepsNamespaces(t *testing.T) {
	root := tree(t, repo)
	rec := &recorder{}

	res, err := newLoader(rec, 2).run(t.Context(), nil, root)
	require.NoError(t, err)

	assert.Equal(t, []string{"greeter.go", "internal/store/store.go", "main.go"}, rec.paths(),
		"paths are repo-relative and slash-separated")

	// The coordinate is resolved once from go.mod and reaches every file.
	assert.Equal(t, "scip-go gomod github.com/foo/bar .", res.Coord.Prefix())
	loaded := rec.facts()
	for path, ff := range loaded {
		assert.Equal(t, res.Coord, ff.File.Coord, "%s carries the repo coordinate", path)
	}

	// The namespace the parser derives from the path it was handed shows up in
	// the descriptors: empty at the module root, the package's directory below
	// it. Handing the parser a repo-relative path instead would silently empty
	// the second one.
	root0, ok := occurrence(loaded["greeter.go"], facts.RoleDefinition, "Greeter")
	require.True(t, ok)
	assert.Equal(t, "scip-go gomod github.com/foo/bar . Greeter#", root0.Descriptor.String())

	nested, ok := occurrence(loaded["internal/store/store.go"], facts.RoleDefinition, "Store")
	require.True(t, ok)
	assert.Equal(t, "scip-go gomod github.com/foo/bar . internal/store/Store#", nested.Descriptor.String())
}

func TestRunRequiresAManifest(t *testing.T) {
	root := tree(t, map[string]string{"main.go": "package main\n"})

	_, err := newLoader(&recorder{}, 2).run(t.Context(), nil, root)
	require.ErrorIs(t, err, coord.ErrNoManifest)
}

func TestRunRejectsAMissingRepo(t *testing.T) {
	// Inside a module, so the run gets past coord.Resolve and fails in the walk
	// — the failure this is about.
	missing := filepath.Join(tree(t, map[string]string{"go.mod": goMod}), "nope")

	_, err := newLoader(&recorder{}, 2).run(t.Context(), nil, missing)
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestDefaultConcurrencyIsPositive(t *testing.T) {
	assert.GreaterOrEqual(t, DefaultConcurrency(), 1)
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}
