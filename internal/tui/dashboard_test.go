package tui

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

func TestRenderEventShowsTitleOnPickupAndOnlyTheIDOnCompletion(t *testing.T) {
	at := time.Date(2026, 9, 28, 16, 6, 27, 0, time.Local)
	picked := ansi.Strip(renderEvent(dispatch.Event{Time: at, Kind: dispatch.EvDispatch, N: 3, Limit: 40,
		Ticket: "kinieta-dg4", Title: "Decide whether the next release is pushed to CocoaPods trunk"}))
	if picked != "16:06:27 ▶ [3/40] kinieta-dg4  Decide whether the next release is pushed to CocoaPods trunk" {
		t.Errorf("picked = %q", picked)
	}
	done := ansi.Strip(renderEvent(dispatch.Event{Time: at, Kind: dispatch.EvClosed, Ticket: "kinieta-2e7",
		Title: "should not appear", Detail: "04c8d47 merged into batch"}))
	if done != "16:06:27 ✓ kinieta-2e7 completed  04c8d47 merged into batch" {
		t.Errorf("completed = %q", done)
	}
}

func TestTicketLinesUseExactColours(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	// Exact colours are sent as 38;2;R;G;B. Palette slots (38;5;N or 3N) are remapped by themes.
	for name, line := range map[string]string{
		"picked":    renderEvent(dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Limit: 40, Ticket: "kinieta-jqm", Title: "Support visionOS"}),
		"completed": renderEvent(dispatch.Event{Kind: dispatch.EvClosed, Ticket: "kinieta-jqm", Detail: "abc merged"}),
	} {
		if !strings.Contains(line, "38;2;") || strings.Contains(line, "38;5;") {
			t.Errorf("%s line does not use an exact colour: %q", name, line)
		}
	}
	picked := renderEvent(dispatch.Event{Kind: dispatch.EvDispatch, Ticket: "x"})
	done := renderEvent(dispatch.Event{Kind: dispatch.EvClosed, Ticket: "x"})
	if picked[strings.Index(picked, "38;2;"):][:16] == done[strings.Index(done, "38;2;"):][:16] {
		t.Error("picked and completed lines should differ in colour")
	}
}

func TestViewFitsThePaneWidth(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch/2026-09-28"}, func() {}, func(bool) {}, func(string) {})
	m.closed, m.deferred, m.queued = 2, 1, 17
	m.began = time.Now().Add(-12 * time.Minute)
	m.active = map[string]dispatch.Status{"x": {Ticket: "kinieta-y6j", Title: "Warn in debug builds when a chain call is silently ignored",
		Tab: "w2B:t9", Started: time.Now().Add(-134 * time.Second), Agent: "working",
		Activity: "⏺ Bash(scripts/ci-local.sh lint ios && git status --short && git diff --stat)"}}
	m.height = 40
	for _, w := range []int{30, 45, 66, 120} {
		m.width = w
		view := m.View()
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > w {
				t.Errorf("width %d: line is %d wide: %q", w, ansi.StringWidth(line), ansi.Strip(line))
			}
		}
		if w == 66 {
			t.Logf("preview at %d columns:\n%s", w, ansi.Strip(view))
		}
	}
	m.active = nil
	m.width = 66
	if v := ansi.Strip(m.View()); !strings.Contains(v, "picking the next ticket") {
		t.Errorf("idle view: %s", v)
	}
}

func runEvents(m Dashboard, evs ...dispatch.Event) Dashboard {
	for _, ev := range evs {
		next, _ := m.Update(eventMsg(ev))
		m = next.(Dashboard)
	}
	return m
}

func TestTicketRowsFollowEachTicket(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m,
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "kinieta-dwv", Title: "Reduce Motion: keep fades"},
		dispatch.Event{Kind: dispatch.EvClosed, Ticket: "kinieta-dwv", Detail: "ffd6ce4 merged into batch"},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 2, Ticket: "kinieta-vzg", Title: "Competing timelines"},
		dispatch.Event{Kind: dispatch.EvDeferred, Ticket: "kinieta-vzg", Detail: "still open, noted for review"},
		dispatch.Event{Kind: dispatch.EvTriage, Ticket: "kinieta-vzg", Detail: "environment · high", Title: "prompt never submitted"},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 3, Ticket: "kinieta-kco", Title: "Open the property model"},
	)
	if len(m.rows) != 3 || m.closed != 1 || m.deferred != 1 || m.triaged != 1 {
		t.Fatalf("rows %+v closed %d deferred %d triaged %d", m.rows, m.closed, m.deferred, m.triaged)
	}
	m.width, m.height = 70, 40
	view := ansi.Strip(m.View())
	for _, want := range []string{
		"✓ done", "kinieta-dwv", "ffd6ce4 merged", // completed: the commit, not the title
		"↷ deferred", "◆ environment · high · prompt", // triage replaces the reason (cut to fit)
		"▶ working", "Open the property model",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Reduce Motion: keep fades") {
		t.Error("a completed ticket should show its commit, not its title")
	}

	m = runEvents(m, dispatch.Event{Kind: dispatch.EvStop, Text: "PAUSED: kinieta-kco"})
	final := ansi.Strip(printedEnd(m, 70))
	if !strings.Contains(final, "■ stopped") || strings.Contains(final, "to stop after the current tickets") ||
		strings.Contains(final, "Current") {
		t.Errorf("the summary after the dashboard should mark the stopped ticket, without the live parts:\n%s", final)
	}
}

