package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/project"
)

// A step's label wider than its column, such as a requirement's long name, stays on one line, cut
// with an ellipsis, with the detail after it; a short one is shown whole.
func TestLongStepLabelStaysOnOneLine(t *testing.T) {
	var b strings.Builder
	InitScreen{out: &b, width: 80}.Steps([]project.Step{
		{Kind: project.StepDone, Label: "postgresql-client-tools", Detail: "installed"},
		{Kind: project.StepDone, Label: "requires", Detail: "checked"},
	})
	lines := strings.Split(strings.TrimSuffix(ansi.Strip(b.String()), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("the steps took %d lines; want one each:\n%s", len(lines), ansi.Strip(b.String()))
	}
	if want := "  ✓ postgresql-cl… installed"; lines[0] != want {
		t.Errorf("long label's line = %q; want %q", lines[0], want)
	}
	if want := "  ✓ requires       checked"; lines[1] != want {
		t.Errorf("short label's line = %q; want %q", lines[1], want)
	}
}
