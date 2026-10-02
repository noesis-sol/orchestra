//go:build darwin || linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/term"
)

// The plan and its question go to stdout: with stdout sent to a file, typed in a terminal, the
// question would go unseen, so nothing is asked or filed, as with no terminal at all.
func TestFeatureAsksOnlyWhereTheQuestionIsSeen(t *testing.T) {
	tty, master := openTerminal(t)
	if !term.IsTerminal(int(tty.Fd())) {
		t.Fatal("the pseudo-terminal isn't a terminal")
	}
	// Typed ahead, so that a question asked after all is answered rather than waited on.
	if _, err := master.WriteString("n\n"); err != nil {
		t.Fatal(err)
	}
	dir := featureTools(t, featureScreenOK, `{"type":"result","is_error":false,"structured_output":`+featurePlanJSON+`}`, 0)
	_, stdout, stderr, code := runFeatureFrom(t, tty, "--feature", "Add a --json flag")
	if code != 2 || !strings.Contains(stderr, "orchestra has no terminal to ask on: nothing was filed.") {
		t.Errorf("exit %d, stderr:\n%s", code, stderr)
	}
	if strings.Contains(stdout, "[y/N]") || !strings.Contains(stdout, "Epic: JSON output") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "bd-n")); !os.IsNotExist(err) {
		t.Errorf("bd filed something: %v", err)
	}
}
