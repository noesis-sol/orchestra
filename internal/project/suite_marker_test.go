package project

import (
	"strings"
	"testing"
	"time"
)

// A runner says which suite starts, one SuiteMarker line each, so the last one before a failure
// names the suite that failed; a name with a quote in it is printed as it is.
func TestRunnerNamesEachSuiteAsItStarts(t *testing.T) {
	repo := t.TempDir()
	writeRunner(t, repo, FastScript([]Suite{
		{Name: "unit tests", Command: "true"},
		{Name: "it's e2e", Command: "echo failing; exit 2", Serial: true},
		{Command: "touch never"},
	}))
	out, err := runRunner(t, repo, repo, t.TempDir(), 10*time.Second)
	if err == nil {
		t.Fatalf("the runner passed:\n%s", out)
	}
	var said []string
	for line := range strings.Lines(out) {
		if strings.HasPrefix(line, SuiteMarker) {
			said = append(said, strings.TrimSpace(line))
		}
	}
	if want := []string{"SUITE: unit tests", "SUITE: it's e2e"}; strings.Join(said, "\n") != strings.Join(want, "\n") {
		t.Errorf("the runner said %q, want %q; output:\n%s", said, want, out)
	}
}
