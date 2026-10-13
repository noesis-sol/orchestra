#!/bin/sh
# Installs orchestra from its latest GitHub Release, or the one ORCHESTRA_VERSION names:
#
#	curl -fsSL https://raw.githubusercontent.com/noesis-sol/orchestra/main/scripts/install.sh | sh
#
# It downloads the release's archive for this platform (darwin or linux, amd64 or arm64), checksums.txt and
# its signature, checksums.txt.sig; verifies the signature with the release keys below when an OpenSSL 3 is
# there to do it (macOS's own openssl, LibreSSL, can't verify ed25519: it then says so and goes on), and the
# archive's SHA-256 against checksums.txt; and puts the binary in it in place. It never calls sudo.
#
#	ORCHESTRA_VERSION      the release to install, such as v0.6.0; the latest when unset
#	ORCHESTRA_INSTALL_DIR  the folder to install in; else /usr/local/bin where it can write, or ~/.local/bin
#
# For the tests only (scripts/install_test.go): ORCHESTRA_DOWNLOAD_URL replaces the releases' address,
# https://github.com/noesis-sol/orchestra/releases, and in that mode alone ORCHESTRA_TEST_RELEASE_KEY (a PEM
# public key) replaces the release keys and ORCHESTRA_TEST_SYSTEM_DIR replaces /usr/local/bin.
#
# Everything runs from main, at the end, so that a download cut short runs nothing. POSIX sh: it runs under
# sh, dash and bash.
set -eu

# The public keys a release's checksums.txt may be signed with, those of internal/update/release-keys/, which
# a test checks this list against: a new key goes in both, and an old one leaves both.
release_keys='
-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAQtJ/wvLswsjQ/ZdWOmUEf2Bc14cFvf8lORE4uqdx0hk=
-----END PUBLIC KEY-----
'

releases=https://github.com/noesis-sol/orchestra/releases
system_dir=/usr/local/bin

say() { printf '%s\n' "$*"; }
fail() {
	printf 'orchestra install: %s\n' "$*" >&2
	exit 1
}

# platform sets os and arch from uname, or stops on a platform without a release binary.
platform() {
	kernel="$(uname -s)"
	machine="$(uname -m)"
	case "$kernel" in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) os= ;;
	esac
	case "$machine" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) arch= ;;
	esac
	if [ -z "$os" ] || [ -z "$arch" ]; then
		fail "there is no release binary for $kernel $machine: releases have darwin and linux, amd64 and arm64.
Build it with Go instead: go install github.com/noesis-sol/orchestra/cmd/orchestra@latest"
	fi
}

# fetch downloads the address $1 into the file $2, with curl or else wget.
fetch() {
	if [ "$downloader" = curl ]; then
		curl -fsSL -o "$2" "$1" </dev/null
	else
		wget -q -O "$2" "$1" </dev/null
	fi || fail "cannot download $1"
}

# latest prints the latest release's tag, read from where $releases/latest redirects (…/releases/tag/v0.6.0):
# the GitHub API limits the requests of callers without a token.
latest() {
	if [ "$downloader" = curl ]; then
		where="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$releases/latest" </dev/null)" ||
			fail "cannot read the latest release from $releases/latest"
	else
		# wget prints each redirect's Location; the last is the release's page.
		where="$(wget -q -S --spider "$releases/latest" </dev/null 2>&1 |
			sed -n 's/^ *[Ll]ocation: *//p' | tail -n 1 | tr -d '\r')"
	fi
	case "$where" in
	*/tag/v*) printf '%s\n' "${where##*/tag/}" ;;
	*) fail "cannot read the latest release from $releases/latest (it led to '$where')" ;;
	esac
}

# openssl3 prints the path of an OpenSSL 3 or later, which can verify ed25519, or nothing.
openssl3() {
	for candidate in openssl /opt/homebrew/bin/openssl /usr/local/opt/openssl@3/bin/openssl; do
		if command -v "$candidate" >/dev/null 2>&1; then
			case "$("$candidate" version 2>/dev/null </dev/null)" in
			"OpenSSL "[3-9].* | "OpenSSL "[1-9][0-9]*)
				command -v "$candidate"
				return
				;;
			esac
		fi
	done
}

# verify_signature checks checksums.txt.sig with the release keys, or says why it can't.
verify_signature() {
	openssl="$(openssl3)"
	if [ -z "$openssl" ]; then
		say "The signature of checksums.txt wasn't checked: that needs OpenSSL 3 or later, and there is none here"
		say "(macOS's own openssl is LibreSSL, which can't verify ed25519; brew install openssl@3 provides one)."
		say "The archive's SHA-256 is still checked against checksums.txt."
		return
	fi
	printf '%s\n' "$keys" | awk -v dir="$tmp" '
		/-----BEGIN PUBLIC KEY-----/ { n++; file = dir "/key" n ".pem" }
		file != "" { print > file }
		/-----END PUBLIC KEY-----/ { close(file); file = "" }'
	for key in "$tmp"/key*.pem; do
		[ -f "$key" ] || break
		if "$openssl" pkeyutl -verify -pubin -inkey "$key" -rawin -in "$tmp/checksums.txt" \
			-sigfile "$tmp/checksums.txt.sig" >/dev/null 2>&1 </dev/null; then
			say "Verified the signature of checksums.txt with the release key."
			return
		fi
	done
	fail "checksums.txt.sig doesn't verify checksums.txt with a release key: the release wasn't made by
orchestra's release workflow, or was changed since. Nothing was installed."
}

# sha256 prints the SHA-256 of the file $1.
sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{ print $1 }'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{ print $1 }'
	else
		fail "neither sha256sum nor shasum is here to check the archive's SHA-256"
	fi
}

