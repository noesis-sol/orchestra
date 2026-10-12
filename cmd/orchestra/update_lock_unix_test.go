//go:build unix

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A run in the repository holds orchestra update off, as its workers' hooks call the binary: nothing is
// read from GitHub and the binary stays. --check, which changes nothing, still answers.
func TestUpdateIsRefusedDuringARun(t *testing.T) {
	repo, _ := gitRepo(t)
	holdLock(t, repo, project.Holder{PID: 44497, Branch: "main"})
	u, requested := releaseServer(t, "v0.6.0", false)
	exe := oldBinary(t)

	code, out, errOut := updateIn(t, repo, "v0.5.1", exe, u)
	resolved, _ := filepath.EvalSymlinks(repo) // git names the repository through any links
	want := "orchestra update: orchestra is already running in " + resolved + " (pid 44497, main). Workers' hooks call " +
		"orchestra while a run goes: update between runs. Runs in other repositories aren't checked.\n"
	if code != 2 || errOut != want || out != "" {
		t.Errorf("exit %d, stdout %q, stderr:\n%s\nwant:\n%s", code, out, errOut, want)
	}
	if got := requested(); len(got) > 0 || read(t, exe) != "the old one\n" {
		t.Errorf("requested %q, binary %q: want it left alone", got, read(t, exe))
	}

	if code, out, _ := updateIn(t, repo, "v0.5.1", exe, u, "--check"); code != 1 || !strings.Contains(out, "the latest release is v0.6.0") {
		t.Errorf("--check: exit %d, stdout %q", code, out)
	}
}
