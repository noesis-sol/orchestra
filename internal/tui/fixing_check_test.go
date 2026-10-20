package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

func TestWorkerFixingItsCheckShowsFixingCheck(t *testing.T) {
	m := run(t).running(dispatch.Status{Ticket: "kinieta-ce1", Title: "Add a way to repeat a timeline", Started: time.Now(),
		Agent: "working", Doing: "testing", Fixing: true}).sized(70, 40).dash()
	if view := ansi.Strip(m.View()); !strings.Contains(fullTable(m), "⟳ fixing ") || !strings.Contains(view, "kinieta-ce1  fixing check") {
		t.Errorf("a worker fixing its check should show fixing check:\n%s", view)
	}
}
