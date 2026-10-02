# Changelog

All notable changes to orchestra are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Number keys in the dashboard go to a worker's Herdr tab. The running
  workers are numbered 1 to 9 in Current, oldest first, in the boxes and in
  the one-line list, and pressing a number switches Herdr to that worker's
  tab (`herdr tab focus`), in the background: a failure is logged and the
  dashboard carries on. The hint names the keys (`1–3 go to a worker`); they
  do nothing while the stop question is open.
- A run carries over the workers the last run left in their tabs. As a run
  ends, however it ends, it writes those on tickets waiting on a question and
  those it left running (with what stopped it: `PAUSED`, `INTERRUPTED`, …) to
  `.orchestra/run/state.json` in the main checkout, atomically and under its
  lock, and says `left for the next run: …`. The next run reads them in its
  `START` block (`carried over from the last run: …`) and takes them up as
  its own asked tickets: it merges a ticket closed meanwhile (rebased,
  checked, its `unmerged` label removed) rather than leaving it to be merged
  by hand, adopts a worker at work on its ticket again rather than stopping
  with `AGENT_BUSY`, and tells an idle one whose question was answered to
  carry on rather than starting a new worker. A carried-over worker whose
  worktree is gone or whose ticket was merged by hand is dropped, and one
  gone from its tab with the ticket in progress is warned about once
  (`WORKER_GONE`) without stopping the run. A `--ticket` run keeps the others
  for a later run. The `unmerged` labels still hold dependents if the file is
  lost.
- Each run appends its events to `.orchestra/run/events.jsonl` in the main
  checkout, for scripts and agents: one JSON object per line, each written in
  a single append, with the time, the run's start (as its lock gives it), a
  stable `kind` (`dispatch`, `closed`, `deferred`, `stop`, `done`, `queue`, …),
  the ticket, title, detail and the log line as `text`, and the queue counts
  the log never had. A `start` record (version, repo, branch, scope,
  concurrency) opens each run and an `end` record with the exit code closes
  it, quitting at once included. The README documents the records, and the
  orchestra skill reads them instead of grepping the log, which is unchanged.
