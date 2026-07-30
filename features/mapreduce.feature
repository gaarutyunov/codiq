# M4 — the index as a map-reduce batch (SPEC.md §14 M4, §5, §6, §9).
#
# M3 checkpointed M2's loop without changing its shape: one file, one
# transaction, one link pass at the end. M4 splits the loop in two. The parent
# workflow freezes the file list and enqueues one *extract workflow per file* on
# the durable "extract" queue — a queue carries workflows, not steps (§9) — then
# gathers what they produced and hands the successful subset to a single reduce
# step that writes the whole batch and rebuilds the cross-file edges inside one
# transaction (§6: "reduce sequence: base load → core link → overlay producers,
# all within the one transaction").
#
# Two things that were not observable before become observable, and they are
# what this file is about.
#
# The first is the poison file (§5: "a poison file is flagged and skipped, never
# blocking the batch"). M2 could only skip a file the *extractor* rejected, and
# tree-sitter is error-tolerant enough that no Go content ever reaches that path
# — index_go.feature says so, and says why. M4 adds the other half: a map task
# that fails outright is a skipped file rather than a failed run, and a file the
# process cannot read is the way a real repository produces one. The two skips
# are different findings about the tree and the run says which it made.
#
# The second is atomicity at batch granularity. M2 and M3 wrote one file per
# transaction, so a failure left the files before it written and the files after
# it not. M4 writes every file in the batch and the link pass together, so a
# failure leaves the graph exactly as it was — including the files whose rows
# the transaction had already replaced. Seeing that from outside the process
# takes a write the database refuses, on the last file of a batch, and then a
# look at what is still there.
#
# The third thing here is not new behaviour but new risk. Moving the per-file
# work into a child workflow moved the thing that identifies it: a child's id is
# derived from the parent's step id at the moment it was enqueued, and a step id
# is handed out in the order the step starts. So the map phase's determinism is
# now a property of *how the parent loops*, and a resumed run is where a wrong
# answer would show up. The last scenario is that resumed run.
#
# Everything runs against postgres:19beta2, the committed migrations and real
# gopgql, and the indexer is the real `cmd/codiq` binary run as a real
# operating-system process — nothing here is stubbed and nothing in the product
# is told it is under test (SPEC.md §13, "no shims").

