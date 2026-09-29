package main

import (
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