- `orchestra init` sets Beads up itself. Where `bd` is missing it installs it,
  with Homebrew (`brew install beads`) where `brew` is on the PATH, otherwise
  on macOS, Linux and FreeBSD with the Beads install script; on Windows it
  says how. In a terminal the form asks first (offering yes); `--install-beads`
  (or `=false`) answers without asking, and without either `init` installs
  nothing. A `bd` the script put off the PATH is reported with the folder to
  add. In a repository without `.beads/`, `init` then runs `bd init
  --non-interactive --role maintainer --init-if-missing` (no questions, not
  contributing to someone else's repo, auto-export off), before writing
  `.orchestra/`, and lists what `bd` committed and what is left to commit.
  Both are steps in `init`'s summary; a failure shows the command's error, and
  Ctrl+C stops an install under way.
- `orchestra --feature "<request>"` takes a feature request from idea to a
  scoped run: after the usual startup checks, the screen organ judges it, the
  plan organ plans it as an epic and its tickets, orchestra shows the plan and
  asks `File these N tickets and start the run? [y/N]` (`--yes` skips the
  question; without a terminal it must be given), files the epic and its
  children with `bd` (acceptance criteria, `files` metadata, `blocks` links),
  and runs the epic as `--ticket` would. A rejected, unclear or unscreened
  request, a failed plan or one with questions, and `--feature` with
  `--ticket` or an empty request, exit 2 with nothing filed; a `bd` failure
  while filing lists what was filed and how to remove it or carry on. The
  START line, the dashboard (`· feature <epic>`) and the run report name the
  feature and the epic.
- Claude workers get only the MCP servers the project chose (`"mcp_servers"`
  in `.orchestra/settings.json`, set with `orchestra init`): each is resolved
  at start-up from this machine's Claude Code config, written to the worktree's
  `.orchestra/run/mcp.json` (readable by its owner only) and passed with
  `--strict-mcp-config --mcp-config`, so no definition shows on the command
  line. `[]` gives workers none. A chosen server this machine doesn't define,
  or a claude.ai connector, is a setup problem (exit code 2). Without
  `"mcp_servers"`, workers load every server as before, and the run warns once.
  The `START` line names the servers workers get.
- `orchestra init` asks which MCP servers workers get, offering those Claude
  Code defines for the repository on this machine (local scope for the
  repository or its main checkout, project scope in `.mcp.json`, user scope;
  `CLAUDE_CONFIG_DIR` honoured), and saves their names only, never their
  definitions. `--mcp a,b` chooses without asking and `--mcp ""` chooses none.
  claude.ai connectors are listed as not available to workers. A chosen name
  this machine doesn't define is reported with `claude mcp add` as the fix,
  and saved anyway.
- The README defines **workers** (the coding agents that do tickets) and
  **organs** (orchestra's one-shot advisers, with no tools and no MCP servers)
  and explains workers' MCP servers: how they're chosen and resolved, why
  claude.ai connectors aren't available, and why workers should get only what
  the work needs. The skill says how to check and change them and how to fix a
  server a machine doesn't define. `orchestra -h` uses the same words and lists
  exit code 7.
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
  the main checkout and asked to run a single command (a Claude worker starts
  without MCP servers, with `--strict-mcp-config`). If it does, the log and
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
- `--worker-effort` / `WORKER_EFFORT` / `"worker_effort"` starts Claude workers
  with `claude --effort <level>`; unset, they keep Claude Code's default, and
  `orchestra init` says how to choose one.
- A Claude worker whose turn ends (its Stop hook) with its ticket still in
  progress, without a question or a deferral, is told to continue, at most
  twice, before the idle grace and `PAUSED` apply as before.
- With a ticket limit, the worker's prompt says how long it has for the ticket.
- A worker started on a branch an earlier attempt at its ticket left work on
  is told, in its prompt, how many commits the base branch doesn't have
  (`git log <base>..HEAD`) and whether there are uncommitted changes
  (`git status`), and to build on them or revert them deliberately rather
  than start over. A fresh worktree's worker, and an adopted one, get no note.
- The worker prompt (template and this repository's) asks workers not to end a
  turn with a summary, an offer to go on or a non-blocking choice, and lets
  them keep a ticket's acceptance criteria as a todo checklist.
- One run at a time in a repository: a run holds an flock on
  `.orchestra/run/orchestra.lock` in the main checkout from its startup checks
  until it exits, and writes its PID, start time, version, branch, scope and
  Herdr pane there. A second run stops before changing anything (a `--feature`
  request before it is screened) with exit code 2, naming the running one:
  `orchestra is already running in <repo> (pid 44497, since 08:31, main, pane
  w2B:p60)`. The system releases the lock when orchestra exits, `kill -9`
  included, so nothing stale is left. `orchestra plan --apply` warns while a
  run is going. The orchestra skill checks for a run with `lsof` on the lock
  instead of `pgrep -x orchestra`, which matched a run in any repository.

### Changed

- A ticket left for review shows why on its dashboard row, in the few
  words its warning gives (`checks failed`, `conflicts with main`, `closed
  without a commit`, …), as a deferred one does, rather than `left for
  review, see the log`, which stays for a warning that gives no reason.
- In a repository `orchestra init` hasn't set up (no `.orchestra/worker-prompt.md`
  or `settings.json`, nor a `.claude/worker-prompt.md` from before `init`),
  `orchestra` and `orchestra --feature …` say first `orchestra isn't set up in
  this repository yet. Run this first: orchestra init`, and exit 2, in place
  of listing the missing worker prompt (by its absolute path) and Beads
  database (`Run: bd init`), which `init` sets up. Other startup problems
  follow it. A project with settings but no prompt names it as
  `.orchestra/worker-prompt.md` and suggests `orchestra init` to recreate it.
- macOS notifications say in a few words which project did what, titled
  with the repository folder's name alone (`jswallet`, no longer
  `Orchestra: jswallet`), rather than repeating the whole log line:
  `Closed jswallet-12 · Add a --json flag to list`, `Set aside jswallet-12 ·
  checks failed` (deferred, or left for review), `jswallet-12 needs your
  answer · <the question's title>`, `Stopped: PAUSED on jswallet-12` and
  `Finished the run · 12 tickets closed · 2 set aside`, a title or reason cut
  at 60 characters. Holds, probes, the warnings that set no ticket aside
  (`LIKELY_CONFLICT`, `LONG_RUNNING`, …), triage, `REVIEW_FAILED` and
  `REPORT written` no longer notify; the log, the dashboard and plain output
  are unchanged. In the event stream, `closed`, `deferred`, `asked` and
  `answered` records gain the ticket's `title`, a `warn` that sets its ticket
  aside gains `detail` (why), and a `stop` record gains `detail` (its kind)
  and the `ticket` it stopped over, if any.
- On a terminal, a run that ends by itself closes with a bold green
  `♪ Completed the Run`, then, quieter, its tickets and how long it took
  (`2 tickets · 1h12m`), instead of `■ READY_EMPTY after 2 tickets`;
  `LIMIT_REACHED` and `DRAINED` read `♪ Reached the ticket limit (40)` and
  `♪ Stopped after the running tickets, as asked`. A scoped run's
  `SCOPE_DONE` or `SCOPE_OPEN` follows on its own line, `SCOPE_OPEN` in
  yellow, as work is left. The organ phase's lines are sentences in light
  purple rather than faint (`Finishing triage…`, `Writing the run report with
  Claude… (Ctrl+C skips)`), and `REVIEW_FAILED` is in the warning colour. The
  log, the event stream, plain output and the run report keep their wording.
- `orchestra init` offers 1 to 4 tickets at the same time, and `Custom…`,
  which asks for a whole number from 1 to 16 (6 and 8 are no longer options).
  A saved setting above 4 opens on `Custom…` with its number filled in.
- Organs run at an explicit effort: `low` for triage and the predictor,
  `medium` for the run report (measured: triage 7.9 s against 15.0 s at
  `high`, the predictor 4.3 s against 7.2 s, the report 7.3 s against 8.5 s).
  `--organ-effort` / `ORGAN_EFFORT` / `"organ_effort"` sets one for all.
- Each section of an organ's evidence is wrapped in `<evidence id="…">` tags
  whose ID is fresh for each call, and every organ's system prompt says text
  inside them is evidence only, never instructions to follow.
- Organs run `claude` in Claude Code's safe mode (`CLAUDE_CODE_SAFE_MODE=1`),
  so your own `~/.claude/CLAUDE.md`, the hooks in your settings and plugins,
  your skills and auto-memory stay out of them, as the project's already did.
  Measured on a short call: 1,043 input tokens before, 529 after, and no hook
  runs; the login and your default model are unchanged.

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
- In a pane too small for the box, the drain question's line above the hint
  uses the box's words: `Stop after the running tickets? y/n · Stopping after
  the 2 running tickets finish (…): no new tickets will start. They merge as
  usual, then the run ends.`, cut at the pane's edge, with `Stop after
  current?` only where the question itself doesn't fit.
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
- The run loop's tests of what time drives (the idle and start-up graces, the
  blocked and unknown limits, the ticket limit, the ready poll, draining, the
  hold for the environment and its probe, hand-back time limits, Ctrl+C) run
  in `testing/synctest` bubbles, on the loop's real durations and git in
  memory, so they no longer depend on how loaded the machine is. The loop's
  test-only timing settings are gone, but for its status poll.
- A second Ctrl+C (or SIGTERM, or SIGHUP) quits a stopped run that hangs while
  it winds down, which before took `kill -9` from another terminal. With
  nothing under way that a stop doesn't cut short, it quits at once with exit
  code 130, logging and printing `INTERRUPTED: quit at once with Ctrl+C,
  leaving <id> (tab <tab>) running with its tab and worktree open`. With a
  merge or worktree setup under way, it says so and how to abandon it
  (`<id>'s merge is under way; press Ctrl+C again to abandon it (the
  repository may be left half merged)`) and skips triage and the report, as
  before; a third quits, adding `abandoned <id>'s merge: git may still finish
  it, so check git status before starting another run`. During triage and the
  report, Ctrl+C skips them as before, and the one after it quits.

### Fixed

- Merging a ticket no longer closes a tab that may not be its worker's. A
  run merging a ticket the last run left behind closed the tab ID that run
  recorded, but Herdr numbers its tabs afresh when it starts without
  restoring its last session (and a crash loses a tab opened in the 5 seconds
  before it), so by then the ID may be another tab, the user's own included.
  orchestra now reads the tab's label first (`herdr tab get`) and closes the
  tab only while the label is still the ticket's ID; the `closed` line says
  when it left the tab open, and why.
- An asked ticket whose worker, answered in its tab, claims it again and
  closes it while every slot is busy is no longer stranded on its branch:
  `bd ready` lists only open tickets, so it never came back, nothing merged
  it, and the dashboard showed it as "? for you" to the end. The run now reads
  each asked ticket every 30 seconds and before it ends. One closed, or in
  progress again while the run takes tickets, is adopted at once, beside the
  running tickets even beyond `--concurrent`, and merged as usual (`ANSWERED:
  <id> was closed in tab <tab> after <question> (…), so its worker is
  adopted`). One its worker defers shows as deferred; one in progress whose
  worker has gone from its tab stops the run with `PAUSED`. As the run ends,
  an asked ticket left in progress, or closed but unmerged because the run
  stopped (`ASKED_UNMERGED`), is labelled `unmerged`, so later runs hold the
  tickets it blocks. The dashboard's "Needs you" count also drops when an
  asked ticket's row turns deferred, for review or stopped.
- An organ answer whose `structured_output` is `null` is read from `result`,
  as one without `structured_output` is; it was read as an answer of zero
  values, and failed as `unknown triage cause ""`. Where `result` carries the
  answer, with no schema enforced, a plan ticket with no priority (or `null`)
  is an error rather than P0, and a triage verdict's confidence must be high,
  medium or low.
- With triage off (`-triage=false`, or without `claude` installed), a ticket
  its worker defers or leaves open no longer has evidence gathered for triage
  only to be thrown away: a `bd show`, three git reads and a read of the
  worker's screen, each allowed up to 30 seconds. Ctrl+C during
  `finishing triage…` skips the tickets still queued, and the one being
  triaged, quietly: it logged and showed a `TRIAGE_FAILED` warning for each.
- `--feature`'s plan organ sees every ticket that isn't closed, with its
  status: a ticket left in progress by a stopped run, or set aside with
  `bd defer`, was missing, so a request for the same change planned it again.
  A plan with an empty `blocked_by` entry is no longer rejected (exit code 2
  after a long plan); the entry is ignored. A request naming a file with a line
  after it (`internal/x/y.go:120`, `y.go#L120`) gets that file's start as
  evidence. Ctrl+C while the evidence is gathered stops at once (exit code
  130), and a tracked file over 4 MB is no longer read to count its lines.
