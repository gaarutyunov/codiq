// M5's feature suite: features/artifacts.feature against the real stack
// (SPEC.md §13, §14 M5).
//
// There is no new harness and no new stack. m1_test.go's startStack brings up
// the one composition the package shares; m3_test.go's m3State carries the
// generated corpus, the indexer process and the kill machinery; m4_test.go's
// m4State adds the run that is expected to fail, the constraint the database
// refuses a write with, and the read of the child workflows' checkpoints. All
// of it is *embedded* below rather than reimplemented, so the crash scenario
// here runs M3's kill and M4's checkpoint reader against M5's pipeline.
//
// What M5 adds is one thing, and it is the shared volume:
//
//   - A directory the suite can count. Up to M4 the facts lived in the
//     checkpoint database, which the tests could already read; from M5 half of
//     the milestone's claims are about a filesystem, and "the artifacts were
//     deleted" is only a statement about a directory nothing else has written
//     to. So each scenario runs the binary against a volume of its own
//     (SPEC.md §13: "from M5, artifacts use a temp dir in tests"), and the
//     assertions are over the whole of it rather than over the keys they happen
//     to know about — which is what makes "nothing at all is left" sayable.
//
// The claims split the way they have in every suite before this one. The one
// navigation claim — the cross-file call edge that proves the graph really was
// built out of the artifacts — goes over MCP. Everything else is a claim about
// bytes: the payload a step recorded, and the files on a disk. GraphQL exposes
// traversals, not checkpoint rows and not directory listings, so those are read
// where they are written: dbos.operation_outputs, and the volume itself.
package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cucumber/godog"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
)

// TestM5Features is the godog entry point for M5, scoped to its own feature file
// so that each milestone's suite owns the scenarios it has steps for.
func TestM5Features(t *testing.T) {
	ctx := context.Background()
	repoRoot = mustRepoRoot(t)
	m3Log = t

	startStack(t, ctx)

	suite := godog.TestSuite{
		Name:                "m5",
		ScenarioInitializer: InitializeM5Scenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{filepath.Join(repoRoot, "features", "artifacts.feature")},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("m5 feature scenarios failed")
	}
}

// m5State is one scenario's state: everything M4 needed, plus the volume this
// scenario's runs write to and the keys a failed batch left on it.
type m5State struct {
	m4State

	// volume is this scenario's artifact directory, and outerVolume is the
	// package's, restored on the way out. One per scenario because two of the
	// assertions below are about the *whole* directory — "an artifact for every
	// file and no others", "nothing at all is left" — and neither survives a
	// directory a previous scenario's killed run also wrote to.
	volume, outerVolume string

	// kept is the artifact keys a batch left on the volume when it failed, read
	// off that batch's own checkpoints. The retry is required to land on the
	// same ones (SPEC.md Decision 16, artifact.Key).
	kept []string
}

