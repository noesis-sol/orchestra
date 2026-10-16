package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WriteSpecPrompt writes the breakdown's instructions to .orchestra/run/, over an earlier
// breakdown's; they hold the rules the headless breakdown follows.
func TestWriteSpecPromptWritesTheInstructions(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, Dir, RunName), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(repo, RunPath("spec-prompt.md"))
	if err := os.WriteFile(p, []byte("an older version"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := WriteSpecPrompt(repo, false)
	if err != nil || got != p {
		t.Fatalf("WriteSpecPrompt: %q, %v; want %q", got, err, p)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(string(b)), " ") // as read, whatever the lines' ends
	for _, want := range []string{
		"# Spec breakdown",
		"never ask one", // the spec is final
		"the most conservative reading consistent with it", // where it leaves a choice open
		"under the heading `Assumptions`",                  // in the root epic's notes
		"--set-metadata spec_sha256=<sha256>",              // the root epic's, set last
		"carries the metadata `spec`",                      // every issue's
		"label `solo` and priority 0",                      // the scaffold of a project with no code
		"`check_fast`", "`check_full`",
		"Don't file a ticket that an open one already covers",
		"never put a file name in text that many tickets share",
		"<<'EOF'",
		"`root`", "`assumptions`", "`problem`",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the instructions lack %q", want)
		}
	}
}
