# Development

Working on orchestra itself. Back to the [README](../README.md).

## Development

```
scripts/check.sh
```

`scripts/check.sh` is the full check: `go vet ./...`, the race tests (`go test -race ./...`, through [gotestsum](https://github.com/gotestyourself/gotestsum)) and golangci-lint. It is also orchestra's own check command for this repository (`.orchestra/settings.json`), so a ticket that fails lint isn't merged, and workers run it before closing a ticket. golangci-lint runs the linters the [Uber Go style guide](https://github.com/uber-go/guide/blob/master/style.md#linting) asks for, configured in `.golangci.yml`: errcheck (terminal writes excepted), goimports, revive, govet and staticcheck, plus predeclared and lll (lines up to 120 columns). The script runs gotestsum and golangci-lint with `go run` at pinned versions (v1.13.0 and v2.14.0), so a machine needs only Go; the first run downloads them.

A test that fails is run once more, on its own, and the check passes if it passes then, printing a line `FLAKY: <package> <test>` for it, such as `FLAKY: ./internal/tui TestInitFormKeepsOrTypesACustomConcurrency`, which orchestra warns of when it merges. A test that fails its rerun fails the check, and so, without a rerun, do more than three failed tests, a data race, a panic and a package that fails outside its tests (goleak's check in `TestMain`). A `FLAKY:` line is a bug to file and fix, not noise: the test passes alone but fails under load, such as several workers' checks at once, and the rerun only kept it from failing a merge. To reproduce one, run many copies of it at once, as orchestra-4wb.26 did with 12 copies of 500 runs each:

```
go test -c -race -o /tmp/tui.test ./internal/tui
for i in $(seq 12); do /tmp/tui.test -test.count 500 \
  -test.run '^TestInitFormKeepsOrTypesACustomConcurrency$' & done; wait
```

A failure in a few thousand runs is the flake; a fix holds when the same load brings none (`-test.run '^TestParent$/^sub$'` picks a subtest).

`TestLiveOrgans` calls the real `claude` against a real repository without writing anything. Its comment shows how to run it.

The run loop, `internal/dispatch`, has one file per concern, its tests in the `_test.go` file of the same name:

| File | What's in it |
| --- | --- |
| `loop.go` | the `Loop` type, `Config`, exit codes, timings and shared helpers |
| `run.go` | `Run`: picking the next ticket, solo tickets, HOLD, interrupts (tests in `run_test.go` and `schedule_test.go`) |
| `ids.go` | which ticket IDs can be run (plain names, valid in a branch), setting aside the others |
| `start.go` | a ticket's worktree, starting, adopting and naming its worker, delivering its prompt |
| `work.go` | one ticket from start to outcome: asked, deferred, paused or closed |
| `asked.go` | tickets waiting on a question that don't come back through `bd ready`: adopting a worker that claimed or closed its ticket in its tab, one it deferred or left, labelling what is left as the run ends |
| `carry.go` | the workers a run leaves behind (asked, left running), saved to `.orchestra/run/state.json` as it ends and checked and carried over by the next run |
| `settle.go` | waiting for a worker to settle (Herdr's status, its Stop hook or the start-up grace), telling one that stopped with its ticket in progress to continue, reading its status, the dashboard watcher |
| `merge.go` | merging a closed ticket: rebase, check command, fast-forward, cleanup (the worker's tab only while Herdr still has it labelled with the ticket's ID) |
| `holds.go` | tickets held for an unmerged blocker, the `unmerged` label, tickets set aside, deferred or waiting on a question |
| `scope.go` | parents after their children, runs of one ticket and its subtickets (`--ticket`) and how they end (`SCOPE_DONE`, `SCOPE_OPEN`) |
| `footprint.go` | tickets' footprints (the files and functions they name, and the files their workers edit), skipping a ticket that overlaps a running one, warning when two workers edit one file |
| `plan.go` | `orchestra plan`'s proposal: blocks links between open tickets whose footprints overlap |
| `events.go` | the log file, notifications, the event stream (`events.jsonl`), events and status sent to the dashboard |
| `drain.go` | stopping after the running tickets when asked (s in the dashboard, SIGUSR1), and taking that back |
| `environment.go` | holding the run when workers keep failing at once or triage keeps blaming the environment, reopening the tickets that did nothing, probing the machine to take tickets again |
| `advice.go` | triage and the run review |
| `predict.go` | predicting the files of ready tickets that name none, in the background |
| `deps.go` | the interfaces to Beads, Herdr, git and workers' reports |

The fakes the tests share are in `fakes_test.go` (Beads, workers, sinks), `fakeherdr_test.go`, `fakegit_test.go` and `loop_test.go`; the harness that runs a whole loop against them is in `helpers_test.go`. A scenario whose subject is timing uses `newTimedHarness` inside `synctest.Test`: git in memory, and the loop's real durations on the bubble's clock, which moves on whenever every goroutine waits. One whose subject is git (merging, rebasing, conflicts, reusing a worktree) uses `newHarness`, with real git on the real clock.

[Changelog](../CHANGELOG.md)

## Differences from orchestrate.sh

- Several tickets run at once (`--concurrent`), each with its own worker, worktree and tab, and finished ones merge one at a time; `orchestrate.sh` worked on one ticket at a time.
- `python3` is no longer needed.
- If `herdr agent start` reports a failure but the worker came up anyway, the orchestrator uses it instead of retrying into an occupied pane, which ends in `START_FAILED`.
- The dispatch log line includes the ticket title: `[1/40] kinieta-2e7 dispatching: <title>`.
