#!/bin/sh
# The race tests, as scripts/check.sh and scripts/check-own.sh run them: go test -race on the packages
# given, a list such as './...' or './internal/git ./cmd/orchestra', with the go test flags after it, such
# as -short or -run.
#
# usage: scripts/race-tests.sh <packages> [go test flags...]
#
# The race tests run through gotestsum, which runs a failed test once more, on its own: one that passes
# then failed only under load (several workers' checks at once) or after another test (below), so the
# run passes, printing a FLAKY: line for it that orchestra warns of. A test that fails its rerun fails
# the run, and so, without a rerun, do more than three failed tests (a real breakage), a data race, a
# panic and a package that fails outside its tests (below). gotestsum is built with the project's own Go
# (see scripts/lint.sh), and its go test runs with it.
# The tests run shuffled, in a new order each run, to find one that passes or fails only after another:
# go test orders every package's tests by the one seed the run picks (-shuffle=on would pick one for
# each package and print them without the package's name). Each FLAKY: line and the end of a failed
# run's output name the seed, and -shuffle=<seed> repeats the order (docs/development.md).
set -e
if [ $# -lt 1 ] || [ -z "$1" ]; then
	echo "usage: scripts/race-tests.sh <packages> [go test flags...]" >&2
	exit 2
fi
packages="$1"
shift
seed="$(od -An -N4 -tu4 /dev/urandom | tr -d ' ')"
events="$(mktemp)"
keep=
trap 'if [ -z "$keep" ]; then rm -f "$events"; fi' EXIT
# A failed run keeps go test -json's events, every test's output, in a file it names: above all for
# the race detector's reports, as a data race stops the run without a rerun and may never recur, and
# gotestsum prints them amid the failed tests' output, which a long check's reader may not keep. Its end
# names each test that raced, 'DATA RACE: <package> <test>', as the FLAKY: lines below do, or
# '(outside its tests)' for a race in TestMain or after its test ended (docs/development.md).
GOTOOLCHAIN="$(go env GOVERSION)" go run gotest.tools/gotestsum@v1.13.0 --format=standard-quiet \
	--packages="$packages" --jsonfile="$events" --rerun-fails=1 --rerun-fails-max-failures=3 \
	--rerun-fails-abort-on-data-race -- -race -shuffle="$seed" "$@" || {
	awk -v module="$(go list -m)" '
		BEGIN { prefix = length("\"Package\":\"" module) }
		/"Output":"WARNING: DATA RACE\\n"/ {
			pkg = test = ""
			if (match($0, /"Package":"[^"]*"/)) pkg = "." substr($0, RSTART + prefix, RLENGTH - prefix - 1)
			if (match($0, /"Test":"([^"\\]|\\.)*"/)) test = substr($0, RSTART + 8, RLENGTH - 9)
			if (test == "") test = "(outside its tests)"
			if (!seen[pkg " " test]++) print "DATA RACE: " pkg " " test
		}' "$events"
	keep=1
	echo "go test's events (go test -json) are kept in $events"
	echo "The tests ran in the order of -shuffle=$seed"
	exit 1
}
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
