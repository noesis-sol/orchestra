#!/bin/sh
# Runs ShellCheck at a pinned version, for scripts/check.sh: with no arguments on every shell script git
# tracks (git ls-files '*.sh'), with -s sh for those whose shebang is /bin/sh, so that a bashism in one is
# an error; with arguments, ShellCheck runs with those alone.
#
# ShellCheck is Haskell, so go run can't pin it as it pins gotestsum and golangci-lint: this downloads the
# release's prebuilt binary for this platform once, into a cache that every worktree shares,
# ${XDG_CACHE_HOME:-$HOME/.cache}/orchestra-dev/shellcheck-<version>/, having checked the archive's SHA-256
# against the sums below. A wrong sum fails. Where it can't be fetched (offline, nothing cached), it prints
# 'shellcheck skipped: <why>' and passes, except under CI=true (GitHub Actions sets it), where it fails, so
# that CI always lints.
#
# For the tests only (scripts/shellcheck_test.go): SHELLCHECK_DOWNLOAD_URL replaces the releases' download
# address, and in that mode alone SHELLCHECK_TEST_SHA256 replaces this platform's sum.
set -eu

# The version and the SHA-256 of its archive for each platform. ShellCheck publishes no checksum file: these
# were computed from the release's archives when pinning it (and match the digests GitHub shows for them).
# A new version changes the version and the four sums together (docs/development.md).
version=v0.11.0
sum_darwin_aarch64=339b930feb1ea764467013cc1f72d09cd6b869ebf1013296ba9055ab2ffbd26f
sum_darwin_x86_64=c2c15e08df0e8fbc374c335b230a7ee958c313fa5714817a59aa59f1aa594f51
sum_linux_aarch64=68a8133197a50beb8803f8d42f9908d1af1c5540d4bb05fdfca8c1fa47decefc
sum_linux_x86_64=b7af85e41cc99489dcc21d66c6d5f3685138f06d34651e6d34b42ec6d54fe6f6

releases=https://github.com/koalaman/shellcheck/releases/download

fail() {
	printf 'shellcheck: %s\n' "$*" >&2
	exit 1
}

# skip passes without ShellCheck, saying why, or fails under CI=true.
skip() {
	if [ "${CI:-}" = true ]; then
		fail "$* (CI=true: CI always lints)"
	fi
	printf 'shellcheck skipped: %s\n' "$*"
	exit 0
}

# platform sets os and arch as the release's archives name them, and want to the archive's sum.
platform() {
	kernel="$(uname -s)"
	machine="$(uname -m)"
	case "$kernel" in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) os= ;;
	esac
	case "$machine" in
	x86_64 | amd64) arch=x86_64 ;;
	arm64 | aarch64) arch=aarch64 ;;
	*) arch= ;;
	esac
	case "$os.$arch" in
	darwin.aarch64) want="$sum_darwin_aarch64" ;;
	darwin.x86_64) want="$sum_darwin_x86_64" ;;
	linux.aarch64) want="$sum_linux_aarch64" ;;
	linux.x86_64) want="$sum_linux_x86_64" ;;
	*) skip "ShellCheck $version has no binary pinned for $kernel $machine" ;;
	esac
}

# tools sets downloader to curl or wget, and hasher to sha256sum or shasum, or skips without them.
tools() {
	if command -v curl >/dev/null 2>&1; then
		downloader=curl
	elif command -v wget >/dev/null 2>&1; then
		downloader=wget
	else
		skip "neither curl nor wget is here to download ShellCheck $version"
	fi
	if command -v sha256sum >/dev/null 2>&1; then
		hasher=sha256sum
	elif command -v shasum >/dev/null 2>&1; then
		hasher=shasum
	else
		skip "neither sha256sum nor shasum is here to check ShellCheck's SHA-256"
	fi
}

# fetch downloads the address $1 into the file $2.
fetch() {
	if [ "$downloader" = curl ]; then
		curl -fsSL -o "$2" "$1" </dev/null
	else
		wget -q -O "$2" "$1" </dev/null
	fi
}

# sha256 prints the SHA-256 of the file $1.
sha256() {
	if [ "$hasher" = sha256sum ]; then
		sha256sum "$1" | awk '{ print $1 }'
	else
		shasum -a 256 "$1" | awk '{ print $1 }'
	fi
}

# install puts ShellCheck in $binary: downloaded, checked and unpacked beside the cache, then renamed into
# place, so that checks running at once in several worktrees never run a binary half written.
install() {
	tools
	archive="shellcheck-$version.$os.$arch.tar.gz"
	mkdir -p "$cache" || skip "cannot make $cache"
	tmp="$(mktemp -d "$cache/.download.XXXXXX")" || skip "cannot make a folder in $cache"
	trap 'rm -rf "$tmp"' EXIT
	trap 'exit 1' HUP INT TERM
	fetch "$releases/$version/$archive" "$tmp/$archive" 2>"$tmp/error" ||
		skip "cannot download $releases/$version/$archive: $(cat "$tmp/error")"
	got="$(sha256 "$tmp/$archive")"
	[ "$got" = "$want" ] || fail "$archive's SHA-256 is $got, not the pinned $want: it isn't the release's \
ShellCheck $version. Nothing was installed."
	tar -xzf "$tmp/$archive" -C "$tmp" "shellcheck-$version/shellcheck" </dev/null ||
		fail "cannot unpack shellcheck-$version/shellcheck from $archive"
	chmod 755 "$tmp/shellcheck-$version/shellcheck"
	mv -f "$tmp/shellcheck-$version/shellcheck" "$binary" || fail "cannot move ShellCheck into $cache"
	rm -rf "$tmp"
	trap - EXIT
}

main() {
	if [ -n "${SHELLCHECK_DOWNLOAD_URL:-}" ]; then
		releases="$SHELLCHECK_DOWNLOAD_URL"
	fi
	platform
	if [ -n "${SHELLCHECK_DOWNLOAD_URL:-}" ] && [ -n "${SHELLCHECK_TEST_SHA256:-}" ]; then
		want="$SHELLCHECK_TEST_SHA256"
	fi
	cache="${XDG_CACHE_HOME:-$HOME/.cache}/orchestra-dev/shellcheck-$version"
	binary="$cache/shellcheck"
	[ -x "$binary" ] || install

	if [ $# -gt 0 ]; then
		exec "$binary" "$@"
	fi
	cd "$(dirname "$0")/.."
	status=0
	files="$(git ls-files '*.sh')"
	# One run a script, with -s sh where its shebang is /bin/sh. The names are git's, one a line.
	while IFS= read -r file; do
		[ -n "$file" ] || continue
		if [ "$(head -n 1 "$file")" = '#!/bin/sh' ]; then
			"$binary" -s sh -- "$file" || status=1
		else
			"$binary" -- "$file" || status=1
		fi
	done <<EOF
$files
EOF
	return "$status"
}

main "$@"
