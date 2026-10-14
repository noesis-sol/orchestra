//go:build unix

package main

import (
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A run in the repository doesn't hold orchestra update off: nothing runs the binary during a run, and the
// new one is renamed into place, so the run keeps the build it started with. Run from the repository, as
// orchestra would be, it installs the release.
func TestUpdateGoesOnDuringARun(t *testing.T) {
	repo, _ := gitRepo(t)
	holdLock(t, repo, project.Holder{PID: 44497, Branch: "main"})
	t.Chdir(repo)
	u, _ := releaseServer(t, "v0.6.0", false)
	exe := oldBinary(t)

	code, out, errOut := updateIn(t, "v0.5.1", exe, u)
	if code != 0 || errOut != "" || !strings.Contains(out, "Updated orchestra v0.5.1 -> v0.6.0") {
		t.Errorf("exit %d, stdout %q, stderr %q: want the release installed", code, out, errOut)
	}
	if got := read(t, exe); got != "orchestra v0.6.0\n" {
		t.Errorf("the binary is %q, want the release's", got)
	}
}
