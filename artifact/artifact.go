// Package artifact is the map phase's output store: one file's extracted facts,
// as a protobuf blob on the shared volume (SPEC.md §5, §10, §14 M5).
//
// It exists to keep the facts off the checkpoint. Up to M4 a map task returned
// a facts.FileFacts, so DBOS serialised the whole thing into `codiq_dbos` twice
// — once as the extract step's output and once as the task's — and the batch
// held every file's facts in RAM until the reduce. §5 says the opposite in one
// sentence: "the map task checkpoints the artifact *key*, never the blob". So
// the map task writes here and returns a Key, and the reduce reads by that key
// inside its own transaction.
//
// Three properties are what make that safe, and each is load-bearing rather
// than convenient:
//
//   - A key is a pure function of (tree root, repo-relative path) — see Key. It
//     does not depend on when the artifact was written, which run wrote it, or
//     how many times.
//   - Write is idempotent and atomic. DBOS steps are at-least-once (index's
//     dbos.go), so a step whose side effect landed but whose checkpoint did not
//     re-runs and writes the same artifact again; that has to be a no-op in
//     effect, and a reader must never see a half-written file.
//   - Delete is the whole GC (Decision 16): the reduce deletes its batch's
//     artifacts on success and a failed batch keeps them, so an artifact
//     persists exactly until the load that consumes it commits. There is no
//     sweeper, which is why Key is keyed the way it is.
//
// The store is a plain directory. §10 makes it a Docker Compose named volume
// locally (deploy/docker-compose.yml's `artifacts`, CODIQ_ARTIFACT_DIR) and a
// ReadWriteMany PVC under Kubernetes later; nothing here knows the difference,
// because the contract §10 states is "a shared filesystem" and that is all this
// package uses.
//
// # No size budget, and how much disk that costs
//
// M4 declined to cap a file by size or parse time on the grounds that §5 defines
// a poison file as one the extractor could not parse, and that a byte limit is a
// different predicate with a number nobody has a principled value for. Disk
// makes the question worth asking again, and the answer is still no, for three
// reasons that are now measurable rather than argued:
//
//   - An artifact is *cheaper* than what it replaced. The same facts cost
//     4.3x more as the JSON M4 checkpointed, and cost it twice, in a database
//     (TestArtifactSizeAgainstTheSource). A budget introduced here would be
//     protecting against a smaller version of a cost the pipeline already paid
//     without one.
//   - The cost is bounded by the corpus, not by an adversary. CodiQ indexes a
//     repository somebody chose to index; there is no untrusted producer to
//     defend the volume against, which is what a budget is for.
//   - A file skipped for being large is missing from the graph, and it is
//     missing silently and permanently — the skip is checkpointed. That is a
//     worse outcome than a large artifact, and it is the outcome an arbitrary
//     threshold produces the first time it is wrong.
//
// What the absence of a budget does require is that the disk cost be a known
// quantity rather than a surprise, so: an artifact runs about **11x the size of
// the source file** it describes, measured over the Go extractor's output. The
// multiple is structural — every occurrence carries a full SCIP descriptor
// (§4.3), which is longer than the identifier it names — so it will move with
// the language but not with the file. Size the volume at ~15x the corpus and a
// batch cannot fill it.
package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/proto"

	factsv1 "github.com/gaarutyunov/codiq/artifact/proto/codiq/facts/v1"
	"github.com/gaarutyunov/codiq/facts"
)

// ErrNotFound reports that no artifact is stored under a key.
//
// Distinct from a read failure on purpose. A missing artifact means the batch
// that wrote it already committed and GC'd it — a reduce replayed after its own
// success sees exactly this — while a read failure means the volume is not
// doing its job. A caller that cannot tell them apart has to treat a finished
// batch as a broken one.
var ErrNotFound = errors.New("artifact: no artifact for key")

// keySuffix is the extension every artifact file carries. Protobuf, not JSON:
// Decision 3.
const keySuffix = ".pb"

