package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
)

// Stage 2 of init, Checks: the scout looks for the project's suites once the stage is reached, under
// a spinner; then the user chooses its checks: the suites found, tests created from scratch with the
// create-check-suite skill, or a check command typed by hand. The scout runs in its own goroutine;
// what it found is applied to the stage's questions on the form's goroutine, the one that draws them,
// whenever the form next looks (settled). huh hands messages only to the stage shown, so the stage
// can't count on the message that wakes it when the scout ends.

// errSkipped is why there are no suites when Esc skipped the scout.
var errSkipped = errors.New("the search was skipped with Esc")

// errNoScout is why there are no suites when AskInit was given no scout.
var errNoScout = errors.New("there is no scout to look for them")

// scoutRun is the scout's one search for the project's suites.
type scoutRun struct {
	find func(context.Context) (organ.Scouting, error)
	done chan struct{} // closed once find has returned
	// Set by the scout's goroutine before done is closed.
	found organ.Scouting
	err   error
	// Read and written on the form's goroutine only.
	cancel   context.CancelCauseFunc
	began    time.Time
	started  bool
	settled  bool
	onSettle func(organ.Scouting, error) // applies what the scout found to the stage's questions
	outcome  string                      // what it found, or why nothing, as onSettle says it
}

func newScoutRun(find func(context.Context) (organ.Scouting, error)) *scoutRun {
	return &scoutRun{find: find, done: make(chan struct{})}
}

// start starts the scout, once.
func (s *scoutRun) start() {
	if s.started {
		return
	}
	s.started, s.began = true, time.Now()
	ctx, cancel := context.WithCancelCause(context.Background())
	s.cancel = cancel
	go func() {
		defer close(s.done)
		if s.find == nil {
			s.err = errNoScout
			return
		}
		s.found, s.err = s.find(ctx)
	}()
}

// poll settles the scout's outcome once it has ended, and reports whether it is settled.
func (s *scoutRun) poll() bool {
	if s.started && !s.settled {
		select {
		case <-s.done:
			s.settle(s.found, s.err)
		default:
		}
	}
	return s.settled
}

func (s *scoutRun) settle(found organ.Scouting, err error) {
	s.settled = true
	if s.onSettle != nil {
		s.onSettle(found, err)
	}
}

// running reports whether the scout is under way, its outcome not settled.
func (s *scoutRun) running() bool { return s.started && !s.poll() }

// skip stops the scout and settles on no suites at once, without waiting for it to end.
func (s *scoutRun) skip() {
	s.cancel(errSkipped)
	s.settle(organ.Scouting{}, errSkipped)
}

// wait waits for the scout's goroutine, stopping it first when it is under way.
func (s *scoutRun) wait() {
	if !s.started {
		return
	}
	s.cancel(errors.New("init's form ended"))
	<-s.done
}

// scoutEnded wakes the form when the scout ends.
type scoutEnded struct{}

// ended waits for the scout to end, then wakes the form.
func (s *scoutRun) ended() tea.Cmd {
	done := s.done
	return func() tea.Msg {
		<-done
		return scoutEnded{}
	}
}

// scoutField is stage 2's first field: a spinner while the scout looks, the time it has taken and
// how to skip it. Focused, it starts the scout; once the scout is settled the stage hides it, and
// initModel moves the focus on to the next field.
type scoutField struct {
	run     *scoutRun
	spinner spinner.Model
	theme   *huh.Theme
	focused bool
	width   int
	// In huh's accessible form, which focuses each field before it asks for it: the scout starts in
	// RunAccessible instead, or it could end before then and the field, hidden, would say nothing.
	accessible bool
}

func newScoutField(run *scoutRun, accessible bool) *scoutField {
	frames := make([]string, len(busySpinner.Frames))
	for i, f := range busySpinner.Frames {
		frames[i] = strings.TrimSpace(f)
	}
	sp := spinner.Spinner{Frames: frames, FPS: busySpinner.FPS}
	return &scoutField{run: run, spinner: spinner.New(spinner.WithSpinner(sp), spinner.WithStyle(pickedStyle)),
		accessible: accessible}
}

// scoutLimit is how long the scout may look, as its spinner says it.
var scoutLimit = fmt.Sprintf("%d minutes", int(organ.ScoutLimit/time.Minute))

