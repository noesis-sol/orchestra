package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestParseReadySortsOpenTicketsByPriority(t *testing.T) {
	raw := `[
	  {"id":"a","title":"P3 first in output","status":"open","priority":3},
	  {"id":"b","title":"claimed","status":"in_progress","priority":0},
	  {"id":"c","title":"P1","status":"open","priority":1},
	  {"id":"d","title":"no priority","status":"open"},
	  {"id":"e","title":"second P3","status":"open","priority":3}
	]`
	got, err := parseReady([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, tk := range got {
		ids = append(ids, tk.ID)
	}
	if want := "c a e d"; strings.Join(ids, " ") != want {
		t.Errorf("order = %v, want %s", ids, want)
	}
	if got[0].Title != "P1" {
		t.Errorf("title = %q", got[0].Title)
	}
}

func TestParseReadyAcceptsTheEnvelopeAndEmptyResults(t *testing.T) {
	got, err := parseReady([]byte(`{"schema_version":2,"data":[{"id":"x","status":"open","priority":2}]}`))
	if err != nil || len(got) != 1 || got[0].ID != "x" {
		t.Errorf("envelope: %v %v", got, err)
	}
	if got, err := parseReady([]byte(`[]`)); err != nil || len(got) != 0 {
		t.Errorf("empty: %v %v", got, err)
	}
	if _, err := parseReady([]byte(`not json`)); err == nil {
		t.Error("garbage should be an error")
	}
	if _, err := parseReady(nil); err == nil {
		t.Error("no output should be an error")
	}
}

func TestParseStatus(t *testing.T) {
	cases := map[string]string{
		`[{"id":"x","status":"closed"}]`:               "closed",
		`{"id":"x","status":"deferred"}`:               "deferred",
		`{"data":[{"id":"x","status":"in_progress"}]}`: "in_progress",
		`[]`:                    "unknown",
		`[{"id":"x"}]`:          "unknown",
		``:                      "unknown",
		`error: no issue found`: "unknown",
	}
	for raw, want := range cases {
		if got := parseStatus([]byte(raw)); got != want {
			t.Errorf("parseStatus(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestOutcomes(t *testing.T) {
	for status, want := range map[string]outcome{
		"closed": outcomeClosed, "deferred": outcomeDeferred, "in_progress": outcomePaused,
		"unknown": outcomeUnreadable, "open": outcomeUnfinished, "blocked": outcomeUnfinished,
	} {
		if got := outcomeOf(status); got != want {
			t.Errorf("outcomeOf(%q) = %v, want %v", status, got, want)
		}
	}
	if closedOutcomeOf("", false) != closedNoCommit || closedOutcomeOf("", true) != closedNoCommit {
		t.Error("a closed ticket without a commit must not merge")
	}
	if closedOutcomeOf("abc123 fix", true) != closedDirty {
		t.Error("a dirty worktree must not merge")
	}
	if closedOutcomeOf("abc123 fix", false) != closedMerge {
		t.Error("a commit and a clean worktree should merge")
	}
}

func TestParseWorktreeOf(t *testing.T) {
	porcelain := "worktree /repo\nHEAD 111\nbranch refs/heads/main\n\n" +
		"worktree /wt/kinieta-abc\nHEAD 222\nbranch refs/heads/wt/kinieta-abc\n\n" +
		"worktree /wt/detached\nHEAD 333\ndetached\n"
	if got := parseWorktreeOf(porcelain, "wt/kinieta-abc"); got != "/wt/kinieta-abc" {
		t.Errorf("got %q", got)
	}
	if got := parseWorktreeOf(porcelain, "wt/kinieta"); got != "" {
		t.Errorf("a prefix must not match, got %q", got)
	}
}

func TestLastActivitySkipsTheInputBoxAndStatusBar(t *testing.T) {
	screen := strings.Join([]string{
		"⏺ Read(Package.swift)",
		"  ⎿  Read 40 lines",
		"⏺ Bash(scripts/ci-local.sh lint ios)",
		"  ⎿  lint passed",
		"✻ Running checks… (1m 12s · ↓ 2.1k tokens)",
		"",
		"────────────────",
		"❯ ",
		"────────────────",
		"  ⏵⏵ auto mode on (shift+tab to cycle)",
	}, "\n")
	if got := lastActivity(screen); !strings.HasPrefix(got, "✻ Running checks") {
		t.Errorf("got %q", got)
	}
	if got := lastActivity("❯ \n  status bar"); got != "" {
		t.Errorf("no activity should give empty, got %q", got)
	}
}

func TestRenderEventShowsTitleOnPickupAndOnlyTheIDOnCompletion(t *testing.T) {
	at := time.Date(2026, 9, 28, 16, 6, 27, 0, time.Local)
	picked := ansi.Strip(renderEvent(Event{Time: at, Kind: EvDispatch, N: 3, Limit: 40,
		Ticket: "kinieta-dg4", Title: "Decide whether the next release is pushed to CocoaPods trunk"}))
	if picked != "16:06:27 ▶ [3/40] kinieta-dg4  Decide whether the next release is pushed to CocoaPods trunk" {
		t.Errorf("picked = %q", picked)
	}
	done := ansi.Strip(renderEvent(Event{Time: at, Kind: EvClosed, Ticket: "kinieta-2e7",
		Title: "should not appear", Detail: "04c8d47 merged into batch"}))
	if done != "16:06:27 ✓ kinieta-2e7 completed  04c8d47 merged into batch" {
		t.Errorf("completed = %q", done)
	}
}

func TestNotifiable(t *testing.T) {
	if !notifiable("  kinieta-x closed (abc); merged") || !notifiable("PAUSED: x") {
		t.Error("closed and PAUSED should notify")
	}
	if notifiable("[1/40] kinieta-x dispatching: Title") || notifiable("  worktree /a on wt/x") {
		t.Error("dispatch and worktree lines should not notify")
	}
}

func TestTicketLinesUseExactColours(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	// Exact colours are sent as 38;2;R;G;B. Palette slots (38;5;N or 3N) are remapped by themes.
	for name, line := range map[string]string{
		"picked":    renderEvent(Event{Kind: EvDispatch, N: 1, Limit: 40, Ticket: "kinieta-jqm", Title: "Support visionOS"}),
		"completed": renderEvent(Event{Kind: EvClosed, Ticket: "kinieta-jqm", Detail: "abc merged"}),
	} {
		if !strings.Contains(line, "38;2;") || strings.Contains(line, "38;5;") {
			t.Errorf("%s line does not use an exact colour: %q", name, line)
		}
	}
	picked := renderEvent(Event{Kind: EvDispatch, Ticket: "x"})
	done := renderEvent(Event{Kind: EvClosed, Ticket: "x"})
	if picked[strings.Index(picked, "38;2;"):][:16] == done[strings.Index(done, "38;2;"):][:16] {
		t.Error("picked and completed lines should differ in colour")
	}
}

func TestViewFitsThePaneWidth(t *testing.T) {
	m := newModel(Config{Limit: 40, Base: "batch/2026-09-28"}, func() {})
	m.n, m.closed, m.deferred, m.queued = 3, 2, 1, 17
	m.began = time.Now().Add(-12 * time.Minute)
	m.st = Status{Ticket: "kinieta-y6j", Title: "Warn in debug builds when a chain call is silently ignored",
		Tab: "w2B:t9", Started: time.Now().Add(-134 * time.Second), Agent: "working",
		Activity: "⏺ Bash(scripts/ci-local.sh lint ios && git status --short && git diff --stat)"}
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
	m.st = Status{}
	m.width = 66
	if v := ansi.Strip(m.View()); !strings.Contains(v, "picking the next ticket") {
		t.Errorf("idle view: %s", v)
	}
}

func TestInputHoldsOnlyAnUnsentPrompt(t *testing.T) {
	prompt := "You are responsible for exactly one Beads ticket: kinieta-vzg. Do not work on any other ticket.\n" +
		"- If you cannot finish: note why on the ticket, defer it, and stop.\n\nWhen you are finished, say DONE and stop.\n"
	chrome := "\n─────────\n  ⏵⏵ auto mode on (shift+tab to cycle)"
	cases := map[string]bool{
		// Unsent, as Claude Code shows a long paste: only its last lines.
		"────────\n❯ - If you cannot finish: note why on the ticket, defer it, and stop.\n  When you are finished, say DONE and stop.\n────────\n  paste again to expand": true,
		// Unsent: the opening words, or the placeholder for a long paste.
		"❯ You are responsible for exactly one Beads ticket: kinieta-vzg. Do not" + chrome: true,
		"❯ [Pasted text #1 +20 lines]" + chrome:                                            true,
		// Sent: the transcript echoes the prompt above an empty input box.
		"❯ You are responsible for exactly one Beads ticket: kinieta-vzg.\n⏺ Claiming it.\n─────────\n❯ " + chrome: false,
		// Empty box, or Claude Code's suggestion placeholder.
		"❯ " + chrome: false,
		"❯ Try \"how do I log an error?\"" + chrome: false,
		// A dialog's selected option is not the input box's prompt.
		"❯ No, exit\n   Yes, I trust this folder": false,
		"": false,
	}
	for screen, want := range cases {
		if got := inputHolds(screen, prompt); got != want {
			t.Errorf("inputHolds(%q) = %v, want %v", screen, got, want)
		}
	}
}

func runEvents(m model, evs ...Event) model {
	for _, ev := range evs {
		next, _ := m.Update(eventMsg(ev))
		m = next.(model)
	}
	return m
}

func TestTicketRowsFollowEachTicket(t *testing.T) {
	m := newModel(Config{Limit: 40, Base: "batch"}, func() {})
	m = runEvents(m,
		Event{Kind: EvDispatch, N: 1, Ticket: "kinieta-dwv", Title: "Reduce Motion: keep fades"},
		Event{Kind: EvClosed, Ticket: "kinieta-dwv", Detail: "ffd6ce4 merged into batch"},
		Event{Kind: EvDispatch, N: 2, Ticket: "kinieta-vzg", Title: "Competing timelines"},
		Event{Kind: EvDeferred, Ticket: "kinieta-vzg", Detail: "still open, noted for review"},
		Event{Kind: EvTriage, Ticket: "kinieta-vzg", Detail: "environment · high", Title: "prompt never submitted"},
		Event{Kind: EvDispatch, N: 3, Ticket: "kinieta-kco", Title: "Open the property model"},
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

	m = runEvents(m, Event{Kind: EvStop, Text: "PAUSED: kinieta-kco"})
	final := ansi.Strip(m.View())
	if !strings.Contains(final, "■ stopped") || strings.Contains(final, "ctrl+c stops") {
		t.Errorf("final view should keep the summary and drop the live parts:\n%s", final)
	}
}

func TestViewFitsThePaneHeight(t *testing.T) {
	m := newModel(Config{Limit: 40, Base: "batch/2026-09-28"}, func() {})
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("kinieta-%03d", i)
		m = runEvents(m, Event{Kind: EvDispatch, N: i + 1, Ticket: id, Title: "A ticket title long enough to need truncating in a narrow pane"},
			Event{Kind: EvClosed, Ticket: id, Detail: "abc1234 merged into batch/2026-09-28"})
	}
	m.st = Status{Ticket: "kinieta-029", Title: "t", Tab: "w2B:t9", Started: time.Now(), Agent: "working", Activity: "⏺ Bash(scripts/ci-local.sh)"}
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

func TestQuestionsAreNeverDispatched(t *testing.T) {
	raw := `[{"id":"q","title":"Decision for k-1: MIT or Apache?","status":"open","priority":1,"labels":["human"]},
	         {"id":"k-2","title":"work","status":"open","priority":2,"labels":["api"]}]`
	got, err := parseReady([]byte(raw))
	if err != nil || len(got) != 1 || got[0].ID != "k-2" {
		t.Errorf("got %+v %v; a human-labelled question must be skipped", got, err)
	}
}

func TestOpenQuestionFromBdShow(t *testing.T) {
	// The shape of 'bd show --json': dependencies carry their own status and labels.
	raw := `[{"id":"k-1","status":"open","labels":["legal"],"dependencies":[
	  {"id":"k-0","status":"closed","labels":["refactor"],"dependency_type":"blocks"},
	  {"id":"q-1","title":"Decision for k-1: MIT or Apache?","status":"open","labels":["human"],"dependency_type":"blocks"}]}]`
	tk, ok := parseTicket([]byte(raw))
	if !ok || tk.Status != "open" {
		t.Fatalf("parseTicket: %v %+v", ok, tk)
	}
	if q := openQuestion(tk); q == nil || q.ID != "q-1" {
		t.Errorf("open question = %+v", q)
	}
	tk.Dependencies[1].Status = "closed" // answered
	if q := openQuestion(tk); q != nil {
		t.Errorf("an answered question should not block: %+v", q)
	}
	if _, ok := parseTicket([]byte("error: not found")); ok {
		t.Error("unreadable output should not parse")
	}
}

func TestIdleWorkerWithTicketInProgressGetsGrace(t *testing.T) {
	cases := []struct {
		status string
		idle   time.Duration
		wait   bool
	}{
		{"in_progress", time.Minute, true},              // probably waiting on its own background command
		{"in_progress", idleGrace + time.Second, false}, // long enough: it needs someone
		{"closed", 0, false}, {"deferred", 0, false}, {"open", 0, false},
	}
	for _, c := range cases {
		if got := keepWaiting(c.status, c.idle); got != c.wait {
			t.Errorf("keepWaiting(%s, %s) = %v, want %v", c.status, c.idle, got, c.wait)
		}
	}
}

func TestAskedTicketIsCountedAndShown(t *testing.T) {
	m := newModel(Config{Limit: 40, Base: "batch"}, func() {})
	m = runEvents(m,
		Event{Kind: EvDispatch, N: 1, Ticket: "k-1", Title: "Choose the licence"},
		Event{Kind: EvAsked, Ticket: "k-1", Detail: "q-1: Decision for k-1: MIT or Apache?"})
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

	m := newModel(Config{Limit: 40, Base: "batch"}, func() {})
	m.width, m.height = 66, 40
	m.st = Status{Ticket: "kinieta-vzg", Title: title, Started: time.Now(), Agent: "working", Activity: "✻ Cooking… (8m 10s)"}
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