func InitializeM5Scenario(sc *godog.ScenarioContext) {
	st := &m5State{}

	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "codiq-m5-tests", Version: "0.1.0"}, nil)
		session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: mcpURL}, nil)
		if err != nil {
			return ctx, fmt.Errorf("mcp handshake with %s: %w", mcpURL, err)
		}
		st.session = session

		// The volume this scenario's codiq processes will write to. Swapping the
		// package's directory out rather than adding a second mechanism, so that
		// every exec site the suite already has — m2's, m3's and m4's — picks it
		// up without knowing M5 exists (m1_test.go's codiqEnv).
		dir, err := os.MkdirTemp("", "codiq-m5-volume-*")
		if err != nil {
			return ctx, fmt.Errorf("artifact volume: %w", err)
		}
		st.volume, st.outerVolume, artifactDir = dir, artifactDir, dir
		return ctx, nil
	})

	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if st.session != nil {
			_ = st.session.Close()
			st.session = nil
		}
		// A scenario that failed between starting an indexer and killing it
		// would otherwise leave one running against the next scenario's
		// database, and its workflow PENDING for the next Launch to recover.
		st.stopIndexer()
		if err := stopRefusing(ctx); err != nil {
			return ctx, err
		}
		if st.volume != "" {
			// Unconditional, and it is the point of a per-scenario volume: two
			// scenarios here kill or fail a batch on purpose, and SPEC.md §6
			// says a batch in that state *keeps* its artifacts. Nothing will
			// ever collect them — Decision 16 ships no sweeper, and the keys
			// name a temp module that is about to stop existing — so this is
			// what collects them.
			_ = os.RemoveAll(st.volume)
			artifactDir = st.outerVolume
		}
		if st.tmp != "" {
			_ = os.RemoveAll(st.tmp)
		}
		st.volume, st.outerVolume, st.kept, st.refused, st.extracted = "", "", nil, "", nil
		st.tmp, st.repo, st.report, st.log, st.runID, st.ledger, st.before = "", "", "", "", "", nil, nil
		return ctx, nil
	})

	// M3's and M4's steps, reused as written: the corpus, the running indexer,
	// the kill, the unreadable file, the refusing database, and the claims about
	// what the resumed run did with the work it inherited.
	sc.Step(`^an empty CodiQ graph and no checkpoints$`, st.emptyGraphAndCheckpoints)
	sc.Step(`^a Go module of (\d+) files$`, st.aModuleOf)
	sc.Step(`^the indexed Go module of (\d+) files$`, st.indexedModuleOf)
	sc.Step(`^"([^"]*)" cannot be read$`, st.cannotBeRead)
	sc.Step(`^the database refuses to store the type "([^"]*)"$`, st.refuseType)
	sc.Step(`^the database stops refusing$`, st.stopRefusingType)
	sc.Step(`^"([^"]*)" is added, defining "([^"]*)"$`, st.addTypeFile)
	sc.Step(`^the module is being indexed$`, st.moduleBeingIndexed)
	sc.Step(`^the module is indexed$`, st.moduleIndexed)
	sc.Step(`^the module is indexed again$`, st.moduleIndexed)
	sc.Step(`^the module is indexed and fails$`, st.moduleIndexedFailing)
	sc.Step(`^the indexer is killed once (\d+) files are checkpointed$`, st.killedAfter)
	sc.Step(`^the run indexed (\d+) files and loaded (\d+)$`, st.runCounted)
	sc.Step(`^the report accounts for all (\d+) files$`, st.reportAccountsFor)
	sc.Step(`^it is the same run that finished$`, st.sameRunFinished)
	sc.Step(`^every extraction the killed run had checkpointed is still exactly what it wrote$`, st.extractionsSurvived)
	sc.Step(`^exactly (\d+) map tasks recorded an extraction$`, st.mapTasksRecorded)
	sc.Step(`^the graph is exactly what it was before$`, st.graphUnchanged)

	// M5's own: the payload that crosses the checkpoint, and the volume.
	sc.Step(`^every map task checkpointed an artifact key and nothing else$`, st.everyTaskCheckpointedAKey)
	sc.Step(`^the map task for "([^"]*)" checkpointed no artifact$`, st.taskCheckpointedNoArtifact)
	sc.Step(`^no extract checkpoint carries more than (\d+) characters$`, st.noCheckpointLargerThan)
	sc.Step(`^every Go source file is deleted$`, st.sourcesDeleted)
	sc.Step(`^the volume holds an artifact for every file the map phase extracted$`, st.volumeHoldsTheBatch)
	sc.Step(`^the artifacts the failed batch kept were reclaimed, not left behind$`, st.keptArtifactsReclaimed)
	sc.Step(`^the volume holds nothing at all$`, st.volumeIsEmpty)

	// M1's steps, reused as written: the MCP tool call and the JSON comparison.
	sc.Step(`^an agent asks over MCP:$`, st.agentAsks)
	sc.Step(`^the answer is:$`, st.answerIs)
}

// --- when ------------------------------------------------------------------

