#!/bin/sh
# The project's full check: vet, ShellCheck, the race tests and the linters in .golangci.yml. Orchestra
# runs it once at the end of a run that merged a ticket (check_full in .orchestra/settings.json). With
# -short it is the merge check, scripts/check-fast.sh, which orchestra runs before merging each ticket
# (check_fast): the same, but the tests run with -short, without those that take over a second. A worker
# checks its own change, before closing its ticket, with scripts/check-own.sh.
# gotestsum and golangci-lint run through go run at pinned versions, so a machine needs only Go; ShellCheck
# is downloaded once at a pinned version (scripts/shellcheck.sh). The tests that run the real tmux skip
# where it isn't installed, unless ORCHESTRA_REQUIRE_TMUX=1 (CI sets it), when they fail instead.
set -e
short=
case "${1-}" in
-short) short=-short ;;
"") ;;
*)
	echo "usage: scripts/check.sh [-short]" >&2
	exit 2
	;;
esac
# The last line says whether the check passed, or the step it failed in, its exit status unchanged:
# 'scripts/check.sh: PASS', 'scripts/check.sh -short: FAIL in tests (exit 1)'. Its end alone then
# answers, through tail or not.
step=vet
finish() {
	status=$?
	if [ "$status" -eq 0 ]; then
		echo "scripts/check.sh${short:+ $short}: PASS"
	else
		echo "scripts/check.sh${short:+ $short}: FAIL in $step (exit $status)"
	fi
	exit "$status"
}
trap finish EXIT
go vet ./...
step=shellcheck
# Every tracked shell script, through ShellCheck: offline with nothing cached, it says it skipped, and
# passes, except under CI=true.
scripts/shellcheck.sh
# The race tests, shuffled, through gotestsum, with its reruns and its FLAKY: and DATA RACE: lines
# (scripts/race-tests.sh, which scripts/check-own.sh runs too). A failed run keeps go test's events.
step=tests
scripts/race-tests.sh ./... ${short:+"$short"}
# golangci-lint at its pinned version (scripts/lint.sh, which scripts/check-own.sh runs too).
step=lint
scripts/lint.sh ./...
