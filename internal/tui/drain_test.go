package tui

import (
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
		func(on bool) { *calls = append(*calls, on) })
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
	if v := ansi.Strip(m.View()); !strings.Contains(v, "s stops after current · ctrl+c stops now") {
		t.Errorf("hint missing:\n%s", v)
	}

	m = press(m, "s")
	v := ansi.Strip(m.View())
	for _, want := range []string{"Stop after the running tickets?", "No new tickets will start. 2 running (k-1, k-2) will", "y stop after current"} {
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
	for _, want := range []string{"· stopping after current", "s keeps going · ctrl+c stops now"} {
		if !strings.Contains(v, want) {
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
	if v := ansi.Strip(m.View()); strings.Contains(v, "stopping after current") || !strings.Contains(v, "s stops after current") {
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
		if !strings.Contains(v, "ctrl+c") {
			t.Errorf("%dx%d: the hint is gone:\n%s", size[0], size[1], v)
		}
		t.Logf("%dx%d:\n%s", size[0], size[1], v)
	}
}

// A drain asked with SIGUSR1 reaches the dashboard through the loop's events.
func TestDrainEventsShowOnTheDashboard(t *testing.T) {
	var calls []bool
	cancelled := false
	m := runEvents(drainDashboard(&calls, &cancelled), dispatch.Event{Kind: dispatch.EvDrain, Text: "DRAIN: stopping after the 2 running tickets (k-1, k-2), asked by SIGUSR1"})
	if v := ansi.Strip(m.View()); !strings.Contains(v, "stopping after current") {
		t.Errorf("drain not shown:\n%s", v)
	}
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvResume, Text: "DRAIN cancelled"})
	if v := ansi.Strip(m.View()); strings.Contains(v, "stopping after current") {
		t.Errorf("resume not shown:\n%s", v)
	}
	if len(calls) != 0 {
		t.Errorf("the loop's own events called drain: %v", calls)
	}
}
