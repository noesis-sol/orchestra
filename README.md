# orchestrate

Works through a [Beads](https://github.com/gastownhall/beads) backlog one ticket at a time. Each ticket goes to a coding agent in its own [Herdr](https://herdr.dev) tab and git worktree, and finished tickets are merged into the branch you started on. A Go rewrite of `orchestrate.sh`, with the same environment variables, log file and exit codes, and a live terminal view built with Bubble Tea.

## What you see

```
16:30:37 ▶ [1/40] kinieta-2e7  cancel()/pause() from a completion block still leaks inside nested sequences
16:30:37   worktree ~/Projects/kinieta-worktrees/kinieta-2e7 on wt/kinieta-2e7
16:37:06 ✓ kinieta-2e7 completed  04c8d47 merged into batch/2026-09-28
⡿  kinieta-y6j  working  2m14s · tab w2B:t9
  ⏺ Bash(scripts/ci-local.sh lint ios)
1 completed · 0 deferred · 2/40 · batch/2026-09-28 · ctrl+c stops (the worker keeps running)
```

- **Picked up** (cyan): the ticket ID and its title.
- **Completed** (green): the ticket ID only, with the merged commit.
- **Deferred** (yellow) and **stops** (red), such as `PAUSED` or `BLOCKED`, name the tab or worktree that needs you.
- **Live area** at the bottom: the worker's Herdr status, elapsed time, tab, and its latest action. It updates every 2 seconds.

Everything is also appended to `.claude/orchestrate.log` in plain text, so `tail -f` works too. When output isn't a terminal, or with `-plain`, it prints those log lines instead of the live view.

## Install

```
go build -o /usr/local/bin/orchestrate .
```

Or `go install .` puts it in `$(go env GOPATH)/bin`, which then needs to be on your `PATH`.

## Run

From the main checkout (not a worktree), inside a Herdr pane, on the branch finished tickets should land on:

```
git switch -c batch/$(date +%F)
WORKSPACE=<herdr workspace id> orchestrate
```

`orchestrate -h` lists the flags. Each flag defaults to the environment variable `orchestrate.sh` used: `WORKSPACE`, `LIMIT` (40), `DONE_SO_FAR`, `AGENT_KIND` (claude), `WORKER_PROMPT` (`.claude/worker-prompt.md`), `NOTIFY`, and `WT_ROOT` (`<repo>-worktrees`).

## Exit codes

| Code | Meaning |
|---|---|
| 0 | nothing left in `bd ready`, or the limit was reached |
| 2 | setup problem found before starting (all problems are listed) |
| 3 | a worker stayed blocked for more than 4 minutes, or went idle with its ticket still `in_progress` |
| 4 | Herdr, Beads or git failure |
| 5 | uncommitted changes in the main checkout, or it left the branch it started on |
| 6 | a finished ticket's branch does not fast-forward |
| 130 | stopped with Ctrl+C; the running worker keeps its tab and worktree |

After a 3, answer the worker in its tab, then resume with `DONE_SO_FAR=<n>`.

## Differences from orchestrate.sh

- `python3` is no longer needed.
- If `herdr agent start` reports a failure but the agent came up anyway, the orchestrator uses it instead of retrying into an occupied pane, which ends in `START_FAILED`.
- The dispatch log line includes the ticket title: `[1/40] kinieta-2e7 dispatching: <title>`.

## Development

```
go test ./...
go vet ./...
```
