// The corpus suite: features/corpus.feature against the real stack.
//
// No new harness and no new stack. m1_test.go's startStack brings up the one
// composition the package shares — postgres:19beta2, the committed migrations
// applied by the real `gopgql migrate`, and `gopgql-mcp` over streamable HTTP —
// and m2State is embedded, so the `codiq` invocation, the graph reset and M1's
// MCP handshake are the same code every other milestone's suite runs. What this
// file adds is a fixture built to collide and the assertions that it does not.
//
// The one thing it does *not* reuse is m2State.moduleIndexed, because that runs
// `codiq` without `-corpus` and this suite's whole subject is the flag. index
// below is the same command with the name added.
package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
)

// TestCorpusFeatures is the godog entry point for the corpus milestone, scoped
// to its own feature file so that each milestone's suite owns the scenarios it
// has steps for.
func TestCorpusFeatures(t *testing.T) {
	ctx := context.Background()
	repoRoot = mustRepoRoot(t)

	startStack(t, ctx)

	suite := godog.TestSuite{
		Name:                "corpus",
		ScenarioInitializer: InitializeCorpusScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{filepath.Join(repoRoot, "features", "corpus.feature")},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("corpus feature scenarios failed")
	}
}

// collidingRepo is one repository of the pair, written out twice into two
// parents. Every path, every directory name and every symbol name is shared, so
// nothing about the two trees distinguishes them except which directory they
// are in — which is exactly the state the corpus has to make distinguishable.
//
// It carries no manifest of its own. That is the fixture's whole point: an
// ecosystem with a manifest was never in danger, because its module path or
// package name already separated it. The repository at risk is the one that
// declares nothing, which before the corpus meant it borrowed a coordinate from
// outside itself.
// It is deliberately the M2 acceptance fixture's shape — one package split over
// two files, the second referring to a type the first defines — because that
// shape is what produces a *cross-file* edge, and a cross-file edge is the only
// kind the link pass materialises from a descriptor match. A repository whose
// files never refer to one another would satisfy "no derived edge joins two
// corpora" by having no derived edges at all.
var collidingRepo = map[string]string{
	"src/greeter/greeter.go": `package greeter

// Greeter greets by name.
type Greeter struct {
	Name string
}

// Greet returns this Greeter's greeting.
func (g *Greeter) Greet() string {
	return "hello, " + g.Name
}
`,
	"src/greeter/run.go": `package greeter

import "fmt"

// Run is the cross-file reference: it names a type and calls a method the
// other file defines, so the link pass has a descriptor to match.
func Run() {
	g := &Greeter{Name: "world"}
	fmt.Println(g.Greet())
}
`,
}

// collidingParent is what sits *above* each repository: a manifest, and nothing
// else.
//
// Nameless on purpose. `coord.FromPackageJSON` reads a `{"private": true}`
// manifest as a real coordinate whose name and version are both Unknown, so
// unbounded resolution stamped a Go file under it `scip-go gomod . .` — a
// prefix two different parents produce identically. A named manifest would have
// made the two prefixes differ and the fixture would prove less than it claims.
const collidingParent = `{"private": true}` + "\n"

// corpusState is one scenario's state: m2State's, plus the second tree and the
// per-corpus snapshot the re-index scenario compares.
type corpusState struct {
	m2State

	// roots maps a corpus name to the directory indexed under it.
	roots map[string]string
	// beforeCorpus is one corpus's rows as they were when last remembered.
	beforeCorpus map[string]*corpusSnapshot
}

// corpusSnapshot is enough of one corpus to notice another corpus's re-index
// changing it: how many rows it owns in every table, and which uuid each of its
// paths was given.
//
// Per corpus rather than whole-graph, because whole-graph is precisely what
// must *not* be compared here — the other corpus is expected to change.
type corpusSnapshot struct {
	counts  map[string]int
	fileIDs map[string]string
}

