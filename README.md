# Agent Orchestra

<img width="2172" height="724" alt="661180647-c6cb72f8-21a8-4805-8bff-76a43d4db1e6" src="https://github.com/user-attachments/assets/f2e76a42-7112-4909-87df-6dc154679b2f" />


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

Everything is also appended to `.orchestra/orchestra.log` in plain text, so `tail -f` works too. When output isn't a terminal, or with `-plain`, it prints those log lines instead of the live view.

## Organs

Organs are LLM-powered steps. The orchestrator gathers the evidence itself and passes it to `claude -p` with every built-in tool and MCP server disabled (`--tools "" --strict-mcp-config`), from outside the project. An organ can only read what it's given and answer. Organs advise: the orchestrator writes their output down, and no organ changes a ticket's status. A call is about 1,500 input tokens and takes 5–15 seconds.

- **Triage**, for each deferred ticket. The evidence is the ticket (`bd show`), the end of the worker's terminal, and its worktree's changes and commits. The model decides whether the cause lies in the **environment** (the machine, tools or services), the **instructions** (the worker prompt or the ticket's wording), or the **problem** itself. It adds a recommendation to the ticket's notes, and a purple `◆` line appears in the terminal. Triage runs in the background, one ticket at a time, so the loop doesn't wait.
- **Reviewer**, when the loop stops for any reason. It waits for pending triage, then reads the run's log lines, the commits merged during the run, the tickets set aside (with their triage notes) and the ticket that was running. It writes a short report in three sections: **Finished**, **Set aside** and **Needs you**. The report is shown in the terminal, rendered with Glamour, and saved to `.orchestra/reports/<start time>.md`. Pressing Ctrl+C while it's writing skips it.

Turn them off with `-triage=false` / `TRIAGE=0` and `-review=false` / `REVIEW=0`, and pick their model with `-organ-model` / `ORGAN_MODEL` (default: the `claude` CLI's). If `claude` isn't installed, organs switch off and the log says so.

## Install

```
go install github.com/noesis-sol/orchestra@latest
```

That puts `orchestra` in `$(go env GOPATH)/bin`, which must be on your `PATH`. From a clone, `go build -o /usr/local/bin/orchestra .` works too. `orchestra -version` shows which version you have. Requires Go 1.26 (fetched automatically by the Go toolchain if yours is older), plus `bd`, `herdr`, `git` and, for the organs, `claude`.

## Set up a project

In the project's repository:

```
orchestra init --check "scripts/ci-local.sh"
```

This creates `.orchestra/`, where everything `orchestra` owns in a project lives:

```
.orchestra/worker-prompt.md   committed: the worker prompt (from the built-in template)
.orchestra/settings.json      committed: the check command and how many tickets run at once
.orchestra/.gitignore         committed: ignores the three below
.orchestra/orchestra.log      the event log
.orchestra/reports/           run reports
.orchestra/run/               per-ticket files in each worktree (the worker's launch prompt)
```

`--check` fills the project's check command (lint, build and tests) into the template and saves it in `settings.json`; without it, fill in the `<…>` placeholders yourself. `init` asks how many tickets to run at the same time by default; `--concurrent N` (or `-c N`) answers it without asking, which is what an agent or a script should use. `init` also checks for `bd`, `.beads`, `herdr` and `claude`, and says what's missing. It never replaces an existing prompt unless you pass `--force`, and keeps existing settings unless `--check` or `--concurrent` change them. In a project set up by an earlier version, it moves `.claude/worker-prompt.md` into `.orchestra/` (staged with `git mv`); the old `.claude/orchestrate.log` and reports stay where they are, as history. Commit `.orchestra/` afterwards.

## Run

From the main checkout (not a worktree), inside a Herdr pane, on the branch finished tickets should land on:

```
git switch -c batch/$(date +%F)
WORKSPACE=<herdr workspace id> orchestra
```

`--concurrent N` (or `-c N`, or `ORCHESTRA_CONCURRENT=N`) sets how many tickets run at the same time for this run, overriding `settings.json`; see [Several tickets at once](#several-tickets-at-once). `orchestra -h` lists the flags. Each flag defaults to the environment variable `orchestrate.sh` used: `WORKSPACE`, `LIMIT` (40), `DONE_SO_FAR`, `AGENT_KIND` (claude), `WORKER_PROMPT` (`.orchestra/worker-prompt.md`), `NOTIFY`, and `WT_ROOT` (`<repo>-worktrees`). The organs add `TRIAGE`, `REVIEW` and `ORGAN_MODEL`, and `PROMPT_AT_LAUNCH` controls how workers get their prompt. A project not yet set up with `orchestra init` keeps working from `.claude/worker-prompt.md`, `.claude/orchestrate.log` and `.claude/orchestrate-reports/`.

## Several tickets at once

With `concurrent` above 1, up to that many workers run side by side, each in its own worktree and tab. What keeps it safe:

- **No ticket runs twice.** A ticket that's been handed out isn't picked again, even before its worker claims it.
- **Merges queue.** A finished ticket waits for its turn. If other tickets merged while it ran, its branch is rebased onto the current one, and the check command from `settings.json` runs again on the rebased code before it merges. A conflict (`MERGE_CONFLICT`) or a failing check (`CHECKS_FAILED`) leaves that ticket for review, and the run goes on. Without a check command, a rebased ticket merges unchecked, and the log says so.
- **One git writer at a time.** Worktree creation, rebases, merges and cleanup in the main repository take a lock, so workers don't trip over git's lock files.
- **Stopping drains.** Something that stops the run (`PAUSED`, `BLOCKED`, a tool failure) is logged as `HOLD`. No new tickets start, the running ones finish and merge, and then the run ends with that reason. With one ticket at a time, nothing changes. Ctrl+C still stops at once.

What `orchestra` can't make safe for you:

- **Checks that collide.** Every worker runs the project's checks. Tests that share one named simulator, a port or a database can fail when two run at once.
- **Files every ticket touches.** If most tickets add to the same spot, like a CHANGELOG's `[Unreleased]` list, rebases conflict. A `.gitattributes` line such as `CHANGELOG.md merge=union` keeps both sides' lines for list-like files.

Start at 1, and raise it once the checks run cleanly side by side.

## Worker prompt

Each worker gets the prompt at `-prompt` / `WORKER_PROMPT` (default `.orchestra/worker-prompt.md`), with every `TICKET_ID` replaced by its ticket. `orchestra init` writes it from [`prompts/worker-prompt.md`](prompts/worker-prompt.md), which is built into the binary. Each rule prevents a way a run goes wrong:

- **Own worktree, never push, the orchestrator merges.** Workers can't disturb each other or the branch that finished tickets land on.
- **Commit with the ticket ID, closing only when the checks pass.** A ticket is merged only if a commit names it and its worktree is clean.
- **Checks in the foreground.** A worker waiting on a background command looks idle, and an idle worker with its ticket still open stops the run (`PAUSED`).
- **Close, and let the batch PR run CI.** A ticket whose change only CI can verify (a workflow, a platform the local checks don't cover) is closed once the local checks pass, with an "Awaits CI" note. The batch goes to the main branch through a pull request that runs every CI job, so each ticket needn't wait for its own.
- **Ask, don't wait.** A ticket that needs the maintainer's decision gets a question ticket labelled `human` that blocks it. The orchestrator never hands a question to a worker; it shows the ticket as **? for you**, and the run goes on. Answer with `bd human respond <question> --response "…"`, and the ticket returns to the queue with its branch rebased onto the current one.

Claude workers are started with a one-line instruction to read `.orchestra/run/prompt.md` in their worktree, where `orchestra` writes the prompt (kept out of git through the repository's `info/exclude`, so it's ignored even on a branch cut before `.orchestra/.gitignore` was committed). Herdr can't pass line breaks to an agent, and a prompt pasted into the input box can go unsubmitted. `-prompt-at-launch=false` pastes it instead.

## Running orchestra through an agent

Optional: [`skills/orchestra/SKILL.md`](skills/orchestra/SKILL.md) is a skill for coding agents such as Claude Code. It tells the agent how to:
- find out where a project stands: whether `init` has run, which files the project uses, whether a run is active;
- launch a run in a Herdr pane beside its own;
- leave the checkout alone during a run;
- read the log, the run report and the tickets' triage notes when something is set aside or a run stops;
- finish a ticket by hand;
- leave pushing, answering questions and worker dialogs to you.

To use it, copy the folder into your skills: `~/.claude/skills/orchestra/` for every project, or `.claude/skills/orchestra/` in one project. Then ask the agent to "run orchestra", "set this project up for orchestra" or "why did the run stop?".

## Exit codes

| Code | Meaning |
|---|---|
| 0 | nothing left in `bd ready`, or the limit was reached |
| 2 | setup problem found before starting (all problems are listed) |
| 3 | a worker stayed blocked for more than 4 minutes, or went idle with its ticket still `in_progress` |
| 4 | Herdr, Beads or git failure |
| 5 | uncommitted changes in the main checkout, or it left the branch it started on |
| 6 | a finished ticket's branch does not fast-forward (it should have been rebased first) |
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
