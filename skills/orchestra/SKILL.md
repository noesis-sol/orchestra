---
name: orchestra
description: Run and look after orchestra, which works through a Beads backlog one ticket at a time with a worker (a coding agent) per Herdr tab and git worktree. Use when the user asks to set a project up for orchestra, launch or restart a run, follow one, or find out why a run stopped, why a ticket was deferred or where a worker's work went.
---

# orchestra

`orchestra` picks the highest-priority ticket from `bd ready`, gives it a git worktree
(`<repo>-worktrees/<ticket>` on branch `wt/<ticket>`) and a Herdr tab labelled with the ticket ID,
starts a worker there (a coding agent: Claude Code by default, with its tools and the project's
MCP servers) with the project's worker prompt, waits for it to settle, then reads the ticket's
status in Beads. A closed ticket with a commit naming it is fast-forwarded
into the branch the main checkout is on, and its worktree, branch and tab are removed. Anything else
is left for review, and the loop moves on or stops. It never pushes.

Two words, kept apart: **workers** do tickets, one per Herdr tab and worktree; **organs** are
orchestra's own one-shot advisers (triage, the predictor, the reviewer that writes the run report),
each a `claude -p` call with no tools and no MCP servers that only reads what it is given and
answers. Organs never change a ticket or the code.

## First, find out where the project stands

Run these before launching anything or answering questions about a run:

```
orchestra -version                          # installed, and which build
git rev-parse --show-toplevel               # the repository (use the main checkout, not a worktree)
git rev-parse --absolute-git-dir            # equal to --git-common-dir in the main checkout
git branch --show-current                   # finished tickets land on this branch
git worktree list                           # per-ticket worktrees still around
bd ready; bd list --status=in_progress      # what a run would pick up; what is claimed
bd human list                               # questions waiting for the user
lsof -t .orchestra/run/orchestra.lock       # is a run active here? its PID; nothing if not
cat .orchestra/run/orchestra.lock           # which run holds it (or held it last)
```

Run the last two in the main checkout. A run holds `.orchestra/run/orchestra.lock` for as long as it
runs, and the system lets go of it when orchestra exits, even killed, so only a running orchestra
has it open. The file is JSON: `pid`, `started`, `version`, `branch`, `ticket` (`--ticket`) or
`feature` (the `--feature` request), and `pane`, the Herdr pane it runs in. It stays after the run;
whether `lsof` lists a PID is what says a run is going. Runs in other repositories have their own.

Where the project's orchestra files are:

| The repository has | Layout | Prompt | Log | Reports |
|---|---|---|---|---|
| `.orchestra/worker-prompt.md` | current | `.orchestra/worker-prompt.md` | `.orchestra/orchestra.log` | `.orchestra/reports/` |
| only `.claude/worker-prompt.md` | before `orchestra init` | `.claude/worker-prompt.md` | `.claude/orchestrate.log` | `.claude/orchestrate-reports/` |
| neither | not set up | run `orchestra init` | | |

