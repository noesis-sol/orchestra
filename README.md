# Agent Orchestra

<img width="2172" height="724" alt="661180647-c6cb72f8-21a8-4805-8bff-76a43d4db1e6" src="https://github.com/user-attachments/assets/f2e76a42-7112-4909-87df-6dc154679b2f" />


Works through a [Beads](https://github.com/gastownhall/beads) backlog one ticket at a time. Each ticket goes to a coding agent in its own [Herdr](https://herdr.dev) tab and git worktree, and finished tickets are merged into the branch you started on. A Go rewrite of `orchestrate.sh`, with the same environment variables, log file and exit codes, and a live terminal view built with Bubble Tea.

## What you see

One dashboard, updated in place: nothing is printed above it while the loop runs.

```
 Orchestra  v0.2.0   batch/2026-09-28 · 18m40s
╭──────────────┬──────────────┬──────────────┬─────────────┬─────────────╮
│ Completed    │ Deferred     │ Needs you    │ Workers     │ In queue    │
│ ✓ 2          │ ↷ 1 ◆ 1      │ 0            │ 1 of 3      │ 12          │
╰──────────────┴──────────────┴──────────────┴─────────────┴─────────────╯
╭────────────┬─────────────┬─────────────────────────────────────────────╮
│ Tickets    │             │                                             │
├────────────┼─────────────┼─────────────────────────────────────────────┤
│ ✓ done     │ kinieta-dwv │ ffd6ce4 merged into batch/2026-09-28        │
│ ↷ deferred │ kinieta-vzg │ ◆ environment · high · prompt never submit… │
│ ✓ done     │ kinieta-y6j │ 6097367 merged into batch/2026-09-28        │
│ ▶ working  │ kinieta-kco │ Open the property model: Interpolatable p…  │
╰────────────┴─────────────┴─────────────────────────────────────────────╯
╭────────────────────────────────────────────────────────────────────────╮
│ ⣾  kinieta-kco  working  4m52s                                         │
│   Open the property model: Interpolatable protocol and custom key-path │
│   / constraint-constant properties                                     │
│   ⏺ Bash(scripts/ci-local.sh lint ios)                                 │
╰────────────────────────────────────────────────────────────────────────╯
  s stops after current · ctrl+c stops now
```

- **Totals** for the run, as one strip: completed, deferred (and how many triaged), questions for you, workers running out of how many may, and how many are still ready. The branch and how long the run has gone are on the title line.
- **Tickets**: one row per ticket, updated as it moves. A **picked-up** ticket (cyan) shows its title. A **completed** one (green) shows only the merged commit. A **deferred** one (yellow) shows why, replaced by the triage organ's verdict (purple `◆`) once it's in. A ticket that stopped the run is red. The table shows the most recent tickets that fit in the pane.
- **Active ticket**: the worker's status, elapsed time, the ticket title and the worker's latest action. The border is cyan while the worker runs, red when it's blocked, and grey between tickets. It updates every 3 seconds.

- **Keys**: **s** asks, in a box over the dashboard (a line above the hint in a small pane), whether to stop after the running tickets: `Stop after the running tickets? No new tickets will start. 2 running (kinieta-kco, kinieta-y6j) will finish and merge, then the run ends.` **y** confirms, **n** or **Esc** closes it. The run then starts no new ticket, from any path; the running ones carry on as usual, merges, rebases and re-checks included, and when the last one returns the run ends normally with `DRAINED after 12 tickets` (exit code 0), triage and the report. The log says `DRAIN: stopping after the 2 running tickets (…), asked from the dashboard`, and the report's first sentence mentions it. With nothing running, it ends at once. Meanwhile the title line shows `· stopping after current`, and **s** offers `Keep taking tickets?` to take it back (logged as `DRAIN cancelled`). **Ctrl+C** stops at once at any time, the question open or not, leaving the workers running. Outside the dashboard (`-plain`, scripts), `kill -USR1 <pid>` asks the same without a question.

When the loop stops, the dashboard stays on screen as the run's summary, followed by the final line and the run report (see Organs).

Everything is also appended to `.orchestra/orchestra.log` in plain text, so `tail -f` works too. When output isn't a terminal, or with `-plain`, it prints those log lines instead of the live view.

## Organs

Organs are LLM-powered steps. The orchestrator gathers the evidence itself and passes it to `claude -p` with every built-in tool and MCP server disabled (`--tools "" --strict-mcp-config`), from outside the project. An organ can only read what it's given and answer. Organs advise: the orchestrator writes their output down, and no organ changes a ticket's status. A call is about 1,500 input tokens and takes 5–15 seconds.

- **Triage**, for each deferred ticket. The evidence is the ticket (`bd show`), the end of the worker's terminal, and its worktree's changes and commits. The model decides whether the cause lies in the **environment** (the machine, tools or services), the **instructions** (the worker prompt or the ticket's wording), or the **problem** itself. It adds a recommendation to the ticket's notes, and a purple `◆` line appears in the terminal. Triage runs in the background, one ticket at a time, so the loop doesn't wait.
- **Reviewer**, when the loop stops for any reason. It waits for pending triage, then reads the run's log lines, the commits merged during the run, the tickets set aside (with their triage notes) and the ticket that was running. It writes a short report in three sections: **Finished**, **Set aside** and **Needs you**. The report is shown in the terminal, rendered with Glamour, and saved to `.orchestra/reports/<start time>.md`. Pressing Ctrl+C while it's writing skips it. A run stopped by SIGTERM or SIGHUP (closing its terminal or Herdr pane) skips the reviewer.

Turn them off with `-triage=false` / `TRIAGE=0` and `-review=false` / `REVIEW=0`, and pick their model with `-organ-model` / `ORGAN_MODEL` (default: the `claude` CLI's). If `claude` isn't installed, organs switch off and the log says so.

## Install

```
go install github.com/noesis-sol/orchestra/cmd/orchestra@latest
```

That puts `orchestra` in `$(go env GOPATH)/bin`, which must be on your `PATH`. From a clone, `go build -o /usr/local/bin/orchestra ./cmd/orchestra` works too. `orchestra -version` shows which version you have. Requires Go 1.26 (fetched automatically by the Go toolchain if yours is older), plus `bd`, `herdr`, `git` and, for the organs, `claude`.

## Set up a project

In the project's repository:

```
orchestra init --check "scripts/ci-local.sh"
```

This creates `.orchestra/`, where everything `orchestra` owns in a project lives:

```
.orchestra/worker-prompt.md   committed: the worker prompt (from the built-in template)
.orchestra/settings.json      committed: the check command and its time limit, how many tickets run at once, an optional ticket limit, the issue types never dispatched, whether tickets are kept apart by footprint, and when failing workers hold the run
.orchestra/.gitignore         committed: ignores the three below
.orchestra/orchestra.log      the event log
.orchestra/reports/           run reports
.orchestra/run/               per-ticket files in each worktree (the launch prompt, the worker's hooks and what they report)
```

In a terminal, `init` asks with a short form: the **check command** (lint, build and tests), pre-filled from the settings or from an existing prompt, its **time limit** (30m offered), and how many **tickets to run at the same time** by default. Where the project keeps a `CHANGELOG.md` that `.gitattributes` doesn't merge by union yet, it also offers to add `CHANGELOG.md merge=union` there, so tickets that each add an entry at the same spot don't conflict (see [Several tickets at once](#several-tickets-at-once)). `--check`, `--check-timeout 5m`, `--concurrent N` (or `-c N`) and `--changelog-union` (or `--changelog-union=false`) answer those without asking, which is what an agent or a script should use. Without a terminal it asks nothing, uses 1 at a time, and leaves `.gitattributes` alone. The check command goes into the prompt template and `settings.json`. `init` then shows each step, whether `bd`, Beads, `herdr` and `claude` are there, and a **Next** box with only what's left, ending with the command to start a run. `init` also checks for `bd`, `.beads`, `herdr` and `claude`, and says what's missing. It never replaces an existing prompt unless you pass `--force`, and keeps existing settings unless its flags change them. In a project set up by an earlier version, it moves `.claude/worker-prompt.md` into `.orchestra/` (staged with `git mv`); the old `.claude/orchestrate.log` and reports stay where they are, as history. Commit `.orchestra/` (and `.gitattributes`, if it changed) afterwards.

## Run

From the main checkout (not a worktree), inside a Herdr pane, on the branch finished tickets should land on:

```
git switch -c batch/$(date +%F)
orchestra
```

`--concurrent N` (or `-c N`, or `ORCHESTRA_CONCURRENT=N`) sets how many tickets run at the same time for this run, overriding `settings.json`; see [Several tickets at once](#several-tickets-at-once). Worker tabs open in the Herdr workspace `orchestra` runs in; `--workspace ID` puts them in another, for example a separate space for workers. `orchestra -h` lists the flags. Most default to the environment variable `orchestrate.sh` used: `LIMIT` (40), `DONE_SO_FAR`, `AGENT_KIND` (claude), `WORKER_PROMPT` (`.orchestra/worker-prompt.md`), `NOTIFY`, and `WT_ROOT` (`<repo>-worktrees`); a relative `WORKER_PROMPT` or `WT_ROOT`, or their flags, is relative to the repository. The organs add `TRIAGE`, `REVIEW` and `ORGAN_MODEL`, and `PROMPT_AT_LAUNCH` controls how workers get their prompt.

`--ticket-limit 2h` (or `TICKET_LIMIT=2h`, or `"ticket_limit": "2h"` in `settings.json`) stops the run, like `PAUSED`, when a worker is still going that long after its ticket was dispatched: a hung command or a stuck agent would otherwise hold its slot for good. The ticket gets a note, and its tab and worktree are left open. `0` turns a limit from `settings.json` off for a run. Without a limit, a worker still going after 2 hours is logged and notified once (`LONG_RUNNING`), and the run keeps waiting. Whatever the limit, a worker whose status Herdr can't tell (`unknown`) for 5 minutes stops the run (`UNKNOWN >5min`), as one blocked on a dialog for 4 does. A project not yet set up with `orchestra init` keeps working from `.claude/worker-prompt.md`, `.claude/orchestrate.log` and `.claude/orchestrate-reports/`.

Epics are never dispatched: their children are the work. `"exclude_types"` in `settings.json` lists the issue types a run leaves out of `bd ready`, for a project with other container or non-work types, for example `["epic", "decision", "milestone"]`; without it, only `epic` is left out, and `[]` dispatches every type.

## Several tickets at once

With `concurrent` above 1, up to that many workers run side by side, each in its own worktree and tab. What keeps it safe:

- **No ticket runs twice.** A ticket that's been handed out isn't picked again, even before its worker claims it.
- **Free slots don't wait.** While workers run, `bd ready` is read every 30 seconds: a ticket that becomes ready meanwhile (an answered question, a worker's follow-up, a closed blocker) takes a free slot then, and the dashboard's **In queue** follows.
- **Merges queue.** A finished ticket waits for its turn. If other tickets merged while it ran, its branch is rebased onto the current one, and the check command from `settings.json` runs again on the rebased code before it merges. A conflict (`MERGE_CONFLICT`) or a failing check (`CHECKS_FAILED`) leaves that ticket for review, and the run goes on. Tickets it blocks wait until it merges, in later runs too: it is labelled `unmerged` until then. Without a check command, a rebased ticket merges unchecked, and the log says so. The queue waits on the check, so it is stopped (with everything it started) after 30 minutes, or `"check_timeout"` from `settings.json` (`"5m"`, `"45m"`; `--check-timeout` or `ORCHESTRA_CHECK_TIMEOUT` for one run): the ticket is set aside with `CHECKS_FAILED: … did not finish within 5m`, and the next finished ticket merges. Set it to a few times the check's usual running time.
- **Some tickets run alone.** A ticket labelled `solo` (`bd label add <id> solo`), such as one splitting a file every other ticket touches, would conflict with whatever runs beside it. It starts only when no other ticket is running, and nothing new starts while it runs: free slots wait, and the log says once `waiting for solo ticket <id> to finish`. When it is next in priority while others run, nothing new starts behind it, so it isn't starved; the log says `solo ticket <id> is next`, and it starts as soon as the running ones finish. The dashboard's title line shows `solo <id> next` or `solo <id> running`. Holds and questions work as for any ticket, and with one ticket at a time the label changes nothing.
- **Tickets that touch the same code don't run together.** Each ticket has a **footprint**: the files and functions its title, description, design, acceptance criteria and notes name (`internal/dispatch/merge.go:120`, `run.go`, `Loop.merge`, `refreshBranch()`, a camel-case name in backticks such as `waitSettled`), its `area:<name>` labels, and the files its `files` metadata lists (`bd update <id> --set-metadata files=a.go,b.go`). Names are checked against `git ls-files`: a bare file name means every file of that name, and a path that isn't there counts only in a folder that is (a file the ticket adds). Two tickets overlap when they share an area label, a function (when both name functions), or else a file. A running ticket's footprint also grows with every file its worker edits (Claude workers report each Edit and Write through their hooks, in `.orchestra/run/edits`), compared file by file. A free slot goes to the highest-priority ready ticket that overlaps no running ticket; one that does is skipped, not waited for, and the log says once `skipping <id>: touches Loop.merge, like running <other>`. If every ready ticket overlaps, the slot stays empty until something finishes. A ticket naming nothing runs beside anything, as before; a `solo` ticket waits for every running ticket anyway. The dispatch log shows each ticket's footprint (`<id> footprint: …`). When two running workers edit the same file, the log and a notification say once `LIKELY_CONFLICT: <a> and <b> both edit <file>`. `"footprint": false` in `settings.json` turns all of this off.
- **Planning orders tickets that touch the same code.** Keeping overlapping tickets apart at dispatch still lets them run in either order; `orchestra plan` orders them ahead of time. It reads the open tickets (leaving out questions and the `exclude_types`), computes each one's footprint, and proposes a blocks link between two that name the same function, or, when either names no function, the same file of at most 200 lines (in a larger file they likely work in different places; a file a ticket adds counts as small). The higher-priority ticket goes first, then the older one. Area labels alone link nothing, and tickets already ordered by blocks links, directly or through others, are left alone; tickets touching one function form a chain rather than each waiting for every other. It prints each link (`k-2 (P3) waits for k-1 (P1): both touch Loop.merge`) and changes nothing; `orchestra plan --apply` adds them with `bd dep add`. Run it after filing a batch of tickets, before a run: `bd ready` then holds each second ticket until the first closes, and `orchestra` until it merges.
- **One git writer at a time.** Worktree creation, rebases, merges and cleanup in the main repository take a lock, so workers don't trip over git's lock files.
- **A failing machine holds the run.** Sometimes the environment fails every worker the same way, whichever ticket it has: a safety classifier that is down refuses every command, say, and each worker gives up at once. Setting each ticket aside in turn would burn through the queue, so the run holds for the environment instead when 2 tickets in a row either had workers that settled within 2 minutes of dispatch without claiming the ticket, committing or leaving changes, or were blamed on the environment with high confidence by [triage](#organs). The log and a notification say once `ENVIRONMENT: the last 2 tickets (<a>, <b>) each settled within 2m of starting without being claimed or changed; check the machine, then restart`: no new tickets start, the running ones finish, and the run ends with exit code 7. Tickets whose workers failed at once did nothing, so they are reopened rather than left deferred, with their notes kept; tickets triage blamed, whose workers had started on them, stay deferred with the verdict in their notes. One such failure alone changes nothing. `"environment_hold": {"count": 3, "window": "90s"}` in `settings.json` changes the thresholds, and `{"count": 0}` turns it off.
- **Stopping drains.** Something that stops the run (`PAUSED`, `BLOCKED`, a tool failure) is logged as `HOLD`. No new tickets start, the running ones finish and merge, and then the run ends with that reason. With one ticket at a time, nothing changes. Ctrl+C still stops at once. To wind a run down yourself, press **s** in the dashboard (or send SIGUSR1): see [What you see](#what-you-see).

What `orchestra` can't make safe for you:

- **Checks that collide.** Every worker runs the project's checks. Tests that share one named simulator, a port or a database can fail when two run at once.
- **Files every ticket touches.** If most tickets add to the same spot, like a CHANGELOG's `[Unreleased]` list, the end of a test file or the end of a struct, rebases conflict: each side only added lines, but git can't order them. The worker prompt asks workers to put new tests in a new file named after the feature and new fields and helpers next to the code they belong to. For the changelog, `orchestra init` offers a `.gitattributes` line, `CHANGELOG.md merge=union`, which keeps both sides' lines; a line both sides add identically (a `### Fixed` heading, say) is kept once. The same works for other list-like files.

Start at 1, and raise it once the checks run cleanly side by side.

## Worker prompt

Each worker gets the prompt at `-prompt` / `WORKER_PROMPT` (default `.orchestra/worker-prompt.md`), with every `TICKET_ID` replaced by its ticket. `orchestra init` writes it from [`internal/project/worker-prompt.md`](internal/project/worker-prompt.md), which is built into the binary. Each rule prevents a way a run goes wrong:

- **Own worktree, never push, the orchestrator merges.** Workers can't disturb each other or the branch that finished tickets land on.
- **Commit with the ticket ID, closing only when the checks pass.** A ticket is merged only if a commit names it and its worktree is clean.
- **Don't add where every ticket adds.** New tests go in a new file named after the feature, new struct fields, constants and helpers next to the code they belong to, and a changelog entry as new lines (merged by union, above). Two tickets that each append at the end of the same file conflict when the second is rebased, though neither changed the other's lines.
- **Link follow-ups that overlap.** A follow-up that touches the same files or functions as another open ticket is linked to it (`--deps blocked-by:<id>` to wait for it, or `related:<id>`), and one that restructures code most tickets touch is labelled `solo`, so the two don't run side by side.
- **Checks in the foreground.** A worker waiting on a background command looks idle, and an idle worker with its ticket still open stops the run (`PAUSED`).
- **Close, and let the batch PR run CI.** A ticket whose change only CI can verify (a workflow, a platform the local checks don't cover) is closed once the local checks pass, with an "Awaits CI" note. The batch goes to the main branch through a pull request that runs every CI job, so each ticket needn't wait for its own.
- **Ask, don't wait.** A ticket that needs the maintainer's decision gets a question ticket labelled `human` that blocks it. The orchestrator never hands a question to a worker; it shows the ticket as **? for you**, and the run goes on. Answer with `bd human respond <question> --response "…"`, and the ticket returns to the queue with its branch rebased onto the current one.

Claude workers are started with a one-line instruction to read `.orchestra/run/prompt.md` in their worktree, where `orchestra` writes the prompt (kept out of git through the repository's `info/exclude`, so it's ignored even on a branch cut before `.orchestra/.gitignore` was committed). Herdr can't pass line breaks to an agent, and a prompt pasted into the input box can go unsubmitted. `-prompt-at-launch=false` pastes it instead.

Claude workers also start with `--settings .orchestra/run/hooks.json`: hooks, for that worker only, that record each tool it uses in `.orchestra/run/activity.json`. That is how the dashboard tells `testing` (the check command or a test runner), `editing` and `reading` apart from plain `working`, without reading the worker's screen.

Workers of another agent kind (`--agent` / `AGENT_KIND`, any kind Herdr can start) are dispatched, watched and merged the same way, but these features only work with `claude`:
- **Prompt at launch.** Other agents always get the prompt pasted.
- **Recovering a paste whose Enter didn't register.** `orchestra` only recognises Claude Code's `❯` input box, so it pastes the prompt once more instead of pressing Enter, and if the worker still doesn't start, defers the ticket (`PROMPT_FAILED`).
- **The latest action on the dashboard,** read from Claude Code's `⏺` and spinner lines, and `testing`, `editing` or `reading`, from the hooks above.

The [organs](#organs) run `claude` whatever the workers' agent kind.

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
| 0 | nothing left in `bd ready`, the limit was reached, or it stopped after the running tickets as asked (`DRAINED`) |
| 2 | setup problem found before starting (all problems are listed) |
| 3 | a worker stayed blocked for more than 4 minutes or unknown for more than 5, went idle with its ticket still `in_progress`, or was still going after the ticket limit |
| 4 | Herdr, Beads or git failure |
| 5 | uncommitted changes in the main checkout, or it left the branch it started on |
| 6 | a finished ticket's branch does not fast-forward (it should have been rebased first) |
| 7 | workers kept failing at once, whichever ticket they had: the environment, not the tickets (`ENVIRONMENT`) |
| 130 | stopped with Ctrl+C, SIGTERM or SIGHUP; the running worker keeps its tab and worktree |

After a 3, answer the worker in its tab, then resume with `DONE_SO_FAR=<n>`.

## Differences from orchestrate.sh

- `python3` is no longer needed.
- If `herdr agent start` reports a failure but the agent came up anyway, the orchestrator uses it instead of retrying into an occupied pane, which ends in `START_FAILED`.
- The dispatch log line includes the ticket title: `[1/40] kinieta-2e7 dispatching: <title>`.

## Development

```
go test ./...
go vet ./...
staticcheck ./...
```

`staticcheck` installs with `go install honnef.co/go/tools/cmd/staticcheck@latest`.

`TestLiveOrgans` calls the real `claude` against a real repository without writing anything. Its comment shows how to run it.

The run loop, `internal/dispatch`, has one file per concern, its tests in the `_test.go` file of the same name:

| File | What's in it |
| --- | --- |
| `loop.go` | the `Loop` type, `Config`, exit codes, timings and shared helpers |
| `run.go` | `Run`: picking the next ticket, solo tickets, HOLD, interrupts (tests in `run_test.go` and `schedule_test.go`) |
| `start.go` | a ticket's worktree, starting, adopting and naming its worker, delivering its prompt |
| `work.go` | one ticket from start to outcome: asked, deferred, paused or closed |
| `settle.go` | waiting for a worker to settle, reading its status, the dashboard watcher |
| `merge.go` | merging a closed ticket: rebase, check command, fast-forward, cleanup |
| `holds.go` | tickets held for an unmerged blocker, the `unmerged` label, tickets set aside, deferred or waiting on a question |
| `footprint.go` | tickets' footprints (the files and functions they name, and the files their workers edit), skipping a ticket that overlaps a running one, warning when two workers edit one file |
| `plan.go` | `orchestra plan`'s proposal: blocks links between open tickets whose footprints overlap |
| `events.go` | the log file, notifications, events and status sent to the dashboard |
| `drain.go` | stopping after the running tickets when asked (s in the dashboard, SIGUSR1), and taking that back |
| `environment.go` | holding the run when workers keep failing at once or triage keeps blaming the environment, reopening the tickets that did nothing |
| `advice.go` | triage and the run review |
| `deps.go` | the interfaces to Beads, Herdr, git and workers' reports |

The fakes the tests share are in `fakes_test.go` (Beads, workers, sinks), `fakeherdr_test.go` and `loop_test.go`; the harness that runs a whole loop against them is in `helpers_test.go`.

[Changelog](CHANGELOG.md)