// scoutSkipKey is the key that skips the scout; the form's own Esc cancels it.
var scoutSkipKey = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "skip and type a check yourself"))

func (f *scoutField) Init() tea.Cmd { return nil }

// Update turns the spinner while the scout looks.
func (f *scoutField) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if f.run.poll() {
		return f, nil
	}
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		f.spinner, cmd = f.spinner.Update(msg)
		return f, cmd
	case tea.KeyMsg:
		if f.focused && msg.String() == "shift+tab" {
			return f, huh.PrevField
		}
	}
	return f, nil
}

// View is the spinner, what the scout does and the time it has taken, then how to skip it.
func (f *scoutField) View() string {
	styles := f.styles()
	took := time.Duration(0)
	if f.run.started {
		took = time.Since(f.run.began)
	}
	line := f.spinner.View() + " " + styles.Title.Render("Looking for the project's suites") + " " +
		dimStyle.Render(clock(took)+" (up to "+scoutLimit+")")
	hint := styles.Description.Render("Its tests, lints, type checks and builds.\n" +
		keyStyle.Render("Esc") + ": skip and type a check yourself")
	return styles.Base.Width(f.width).Render(line + "\n" + hint)
}

func (f *scoutField) styles() huh.FieldStyles {
	theme := f.theme
	if theme == nil {
		theme = huh.ThemeCharm()
	}
	if f.focused {
		return theme.Focused
	}
	return theme.Blurred
}

// Focus starts the scout, and the spinner.
func (f *scoutField) Focus() tea.Cmd {
	f.focused = true
	if f.accessible {
		return nil
	}
	f.run.start()
	return tea.Batch(f.spinner.Tick, f.run.ended())
}

func (f *scoutField) Blur() tea.Cmd {
	f.focused = false
	return nil
}

func (f *scoutField) Error() error { return nil }

func (f *scoutField) Run() error { return errors.New("the scout's field runs in a form") }

// RunAccessible runs the scout to its end, saying so, then what it found.
func (f *scoutField) RunAccessible(w io.Writer, _ io.Reader) error {
	if _, err := fmt.Fprintf(w, "Looking for the project's suites (up to %s)…\n", scoutLimit); err != nil {
		return err
	}
	f.run.start()
	<-f.run.done
	f.run.poll()
	_, err := fmt.Fprintf(w, "%s\n", f.run.outcome)
	return err
}

// Skip is false: the stage hides the field once the scout is settled.
func (f *scoutField) Skip() bool { return false }

func (f *scoutField) Zoom() bool { return false }

func (f *scoutField) KeyBinds() []key.Binding { return []key.Binding{scoutSkipKey} }

func (f *scoutField) WithTheme(theme *huh.Theme) huh.Field {
	if f.theme == nil {
		f.theme = theme
	}
	return f
}

func (f *scoutField) WithAccessible(bool) huh.Field { return f }

func (f *scoutField) WithKeyMap(*huh.KeyMap) huh.Field { return f }

func (f *scoutField) WithWidth(width int) huh.Field {
	f.width = width
	return f
}

func (f *scoutField) WithHeight(int) huh.Field { return f }

func (f *scoutField) WithPosition(huh.FieldPosition) huh.Field { return f }

func (f *scoutField) GetKey() string { return "" }

func (f *scoutField) GetValue() any { return nil }

// checksStage is stage 2's choice of the project's checks, and what it sets on the choice.
type checksStage struct {
	c       *project.Choice
	scout   *scoutRun
	runners []project.Runner  // the runners as they are
	skill   project.SkillPlan // the create-check-suite skill as it is

	suites     []organ.Suite
	fast, full []int        // the suites ticked in each checklist
	picked     checksChoice // the choice made
	choice     *boundedSelect[checksChoice]
	fastList   *huh.MultiSelect[int]
	fullList   *huh.MultiSelect[int]

	check, checkBefore string // the check command typed, for Manual
	replaceFast        bool
	replaceFull        bool
	replaceSkill       bool
}

// checksChoice is stage 2's choice, as its select holds it: none is the zero value, which huh
// scrolls a select to, hiding the options above it.
type checksChoice int

