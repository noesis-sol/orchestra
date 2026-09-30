# Changelog

All notable changes to orchestra are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

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
  by itself. `prompts/worker-prompt.md` has the commands.
- `prompts/worker-prompt.md`, a reference worker prompt to copy into a project.

### Changed

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
- `prompts/worker-prompt.md` asks for ticket and question titles of at most 60
  characters, with details in the description.
- `prompts/worker-prompt.md`: a ticket that only CI can verify is closed once
  the local checks pass, with an "Awaits CI" note, instead of deferred; the
  batch's pull request runs every CI job.
- A ticket closed but left unmerged (`MERGE_CONFLICT`, `CHECKS_FAILED`,
  `CLOSED_WITHOUT_COMMIT`, …) is labelled `unmerged`, so the tickets it blocks
  wait in later runs too, not only in the run that set it aside; bd ready
  counts a closed blocker as done. The label goes when orchestra merges the
  ticket, or at the start of a run that finds it merged by hand (its branch on
  the base with a commit naming it, or, with the branch deleted, such a commit
  on the base). `LABEL_FAILED` says when bd can't add or remove it.

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
