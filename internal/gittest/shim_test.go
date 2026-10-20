package gittest

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

// Under Main, git on macOS is the developer tools' git the shim would run, rather than the shim.
func TestMainBypassesMacOSsGitShim(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("the shim is macOS's")
	}
	want, err := command.Output(context.Background(), command.ReadLimit, "", "xcrun", "--find", "git")
	if err != nil {
		t.Skipf("xcrun can't say which git the shim runs: %v", err)
	}
	got, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if got == shim {
		t.Fatalf("git on PATH is still the shim, %s", got)
	}
	if target, err := filepath.EvalSymlinks(got); err != nil || target != strings.TrimSpace(want) {
		t.Errorf("git on PATH is %s, a link to %s (%v), want one to %s", got, target, err, strings.TrimSpace(want))
	}
	if out, err := command.Output(context.Background(), command.ReadLimit, t.TempDir(), "git", "init", "-q"); err != nil {
		t.Errorf("git init through the link: %v\n%s", err, out)
	}
}
