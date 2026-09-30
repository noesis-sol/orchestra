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
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch", Concurrency: 3}, func() {})
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
	for _, want := range []string{"kinieta-0", "kinieta-1", "kinieta-2", "workers 3/3"} {
		if !strings.Contains(plain, want) {
			t.Errorf("view lacks %q", want)
		}
	}
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvHold, Ticket: "kinieta-0", Text: "HOLD: PAUSED: kinieta-0 …"})
	if !strings.Contains(ansi.Strip(m.View()), "stopping") {
		t.Error("a hold should show the run as stopping")
	}
}

func TestDashboardFitsShortPanes(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch/2026-09-28", Concurrency: 3}, func() {})
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
