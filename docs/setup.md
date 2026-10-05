# Setting a project up

What `orchestra init` does, in full. Back to the [README](../README.md).

## Set up a project

In the project's repository:

```
orchestra init --check "scripts/ci-local.sh"
```

This creates `.orchestra/`, where everything `orchestra` owns in a project lives:

```
.orchestra/worker-prompt.md   committed: the worker prompt (from the built-in template)
.orchestra/settings.json      committed: the check command and its time limit, how many tickets run at once, the MCP servers workers get (by name), an optional ticket limit, the issue types never dispatched, whether tickets are kept apart by footprint, whether conflicts go back to their workers, and when failing workers hold the run
.orchestra/.gitignore         committed: ignores the three below
.orchestra/orchestra.log      the event log
.orchestra/reports/           run reports
.orchestra/run/               per-ticket files in each worktree (the launch prompt, the worker's MCP servers, its hooks and what they report, and the whole output of a merge check that failed there, check.log); in the main checkout, the event stream, the workers the last run left behind (state.json), and a feature interview's instructions, its description (feature-request.md) and the epic it filed (feature.json)
```

The run lock is not there but in the git directory, as `.git/orchestra.lock`, where `git clean` doesn't remove it during a run (see [Run](running.md#run)).

First, `init` sets Beads up. Where `bd` isn't installed, it installs it: with Homebrew (`brew install beads`) where `brew` is on the `PATH`, as Beads recommends, otherwise, on macOS, Linux and FreeBSD, with the Beads install script (`curl -fsSL https://raw.githubusercontent.com/gastownhall/beads/main/scripts/install.sh | bash`), which checks what it downloads and falls back to `go install`. On Windows it installs nothing and says how: `irm https://raw.githubusercontent.com/gastownhall/beads/main/install.ps1 | iex` in PowerShell. In a terminal the form asks first, offering yes; `--install-beads` installs without asking and `--install-beads=false` declines. Without a terminal or either flag, `init` installs nothing and reports `bd` missing. The install may take up to 15 minutes, and Ctrl+C stops it. If `bd` lands in a folder that isn't on your `PATH` (the script uses `~/.local/bin` where it can't write to `/usr/local/bin`), `init` says where it is and how to add the folder. Then, in a repository without `.beads/`, `init` runs `bd init --non-interactive --role maintainer --init-if-missing`: no questions, the answers being "not contributing to someone else's repo" and auto-export off. Otherwise `bd init` does what it does when run by hand: it adds `.beads/`, `AGENTS.md`, `CLAUDE.md` and other agents' integration files, and points git's hooks at `.beads/hooks`. bd 1.3.0 also commits what it adds, along with anything already staged, so `init` runs it before staging anything of its own, then lists what `bd` committed and what is left to commit with `.orchestra/`. A failed install or `bd init` shows the command's error, and the **Next** box keeps the fix.

In a terminal, `init` asks with a short form in two steps, one screen each, under a header such as **Step 1 of 2 · Workers**. **Workers** asks whether to install Beads (where `bd` is missing, as above), how many **tickets to run at the same time** by default, and which **MCP servers workers get** (see [MCP servers for workers](workers.md#mcp-servers-for-workers)). Where the project keeps a `CHANGELOG.md` that `.gitattributes` doesn't merge by union yet, it also offers to add `CHANGELOG.md merge=union` there, so tickets that each add an entry at the same spot don't conflict (see [Several tickets at once](running.md#several-tickets-at-once)). **Checks** asks for the **check command** (lint, build and tests), pre-filled from the settings or from an existing prompt, and its **time limit** (30m offered). Enter on a step's last question goes on to the next step, and Shift+Tab on its first goes back to the previous one, with its answers kept. Nothing is installed or written until the last step is submitted: Esc or Ctrl+C at any step ends `init` with the project as it was. A step whose every question a flag answers is skipped, and the one step left has its name alone as its header; with every question answered, there is no form. With `TERM=dumb`, the form asks its questions one after another as plain lines, each step's under its header. `--check`, `--check-timeout 5m`, `--concurrent N` (or `-c N`), `--mcp a,b` (or `--mcp ""` for none) and `--changelog-union` (or `--changelog-union=false`) answer those without asking, which is what a script, or an agent setting the project up for you, should use. Without a terminal it asks nothing, uses 1 at a time, leaves the MCP servers as they were (unset on a first run), and leaves `.gitattributes` alone. The check command goes into the prompt template and `settings.json`. `init` then shows each step, whether `bd`, Beads, `herdr` and `claude` are there, and a **Next** box with only what's left, ending with the command to start a run. `init` also checks for `bd`, `.beads`, `herdr` and `claude`, and says what's missing. It never replaces an existing prompt unless you pass `--force`, and keeps existing settings unless its flags change them. In a project set up by an earlier version, it moves `.claude/worker-prompt.md` into `.orchestra/` (staged with `git mv`); the old `.claude/orchestrate.log` and reports stay where they are, as history. Commit `.orchestra/` (and `.gitattributes`, if it changed, and whatever `bd init` left uncommitted; the **Next** box names them) afterwards.

## The create-check-suite skill

Where the project's tests are to be created from scratch, or its untested areas to be ticketed (stage 2's "Create from scratch", or "Use them as they are" with "Also file tickets for untested areas"), the tickets `init` files ask the workers to use the create-check-suite skill: it sets up a harness and deterministic suites in the project's own frameworks, wires them into the runners, maps the features in `FEATURES.md` and files a ticket for each untested area. orchestra carries the skill in its binary, and `init` writes it into the project's skill folder for the workers' agent, to be committed with the rest of its files, since a ticket's worktree has only what is committed:

```
.claude/skills/create-check-suite/   committed: for Claude Code workers (the default)
.agents/skills/create-check-suite/   committed: for Codex workers
```

`--agent codex` names the workers' agent, as on a run; without it `init` reads `AGENT_KIND`, else takes `claude`. For an agent whose skill folder orchestra doesn't know, it says so and installs nothing. Run again, it leaves a copy that is the same as its own. A copy that differs is kept, and `init` says so, unless replacing it is chosen; replacing it writes the skill's files over those there and leaves any file of the project's own in the folder. Without a terminal there is no stage 2, so no test work is filed and the skill isn't installed.
