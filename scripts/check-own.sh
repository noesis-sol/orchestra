#!/bin/sh
# A worker's check of its own change, before it closes its ticket: what the worktree's branch changed
# against Base (the argument, main by default), committed or not, untracked files included. For each Go
# package with a changed file (one of its own, or one in a folder below it that holds no package, such as
# its testdata or the files it embeds): go vet, the race tests without -short, so with the slow ones the
# merge check skips, and golangci-lint, as scripts/check.sh runs them (scripts/race-tests.sh, scripts/lint.sh).
# A change to go.mod or go.sum checks every package; one to .golangci.yml lints every package. ShellCheck
# runs on each changed shell script, and for a changed doc, skill, changelog or worker prompt the tests
# that read it run (below), though their package didn't change.
# The rest, the packages the change didn't touch but which may use it, is the merge check's
# (scripts/check-fast.sh), which orchestra runs on every ticket before merging it, and the full check's
# (scripts/check.sh) at the end of a run.
#
# usage: scripts/check-own.sh [base]
set -e
base=main
case $# in
0) ;;
1) base="$1" ;;
*)
	echo "usage: scripts/check-own.sh [base]" >&2
	exit 2
	;;
esac
cd "$(dirname "$0")/.."
# The last line says whether the check passed, or the step it failed in, its exit status unchanged:
# 'scripts/check-own.sh: PASS', 'scripts/check-own.sh: FAIL in lint (exit 1)', as scripts/check.sh's does.
step=changes
finish() {
	status=$?
	if [ "$status" -eq 0 ]; then
		echo "scripts/check-own.sh: PASS"
	else
		echo "scripts/check-own.sh: FAIL in $step (exit $status)"
	fi
	exit "$status"
}
trap finish EXIT

fork="$(git merge-base "$base" HEAD)"
# The changed files, one a line, from the top of the repository: the branch's commits and the worktree's
# changes since it left Base, a rename as both its names, and the untracked files git doesn't ignore.
changed="$({
	git -c core.quotePath=false diff --name-only --no-renames "$fork" --
	git -c core.quotePath=false ls-files --others --exclude-standard
} | sort -u)"

# The folders that hold a Go package, as go sees them: not testdata, nor one whose name starts with . or _.
dirs="$({
	git -c core.quotePath=false ls-files -- '*.go'
	git -c core.quotePath=false ls-files --others --exclude-standard -- '*.go'
} | awk '
	{
		n = split($0, part, "/")
		for (i = 1; i < n; i++) if (part[i] == "testdata" || part[i] ~ /^[._]/) next
		dir = "."
		if (n > 1) { dir = part[1]; for (i = 2; i < n; i++) dir = dir "/" part[i] }
		print dir
	}' | sort -u)"
# Each changed file's package: the one in its folder, else in the nearest folder above it with one.
packages="$(printf '%s\n' "$changed" | dirs="$dirs" awk '
	BEGIN { n = split(ENVIRON["dirs"], d, "\n"); for (i = 1; i <= n; i++) pkg[d[i]] = 1 }
	$0 != "" {
		dir = $0
		while (1) {
			if (dir ~ /\//) sub(/\/[^\/]*$/, "", dir); else dir = "."
			if (dir in pkg) { print (dir == "." ? "." : "./" dir); break }
			if (dir == ".") break
		}
	}' | sort -u | tr '\n' ' ' | sed 's/ $//')"
lint="$packages"
scripts=
# The tests that read a changed file outside the packages, one 'package test' a line. A test that comes to
# read another of the repository's files, or reads these from elsewhere, is added here.
readers=
reads() { readers="$readers$1 $2
"; }
while IFS= read -r file; do
	case "$file" in
	go.mod | go.sum) packages=./... lint=./... ;;
	.golangci.yml) lint=./... ;;
	esac
	case "$file" in
	*.sh) if [ -f "$file" ]; then scripts="$scripts $file"; fi ;;
	esac
	case "$file" in
	README.md | docs/*.md) reads ./cmd/orchestra TestDocLinksResolve ;;
	esac
	case "$file" in
	docs/organs.md) reads ./internal/organ TestDocsGiveTheOrgansSpecs ;;
	esac
	case "$file" in
	.orchestra/worker-prompt.md)
		reads ./internal/project TestWorkerPromptEndsWithWorkingUnattended
		reads ./internal/project TestWorkerPromptWaitsForUnmergedWork
		;;
	esac
	case "$file" in
	.gitattributes) reads ./internal/git TestTicketsAddingAtTheSameSpotRebaseCleanly ;;
	esac
	# The text that mustn't write a long flag with one dash: the docs, the skills, the prompts and the
	# changelog (and the Go source's comments, which the full check reads at the end of a run).
	case "$file" in
	*/testdata/*) ;;
	README.md | CLAUDE.md | CHANGELOG.md | .orchestra/worker-prompt.md | docs/*.md | skills/*.md | \
		internal/*.md | cmd/*.md)
		reads ./cmd/orchestra TestNoTextWritesALongFlagWithOneDash
		;;
	esac
done <<EOF
$changed
EOF
# The tests that read a changed file, by package, as a -run pattern: '<package> ^(TestA|TestB)$', but for a
# package whose tests all run already.
docs="$(printf '%s' "$readers" | sort -u | awk -v all=" $packages " '
	$1 != "" && index(all, " " $1 " ") == 0 && all != " ./... " {
		if (!($1 in run)) order[++n] = $1
		run[$1] = ($1 in run) ? run[$1] "|" $2 : $2
	}
	END { for (i = 1; i <= n; i++) print order[i] " ^(" run[order[i]] ")$" }')"

echo "scripts/check-own.sh: the changes since $base ($(git rev-parse --short "$fork")):"
echo "  Go packages: ${packages:-none}"
if [ "$lint" != "$packages" ]; then echo "  linted: $lint"; fi
echo "  shell scripts:${scripts:- none}"
if [ -n "$docs" ]; then
	echo "  tests that read the changed files:"
	printf '%s\n' "$docs" | sed 's/^/    /'
else
	echo "  tests that read the changed files: none"
fi

# The package lists are split into their words on purpose: a package's path has no space.
# shellcheck disable=SC2086
if [ -n "$packages" ]; then
	step=vet
	go vet $packages
fi
if [ -n "$scripts" ]; then
	step=shellcheck
	status=0
	for file in $scripts; do
		if [ "$(head -n 1 "$file")" = '#!/bin/sh' ]; then
			scripts/shellcheck.sh -s sh -- "$file" || status=1
		else
			scripts/shellcheck.sh -- "$file" || status=1
		fi
	done
	if [ "$status" -ne 0 ]; then exit "$status"; fi
fi
step=tests
if [ -n "$packages" ]; then
	scripts/race-tests.sh "$packages"
fi
if [ -n "$docs" ]; then
	while read -r pkg pattern; do
		scripts/race-tests.sh "$pkg" -run "$pattern" </dev/null
	done <<EOF
$docs
EOF
fi
# shellcheck disable=SC2086
if [ -n "$lint" ]; then
	step=lint
	scripts/lint.sh $lint
fi
