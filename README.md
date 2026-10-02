# Agent Orchestra

<img width="2172" height="724" alt="661180647-c6cb72f8-21a8-4805-8bff-76a43d4db1e6" src="https://github.com/user-attachments/assets/f2e76a42-7112-4909-87df-6dc154679b2f" />


Works through a [Beads](https://github.com/gastownhall/beads) backlog one ticket at a time. Each ticket goes to a worker in its own [Herdr](https://herdr.dev) tab and git worktree, and finished tickets are merged into the branch you started on. A Go rewrite of `orchestrate.sh`, with the same environment variables, log file and exit codes, and a live terminal view built with Bubble Tea.

## Workers and organs

Two kinds of model calls do orchestra's work, and this README keeps their names apart:

- **Workers** are the coding agents that do the tickets: one per ticket, each in its own Herdr tab and git worktree, a full Claude Code session (by default) with its tools and the [MCP servers the project chose](#mcp-servers-for-workers). They edit, test, commit and close their ticket.
- **[Organs](#organs)** are orchestra's own one-shot advisers: triage, the predictor, the reviewer, which writes the run report, the screen, which checks a feature request, and the plan, which turns it into tickets. Each is a single `claude -p` call with no tools and no MCP servers. They answer from the evidence orchestra hands them, in Claude Code's safe mode, so your own `CLAUDE.md`, hooks and skills stay out (see [Organs](#organs)); they change nothing.

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
  Current
╭────────────────────────────────────────────────────────────────────────╮
│ ⣾  kinieta-kco  working  4m52s                                         │
│   Open the property model: Interpolatable protocol and custom key-path │
│   / constraint-constant properties                                     │
│   ⏺ Bash(scripts/ci-local.sh lint ios)                                 │
╰────────────────────────────────────────────────────────────────────────╯
  s stops after current · ctrl+c stops now
```

- **Totals** for the run, as one strip: completed, deferred (and how many triaged), questions for you, workers running out of how many may, and how many are still ready. The branch and how long the run has gone are on the title line.
- **Tickets**: one row per ticket, updated as it moves. A **picked-up** ticket (cyan) shows its title. A **completed** one (green) shows only the merged commit. A **deferred** one (yellow) shows why, replaced by the triage organ's verdict (purple `◆`) once it's in. One left for review (`CHECKS_FAILED`, `MERGE_CONFLICT`, closed without a commit) reads `! review`; the log says why. A ticket that stopped the run is red. The table shows the most recent tickets that fit in the pane.
- **Current**: a box per running ticket, with the worker's status, elapsed time, the ticket title and the worker's latest action (one line per worker, in a single box, when the pane is short). The border is cyan while the worker runs, red when it's blocked, and grey between tickets. It updates every 3 seconds. In the smallest panes the `Current` label is the first thing left out.

- **Keys**: **s** asks, in a box over the dashboard (a line above the hint in a small pane), whether to stop after the running tickets: `Stop after the running tickets? Stopping after the 2 running tickets finish (kinieta-kco, kinieta-y6j): no new tickets will start. They merge as usual, then the run ends.` **y** confirms, **n** or **Esc** closes it. The run then starts no new ticket, from any path; the running ones carry on as usual, merges, rebases and re-checks included, and when the last one returns the run ends normally with `DRAINED after 12 tickets` (exit code 0), triage and the report. The log says `DRAIN: stopping after the 2 running tickets finish (…): no new tickets will start, asked from the dashboard`, and the report's first sentence mentions it. With nothing running, it ends at once. Meanwhile a line above the hint says the same, `■ Stopping after the 2 running tickets finish (…): no new tickets will start`, naming the tickets left as they finish (in a narrow pane it wraps and shortens the IDs only); the totals mark the queue `held`; and the hint reads `s cancels the stop · ctrl+c stops now`: **s** offers `Keep taking tickets?` to take it back (logged as `DRAIN cancelled`). **Ctrl+C** stops at once at any time, the question open or not, leaving the workers running; the run then waits for a merge already under way, and a second Ctrl+C quits without waiting (see [Run](#run)). Outside the dashboard (`-plain`, scripts), `kill -USR1 <pid>` asks the same without a question.

When the loop stops, the dashboard stays on screen as the run's summary, followed by the final line and the run report (see [Organs](#organs)).

Everything is also appended to `.orchestra/orchestra.log` in plain text, so `tail -f` works too. When output isn't a terminal, or with `-plain`, it prints those log lines instead of the live view. Scripts and agents should read the [event stream](#event-stream) instead, whose records don't change with the log's wording.

## Organs

Organs are orchestra's one-shot advisers (see [Workers and organs](#workers-and-organs)). The orchestrator gathers the evidence itself and passes it to `claude -p` with every built-in tool and MCP server disabled (`--tools "" --strict-mcp-config`), from outside the project and in Claude Code's safe mode (`CLAUDE_CODE_SAFE_MODE=1`). An organ has no tool to look further than what it's given, and can only answer. Organs advise: the orchestrator writes their output down, and no organ changes a ticket's status. A call is about 1,500 input tokens and takes 5–15 seconds; the plan's evidence is larger, with the repository's file list, and the call takes longer at its higher effort. Each call has a time limit: 2 minutes for the predictor and the screen, 3 for triage, 5 for the report and 10 for the plan. A call still running then is stopped and fails with `claude: timed out after 10m`; one that answers and exits isn't held up by a process `claude` left running.

Running from outside the project keeps the project's `CLAUDE.md`, settings and hooks out of an organ; safe mode keeps out your own Claude Code setup, which `claude -p` loads from anywhere. Measured on 2026-10-02 with Claude Code 2.1.287 and a subscription login, on a short organ call (a one-line system prompt and a question about the model's context): without safe mode, the model's context held `~/.claude/CLAUDE.md`, a `SessionStart` hook from the user settings and a `Stop` hook from a plugin ran, and auto-memory was on, for 1,043 input tokens. With it, the call took 529 input tokens, no hook ran, auto-memory was off, and the login and the model were the same. What still applies: your default model and other settings, managed (policy) settings, and Claude Code's note of your account's email. Without safe mode, a hook that acts on its environment would act on every organ call: Herdr's Claude integration, for one, installs a `SessionStart` hook that reports a Claude session to the Herdr pane it runs in, which for an organ is orchestra's own. A `claude` too old to know safe mode ignores the variable and loads your setup as before.

- **Triage**, for each deferred ticket. The evidence is the ticket (`bd show`), the end of the worker's terminal, and its worktree's changes and commits. The model decides whether the cause lies in the **environment** (the machine, tools or services), the **instructions** (the worker prompt or the ticket's wording), or the **problem** itself. It adds a recommendation to the ticket's notes, and a purple `◆` line appears in the terminal. Triage runs in the background, one ticket at a time, so the loop doesn't wait. With triage off, no evidence is gathered. When the loop stops, orchestra says `finishing triage…` and waits for the tickets still queued; Ctrl+C then skips them, and the one being triaged, without a warning for each.
- **Predictor**, for each ready ticket that names nothing its [footprint](#several-tickets-at-once) can use. The evidence is the ticket (`bd show`) and the repository's files (`git ls-files`); the model picks the few files the ticket will most likely change. The guess is cached on the ticket as `predicted_files` metadata (apart from your `files`), so it's asked once per ticket, and scheduling uses it from then on. It runs in the background, one ticket at a time, only with several tickets at once and footprints on; a ticket is dispatched without waiting for its prediction.
- **Reviewer**, when the loop stops for any reason. It waits for pending triage, then reads the run's log lines, the commits merged during the run, the tickets set aside (with their triage notes) and the ticket that was running. It writes a short report in three sections: **Finished**, **Set aside** and **Needs you**. The report is shown in the terminal, rendered with Glamour, and saved to `.orchestra/reports/<start time>.md`. Pressing Ctrl+C while it's writing skips it, and pressing it again quits at once; a second Ctrl+C while a stopped run waits for its workers skips it too. A SIGTERM or SIGHUP (closing its terminal or Herdr pane) during the run skips the reviewer.
- **Screen**, for a feature request typed or pasted, before it is planned. The evidence is the request and the first 4,000 bytes of the repository's README, so the model knows what the project is. Its verdict is **ok**, **reject** (malicious, such as a backdoor or sending secrets away, or unfit for a coding agent on this repository) or **unclear** (too vague to plan, such as "make it better"), with a sentence or two of reason for the user. Ambitious, refactoring and exploratory requests are ok. If the screen fails, the request counts as not screened. It is a guard against mistakes and pasted content, not a security boundary: workers run with your own permissions.
- **Plan**, for a feature request the screen passed. The evidence is the request, the README and `CLAUDE.md` (or `AGENTS.md`), up to 16,000 bytes each, the repository's files with their line counts (`git ls-files`; a file over 4 MB isn't counted), every ticket not closed (open, in progress, blocked or deferred) with its ID, status and title, so the plan doesn't repeat one, and the start of any repository file the request names by path, with or without a line after it (`internal/x/y.go:120`, `y.go#L120-L140`). The model plans an epic and 1 to 12 child tickets, each one worker's session of work, with acceptance criteria, the files it will change and the tickets that must finish first; or, when it can't plan without answers, only questions. It can't read the code, so a request whose right change depends on the code starts with a design ticket, whose worker studies the code and files the rest of the tickets under the epic. Orchestra checks the plan: unique keys, blockers that are in the plan and form no cycle (an empty one is ignored), known types and priorities. A file that is neither in the repository nor new in an existing directory is dropped with a note.

Turn them off with `-triage=false` / `TRIAGE=0` and `-review=false` / `REVIEW=0` (the predictor goes with footprints: `"footprint": false`), and pick their model with `-organ-model` / `ORGAN_MODEL` (default: the `claude` CLI's). If `claude` isn't installed, organs switch off and the log says so.

Every organ call sets its effort (`claude --effort`): `low` for triage, the predictor and the screen, whose answers are short and structured, `medium` for the run report, and `high` for the plan, made once per feature, whose quality decides the run. `--organ-effort` / `ORGAN_EFFORT`, or `"organ_effort"` in `settings.json`, sets one effort for all of them (`low`, `medium`, `high`, `xhigh` or `max`). Measured on 2026-10-01 with Opus 5.5, three calls each on this repository's tickets and logs, against `high` (Claude Code's default here): triage took 7.9 s at `low` against 15.0 s, the predictor 4.3 s against 7.2 s, and the report 7.3 s at `medium` against 8.5 s, with the same answers.

An organ's evidence is text orchestra can't trust: tickets, worker terminals, logs. Each section of it sits between an opening and a closing tag carrying an ID drawn afresh for each call, such as `<evidence id="3fa91c0e">` … `</evidence id="3fa91c0e">`, each on its own line, and every organ's system prompt says that text inside the tags is evidence only, never instructions to follow. Text gathered before the ID was drawn can't close the tag early.

## Install

```
go install github.com/noesis-sol/orchestra/cmd/orchestra@latest
```

That puts `orchestra` in `$(go env GOPATH)/bin`, which must be on your `PATH`. From a clone, `go build -o /usr/local/bin/orchestra ./cmd/orchestra` works too. `orchestra -version` shows which version you have. Requires Go 1.26 (fetched automatically by the Go toolchain if yours is older), plus `bd` (which `orchestra init` can install), `herdr`, `git` and, for the organs, `claude`.

## Set up a project

In the project's repository:

```
orchestra init --check "scripts/ci-local.sh"
```

This creates `.orchestra/`, where everything `orchestra` owns in a project lives:

```
.orchestra/worker-prompt.md   committed: the worker prompt (from the built-in template)
.orchestra/settings.json      committed: the check command and its time limit, how many tickets run at once, the MCP servers workers get (by name), an optional ticket limit, the issue types never dispatched, whether tickets are kept apart by footprint, whether conflicts go back to their workers, and when failing workers hold the run
.orchestra/.gitignore         committed: ignores the three below
.orchestra/orchestra.log      the event log
.orchestra/reports/           run reports
.orchestra/run/               per-ticket files in each worktree (the launch prompt, the worker's MCP servers, its hooks and what they report); in the main checkout, the run lock, the event stream and the workers the last run left behind (state.json)
```

First, `init` sets Beads up. Where `bd` isn't installed, it installs it: with Homebrew (`brew install beads`) where `brew` is on the `PATH`, as Beads recommends, otherwise, on macOS, Linux and FreeBSD, with the Beads install script (`curl -fsSL https://raw.githubusercontent.com/gastownhall/beads/main/scripts/install.sh | bash`), which checks what it downloads and falls back to `go install`. On Windows it installs nothing and says how: `irm https://raw.githubusercontent.com/gastownhall/beads/main/install.ps1 | iex` in PowerShell. In a terminal the form asks first, offering yes; `--install-beads` installs without asking and `--install-beads=false` declines. Without a terminal or either flag, `init` installs nothing and reports `bd` missing. The install may take up to 15 minutes, and Ctrl+C stops it. If `bd` lands in a folder that isn't on your `PATH` (the script uses `~/.local/bin` where it can't write to `/usr/local/bin`), `init` says where it is and how to add the folder. Then, in a repository without `.beads/`, `init` runs `bd init --non-interactive --role maintainer --init-if-missing`: no questions, the answers being "not contributing to someone else's repo" and auto-export off. Otherwise `bd init` does what it does when run by hand: it adds `.beads/`, `AGENTS.md`, `CLAUDE.md` and other agents' integration files, and points git's hooks at `.beads/hooks`. bd 1.3.0 also commits what it adds, along with anything already staged, so `init` runs it before staging anything of its own, then lists what `bd` committed and what is left to commit with `.orchestra/`. A failed install or `bd init` shows the command's error, and the **Next** box keeps the fix.

In a terminal, `init` asks with a short form: the **check command** (lint, build and tests), pre-filled from the settings or from an existing prompt, its **time limit** (30m offered), how many **tickets to run at the same time** by default, and which **MCP servers workers get** (see [MCP servers for workers](#mcp-servers-for-workers)). Where the project keeps a `CHANGELOG.md` that `.gitattributes` doesn't merge by union yet, it also offers to add `CHANGELOG.md merge=union` there, so tickets that each add an entry at the same spot don't conflict (see [Several tickets at once](#several-tickets-at-once)). `--check`, `--check-timeout 5m`, `--concurrent N` (or `-c N`), `--mcp a,b` (or `--mcp ""` for none) and `--changelog-union` (or `--changelog-union=false`) answer those without asking, which is what a script, or an agent setting the project up for you, should use. Without a terminal it asks nothing, uses 1 at a time, leaves the MCP servers as they were (unset on a first run), and leaves `.gitattributes` alone. The check command goes into the prompt template and `settings.json`. `init` then shows each step, whether `bd`, Beads, `herdr` and `claude` are there, and a **Next** box with only what's left, ending with the command to start a run. `init` also checks for `bd`, `.beads`, `herdr` and `claude`, and says what's missing. It never replaces an existing prompt unless you pass `--force`, and keeps existing settings unless its flags change them. In a project set up by an earlier version, it moves `.claude/worker-prompt.md` into `.orchestra/` (staged with `git mv`); the old `.claude/orchestrate.log` and reports stay where they are, as history. Commit `.orchestra/` (and `.gitattributes`, if it changed, and whatever `bd init` left uncommitted; the **Next** box names them) afterwards.

## MCP servers for workers

A worker reads ticket text that orchestra can't fully trust: a ticket filed by someone else, a follow-up another worker wrote, text pasted from an issue or a web page. Whatever it reads can steer what it does with its tools, so give it only the MCP servers the project's work needs. Each server a worker doesn't need is something a misled worker could reach, and its tool descriptions cost input tokens in every request: choosing 1 server out of 10 saved about 17,000 per request in one measurement.

`orchestra init` asks which servers workers get, offering every server Claude Code defines for the repository on your machine, with its scope and transport, the current choice ticked (none on a first run); with no server to offer, it chooses none. `--mcp postgres,firecrawl` chooses without asking, and `--mcp ""` chooses none. `settings.json` keeps their names only:

```json
{ "mcp_servers": ["postgres", "firecrawl"] }
```

The definitions stay in Claude Code's own config, because they can hold API keys and tokens and differ from machine to machine. Each machine resolves the names the way Claude Code does, the first scope that defines a name winning:

1. **local**: `~/.claude.json`, under the repository's path (or its main checkout's, from a worktree): `claude mcp add <name> …`;
2. **project**: `.mcp.json` in the repository, committed and shared: `claude mcp add --scope project <name> …`;
3. **user**: `~/.claude.json`, for every repository: `claude mcp add --scope user <name> …`.

With `CLAUDE_CONFIG_DIR` set, `.claude.json` is read from there instead of your home folder.

**claude.ai connectors** (the servers Claude Code lists as `claude.ai …`) belong to your claude.ai account: Claude Code fetches them when it starts, and there is no definition on the machine that orchestra could hand a worker. `init` lists them as not available to workers and doesn't offer them. For a worker to use the same service, define an MCP server for it with `claude mcp add`.

When a run starts, it looks up each chosen name on this machine. A name that isn't defined here, or names a claude.ai connector, is a setup problem (exit code 2) naming the fix: `redis isn't defined on this machine (define it: claude mcp add redis …)`, or `… is a claude.ai connector, which workers can't get`, or choose the servers again with `orchestra init`. A worker is never started without a server its project chose. `init` itself only warns about such a name and saves it anyway, as a teammate may define it.

Each Claude worker then gets the chosen servers' definitions in `.orchestra/run/mcp.json` in its worktree (readable by you only, and out of git) and starts with `--mcp-config <that file> --strict-mcp-config`, so no definition appears on a command line and no other server loads. `"mcp_servers": []` starts workers with `--strict-mcp-config` alone: no MCP servers. The `START` line in the log says what workers get: `MCP servers: postgres, firecrawl`, `MCP servers: none`, or `MCP servers: all, not configured`. If Herdr refuses those arguments, the run stops with `START_FAILED` rather than start the worker with every server.

Without `"mcp_servers"`, workers load every MCP server Claude Code finds on the machine, as before this setting existed, and the run warns once, in the log and on the dashboard: `workers load every MCP server Claude Code finds on this machine; choose theirs with orchestra init`. Workers of another agent kind (`--agent`) don't get the setting; the log says so once. Organs never get any MCP server.

## Run

From the main checkout (not a worktree), inside a Herdr pane, on the branch finished tickets should land on:

```
git switch -c batch/$(date +%F)
orchestra
```

One run at a time works on a repository: a second would race the first for the same tickets, worktrees and branch. Once its startup checks pass, a run holds a lock, `.orchestra/run/orchestra.lock` in the main checkout, until it exits after the run report. A second run in the same repository stops before changing anything, and a `--feature` request before it is screened, with exit code 2: `orchestra is already running in <repo> (pid 44497, since 08:31, main, pane w2B:p60)`. The lock is an `flock`, which the system releases when orchestra exits however it exits, `kill -9` included, so there is never a stale lock to remove. The file says which run holds it, or held it last: its PID, start time, version, branch, `ticket` or `feature` and Herdr `pane`. `lsof .orchestra/run/orchestra.lock` shows whether a run holds it. Runs in different repositories don't block each other. `orchestra plan --apply` warns when a run is going: a ticket the run has started keeps going whatever it now waits for.

`--concurrent N` (or `-c N`, or `ORCHESTRA_CONCURRENT=N`) sets how many tickets run at the same time for this run, overriding `settings.json`; see [Several tickets at once](#several-tickets-at-once). Worker tabs open in the Herdr workspace `orchestra` runs in; `--workspace ID` puts them in another, for example a separate space for workers. `orchestra -h` lists the flags. Most default to the environment variable `orchestrate.sh` used: `LIMIT` (40), `DONE_SO_FAR`, `AGENT_KIND` (claude), `WORKER_PROMPT` (`.orchestra/worker-prompt.md`), `NOTIFY`, and `WT_ROOT` (`<repo>-worktrees`); a relative `WORKER_PROMPT` or `WT_ROOT`, or their flags, is relative to the repository. The organs add `TRIAGE`, `REVIEW`, `ORGAN_MODEL` and `ORGAN_EFFORT`, and `PROMPT_AT_LAUNCH` controls how workers get their prompt.

`--worker-effort medium` (or `WORKER_EFFORT=medium`, or `"worker_effort": "medium"` in `settings.json`) starts Claude workers with `--effort medium`; any of `low`, `medium`, `high`, `xhigh` and `max` will do. Without it they run at Claude Code's own default, and `orchestra init` says so. There is no recommended level yet: it waits on measuring `high` against `medium` on real tickets (time to close, tokens, the check passing first time).

`--ticket-limit 2h` (or `TICKET_LIMIT=2h`, or `"ticket_limit": "2h"` in `settings.json`) stops the run, like `PAUSED`, when a worker is still going that long after its ticket was dispatched: a hung command or a stuck worker would otherwise hold its slot for good. The worker's prompt says how long it has, as agents pace themselves to a stated budget. The ticket gets a note, and its tab and worktree are left open. `0` turns a limit from `settings.json` off for a run. Without a limit, a worker still going after 2 hours is logged and notified once (`LONG_RUNNING`), and the run keeps waiting. Whatever the limit, a worker whose status Herdr can't tell (`unknown`) for 5 minutes stops the run (`UNKNOWN >5min`), as one blocked on a dialog for 4 does. A project not yet set up with `orchestra init` keeps working from `.claude/worker-prompt.md`, `.claude/orchestrate.log` and `.claude/orchestrate-reports/`.

Every `bd`, `git` and `herdr` command orchestra runs has a time limit: 30 seconds for a read or a Herdr call, 2 minutes for a git write (worktree, rebase, merge, branch) or a `bd` update; Herdr's own waits keep their timeouts, with 30 seconds to spare. A command still running then is stopped and fails with `<command>: timed out after 30s`, reported like any failure of it (`STATUS_UNREADABLE`, `HERDR_FAILED`, `GIT_FAILED`, …). Ctrl+C stops the commands in flight too, and the run ends once its workers have returned; a merge already under way, and the notes after it, finish first, so one Ctrl+C never leaves the repository half merged.

Pressing Ctrl+C again meanwhile quits rather than wait, so that a shutdown that hangs can be left without `kill -9`. With nothing under way that Ctrl+C doesn't cut short, orchestra quits at once with exit code 130, logging and printing `INTERRUPTED: quit at once with Ctrl+C, leaving <id> (tab <tab>) running with its tab and worktree open`, without triage or the report. With a merge or a worktree setup under way, the second Ctrl+C says so, `<id>'s merge is under way; press Ctrl+C again to abandon it (the repository may be left half merged)`, and skips triage and the report, as it always did; a third quits, the line adding `abandoned <id>'s merge: git may still finish it, so check git status before starting another run`. The commands under way run in their own process groups, so they carry on after orchestra has gone: a `git` command may still finish, or hold git's lock files for a while, though a new run can take orchestra's own lock at once. The workers left running aren't labelled `unmerged` as they are after a stop, nor saved for the next run to carry on with (see [Exit codes](#exit-codes)). SIGTERM and SIGHUP (closing the terminal or pane) count as Ctrl+C does, so a supervisor's second SIGTERM quits too. During triage and the report, Ctrl+C skips them, and the one after it quits.

Epics are never dispatched: their children are the work. `"exclude_types"` in `settings.json` lists the issue types a run leaves out of `bd ready`, for a project with other container or non-work types, for example `["epic", "decision", "milestone"]`; without it, only `epic` is left out, and `[]` dispatches every type.

Parents run last. A ticket with subtickets (`bd create --parent <id>`) that aren't all closed and merged waits for them, logged once as `<id> waits: its subtickets are not all closed and merged`: its worker couldn't close it before they are, and its work builds on theirs. An epic is never dispatched; once its last subticket merges, the log says `all of <id>'s subtickets are merged; close it with: bd close <id>`. orchestra doesn't close it itself.

### One ticket and its subtickets

```
orchestra --ticket CalendarView-bl0
```

`--ticket <id>` (or `ORCHESTRA_TICKET=<id>`) runs that ticket and its descendants (children, grandchildren, …) and nothing else: the queue is `bd ready --parent <id>` plus the ticket itself, with the usual filters, holds and priority order, each parent after its children. A ticket without subtickets is a run of one. An unknown ticket, a closed one or a question (label `human`) is a setup problem. The START line and the dashboard's title line show the scope (`· ticket <id>`), and so does the run report.

Workers are told to file follow-ups that belong to the work as children of `<id>`, and those join the run; anything filed otherwise waits for a later run, and the log and the run report list it. `--limit`, `--concurrent` and the rest apply as usual. The run ends as any other, with `READY_EMPTY`, `LIMIT_REACHED` or `DRAINED`, followed by whether the scope is finished:

- `SCOPE_DONE: <id> and its 3 subtickets are merged`, or for an epic, `SCOPE_DONE: <id>'s 3 subtickets are merged; close it with: bd close <id>`;
- `SCOPE_OPEN: <id>: 2 of its 5 subtickets not done: <id>.2 (blocked by other-7 outside the scope), <id>.4 (waiting on your answer to <question>)`, naming why each isn't: set aside in this run, deferred, closed but not merged, waiting for its own subtickets, not started.

Both exit with 0.

### A feature from one request

```
orchestra --feature "Add a --json flag to the list command"
```

`--feature "<request>"` takes a request from idea to a finished run. After the usual startup checks (repository, `bd`, Herdr, settings, MCP servers, and a clean main checkout), so that nothing is filed for a run that couldn't start:

1. The [screen organ](#organs) judges the request. A request it rejects or finds unclear stops with its reason (exit code 2), as does a screen that fails: an unscreened request isn't planned.
2. The [plan organ](#organs) plans it as an epic and its tickets. When it needs answers first, orchestra prints its questions and stops (exit code 2); run it again with the answers in the request.
3. orchestra shows the plan: the epic, then each ticket with its type, priority, files and the tickets it waits for (`after:`), and any file dropped when the plan was checked. It asks `File these N tickets and start the run? [y/N]`. `--yes` files it without asking; without a terminal to ask on (input and output both: the plan and the question go to standard output, so `> run.log` counts as none), and without `--yes`, nothing is filed (exit code 2).
4. orchestra files the plan with `bd`: the epic, each ticket as its child with its description, acceptance criteria, type, priority and `files` metadata (which [footprints](#several-tickets-at-once) read), then a `blocks` link for each ticket it waits for. If a `bd` command fails, orchestra stops (exit code 4) and lists what it filed, each ticket by ID and plan key, with the `bd delete … --force` command that removes it and the `orchestra --ticket <epic>` command that carries on with it; when a link fails, it first gives a `bd dep add <blocked> <blocker>` for every link not added, that one included, so the tickets still run in order. It says nothing was filed only when `bd` exited with an error of its own: a `bd` that was stopped, was killed, or gave output orchestra couldn't read may have filed what it was filing, and orchestra says how to check with `bd list`.
5. The run is that of `--ticket <epic>`, with the other flags as given (`-c`, `--plain`, the effort flags, …). The START line and the dashboard's title show `· feature <epic>`, the log names the request, and the run report gets the request as evidence.

Nothing is filed before you confirm the plan, and Ctrl+C stops the checks, the screen, the plan or the question with nothing filed (exit code 130). `--organ-model` and `--organ-effort` apply to both organs. `--feature` with `--ticket` (or `ORCHESTRA_TICKET`), an empty request, and `DONE_SO_FAR` at or above `LIMIT`, which would file a plan and start none of it, are setup problems.

## Several tickets at once

With `concurrent` above 1, up to that many workers run side by side, each in its own worktree and tab. What keeps it safe:

- **No ticket runs twice.** A ticket that's been handed out isn't picked again, even before its worker claims it.
- **Free slots don't wait.** While workers run, `bd ready` is read every 30 seconds: a ticket that becomes ready meanwhile (an answered question, a worker's follow-up, a closed blocker) takes a free slot then, and the dashboard's **In queue** follows.
- **Merges queue.** A finished ticket waits for its turn. If other tickets merged while it ran, its branch is rebased onto the current one, and the check command from `settings.json` runs again on the rebased code before it merges. A conflict (`MERGE_CONFLICT`) or a failing check (`CHECKS_FAILED`) leaves that ticket for review, and the run goes on. Tickets it blocks wait until it merges, in later runs too: it is labelled `unmerged` until then. Without a check command, a rebased ticket merges unchecked, and the log says so. The queue waits on the check, so it is stopped (with everything it started) after 30 minutes, or `"check_timeout"` from `settings.json` (`"5m"`, `"45m"`; `--check-timeout` or `ORCHESTRA_CHECK_TIMEOUT` for one run): the ticket is set aside with `CHECKS_FAILED: … did not finish within 5m`, and the next finished ticket merges. Set it to a few times the check's usual running time.
- **A conflict goes back to its worker first.** When the rebase stops on conflicts, the worker that did the ticket, still idle in its tab with the whole change in mind, is asked to resolve it: the rebase is left stopped in its worktree, and the worker is told which files conflict, to keep the intent of both sides, to run the check command until it passes and to finish the rebase, and to do nothing else. The log says `RESOLVING: wt/<id> conflicts with <base> in <files>; handed back to its worker in tab <tab>`, and the dashboard shows the ticket as `⟳ resolving`. The merge queue isn't held meanwhile: other finished tickets merge. When the worker is done, orchestra checks the result itself rather than taking its word: the rebase is finished, the worktree clean, the branch on top of `<base>` with the ticket's own commits and no others, and the check command passes. Then the ticket queues again and merges (rebased again, and re-checked, if more landed meanwhile; a new conflict goes back to the worker once more, twice at most). If the worker doesn't finish within 20 minutes (`"resolve_timeout"` in `settings.json`), leaves the rebase unfinished, makes a commit of its own or the check fails, its branch is put back as the ticket closed it and the ticket is set aside with `MERGE_CONFLICT` as before, the line and a note on the ticket saying what was tried and why it failed. Without a check command, with the worker gone from its tab or busy, conflicts aren't handed back, and the `MERGE_CONFLICT` line says why. `"resolve_conflicts": false` in `settings.json`, or `--resolve-conflicts=false` for one run, turns it off. Ctrl+C during a hand-back leaves the rebase in progress, and the `INTERRUPTED` line says so: once the worker has finished it, run the check and merge the branch by hand.
- **Some tickets run alone.** A ticket labelled `solo` (`bd label add <id> solo`), such as one splitting a file every other ticket touches, would conflict with whatever runs beside it. It starts only when no other ticket is running, and nothing new starts while it runs: free slots wait, and the log says once `waiting for solo ticket <id> to finish`. When it is next in priority while others run, nothing new starts behind it, so it isn't starved; the log says `solo ticket <id> is next`, and it starts as soon as the running ones finish. The dashboard's title line shows `solo <id> next` or `solo <id> running`. Holds and questions work as for any ticket, and with one ticket at a time the label changes nothing.
- **Tickets that touch the same code don't run together.** Each ticket has a **footprint**: the files and functions its title, description, design, acceptance criteria and notes name (`internal/dispatch/merge.go:120`, `run.go`, `Loop.merge`, `refreshBranch()`, a camel-case name in backticks such as `waitSettled`, but not a Go keyword or builtin such as `func()` or `len(x)`, nor a standard package's function such as `synctest.Wait()`), its `area:<name>` labels, and the files its `files` metadata lists (`bd update <id> --set-metadata files=a.go,b.go`, or a list: `'files=["a.go","b.go"]'`). Names are checked against `git ls-files`: a bare file name means every file of that name, and a path that isn't there counts only in a folder that is (a file the ticket adds). The files the project's check command names (`scripts/check.sh`) are left out, as are predicted ones, unless the `files` metadata lists them: nearly every ticket names the check in "`scripts/check.sh` passes" without changing it. Two tickets overlap when they share an area label, a function (when both name functions), or else a file. A running ticket's footprint also grows with every file its worker edits (Claude workers report each Edit and Write through their hooks, in `.orchestra/run/edits`), compared file by file. A free slot goes to the highest-priority ready ticket that overlaps no running ticket; one that does is skipped, not waited for, and the log says once `skipping <id>: touches Loop.merge, like running <other>`. If every ready ticket overlaps, the slot stays empty until something finishes. A ticket naming nothing runs beside anything until the [predictor](#organs) has guessed its files (`<id> footprint predicted: …`), which then count like files it names, marked `(predicted)` in the log; a `solo` ticket waits for every running ticket anyway. The dispatch log shows each ticket's footprint (`<id> footprint: …`). When two running workers edit the same file, the log and a notification say once `LIKELY_CONFLICT: <a> and <b> both edit <file>`. `"footprint": false` in `settings.json` turns all of this off.
- **Planning orders tickets that touch the same code.** Keeping overlapping tickets apart at dispatch still lets them run in either order; `orchestra plan` orders them ahead of time. It reads the open tickets (leaving out questions and the `exclude_types`), computes each one's footprint (leaving out the check command's files, as above), and proposes a blocks link between two that name the same function, or, when either names no function, the same file of at most 200 lines (in a larger file they likely work in different places; a file a ticket adds counts as small). The higher-priority ticket goes first, then the older one. Area labels and predicted files alone link nothing, and tickets already ordered by blocks links, directly or through others, are left alone; tickets touching one function form a chain rather than each waiting for every other. It prints each link (`k-2 (P3) waits for k-1 (P1): both touch Loop.merge`) and changes nothing; `orchestra plan --apply` adds them with `bd dep add`. Run it after filing a batch of tickets, before a run: `bd ready` then holds each second ticket until the first closes, and `orchestra` until it merges.
- **One git writer at a time.** Worktree creation, rebases, merges and cleanup in the main repository take a lock, so workers don't trip over git's lock files.
- **A failing machine holds the run.** Sometimes the environment fails every worker the same way, whichever ticket it has: a safety classifier that is down refuses every command, say, and each worker gives up at once. Setting each ticket aside in turn would burn through the queue, so the run holds for the environment instead when 2 tickets in a row either had workers that went idle for good within 2 minutes of dispatch without claiming the ticket, committing or leaving changes, or were blamed on the environment with high confidence by [triage](#organs). The log and a notification say once `ENVIRONMENT: the last 2 tickets (<a>, <b>) each settled within 2m of starting without being claimed or changed; check the machine, then restart`: no new tickets start, the running ones finish, and the run ends with exit code 7. Tickets whose workers failed at once did nothing, so they are reopened rather than left deferred, with their notes kept; tickets triage blamed, whose workers had started on them, stay deferred with the verdict in their notes. One such failure alone changes nothing. Once the running tickets finish, the run probes the machine: the log says `PROBE: the run holds for the environment; in 10m one worker without a ticket runs a command, …`, and 10 minutes later one worker is started in the main checkout, in a tab of its own, and asked to run a single command; a Claude worker starts with `--strict-mcp-config`, without MCP servers, as it needs none. If it does, the machine works again: its tab is closed, the log and a notification say `PROBE_OK: …; taking tickets again`, and the run goes on with the reopened tickets. If it doesn't within 5 minutes, or stops without running it, the run ends as it would have, the `ENVIRONMENT` line adding how the probe failed, and the probe's tab is left open to read. The machine is probed once per run, so a second hold ends it. Press Ctrl+C to end the run during the wait. `"environment_hold": {"count": 3, "window": "90s", "probe": "30m"}` in `settings.json` changes the thresholds and the wait, `{"probe": "0"}` turns the probe off, and `{"count": 0}` turns the hold off.
- **Stopping drains.** Something that stops the run (`PAUSED`, `BLOCKED`, a tool failure) is logged as `HOLD`. No new tickets start, the running ones finish and merge, and then the run ends with that reason. With one ticket at a time, nothing changes. Ctrl+C still stops at once. A worker left running when the run ends (the one that stopped it, or any at Ctrl+C) may still close its ticket after orchestra has gone: the next run carries it over and merges it then (see below). Its ticket is labelled `unmerged` too (`<id> is left running in tab <tab> and labelled 'unmerged'`), so the tickets it blocks wait in later runs until it merges, even if the next run can't carry it over. To wind a run down yourself, press **s** in the dashboard (or send SIGUSR1): see [What you see](#what-you-see).
- **The next run carries on with the workers a run leaves.** As a run ends, however it ends (done, a stop, Ctrl+C, winding down), it writes the workers it leaves in their tabs to `.orchestra/run/state.json` in the main checkout, while it still holds its lock: those on tickets waiting on a question, and those it left running, with what stopped it (`PAUSED`, `BLOCKED`, `TICKET_LIMIT`, `INTERRUPTED`, …); the log says `left for the next run: <id> (asked <question>), <id> (left running: PAUSED)`. The next run reads the file in its `START` block, `carried over from the last run: …`, and takes each up as it does a ticket asked in the run itself: a ticket closed meanwhile is adopted and merged (rebased and checked if the base moved on, its `unmerged` label removed), and the tickets it blocks start once it has; one whose worker is at work on it again is adopted and watched, rather than stopping the run with `AGENT_BUSY`; one whose question was answered comes back through `bd ready` to its earlier worker, told the answer is in, rather than to a new one that starts over. One still waiting on its question, or left running with its worker idle (waiting for you, as when the run stopped), is left as it is and saved again. One deferred meanwhile shows as deferred. Each is checked first: one whose worktree is gone, or whose ticket is closed with its branch on the base already (merged by hand), is dropped, as the log says; one in progress whose worker has gone from its tab is warned about once (`WORKER_GONE: <id> is in progress after …, but its worker is gone from tab <tab> (worktree <path>)`), noted on the ticket and dropped, and the run goes on. A run scoped with `--ticket` keeps the workers on other tickets for a later run. A file that can't be read carries nothing over, and the log says why. A ticket left for review (`MERGE_CONFLICT`, `CHECKS_FAILED`, …) isn't carried over, nor one whose rebase Ctrl+C left in progress during a hand-back, nor anything after a quit at once; the `unmerged` labels still hold what they block.

What `orchestra` can't make safe for you:

- **Checks that collide.** Every worker runs the project's checks. Tests that share one named simulator, a port or a database can fail when two run at once.
- **Files every ticket touches.** If most tickets add to the same spot, like a CHANGELOG's `[Unreleased]` list, the end of a test file or the end of a struct, rebases conflict: each side only added lines, but git can't order them. The worker prompt asks workers to put new tests in a new file named after the feature and new fields and helpers next to the code they belong to. For the changelog, `orchestra init` offers a `.gitattributes` line, `CHANGELOG.md merge=union`, which keeps both sides' lines; a line both sides add identically (a `### Fixed` heading, say) is kept once. The same works for other list-like files.

Start at 1, and raise it once the checks run cleanly side by side.

## Worker prompt

Each worker gets the prompt at `-prompt` / `WORKER_PROMPT` (default `.orchestra/worker-prompt.md`), with every `TICKET_ID` replaced by its ticket. In a `--ticket` run, orchestra adds a line asking the worker to file follow-ups that belong to the work as children of that ticket, so they join the run (not for the ticket's own worker, which runs last). A worker started on a branch an earlier attempt left work on (a ticket that comes back) is told what it left, commits `<base>` doesn't have (`git log <base>..HEAD`) or uncommitted changes (`git status`), and to build on them or revert them deliberately rather than start over. `orchestra init` writes it from [`internal/project/worker-prompt.md`](internal/project/worker-prompt.md), which is built into the binary. Each rule prevents a way a run goes wrong:

- **Own worktree, never push, the orchestrator merges.** Workers can't disturb each other or the branch that finished tickets land on.
- **Commit with the ticket ID, closing only when the checks pass.** A ticket is merged only if a commit names it and its worktree is clean.
- **Don't add where every ticket adds.** New tests go in a new file named after the feature, new struct fields, constants and helpers next to the code they belong to, and a changelog entry as new lines (merged by union, above). Two tickets that each append at the end of the same file conflict when the second is rebased, though neither changed the other's lines.
- **Link follow-ups that overlap.** A follow-up that touches the same files or functions as another open ticket is linked to it (`--deps blocked-by:<id>` to wait for it, or `related:<id>`), and one that restructures code most tickets touch is labelled `solo`, so the two don't run side by side.
- **Checks in the foreground.** A worker waiting on a background command looks idle, and an idle worker with its ticket still open stops the run (`PAUSED`).
- **Work unattended.** Nobody reads a worker's turns as they happen, so a turn that ends without a tool call ends its work. The prompt asks it not to end a turn with a summary announcing its next step, an offer to go on, or a choice that doesn't block the rest, and keeps the stops it wants to the Close section: DONE, a question, a deferral. For a ticket with several parts it keeps the acceptance criteria as a todo checklist, which the prompt allows despite the Beads instructions. A Claude worker whose turn ends anyway with its ticket still in progress, without a question or a deferral, is told so and to continue, at most twice (`told it to continue (1 of 2)` in the log); after that the 10-minute idle grace and `PAUSED` apply as before.
- **Stop only your own processes, by PID.** Workers run the same test binaries side by side, so a `pkill -f dispatch.test` from one ends another worker's check with `signal: terminated`.
- **Close, and let the batch PR run CI.** A ticket whose change only CI can verify (a workflow, a platform the local checks don't cover) is closed once the local checks pass, with an "Awaits CI" note. The batch goes to the main branch through a pull request that runs every CI job, so each ticket needn't wait for its own.
- **Ask, don't wait.** A ticket that needs the maintainer's decision gets a question ticket labelled `human` that blocks it. The orchestrator never hands a question to a worker; it shows the ticket as **? for you**, and the run goes on. A Claude worker that files its question but leaves its ticket in progress settles at the end of that turn (`A settled: Stop hook at 20:48:39, waiting on Q`), and orchestra reopens the ticket for it; without hooks, that waits out the 10-minute idle grace. Answer with `bd human respond <question> --response "…"`, and the ticket returns to the queue with its branch rebased onto the current one. If you answer in the worker's tab instead and it carries on, orchestra adopts that worker and merges its work, whether the ticket returns through `bd ready` or not: the worker claims it again, and `bd ready` lists only open tickets, so orchestra also reads each asked ticket every 30 seconds and before the run ends. One in progress again (while the run takes tickets) or closed (unless the run stops for a failure) is adopted at once, beside the running tickets even beyond `--concurrent`, since its worker is already at work, rather than waiting for a free slot: `ANSWERED: <id> was closed in tab <tab> after <question> (…), so its worker is adopted`. A worker still idle there once its question is answered is told so, and carries on with what it knows. An asked ticket its worker defers in its tab shows as deferred, and one in progress whose worker has gone from its tab stops the run with `PAUSED` and a note on the ticket. As the run ends, an asked ticket left in progress, or closed but unmerged because the run stopped (`ASKED_UNMERGED`), is labelled `unmerged`, as a ticket left running is. The log says `ANSWERED`, and the ticket's row goes back to working. Answering in the tab works between runs too: the next run carries each asked ticket over (see [Several tickets at once](#several-tickets-at-once)), merges it if its worker closed it meanwhile, adopts the worker if it is at work on it again, and otherwise tells it, once the question is answered, to carry on.

Claude workers are started with a one-line instruction to read `.orchestra/run/prompt.md` in their worktree, where `orchestra` writes the prompt (kept out of git through the repository's `info/exclude`, so it's ignored even on a branch cut before `.orchestra/.gitignore` was committed). Herdr can't pass line breaks to a worker, and a prompt pasted into the input box can go unsubmitted. `-prompt-at-launch=false` pastes it instead.

Claude workers also start with `--settings .orchestra/run/hooks.json`: hooks, for that worker only, that record each tool it uses in `.orchestra/run/activity.json`. A hook that can't write its record (say, the worker removed `.orchestra/run/`) leaves it unwritten and exits 0, so it never blocks the worker. That is how the dashboard tells `testing` (the check command or a test runner), `editing` and `reading` apart from plain `working`, without reading the worker's screen. It is also how `orchestra` knows a worker has finished: Herdr can show a worker as idle while it is still starting up, so a Claude worker counts as settled only at its Stop hook, the end of its turn. While its last hook was a tool use it is mid-turn, whatever Herdr says, for up to 10 minutes (a turn that fails ends without a Stop). Without hooks (another agent kind), or before its first one, an idle worker gets 3 minutes from its start to claim its ticket before it counts as settled with the ticket still open. The log says what decided it, such as `A settled: Stop hook at 20:48:39`.

A worktree is its worker's to change, so `orchestra` reaches the files in its `.orchestra/run/` (and the environment probe's file in the main checkout's) through an `os.Root` at the checkout, which won't follow a symlink out of it. Nor will it write through a symlink in place of `.orchestra` or `.orchestra/run`, wherever it points: git's ignore rules for the folder match a directory, not a link, so the files (`mcp.json` holds the MCP servers' secrets) would be untracked where it points, for a worker to commit. A file `orchestra` writes there that is a symlink staying inside the worktree is replaced by the file, not written through. A worktree whose `.orchestra` or `.orchestra/run` is a symlink, or with a file `orchestra` writes there that is a symlink leading outside, is set aside without a worker: `RUN_FILES_OUTSIDE: its worktree's .orchestra/run is a symlink -> <id> deferred …` (`… points outside the worktree` for such a file), with a note on the ticket naming the link. Remove the link, then `bd undefer <id>`.

A ticket's ID names its worktree (a folder under the worktree root) and its branch, `wt/<id>`, and workers file tickets themselves, while Beads checks only an ID's prefix. A ready ticket whose ID isn't a plain name (one with a `/`, a `\` or `..`, or one git won't take in a branch name) is set aside before anything is made from it: `BAD_TICKET_ID: IDs with path characters can't be run: <id> -> deferred …`, with a note on the ticket, and the run goes on. Give it a plain ID with `bd rename <id> <new-id>`, then `bd undefer <new-id>`. `--ticket` with such an ID is a setup problem (exit code 2).

Workers of another agent kind (`--agent` / `AGENT_KIND`, any kind Herdr can start) are dispatched, watched and merged the same way, but these features only work with `claude`:
- **Prompt at launch.** Workers of other kinds always get the prompt pasted.
- **Recovering a paste whose Enter didn't register.** `orchestra` only recognises Claude Code's `❯` input box, so it pastes the prompt once more instead of pressing Enter, and if the worker still doesn't start, defers the ticket (`PROMPT_FAILED`).
- **The latest action on the dashboard,** read from Claude Code's `⏺` and spinner lines, and `testing`, `editing` or `reading`, from the hooks above.

The [organs](#organs) run `claude` whatever the workers' agent kind.

## Running orchestra through an agent

Optional: [`skills/orchestra/SKILL.md`](skills/orchestra/SKILL.md) is a skill for coding agents such as Claude Code. It tells the agent how to:
- find out where a project stands: whether `init` has run, which files the project uses, whether a run is active;
- choose, check and fix the MCP servers workers get;
- launch a run in a Herdr pane beside its own;
- leave the checkout alone during a run;
- read the event stream, the log, the run report and the tickets' triage notes when something is set aside or a run stops;
- finish a ticket by hand;
- leave pushing, answering questions and worker dialogs to you.

To use it, copy the folder into your skills: `~/.claude/skills/orchestra/` for every project, or `.claude/skills/orchestra/` in one project. Then ask the agent to "run orchestra", "set this project up for orchestra" or "why did the run stop?".

## Event stream

The log is written for people, and its wording changes. For scripts and agents, each run also appends its events to `.orchestra/run/events.jsonl` in the main checkout, out of git with the rest of `.orchestra/run/`: one JSON object per line, each appended in a single write, so a reader tailing the file never sees half a line. Like the log, the file keeps every run.

```
{"time":"2026-10-02T09:12:04.51+02:00","run":"2026-10-02T09:12:03.98+02:00","kind":"start","version":"v0.3.0","repo":"/Users/me/kinieta","branch":"batch/2026-10-02","concurrency":3}
{"time":"2026-10-02T09:12:06.2+02:00","run":"2026-10-02T09:12:03.98+02:00","kind":"dispatch","ticket":"kinieta-kco","title":"Open the property model","text":"[1/40] kinieta-kco dispatching: Open the property model","n":1,"limit":40,"queued":12}
{"time":"2026-10-02T09:31:40.07+02:00","run":"2026-10-02T09:12:03.98+02:00","kind":"closed","ticket":"kinieta-kco","detail":"ffd6ce4 merged into batch/2026-10-02","text":"  kinieta-kco closed (ffd6ce4 kinieta-kco: Open the property model); merged into batch/2026-10-02, worktree, branch and tab removed"}
{"time":"2026-10-02T11:02:13.6+02:00","run":"2026-10-02T09:12:03.98+02:00","kind":"done","text":"READY_EMPTY after 12 tickets"}
{"time":"2026-10-02T11:03:55.1+02:00","run":"2026-10-02T09:12:03.98+02:00","kind":"end","code":0}
```

Every record has `time`, when it was written, and `run`, when the run started: RFC 3339 with fractional seconds, in local time. `run` is the same in each of a run's records and in its lock's `started`, so it picks one run's records out of the file. `kind` says what the record is:

| `kind` | What happened | Its other fields |
|---|---|---|
| `start` | the run's first record, once its startup checks pass (with `--feature`, once the plan is filed, or isn't) | `version`; `repo`, the main checkout; `branch`, where finished tickets land; `scope`, the ticket the run is scoped to (`--ticket`, or the epic a `--feature` request was filed as), absent for all of `bd ready`; `feature`, the `--feature` request; `concurrency` |
| `info` | progress: the `START` line, worktrees, how workers settled… | `text`; `ticket` on some |
| `dispatch` | a ticket was picked up | `ticket`, `title`; `n` and `limit`, as in `[n/limit]`; `queued`, how many ready tickets wait for a slot; `solo` |
| `queue` | the number of ready tickets waiting for a slot changed: the dashboard's **In queue**, which the log doesn't have | `queued`, `solo` |
| `closed` | a ticket closed and was merged | `ticket`; `detail`, as in `ffd6ce4 merged into main` |
| `deferred` | a ticket was set aside | `ticket`; `detail`, why: `by the worker`, `still in_progress, noted for review`, … |
| `asked` | a ticket waits on a question for you | `ticket`; `detail`, the question's ID and title |
| `answered` | its question was answered: it comes back, dispatched next | `ticket`, `detail` |
| `triage` | the triage organ's verdict on a deferred ticket | `ticket`; `title`, the verdict's summary; `detail`, as in `environment · high` |
| `warn` | something to review, while the run goes on: `CHECKS_FAILED`, `MERGE_CONFLICT`, `LIKELY_CONFLICT`, … | `ticket` when it is about one; `aside: true` when that ticket is left for review, out of this run |
| `hold` | something stopped the run: no new tickets while the running ones finish | `ticket` on some |
| `drain`, `resume` | the run was asked to stop after the running tickets, or that was taken back | |
| `probed` | a probe found the machine working after an environment hold: tickets start again | |
| `stop` | the loop stopped and needs you: `PAUSED`, `MERGE_FAILED`, `INTERRUPTED`, … | |
| `done` | the loop finished: `READY_EMPTY`, `LIMIT_REACHED` or `DRAINED`, with `SCOPE_DONE` or `SCOPE_OPEN` in a scoped run | |
| `end` | the run's last record, as orchestra exits, after triage and the run report | `code`, the [exit code](#exit-codes) |

Every record but `start`, `queue` and `end` also has `text`, its line in the log as it is there, without the time. A `stop` or `done` record's text starts with the word that says how the loop ended (`PAUSED: …`, `READY_EMPTY after 12 tickets`), a `hold` record's with `HOLD: ` and that word. `solo` is there while a ticket labelled `solo` runs (`{"ticket":"<id>"}`) or is next (`{"ticket":"<id>","next":true}`). A field without a value is left out, except `queued` and `code`, which can be 0. Kinds and fields may be added, but those here keep their names: read the ones you know and skip the rest.

Until its `end` record a run is still going, or was killed (`kill -9`, a crash): `lsof -t .orchestra/run/orchestra.lock` says which. A `--feature` run writes nothing there while it screens and plans the request. If the stream can't be written, the log says so once, and the run goes on.

With `jq`, in the main checkout:

```
E=.orchestra/run/events.jsonl
run=$(jq -r .started .orchestra/run/orchestra.lock)   # the latest run's start, as its lock gives it
jq -c --arg run "$run" 'select(.run == $run and (.kind | IN("hold", "stop", "done", "end")))' $E   # how it ended
jq -r --arg run "$run" 'select(.run == $run and .kind == "closed") | .ticket' $E               # what it merged
tail -f $E | jq -c 'select(.kind != "info" and .kind != "queue") | {kind, ticket, text}'      # follow a run
```

## Exit codes

| Code | Meaning |
|---|---|
| 0 | nothing left in `bd ready`, the limit was reached, or it stopped after the running tickets as asked (`DRAINED`); with `--ticket`, the last line says whether the ticket's scope is finished (`SCOPE_DONE` or `SCOPE_OPEN`); with `--feature`, also a plan you declined |
| 2 | setup problem found before starting (all problems are listed), or another run going in the same repository; with `--feature`, also a request the screen turned down or couldn't judge, a plan that failed or has questions, or no terminal to confirm on without `--yes` |
| 3 | a worker stayed blocked for more than 4 minutes or unknown for more than 5, went idle with its ticket still `in_progress`, or was still going after the ticket limit |
| 4 | Herdr, Beads or git failure, or orchestra panicked while working on a ticket (`PANIC`) |
| 5 | uncommitted changes in the main checkout, or it left the branch it started on |
| 6 | a finished ticket's branch does not fast-forward (it should have been rebased first) |
| 7 | workers kept failing at once, whichever ticket they had: the environment, not the tickets (`ENVIRONMENT`) |
| 130 | stopped with Ctrl+C, SIGTERM or SIGHUP, or quit at once by a second one while the stopped run wound down (`INTERRUPTED: quit at once …`); the running worker keeps its tab and worktree |

After a 3, answer the worker in its tab, then resume with `DONE_SO_FAR=<n>`: the next run carries over the workers this one left running (see [Several tickets at once](#several-tickets-at-once)), so it merges a ticket the worker closed meanwhile, and adopts a worker still at work on its ticket. A ticket whose worker was left running (after a 3, 4 or 130, say) is also labelled `unmerged`: the tickets it blocks wait until its branch (`wt/<id>`) is merged, by the next run or by hand, and the next run removes the label once the branch is on the base. A quit at once labels and saves nothing: label each ticket it names yourself (`bd label add <id> unmerged`), merge one its worker closes by hand, and run `git status` before the next run if it abandoned a merge.

## Differences from orchestrate.sh

- `python3` is no longer needed.
- If `herdr agent start` reports a failure but the worker came up anyway, the orchestrator uses it instead of retrying into an occupied pane, which ends in `START_FAILED`.
- The dispatch log line includes the ticket title: `[1/40] kinieta-2e7 dispatching: <title>`.

## Development

```
scripts/check.sh
```

`scripts/check.sh` is the full check: `go vet ./...`, `go test -race ./...` and golangci-lint. It is also orchestra's own check command for this repository (`.orchestra/settings.json`), so a ticket that fails lint isn't merged, and workers run it before closing a ticket. golangci-lint runs the linters the [Uber Go style guide](https://github.com/uber-go/guide/blob/master/style.md#linting) asks for, configured in `.golangci.yml`: errcheck (terminal writes excepted), goimports, revive, govet and staticcheck, plus predeclared and lll (lines up to 120 columns). The script runs it with `go run` at a pinned version (v2.14.0), so a machine needs only Go; the first run downloads it.

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
| `merge.go` | merging a closed ticket: rebase, check command, fast-forward, cleanup |
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

[Changelog](CHANGELOG.md)
