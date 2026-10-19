#!/bin/sh
# golangci-lint at its pinned version, with the configuration in .golangci.yml, on the packages given,
# as scripts/check.sh (./...) and scripts/check-own.sh (the packages a change touched) run it.
#
# usage: scripts/lint.sh <packages...>
#
# Built with the project's own Go: golangci-lint refuses to check code that targets a newer Go than it
# was built with, and go run would otherwise build it with the older Go its own module asks for.
# golangci-lint locks a file in the system temp directory, one for every worktree, and by default fails
# ('parallel golangci-lint is running') when another run still holds it after 5s, but orchestra runs
# several workers' checks and its own merge checks at once. --allow-serial-runners waits for the lock
# instead, a few seconds with a warm cache. Not --allow-parallel-runners: its docs don't say parallel
# runs share the lint cache safely, and one lint at a time eases the load on the race tests beside it.
set -e
if [ $# -eq 0 ]; then
	echo "usage: scripts/lint.sh <packages...>" >&2
	exit 2
fi
GOTOOLCHAIN="$(go env GOVERSION)" exec go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run \
	--allow-serial-runners "$@"