- `--feature` asks to confirm its plan only when standard input and output are
  both a terminal: `orchestra --feature "…" > run.log`, typed in a terminal,
  waited on a question written to the file. Without `--yes` it now files
  nothing and says so (exit code 2). "Nothing was filed." appears only when
  `bd` exited with an error of its own; a `bd` that was stopped, was killed or
  gave output orchestra couldn't read may have filed the epic or ticket, and
  orchestra says to check with `bd list`. When a link fails, it is named by
  its tickets' IDs, the tickets filed are listed with their plan keys, and
  every link not added comes with its `bd dep add <blocked> <blocker>`, so the
  epic's tickets still run in order. `--feature` with `DONE_SO_FAR` at or above
  `LIMIT` is a setup problem rather than a plan filed and never started, and
  Ctrl+C while the main checkout is checked exits 130, not 4.
- Each dashboard line stays one line, within the pane. A ticket title, a
  question's title or a triage summary with a line break in it made its row
  in the tickets table, or its line in the worker list, two lines tall; with
  two such rows nothing fitted, and the dashboard fell back to one-line totals
  and no tickets table for the rest of the run. Line breaks and tabs now show
  as spaces. With a double-width character (CJK, emoji) cut by the edge of the
  `s` question's box, a line came out one column wider than the pane, and the
  terminal's wrapping left scraps on the screen; the cut character is now
  blanked.