func InitializeCorpusScenario(sc *godog.ScenarioContext) {
	st := &corpusState{}

	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "codiq-corpus-tests", Version: "0.1.0"}, nil)
		session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: mcpURL}, nil)
		if err != nil {
			return ctx, fmt.Errorf("mcp handshake with %s: %w", mcpURL, err)
		}
		st.session = session
		st.roots = map[string]string{}
		st.beforeCorpus = map[string]*corpusSnapshot{}
		return ctx, nil
	})

	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if st.session != nil {
			_ = st.session.Close()
			st.session = nil
		}
		if st.tmp != "" {
			_ = os.RemoveAll(st.tmp)
		}
		st.repo, st.tmp, st.report, st.before = "", "", "", nil
		st.roots, st.beforeCorpus = nil, nil
		return ctx, nil
	})

	sc.Step(`^an empty CodiQ graph$`, st.emptyGraph)
	sc.Step(`^two repositories that share a path, a directory and a symbol name$`, st.twoCollidingRepositories)
	sc.Step(`^the two repositories really are indistinguishable by path$`, st.trapIsSet)
	sc.Step(`^both are indexed under their own corpus$`, st.bothIndexed)
	sc.Step(`^"([^"]*)" is indexed again$`, st.corpusIndexedAgain)
	sc.Step(`^"([^"]*)" exists once in "([^"]*)" and once in "([^"]*)"$`, st.pathExistsOnceInEach)
	sc.Step(`^each corpus has its own occurrences of "([^"]*)"$`, st.eachCorpusOwnsItsOccurrences)
	sc.Step(`^no derived edge joins two corpora$`, st.noCrossCorpusEdges)
	sc.Step(`^every file in "([^"]*)" carries the package name "([^"]*)"$`, st.corpusNamesThePackage)
	sc.Step(`^no descriptor in "([^"]*)" appears in "([^"]*)"$`, st.noSharedDescriptors)
	sc.Step(`^"([^"]*)" is exactly what it was before$`, st.corpusUnchanged)
	sc.Step(`^every file in "([^"]*)" kept the identity it was first given$`, st.corpusKeptIdentity)

	// M1's steps, reused as written: the MCP tool call and the JSON comparison.
	sc.Step(`^an agent asks over MCP:$`, st.agentAsks)
	sc.Step(`^the answer is:$`, st.answerIs)
}

// --- given -----------------------------------------------------------------

// twoCollidingRepositories lays down the pair:
//
//	<tmp>/one/package.json      {"private": true}
//	<tmp>/one/repo/…            the corpus `alpha`
//	<tmp>/two/package.json      {"private": true}
//	<tmp>/two/repo/…            the corpus `beta`
//
// Both indexed directories are called `repo` and hold identical trees, so under
// unbounded resolution — Root at the parent — their namespaces are the same
// string, `repo/src/greeter/`. That is the collision; trapIsSet checks each
// piece of it rather than trusting this comment.
func (st *corpusState) twoCollidingRepositories() error {
	dir, err := os.MkdirTemp("", "codiq-corpus-*")
	if err != nil {
		return err
	}
	st.tmp = dir

	for corpus, parent := range map[string]string{"alpha": "one", "beta": "two"} {
		parentDir := filepath.Join(dir, parent)
		if err := os.MkdirAll(parentDir, 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(parentDir, "package.json"), []byte(collidingParent), 0o600); err != nil {
			return err
		}
		// The same base name for both, which is what makes the two
		// parent-relative namespaces identical.
		repo := filepath.Join(parentDir, "repo")
		for rel, body := range collidingRepo {
			path := filepath.Join(repo, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				return err
			}
		}
		st.roots[corpus] = repo
	}
	return nil
}

