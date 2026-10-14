package cmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// In a repository whose path holds a quote, the line typed into the shell holds no backslash, which
// cmux can't type, and runs the launch script.
func TestLaunchInARepositoryWhosePathHoldsAQuote(t *testing.T) {
	f := newFakeCmux(t)
	repo := filepath.Join(t.TempDir(), "Bob's app")
	term := Terminal{Book: filepath.Join(repo, ".orchestra", "run", BookName), Own: ownWS}
	keep(t, term, book{Workspaces: map[string]workspaceEntry{workWS: {Label: "x-1"}},
		Surfaces: map[string]surfaceEntry{workSF: {Workspace: workWS, Dir: repo}}})
	args := []string{"--settings", filepath.Join(repo, ".orchestra", "run", "hooks.json")}
	if err := term.LaunchInPane(bg, workSF, "claude", args); err != nil {
		t.Fatal(err)
	}
	writes := f.writes()
	if len(writes) == 0 || writes[0][0] != "send" {
		t.Fatalf("cmux got %q, want a line typed", writes)
	}
	line := writes[0][len(writes[0])-1]
	if strings.Contains(line, `\`) {
		t.Errorf("the typed line %q holds a backslash", line)
	}
	// The shell, given the line, runs the script at its path.
	if err := os.WriteFile(term.launchPath(workSF, ".sh"), []byte("echo ran\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("/bin/sh", "-c", line).Output()
	if err != nil || string(out) != "ran\n" {
		t.Errorf("the shell, given %q, printed %q (%v), want the script run", line, out, err)
	}
}
