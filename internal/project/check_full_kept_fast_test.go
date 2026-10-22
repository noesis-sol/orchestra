package project

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A check-fast.sh init keeps, beside tests created from scratch, is the project's: check-full.sh runs
// it rather than exit 0.
func TestCheckFullRunsTheCheckFastKept(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	kept := "#!/bin/sh\necho the merge check ran\n"
	if err := os.WriteFile(filepath.Join(repo, FastRunner), []byte(kept), 0o755); err != nil {
		t.Fatal(err)
	}
	none := []Suite{}
	c := Choice{Fast: &none, Full: &none} // created from scratch, check-fast.sh kept
	if _, err := ApplyRunners(context.Background(), repo, c); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(repo, FastRunner)); got != kept {
		t.Errorf("check-fast.sh not kept:\n%s", got)
	}
	full := read(t, filepath.Join(repo, FullRunner))
	if full != FullScript(nil, false) {
		t.Errorf("check-full.sh doesn't run the check-fast.sh kept:\n%s", full)
	}
	out, err := exec.CommandContext(t.Context(), "sh", filepath.Join(repo, FullRunner)).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "the merge check ran") {
		t.Errorf("check-full.sh: %v\n%s", err, out)
	}

	// Replaced, check-fast.sh checks nothing, and check-full.sh nothing either.
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, FastRunner), []byte(kept), 0o755); err != nil {
		t.Fatal(err)
	}
	c.ReplaceFast = true
	if _, err := ApplyRunners(context.Background(), other, c); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(other, FullRunner)); got != FullScript(nil, true) {
		t.Errorf("check-full.sh beside a check-fast.sh replaced:\n%s", got)
	}
}

func TestFastChecksNothing(t *testing.T) {
	none, some := []Suite{}, []Suite{{Command: "make test"}}
	only := []Suite{{Command: "scripts/check-full.sh"}} // left out: it runs a runner
	for _, tc := range []struct {
		name string
		fast *[]Suite
		kept bool
		want bool
	}{
		{"unknown", nil, false, false},
		{"none", &none, false, true},
		{"none, one kept", &none, true, false},
		{"a suite", &some, false, false},
		{"only a runner", &only, false, true},
	} {
		if got := FastChecksNothing(tc.fast, tc.kept); got != tc.want {
			t.Errorf("%s: %v; want %v", tc.name, got, tc.want)
		}
	}
}