// trapIsSet asserts the fixture is the trap it claims to be, before anything
// relies on it having been sprung.
//
// Five separate claims, because five separate things had to be true for the
// defect to occur, and any one of them silently ceasing to hold would leave the
// scenario passing for a reason that is not the corpus:
//
//  1. Two distinct directories on disk.
//  2. With the same base name — so their parent-relative namespaces match.
//  3. Neither holds a manifest of its own — so resolution has to look outward.
//  4. Each has a parent that *does* hold one — so there is somewhere outward to
//     look, and both parents' manifests are nameless, so the two would have
//     rendered the same prefix as well as the same namespace.
//  5. The same repo-relative path, defining the same symbol in the same
//     directory — so `file.path` and the descriptor suffix both collide.
//
// This is the convention `TestReplayFromZeroReproducesTheSchema` set in gopgql:
// check the trap is present before relying on it, or a green run means nothing.
func (st *corpusState) trapIsSet() error {
	alpha, beta := st.roots["alpha"], st.roots["beta"]
	if alpha == "" || beta == "" {
		return errors.New("no repositories; the Given step did not run")
	}

	same := filepath.FromSlash("src/greeter/greeter.go")
	alphaFile, betaFile := filepath.Join(alpha, same), filepath.Join(beta, same)
	alphaSrc, err := os.ReadFile(alphaFile)
	if err != nil {
		return err
	}
	betaSrc, err := os.ReadFile(betaFile)
	if err != nil {
		return err
	}

	return check(func(t assert.TestingT) {
		assert.NotEqual(t, alpha, beta, "two distinct repositories")
		assert.Equal(t, filepath.Base(alpha), filepath.Base(beta),
			"the two indexed directories must share a base name, or their parent-relative namespaces differ on their own")

		for _, repo := range []string{alpha, beta} {
			for _, manifest := range []string{"go.mod", "package.json"} {
				assert.NoFileExists(t, filepath.Join(repo, manifest),
					"the repository must declare no manifest, or it was never at risk")
			}
			assert.FileExists(t, filepath.Join(filepath.Dir(repo), "package.json"),
				"a manifest above the repository is what unbounded resolution would have found")
		}

		assert.Equal(t, string(alphaSrc), string(betaSrc),
			"the same symbol in the same directory at the same path, or nothing collides")
		assert.Contains(t, string(alphaSrc), "type Greeter struct")
	})
}

// --- when ------------------------------------------------------------------

// index runs the real binary over one tree, named. It is m2State.moduleIndexed
// with `-corpus` added, which is the one thing this suite cannot borrow.
func (st *corpusState) index(ctx context.Context, corpus string) error {
	repo := st.roots[corpus]
	if repo == "" {
		return fmt.Errorf("no repository for corpus %q; the Given step did not run", corpus)
	}
	dbosDSN, err := dbosConnString(connString)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, codiqBin,
		"-dsn", connString, "-dbos-dsn", dbosDSN, "-corpus", corpus, "-v", repo)
	cmd.Env = codiqEnv()
	out, err := cmd.CombinedOutput()
	st.report = string(out)
	if err != nil {
		return fmt.Errorf("codiq -corpus %s %s: %w\n%s", corpus, repo, err, out)
	}
	// The report is the run's own account of what it did, and it is the only
	// place the corpus the rows were written under is visible without querying.
	if !strings.Contains(st.report, "corpus      "+corpus) {
		return fmt.Errorf("the report does not name the corpus %q:\n%s", corpus, st.report)
	}
	return nil
}

// bothIndexed runs the binary over each tree in turn, into the same database.
// Serially and never concurrently: link.RebuildAll is a whole-graph operation,
// so two indexers race in it even over different repositories (cmd/codiq's
// start records the measurement).
func (st *corpusState) bothIndexed(ctx context.Context) error {
	for _, corpus := range []string{"alpha", "beta"} {
		if err := st.index(ctx, corpus); err != nil {
			return err
		}
	}
	// Remembered after both runs so the re-index scenario compares a corpus
	// against what it looked like with its sibling already present.
	for _, corpus := range []string{"alpha", "beta"} {
		snap, err := st.snapshot(ctx, corpus)
		if err != nil {
			return err
		}
		st.beforeCorpus[corpus] = snap
	}
	return nil
}

