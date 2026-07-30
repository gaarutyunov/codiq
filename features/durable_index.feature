# M3 — the index as a DBOS workflow (SPEC.md §14 M3, §9).
#
# M2's loop is a process. It walks a tree, loads every file it can parse and
# rebuilds the cross-file edges; if the process dies halfway, the next one
# starts from nothing. M3 is that same loop with every stage checkpointed into
# a second database — `codiq_dbos`, a separate database on the same instance so
# checkpoints never contend with the bulk writes they are checkpointing (§9,
# "Isolation") — so a run that dies halfway is *finished* by the next
# invocation rather than redone.
#
# Nothing about *what* is indexed changes, which is why the first scenario ends
# where M2's scenarios do: an agent asks a question over MCP and gets the answer
# M2 would have given. The new sentence is the one in the middle — the process
# that started the index is not the process that finished it.
#
# The corpus is generated rather than committed, and generated large on purpose.
# M2's two-file fixture is indexed in milliseconds, which is nothing to
# interrupt. 152 files takes long enough that a signal lands in the middle of a
# run, and its shape is still M2's fixture: greeter.go, main.go, and 150
# independent files padded around them. The one cross-file call remains the only
# call in the whole graph, so the navigation assertion below is about the same
# edge M2's is.
#
# Interrupting is deterministic and never a sleep. Every scenario that stops a
# run waits until a stated number of files are *checkpointed* — counted in the
# DBOS tables, which is the only place "this file's work is durable" is written
# down — and signals only then. A corpus too small to reach that count before
# the run ends fails the step saying so, rather than passing vacuously against a
# process that had already exited.
#
# Two ways of stopping a run are covered and they are not the same thing, which
# is the whole reason both are here. A killed process (SIGKILL) leaves its
# workflow PENDING and DBOS's own recovery restarts it; a process asked to stop
# (SIGTERM, Ctrl-C) cancels its workflow on the way out and recovery does not
# look at cancelled workflows at all, so the next run has to resume it by name.
# Only the first is a crash. Both resume from the same checkpoints.
#
# Everything runs against postgres:19beta2, the committed migrations and real
# gopgql, and the indexer is the real `cmd/codiq` binary, signalled as a real
# operating-system process — there is no in-process stand-in for a crash
# (SPEC.md §13, "no shims").

Feature: Indexing that outlives the process it started in
  As an operator who indexes a repository on every push
  I want an index whose process died to be finished rather than started over
  So that a crash costs the work that was in flight and not the work already done

  Background:
    Given an empty CodiQ graph and no checkpoints
    And a Go module of 152 files

  # SPEC.md §14 M3's literal acceptance: "kill after K files, restart, assert
  # resume + graph == M2".
  #
  # Every clause matters. PENDING is the state a *crash* leaves behind — a run
  # nobody stopped, whose executor is simply gone — and it is what makes the
  # claim a durability claim rather than a shutdown-hook claim. "The same run"
  # is the second half: starting a fresh workflow beside the dead one would also
  # end with a complete graph, and would also be wrong, because two indexers
  # over one corpus collide in the link pass. And the graph is compared against
  # one an uninterrupted run builds rather than against a hardcoded shape, so
  # the assertion stays true of whatever M2 means by a correct graph.
  Scenario: An index whose process was killed is finished by the next run
    Given the module is being indexed
    When the indexer is killed once 16 files are checkpointed
    Then the run is left pending
    When the module is indexed again
    Then it is the same run that finished
    And the report accounts for all 152 files
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
    And the graph is exactly what an uninterrupted index builds

  # Resuming is only worth anything if it *continues*. A run that replayed its
  # checkpoints by re-doing the work behind them would end in the same place and
  # buy nothing, and would look identical in the graph, because loading a file
  # twice is loading it once (§6, and the reason at-least-once steps are safe
  # here at all).
  #
  # So the evidence is the checkpoint ledger itself, which is the one place the
  # difference is visible: a step that is replayed reads its recorded output,
  # while a step that is re-run writes a new one. Every row the killed run wrote
  # is therefore compared byte for byte against what is there once the resumed
  # run has finished, and the finished ledger is required to hold exactly one
  # row per file — not the two a restart would leave.
  Scenario: The work the killed run had already checkpointed is not done again
    Given the module is being indexed
    When the indexer is killed once 16 files are checkpointed
    And the module is indexed again
    Then every checkpoint the killed run wrote is still exactly what it wrote
    And each of the 152 files was checkpointed exactly once

  # Ctrl-C is not a crash, and CodiQ does not pretend it is. SIGINT and SIGTERM
  # are trapped and turned into a cancel, so the run stops between transactions
  # instead of inside one and the process says where the work went; the workflow
  # ends CANCELLED rather than PENDING, and DBOS's recovery ignores cancelled
  # workflows entirely. The next invocation has to resume it explicitly, by name.
  #
  # It is a different door back in, and this scenario exists because it is the
  # one a person actually uses. What is on the other side of it is the same: the
  # same run, the same checkpoints, the same module in the graph at the end.
  Scenario: An index stopped on purpose is cancelled, and resumed by name
    Given the module is being indexed
    When the indexer is interrupted once 16 files are checkpointed
    Then the run is left cancelled
    And the process said the index continues on the next run
    When the module is indexed again
    Then it is the same run that finished
    And the report accounts for all 152 files

  # The converse of the first scenario, and the reason a run's identity cannot
  # be a pure function of the repository. Indexing is idempotent and meant to be
  # re-run on every push (§6, M2's own re-index scenario), so a run whose ID was
  # the tree's would hand back the first run's checkpointed result and index
  # nothing at all — durability would have quietly turned into a cache.
  #
  # A finished run is therefore left finished, the next invocation is a new run
  # that does the work again, and the graph it leaves behind is the graph that
  # was already there, ids included. That is M2's idempotence, restated for a
  # loop that now has a memory.
  Scenario: A finished index is not resumed; the next run is a new run
    Given the module has been indexed
    When the module is indexed again
    Then it is not a continuation
    And 2 runs are recorded, all successful
    And the graph is exactly what it was before
    And every file kept the identity it was first given
