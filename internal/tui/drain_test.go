package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// drainDashboard is a dashboard with two workers running, recording the drain calls.
func drainDashboard(calls *[]bool, cancelled *bool) Dashboard {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch", Concurrency: 2}, func() { *cancelled = true },
		func(on bool) { *calls = append(*calls, on) }, func(string) {})
	m.width, m.height = 80, 40
	now := time.Now()
	m.active = map[string]dispatch.Status{
		"k-1": {Ticket: "k-1", Title: "First", Started: now.Add(-2 * time.Minute), Agent: "working"},
		"k-2": {Ticket: "k-2", Title: "Second", Started: now.Add(-time.Minute), Agent: "working"},
	}
	return m
}

func press(m Dashboard, keys ...string) Dashboard {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "ctrl+c":
			msg = tea.KeyMsg{Type: tea.KeyCtrlC}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		next, _ := m.Update(msg)
		m = next.(Dashboard)
	}
	return m
}

func TestSAsksBeforeStoppingAfterTheRunningTickets(t *testing.T) {
	var calls []bool
	cancelled := false
	m := drainDashboard(&calls, &cancelled)
	if v := ansi.Strip(m.View()); !strings.Contains(v, "1–2 to go to a worker's tab · s to stop after the current tickets") {
		t.Errorf("hint missing:\n%s", v)
	}

	m = press(m, "s")
	v := ansi.Strip(m.View())
	for _, want := range []string{"Stop after the running tickets?", "Stopping after the 2 running tickets finish (k-1, k-2):", "y stop after current"} {
		if !strings.Contains(v, want) {
			t.Errorf("the question lacks %q:\n%s", want, v)
		}
	}
	if len(calls) != 0 {
		t.Errorf("drain called before the answer: %v", calls)
	}

	m = press(m, "y")
	if len(calls) != 1 || !calls[0] {
		t.Fatalf("drain calls %v, want [true]", calls)
	}
	v = ansi.Strip(m.View())
	joined := strings.Join(strings.Fields(v), " ") // the line wraps at 80 columns
	for _, want := range []string{"■ Stopping after the 2 running tickets finish (k-1, k-2): no new tickets will start", "s to keep taking tickets"} {
		if !strings.Contains(joined, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "Stop after the running tickets?") {
		t.Errorf("the question stayed open:\n%s", v)
	}

	// s again offers to go on.
	m = press(m, "s")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Keep taking tickets?") {
		t.Errorf("no offer to go on:\n%s", v)
	}
	m = press(m, "y")
	if len(calls) != 2 || calls[1] {
		t.Fatalf("drain calls %v, want [true false]", calls)
	}
	if v := ansi.Strip(m.View()); strings.Contains(v, "Stopping after") || strings.Contains(v, "held") || !strings.Contains(v, "s to stop after the current tickets") {
		t.Errorf("still winding down after going on:\n%s", v)
	}
	if cancelled {
		t.Error("the run was cancelled")
	}
}

func TestNoOrEscClosesTheQuestionWithoutAnything(t *testing.T) {
	for _, key := range []string{"n", "esc"} {
		var calls []bool
		cancelled := false
		m := press(drainDashboard(&calls, &cancelled), "s", key)
		v := ansi.Strip(m.View())
		if len(calls) != 0 || m.draining || strings.Contains(v, "Stop after the running tickets?") || strings.Contains(v, "stopping") {
			t.Errorf("%s: calls %v, view:\n%s", key, calls, v)
		}
		if m = press(m, "y"); len(calls) != 0 {
			t.Errorf("%s: y with the question closed called drain", key)
		}
	}
}

func TestCtrlCStopsAtOnceWithTheQuestionOpen(t *testing.T) {
	var calls []bool
	cancelled := false
	m := press(drainDashboard(&calls, &cancelled), "s", "ctrl+c")
	if !cancelled || !m.Interrupted() || len(calls) != 0 {
		t.Errorf("cancelled %v, interrupted %v, drain calls %v", cancelled, m.Interrupted(), calls)
	}
}

func TestDrainQuestionFitsTheWindow(t *testing.T) {
	var calls []bool
	cancelled := false
	for _, size := range [][2]int{{80, 40}, {40, 30}, {80, 10}, {60, 6}, {30, 5}} {
		m := press(drainDashboard(&calls, &cancelled), "s")
		m.width, m.height = size[0], size[1]
		view := m.View()
		lines := strings.Split(view, "\n")
		if len(lines) > m.height {
			t.Errorf("%dx%d: view is %d lines:\n%s", size[0], size[1], len(lines), ansi.Strip(view))
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > m.width {
				t.Errorf("%dx%d: line %d wide: %q", size[0], size[1], ansi.StringWidth(l), ansi.Strip(l))
			}
		}
		v := ansi.Strip(view)
		if !strings.Contains(v, "Stop after the running tickets?") && !strings.Contains(v, "Stop after current? y/n") {
			t.Errorf("%dx%d: the question is not shown:\n%s", size[0], size[1], v)
		}
		if !strings.Contains(v, "stop after") {
			t.Errorf("%dx%d: the hint is gone:\n%s", size[0], size[1], v)
		}
		t.Logf("%dx%d:\n%s", size[0], size[1], v)
	}
}

// A drain asked with SIGUSR1 reaches the dashboard through the loop's events.
func TestDrainEventsShowOnTheDashboard(t *testing.T) {
	var calls []bool
	cancelled := false
	m := runEvents(drainDashboard(&calls, &cancelled), dispatch.Event{Kind: dispatch.EvDrain, Text: "DRAIN: stopping after the 2 running tickets finish (k-1, k-2): no new tickets will start, asked by SIGUSR1"})
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Stopping after the 2 running tickets finish") {
		t.Errorf("drain not shown:\n%s", v)
	}
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvResume, Text: "DRAIN cancelled"})
	if v := ansi.Strip(m.View()); strings.Contains(v, "Stopping after") {
		t.Errorf("resume not shown:\n%s", v)
	}
	if len(calls) != 0 {
		t.Errorf("the loop's own events called drain: %v", calls)
	}
}