func TestViewFitsThePaneHeight(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch/2026-09-28"}, func() {}, func(bool) {}, func(string) {})
	for i := 0; i < 50; i++ { // more than 120x50 shows
		id := fmt.Sprintf("kinieta-%03d", i)
		m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: i + 1, Ticket: id, Title: "A ticket title long enough to need truncating in a narrow pane"},
			dispatch.Event{Kind: dispatch.EvClosed, Ticket: id, Detail: "abc1234 merged into batch/2026-09-28"})
	}
	m.active = map[string]dispatch.Status{"x": {Ticket: "kinieta-049", Title: "t", Tab: "w2B:t9", Started: time.Now(), Agent: "working", Activity: "⏺ Bash(scripts/ci-local.sh)"}}
	for _, size := range [][2]int{{40, 30}, {66, 36}, {120, 50}} {
		m.width, m.height = size[0], size[1]
		lines := strings.Split(m.View(), "\n")
		if len(lines) > m.height {
			t.Errorf("%dx%d: view is %d lines", size[0], size[1], len(lines))
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > m.width {
				t.Errorf("%dx%d: line %d wide: %q", size[0], size[1], ansi.StringWidth(l), ansi.Strip(l))
			}
		}
		if !regexp.MustCompile(`│ \+\d+ +│`).MatchString(ansi.Strip(m.View())) {
			t.Errorf("%dx%d: hidden tickets are not mentioned", size[0], size[1])
		}
		if size[0] == 66 {
			t.Logf("preview %dx%d:\n%s", size[0], size[1], ansi.Strip(m.View()))
		}
	}
}

func TestAskedTicketIsCountedAndShown(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m,
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Choose the licence"},
		dispatch.Event{Kind: dispatch.EvAsked, Ticket: "k-1", Detail: "q-1: Decision for k-1: MIT or Apache?"})
	m.width, m.height = 80, 40
	v := ansi.Strip(m.View())
	for _, want := range []string{"Needs you", "? 1", "? for you", "answer q-1: Decision for k-1"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
}

// Once its question is answered, an asked ticket's row goes back to working, rather than a second
// row being added, and it no longer counts as needing the maintainer.
func TestAnsweredTicketGoesBackToWorkInItsRow(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m,
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Choose the licence"},
		dispatch.Event{Kind: dispatch.EvAsked, Ticket: "k-1", Detail: "q-1: MIT or Apache?"},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 2, Ticket: "k-2", Title: "Write the README"},
		dispatch.Event{Kind: dispatch.EvAnswered, Ticket: "k-1", Detail: "q-1: MIT or Apache?"},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 3, Ticket: "k-1", Title: "Choose the licence"})
	if len(m.rows) != 2 || m.rows[0].id != "k-1" || m.rows[0].state != rowWorking || m.rows[0].note != "" || m.asked != 0 {
		t.Fatalf("rows %+v, asked %d; want k-1's first row working again and nothing asked", m.rows, m.asked)
	}
	m.width, m.height = 80, 40
	if v := ansi.Strip(m.View()); strings.Contains(v, "? for you") || !strings.Contains(v, "Choose the licence") {
		t.Errorf("view:\n%s", v)
	}
	if got := ansi.Strip(renderEvent(dispatch.Event{Kind: dispatch.EvAnswered, Ticket: "k-1", Detail: "q-1: MIT or Apache?"})); !strings.Contains(got, "↺ k-1 answered  q-1: MIT or Apache?") {
		t.Errorf("rendered %q", got)
	}
}

// An asked ticket its worker deferred in its tab shows as deferred, and no longer counts as needing
// the maintainer.
func TestAskedTicketDeferredLeavesTheAskedCount(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m,
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Choose the licence"},
		dispatch.Event{Kind: dispatch.EvAsked, Ticket: "k-1", Detail: "q-1: MIT or Apache?"},
		dispatch.Event{Kind: dispatch.EvDeferred, Ticket: "k-1", Detail: "by the worker"})
	if len(m.rows) != 1 || m.rows[0].state != rowDeferred || m.asked != 0 || m.deferred != 1 {
		t.Fatalf("rows %+v, asked %d, deferred %d; want k-1 deferred and nothing asked", m.rows, m.asked, m.deferred)
	}
}

