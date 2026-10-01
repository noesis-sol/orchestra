# Changelog

All notable changes to orchestra are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- A finished ticket whose rebase onto work merged while it ran stops on
  conflicts goes back to its own worker instead of straight to review: the
  rebase is left stopped in its worktree, and the worker, still idle in its tab,
  is asked to resolve it, run the check command and finish the rebase, and
  nothing else. The log says `RESOLVING: …` and the dashboard `⟳ resolving`;
  other finished tickets merge meanwhile. Orchestra then checks the result
  itself (rebase finished, worktree clean, only the ticket's own commits, check
  passing) and merges it. A worker that times out (20 minutes,
  `"resolve_timeout"`), leaves the rebase unfinished, commits anything else or
  fails the check has its branch put back, and the ticket is set aside with
  `MERGE_CONFLICT` as before, saying what was tried. Without a check command, or
  with the worker gone, nothing is handed back. `"resolve_conflicts": false` in
  `.orchestra/settings.json` or `--resolve-conflicts=false` turns it off.
- After a run holds for the environment, it probes the machine once the running
  tickets finish: 10 minutes later one worker without a ticket is started in
  the main checkout and asked to run a single command. If it does, the log and
  a notification say `PROBE_OK: …; taking tickets again`, and the run goes on
  with the reopened tickets. If not, the run ends with `ENVIRONMENT` (exit code
  7) as before, the line saying how the probe failed and its tab left open. A
  run probes once; a second hold ends it. `"environment_hold": {"probe": "30m"}`
  in `.orchestra/settings.json` changes the wait, and `"probe": "0"` turns the
  probe off.
- **s** in the dashboard stops the run after its running tickets: a box asks
  `Stop after the running tickets?`, naming them, and **y** confirms (**n** or
  **Esc** closes it). No new ticket starts from any path; the running ones
  finish and merge as usual, and the run ends with `DRAINED after <n> tickets`,
  exit code 0, then triage and the report, whose first sentence says so. The
  log says `DRAIN: stopping after the 2 running tickets (…), asked from the
  dashboard`; the title line shows `· stopping after current`, and **s** again
  takes it back (`DRAIN cancelled`). Ctrl+C still stops at once. SIGUSR1 does
  the same without the question, for `-plain` and scripts. Before, the only way
  to end a run from the dashboard was Ctrl+C, which left finished work unmerged.
- A ready ticket that names no files, functions or area labels has its files
  predicted by a new organ, in the background, from the ticket and `git
  ls-files`; the guess is cached as `predicted_files` metadata and keeps it
  apart from running tickets that touch those files. Dispatch never waits for
  it. Off without `claude`, with one ticket at a time, and with `"footprint":
  false`.
- `orchestra plan` proposes blocks links between open tickets that touch the
  same code, so they run one after the other: two tickets that name the same
  function, or, when either names none, the same file of at most 200 lines. The
  higher-priority ticket (then the older one) goes first; pairs already ordered
  are left alone, and tickets on one function form a chain. It prints the
  proposal and changes nothing; `--apply` adds the links with `bd dep add`.
- The worker prompt asks workers to link a follow-up that touches the same
  files or functions as another open ticket (`--deps blocked-by:<id>` or
  `related:<id>`).
- A run holds when the machine, not the tickets, fails its workers: when 2
  tickets in a row had workers that settled within 2 minutes of dispatch
  without claiming the ticket or changing anything, or were blamed on the
  environment with high confidence by triage. No new tickets start, the running
  ones finish, and the run ends with `ENVIRONMENT: …; check the machine, then
  restart` (exit code 7), logged and notified once. Tickets whose workers failed
  at once are reopened rather than left deferred, keeping their notes. Before,
  a safety classifier that was down for a few minutes had every worker give up,
  and each ticket was deferred in turn. `"environment_hold": {"count": 2,
  "window": "2m"}` in `.orchestra/settings.json` sets the thresholds; `"count":
  0` turns it off.
