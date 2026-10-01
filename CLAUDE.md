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
go test ./internal/dispatch/...        # one package while iterating
go build -o /tmp/orchestra ./cmd/orchestra && /tmp/orchestra -version
```

- `scripts/check.sh` is also orchestra's merge check for this repository (`.orchestra/settings.json`): a change that
  fails lint is not merged. Run it in full before closing a ticket.
- go.mod requires Go 1.26 (`go 1.26.0`) and pins `toolchain go1.27.1`, the version the project builds
  and tests with. Keep the toolchain at 1.26.5 or later, which fixes a race-detector hang in fork on
  darwin/arm64 (golang/go#79804).
- `TestLiveOrgans` calls the real `claude`; it is skipped unless enabled (its comment says how).

## Architecture Overview

orchestra is a Go CLI that works through a Beads backlog: it hands each ready ticket to a **worker** (a Claude Code
agent) in its own Herdr tab and git worktree, and fast-forwards finished tickets into the branch it runs on.
**Organs** are its one-shot advisers (triage, the run report): `claude -p` with no tools and no MCP servers. The
README defines both terms; use them consistently.

- `cmd/orchestra`: flags and settings, `orchestra init`, `orchestra plan`, the dashboard or plain output, signals,
  the organ phase after a run.
- `internal/dispatch`: the run loop, one file per concern (the README's Development section lists them). It
  reaches Beads, Herdr, git and workers' reports only through the interfaces in `deps.go`.
- Adapters: `internal/beads` (bd), `internal/herdr` (Herdr), `internal/git` (git), `internal/command` (every
  external command: context, time limit, process group), `internal/claude` (workers' hooks), `internal/mcp`
  (discovering MCP servers for workers).
- `internal/organ`: the organs. `internal/project`: `.orchestra/` (settings, worker prompt, init, per-ticket files).
  `internal/tui`: the Bubble Tea dashboard and init's screen.

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
  git in memory), `newHarness` when git is its subject; goroutine leaks fail the dispatch tests (goleak). Put new
  tests in a file named after the feature rather than at the end of a shared test file; add changelog entries as new
  lines (`.gitattributes` has `CHANGELOG.md merge=union`), so parallel tickets don't conflict.
- Workers never push; orchestra merges. `.orchestra/settings.json` and `.orchestra/worker-prompt.md` are committed;
  `.orchestra/run/` (per-ticket files, including workers' MCP definitions) never is.
