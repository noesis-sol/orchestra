package tui

import (
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/project"
)

// On a terminal shorter than a stage, the form scrolls the stage's questions under its stepper,
// which stays on the form's first line.

// shortTerminalForm is the init form for ask as AskInit builds it, with stage 2's checks (nil when not
// asked).
func shortTerminalForm(t *testing.T, c *project.Choice, ask Ask) (initModel, *checksStage) {
	t.Helper()
	workers, _ := workersStage(c, ask)
	checks := &initStage{name: "Checks"}
	stage2 := addChecks(t.Context(), checks, c, ask, false)
	t.Cleanup(stage2.scout.wait)
	checksStageFields(checks, c, ask, stage2, stage2.scout.poll)
	form, _ := buildForm([]*initStage{workers, checks}, strings.NewReader(""), io.Discard)
	m := initModel{form: form, scout: stage2.scout}
	m.Init()
	return m, stage2
}

// sized is the form once told the terminal is width by height.
func (m initModel) sized(width, height int) initModel {
	model, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return model.(initModel)
}

// fitsUnder checks the form fits a terminal height lines tall, the stepper on its first line, and shows
// focused, the focused field.
func fitsUnder(t *testing.T, m initModel, height int, stepper, focused string) {
	t.Helper()
	view := ansi.Strip(m.View())
	lines := strings.Split(view, "\n")
	if len(lines) > height {
		t.Errorf("the form is %d lines tall on a terminal %d lines tall:\n%s", len(lines), height, view)
	}
	if !strings.Contains(lines[0], stepper) {
		t.Errorf("the first line isn't the stepper %q:\n%s", stepper, view)
	}
	if !strings.Contains(view, focused) {
		t.Errorf("the focused field's %q is out of sight:\n%s", focused, view)
	}
}

func TestTheStepperStaysOnAShortTerminal(t *testing.T) {
	const width, height = 80, 20
	c := project.Choice{Union: true}
	m, stage2 := shortTerminalForm(t, &c, Ask{
		Questions: project.Questions{Concurrent: true, Union: true, Check: true},
		Scout:     finds(fourSuites, nil),
	})
	m = m.sized(width, 12)
	fitsUnder(t, m, 12, workersStep, "┃ Tickets at the same time")

	m.form.NextGroup()
	<-stage2.scout.done
	m = m.sized(width, height) // settles the scout, which shows the suites' checklists
	fitsUnder(t, m, height, checksStep, onFound)
	for _, step := range []struct {
		key     tea.KeyType
		focused string
	}{{tea.KeyDown, onScratch}, {tea.KeyDown, onManual}, {tea.KeyUp, onScratch}, {tea.KeyUp, onFound}} {
		model, _ := m.Update(tea.KeyMsg{Type: step.key})
		m = model.(initModel).sized(width, height) // as Bubble Tea answers the form's tea.WindowSize
		fitsUnder(t, m, height, checksStep, step.focused)
	}
}