- `orchestra --ticket <id>` (or `ORCHESTRA_TICKET`) runs one ticket and its
  subtickets, at any depth, and nothing else: an epic and its children, say, or
  a ticket someone broke up. Follow-ups filed as its children (`bd create
  --parent <id>`, which workers are asked to use) join the run; others wait for
  a later run, and the log and run report list them. The run ends with
  `SCOPE_DONE` when everything in it is merged, or `SCOPE_OPEN` naming each
  subticket not done and why (blocked outside the scope, set aside, waiting on
  a question). An unknown, closed or question ticket is a setup problem. The
  START line, the dashboard's title and the run report show the scope.
- Tickets that touch the same code no longer run side by side. A ticket's
  footprint is what its text names (paths such as `internal/dispatch/merge.go`
  or `run.go`, checked against `git ls-files`, and functions such as
  `Loop.merge` or `refreshBranch()`), its `area:<name>` labels and its `files`
  metadata; a running ticket's also grows with every file its worker edits,
  which Claude workers now report through their hooks. A free slot goes to the
  highest-priority ready ticket that overlaps no running ticket (the same
  function when both name functions, else the same file, or the same area),
  logged once as `skipping <id>: touches Loop.merge, like running <other>`, and
  stays empty if every ready ticket overlaps. Each dispatch logs the ticket's
  footprint, and two running workers editing the same file are reported once
  as `LIKELY_CONFLICT`. Tickets naming nothing run as before. `"footprint":
  false` in `.orchestra/settings.json` turns it off.
- Fewer rebase conflicts between tickets that run side by side. The worker
  prompt asks for new tests in a new file named after the feature, new struct
  fields, constants and helpers next to the code they belong to rather than at
  the end of a list, and changelog entries as new lines. Where the project keeps
  a `CHANGELOG.md`, `orchestra init` offers to add `CHANGELOG.md merge=union` to
  `.gitattributes` (or takes `--changelog-union`), so git keeps both tickets'
  entries instead of stopping on a conflict; orchestra's own repository has it.
