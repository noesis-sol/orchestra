package project

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// updateGolden rewrites the instructions' renderings in testdata/ from spec-prompt.md, for a change to
// them that is meant: go test ./internal/project -run TestSpecModesRenderAsTheirGoldenFiles -update-golden.
var updateGolden = flag.Bool("update-golden", false, "rewrite the spec breakdown's renderings in testdata/")

// Each spec mode renders as its file in testdata/spec-prompt/ has it, word for word: a change to one
// mode's sections leaves the others as they are.
func TestSpecModesRenderAsTheirGoldenFiles(t *testing.T) {
	for _, tc := range []struct {
		golden string
		write  func(repo string) (string, error)
	}{
		{"headless.md", func(repo string) (string, error) { return WriteSpecPrompt(repo, false) }},
		{"headless-update.md", func(repo string) (string, error) { return WriteSpecPrompt(repo, true) }},
		{"refine.md", func(repo string) (string, error) { return WriteRefinePrompt(repo, false) }},
		{"refine-update.md", func(repo string) (string, error) { return WriteRefinePrompt(repo, true) }},
		{"terminal.md", func(repo string) (string, error) { return WriteTerminalRefine(repo, false) }},
		{"terminal-update.md", func(repo string) (string, error) { return WriteTerminalRefine(repo, true) }},
	} {
		t.Run(tc.golden, func(t *testing.T) {
			p, err := tc.write(runFolder(t))
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", "spec-prompt", tc.golden)
			if *updateGolden {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Errorf("the instructions differ from %s (rewrite it with -update-golden if that is meant):\n%s",
					golden, got)
			}
		})
	}
}