// Key returns the artifact key for the file at repo-relative path in the tree
// rooted at root.
//
// It is `<2 hex>/<64 hex>.pb`, the hex being SHA-256 over root and path joined
// by a NUL. Four properties, in the order they matter:
//
//   - Collision-free. NUL cannot occur in a path on any platform CodiQ runs on,
//     so the pair is unambiguously delimited and the digest is over the pair
//     rather than over a concatenation two different pairs could share.
//   - Deterministic, and a function of the map task's *input* alone. A step
//     re-run after an at-least-once replay recomputes the same key and
//     overwrites its own artifact, which is what makes Write's idempotence
//     reachable. Nothing about the run — its workflow ID, its start time —
//     is in here, deliberately: two runs over the same tree must land on the
//     same keys, or a batch that failed would leave artifacts no later run
//     could ever claim, and Decision 16 ships no sweeper to collect them. As it
//     stands, the next index of the same tree reclaims them by writing over
//     them.
//   - Path-safe by construction. The key is hex and one separator, so it can
//     name no directory but its own shard, escape no root, and collide with no
//     sibling on a case-insensitive filesystem. A key read back off a
//     checkpoint is still checked (see valid) rather than trusted.
//   - Sharded. A repository is one artifact per file, and a flat directory of
//     100k entries is a filesystem's worst case; two hex characters spread them
//     over 256 subdirectories at no cost to any of the above.
//
// Root is included because the artifact directory is shared: §10's volume is
// mounted by whatever indexes on that machine, and two trees that both contain
// `main.go` must not write the same file. It is the resolve step's frozen root
// (index's site), so a resumed run in a process with a different working
// directory computes the same key as the run it is continuing.
func Key(root, path string) string {
	sum := sha256.Sum256([]byte(root + "\x00" + path))
	h := hex.EncodeToString(sum[:])
	return h[:2] + "/" + h + keySuffix
}

// valid reports whether key has the exact shape Key produces.
//
// Keys arrive from a checkpoint written by an earlier process, so they are
// input: an unchecked one is a path this package would join onto the shared
// volume and then read, write or delete. The check is on the shape rather than
// on a lookup, so it costs nothing and rejects before any filesystem call.
func valid(key string) bool {
	shard, name, ok := strings.Cut(key, "/")
	if !ok || len(shard) != 2 || !hexOnly(shard) {
		return false
	}
	name, ok = strings.CutSuffix(name, keySuffix)
	return ok && len(name) == 2*sha256.Size && hexOnly(name) && strings.HasPrefix(name, shard)
}

func hexOnly(s string) bool {
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Store is an artifact directory.
//
// A value rather than package state, so a test can point one at t.TempDir()
// (SPEC.md §13: "from M5, artifacts use a temp dir in tests") and so two of them
// cannot quietly share a directory.
type Store struct {
	dir string
}

// Open prepares dir as an artifact store, creating it if it does not exist.
//
// Creating rather than requiring: the directory is scratch space for a batch,
// not a configured resource, and a run that has a volume mounted but no
// directory on it yet is the ordinary first-boot case (§11.1's `artifacts`
// volume starts empty).
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("artifact: no directory")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("artifact: %s: %w", dir, err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("artifact: %w", err)
	}
	return &Store{dir: abs}, nil
}

// Dir is the directory the store writes to, absolute.
func (s *Store) Dir() string { return s.dir }

// path resolves a key to a filename, refusing anything Key did not produce.
func (s *Store) path(key string) (string, error) {
	if !valid(key) {
		return "", fmt.Errorf("artifact: malformed key %q", key)
	}
	return filepath.Join(s.dir, filepath.FromSlash(key)), nil
}