// sourcesDeleted removes every .go file from the module, leaving the directory
// and its go.mod.
//
// It is the load-bearing step of the crash scenario and not a tidy-up. A resumed
// run that re-extracted a file would call os.ReadFile on a path that is no
// longer there, so its map task would fail and the file would be skipped
// (index/dbos.go's mapFiles); the run would then report 152 files and 0 loaded
// instead of 152 and 152, and the graph would be empty. There is no way to pass
// the steps that follow except by never opening a source file at all — which is
// SPEC.md §6's "no re-extraction" stated as something a test can fail.
//
// go.mod stays because the argument cmd/codiq is given still has to be a module
// root for the process to start; the `resolve` and `walk` steps that read it are
// replayed from the parent's own checkpoints, so what it now contains is not
// consulted either way.
func (st *m5State) sourcesDeleted() error {
	if st.repo == "" {
		return errors.New("no module; the Given step did not run")
	}
	var removed int
	err := filepath.WalkDir(st.repo, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		removed++
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete the module's sources: %w", err)
	}
	if removed == 0 {
		return fmt.Errorf("%s held no .go files to delete", st.repo)
	}
	m3Logf("deleted all %d source files; anything the resumed run loads came off the volume", removed)
	return nil
}

// --- then: the payload that crosses a checkpoint ---------------------------

// everyTaskCheckpointedAKey reads every map task's recorded output and requires
// each to be an artifact key and nothing besides (SPEC.md §5, §14 M5).
//
// The whole payload is decoded rather than searched, and unknown fields are
// refused, because the claim is about what is *absent*: a build that also
// recorded the facts, or a digest of them, or a count, would still contain a
// valid key and would still pass a step that only looked for one.
//
// The key's shape is checked too, and against artifact.Key's contract rather
// than against a literal: two hex characters, a slash, the same two characters
// again at the head of a 64-character digest, and `.pb`. That shape is what
// makes a key path-safe — it can name no directory but its own shard and escape
// no root — so a key that has drifted out of it is a finding even when the run
// it came from succeeded.
func (st *m5State) everyTaskCheckpointedAKey(ctx context.Context) error {
	byTask, err := st.extractPayloads(ctx)
	if err != nil {
		return err
	}
	if len(byTask) == 0 {
		return errors.New("no map task recorded anything; the When step did not run")
	}
	var withKey, withoutKey int
	for _, out := range byTask {
		if out.Key == "" {
			withoutKey++
			continue
		}
		withKey++
	}
	m3Logf("%d map task(s) recorded a key, %d recorded none; the largest payload was %d characters",
		withKey, withoutKey, widestPayload(byTask))
	return check(func(t assert.TestingT) {
		for path, out := range byTask {
			if out.Poison {
				continue
			}
			assert.NotEmpty(t, out.Key, "the artifact key %s's map task recorded", path)
			assert.Empty(t, out.ParseError, "the parse error %s's map task recorded alongside a key", path)
			assert.Truef(t, validArtifactKey(out.Key),
				"%s's map task recorded %q, which is not the shape artifact.Key produces", path, out.Key)
		}
	})
}

// taskCheckpointedNoArtifact is the other shape §5 allows: a file that produced
// no artifact produced no key either.
//
// A poison file gets no artifact deliberately (index/dbos.go): the reduce loads
// what a key names, and loading a file the extractor could not read would delete
// that file's rows and put nothing back. So the absence is the assertion, and it
// has to be the absence of a key rather than the presence of an error message —
// an unreadable file fails its map task outright, which is a different record
// from a parse failure and the only poison a real repository produces at M4.
func (st *m5State) taskCheckpointedNoArtifact(ctx context.Context, path string) error {
	byTask, err := st.extractPayloads(ctx)
	if err != nil {
		return err
	}
	out, seen := byTask[path]
	return check(func(t assert.TestingT) {
		if !assert.Truef(t, seen, "%s's map task recorded nothing at all; the tasks that recorded something were %v",
			path, extractedPaths(byTask)) {
			return
		}
		assert.Empty(t, out.Key, "the artifact key %s's map task recorded", path)
		assert.Truef(t, out.Poison,
			"%s's map task was expected to have failed outright; it recorded %q with no error beside it",
			path, out.JSON)
	})
}

