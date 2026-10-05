package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

func TestDashboardShowsSeveralWorkers(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch", Concurrency: 3}, func() {}, func(bool) {}, func(string) {})
	m.active = map[string]dispatch.Status{}
	for i, title := range []string{"Competing timelines on the same view and property fight each other every frame",
		"Open the property Dashboard", "Warn in debug builds when a chain call is silently ignored"} {
		id := fmt.Sprintf("kinieta-%d", i)
		m.active[id] = dispatch.Status{Ticket: id, Title: title, Started: time.Now().Add(-time.Duration(i) * time.Minute), Agent: "working", Activity: "⏺ Bash(scripts/ci-local.sh)"}
	}
	m.width, m.height = 66, 40
	v := m.View()
	if lines := strings.Split(v, "\n"); len(lines) > m.height {
		t.Errorf("view is %d lines", len(lines))
	}
	for l := range strings.SplitSeq(v, "\n") {
		if ansi.StringWidth(l) > m.width {
			t.Errorf("line %d wide", ansi.StringWidth(l))
		}
	}
	plain := ansi.Strip(v)
	for _, want := range []string{"kinieta-0", "kinieta-1", "kinieta-2", "Workers", "3 of 3"} {
		if !strings.Contains(plain, want) {
			t.Errorf("view lacks %q", want)
		}
	}
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvHold, Ticket: "kinieta-0", Text: "HOLD: PAUSED: kinieta-0 …"})
	if !strings.Contains(ansi.Strip(m.View()), "stopping") {
		t.Error("a hold should show the run as stopping")
	}
}

func TestDashboardHoldWithoutTicket(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch", Concurrency: 2}, func() {}, func(bool) {}, func(string) {})
	m.width, m.height = 100, 30
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "kinieta-0", Title: "A ticket"},
		dispatch.Event{Kind: dispatch.EvHold, Text: "HOLD: DIRTY_TREE: …; no new tickets while the 1 running finish"})
	if !strings.Contains(ansi.Strip(m.View()), "stopping") {
		t.Error("a hold found before dispatching should show the run as stopping")
	}
	if len(m.rows) != 1 {
		t.Errorf("%d ticket rows, want 1: a hold with no ticket adds none", len(m.rows))
	}
}

func TestDashboardProbeEndsTheHold(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch", Concurrency: 1}, func() {}, func(bool) {}, func(string) {})
	m.width, m.height = 100, 30
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvHold, Text: "PROBE: the run holds for the environment; in 10m …"},
		dispatch.Event{Kind: dispatch.EvProbed, Text: "PROBE_OK: a worker without a ticket ran a command 10m after the hold; taking tickets again"})
	if v := ansi.Strip(m.View()); strings.Contains(v, "stopping") {
		t.Errorf("a probe that ran its command should end the hold:\n%s", v)
	}
}

func TestDashboardFitsShortPanes(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch/2026-09-28", Concurrency: 3}, func() {}, func(bool) {}, func(string) {})
	for i := range 30 {
		id := fmt.Sprintf("kinieta-%03d", i)
		m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: i + 1, Ticket: id, Title: "A ticket"},
			dispatch.Event{Kind: dispatch.EvClosed, Ticket: id, Detail: "abc1234 merged"})
	}
	m.active = map[string]dispatch.Status{}
	for i := range 3 {
		id := fmt.Sprintf("kinieta-w%d", i)
		m.active[id] = dispatch.Status{Ticket: id, Title: "Competing timelines on the same view and property fight each other every frame",
			Started: time.Now(), Agent: "working", Activity: "⏺ Bash(scripts/ci-local.sh)"}
	}
	for _, size := range [][2]int{{140, 16}, {66, 12}, {40, 8}, {66, 24}, {120, 50}} {
		m.width, m.height = size[0], size[1]
		v := m.View()
		lines := strings.Split(v, "\n")
		if len(lines) > m.height {
			t.Errorf("%dx%d: view is %d lines", size[0], size[1], len(lines))
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > m.width {
				t.Errorf("%dx%d: line %d wide", size[0], size[1], ansi.StringWidth(l))
			}
		}
		plain := ansi.Strip(v)
		if !strings.Contains(plain, "kinieta-w0") || (m.width >= 60 && !strings.Contains(plain, "3 of 3") && !strings.Contains(plain, "3/3")) {
			t.Errorf("%dx%d: workers not shown:\n%s", size[0], size[1], plain)
		}
	}
}

func TestTriageLineIsPurpleDiamondWithCause(t *testing.T) {
	at := time.Date(2026, 9, 28, 17, 0, 0, 0, time.Local)
	line := ansi.Strip(renderEvent(dispatch.Event{Time: at, Kind: dispatch.EvTriage, Ticket: "kinieta-jqm", Detail: "environment · high", Title: "visionOS runtime missing"}))
	if line != "17:00:00 ◆ kinieta-jqm triage: environment · high  visionOS runtime missing" {
		t.Errorf("line = %q", line)
	}
}

