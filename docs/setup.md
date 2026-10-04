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
.orchestra/run/               per-ticket files in each worktree (the launch prompt, the worker's MCP servers, its hooks and what they report); in the main checkout, the run lock, the event stream, the workers the last run left behind (state.json), and a feature interview's instructions, its description (feature-request.md) and the epic it filed (feature.json)
```

First, `init` sets Beads up. Where `bd` isn't installed, it installs it: with Homebrew (`brew install beads`) where `brew` is on the `PATH`, as Beads recommends, otherwise, on macOS, Linux and FreeBSD, with the Beads install script (`curl -fsSL https://raw.githubusercontent.com/gastownhall/beads/main/scripts/install.sh | bash`), which checks what it downloads and falls back to `go install`. On Windows it installs nothing and says how: `irm https://raw.githubusercontent.com/gastownhall/beads/main/install.ps1 | iex` in PowerShell. In a terminal the form asks first, offering yes; `--install-beads` installs without asking and `--install-beads=false` declines. Without a terminal or either flag, `init` installs nothing and reports `bd` missing. The install may take up to 15 minutes, and Ctrl+C stops it. If `bd` lands in a folder that isn't on your `PATH` (the script uses `~/.local/bin` where it can't write to `/usr/local/bin`), `init` says where it is and how to add the folder. Then, in a repository without `.beads/`, `init` runs `bd init --non-interactive --role maintainer --init-if-missing`: no questions, the answers being "not contributing to someone else's repo" and auto-export off. Otherwise `bd init` does what it does when run by hand: it adds `.beads/`, `AGENTS.md`, `CLAUDE.md` and other agents' integration files, and points git's hooks at `.beads/hooks`. bd 1.3.0 also commits what it adds, along with anything already staged, so `init` runs it before staging anything of its own, then lists what `bd` committed and what is left to commit with `.orchestra/`. A failed install or `bd init` shows the command's error, and the **Next** box keeps the fix.

In a terminal, `init` asks with a short form: the **check command** (lint, build and tests), pre-filled from the settings or from an existing prompt, its **time limit** (30m offered), how many **tickets to run at the same time** by default, and which **MCP servers workers get** (see [MCP servers for workers](workers.md#mcp-servers-for-workers)). Where the project keeps a `CHANGELOG.md` that `.gitattributes` doesn't merge by union yet, it also offers to add `CHANGELOG.md merge=union` there, so tickets that each add an entry at the same spot don't conflict (see [Several tickets at once](running.md#several-tickets-at-once)). `--check`, `--check-timeout 5m`, `--concurrent N` (or `-c N`), `--mcp a,b` (or `--mcp ""` for none) and `--changelog-union` (or `--changelog-union=false`) answer those without asking, which is what a script, or an agent setting the project up for you, should use. Without a terminal it asks nothing, uses 1 at a time, leaves the MCP servers as they were (unset on a first run), and leaves `.gitattributes` alone. The check command goes into the prompt template and `settings.json`. `init` then shows each step, whether `bd`, Beads, `herdr` and `claude` are there, and a **Next** box with only what's left, ending with the command to start a run. `init` also checks for `bd`, `.beads`, `herdr` and `claude`, and says what's missing. It never replaces an existing prompt unless you pass `--force`, and keeps existing settings unless its flags change them. In a project set up by an earlier version, it moves `.claude/worker-prompt.md` into `.orchestra/` (staged with `git mv`); the old `.claude/orchestrate.log` and reports stay where they are, as history. Commit `.orchestra/` (and `.gitattributes`, if it changed, and whatever `bd init` left uncommitted; the **Next** box names them) afterwards.
