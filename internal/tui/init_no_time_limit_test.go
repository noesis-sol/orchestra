package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/noesis-sol/orchestra/internal/project"
)

// The Checks stage asks no time limit: init writes the defaults to settings.json, edited there.
func TestInitFormAsksNoTimeLimit(t *testing.T) {
	t.Setenv("TERM", "dumb")
	c := project.Choice{Concurrent: 1, SetupOffer: "npm ci"}
	var out strings.Builder
	// The choice of checks (3, Manual), the check command and the setup command offered.
	ask := Ask{Questions: project.Questions{Check: true, Setup: true}}
	if err := AskInit(t.Context(), typed("3", "make check", ""), &out, &c, ask); err != nil {
		t.Fatal(err)
	}
	if screen := ansi.Strip(out.String()); strings.Contains(screen, "time limit") || !strings.Contains(screen, "Setup") {
		t.Errorf("want the setup command asked and no time limit:\n%s", screen)
	}
	if c.CheckFastTimeout != "" || c.CheckFullTimeout != "" {
		t.Errorf("time limits %q, %q; want them left to the settings", c.CheckFastTimeout, c.CheckFullTimeout)
	}
}
