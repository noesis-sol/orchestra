# Setting a project up

What `orchestra init` does, in full. Back to the [README](../README.md).

## Set up a project

In the project's repository:

```
orchestra init --check-fast "scripts/ci-local.sh"
```

This creates `.orchestra/`, where everything `orchestra` owns in a project lives:

```
.orchestra/worker-prompt.md   committed: the worker prompt (from the built-in template)
.orchestra/settings.json      committed: the two checks and their time limits, how many tickets run at once, the MCP servers workers get (by name), an optional ticket limit, the issue types never dispatched, whether tickets are kept apart by footprint, whether conflicts go back to their workers, and when failing workers hold the run
.orchestra/.gitignore         committed: ignores the three below
.orchestra/orchestra.log      the event log
.orchestra/reports/           run reports
.orchestra/run/               per-ticket files in each worktree (the launch prompt, the worker's MCP servers, its hooks and what they report, and the whole output of a merge check that failed there, check.log); in the main checkout, the event stream, the workers the last run left behind (state.json), and a feature interview's instructions, its description (feature-request.md) and the epic it filed (feature.json)
```

and the project's two checks, the runners, in `scripts/`:

```
scripts/check-fast.sh         committed: the merge check, run on every ticket rebased onto work merged while it ran
scripts/check-full.sh         committed: every check, check-fast.sh then the slower suites, run once at the end of a run
```

The run lock is not there but in the git directory, as `.git/orchestra.lock`, where `git clean` doesn't remove it during a run (see [Run](running.md#run)).

