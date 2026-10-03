package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
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
// description it then asks for. It returns the description, trimmed, or "" for the current
// tickets. Ctrl+C or Esc cancels it with ErrCancelled; ctx ending stops it with ctx's error.
func AskWork(ctx context.Context, in io.Reader, out io.Writer, ready int) (string, error) {
	choice, description := workTickets, ""
	tickets := "Current tickets"
	if ready >= 0 {
		tickets = fmt.Sprintf("Current tickets: %d ready", ready)
	}
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
		huh.NewSelect[string]().
			Title("What should this run work on?").
			Description("orchestra --tickets runs the current tickets without asking.").
			Options(
				huh.NewOption(tickets, workTickets),
				huh.NewOption("New feature: describe it, talk it through with claude, run its tickets", workFeature),
			).
			Value(&choice),
		describe,
	)).WithTheme(theme).WithKeyMap(keys).
		// orchestra watches the stop signals itself and ends ctx on one; Bubble Tea's handler would
		// take SIGTERM for a submitted form. Before the input and output: it replaces the options.
		WithProgramOptions(tea.WithoutSignalHandler()).
		WithInput(in).WithOutput(out)
	err := form.RunWithContext(ctx)
	switch {
	case ctx.Err() != nil:
		return "", ctx.Err()
	case errors.Is(err, huh.ErrUserAborted):
		return "", ErrCancelled
	case err != nil:
		return "", err
	case choice != workFeature:
		return "", nil
	}
	return strings.TrimSpace(description), nil
}
