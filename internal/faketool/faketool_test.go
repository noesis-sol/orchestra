//go:build unix

package faketool

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

func TestMain(m *testing.M) { os.Exit(Main(m).Run()) }

// Two tests' fakes are the one dispatcher, each running its own script as if it were the tool:
// with its arguments, its input, its path as $0 and its exit status.
func TestFakesShareTheDispatcherAndRunTheirOwnScripts(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	bd := Write(t, a, "bd", `printf '%s|' "$0" "$@"; cat; exit 3`)
	herdr := Write(t, b, "herdr", "echo herdr")
	same, err := sameFile(bd, herdr)
	if err != nil || !same {
		t.Errorf("bd and herdr are different files (%v)", err)
	}

	out, err := command.OutputWithInput(context.Background(), command.ReadLimit, a, nil, "input", bd, "show", "k 1")
	var exit *exec.ExitError
	if want := bd + "|show|k 1|input"; out != want || !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Errorf("bd: %q, %v; want %q and exit status 3", out, err, want)
	}
	if out, err := command.Output(context.Background(), command.ReadLimit, b, herdr); out != "herdr\n" || err != nil {
		t.Errorf("herdr: %q, %v", out, err)
	}
}

// A fake installs another as the package's comment says: by linking it and copying its script.
func TestAFakeInstallsAnother(t *testing.T) {
	d, home := t.TempDir(), t.TempDir()
	Write(t, d, "bd.fake", "echo installed")
	brew := Write(t, d, "brew", `d=$(dirname "$0")
mkdir -p "$HOME/bin" && cp "$d/bd.fake.sh" "$HOME/bin/bd.sh" && ln "$d/bd.fake" "$HOME/bin/bd"`)
	if _, err := command.OutputWithInput(context.Background(), command.ReadLimit, d, []string{"HOME=" + home}, "",
		brew, "install", "beads"); err != nil {
		t.Fatal(err)
	}
	bd := filepath.Join(home, "bin", "bd")
	if out, err := command.Output(context.Background(), command.ReadLimit, d, bd); out != "installed\n" || err != nil {
		t.Errorf("the installed bd: %q, %v", out, err)
	}
}

// No fake can write over the dispatcher, and so over every other fake.
func TestTheDispatcherIsReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes read-only files")
	}
	bin := Write(t, t.TempDir(), "claude", "exit 0")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho changed\n"), 0o755); err == nil {
		t.Error("wrote over the dispatcher")
	}
}

func sameFile(a, b string) (bool, error) {
	fa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(fa, fb), nil
}
