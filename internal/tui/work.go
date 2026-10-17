package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// ErrCancelled is AskWork's error when the user cancels the question with Ctrl+C or Esc, or ends
// its input.
var ErrCancelled = errors.New("cancelled")

// WorkKind is what a run works on, as the answer to "What should this run work on?" gives it.
type WorkKind int

// The answers to "What should this run work on?", in the order it offers them.
const (
	WorkTickets WorkKind = iota // the current tickets
	WorkSpec                    // what a spec file describes, as orchestra --spec runs it
	WorkFeature                 // a new feature, talked through with claude
)

// Work is what a run works on, as AskWork's answer gives it: Kind, and what that kind needs.
type Work struct {
	Kind        WorkKind
	Description string // WorkFeature: the feature's description, trimmed
	Spec        string // WorkSpec: the spec's file, absolute and clean
	Refine      bool   // WorkSpec: refine the spec with claude before it is broken down (as --refine does)
}

// errNoDescription is the description's error while it is empty.
var errNoDescription = errors.New("describe the feature first, or press Esc to cancel")

// AskWork asks what a run in the repository repo (its main checkout) should work on, reading the
// answers from in and drawing the form on out: the current tickets, ready of them ready (-1 when bd
// can't say); a spec, whose file it then asks for, a relative path starting at repo, and whether to
// refine it with claude first; or a new feature, whose description it then asks for. nothing is why
// the run has nothing to run, none ready or every ready ticket held back (see
// dispatch.CheckNothingToRun), or nil when it has something to run. Ctrl+C or Esc cancels it with
// ErrCancelled, as the end of in does in the accessible form (TERM=dumb); ctx ending stops it with
// ctx's error.
func AskWork(
	ctx context.Context, in io.Reader, out io.Writer, repo string, ready int, nothing *dispatch.NothingToRun,
) (Work, error) {
	choice, description, spec, refine := WorkTickets, "", "", false
	theme := huh.ThemeCharm()
	gap := theme.FieldSeparator.Render()
	describe := &formField{
		Field: huh.NewText().
			Title("Describe the feature").
			Description("What it should do, and why. claude then interviews you about it and files the tickets " +
				"you agree on; orchestra then runs them. Alt+Enter or Ctrl+J starts a new line.").
			Lines(6).
			Validate(func(v string) error {
				if strings.TrimSpace(v) == "" {
					return errNoDescription
				}
				return nil
			}).
			Value(&description),
		gap:    gap,
		hidden: func() bool { return choice != WorkFeature },
	}
	describe.wasHidden = describe.isHidden()
	specFields := specQuestions(repo, &spec, &refine, gap, func() bool { return choice != WorkSpec })
	theme.FieldSeparator = lipgloss.NewStyle() // the fields below the choice draw their own
	keys := huh.NewDefaultKeyMap()
	keys.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"))
	fields := slices.Concat([]huh.Field{
		newBoundedSelect(huh.NewSelect[WorkKind]().
			Title("What should this run work on?").
			Description("orchestra --tickets runs the current tickets without asking, and orchestra --spec <file> "+
				"a spec."),
			&choice,
			huh.NewOption(ticketsOption(ready, nothing), WorkTickets),
			huh.NewOption("Spec: build what a spec file describes", WorkSpec),
			huh.NewOption("New feature: describe it, talk it through with claude, run its tickets", WorkFeature),
		),
	}, specFields, []huh.Field{describe})
	form := huh.NewForm(huh.NewGroup(fields...)).WithTheme(theme).WithKeyMap(keys)
	var err error
	if os.Getenv("TERM") == "dumb" { // huh.NewForm's test for its accessible form
		// Run here rather than by huh's RunWithContext, which takes the end of input for answers.
		err = runAccessible(ctx, fields, in, out)
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
		return Work{}, ctx.Err()
	case form.State == huh.StateAborted || errors.Is(err, huh.ErrUserAborted):
		return Work{}, ErrCancelled
	case err != nil:
		return Work{}, err
	}
	switch choice {
	case WorkSpec:
		return Work{Kind: WorkSpec, Spec: spec, Refine: refine}, nil
	case WorkFeature:
		return Work{Kind: WorkFeature, Description: strings.TrimSpace(description)}, nil
	}
	return Work{Kind: WorkTickets}, nil
}

// specQuestions are the start question's fields for a spec, each below gap and hidden while hidden
// says so: its file (see specField), a relative path starting at the repository repo, which it keeps
// in spec, resolved; then, drawn once a path naming a spec is entered, whether to refine it with
// claude first, which it keeps in refine. Running it as it is, as --spec does, is selected to start
// with.
func specQuestions(repo string, spec *string, refine *bool, gap string, hidden func() bool) []huh.Field {
	path := newSpecField(repo, spec)
	file := &formField{Field: path, gap: gap, hidden: hidden}
	how := &formField{
		Field: newBoundedSelect(huh.NewSelect[bool]().
			Title("Refine it with claude first?").
			Description("As it is, claude breaks it down without asking, taking the spec as final. Refined, "+
				"claude first asks you about the gaps it leaves (orchestra --spec <file> --refine)."),
			refine,
			huh.NewOption("Run it as it is", false),
			huh.NewOption("Refine it with claude first", true),
		),
		gap:    gap,
		hidden: hidden,
	}
	file.wasHidden, how.wasHidden = hidden(), hidden()
	return []huh.Field{file, &drawnLater{formField: how, when: path.entered}}
}

// drawnLater is a field asked whenever it is shown, but drawn only once when reports true. While it
// isn't drawn yet, huh still counts it among the fields to come: its help line on the field above
// says that Enter goes on, not that it submits the form.
type drawnLater struct {
	*formField
	when  func() bool
	drawn bool // whether it was drawn when last updated
}

// Update updates the field and, when it was just drawn or no longer is, has the form measure itself
// again, as formField's does.
func (f *drawnLater) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := f.formField.Update(msg)
	if drawn := f.when(); drawn != f.drawn {
		f.drawn = drawn
		cmd = tea.Batch(cmd, tea.WindowSize())
	}
	return f, cmd
}

// View draws the field once when says so.
func (f *drawnLater) View() string {
	if !f.when() {
		return ""
	}
	return f.formField.View()
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
