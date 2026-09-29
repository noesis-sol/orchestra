# Changelog

All notable changes to orchestra are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Questions for the maintainer. A worker that needs a decision asks it as its
  own ticket, labelled `human`, that blocks the work ticket, and stops. The
  orchestrator never dispatches a question, shows the ticket as "? for you"
  with a "Needs you" count, and carries on; the report says how to answer.
  `bd human respond <question>` answers it, and the ticket returns to the queue
  by itself. `prompts/worker-prompt.md` has the commands.
- `prompts/worker-prompt.md`, a reference worker prompt to copy into a project.

### Changed

- Claude workers start with their prompt instead of having it pasted in. The
  prompt goes to `.orchestra/prompt.md` in the ticket's worktree (kept out of
  git through the repository's `info/exclude`), and the worker is started with
  a one-line instruction to follow it; Herdr can't pass line breaks.
  `-prompt-at-launch=false` / `PROMPT_AT_LAUNCH=0` pastes it as before.
- An idle worker whose ticket is still in progress gets 10 minutes to resume
  before the run pauses: it is usually waiting on its own background command.
- A returning ticket's branch is rebased onto the current branch before its
  worker starts, so its merge can fast-forward.
- A returning ticket's earlier worker is renamed (`<ticket>-1`, …) so the new
  worker can take the ticket's name; its tab is left open.
- Error messages in the log cut long arguments short.
- The active ticket's title wraps onto up to three lines instead of being cut
  off after one.
- `prompts/worker-prompt.md` asks for ticket and question titles of at most 60
  characters, with details in the description.

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
