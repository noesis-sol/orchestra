package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

func TestWorkerFixingItsCheckShowsFixingCheck(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "kinieta-ce1", Title: "Add a way to repeat a timeline"})
	m.width, m.height = 70, 40
	m.active = map[string]dispatch.Status{"kinieta-ce1": {Ticket: "kinieta-ce1", Title: "Add a way to repeat a timeline", Started: time.Now(),
		Agent: "working", Doing: "testing", Fixing: true}}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "⟳ fixing ") || !strings.Contains(view, "kinieta-ce1  fixing check") {
		t.Errorf("a worker fixing its check should show fixing check:\n%s", view)
	}
}
