# M5 — protobuf artifacts and disk offload (SPEC.md §14 M5, §5, §6, §10).
#
# M4 got the shape of the pipeline right and the *payload* wrong. A map task
# returned a whole file's facts, so DBOS wrote them into `codiq_dbos` twice —
# once as the extract step's output and once as the task's — and the parent held
# every file's facts in memory from the first extraction until the reduce. §5
# says what should have crossed instead, in one sentence: "the map task
# checkpoints the artifact *key*, never the blob". M5 is that sentence.
#
# The facts now go to a shared volume as a protobuf artifact (§10: a Compose
# named volume locally, a ReadWriteMany PVC under Kubernetes later — the
# contract is "a shared filesystem" and nothing more), and what crosses the
# checkpoint is a 69-byte key. The reduce reads them back by key, one at a time,
# inside its transaction.
#
# Three things become observable that were not, and they are what this file is
# about — the same three §14 M5 names as its test.
#
# The first is the offload itself. "The facts are not in the checkpoint" is a
# claim about a payload, so the payload is what is read: every map task's
# recorded output, in full, compared against the only two shapes §5 allows it to
# have.
#
# The second is what the artifacts are *for*. A crash between the map phase and
# the commit must be finished by a reduce that consumes what is already on the
# volume — §6: "the step re-runs deterministically from its checkpoint over the
# already-produced artifacts — no re-extraction". A count of extractions can
# show that no file was extracted twice; it cannot show that the second half of
# the run did not simply parse everything again under the same checkpoints. So
# the scenario removes the source files before resuming. A run that re-extracted
# anything could not then finish at all, and one that finishes has proved the
# graph was built out of the artifacts and nothing else.
#
# The third is Decision 16, which is the whole of the artifact GC: deleted on
# reduce success, kept on reduce failure. Both halves are load-bearing and they
# are asserted as one pair, because either alone is satisfiable by a bug — never
# deleting satisfies "kept on failure", and always deleting satisfies "deleted
# on success". The keys are a pure function of (tree root, repo-relative path)
# rather than of the run, precisely so that a failed batch's artifacts are
# reclaimed by the next index of the same tree; that is why there is no sweeper
# to test, and it is asserted here as the retry landing on the same keys.
#
# Everything runs against postgres:19beta2, the committed migrations and real
# gopgql, and the indexer is the real `cmd/codiq` binary run as a real
# operating-system process against a real directory on a real filesystem —
# nothing here is stubbed and nothing in the product is told it is under test
# (SPEC.md §13, "no shims"). The volume is a temp directory, which §13 asks for
# in as many words.

