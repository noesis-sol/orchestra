# Development

Working on orchestra itself. Back to the [README](../README.md).

## Development

```
scripts/check.sh
scripts/check-fast.sh
```

`scripts/check.sh` is the full check: `go vet ./...`, ShellCheck on the shell scripts (below), the race tests, shuffled (`go test -race -shuffle=<seed> ./...`, through [gotestsum](https://github.com/gotestyourself/gotestsum)) and golangci-lint. `scripts/check-fast.sh` is the merge check, `scripts/check.sh -short`: the same, but the race tests run with `-short`, without those that take over a second (below). orchestra runs them as this repository's checks (`.orchestra/settings.json`): the merge check on every ticket rebased onto work merged while it ran, so a ticket that fails lint isn't merged, and the full check, `check_full`, once at the end of a run that merged one, filing a ticket when it fails ([The full check](running.md#the-full-check)). Workers run the full check before closing a ticket. golangci-lint runs the linters the [Uber Go style guide](https://github.com/uber-go/guide/blob/master/style.md#linting) asks for, configured in `.golangci.yml`: errcheck (terminal writes excepted), goimports, revive, govet and staticcheck, plus predeclared, lll (lines up to 120 columns), modernize (the newer standard-library forms, such as `slices.Contains`, `strings.Cut` and `WaitGroup.Go`) and errorlint (`errors.Is` and `errors.As` rather than `==` or a type assertion on an error, which may be wrapped; its `fmt.Errorf` check is off, since some calls format a second error with `%v` on purpose). The script runs gotestsum and golangci-lint with `go run` at pinned versions (v1.13.0 and v2.14.0), so a machine needs only Go; the first run downloads them. Its last line is its verdict, with its exit status unchanged: `scripts/check.sh: PASS`, or the step it failed in, such as `scripts/check.sh -short: FAIL in tests (exit 1)` (vet, shellcheck, tests or lint), so the output's end alone says whether it passed. Run it once with its whole output saved, `scripts/check.sh > .orchestra/run/worker-check.log 2>&1; echo "exit $?"`, and read the file, rather than again to see what it said.

The check also runs [ShellCheck](https://www.shellcheck.net) on every shell script git tracks (`git ls-files '*.sh'`), through `scripts/shellcheck.sh`, as `sh` (`-s sh`) where the shebang is `/bin/sh`, so that a bashism in `scripts/install.sh`, which users pipe into `sh`, is an error. Fix a finding, or disable it on the line before with `# shellcheck disable=SC<code>` and a comment saying why. `scripts/shellcheck.sh <args>` runs ShellCheck with those arguments instead, such as a script to try. ShellCheck is Haskell, so go run can't pin it: the script downloads the release's binary for darwin or linux, amd64 or arm64, once per user into `${XDG_CACHE_HOME:-$HOME/.cache}/orchestra-dev/shellcheck-<version>/`, which every worktree shares, after checking the archive's SHA-256 against the sum pinned in the script; a wrong sum fails the check. Offline with nothing cached, it prints `shellcheck skipped: <why>` and the check goes on, except under `CI=true` (GitHub Actions sets it), where it fails, so CI always lints. `TestRunnersPassShellcheck` (`internal/project`) runs it on the runners `orchestra init` writes.

To move to a new ShellCheck, change `version` in `scripts/shellcheck.sh` and its four sums together. ShellCheck publishes no checksum file: compute them from the release's archives, `shellcheck-<version>.<darwin|linux>.<aarch64|x86_64>.tar.gz`, and compare them with the digests GitHub shows for its assets:

```
for p in darwin.aarch64 darwin.x86_64 linux.aarch64 linux.x86_64; do
  curl -fsSL "https://github.com/koalaman/shellcheck/releases/download/vX.Y.Z/shellcheck-vX.Y.Z.$p.tar.gz" |
    shasum -a 256
done
gh release view vX.Y.Z -R koalaman/shellcheck --json assets -q '.assets[] | .name + " " + .digest'
```

Then `scripts/check.sh` downloads the new version into a folder of its own; the old one's folder can be removed.

GitHub Actions runs the check on Linux and macOS for each push to main and each pull request (`.github/workflows/check.yml`, Actions, Check). The tests need nothing of the machine's but Go, git and `/bin/sh`, so they pass on a runner as on a workstation: a test that runs bd, herdr or claude puts a fake first on PATH (`faketool`), a configuration test finds a stand-in bd and herdr at the end of PATH (`hostTools`), its repositories start on `main` whatever git's default, and `gittest.Main` gives git an identity for the commits orchestra's rebases make. The one exception is tmux, for the real-tmux tests (below): both legs install it (apt on Linux, Homebrew on macOS, unless the runner has it already), print `tmux -V`, and run the check with `ORCHESTRA_REQUIRE_TMUX=1`, so those tests fail there rather than skip; the release workflow's Check (`.github/workflows/release.yml`) does the same, so a tag can't be released with them skipped. Their tmux servers are detached, so the runners' lack of a terminal, and of `TERM`, is no matter. The macOS leg also installs cmux (`brew install --cask cmux`, the current one), prints `cmux version`, and runs the check with `ORCHESTRA_REQUIRE_CMUX=1`, for the real-cmux tests (The cmux tests, below); cmux is a macOS app, so the Linux leg and the release workflow, which runs on Linux, skip them, and the Check workflow's macOS run on main is their gate. Herdr isn't installed in CI: its path is covered by the fakes.

While iterating, `go test -short` runs a package without its slow tests, those that take over a second: the scenarios with real git in `internal/dispatch` (`gitRepo` skips them), the runs on a pseudo-terminal in `cmd/orchestra` (`openTerminal`), and the others, each skipped at its start. `go test -short ./internal/dispatch/...` takes a few seconds, against about 25 in full. The merge check, `scripts/check-fast.sh`, passes `-short` too, so a test that takes over a second under `-race` must skip itself under it, or it holds the merge queue on every merge; `go test -race -short -json ./...` gives each test's time (its `Elapsed`). `scripts/check.sh` without `-short` runs every test, before a ticket closes and at the end of every run that merged one. A new test that takes over a second starts the same way:

```go
if testing.Short() {
	t.Skip("skipped by -short: <why it is slow>")
}
```

On macOS, a program a test has just written (a fake `claude`, `bd` or `herdr`) takes about 0.2 seconds to start the first time, against 0.01 seconds after, so a test that writes one for each of its cases is slow.

The check runs the tests shuffled, to find a test that passes or fails only after another one: go test runs each package's tests in an order drawn from a seed, which the check picks anew each time and uses for every package. Each package's output starts with `-test.shuffle <seed>`, a failed check's output ends with `The tests ran in the order of -shuffle=<seed>`, and each `FLAKY:` line ends with `(-shuffle=<seed>)`. `-shuffle=<seed>` runs a package's tests in that order again, and `-run` keeps the order among the tests it picks, so a failure can be narrowed down to the test that leaves something behind:

```
go test -race -shuffle=2513478067 ./internal/dispatch
go test -race -shuffle=2513478067 -run '^(TestA|TestB)$' ./internal/dispatch
```

The fuzz targets' seeds run after the tests, unshuffled. go test caches no shuffled run, so the check runs every test each time, even in a package that hasn't changed since the last check. While iterating, `go test -shuffle=on` (with `-short` too) runs the tests in a new order, printing each package's seed.

A test that fails is run once more, on its own, and the check passes if it passes then, printing a line `FLAKY: <package> <test> (-shuffle=<seed>)` for it, such as `FLAKY: ./internal/tui TestInitFormKeepsOrTypesACustomConcurrency (-shuffle=2513478067)`, which orchestra warns of when it merges. A test that fails its rerun fails the check, and so, without a rerun, do more than three failed tests, a data race, a panic and a package that fails outside its tests (goleak's check in `TestMain`). A `FLAKY:` line is a bug to file and fix, not noise: the test passes alone but fails after another test or under load, such as several workers' checks at once, and the rerun only kept it from failing a merge. First run its package's tests in the line's order, as above: if the test fails then, the order is the cause. If it passes, reproduce it under load: run many copies of it at once, as orchestra-4wb.26 did with 12 copies of 500 runs each:

```
go test -c -race -o /tmp/tui.test ./internal/tui
for i in $(seq 12); do /tmp/tui.test -test.count 500 \
  -test.run '^TestInitFormKeepsOrTypesACustomConcurrency$' & done; wait
```

A failure in a few thousand runs is the flake; a fix holds when the same load brings none (`-test.run '^TestParent$/^sub$'` picks a subtest).

A failed check keeps go test's events (`go test -json`, every test's output) in a temporary file it names in its last lines: `go test's events (go test -json) are kept in <file>`. They are for the race detector's reports above all: a data race stops the check without a rerun and may never recur, and gotestsum prints the report amid the failed tests' output, which a long check's reader may not keep. Above that line the check names each test that raced, `DATA RACE: <package> <test>`, or `DATA RACE: <package> (outside its tests)` for a race in `TestMain` or after its test ended. gotestsum shows the failed tests' output again, the reports with it, from the file:

```
go run gotest.tools/gotestsum@v1.13.0 --format=standard-quiet --raw-command -- cat <file>
```

The functions that read text a model, a worker or a ticket's author wrote have fuzz tests, each in a `_fuzz_test.go` file: `FuzzTicketFootprint`, `FuzzGlobsMeet` and `FuzzIDProblem` (`internal/dispatch`), `FuzzAgentName` and `FuzzReadAgent` (`internal/herdr`) and `FuzzParseLinked` (`internal/beads`). `go test` and the check run only their seeds, as ordinary tests. After changing one of those functions, fuzz it for a minute or so; Go fuzzes one target of one package at a time:

```
go test -run '^$' -fuzz '^FuzzTicketFootprint$' -fuzztime 60s ./internal/dispatch
```

The fuzzer saves an input that fails in the package's `testdata/fuzz/<target>/`, where `go test` runs it with the seeds from then on: commit it with the fix, or add it to the target's seeds (`f.Add`). The inputs it found new paths with stay in Go's cache, so the next run goes on from them; `go clean -fuzzcache` empties it.

`TestDocLinksResolve` (`cmd/orchestra`) follows every relative link in the README and `docs/*.md`: the file it names must exist, and its `#anchor`, in a Markdown file, must be one of that file's headings as GitHub makes them (lowercase, punctuation dropped, each space a hyphen, `-1`, `-2` and so on for repeats). Links in code spans and fenced blocks, and to other sites, are left alone. A heading renamed takes the links to it along in the same commit.

`TestLiveOrgans` calls the real `claude` against a real repository without writing anything. Its comment shows how to run it.

`TestLiveSpec` (`cmd/orchestra`) breaks a tiny spec down with the real `claude`, in a new repository with Beads, and checks the tree it filed; it takes minutes and costs money, so it runs only with `SPEC_LIVE=1`: `SPEC_LIVE=1 go test -run TestLiveSpec -v -timeout 30m ./cmd/orchestra/`.

The fake bd, herdr, claude and git the tests run are shell scripts written with `internal/faketool`, whose `Write` makes each a hard link to one dispatcher per test binary, run once, beside the script it runs. A new executable would cost far more: macOS scans each one the first time it runs, which takes 0.2-0.6 seconds, and several seconds while other workers run their checks. A package whose tests call `Write` runs them through `faketool.Main` from its `TestMain`.

A fake too involved for a script, such as a claude that reads its `--settings` and runs their hooks, is a Go function the test binary runs: `faketool.Func` registers it, from a package variable, and `faketool.WriteFunc` puts it on PATH, a script that runs the test binary again, which macOS has scanned already. `claudetest.Claude` (`internal/claudetest`) is such a fake Claude Code, for a tmux pane or a cmux terminal: it goes through a script of steps (the trust dialog, start, a prompt pasted or typed, with a file it attaches, tool uses, a permission prompt, the turn's end, exiting), running the hooks orchestra gave it with the input Claude Code gives them. Those hooks run the binary that wrote them (`os.Executable`), here the test binary, as `hook <event> <folder> <process>` with `ORCHESTRA_HOOK=1`: `internal/claude` serves the hook from its `init` in any binary that has it, so a test binary is the hook as orchestra's own is, without running its tests again.

The tests that run the real tmux, the `TestLive…` tests of `internal/tmux` (every method of the adapter) and the whole runs in `internal/dispatch/tmux_run_test.go` (a ticket merged, a permission prompt, a worker gone and resumed), each start a tmux server of their own with `tmuxtest.Server`: on a socket in a folder of the test's own (`$TMUX_TMPDIR`), never the user's server, and killed as the test ends. `-short` skips them. Where tmux isn't installed they skip, unless `ORCHESTRA_REQUIRE_TMUX=1`, when they fail instead: a local check without tmux passes without them, and CI, which sets the variable, runs them (GitHub Actions, above).

A package whose tests run the real git runs them through `gittest.Main` (`internal/gittest`) from its `TestMain`, as `internal/git`, `internal/dispatch`, `internal/project` and `cmd/orchestra` do. It turns off git's automatic maintenance (`maintenance.auto=false`, through `GIT_CONFIG_COUNT`) for every git the tests start, orchestra's included. After a commit, a merge or a rebase, git starts that maintenance detached, and since git 2.54 it repacks a repository with a few loose objects: one still writing in a test's repository as the test ends fails the test with `TempDir RemoveAll cleanup: unlinkat .../.git/objects: directory not empty`.

`cmd/orchestra`'s tests run in parallel (`t.Parallel`), so none sets the process's environment or working directory (`t.Setenv`, `t.Chdir`), which they all share. A test sets its own instead (`cmd/orchestra/test_env_test.go`): `setenv` a variable, `onPath` a folder of fakes first on its PATH, `chdir` its working directory, `breakDownWith` its spec runs' breakdown, and `buildOf` the build orchestra is (`cmdEnv`'s `version` and `releases`), each for the test and its subtests. It runs orchestra in them: `run(testCtx(ctx, t), args, testCmdEnv(t, stdin, stdout, stderr))`, `getenv(t)` for a function that takes the environment, and `testCtx(ctx, t)` for any context that reaches a command. The context carries the environment to the commands orchestra runs (`command.WithEnv`), which look their name up in its PATH (`command.LookPath`), and a context made from `context.Background` to outlive another keeps it (`context.WithoutCancel`, `command.WithEnvOf`). A test whose orchestra is sent a signal by what it runs, as a fake claude's `kill -INT $PPID` sends one, runs orchestra in a process of its own, the test binary running `runMain` (`runApartOnTerminal`), so that the signal reaches it alone. A test stays serial, with a comment saying why, where it can't help touching the process: it signals it (the stop signals) or counts what every test has running (goleak, `runsLeft`).

The run loop, `internal/dispatch`, has one file per concern, its tests in the `_test.go` file of the same name:

| File | What's in it |
| --- | --- |
| `loop.go` | the `Loop` type, its state grouped by owner (`deps`, `tallies`, `ledger`, `carryover`, `triageQueue`, `envHold`: each says which lock guards it, or whose it is), `Config`, exit codes, timings and shared helpers |
| `recover.go` | a worker's panic: logging it and stopping the run, and `held`, a mutex a deferred call releases if a panic leaves it taken |
| `run.go` | `Run`: picking the next ticket, solo tickets, HOLD, interrupts; what only `Run` reads and writes is its `runState`'s (tests in `run_test.go` and `schedule_test.go`) |
| `ids.go` | which ticket IDs can be run (plain names, valid in a branch), setting aside the others |
| `start.go` | a ticket's worktree, starting, adopting and naming its worker, delivering its prompt |
| `rules.go` | a worker's standing rules (the worker prompt) and its first message, with its rules in its system prompt or without |
| `mcp.go` | the MCP servers a Claude worker starts with, and orchestra's own sessions (`SessionMCP`), and the warning the run gives once about them |
| `permission_mode.go` | the permission mode Claude workers start in (`--permission-mode`), as the `START` line says it, and whether their `claude --help` lists it |
| `unused_tools.go` | the tools Claude workers start without (`--disallowedTools`), Skill unless the project has skills of its own, when their `claude --help` lists it |
| `screen.go` | reading a worker's screen: its prompt still unsent in the input box, its latest action |
| `work.go` | one ticket from start to outcome (`ticketRun`): its earlier worker, its worktree, its worker's files, starting or adopting the worker, and the outcome: asked, deferred, paused or closed |
| `asked.go` | tickets waiting on a question that don't come back through `bd ready`: where each left its worker, adopting a worker that claimed or closed its ticket in its tab, one it deferred or left, labelling what is left as the run ends |
| `carry.go` | the workers a run leaves behind (asked, left running), saved to `.orchestra/run/state.json` as it ends and checked and carried over by the next run |
| `settle.go` | waiting for a worker to settle (the terminal's status, its Stop hook or the start-up grace), its hooks read once a poll for the settle decisions, which are pure functions of them, telling one that stopped with its ticket in progress to continue, reading its status, the dashboard watcher |
| `resume.go` | resuming a Claude worker gone from its tab with its ticket in progress, or back from its answered question: its session, recorded by its hooks, in a new tab, once per ticket in a run |
| `merge.go` | merging a closed ticket: rebase, check, fast-forward, cleanup (the worker's tab only while the terminal still has it labelled with the ticket's ID) |
| `check.go` | running the check command (`check`) on a rebased branch within its time limit, saving its whole output when it fails, what it said for the reviewer, its `FLAKY:` lines; the runner the check, the setup and the full check share (`runLimited`: the time limit, how a command failed, the lines that say what failed, saving the whole output) |
| `mergequeue.go` | the merge queue: finished tickets merge one at a time, in the order they finish, each shown on the dashboard as to merge (with its place in line) or merging |
| `recheck.go` | checking a ticket set aside for a failed check once more after Base moves on, unless it failed in its own code |
| `checkback.go` | handing a check that fails on a rebased branch back to the ticket's worker to fix, and checking what it committed |
| `handback.go` | handing a finished ticket's rebase conflicts back to its worker to resolve, then checking or undoing its resolution; the hand-back itself (`handBackTo`) and putting the branch back after one is rejected (`restoreBranch`), shared with `checkback.go` |
| `checkoutput.go` | what is kept of the output of the check, the setup and the full check as they print it: the whole in a temporary file, saved when they fail, and in memory its end, what failed, its `FLAKY:` lines and suite, each bounded |
| `setup.go` | the setup command (`setup`): run in a ticket's new worktree before its worker starts, and a setup that fails there (`SETUP_FAILED`); run before the check on a rebased branch when a dependency file changed since it last ran; taken from `.orchestra/settings.json` as a merge changes it |
| `holds.go` | tickets held for a blocker or a source ticket not yet merged, or for their subtickets; `pendingWhy`, the one rule for whether a ticket has merged |
| `unmerged.go` | tickets closed but not merged: the `unmerged` label, those earlier runs left, whether one has merged since by hand |
| `aside.go` | setting a ticket aside: its note, deferring it, the tickets set aside in this run |
| `late_close.go` | tickets set aside with their workers left in their tabs: following them, and merging one closed there (`LATE_CLOSE`) |
| `closed_unmerged.go` | closed tickets whose work is only on their branch, which hold what they block (`CLOSED_UNMERGED`) |
| `deferred_free.go` | deferred tickets whose blockers have all merged, listed in the `START` block and the nothing-to-run box (`DEFERRED_FREE`) |
| `scope.go` | parents after their children, runs of one ticket and its subtickets (`--ticket`) and how they end (`SCOPE_DONE`, `SCOPE_OPEN`) |
| `nothing.go` | why a run has nothing to run (`NothingToRun`, `CheckNothingToRun`): everything done, or nothing ready and what holds it |
| `footprint.go` | tickets' footprints (the files and functions they name, and the files their workers edit), skipping a ticket that overlaps a running one, warning when two workers edit one file |
| `plan.go` | `orchestra plan`'s proposal: blocks links between open tickets whose footprints overlap |
| `events.go` | events and status sent to the dashboard, and what the run counts of them |
| `log.go` | the log file and notifications |
| `eventstream.go` | the event stream (`events.jsonl`), and what a notification says of an event (`Notice`) |
| `drain.go` | stopping after the running tickets when asked (s in the dashboard, SIGUSR1), and taking that back |
| `environment.go` | holding the run when workers keep failing at once, setups keep failing in new worktrees or triage keeps blaming the environment, reopening the tickets that did nothing, probing the machine to take tickets again |
| `usagelimit.go` | holding the run while workers wait on the account's usage limit, telling them to carry on once it resets, and stopping the run (`USAGE_LIMIT`) when it holds longer than `usage_limit_wait` |
| `advice.go` | triage and the run review |
| `hooktally.go` | what each ticket's workers' hooks noted in the run (permission prompts, compactions), for triage and the run review |
| `organcost.go` | what the run's organ calls cost: logged with each call, summed for the run review |
| `fullcheck.go` | the full check (`check_full`) after a run that merged a ticket, in a worktree of its own, the setup first, and the ticket filed when it fails |
| `predict.go` | predicting the files of ready tickets that name none, in the background; the `predictor`, its queue and predictions |
| `deps.go` | the interfaces to Beads, the terminal (Herdr, tmux or cmux), git and workers' reports; what every terminal keeps to: the launch contract, its refusals (`ErrNameRefused`, `ErrArgumentRefused`) and how long it waits for an agent |
| `ticket.go` | `Ticket`, a Beads issue as the loop sees it: its status, labels, deferral and the question it waits on |
| `activity.go` | what a worker's hooks report (`ToolUse`, `HookRecord`) and what the dashboard says it is doing (`Doing`) |

The fakes the tests share are in `fakes_test.go` (Beads, workers, sinks), `fakeherdr_test.go`, `fakegit_test.go` and `loop_test.go`; the harness that runs a whole loop against them is in `helpers_test.go`. A scenario whose subject is timing uses `newTimedHarness` inside `synctest.Test`: git in memory, and the loop's real durations on the bubble's clock, which moves on whenever every goroutine waits. One whose subject is git (merging, rebasing, conflicts, reusing a worktree) uses `newHarness`, with real git on the real clock.

The organs, `internal/organ`, have one file each (`triage.go`, `review.go`, `predict.go`, `scout.go`), and `organ.go` runs them. An organ is a `spec`: its name, time limit, effort, JSON schema and tools, and its system prompt, a template in `prompts/` named after it, rendered once with the numbers and paths the code holds (`prompts.go`) and ended by the evidence rule every organ shares (`prompts/evidence.md`). Its method gathers the input and calls `askAs` with the spec and a parser for the answer. `TestDocsGiveTheOrgansSpecs` checks the time limits and efforts [docs/organs.md](organs.md) gives against the specs.

### Adding a terminal or an agent

A terminal workers run in is a package of its own (`internal/herdr`, `internal/tmux`, `internal/cmux`) that keeps to the interfaces in `internal/dispatch/deps.go` and the one for a session with the user in a pane beside orchestra's (`interviewPanes`: a feature talked through, a spec refined), plus one row of `terminalKinds` in `cmd/orchestra/terminal.go`: its name, as settings.json and `--terminal` take it; how the log names it and the place of its workers' tabs (`dispatch.TerminalLabel`, for the `START` line and the workers kept for a run elsewhere); whether orchestra runs in one of its panes (`inside`, its variables), how deep its panes are where terminals run one in another (`depth`) and whether it can tell its own pane (`ownPane`), which `pickTerminal` reads to choose under auto; whether auto picks it outside every terminal (`fallback`); the check that finds what a run needs there (`check`); whether its workers report through hooks (`hookDriven`); and how a run opens it (`open`). Nothing outside the table switches on a terminal's name: `checkTerminal`, `terminalOf`, the sessions with the user and the run's labels all read the row. A terminal that can't read what an agent is doing from its screen is hook-driven: its adapter uses `internal/hookterm`, and a run there takes only agents whose sessions report through hooks, its probe's and the sessions' with the user included.

An agent a hook-driven terminal can run is a `hookterm.AgentUI` (`claude.UI` for Claude Code): where the hooks it is launched with write their records, and what they, with its screen, tell of its session. Adding one is its package and a row of `agentUIs` in `internal/hookterm/agentui.go`, keyed by the agent kind (`--agent`) the adapters keep for each pane; the problem a hook-driven terminal gives any other agent (`hookWorkers`) names the agents that report.

## The cmux tests

cmux is a macOS app with one instance on a machine, whose socket by default answers only programs started in its own terminals. The tests that run it, `internal/cmux`'s `TestLive…` (workspaces, the split, the trust dialog, a prompt of several lines, `cmux top`), `internal/dispatch/cmux_run_test.go`'s whole runs (a ticket merged, a permission prompt, Ctrl+C and the next run carrying the worker over, cmux quitting and the next run resuming the worker in a new workspace) and `cmd/orchestra`'s `TestCmux…` (the interview in a split, the dashboard's 1–9 keys), each start a cmux of their own with `cmuxtest.Start`:

- The app's binary (`cmux.app/Contents/MacOS/cmux`) is started directly, not through `open`, with `CMUX_SOCKET_MODE=allowAll`, which cmux takes only from its environment and which has its socket answer any process: the test's, outside cmux's terminals. Running the tests from inside a cmux terminal instead, where the default mode lets them in, would need the user's cmux, which the tests mustn't touch, and a runner has no cmux terminal to start them from.
- Its `HOME` and `CFFIXED_USER_HOME` are a folder of the test's own, so its settings, saved session and socket (`.local/state/cmux/`, the path in `last-socket-path`) are the test's; `CMUX_SOCKET_PATH` names that socket for the test, and the app's own CLI goes first on PATH. Its terminals' shells are login shells, with the user's `HOME` again.
- It is quit (SIGTERM) as the test ends, and the test waits for it to exit; `Restart` starts it again on the same folder, which restores the workspaces it saved, by UUID.
- cmux won't run twice: a second app exits as it starts. So the tests of every package take turns (a lock file in the temporary folder), and a test that finds a cmux it didn't start running skips, or with `ORCHESTRA_REQUIRE_CMUX=1` fails.

Locally they skip unless asked for, as they start the cmux app: quit your cmux, and run them with `ORCHESTRA_CMUX_TEST=1`, for example `ORCHESTRA_CMUX_TEST=1 go test -run 'TestLive|TestCmux' ./internal/cmux/ ./internal/dispatch/ ./cmd/orchestra/`. Its windows open on your screen while they run. `ORCHESTRA_CMUX_APP` names another `cmux.app` to run them against, one of another version unpacked from its disk image, say. `-short` skips them, and they skip without cmux, unless `ORCHESTRA_REQUIRE_CMUX=1`, which CI's macOS leg sets.

The workers are the fake Claude Code (`claudetest.Claude`), so what is left to a person, before a release that changes the adapter, is the real Claude Code in cmux: one ticket through a run (`--terminal cmux`, from a cmux terminal), its prompt arriving whole and the trust dialog answered, and a feature talked through with claude in the split. Note the result on the ticket.

## Releasing

A release is a commit on main and a tag; the release workflow does the rest. Nothing in the code carries the version: a release's binaries get it from the tag (`-X main.version=vX.Y.Z`, which `buildVersion` in `cmd/orchestra/run.go` reads), and `go install …@vX.Y.Z` from the module's version.

1. In `CHANGELOG.md`, rename `## [Unreleased]` to `## [X.Y.Z] - YYYY-MM-DD`, and add its compare link above the others at the end: `[X.Y.Z]: https://github.com/noesis-sol/orchestra/compare/vP.Q.R...vX.Y.Z`, from the previous release. `scripts/release-notes.sh vX.Y.Z` prints the section, which becomes the Release's notes.
2. On main, with a clean tree and `scripts/check.sh` passing, commit it as `Release X.Y.Z`.
3. Tag the commit, annotated: `git tag -a vX.Y.Z -m "orchestra X.Y.Z"`.
4. Push both: `git push origin main vX.Y.Z`.

The tag starts `.github/workflows/release.yml` (Actions, Release), which on Linux (`ubuntu-latest`; GoReleaser cross-compiles the darwin binaries, and the Check workflow runs the check on macOS too):

- checks out the tag with every tag, and beside it the workflow's own commit, whose `.goreleaser.yaml`, `scripts/release-notes.sh` and `scripts/sign-checksums` it uses (a tag released again may predate them);
- sets up the Go of the tag's `go.mod` (its `toolchain` line) and runs the tag's `scripts/check.sh`: a failing check fails the release, before anything is built;
- takes the notes from the tag's `CHANGELOG.md` (`scripts/release-notes.sh`), and fails when the version has no section;
- runs GoReleaser (`.goreleaser.yaml`): `orchestra` for darwin and linux, amd64 and arm64, with `CGO_ENABLED=0` and `-trimpath`, in `orchestra_X.Y.Z_<os>_<arch>.tar.gz` with `README.md`, `CHANGELOG.md` and `THIRD_PARTY_NOTICES.md` (and a `LICENSE` once there is one), and their SHA-256 sums in `checksums.txt`;
- signs `checksums.txt` with the release key (below), within GoReleaser (its `signs:`), as `checksums.txt.sig`. `orchestra update` verifies it before trusting a sum in `checksums.txt` and installs nothing from a release without a valid one. Signing comes before anything is published: without the key, or with one whose public half isn't among the release keys, the release fails and the Release is left as it was;
- creates the GitHub Release with the notes and attaches the archives, `checksums.txt` and `checksums.txt.sig`; a tag with a suffix (`v1.2.0-rc.1`) makes a pre-release. These names are what `orchestra update` and `scripts/install.sh` download: change them together;
- marks the Release as GitHub's latest only when its tag is the newest release tag, so releasing an older tag again leaves the latest where it is;
- attests the archives' build provenance (`actions/attest`), which `gh attestation verify orchestra_X.Y.Z_darwin_arm64.tar.gz --repo noesis-sol/orchestra` checks.

To release an existing tag again (a failed run, a tag from before the workflow, notes fixed on main), run the workflow by hand with the tag: Actions, Release, Run workflow, or `gh workflow run release.yml -f tag=vX.Y.Z`. It builds the tag as it is, with the configuration of the branch it runs from (main), and replaces the Release's notes and assets, including those of a Release made by hand with notes only. Changing what the tag points at needs a new version instead.

To try the build without publishing anything, run [GoReleaser](https://goreleaser.com/install/) (v2.18.2, as the workflow does) in the repository:

```
goreleaser check
goreleaser release --snapshot --clean --skip=sign
```

The snapshot builds the four archives and `checksums.txt` in `dist/` (git-ignored), versioned after the last tag (`v0.5.2-snapshot` after `v0.5.1`, which `--version` prints). Without `--skip=sign` it signs `checksums.txt` too, and fails without the release key in `ORCHESTRA_RELEASE_KEY` (below), as the release would. A snapshot has no release notes: GoReleaser skips its changelog step for one, `--release-notes` included, and the notes reach only the Release.

### The release key

A release's `checksums.txt` is signed with an ed25519 key, so that `orchestra update` installs only what the release workflow built: the SHA-256 sums alone come from the same Release as the archives, and catch a corrupt download, not a changed Release. The private key, PKCS#8 PEM, is the repository secret `ORCHESTRA_RELEASE_KEY`, which only the release workflow reads. Its public half, PKIX PEM, is committed in `internal/update/release-keys/` (one `.pem` a key, named after the month it was made) and built into orchestra; `orchestra update` takes a signature by any of them. The signature is the raw 64 bytes, and `scripts/sign-checksums` makes it. GoReleaser runs it (`signs:` in `.goreleaser.yaml`) from `$ORCHESTRA_RELEASE_DIR`, which the workflow sets to the checkout of its own commit, or else from the repository it runs in; the key reaches it with the rest of GoReleaser's environment. A release signed by hand runs it with the key in the variable:

```
ORCHESTRA_RELEASE_KEY="$(cat release-key.pem)" go run ./scripts/sign-checksums dist/checksums.txt
gh release upload vX.Y.Z dist/checksums.txt.sig --clobber
```

It refuses a key whose public half isn't in `internal/update/release-keys/`, as `orchestra update` would refuse the release. To check a release's signature by hand: `openssl pkeyutl -verify -pubin -inkey internal/update/release-keys/<key>.pem -rawin -in checksums.txt -sigfile checksums.txt.sig`.

To replace the key (it leaked, or is old), add the new one a release before retiring the old, so that the binaries of both releases verify the next:

1. Make the new pair: `openssl genpkey -algorithm ed25519 -out release-key.pem` and `openssl pkey -in release-key.pem -pubout -out internal/update/release-keys/YYYY-MM.pem`. Add the public key to `release_keys` in `scripts/install.sh` too (a test checks the two match). Commit the public key and release, still signed with the old key: this release trusts both.
2. Set the secret `ORCHESTRA_RELEASE_KEY` to the new private key (`gh secret set ORCHESTRA_RELEASE_KEY < release-key.pem`), and keep the file out of the repository.
3. Once a release signed with the new key is out, delete the old `.pem`, and the old key from `scripts/install.sh`.

A binary from before step 1's release trusts only the old key: once the latest release is signed with the new one, `orchestra update` refuses it, and that binary needs a new install (`go install`, or a release's archive). Releasing step 1 well before step 2 keeps that to binaries that missed it. If the old key leaked, set the new secret and delete the old `.pem` in one go instead: a leaked key's signatures mustn't verify for a release longer than needed.

The build provenance (`gh attestation verify`, above) is a second check, by hand: it doesn't need the release key, and `orchestra update` doesn't read it.

[Changelog](../CHANGELOG.md)

## Differences from orchestrate.sh

- Several tickets run at once (`--concurrent`), each with its own worker, worktree and tab, and finished ones merge one at a time; `orchestrate.sh` worked on one ticket at a time.
- `python3` is no longer needed.
- If `herdr agent start` reports a failure but the worker came up anyway, the orchestrator uses it instead of retrying into an occupied pane, which ends in `START_FAILED`.
- The dispatch log line includes the ticket title: `[1/40] kinieta-2e7 dispatching: <title>`.