// Write stores one file's facts under key.
//
// Atomic and idempotent, which is one requirement rather than two. A DBOS step
// is at-least-once (index's dbos.go), so the map task that wrote this artifact
// may run again and write it again; and the reduce reads artifacts a different
// process wrote, so it must never observe a partial one. Writing to a temporary
// file in the same directory and renaming gives both: rename over an existing
// name is atomic on POSIX, so a re-run replaces the artifact in one step and a
// concurrent reader sees either the whole previous artifact or the whole new
// one — which are byte-identical anyway, since the facts are a pure function of
// the file that was parsed.
//
// The one thing this does not survive is the process dying between the
// temporary file's creation and the rename, which leaves that temporary file
// behind. It is bounded — one per in-flight write per crash — and it is the same
// state a failed batch leaves on purpose (§6: a failed batch keeps its
// artifacts), so there is nothing here for a sweeper Decision 16 does not want.
//
// ctx is deliberately *not* consulted, and the asymmetry with Read and Delete
// is the point. Those two run in a loop over the whole batch inside the reduce
// step, where stopping early is both meaningful and free. This one is a single
// small write at the end of a map task, and making it cancellable would give
// the extract step a way to *fail* during a shutdown that it did not have up to
// M4 — where reading and parsing observed no context at all and a step
// interrupted by Ctrl-C simply finished. A failed map task is a skipped file
// (mapFiles), so a step that can fail on shutdown is a step that can quietly
// drop a file from the graph. Finishing a 14 kB write is cheaper than the
// analysis of whether that can happen.
func (s *Store) Write(_ context.Context, key string, ff facts.FileFacts) error {
	name, err := s.path(key)
	if err != nil {
		return err
	}
	msg, err := encode(ff)
	if err != nil {
		return fmt.Errorf("artifact: encode %s: %w", ff.File.Path, err)
	}
	blob, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("artifact: marshal %s: %w", ff.File.Path, err)
	}

	dir := filepath.Dir(name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("artifact: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("artifact: %w", err)
	}
	// Removed on every path that does not reach the rename; a no-op once the
	// rename has taken the name away.
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(blob); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("artifact: write %s: %w", key, err)
	}
	// Before the rename, not after: the rename is what publishes the artifact,
	// and publishing a name whose contents are still only in the page cache is
	// how a reader comes to see a file full of zeroes after a host crash.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("artifact: sync %s: %w", key, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("artifact: close %s: %w", key, err)
	}
	if err := os.Rename(tmp.Name(), name); err != nil {
		return fmt.Errorf("artifact: publish %s: %w", key, err)
	}
	return nil
}

// Read returns the facts stored under key, or ErrNotFound if there are none.
func (s *Store) Read(ctx context.Context, key string) (facts.FileFacts, error) {
	if err := ctx.Err(); err != nil {
		return facts.FileFacts{}, err
	}
	name, err := s.path(key)
	if err != nil {
		return facts.FileFacts{}, err
	}
	blob, err := os.ReadFile(name)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return facts.FileFacts{}, fmt.Errorf("%w %s", ErrNotFound, key)
	case err != nil:
		return facts.FileFacts{}, fmt.Errorf("artifact: read %s: %w", key, err)
	}
	var msg factsv1.FileFacts
	if err := proto.Unmarshal(blob, &msg); err != nil {
		return facts.FileFacts{}, fmt.Errorf("artifact: decode %s: %w", key, err)
	}
	ff, err := decode(&msg)
	if err != nil {
		return facts.FileFacts{}, fmt.Errorf("artifact: %s: %w", key, err)
	}
	return ff, nil
}

// Delete removes the artifacts under keys. It is the batch's GC (SPEC.md §6,
// Decision 16): the reduce calls it once, after its transaction commits.
//
// A key with no artifact is not an error. Delete is reached from a step, and a
// step is at-least-once, so the second run of a reduce that already succeeded
// deletes what the first already deleted; treating that as a failure would make
// a replay of a finished batch fail.
//
// Every key is attempted even after one fails, so a single unreadable entry
// does not strand the rest of the batch on the volume.
func (s *Store) Delete(ctx context.Context, keys ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var errs []error
	for _, key := range keys {
		name, err := s.path(key)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("artifact: delete %s: %w", key, err))
		}
	}
	return errors.Join(errs...)
}