// noCheckpointLargerThan is the offload itself, stated as the only thing about
// it that is externally visible: the size of what `codiq_dbos` now holds.
//
// The *stored* payload is measured rather than the decoded one, because the
// stored payload is what the checkpoint database actually pays for and what M4's
// 82,180-character rows actually were.
func (st *m5State) noCheckpointLargerThan(ctx context.Context, limit int) error {
	byTask, err := st.extractPayloads(ctx)
	if err != nil {
		return err
	}
	if len(byTask) == 0 {
		return errors.New("no map task recorded anything; the When step did not run")
	}
	return check(func(t assert.TestingT) {
		for path, out := range byTask {
			assert.LessOrEqualf(t, len(out.Raw), limit,
				"%s's map task checkpointed %d characters; the facts are supposed to be on the volume:\n%s",
				path, len(out.Raw), truncate(out.JSON))
		}
	})
}

// --- then: the volume ------------------------------------------------------

// volumeHoldsTheBatch requires the volume to hold exactly the artifacts this
// batch's map tasks say they wrote — every one of them, and nothing else.
//
// Set equality in both directions is what makes it worth asserting. That every
// key is on disk is SPEC.md §14 M5's "artifacts written in map" and §6's "they
// must persist until reduce succeeds"; that nothing *else* is on disk is what
// rules out a half-written temporary file left behind by a killed process, and
// a second generation of the same batch written under different keys — which is
// the failure a key derived from the run rather than from the tree would
// produce, and the reason artifact.Key is a function of (root, path) alone.
//
// The keys are read off the batch's own checkpoints rather than recomputed here,
// so the assertion compares what the run recorded against what the run wrote,
// with the test in the middle asserting rather than predicting.
func (st *m5State) volumeHoldsTheBatch(ctx context.Context) error {
	byTask, err := st.extractPayloads(ctx)
	if err != nil {
		return err
	}
	want := make([]string, 0, len(byTask))
	for _, out := range byTask {
		if out.Key != "" {
			want = append(want, out.Key)
		}
	}
	if len(want) == 0 {
		return errors.New("no map task recorded an artifact key; the When step did not run")
	}
	slices.Sort(want)
	got, err := st.artifactsOnVolume()
	if err != nil {
		return err
	}
	// Remembered here so that the failure-and-retry scenario can ask, after the
	// batch that succeeded, whether these are the keys it reclaimed.
	st.kept = want
	m3Logf("the volume holds %d artifact(s) for a batch of %d extracted file(s)", len(got), len(want))
	return check(func(t assert.TestingT) {
		assert.Equal(t, want, got, "the artifacts on the volume against the keys the map tasks recorded")
	})
}

// keptArtifactsReclaimed closes SPEC.md Decision 16's loop: the batch that
// failed kept its artifacts, and the batch that succeeded took *those* away
// rather than writing a second set beside them.
//
// This is the assertion that pays for there being no sweeper. A failed batch's
// workflow ends in ERROR and nothing resumes it (features/mapreduce.feature), so
// its artifacts belong to a run that will never return for them; they are
// reclaimable only because artifact.Key is a pure function of the tree root and
// the repo-relative path, so the next index of the same tree computes the same
// keys, overwrites them, and deletes them when it commits. Asserted from both
// ends: the retry's map tasks recorded the same key set, and none of those keys
// is still on the volume.
func (st *m5State) keptArtifactsReclaimed(ctx context.Context) error {
	if len(st.kept) == 0 {
		return errors.New("no artifacts were recorded as kept; the earlier Then step did not run")
	}
	byTask, err := st.extractPayloads(ctx)
	if err != nil {
		return err
	}
	retried := make([]string, 0, len(byTask))
	for _, out := range byTask {
		if out.Key != "" {
			retried = append(retried, out.Key)
		}
	}
	slices.Sort(retried)

	var left []string
	for _, key := range st.kept {
		if _, err := os.Stat(filepath.Join(st.volume, filepath.FromSlash(key))); err == nil {
			left = append(left, key)
		}
	}
	m3Logf("the retry recomputed %d of the failed batch's %d keys and left %d of them on the volume",
		len(retried), len(st.kept), len(left))
	return check(func(t assert.TestingT) {
		assert.Equal(t, st.kept, retried,
			"the keys the retry's map tasks recorded, against the keys the failed batch left")
		assert.Empty(t, left, "artifacts of the failed batch still on the volume after a batch that committed")
	})
}

