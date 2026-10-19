package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// aboutWidth is the room the tickets table leaves its last column's text: a title that long shows in
// full in a table as wide as asked, one a column longer is cut.
func TestAboutWidthIsTheLastColumnsRoom(t *testing.T) {
	for _, tc := range []struct{ w, idWidth, want int }{
		{80, minIDWidth, 52},
		{120, 10, 89},
		{60, 20, 19},
	} {
		got := aboutWidth(tc.w, tc.idWidth)
		if got != tc.want {
			t.Errorf("aboutWidth(%d, %d) = %d, want %d", tc.w, tc.idWidth, got, tc.want)
		}
		id := strings.Repeat("k", tc.idWidth)
		for _, extra := range []int{0, 1} {
			m := NewDashboard(dispatch.Config{}, func() {}, func(bool) {}, func(string) {})
			m.pickUp(id, strings.Repeat("t", got+extra))
			table := ansi.Strip(m.ticketsTable(tc.w, 3))
			if width := lipgloss.Width(table); width != tc.w {
				t.Errorf("a table %d wide, IDs %d: it is %d wide:\n%s", tc.w, tc.idWidth, width, table)
			}
			if cut := strings.Contains(table, "…"); cut != (extra > 0) {
				t.Errorf("a table %d wide, IDs %d, a title %d long: cut %v, want %v:\n%s", tc.w, tc.idWidth,
					got+extra, cut, extra > 0, table)
			}
		}
	}
}

// boxInner is the room inside a box: content that wide takes one line, a column more wraps.
func TestBoxInnerIsTheContentsRoom(t *testing.T) {
	for _, w := range []int{10, 36, 80} {
		if got := boxInner(w); got != w-boxChrome {
			t.Errorf("boxInner(%d) = %d, want %d", w, got, w-boxChrome)
		}
		for extra, lines := range []int{3, 4} {
			b := box(w, 0, grey, strings.Repeat("x", boxInner(w)+extra))
			if lipgloss.Width(b) != w || lipgloss.Height(b) != lines {
				t.Errorf("a box %d wide holding %d columns is %dx%d, want %dx%d:\n%s", w, boxInner(w)+extra,
					lipgloss.Width(b), lipgloss.Height(b), w, lines, b)
			}
		}
	}
}

// A total reads in the strip as its mark and count, faint at zero;
// on the one-line form always with its mark, and its slots after a slash.
func TestTotalCellAndPart(t *testing.T) {
	for _, tc := range []struct {
		name       string
		t          total
		cell, part string
	}{
		{"zero", total{mark: "✓", style: closedStyle}, "0", "✓ 0"},
		{"some", total{mark: "✓", n: 3, style: closedStyle}, "✓ 3", "✓ 3"},
		{"zero with verdicts", total{mark: "↷", style: deferredStyle, extra: " ◆ 2"}, "0 ◆ 2", "↷ 0"},
		{"some with verdicts", total{mark: "↷", n: 1, style: deferredStyle, extra: " ◆ 2"}, "↷ 1 ◆ 2", "↷ 1"},
		// The workers are on the one-line form alone, the strip leaving them to the load line.
		{"out of slots", total{mark: "▶", n: 2, style: pickedStyle, of: 4, lineOnly: true}, "", "▶ 2/4"},
		{"none of slots", total{mark: "▶", style: pickedStyle, of: 1, lineOnly: true}, "", "▶ 0/1"},
	} {
		if got := ansi.Strip(tc.t.cell()); !tc.t.lineOnly && got != tc.cell {
			t.Errorf("%s: cell %q, want %q", tc.name, got, tc.cell)
		}
		if got := ansi.Strip(tc.t.part()); got != tc.part {
			t.Errorf("%s: part %q, want %q", tc.name, got, tc.part)
		}
	}
}

// The queue's total is — until the loop says, then its count, and " of " the tickets not closed once
// the loop has said how many.
func TestQueueCount(t *testing.T) {
	for _, tc := range []struct {
		queued, unclosed int
		n, of            string
	}{
		{-1, -1, "—", ""},
		{-1, 5, "—", ""},
		{0, -1, "0", ""},
		{3, 7, "3", " of 7"},
	} {
		m := Dashboard{queued: tc.queued, unclosed: tc.unclosed}
		if n, of := m.queueCount(); n != tc.n || of != tc.of {
			t.Errorf("queued %d, unclosed %d: %q, %q, want %q, %q", tc.queued, tc.unclosed, n, of, tc.n, tc.of)
		}
	}
}

// The run's totals count from the dashboard's state, the workers out of its slots, one at the least.
func TestTotalsCountTheRun(t *testing.T) {
	m := Dashboard{closed: 2, triaged: 1, active: map[string]dispatch.Status{"k-9": {}}, rows: []ticketRow{
		{id: "k-1", state: rowDeferred}, {id: "k-2", state: rowReview}, {id: "k-3", state: rowAsked},
	}}
	var got []string
	for _, t := range m.totals() {
		got = append(got, ansi.Strip(t.part()))
	}
	if want := []string{"✓ 2", "↷ 2", "? 1", "▶ 1/1"}; strings.Join(got, " · ") != strings.Join(want, " · ") {
		t.Errorf("totals %q, want %q", got, want)
	}
	m.cfg.Concurrency = 3
	if s := m.slots(); s != 3 {
		t.Errorf("slots with concurrency 3: %d", s)
	}
}