- Organ calls run `claude` the way orchestra runs `git`, `bd` and `herdr`.
  An organ stopped at its time limit fails with `claude: timed out after
  10m` (a plan that took too long said `claude: signal: killed:`), and one
  stopped by Ctrl+C gets SIGTERM, then SIGKILL, in a process group of its
  own. A `claude` that answered and exited, leaving a process behind that
  held its output, failed after 5 seconds with `exec: WaitDelay expired
  before I/O complete`, its answer thrown away; the answer now counts.
- The dashboard marks a ticket `! review` only when a warning sets it aside
  (`CHECKS_FAILED`, `MERGE_CONFLICT`, `CLOSED_WITHOUT_COMMIT`,
  `DEFER_FAILED`). `LONG_RUNNING` and `WATCH_FAILED` no longer turn a running
  ticket's row to review while its worker goes on, hiding `testing`, `editing`
  or `reading`, and `TRIAGE_FAILED` no longer replaces a deferred ticket's
  reason. A ticket deferred again in the same run shows its new reason, not
  the first deferral's triage verdict.
- The dashboard's summary at the end of a run keeps its title line (the branch,
  how long the run took) and its totals in a pane too short for every ticket:
  the tickets table shows the latest ones and counts the earlier ones (`+N
  earlier tickets, see the log`). The summary was drawn whole, and the terminal
  lost its top lines, never writing them, not even to scrollback.
