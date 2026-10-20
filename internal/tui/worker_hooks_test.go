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
	status := dispatch.Status{Ticket: "kinieta-ce1", Title: "Add a way to repeat a timeline", Started: time.Now(),
		Agent: dispatch.StateIdle, Permission: true}
	f := run(t).running(status).sized(90, 40)
	m := f.dash()
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "kinieta-ce1  permission — waiting for you") || !strings.Contains(fullTable(m), "■ allow?") ||
		strings.Contains(view, "idle") {
		t.Errorf("a worker waiting on a permission prompt should show it:\n%s", view)
	}
	if got := workerBorder(status); got != red {
		t.Errorf("its box should be red, as a blocked worker's is: %v", got)
	}

	m = f.status(dispatch.Status{Ticket: "kinieta-ce1", Title: "Add a way to repeat a timeline",
		Started: time.Now(), Agent: dispatch.StateWorking, Doing: dispatch.DoingSubagent}).dash()
	if view := ansi.Strip(m.View()); !strings.Contains(view, "kinieta-ce1  subagent") || !strings.Contains(fullTable(m), "▶ subagent") {
		t.Errorf("a worker running a subagent should show it:\n%s", view)
	}
}
