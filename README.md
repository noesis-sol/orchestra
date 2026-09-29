# Agent Orchestra

<img width="2172" height="724" alt="52045c80-b098-423f-8c98-246654400497" src="https://github.com/user-attachments/assets/c6cb72f8-21a8-4805-8bff-76a43d4db1e6" />

Works through a [Beads](https://github.com/gastownhall/beads) backlog one ticket at a time. Each ticket goes to a coding agent in its own [Herdr](https://herdr.dev) tab and git worktree, and finished tickets are merged into the branch you started on. A Go rewrite of `orchestrate.sh`, with the same environment variables, log file and exit codes, and a live terminal view built with Bubble Tea.

## What you see

One dashboard, updated in place: nothing is printed above it while the loop runs.

```
 Orchestra v0.1.0
╭────────────┬───────────────────────────────────────────────────╮
│ Completed  │ ✓ 2                                               │
│ Deferred   │ ↷ 1 · ◆ 1 triaged                                 │
│ Picked up  │ 4 of 40 max                                       │
│ In queue   │ 12 ready                                          │
│ Branch     │ batch/2026-09-28                                  │
│ Running    │ 18m40s                                            │
╰────────────┴───────────────────────────────────────────────────╯
╭────────────┬─────────────┬────────────────────────────────────╮
│ Tickets    │             │                                    │
├────────────┼─────────────┼────────────────────────────────────┤
│ ✓ done     │ kinieta-dwv │ ffd6ce4 merged into batch/2026-09… │
│ ↷ deferred │ kinieta-vzg │ ◆ environment · high · Prompt was… │
│ ✓ done     │ kinieta-y6j │ 6097367 merged into batch/2026-09… │
│ ▶ working  │ kinieta-kco │ Open the property model: Interpol… │
╰────────────┴─────────────┴────────────────────────────────────╯
╭────────────────────────────────────────────────────────────────╮
│ ⣾  kinieta-kco  working  4m52s                                 │
│   Open the property model: Interpolatable protocol and custom… │
│   ⏺ Bash(scripts/ci-local.sh lint ios)                         │
╰────────────────────────────────────────────────────────────────╯
  ctrl+c stops · the worker keeps running
```

- **Totals** for the run.
- **Tickets**: one row per ticket, updated as it moves. A **picked-up** ticket (cyan) shows its title. A **completed** one (green) shows only the merged commit. A **deferred** one (yellow) shows why, replaced by the triage organ's verdict (purple `◆`) once it's in. A ticket that stopped the run is red. The table shows the most recent tickets that fit in the pane.
- **Active ticket**: the worker's status, elapsed time, the ticket title and the worker's latest action. The border is cyan while the worker runs, red when it's blocked, and grey between tickets. It updates every 2 seconds.

When the loop stops, the dashboard stays on screen as the run's summary, followed by the final line and the run report (see Organs).

Everything is also appended to `.claude/orchestrate.log` in plain text, so `tail -f` works too. When output isn't a terminal, or with `-plain`, it prints those log lines instead of the live view.

## Organs

Organs are LLM-powered steps. The orchestrator gathers the evidence itself and passes it to `claude -p` with every built-in tool and MCP server disabled (`--tools "" --strict-mcp-config`), from outside the project. An organ can only read what it's given and answer. Organs advise: the orchestrator writes their output down, and no organ changes a ticket's status. A call is about 1,500 input tokens and takes 5–15 seconds.

- **Triage**, for each deferred ticket. The evidence is the ticket (`bd show`), the end of the worker's terminal, and its worktree's changes and commits. The model decides whether the cause lies in the **environment** (the machine, tools or services), the **instructions** (the worker prompt or the ticket's wording), or the **problem** itself. It adds a recommendation to the ticket's notes, and a purple `◆` line appears in the terminal. Triage runs in the background, one ticket at a time, so the loop doesn't wait.
- **Reviewer**, when the loop stops for any reason. It waits for pending triage, then reads the run's log lines, the commits merged during the run, the tickets set aside (with their triage notes) and the ticket that was running. It writes a short report in three sections: **Finished**, **Set aside** and **Needs you**. The report is shown in the terminal, rendered with Glamour, and saved to `.claude/orchestrate-reports/<start time>.md`. Pressing Ctrl+C while it's writing skips it.

Turn them off with `-triage=false` / `TRIAGE=0` and `-review=false` / `REVIEW=0`, and pick their model with `-organ-model` / `ORGAN_MODEL` (default: the `claude` CLI's). If `claude` isn't installed, organs switch off and the log says so. Add `.claude/orchestrate-reports/` to the project's `.gitignore`.

## Install

```
go install github.com/noesis-sol/orchestra@latest
```

That puts `orchestra` in `$(go env GOPATH)/bin`, which must be on your `PATH`. From a clone, `go build -o /usr/local/bin/orchestra .` works too. `orchestra -version` shows which version you have. Requires Go 1.26 (fetched automatically by the Go toolchain if yours is older), plus `bd`, `herdr`, `git` and, for the organs, `claude`.

## Run

From the main checkout (not a worktree), inside a Herdr pane, on the branch finished tickets should land on:

```
git switch -c batch/$(date +%F)
WORKSPACE=<herdr workspace id> orchestra
```

`orchestra -h` lists the flags. Each flag defaults to the environment variable `orchestrate.sh` used: `WORKSPACE`, `LIMIT` (40), `DONE_SO_FAR`, `AGENT_KIND` (claude), `WORKER_PROMPT` (`.claude/worker-prompt.md`), `NOTIFY`, and `WT_ROOT` (`<repo>-worktrees`). The organs add `TRIAGE`, `REVIEW` and `ORGAN_MODEL`, and `PROMPT_AT_LAUNCH` controls how workers get their prompt.

## Worker prompt

Each worker gets the prompt at `-prompt` / `WORKER_PROMPT` (default `.claude/worker-prompt.md` in the project), with every `TICKET_ID` replaced by its ticket. [`prompts/worker-prompt.md`](prompts/worker-prompt.md) is a reference to start from, not used by `orchestra` itself. Copy it into the project and replace the `<…>` placeholders with the project's own check commands. Each rule prevents a way a run goes wrong:

- **Own worktree, never push, the orchestrator merges.** Workers can't disturb each other or the branch that finished tickets land on.
- **Commit with the ticket ID, closing only when the checks pass.** A ticket is merged only if a commit names it and its worktree is clean.
- **Checks in the foreground.** A worker waiting on a background command looks idle, and an idle worker with its ticket still open stops the run (`PAUSED`).
- **Defer, don't wait.** A ticket that needs CI is deferred with a note, so the run moves on and triage and the report pick it up.
- **Ask, don't wait.** A ticket that needs the maintainer's decision gets a question ticket labelled `human` that blocks it. The orchestrator never hands a question to a worker; it shows the ticket as **? for you**, and the run goes on. Answer with `bd human respond <question> --response "…"`, and the ticket returns to the queue with its branch rebased onto the current one.

Claude workers are started with a one-line instruction to read `.orchestra/prompt.md` in their worktree, where `orchestra` writes the prompt (`.orchestra/` is kept out of git through the repository's `info/exclude`). Herdr can't pass line breaks to an agent, and a prompt pasted into the input box can go unsubmitted. `-prompt-at-launch=false` pastes it instead.

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

[Changelog](CHANGELOG.md)
