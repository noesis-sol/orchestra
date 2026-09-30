package main

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
	m := newModel(dispatch.Config{Limit: 40, Base: "batch/2026-09-28"}, func() {})
	m.n, m.closed, m.deferred, m.queued = 3, 2, 1, 17
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

func runEvents(m model, evs ...dispatch.Event) model {
	for _, ev := range evs {
		next, _ := m.Update(eventMsg(ev))
		m = next.(model)
	}
	return m
}

func TestTicketRowsFollowEachTicket(t *testing.T) {
	m := newModel(dispatch.Config{Limit: 40, Base: "batch"}, func() {})
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
	final := ansi.Strip(m.View())
	if !strings.Contains(final, "■ stopped") || strings.Contains(final, "ctrl+c stops") {
		t.Errorf("final view should keep the summary and drop the live parts:\n%s", final)
	}
}

func TestViewFitsThePaneHeight(t *testing.T) {
	m := newModel(dispatch.Config{Limit: 40, Base: "batch/2026-09-28"}, func() {})
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("kinieta-%03d", i)
		m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: i + 1, Ticket: id, Title: "A ticket title long enough to need truncating in a narrow pane"},
			dispatch.Event{Kind: dispatch.EvClosed, Ticket: id, Detail: "abc1234 merged into batch/2026-09-28"})
	}
	m.active = map[string]dispatch.Status{"x": {Ticket: "kinieta-029", Title: "t", Tab: "w2B:t9", Started: time.Now(), Agent: "working", Activity: "⏺ Bash(scripts/ci-local.sh)"}}
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
		if !strings.Contains(ansi.Strip(m.View()), "+") {
			t.Errorf("%dx%d: hidden tickets are not mentioned", size[0], size[1])
		}
		if size[0] == 66 {
			t.Logf("preview %dx%d:\n%s", size[0], size[1], ansi.Strip(m.View()))
		}
	}
}

func TestAskedTicketIsCountedAndShown(t *testing.T) {
	m := newModel(dispatch.Config{Limit: 40, Base: "batch"}, func() {})
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

	m := newModel(dispatch.Config{Limit: 40, Base: "batch"}, func() {})
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