Feature: Indexing as a map-reduce batch
  As an operator indexing a repository that is not perfectly clean
  I want one file the indexer cannot handle to cost me that file and nothing else
  So that a batch either lands whole or leaves the graph I already had standing

  Background:
    Given an empty CodiQ graph and no checkpoints

  # SPEC.md §14 M4's literal acceptance, first half: "repo with one broken file
  # → poison-skip + others indexed".
  #
  # Broken has to mean something a repository can actually contain. At M4 there
  # is exactly one such thing, and it is not a matter of content: tree-sitter
  # parses anything, so a file of prose is a file with no facts in it rather
  # than a failure (index_go.feature covers that case, and it is a different
  # one). What does fail is a file `walk` selects and `os.ReadFile` refuses. Its
  # map task returns an error, and §9's "the batch proceeds over the successful
  # subset" is the sentence under test.
  #
  # The count on its own would not be evidence. A run that never selected the
  # file would report the same three files loaded, so the file is required to be
  # *named* as skipped — §5's "flagged", and the reason cmd/codiq lists skips
  # rather than counting them — and the reason is required to be the read
  # failure and not a parse failure, because Skip.Err is documented to tell
  # those apart and a batch that conflated them would report the wrong finding
  # about the tree. Then the rest of the module is required to have arrived
  # whole, which is the cross-file edge M2's first scenario asks for, asked here
  # the same way over MCP.
  Scenario: A file the indexer cannot read is skipped and the rest is indexed
    Given a Go module of 3 files
    And "unreadable.go" cannot be read
    When the module is indexed
    Then the run indexed 4 files and loaded 3
    And the report names "unreadable.go" as skipped
    And the reason given is the read failure and not a parse error
    And "unreadable.go" is not in the graph
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

  # SPEC.md §14 M4's literal acceptance, second half: "reduce atomicity".
  #
  # The batch is one transaction, and the only way to see that from outside the
  # process is to make it fail. reduce.go names the failure it is defending
  # against precisely — a file the extractor could not parse never reaches the
  # reduce, so "a failure at this point is the database refusing a write" — and
  # that is the failure staged here: a constraint the last file of the batch
  # violates, on a real table, enforced by PostgreSQL against the real binary's
  # real COPY. Nothing in the product is stubbed or aware of the test; what is
  # arranged is the database's behaviour, which is the collaborator whose
  # failure the transaction exists for.
  #
  # It has to be the *last* file, because everything interesting is what happens
  # to the earlier ones. Every file in a batch is deleted and re-COPYed, so by
  # the time the refusal lands the three files that were already there have been
  # rewritten on the transaction. A per-file transaction would leave them
  # rewritten with fresh row ids and only the fourth missing — a graph that
  # looks right, counts right, and has quietly churned every id below the file.
  # So the assertion is not "the new type is absent" but "nothing moved": the
  # same rows, the same cross-file edges, the same file ids as before the run.
  Scenario: A batch the database refuses leaves the graph exactly as it was
    Given the indexed Go module of 3 files
    And the database refuses to store the type "Boom"
    When "z_boom.go" is added, defining "Boom"
    And the module is indexed and fails
    Then the runs recorded are, oldest first: successful, refused
    And the graph is exactly what it was before
    And every file kept the identity it was first given
    And "Boom" is nowhere in the graph

  # A reduce that failed is not a crash, and CodiQ must not treat it as one.
  # durable_index.feature is about the run whose process died: it is left
  # PENDING, DBOS's recovery restarts it, and the next invocation *finishes* it.
  # A reduce that returned an error ends the workflow in ERROR instead, which
  # recovery does not look at and cmd/codiq's `start` does not list. So the next
  # invocation is a new run that maps every file again.
  #
  # That is the right answer and the opposite one would be a trap. A step's
  # checkpoint records its failure as faithfully as its success, so resuming an
  # ERRORed run would replay the recorded failure rather than retry the write —
  # and the repository could never be indexed again, by anyone, until somebody
  # went into the checkpoint database by hand. The scenario therefore ends where
  # an operator would want it to: the constraint is lifted, the indexer is run
  # again, and the file that could not be written is in the graph.
  Scenario: A batch that failed is not resumed; the next run indexes everything
    Given the indexed Go module of 3 files
    And the database refuses to store the type "Boom"
    And "z_boom.go" is added, defining "Boom"
    And the module is indexed and fails
    When the database stops refusing
    And the module is indexed again
    Then it is not a continuation
    And the runs recorded are, oldest first: successful, refused, successful
    And an agent asks over MCP:
      """
      {
        occurrence(name: "Boom", role: "definition") {
          symbolKind
          descriptor
          definedIn {
            path
          }
        }
      }
      """
    And the answer is:
      """
      {
        "occurrence": [
          {
            "symbolKind": "type",
            "descriptor": "scip-go gomod github.com/foo/durable . Boom#",
            "definedIn": [
              { "path": "z_boom.go" }
            ]
          }
        ]
      }
      """

  # The rewrite's own risk, and the only scenario here that needs a corpus big
  # enough to interrupt.
  #
  # A DBOS step is replayed by step id; a step id is handed out in the order the
  # step starts; and a child workflow's id is `<parent id>-<the step id the
  # parent held when it enqueued the child>`. So at M4 a file's *identity* is
  # its position in the parent's step sequence, and that sequence is only stable
  # because the parent enqueues and gathers in two plain sequential loops over
  # the frozen walk. Start those steps from goroutines, or gather whichever
  # finishes first, and the numbering becomes a function of scheduling: a
  # resumed run then hands the same file a different child id, extracts it
  # again, and eventually fails replay outright.
  #
  # Which is why the evidence is the children's own checkpoints rather than the
  # parent's. Every extract checkpoint the killed process had written is read
  # before anything resumes it, and compared byte for byte once another process
  # has finished the run: same child workflow, same step id, same recorded
  # output. A re-extracted file would have written its facts under a different
  # child; a renumbered one would leave the ledger it wrote orphaned; and either
  # would push the number of children that recorded an extraction above the
  # number of files, which is asserted too because it is the cheapest way to
  # notice a rival task nobody is waiting for.
  Scenario: A crash does not make the resumed run extract a file twice
    Given a Go module of 152 files
    And the module is being indexed
    When the indexer is killed once 16 files are checkpointed
    And the module is indexed again
    Then it is the same run that finished
    And every extraction the killed run had checkpointed is still exactly what it wrote
    And exactly 152 map tasks recorded an extraction