- Three repository quirks no longer mislead orchestra's git calls. With
  `log.showSignature` set, signed commits no longer put git's signature check
  in place of a commit: the dashboard read `Good merged into …`, and the log and
  the organs got the signature text. A base branch or ticket branch named like a
  top-level file or folder (a base branch `docs` beside `docs/`) is no longer
  read as a path, which made git fail and left a ticket merged by hand marked
  unmerged, holding its dependents in every later run. A rebase conflict in a
  file with a space or a non-ASCII character in its name (`docs/My Guide.md`,
  `café.md`) now names that file, not two halves or quoted octal.
- orchestra no longer writes a worker's run files through a symlink in place
  of its worktree's `.orchestra` or `.orchestra/run` that stays inside the
  worktree. git's ignore rules for the folder match a directory, not a link,
  so with `.orchestra/run -> ../docs`, `mcp.json` (with the MCP servers'
  secrets) and `prompt.md` landed in `docs/` as untracked files, for the
  worker's `git add -A` to commit and the merge to bring into the branch. Such
  a link, wherever it points, now sets the ticket aside without a worker
  (`RUN_FILES_OUTSIDE: its worktree's .orchestra/run is a symlink -> <id>
  deferred …`), with a note on the ticket naming the link.
- orchestra no longer writes a worker's `prompt.md` or `hooks.json` through a
  symlink in their place that stays inside the worktree
  (`.orchestra/run/prompt.md -> ../../docs/prompt.md`): the file landed where
  the link pointed, untracked and not ignored, for the worker's `git add -A` to
  commit. It now removes the link and writes the file in its place, as it
  already did for `mcp.json`.
- A worker's reporting hooks can no longer block it. Under dash (`/bin/sh` on
  Debian and Ubuntu) a hook whose write failed, as every write does once the
  worker has removed `.orchestra/run/` (`git clean -fdx`, `git stash --all`),
  exited 2, which Claude Code takes as "block": every tool call was refused and
  the turn could not end, until the ticket limit or a person stopped it. Each
  hook now ignores its errors, reads all its input and exits 0. A tool that
  fails is now reported too (`PostToolUseFailure`), so the dashboard no longer
  goes on showing the worker `editing` or `testing` while it thinks.