# install_dir sets dir to the folder to install in, making it where it doesn't exist.
install_dir() {
	if [ -n "${ORCHESTRA_INSTALL_DIR:-}" ]; then
		dir="$ORCHESTRA_INSTALL_DIR"
		mkdir -p "$dir" 2>/dev/null || fail "cannot make $dir"
		[ -w "$dir" ] || fail "cannot write in $dir. Where it belongs to root, run the install with sudo:
curl -fsSL https://raw.githubusercontent.com/noesis-sol/orchestra/main/scripts/install.sh | sudo ORCHESTRA_INSTALL_DIR=$dir sh"
		return
	fi
	if [ -d "$system_dir" ] && [ -w "$system_dir" ]; then
		dir="$system_dir"
		return
	fi
	dir="$HOME/.local/bin"
	mkdir -p "$dir" || fail "cannot make $dir"
	say "Installing in $dir, as $system_dir isn't writable without sudo. To install there instead:"
	say "  curl -fsSL https://raw.githubusercontent.com/noesis-sol/orchestra/main/scripts/install.sh | sudo ORCHESTRA_INSTALL_DIR=$system_dir sh"
}

# after says how to put dir on the PATH when it isn't there, or that another orchestra comes first.
after() {
	case ":$PATH:" in
	*":$dir:"*)
		first="$(command -v orchestra 2>/dev/null || true)"
		if [ -n "$first" ] && [ "$first" != "$dir/orchestra" ]; then
			say ""
			say "$first comes before $dir on your PATH, so 'orchestra' still runs that one: remove it, or"
			say "put $dir before its folder."
		fi
		;;
	*)
		say ""
		say "$dir isn't on your PATH. To add it:"
		case "${SHELL:-}" in
		*/zsh) say "  echo 'export PATH=\"$dir:\$PATH\"' >> ~/.zshrc && exec zsh" ;;
		*/bash)
			if [ "$os" = darwin ]; then
				say "  echo 'export PATH=\"$dir:\$PATH\"' >> ~/.bash_profile && exec bash -l"
			else
				say "  echo 'export PATH=\"$dir:\$PATH\"' >> ~/.bashrc && exec bash"
			fi
			;;
		*/fish) say "  fish_add_path $dir" ;;
		*) say "  echo 'export PATH=\"$dir:\$PATH\"' >> ~/.profile, then log in again" ;;
		esac
		;;
	esac
	say ""
	say "Next: run 'orchestra init' in a project's repository, and 'orchestra update' for later releases."
}

main() {
	keys="$release_keys"
	if [ -n "${ORCHESTRA_DOWNLOAD_URL:-}" ]; then
		releases="$ORCHESTRA_DOWNLOAD_URL"
		keys="${ORCHESTRA_TEST_RELEASE_KEY:-$keys}"
		system_dir="${ORCHESTRA_TEST_SYSTEM_DIR:-$system_dir}"
		say "Test mode: downloading from $releases"
	fi

	platform
	if command -v curl >/dev/null 2>&1; then
		downloader=curl
	elif command -v wget >/dev/null 2>&1; then
		downloader=wget
	else
		fail "neither curl nor wget is here to download the release"
	fi
	command -v tar >/dev/null 2>&1 || fail "tar is needed to unpack the release"

	tag="${ORCHESTRA_VERSION:-}"
	if [ -z "$tag" ]; then
		tag="$(latest)"
	fi
	case "$tag" in
	v*) ;;
	*) tag="v$tag" ;;
	esac
	archive="orchestra_${tag#v}_${os}_${arch}.tar.gz"

	tmp="$(mktemp -d 2>/dev/null || mktemp -d -t orchestra)"
	new=
	trap 'rm -rf "$tmp"; [ -z "$new" ] || rm -f "$new"' EXIT
	trap 'exit 1' HUP INT TERM

	say "Installing orchestra $tag for $os/$arch."
	fetch "$releases/download/$tag/checksums.txt" "$tmp/checksums.txt"
	fetch "$releases/download/$tag/checksums.txt.sig" "$tmp/checksums.txt.sig"
	# Verified before any sum in it is trusted, or the archive downloaded.
	verify_signature
	want="$(awk -v name="$archive" '$2 == name { print $1 }' "$tmp/checksums.txt")"
	[ -n "$want" ] || fail "checksums.txt has no sum for $archive: release $tag has no binary for $os/$arch"
	fetch "$releases/download/$tag/$archive" "$tmp/$archive"
	got="$(sha256 "$tmp/$archive")"
	[ "$got" = "$want" ] || fail "$archive's SHA-256 is $got, not checksums.txt's $want. Nothing was installed."
	mkdir "$tmp/x"
	tar -xzf "$tmp/$archive" -C "$tmp/x" orchestra </dev/null || fail "cannot unpack orchestra from $archive"
	chmod 755 "$tmp/x/orchestra"
	version="$("$tmp/x/orchestra" --version </dev/null 2>&1)" || fail "the new binary doesn't run: $version"
	[ "$version" = "orchestra $tag" ] || fail "the new binary's --version says '$version', not orchestra $tag"

	install_dir
	# Copied beside the old binary and renamed over it, so that 'orchestra' is never half written.
	new="$dir/.orchestra-install-$$"
	cp "$tmp/x/orchestra" "$new" || fail "cannot write in $dir"
	chmod 755 "$new"
	mv -f "$new" "$dir/orchestra" || fail "cannot replace $dir/orchestra"
	new=
	say "Installed $("$dir/orchestra" --version </dev/null) in $dir/orchestra."
	after
}

main "$@"