// The choices of stage 2.
const (
	choseFound checksChoice = iota + 1
	choseScratch
	choseManual
)

// tests is the choice as project.Choice's Tests says it.
func (k checksChoice) tests() project.Tests {
	switch k {
	case choseFound:
		return project.TestsFound
	case choseScratch:
		return project.TestsScratch
	}
	return project.TestsManual
}

// addChecks adds stage 2's questions to the stage: the scout, the choice and what each choice asks,
// and whether to keep or replace what differs from what init would write. accessible is whether
// huh's accessible form asks them.
func addChecks(stage *initStage, c *project.Choice, ask Ask, accessible bool) *checksStage {
	s := &checksStage{c: c, scout: newScoutRun(ask.Scout), runners: ask.Runners, skill: ask.Skill}
	s.scout.onSettle = s.settle
	s.check = c.FastCommand()
	s.checkBefore = s.check

	scout := stage.add(newScoutField(s.scout, accessible))
	hide(scout, s.scout.poll)
	settled := s.scout.poll
	is := func(k checksChoice) func() bool {
		return func() bool { return !settled() || s.picked != k }
	}

	s.picked = choseManual
	s.choice = newBoundedSelect(huh.NewSelect[checksChoice]().Title("The project's checks"), &s.picked,
		huh.NewOption("Use them as they are", choseFound),
		huh.NewOption("Create from scratch with the create-check-suite skill", choseScratch),
		huh.NewOption("Manual", choseManual))
	choice := stage.add(s.choice)
	choice.top = true // the first field shown once the scout's is hidden
	hide(choice, func() bool { return !settled() })

	empty := "Empty for a runner that checks nothing."
	if c.Fast == nil {
		empty = "Empty keeps " + project.FastRunner + " as it is."
	}
	hide(stage.add(huh.NewInput().
		Title("Check command").
		Description("Lint, build and tests, the one command "+project.FastRunner+" runs. Workers run it before "+
			"they close a ticket, and orchestra runs it again on a ticket rebased onto work merged meanwhile. "+
			empty).
		Placeholder("e.g. make check").
		Value(&s.check)), is(choseManual))

	// Not filterable: Esc, which ends a filter, cancels the form.
	s.fastList = huh.NewMultiSelect[int]().
		Title("On every merge (check-fast)").
		Description(project.FastRunner + " runs these on every ticket rebased before it merges: the quick ones.").
		Filterable(false).
		Value(&s.fast)
	hide(stage.add(s.fastList), func() bool { return is(choseFound)() || len(s.suites) == 0 })
	s.fullList = huh.NewMultiSelect[int]().
		Title("At the end of a run (check-full)").
		Description(project.FullRunner + " runs " + project.FastRunner + ", then these, once at the end of a " +
			"run. A suite ticked above as well runs in check-fast only.").
		Filterable(false).
		Value(&s.full)
	hide(stage.add(s.fullList), func() bool { return is(choseFound)() || len(s.fullOffered()) == 0 })
	hide(stage.add(huh.NewConfirm().
		Title("Also file tickets for untested areas").
		Description("A ticket asks a worker to map the project's features with the create-check-suite skill, "+
			"and to file a ticket for each area no suite tests.").
		Affirmative("File them").
		Negative("No").
		Value(&c.FileUntested)), is(choseFound))

	for _, q := range []struct {
		path    string
		replace *bool
	}{{project.FastRunner, &s.replaceFast}, {project.FullRunner, &s.replaceFull}} {
		r, ok := s.runner(q.path)
		if !ok || !r.Exists {
			continue
		}
		path := q.path
		hide(stage.add(keepOrReplace(path+" is there, and differs from the checks chosen",
			runsDescription(r.Script), q.replace)), func() bool { return !settled() || !s.differs(path) })
	}
	if s.skill.Exists && s.skill.Differs {
		hide(stage.add(keepOrReplace(s.skill.Dir+"/ is there, and differs from orchestra's",
			"The tickets init files ask workers to use the create-check-suite skill. Replacing it writes "+
				"orchestra's files over those there, and leaves any of the project's own.", &s.replaceSkill)),
			func() bool { return !settled() || !s.filesTestWork() })
	}
	return s
}