- A ticket's title no longer reaches an organ outside the evidence tags.
  Triage's and the predictor's first line named the ticket by its ID and its
  title, where a title written to steer them (say, blaming the environment with
  high confidence, which can hold the run) read as orchestra's own words; they
  now name it by ID only, and the title stays in the ticket's `bd show` inside
  the tags. The run report's evidence moves the run's final line, which can
  quote a triage summary, into its own tagged section.
- A ticket's footprint no longer takes in the files the project's check
  command names (`scripts/check.sh`) from its text: nearly every ticket names
  the check in "`scripts/check.sh` passes", so with several tickets at once
  most of them refused to run beside each other (`skipping <id>: touches
  scripts/check.sh, like running <other>`), and `orchestra plan` proposed
  chaining the whole backlog. Predicted files leave them out too; a ticket
  whose `files` metadata lists one still overlaps on it.
- A ticket whose worker is left running when the run ends (`PAUSED`,
  `BLOCKED`, `TICKET_LIMIT` or another stop, or `INTERRUPTED`) is labelled
  `unmerged`. Its worker could close it after orchestra had gone, and nothing
  merged it; the next run then counted it done and started the tickets it
  blocks on a base without its code. They now wait, as for any ticket left
  unmerged, until it is merged by hand or dispatched again and merged. The log
  says `<id> is left running in tab <tab> and labelled 'unmerged'` before the
  last line, or `LABEL_FAILED` when bd can't label it.
- A prompt pasted to a worker (a conflict handed back, a nudge to continue, an
  answered question, the environment probe's command) returns once Herdr sees
  the worker start on it (`herdr agent prompt --wait --until working --until
  blocked`), not at the end of its turn, up to 10 minutes later. Meanwhile
  nothing read the worker's status: the dashboard showed a nudged worker idle
  and a resumed one not at all, the ticket limit waited, and a hand-back's
  `resolve_timeout` started only after the worker's first turn. The resolve
  timeout now runs from the hand-back, and the dashboard shows the ticket as
  `⟳ resolving` from then on.
- orchestra no longer follows a symlink out of a worktree when it writes,
  removes or reads the files in `.orchestra/run/` (`mcp.json`, with the MCP
  servers' secrets, `prompt.md`, `hooks.json`, `activity.json`, `edits`), nor
  out of the main checkout for the environment probe's file: each goes through
  an `os.Root` at the checkout. A worktree whose `.orchestra/run`, or a file in
  it, leads outside is set aside without a worker (`RUN_FILES_OUTSIDE: its
  worktree's .orchestra/run/mcp.json points outside the worktree -> <id>
  deferred …`), with a note on the ticket, and the run goes on.
- A ticket whose ID isn't a plain name, such as `x-a/b` or `x-../../y` (Beads
  checks only the prefix, and workers file tickets themselves), is set aside
  before its ID is made a worktree folder or a branch (`BAD_TICKET_ID: IDs
  with path characters can't be run: <id> -> deferred …`), with a note on the
  ticket saying to `bd rename` it, and the run goes on. It used to nest folders
  under the worktree root, or stop the run with `WORKTREE_FAILED`. `--ticket`
  with such an ID is a setup problem (exit code 2).
- A panic while working on one ticket (a nil pointer, an index out of range)
  no longer kills orchestra and leaves the other workers running unsupervised:
  the run holds with `PANIC in <ticket>: <value>; its worktree and tab … are
  left for review`, the running tickets finish and merge, and the run ends with
  that line (exit code 4) and its report. The stack is in the log. A panic in
  triage only fails that ticket's triage (`TRIAGE_FAILED … panic: …`), one in
  the footprint predictor only that prediction, and one watching a starting
  worker only stops its status showing (`WATCH_FAILED`). A panic in the loop
  itself still ends orchestra, now with the dashboard's terminal restored.
- Failures orchestra used to ignore now show. A git that can't say where the
  repository's git directories are is a setup problem (exit code 2) instead of
  passing the linked-worktree check, as are a worktree or log folder that can't
  be created and a worker prompt that vanished after the setup check. A `git
  rebase --abort` that fails no longer goes unmentioned: the ticket set aside
  says its worktree is left mid-rebase, and a returning ticket whose refresh
  can't be aborted logs `REBASE_ABORT_FAILED`. A Herdr tab that won't close, an
  Enter that can't be pressed into a worker and a failed `git worktree prune`
  are logged. A `.git/info/exclude` that can't be read is no longer rewritten
  without its entries, and stale worker or probe records that can't be removed
  stop the start rather than being read later.

### Fixed

- A ticket back from a question whose worker is still in its tab no longer
  stops the run with `AGENT_BUSY`. Answering in the worker's tab lets it carry
  on before orchestra sees the answer; the run now adopts that worker when the
  ticket comes back: it waits for it to settle and merges its work as usual.
  A worker idle in its tab is told the question is answered and to carry on,
  keeping what it knows; only if it doesn't is a new worker started. The log
  says `ANSWERED: <question> (…) is answered, so <id> comes back`, and the
  dashboard turns the ticket's own row from "? for you" back to working rather
  than adding a second one. An earlier worker is now looked at before its
  worktree is touched, so a branch is never rebased under a live worker.
- A `bd` that fails to read a ticket's status while its worker is idle (another
  `bd` holding the database, a 30-second time limit) no longer decides how the
  worker settled. It used to count as an unknown status: the worker counted as
  settled at once, so one whose turn ended mid-ticket wasn't told to continue
  and the run paused, and one idle a moment with its ticket still open was
  deferred while it carried on. The failure is logged, the wait goes on, and
  only a minute's worth of failed reads in a row stops the run, with
  `STATUS_UNREADABLE for <id>: its status could not be read 20 times in a row
  while its worker was idle: <error>`.
