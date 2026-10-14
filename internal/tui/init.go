package tui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/mcp"
	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
	"golang.org/x/term"
)

// InitScreen prints 'orchestra init' in colour on a terminal, and plainly otherwise (Lip Gloss drops
// the colours when the output isn't a terminal).
type InitScreen struct {
	out   io.Writer
	width int
}

// NewInitScreen returns the screen for out, as wide as its terminal (at most 100), or 80.
func NewInitScreen(out io.Writer) InitScreen {
	w := 80
	if f, ok := out.(interface{ Fd() uintptr }); ok {
		if tw, _, err := term.GetSize(int(f.Fd())); err == nil && tw > 0 {
			w = min(tw, 100)
		}
	}
	return InitScreen{out: out, width: w}
}

var (
	initLabel    = lipgloss.NewStyle().Bold(true).Width(15)
	nextTitle    = lipgloss.NewStyle().Bold(true).Foreground(purple)
	commandStyle = lipgloss.NewStyle().Bold(true).Foreground(cyan)
	nextBox      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7D56F4")).Padding(0, 1)
)

// Header prints the title and the repository being set up.
func (u InitScreen) Header(repo string) {
	fmt.Fprintln(u.out)
	fmt.Fprintln(u.out, titleStyle.Render("Orchestra")+" "+organStyle.Render("init")+"  "+dimStyle.Render(Tildify(repo)))
	fmt.Fprintln(u.out, dimStyle.Render(" Sets this project up for orchestra, in .orchestra/."))
	fmt.Fprintln(u.out)
}

// Working says what init is doing while it takes a while.
func (u InitScreen) Working(what string) {
	fmt.Fprintln(u.out, dimStyle.Render("  … "+what))
}

// Cancelled says init was cancelled with nothing changed.
func (u InitScreen) Cancelled() {
	fmt.Fprintln(u.out, deferredStyle.Render("  Cancelled; nothing was changed."))
}

// Steps prints each step: ✓ done, • already there, ! done with something to watch.
func (u InitScreen) Steps(steps []project.Step) {
	for _, s := range steps {
		mark := map[project.StepKind]string{
			project.StepDone:    closedStyle.Render("✓"),
			project.StepKept:    dimStyle.Render("•"),
			project.StepMissing: stopStyle.Render("✗"),
			project.StepCaution: deferredStyle.Render("!"),
		}[s.Kind]
		detail := s.Detail
		if s.Kind == project.StepCaution {
			detail = deferredStyle.Render(detail)
		} else {
			detail = dimStyle.Render(detail)
		}
		// Wrap the detail under itself, past the mark and label.
		indent := 2 + 2 + 15
		lines := wrapLines(ansi.Strip(detail), max(u.width-indent, 20), 6)
		for i, l := range lines {
			if s.Kind == project.StepCaution {
				lines[i] = deferredStyle.Render(l)
			} else {
				lines[i] = dimStyle.Render(l)
			}
		}
		first := ""
		if len(lines) > 0 {
			first = lines[0]
		}
		fmt.Fprintf(u.out, "  %s %s%s\n", mark, initLabel.Render(s.Label), first)
		for _, l := range lines[min(1, len(lines)):] {
			fmt.Fprintf(u.out, "%s%s\n", strings.Repeat(" ", indent), l)
		}
	}
}

// Prerequisites prints what orchestra needs on one line, then a line per missing one.
func (u InitScreen) Prerequisites(pre []project.Step) {
	var marks []string
	var missing []project.Step
	lead := closedStyle.Render("✓")
	for _, p := range pre {
		if p.Kind == project.StepMissing {
			marks = append(marks, stopStyle.Render("✗ "+p.Label))
			missing = append(missing, p)
			lead = stopStyle.Render("✗")
		} else {
			marks = append(marks, closedStyle.Render("✓ ")+p.Label)
		}
	}
	fmt.Fprintf(u.out, "  %s %s%s\n", lead, initLabel.Render("needs"), strings.Join(marks, dimStyle.Render("  ·  ")))
	for _, p := range missing {
		fmt.Fprintf(u.out, "%s%s\n", strings.Repeat(" ", 19), stopStyle.Render(p.Label+": "+p.Detail))
	}
}

