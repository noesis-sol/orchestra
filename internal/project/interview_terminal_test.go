package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The interview's instructions say that orchestra closes the session by itself once the feature is
// filed, so claude doesn't send the user to /exit then. WriteTerminalInterview writes them over for
// a session on orchestra's own terminal, which orchestra can't close: they add that claude tells
// the user /exit hands back. The earlier interview's feature.json was removed already and stays so.
func TestWriteTerminalInterviewAddsHowToHandBack(t *testing.T) {
	repo := t.TempDir()
	pane, err := WriteInterview(repo)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(pane)
	if err != nil {
		t.Fatal(err)
	}
	inPane := string(b)
	if !strings.Contains(inPane, "once your turn is over, it closes\nthis session by itself") ||
		strings.Contains(inPane, "to hand back") {
		t.Errorf("the instructions don't say orchestra closes the session:\n%s", inPane)
	}

	feature := filepath.Join(repo, RunPath(FeatureName))
	if err := os.WriteFile(feature, []byte(`{"epic":"f-1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	terminal, err := WriteTerminalInterview(repo)
	if err != nil || terminal != pane {
		t.Fatalf("the terminal's instructions are at %s, %v; want %s", terminal, err, pane)
	}
	b, err = os.ReadFile(terminal)
	if err != nil {
		t.Fatal(err)
	}
	added, ok := strings.CutPrefix(string(b), inPane)
	if !ok || !strings.HasPrefix(added, "\n## On orchestra's terminal\n\n") ||
		!strings.Contains(added, "Once the feature is filed, tell the user to type `/exit` to hand back\nto orchestra") {
		t.Errorf("the terminal's instructions add:\n%s", added)
	}
	if epic, err := FiledFeature(repo); err != nil || epic != "f-1" {
		t.Errorf("after WriteTerminalInterview, the feature reads %q, %v", epic, err)
	}
}