`-prompt` / `WORKER_PROMPT` can point elsewhere; the log's `START` line records what a run used.
`.orchestra/settings.json` holds the check command, `mcp_servers` (the MCP servers workers get, by
name; see [Workers' MCP servers](#workers-mcp-servers)) and `concurrent`, how many tickets run at the
same time by default (`--concurrent N` / `-c N` / `ORCHESTRA_CONCURRENT` overrides it for a run),
and optionally `ticket_limit`, how long a worker may go on before the run stops for it (`"2h"`;
`--ticket-limit` / `TICKET_LIMIT` overrides it, `0` for none), `check_timeout`, how long the check
command may run on a rebased ticket before it is stopped and the ticket set aside (`"5m"`, default
30m; `--check-timeout` / `ORCHESTRA_CHECK_TIMEOUT` overrides it), and `exclude_types`, the issue
types never dispatched (default `["epic"]`; `[]` dispatches every type), and `environment_hold`,
when workers failing the same way hold the run (default `{"count": 2, "window": "2m", "probe": "10m"}`;
`"count": 0` turns it off), and how long after a hold a worker without a ticket probes the machine
(`"probe": "0"` for no probe).

## Setting a project up

```
orchestra init --check "<the project's check command>" --concurrent 1
```

It writes `.orchestra/worker-prompt.md` from the built-in template (or moves an existing
`.claude/worker-prompt.md`, staged with `git mv`), adds `.orchestra/.gitignore`, and reports what is
missing (`bd`, `bd init`, `herdr`, `claude`), and writes `.orchestra/settings.json`. Ask the user
for the check command if you don't know it (the command that runs lint, build and tests) and how
many tickets to run at the same time, then pass both: run by an agent, `init` can't ask
interactively and would default to 1. Ask which MCP servers workers need for the project's work
(`claude mcp list` shows what this machine defines) and pass `--mcp a,b`, or `--mcp ""` for none;
without it, an agent's `init` leaves them unchosen and workers get every server. `--check-timeout 5m` sets the check's time limit (default
30m): a few times the check's usual running time. More than 1 needs checks that can run side by side; say so.
Where the project keeps a `CHANGELOG.md`, ask whether to add `CHANGELOG.md merge=union` to
`.gitattributes` (so tickets that each add an entry at the same spot don't conflict) and pass
`--changelog-union` or `--changelog-union=false`; without either, `init` leaves it alone.
Without `--check`, fill in the `<…>` placeholders in the prompt. It never replaces an existing prompt
unless given `--force`; don't pass `--force` without the user's say-so. Afterwards, show the user the prompt and commit `.orchestra/` (and `.gitattributes`, if it changed) if they agree.

## Launching a run

Requirements, checked by `orchestra` at startup (it lists every problem, exit code 2):

- inside a Herdr pane (`HERDR_ENV=1`). Worker tabs open in that pane's workspace, unless
  `--workspace ID` names another;
- in the main checkout, on a branch, not a detached HEAD;
- no uncommitted changes outside `.claude/`, `.beads/` and `.orchestra/`;
- no other run in the same repository. A second one stops before changing anything (a `--feature`
  request before it is screened) with `orchestra is already running in <repo> (pid …, since …,
  <branch>, pane …)`: follow that run in its pane instead, or ask the user before stopping it.

Run it on the branch finished tickets should land on. Unless the user says otherwise, propose a
batch branch from the main branch (`git switch -c batch/$(date +%F)`), so the work reaches main
through one pull request.

`orchestra` is a long-running terminal UI: never run it in your own shell tool. Start it in a pane
beside yours and keep your own pane free (the pane ID is read from Herdr's JSON with `jq`):

```
P=$(herdr pane split --current --direction right --cwd "$PWD" --no-focus \
    | jq -r .result.pane.pane_id)
herdr pane run "$P" "orchestra"
herdr pane wait-output "$P" --regex "dispatching|cannot start|READY_EMPTY" --timeout 60000
herdr pane read "$P" --source visible
```

Useful settings (environment variable or flag): `--concurrent N` / `-c N` (tickets at the same
time, overriding `settings.json`), `LIMIT` (tickets per run, default 40),
`DONE_SO_FAR` (count earlier tickets toward the limit), `TRIAGE=0` / `REVIEW=0` (no organs),
`ORGAN_MODEL`, `NOTIFY=0` (no macOS notifications), `PROMPT_AT_LAUNCH=0` (paste the prompt instead
of starting the worker with it), `--ticket-limit 2h` / `TICKET_LIMIT` (stop when a worker is still
going that long after dispatch; `0` for none, overriding `settings.json`), `--check-timeout 5m` /
`ORCHESTRA_CHECK_TIMEOUT` (the check's time limit, overriding `settings.json`). `orchestra -h` lists
them all.

When the user wants one piece of work finished (an epic and its children, a ticket broken into
subtickets) rather than the whole backlog, run `orchestra --ticket <id>` (or `ORCHESTRA_TICKET`):
only that ticket and its descendants are dispatched, each parent after its children, and follow-ups
join only when filed as its children (`bd create --parent <id>`). An epic is never dispatched;
orchestra says when it can be closed (`bd close <id>`) but doesn't close it. In every run, a ticket
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
name the same function (or, when one names none, the same small file), the higher-priority ticket
first, and changes nothing. Show the user the proposal; run `orchestra plan --apply` (which adds
them with `bd dep add`) only when they approve.

## While it runs

- **Leave the main checkout alone.** An uncommitted change outside `.claude/`, `.beads/` and
  `.orchestra/`, or a branch switch there, stops the run at the next ticket or merge, whichever
  comes first (`DIRTY_TREE`); a finished ticket is then left unmerged in its worktree for review.
  A commit on the base branch is picked up: running tickets are rebased onto it and checked again
  before they merge. Beads changes (`bd update`, `bd create`) are fine.
- **The prompt is read once, at startup.** Changes to it apply to the next run.
- **Follow it** in its pane, or with `tail -f` on the log. With several at once, the dashboard shows
  a box per worker (one line each in a short pane). Each worker is an agent named after its
  ticket, in a tab labelled with the ticket: `herdr agent get <name>`,
  `herdr agent read <name> --source visible` (its scrollback can only be read while it is idle).
  The name is the ticket ID lowercased, with anything other than letters, digits, `-` and `_`
  turned into `_` (`CalendarView-bl0.1` → `calendarview-bl0_1`); an ID over 32 characters is cut
  and ends in a short hash.
- **To wind the run down**, the user presses s in the dashboard and confirms with y: no new
  tickets start, the running ones finish and merge, and the run ends with `DRAINED` and exit 0.
  Asked by the user to do it for them, send `kill -USR1 <orchestra's pid>` (the PID from
  `lsof -t .orchestra/run/orchestra.lock`); don't press keys in its pane. Ctrl+C stops at once instead, leaving the workers running.
- **A `PROBE:` line means the run is still going**: it held for the environment, and after the
  wait it names, a worker without a ticket (tab `orchestra-probe`, in the main checkout) runs one
  command. `PROBE_OK` means the run takes tickets again; otherwise it ends with `ENVIRONMENT`. Don't
  restart it meanwhile; `kill -USR1` ends it during the wait, as `ENVIRONMENT`.
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

Workers of another agent kind (`--agent`) don't get `mcp_servers`; organs never get any server.

## When a ticket is set aside or the run stops

Read the run report first (`reports/<start time>.md`: Finished, Set aside, Needs you), then the log,
then the tickets. The final log line and the exit code say why the run ended:

| Last line | Exit | Meaning | What to do |
|---|---|---|---|
| `READY_EMPTY`, `LIMIT_REACHED` | 0 | queue empty, or limit reached | Read the report; the next step is usually the batch PR. |
| `DRAINED`, after a `DRAIN: stopping after …` line | 0 | the user pressed s in the dashboard (or orchestra got SIGUSR1): no new tickets started, the running ones finished and merged | As for `READY_EMPTY`; the queue may still hold tickets for the next run. A `DRAIN cancelled` line means the user took it back. |
| `…; SCOPE_DONE: …` | 0 | a `--ticket` run: the ticket and all its subtickets are merged (an epic is left to close) | Close an epic with `bd close <id>`; then the batch PR. |
| `…; SCOPE_OPEN: …` | 0 | a `--ticket` run with subtickets not done; each is named with why | Handle each reason: answer a question, merge or rebase an unmerged one, unblock or rerun. The report lists follow-ups filed outside the scope. |
| any of the lines below, after a `HOLD: …` line | as below | with several tickets at once, a stop first holds: no new tickets, the running ones finish | Handle the reason as below; the `HOLD` line names the ticket. |
| `PAUSED` | 3 | a worker was idle for 10 minutes with its ticket still `in_progress` | Read its tab. Relay any question to the user. If the worker finishes later, merge by hand (below). |
| `BLOCKED >4min` | 3 | a worker sat on an approval or question dialog | Show the user the dialog; don't answer it yourself. |
| `UNKNOWN >5min` | 3 | Herdr couldn't tell what a worker was doing for 5 minutes | Read its tab: it may be hung, or its status undetectable for its agent kind. Tell the user what you see. |
| `TICKET_LIMIT` | 3 | a worker was still going after the ticket limit | Read its tab: a hung command, or a big ticket. Tell the user; if the worker finishes later, merge by hand (below). |
| `ENVIRONMENT` | 7 | the last tickets' workers all failed at once (settled soon after dispatch without claiming or changing anything), or triage blamed the environment for each with high confidence: the machine, not the tickets | If the line says a probe failed too, read the probe's tab (`orchestra-probe`) first: the run already waited and tried once more. Check the machine: read the workers' tabs and the triage notes for the cause (a refused permission or safety check, a missing tool, the network). Tell the user what you find. Tickets that failed at once were reopened already; reopen triaged ones with `bd update <id> --status open` once the cause is fixed. Then restart the run. |
| `MERGE_FAILED` | 6 | the ticket's branch doesn't fast-forward after rebasing | Rare: something else changed the base. Rebase the worktree, check, merge by hand. |
| `DIRTY_TREE` | 5 | uncommitted changes in the main checkout, or it left its branch | `git status`. These are the user's changes: ask before touching them. |
| `START_FAILED`, `TAB_FAILED`, `WORKTREE_FAILED`, `AGENT_BUSY`, `AGENT_NAME_TAKEN`, `STATUS_UNREADABLE`, `READY_UNREADABLE`, `GIT_FAILED` | 4 | Herdr, Beads or git failed | `STATUS_UNREADABLE` and `READY_UNREADABLE` end with bd's error, `GIT_FAILED` with git's (it could not read the main checkout three times running); for the others the raw error is in the log, on lines without a timestamp just above. A worker may still be running: check its tab. |
| `INTERRUPTED` | 130 | the user pressed Ctrl+C, or orchestra got SIGTERM or SIGHUP (its terminal or pane closed) | The worker keeps running, and its ticket is labelled `unmerged` (below). If it finishes, merge by hand (below). If it leaves no work, reopen its ticket (`bd update <id> --status open`) and remove its empty worktree. |
| (printed, not logged) | 2 | setup problem | The terminal lists each problem and its fix. `orchestra is already running in …` names the run going in this repository: follow it in its pane. |

Lines about single tickets, which don't stop the run:

- `closed (…); merged into …`: done.
- `deferred by worker`, `still <status> -> noted and deferred`: set aside. A `triage` line follows
  with the triage organ's verdict (cause: environment, instructions or problem), also appended to
  the ticket's notes. `TRIAGE_FAILED` means triage itself failed.
- `PROMPT_FAILED`: the worker never started on its prompt; deferred, safe to reopen.
- `ASKED`: the worker asked the user a question, as a ticket labelled `human` that blocks the work.
  Only the user answers it: `bd human respond <question> --response "…"`. The ticket then returns to
  the queue, and its branch is rebased onto the current one when it is picked up. `ANSWERED` says it
  came back. If its worker is still in its tab (answered there, say), orchestra adopts it instead,
  or tells it, idle, that the question is answered, and merges its work as usual.
- `CLOSED_WITHOUT_COMMIT`: closed, but no commit names it, or its worktree has uncommitted changes.
- `<id> waits: <blocker> closed but not merged (…)`: a ready ticket held because a ticket blocking
  it isn't on the base branch yet. A ticket closed but left unmerged is labelled `unmerged`, which
  holds its dependents in later runs too; orchestra removes the label when it merges the ticket, or
  when a run starts and finds it merged by hand. If the ticket needs no merge after all, remove it:
  `bd label remove <id> unmerged`.
- `<id> is left running in tab <tab> and labelled 'unmerged'`, just before the last line: the run
  ended with that ticket's worker still on it (the worker that stopped the run, or any at
  `INTERRUPTED`). If the worker closes it after the run, nothing merges it, so the label holds the
  tickets it blocks until it is merged by hand (below) or dispatched again and merged. While the
  ticket isn't closed the label holds nothing.
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
  at once.
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
- `CHECKS_FAILED`: closed, but the check command fails on the rebased branch (output in the log).
  Not merged: fix in its worktree or reopen the ticket, with the user. `did not finish within 5m`
  means the check hung or ran past `check_timeout` and was stopped: a hang, or a limit set too low,
  rather than a failing test.
- `REBASE_FAILED`: a returning ticket's branch conflicts with the base branch, so it was deferred
  without starting a worker. Rebase it in its worktree, resolve, then `bd undefer <id>`.
- `RUN_FILES_OUTSIDE`: the ticket's worktree has `.orchestra` or `.orchestra/run` as a symlink,
  wherever it points, or a file orchestra writes in `.orchestra/run` as a symlink leading out of the
  worktree, so it was deferred without starting a worker: orchestra writes those files only in the
  worktree's own `.orchestra/run` folder, which git ignores. Look at the link with the user, remove
  it, then `bd undefer <id>`.
- `REBASE_SKIPPED`: a returning ticket's worktree had uncommitted changes, so its branch wasn't
  rebased before its worker started; it is rebased when it merges.

## Where to look

| What | Where |
|---|---|
| every event, with raw tool errors | the log file (see the layout table) |
| what finished, what was set aside and why, what needs the user | the latest run report |
| why a ticket was deferred | `bd show <id>`: the worker's notes, orchestra's notes, `Triage (orchestra): cause = …` |
| a worker's screen and final message | its Herdr tab, labelled with the ticket ID |
| a ticket's work | worktree `<repo>-worktrees/<id>`, branch `wt/<id>`: `git log --oneline <base>..wt/<id>` |
| the prompt a worker was started with | `.orchestra/run/prompt.md` in its worktree |
| the last tool a worker used (its `testing` / `editing` / `reading` status) | `.orchestra/run/activity.json` in its worktree, written by the hooks in `.orchestra/run/hooks.json` |
| questions for the user | `bd human list` |

## Finishing a ticket by hand

When a worker finished after the run had stopped (the ticket is closed but nothing merged it; the
run labelled it `unmerged` as it ended, so the tickets it blocks wait until it is merged):

1. Check it: `bd show <id>` is closed, `git log --oneline <base>..wt/<id>` has a commit naming it,
   and `git -C <worktree> status --porcelain` is empty.
2. If the base branch has moved since the branch was cut, rebase it in the worktree:
   `git -C <worktree> rebase <base>`.
3. Run the project's check command in the worktree.
4. From the main checkout, on the base branch:
   `git merge --ff-only wt/<id>`, `git worktree remove <worktree>`, `git branch -d wt/<id>`,
   `bd label remove <id> unmerged` (if it has the label), and close its tab
   (`herdr tab close <tab>`).

A set-aside ticket whose worktree has no commits and no changes can be cleaned up the same way,
without the merge. A worktree with work in it is the user's call.

## Leave to the user

- `git push` and anything else that leaves the machine, such as opening or merging pull requests,
  unless they asked for it.
- Answers to questions (`human` tickets) and to dialogs in worker tabs.
- `orchestra init --force`, raising `concurrent` above 1, and installing `orchestra` where it needs
  `sudo`.
