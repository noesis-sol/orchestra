package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// ErrCancelled is AskWork's error when the user cancels the question with Ctrl+C or Esc.
var ErrCancelled = errors.New("cancelled")

// The answers to "What should this run work on?".
const (
	workTickets = "tickets"
	workFeature = "feature"
)

// errNoDescription is the description's error while it is empty.
var errNoDescription = errors.New("describe the feature first, or press Esc to cancel")

// AskWork asks what a run should work on, reading the answers from in and drawing the form on out:
// the current tickets, ready of them ready (-1 when bd can't say), or a new feature, whose
// description it then asks for. nothing is why the run has nothing to run, none ready or every ready
// ticket held back (see dispatch.CheckNothingToRun), or nil when it has something to run. It
// returns the description, trimmed, or "" for the current tickets. Ctrl+C or Esc cancels it with
// ErrCancelled; ctx ending stops it with ctx's error.
func AskWork(
	ctx context.Context, in io.Reader, out io.Writer, ready int, nothing *dispatch.NothingToRun,
) (string, error) {
	choice, description := workTickets, ""
	theme := huh.ThemeCharm()
	describe := &formField{
		Field: huh.NewText().
			Title("Describe the feature").
			Description("What it should do, and why. claude then interviews you about it and files the tickets " +
				"you agree on; orchestra runs them once you confirm. Alt+Enter or Ctrl+J starts a new line.").
			Lines(6).
			Validate(func(v string) error {
				if strings.TrimSpace(v) == "" {
					return errNoDescription
				}
				return nil
			}).
			Value(&description),
		gap:    theme.FieldSeparator.Render(),
		hidden: func() bool { return choice != workFeature },
	}
	describe.wasHidden = describe.isHidden()
	theme.FieldSeparator = lipgloss.NewStyle() // the description draws its own
	keys := huh.NewDefaultKeyMap()
	keys.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"))
	form := huh.NewForm(huh.NewGroup(
		newBoundedSelect(huh.NewSelect[string]().
			Title("What should this run work on?").
			Description("orchestra --tickets runs the current tickets without asking."),
			&choice,
			huh.NewOption(ticketsOption(ready, nothing), workTickets),
			huh.NewOption("New feature: describe it, talk it through with claude, run its tickets", workFeature),
		),
		describe,
	)).WithTheme(theme).WithKeyMap(keys)
	var err error
	if os.Getenv("TERM") == "dumb" { // huh.NewForm's test for its accessible form, which RunWithContext runs
		err = form.WithInput(in).WithOutput(out).RunWithContext(ctx)
	} else {
		// Run here rather than by huh's RunWithContext, which has Esc and Ctrl+C send tea.Interrupt:
		// on it Bubble Tea closes the terminal's input without waiting for its goroutine still reading
		// it, a data race. On tea.Quit it waits; form.State then tells a cancelled form from a
		// submitted one.
		form.SubmitCmd, form.CancelCmd = tea.Quit, tea.Quit
		// orchestra watches the stop signals itself and ends ctx on one; Bubble Tea's handler would
		// take SIGTERM for a submitted form.
		_, err = tea.NewProgram(form, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out),
			tea.WithoutSignalHandler()).Run()
	}
	switch {
	case ctx.Err() != nil:
		return "", ctx.Err()
	case form.State == huh.StateAborted || errors.Is(err, huh.ErrUserAborted):
		return "", ErrCancelled
	case err != nil:
		return "", err
	case choice != workFeature:
		return "", nil
	}
	return strings.TrimSpace(description), nil
}

// ticketsOption is the current tickets' option, as AskWork takes ready and nothing: how many are
// ready, or why none is.
func ticketsOption(ready int, nothing *dispatch.NothingToRun) string {
	switch {
	case ready < 0:
		return "Current tickets"
	case nothing != nil && nothing.AllDone:
		return "Current tickets: none, all done"
	case nothing != nil:
		return fmt.Sprintf("Current tickets: none ready (%d open)", nothing.Open())
	}
	return fmt.Sprintf("Current tickets: %d ready", ready)
}