// Next prints what is left for the user, numbered, in a box.
func (u InitScreen) Next(items []string) {
	var b strings.Builder
	b.WriteString(nextTitle.Render("Next") + "\n")
	for i, it := range items {
		text, cmd, hasCmd := strings.Cut(it, "\n")
		fmt.Fprintf(&b, "%s %s", dimStyle.Render(fmt.Sprintf("%d.", i+1)), text)
		if hasCmd {
			fmt.Fprintf(&b, "\n   %s", commandStyle.Render(cmd))
		}
		if i < len(items)-1 {
			b.WriteString("\n")
		}
	}
	fmt.Fprintln(u.out)
	fmt.Fprintln(u.out, nextBox.Width(u.width-2).Render(b.String()))
}

// SignOff closes init: ready, or almost.
func (u InitScreen) SignOff(ready bool) {
	if ready {
		fmt.Fprintln(u.out, organStyle.Render(" ♪ The orchestra is ready."))
	} else {
		fmt.Fprintln(u.out, deferredStyle.Render(" ♪ Almost ready: fix what's missing above."))
	}
	fmt.Fprintln(u.out)
}

// concurrencyNotes are the notes of the options for tickets at the same time: 1, 2, 3 and 4.
var concurrencyNotes = []string{
	"one at a time (safest)",
	"checks must cope with running side by side",
	"that, and room for three workers and their checks",
	"that, for four: a busy machine",
}

// customConcurrency is the value of the "Custom…" option, whose number is typed in an input. It
// isn't 0: huh scrolls a select's options to the one whose value is the zero value, hiding those
// above it.
const customConcurrency = -1

// concurrencyOptions are the choices offered for tickets at the same time: 1 to 4, and a number
// typed in.
func concurrencyOptions() []huh.Option[int] {
	var opts []huh.Option[int]
	for i, note := range concurrencyNotes {
		opts = append(opts, huh.NewOption(fmt.Sprintf("%d  · %s", i+1, note), i+1))
	}
	custom := fmt.Sprintf("Custom…  · type a number, up to %d", project.MaxConcurrency)
	return append(opts, huh.NewOption(custom, customConcurrency))
}

// concurrencyStart is where the choice of tickets at the same time starts for the current setting:
// on its option, or on "Custom…" with the number typed in when no option offers it.
func concurrencyStart(current int) (option int, custom string) {
	if current > len(concurrencyNotes) {
		return customConcurrency, strconv.Itoa(current)
	}
	return max(current, 1), ""
}

// parseConcurrency reads a typed number of tickets at the same time.
func parseConcurrency(v string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 1 || n > project.MaxConcurrency {
		return 0, fmt.Errorf("must be a whole number from 1 to %d (got '%s')", project.MaxConcurrency, v)
	}
	return n, nil
}

// formField is a field of the init form, which huh v1.0.0 can't hide on its own (it hides whole
// groups). A hidden field is skipped, has no error, draws nothing and isn't asked in huh's accessible
// mode. Each field draws the gap above it, the form's theme drawing none, so a hidden one leaves no
// gap either.
type formField struct {
	huh.Field
	gap       string      // drawn above the field: the theme's field separator, but not above the first
	header    string      // printed above the field in huh's accessible mode, which drops groups' titles
	own       *huh.Theme  // the field's own copy of the form's theme, in which Blur ticks a select's answer
	hidden    func() bool // nil: always shown
	refresh   func()      // nil, or sets the field's title from the answers above it before it is drawn
	top       bool        // drawn with no gap above: shown only while the fields above it are hidden
	wasHidden bool        // whether it was hidden when last updated
}

func (f *formField) isHidden() bool { return f.hidden != nil && f.hidden() }

// Update updates the field and, when it was just shown or hidden, has the form measure itself
// again: huh sizes the form on a window size message only, so it would scroll its top away.
func (f *formField) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := f.Field.Update(msg)
	f.Field = m.(huh.Field)
	if hidden := f.isHidden(); hidden != f.wasHidden {
		f.wasHidden = hidden
		cmd = tea.Batch(cmd, tea.WindowSize())
	}
	return f, cmd
}

// View draws the field below its gap, or nothing when hidden.
func (f *formField) View() string {
	if f.isHidden() {
		return ""
	}
	if f.refresh != nil {
		f.refresh()
	}
	return f.gap + f.Field.View()
}

// answeredSelector marks the answer of a select that was left, in place of huh's cursor.
var answeredSelector = closedStyle.SetString("✓ ")

// Blur leaves the field. A select left shows its answer ticked and green from then on; one not
// reached yet shows no cursor, the form's theme drawing none on a field that isn't focused. huh draws
// every field that isn't focused with the theme's Blurred styles, hence each field's own theme.
func (f *formField) Blur() tea.Cmd {
	if f.own != nil {
		f.own.Blurred.SelectSelector = answeredSelector
		f.own.Blurred.SelectedOption = f.own.Focused.SelectedOption
	}
	return f.Field.Blur()
}

