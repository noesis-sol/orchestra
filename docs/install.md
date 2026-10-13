# Installing and updating

How orchestra is installed, updated and checked, in full. Back to the [README](../README.md#install).

## The install script

```
curl -fsSL https://raw.githubusercontent.com/noesis-sol/orchestra/main/scripts/install.sh | sh
```

[The script](../scripts/install.sh) (read it first if you like) installs the latest [GitHub Release](https://github.com/noesis-sol/orchestra/releases)'s binary for your platform, darwin or linux, amd64 or arm64. It checks the archive's SHA-256 against the release's `checksums.txt` and, where an OpenSSL 3 is installed (`openssl` on the `PATH`, or Homebrew's), the signature of `checksums.txt` with the release keys, as `orchestra update` does; macOS's own `openssl` is LibreSSL, which can't verify it, so there it says the signature wasn't checked and goes on. A sum or signature that doesn't match installs nothing.

It installs in `/usr/local/bin` where it can write there, and otherwise in `~/.local/bin`, saying how to add that to your `PATH`; it never calls `sudo`. Two variables change what it does:

- `ORCHESTRA_VERSION=v0.6.0` installs that release instead of the latest.
- `ORCHESTRA_INSTALL_DIR=<folder>` installs there. For root's `/usr/local/bin`: `curl -fsSL … | sudo ORCHESTRA_INSTALL_DIR=/usr/local/bin sh`.

## With Go, or from a clone

```
go install github.com/noesis-sol/orchestra/cmd/orchestra@latest
```

Or build it from a clone, on the commit you want, and install it over the one on your `PATH`:

```
go build -o /tmp/orchestra ./cmd/orchestra && /tmp/orchestra --version
sudo install -m 755 /tmp/orchestra "$(command -v orchestra)"
```

The build runs as you; only the install needs `sudo`, and only where the folder belongs to root, as `/usr/local/bin` usually does. For a first install, name the folder: `/usr/local/bin/orchestra`. Install between runs.

`go install` and a build from a clone need Go 1.26.

## Updating

`orchestra update` replaces orchestra with the latest [GitHub Release](https://github.com/noesis-sol/orchestra/releases)'s binary for your platform (darwin or linux, amd64 or arm64):

1. It verifies the release's `checksums.txt` with its signature, `checksums.txt.sig`, against the release keys built into orchestra, and installs nothing from a release without a valid one (there is no override).
2. It checks the archive's SHA-256 against `checksums.txt`, and that the new binary's `--version` names the release, before putting it in place.
3. It prints the old and new versions and the release's page.

`orchestra update --check` only says whether a newer release is out, and changes nothing: it exits 1 when there is one, 0 when not. A build from a clone (`dev`, or a version naming a commit, [below](#which-version-you-have)) is replaced only with `--force`.

Where the binary's folder belongs to root, run `sudo orchestra update`; orchestra never calls `sudo` itself. It refuses while a run holds the repository's lock, as workers' hooks call the binary mid-run; runs in other repositories aren't checked, so update between runs.

## Build provenance

The signature shows a release was made by orchestra's release workflow, which alone holds the signing key. As a second check, by hand, each archive carries the workflow's build provenance, which the [GitHub CLI](https://cli.github.com/) verifies for an archive you downloaded:

```
gh attestation verify orchestra_X.Y.Z_darwin_arm64.tar.gz --repo noesis-sol/orchestra
```

## The newer-release notice

A run on a release build ends, on a terminal, with `orchestra vX.Y.Z is available (you have vA.B.C): orchestra update` when a newer release is out. It asks GitHub in the background as it starts, at most once a day, keeping the answer in `orchestra/latest-release.json` in your user cache folder (`~/Library/Caches` on macOS, `$XDG_CACHE_HOME` or `~/.cache` on Linux), and a slow or failed check is dropped without delaying or failing the run. The log and the event stream don't have the line, and builds from a clone never check. `ORCHESTRA_NO_UPDATE_CHECK=1` turns the check off: no request, no cache.

## Which version you have

`orchestra --version` (or `-v`) shows which version you have. A build from a clone names its commit: `v0.4.1-0.20261005062236-d70b642867ad` is commit `d70b642867ad`, which `git rev-parse --short=12 HEAD` prints in the clone, and a version ending in `+dirty` was built with uncommitted changes.

## What orchestra needs

`herdr`, `git`, `claude` and `bd`; `orchestra init` can install `bd` ([docs/setup.md](setup.md)).