func (st *corpusState) corpusIndexedAgain(ctx context.Context, corpus string) error {
	return st.index(ctx, corpus)
}

// --- then ------------------------------------------------------------------

// pathExistsOnceInEach is (a): both files exist, as two rows with two ids.
//
// SQL rather than MCP for m6_test.go's reason — `File` carries no key on
// `path`, so there is no query root that selects one file — and the count is
// the claim: before the corpus this path resolved to exactly one row, and the
// second run replaced the first's occurrences in it.
func (st *corpusState) pathExistsOnceInEach(ctx context.Context, path, first, second string) error {
	ids := map[string]string{}
	for _, corpus := range []string{first, second} {
		rows, err := pool.Query(ctx, `SELECT id::text FROM file WHERE corpus = $1 AND path = $2`, corpus, path)
		if err != nil {
			return fmt.Errorf("read %s in %s: %w", path, corpus, err)
		}
		var got []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			got = append(got, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(got) != 1 {
			return check(func(t assert.TestingT) {
				assert.Len(t, got, 1, "rows for %s in corpus %s", path, corpus)
			})
		}
		ids[corpus] = got[0]
	}

	return check(func(t assert.TestingT) {
		assert.NotEqual(t, ids[first], ids[second],
			"the same path in two corpora must be two rows with two ids, not one row indexed twice")
	})
}

// eachCorpusOwnsItsOccurrences is (b): the definitions are there twice, once
// per corpus, and each belongs to its own corpus's file.
//
// The count is what the path-keyed loader got wrong in the quietest way. It did
// not error — it resolved the second repository's file to the first's row,
// deleted that row's occurrences and loaded its own, so the graph ended up with
// exactly one definition of Greeter and no sign that a second had ever existed.
func (st *corpusState) eachCorpusOwnsItsOccurrences(ctx context.Context, name string) error {
	counts := map[string]int{}
	for _, corpus := range []string{"alpha", "beta"} {
		var defs int
		err := pool.QueryRow(ctx, `
			SELECT count(*)
			FROM occurrence o
			JOIN file f ON f.id = o.file_id
			WHERE f.corpus = $1 AND o.name = $2 AND o.role = 'definition'`, corpus, name).Scan(&defs)
		if err != nil {
			return fmt.Errorf("count %s definitions in %s: %w", name, corpus, err)
		}
		counts[corpus] = defs
	}
	return check(func(t assert.TestingT) {
		assert.Equal(t, 1, counts["alpha"], "definitions of %s in alpha", name)
		assert.Equal(t, 1, counts["beta"], "definitions of %s in beta", name)
	})
}

// noCrossCorpusEdges is (c), and it is the assertion the milestone exists for.
//
// It would have failed before the coordinate bound, and it would have failed
// *because of the bound* rather than because of the column: with both
// repositories' Greeter rendering `scip-go gomod . . repo/src/greeter/Greeter#`,
// the link pass's descriptor self-join matches alpha's reference against beta's
// definition and materialises the edge. `file.corpus` alone does not touch that
// — it keeps the rows apart and lets the descriptors join them anyway — which is
// why 1.1 is the load-bearing half and this is how you can tell.
//
// SQL because it is a claim about the absence of a row anywhere, which a
// traversal can never return. Over every derived table at once, plus `imports`,
// because a single row in any of them is a navigation answer that is not real.
//
// `total` guards the guard: an assertion that no edge crosses is trivially true
// of a graph with no edges, so the scenario also has to have produced some.
func (st *corpusState) noCrossCorpusEdges(ctx context.Context) error {
	crossing := map[string]int{}
	total := 0
	for _, table := range []string{"resolves_to", "calls", "implements", "type_defines", "references_local"} {
		var n, all int
		// table is one of this file's own constants, never scenario input.
		err := pool.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE sf.corpus <> tf.corpus), count(*)
			FROM `+table+` e
			JOIN occurrence s ON s.id = e.source_id
			JOIN occurrence t ON t.id = e.target_id
			JOIN file sf ON sf.id = s.file_id
			JOIN file tf ON tf.id = t.file_id`).Scan(&n, &all)
		if err != nil {
			return fmt.Errorf("scan %s for cross-corpus edges: %w", table, err)
		}
		crossing[table] = n
		total += all
	}

	for _, table := range []string{"imports", "defines"} {
		var n int
		var err error
		if table == "imports" {
			err = pool.QueryRow(ctx, `
				SELECT count(*)
				FROM imports e
				JOIN file s ON s.id = e.source_id
				JOIN file t ON t.id = e.target_id
				WHERE s.corpus <> t.corpus`).Scan(&n)
		} else {
			err = pool.QueryRow(ctx, `
				SELECT count(*)
				FROM defines e
				JOIN file s ON s.id = e.source_id
				JOIN occurrence o ON o.id = e.target_id
				JOIN file t ON t.id = o.file_id
				WHERE s.corpus <> t.corpus`).Scan(&n)
		}
		if err != nil {
			return fmt.Errorf("scan %s for cross-corpus edges: %w", table, err)
		}
		crossing[table] = n
	}

	return check(func(t assert.TestingT) {
		assert.Positive(t, total, "derived occurrence edges to check; an empty graph proves nothing")
		for table, n := range crossing {
			assert.Zerof(t, n, "%s edges joining two corpora", table)
		}
	})
}

// corpusNamesThePackage is 1.20 read off the rows: a repository that declares no
// manifest is coordinate-named after its corpus, not after an ancestor's
// manifest and not `.`.
//
// Both halves are asserted. `= corpus` is the new behaviour; `<> '.'` is the old
// one named explicitly, because Unknown for a name is what put every
// manifest-less repository in one namespace.
func (st *corpusState) corpusNamesThePackage(ctx context.Context, corpus, want string) error {
	var files, named, unknown int
	err := pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE pkg_name = $2),
		       count(*) FILTER (WHERE pkg_name = '.')
		FROM file WHERE corpus = $1`, corpus, want).Scan(&files, &named, &unknown)
	if err != nil {
		return fmt.Errorf("read package names in %s: %w", corpus, err)
	}
	return check(func(t assert.TestingT) {
		assert.Positive(t, files, "files in corpus %s", corpus)
		assert.Equal(t, files, named, "files in %s whose package name is %q", corpus, want)
		assert.Zero(t, unknown, "files in %s stamped with an unknown package name", corpus)
	})
}