// Skip skips a hidden field.
func (f *formField) Skip() bool { return f.isHidden() || f.Field.Skip() }

// Error is the field's error, none when hidden.
func (f *formField) Error() error {
	if f.isHidden() {
		return nil
	}
	return f.Field.Error()
}

// WithAccessible sets the field's accessible mode and returns the formField: huh's accessible form
// (TERM=dumb) runs each field as WithAccessible returns it, and the field's own would bypass the
// hiding. The field keeps its own value, not what its WithAccessible returns: a wrapper such as
// boundedSelect returns the huh field inside it.
func (f *formField) WithAccessible(accessible bool) huh.Field {
	f.Field.WithAccessible(accessible) //nolint:staticcheck // huh's accessible form still calls it
	return f
}

// RunAccessible asks for the field on w, reading the answer from r, or does nothing when hidden.
// It runs after the fields above it have their answers, so the hiding follows them.
func (f *formField) RunAccessible(w io.Writer, r io.Reader) error {
	if f.isHidden() {
		return nil
	}
	if f.refresh != nil {
		f.refresh()
	}
	if f.header != "" {
		if _, err := fmt.Fprintf(w, "%s\n\n", f.header); err != nil {
			return err
		}
	}
	return f.Field.RunAccessible(w, r)
}

// mcpOptions are the MCP servers offered to workers, each with where it is defined, and the names
// selected to start with: the current setting (nothing on first run). A chosen server this machine
// doesn't define is offered too, so keeping the selection keeps it. claude.ai connectors aren't
// offered: workers can't have them.
func mcpOptions(c project.Choice) (opts []huh.Option[string], selected []string) {
	var current []string
	if c.MCP != nil {
		current = *c.MCP
	}
	for _, s := range c.Servers {
		switch {
		case s.Scope == mcp.ScopeBuiltIn:
			label := s.Name + "  · built into Claude Code, drives Chrome on this machine"
			opts = append(opts, huh.NewOption(label, s.Name))
		case s.Available():
			// The name and type as a config file has them, .mcp.json a teammate's: shown printable.
			label := fmt.Sprintf("%s  · %s scope, %s", oneLine(s.Name), s.Scope, oneLine(s.Type))
			opts = append(opts, huh.NewOption(label, s.Name))
		}
	}
	for _, name := range current {
		if _, ok := mcp.Find(c.Servers, name); !ok {
			missing := "  · not defined on this machine"
			if name == mcp.Chrome {
				missing = "  · Claude in Chrome isn't set up on this machine"
			}
			opts = append(opts, huh.NewOption(oneLine(name)+missing, name)) // settings.json's, committed
		}
		if s, ok := mcp.Find(c.Servers, name); !ok || s.Available() {
			selected = append(selected, name)
		}
	}
	return opts, selected
}

// mcpDescription explains the MCP server choice, naming the claude.ai connectors workers can't have.
func mcpDescription(c project.Choice) string {
	d := "Workers get only the servers chosen here. settings.json keeps " +
		"their names; each machine's Claude Code config defines them."
	if connectors := project.Connectors(c.Servers); len(connectors) > 0 {
		d += "\nNot available to workers (claude.ai connector): " + oneLine(strings.Join(connectors, ", ")) + "."
	}
	return d
}

// installField asks whether to install bd as c.Install says, naming the method and the command.
func installField(c *project.Choice) *huh.Confirm {
	return huh.NewConfirm().
		Title("Install Beads with " + c.Install.Method + "?").
		Description("orchestra works through a Beads backlog, and bd isn't installed. This runs:\n" +
			c.Install.Command).
		Affirmative("Install it").
		Negative("No").
		Value(&c.InstallBeads)
}

// setupField asks for the setup command, naming the lockfiles it was offered for.
func setupField(c *project.Choice) *huh.Input {
	return huh.NewInput().
		Title("Setup command").
		Description("Found " + strings.Join(c.SetupFrom, ", ") + ". orchestra runs this in a ticket's worktree " +
			"before " + project.FastRunner + " whenever its rebase changes a dependency manifest or lockfile, so " +
			"the check doesn't run against the dependencies installed before it. Empty for none.").
		Placeholder(c.SetupOffer).
		Value(&c.Setup)
}

