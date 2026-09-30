---
name: orchestra
description: Run and look after orchestra, which works through a Beads backlog one ticket at a time with a coding agent per Herdr tab and git worktree. Use when the user asks to set a project up for orchestra, launch or restart a run, follow one, or find out why a run stopped, why a ticket was deferred or where a worker's work went.
---

# orchestra

`orchestra` picks the highest-priority ticket from `bd ready`, gives it a git worktree
(`<repo>-worktrees/<ticket>` on branch `wt/<ticket>`) and a Herdr tab labelled with the ticket ID,
starts a coding agent (a "worker") there with the project's worker prompt, waits for it to settle,
then reads the ticket's status in Beads. A closed ticket with a commit naming it is fast-forwarded
into the branch the main checkout is on, and its worktree, branch and tab are removed. Anything else
is left for review, and the loop moves on or stops. It never pushes.

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
pgrep -x orchestra                          # is a run active?
```

Where the project's orchestra files are:

| The repository has | Layout | Prompt | Log | Reports |
|---|---|---|---|---|
| `.orchestra/worker-prompt.md` | current | `.orchestra/worker-prompt.md` | `.orchestra/orchestra.log` | `.orchestra/reports/` |
| only `.claude/worker-prompt.md` | before `orchestra init` | `.claude/worker-prompt.md` | `.claude/orchestrate.log` | `.claude/orchestrate-reports/` |
| neither | not set up | run `orchestra init` | | |

`-prompt` / `WORKER_PROMPT` can point elsewhere; the log's `START` line records what a run used.
`.orchestra/settings.json` holds the check command and `concurrent`, how many tickets run at the
same time by default (`--concurrent N` / `-c N` / `ORCHESTRA_CONCURRENT` overrides it for a run).

## Setting a project up

```
orchestra init --check "<the project's check command>" --concurrent 1
```

It writes `.orchestra/worker-prompt.md` from the built-in template (or moves an existing
`.claude/worker-prompt.md`, staged with `git mv`), adds `.orchestra/.gitignore`, and reports what is
missing (`bd`, `bd init`, `herdr`, `claude`), and writes `.orchestra/settings.json`. Ask the user
for the check command if you don't know it (the command that runs lint, build and tests) and how
many tickets to run at the same time, then pass both: run by an agent, `init` can't ask
interactively and would default to 1. More than 1 needs checks that can run side by side; say so.
Without `--check`, fill in the `<…>` placeholders in the prompt. It never replaces an existing prompt
unless given `--force`; don't pass `--force` without the user's say-so. Afterwards, show the user the prompt and commit `.orchestra/` if they agree.

## Launching a run

Requirements, checked by `orchestra` at startup (it lists every problem, exit code 2):

- inside a Herdr pane (`HERDR_ENV=1`). Worker tabs open in that pane's workspace, unless
  `--workspace ID` names another;
- in the main checkout, on a branch, not a detached HEAD;
- no uncommitted changes outside `.claude/`, `.beads/` and `.orchestra/`.

Run it on the branch finished tickets should land on. Unless the user says otherwise, propose a
batch branch from the main branch (`git switch -c batch/$(date +%F)`), so the work reaches main
through one pull request.

`orchestra` is a long-running terminal UI: never run it in your own shell tool. Start it in a pane
beside yours and keep your own pane free:

```
P=$(herdr pane split --current --direction right --cwd "$PWD" --no-focus \
    | python3 -c 'import json,sys; print(json.load(sys.stdin)["result"]["pane"]["pane_id"])')
