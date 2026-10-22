package cmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// In a repository whose path holds a quote, the line typed into the shell holds no quote, which the
// surface's shell would read by its own rules, nor a backslash, which cmux can't type, and runs the
// launch script, which /bin/sh reads.
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
	if strings.ContainsAny(line, `\'"`) {
		t.Errorf("the typed line %q holds a quote or a backslash", line)
	}
	// The shell, given the line, runs the script, which writes its process ID beside the book.
	script, err := os.ReadFile(strings.TrimPrefix(line, " /bin/sh "))
	if err != nil {
		t.Fatal(err)
	}
	fake := strings.Replace(string(script), "exec 'claude'", "echo", 1)
	if err := os.WriteFile(strings.TrimPrefix(line, " /bin/sh "), []byte(fake), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("/bin/sh", "-c", line).Output()
	if err != nil || string(out) != strings.Join(args, " ")+"\n" {
		t.Errorf("the shell, given %q, printed %q (%v), want the script run", line, out, err)
	}
	if _, _, ok := term.launched(workSF); !ok {
		t.Error("the script wrote no process ID")
	}
}