// verifierDescription explains the verifier question, naming the stack the scout found, if any.
func verifierDescription(stack []string) string {
	d := "Writes " + project.VerifierPath + ", a subagent that checks each ticket's change against the ticket " +
		"in a fresh context, seeing the diff but not the reasoning behind it, and has the worker prompt run it " +
		"before a worker closes its ticket. It costs a subagent's run per ticket."
	if len(stack) > 0 {
		d += "\nIt is told the stack: " + Printable(strings.Join(stack, ", ")) + "." // the scout's words
	}
	return d
}

// verifierField asks whether to add the verifier.
func verifierField(c *project.Choice) *huh.Confirm {
	return huh.NewConfirm().
		Title("Add a verification step for workers").
		Description(verifierDescription(c.Stack)).
		Affirmative("Add it").
		Negative("No").
		Value(&c.Verifier)
}

// prefixDescription explains the ticket prefix, saying what changing it does, or why it can't change.
func prefixDescription(c project.Choice) string {
	d := "Ticket IDs read <prefix>-<hash>, e.g. " + c.PrefixWas + "-a1b2: in branches (wt/<id>), worktrees, " +
		"tabs, the dashboard and the log. Lowercase letters, digits and hyphens, starting with a letter."
	switch {
	case c.PrefixNew:
		d += "\nbd init gives the new Beads this one; the folder's name, unless changed."
	case c.PrefixRefusal != "":
		d += "\nIt stays " + c.PrefixWas + " for now: the tickets can't be renamed while " + c.PrefixRefusal + "."
	default:
		d += "\nChanging it renames every ticket, and every mention of one in the tickets, with bd rename-prefix."
	}
	return Printable(d) // bd's and git's words
}

// typedPrefix is the ticket prefix typed: the current one when cleared.
func typedPrefix(c project.Choice) string {
	return cmp.Or(project.NormalPrefix(c.Prefix), c.PrefixWas)
}

// prefixField asks for the ticket prefix, starting from the current one, which it always takes back
// (cleared, it keeps it too).
func prefixField(c *project.Choice) *huh.Input {
	return huh.NewInput().
		Title("Ticket prefix").
		Description(prefixDescription(*c)).
		Placeholder(c.PrefixWas).
		Validate(func(v string) error {
			p := cmp.Or(project.NormalPrefix(v), c.PrefixWas)
			switch {
			case p == c.PrefixWas:
				return nil
			case project.PrefixProblem(p) != "":
				return errors.New("the prefix " + project.PrefixProblem(p))
			case !c.PrefixNew && c.PrefixRefusal != "":
				return errors.New("the tickets can't be renamed now; keep " + c.PrefixWas)
			}
			return nil
		}).
		Value(&c.Prefix)
}

// renameField asks to confirm renaming the tickets to the prefix typed above it, as RenameQuestion
// says: no unless answered yes.
func renameField(c *project.Choice) *formField {
	confirm := huh.NewConfirm().
		Description("bd rename-prefix rewrites every ticket's ID, and every mention of one in the tickets.").
		Affirmative("Rename them").
		Negative("No").
		Value(&c.RenamePrefix)
	f := &formField{Field: confirm}
	f.refresh = func() {
		typed := *c
		typed.Prefix = typedPrefix(*c)
		confirm.Title(Printable(project.RenameQuestion(typed)))
	}
	f.refresh()
	return f
}

// Ask names the questions AskInit asks, those the flags didn't answer, with what they need.
type Ask struct {
	project.Questions
	// For Check: Scout looks for the project's suites once stage 2 is reached (with none, the choice
	// starts on Manual); Runners are the runners as they are (PlanRunners of a choice that keeps them)
	// and Skill the create-check-suite skill as it is, for keep or replace where they differ.
	// CheckScript is set when the project has scripts/check.sh: the suites offered start with it.
	Scout       func(context.Context) (organ.Scouting, error)
	Runners     []project.Runner
	Skill       project.SkillPlan
	CheckScript bool
}

// initStage is a stage of the init form: a header, then its questions. The form shows one stage at
// a time; Enter on a stage's last question goes on to the next, Shift+Tab on its first goes back.
type initStage struct {
	name   string
	fields []*formField
}

// add adds f to the stage's questions.
func (s *initStage) add(f huh.Field) *formField {
	ff := &formField{Field: f}
	s.fields = append(s.fields, ff)
	return ff
}

// stageHeader is the header of the stage numbered n (from 1) of the total shown in the accessible
// form: "Step 1 of 2 · Workers", or its name alone when it is the only one.
func stageHeader(name string, n, total int) string {
	if total == 1 {
		return name
	}
	return fmt.Sprintf("Step %d of %d · %s", n, total, name)
}

