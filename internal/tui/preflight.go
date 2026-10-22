package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// An overnight run's pre-flight: its checklist, each check passed, warned of or failed, and the
// questions that offer each check's fix and, while a failed check is left, whether to start anyway.

// CheckVerdict is how a pre-flight check came out.
type CheckVerdict int

// The verdicts, from best to worst.
const (
	CheckPassed CheckVerdict = iota // nothing in the way
	CheckWarned                     // the night would go slower, cost tickets or leave something for the morning
	CheckFailed                     // the run would stop, or every ticket's merge fail, in the night
)

// PreflightCheck is one line of an overnight run's checklist.
type PreflightCheck struct {
	Verdict CheckVerdict
	Text    string // what the check found, and for a warning or a failure what to do about it by hand
	Fix     string // the question that offers the fix orchestra can make, "Run without a limit?"; "" for none
}

// String is the verdict's word, as plain output and the log say it: passed, warning or failed.
func (v CheckVerdict) String() string { return checkLooks[v].word }

// checkLooks are each verdict's glyph and colour on a terminal, and its word in plain output.
var checkLooks = map[CheckVerdict]struct {
	glyph, word string
	style       lipgloss.Style
}{
	CheckPassed: {"✓", "passed", closedStyle},
	CheckWarned: {"!", "warning", deferredStyle},
	CheckFailed: {"✗", "failed", stopStyle},
}

// Checklist prints an overnight run's checklist: a heading that counts the checks failed and warned
// of, then a line for each check, its verdict's glyph before it on a terminal, its word in plain
// output, after the time, as Say prints it.
func (p Printer) Checklist(checks []PreflightCheck) {
	p.checklist("Overnight pre-flight: "+checklistCount(checks), checks)
}

// QuestionsLeft prints the questions an overnight run's pre-flight leaves open, each said in a line of
// its own, as Checklist prints a warning: a heading that counts them, then their lines.
func (p Printer) QuestionsLeft(questions []string) {
	head := "Overnight pre-flight: 1 question left open"
	if len(questions) != 1 {
		head = fmt.Sprintf("Overnight pre-flight: %d questions left open", len(questions))
	}
	checks := make([]PreflightCheck, len(questions))
	for i, q := range questions {
		checks[i] = PreflightCheck{Verdict: CheckWarned, Text: q}
	}
	p.checklist(head, checks)
}

// checklist prints head, then a line for each check, as Checklist has them.
func (p Printer) checklist(head string, checks []PreflightCheck) {
	if !p.Styled {
		p.sayPlain(head)
		for _, c := range checks {
			p.sayPlain("  " + c.Verdict.String() + ": " + oneLine(Printable(c.Text)))
		}
		return
	}
	var lines strings.Builder
	lines.WriteString(organStyle.Render("◆ ") + sayStyle.Render(head) + "\n")
	for _, c := range checks {
		l := checkLooks[c.Verdict]
		lines.WriteString("  " + l.style.Render(l.glyph) + " " + oneLine(Printable(c.Text)) + "\n")
	}
	p.print(lines.String())
}

// checklistCount says how the checks came out: "every check passed", or how many failed, were warned
// of and passed, "1 failed, 2 warnings, 3 passed".
func checklistCount(checks []PreflightCheck) string {
	if len(checks) == 0 {
		return "nothing to check"
	}
	count := map[CheckVerdict]int{}
	for _, c := range checks {
		count[c.Verdict]++
	}
	if count[CheckPassed] == len(checks) {
		return "every check passed"
	}
	var parts []string
	for _, v := range []CheckVerdict{CheckFailed, CheckWarned, CheckPassed} {
		if n := count[v]; n > 0 {
			word := v.String()
			if v == CheckWarned && n > 1 {
				word += "s"
			}
			parts = append(parts, fmt.Sprintf("%d %s", n, word))
		}
	}
	return strings.Join(parts, ", ")
}

