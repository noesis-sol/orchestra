#!/bin/sh
# The project's full check: vet, the race tests and the linters in .golangci.yml. Orchestra runs it
# before merging a rebased ticket (.orchestra/settings.json), and workers run it before closing one.
# gotestsum and golangci-lint run through go run at pinned versions, so a machine needs only Go.
set -e
go vet ./...
# The race tests run through gotestsum, which runs a failed test once more, on its own: one that passes
# then failed only under load (several workers' checks at once) or after another test (below), so the
# check passes, printing a FLAKY: line for it that orchestra warns of. A test that fails its rerun fails
# the check, and so, without a rerun, do more than three failed tests (a real breakage), a data race, a
# panic and a package that fails outside its tests (below). gotestsum is built with the project's own Go
# (see golangci-lint below), and its go test runs with it.
# The tests run shuffled, in a new order each check, to find one that passes or fails only after another:
# go test orders every package's tests by the one seed the check picks (-shuffle=on would pick one for
# each package and print them without the package's name). Each FLAKY: line and the end of a failed
# check's output name the seed, and -shuffle=<seed> repeats the order (docs/development.md).
seed="$(od -An -N4 -tu4 /dev/urandom | tr -d ' ')"
events="$(mktemp)"
trap 'rm -f "$events"' EXIT
GOTOOLCHAIN="$(go env GOVERSION)" go run gotest.tools/gotestsum@v1.13.0 --format=standard-quiet \
	--packages=./... --jsonfile="$events" --rerun-fails=1 --rerun-fails-max-failures=3 \
	--rerun-fails-abort-on-data-race -- -race -shuffle="$seed" ||
	{ echo "The tests ran in the order of -shuffle=$seed"; exit 1; }
# gotestsum passed, so each test that failed passed its rerun: 'FLAKY: <package> <test>
# (-shuffle=<seed>)', the package relative to the module, but for a subtest's parent, which failed with
# it. Not so a package that failed with none of its tests failing, in TestMain (goleak's check) or an
# init: gotestsum reruns it with no test, which passes. go test -json's events, of the first run and the
# rerun, open each run of a package with a start.
awk -v module="$(go list -m)" -v seed="$seed" '
	BEGIN { prefix = length("\"Package\":\"" module) }
	{
		pkg = test = ""
		if (match($0, /"Package":"[^"]*"/)) pkg = "." substr($0, RSTART + prefix, RLENGTH - prefix - 1)
		if (match($0, /"Test":"([^"\\]|\\.)*"/)) test = substr($0, RSTART + 8, RLENGTH - 9)
	}
	/"Action":"start"/ { failedTest[pkg] = 0 }
	/"Action":"fail"/ && test != "" { failedTest[pkg] = 1; failed[++n] = pkg " " test }
	/"Action":"fail"/ && test == "" && !failedTest[pkg] {
		print "FAIL: " pkg " failed outside its tests, in TestMain or an init"
		outside = 1
	}
	END {
		if (outside) exit 1
		for (i = 1; i <= n; i++) {
			parent = 0
			for (j = 1; j <= n; j++) if (index(failed[j], failed[i] "/") == 1) parent = 1
			if (!parent) print "FLAKY: " failed[i] " (-shuffle=" seed ")"
		}
	}' "$events"
# Built with the project's own Go: golangci-lint refuses to check code that targets a newer Go than it
# was built with, and go run would otherwise build it with the older Go its own module asks for.
# golangci-lint locks a file in the system temp directory, one for every worktree, and by default fails
# ('parallel golangci-lint is running') when another run still holds it after 5s, but orchestra runs
# several workers' checks and its own merge checks at once. --allow-serial-runners waits for the lock
# instead, a few seconds with a warm cache. Not --allow-parallel-runners: its docs don't say parallel
# runs share the lint cache safely, and one lint at a time eases the load on the race tests beside it.
GOTOOLCHAIN="$(go env GOVERSION)" go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run \
	--allow-serial-runners ./...