// noSharedDescriptors is the property one level below noCrossCorpusEdges, and
// the reason that one holds.
//
// An edge between two corpora can only exist if a descriptor does, because the
// link pass has nothing else to join on. Asserting the intersection is empty
// says *why* there are no edges rather than only that there are none — so a
// future change that reintroduced the collision but happened to produce no
// matching reference would still fail here.
func (st *corpusState) noSharedDescriptors(ctx context.Context, first, second string) error {
	var shared, each int
	err := pool.QueryRow(ctx, `
		WITH a AS (
			SELECT DISTINCT o.descriptor FROM occurrence o
			JOIN file f ON f.id = o.file_id WHERE f.corpus = $1
		), b AS (
			SELECT DISTINCT o.descriptor FROM occurrence o
			JOIN file f ON f.id = o.file_id WHERE f.corpus = $2
		)
		SELECT (SELECT count(*) FROM a JOIN b USING (descriptor)), (SELECT count(*) FROM a)`,
		first, second).Scan(&shared, &each)
	if err != nil {
		return fmt.Errorf("intersect descriptors of %s and %s: %w", first, second, err)
	}
	return check(func(t assert.TestingT) {
		assert.Positive(t, each, "descriptors in %s to compare", first)
		assert.Zerof(t, shared, "descriptors present in both %s and %s", first, second)
	})
}