// volumeIsEmpty requires the volume to hold no files whatsoever.
//
// Files and not entries: artifact.Store.Write creates a shard directory per key
// prefix and Delete removes the artifact rather than the shard, so a volume that
// has been fully collected is a tree of empty directories. What must not be
// there is *any* regular file — which is the stronger form of "the batch's
// artifacts were deleted", because it also fails on a temporary file left behind
// by a write that did not reach its rename, and on an artifact written under a
// key nobody recorded.
func (st *m5State) volumeIsEmpty() error {
	files, err := st.filesOnVolume()
	if err != nil {
		return err
	}
	return check(func(t assert.TestingT) {
		assert.Empty(t, files, "files left on the artifact volume after a batch that committed")
	})
}

// --- reading the checkpoints and the volume --------------------------------

// extractPayload is one map task's recorded output, decoded and kept whole.
type extractPayload struct {
	// Key and ParseError are index's `extracted`, which is the entire vocabulary
	// §5 allows across this checkpoint.
	Key        string
	ParseError string
	// Poison is true when the task itself failed rather than returning — the
	// unreadable file's case, where DBOS records an error beside a zero output.
	Poison bool
	// Raw is the output exactly as `codiq_dbos` stores it — base64 — which is
	// what the checkpoint database pays for and what the size claim is about.
	// JSON is the same payload decoded, which is what the shape claims are about.
	Raw, JSON string
}

// extractPayloads reads the most recent run's map-task outputs, keyed by the
// repo-relative path of the file each task extracted.
//
// The path comes out of the step name — index names the step `extract:<rel>`
// (index/dbos.go) — rather than out of the payload, and that is deliberate:
// from M5 the payload no longer says which file it is about, which is the whole
// change, so the only thing left that does is the step's name.
//
// Scoped to the newest run because two of these scenarios run the module twice
// and every child of every run shares the module's ID prefix. The children of
// one parent are exactly the workflows whose ID starts `<parent>-`, which is how
// DBOS names them (index.RunIDPrefix).
func (st *m5State) extractPayloads(ctx context.Context) (map[string]extractPayload, error) {
	ids, err := st.workflowIDs(ctx)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no run of %s is recorded", st.repo)
	}
	byWorkflow, err := extractCheckpoints(ctx, ids[len(ids)-1]+"-%")
	if err != nil {
		return nil, err
	}
	out := make(map[string]extractPayload, len(byWorkflow))
	for _, cs := range byWorkflow {
		for _, c := range cs {
			rel, ok := strings.CutPrefix(c.Name, "extract:")
			if !ok {
				return nil, fmt.Errorf("checkpoint %q is not an extraction", c.Name)
			}
			p, err := decodeExtracted(c.Output)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", rel, err)
			}
			p.Poison = c.Err != ""
			out[rel] = p
		}
	}
	return out, nil
}

