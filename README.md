# Claude Agent Orchestra

<p align="center">
<img width="1036" height="380" alt="AI Conductor and Orbital Network (web)" src="https://github.com/user-attachments/assets/3dae50d5-103b-468e-b0c2-44e2b214a5b9" />
</p>





Works through a [Beads](https://github.com/gastownhall/beads) backlog with several workers side by side, as many at once as the project allows. Each ticket goes to a worker in its own tab, in [Herdr](https://herdr.dev), [tmux](https://github.com/tmux/tmux) or [cmux](https://cmux.com), and git worktree, and finished tickets are rebased, checked and merged, one at a time, into the branch you started on. A live dashboard, built with Bubble Tea, shows the run.

## Workers and organs

Two kinds of model calls do orchestra's work, and these docs keep their names apart:

- **Workers** are the coding agents that do the tickets: one per ticket, each in its own tab (Herdr's, tmux's or cmux's) and git worktree, a full Claude Code session (by default) with its tools and the [MCP servers the project chose](docs/workers.md#mcp-servers-for-workers). They edit, test, commit and close their ticket.
- **[Organs](docs/organs.md)** are orchestra's own one-shot advisers: triage of set-aside tickets, the predictor of a ticket's files, the reviewer that writes the run report, the screen and plan that turn a feature request into tickets, and the scout that finds a project's test suites for `orchestra init`. Each is a single `claude -p` call with no tools, except the scout, which has read-only ones (Read, Glob and Grep) to read the repository; they advise and change nothing.

## Install

```
curl -fsSL https://raw.githubusercontent.com/noesis-sol/orchestra/main/scripts/install.sh | sh
```

[The script](scripts/install.sh) installs the latest signed release for darwin or linux, amd64 or arm64, in `/usr/local/bin` or `~/.local/bin`. Or with Go: `go install github.com/noesis-sol/orchestra/cmd/orchestra@latest`.

`orchestra update` updates it (`sudo orchestra update` where its folder is root's), and `orchestra --version` says which you have. orchestra needs `herdr`, `tmux` (`brew install tmux`, `apt install tmux`) or [cmux](https://cmux.com) (macOS) for the workers, `git`, `claude` and `bd` (which `orchestra init` can install).

[docs/install.md](docs/install.md) has the rest: what the script checks and its variables, building from a clone, how `update` verifies a release, build provenance, the newer-release notice and reading a clone's version.

## Set up a project

In the project's repository:

```
orchestra init
```

`init` offers to install Beads if it's missing and runs `bd init` where the repository has no `.beads/`. It writes `.orchestra/` (the worker prompt, `settings.json` and a `.gitignore`) and the project's two checks: `scripts/check-fast.sh`, run on each rebased ticket before it merges, and `scripts/check-full.sh`, run once at the end of a run. It asks in two steps, **Workers** then **Checks**, and writes nothing until the last one is submitted. Commit `.orchestra/` and `scripts/` afterwards: a ticket's worktree has only what is committed.

**Checks** starts with the scout, an organ that looks through the repository for its test suites, lints, type checks and builds. You then use them as they are, ticking which run on every merge and which at the end of a run; create them from scratch with the create-check-suite skill; or give the one command the merge check runs. It can also file tickets for the areas without tests, and offers a **verifier**: a subagent that checks each ticket's change against the ticket before its worker closes it.

Most of it can change later: `settings.json` is plain JSON you can edit, `init` run again keeps what is there unless its flags change it, the checks are ordinary scripts, one line per suite, and a run's flags (`-c`, `--check-timeout`, …) override the settings for that run.

[docs/setup.md](docs/setup.md) has the rest: every question, the flags that answer them without asking, the time limits and the setup command, and what a check prints. [Running](docs/running.md) says what happens when a ticket's check fails ([Several tickets at once](docs/running.md#several-tickets-at-once)) and when the full check does ([The full check](docs/running.md#the-full-check)).

## Run

From the main checkout, on the branch finished tickets should land on, in a Herdr pane, a cmux terminal or, with tmux, any terminal:

```
git switch -c batch/$(date +%F)
orchestra
```

Workers run in the terminal orchestra runs in, the innermost when one runs in another, or in tmux when it runs in none of them:

| Terminal | Chosen by default | Each worker gets | To watch the workers |
|---|---|---|---|
| [Herdr](https://herdr.dev) | in a Herdr pane | a tab in orchestra's workspace | Herdr's tabs |
| [tmux](https://github.com/tmux/tmux) | in tmux, or in any other terminal when tmux is installed | a window: in orchestra's session when it runs in tmux, otherwise in a detached session named when the run starts | `tmux attach -t orchestra-<repository folder>`, as the run says |
| [cmux](https://cmux.com) (macOS) | in a cmux terminal | a workspace in orchestra's window, listed in the sidebar | cmux's sidebar |

Under tmux and cmux, workers are Claude Code: its hooks say what each one is doing. `--terminal herdr|tmux|cmux` (or `"terminal"` in `.orchestra/settings.json`) chooses instead; see [Herdr, tmux or cmux](docs/running.md#herdr-tmux-or-cmux).

In a terminal, `orchestra` first asks what the run should work on. **Current tickets** (the default) runs the backlog as below; it reads `Current tickets: N ready`, or with none ready `Current tickets: none, all done` or `Current tickets: none ready (N open)`. **New feature** asks you to describe a feature in a few lines, then opens Claude Code in a pane beside orchestra's, which interviews you about it until you share an understanding, proposes an epic and its tickets, and files them once you agree. Once the tickets are filed and Claude has said so, orchestra closes that pane by itself, shows the tickets and runs them once you confirm; `/exit` there, or closing the pane, ends an interview that files nothing. Ctrl+C in orchestra's pane stops the interview. When orchestra has no pane to split (tmux run from outside tmux), or can't open one, Claude Code gets orchestra's terminal instead. Esc or Ctrl+C at the question exits with nothing changed. `--tickets` skips the question and runs the current tickets, as scripts and agents should; `--ticket`, `--feature` and `-plain` don't ask either. `--feature "<request>"` plans a feature with the organs instead, without the interview.

How a run goes:
- **Dispatch.** Each ready ticket in `bd ready` goes to its own worker, up to `concurrent` at a time. Tickets that touch the same files or functions don't run side by side, and a ticket labelled `solo` runs alone.
- **Merge.** A finished ticket is rebased onto the branch and the check runs again before it merges. A conflict goes back to its worker to resolve. A ticket that still can't merge is set aside for review.
- **Questions.** A worker that needs your decision files a question (a ticket labelled `human`), and the run goes on. Answer it with `bd human respond <question> --response "…"`.
- **Nothing to run.** A run with nothing ready (or only tickets it would hold back, such as a parent waiting for its subtickets), and no worker the last run left to carry on with, shows `✓ All done` or what holds the tickets left (questions for you, tickets waiting on others, in progress, deferred, closed but not merged), and exits 0 without opening the dashboard.
- **Ending.** The run ends when nothing is left to start. Something that needs you (a worker blocked or idle with its ticket open, a tool failing) stops new tickets, lets the running ones finish, and ends the run. The next run carries on with any workers it left behind.
- **One at a time.** One run at a time works on a repository. Run `orchestra init` first; `orchestra` says so if you haven't.

| Option | |
|---|---|
| `-c N` (`--concurrent`) | tickets at the same time, for this run |
| `--tickets` | run the current tickets without asking first |
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
│ Completed    │ Set aside    │ Needs you    │ Workers     │ In queue    │
│ ✓ 2          │ ↷ 1 ◆ 1      │ 0            │ 2 of 3      │ 11 of 16    │
╰──────────────┴──────────────┴──────────────┴─────────────┴─────────────╯
  Tickets
╭────────────┬─────────────┬─────────────────────────────────────────────╮
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

A finished ticket that can't merge until you act reads `■ blocked`, with why (`main checkout has uncommitted changes`, `merge conflict in <files>`), and counts under **Needs you** with the questions waiting for your answer; `■ stopped` is a worker still running when the run stopped.

The dashboard's keys:
- **1**–**9** switch to that worker's tab (its workspace in cmux; in tmux, while its windows are in orchestra's session).
- **s** asks whether to stop after the running tickets.
- **Ctrl+C** stops at once and leaves the workers running; press it again to quit without waiting.

Everything is also logged to `.orchestra/orchestra.log`. Scripts and agents should read the [event stream](docs/events.md) instead. See [docs/dashboard.md](docs/dashboard.md) for the rest, including the notifications.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | done: nothing left to start, nothing to run from the start (`✓ All done` or `Nothing ready to run`), the limit reached, or stopped after the running tickets as asked |
| 2 | a setup problem (all are listed), or another run going in the repository |
| 3 | a worker needs you: blocked, idle with its ticket in progress, or past the ticket limit |
| 4 | a terminal (Herdr, tmux or cmux), Beads or git failure |
| 5 | uncommitted changes in the main checkout, or it left its branch |
| 6 | a finished branch didn't fast-forward |
| 7 | workers kept failing at once: the machine, not the tickets |
| 130 | stopped with Ctrl+C, SIGTERM or SIGHUP |

Each code in full, and what to do after one, is in [docs/running.md](docs/running.md#exit-codes).

## Running orchestra through an agent

[`skills/orchestra/SKILL.md`](skills/orchestra/SKILL.md) is a skill that lets a coding agent such as Claude Code set a project up, launch a run beside its own pane, and read how it went. Copy the folder to `~/.claude/skills/orchestra/` (or `.claude/skills/orchestra/` in one project), then ask the agent to "run orchestra".

[`skills/create-check-suite/SKILL.md`](skills/create-check-suite/SKILL.md) is a skill for a worker in a project with no tests, or areas without them: it sets up deterministic unit and end-to-end suites in the project's own frameworks (go test, pytest, Mocha, Playwright…), adds them to `scripts/check-fast.sh` and `scripts/check-full.sh`, gets them passing, maps the features in `FEATURES.md` and files a ticket for each untested area. Install it the same way.

## Documentation

- [docs/install.md](docs/install.md): the install script, building from a clone, `orchestra update`, build provenance, versions
- [docs/dashboard.md](docs/dashboard.md): what you see, the keys, the closing lines, notifications
- [docs/setup.md](docs/setup.md): everything `orchestra init` does
- [docs/running.md](docs/running.md): Herdr, tmux or cmux, options, scoped and feature runs, several tickets at once, the full check, stops and exit codes
- [docs/workers.md](docs/workers.md): the worker prompt, MCP servers for workers, hooks and run files, workers in tmux and cmux
- [docs/organs.md](docs/organs.md): triage, the predictor, the reviewer, the screen, the plan and the scout
- [docs/events.md](docs/events.md): the event stream for scripts and agents
- [docs/development.md](docs/development.md): the check, the code's layout and tests, differences from `orchestrate.sh`
- [Changelog](CHANGELOG.md)
- [Third-party notices](THIRD_PARTY_NOTICES.md)
