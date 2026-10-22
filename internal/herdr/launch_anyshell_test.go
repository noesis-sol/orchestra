package herdr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// A worker launched in a repository whose path holds a quote, into a pane whose shell is nushell, is
// given its arguments as they are: the line typed into the shell quotes nothing, and runs a script
// that /bin/sh reads, whichever shell takes the line.
func TestLaunchInPaneTypesALineNoShellQuotes(t *testing.T) {
	dir := t.TempDir()
	typed := filepath.Join(dir, "typed")
	herdrScript(t, "#!/bin/sh\n"+
		`[ "$1 $2" = "pane run" ] && { printf '%s' "$4" > '`+typed+`'; exit 0; }`+"\n"+
		"echo '"+processInfoJSON(7, 7, "nu")+"'\n")
	t.Setenv("TMPDIR", dir)
	args := []string{"%s|", "--append-system-prompt-file", filepath.Join(dir, "Bob's app", "rules.md"), "two\nlines"}
	if err := (Terminal{}).LaunchInPane(context.Background(), "w2E:p3T", "printf", args); err != nil {
		t.Fatal(err)
	}
	line, err := os.ReadFile(typed)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^ /bin/sh [A-Za-z0-9_./-]+$`).Match(line) {
		t.Fatalf("the typed line %q holds what a shell may quote or expand", line)
	}
	out, err := exec.Command("/bin/sh", "-c", string(line)).Output()
	want := "--append-system-prompt-file|" + args[2] + "|two\nlines|"
	if err != nil || string(out) != want {
		t.Errorf("the line %q ran printf with %q (%v), want %q", line, out, err, want)
	}
}
