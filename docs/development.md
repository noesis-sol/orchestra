# Development

Working on orchestra itself. Back to the [README](../README.md).

## Development

```
scripts/check.sh
```

`scripts/check.sh` is the full check: `go vet ./...`, the race tests, shuffled (`go test -race -shuffle=<seed> ./...`, through [gotestsum](https://github.com/gotestyourself/gotestsum)) and golangci-lint. It is also orchestra's own check command for this repository (`.orchestra/settings.json`), so a ticket that fails lint isn't merged, and workers run it before closing a ticket. golangci-lint runs the linters the [Uber Go style guide](https://github.com/uber-go/guide/blob/master/style.md#linting) asks for, configured in `.golangci.yml`: errcheck (terminal writes excepted), goimports, revive, govet and staticcheck, plus predeclared, lll (lines up to 120 columns), modernize (the newer standard-library forms, such as `slices.Contains`, `strings.Cut` and `WaitGroup.Go`) and errorlint (`errors.Is` and `errors.As` rather than `==` or a type assertion on an error, which may be wrapped; its `fmt.Errorf` check is off, since some calls format a second error with `%v` on purpose). The script runs gotestsum and golangci-lint with `go run` at pinned versions (v1.13.0 and v2.14.0), so a machine needs only Go; the first run downloads them.

GitHub Actions runs the check on Linux and macOS for each push to main and each pull request (`.github/workflows/check.yml`, Actions, Check). The tests need nothing of the machine's but Go, git and `/bin/sh`, so they pass on a runner as on a workstation: a test that runs bd, herdr or claude puts a fake first on PATH (`faketool`), a configuration test finds a stand-in bd and herdr at the end of PATH (`hostTools`), its repositories start on `main` whatever git's default, and `gittest.Main` gives git an identity for the commits orchestra's rebases make.

While iterating, `go test -short` runs a package without its slow tests, those that take over a second: the scenarios with real git in `internal/dispatch` (`gitRepo` skips them), the runs on a pseudo-terminal in `cmd/orchestra` (`openTerminal`), and the others, each skipped at its start. `go test -short ./internal/dispatch/...` takes a few seconds, against about 25 in full. `scripts/check.sh` never passes `-short`, so every test still runs before a ticket closes and before it merges. A new test that takes over a second starts the same way:

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

The functions that read text a model, a worker or a ticket's author wrote have fuzz tests, each in a `_fuzz_test.go` file: `FuzzParsePlan` (`internal/organ`), `FuzzTicketFootprint` and `FuzzIDProblem` (`internal/dispatch`), `FuzzAgentName` and `FuzzReadAgent` (`internal/herdr`) and `FuzzParseLinked` (`internal/beads`). `go test` and the check run only their seeds, as ordinary tests. After changing one of those functions, fuzz it for a minute or so; Go fuzzes one target of one package at a time:

```
go test -run '^$' -fuzz '^FuzzParsePlan$' -fuzztime 60s ./internal/organ
```

The fuzzer saves an input that fails in the package's `testdata/fuzz/<target>/`, where `go test` runs it with the seeds from then on: commit it with the fix, or add it to the target's seeds (`f.Add`). The inputs it found new paths with stay in Go's cache, so the next run goes on from them; `go clean -fuzzcache` empties it.

`TestLiveOrgans` calls the real `claude` against a real repository without writing anything. Its comment shows how to run it.

The fake bd, herdr, claude and git the tests run are shell scripts written with `internal/faketool`, whose `Write` makes each a hard link to one dispatcher per test binary, run once, beside the script it runs. A new executable would cost far more: macOS scans each one the first time it runs, which takes 0.2-0.6 seconds, and several seconds while other workers run their checks. A package whose tests call `Write` runs them through `faketool.Main` from its `TestMain`.

A package whose tests run the real git runs them through `gittest.Main` (`internal/gittest`) from its `TestMain`, as `internal/git`, `internal/dispatch`, `internal/project` and `cmd/orchestra` do. It turns off git's automatic maintenance (`maintenance.auto=false`, through `GIT_CONFIG_COUNT`) for every git the tests start, orchestra's included. After a commit, a merge or a rebase, git starts that maintenance detached, and since git 2.54 it repacks a repository with a few loose objects: one still writing in a test's repository as the test ends fails the test with `TempDir RemoveAll cleanup: unlinkat .../.git/objects: directory not empty`.

The run loop, `internal/dispatch`, has one file per concern, its tests in the `_test.go` file of the same name:

| File | What's in it |
| --- | --- |
| `loop.go` | the `Loop` type, `Config`, exit codes, timings and shared helpers |
| `run.go` | `Run`: picking the next ticket, solo tickets, HOLD, interrupts (tests in `run_test.go` and `schedule_test.go`) |
| `ids.go` | which ticket IDs can be run (plain names, valid in a branch), setting aside the others |
| `start.go` | a ticket's worktree, starting, adopting and naming its worker, delivering its prompt |
| `work.go` | one ticket from start to outcome: asked, deferred, paused or closed |
| `asked.go` | tickets waiting on a question that don't come back through `bd ready`: adopting a worker that claimed or closed its ticket in its tab, one it deferred or left, labelling what is left as the run ends |
| `carry.go` | the workers a run leaves behind (asked, left running), saved to `.orchestra/run/state.json` as it ends and checked and carried over by the next run |
| `settle.go` | waiting for a worker to settle (Herdr's status, its Stop hook or the start-up grace), telling one that stopped with its ticket in progress to continue, reading its status, the dashboard watcher |
| `resume.go` | resuming a Claude worker gone from its tab with its ticket in progress, or back from its answered question: its session, recorded by its hooks, in a new tab, once per ticket in a run |
| `merge.go` | merging a closed ticket: rebase, check command, fast-forward, cleanup (the worker's tab only while Herdr still has it labelled with the ticket's ID) |
| `recheck.go` | checking a ticket set aside for a failed check once more after Base moves on, unless it failed in its own code |
| `checkback.go` | handing a check that fails on a rebased branch back to the ticket's worker to fix, and checking what it committed |
| `setup.go` | the setup command (`setup`), run before the check on a rebased branch whose rebase changed a dependency file |
| `holds.go` | tickets held for an unmerged blocker, the `unmerged` label, tickets set aside, deferred or waiting on a question |
| `scope.go` | parents after their children, runs of one ticket and its subtickets (`--ticket`) and how they end (`SCOPE_DONE`, `SCOPE_OPEN`) |
| `footprint.go` | tickets' footprints (the files and functions they name, and the files their workers edit), skipping a ticket that overlaps a running one, warning when two workers edit one file |
| `plan.go` | `orchestra plan`'s proposal: blocks links between open tickets whose footprints overlap |
| `events.go` | the log file, notifications, the event stream (`events.jsonl`), events and status sent to the dashboard |
| `drain.go` | stopping after the running tickets when asked (s in the dashboard, SIGUSR1), and taking that back |
| `environment.go` | holding the run when workers keep failing at once or triage keeps blaming the environment, reopening the tickets that did nothing, probing the machine to take tickets again |
| `advice.go` | triage and the run review |
| `fullcheck.go` | the full check (`check_full`) after a run that merged a ticket, in a worktree of its own, the setup first, and the ticket filed when it fails |
| `predict.go` | predicting the files of ready tickets that name none, in the background |
| `deps.go` | the interfaces to Beads, Herdr, git and workers' reports |

The fakes the tests share are in `fakes_test.go` (Beads, workers, sinks), `fakeherdr_test.go`, `fakegit_test.go` and `loop_test.go`; the harness that runs a whole loop against them is in `helpers_test.go`. A scenario whose subject is timing uses `newTimedHarness` inside `synctest.Test`: git in memory, and the loop's real durations on the bubble's clock, which moves on whenever every goroutine waits. One whose subject is git (merging, rebasing, conflicts, reusing a worktree) uses `newHarness`, with real git on the real clock.

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
- creates the GitHub Release with the notes and attaches the archives, `checksums.txt` and `checksums.txt.sig`; a tag with a suffix (`v1.2.0-rc.1`) makes a pre-release. These names are what `orchestra update` downloads: change them together;
- marks the Release as GitHub's latest only when its tag is the newest release tag, so releasing an older tag again leaves the latest where it is;
- attests the archives' build provenance (`actions/attest`), which `gh attestation verify orchestra_X.Y.Z_darwin_arm64.tar.gz --repo noesis-sol/orchestra` checks.

To release an existing tag again (a failed run, a tag from before the workflow, notes fixed on main), run the workflow by hand with the tag: Actions, Release, Run workflow, or `gh workflow run release.yml -f tag=vX.Y.Z`. It builds the tag as it is, with the configuration of the branch it runs from (main), and replaces the Release's notes and assets, including those of a Release made by hand with notes only. Changing what the tag points at needs a new version instead.

To try the build without publishing anything, run [GoReleaser](https://goreleaser.com/install/) (v2.18.2, as the workflow does) in the repository:

```
goreleaser check
goreleaser release --snapshot --clean --skip=sign
```

The snapshot builds the four archives and `checksums.txt` in `dist/` (git-ignored), versioned after the last tag (`v0.5.2-snapshot` after `v0.5.1`, which `--version` prints). Without `--skip=sign` it signs `checksums.txt` too, and fails without the release key in `ORCHESTRA_RELEASE_KEY` (below), as the release would.

### The release key

A release's `checksums.txt` is signed with an ed25519 key, so that `orchestra update` installs only what the release workflow built: the SHA-256 sums alone come from the same Release as the archives, and catch a corrupt download, not a changed Release. The private key, PKCS#8 PEM, is the repository secret `ORCHESTRA_RELEASE_KEY`, which only the release workflow reads. Its public half, PKIX PEM, is committed in `internal/update/release-keys/` (one `.pem` a key, named after the month it was made) and built into orchestra; `orchestra update` takes a signature by any of them. The signature is the raw 64 bytes, and `scripts/sign-checksums` makes it. GoReleaser runs it (`signs:` in `.goreleaser.yaml`) from `$ORCHESTRA_RELEASE_DIR`, which the workflow sets to the checkout of its own commit, or else from the repository it runs in; the key reaches it with the rest of GoReleaser's environment. A release signed by hand runs it with the key in the variable:

```
ORCHESTRA_RELEASE_KEY="$(cat release-key.pem)" go run ./scripts/sign-checksums dist/checksums.txt
gh release upload vX.Y.Z dist/checksums.txt.sig --clobber
```

It refuses a key whose public half isn't in `internal/update/release-keys/`, as `orchestra update` would refuse the release. To check a release's signature by hand: `openssl pkeyutl -verify -pubin -inkey internal/update/release-keys/<key>.pem -rawin -in checksums.txt -sigfile checksums.txt.sig`.

To replace the key (it leaked, or is old), add the new one a release before retiring the old, so that the binaries of both releases verify the next:

1. Make the new pair: `openssl genpkey -algorithm ed25519 -out release-key.pem` and `openssl pkey -in release-key.pem -pubout -out internal/update/release-keys/YYYY-MM.pem`. Commit the public key and release, still signed with the old key: this release trusts both.
2. Set the secret `ORCHESTRA_RELEASE_KEY` to the new private key (`gh secret set ORCHESTRA_RELEASE_KEY < release-key.pem`), and keep the file out of the repository.
3. Once a release signed with the new key is out, delete the old `.pem`.

A binary from before step 1's release trusts only the old key: once the latest release is signed with the new one, `orchestra update` refuses it, and that binary needs a new install (`go install`, or a release's archive). Releasing step 1 well before step 2 keeps that to binaries that missed it. If the old key leaked, set the new secret and delete the old `.pem` in one go instead: a leaked key's signatures mustn't verify for a release longer than needed.

The build provenance (`gh attestation verify`, above) is a second check, by hand: it doesn't need the release key, and `orchestra update` doesn't read it.

[Changelog](../CHANGELOG.md)

## Differences from orchestrate.sh

- Several tickets run at once (`--concurrent`), each with its own worker, worktree and tab, and finished ones merge one at a time; `orchestrate.sh` worked on one ticket at a time.
- `python3` is no longer needed.
- If `herdr agent start` reports a failure but the worker came up anyway, the orchestrator uses it instead of retrying into an occupied pane, which ends in `START_FAILED`.
- The dispatch log line includes the ticket title: `[1/40] kinieta-2e7 dispatching: <title>`.