// stepper is the header of the stage numbered n (from 1) of those named on a terminal: each stage,
// ✓ done, ● current or ○ to come, joined by lines that turn green once reached ("✓ Workers ──── ●
// Checks"); or its name alone when it is the only one.
func stepper(names []string, n int) string {
	if len(names) == 1 {
		return organStyle.Render(names[0])
	}
	var b strings.Builder
	for i, name := range names {
		if i > 0 {
			line := dimStyle
			if i < n {
				line = doneStyle
			}
			b.WriteString(line.Render(" ──── "))
		}
		switch {
		case i+1 < n:
			b.WriteString(closedStyle.Render("✓ " + name))
		case i+1 == n:
			b.WriteString(organStyle.Render("● " + name))
		default:
			b.WriteString(dimStyle.Render("○ " + name))
		}
	}
	return b.String()
}

// timeLimit asks for a check's time limit.
func timeLimit(title, description, placeholder string, value *string) *huh.Input {
	return huh.NewInput().
		Title(title).
		Description(description).
		Placeholder(placeholder).
		Validate(func(v string) error {
			_, err := project.ParseCheckTimeout(strings.TrimSpace(v))
			return err
		}).
		Value(value)
}

// AskInit asks the questions ask names, starting from the current choice, reading the answers
// from in and drawing the form on out. It asks them in stages, each stage under its header; a stage
// with no question to ask is left out. It changes nothing but c: Esc or Ctrl+C at any stage returns
// huh.ErrUserAborted, as the end of in does in the accessible form (TERM=dumb), and init then writes
// and installs nothing.
func AskInit(in io.Reader, out io.Writer, c *project.Choice, ask Ask) error {
	workers, checks := &initStage{name: "Workers"}, &initStage{name: "Checks"}
	if ask.Install {
		workers.add(installField(c))
	}
	option, custom := concurrencyStart(c.Concurrent)
	if ask.Concurrent {
		workers.add(newBoundedSelect(huh.NewSelect[int]().
			Title("Tickets at the same time").
			Description("Each gets its own worker, worktree and checks. A run can override it with --concurrent."),
			&option, concurrencyOptions()...))
		typed := workers.add(huh.NewInput().
			Title("Number of tickets at the same time").
			Description(fmt.Sprintf("From 1 to %d. Above %d: a big machine, and tickets that rarely touch "+
				"the same files.", project.MaxConcurrency, len(concurrencyNotes))).
			Placeholder("e.g. 6").
			Validate(func(v string) error {
				_, err := parseConcurrency(v)
				return err
			}).
			Value(&custom))
		typed.hidden = func() bool { return option != customConcurrency }
		typed.wasHidden = typed.isHidden()
	}
	if ask.Union {
		workers.add(huh.NewConfirm().
			Title("Merge CHANGELOG.md by union").
			Description("Adds 'CHANGELOG.md merge=union' to .gitattributes. Tickets running side by side each " +
				"add an entry at the same spot, and git stops the second one's rebase on a conflict; with " +
				"this line it keeps both sides' lines instead.").
			Affirmative("Add it").
			Negative("No").
			Value(&c.Union))
	}
	mcpOpts, servers := mcpOptions(*c)
	if ask.MCP && len(mcpOpts) > 0 {
		// Not filterable: Esc, which ends a filter, cancels the form.
		workers.add(newWrappingMultiSelect(huh.NewMultiSelect[string]().
			Title("MCP servers for workers").
			Description(mcpDescription(*c)).
			Filterable(false),
			&servers, mcpOpts...))
	}
	if ask.MCP && len(mcpOpts) == 0 {
		c.MCP = &[]string{}
	}
	accessible := os.Getenv("TERM") == "dumb" // huh's accessible form, which asks with plain lines
	var stage2 *checksStage
	if ask.Check {
		stage2 = addChecks(checks, c, ask, accessible)
		defer stage2.scout.wait()
	}
	settled := func() bool { return stage2 == nil || stage2.scout.poll() }
	if ask.Timeout {
		if c.CheckFastTimeout == "" {
			c.CheckFastTimeout = project.DefaultCheckTimeoutText
		}
		hide(checks.add(timeLimit("check-fast time limit", "How long orchestra lets "+project.FastRunner+
			" run on a rebased ticket before it stops it and sets the ticket aside; other finished tickets "+
			"wait for it meanwhile. A run can override it with --check-timeout.", "e.g. 5m, 45m",
			&c.CheckFastTimeout)), func() bool { return !settled() })
	}
	if ask.FullTimeout {
		if c.CheckFullTimeout == "" {
			c.CheckFullTimeout = project.DefaultCheckFullTimeoutText
		}
		hide(checks.add(timeLimit("check-full time limit", "How long orchestra lets "+project.FullRunner+
			" run at the end of a run before it stops it. A run can override it with --check-full-timeout.",
			"e.g. 60m, 2h", &c.CheckFullTimeout)), func() bool { return !settled() })
	}
	if ask.Setup {
		if c.Setup == "" {
			c.Setup = c.SetupOffer
		}
		hide(checks.add(setupField(c)), func() bool { return !settled() })
	}
	if ask.Verifier {
		// Last: asked once the scout has said what the project is built with.
		field := verifierField(c)
		if stage2 != nil {
			stage2.verifier = field
		}
		hide(checks.add(field), func() bool { return !settled() })
	}
	if ask.Prefix {
		// Last: its rename asks to confirm what it changes everywhere.
		hide(checks.add(prefixField(c)), func() bool { return !settled() })
		rename := renameField(c)
		checks.fields = append(checks.fields, rename)
		hide(rename, func() bool {
			p := typedPrefix(*c)
			return !settled() || c.PrefixNew || c.PrefixTickets == 0 || p == c.PrefixWas ||
				project.PrefixProblem(p) != ""
		})
	}
	var stages []*initStage
	for _, s := range []*initStage{workers, checks} {
		if len(s.fields) > 0 {
			stages = append(stages, s)
		}
	}
	if len(stages) == 0 {
		return nil
	}
	theme := huh.ThemeCharm()
	gap := theme.FieldSeparator.Render()
	theme.FieldSeparator = lipgloss.NewStyle()              // each formField draws its own
	theme.Group.Title = lipgloss.NewStyle().MarginBottom(1) // the stepper styles itself
	// No cursor on a field that isn't focused: huh's Charm theme keeps it there. formField.Blur ticks a
	// select's answer once it is left.
	theme.Blurred.SelectSelector = lipgloss.NewStyle().SetString("  ")
	theme.Blurred.MultiSelectSelector = theme.Blurred.SelectSelector
	theme.Blurred.SelectedOption = theme.Blurred.UnselectedOption
	names := make([]string, len(stages))
	for i, s := range stages {
		names[i] = s.name
	}
	groups := make([]*huh.Group, len(stages))
	for i, s := range stages {
		header := stageHeader(s.name, i+1, len(stages))
		group := make([]huh.Field, len(s.fields))
		for j, f := range s.fields {
			if j > 0 && !f.top {
				f.gap = gap
			} else {
				f.header = header
			}
			// Its own copy, before the form's: a huh field keeps the first theme it is given.
			own := *theme
			f.own = &own
			f.WithTheme(f.own)
			group[j] = f
		}
		groups[i] = huh.NewGroup(group...).Title(stepper(names, i+1))
	}
	keys := huh.NewDefaultKeyMap()
	keys.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"))
	form := huh.NewForm(groups...).WithTheme(theme).WithKeyMap(keys).WithInput(in).WithOutput(out)
	var err error
	if accessible {
		var fields []huh.Field
		for _, s := range stages {
			for _, f := range s.fields {
				fields = append(fields, f)
			}
		}
		err = runAccessible(context.Background(), fields, in, out)
	} else {
		var scout *scoutRun
		if stage2 != nil {
			scout = stage2.scout
		}
		err = runForm(form, scout, in, out)
	}
	if err != nil {
		return err
	}
	c.CheckFastTimeout = strings.TrimSpace(c.CheckFastTimeout)
	c.CheckFullTimeout = strings.TrimSpace(c.CheckFullTimeout)
	c.Setup = strings.TrimSpace(c.Setup)
	if ask.Prefix {
		c.Prefix = typedPrefix(*c)
	}
	if stage2 != nil {
		stage2.apply()
	}
	if ask.Concurrent {
		c.Concurrent, c.Unasked.Concurrent = option, false
		if option == customConcurrency {
			n, err := parseConcurrency(custom)
			if err != nil {
				return err // the form doesn't submit one
			}
			c.Concurrent = n
		}
	}
	if ask.MCP && len(mcpOpts) > 0 {
		chosen := append([]string{}, servers...)
		c.MCP, c.Unasked.MCP = &chosen, false
	}
	return nil
}