- A second Ctrl+C, or closing the terminal or Herdr pane, no longer kills
  orchestra in the middle of a merge. Once the dashboard had closed, while the
  run waited for a merge (`waiting for <id>'s merge to finish…`), the stop
  signals had their default effect again, so the merge's notes and label were
  never written and the log wasn't closed. They now stay caught for the whole
  run; one that comes while a stopped run winds down skips triage and the
  report, as SIGTERM and SIGHUP do at any time. And the `git`, `bd` and `herdr`
  commands orchestra runs no longer share its process group, so a Ctrl+C typed
  at the terminal, or the SIGHUP of a closing pane, no longer reaches them
  directly: a rebase killed this way passed for a conflict (`MERGE_CONFLICT`
  and the `unmerged` label on a clean ticket), and a killed fast-forward ended
  the run with `MERGE_FAILED`. Orchestra stops them itself, as before.
- A ticket's `files` (or `predicted_files`) metadata stored as a string that
  holds a JSON list, which is what `bd update <id> --set-metadata
  'files=["a.go","b.go"]'` writes, is read as the list it holds. It used to be
  split on its commas, so its footprint was made of names like
  `["internal/organ/screen.go"` that match no file, and an overlap with
  another ticket on those files went unseen. Quotes and brackets left on the
  paths of a list that isn't quite JSON are dropped too.
- A Claude worker that asks the maintainer and ends its turn without reopening
  its ticket (the question filed, `bd update <id> --status open` skipped or
  failed) settles at that Stop hook: the log says `<id> settled: Stop hook at
  <time>, waiting on <question>`, then `ASKED`, and orchestra reopens the
  ticket and starts the next one at once. It used to wait out the 10-minute
  idle grace first, its slot idle. A worker without hooks still gets the idle
  grace, as Herdr's idle can't tell the end of its turn from a wait on its own
  background command.
- A ticket's footprint no longer counts Go keywords and builtins as functions
  it works on (`func()`, `len(x)`, `make()`, `string(b)`), nor calls into
  more standard packages (`synctest.Test()`, `synctest.Wait()`, `testing`,
  `atomic`, `signal`, `maps`, `cmp`, `utf8`, `rand`, `log`, `slog`, `runtime`,
  …), which used to count by their bare name (`Test`, `Wait`). Two unrelated
  tickets that both said `func()` or `synctest.Wait()` shared a function: the
  run kept them apart, and `orchestra plan` proposed a blocks link between
  them. A method named like a builtin (`o.close()`) still counts.

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