// An asked ticket whose worker closed it in its tab is adopted without a second dispatch: its row
// goes from "? for you" to done.
func TestAskedTicketAdoptedGoesToDone(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m,
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Choose the licence"},
		dispatch.Event{Kind: dispatch.EvAsked, Ticket: "k-1", Detail: "q-1: MIT or Apache?"},
		dispatch.Event{Kind: dispatch.EvAnswered, Ticket: "k-1", Detail: "q-1: MIT or Apache?"},
		dispatch.Event{Kind: dispatch.EvClosed, Ticket: "k-1", Detail: "abc123 merged into batch"})
	if len(m.rows) != 1 || m.rows[0].state != rowDone || m.asked != 0 || m.closed != 1 {
		t.Fatalf("rows %+v, asked %d, closed %d; want k-1 done and nothing asked", m.rows, m.asked, m.closed)
	}
}

func TestActiveTitleWrapsToAFewLines(t *testing.T) {
	title := "Competing timelines on the same view and property fight each other every frame"
	got := wrapLines(title, 30, titleLines)
	if len(got) != 3 || strings.Join(got, " ") != title {
		t.Errorf("wrapped = %q", got)
	}
	for _, l := range got {
		if ansi.StringWidth(l) > 30 {
			t.Errorf("line too wide: %q", l)
		}
	}
	long := strings.Repeat("word ", 40)
	cut := wrapLines(long, 30, titleLines)
	if len(cut) != 3 || !strings.HasSuffix(cut[2], "…") {
		t.Errorf("a long title should stop at 3 lines ending in …: %q", cut)
	}
	if got := wrapLines("Short title", 30, titleLines); len(got) != 1 {
		t.Errorf("short title = %q", got)
	}

	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m.width, m.height = 66, 40
	m.active = map[string]dispatch.Status{"x": {Ticket: "kinieta-vzg", Title: title, Started: time.Now(), Agent: "working", Activity: "✻ Cooking… (8m 10s)"}}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Competing timelines") || !strings.Contains(v, "other every frame") {
		t.Errorf("the whole title should be visible when it fits in 3 lines:\n%s", v)
	}
	for _, l := range strings.Split(m.View(), "\n") {
		if ansi.StringWidth(l) > m.width {
			t.Errorf("line %d wide: %q", ansi.StringWidth(l), ansi.Strip(l))
		}
	}
}

func TestWordWrapKeepsHyphenatedWords(t *testing.T) {
	got := wordWrap("moved .claude/worker-prompt.md to .orchestra/worker-prompt.md", 30)
	for _, l := range got {
		if strings.HasSuffix(l, "-") || ansi.StringWidth(l) > 30 {
			t.Errorf("bad line %q in %q", l, got)
		}
	}
	if strings.Join(got, " ") != "moved .claude/worker-prompt.md to .orchestra/worker-prompt.md" {
		t.Errorf("words lost: %q", got)
	}
	long := wordWrap(strings.Repeat("x", 25), 10)
	if len(long) != 3 || long[0] != strings.Repeat("x", 10) {
		t.Errorf("a long word should be cut: %q", long)
	}
}

func TestShortVersion(t *testing.T) {
	for in, want := range map[string]string{
		"v0.1.2-0.20260930072042-09ffc8431bb5+dirty": "v0.1.2-dev 09ffc84+dirty",
		"v0.1.2-0.20260930072042-09ffc8431bb5":       "v0.1.2-dev 09ffc84",
		"v0.2.0":                                     "v0.2.0",
		"dev":                                        "dev",
	} {
		if got := shortVersion(in); got != want {
			t.Errorf("shortVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWorkerShowsWhatItIsDoing(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "kinieta-ce1", Title: "Add a way to repeat a timeline"})
	m.width, m.height = 70, 40
	status := dispatch.Status{Ticket: "kinieta-ce1", Title: "Add a way to repeat a timeline", Started: time.Now(),
		Agent: "working", Doing: "testing", Activity: "⏺ Running the full local CI · 59s"}
	m.active = map[string]dispatch.Status{"kinieta-ce1": status}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "▶ testing") || !strings.Contains(view, "kinieta-ce1  testing") || strings.Contains(view, "working") {
		t.Errorf("a worker running the checks should show testing:\n%s", view)
	}

	status.Agent = "blocked" // a dialog outranks the last report
	m.active["kinieta-ce1"] = status
	if view := ansi.Strip(m.View()); !strings.Contains(view, "blocked") || !strings.Contains(view, "▶ working") {
		t.Errorf("a blocked worker should show blocked:\n%s", view)
	}
}

// A status Herdr failed to read shows as unreadable, not as the empty state it comes with.
func TestWorkerWhoseStatusCannotBeReadShowsUnreadable(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "kinieta-ce1", Title: "Add a way to repeat a timeline"})
	m.width, m.height = 70, 40
	m.active = map[string]dispatch.Status{"kinieta-ce1": {Ticket: "kinieta-ce1", Title: "Add a way to repeat a timeline", Started: time.Now(),
		Unreadable: true}}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "kinieta-ce1  unreadable") {
		t.Errorf("a worker whose status can't be read should show unreadable:\n%s", view)
	}
}

