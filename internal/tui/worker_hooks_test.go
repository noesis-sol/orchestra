package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A worker its hooks say waits on a permission prompt shows so, as waiting for the maintainer,
// whatever Herdr shows; one running a subagent shows that.
func TestWorkerShowsPermissionPromptsAndSubagents(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "kinieta-ce1", Title: "Add a way to repeat a timeline"})
	m.width, m.height = 90, 40
	status := dispatch.Status{Ticket: "kinieta-ce1", Title: "Add a way to repeat a timeline", Started: time.Now(),
		Agent: dispatch.StateIdle, Permission: true}
	m.active = map[string]dispatch.Status{"kinieta-ce1": status}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "kinieta-ce1  permission — waiting for you") || !strings.Contains(view, "■ allow?") ||
		strings.Contains(view, "idle") {
		t.Errorf("a worker waiting on a permission prompt should show it:\n%s", view)
	}
	if got := workerBorder(status); got != red {
		t.Errorf("its box should be red, as a blocked worker's is: %v", got)
	}

	m.active["kinieta-ce1"] = dispatch.Status{Ticket: "kinieta-ce1", Title: "Add a way to repeat a timeline",
		Started: time.Now(), Agent: dispatch.StateWorking, Doing: dispatch.DoingSubagent}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "kinieta-ce1  subagent") || !strings.Contains(view, "▶ subagent") {
		t.Errorf("a worker running a subagent should show it:\n%s", view)
	}
}