// Once confirmed, the dashboard says on a line of its own that the run winds down and after which
// tickets, whatever the pane: the IDs may be shortened, the words never. The hint says s keeps taking
// tickets and the queue is marked as held, until the stop is cancelled.
func TestWindingDownIsShownInAnyPane(t *testing.T) {
	// ids is how much of the running tickets' IDs shows: all of them, or the first and a cut.
	sizes := []struct {
		w, h      int
		hint, ids string
		held      bool
	}{
		{70, 40, "1–2 to go to a worker's tab · s to keep taking tickets", "orchestra-20w, orch", true},
		{40, 40, "1–2 worker tab · s keep going", "orchestra-20w, orchestra-a", true},
		{70, 8, "1–2 to go to a worker's tab · s to keep taking tickets", "orchestra-20w, orch", true},
		{40, 9, "1–2 worker tab · s keep going", "orchestra-20w, orchestra-a", true},
		{30, 12, "1–2 worker tab · s keep going", "orchestra-20w, orchestr", false}, // the totals line is cut at 30
		{30, 7, "1–2 worker tab · s keep going", "orchestra-20w, orchestr", false},
	}
	for _, size := range sizes {
		var calls []bool
		cancelled := false
		m := drainDashboard(&calls, &cancelled)
		now := time.Now()
		m.active = map[string]dispatch.Status{
			"orchestra-20w": {Ticket: "orchestra-20w", Title: "Settle a Claude worker at its Stop hook", Started: now.Add(-2 * time.Minute), Agent: "working"},
			"orchestra-a1b": {Ticket: "orchestra-a1b", Title: "Make every external command cancellable", Started: now.Add(-time.Minute), Agent: "working"},
		}
		m.cfg.Version = "v0.1.2-0.20261001072042-ec29c72a1b2c+dirty"
		m.cfg.Base = "batch/2026-10-01"
		m.queued = 4
		m.width, m.height = size.w, size.h
		for i := range 6 {
			m.rows = append(m.rows, ticketRow{id: fmt.Sprintf("orchestra-%03d", i), title: "Done earlier", state: rowDone, note: "abc1234 merged"})
		}
		m = press(m, "s", "y")

		view := m.View()
		lines := strings.Split(view, "\n")
		if len(lines) > m.height {
			t.Errorf("%dx%d: view is %d lines:\n%s", size.w, size.h, len(lines), ansi.Strip(view))
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > m.width {
				t.Errorf("%dx%d: line %d wide: %q", size.w, size.h, ansi.StringWidth(l), ansi.Strip(l))
			}
		}
		plain := ansi.Strip(view)
		// The message may be wrapped: compare it with the lines joined.
		joined := strings.Join(strings.Fields(plain), " ")
		message := "■ Stopping after the 2 running tickets finish (" + size.ids
		if !strings.Contains(joined, message) || !strings.Contains(joined, "): no new tickets will start") {
			t.Errorf("%dx%d: the winding-down line lacks %q … %q:\n%s", size.w, size.h, message, "): no new tickets will start", plain)
		}
		if !strings.Contains(plain, size.hint) {
			t.Errorf("%dx%d: the hint lacks %q:\n%s", size.w, size.h, size.hint, plain)
		}
		if size.held && !strings.Contains(plain, "4 · held") && !strings.Contains(plain, "queue 4 held") {
			t.Errorf("%dx%d: the queue is not marked as held:\n%s", size.w, size.h, plain)
		}
		t.Logf("%dx%d:\n%s", size.w, size.h, plain)

		m = press(m, "s", "y")
		plain = ansi.Strip(m.View())
		if strings.Contains(plain, "Stopping after") || strings.Contains(plain, "held") || strings.Contains(plain, "keep taking") || strings.Contains(plain, "keep going") {
			t.Errorf("%dx%d: still winding down after cancelling:\n%s", size.w, size.h, plain)
		}
		if size.w == 70 && !strings.Contains(plain, "s to stop after the current tickets") {
			t.Errorf("%dx%d: the normal hint is not back:\n%s", size.w, size.h, plain)
		}
	}
}

// As tickets finish, the line names those left; with none left it replaces the box saying the
// next ticket is being picked.
func TestWindingDownFollowsTheRunningTickets(t *testing.T) {
	var calls []bool
	cancelled := false
	m := press(drainDashboard(&calls, &cancelled), "s", "y")
	next, _ := m.Update(statusMsg(dispatch.Status{Ticket: "k-1", Gone: true}))
	m = next.(Dashboard)
	if v := ansi.Strip(m.View()); !strings.Contains(v, "■ Stopping after k-2 finishes: no new tickets will start") {
		t.Errorf("one ticket left:\n%s", v)
	}
	next, _ = m.Update(statusMsg(dispatch.Status{Ticket: "k-2", Gone: true}))
	m = next.(Dashboard)
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "■ Stopping now, as nothing is running") || strings.Contains(v, "picking the next ticket") {
		t.Errorf("none left:\n%s", v)
	}
}

func TestWrapAroundShortensOnlyTheIDs(t *testing.T) {
	got := wrapAround(" ■ Stopping after the 3 running tickets finish (", "orchestra-aaa, orchestra-bbb, orchestra-ccc", "): no new tickets will start", 40, "   ")
	want := []string{
		" ■ Stopping after the 3 running tickets",
		"   finish (orchestra-aaa, orchestra-b…):",
		"   no new tickets will start",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