// hide hides f while hidden says so.
func hide(f *formField, hidden func() bool) {
	f.hidden = hidden
	f.wasHidden = f.isHidden()
}

// keepOrReplace asks whether to keep what is there (the default) or replace it.
func keepOrReplace(title, description string, replace *bool) *boundedSelect[bool] {
	return newBoundedSelect(huh.NewSelect[bool]().Title(title).Description(description), replace,
		huh.NewOption("Keep it", false), huh.NewOption("Replace it", true))
}

// runsDescription says what a runner's script runs.
func runsDescription(script string) string {
	commands := project.RunnerCommands(script)
	if len(commands) == 0 {
		return "It runs nothing."
	}
	return "It runs:\n  " + strings.Join(commands, "\n  ")
}

// settle applies the scout's outcome: the choice it starts on, with why, and the checklists' suites.
func (s *checksStage) settle(found organ.Scouting, err error) {
	var why string
	switch {
	case err != nil:
		s.picked, why = choseManual, capitalize(err.Error())+"."
	case len(found.Suites) == 0:
		s.picked, why = choseScratch, "The scout found no suites."
	default:
		s.picked, why = choseFound, "The scout found "+plural(len(found.Suites), "suite", "suites")+"."
		s.suites = found.Suites
	}
	if err == nil && found.Note != "" {
		why += " " + found.Note
	}
	s.scout.outcome = why
	s.choice.Description(why)
	s.choice.Value(&s.picked)
	if len(s.suites) == 0 {
		return
	}
	// The options first, nothing ticked: options set with one ticked scroll to it, hiding those above.
	var fast, full []huh.Option[int]
	var fastTicked, fullTicked []int
	for i, su := range s.suites {
		fast = append(fast, huh.NewOption(suiteLabel(su), i))
		if su.Tier == organ.TierFast {
			fastTicked = append(fastTicked, i)
		} else {
			full = append(full, huh.NewOption(suiteLabel(su), i))
			fullTicked = append(fullTicked, i)
		}
	}
	s.fastList.Options(fast...)
	s.fullList.Options(full...)
	s.fast, s.full = fastTicked, fullTicked
	s.fastList.Value(&s.fast)
	s.fullList.Value(&s.full)
}

// suiteLabel is a suite as a checklist shows it: its command, then its name, where it was found and
// what it needs.
func suiteLabel(su organ.Suite) string {
	var about []string
	for _, a := range []string{su.Name, su.FoundIn} {
		if a != "" {
			about = append(about, a)
		}
	}
	if len(su.Needs) > 0 {
		about = append(about, "needs "+strings.Join(su.Needs, ", "))
	}
	if !su.ParallelSafe {
		about = append(about, "takes turns") // in a runner, between lock and unlock
	}
	return su.Command + "  · " + strings.Join(about, " · ")
}

// fullOffered are the suites check-full's checklist offers: those the scout put in the full tier.
func (s *checksStage) fullOffered() []int {
	var offered []int
	for i, su := range s.suites {
		if su.Tier != organ.TierFast {
			offered = append(offered, i)
		}
	}
	return offered
}

func (s *checksStage) runner(path string) (project.Runner, bool) {
	i := slices.IndexFunc(s.runners, func(r project.Runner) bool { return r.Path == path })
	if i < 0 {
		return project.Runner{}, false
	}
	return s.runners[i], true
}

// chosen are the runners' suites as the answers choose them so far; ok is false for Manual, whose
// check command init writes over the runner there.
func (s *checksStage) chosen() (fast, full []project.Suite, ok bool) {
	switch s.picked {
	case choseScratch:
		return []project.Suite{}, []project.Suite{}, true
	case choseFound:
		fast, full = []project.Suite{}, []project.Suite{}
		for _, i := range s.fast {
			fast = append(fast, runnerSuite(s.suites[i]))
		}
		for _, i := range s.full {
			if !slices.Contains(s.fast, i) {
				full = append(full, runnerSuite(s.suites[i]))
			}
		}
		return fast, full, true
	}
	return nil, nil, false
}

// runnerSuite is a suite the scout found as a runner's line.
func runnerSuite(su organ.Suite) project.Suite {
	return project.Suite{Name: su.Name, Command: su.Command, FoundIn: su.FoundIn, Serial: !su.ParallelSafe}
}

