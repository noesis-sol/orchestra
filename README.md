# Claude Agent Orchestra

<p align="center">
<img width="1036" height="380" alt="AI Conductor and Orbital Network (web)" src="https://github.com/user-attachments/assets/3dae50d5-103b-468e-b0c2-44e2b214a5b9" />
</p>

Works through a [Beads](https://github.com/gastownhall/beads) backlog with several workers side by side, as many at once as the project allows. Each ticket goes to a worker in its own tab, in [Herdr](https://herdr.dev), [tmux](https://github.com/tmux/tmux) or [cmux](https://cmux.com), and git worktree, and finished tickets are rebased, checked and merged, one at a time, into the branch you started on. A live dashboard, built with Bubble Tea, shows the run.

<img width="3126" height="1950" alt="Screenshot 2026-10-15 at 12 57 51 PM" src="https://github.com/user-attachments/assets/5ad67440-3f80-42d6-a8fa-34dbd91d905b" />

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

`init` offers to install Beads if it's missing and runs `bd init` where the repository has no `.beads/`. It writes `.orchestra/` (the worker prompt, `settings.json` and a `.gitignore`) and the project's two checks: `scripts/check-fast.sh`, run on each ticket before it merges, and `scripts/check-full.sh`, run once at the end of a run. It asks in two steps, **Workers** then **Checks**, and writes nothing until the last one is submitted. Commit `.orchestra/` and `scripts/` afterwards: a ticket's worktree has only what is committed.

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

In a terminal, `orchestra` first asks what the run should work on. **Current tickets** (the default) runs the backlog as below; it reads `Current tickets: N ready`, or with none ready `Current tickets: none, all done` or `Current tickets: none ready (N open)`. **Spec** asks for a spec file (Tab completes the path, Ctrl+O browses for it) and whether to refine it with Claude first, then builds what it describes, as `--spec` does ([below](#a-whole-app-from-a-spec)). **New feature** asks you to describe a feature in a few lines, then opens Claude Code in a pane beside orchestra's, which interviews you about it until you share an understanding, sums up the epic and tickets it will file, and files them once you agree, as `--spec --refine` breaks a spec down. Once the tickets are filed and Claude has said so, orchestra closes that pane by itself, checks what was filed and runs it without asking, as a spec run does: with workers in Claude Code's auto permission mode. `/exit` there, or closing the pane, ends a session that files nothing. Ctrl+C in orchestra's pane stops it. When orchestra has no pane to split (tmux run from outside tmux), or can't open one, Claude Code gets orchestra's terminal instead. Esc or Ctrl+C at the question exits with nothing changed. `--tickets` skips the question and runs the current tickets, as scripts and agents should; `--ticket`, `--feature`, `--spec` and `--plain` don't ask either. `--feature "<request>"` talks the request through with you the same way, without the question. A feature always needs you at a terminal: scripts and agents write the request to a file and run `orchestra --spec <file>`.

How a run goes:
- **Dispatch.** Each ready ticket in `bd ready` goes to its own worker, up to `concurrent` at a time. Tickets that touch the same files or functions don't run side by side, and a ticket labelled `solo` runs alone.
- **Merge.** A finished ticket is rebased onto the branch if it moved on, and the check runs on every ticket before it merges. A conflict goes back to its worker to resolve. A ticket that still can't merge is set aside for review.
- **Set aside.** A ticket whose worker stops without closing it is deferred for review; a worker left in its tab that closes it later is merged then.
- **Questions.** A worker that needs your decision files a question (a ticket labelled `human`), and the run goes on. Answer it in the answer talk, a Claude Code session that offers each question's options: the dashboard opens it beside itself as soon as a question waits, and **a** goes to it; `orchestra answer` opens it on any terminal. On the command line alone, `bd human respond <question> --response "…"` answers it too ([The answer talk](docs/dashboard.md#the-answer-talk)).
- **Nothing to run.** A run with nothing ready (or only tickets it would hold back, such as a parent waiting for its subtickets), and no worker the last run left to carry on with, shows `✓ All done` or what holds the tickets left (questions for you, tickets waiting on others, in progress, deferred, closed but not merged), and exits 0 without opening the dashboard.
- **Ending.** The run ends when nothing is left to start. Something that needs you (a worker blocked or idle with its ticket open, a tool failing) stops new tickets, lets the running ones finish, and ends the run. The next run carries on with any workers it left behind.
- **One at a time.** One run at a time works on a repository. Run `orchestra init` first; `orchestra` says so if you haven't (a spec run sets the repository up itself).
- **The usage limit.** A worker that reaches the account's usage limit holds the run: no new tickets start until the limit resets, then the limited workers are told to carry on. A hold longer than `usage_limit_wait` (6 hours by default) ends the run with exit code 8.

| Option | |
|---|---|
| `-c N` (`--concurrent`) | tickets at the same time, for this run |
| `--tickets` | run the current tickets without asking first |
| `--ticket <id>` | run only this ticket and its subtickets |
| `--overnight` | run the current tickets after a pre-flight checklist that refuses to start on what would stop the run in the night ([Overnight](docs/running.md#overnight)) |
| `--feature "<request>"` | talk a request through with you in Claude Code, which files it as an epic and its tickets, and run them |
| `--spec <file>` | commit a spec, break it down into an epic and its tickets, and run them, unattended; `--refine` breaks it down with you |
| `--ticket-limit 2h` | stop if a worker is still going this long after its ticket started |
| `--worker-effort medium` | Claude workers' effort (`low` … `max`; `high` if unset) |
| `--plain` | log lines instead of the dashboard |
| `LIMIT=40`, `NOTIFY=0`, `TRIAGE=0`, `REVIEW=0` | stop after so many tickets (none by default), macOS notifications, the triage and report organs |

`orchestra -h` lists every flag. Run `orchestra plan` after filing a batch of tickets: it proposes blocks links between tickets that touch the same code, and `--apply` adds them. The details, from footprints and merges to conflict hand-back and carrying workers over, are in [docs/running.md](docs/running.md).

### A whole app from a spec

```
orchestra --spec ~/specs/shop.md
```

`--spec` builds what a spec describes, a feature or a whole app written down in Markdown or plain text, with nobody there. It commits the spec, where workers can read it (a file from outside the repository as a copy in `.orchestra/specs/`), and Claude breaks it down into Beads tickets: a root epic, child epics where the spec has separable parts, and tickets of one worker session each, citing the spec's sections. Where the spec leaves a choice open, it takes the most conservative reading and writes it under Assumptions in the root epic's notes. Then the tickets run as `--ticket <root epic>` would, without asking: with workers in Claude Code's auto permission mode, and the usage limit waited out. `--refine` breaks the spec down with you instead, in Claude Code beside orchestra's pane, asking only what the spec leaves open.

The same command carries on the next night: with the spec unchanged, it runs what is left; changed, it files tickets for the change. It starts from nothing too: `mkdir shop && cd shop && git init && orchestra --spec ~/specs/shop.md` sets the repository up as `orchestra init` would and builds the app. [A whole app from a spec](docs/running.md#a-whole-app-from-a-spec) has the rest, and [Every night](docs/running.md#every-night) a recipe for running it each night from cron or launchd, and what to read in the morning.

## Running orchestra through an agent

[`skills/orchestra/SKILL.md`](skills/orchestra/SKILL.md) is a skill that lets a coding agent such as Claude Code set a project up, launch a run beside its own pane, and read how it went. Copy the folder to `~/.claude/skills/orchestra/` (or `.claude/skills/orchestra/` in one project), then ask the agent to "run orchestra".

[`skills/create-check-suite/SKILL.md`](skills/create-check-suite/SKILL.md) is a skill for a worker in a project with no tests, or areas without them: it sets up deterministic unit and end-to-end suites in the project's own frameworks (go test, pytest, Mocha, Playwright…), adds them to `scripts/check-fast.sh` and `scripts/check-full.sh`, gets them passing, maps the features in `FEATURES.md` and files a ticket for each untested area. Install it the same way.

## Documentation

- [docs/install.md](docs/install.md): the install script, building from a clone, `orchestra update`, build provenance, versions
- [docs/dashboard.md](docs/dashboard.md): what you see, the keys, the closing lines, notifications
- [docs/setup.md](docs/setup.md): everything `orchestra init` does, and the setup a spec run does without it
- [docs/running.md](docs/running.md): Herdr, tmux or cmux, options, scoped and feature runs, spec runs and the nightly recipe, several tickets at once, tickets set aside, the usage limit, the full check, stops and exit codes
- [docs/workers.md](docs/workers.md): the worker prompt, MCP servers for workers, their permission mode, hooks and run files, workers in tmux and cmux
- [docs/organs.md](docs/organs.md): triage, the predictor, the reviewer and the scout
- [docs/events.md](docs/events.md): the event stream for scripts and agents
- [docs/development.md](docs/development.md): the check, the code's layout and tests, differences from `orchestrate.sh`
- [Changelog](CHANGELOG.md)
- [Third-party notices](THIRD_PARTY_NOTICES.md)