// PreflightAnswer is what the pre-flight's questions chose: Fix[i] whether to make the fix of check i,
// and Force whether to start the run with failed checks left.
type PreflightAnswer struct {
	Fix   []bool
	Force bool
}

// PreflightAsks reports whether AskPreflight has anything to ask of checks: a fix to offer, or, unless
// forced (--force-overnight), a failed check.
func PreflightAsks(checks []PreflightCheck, forced bool) bool {
	return slices.ContainsFunc(checks, func(c PreflightCheck) bool {
		return c.Fix != "" || c.Verdict == CheckFailed && !forced
	})
}

// AskPreflight asks, of an overnight run's checks, reading the answers from in and drawing the form on
// out, a terminal TERM names termName: for each check with a fix, whether to make it, yes to start
// with; then, while a failed check is left without its fix, whether to start the run anyway, no to
// start with, unless forced (--force-overnight) says so already. Ctrl+C or Esc cancels it with
// ErrCancelled, as the end of in does in the accessible form (TERM=dumb); ctx ending stops it with
// ctx's error. With nothing to ask (see PreflightAsks) it returns at once, every fix unmade.
func AskPreflight(
	ctx context.Context, in io.Reader, out io.Writer, termName string, checks []PreflightCheck, forced bool,
) (PreflightAnswer, error) {
	a := PreflightAnswer{Fix: make([]bool, len(checks)), Force: forced}
	if !PreflightAsks(checks, forced) {
		return a, nil
	}
	theme := formTheme()
	gap := theme.FieldSeparator.Render()
	theme.FieldSeparator = lipgloss.NewStyle() // each field below the first draws its own
	var fields []huh.Field
	for i, c := range checks {
		if c.Fix == "" {
			continue
		}
		a.Fix[i] = true
		fields = append(fields, &formField{Field: huh.NewConfirm().
			Title(oneLine(Printable(c.Fix))).
			Description(oneLine(Printable(c.Text))).
			Affirmative("Yes").
			Negative("No").
			Value(&a.Fix[i])})
	}
	unfixed := func() int {
		n := 0
		for i, c := range checks {
			if c.Verdict == CheckFailed && !a.Fix[i] {
				n++
			}
		}
		return n
	}
	if !forced {
		force := huh.NewConfirm().
			Description("A failed check would stop the run, or every ticket's merge, in the night. " +
				"orchestra --overnight --force-overnight starts without asking.").
			Affirmative("Start anyway").
			Negative("No").
			Value(&a.Force)
		f := &formField{Field: force, hidden: func() bool { return unfixed() == 0 }}
		f.refresh = func() {
			force.Title(fmt.Sprintf("Start the run anyway, with %s?", ChecksFailed(unfixed())))
		}
		f.refresh()
		fields = append(fields, f)
	}
	for _, f := range fields[1:] {
		f.(*formField).gap = gap
	}
	keys := huh.NewDefaultKeyMap()
	keys.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"))
	form := huh.NewForm(huh.NewGroup(fields...)).WithTheme(theme).WithKeyMap(keys)
	var err error
	if termName == "dumb" { // huh's test for its accessible form, which asks with plain lines
		err = runAccessible(ctx, fields, in, out)
	} else {
		// As AskWork runs its form: on tea.Quit, without Bubble Tea's signal handler, quit on the end of ctx.
		form.SubmitCmd, form.CancelCmd = tea.Quit, tea.Quit
		err = runProgram(ctx, form, in, out)
	}
	switch {
	case ctx.Err() != nil:
		return PreflightAnswer{}, ctx.Err()
	case form.State == huh.StateAborted || errors.Is(err, huh.ErrUserAborted):
		return PreflightAnswer{}, ErrCancelled
	case err != nil:
		return PreflightAnswer{}, err
	}
	if unfixed() == 0 { // nothing left to start anyway with
		a.Force = forced
	}
	return a, nil
}

// ChecksFailed is "1 check failed" or "n checks failed".
func ChecksFailed(n int) string {
	if n == 1 {
		return "1 check failed"
	}
	return fmt.Sprintf("%d checks failed", n)
}