// filesTestWork reports whether the answers so far file test work, as Choice.FilesTestWork does.
func (s *checksStage) filesTestWork() bool {
	return s.picked == choseScratch || s.picked == choseFound && s.c.FileUntested
}

// differs reports whether the runner at path is there and differs from what the answers would write.
func (s *checksStage) differs(path string) bool {
	r, ok := s.runner(path)
	fast, full, chosen := s.chosen()
	if !ok || !r.Exists || !chosen {
		return false
	}
	want := project.FastScript(fast)
	if path == project.FullRunner {
		kept, _ := project.WithoutRunners(fast)
		want = project.FullScript(full, len(kept) == 0)
	}
	return r.Script != want
}

// apply sets the answers on the choice once the form is submitted.
func (s *checksStage) apply() {
	c := s.c
	c.Tests = s.picked.tests()
	if fast, full, ok := s.chosen(); ok {
		c.Fast, c.Full = &fast, &full
		c.ReplaceFast, c.ReplaceFull = s.replaceFast && s.differs(project.FastRunner),
			s.replaceFull && s.differs(project.FullRunner)
		c.CheckFrom = "the scout"
		if c.Tests == project.TestsScratch {
			c.CheckFrom = "the form"
		}
	} else if check := strings.TrimSpace(s.check); check != s.checkBefore {
		c.Fast = project.RunnerSuites(check, "orchestra init's form", project.FastRunner)
		c.ReplaceFast, c.CheckFrom = true, "the form"
	}
	if c.Tests != project.TestsFound {
		c.FileUntested = false
	}
	c.ReplaceSkill = s.replaceSkill && c.FilesTestWork()
}

// initModel is the init form, run by AskInit's own program so that Esc skips the scout while it
// looks instead of cancelling the form, as it does everywhere else.
type initModel struct {
	form  *huh.Form
	scout *scoutRun // nil when stage 2 asks no choice of checks
}

func (m initModel) Init() tea.Cmd { return m.form.Init() }

func (m initModel) View() string { return m.form.View() }

func (m initModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && m.scout != nil && key.Matches(k, scoutSkipKey) && m.scout.running() {
		if _, onScout := unwrapField(m.form.GetFocusedField()).(*scoutField); onScout {
			m.scout.skip()
			return m, m.leaveScout()
		}
	}
	leave := m.leaveScout()
	f, cmd := m.form.Update(msg)
	m.form = f.(*huh.Form)
	return m, tea.Batch(leave, cmd, m.leaveScout())
}

// leaveScout moves the focus from the scout's field to the next once the scout is settled, at once
// rather than by huh's NextField message: the screen shows the stage's choice as soon as the scout
// is settled, even while drawing, and a key typed before that message would reach the hidden scout's
// field instead of the choice.
func (m initModel) leaveScout() tea.Cmd {
	if m.scout == nil || !m.scout.poll() {
		return nil
	}
	if _, onScout := unwrapField(m.form.GetFocusedField()).(*scoutField); !onScout {
		return nil
	}
	return m.form.NextField()
}

// unwrapField is the field inside a formField.
func unwrapField(f huh.Field) huh.Field {
	if ff, ok := f.(*formField); ok {
		return ff.Field
	}
	return f
}

// runForm runs the form on in and out, as huh's Run does, with Esc skipping the scout. Esc and Ctrl+C
// quit rather than interrupt, as AskWork's do: on tea.Interrupt Bubble Tea closes the terminal's
// input without waiting for its goroutine still reading it, a data race. The form's State then tells
// a cancelled form from a submitted one.
func runForm(form *huh.Form, scout *scoutRun, in io.Reader, out io.Writer) error {
	form.SubmitCmd, form.CancelCmd = tea.Quit, tea.Quit
	_, err := tea.NewProgram(initModel{form: form, scout: scout}, tea.WithInput(in), tea.WithOutput(out),
		tea.WithReportFocus()).Run()
	switch {
	case form.State == huh.StateAborted:
		return huh.ErrUserAborted
	case err != nil:
		return fmt.Errorf("huh: %w", err)
	}
	return nil
}
