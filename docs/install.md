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

Both need Go 1.26:

```
go install github.com/noesis-sol/orchestra/cmd/orchestra@latest
```

Or, in a clone on the commit you want:

```
go build -o /tmp/orchestra ./cmd/orchestra && /tmp/orchestra --version
sudo install -m 755 /tmp/orchestra "$(command -v orchestra)"
```

`sudo` only where the folder is root's, as `/usr/local/bin` usually is.

## Updating

`orchestra update` installs the latest release for your platform after verifying its signed checksums; without a valid signature it installs nothing. `--check` only says whether one is out (exit 1 if so, 0 if not). Where the folder is root's, run `sudo orchestra update`: orchestra never calls `sudo`. A build from a clone is replaced only with `--force`. A run going keeps its build; the next starts the new one.

To check a release archive's build provenance with the [GitHub CLI](https://cli.github.com/):

```
gh attestation verify orchestra_X.Y.Z_darwin_arm64.tar.gz --repo noesis-sol/orchestra
```

A release build checks GitHub at most once a day, in the background, and says when a newer release is out. `ORCHESTRA_NO_UPDATE_CHECK=1` turns that off.

## Which version you have

`orchestra --version`. A build from a clone ends with its commit's 12-character hash; `+dirty` means uncommitted changes.

## What orchestra needs

`herdr`, `tmux` or `cmux`, `git`, `claude` and `bd`; `orchestra init` can install `bd` ([docs/setup.md](setup.md)).

Workers run in [Herdr](https://herdr.dev) tabs when `orchestra` runs in a Herdr pane, in [cmux](https://cmux.com) workspaces when it runs in a cmux terminal (macOS: `brew install --cask cmux`), and otherwise in tmux windows ([Herdr, tmux or cmux](running.md#herdr-tmux-or-cmux)). Without Herdr or cmux, install tmux, 3.0 or later: `brew install tmux` on macOS, `sudo apt install tmux` on Debian and Ubuntu, or your system's package manager. An older tmux is a setup problem.
