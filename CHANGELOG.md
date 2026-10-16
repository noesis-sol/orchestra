# Changelog

All notable changes to orchestra are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- `orchestra --spec <file> --refine` breaks the spec down with you: an
  interactive Claude Code session in a pane beside orchestra's (Herdr, tmux
  or cmux), as the New feature interview has, or on orchestra's own terminal
  where there is no pane. Taking the spec as mostly settled, it asks only
  where the spec leaves open a decision that changes what gets built, in the
  interview's rounds of numbered questions with recommended answers; then it
  sums up the epics and tickets it will file and asks to go ahead. It files
  them with the headless breakdown's rules, the decisions settled under
  Decisions in the root epic's description, and hands the root epic back in
  `.orchestra/run/spec.json`. orchestra then closes the pane, checks the tree
  and starts the run on it without asking. Meanwhile orchestra's pane shows
  claude's state, the time so far and the epics and tickets filed, counted
  from bd every 5 seconds. `/exit` or closing the pane before the hand-back
  files nothing more and ends the run: exit 0 if nothing was filed for the
  spec, else exit 2, listing what was. Without a terminal or a pane it ends
  with exit 2 once the spec is committed.
- A worker on a ticket whose metadata names a spec (`spec`, the committed
  spec's path, which a spec run's breakdown gives every ticket it files) is
  told in its first message, and again when its session is resumed, that the
  ticket is part of that spec: to read the sections the ticket cites before
  it starts, and to treat the spec as settled. Its standing rules are
  unchanged.
- A spec run starts from an empty repository: `mkdir app && cd app && git
  init && orchestra --spec ~/app-spec.md` works unattended. In a repository
  `orchestra init` hasn't set up, it sets it up as init does without a
  terminal (bd init where Beads isn't set up, the worker prompt, runners that
  check nothing yet, settings with the concurrency `-c` gives, else 1, and no
  MCP servers for workers; no test work and no skill, as the breakdown's first
  ticket scaffolds the project and its checks), and commits that with the spec
  as `orchestra: set up for spec <name>`, the repository's first commit where
  it has none. Outside a git repository it says to run `git init` first; bd
  missing is a setup problem, as nothing is installed unattended.
- `worker_permission_mode` in `.orchestra/settings.json`, or
  `--worker-permission-mode` (`WORKER_PERMISSION_MODE`) for one run, starts
  Claude workers, the resumed and the environment probe's included, with
  `--permission-mode default`, `acceptEdits` or `auto`; `bypassPermissions`
  and `plan` are setup problems. A spec run defaults to `auto`, so Claude
  Code's classifier decides and a worker waits on a permission prompt only
  when it can't; other runs leave the mode to Claude Code, as before. The
  START line names the mode. A `claude` whose `--help` doesn't list it gets
  a `PERMISSION_MODE` warning once, and its workers start without it.
