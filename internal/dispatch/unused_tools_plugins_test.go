package dispatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
)

// A project with no skills of its own but a plugin its committed Claude settings enable keeps Skill,
// through which its workers reach the plugin's skills; one whose plugins are all disabled, or that has
// neither, has Skill denied.
func TestWorkersKeepSkillForTheProjectsPlugins(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, settings, want, says string
	}{
		{"a plugin enabled", `{"enabledPlugins": {"formatter@tools": true}}`, deniedWithoutSkill,
			"(the project's skills kept)"},
		{"plugins disabled", `{"enabledPlugins": {"formatter@tools": false}}`, deniedWithSkill,
			"(Skill and the skill listing, as the project has no skills or plugins)"},
		{"no plugins", `{"permissions": {"allow": ["Bash"]}}`, deniedWithSkill, "as the project has no skills"},
		{"no settings", "", deniedWithSkill, "as the project has no skills"},
		{"unreadable settings", `{"enabledPlugins": `, deniedWithSkill, "as the project has no skills"},
	} {
		synctest.Test(t, func(t *testing.T) {
			h := newTimedHarness(t)
			h.help = &fakeHelp{help: denyingHelp}
			if tc.settings != "" {
				dir := filepath.Join(h.cfg.Repo, ".claude")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(tc.settings), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			h.beads.add("A", "first", 1)
			h.worker("A", finishes("a.txt"))
			if o, code := h.run(); code != ExitOK {
				t.Fatalf("%s: exit %d, final %q\n%s", tc.name, code, o.Final(), h.sink.text())
			}
			starts := h.herdr.argsFor("A")
			if len(starts) != 1 {
				t.Fatalf("%s: A's starts %q; want one", tc.name, starts)
			}
			if got := denied(starts[0]); got != tc.want {
				t.Errorf("%s: the worker started with %q, want %q", tc.name, starts[0], tc.want)
			}
			if !strings.Contains(h.sink.text(), tc.says) {
				t.Errorf("%s: the run didn't say %q:\n%s", tc.name, tc.says, h.sink.text())
			}
		})
	}
}
