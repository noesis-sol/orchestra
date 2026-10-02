# Agent Orchestra

<img width="2172" height="724" alt="661180647-c6cb72f8-21a8-4805-8bff-76a43d4db1e6" src="https://github.com/user-attachments/assets/f2e76a42-7112-4909-87df-6dc154679b2f" />


Works through a [Beads](https://github.com/gastownhall/beads) backlog with several workers side by side, as many at once as the project allows. Each ticket goes to a worker in its own [Herdr](https://herdr.dev) tab and git worktree, and finished tickets are rebased, checked and merged, one at a time, into the branch you started on. A live dashboard, built with Bubble Tea, shows the run.

## Workers and organs

Two kinds of model calls do orchestra's work, and these docs keep their names apart:

- **Workers** are the coding agents that do the tickets: one per ticket, each in its own Herdr tab and git worktree, a full Claude Code session (by default) with its tools and the [MCP servers the project chose](docs/workers.md#mcp-servers-for-workers). They edit, test, commit and close their ticket.
- **[Organs](docs/organs.md)** are orchestra's own one-shot advisers: triage of set-aside tickets, the predictor of a ticket's files, the reviewer that writes the run report, and the screen and plan that turn a feature request into tickets. Each is a single `claude -p` call with no tools; they advise and change nothing.

## Install

```
go install github.com/noesis-sol/orchestra/cmd/orchestra@latest
```

Or, from a clone, `go build -o /usr/local/bin/orchestra ./cmd/orchestra`. `orchestra --version` (or `-v`) shows which version you have. Needs Go 1.26, plus `herdr`, `git`, `claude` and `bd` (which `orchestra init` can install).

## Set up a project

In the project's repository:

```
orchestra init --check "scripts/ci-local.sh"
```

`init` offers to install Beads if it's missing and runs `bd init` where the repository has no `.beads/`. It then writes `.orchestra/`: the worker prompt, `settings.json` and a `.gitignore`. In a terminal it asks for:
- the **check command** (lint, build and tests) and its time limit;
- how many **tickets run at the same time**;
- which **MCP servers** workers get.

Flags answer without asking (`--check`, `--check-timeout`, `-c N`, `--mcp a,b`). Commit `.orchestra/` afterwards. The details are in [docs/setup.md](docs/setup.md).

## Run

From the main checkout, inside a Herdr pane, on the branch finished tickets should land on:

```
git switch -c batch/$(date +%F)
orchestra
```

How a run goes:
- **Dispatch.** Each ready ticket in `bd ready` goes to its own worker, up to `concurrent` at a time. Tickets that touch the same files or functions don't run side by side, and a ticket labelled `solo` runs alone.
- **Merge.** A finished ticket is rebased onto the branch and the check runs again before it merges. A conflict goes back to its worker to resolve. A ticket that still can't merge is set aside for review.
- **Questions.** A worker that needs your decision files a question (a ticket labelled `human`), and the run goes on. Answer it with `bd human respond <question> --response "…"`.
- **Ending.** The run ends when nothing is left to start. Something that needs you (a worker blocked or idle with its ticket open, a tool failing) stops new tickets, lets the running ones finish, and ends the run. The next run carries on with any workers it left behind.
- **One at a time.** One run at a time works on a repository. Run `orchestra init` first; `orchestra` says so if you haven't.

| Option | |
|---|---|
| `-c N` (`--concurrent`) | tickets at the same time, for this run |
| `--ticket <id>` | run only this ticket and its subtickets |
| `--feature "<request>"` | plan a request into an epic and its tickets, confirm, file them and run them |
| `--ticket-limit 2h` | stop if a worker is still going this long after its ticket started |
| `--worker-effort medium` | Claude workers' effort (`low` … `max`) |
| `-plain` | log lines instead of the dashboard |
| `LIMIT=40`, `NOTIFY=0`, `TRIAGE=0`, `REVIEW=0` | tickets per run, macOS notifications, the triage and report organs |

`orchestra -h` lists every flag. Run `orchestra plan` after filing a batch of tickets: it proposes blocks links between tickets that touch the same code, and `--apply` adds them. The details, from footprints and merges to conflict hand-back and carrying workers over, are in [docs/running.md](docs/running.md).

## The dashboard

```
 Orchestra  v0.2.0   batch/2026-09-28 · 18m40s
╭──────────────┬──────────────┬──────────────┬─────────────┬─────────────╮
│ Completed    │ Deferred     │ Needs you    │ Workers     │ In queue    │
│ ✓ 2          │ ↷ 1 ◆ 1      │ 0            │ 2 of 3      │ 11          │
╰──────────────┴──────────────┴──────────────┴─────────────┴─────────────╯
╭────────────┬─────────────┬─────────────────────────────────────────────╮
│ Tickets    │             │                                             │
├────────────┼─────────────┼─────────────────────────────────────────────┤
│ ✓ done     │ kinieta-dwv │ ffd6ce4 merged into batch/2026-09-28        │
│ ↷ deferred │ kinieta-vzg │ ◆ environment · high · prompt never submit… │
│ ▶ working  │ kinieta-kco │ Open the property model: Interpolatable p…  │
│ ▶ testing  │ kinieta-zq4 │ Add an undo stack for property edits        │
╰────────────┴─────────────┴─────────────────────────────────────────────╯
  Current
╭────────────────────────────────────────────────────────────────────────╮
│ 1 ⣾  kinieta-kco  working  4m52s                                       │
│   Open the property model: Interpolatable protocol and custom key-path │
│   / constraint-constant properties                                     │
│   ⏺ Bash(scripts/ci-local.sh lint ios)                                 │
╰────────────────────────────────────────────────────────────────────────╯
╭────────────────────────────────────────────────────────────────────────╮
│ 2 ⣾  kinieta-zq4  testing  1m07s                                       │
│   Add an undo stack for property edits                                 │
│   ⏺ Bash(scripts/ci-local.sh test core)                                │
╰────────────────────────────────────────────────────────────────────────╯
  1–2 to go to a worker's tab · s to stop after the current tickets
```

The dashboard's keys:
- **1**–**9** switch Herdr to that worker's tab.
- **s** asks whether to stop after the running tickets.
- **Ctrl+C** stops at once and leaves the workers running; press it again to quit without waiting.

Everything is also logged to `.orchestra/orchestra.log`. Scripts and agents should read the [event stream](docs/events.md) instead. See [docs/dashboard.md](docs/dashboard.md) for the rest, including the notifications.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | done: nothing left to start, the limit reached, or stopped after the running tickets as asked |
| 2 | a setup problem (all are listed), or another run going in the repository |
| 3 | a worker needs you: blocked, idle with its ticket in progress, or past the ticket limit |
| 4 | a Herdr, Beads or git failure |
| 5 | uncommitted changes in the main checkout, or it left its branch |
| 6 | a finished branch didn't fast-forward |
| 7 | workers kept failing at once: the machine, not the tickets |
| 130 | stopped with Ctrl+C, SIGTERM or SIGHUP |

Each code in full, and what to do after one, is in [docs/running.md](docs/running.md#exit-codes).

## Running orchestra through an agent

[`skills/orchestra/SKILL.md`](skills/orchestra/SKILL.md) is a skill that lets a coding agent such as Claude Code set a project up, launch a run beside its own pane, and read how it went. Copy the folder to `~/.claude/skills/orchestra/` (or `.claude/skills/orchestra/` in one project), then ask the agent to "run orchestra".

## Documentation

- [docs/dashboard.md](docs/dashboard.md): what you see, the keys, the closing lines, notifications
- [docs/setup.md](docs/setup.md): everything `orchestra init` does
- [docs/running.md](docs/running.md): options, scoped and feature runs, several tickets at once, stops and exit codes
- [docs/workers.md](docs/workers.md): the worker prompt, MCP servers for workers, hooks and run files
- [docs/organs.md](docs/organs.md): triage, the predictor, the reviewer, the screen and the plan
- [docs/events.md](docs/events.md): the event stream for scripts and agents
- [docs/development.md](docs/development.md): the check, the code's layout and tests, differences from `orchestrate.sh`
- [Changelog](CHANGELOG.md)
