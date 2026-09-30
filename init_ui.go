package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

// initUI prints 'orchestra init' in colour on a terminal, and plainly otherwise (Lip Gloss drops
// the colours when the output isn't a terminal).
type initUI struct {
	out   io.Writer
	width int
}

func newInitUI(out io.Writer) initUI {
	w := 80
	if f, ok := out.(interface{ Fd() uintptr }); ok {
		if tw, _, err := term.GetSize(int(f.Fd())); err == nil && tw > 0 {
			w = min(tw, 100)
		}
	}
	return initUI{out: out, width: w}
}

var (
	initLabel = lipgloss.NewStyle().Bold(true).Width(15)
	nextBox   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#7D56F4")).Padding(0, 1)
	nextTitle = lipgloss.NewStyle().Bold(true).Foreground(purple)
	command   = lipgloss.NewStyle().Bold(true).Foreground(cyan)
)

func (u initUI) header(repo string) {
	fmt.Fprintln(u.out)
	fmt.Fprintln(u.out, titleStyle.Render("Orchestra")+" "+organStyle.Render("init")+"  "+dimStyle.Render(tildify(repo)))
	fmt.Fprintln(u.out, dimStyle.Render(" Sets this project up for orchestra, in .orchestra/."))
	fmt.Fprintln(u.out)
}

func (u initUI) cancelled() {
	fmt.Fprintln(u.out, deferredStyle.Render("  Cancelled; nothing was changed."))
}

// steps prints each step: ✓ done, • already there, ! done with something to watch.
func (u initUI) steps(steps []step) {
	for _, s := range steps {
		mark := map[stepKind]string{
			stepDone:    closedStyle.Render("✓"),
			stepKept:    dimStyle.Render("•"),
			stepMissing: stopStyle.Render("✗"),
			stepCaution: deferredStyle.Render("!"),
		}[s.kind]
		detail := s.detail
		if s.kind == stepCaution {
			detail = deferredStyle.Render(detail)
		} else {
			detail = dimStyle.Render(detail)
		}
		// Wrap the detail under itself, past the mark and label.
		indent := 2 + 2 + 15
		lines := wrapLines(ansi.Strip(detail), max(u.width-indent, 20), 6)
		for i, l := range lines {
			if s.kind == stepCaution {
				lines[i] = deferredStyle.Render(l)
			} else {
				lines[i] = dimStyle.Render(l)
			}
		}
		first := ""
		if len(lines) > 0 {
			first = lines[0]
		}
		fmt.Fprintf(u.out, "  %s %s%s\n", mark, initLabel.Render(s.label), first)
		for _, l := range lines[min(1, len(lines)):] {
			fmt.Fprintf(u.out, "%s%s\n", strings.Repeat(" ", indent), l)
		}
	}
}

// prerequisites prints what orchestra needs on one line, then a line per missing one.
func (u initUI) prerequisites(pre []step) {
	var marks []string
	var missing []step
	for _, p := range pre {
		if p.kind == stepMissing {
			marks = append(marks, stopStyle.Render("✗ "+p.label))
			missing = append(missing, p)
		} else {
			marks = append(marks, closedStyle.Render("✓ ")+p.label)
		}
	}
	fmt.Fprintf(u.out, "  %s %s%s\n", closedStyle.Render("✓"), initLabel.Render("needs"), strings.Join(marks, dimStyle.Render("  ·  ")))
	for _, p := range missing {
		fmt.Fprintf(u.out, "%s%s\n", strings.Repeat(" ", 19), stopStyle.Render(p.label+": "+p.detail))
	}
}

// next prints what is left for the user, numbered, in a box.
func (u initUI) next(items []string) {
	var b strings.Builder
	b.WriteString(nextTitle.Render("Next") + "\n")
	for i, it := range items {
		text, cmd, hasCmd := strings.Cut(it, "\n")
		fmt.Fprintf(&b, "%s %s", dimStyle.Render(fmt.Sprintf("%d.", i+1)), text)
		if hasCmd {
			fmt.Fprintf(&b, "\n   %s", command.Render(cmd))
		}
		if i < len(items)-1 {
			b.WriteString("\n")
		}
	}
	fmt.Fprintln(u.out)
	fmt.Fprintln(u.out, nextBox.Width(u.width-2).Render(b.String()))
}

// signOff closes init: ready, or almost.
func (u initUI) signOff(ready bool) {
	if ready {
		fmt.Fprintln(u.out, organStyle.Render(" ♪ The orchestra is ready."))
	} else {
		fmt.Fprintln(u.out, deferredStyle.Render(" ♪ Almost ready: fix what's missing above."))
	}
	fmt.Fprintln(u.out)
}

// concurrencyOptions are the choices offered for tickets at the same time.
func concurrencyOptions(current int) []huh.Option[int] {
	notes := map[int]string{
		1: "one at a time (safest)",
		2: "checks must cope with running side by side",
		3: "checks must cope with running side by side",
		4: "a busy machine",
		6: "a big machine, and tickets that rarely touch the same files",
		8: "a big machine, and tickets that rarely touch the same files",
	}
	values := []int{1, 2, 3, 4, 6, 8}
	if _, ok := notes[current]; !ok && current > 0 {
		values = append(values, current)
		notes[current] = "the current setting"
	}
	var opts []huh.Option[int]
	for _, v := range values {
		opts = append(opts, huh.NewOption(fmt.Sprintf("%d  · %s", v, notes[v]), v))
	}
	return opts
}

// askInit asks for what the flags didn't give, starting from the current choice.
func askInit(c *initChoice, askCheck, askConcurrent bool) error {
	var fields []huh.Field
	if askCheck {
		fields = append(fields, huh.NewInput().
			Title("Check command").
			Description("Lint, build and tests. Workers run it before they close a ticket, and orchestra runs "+
				"it again on a ticket rebased onto work merged meanwhile. Empty for none.").
			Placeholder("e.g. make check").
			Value(&c.Check))
	}
	if askConcurrent {
		fields = append(fields, huh.NewSelect[int]().
			Title("Tickets at the same time").
			Description("Each gets its own worker, worktree and checks. A run can override it with --concurrent.").
			Options(concurrencyOptions(c.Concurrent)...).
			Value(&c.Concurrent))
	}
	if len(fields) == 0 {
		return nil
	}
	before := c.Check
	if err := huh.NewForm(huh.NewGroup(fields...)).WithTheme(huh.ThemeCharm()).Run(); err != nil {
		return err
	}
	c.Check = strings.TrimSpace(c.Check)
	if askCheck && c.Check != before {
		c.checkFrom = "the form"
	}
	if askConcurrent {
		c.unasked = false
	}
	return nil
}