herdr pane run "$P" "orchestra"
herdr pane wait-output "$P" --regex "dispatching|cannot start|READY_EMPTY" --timeout 60000
herdr pane read "$P" --source visible
```

Useful settings (environment variable or flag): `--concurrent N` / `-c N` (tickets at the same
time, overriding `settings.json`), `LIMIT` (tickets per run, default 40),
`DONE_SO_FAR` (count earlier tickets toward the limit), `TRIAGE=0` / `REVIEW=0` (no organs),
`ORGAN_MODEL`, `NOTIFY=0` (no macOS notifications), `PROMPT_AT_LAUNCH=0` (paste the prompt instead
of starting the worker with it). `orchestra -h` lists them all.

## While it runs

- **Leave the main checkout alone.** An uncommitted change outside `.claude/`, `.beads/` and
  `.orchestra/` stops the run at the next ticket (`DIRTY_TREE`). A commit or branch switch there
  makes the running ticket's merge fail (`MERGE_FAILED`). Beads changes (`bd update`, `bd create`)
  are fine.
- **The prompt is read once, at startup.** Changes to it apply to the next run.
- **Follow it** in its pane, or with `tail -f` on the log. With several at once, the dashboard shows
  a box per worker (one line each in a short pane). Each worker is an agent named after its
  ticket, in a tab with that label: `herdr agent get <ticket>`,
  `herdr agent read <ticket> --source visible` (its scrollback can only be read while it is idle).
- **Don't type into a worker's tab or press keys on its dialogs** unless the user asks; Enter on a
  dialog picks an option (on Claude Code's trust dialog, "No, exit").

## When a ticket is set aside or the run stops

Read the run report first (`reports/<start time>.md`: Finished, Set aside, Needs you), then the log,
then the tickets. The final log line and the exit code say why the run ended:

| Last line | Exit | Meaning | What to do |
|---|---|---|---|
| `READY_EMPTY`, `LIMIT_REACHED` | 0 | queue empty, or limit reached | Read the report; the next step is usually the batch PR. |
| any of the lines below, after a `HOLD: …` line | as below | with several tickets at once, a stop first holds: no new tickets, the running ones finish | Handle the reason as below; the `HOLD` line names the ticket. |
| `PAUSED` | 3 | a worker was idle for 10 minutes with its ticket still `in_progress` | Read its tab. Relay any question to the user. If the worker finishes later, merge by hand (below). |
| `BLOCKED >4min` | 3 | a worker sat on an approval or question dialog | Show the user the dialog; don't answer it yourself. |
| `MERGE_FAILED` | 6 | the ticket's branch doesn't fast-forward after rebasing | Rare: something else changed the base. Rebase the worktree, check, merge by hand. |
| `DIRTY_TREE` | 5 | uncommitted changes in the main checkout, or it left its branch | `git status`. These are the user's changes: ask before touching them. |
| `START_FAILED`, `TAB_FAILED`, `WORKTREE_FAILED`, `AGENT_BUSY`, `AGENT_NAME_TAKEN`, `STATUS_UNREADABLE`, `READY_UNREADABLE` | 4 | Herdr, Beads or git failed | The raw error is in the log, on lines without a timestamp just above. A worker may still be running: check its tab. |
| `INTERRUPTED` | 130 | the user pressed Ctrl+C | The worker keeps running. If it leaves no work, reopen its ticket (`bd update <id> --status open`) and remove its empty worktree. |
| (printed, not logged) | 2 | setup problem | The terminal lists each problem and its fix. |

Lines about single tickets, which don't stop the run:

- `closed (…); merged into …`: done.
- `deferred by worker`, `still <status> -> noted and deferred`: set aside. A `triage` line follows
  with the triage organ's verdict (cause: environment, instructions or problem), also appended to
  the ticket's notes. `TRIAGE_FAILED` means triage itself failed.
- `PROMPT_FAILED`: the worker never started on its prompt; deferred, safe to reopen.
- `ASKED`: the worker asked the user a question, as a ticket labelled `human` that blocks the work.
  Only the user answers it: `bd human respond <question> --response "…"`. The ticket then returns to
  the queue, and its branch is rebased onto the current one when it is picked up.
- `CLOSED_WITHOUT_COMMIT`: closed, but no commit names it, or its worktree has uncommitted changes.
- `CLEANUP_FAILED`: merged, but its worktree or branch couldn't be removed.
- `rebased … onto …, which moved on while it ran` and `'<check>' passes on the rebased …`: other
  tickets merged first; the branch was rebased and re-checked before merging. Normal with several
  at once.
- `MERGE_CONFLICT`: closed, but its branch conflicts with work merged while it ran. Not merged:
  rebase it in its worktree, resolve, run the checks, merge by hand. The ticket stays closed.
- `CHECKS_FAILED`: closed, but the check command fails on the rebased branch (output in the log).
  Not merged: fix in its worktree or reopen the ticket, with the user.
- `REBASE_FAILED`, `REBASE_SKIPPED`: a returning ticket's branch couldn't be brought up to date.

## Where to look

| What | Where |
|---|---|
| every event, with raw tool errors | the log file (see the layout table) |
| what finished, what was set aside and why, what needs the user | the latest run report |
| why a ticket was deferred | `bd show <id>`: the worker's notes, orchestra's notes, `Triage (orchestra): cause = …` |
| a worker's screen and final message | its Herdr tab, labelled with the ticket ID |
| a ticket's work | worktree `<repo>-worktrees/<id>`, branch `wt/<id>`: `git log --oneline <base>..wt/<id>` |
| the prompt a worker was started with | `.orchestra/run/prompt.md` in its worktree |
| questions for the user | `bd human list` |

## Finishing a ticket by hand

When a worker finished after the run had stopped (the ticket is closed but nothing merged it):

1. Check it: `bd show <id>` is closed, `git log --oneline <base>..wt/<id>` has a commit naming it,
   and `git -C <worktree> status --porcelain` is empty.
2. If the base branch has moved since the branch was cut, rebase it in the worktree:
   `git -C <worktree> rebase <base>`.
3. Run the project's check command in the worktree.
4. From the main checkout, on the base branch:
   `git merge --ff-only wt/<id>`, `git worktree remove <worktree>`, `git branch -d wt/<id>`,
   and close its tab (`herdr tab close <tab>`).

A set-aside ticket whose worktree has no commits and no changes can be cleaned up the same way,
without the merge. A worktree with work in it is the user's call.

## Leave to the user

- `git push` and anything else that leaves the machine, such as opening or merging pull requests,
  unless they asked for it.
- Answers to questions (`human` tickets) and to dialogs in worker tabs.
- `orchestra init --force`, raising `concurrent` above 1, and installing `orchestra` where it needs
  `sudo`.
