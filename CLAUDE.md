# Project Instructions for AI Agents

This file provides instructions and context for AI coding agents working on this project.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:1105d646 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/core-concepts/sync-concepts.md for details and anti-patterns.

## Agent Context Profiles

The managed Beads block is task-tracking guidance, not permission to override repository, user, or orchestrator instructions.

- **Conservative (default)**: Use `bd` for task tracking. Do not run git commits, git pushes, or Dolt remote sync unless explicitly asked. At handoff, report changed files, validation, and suggested next commands.
- **Minimal**: Keep tool instruction files as pointers to `bd prime`; use the same conservative git policy unless active instructions say otherwise.
- **Team-maintainer**: Only when the repository explicitly opts in, agents may close beads, run quality gates, commit, and push as part of session close. A current "do not commit" or "do not push" instruction still wins.

## Session Completion

This protocol applies when ending a Beads implementation workflow. It is subordinate to explicit user, repository, and orchestrator instructions.

1. **File issues for remaining work** - Create beads for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Handle git/sync by active profile**:
   ```bash
   # Conservative/minimal/default: report status and proposed commands; wait for approval.
   git status

   # Team-maintainer opt-in only, unless current instructions forbid it:
   git pull --rebase
   git push
   git status
   ```
5. **Hand off** - Summarize changes, validation, issue status, and any blocked sync/commit/push step

**Critical rules:**
- Explicit user or orchestrator instructions override this Beads block.
- Do not commit or push without clear authority from the active profile or the current user request.
- If a required sync or push is blocked, stop and report the exact command and error.
<!-- END BEADS INTEGRATION -->


## Build & Test

```bash
scripts/check.sh                       # the full check: go vet, go test -race, golangci-lint (pinned, via go run)
scripts/check-fast.sh                  # the merge check: the same with go test -race -short
scripts/check-own.sh                   # a worker's check of its change against main: the packages it touches
go test -short ./internal/dispatch/... # one package while iterating, without its slow tests
go build -o /tmp/orchestra ./cmd/orchestra && /tmp/orchestra --version
```

- A new build, when asked for one, is that `go build` on main with a clean tree; installing it over
  `/usr/local/bin/orchestra` (root's) is the maintainer's: give them
  `! sudo install -m 755 /tmp/orchestra /usr/local/bin/orchestra && orchestra --version`. docs/install.md and
  the skill's "A new build of orchestra" say how to read the version.
- `-short` skips the tests that take over a second: real git in `internal/dispatch` (`gitRepo`), runs on a
  pseudo-terminal (`openTerminal`) and the others, each skipped where it starts. Drop it to run a package in full;
  the full check never uses it, the merge check does.
- `scripts/check-fast.sh` is orchestra's merge check for this repository (`check_fast` in
  `.orchestra/settings.json`): `scripts/check.sh -short`, so a change that fails lint is not merged, and a test over
  a second under `-race` that doesn't skip under `-short` holds every merge. `scripts/check.sh` is the full check
  (`check_full`), which orchestra runs once at the end of a run.
- A worker closes its ticket once `scripts/check-own.sh` passes after its last change, and leaves the whole suite
  to those two: it checks what the branch changed against main (or the Base it is given), committed or not, with
  go vet, the race tests without `-short` and golangci-lint on each Go package with a changed file, ShellCheck on
  the changed shell scripts, and the tests that read the changed docs, skill, changelog or worker prompt.
- The check runs the tests through gotestsum, which reruns a failed test once: one that passes then prints
  `FLAKY: <package> <test>` and the check passes. A FLAKY line is a bug to file and fix (docs/development.md).