- Tickets labelled `solo` run alone, for work that restructures code every
  other ticket touches (splitting a shared file, say) and would conflict with
  anything beside it. One starts only when no other ticket is running, and
  nothing new starts while it runs (logged once: `waiting for solo ticket <id>
  to finish`). One next in priority while others run holds back new starts
  behind it, so it isn't starved, and starts as soon as they finish (`solo
  ticket <id> is next: …`). Its dispatch line says `dispatching solo`, and the
  dashboard's title line shows `solo <id> next` or `solo <id> running`. With
  one ticket at a time nothing changes. The worker prompt asks for the label on
  a follow-up that restructures shared code.
- `"check_timeout"` in `.orchestra/settings.json`: how long the check command
  may run on a rebased ticket, such as `"5m"` or `"45m"` (default 30 minutes,
  which was fixed before). The merge queue waits on the check, so a hung one
  held every finished ticket; a project with a quick check can now stop it
  sooner. `--check-timeout` / `ORCHESTRA_CHECK_TIMEOUT` overrides it for a run,
  and `orchestra init` asks for it (or takes `--check-timeout`). A check
  stopped at the limit is reported as `CHECKS_FAILED: … '<check>' did not
  finish within 5m on …` instead of a bare failure, and the `START` line
  records the limit.
- `"exclude_types"` in `.orchestra/settings.json`: the issue types a run never
  takes from `bd ready`, such as `["epic", "decision", "milestone"]`. Without
  it, epics alone are left out; `[]` dispatches every type.
- A worker's status says what it is doing: `testing` (yellow) while it runs the
  project's check command or a test runner, `editing` or `reading`, and
  `working` otherwise. Claude workers report each tool they use through hooks
  loaded for them alone (`claude --settings .orchestra/run/hooks.json`, which
  writes `.orchestra/run/activity.json`); the project's and the user's own
  settings are untouched.
- Several tickets at once. `.orchestra/settings.json` (written by `init`, which
  asks) holds the default; `--concurrent N` / `-c N` / `ORCHESTRA_CONCURRENT`
  overrides it for a run. Tickets are never handed out twice; finished tickets
  merge one at a time, rebased and re-checked with the settings' check command
  when others merged meanwhile (`MERGE_CONFLICT` and `CHECKS_FAILED` leave them
  for review); git writes to the main repository take a lock; a stop is logged
  as `HOLD` and lets the running tickets finish. The dashboard shows a box per
  worker and a Workers row, and gives way in short panes (one line per worker,
  then no tickets table, then totals on one line).
- The dashboard's totals are one strip (a column per total) above the tickets
  table, 4 lines instead of 10; the branch, running time and "stopping" moved
  to the title line, which shows an untagged build as `v0.1.2-dev 09ffc84`.
- No more `WORKSPACE=`: worker tabs open in the Herdr workspace orchestra
  runs in (from `HERDR_WORKSPACE_ID`, or Herdr itself). `--workspace ID` puts
  them in another. The `WORKSPACE` environment variable is no longer read.
- `orchestra init` asks with a form in a terminal (built with Charm's `huh`):
  the check command, pre-filled from the settings or found in an existing
  prompt, and the tickets at the same time. It prints each step in colour, the
  prerequisites on one line, and a Next box with only what's left and the
  exact command to start a run. Without a terminal it asks nothing.
- `.orchestra/settings.json`: the check command and the default number of
  tickets at the same time. `orchestra init` flags are now `--check`,
  `--concurrent` / `-c` and `--force`.

- `skills/orchestra/SKILL.md`, an optional skill for coding agents: how to
  check a project's state, launch and follow a run, find out why a ticket was
  set aside or a run stopped, and finish a ticket by hand.

- `orchestra init` sets up `.orchestra/` in a project: the worker prompt from
  the built-in template (`-check` fills in the check command) or moved from
  `.claude/worker-prompt.md`, a `.gitignore`, and a check of `bd`, `.beads`,
  `herdr` and `claude`. It never replaces an existing prompt without `-force`.

- Questions for the maintainer. A worker that needs a decision asks it as its
  own ticket, labelled `human`, that blocks the work ticket, and stops. The
  orchestrator never dispatches a question, shows the ticket as "? for you"
  with a "Needs you" count, and carries on; the report says how to answer.
  `bd human respond <question>` answers it, and the ticket returns to the queue
  by itself. The built-in worker prompt has the commands.
- `internal/project/worker-prompt.md`, the worker prompt template built into
  the binary.

### Changed

- The dashboard heads the boxes of the tickets being worked on with a faint
  `Current` label, like the tickets table's `Tickets` header, in the boxed and
  the one-line-per-worker layouts and over the `picking the next ticket…` box.
  It is the first thing left out when the pane is too short.
- A run winding down after **s** → **y** (or SIGUSR1) says so on its own line
  above the dashboard's hint, in the stop colour:
  `■ Stopping after the 2 running tickets finish (…): no new tickets will start`,
  updated as tickets finish and wrapped rather than cut in a narrow pane (only
  the IDs are shortened). It replaces `· stopping after current` on the title
  line, which a narrow pane cut off. The hint reads `s cancels the stop`
  instead of `s keeps going`, the queue count is marked `held`, and the title
  line puts `stopping` and `solo` before the branch and time. The question, the
  line and the `DRAIN` log line use the same words.
- The built-in worker prompt tells workers never to stop processes by name or
  pattern (`pkill -f dispatch.test` from one worker ended another's check with
  `signal: terminated`), only the ones they started, by PID.
- Parents run last: a ticket whose subtickets aren't all closed and merged
  waits for them, in every run. It used to be dispatched, and its worker, unable
  to close it while its children were open, ended paused or deferred. An epic
  is still never dispatched; once its last subticket merges, the log says it
  can be closed with `bd close <id>`.
- The dashboard's totals strip drops its "Picked up" column (N of LIMIT), and
  the one-line form its `picked N/LIMIT` count: the tickets table lists every
  ticket picked up. The `[3/40]` count in the log lines is unchanged.

- Everything orchestra owns in a project lives in `.orchestra/`: the prompt
  (`worker-prompt.md`), the log (`orchestra.log`), reports (`reports/`) and
  per-ticket files (`run/`). Projects not yet set up with `orchestra init` keep
  using their `.claude/` files. The per-ticket launch prompt moves to
  `.orchestra/run/prompt.md`; the old `/.orchestra/` entry in `info/exclude`,
  which would hide the committed prompt, is narrowed to `/.orchestra/run/`.

- Claude workers start with their prompt instead of having it pasted in. The
  prompt goes to a file in the ticket's worktree (kept out of git), and the
  worker is started with
  a one-line instruction to follow it; Herdr can't pass line breaks.
  `-prompt-at-launch=false` / `PROMPT_AT_LAUNCH=0` pastes it as before.
- An idle worker whose ticket is still in progress gets 10 minutes to resume
  before the run pauses: it is usually waiting on its own background command.
- A returning ticket's branch is rebased onto the current branch before its
  worker starts, so its merge can fast-forward.
- A returning ticket's earlier worker is renamed (`<ticket>-1`, …) so the new
  worker can take the ticket's name; its tab is left open.
- Error messages in the log cut long arguments short.
- Workers given their prompt at launch are started by typing the command into
  their tab and named as soon as Herdr recognises them, a few seconds instead
  of ~24. `herdr agent start` waits for the agent to look ready for input,
  which such a worker never does, so it always timed out, once for 15
  minutes. It remains the fallback, and a worker whose start timed out is
  adopted (named after its ticket) instead of failing the run with
  `START_FAILED`.
- A worker launched with its prompt that Herdr takes over a minute to
  recognise is still adopted, for up to two more minutes, instead of a second
  one being started in its tab and the prompt pasted into it again. The
  fallback start happens only once the tab plainly holds no agent.
- The dashboard clears the screen when it starts, so it begins at the top;
  earlier output stays in the terminal's scrollback. Plain mode doesn't clear.
- The active ticket's title wraps onto up to three lines instead of being cut
  off after one.
- The built-in worker prompt asks for ticket and question titles of at most 60
  characters, with details in the description.
- The built-in worker prompt: a ticket that only CI can verify is closed once
  the local checks pass, with an "Awaits CI" note, instead of deferred; the
  batch's pull request runs every CI job.
- A ticket closed but left unmerged (`MERGE_CONFLICT`, `CHECKS_FAILED`,
  `CLOSED_WITHOUT_COMMIT`, …) is labelled `unmerged`, so the tickets it blocks
  wait in later runs too, not only in the run that set it aside; bd ready
  counts a closed blocker as done. The label goes when orchestra merges the
  ticket, or at the start of a run that finds it merged by hand (its branch on
  the base with a commit naming it, or, with the branch deleted, such a commit
  on the base). `LABEL_FAILED` says when bd can't add or remove it.
- orchestra builds with Go 1.26.8 (`toolchain` in go.mod; an older Go fetches
  it). Go 1.26.0–1.26.4 on Apple Silicon can leave a process forked by a
  `-race` build spinning before exec, which hung the test suite
  ([golang/go#79804](https://github.com/golang/go/issues/79804)); Go 1.26 is
  still the minimum.
- A worker that has only just started is no longer taken for one that has
  finished. Herdr can show a worker as idle while it starts up, with its ticket
  still open, and its ticket was deferred while it went on to do the work,
  which was then never merged. A Claude worker now counts as settled only at
  its Stop hook (the end of its turn); while its last hook was a tool use it is
  mid-turn whatever Herdr says, for up to 10 minutes. Without hooks, or before
  its first one, an idle worker gets 3 minutes from its start to claim its
  ticket. The log says what decided it: `<ticket> settled: Stop hook at
  20:48:39`, `… idle 3m after it started, with the ticket still open`. The
  environment hold counts a worker by when it went idle, so the grace doesn't
  hide one that failed at once.
- Every `bd`, `git` and `herdr` command orchestra runs stops on Ctrl+C and has
  a time limit: 30 seconds for a read or a Herdr call, 2 minutes for a git
  write (worktree, rebase, merge, branch) or a `bd` update, and Herdr's own
  waits their timeout with 30 seconds to spare. A hung command (`bd` waiting on
  Dolt's lock, git on a lock or a hook, Herdr restarting) used to hold its
  worker for good; it now fails with `<command>: timed out after 30s`, reported
  like any failure of it (`STATUS_UNREADABLE`, `HERDR_FAILED`, `GIT_FAILED`, …).
  After Ctrl+C the run waits for its workers to return rather than giving up
  after 10 seconds and leaving them behind; a merge already under way, and the
  notes after it, finish first, so the repository is never left half merged.
  A worker not back within a second is named under the `INTERRUPTED` line (in
  `-plain`, above it), with what it is finishing: `waiting for <ticket>'s merge
  to finish…`, its `worktree setup`, or its `last command`, which stops by its
  time limit.

### Fixed

- A panic while working on one ticket (a nil pointer, an index out of range)
  no longer kills orchestra and leaves the other workers running unsupervised:
  the run holds with `PANIC in <ticket>: <value>; its worktree and tab … are
  left for review`, the running tickets finish and merge, and the run ends with
  that line (exit code 4) and its report. The stack is in the log. A panic in
  triage only fails that ticket's triage (`TRIAGE_FAILED … panic: …`), one in
  the footprint predictor only that prediction, and one watching a starting
  worker only stops its status showing (`WATCH_FAILED`). A panic in the loop
  itself still ends orchestra, now with the dashboard's terminal restored.

## [0.1.1] - 2026-09-29

### Changed

- The dashboard's totals no longer show the worker's Herdr tab ID. The tab is
  labelled with the ticket ID in Herdr, and the ID stays in the log and the
  run report.

## [0.1.0] - 2026-09-29

First release: a Go rewrite of `orchestrate.sh`, which works through a Beads
backlog one ticket at a time, one coding agent per Herdr tab and git worktree.

### Added

- The orchestration loop from `orchestrate.sh`, with the same environment
  variables, log file (`.claude/orchestrate.log`) and exit codes. Every
  setting is also a flag; `-h` lists them and `-version` prints the version.
- A Bubble Tea dashboard, updated in place: the run's totals, a table of its
  tickets (picked up with their title, completed with only their merged
  commit, deferred with the reason), and the active ticket with the worker's
  status, elapsed time and latest action. It stays on screen as the run's
  summary. Colours are exact, so terminal themes can't remap them. Plain log
  lines when not on a terminal, or with `-plain`.
- Organs: LLM-powered steps that run `claude -p` with no tools and no MCP
  servers, on evidence the orchestrator gathers, and only advise.
  - Triage: each deferred ticket gets a cause (environment, instructions or
    problem) and a recommendation in its notes.
  - Reviewer: when the loop stops, a short report (Finished / Set aside /
    Needs you), shown and saved to `.claude/orchestrate-reports/`.
  - `-triage`, `-review` and `-organ-model` (`TRIAGE`, `REVIEW`, `ORGAN_MODEL`).

### Fixed (compared with orchestrate.sh)

- A worker whose prompt was pasted but never submitted is no longer taken
  for finished and deferred untouched: the orchestrator checks the worker
  started, presses Enter if the prompt is waiting in its input box, or sends
  it again if the box is empty. Otherwise it defers the ticket as
  `PROMPT_FAILED`.
- If `herdr agent start` reports a failure but the agent comes up, it is
  used instead of retrying into the occupied pane (`START_FAILED`).
- Screens of busy workers are read from the visible screen, since Herdr
  can't capture a working agent's scrollback.
- `python3` is no longer needed.

[Unreleased]: https://github.com/noesis-sol/orchestra/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/noesis-sol/orchestra/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/noesis-sol/orchestra/releases/tag/v0.1.0