// decodeExtracted parses one recorded `extracted` (index/dbos.go), refusing any
// field that is not one of its two.
//
// DBOS stores a step's output base64-encoded, so the stored form is decoded
// first and both forms are kept: the JSON is what the structural claims are
// about, and the base64 is what `codiq_dbos` actually holds and therefore what
// the size claim is about. The two are a fixed ratio apart — the 80-character
// `{"key":"<2 hex>/<64 hex>.pb"}` is stored as 108 characters — which is why the
// feature file's bound is stated over the stored form.
//
// Unknown fields are an error rather than an oversight: this payload is the
// whole of what M5 lets cross a checkpoint, so a third field appearing in it is
// the milestone quietly coming undone, and DisallowUnknownFields is the cheapest
// way to notice.
//
// A step that failed records the zero value, which is `{}` under the omitempty
// tags — no key, no reason, and no artifact on the volume. That decodes to an
// empty payload here and is told apart from a successful task by the error DBOS
// records beside it.
func decodeExtracted(stored string) (extractPayload, error) {
	p := extractPayload{Raw: stored}
	if strings.TrimSpace(stored) == "" {
		return p, nil
	}
	blob, err := base64.StdEncoding.DecodeString(stored)
	if err != nil {
		return p, fmt.Errorf("the recorded output is not base64: %w\n%s", err, truncate(stored))
	}
	p.JSON = string(blob)
	var body struct {
		Key        string `json:"key"`
		ParseError string `json:"parse_error"`
	}
	dec := json.NewDecoder(bytes.NewReader(blob))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		return p, fmt.Errorf("the recorded output is not an `extracted`: %w\n%s", err, truncate(p.JSON))
	}
	p.Key, p.ParseError = body.Key, body.ParseError
	return p, nil
}

// artifactsOnVolume lists the artifacts on this scenario's volume as keys, in
// the form artifact.Key produces them, sorted.
func (st *m5State) artifactsOnVolume() ([]string, error) {
	files, err := st.filesOnVolume()
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(files))
	for _, f := range files {
		keys = append(keys, filepath.ToSlash(f))
	}
	slices.Sort(keys)
	return keys, nil
}

// filesOnVolume lists every regular file under the scenario's volume, relative
// to it. Every file, including one no key names: a stray temporary file is a
// finding rather than something to filter out (artifact.Store.Write).
func (st *m5State) filesOnVolume() ([]string, error) {
	if st.volume == "" {
		return nil, errors.New("this scenario has no artifact volume")
	}
	var files []string
	err := filepath.WalkDir(st.volume, func(path string, d fs.DirEntry, err error) error {
		switch {
		case errors.Is(err, os.ErrNotExist):
			// The directory is created by the first codiq process that runs
			// (artifact.Open), so a scenario that has not run one yet finds
			// nothing rather than an error.
			return nil
		case err != nil:
			return err
		case d.IsDir():
			return nil
		}
		rel, err := filepath.Rel(st.volume, path)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the artifact volume: %w", err)
	}
	slices.Sort(files)
	return files, nil
}

// --- helpers ---------------------------------------------------------------

// validArtifactKey reports whether key has the shape artifact.Key produces:
// `<2 hex>/<64 hex>.pb`, the shard being the digest's own first two characters.
//
// Restated here rather than imported because artifact's own check is unexported,
// and because a test that called the implementation would agree with it by
// construction — including about a change nobody meant to make.
func validArtifactKey(key string) bool {
	shard, name, ok := strings.Cut(key, "/")
	if !ok || len(shard) != 2 {
		return false
	}
	digest, ok := strings.CutSuffix(name, ".pb")
	if !ok || len(digest) != 64 || !strings.HasPrefix(digest, shard) {
		return false
	}
	return strings.Trim(digest, "0123456789abcdef") == ""
}

// extractedPaths is the files a batch's map tasks recorded something for, for a
// failure message that has to say which ones those were.
func extractedPaths(byTask map[string]extractPayload) []string {
	paths := make([]string, 0, len(byTask))
	for p := range byTask {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	return paths
}

// widestPayload is the largest recorded output in a batch, which is the number
// M5 moved: 82,180 characters at M4.
func widestPayload(byTask map[string]extractPayload) int {
	var widest int
	for _, out := range byTask {
		widest = max(widest, len(out.Raw))
	}
	return widest
}

// truncate bounds a payload quoted into a failure message. The payloads this
// milestone exists to prevent are tens of kilobytes, and a test failure that
// prints one is unreadable.
func truncate(s string) string {
	const limit = 240
	if len(s) <= limit {
		return s
	}
	var b bytes.Buffer
	b.WriteString(s[:limit])
	fmt.Fprintf(&b, "… (%d characters in all)", len(s))
	return b.String()
}