- go.mod requires Go 1.26 (`go 1.26.0`) and pins `toolchain go1.27.1`, the version the project builds
  and tests with. Keep the toolchain at 1.26.5 or later, which fixes a race-detector hang in fork on
  darwin/arm64 (golang/go#79804).
- `TestLiveOrgans` calls the real `claude`; it is skipped unless enabled (its comment says how).

## Architecture Overview

orchestra is a Go CLI that works through a Beads backlog: it hands each ready ticket to a **worker** (a Claude Code
agent) in its own tab (Herdr's, a tmux window or a cmux workspace) and git worktree, and fast-forwards finished
tickets into the branch it runs on. **Organs** are its one-shot advisers (triage, the run report, init's scout):
`claude -p` with no MCP servers, and no tools but the scout's read-only ones (Read, Glob, Grep). docs/organs.md and
the orchestra skill's opening define both terms; use them consistently.

- `cmd/orchestra`: flags and settings, choosing the terminal (`terminal.go`: the one it runs in, Herdr, tmux or cmux,
  else tmux; each terminal a row of `terminalKinds`), `orchestra init`, `orchestra plan`, the dashboard or plain output,
  signals, the organ phase after a run.
- `internal/dispatch`: the run loop, one file per concern (docs/development.md lists them). It reaches Beads, the
  terminal (Herdr, tmux or cmux), git and workers' reports only through the interfaces in `deps.go`.
- `internal/agentio`: what the loop and its adapters share of a worker's agent, a leaf package: its state as the
  terminal reports it (`State`), what every terminal keeps to (its refusals, how long it waits, `ScreenLines`), and the
  records its hooks write (`ToolUse`, `HookRecord`, `Session`, `Reporter`). dispatch aliases its types; the terminals
  and `internal/claude` import it rather than dispatch.
- `internal/tracker`: what the loop and the Beads adapter share of the backlog, a leaf package: a ticket as the loop
  sees it (`Ticket`, its `Status`), the links between tickets (`Link`), the labels and metadata the loop reads
  (`HumanLabel`, `UnmergedLabel`, `SoloLabel`, `FilesKey`) and `ErrNoTicket`. dispatch aliases its types;
  `internal/beads` imports it rather than dispatch.
- Adapters: `internal/beads` (bd; `internal/beads/bdjson` runs it and reads its JSON envelope, for `internal/project`
  too), `internal/herdr` (Herdr), `internal/tmux` (tmux: windows for workers, their state from their hooks),
  `internal/cmux` (cmux: a workspace per worker, kept by UUID in `.orchestra/run/cmux.json`, their state from their
  hooks), `internal/hookterm` (what tmux and cmux share: their errors, the agents whose hooks they read
  (`AgentUI`), waiting for a worker's state, the end of a prompt, key names, free agent names, probing a socket),
  `internal/git` (git), `internal/command` (every external command: context, time limit, process group),
  `internal/claude` (workers' hooks, which are orchestra's own binary run as the hidden `orchestra hook <event>`,
  the state they give, and what their screen adds to it), `internal/mcp` (discovering MCP servers for workers).
- Test helpers: `internal/faketool` (fake commands), `internal/claudetest` (a fake Claude Code that runs its hooks,
  for the tests on a real tmux or cmux), `internal/tmuxtest` and `internal/cmuxtest` (a tmux server or cmux app of
  the test's own), `internal/watchdog` (stops what such a test started once the test binary exits, however it
  exits), `internal/gittest`.
- `internal/agentkind`: the workers' agent kinds, a row each (`Of`): what a worker of the kind is given and told, and
  how its screen is read (`ScreenReader`). `internal/claudescreen`: Claude Code's screen, its glyphs and dialogs in one
  package: its input box, latest action, usage limit, start-up and permission dialogs.
- `internal/update`: `orchestra update`, replacing the binary with the latest GitHub Release's.
- `internal/requires`: a project's requirements (`requires` in settings.json): their checks run side by side, the
  installs of those missing one at a time, for a run's start, `orchestra init` and `orchestra requires`.
- `internal/load`: the machine's load for the dashboard's load line: each worker's process-tree memory from one `ps`
  listing, the kernel's memory pressure, swapping and CPU load (macOS and Linux), and the thresholds that colour it.
- `internal/reap`: closing a worker's tab (`reap.Close`, through `reapingTabs` in `cmd/orchestra`): the process groups
  below its processes (each terminal's `TabProcesses`) are noted first, and those Claude Code's shutdown leaves are
  stopped.
- `internal/organ`: the organs, one file each, with its prompt in `prompts/`. `internal/project`: `.orchestra/`
  (settings, worker prompt, init, per-ticket files). `internal/tui`: the Bubble Tea dashboard and init's screen.

## Conventions & Patterns

- Commit messages: `orchestra-<id>: <imperative summary>`, naming the Beads ticket, with a body that says why.
- Lines up to 120 columns (lll); doc comments on exported identifiers; `golangci-lint` clean.
- Errors: wrap with `%w`; inspect with `errors.Is`/`errors.As` on typed errors (e.g. `herdr.Error`), never by
  matching error text; a loop stop is a `halt(code, kind, …)` with its cause. Check every error that changes
  behaviour; comment the deliberate discards.
- Every external command goes through `internal/command` with a context and a time limit (`ReadLimit`,
  `WriteLimit`); nothing blocks without watching for cancellation.
- Agent status is the typed `AgentState`, not strings.
- Tests: whole-run scenarios use the fakes (`fakes_test.go`, `fakeherdr_test.go`, `fakegit_test.go`) and the harness
  in `helpers_test.go`: `newTimedHarness` inside `synctest.Test` when time drives the scenario (real durations,
  git in memory), `newHarness` when git is its subject (skipped by `-short`); goroutine leaks fail the dispatch tests
  (goleak). A new test that takes over a second skips itself under `testing.Short()`. Put new
  tests in a file named after the feature rather than at the end of a shared test file; add changelog entries as new
  lines (`.gitattributes` has `CHANGELOG.md merge=union`), so parallel tickets don't conflict.
- A fake bd, herdr, claude or git is a script written with `faketool.Write` (the package's `TestMain` runs its
  tests through `faketool.Main`), or for one too involved for a script a Go function the test binary runs
  (`faketool.Func`, `faketool.WriteFunc`), never a new executable: macOS scans each new one on its first run, for
  0.2s to several seconds on a loaded machine.
- Workers never push; orchestra merges. `.orchestra/settings.json` and `.orchestra/worker-prompt.md` are committed;
  `.orchestra/run/` (per-ticket files, including workers' MCP definitions) never is.