Feature: Facts travel on the volume, not through the checkpoint
  As an operator indexing a repository too large to hold in a checkpoint
  I want the map phase to leave its facts on disk and hand the reduce a key
  So that a batch is bounded by the volume rather than by the checkpoint database

  Background:
    Given an empty CodiQ graph and no checkpoints

  # SPEC.md §14 M5's acceptance, first clause: "artifacts written in map".
  #
  # Stated as the payload, because the payload is the thing that changed. §5
  # allows a map task's output exactly two shapes and no others: a key naming
  # the artifact it wrote, or nothing at all. There is no third shape in which
  # the facts, or a truncated version of them, or a count of them, ride along —
  # so the assertion is over the whole recorded output rather than over one
  # field of it, and it is made for every file rather than for a sample.
  #
  # The unreadable file is here for the second shape. A poison file gets no
  # artifact on purpose (index/dbos.go: loading a FileFacts with a parse error
  # would delete a good file's graph and put nothing back, and an artifact the
  # reduce must never read is a footgun on a shared volume that nothing would
  # ever collect), so its task records no key — and, being the one poison a real
  # repository can actually contain at M4, it is the one this can use.
  #
  # The size bound is the milestone's own claim, made checkable. Under M4 the
  # largest of these payloads was 82,180 characters, because it was a file's
  # entire fact set as JSON; a key is 69 characters and there is nothing else in
  # the object. Two hundred is not a budget anybody has to hit — it is a bound
  # nothing but a key can fit inside, so it fails the moment a blob comes back.
  Scenario: The map phase checkpoints a key and leaves the facts on the volume
    Given a Go module of 12 files
    And "unreadable.go" cannot be read
    When the module is indexed
    Then the run indexed 13 files and loaded 12
    And every map task checkpointed an artifact key and nothing else
    And the map task for "unreadable.go" checkpointed no artifact
    And no extract checkpoint carries more than 200 characters

  # SPEC.md §14 M5's acceptance, second clause: "crash after map → reduce
  # consumes them w/o re-extract".
  #
  # The kill lands once every file has been extracted, which puts it inside the
  # reduce — the one place where "the artifacts exist and the graph does not
  # yet" is true. That is the state §6 is about, and the first Then is that
  # state read off the volume: 152 artifacts, written by the map phase of a run
  # that has not committed anything. It is also §14 M5's "artifacts written in
  # map" asserted where it can be, since a batch that reaches its commit takes
  # them away again.
  #
  # Then the source files are deleted, and that is the entire argument. Up to
  # here a resumed run could have re-read and re-parsed all 152 files and left
  # the same graph behind, and no assertion over the graph would notice; the
  # extraction count would not notice either, since a replayed step and a re-run
  # step are both one checkpoint. With the sources gone, re-extraction is not
  # slower or wasteful, it is *impossible* — os.ReadFile has nothing to open —
  # so a run that finishes has demonstrated that it never tried. The resolve and
  # walk steps are replayed from the parent's own checkpoints for the same
  # reason, which is why the module can be deleted rather than merely mutated.
  #
  # The graph is then asked for the one edge that spans two files, over MCP, as
  # every suite before this one asks it: it is only there if the artifacts
  # carried the facts faithfully across the crash, through protobuf, off the
  # disk and into the load.
  Scenario: A crash after the map phase is finished from the volume, with nothing re-extracted
    Given a Go module of 152 files
    And the module is being indexed
    When the indexer is killed once 152 files are checkpointed
    Then the volume holds an artifact for every file the map phase extracted
    When every Go source file is deleted
    And the module is indexed again
    Then it is the same run that finished
    And the report accounts for all 152 files
    And every extraction the killed run had checkpointed is still exactly what it wrote
    And exactly 152 map tasks recorded an extraction
    And an agent asks over MCP:
      """
      {
        occurrence(name: "main", role: "definition") {
          name
          calls {
            name
            symbolKind
            descriptor
            definedIn {
              path
            }
          }
        }
      }
      """
    And the answer is:
      """
      {
        "occurrence": [
          {
            "name": "main",
            "calls": [
              {
                "name": "Greet",
                "symbolKind": "method",
                "descriptor": "scip-go gomod github.com/foo/durable . Greeter#Greet().",
                "definedIn": [
                  { "path": "greeter.go" }
                ]
              }
            ]
          }
        ]
      }
      """

  # SPEC.md §14 M5's acceptance, third clause, first half: "deleted on success".
  #
  # §6: "on reduce success the batch's artifacts are deleted from the shared
  # volume". The volume is a directory of this suite's own, so "deleted" can be
  # read literally rather than inferred — nothing else has ever written to it,
  # and after a batch that committed there is nothing left in it at all.
  #
  # Emptiness rather than a count is what is asserted, and it is the stronger
  # claim: a GC that deleted the batch's own keys and left a temporary file
  # behind would pass a count of artifacts and fail this.
  Scenario: A batch that commits takes its artifacts with it
    Given a Go module of 12 files
    When the module is indexed
    Then the run indexed 12 files and loaded 12
    And the volume holds nothing at all

  # SPEC.md §14 M5's acceptance, third clause, second half: "kept on failure" —
  # and the reason Decision 16 can ship no sweeper.
  #
  # §6 says a failed batch keeps its artifacts, and the reason is the scenario
  # above this one: they are what the retry consumes instead of re-extracting.
  # So the failure is staged the way features/mapreduce.feature stages it — a
  # constraint the last file of the batch violates, enforced by PostgreSQL
  # against the real binary's real COPY — and afterwards every file in the batch
  # is required to still have its artifact on the volume.
  #
  # What happens next is the half that is easy to forget. A failed batch ends
  # its workflow in ERROR, which nothing resumes (features/mapreduce.feature),
  # so the artifacts it kept belong to a run that will never come back for them
  # and there is no sweeper that will either. They are not orphaned because a
  # key is a pure function of the tree and the path rather than of the run: the
  # next index of the same tree computes the same keys, writes over them, and
  # takes them with it when it commits. That is asserted as what it is — the
  # keys the failed batch left are the keys the successful one deleted, and the
  # volume ends empty rather than holding two generations of the same files.
  Scenario: A batch the database refuses keeps its artifacts, and the retry reclaims them
    Given the indexed Go module of 3 files
    And the database refuses to store the type "Boom"
    And "z_boom.go" is added, defining "Boom"
    When the module is indexed and fails
    Then the volume holds an artifact for every file the map phase extracted
    And the graph is exactly what it was before
    When the database stops refusing
    And the module is indexed again
    Then the run indexed 4 files and loaded 4
    And the artifacts the failed batch kept were reclaimed, not left behind
    And the volume holds nothing at all