- A run waits out the account's usage limit rather than ending: once a
  worker reaches it, the run holds (`USAGE_LIMIT: <id>'s worker reached the
  usage limit; it resets at 14:05`), starting no new tickets, and the limited
  workers wait, their wait not counting toward the ticket limit. A minute
  after the reset each is told to carry on (Escape first, which dismisses
  Claude Code's own question) and the hold lifts (`USAGE_LIMIT lifted`); with
  no reset time known, one limited worker is told every 15 minutes, and once
  one goes on past the limit the rest are told too. `usage_limit_wait` in
  `settings.json` (default `6h`, `0` for none) is how long a hold may last:
  then the run stops with `USAGE_LIMIT` and exit code 8, the workers left in
  their tabs for the next run. The dashboard's title line shows `usage limit
  · resets 14:05`, one notification says so, and the event stream records
  `usage_limit` (with `reset`) and `usage_limit_lifted`. Triage, the
  predictor and the run report skip a call the limit refuses, with a line
  saying so.
- A spec run without `--refine` breaks the spec down by itself, with nobody
  there to ask: `claude -p` in the main checkout, with your own Claude Code
  setup, reads the spec, the README, CLAUDE.md or AGENTS.md and the code,
  and files a root epic, child epics and tickets with `bd`, each citing the
  spec's sections and carrying its `spec` metadata. Where the spec leaves a
  choice open it takes the most conservative reading, and writes the
  assumption into the tickets that depend on it and under Assumptions in the
  root epic's notes. It reads, and runs only `bd` and read-only `git`;
  anything else is denied without asking. It may take 60 minutes
  (`spec_timeout`, `--spec-timeout`, `ORCHESTRA_SPEC_TIMEOUT`) at effort high
  (`spec_effort`, `--spec-effort`, `ORCHESTRA_SPEC_EFFORT`). A terminal shows
  a spinner counting the epics and tickets filed; plain output has a line
  for each epic and the counts every minute. orchestra then checks the tree
  (the root epic, issues outside it, tickets without files metadata or
  acceptance criteria, blocks cycles) and runs it; a session that fails,
  runs out of time or names no root epic ends the run with exit 2, listing
  what it filed. `--refine` still stops once the spec is committed.
### Changed

- Footprints leave out specs under `.orchestra/specs/`, as they leave out
  the check command's files, unless a ticket's `files` metadata lists them:
  every ticket broken down from a spec cites it, so naming its file kept
  them all from running side by side, and `orchestra plan` ordered them in
  one chain.
- A ticket's footprint also leaves out the spec its `spec` metadata names,
  unless its `files` metadata lists it: a spec run keeps a spec already in
  the repository where it is (`docs/shop-spec.md`, say), outside
  `.orchestra/specs/`, so the tickets broken down from it, which all cite
  it, ran one at a time. A ticket from no spec, or another, still counts it.

### Fixed

- `worker_permission_mode` (and `--worker-permission-mode`) takes `manual`,
  the name Claude Code 2.1.301's `--help` gives the mode it listed as
  `default`, and a run passes whichever of the two its workers' `claude`
  lists. `default` was dropped on a current `claude` with a
  `PERMISSION_MODE` warning, and workers started in the mode Claude Code's
  settings give; one setting now suits an older `claude` and a newer alike.
- A worker that reaches the account's usage limit while it resolves its
  rebase's conflicts or fixes its failing check now holds the run and waits
  out the limit, as one on its ticket does: no new tickets start, the wait
  doesn't count toward `resolve_timeout`, and it is told to carry on after
  the reset. It was taken for busy until the 20 minutes ran out and the
  ticket was set aside, while new tickets kept starting. A worker already at
  the limit when its ticket is to be handed back is handed it once the limit
  lifts, rather than the ticket being set aside (`its worker is limited`).
- A folder that doesn't exist yet in a ticket's `files` metadata, written
  with a trailing slash (`web/src/`), now holds the files under it: a
  ticket listing it overlaps one naming `web/src/cart.ts`, in the run and
  in `orchestra plan`, whether the repository's files are known or not. It
  was kept as one file, `web/src`, so the two ran side by side over the
  same new files. A name without the slash is still a file.
- A folder that exists in a ticket's `files` metadata (`internal/dispatch/`)
  now also holds the files other tickets add in it: a ticket listing it
  overlaps one naming a new `internal/dispatch/cart.go`, in the run and in
  `orchestra plan`. It meant only the files already there, so the two ran
  side by side. Two tickets listing the folder are still compared on its
  files one by one, by function where both name functions a file mentions.
- A glob in a ticket's `files` metadata that matches no file yet
  (`web/src/*.ts`, in a folder a project built from a spec adds) now holds
  the files and folders other tickets name that it would match, from the
  repository's root: a ticket listing it overlaps one naming
  `web/src/cart.ts`, in the run and in `orchestra plan`, whether the
  repository's files are known or not, but not one naming `web/lib/a.ts`.
  It was dropped, or kept as a file named `web/src/*.ts` when git couldn't
  list the files, so the two ran side by side over the same new files.
- `orchestra --spec <file>` (or `ORCHESTRA_SPEC`) runs a spec, a Markdown or
  plain-text file: it commits the spec, alone, where workers can read it (a
  file outside the repository as a copy in `.orchestra/specs/`), has it broken
  down into an epic and its tickets, and runs that epic without asking, with
  no ticket limit unless `-limit` or `LIMIT` gives one. `--refine` (or
  `ORCHESTRA_REFINE`) asks to talk the spec through first. The breakdown
  itself isn't in this build yet: a spec run stops once the spec is committed.
  The run lock, the START line and the event stream's start record name the
  spec.
- A worker whose turn ends on the account's usage limit (the five-hour
  session limit, the weekly one) now shows as `limited`, rather than idle
  with its ticket in progress, and is no longer told to continue into the
  limit: the run waits on it, and Claude Code's own automatic continue, when
  on, carries it on once the limit resets. Workers' hooks now include
  StopFailure, which Claude Code runs instead of Stop when an API error ends
  a turn, and the time the limit resets is read from what Claude Code says
  of it. In Herdr, the limit is read from the worker's screen.
- A Claude worker whose turn ends on an API error other than the usage
  limit (a 500, a 529, a rate limit that isn't the account's) now counts as
  at the end of its turn, as at a Stop: with its ticket in progress it is
  told to continue at once, rather than waiting out the 10-minute idle grace
  before `PAUSED`, and one done with its ticket settles there. The log names
  it: `A settled: StopFailure hook (server_error) at 15:04:05`.

## [0.7.7] - 2026-10-16

### Fixed

- `orchestra init` now holds the run lock from the form's answers to its
  end, so a run started meanwhile is refused, naming init (`orchestra init,
  pid …`). It only looked for a run, so one started while init renamed the
  tickets, wrote its files or filed tickets went ahead on tickets renamed
  under it, in a tree init made dirty.
- A run no longer starts a second worker on a ticket whose worker the last
  run left in another terminal (tmux, Herdr or cmux), once bd ready lists
  it again, as when its question is answered. The run left that worker in
  its tab on the same worktree and saved it for a run there; this one now
  leaves its ticket alone, holds the tickets it blocks, and saves it once.
- A run file in a worktree's `.orchestra/run/` that a worker replaced with
  a symlink to a file in the same worktree, by an absolute path or by one
  that climbs out of the worktree and back in, is replaced by the file again.
  It stopped the whole run when the ticket came back with MCP servers
  chosen, and its worker started without its launch prompt or hooks.
- In tmux and cmux, an earlier worker left running in its tab beside the
  worker started since in the same worktree no longer takes over that
  worker's records when you clear it (`/clear`) or it resumes another session:
  the session it then started, under a new ID, read as the worker's, and the
  worker's own reports were left out. The hooks now name the Claude Code
  process that runs them, and record only the one that started first since
  the worker's launch.
- A stop signal while `orchestra init` asks its questions (`kill`, a
  process supervisor, an IDE stopping the task) cancels init as Esc does,
  with nothing changed, and exits 130. Bubble Tea took SIGTERM for a
  submitted form, so init went on to set up Beads and write `.orchestra/`
  with the answers given so far.
- An organ call runs claude in an empty directory of its own that only you
  can open, no longer in the temporary directory itself. On Linux with
  TMPDIR unset that was the world-writable /tmp, and claude read the
  `.claude` settings another user left there as project settings, so their
  `env` or `apiKeyHelper` could send each organ call, with your credentials
  and the run's evidence, to their server or run their command as you.
- In tmux and cmux, a worker started in the worktree of an earlier one left
  running in its tab, as when its deferred ticket is taken up again, no
  longer reads as gone when the earlier one exits or is closed: the earlier
  one's hooks wrote to the same records, so its end ended the worker too and
  orchestra resumed the worker's session in a second Claude beside it. The
  hooks now record only the session the worktree's last start named.
- On tmux 3.7, a child ticket's worker gets its window, named with `_` for
  the `.` in its ID: tmux 3.7 refuses a window name holding `.` or `:`, so the
  run stopped on the first such ticket, blaming the workspace.
- An MCP server unticked in orchestra init stays unticked when the terminal
  is resized: the resize drew it ticked again, and leaving the field or
  toggling another server saved it to settings.json.
- A worker waiting on a permission prompt for one call of a tool, which it
  runs beside another call of the same tool (two WebFetches, Reads or
  read-only Bash commands at once), reads as blocked until that call is
  answered. The other call's end cleared the prompt's record, so tmux and cmux
  showed the worker as working and the run never stopped on the prompt.
- The footprint of a ticket whose worker a run adopts as it starts names the
  repository's files, as a ready ticket's does: read before the repository's
  files were, it kept a name such as loop.go as written, so a ready ticket
  naming the same file by its path ran beside it.
- A worker that asked a question and, answered in its tab, waits on a
  permission prompt Herdr shows as idle is adopted when its ticket is taken up
  again, never told the answer is in: the message's Enter picked the dialog's
  first option, so orchestra allowed the tool itself, a removal Claude Code's
  safety check held for a person included.
- A ready ticket whose ID carries a character that can't be shown, a C1
  control such as U+009B (the 8-bit CSI), a format character such as U+202E
  or a space other than ASCII's, is set aside like one whose ID can't name a
  git branch, instead of naming a branch, a worktree and a tab.
- A ticket set aside as its check failed again after its worker's fix is
  checked once more only after its merge has returned. Taken up while bd was
  still labelling it, with Base moved on during the hand-back, it ran twice at
  once: the first's end cleared the second's slot and dashboard row, so the
  run could end, report and exit while the second still rebased, checked or
  fast-forwarded Base, and one ticket more than the concurrency could run.
- A run whose output goes to a pipe nobody reads any more, as when Ctrl+C on
  `orchestra -plain | tee run.txt` ends tee too, winds down as a stopped run
  does: it labels the tickets of the workers it leaves running, saves them for
  the next run, logs its end and exits 130. SIGPIPE ended it at its next line
  of output, before that bookkeeping, with exit 141.
- A ticket handed back to its worker, to resolve a rebase or fix a failing
  check, whose worker left its worktree on another branch or a detached HEAD
  is set aside instead of merged. The check ran on what the worktree had
  checked out, and the merge then brought in the ticket's branch, which was
  never checked. Its branch is no longer reset either: the reset reset the
  branch the worktree was on.
- The runners `orchestra init` writes run each suite in a subshell, `( … )`,
  so a suite such as `scripts/check.sh && make lint` fails the runner when
  `scripts/check.sh` fails: `set -e` alone let sh go on to the next suite and
  pass, and the merge check merged the broken ticket; an `unlock` after a
  suite taking turns hid even the last one's failure. A suite's `cd` no longer
  carries over to the next suite either. A runner written before keeps the
  bug until replaced: `orchestra init` says it differs from what init writes
  now, and replacing it there fixes it.
- A worker whose permission prompt you refused, waiting at its prompt with its
  ticket in progress, settles after the idle grace as any idle worker does.
  The run took it for still waiting on the prompt, which no hook clears on a
  refusal, showed `permission — waiting for you` and stopped 4 minutes on with
  `BLOCKED >4min: … its worker waits on a permission prompt`.
- The dashboard and the summary printed after it show a ticket's ID without
  its escape sequences, in the tickets table, the worker list and box, the
  solo line and the stop question. A worker files tickets itself, so an ID
  such as `x` followed by an OSC 52 clipboard write reached the terminal on
  every redraw, though the printed log already dropped them.
- A worker idle at its prompt after you refused a permission prompt, its
  ticket still in progress, is told to continue, at most twice, as one whose
  turn ends at its Stop hook is, before the idle grace and `PAUSED` apply.
  With its ticket waiting on a question it asked, it settles there at once.

## [0.7.6] - 2026-10-16

### Added

- The worker prompt and the verifier tell workers to leave throwaway files in
  their scratchpad and never `rm` a target built from a variable or a glob
  (`rm -f $S/*`, `rm -f *`): Claude Code's built-in safety check holds such a
  removal for a person whatever the settings allow, and three workers in a
  run stalled on cleanups they didn't need. An existing project copies the
  bullets in (docs/workers.md says where).

### Changed

- `orchestra init` starts on a cleared screen, its header on the first line,
  instead of below whatever the terminal showed: the form gets the terminal's
  full height. Written to a pipe or a file, or with `TERM=dumb`, it clears
  nothing, and what init prints after the form stays on the screen.

### Fixed

- A permission prompt held by Claude Code's safety check on removals is no
  longer reported as an allowlist miss. The hooks note each prompt's command
  start and the removal the check holds; `PERMISSION_PROMPT` names that
  removal rather than the command's first 80 characters, and the run report
  and triage say no allow rule skips it and suggest the worker-prompt rule.
  For an ordinary prompt the report names the allow rule, such as
  `Bash(ffprobe:*)`, rather than "those commands".
- `orchestra init`'s stepper stays at the top of the form on a terminal
  shorter than a stage, such as the Checks stage with the suites the scout
  found listed: huh drew the stage a line taller than the terminal, which
  scrolled the stepper out of sight.
- `orchestra init`'s form no longer asks the checks' time limits. A first
  init writes the defaults to settings.json (`check_fast_timeout` 30m,
  `check_full_timeout` 60m), where they are changed by editing the file; an
  existing value is kept, and `--check-fast-timeout` and
  `--check-full-timeout` still set them.

## [0.7.5] - 2026-10-15

### Changed

- A worker's hook starts in about half the time: orchestra renders the run
  report's Markdown itself instead of with Glamour, whose syntax highlighting
  (Chroma's lexers and styles) and HTML and Markdown libraries took about
  10 ms of package init on every run of the binary, each hook included. A
  hook now takes about 8 ms on macOS, and the binary is a quarter smaller.

- A worker's hooks are orchestra's own binary, the one that launched it, run
  by its absolute path as `orchestra hook <event>` (hidden, and inert unless
  the hook's command sets `ORCHESTRA_HOOK=1`), instead of shell one-liners
  that pulled fields out of the hook's input with `grep` and `sed`. The
  records read and write the same Go types, a note's value with a quote or a
  backslash is kept rather than noted empty, and each event runs one hook
  process instead of up to three. A hook takes about 13 ms on Linux and 26 ms
  on macOS.

- Each JSON record a worker's hooks write in `.orchestra/run/` starts with
  its format, `"v":1`. orchestra reads records in that format and those
  with none (written before) alike, and leaves a record in a newer format
  unread, with one log line for the worker, instead of misreading it: a
  worker keeps the hooks of the orchestra that launched it, which may be
  newer than the one reading its records.

- orchestra closes an epic itself once a run merges the last of its
  subtickets (none left open, running, or closed but not merged), with
  `bd close <id>` and a reason naming the last ticket, and logs
  `closed epic <id>: all N subtickets are merged`; an epic closed so that was
  the last open subticket of another closes that one too. It only said how
  to close it before, so a finished epic stayed in `bd ready` and the queue's
  count. A scoped run's `SCOPE_DONE` says `orchestra closed <id>`. Should
  `bd close` fail, the log keeps the hint and the run goes on.

- The dashboard's **In queue** shows an empty queue's `0 of N` all in the
  faint grey, like the strip's other zeros, instead of a bright 0 that drew
  the eye to a queue with nothing in it.
- The dashboard's **Workers** shows `0 of N` all in the faint grey when no
  worker is running, instead of a bright cyan 0 beside a faint ` of N`.
- The run report's **Needs you** lists only what the run itself flagged.
  A step a worker's close reason says is still pending, such as `Awaits CI`,
  no longer becomes a to-do there (at most the ticket's **Finished** bullet
  mentions it), so a run that flagged nothing says "Nothing.", matching the
  dashboard's 0.

## [0.7.4] - 2026-10-15

### Added

- The dashboard's **In queue** counts the ready tickets held back, waiting
  for something besides a free slot: `0 of 29 · 4 held back`. A ticket
  skipped for overlapping a running one, a parent waiting for its
  subtickets, one waiting for a blocker to merge and those behind a solo
  ticket counted nowhere, so a queue of 0 beside empty slots looked like
  nothing to do; only the log said why. The `dispatch` and `queue` events
  carry them as `held` and `held_by`, by reason.

### Fixed

- The dashboard's title line gives how long the run has gone as the closing
  line does: `1h12m`, not `1h12m0s`, at a whole minute.
- `orchestra init`'s flags read the same in `orchestra -h`, in `orchestra init -h`
  and in the hint for flags given before `init`: each listed some and not
  others (`--mcp`, `--setup`, `--agent`). The hint now puts the usage line on
  a line of its own, for `plan` and `update` too.
- A worker that stops with nothing done no longer goes back in the queue
  (`WAITS_FOR_MERGE`) for a blocker that has merged but kept its `unmerged`
  label, as when `bd` couldn't remove it (`LABEL_FAILED`): the holds then
  released it at once. It is deferred for review, as with every blocker
  merged. What git and the run know, not the label, says whether a blocker
  has merged, alike for the holds, `WAITS_FOR_MERGE` and `DEFERRED_FREE`.
- A deferred ticket whose blockers have merged is no longer listed as
  `DEFERRED_FREE` in the `START` block, or under `Deferred:` in the box of a
  run with nothing to run, while it is deferred until a later date
  (`bd defer --until`): it was deferred on purpose, with a time to look again.
  Once that date has passed it is listed as before.
- A worker's terminal reads the same in tmux, cmux and Herdr: its last 80
  lines, or those of its visible screen while it works or is blocked.
  Triage, which asks for 80, got 60 in cmux and Herdr and up to 110 in tmux.
  tmux also looks for Claude Code's start-up dialogs on the visible screen
  only, as cmux does, not in the scrollback above it.
- In Herdr, a worker launched with its prompt is no longer typed into a
  program still running in its pane, which would take the command for its
  input: the launch waits up to 5 seconds for the pane's shell to be back at
  its prompt, and otherwise is refused and the worker started with
  `herdr agent start`, as tmux and cmux already kept to.
- A `concurrent` in `.orchestra/settings.json` out of range is reported as
  `.orchestra/settings.json: concurrent must be between 1 and 16 (got …)`,
  naming the file and key as every other setting's problem does, where it
  read `--concurrent must be between …`, as if the flag were wrong.

## [0.7.2] - 2026-10-14

### Added

- `orchestra init` asks for the Beads ticket prefix in its last stage,
  prefilled with the current one (or, for a new Beads, the folder's name
  bd init would use), and `--prefix <name>` sets it without a terminal: a
  new Beads gets it from `bd init --prefix`, and an existing one's tickets
  are renamed with `bd rename-prefix` once confirmed. The rename is refused
  while a ticket not closed has a `wt/<id>` branch, the last run left
  workers in `.orchestra/run/state.json`, or tickets are labelled
  `unmerged`, as those would name IDs that no longer exist.
- The dashboard shows a finished ticket's merge: `◷ to merge`, in blue,
  while it waits for its turn behind another ticket's merge (`to merge
  (2nd)` in Current, its place in line), and `◆ merging`, in pink, while
  its rebase, check and fast-forward are under way, in the tickets table
  and in Current, in place of `▶ working` and the worker's `done`. The
  spinner in Current stops once a worker has finished, a static mark in
  its place, so motion means a worker at work. Merges now go one at a
  time in the order the tickets finish. A ticket handed back to its worker
  reads `resolving` or `fixing` as before, then to merge or merging again.

### Changed

- `orchestra init` reports every flag it can't take at once, as a run does,
  rather than the first: `--check-full-timeout 0 -c 0` names both.
- `orchestra init --force` writes the worker prompt through a temporary
  file, as it writes the settings: a full disk or a crash no longer leaves
  it empty or half written.
- While the run winds down, the dashboard's line above the hint is one
  short amber line, `■ ■ ■ ■  Winding down · 3 running, no new tickets`:
  a `■` for each ticket running when it began, amber while it runs, green
  once merged, red once set aside. It was bold red, the colour for what
  needs you, and listed the running tickets, wrapping in most panes; a
  narrow pane now drops words and never wraps. The totals' `· held` marker
  is amber too. The `DRAIN` log line and the **s** question still name the
  tickets.
- `orchestra update` no longer refuses while a run holds the repository's
  lock: nothing runs the binary during a run, so the reason it gave
  (workers' hooks call orchestra) wasn't true. The new binary is renamed
  into place, so a run going keeps the build it started with.
- A deferred ticket whose blockers have merged, and whose worktree has no
  commits of its own but uncommitted changes, is no longer said to have a
  worktree safe to remove, in the START block's `DEFERRED_FREE` line or the
  box of a run with nothing to run: the line says the worktree holds
  uncommitted changes and gives `git -C <worktree> status` to see them.

### Fixed

- A worker whose MCP server asks it a question in the same second as the tool
  use that asks, on a filesystem with 1-second file times (HFS+, exFAT), reads
  blocked rather than working until its time limit.
- Closing a worker's cmux workspace, or the interview's terminal, no longer
  fails with `cmux closed other workspaces too` when the maintainer, or
  another orchestra run, closes one in cmux while orchestra waits for cmux to
  list the close: only what went by the time cmux answered it counts.
- A finished ticket whose worktree its worker left on another branch, or on a
  detached HEAD, is set aside with `OFF_BRANCH`, naming what the worktree has
  checked out. Before, the merge rebased and checked the other branch up to
  three times and ended in `MERGE_CONFLICT`, saying the base kept changing.
- A finished ticket whose branch was rebased a third time, as the base moved
  on after each of its first two rebases (commits made by hand), is merged
  when the base stayed put while that rebase was checked. Before, the merge
  ran the check and then gave up with `MERGE_CONFLICT`, saying the base kept
  changing.
- Ctrl+C while a ticket set aside for its failed check is checked once more
  no longer names it among the workers left running: only its check is cut
  short, and it stays set aside.
- Waiting to read the main checkout again after git failed no longer holds up
  the merges and the worktrees being made meanwhile.
- A worker start whose arguments the terminal refuses, with none left to drop,
  stops at once with `START_FAILED … the terminal refused the arguments`,
  rather than asking nine more times with the same ones.
- A run no longer opens the dashboard only to end at once when the only
  worker the last run left behind is in the other terminal.
- Ctrl+C as a finished run leaves its workers behind no longer turns its
  `READY_EMPTY` (exit 0) into `INTERRUPTED` (exit 130).
- In cmux, with no socket named in the environment (`CMUX_SOCKET_PATH`,
  `CMUX_SOCKET`), a failed call no longer reads as cmux not running, which had
  every worker's workspace read as closed and every worker as gone: cmux's CLI
  then finds the socket of whichever cmux runs, not only
  `~/.local/state/cmux/cmux.sock`.
- In cmux, two workspaces orchestra closes at once no longer each report that
  cmux closed the other too, and leave both in `.orchestra/run/cmux.json`:
  orchestra closes one at a time, and drops a closed workspace from its book
  even when the check finds another gone.
- A worker whose background subagent stops after its turn ended, its ticket
  still in progress, is no longer told to continue just as the subagent's
  answer starts its next turn: a subagent's stop after the worker's Stop
  hook keeps the turn going until the turn on its answer ends.
- A ticket left `unmerged` whose branch was squash-merged by hand and kept
  loses its label when a run starts: the base holds the branch's changes and
  a commit on it names the ticket. Before, it held the tickets it blocks in
  every later run.
- A main checkout with a local change to a file a ticket's branch renamed
  stops the merge with `DIRTY_TREE` naming that file, rather than
  `MERGE_FAILED`.
- Two full-check suites whose names begin alike past a ticket title's 60
  characters each get a ticket of their own, rather than sharing one.
- A ticket closed with a commit but uncommitted changes in its worktree is
  left for review as `CLOSED_UNCOMMITTED`, no longer `CLOSED_WITHOUT_COMMIT`.
- Exit code 4's meaning in the run report and `--help` names cmux along with
  Herdr and tmux.
- In tmux, a worker whose `claude` is a wrapper script that runs Claude Code
  as its child, rather than through `exec`, is no longer taken for gone, and
  concluded on, as soon as its first hook runs: only a pane orchestra didn't
  launch the agent in is gone once back at a shell's prompt.
- `orchestra plan` compares two tickets as the dispatch footprints do: a
  ticket naming only `Loop.merge` is ordered with one naming the small file
  that mentions it, and two tickets naming functions are compared by function
  in a file only when both name functions it mentions, so a stray function
  name no longer hides a file they share.
- A Claude worker whose subagent was cut off by Esc, which runs no
  `SubagentStop`, no longer reads working for the rest of its session nor
  holds its turn open: a subagent counts as running only while hooks report,
  and as ended once none has for 10 minutes.
- `orchestra init` no longer runs `bd init` while you have changes staged,
  which bd committed along with its own files: it says what is staged and to
  commit or unstage it, then run init again.
- A worker the last run left behind on a ticket deleted from `bd` since is
  dropped (`<id>, carried over from the last run, is dropped: bd has no such
  ticket`), rather than kept in `.orchestra/run/state.json` and reported
  `kept for the next run: bd can't show it` on every run.
- A worker waiting on a background subagent is no longer paused in the
  moment between the subagent's stop and Herdr showing the worker on its
  answer: its idle time counts from the hooks' last note, not from the
  Stop of the turn before.

- A check, setup or full check that prints without end, until its time
  limit, no longer holds its whole output in orchestra's memory: the output
  goes to a temporary file as it is printed, which a failure keeps in
  `.orchestra/run/`, and only its last lines, the lines that say what
  failed, its `FLAKY:` lines and its suite are kept, each bounded. Reading
  it is faster too, so a check that prints hundreds of MB isn't slowed down
  waiting for orchestra.
- `orchestra update` on a read-only filesystem gives the advice to run it
  as the folder's owner (exit 2), where it said only that the update failed.
  A busy line on a terminal that reports no width, as a fresh pseudo-terminal
  can, or one resized during the step, stays on one line instead of piling
  up its frames. `orchestra init` runs the scout at its own effort when
  `ORGAN_EFFORT` or `organ_effort` isn't an effort, rather than passing it
  to claude; another repository's record in `~/.claude.json` in a shape
  orchestra doesn't understand no longer fails init or a run; and MCP server
  names from `.mcp.json` are shown printable in init's choice. A settings
  file that is a symlink to no file yet is written where the link leads, as
  before it was replaced by a regular file.

- The commands orchestra gives to paste quote a path holding a space or a
  character a shell would read: `DEFERRED_FREE`'s `git worktree remove` and
  `git -C … status`, the full check's `CLEANUP_FAILED`, the notes on a
  ticket set aside to rebase by hand or for a run-files link, and
  `orchestra init`'s `export PATH=…` for bd. With such a repository or
  `WT_ROOT`, the pasted command broke or acted on another path.
- Footprints overlap where they missed: a bare name (`run.go`) means every
  file of that name, the top-level one no longer hiding the others; a
  folder or glob in the `files` metadata means the files under it or
  matching it; a ticket naming only `Loop.merge` overlaps one naming
  `loop.go`; and a stray name (`t.Run()`, `GitHub`) no longer turns off the
  shared-file check, which now compares by function only in a file that
  mentions functions both tickets name. `o.log.Raw()` names `Raw`.
- With tmux before 3.8, the hint to attach to the workers' session names
  the session tmux made for `--workspace foo.bar`, `foo_bar`, where it named
  `foo.bar`, which those versions can't find.
- Under tmux, `--workspace foo:bar` runs the workers in the session `foo_bar`
  on every tmux, as before 3.8: tmux 3.8 kept `foo:bar`, which the hint to
  attach to it couldn't name, as tmux reads `foo:bar` as session `foo`,
  window `bar`.
- The run report is `REVIEW_FAILED` when claude writes it empty or cuts it
  off at its output token limit, where it was shown blank or half-written.
  Organ answers from an older claude CLI, without a schema, are read when
  the JSON comes in a ```` ```json ```` fence or amid prose. `orchestra init`
  no longer adds a second check suite when the scout's runs
  `scripts/check.sh` as `$PWD/scripts/check.sh`, through `bash -c`, or
  before `&&`.
- A follow-up linked to the ticket it was discovered from in a second
  command (`bd create`, then `bd dep add … discovered-from`) waits for that
  ticket to merge, as one filed with the link does: the run could read its
  links between the two commands and start it without the source's code.
  The ticket about to start has its links read again, which also catches a
  blocker swapped for another.
- An asked ticket its worker closes as the run stops (`ASKED_UNMERGED`)
  says its worktree is left for the next run to merge, as the next run does,
  where it said left for review; docs/running.md said it wasn't carried over.
- An organ's verdict or a warning that came in as the dashboard closed is
  printed below the run's summary: it went with the alternate screen, and
  only the log had it.
- A run on a `TERM=dumb` terminal, or with a stdin that isn't a terminal,
  prints plain lines, as it does for a pipe, where it drew the dashboard.
- A finished ticket whose worktree has uncommitted changes to tracked files,
  as a check that rewrites them leaves, is set aside with `DIRTY_WORKTREE`,
  naming the files, when its branch needs rebasing; a rebase git refuses to
  start for another reason is `REBASE_FAILED`. Both were reported as a
  `MERGE_CONFLICT` left mid-rebase, which they weren't.
- The dashboard's tickets table cuts ticket IDs longer than a third of its
  width with `…`, so long IDs no longer squeeze titles and notes out of
  sight; IDs are measured in screen cells rather than bytes.
- Under tmux and cmux, a worker whose subagent waits on a permission prompt
  reads blocked while another subagent works, where that one's tool uses hid
  the prompt and the worker read working until the ticket's time limit.
- An older tmux than 3.0, which lacks what orchestra uses (`set-option -p`,
  `respawn-pane -e`), is a setup problem naming its version, rather than
  failing once workers start.
- A tmux server orchestra started no longer gives `BD_JSON_ENVELOPE=0` to
  every pane opened in it, the user's included: only orchestra's own bd
  commands get it.
- Under tmux, keys typed into a worker's pane that start with `-` or end in
  `;` arrive as given, where tmux took them for options or the end of a
  command; `return` is Enter, as under cmux.
- Under Herdr, a worker's tab the user closed already is no error when
  orchestra closes it, as under tmux and cmux; a `herdr tab create` answer
  naming a tab Herdr doesn't have says so, where it said the tab could not
  be closed.
- Under cmux, closing a workspace orchestra didn't open, when cmux can't
  list its workspaces, says so with cmux's error, where it said orchestra
  didn't open workspace "".
- `--concurrent 0`, `-c 0` and `ORCHESTRA_CONCURRENT=0` are a setup problem,
  as any number outside 1 to 16 is, where they were taken for unset and the
  run went on with `settings.json`'s number, or 1.
- The on-or-off variables (`NOTIFY`, `TRIAGE`, `REVIEW`, `PROMPT_AT_LAUNCH`,
  `ORCHESTRA_TICKETS`, `ORCHESTRA_NO_UPDATE_CHECK`) all take `1`/`0`,
  `true`/`false`, `yes`/`no` and `on`/`off`, and report anything else as a
  setup problem: `TRIAGE=false` or `NOTIFY=off` left the feature on, and
  `ORCHESTRA_TICKETS=true` was taken for off.

- Two runs on one tmux server, in two clones sharing a Beads database, no
  longer take each other's workers for their own when their tickets' IDs
  match: a worker's pane is marked with its run's repository, and a run
  finds, names and prompts only its own repository's workers.
- `orchestra init` no longer saves over a `.orchestra/settings.json` it
  can't read, such as one edited while its form was open to hold a value of
  the wrong type: it stops with the error and leaves the file as it was,
  where it wrote back only its own choices and lost every other setting.
- A ticket that started too early and went back in the queue to wait for
  its blocker (`WAITS_FOR_MERGE`) counts toward `--limit` once, as it starts
  again, rather than for each start: with a small limit, early starts could
  end a run `LIMIT_REACHED` with tickets left, and the done line counted
  them twice.

## [0.7.1] - 2026-10-14

### Changed

- Under cmux 0.65.0 or later, a worker's prompt is pasted into its input
  box with `cmux paste`, bracketed, so a prompt of several lines arrives
  whole, rather than going through `.orchestra/run/cmux-prompt-*.md`; and
  what orchestra types or presses in its workers' terminals goes with
  `--force`, which cmux 0.65.0's input guard needs before letting a key into
  a Claude Code dialog, such as the trust-this-folder one. orchestra reads
  cmux's version with `cmux version`, again whenever the app updates its
  CLI, and an older cmux is used as before.
- Under cmux 0.65.0 or later, orchestra closes a worker's workspace, and
  the interview's split, with `--force`, which cmux now needs to close a
  terminal a program runs in, and waits for cmux to list a workspace or
  split it opened, and to drop one it closed, as cmux answers before it
  does. A worker that `cmux top` doesn't list yet, as its list is a sample
  taken every few seconds, is no longer taken for gone.
- The cmux tests run against a real cmux in CI, on macOS, in place of the
  checks a person made by hand before a release (docs/development.md, The
  cmux tests): a cmux app of each test's own, with its socket open to the
  test, and a fake Claude Code.

### Fixed

- Left to choose its terminal, orchestra run in tmux started from a Herdr
  pane picks tmux, the terminal it runs in, rather than Herdr, whose
  `HERDR_ENV` and `HERDR_PANE_ID` tmux copied into its panes, even once that
  pane was closed. Herdr's variables beside tmux's or cmux's mean Herdr only
  when Herdr says orchestra runs in that pane; likewise cmux started from a
  Herdr pane.
- `orchestra init` refuses, changing nothing, while a run holds the
  repository's lock, naming the run, rather than rewrite the runners and
  `.gitattributes` under it (which stopped the run with `DIRTY_TREE`) and
  file tickets it would take up; and in a linked worktree, such as a
  worker's, where it wrote `.orchestra/` and the runners onto that ticket's
  branch.
- Run from a linked worktree, workers get the worktree's local-scope
  definition of an MCP server that `~/.claude.json` also defines for the
  main checkout, every run, instead of either one by chance.
- A `solo` ticket next in line keeps its place when a slot frees while
  every slot was taken: a failed check due to be tried again no longer
  takes the slot ahead of it. And a `solo` ticket adopted after its
  question, its worker carrying on in its tab, runs alone as a dispatched
  one does: no new ticket starts beside it.
- `orchestra init` and `orchestra feature` show what the scout and the
  screen organ say (the scout's note, suites and stack, the screen's reason,
  their errors) without the escape sequences and control characters a
  repository's files or a request could make them echo, in the form and in
  accessible mode.
- `bd ready` or `bd list` failing once or twice, as when a worker's `bd`
  holds the database, no longer stops the run with `READY_UNREADABLE`: the
  queue is read again a poll later, up to three times, and each failure is
  logged.
- In tmux and cmux, an idle worker whose screen shows the words of Claude
  Code's trust or MCP-servers dialog in its output, as a diff of orchestra's
  own terminal code does, reads idle rather than blocked: the screen is
  looked at for those dialogs only before the session's first turn.
- A worker the last run left behind that the next run can't check, as `bd`
  can't show its ticket or `git` can't list the worktrees for a moment, is
  kept for the run after (`<id>, carried over from the last run, is kept for
  the next run: …`) rather than dropped and forgotten.
- Quitting at once with a second Ctrl+C (or SIGTERM, SIGHUP) saves the
  workers the run leaves in `.orchestra/run/state.json`, those at work as
  left running `INTERRUPTED`, so the next run carries them over; it saved
  none before.
- The dashboard's totals and the run's closing "N set aside" leave out
  tickets a hold for the environment reopened: back in the queue, they show
  `◷ waiting`, and one that merges later counts only as completed. The
  event stream has a `reopened` event for them. The dashboard's Deferred
  total is now **Set aside**, and counts tickets left for review
  (`CHECKS_FAILED`, closed without a commit, …) as well as deferred ones.
- In tmux, workers' windows open in a session whose name holds `.`, as tmux
  3.8 keeps it: orchestra run in a session named `my.sess`, or with
  `--workspace foo.bar`, no longer fails to open any window. A session named
  after a repository folder holding `#` is made under that name, not one
  tmux expanded from it, and `--workspace foo.bar` works with tmux 3.0 to
  3.7 as well, in the session `foo_bar`.
- cmux no longer types a worker's launch line into a Claude Code still
  running in its workspace, where a retried start could answer its trust
  or MCP dialog or send the line as a prompt: a launch is refused while the
  last one runs. And under cmux before 0.65, a worker whose Enter was lost
  after the `@` line pointing at its prompt file is given Enter, rather
  than the line again.
- cmux starts workers in a repository whose path holds a quote, such as
  `~/Projects/Bob's app`: the line orchestra types to launch them no longer
  holds a backslash, which cmux refused to type.
- In tmux and cmux, a worker that was allowed a tool on a permission prompt
  no longer reads as blocked until the tool is done, which stopped the run
  with `BLOCKED >4min` while the worker ran a long command it was allowed,
  such as a test suite: Claude Code runs no hook as a prompt is allowed, so
  orchestra now checks its screen for the dialog, and reads the worker as
  working once the dialog is gone.
- A ticket back from a question whose earlier worker, told the answer is in,
  wasn't seen to take that up is read again: one working by then, as when
  it started just after Herdr gave up waiting, is adopted rather than
  renamed with a second worker started on its rebased branch.
- A ticket that asked a question earlier in the run, and whose worker, once
  it was answered, found it needs a ticket not merged yet, runs again once
  that ticket merges, as WAITS_FOR_MERGE says, rather than staying out of the
  run and being reported as set aside.

## [0.7.0] - 2026-10-13

### Added

- Workers can run in [cmux](https://cmux.com), each in a workspace of its
  own in orchestra's window, named with its ticket's ID: `"terminal":
  "cmux"`, `--terminal cmux`, or auto from a cmux terminal (Herdr in a
  Herdr pane and tmux in tmux come first). orchestra addresses cmux's
  workspaces and terminals by UUID, never by short refs, keeps those it
  opened in `.orchestra/run/cmux.json`, and closes only a workspace it
  opened that still has its ticket's name, checking afterwards that the
  right one went. As under tmux, workers must be Claude Code, whose hooks
  give their state; the feature interview splits orchestra's terminal. cmux
  answers only programs started in its terminals, so a run choosing cmux
  must start in one. `docs/development.md` has the checks to run by hand
  before a release that changes the adapter.

- Workers run in Herdr or in tmux. The project's `"terminal"` setting,
  `--terminal` or `ORCHESTRA_TERMINAL` chooses `herdr`, `tmux` or `auto`
  (the default): Herdr in a Herdr pane, else tmux when it is installed,
  else a setup problem saying to run from a Herdr pane or install tmux.
  Under tmux the workers' windows open in orchestra's own session, or,
  outside tmux, in a detached session `orchestra-<repository folder>`
  (`--workspace` names another), which the `START` block, the dashboard
  and the end of the run say how to attach to; workers must be Claude Code,
  whose hooks give their state. `.orchestra/run/state.json` records each
  carried-over worker's terminal: a run in the other terminal keeps it for
  a run in its own and says so. `orchestra init` checks for herdr or tmux.

- A run's `START` block, and the box of a run with nothing to run, list each
  deferred ticket in the run's scope whose blockers have all closed and
  merged, with the command that reopens it: `DEFERRED_FREE: <id>: its
  blockers have merged (<blockers>); to run it: bd update <id> --status
  open`, and the commands that remove its worktree when it has no commits
  beyond the base. A ticket deferred because it started before its blocker
  merged otherwise stayed deferred with nothing pointing it out. orchestra
  only lists them; a bd failure drops the list, with a line in the log.

- Claude workers' hooks also note each session's start and end
  (`SessionStart` with its source, `SessionEnd` with its reason) in
  `.orchestra/run/events`, and write the last notification to
  `.orchestra/run/notice.json`, from which orchestra can read
  whether a Claude session is idle, working, blocked on a prompt or dialog,
  gone or not yet known, for a terminal that can't read its screen as Herdr
  does. The environment probe's worker and the feature interview can report
  the same way, to `.orchestra/run/probe-session/` and
  `.orchestra/run/interview-session/` in the main checkout.

- orchestra's own check, `scripts/check.sh`, runs ShellCheck v0.11.0 on
  every shell script the repository tracks, `scripts/install.sh` among them,
  as `sh` where the shebang is `/bin/sh`. `scripts/shellcheck.sh` downloads
  the release's binary once into `~/.cache/orchestra-dev/`, shared by all
  worktrees, and checks it against pinned SHA-256 sums; offline with nothing
  cached, the check says it skipped ShellCheck, but fails under `CI=true`.

### Changed

- `orchestra init` counts cmux, on the `PATH` or in
  `/Applications/cmux.app`, as a terminal for the workers beside Herdr and
  tmux, and its last step says to start a run from a cmux terminal where
  cmux is there.
- Notes, log lines, stop reasons and the run report say `tab <id>` and "the
  terminal" where they said `Herdr tab <id>` and "Herdr", as workers will
  also run in tmux. The stop word `HERDR_FAILED` is now `TERMINAL_FAILED` in
  the log and `.orchestra/run/events.jsonl` (docs/events.md); exit code 4
  reads "a terminal (Herdr or tmux), Beads or git failure".
- The README, the docs, the orchestra skill and `CLAUDE.md` describe running
  workers in Herdr or tmux: what a run needs (`herdr` or `tmux`), where to
  start one, choosing the terminal and the tmux session
  (`docs/running.md`, Herdr or tmux), the dashboard's number keys under
  tmux, and how a worker's state comes from its hooks there
  (`docs/workers.md`, Workers in tmux).

- The dashboard's tickets table has no header row or rule under it: a bold
  `Tickets` label heads it, indented like `Current` over the workers but in
  a neutral colour of its own. A pane as tall shows as many tickets, and the
  line saved goes to the workers, whose boxes fit in a pane a line shorter;
  where the `Current` label is left out, the `Tickets` label goes with it.
- The worker prompt tells a worker to file a follow-up that changes or
  builds on what its ticket adds `--deps blocked-by:<ticket>`, not
  `discovered-from`, so it doesn't start before that ticket merges; and,
  under Close, when its ticket needs another ticket's work not on its base
  yet, not to build on that ticket's branch or copy its code, but to leave
  nothing committed, link it (`bd dep add`), reopen its ticket with a
  `Waiting on <other> to merge` note and stop, for orchestra to start it
  again once the other merges. `orchestra init` writes the new template; a
  project set up earlier keeps its prompt (init replaces it only with
  `--force`), so add the lines by hand, as docs/workers.md says.
- The README's **Set up a project** is a short section on what `orchestra
  init` does, what can change later and the Checks step; the flags, `FLAKY:`
  lines, `check.log`, the recheck and the full check are in `docs/setup.md`
  and `docs/running.md`; `docs/setup.md` now says a check should print
  `FLAKY:` lines and what orchestra does with them.
- The README's **Install** is the one-line install, `go install`,
  `orchestra update` and what orchestra needs; the install script's checks
  and variables, building from a clone, how `orchestra update` verifies a
  release, build provenance, the newer-release notice and reading a clone's
  version are in the new `docs/install.md`.

### Fixed

- A follow-up a worker files linked `discovered-from` its ticket, which `bd`
  doesn't count as blocking, no longer starts from a base without that
  ticket's code: it waits while its source runs, waits on a question or is
  closed but not merged (`<id> waits: filed by <source>'s worker; waiting
  for it to merge`), and starts once the source merges. Nothing to run says
  `N follow-ups wait for the tickets they were filed from to merge`, and
  `SCOPE_OPEN` names such a follow-up.
- A ticket whose worker stops it, with nothing done, on finding it needs a
  ticket not merged yet (a new `blocks` link to one running, asked, open or
  closed but not merged) is no longer deferred for good: orchestra reopens
  it, notes why, removes its worktree, branch and tab, says
  `WAITS_FOR_MERGE: <id> waits for <blocker> to merge` (event kind `waits`,
  `◷ waiting` on the dashboard), and starts it again once the blocker
  merges. It isn't triaged or counted toward the hold for the environment.
  With commits of its own, or a blocker merged already, it is deferred as
  before.

## [0.6.0] - 2026-10-13

### Added

- One line installs orchestra without Go:
  `curl -fsSL https://raw.githubusercontent.com/noesis-sol/orchestra/main/scripts/install.sh | sh`
  installs the latest release's binary for darwin or linux, amd64 or
  arm64, after checking its SHA-256 and, where OpenSSL 3 is installed, the
  signature of `checksums.txt`; in `/usr/local/bin` where it can write,
  else `~/.local/bin`, never calling `sudo`. `ORCHESTRA_VERSION` and
  `ORCHESTRA_INSTALL_DIR` choose the release and the folder.
- The dashboard's **In queue** box gives the ready tickets waiting out of
  the tickets not closed, `1 of 5` as the Workers box does (in a run scoped
  to a ticket, the scope's), and the one-line totals `queue 1 of 5`. The
  event stream's `dispatch` and `queue` records carry the count as
  `unclosed`.
- Releases carry binaries: pushing a `v*` tag runs the release workflow
  (`.github/workflows/release.yml`), which runs `scripts/check.sh` and
  attaches `orchestra_<version>_<os>_<arch>.tar.gz` for darwin and linux,
  amd64 and arm64, with `checksums.txt` and build provenance, to the
  tag's GitHub Release, its notes the version's section of this changelog.
  docs/development.md (Releasing) describes the release process.
- `orchestra update` replaces the binary with the latest GitHub Release's
  for its platform, the archive's SHA-256 checked against the release's
  `checksums.txt` and the new binary's `--version` before it is renamed into
  place. `--check` only reports (exit 1 when a newer release is out), and a
  build from a clone is replaced only with `--force`. It refuses while a run
  holds the repository's lock, and where the folder isn't writable names
  `sudo orchestra update` rather than calling it.
- Releases are signed: the release workflow signs `checksums.txt` with the
  release key (`scripts/sign-checksums`) and attaches `checksums.txt.sig`,
  and `orchestra update` verifies it with the public keys built into
  orchestra (`internal/update/release-keys/`) before trusting a checksum,
  refusing a release whose signature is missing or doesn't verify.
  docs/development.md (Releasing) describes replacing the key.
- A run on a release build ends, on a terminal, with a line naming a newer
  release when one is out (`orchestra update` installs it). The check runs in
  the background, asks GitHub at most once a day (the answer is cached in the
  user cache folder), and never delays or fails the run;
  `ORCHESTRA_NO_UPDATE_CHECK=1` turns it off.

### Fixed

- On init's "Tickets at the same time", options 2 and 3 no longer share one
  note: 3 adds a machine with room for three workers and their checks.
- `orchestra init`'s MCP server options wrap between words at the
  terminal's width, the rest of a long one under its label, instead of
  running past the edge and wrapping back to the margin.
### Changed

- `orchestra init`'s form ticks a question's answer once it is left: a select
  shows ✓ on its chosen option instead of keeping its `>` cursor, and a field
  not reached yet shows no cursor. Its stage header is a stepper showing the
  stages done, current and to come (`✓ Workers ──── ● Checks`); with
  `TERM=dumb` it stays the plain `Step 1 of 2 · Workers`.

## [0.5.1] - 2026-10-08

### Fixed

- A Claude worker waiting on a subagent (the verifier, say) is no longer
  told to continue or settled at its Stop hook while the subagent runs, and
  the subagent's tool uses keep it from the 10-minute idle grace. Workers
  start with fork mode off (`CLAUDE_CODE_FORK_SUBAGENT=0` in their
  settings), so they can run a subagent they wait on in the foreground.

## [0.5.0] - 2026-10-07

### Added

- `orchestra init`'s Checks step ends by asking whether to add a verification
  step for workers (`--verifier`, or `--verifier=false`). Yes writes
  `.claude/agents/verifier.md`, a subagent told the project's stack and its
  checks, which checks a ticket's change against the ticket in a fresh
  context and answers PASS or FAIL; and it appends a Verify section to the
  worker prompt, so a worker runs it before closing its ticket. The scout's
  answer gives the stack it is built with.

### Changed

- The run report's evidence lists each ticket merged or closed with no
  change in the run, with its title and close reason, cut to 400
  characters; the reviewer puts such a ticket, or a commit whose subject
  differs from its ticket's title, under Needs you only when the close
  reason doesn't explain it.
- A worker whose Stop hook a `PreToolUse` record with no turn of its own
  replaced (an agent Claude Code forks at the end of a turn, refused the
  tool) settles at that Stop, or is told to continue, rather than after the
  10-minute idle grace. The Stop and `UserPromptSubmit` hooks write
  `.orchestra/run/turn.json`, which tool uses don't replace.
- A worker that settles by the 10-minute idle grace after a tool-use hook,
  with no Stop hook after it, has the log say who wrote that record: when,
  the tool, the Claude Code session, the subagent (if any) and the
  transcript. It traces the record that replaces a worker's Stop.
- The plan organ's answer gives the epic as two fields, `epic_title` and
  `epic_description`, rather than an `epic` object, which often made
  Claude's first answer malformed and cost it a retry of one or two turns.
- The feature interview asks Claude to look a fact up in the repository
  itself and to dispatch a sub-agent only for a wide search, which runs
  without holding up the rest of the round, rather than send a sub-agent
  for every fact.
- The worker prompt ends with Working unattended, moved after Close. It
  names a fourth way a worker stops too early, a report because the turn has
  been long or a part is done; tells it to delete an invitation to redirect
  it or an offer to wait; and says that its context is compacted as it
  fills, so a long conversation is no reason to cut work short. `orchestra
  init` writes the new template; a project's own prompt changes only with
  `--force` or by copying the section.
- The feature interview gives Claude the description between
  `<pasted_content>` tags carrying a fresh random ID, in a pane and on the
  terminal alike, and its instructions say that text inside them was pasted
  and may hold instructions the user didn't write: Claude follows them only
  where the user's own message asks it to.
- Each organ's input (triage, the predictor, the screen, the plan and the
  run report) gives the evidence first and orchestra's request last, after
  it, as Claude reads long inputs best with the request at the end. The
  system prompts are unchanged.
- A worker that has finished its turn reads `done` in plain green in the
  dashboard's Current box, rather than the yellow of a ticket set aside.
  docs/dashboard.md names the colours of a worker's states.
- The dashboard's tickets table keeps its place and height while workers
  start, report and finish: it takes about a third of the pane, for 3 to 10
  tickets, blank rows filling it until there are as many, and changes only
  on a resize. A pane too short for it and a line per worker the run may
  have leaves it out. Each worker's box keeps a line for its latest action
  and the boxes are equally tall, so they switch to a line per worker only
  as workers start and finish, or on a resize.
- Claude workers' hooks also record permission prompts, compactions and
  subagents (`PermissionRequest`, `PreCompact`, `SubagentStart` and
  `SubagentStop`). A worker waiting on a permission prompt shows as
  `permission — waiting for you` (`■ allow?` in the tickets table), even
  when Herdr shows it idle; the log says `PERMISSION_PROMPT` with the tool,
  and the run stops for it after 4 minutes with `BLOCKED`, as for a blocked
  worker, rather than with `PAUSED` after the 10-minute idle grace. A worker
  running a subagent shows `subagent`; each compaction of a worker's context
  is logged (`A's worker's context was compacted (auto)`). Triage's evidence
  names the permission prompts and compactions there were.
- The run report's reviewer is given, for each ticket, the permission
  prompts its Claude workers waited on and the compactions of their
  context in the run (read before each worktree is removed), and adds the
  counts to the ticket's bullet.
- Claude workers get the worker prompt's standing rules in their system
  prompt (`--append-system-prompt-file .orchestra/run/rules.md`), which
  survives compaction of a long ticket's conversation; their first message
  carries only the ticket and what an earlier attempt left. Workers of
  other kinds still get the whole prompt as their first message.
- `orchestra init` says what the scout's call cost, on a `scout` line of
  its summary (even when the form is cancelled), as it has no log for an
  `ORGAN` line.
- `orchestra init`'s scout is capped at $2.00 (`--max-budget-usd`) when
  `claude --help` lists the flag, as well as at 5 minutes; a scout stopped
  there says so (`the scout was stopped at its budget of $2.00: …`).
  docs/organs.md has the costs measured to pick it.
- Organ calls say what they cost: each is logged with its cost and turns
  (`ORGAN triage: $0.0123 in 1 turn, …`), the reviewer is given what the
  run's organs cost, and the run report ends with an `Organs:` line giving
  the total. A call that ends on an error result names it (`claude reached
  its budget limit: …`, `refused to answer`, `gave no answer that matches
  the schema`) instead of `claude reported an error: ` with nothing after
  it, and `organ.CallError` lets callers tell which.
- A Claude worker's hooks record its session (`.orchestra/run/session.json`,
  from a `SessionStart` hook). A worker gone from its tab with its ticket in
  progress no longer stops the run with `PAUSED` when its session can be
  resumed: orchestra starts `claude --resume <session>` in a new tab in its
  worktree, told to carry on (`RESUMED: …`), once per ticket in a run; the
  next run resumes one gone again, or carried over gone. Triage's evidence
  includes the end of the worker's transcript.
- A ticket back through `bd ready` once its question is answered, its
  worker gone from its tab, has that worker's session resumed in a new tab
  (`RESUMED: <id> is back, …`), told the answer is in and to claim the
  ticket again, instead of a new worker starting over; once per ticket in a
  run. A ticket back from a deferral still gets a new worker.

- A worker handed a rebase stopped on conflicts is now asked to run the
  check once the rebase has finished, not before `git rebase --continue`,
  as the commits that apply cleanly after a resolved one can break it too,
  and to `git rebase --skip` a commit whose change the base has already.
  orchestra accepts a resolved branch with fewer commits than the ticket
  had; one left with no change of its own closes with no change to merge,
  without running the check.
- `orchestra init` offers the setup command where the repository has a
  lockfile and `settings.json` has none: its Checks step asks for it,
  pre-filled with `npm ci` for `package-lock.json`, `pnpm install
  --frozen-lockfile`, `yarn install --frozen-lockfile`, `uv sync`, `poetry
  install` or `bundle install` for the others. `--setup "<command>"` sets it
  without asking and `--setup ""` removes it; an existing one is kept, and
  without a terminal the summary names the flag.
- A check that fails on a finished ticket's rebased branch, after a clean
  rebase or once its worker has resolved a conflict, is handed back to the
  worker, idle in its tab, to fix (`FIXING: …`, `⟳ fixing` on the
  dashboard): it is given where the check's output is and the lines saying
  what failed, and what landed on the base since its branch was cut, and
  commits the fix, naming the ticket. orchestra then checks the commits and
  runs the check again; a fixed branch merges in the same run. Twice at most
  by default (`"check_hand_backs"` in `settings.json`, `0` for never), not
  for a check that timed out, and not while the run winds down or holds.
  When the attempts run out, the ticket is set aside as before, with a note
  naming each attempt.
- After stage 2, `orchestra init` files the test work its choice calls for
  (label `orchestra-tests`), listed in its summary: with "Create from
  scratch", an epic and a P1 `solo` ticket that sets the tests up with the
  create-check-suite skill; with the suites as they are, a P1 `solo` ticket
  that gets both runners passing, under an epic with "Map the untested
  areas", which waits for it, when the untested areas are to be ticketed.
  Run again, it files nothing already open.
- The worker prompt `orchestra init` writes names `scripts/check-fast.sh` as
  the check to pass before closing a ticket, with one of its suites' commands
  to run while iterating, and `scripts/check-full.sh` for a change that
  touches what the slower suites cover. It asks a ticket that changes
  behaviour to add or update tests in the project's suites, never a new test
  framework.
- A run that ends by itself (`READY_EMPTY`, `LIMIT_REACHED`, `DRAINED`)
  having merged a ticket runs `check_full` (`scripts/check-full.sh`) once,
  after its last merge and before the run report, on the branch's head in a
  worktree of its own, removed afterwards, for at most `check_full_timeout`.
  A pass is a `FULL_CHECK passed` line. A failure is a `FULL_CHECK_FAILED`
  warning and a notification; its whole output is kept in
  `.orchestra/run/check-full.log`, its end goes to the log, the reviewer and
  the run report, and a P2 bug `Full check fails: <suite>` (label
  `check-full`) is filed, unless one is open for that suite. Ctrl+C skips
  it. The event stream records it as a `full_check` record. The runners
  print `SUITE: <name>` as each suite starts, which names the suite.
- Stage 2 of `orchestra init`, Checks, looks for the project's suites with
  the scout once it is reached, under a spinner (Esc skips it), then offers
  three choices: use the suites found as they are, ticked in two checklists
  (on every merge, check-fast; at the end of a run, check-full), with an
  option to file tickets for untested areas; create the tests from scratch
  with the create-check-suite skill; or type the check command by hand. A
  scout that fails lands on the last, saying why. It asks both checks' time
  limits, and whether to keep or replace a runner or skill copy that differs.
- `orchestra init` installs the create-check-suite skill, built into the
  binary, where the project's tests are to be created from scratch or its
  untested areas ticketed: in `.claude/skills/create-check-suite/` for Claude
  workers and `.agents/skills/create-check-suite/` for Codex ones, to be
  committed. A copy there that differs is kept unless replacing it is chosen.
  `init --agent` (default `AGENT_KIND`, else `claude`) names the workers'
  agent, as for a run.
- A new skill, `skills/create-check-suite/`, for a worker in a project with
  no tests or areas without them: it reads the repository, sets up unit and
  end-to-end suites in the project's own frameworks, adds them to
  `scripts/check-fast.sh` or `scripts/check-full.sh`, gets both passing and
  times them, maps the features in `FEATURES.md`, and files a P3 ticket for
  each untested area. Its tests are isolated and deterministic, and a test
  that finds a bug is committed skipped, naming a bug ticket.
- The scout's reads stay inside the repository. With a `claude` that lists
  them in its `--help` (Claude Code 2.1.289 and later), it runs with
  `--restricted`, which confines its Read, Glob and Grep to the working
  directory whatever an allow rule or `bypassPermissions` in your settings
  says, and `--permission-prompts none`. Restricted mode ignores the user,
  project and local settings files, so the scout doesn't use the default
  model set there. An older `claude` runs the scout as before.
- A project's checks are two runners, shell scripts `orchestra init`
  writes in `scripts/` and the project then owns: `check-fast.sh`, the
  merge check run on every rebased ticket, and `check-full.sh`, which runs
  `check-fast.sh` and then the slower suites, for orchestra to run once at
  the end of a run (in a later change). Each lists one command per suite,
  under a comment naming where it was found; a suite that can't run in two
  worktrees at once takes turns through a lock (a directory in the temp
  directory, taken over when its holder is gone). An existing
  `scripts/check.sh` is called as a suite, never edited. Re-run, `init`
  keeps a runner that differs from what it would write, and says so.
  `settings.json` names them as `check_fast` and `check_full`, with
  `check_fast_timeout` (30m) and `check_full_timeout` (60m); the old
  `check` and `check_timeout` are still read, as the fast ones, and `init`
  rewrites them under the new names. `init` takes `--check-fast`,
  `--check-full`, `--check-fast-timeout` and `--check-full-timeout`
  (`--check` and `--check-timeout` stay, as aliases), and a run takes
  `--check-full-timeout` (`ORCHESTRA_CHECK_FULL_TIMEOUT`).
- On a terminal, `orchestra --feature` (and `New feature` planned by the
  organs) shows its two waits on Claude as busy lines: a spinner and the
  time so far take the place of `screening the request with claude…` and
  `planning the feature with claude…`, which sat still for minutes. When the
  step ends, the line becomes its outcome: `◆ Request screened (8s)`,
  `◆ Feature planned (2m31s)`, or `◆ Planning stopped` after Ctrl+C.
  With `--plain` or without a terminal, the lines are unchanged.
- A new organ, the scout, finds the test suites, lints, type checks and
  builds a project already has, for the checks step of `orchestra init` to
  offer (which uses it in a later change). It is the one organ with tools,
  only the read-only Read, Glob and Grep, started in the repository's root
  for at most 5 minutes: each suite comes with its command, the `file:line`
  that defines it, a fast or full tier, whether two copies can run at once,
  and what it needs. An existing `scripts/check.sh` comes first.
- On a terminal, the organ phase's two waits after a run show they are
  under way: a spinner, in the dashboard's style, and the time so far take
  the place of `◆ Finishing triage…` and `◆ Writing the run report with
  Claude…`, which could sit still for a minute or more. When the step ends,
  the line becomes its outcome: `◆ Triage finished (12s)`, `◆ Run report
  written (1m04s)`, `◆ Run report skipped` after Ctrl+C, or the
  `REVIEW_FAILED` warning. Ctrl+C still skips them. Plain output and the log
  are unchanged.
- `orchestra init` asks in two steps, a screen each under its header:
  **Step 1 of 2 · Workers** (installing Beads, tickets at the same time, the
  `CHANGELOG.md` union, MCP servers), then **Step 2 of 2 · Checks** (the check
  command and its time limit). Shift+Tab goes back a step with its answers
  kept, Esc now cancels as Ctrl+C does, and nothing is written or installed
  until the last step is submitted. A step the flags answer whole is skipped.
- In a pane 120 columns wide or wider, the dashboard's Current boxes go two
  to a row instead of each taking the full width: four workers show as a
  grid of two by two, numbered 1 2 over 3 4, so the number keys are
  unchanged. The two boxes in a row are equally tall, and an odd one out
  keeps the left column's width. A narrower pane, such as Herdr's split
  pane, stacks them as before.
- `orchestra -h` and the command's package comment say it works on as many
  tickets at once as `--concurrent` allows, rather than one ticket at a
  time.
- The orchestra skill's description says orchestra runs several workers side
  by side, as many tickets at once as the project allows, rather than one
  ticket at a time.
- **New feature** talks the feature through with Claude Code in a Herdr pane
  split off orchestra's, to its right, rather than on orchestra's own
  terminal, so orchestra stays in view, saying where the interview is and how
  it ends. The description, every line of it, goes in
  `.orchestra/run/feature-request.md`, which Claude's first message attaches.
  `/exit` in Claude's pane, or closing it, returns to orchestra, which closes
  the pane and shows the filed tickets and the question in its own pane, with
  the keyboard focus. Ctrl+C in orchestra's pane, SIGTERM or SIGHUP close the
  interview's pane and stop orchestra (exit code 130). When Herdr can't split
  the pane or start Claude Code in it, orchestra says why and hands Claude Code
  its terminal, as before.
- The interview in a pane ends by itself once the feature is filed: when
  `.orchestra/run/feature.json` names the epic and Claude's turn is over,
  orchestra closes Claude's pane and shows the tickets and the question, so
  you no longer type `/exit`. A `feature.json` written while Claude is still
  working leaves the pane open until that turn ends. `/exit`, or closing the
  pane, still ends an interview that files nothing. On orchestra's own
  terminal, where orchestra can't end the session, Claude's instructions add
  that it tells you to type `/exit` once the feature is filed.
- While the interview runs in its pane, orchestra's pane shows where it
  stands on a line under its message, updated in place each second: whether
  Claude is working, waiting for you or waiting for a permission answer, and
  how long the interview has run; once the feature is filed, `Filed <epic>:
  <title> (N tickets)`. The line is gone before the tickets and the question.
- The functions that read text a model, a worker or a ticket's author wrote
  have Go fuzz tests: the plan organ's answer, a ticket's footprint, which
  ticket IDs can be run, the Herdr agent name made from a ticket's ID,
  `herdr agent get`'s output and `bd list --json`. The check runs their seeds as ordinary tests;
  docs/development.md says how to fuzz them.
- orchestra's own check, `scripts/check.sh`, runs the tests shuffled
  (`go test -shuffle`), in a new order each time, to find a test that passes
  or fails only after another. Its `FLAKY:` lines, and the end of a failed
  check's output, name the order's seed, and docs/development.md says how to
  run a package's tests in that order again. go test caches no shuffled run,
  so the check runs every test each time.
- orchestra's own check, `scripts/check.sh`, also runs golangci-lint's
  modernize linter, which asks for the newer standard-library forms:
  `slices.Contains` for a hand loop, `strings.Cut` for index arithmetic,
  `WaitGroup.Go` for `Add`, `go` and `Done`, `errors.AsType` and the like. The
  code uses them throughout, with no change in what orchestra does.
- `scripts/check.sh` also runs golangci-lint's errorlint, which asks for
  `errors.Is` and `errors.As` rather than `==` or a type assertion on an
  error, so a wrapped error still matches. The five places that compared an
  error with `==` or `!=` use `errors.Is`, with no change in what orchestra
  does.
- A ticket set aside because its check failed (`CHECKS_FAILED`) is checked
  once more after another ticket merges: its branch is rebased onto the base
  and the check runs again, through the merge queue and in one of the run's
  slots, before any new ticket starts. If it passes, the ticket merges, its
  `unmerged` label removed, and the tickets it blocks start; if the rebase
  stops on conflicts, its worker is asked to resolve them, as for any merge;
  if the check fails again, the ticket stays set aside (`CHECKS_FAILED: …
  fails again`). Each ticket is checked again once at most, and none while
  the run winds down or holds or a `solo` ticket runs, nor one taken up since
  it was set aside: reopened, its worktree or branch changed, or its worker at
  work again (`<id> is not checked again: …`). The `CHECKS_FAILED` line says
  whether it will be: not when the check failed in a package or file the
  ticket's own commits change, which another ticket won't fix. On 2026-10-04
  two tickets set aside for flaky tests that later tickets fixed stayed aside
  for the rest of the run, holding up the tickets they blocked, and by the
  next day one no longer compiled against main.

### Fixed

- A worker left waiting on a question, whose ticket is closed before the
  next run with nothing committed, is taken up by that run rather than
  dropped as merged by hand when its branch was cut just after a commit
  naming the ticket (an earlier attempt at it, say): `.orchestra/run/state.json`
  notes where each worker's branch was cut (`fork`), and only the branch's
  own commits since then count as its merge.
- Triage gets the end of a worker's transcript when one of its last lines
  is very long (a screenshot, a large tool result): the transcript is read
  back from its end a line at a time, skipping lines over 256 KB, instead
  of from 512 KB before its end, where such a line left nothing.
- A worker gone from its tab, carried over from the last run or back at
  work after its question, is resumed even when the run takes no more
  tickets (its limit reached, `DONE_SO_FAR`, winding down), rather than
  stopping the run with `PAUSED` and being saved, with another note on its
  ticket, for the next run to stop for again. One that can't be resumed
  then (no session, or resumed once in the run already) is warned about
  (`WORKER_GONE: … so it is no longer followed`), noted and dropped.
- A finished ticket whose fast-forward git refuses because the main
  checkout has uncommitted changes to files the ticket changes under
  `.orchestra/`, `.claude/` or `.beads/` (which the check before a merge
  leaves out) stops the run with `DIRTY_TREE` naming those files, rather
  than with `MERGE_FAILED` saying the branch does not fast-forward. A
  `MERGE_FAILED` line now says what git said.
- A resumed worker whose start fails (Herdr refusing the tab, Ctrl+C) no
  longer loses the session it was to resume: its `.orchestra/run/session.json`
  is kept until the resumed worker's hook writes it again, so the next run
  resumes the session rather than dropping the ticket with `WORKER_GONE`.
- `orchestra feature` accepts the screen organ's ok verdict without a
  reason, rather than stopping a valid request with `orchestra couldn't
  screen the request`. Reject and unclear still need one.
- The plan organ reads the project's instructions behind a `CLAUDE.md` that
  is blank or holds only imports, such as a lone `@AGENTS.md`: the files it
  imports, read inside the repository only, or else `AGENTS.md`. It saw the
  literal `@AGENTS.md` before.
- A ticket that comes back while Herdr shows its earlier worker as
  `unknown` is read again for 15 seconds before a new worker takes it. One
  that stays unknown counts as still at work: it is adopted if its ticket
  waited on a question, and otherwise the run stops with `AGENT_BUSY`,
  rather than renaming it, rebasing its branch under it and starting a
  second worker beside it.
- With `TERM=dumb`, the end of input (Ctrl+D) at any of the plain-line
  questions of `orchestra init` or of what a run should work on cancels it,
  as Esc does in the terminal's form, rather than taking the default
  answers: `orchestra` no longer starts a run, and `init` no longer goes on
  to install Beads and write its files. Ctrl+C at the run's question stops
  it at once rather than after the next line.
- A ticket bd fails once to show as its worker settles (another bd
  holding the database, say) is read again, up to three times, before the
  run stops with `STATUS_UNREADABLE`: a finished ticket is no longer left
  unmerged by one failed read.
- A worker resumed with its session that doesn't take the message it was
  launched with now gets it pasted, or the run stops with `never took up
  carrying on`, rather than sitting out the 10-minute idle grace and
  stopping with `PAUSED`: its ticket, claimed by the worker before it, and
  that worker's actions on its screen no longer count as its taking it.
- Ticket titles, worker-filed follow-ups, organ text (triage summaries, the
  run report, a feature plan) and log lines are shown without escape
  sequences or other control characters, on the dashboard, in printed and
  `-plain` lines and in the interview's line: a title can no longer write
  the clipboard (OSC 52), clear the screen or set the window title.
- A ticket blocked by one waiting on a question, or carried over from the
  last run, no longer starts before that ticket merges when its worker
  closes it in its tab: until the run adopts and merges it, the tickets it
  blocks wait for it (`waiting for <id> to merge`), and so does its parent,
  whichever event starts tickets next.
- Ctrl+C or SIGTERM while a run reads the last run's state no longer
  forgets the workers it left behind: the run ends `INTERRUPTED` with
  `.orchestra/run/state.json` as it was, and the next run carries them over,
  rather than dropping each as one `bd` couldn't show. Ctrl+C while it lists
  the tickets left `unmerged` exits as interrupted, not `READY_UNREADABLE`.
- `orchestra init` never writes a runner that runs itself: a suite whose
  command runs `scripts/check-fast.sh` or `scripts/check-full.sh` (the
  scout listing them on a project set up before, `bash
  scripts/check-fast.sh`, an absolute path, `--check-fast
  scripts/check-full.sh`) is left out, with a note, where each merge check
  would have recursed until fork failed. The scout is told to leave the
  runners out, and `bash` or an absolute path to the runner keeps it as it is.
- A ticket labelled `unmerged` whose branch has no commits of its own (closed
  with its work uncommitted) is no longer taken for merged by hand because an
  older commit on the base names it, such as another ticket's mentioning it or
  an earlier attempt at a reopened ticket: the next run removed its label and
  started the tickets it blocks without its work. Only commits its branch made
  since it was cut count, from the merge-base orchestra now notes in the
  ticket's metadata (`unmerged_fork`) as it labels it; for a ticket labelled
  before, the branch's last commit must name it. The same goes for a
  carried-over worker's ticket closed meanwhile.
- A worker resumed with its session keeps the files it edited before in its
  ticket's footprint (`.orchestra/run/edits` is no longer removed), so a
  ticket naming one of them still waits for it, and `LIKELY_CONFLICT` still
  warns about them.
- `orchestra init` with `TERM=dumb` says what the scout found, or why it
  can't run, when the scout ends at once, as it does without `claude`; the
  line was left out.
- The full check no longer fails in an npm or Python project for want of
  dependencies in its new worktree: with `"setup"` in
  `.orchestra/settings.json`, it runs there first, always, within
  `check_full_timeout`. A setup that fails is `FULL_CHECK_FAILED: the setup
  '<setup>', run before '<check>', fails on <base> at <commit>`, its output
  kept in `.orchestra/run/check-full.log`, and files no ticket.
- A finished ticket whose commits, taken together, change nothing (a commit
  and its revert, as when its worker found the change on the branch already)
  closes with no change to merge, before any rebase, as one with no commits
  does: its worktree, branch and tab are removed and the tickets it blocks run.
  It was rebased, which replayed the revert onto the branch's own change, and
  set aside as `MERGE_CONFLICT`, holding its dependents. One whose rebase drops
  every commit, the branch having its changes already, closes so too, without
  running the check.
- A finished ticket rebased over a merged ticket's dependency bump no longer
  fails its check against the dependencies its worker installed before:
  `"setup"` in `.orchestra/settings.json` (such as `"npm ci"`) runs in the
  worktree before the merge check, on a merge, a RECHECK and a hand-back's
  resolution, whenever the rebase changed a dependency manifest or lockfile
  (`"setup_files"`, globs, with the usual ones by default). The log says
  `<id>: package-lock.json changed in the rebase; running 'npm ci' before the
  check`; a setup that fails is `CHECKS_FAILED`, its output in the worktree's
  `.orchestra/run/setup.log`.
- Esc or Ctrl+C at "What should this run work on?" no longer closes the
  terminal's input while Bubble Tea still reads it, a data race the race
  detector caught about once in 180 runs of the test that cancels it.
- A ticket closed with no change of its own, its branch with no commits
  beyond the base and its worktree clean, as when tickets merged before it
  did what it was about, has its worktree, branch and tab removed as after a
  merge, with `<id> closed with no change: … nothing to merge` in the log
  and `no change to merge` on its dashboard row. It was labelled `unmerged`
  and left for review as `CLOSED_WITHOUT_COMMIT`, holding the tickets it
  blocks, and the run report asked you to remove it by hand. A branch with
  commits that don't name the ticket, or a worktree with changes, is still
  left for review.
- A ticket's `files` metadata entry that can't be a path in the repository,
  such as `""`, `./`, `.`, `/etc/passwd` or `../x.go`, is left out of its
  footprint, as is such a path in its text. It was kept as written, so two
  tickets listing the same one never ran side by side, and a ticket listing
  only `""` counted as naming something and missed the footprint predictor.
- The dashboard says `■ blocked` for a ticket whose work is done but which
  can't merge until you act, with why in a few words: `main checkout has
  uncommitted changes`, `merge conflict in <files>`, `git failed`, `does not
  fast-forward onto <branch>`. It counts under Needs you, which counted only
  questions. Such a ticket used to read `■ stopped` with its title, as a
  worker still running when the run stopped does, or `! review` for a
  conflict; `CHECKS_FAILED` and closed without a commit still read
  `! review`. The event stream gives the reason as `blocked` on the `hold`,
  `stop` or `warn` record.
- orchestra's own check, `scripts/check.sh`, no longer fails when another
  check is linting at the same time. golangci-lint locks one file for every
  worktree and gave up with `parallel golangci-lint is running` after 5
  seconds, so with several workers' checks and merge checks at once, a
  ticket with nothing wrong was set aside as CHECKS_FAILED. The check now
  waits for the other lint to finish (`--allow-serial-runners`).
- `orchestra plan` orders two tickets of the same priority filed in the same
  second by their subticket numbers as numbers, so `e.2` goes before `e.10`
  as the older one would; it compared IDs as text and proposed `e.10` blocks
  `e.2`. bd's `created_at` counts whole seconds, and the tickets of a planned
  feature are filed together. Hash IDs still compare as text.
- Workers started at the same instant are listed by ticket ID, as the
  dashboard lists them, in the INTERRUPTED and quit lines, the order tickets
  left running are labelled in, `.orchestra/run/state.json` and the run
  review's input. They followed Go's random map order and could change places
  from one listing to the next.
- A worker told to continue after a turn that ended with its ticket in
  progress gets the full 4 minutes blocked, or 5 in a status Herdr can't tell,
  counted afresh from then. The poll that told it didn't break the streak, so
  a worker blocked for 3 minutes before that turn ended and blocked again
  right after stopped the run a minute later with `BLOCKED >4min`.
- An agent that `herdr agent get` shows without an `agent_status` no longer
  reads as gone. A Herdr that renamed or dropped that field would have made
  every live worker look gone, and the run would have settled workers still
  at work. Such output is now a failed read, which orchestra logs and tries
  again, as when Herdr is busy. Only Herdr's `agent_not_found` (its answer
  for a pane without an agent, too) or a result with no agent reads as gone.
- The run lock is `orchestra.lock` in the repository's git directory
  (`.git/orchestra.lock` in the main checkout), where `git clean` never
  reaches, rather than `.orchestra/run/orchestra.lock`. `git clean -fdX` or
  `-fdx`, or `rm -rf .orchestra/run`, removed that file during a run: a second
  run then made and locked a new one and ran beside the first, on the same
  tickets, worktrees and branch, and `orchestra plan --apply` said no run was
  going. Once it holds the lock, a run also checks that the path still names
  the file it locked, and tries again if not. Scripts that read the lock
  (`lsof -t`, `jq .started`) need the new path. A run of an earlier version
  locks the old one: let it end before starting a run of this version.
- A failed merge check's whole output is kept in the ticket's worktree, in
  `.orchestra/run/check.log`, which the `CHECKS_FAILED` line names. The log
  keeps only the last 40 lines, and `go test` prints a failing test's name and
  error before its output, so a long failure message pushed out which test
  failed, and why.
- A passing check's lines starting with `FLAKY:` are warnings naming the
  ticket (`FLAKY: <id>'s check … passed, but a test failed and then passed on
  a rerun: …`), in the log, the dashboard, the event stream and the run
  report. A check that reruns a failed test and passes says so this way, as
  the README describes; the ticket merges as before, but the flaky test no
  longer goes unseen.
- The run report says what failed in a ticket's failed check, and whether in
  the ticket's code or elsewhere. The reviewer saw only the `CHECKS_FAILED`
  line, which says the output is elsewhere, and guessed: on 2026-10-04 it put
  two tickets' failures, flaky tests in a package neither changed, down to
  needing each other's change, and sent the maintainer to the log for a third,
  which failed on golangci-lint's lock. Its evidence now has, for each ticket
  set aside after its check failed, the lines of the output that say what
  failed (`FAIL` lines, the failing tests, a data race, a lint, vet or build
  error, a tool's own error), and the directories the ticket's own commits
  change. Its bullet says which it is, and **Needs you** says to rerun the
  check for a failure elsewhere, and to fix the ticket's code for one in it.
- A Herdr tab opened for a worker, or for the startup probe, is closed again
  when Herdr's answer to `herdr tab create` can't be used: output that isn't
  JSON, or that lacks the tab's or its pane's ID. Each retry used to leave
  another empty tab behind, and unreadable output failed with JSON's bare
  `unexpected end of JSON input`. The error now reads `unexpected 'herdr tab
  create' output`, with the output, and says what became of the tab: closed,
  found by the ID in the answer or else by its label among the tabs that
  weren't there before (an earlier tab with the same label, such as one left
  for review, stays open), or left open, naming it and saying why.
- So is a tab Herdr opened when `herdr tab create` itself fails without
  Herdr's own error: stopped at its 30-second limit or by Ctrl+C, or exiting
  without saying why. orchestra finds the tab by its label among the tabs that
  weren't there before and closes it, and the error says what became of it,
  as above. On Ctrl+C the error is now logged; it used to be dropped, so a tab
  left open went unmentioned. Finding the tab takes one more call to Herdr,
  so a Herdr that stopped answering can hold the run up to 30 seconds more
  before it stops; a second Ctrl+C quits at once. A call Herdr refuses opens
  no tab and is reported as before.
- orchestra's own check, `scripts/check.sh`, runs the race tests through
  gotestsum (v1.13.0, through `go run`) and runs a failed test once more, on
  its own: one that passes then failed only under load, such as several
  workers' checks at once, so the check passes with a line `FLAKY: <package>
  <test>` for it. Such a test used to fail the merge, leaving the ticket for a
  merge by hand. A test that fails its rerun still fails the check, as do,
  without a rerun, more than three failed tests, a data race, a panic and a
  package that fails outside its tests (goleak's check in `TestMain`).
  docs/development.md says how to reproduce a flaky test under load.
- `go test -short` skips orchestra's slow tests, for a quick check while
  iterating: those that run real git in `internal/dispatch`, those that run
  orchestra on a pseudo-terminal, and the others that take over a second.
  `go test -short ./internal/dispatch/...` takes a few seconds instead of
  about 25. `scripts/check.sh` still runs every test.
- The dashboard draws on the terminal's alternate screen, so resizing its
  pane leaves no earlier frames above it. Narrowing or shortening a Herdr
  pane reflowed the frame and pushed its top rows into the scrollback, which
  clearing the screen didn't reach, so each resize stacked another slice of
  the title and totals above the dashboard. The terminal's earlier output,
  which the dashboard used to clear, is there again after the run, followed
  by the run's summary (the title, the totals and every ticket, no longer cut
  to the pane's height), the closing line and the organ phase.
- The predictor's guess is cached on the ticket as a JSON list
  (`predicted_files: ["docs/User Guide.md"]`) rather than joined with
  commas, so a predicted path holding a space or a comma reads back whole.
  `docs/User Guide.md` read back as `docs/User`, taken for a new file in
  `docs/`, and the ticket could start beside one editing the real file.
  Predictions already cached with commas read as before.
- Ctrl+C while an answered ticket's earlier worker is being told to carry
  on no longer loses the ticket. The worker has the message by then, and
  may close the ticket after orchestra has exited, but the ticket was no
  longer waiting on its question, nor counted as running: it wasn't
  labelled `unmerged`, saved in `.orchestra/run/state.json` or named on the
  INTERRUPTED line, so no later run merged it, and the tickets it blocks
  started without its code. It is now left running, as an adopted worker's
  ticket is: labelled, carried over and named. Ctrl+C or a stop before the
  worker is told anything leaves the ticket waiting on its question, saved
  with it for the next run, which tells the worker the answer is in.
- A command orchestra stops, at its time limit or on Ctrl+C, no longer leaves
  behind what it started: git's hooks, its `git-remote-https` and ssh, a
  credential helper, whatever bd forks. The whole process group gets SIGTERM,
  so git still removes its lock files, and SIGKILL half a second later, where
  only the command itself got them before, and the rest ran on as orphans: a
  hook could go on changing the worktree while orchestra aborted the rebase
  that ran it. A command that exits by itself still leaves running what it
  started on purpose, such as a server bd starts.
- A project with `mcp_servers` set no longer refuses to start when Claude
  Code changes the shape of one of the records orchestra reads from
  `~/.claude.json` to tell which claude.ai connectors and whether Claude in
  Chrome are set up (`claudeAiMcpEverConnected` becoming a list of objects,
  say). Such a record is taken as not set, and the file's MCP servers are
  read as before; a file that isn't JSON, or whose `mcpServers` is malformed,
  still stops the run with "Cannot read Claude Code's MCP config".
- `orchestra init` and a run write `.orchestra/settings.json`,
  `.gitattributes` and git's `info/exclude` to a temporary file beside each,
  synced to disk, then renamed over it, as the run state already was, rather
  than rewrite them in place. A full disk, an I/O error or a crash while
  writing left the file empty or half written: your entries in `info/exclude`,
  which git doesn't keep, were lost for good, and a broken `settings.json`
  stopped the next run with `Unreadable settings`. Each file keeps its mode,
  and one behind a symlink is still written where the link leads. In a
  read-only folder the write now fails and leaves the file as it was.
- With `TERM=dumb` (an Emacs shell, some editor and CI terminals), where
  the forms ask their questions one per line, a question the form hides is
  no longer asked. Choosing **Current tickets** asked to describe the
  feature anyway, and wouldn't go on without a description, which was then
  thrown away; `orchestra init` asked for the number of tickets at the same
  time after 1 to 4 was chosen, and wouldn't take an empty answer.
- The init form's tests no longer fail on a busy machine, as when several
  workers' checks run at once before a merge: they typed every key at once,
  and a key typed before huh had moved the focus went to the field just left
  (`must be a whole number from 1 to 16 (got '')`, or the form never ended).
  They now type each key once the screen shows the field it is meant for.
- A terminal test that fails before orchestra exits no longer leaves it
  running: the test stops orchestra and waits for it before closing its
  terminal and removing its repository, so the failure isn't followed by a
  data race on the closed terminal, blamed on whichever test runs then.
- The plan organ's check of a ticket's files drops a path that, cleaned,
  starts or ends with a space, such as `./ NEW.md`, with a note. It kept
  ` NEW.md`, a file the model hardly meant, and the plan read back from its
  own JSON named `NEW.md` instead. A fuzz test found it.
- A ticket naming a path with letters outside ASCII, such as `docs/café.md`
  or `docs/日本語.md`, has that file in its footprint, so it no longer starts
  beside a running ticket that edits it. The path was cut at the first such
  letter, into `docs/caf` and `.md`, which named no file. A path run into
  text written without spaces, as in `loop.goを直す`, is still found.
- A ticket naming a path with an accent in the other Unicode form than git
  lists it, such as `résumé.md` with each é written as e and a combining
  accent (as Finder gives names) for a file git lists with é, has that file
  in its footprint, as git lists it. So does the check command naming it,
  and so do the files a worker edits, so a ticket naming one waits for the
  worker. The names were compared byte for byte, and the file was missed.
- The files a worker edits count when its worktree's path has an accent,
  such as a project folder named `projét`, and the worker names it with the
  accent in the other Unicode form than orchestra does: é written as e and a
  combining accent, as Finder gives names, or the reverse. They were taken
  for files outside the worktree and dropped, so a ticket naming one could
  start beside the worker.
- A run that held for the environment and took tickets again after
  `PROBE_OK` no longer ends with `ENVIRONMENT` (exit 7) on triage's verdicts
  about the tickets deferred before the probe. Triage takes deferred tickets
  one at a time, a model call each, so its verdicts on the tickets that
  failed at once could come after the probe, and two of them blaming the
  environment held the run again, now for good, although the probe had just
  shown the machine works and those tickets may have been running fine
  again. Those verdicts are still written to the tickets' notes; only the
  verdicts on tickets deferred after the probe count toward another hold.
- After Ctrl+C, a ticket waiting on your answer whose worker deferred it in
  its tab is set aside as deferred without going to triage, as other
  deferrals after Ctrl+C are. It went to triage all the same: before the
  INTERRUPTED line orchestra read the ticket, its worktree and its worker's
  screen for triage, each read allowed up to its time limit.
- A run no longer waits for room in triage's queue when it finds such a
  ticket deferred, which could leave it hanging until a second signal: with
  the queue full, triage waits for the run to take its last verdict. The
  ticket is set aside without triage instead, and the run says so
  (`<id> is not triaged: 64 deferrals wait for triage already`).

## [0.4.0] - 2026-10-04

### Changed

- A run with nothing to run says so and exits 0, instead of opening the
  dashboard, ending at once with `READY_EMPTY after 0 tickets` and asking
  claude for a run report about nothing. It has nothing to run when its own
  `bd ready` query finds nothing and `.orchestra/run/state.json` carries no
  worker over from the last run (in its scope, with `--ticket`). It shows
  `✓ All done`, with `Still open: <ids> (bd close <ids>)` for epics left
  open, or `○ Nothing ready to run` with what holds the tickets left:
  questions waiting for your answer, tickets waiting on others, in progress,
  deferred, closed but not merged (`○ Nothing under <id> is ready to run`
  and each ticket's `SCOPE_OPEN` reason, with `--ticket`). In a box on a
  terminal, as plain lines with `-plain` or without one; no screen clear,
  triage, report or notification. The event stream records `start`, `done`
  (`READY_EMPTY after 0 tickets: everything is done`, or `: nothing ready`)
  and `end` with code 0, and the log the done line. Runs that don't ask check
  once they hold the run lock (a feature run once its epic is filed); a run
  that asks checks when `Current tickets` is picked, and the option reads
  `Current tickets: none, all done` or `none ready (N open)` when none is
  ready. The orchestra skill's pane wait matches the new wording.
- A run whose ready tickets are all held back has nothing to run too: a
  parent whose subtickets aren't all closed and merged, or a ticket blocked
  by one closed but not merged, which `bd ready` lists but the run wouldn't
  start. `○ Nothing ready to run` counts them on lines of their own
  (`N tickets wait for their subtickets to merge`, `N tickets wait for their
  blockers to merge`), and the question's option reads `none ready (N open)`.

### Fixed

- The cursor of `What should this run work on?` and of init's `Tickets at
  the same time` stops at the first and last option, as the MCP servers
  choice's does, instead of wrapping around: with two options, every Up or
  Down used to move it, so it seemed to bounce between them. `/` no longer
  filters either choice's few options, and the help line no longer offers it.
- The dashboard starts again on a clear screen when the terminal resizes.
  A pane that narrowed under it (by a column, as a worker's Herdr tab opened)
  re-wrapped its frame, and the next frame was drawn over the middle of the
  old one, whose top half stayed above the dashboard for the rest of the run.

## [0.3.0] - 2026-10-03

### Added

- `orchestra` in a terminal asks first what the run should work on, once its
  startup checks have passed and it holds the run lock: `Current tickets: N
  ready` (the default, N from the run's own `bd ready` query) runs as before;
  `New feature` asks for the feature's description, several lines and not
  empty, and runs it as `--feature` would: screened, planned, shown and filed
  once confirmed. Esc or Ctrl+C at the question exits 130 with nothing
  changed. `--tickets` (or `ORCHESTRA_TICKETS=1`) skips it and runs the
  current tickets; `--ticket`, `--feature`, `-plain` and a run without a
  terminal don't ask either. The orchestra skill launches runs with
  `orchestra --tickets`, since a Herdr pane is a terminal.
- `New feature` talks the feature through instead of planning it with the
  organs: orchestra hands its terminal to an interactive Claude Code session
  in the main checkout (`claude --append-system-prompt-file
  .orchestra/run/interview-prompt.md -- "<description>"`), whose instructions
  adapt Matt Pocock's grilling skill (MIT; see THIRD_PARTY_NOTICES.md). Claude
  interviews you in rounds of numbered questions with recommended answers,
  looking facts up itself, until you confirm a shared understanding; proposes
  an epic and its tickets (descriptions, acceptance criteria, types,
  priorities, `files` metadata, blocks links); files them with `bd` once you
  agree; and names the epic in `.orchestra/run/feature.json`. After `/exit`,
  orchestra shows the epic's tickets and asks `Start the run on <epic> (N
  tickets)? [y/N]`; `y` runs it as `--feature` runs its plan. A session that
  files nothing ends with `No feature was filed; nothing to run.` and exit 0.
  Ctrl+C during the session is Claude Code's; SIGTERM and SIGHUP still stop
  orchestra. Without `claude` on the PATH, or with `--agent` other than
  claude, `New feature` plans with the organs as before and says why.
  `orchestra --feature "<request>"` is unchanged.
- A feature described at the question is recorded in the run lock
  (`.orchestra/run/orchestra.lock`) once it is typed, as `--feature` records
  it: a second run refused the lock names it.
- Ctrl+Z in the feature interview suspends orchestra with the Claude Code
  session, and `fg` brings both back. Claude Code stops only itself on Ctrl+Z,
  so orchestra, waiting for it, used to leave the terminal hung; now it stops
  its process group as the terminal would, and continues the session when it
  is continued itself (`kill -CONT`).

## [0.2.0] - 2026-10-02

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

- The README is a short overview (install, set up, run, the dashboard's keys,
  exit codes), and the reference moved to `docs/` unchanged: `dashboard.md`,
  `setup.md`, `running.md`, `workers.md`, `organs.md`, `events.md` and
  `development.md`. It no longer says orchestra works one ticket at a time,
  and its sample dashboard shows two workers.
- `orchestra --version`, or `-v` for short, prints the version; `-h` lists
  `-v` as the shorthand for `--version`. The README and the skill show these
  forms; `-version` still works.
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
- The dashboard's key hint reads `<key> to <what it does>`, each key bold in a
  colour brighter than its faint words, so the keys are what the eye finds:
  `1–3 to go to a worker's tab · s to stop after the current tickets`, and
  `s to keep taking tickets` while the run winds down; a narrow pane gets
  `1–3 worker tab · s stop after current`. It no longer says
  `ctrl+c stops now`: Ctrl+C needs no hint, and still stops the run at once. A
  run stopping for another reason names only the number keys, and with no
  worker running the hint line is left out.
- The stop question names its keys as the hint does: in the box
  (`y stop after current · n keep going`) and in the one-line prompt
  (`y/n`), y and n are bold in the hint's key colour, and what they do faint.
  The words and widths are unchanged.
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

- A worker launched just before Ctrl+C is carried over to the next run. The
  run recorded where a worker was only once Herdr had named it, up to three
  minutes after its launch, so Ctrl+C in that time left a worker at work in
  its tab but not in `.orchestra/run/state.json`, and the next run never
  adopted or merged it. A worker is now recorded as soon as its tab is open,
  and kept however its start ends (`START_FAILED` included). Cut short
  before Herdr named it, the run names it as it stops, so the next run finds
  it; one that never came up is replaced by a new worker when its ticket
  comes back.
- A worker launched just before Ctrl+C, and still unseen by Herdr as the run
  stopped, is no longer dropped by the next run as gone. It came up without
  the name the next run looks for it by, so once it had claimed its ticket
  that run warned `WORKER_GONE` and dropped it while it was at work in its
  tab, and before that it would start a second worker on the same worktree.
  `.orchestra/run/state.json` now holds each worker's pane as well, and a run
  that finds no worker under the name looks in that pane and names the
  unnamed worker there (`<id>'s worker in tab <tab> came up without its
  name; named it <name>`), only while Herdr still has the tab labelled with
  the ticket's ID, since Herdr numbers tabs and panes afresh after a restart.
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

[0.7.7]: https://github.com/noesis-sol/orchestra/compare/v0.7.6...v0.7.7
[0.7.6]: https://github.com/noesis-sol/orchestra/compare/v0.7.5...v0.7.6
[0.7.5]: https://github.com/noesis-sol/orchestra/compare/v0.7.4...v0.7.5
[0.7.4]: https://github.com/noesis-sol/orchestra/compare/v0.7.2...v0.7.4
[0.7.2]: https://github.com/noesis-sol/orchestra/compare/v0.7.1...v0.7.2
[0.7.1]: https://github.com/noesis-sol/orchestra/compare/v0.7.0...v0.7.1
[0.7.0]: https://github.com/noesis-sol/orchestra/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/noesis-sol/orchestra/compare/v0.5.1...v0.6.0
[0.5.1]: https://github.com/noesis-sol/orchestra/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/noesis-sol/orchestra/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/noesis-sol/orchestra/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/noesis-sol/orchestra/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/noesis-sol/orchestra/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/noesis-sol/orchestra/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/noesis-sol/orchestra/releases/tag/v0.1.0