func TestWorkerResolvingItsRebaseShowsResolving(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "kinieta-ce1", Title: "Add a way to repeat a timeline"})
	m.width, m.height = 70, 40
	m.active = map[string]dispatch.Status{"kinieta-ce1": {Ticket: "kinieta-ce1", Title: "Add a way to repeat a timeline", Started: time.Now(),
		Agent: "working", Doing: "testing", Resolving: true}}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "⟳ resolving") || !strings.Contains(view, "kinieta-ce1  resolving") {
		t.Errorf("a worker resolving its rebase should show resolving:\n%s", view)
	}
}

// recorder is a sink that keeps the events' texts.
type recorder struct{ texts []string }

func (r *recorder) Event(ev dispatch.Event) { r.texts = append(r.texts, ev.Text) }
func (*recorder) Status(dispatch.Status)    {}

// runDashboard starts a dashboard program without a terminal and returns it with its sink and the
// channel its final model arrives on.
func runDashboard(t *testing.T) (*tea.Program, *ProgramSink, chan Dashboard) {
	t.Helper()
	p := tea.NewProgram(NewDashboard(dispatch.Config{Limit: 40}, func() {}, func(bool) {}, func(string) {}),
		tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutSignalHandler())
	final := make(chan Dashboard, 1)
	go func() {
		m, _ := p.Run()
		d, _ := m.(Dashboard)
		final <- d
	}()
	return p, NewProgramSink(p), final
}

func TestHandoffPassesOnWhatTheClosedDashboardMissed(t *testing.T) {
	_, sink, final := runDashboard(t)
	sink.Event(dispatch.Event{Kind: dispatch.EvInfo, Text: "START"})
	sink.Event(dispatch.Event{Kind: dispatch.EvDone, Text: "READY_EMPTY"}) // closes the dashboard
	m := <-final
	sink.Event(dispatch.Event{Kind: dispatch.EvTriage, Text: "TRIAGE a-1"}) // sent to the exited program
	var r recorder
	sink.Handoff(&r, m.Received())
	sink.Event(dispatch.Event{Kind: dispatch.EvTriage, Text: "TRIAGE a-2"})
	if m.Received() != 2 || m.Final() == nil || m.Final().Text != "READY_EMPTY" {
		t.Errorf("dashboard received %d, final %v", m.Received(), m.Final())
	}
	if got := strings.Join(r.texts, ","); got != "TRIAGE a-1,TRIAGE a-2" {
		t.Errorf("handed off %s, want the two triage lines", got)
	}
}

func TestHandoffAfterAFailedDashboardPassesOnEverything(t *testing.T) {
	p := tea.NewProgram(NewDashboard(dispatch.Config{}, func() {}, func(bool) {}, func(string) {}), tea.WithInput(nil), tea.WithOutput(io.Discard))
	p.Kill() // as good as a program that never started: it drops what it is sent
	sink := NewProgramSink(p)
	sink.Event(dispatch.Event{Text: "START"})
	sink.Event(dispatch.Event{Text: "DISPATCH a-1"})
	var r recorder
	sink.Handoff(&r, Dashboard{}.Received())
	if got := strings.Join(r.texts, ","); got != "START,DISPATCH a-1" {
		t.Errorf("handed off %s", got)
	}
}

func TestQueueEventUpdatesTheQueueCount(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "First", Queued: 0},
		dispatch.Event{Kind: dispatch.EvQueue, Queued: 3})
	if m.queued != 3 {
		t.Errorf("queued = %d, want 3", m.queued)
	}
	if len(m.rows) != 1 {
		t.Errorf("a queue count should add no row: %d rows", len(m.rows))
	}
}

func TestPlainPrinterWritesLogLinesToOut(t *testing.T) {
	var b strings.Builder
	p := Printer{Out: &b}
	at := time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)
	p.Event(dispatch.Event{Kind: dispatch.EvQueue, Text: "3 ready", Time: at})
	p.Event(dispatch.Event{Text: "MERGED orchestra-1", Time: at})
	p.Say("finishing triage…", "Finishing triage…")
	p.Report("# Run report")
	out := b.String()
	if strings.Contains(out, "3 ready") {
		t.Errorf("the queue count was printed:\n%s", out)
	}
	for _, want := range []string{"2026-09-30 12:00:00 MERGED orchestra-1\n", " finishing triage…\n", "\n\n# Run report\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
