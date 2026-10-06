package organ

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A CLAUDE.md that is blank or only imports AGENTS.md (Claude Code's @AGENTS.md) says nothing
// itself: the plan sees the instructions it stands for, read inside the repository only.
func TestPlanGuideFollowsImports(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		files    map[string]string // under the repository; "" for none
		guide    string
		guideFor string
	}{
		{"blank", map[string]string{"CLAUDE.md": " \n\n", "AGENTS.md": "Run make check.\n"},
			"Run make check.\n", "AGENTS.md"},
		{"empty, no AGENTS.md", map[string]string{"CLAUDE.md": ""}, "", "CLAUDE.md"},
		{"import", map[string]string{"CLAUDE.md": "@AGENTS.md\n", "AGENTS.md": "Run make check.\n"},
			"Run make check.\n", "AGENTS.md"},
		{"imports", map[string]string{"CLAUDE.md": "@./docs/a.md\n@docs/a.md @b.md\n", "docs/a.md": "A.",
			"b.md": "B.", "AGENTS.md": "Agents'"}, "A.\n\nB.", "docs/a.md, b.md"},
		{"import missing", map[string]string{"CLAUDE.md": "@gone.md", "AGENTS.md": "Agents'"}, "Agents'", "AGENTS.md"},
		{"import of itself", map[string]string{"CLAUDE.md": "@CLAUDE.md", "AGENTS.md": "Agents'"},
			"Agents'", "AGENTS.md"},
		{"nothing to read", map[string]string{"CLAUDE.md": "@gone.md"}, "@gone.md", "CLAUDE.md"},
		{"outside", map[string]string{"CLAUDE.md": "@../secret.md @" + outside + " @~/secret.md @link.md",
			"AGENTS.md": "Agents'"}, "Agents'", "AGENTS.md"},
		{"text and an import", map[string]string{"CLAUDE.md": "See @AGENTS.md\n", "AGENTS.md": "Agents'"},
			"See @AGENTS.md\n", "CLAUDE.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(filepath.Dir(outside), "repo") // so ../secret.md is the secret
			if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			if err := os.Symlink(outside, filepath.Join(dir, "link.md")); err != nil {
				t.Fatal(err)
			}
			for name, body := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			ev, err := GatherFeature(context.Background(), dir, "r", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if ev.Guide != tc.guide || ev.GuideName != tc.guideFor {
				t.Errorf("guide %q from %q, want %q from %q", ev.Guide, ev.GuideName, tc.guide, tc.guideFor)
			}
			if in := planInput(ev); !strings.Contains(in, "## Agent instructions ("+tc.guideFor+")") {
				t.Errorf("the input should name %s:\n%s", tc.guideFor, in)
			}
		})
	}
}
