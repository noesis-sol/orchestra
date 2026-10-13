package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Workers whose tabs are out of orchestra's sight, in a tmux session it doesn't run in: the numbers
// do nothing and the hint leaves them out, and the dashboard says how to see the tabs instead.
func TestWorkersOutOfSightHaveNoNumberKeys(t *testing.T) {
	var focused []string
	m := numberedDashboard(2, &focused)
	m.focus = nil
	m.cfg.Watch = "tmux attach -t orchestra-app"
	m = press(m, "1", "2")
	if len(focused) != 0 {
		t.Errorf("the numbers focused %v", focused)
	}
	if got := ansi.Strip(m.hintLine(80)); got != "  s to stop after the current tickets" {
		t.Errorf("hint %q", got)
	}
	v := ansi.Strip(m.View())
	lines := strings.Split(v, "\n")
	if len(lines) < 2 || strings.TrimRight(lines[len(lines)-2], " ") != " to see the workers' tabs: tmux attach -t orchestra-app" ||
		strings.TrimRight(lines[len(lines)-1], " ") != "  s to stop after the current tickets" {
		t.Errorf("the dashboard doesn't say how to see the tabs above its hint:\n%s", v)
	}
	if n := strings.Count(v, "\n") + 1; n > m.height {
		t.Errorf("the view takes %d lines of %d", n, m.height)
	}
}
