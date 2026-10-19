---
name: orchestra
description: Run and look after orchestra, which works through a Beads backlog with several workers (coding agents) side by side, as many tickets at once as the project allows, each in its own tab (in Herdr, tmux or cmux) and git worktree. Use when the user asks to set a project up for orchestra, launch or restart a run, build what a spec describes (orchestra --spec, also nightly), follow a run, or find out why a run stopped, why a ticket was deferred or where a worker's work went.
---

# orchestra

`orchestra` picks the highest-priority ticket from `bd ready`, gives it a git worktree
(`<repo>-worktrees/<ticket>` on branch `wt/<ticket>`) and a tab labelled with the ticket ID (a Herdr
tab, a tmux window named with it, or a cmux workspace),
starts a worker there (a coding agent: Claude Code by default, with its tools and the project's
MCP servers) with the project's worker prompt, waits for it to settle, then reads the ticket's
status in Beads. A closed ticket with a commit naming it is fast-forwarded
into the branch the main checkout is on, and its worktree, branch and tab are removed; so are those of
a closed ticket with no commits of its own and a clean worktree, which has nothing to merge. Anything
else is left for review, and the loop moves on or stops. It never pushes.

Two words, kept apart: **workers** do tickets, one per tab and worktree; **organs** are
orchestra's own one-shot advisers: triage, the predictor, the reviewer that writes the run report,
and the scout (for `orchestra init`'s checks).
Each is a `claude -p` call with no MCP servers that answers from what it is given. All but the
scout have no tools; the scout reads the repository itself, with read-only tools only (Read, Glob,
Grep). Organs never change a ticket or the code.

## First, find out where the project stands

Run these before launching anything or answering questions about a run:

```
orchestra --version                         # installed, and which build (-v for short)
git rev-parse --show-toplevel               # the repository (use the main checkout, not a worktree)
git rev-parse --absolute-git-dir            # equal to --git-common-dir in the main checkout
git branch --show-current                   # finished tickets land on this branch
git worktree list                           # per-ticket worktrees still around
bd ready; bd list --status=in_progress      # what a run would pick up; what is claimed
bd human list                               # questions waiting for the user
lsof -t .git/orchestra.lock                 # is a run active here? its PID; nothing if not
cat .git/orchestra.lock                     # which run holds it (or held it last)
tail -n 1 .orchestra/run/events.jsonl | jq -c .   # the last run's last record: "end" with its exit code once over
jq . .orchestra/run/state.json              # the workers the last run left behind, for the next to carry on with
```

Run the last four in the main checkout. A run holds `.git/orchestra.lock`, in the git directory the
worktrees share (`git rev-parse --git-common-dir` names it from any of them), for as long as it runs,
and the system lets go of it when orchestra exits, even killed, so only a running orchestra has it
open. `git clean` never removes it. The file is JSON: `pid`, `started`, `version`, `branch`, `ticket` (`--ticket`),
`feature` (the `--feature` request) or `spec` (a `--spec` run's spec, its path in the repository), and `pane`, the
Herdr or tmux pane, or cmux terminal, it runs in. It stays after the run;
whether `lsof` lists a PID is what says a run is going. Runs in other repositories have their own.

`.orchestra/run/events.jsonl` is the runs' events for you to read: one JSON
object per line, every run appended (see [Reading a run's events](#reading-a-runs-events)). Read it
rather than grepping the log, whose wording is for people and may change.

`.orchestra/run/state.json`, there too, is what the last run left in worker tabs, written as it
ended (it is absent when it left nothing): each worker's `ticket`, `agent`, `tab`, `worktree` and
`hooks`, with the `question` (and `question_title`) its ticket waits on, or why the run `left` it
running (`PAUSED`, `INTERRUPTED`, …). The next run carries them over: it merges a ticket closed
meanwhile, adopts a worker at work again, and tells an idle one whose question is answered to carry
on. Leave the file to orchestra; don't edit it.

Where the project's orchestra files are:

| The repository has | Layout | Prompt | Log | Reports |
|---|---|---|---|---|
| `.orchestra/worker-prompt.md` | current | `.orchestra/worker-prompt.md` | `.orchestra/orchestra.log` | `.orchestra/reports/` |
| only `.claude/worker-prompt.md` | before `orchestra init` | `.claude/worker-prompt.md` | `.claude/orchestrate.log` | `.claude/orchestrate-reports/` |
| neither | not set up | run `orchestra init` | | |

`--prompt` / `WORKER_PROMPT` can point elsewhere; the log's `START` line records what a run used.
`.orchestra/settings.json` holds the checks, `check_fast` (the merge check, `scripts/check-fast.sh`)
and `check_full` (everything, `scripts/check-full.sh`, run once at the end of a run), optionally `check_own`
(a worker's check of its own change, which its rules name in place of `check_fast`), `mcp_servers` (the MCP servers workers get, by
name; see [Workers' MCP servers](#workers-mcp-servers)) and `concurrent`, how many tickets run at the
same time by default (`--concurrent N` / `-c N` / `ORCHESTRA_CONCURRENT` overrides it for a run),
and optionally `ticket_limit`, how long a worker may go on before the run stops for it (`"2h"`;
`--ticket-limit` / `TICKET_LIMIT` overrides it, `0` for none), `check_fast_timeout`, how long the
merge check may run on a ticket before it is stopped and the ticket set aside (`"5m"`, default
30m; `--check-timeout` / `ORCHESTRA_CHECK_TIMEOUT` overrides it), `check_full_timeout`, the same for
check-full (default 60m; `--check-full-timeout` / `ORCHESTRA_CHECK_FULL_TIMEOUT`), and `exclude_types`, the issue
types never dispatched (default `["epic"]`; `[]` dispatches every type), and `environment_hold`,
when workers failing the same way hold the run (default `{"count": 2, "window": "2m", "probe": "10m"}`;
`"count": 0` turns it off), and how long after a hold a worker without a ticket probes the machine
(`"probe": "0"` for no probe), `usage_limit_wait`, how long the run waits for the account's usage
limit to reset before it stops (default `"6h"`; `"0"` doesn't wait), `worker_permission_mode`, the Claude
Code permission mode workers start in (`default` or `manual`, `acceptEdits`, `auto`; unset, Claude Code's
own, or `auto` in a spec run; `--worker-permission-mode` / `WORKER_PERMISSION_MODE` overrides it), and
`spec_timeout` and `spec_effort`, how long a spec's breakdown may take and its effort (default `"60m"`
and `high`; `--spec-timeout`, `--spec-effort`). The old `check` and `check_timeout` are read as
`check_fast` and `check_fast_timeout`.

## Setting a project up

```
orchestra init --check-fast "<the project's check command>" --concurrent 1
```

It sets Beads up first: where `bd` is missing it can install it (Homebrew, otherwise the Beads
install script), and in a repository without `.beads/` it runs `bd init` with no questions
(maintainer, auto-export off). Installing software is the user's call: ask, then pass
`--install-beads` or `--install-beads=false`; without either, an agent's `init` installs nothing and
reports `bd` missing. `bd init` commits the files it adds along with anything already staged, so
check `git status` before `init`. It then writes `.orchestra/worker-prompt.md` from the built-in
template (or moves an existing `.claude/worker-prompt.md`, staged with `git mv`), adds
`.orchestra/.gitignore`, reports what is missing (`bd`, Beads, one of `herdr`, `tmux` and `cmux`, `claude`), writes the
runners `scripts/check-fast.sh` and `scripts/check-full.sh` (one line per suite; an existing
`scripts/check.sh` is called as a suite, never edited; a runner already there that differs is kept
and reported, unless its flag is given) and writes `.orchestra/settings.json`. Ask the user
for the check command if you don't know it (the command that runs lint, build and tests) and how
many tickets to run at the same time, then pass both: run by an agent, `init` can't ask
interactively and would default to 1. Ask which MCP servers workers need for the project's work
(`claude mcp list` shows what this machine defines) and pass `--mcp a,b`, or `--mcp ""` for none;
without it, an agent's `init` leaves them unchosen and workers get every server. `--check-fast-timeout 5m`
(or `--check-timeout`) sets the merge check's time limit (default 30m): a few times the check's usual
running time. `--check-full "<command>"` adds the slower suites to `check-full.sh`, with
`--check-full-timeout` (default 60m); `--check-fast ""` writes a runner that checks nothing yet. More than 1 needs checks that can run side by side; say so.
Where the project keeps a `CHANGELOG.md`, ask whether to add `CHANGELOG.md merge=union` to
`.gitattributes` (so tickets that each add an entry at the same spot don't conflict) and pass
`--changelog-union` or `--changelog-union=false`; without either, `init` leaves it alone.
Where the repository has a lockfile, pass `--setup "<command>"`, the command that installs its dependencies
(`npm ci`, `pnpm install --frozen-lockfile`, `uv sync`, `bundle install`, …; the summary names the one it found),
which orchestra runs in each new worktree before its worker starts, and before the merge check when a
rebase changes a lockfile; `--setup ""` for none.
The prompt names `scripts/check-fast.sh` as the check. It never replaces an existing prompt
unless given `--force`; don't pass `--force` without the user's say-so. Afterwards, show the user the prompt and the runners and commit `.orchestra/` and `scripts/` (and `.gitattributes`, if it changed, and what `bd init` left uncommitted: the Next box names it) if they agree.

## Launching a run

Requirements, checked by `orchestra` at startup (it lists every problem, exit code 2):

- set up with `orchestra init` (see above). In a repository without `.orchestra/worker-prompt.md`
  or `.orchestra/settings.json` (or a legacy `.claude/worker-prompt.md`), `orchestra` and
  `orchestra --feature` say first `orchestra isn't set up in this repository yet. Run this first:
  orchestra init`, then any other problem under `Also fix before a run:`. Set the project up as
  above, with the user's answers, rather than run `bd init` yourself. A spec run (below) is the
  exception: it sets the repository up itself;
- a terminal for the workers: Herdr when `orchestra` runs inside a Herdr pane (`HERDR_ENV=1`),
  its tabs in that pane's workspace unless `--workspace ID` names another; otherwise tmux, which must
  be installed (`brew install tmux`, `apt install tmux`: the user's call), its windows in orchestra's
  own session when it runs in tmux, else in a detached session `orchestra-<repository folder>`
  (`--workspace <session>` names another), which the user watches with `tmux attach -t <session>`, as
  the `START` block says. Inside tmux it uses tmux, and inside a cmux terminal (`CMUX_WORKSPACE_ID`
  set) cmux, one workspace per worker in orchestra's window; cmux must be chosen from inside one of
  its terminals. `--terminal herdr|tmux|cmux` (or `"terminal"` in `.orchestra/settings.json`)
  chooses instead. In tmux and cmux, workers must be Claude Code (no `--agent` other than `claude`);
- in the main checkout, on a branch, not a detached HEAD;
- no uncommitted changes outside `.claude/`, `.beads/` and `.orchestra/`;
- no other run in the same repository. A second one stops before changing anything (a `--feature`
  request before it is talked through) with `orchestra is already running in <repo> (pid …, since …,
  <branch>, pane …)`: follow that run in its pane instead, or ask the user before stopping it.

Run it on the branch finished tickets should land on. Unless the user says otherwise, propose a
batch branch from the main branch (`git switch -c batch/$(date +%F)`), so the work reaches main
through one pull request.

`orchestra` is a long-running terminal UI: never run it in your own shell tool. Start it in a pane
beside yours and keep your own pane free. In Herdr (the pane ID is read from Herdr's JSON with `jq`):

```
P=$(herdr pane split --current --direction right --cwd "$PWD" --no-focus \
    | jq -r .result.pane.pane_id)
herdr pane run "$P" "orchestra --tickets"
herdr pane wait-output "$P" --regex "dispatching|cannot start|isn't set up|READY_EMPTY|Completed the Run|All done|Nothing ready|Nothing under" --timeout 60000
herdr pane read "$P" --source visible
```

In tmux, split your own pane when you run in tmux (`$TMUX` is set), so orchestra's workers open in
your session; otherwise start a detached session for it, and tell the user to watch with
`tmux attach -t orchestra`:

```
P=$(tmux split-window -h -d -c "$PWD" -P -F '#{pane_id}')    # in tmux
P=$(tmux new-session -d -s orchestra -c "$PWD" -P -F '#{pane_id}')    # or outside it
tmux send-keys -t "$P" "orchestra --tickets" Enter
for _ in $(seq 60); do
  tmux capture-pane -p -t "$P" | grep -qE "dispatching|cannot start|isn't set up|READY_EMPTY|Completed the Run|All done|Nothing ready|Nothing under" && break
  sleep 1
done
tmux capture-pane -p -t "$P"
```

In a cmux terminal, split your own terminal with `cmux new-split right`, type `orchestra --tickets`
and Enter into the new one with `cmux send` and `cmux send-key`, and read it with
`cmux read-screen` (`cmux --help` lists their options; address the terminal by the UUID
`new-split` prints).

Launch with `--tickets`. A pane is a terminal, and there the bare `orchestra` first asks what the run
should work on (`Current tickets: N ready`, `none, all done` or `none ready (N open)`, `Spec`, which
asks for a spec file, or `New feature`, which asks the user to describe one and then talks it through
with them in a Claude Code session): the wait above would time out on the question. `--ticket <id>`
and `--spec <file>` don't ask either. Leave the bare command to a user at the keyboard, and
`--feature "<request>"` too: it skips the question but talks the feature through with the user in a
Claude Code session beside orchestra's pane, and ends with exit code 2 and nothing filed where there
is no pane or terminal for it. For a feature built with nobody there, see A spec run below.

A run with nothing to run (nothing ready, or only tickets it would hold back, and no worker the last
run left to carry on with) doesn't open the dashboard: it shows a box, `✓ All done` (with `Still
open: <ids> (bd close <ids>)` for epics left open) or `○ Nothing ready to run` with what holds the
tickets left (`○ Nothing under <id> is ready to run` and each ticket's reason, with `--ticket`), and
exits 0 without a report. Tell the user what it says.

Useful settings (environment variable or flag): `--concurrent N` / `-c N` (tickets at the same
time, overriding `settings.json`), `LIMIT` (stop after so many tickets; none by default),
`DONE_SO_FAR` (count earlier tickets toward the limit), `TRIAGE=0` / `REVIEW=0` (no organs),
`ORGAN_MODEL`, `NOTIFY=0` (no macOS notifications), `PROMPT_AT_LAUNCH=0` (paste the prompt instead
of starting the worker with it), `--ticket-limit 2h` / `TICKET_LIMIT` (stop when a worker is still
going that long after dispatch; `0` for none, overriding `settings.json`), `--check-timeout 5m` /
`ORCHESTRA_CHECK_TIMEOUT` (the merge check's time limit, overriding `settings.json`),
`--check-full-timeout 90m` / `ORCHESTRA_CHECK_FULL_TIMEOUT` (check-full's). `orchestra -h` lists
them all.
`--machine-workers N` / `ORCHESTRA_MACHINE_WORKERS` caps the workers across every orchestra run on
the machine (none by default: a free slot waits at the cap, `HOLD: MACHINE`); a run beside another
says `SHARED_MACHINE` once for it, as their checks slow each other down.

When the user wants one piece of work finished (an epic and its children, a ticket broken into
subtickets) rather than the whole backlog, run `orchestra --ticket <id>` (or `ORCHESTRA_TICKET`):
only that ticket and its descendants are dispatched, each parent after its children, and follow-ups
join only when filed as its children (`bd create --parent <id>`). An epic is never dispatched;
orchestra closes it once a run merges its last subticket (`closed epic <id>` in the log). In every run, a ticket
with subtickets not yet closed and merged waits for them (`<id> waits: its subtickets are not all
closed and merged`).

With several at once, a ticket labelled `solo` runs with no other beside it. Suggest the label
(`bd label add <id> solo`) for a ticket that restructures code most tickets touch, such as splitting
a shared file: run next to other work, it guarantees merge conflicts.

Tickets are also kept apart by footprint: a ready ticket that names the same function (or, when one
of the two names no functions, the same file) as a running ticket, shares an `area:<name>` label with
it, or names a file its worker has edited, waits for a later slot while the next ticket takes this
one. Naming files and functions in a ticket's description helps; so does `bd update <id>
--set-metadata files=a.go,b.go` or an `area:<name>` label. For a ticket naming none of these, the
predictor organ guesses its files in the background and caches them as `predicted_files` metadata.
`"footprint": false` in `settings.json` turns it off.

After a batch of tickets is filed, `orchestra plan` proposes blocks links between open tickets that
name the same function or work on the same small file (one a ticket names, or that mentions a
function it names, unless both name different functions the file mentions), the higher-priority
ticket first, and changes nothing. Show the user the proposal; run `orchestra plan --apply` (which adds
them with `bd dep add`) only when they approve.

## A spec run

When the user wants a feature or a whole app built from a spec (a Markdown or plain-text file that
describes it), launch `orchestra --spec <file>` in a pane, as above, in place of `--tickets`. It asks
nothing: it commits the spec (one outside the repository as a copy in `.orchestra/specs/<name>`), has
`claude -p` break it down into a root epic, child epics and tickets, each carrying the metadata `spec`,
and runs them as `--ticket <root epic>` would, with workers in Claude Code's `auto` permission mode. In Herdr, for
example:

```
herdr pane run "$P" "orchestra --spec ~/specs/shop.md"
herdr pane wait-output "$P" --regex "[Bb]reaking the spec down|carrying on with|is built|cannot start|cannot run|couldn't|dispatching" --timeout 60000
grep 'SPEC:' .orchestra/orchestra.log | tail    # the breakdown's steps and the epics it has filed
```

The breakdown may take up to an hour (`spec_timeout`), while the pane shows a spinner counting the
epics and tickets filed; the log has a `SPEC:` line for each epic and each step, and the event stream
starts only once the breakdown is over. Its first lines say what it did: `committed the spec <path>`
or `the spec <path> is committed already`, then `broke the spec down into <root> (N epics, M
tickets)` and the assumptions it made, which also go under `Assumptions` in the root epic's notes.
Relay those to the user: they are the choices the spec left open, and changing the spec is how to
change one.

**A request rather than a file.** When the user asks for a feature to be built by a run you start
(a request in words, not a spec file), don't launch `orchestra --feature`: it needs the user at a
terminal, to talk the feature through with them before it files anything, and there is no unattended
feature run. Write the request to a Markdown file instead, with what the user settled when they
asked, and launch `orchestra --spec <file>` as above. Its breakdown takes the file as complete and
final and asks nothing, so say there what you know of the user's choices; where it leaves a choice
open, the breakdown takes the plainest reading and writes it under `Assumptions`. A file outside the
repository is committed as a copy in `.orchestra/specs/<name>`, which workers read; one inside it is
committed where it is. Run again later, the same command carries on with what is left.

- **In a repository `orchestra init` hasn't set up**, even an empty one with no commit, a spec run
  sets it up itself as `init` does without a terminal (bd init, the worker prompt, runners that check
  nothing yet, no MCP servers for workers) and commits that with the spec. Outside a git repository
  it says to run `git init` first: ask the user before you do. It never installs `bd`.
- **The environment setup ticket comes first**, P0 and `solo`, every other ticket waiting for it, when
  the spec needs a tool the repository doesn't have or there is no code yet: its worker installs the
  toolchain without `sudo`, scaffolds a project with no code, installs the dependencies and sets
  `setup`, which the run then runs in each new worktree (`SETUP: <id>'s merge changed
  .orchestra/settings.json …`). A tool it can't install without `sudo`, a GUI installer or a licence
  is a question for the user (`bd human list`): the run waits on it. Relay it; don't install it yourself.
- **Run again, the same command carries on**: with the spec unchanged, it runs what is left under the
  root epic (`the spec <path> is unchanged; carrying on with <root>`), or says `✓ <path> is built
  (<root>)` and exits 0; with the spec changed, it commits it and the breakdown files tickets for the
  change under the same root epic (`the spec <path> changed since <root> was filed: updating its
  tickets`). A breakdown that didn't finish is carried on too.
- **`--refine`** breaks the spec down with the user, in an interactive Claude Code session in a pane
  split off orchestra's, which asks them what the spec leaves open; the run starts once they agree
  and the tickets are filed. Leave it to the user at the keyboard, or launch it only in a pane they
  watch and say so: don't answer its questions yourself. Without a terminal it stops with exit 2.
- **Exit 2 after the breakdown** (`orchestra couldn't break the spec down: …`, or `won't run what the
  spec's breakdown filed`) lists what was filed and ends `Run it again to carry on: orchestra --spec
  <file>`: tell the user why, and rerun it once they agree. `warning: …` lines after a breakdown
  (tickets without files metadata or acceptance criteria, blocks cycles) don't stop the run: relay them.
- **The root epic**: `jq -r 'select(.kind == "start" and .spec) | .scope' .orchestra/run/events.jsonl
  | tail -n 1`, or the `START` block's `  spec: epic <root>, broken down from: <path>` line. `bd show
  <root>` has the Assumptions; `bd list --metadata-field spec=<path> --all` lists the spec's issues.
- **Every night**: docs/running.md's "Every night" in the orchestra repository has a cron and a launchd
  recipe, and what to read in the morning. Installing a crontab line or a launchd agent changes the
  user's machine: show them the recipe with their paths, and install it only when they ask you to.

## While it runs

- **Leave the main checkout alone.** An uncommitted change outside `.claude/`, `.beads/` and
  `.orchestra/`, or a branch switch there, stops the run at the next ticket or merge, whichever
  comes first (`DIRTY_TREE`); a finished ticket is then left unmerged in its worktree for review.
  The dashboard shows such a ticket `■ blocked` with why (`main checkout has uncommitted changes`),
  as it does one a merge conflict or git keeps from merging, and counts it under Needs you.
  A commit on the base branch is picked up: running tickets are rebased onto it and checked again
  before they merge. Beads changes (`bd update`, `bd create`) are fine.
- **The prompt is read once, at startup.** Changes to it apply to the next run.
- **Follow it** in its pane, or with `tail -f .orchestra/run/events.jsonl | jq -c 'select(.kind != "info"
  and .kind != "queue") | {kind, ticket, text}'`. With several at once, the dashboard shows
  a box per worker (one line each in a short pane). In Herdr, each worker is an agent named after
  its ticket, in a tab labelled with the ticket: `herdr agent get <name>`,
  `herdr agent read <name> --source visible` (its scrollback can only be read while it is idle).
  The name is the ticket ID lowercased, with anything other than letters, digits, `-` and `_`
  turned into `_` (`CalendarView-bl0.1` → `calendarview-bl0_1`); an ID over 32 characters is cut
  and ends in a short hash. In tmux, each worker is in a window named with its ticket ID as it is:
  `tmux list-windows -a -F '#{window_name} #{pane_id}'` finds its pane, and
  `tmux capture-pane -p -t <pane>` reads its screen. In cmux, each worker has a workspace named with
  its ticket ID; `.orchestra/run/cmux.json` in the main checkout gives its workspace and terminal
  UUIDs, and `cmux read-screen --workspace <uuid> --surface <uuid>` reads its screen. In tmux and
  cmux, its state comes from its hooks' records in `.orchestra/run/` in its worktree.
- **To wind the run down**, the user presses s in the dashboard and confirms with y: no new
  tickets start, the running ones finish and merge, and the run ends with `DRAINED` and exit 0.
  Asked by the user to do it for them, send `kill -USR1 <orchestra's pid>` (the PID from
  `lsof -t .git/orchestra.lock`); don't press keys in its pane. Ctrl+C stops at once instead, leaving the workers running.
- **A stopped run that hangs** while it winds down (`waiting for <id>'s merge to finish…` for minutes)
  quits on a second Ctrl+C, SIGTERM or SIGHUP, without triage or the report: at once when nothing
  is under way that a stop doesn't cut short; otherwise the second says what is
  (`<id>'s merge is under way; press Ctrl+C again to abandon it …`) and a third abandons it. Asked
  by the user to end such a run for them, `kill <pid>` it again rather than `kill -9`, which skips
  the last log line and the `end` record; tell them first if a merge is under way.
- **A `PROBE:` line means the run is still going**: it held for the environment, and after the
  wait it names, a worker without a ticket (tab `orchestra-probe`, in the main checkout) runs one
  command. `PROBE_OK` means the run takes tickets again; otherwise it ends with `ENVIRONMENT`. Don't
  restart it meanwhile; `kill -USR1` ends it during the wait, as `ENVIRONMENT`.
- **A `USAGE_LIMIT:` line means the run is still going**: a worker reached the account's usage limit,
  and the run holds, starting no new tickets, until it resets (the line says when, as does the
  dashboard's title line); then the limited workers are told to carry on (`USAGE_LIMIT lifted`). Don't
  restart it or type into a limited worker's tab. A hold longer than `usage_limit_wait` (6h) ends the
  run with `USAGE_LIMIT`, exit 8.
- **Don't type into a worker's tab or press keys on its dialogs** unless the user asks; Enter on a
  dialog picks an option (on Claude Code's trust dialog, "No, exit").

## Workers' MCP servers

Workers get only the MCP servers named in `mcp_servers` in `.orchestra/settings.json`: names only,
each resolved on this machine from Claude Code's config (local scope for this repository or its main
checkout in `~/.claude.json`, then project scope in `.mcp.json`, then user scope in `~/.claude.json`;
`$CLAUDE_CONFIG_DIR/.claude.json` when that is set). Workers read ticket text orchestra can't fully
trust, so they should get only what the work needs.

To check:

```
jq '.mcp_servers' .orchestra/settings.json   # null: not chosen (workers get every server); []: none
claude mcp list                              # what this machine defines, run in the main checkout
grep 'MCP servers:' .orchestra/orchestra.log | tail -1   # what the last run's workers got
```

The `START` line says `MCP servers: <names>`, `none`, or `all, not configured`. The last also comes
with a warning: `workers load every MCP server Claude Code finds on this machine; choose theirs with
orchestra init`. Suggest choosing them.

To change: `orchestra init --mcp a,b` (or `--mcp ""` for none). It keeps the other settings and the
prompt. Show the user the change to `settings.json` and commit it if they agree. A run reads it
when it starts, so a run already going keeps the servers it started with.

A setup problem (exit 2) naming the workers' MCP servers means a chosen name can't be resolved here:

- `<name> isn't defined on this machine (define it: claude mcp add <name> …)`: the server is defined
  on a teammate's machine, or was removed. Ask the user for its command or URL and credentials, and
  have them run `claude mcp add <name> …` (local scope, this repository) or `claude mcp add --scope
  user <name> …`; don't invent a definition. Or, if workers don't need it, drop it with
  `orchestra init --mcp <the others>`.
- `<name> is a claude.ai connector, which workers can't get`: connectors come with the user's
  claude.ai login and have no definition orchestra can pass on. Drop it with `orchestra init --mcp …`,
  or have the user define an MCP server for the same service with `claude mcp add`.
- `Cannot read Claude Code's MCP config …`: `~/.claude.json` or `.mcp.json` isn't valid JSON; the
  message names the file. Tell the user; don't edit `~/.claude.json` yourself.

Workers of another agent kind (`--agent`) don't get `mcp_servers`; organs never get any server. The
sessions that break a spec down (`--spec`, with `--refine` or not) or talk a feature through get the
Claude workers' servers, and none when `mcp_servers` is unset, empty, or for workers of another kind.

## Reading a run's events

Each record has `kind`, `time` and `run`, the run's start time, which is the same in all its
records and in the lock's `started`; docs/events.md in the orchestra repository lists every kind and
field.
A run's records go `start` (with `version`, `repo`, `branch`, `scope`, `feature`, `spec`, `concurrency`),
then its events, each with the log line as `text` (`dispatch`, `closed`, `deferred`, `asked`,
`warn`, `hold`, `triage`, `info`, …), then `stop` or `done`, and last `end`, with `code`, the exit
code, once orchestra has exited. No `end` yet: the run is still going (`lsof` lists its PID), or was
killed. A feature run writes its `start` only once the feature is filed (or isn't), and a spec run once
its breakdown is over. A `usage_limit` record (`ticket`, `detail`, `reset`) starts a hold for the
account's usage limit, and `usage_limit_lifted` ends it.

```
E=.orchestra/run/events.jsonl
run=$(jq -r .started .git/orchestra.lock)   # the latest run's start
jq -c --arg run "$run" 'select(.run == $run and (.kind | IN("hold", "stop", "done", "end"))) | {kind, code, text}' $E
jq -c --arg run "$run" 'select(.run == $run and (.kind | IN("deferred", "asked", "warn", "triage"))) | {kind, ticket, text}' $E
jq -r --arg run "$run" 'select(.run == $run and .kind == "closed") | .ticket' $E
```

The first says how the run ended: the `end` record's `code`, and the `stop` or `done` record's
`text`, which starts with the word in the table below (`PAUSED: …`, `READY_EMPTY after …`), after
any `hold` that came first. The second lists what was set aside or needs looking at, the third what
was merged. A `warn` record with `"aside": true` left its ticket for review. A `hold`, `stop` or
`warn` record with `blocked` is a ticket whose work is done but which can't merge until the user
acts, and says why (`main checkout has uncommitted changes`, `merge conflict in <files>`).
A `full_check` record, after `done`, says how the full check went (`detail`: `passed`, `failed`,
`timed out`, `skipped`, `not run`); a failed one has `suite`, `output` and `ticket`.

## When a ticket is set aside or the run stops

Read the run report first (`reports/<start time>.md`: Finished, Set aside, Needs you), then the run's
events (above), then the tickets; the log has what the events don't: raw tool errors. The exit code
(the `end` record's `code`) and the last `stop` or `done` record, whose `text` is the line the run
ended with, say why it ended:

| Last line | Exit | Meaning | What to do |
|---|---|---|---|
| `READY_EMPTY`, `LIMIT_REACHED` | 0 | queue empty, or limit reached; `READY_EMPTY after 0 tickets: everything is done` (or `: nothing ready`) is a run that had nothing to run, which showed why in place of the dashboard and wrote no report | Read the report; the next step is usually the batch PR. With nothing to run, there is no report: relay the box (close the epics it names, answer the questions in `bd human list`, see what `bd blocked` holds). |
| `DRAINED`, after a `DRAIN: stopping after …` line | 0 | the user pressed s in the dashboard (or orchestra got SIGUSR1): no new tickets started, the running ones finished and merged | As for `READY_EMPTY`; the queue may still hold tickets for the next run. A `DRAIN cancelled` line means the user took it back. |
| `…; SCOPE_DONE: …` | 0 | a `--ticket` run: the ticket and all its subtickets are merged (an epic is left to close) | Close an epic with `bd close <id>`; then the batch PR. |
| `…; SETUP_WAITS: …` | 0 | ready tickets the run kept out because the project's `setup` failed in their worktrees (`SETUP_FAILED`), each with its output | Read each output with the user, fix the setup (a missing tool, the network, a wrong command), then run again. |
| `…; SCOPE_OPEN: …` | 0 | a `--ticket` run with subtickets not done; each is named with why | Handle each reason: answer a question, merge or rebase an unmerged one, unblock or rerun. The report lists follow-ups filed outside the scope. |
| any of the lines below, after a `HOLD: …` line | as below | with several tickets at once, a stop first holds: no new tickets, the running ones finish | Handle the reason as below; the `HOLD` line names the ticket. |
| `PAUSED` | 3 | a worker was idle for 10 minutes with its ticket still `in_progress`, or is gone from its tab with no session to resume, or gone again after its session was resumed (`its worker gone from tab …`) | Read its tab. Relay any question to the user. If the worker finishes later, the next run merges it; one gone after a resume is resumed again by the next run. |
| `BLOCKED >4min` | 3 | a worker sat on an approval or question dialog | Show the user the dialog; don't answer it yourself. |
| `UNKNOWN >5min` | 3 | the terminal couldn't tell what a worker was doing for 5 minutes (Herdr from its screen, or in tmux and cmux its hooks) | Read its tab: it may be hung, or its status undetectable for its agent kind. Tell the user what you see. |
| `TICKET_LIMIT` | 3 | a worker was still going after the ticket limit | Read its tab: a hung command, or a big ticket. Tell the user; if the worker finishes later, the next run merges it. |
| `ENVIRONMENT` | 7 | the last tickets' workers all failed at once (settled soon after dispatch without claiming or changing anything, or the setup failed in their new worktrees before they could start), or triage blamed the environment for each with high confidence: the machine, not the tickets | If the line says a probe failed too, read the probe's tab (`orchestra-probe`) first: the run already waited and tried once more. Check the machine: read the workers' tabs and the triage notes for the cause (a refused permission or safety check, a missing tool, the network). Tell the user what you find. Tickets that failed at once were reopened already, and those whose setup failed are open; reopen triaged ones with `bd update <id> --status open` once the cause is fixed. Then restart the run. |
| `USAGE_LIMIT` | 8 | a worker reached the account's usage limit, and the run held for it longer than `usage_limit_wait` (6h by default) | Nothing to fix: the limited workers are left in their tabs, noted on their tickets. Tell the user when the limit resets (the line says); the next run carries the workers over and tells them to carry on once it has. |
| `MERGE_FAILED` | 6 | the ticket's branch doesn't fast-forward after rebasing | Rare: something else changed the base. Rebase the worktree, check, merge by hand. |
| `DIRTY_TREE` | 5 | uncommitted changes in the main checkout, or it left its branch | `git status`. These are the user's changes: ask before touching them. |
| `START_FAILED`, `TAB_FAILED`, `TERMINAL_FAILED`, `WORKTREE_FAILED`, `AGENT_BUSY`, `AGENT_NAME_TAKEN`, `STATUS_UNREADABLE`, `READY_UNREADABLE`, `GIT_FAILED` | 4 | the terminal (Herdr, tmux or cmux), Beads or git failed | `STATUS_UNREADABLE` and `READY_UNREADABLE` end with bd's error, `GIT_FAILED` with git's (it could not read the main checkout three times running); for the others the raw error is in the log, on lines without a timestamp just above. A worker may still be running: check its tab. |
| `INTERRUPTED` | 130 | the user pressed Ctrl+C, or orchestra got SIGTERM or SIGHUP (its terminal or pane closed) | The worker keeps running, and its ticket is labelled `unmerged` (below). If it finishes, the next run merges it. If it leaves no work, reopen its ticket (`bd update <id> --status open`) and remove its empty worktree. |
| `INTERRUPTED: quit at once …` | 130 | a second stop signal (a third with a merge under way) quit while the stopped run wound down | As for `INTERRUPTED`, but nothing was labelled or left for the next run: label each ticket the line leaves running (`bd label add <id> unmerged`), and merge one its worker closes by hand (below). With `abandoned <id>'s merge`, check `git status` in the main checkout and `git worktree list`, and tell the user what is half done before another run. |
| (printed, not logged) | 2 | setup problem | The terminal lists each problem and its fix. `orchestra is already running in …` names the run going in this repository: follow it in its pane. |

Lines about single tickets, which don't stop the run (in the events, `closed`, `deferred`, `asked`,
`answered`, `triage`, `warn` and `info` records with the line as `text`):

- `closed (…); merged into …`: done. The worker's tab is closed only while the terminal still has it
  labelled with the ticket's ID: Herdr numbers tabs afresh when it starts without restoring its last
  session, and tmux its windows when its server starts again, so the tab ID a run recorded may by
  then be another tab, the user's own included. The line then ends `worktree and branch removed; tab
  <tab> left open: the terminal has it labelled '<label>' now, …` (or `its tab <tab> was closed
  already`, or `… as the terminal can't say whose it is`): leave
  that tab alone, and close the worker's own tab by hand if it is still open.
- `deferred by worker`, `still <status> -> noted and deferred`: set aside. A `triage` line follows
  with the triage organ's verdict (cause: environment, instructions or problem), also appended to
  the ticket's notes. `TRIAGE_FAILED` means triage itself failed. A worker that left nothing done
  (no commits, a clean worktree) has its worktree, branch and tab removed, the line ending `nothing
  done, so its worktree, branch and tab are removed`. Otherwise it is left in its tab, its ticket
  labelled `unmerged`, and the run follows it: should it close the ticket there (Claude Code
  carries a worker on by itself once the usage limit resets), `LATE_CLOSE: <id> was closed in tab
  <tab> after it was set aside; merging it now` follows, and it merges as usual. One closed once the
  run has ended, or after a stop, is merged by hand (below); its label holds the tickets it blocks
  until then.
- `<id>'s earlier worker was idle in tab <tab>; its tab is closed, as the new worker takes up its
  worktree; its session's transcript is <path>`: a set-aside ticket dispatched again. Nothing to do;
  the earlier session's transcript is there to read.
- `PROMPT_FAILED`: the worker never started on its prompt; deferred, safe to reopen.
- `ASKED`: the worker asked the user a question, as a ticket labelled `human` that blocks the work.
  Only the user answers it: `bd human respond <question> --response "…"`. The ticket then returns to
  the queue, and its branch is rebased onto the current one when it is picked up. `ANSWERED` says it
  came back. If its worker is still in its tab (answered there, say), orchestra adopts it instead,
  or tells it, idle, that the question is answered, and merges its work as usual. A run that ends
  with the ticket still asked leaves it to the next run, which does the same: the user may answer
  between runs, in the tab or with `bd human respond`.
- `CLOSED_WITHOUT_COMMIT`: closed, but its branch has commits and none names it, or it has none and
  its worktree has uncommitted changes. A ticket closed with no commits and no changes says `<id>
  closed with no change` instead, and its worktree, branch and tab are removed.
- `CLOSED_UNCOMMITTED`: closed with a commit naming it, but its worktree has uncommitted changes:
  commit or discard them, then merge its branch by hand.
- `OFF_BRANCH`: closed, but its worktree has another branch or a detached HEAD checked out rather
  than its own `wt/<id>`, as when its worker switched, against its rules. Nothing was rebased,
  checked or merged. Look at both branches with the user, bring the work onto `wt/<id>`, switch the
  worktree back, then merge it by hand.
- `<id> waits: <blocker> closed but not merged (…)`: a ready ticket held because a ticket blocking
  it isn't on the base branch yet. A ticket closed but left unmerged is labelled `unmerged`, which
  holds its dependents in later runs too; orchestra removes the label when it merges the ticket, or
  when a run starts and finds it merged by hand. If the ticket needs no merge after all, remove it:
  `bd label remove <id> unmerged`. `(its worker was left in tab <tab>)` names a ticket set aside
  with its worker left there, which the run merges should the worker close it.
- `CLOSED_UNMERGED: <id> is closed, but wt/<id> is not on <base>`, once, then `<dep> waits: <id>
  closed but not merged (wt/<id> is not on <base>)`: a ticket closed where no run merged it (by
  hand, or by a worker after its run ended) still has its work only on its branch, so the tickets it
  blocks wait. No label is involved. Look at the branch with the user: merge it into the base
  (below), or delete it if its work isn't wanted (`git branch -D wt/<id>`).
- `<id> is left running in tab <tab> and labelled 'unmerged'`, just before the last line: the run
  ended with that ticket's worker still on it (the worker that stopped the run, or any at
  `INTERRUPTED`). If the worker closes it after the run, the next run merges it; until then the
  label holds the tickets it blocks, and if no run can carry it over, until it is merged by hand
  (below). While the ticket isn't closed the label holds nothing.
- `left for the next run: <id> (asked <question>), <id> (left running: PAUSED)`, just before the
  last line: the workers saved in `.orchestra/run/state.json` for the next run. That run's `START`
  block says `carried over from the last run: …`, and then takes each up: `ANSWERED: <id> was closed
  in tab …` (or `<id>, which the last run left running (PAUSED), is closed in tab …`) when it adopts
  a worker, and merges its ticket as usual. One still waiting on its question, or left running with
  its worker idle in its tab, is left for the user and saved again.
- `<id>, carried over from the last run, is dropped: …`: its worktree is gone, its branch was merged
  by hand, or bd can't show the ticket. Nothing to do unless the line surprises you.
- `WORKER_GONE: <id> is in progress after …, but its worker is gone from tab <tab>`: a carried-over
  ticket's worker has gone (its tab closed, Herdr, tmux or cmux restarted) with no session to resume; said once,
  with a note on the ticket. Look at its worktree with the user: reopen the ticket
  (`bd update <id> --status open`) to run it again, or finish it by hand (below).
- `RESUMED: <id>'s worker is gone from tab <tab> with the ticket in progress; resuming its session
  …`: a Claude worker vanished mid-ticket, and orchestra started `claude --resume` in a new tab,
  once per ticket in a run. Nothing to do; the old tab is left open, and can be closed.
  `RESUMED: <id> is back, and its worker is gone from tab <tab>; …` is the same for a ticket back from
  its answered question; one back from a deferral gets a new worker instead.
- `<id>'s worker lost its name in tab <tab>; named it <name> again`: something the worker ran (in Herdr,
  a Claude Code started in its pane) took its agent name, and orchestra found it unnamed in its pane and
  named it again. Nothing to do; it is followed as before.
- `kept for a later run, outside this run's scope: …`: a `--ticket` run leaves the workers on other
  tickets saved for a run that takes them.
- `ASKED_UNMERGED`, `LEFT_UNMERGED`: a ticket asked (or carried over as left running) was closed in
  its tab, but the run stopped before merging it. It is labelled `unmerged`, and the next run
  merges it.
- `STATE_UNSAVED`: the run couldn't write `.orchestra/run/state.json` (the filesystem's
  error is on the line); the next run carries nothing over, so finish what it names by hand (below)
  once the workers are done.
- `LABEL_FAILED`: bd couldn't add or remove the `unmerged` label; run the command on the line.
- `solo ticket <id> is next: no new tickets start…` and `waiting for solo ticket <id> to finish`: a
  ticket labelled `solo` runs alone, so free slots wait until the running tickets finish, or until
  it does. Normal; the dashboard's title line shows `solo <id> next` or `solo <id> running`.
- `<id> footprint predicted: …` and `<id> footprint not predicted: …`: the predictor organ guessed the
  files of a ticket naming none, or couldn't. Normal; a ticket never waits for its prediction.
- `<id> footprint: …` (at dispatch, `(predicted)` for a guess) and `skipping <id>: touches <file or function>, like running
  <other>`: the ticket overlaps a running one, so a later ticket took the slot. Normal; it starts once
  nothing running overlaps it.
- `LIKELY_CONFLICT: <a> and <b> both edit <file>`: two running workers changed the same file, so the
  second to merge may end in `MERGE_CONFLICT`. Nothing to do yet; watch for it at merge.
- `CLEANUP_FAILED`: merged, but its worktree or branch couldn't be removed.
- `LONG_RUNNING`: a worker is still going after 2 hours and no ticket limit is set; the run keeps
  waiting. Look at its tab for a hung command.
- `DEFER_FAILED`: bd couldn't defer the ticket (its error is on the line), so it is still ready;
  the run leaves it alone. Once bd works again, defer it: `bd defer <id>`.
- `REOPEN_FAILED`: a ticket waiting on a question couldn't be put back in the queue, so it won't
  return once answered. Reopen it with the command on the line.
- `rebased … onto …, which moved on while it ran` and `'<check>' passes on the rebased …`: other
  tickets merged first; the branch was rebased and re-checked before merging. Normal with several
  at once. `wt/<id> is on top of <base> already: checking it with …` and `'<check>' passes on wt/<id>
  on top of <base>`: nothing merged meanwhile; the branch was checked as it is. Every merge is checked.
- `RESOLVING: wt/<id> conflicts with <base> in <files>; handed back to its worker`: the rebase
  stopped on conflicts and the ticket's own worker was asked to resolve it (the dashboard shows
  `⟳ resolving`). Normal; other tickets merge meanwhile. Leave its tab alone: orchestra checks the
  result itself (rebase finished, worktree clean, only the ticket's commits, check passing) and
  merges it, or sets it aside with `MERGE_CONFLICT`.
- `MERGE_CONFLICT`: closed, but its branch conflicts with work merged while it ran. Not merged:
  rebase it in its worktree, resolve, run the checks, merge by hand. The ticket stays closed. The
  parenthesis says why its worker didn't resolve it: `not handed back to its worker: …` (no check
  command, worker gone or busy, turned off) or `handed back to its worker, but …` (it timed out,
  left the rebase unfinished, made a commit of its own, or the check failed; its branch was put
  back as the ticket closed it, and `bd show <id>` has the same note).
- `INTERRUPTED: … (tab …, resolving conflicts: its rebase is left in progress …)`: the run stopped
  during a hand-back. The worker may still finish the rebase in its tab; once `git status` in its
  worktree shows no rebase, run the check there and merge the branch by hand (see below).
- `SETUP_FAILED`: the project's `setup` failed (or ran past `check_fast_timeout`) in a ticket's
  worktree before its worker could start. No worker started and the ticket is open, not set aside: it
  is taken again once a ticket merges, and setups failing ticket after ticket hold the run
  (`ENVIRONMENT`). Read the output the line names (`.orchestra/run/setup.log` in that worktree) with
  the user: a missing tool, the network or a registry, or a setup command that is wrong. A run that
  ends with such tickets still out names them in its last line (`…; SETUP_WAITS: …`).
- `CHECKS_FAILED`: closed, but the check command fails on its branch, rebased or not (output in the log).
  Not merged: fix in its worktree or reopen the ticket, with the user. `did not finish within 5m`
  means the check hung or ran past `check_fast_timeout` and was stopped: a hang, or a limit set too low,
  rather than a failing test.
- `FULL_CHECK_FAILED`: after a run that ended by itself and merged a ticket, `check_full`
  (`scripts/check-full.sh`) failed on the branch's head, in its own worktree, removed since. The main
  checkout is untouched. The whole output is in `.orchestra/run/check-full.log`, its end in the run
  report, and a P2 bug `Full check fails: <suite>` (label `check-full`) is filed for the suite, or
  noted when one is open already: it goes through the next run like any ticket. The suite is the
  last `SUITE: <name>` line the runners print; `did not finish within 60m` means it ran past
  `check_full_timeout`.
- `REBASE_FAILED`: a returning ticket's branch conflicts with the base branch, so it was deferred
  without starting a worker. Rebase it in its worktree, resolve, then `bd undefer <id>`.
- `RUN_FILES_OUTSIDE`: the ticket's worktree has `.orchestra` or `.orchestra/run` as a symlink,
  wherever it points, or a file orchestra writes in `.orchestra/run` as a symlink leading out of the
  worktree, so it was deferred without starting a worker: orchestra writes those files only in the
  worktree's own `.orchestra/run` folder, which git ignores. Look at the link with the user, remove
  it, then `bd undefer <id>`.
- `REBASE_SKIPPED`: a returning ticket's worktree had uncommitted changes, so its branch wasn't
  rebased before its worker started; it is rebased when it merges.
- `PERMISSION_MODE: claude --help doesn't list the permission mode <mode>` (or `cannot read claude
  --help …`), in the `START` block: the workers start in the mode Claude Code's settings give, without
  `--permission-mode`. Tell the user; `claude update` may bring the mode.
- `SPEC: warning: …`, before a spec run's loop: what orchestra's check found amiss in the tree the
  breakdown filed (issues outside the root epic or without the spec's metadata, tickets without files
  metadata or acceptance criteria, a blocks cycle, which keeps its tickets from ever starting). The run
  goes on; relay it, and for a cycle suggest the `bd dep remove` that breaks it.

## Where to look

| What | Where |
|---|---|
| every event, as JSON with its kind and ticket | `.orchestra/run/events.jsonl` in the main checkout |
| every event, with raw tool errors | the log file (see the layout table) |
| what finished, what was set aside and why, what needs the user | the latest run report |
| the whole output of the last full check that failed | `.orchestra/run/check-full.log` in the main checkout |
| why a ticket was deferred | `bd show <id>`: the worker's notes, orchestra's notes, `Triage (orchestra): cause = …` |
| what a spec's breakdown assumed where the spec left a choice open | `bd show <root epic>`: `Assumptions` in its notes (`Decisions` in its description after `--refine`); `Assumed:` in each ticket that depends on one |
| a worker's screen and final message | its tab, labelled with the ticket ID (a tmux window or cmux workspace named with it) |
| a ticket's work | worktree `<repo>-worktrees/<id>`, branch `wt/<id>`: `git log --oneline <base>..wt/<id>` |
| the prompt a worker was started with | `.orchestra/run/prompt.md` in its worktree |
| the last tool a worker used (its `testing` / `editing` / `reading` status) | `.orchestra/run/activity.json` in its worktree, written by the hooks in `.orchestra/run/hooks.json`; the end of its last turn, `.orchestra/run/turn.json` |
| the workers the last run left, for the next to carry on with | `.orchestra/run/state.json` in the main checkout |
| questions for the user | `bd human list` |

## Finishing a ticket by hand

A worker that finishes after the run has stopped is merged by the next run, which carries it over
(`.orchestra/run/state.json`). Finish a ticket by hand only when no run will: after a quit at once,
a `STATE_UNSAVED` line, or a carried-over worker dropped or `WORKER_GONE`, or when the user wants it
merged before the next run. The ticket is closed but nothing merged it; if the run labelled it
`unmerged` as it ended, the tickets it blocks wait until it is merged (quote `<worktree>` when its
path holds a space, as orchestra's own commands do):

1. Check it: `bd show <id>` is closed, `git log --oneline <base>..wt/<id>` has a commit naming it,
   and `git -C <worktree> status --porcelain` is empty.
2. If the base branch has moved since the branch was cut, rebase it in the worktree:
   `git -C <worktree> rebase <base>`.
3. Run the project's check command in the worktree.
4. From the main checkout, on the base branch:
   `git merge --ff-only wt/<id>`, `git worktree remove <worktree>`, `git branch -d wt/<id>`,
   `bd label remove <id> unmerged` (if it has the label), and close its tab
   (`herdr tab close <tab>`, `tmux kill-window -t <tab>`, or `cmux close-workspace --workspace <uuid>`).

A set-aside ticket whose worktree has no commits and no changes can be cleaned up the same way,
without the merge. A worktree with work in it is the user's call.

## A new build of orchestra

When the user asks for a new build, build orchestra from its own clone (the repository of module
`github.com/noesis-sol/orchestra`), on the branch runs merge into, with `git status` clean so the
version names a commit:

```
go build -o /tmp/orchestra ./cmd/orchestra && /tmp/orchestra --version   # in the clone, as yourself
orchestra --version; command -v orchestra    # the installed build, and where it is
```

A build from a clone names its commit: `v0.4.1-0.20261005062236-d70b642867ad` is commit
`d70b642867ad` (`git rev-parse --short=12 HEAD` in the clone), and `+dirty` at the end means the tree
had uncommitted changes. Installing goes over the path `command -v orchestra` printed, so the old
build doesn't shadow the new one on `PATH`. Where that folder belongs to root (`/usr/local/bin`
usually does), the install needs `sudo`, which is the user's (below). Give them the line to run,
with `!` in front in Claude Code so it runs in the session; a run going keeps the build it started
with, and the next run starts the new one:

```
sudo install -m 755 /tmp/orchestra "$(command -v orchestra)" && orchestra --version
```

## Leave to the user

- `git push` and anything else that leaves the machine, such as opening or merging pull requests,
  unless they asked for it.
- Answers to questions (`human` tickets), to dialogs in worker tabs, and to a `--refine` session's
  questions.
- Scheduling runs: a crontab line or a launchd agent for a nightly spec run, unless they ask you to
  install it.
- `orchestra init --force`, raising `concurrent` above 1, and installing `orchestra` where it needs
  `sudo`.