First, `init` sets Beads up. Where `bd` isn't installed, it installs it: with Homebrew (`brew install beads`) where `brew` is on the `PATH`, as Beads recommends, otherwise, on macOS, Linux and FreeBSD, with the Beads install script (`curl -fsSL https://raw.githubusercontent.com/gastownhall/beads/main/scripts/install.sh | bash`), which checks what it downloads and falls back to `go install`. On Windows it installs nothing and says how: `irm https://raw.githubusercontent.com/gastownhall/beads/main/install.ps1 | iex` in PowerShell. In a terminal the form asks first, offering yes; `--install-beads` installs without asking and `--install-beads=false` declines. Without a terminal or either flag, `init` installs nothing and reports `bd` missing. The install may take up to 15 minutes, and Ctrl+C stops it. If `bd` lands in a folder that isn't on your `PATH` (the script uses `~/.local/bin` where it can't write to `/usr/local/bin`), `init` says where it is and how to add the folder. Then, in a repository without `.beads/`, `init` runs `bd init --non-interactive --role maintainer --init-if-missing`: no questions, the answers being "not contributing to someone else's repo" and auto-export off. Otherwise `bd init` does what it does when run by hand: it adds `.beads/`, `AGENTS.md`, `CLAUDE.md` and other agents' integration files, and points git's hooks at `.beads/hooks`. bd 1.3.0 also commits what it adds, along with anything already staged, so `init` runs it before staging anything of its own, then lists what `bd` committed and what is left to commit with `.orchestra/`. A failed install or `bd init` shows the command's error, and the **Next** box keeps the fix.

In a terminal, `init` asks with a short form in two steps, one screen each, under a header such as **Step 1 of 2 · Workers**. **Workers** asks whether to install Beads (where `bd` is missing, as above), how many **tickets to run at the same time** by default, and which **MCP servers workers get** (see [MCP servers for workers](workers.md#mcp-servers-for-workers)). Where the project keeps a `CHANGELOG.md` that `.gitattributes` doesn't merge by union yet, it also offers to add `CHANGELOG.md merge=union` there, so tickets that each add an entry at the same spot don't conflict (see [Several tickets at once](running.md#several-tickets-at-once)). **Checks** chooses the project's checks, as [Choosing the checks](#choosing-the-checks) says, and asks their **time limits** (30m offered for check-fast, 60m for check-full). Enter on a step's last question goes on to the next step, and Shift+Tab on its first goes back to the previous one, with its answers kept. Nothing is installed or written until the last step is submitted: Esc or Ctrl+C at any step ends `init` with the project as it was. A step whose every question a flag answers is skipped, and the one step left has its name alone as its header; with every question answered, there is no form. With `TERM=dumb`, the form asks its questions one after another as plain lines, each step's under its header. `--check-fast "<command>"` (or `--check`) and `--check-full "<command>"` (either answers the choice of checks, and there is then no scout), `--check-fast-timeout 5m` (or `--check-timeout`), `--check-full-timeout 90m`, `--concurrent N` (or `-c N`), `--mcp a,b` (or `--mcp ""` for none) and `--changelog-union` (or `--changelog-union=false`) answer those without asking, which is what a script, or an agent setting the project up for you, should use. Without a terminal it asks nothing, uses 1 at a time, leaves the MCP servers as they were (unset on a first run), and leaves `.gitattributes` alone. `init` then shows each step, whether `bd`, Beads, `herdr` and `claude` are there, and a **Next** box with only what's left, ending with the command to start a run. `init` also checks for `bd`, `.beads`, `herdr` and `claude`, and says what's missing. It never replaces an existing prompt unless you pass `--force`, and keeps existing settings unless its flags change them. In a project set up by an earlier version, it moves `.claude/worker-prompt.md` into `.orchestra/` (staged with `git mv`); the old `.claude/orchestrate.log` and reports stay where they are, as history. Commit `.orchestra/` and the runners (and `.gitattributes`, if it changed, and whatever `bd init` left uncommitted; the **Next** box names them) afterwards: a ticket's worktree has only what is committed, and its merge check runs there.

## The checks

A project has two checks, each a shell script `init` writes in `scripts/` and the project then owns:

- **`scripts/check-fast.sh`**, the merge check: orchestra runs it on every finished ticket that had to be rebased onto work merged while it ran, and workers run it before they close a ticket. `settings.json` names it as `check_fast`, with `check_fast_timeout` (default 30m; `--check-timeout` on a run overrides it).
- **`scripts/check-full.sh`**, everything: it runs `scripts/check-fast.sh` first, then the slower suites. orchestra runs it once at the end of a run that merged a ticket ([The full check](running.md#the-full-check)). `settings.json` names it as `check_full`, with `check_full_timeout` (default 60m; `--check-full-timeout` on a run overrides it).

A runner lists the suites' commands, one line each, run from the repository's root, under a comment naming where the suite was found, after a line printing `SUITE: <its name>` (or its command), which names the suite a failing full check failed in. Each framework finds its own test files, so a new test in an existing suite needs no edit there; a new suite is a new line. A suite that can't run in two worktrees at once (a fixed port, a shared database) runs between `lock <name>` and `unlock`: checks running at once in several worktrees take turns on it. The lock is a directory in the temp directory, named for the repository and the suite and holding its holder's PID, taken over once that process is gone (macOS has no `flock`). A runner with no suites is just `exit 0`, with a comment saying it checks nothing yet; `check-full.sh` with no suites of its own runs `check-fast.sh`. An existing `scripts/check.sh` is never edited: `check-fast.sh` calls it as a suite, the line `scripts/check.sh`, and `init` without a check command finds it there.

`--check-fast "<command>"` writes `check-fast.sh` with that command (`""`: the runner that checks nothing), and `--check-full "<command>"` writes `check-full.sh` with it after `check-fast.sh`. Run again, `init` leaves a runner that `settings.json` already names as it is. Where it would write a runner that differs from the one there, it says so and keeps the one there, unless its flag was given or stage 2's question replaces it. A project set up before the runners keeps working: its `check` and `check_timeout` are read as `check_fast` and `check_fast_timeout`, and `init` writes its check command into `check-fast.sh` and the settings under the new names.

A ticket's worktree has the dependencies its worker installed for the code it started from, and a ticket rebased over another that bumped a dependency would be checked against the old ones. `"setup"` in `settings.json` is the command that installs them, such as `"npm ci"`, `"pnpm install --frozen-lockfile"`, `"uv sync"` or `"bundle install"`: orchestra runs it in a rebased ticket's worktree before `check_fast` whenever the rebase changed a dependency manifest or lockfile ([Several tickets at once](running.md#several-tickets-at-once)). `"setup_files"` lists those files, as globs matched against a file's name, or against its path from the repository's top when the glob has a slash (`"web/package-lock.json"`, `"apps/*/yarn.lock"`). Without it, they are the usual manifests and lockfiles: `package.json`, `package-lock.json`, `npm-shrinkwrap.json`, `yarn.lock`, `pnpm-lock.yaml`, `bun.lock`, `bun.lockb`, `go.mod`, `go.sum`, `Cargo.toml`, `Cargo.lock`, `pyproject.toml`, `poetry.lock`, `uv.lock`, `Pipfile`, `Pipfile.lock`, `requirements*.txt`, `Gemfile`, `Gemfile.lock`, `composer.json`, `composer.lock`, `mix.exs`, `mix.lock`, `pubspec.yaml` and `pubspec.lock`. `init` doesn't ask for it: add it to `settings.json` by hand, and commit it.

```json
{
  "check_fast": "scripts/check-fast.sh",
  "setup": "npm ci",
  "setup_files": ["package-lock.json"]
}
```

## Choosing the checks

Stage 2 of the form starts with the scout, the one organ that reads the repository itself (with Read, Glob and Grep only, confined to it): it looks where projects say how they test (package manifests and their scripts, Makefiles and task runners, CI workflows, test configs and the project's own scripts) and lists each suite with its command, where it was found, whether it is fast (unit tests, lint, type checks, a smoke e2e suite) or full, whether it can run in two worktrees at once, and what it needs (a service, a browser, credentials). An existing `scripts/check.sh` comes first, as a fast suite. It starts only when stage 2 is reached, under a spinner with the time it has taken; it may take up to 5 minutes, and Esc skips it. The scout uses `--organ-model`'s and `--organ-effort`'s variables, `ORGAN_MODEL` and `ORGAN_EFFORT` (else the settings' `organ_effort`; medium by default).

Then one of three choices, the one that fits what the scout found selected:

1. **Use them as they are**, selected when the scout found suites. Two checklists follow, each suite with its command, where it was found and what it needs: **On every merge (check-fast)**, every suite, those the scout calls fast ticked; and **At the end of a run (check-full)**, the other suites, all ticked. A suite ticked in both runs in `check-fast.sh` only, since `check-full.sh` runs that first. A suite that can't run in two worktrees at once takes turns (see [The checks](#the-checks)). Last, unticked, **Also file tickets for untested areas**.
2. **Create from scratch with the create-check-suite skill**, selected when the scout found none. The runners check nothing for now (`exit 0`); the skill sets the tests up in a ticket of their own (see [The test work init files](#the-test-work-init-files)).
3. **Manual**, selected when the scout failed (`claude` missing, an error, its time limit) or was skipped, with the reason shown: one line, the command `check-fast.sh` runs, pre-filled from the settings or an existing prompt. Empty writes a runner that checks nothing, or keeps a runner that is there as it is.

Where `init` runs again and a runner there differs from what the choice would write, stage 2 shows the commands that runner runs and asks whether to keep it (the default) or replace it; the same goes for a copy of the create-check-suite skill that differs from orchestra's, where the choice needs the skill. Without a terminal there is no scout and no stage 2: the flags decide.

### The test work init files

After writing its files, `init` files with `bd create` the work the choice calls for, each ticket labelled `orchestra-tests`, and its summary lists each ticket it filed:

- **Create from scratch**: an epic, *The project's tests*, and under it a P1 ticket labelled `solo`, *Set up the test harness and a first suite*, which asks a worker to use the create-check-suite skill for a harness and smoke tests added to the runners, both runners passing on the branch `init` ran on, `FEATURES.md`, and one P3 ticket per untested area under the epic. P1 and solo, it runs first and alone, so nothing merges while the runners still check nothing.
- **Use them as they are**: a P1 ticket labelled `solo`, *Make scripts/check-fast.sh and scripts/check-full.sh pass on main* (the branch `init` ran on): run them, fix what fails or skip it naming a bug ticket filed for it, and record how long each takes. With **Also file tickets for untested areas**, it goes under the epic, beside *Map the untested areas* (P2), which waits for it: the skill's `FEATURES.md` and one P3 ticket per untested area.
- **Manual**: nothing.

Run again, `init` files only what isn't open already: a ticket labelled `orchestra-tests` with the same title that isn't closed is kept (the summary says so) and serves as the parent or the ticket waited for. One that is closed is filed again. Where bd fails (Beads isn't set up, bd is missing), the summary says which tickets weren't filed and why; the files `init` wrote stand, and running it again files them.

## The create-check-suite skill

Where the project's tests are to be created from scratch, or its untested areas to be ticketed (stage 2's "Create from scratch", or "Use them as they are" with "Also file tickets for untested areas"), the tickets `init` files ask the workers to use the create-check-suite skill: it sets up a harness and deterministic suites in the project's own frameworks, wires them into the runners, maps the features in `FEATURES.md` and files a ticket for each untested area. orchestra carries the skill in its binary, and `init` writes it into the project's skill folder for the workers' agent, to be committed with the rest of its files, since a ticket's worktree has only what is committed:

```
.claude/skills/create-check-suite/   committed: for Claude Code workers (the default)
.agents/skills/create-check-suite/   committed: for Codex workers
```

`--agent codex` names the workers' agent, as on a run; without it `init` reads `AGENT_KIND`, else takes `claude`. For an agent whose skill folder orchestra doesn't know, it says so and installs nothing. Run again, it leaves a copy that is the same as its own. A copy that differs is kept, and `init` says so, unless replacing it is chosen; replacing it writes the skill's files over those there and leaves any file of the project's own in the folder. Without a terminal there is no stage 2, so no test work is filed and the skill isn't installed.
