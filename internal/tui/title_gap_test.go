package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// titleGapView is the dashboard of loadedDashboard at height h, its lines without styles or the
// spaces that pad them. The title's margin is its first line.
func titleGapView(t *testing.T, h int) []string {
	t.Helper()
	m := loadedDashboard(t, reading())
	m.height = h
	view := m.View()
	checkWithinPane(t, view, 120, h)
	lines := strings.Split(ansi.Strip(view), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines
}

// A blank line sets the title apart from the load line in a pane with room for it.
func TestABlankLineSetsTheTitleApart(t *testing.T) {
	// 23 lines hold the title, the load line, the strip, the tickets table and a line per worker, with
	// a line to spare; 17 the same without the table.
	for _, h := range []int{40, 23, 17} {
		lines := titleGapView(t, h)
		if !strings.Contains(lines[1], "Orchestra") || lines[2] != "" || !strings.Contains(lines[3], "▶ 4 of 4 workers") {
			t.Errorf("120x%d: want the title, a blank line and the load line:\n%s", h, strings.Join(lines, "\n"))
		}
	}
}

// A pane a line too short for the blank line has none, and loses nothing else for it: the load line,
// the strip, the table where there was one and every worker still show. The one-line totals of a
// shorter pane follow the title as they did.
func TestTheBlankLineUnderTheTitleGoesFirst(t *testing.T) {
	for _, tc := range []struct {
		h     int
		table bool
	}{{22, true}, {16, false}} {
		lines := titleGapView(t, tc.h)
		view := strings.Join(lines, "\n")
		if !strings.Contains(lines[1], "Orchestra") || !strings.Contains(lines[2], "▶ 4 of 4 workers") {
			t.Errorf("120x%d: want the load line right under the title:\n%s", tc.h, view)
		}
		if !strings.Contains(view, "Completed") {
			t.Errorf("120x%d: the strip is lost:\n%s", tc.h, view)
		}
		if top, _ := tableSpan(view); (top >= 0) != tc.table {
			t.Errorf("120x%d: a tickets table %v, want %v:\n%s", tc.h, top >= 0, tc.table, view)
		}
		for _, id := range []string{"k-1", "k-2", "k-3", "k-4"} {
			if !strings.Contains(view, id+"  working") {
				t.Errorf("120x%d: worker %s is lost:\n%s", tc.h, id, view)
			}
		}
	}
	lines := titleGapView(t, 12)
	if !strings.Contains(lines[1], "Orchestra") || !strings.Contains(lines[2], "▶ 4/4") {
		t.Errorf("120x12: want the one-line totals right under the title:\n%s", strings.Join(lines, "\n"))
	}
}