// snapshot reads one corpus's rows: how many it owns in every table, and which
// uuid each of its paths was given.
func (st *corpusState) snapshot(ctx context.Context, corpus string) (*corpusSnapshot, error) {
	snap := &corpusSnapshot{counts: map[string]int{}, fileIDs: map[string]string{}}

	var files, scopes, occurrences int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM file WHERE corpus = $1`, corpus).Scan(&files)
	if err != nil {
		return nil, err
	}
	err = pool.QueryRow(ctx, `
		SELECT count(*) FROM scope s JOIN file f ON f.id = s.file_id WHERE f.corpus = $1`,
		corpus).Scan(&scopes)
	if err != nil {
		return nil, err
	}
	err = pool.QueryRow(ctx, `
		SELECT count(*) FROM occurrence o JOIN file f ON f.id = o.file_id WHERE f.corpus = $1`,
		corpus).Scan(&occurrences)
	if err != nil {
		return nil, err
	}
	snap.counts["file"], snap.counts["scope"], snap.counts["occurrence"] = files, scopes, occurrences

	// Derived edges owned by this corpus: the referencing side is the owner
	// (SPEC.md §7 "Ownership & deletion").
	for _, table := range []string{"resolves_to", "calls", "implements", "type_defines", "references_local"} {
		var n int
		// table is one of this function's own constants, never scenario input.
		err := pool.QueryRow(ctx, `
			SELECT count(*) FROM `+table+` e
			JOIN occurrence s ON s.id = e.source_id
			JOIN file f ON f.id = s.file_id
			WHERE f.corpus = $1`, corpus).Scan(&n)
		if err != nil {
			return nil, fmt.Errorf("count %s in %s: %w", table, corpus, err)
		}
		snap.counts[table] = n
	}

	rows, err := pool.Query(ctx, `SELECT path, id::text FROM file WHERE corpus = $1`, corpus)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var path, id string
		if err := rows.Scan(&path, &id); err != nil {
			return nil, err
		}
		snap.fileIDs[path] = id
	}
	return snap, rows.Err()
}

// corpusUnchanged is 1.21: a re-index of one repository is not an event in
// another's life.
//
// This is the property a shared database is *usable* on rather than merely
// correct on. It would have failed loudly before the corpus — alpha's re-index
// resolved beta's paths to beta's rows and rewrote them — and it is the one
// assertion here that a reader can check by hand against the row counts.
func (st *corpusState) corpusUnchanged(ctx context.Context, corpus string) error {
	before := st.beforeCorpus[corpus]
	if before == nil {
		return fmt.Errorf("no snapshot of %s; the When step did not run", corpus)
	}
	after, err := st.snapshot(ctx, corpus)
	if err != nil {
		return err
	}
	return check(func(t assert.TestingT) {
		assert.Equal(t, before.counts, after.counts, "row counts owned by corpus %s", corpus)
		assert.Equal(t, before.fileIDs, after.fileIDs, "file identities in corpus %s", corpus)
	})
}

// corpusKeptIdentity is store.resolveFile's contract, restated per corpus: a
// re-index reuses the row and its uuid rather than minting a new one, because
// `imports` endpoints are file ids.
func (st *corpusState) corpusKeptIdentity(ctx context.Context, corpus string) error {
	before := st.beforeCorpus[corpus]
	if before == nil {
		return fmt.Errorf("no snapshot of %s; the When step did not run", corpus)
	}
	after, err := st.snapshot(ctx, corpus)
	if err != nil {
		return err
	}
	return check(func(t assert.TestingT) {
		assert.NotEmpty(t, before.fileIDs, "files in corpus %s", corpus)
		assert.Equal(t, before.fileIDs, after.fileIDs, "file identities in corpus %s across a re-index", corpus)
	})
}
