#!/bin/sh
# The project's merge check, which orchestra runs on a finished ticket rebased onto work merged while it
# ran (check_fast in .orchestra/settings.json): scripts/check.sh with -short, so vet, ShellCheck, the
# race tests but those that take over a second, and golangci-lint, with the full check's reruns, FLAKY:
# and DATA RACE: lines and shuffle seed. The full check, scripts/check.sh, runs once at the end of a run.
exec "$(dirname "$0")/check.sh" -short
