# orchestrate

Works through a [Beads](https://github.com/gastownhall/beads) backlog one ticket at a time. Each ticket goes to a coding agent in its own [Herdr](https://herdr.dev) tab and git worktree, and finished tickets are merged into the branch you started on. A Go rewrite of `orchestrate.sh`, with the same environment variables, log file and exit codes, and a live terminal view built with Bubble Tea.

## What you see

```
17:02:34 ▶ [1/40] kinieta-2e7  cancel()/pause() from a completion block still leaks inside nested sequences
17:02:34   worktree ~/Projects/kinieta-worktrees/kinieta-2e7 on wt/kinieta-2e7
17:09:06 ✓ kinieta-2e7 completed  04c8d47 merged into batch/2026-09-28
17:09:07 ▶ [2/40] kinieta-jqm  Support visionOS
17:14:35 ↷ kinieta-jqm deferred  by the worker
17:14:43 ◆ kinieta-jqm triage: instructions · high  Work done in f596fa9; deferred only by the awaits-CI rule

 Orchestrator
╭────────────┬───────────────────────────────────────────────────╮
│ Completed  │ ✓ 1                                               │
│ Deferred   │ ↷ 1 · ◆ 1 triaged                                 │
│ Picked up  │ 3 of 40 max                                       │
│ In queue   │ 17 ready                                          │
│ Branch     │ batch/2026-09-28                                  │
│ Worker tab │ w2B:t9                                            │
│ Running    │ 12m0s                                             │
╰────────────┴───────────────────────────────────────────────────╯
╭────────────────────────────────────────────────────────────────╮
│ ⣾  kinieta-y6j  working  2m14s                                 │
│   Warn in debug builds when a chain call is silently ignored   │
│   ⏺ Bash(scripts/ci-local.sh lint ios)                         │
╰────────────────────────────────────────────────────────────────╯
  ctrl+c stops · the worker keeps running
```

- **Picked up** (cyan): the ticket ID and its title.
- **Completed** (green): the ticket ID only, with the merged commit.
- **Deferred** (yellow) and **stops** (red), such as `PAUSED` or `BLOCKED`, name the tab or worktree that needs you.
- **Triage** (purple `◆`): the triage organ's verdict on a deferred ticket (see Organs).
- **Live area** at the bottom: an **Orchestrator** title, the run's totals, and the active ticket, all sized to the pane. The ticket box has a cyan border while the worker runs, red when it's blocked, and grey between tickets. It updates every 2 seconds.
- **Run report** when the loop stops (see Organs).

Everything is also appended to `.claude/orchestrate.log` in plain text, so `tail -f` works too. When output isn't a terminal, or with `-plain`, it prints those log lines instead of the live view.

## Organs

Organs are LLM-powered steps. The orchestrator gathers the evidence itself and passes it to `claude -p` with every built-in tool and MCP server disabled (`--tools "" --strict-mcp-config`), from outside the project. An organ can only read what it's given and answer. Organs advise: the orchestrator writes their output down, and no organ changes a ticket's status. A call is about 1,500 input tokens and takes 5–15 seconds.

- **Triage**, for each deferred ticket. The evidence is the ticket (`bd show`), the end of the worker's terminal, and its worktree's changes and commits. The model decides whether the cause lies in the **environment** (the machine, tools or services), the **instructions** (the worker prompt or the ticket's wording), or the **problem** itself. It adds a recommendation to the ticket's notes, and a purple `◆` line appears in the terminal. Triage runs in the background, one ticket at a time, so the loop doesn't wait.
- **Reviewer**, when the loop stops for any reason. It waits for pending triage, then reads the run's log lines, the commits merged during the run, the tickets set aside (with their triage notes) and the ticket that was running. It writes a short report in three sections: **Finished**, **Set aside** and **Needs you**. The report is shown in the terminal, rendered with Glamour, and saved to `.claude/orchestrate-reports/<start time>.md`. Pressing Ctrl+C while it's writing skips it.

Turn them off with `-triage=false` / `TRIAGE=0` and `-review=false` / `REVIEW=0`, and pick their model with `-organ-model` / `ORGAN_MODEL` (default: the `claude` CLI's). If `claude` isn't installed, organs switch off and the log says so. Add `.claude/orchestrate-reports/` to the project's `.gitignore`.

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

`orchestrate -h` lists the flags. Each flag defaults to the environment variable `orchestrate.sh` used: `WORKSPACE`, `LIMIT` (40), `DONE_SO_FAR`, `AGENT_KIND` (claude), `WORKER_PROMPT` (`.claude/worker-prompt.md`), `NOTIFY`, and `WT_ROOT` (`<repo>-worktrees`). The organs add `TRIAGE`, `REVIEW` and `ORGAN_MODEL`.

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

`TestLiveOrgans` calls the real `claude` against a real repository without writing anything. Its comment shows how to run it.