// The title line says which solo ticket runs alone, or waits to, and forgets it once it is done.
func TestDashboardShowsTheSoloTicket(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch", Concurrency: 3}, func() {}, func(bool) {}, func(string) {})
	m.width, m.height = 120, 30
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-a", Title: "A ticket"},
		dispatch.Event{Kind: dispatch.EvQueue, Queued: 2, Solo: dispatch.SoloState{Ticket: "k-s", Next: true}})
	if v := ansi.Strip(m.titleLine(m.width)); !strings.Contains(v, "solo k-s next") {
		t.Errorf("title line %q should say k-s waits to run alone", v)
	}
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: 2, Ticket: "k-s", Title: "Split", Queued: 1, Solo: dispatch.SoloState{Ticket: "k-s"}})
	if v := ansi.Strip(m.titleLine(m.width)); !strings.Contains(v, "solo k-s running") {
		t.Errorf("title line %q should say k-s runs alone", v)
	}
	if line := ansi.Strip(renderEvent(dispatch.Event{Kind: dispatch.EvDispatch, N: 2, Limit: 40, Ticket: "k-s", Title: "Split", Solo: dispatch.SoloState{Ticket: "k-s"}})); !strings.Contains(line, "k-s solo  Split") {
		t.Errorf("dispatch line %q should mark k-s solo", line)
	}
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvQueue, Queued: 1})
	if v := ansi.Strip(m.titleLine(m.width)); strings.Contains(v, "solo") {
		t.Errorf("title line %q should drop the solo ticket once it is done", v)
	}
}

func TestCurrentLabelHeadsTheWorkers(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch", Concurrency: 3}, func() {}, func(bool) {}, func(string) {})
	m.active = map[string]dispatch.Status{}
	for i := range 3 {
		id := fmt.Sprintf("kinieta-w%d", i)
		m.active[id] = dispatch.Status{Ticket: id, Title: "Competing timelines", Started: time.Now().Add(-time.Duration(3-i) * time.Minute),
			Agent: "working", Activity: "⏺ Bash(scripts/ci-local.sh)"}
	}
	// labelAbove reports whether the line after the Current label opens the workers' box.
	labelAbove := func(v string) bool {
		lines := strings.Split(ansi.Strip(v), "\n")
		for i, l := range lines {
			if strings.TrimRight(l, " ") == "  Current" {
				return i+1 < len(lines) && strings.HasPrefix(lines[i+1], "╭")
			}
		}
		return false
	}
	m.width, m.height = 70, 40
	if v := m.View(); !labelAbove(v) || strings.Count(ansi.Strip(v), "╭") < 4 { // totals, then a box per worker
		t.Errorf("a box per worker should be headed Current:\n%s", ansi.Strip(v))
	}
	m.height = 16 // too short for a box each: one line per worker
	if v := m.View(); !labelAbove(v) || !strings.Contains(ansi.Strip(v), "kinieta-w2") || strings.Count(ansi.Strip(v), "╭") != 2 {
		t.Errorf("the one-line-per-worker box should be headed Current:\n%s", ansi.Strip(v))
	}
	// Title, totals on one line, the workers' box and the hint fill 9 lines: the label goes first.
	m.width, m.height = 40, 10
	if v := m.View(); !labelAbove(v) {
		t.Errorf("10 lines are room for the label:\n%s", ansi.Strip(v))
	}
	m.height = 9
	v := ansi.Strip(m.View())
	if strings.Contains(v, "Current") || !strings.Contains(v, "kinieta-w2") || !strings.Contains(v, "s stop after current") {
		t.Errorf("the label should be dropped before anything else:\n%s", v)
	}
	m.active = nil
	m.width, m.height = 70, 40
	if v := m.View(); !labelAbove(v) || !strings.Contains(ansi.Strip(v), "picking the next ticket") {
		t.Errorf("the idle box should be headed Current too:\n%s", ansi.Strip(v))
	}
}

func TestCurrentLabelIsBoldInTheWorkingColour(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch", Concurrency: 3}, func() {}, func(bool) {}, func(string) {})
	m.active = map[string]dispatch.Status{}
	for i := range 3 {
		id := fmt.Sprintf("kinieta-w%d", i)
		m.active[id] = dispatch.Status{Ticket: id, Title: "Competing timelines", Started: time.Now(), Agent: "working"}
	}
	// Bold (1) and the exact dark-background cyan #22D3EE, which terminal themes can't remap.
	label := "  \x1b[1;38;2;34;211;238mCurrent\x1b[0m"
	for _, c := range []struct {
		layout string
		height int
		boxes  int // the totals' and the workers'
	}{{"a box per worker", 40, 4}, {"one line per worker", 16, 2}} {
		m.width, m.height = 70, c.height
		if v := m.View(); !strings.Contains(v, label) || strings.Count(ansi.Strip(v), "╭") != c.boxes {
			t.Errorf("with %s the Current label should be bold cyan %q:\n%q", c.layout, label, v)
		}
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
	for l := range strings.SplitSeq(m.View(), "\n") {
		if ansi.StringWidth(l) > m.width {
			t.Errorf("line %d wide: %q", ansi.StringWidth(l), ansi.Strip(l))
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
