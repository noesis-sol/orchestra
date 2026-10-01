package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

func TestDashboardShowsSeveralWorkers(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch", Concurrency: 3}, func() {}, func(bool) {})
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
	for _, l := range strings.Split(v, "\n") {
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
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch", Concurrency: 2}, func() {}, func(bool) {})
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
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch", Concurrency: 1}, func() {}, func(bool) {})
	m.width, m.height = 100, 30
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvHold, Text: "PROBE: the run holds for the environment; in 10m …"},
		dispatch.Event{Kind: dispatch.EvProbed, Text: "PROBE_OK: a worker without a ticket ran a command 10m after the hold; taking tickets again"})
	if v := ansi.Strip(m.View()); strings.Contains(v, "stopping") {
		t.Errorf("a probe that ran its command should end the hold:\n%s", v)
	}
}

func TestDashboardFitsShortPanes(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch/2026-09-28", Concurrency: 3}, func() {}, func(bool) {})
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("kinieta-%03d", i)
		m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: i + 1, Ticket: id, Title: "A ticket"},
			dispatch.Event{Kind: dispatch.EvClosed, Ticket: id, Detail: "abc1234 merged"})
	}
	m.active = map[string]dispatch.Status{}
	for i := 0; i < 3; i++ {
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
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch", Concurrency: 3}, func() {}, func(bool) {})
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
