package tui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// gridHead matches a worker box's first line: its number and ticket ID.
var gridHead = regexp.MustCompile(`(\d) +\S+ +(k-\d+)  working`)

// boxCorners is where the corners of the boxes are on each line of view: column, corner.
func boxCorners(view string) []map[int]rune {
	var lines []map[int]rune
	for _, l := range strings.Split(ansi.Strip(view), "\n") {
		corners := map[int]rune{}
		for col, r := range []rune(l) { // the test's text is one column a rune
			if strings.ContainsRune("╭╮╰╯", r) {
				corners[col] = r
			}
		}
		lines = append(lines, corners)
	}
	return lines
}

// gridHeads is the workers' numbers and IDs on each line of view that has any.
func gridHeads(view string) [][]string {
	var rows [][]string
	for _, l := range strings.Split(ansi.Strip(view), "\n") {
		var heads []string
		for _, s := range gridHead.FindAllStringSubmatch(l, -1) {
			heads = append(heads, s[1]+" "+s[2])
		}
		if heads != nil {
			rows = append(rows, heads)
		}
	}
	return rows
}

// In a pane 120 wide or wider the boxes go two to a row, numbered row by row; an odd one out keeps
// the left column's width.
func TestWideGridPutsTwoBoxesToARow(t *testing.T) {
	var focused []string
	cases := []struct {
		workers int
		want    string
	}{
		{1, "1 k-01"},
		{2, "1 k-01,2 k-02"},
		{3, "1 k-01,2 k-02/3 k-03"},
		{4, "1 k-01,2 k-02/3 k-03,4 k-04"},
		{5, "1 k-01,2 k-02/3 k-03,4 k-04/5 k-05"},
	}
	for _, w := range []int{120, 135, 200} {
		left := (w - 1) / 2
		for _, c := range cases {
			view := numberedDashboard(c.workers, &focused).workerPanels(w)
			var rows []string
			for _, heads := range gridHeads(view) {
				rows = append(rows, strings.Join(heads, ","))
			}
			if got := strings.Join(rows, "/"); got != c.want {
				t.Errorf("%d wide, %d workers: rows %q, want %q:\n%s", w, c.workers, got, c.want, ansi.Strip(view))
			}
			pair := map[int]rune{0: '╭', left - 1: '╮', left + 1: '╭', w - 1: '╮'}
			half := map[int]rune{0: '╭', left - 1: '╮'}
			var tops []map[int]rune
			for _, corners := range boxCorners(view) {
				if len(corners) > 0 && corners[0] == '╭' {
					tops = append(tops, corners)
				}
			}
			for i, top := range tops {
				want := pair
				if 2*i+1 == c.workers {
					want = half
				}
				if !sameCorners(top, want) {
					t.Errorf("%d wide, %d workers: row %d's boxes have corners at %v, want %v:\n%s",
						w, c.workers, i+1, top, want, ansi.Strip(view))
				}
			}
		}
	}
}

func sameCorners(a, b map[int]rune) bool {
	if len(a) != len(b) {
		return false
	}
	for col, r := range a {
		if b[col] != r {
			return false
		}
	}
	return true
}

// The two boxes in a row are equally tall, their borders on the same lines, whichever of them has
// the more lines: an activity line, or a title that wraps to a second line.
func TestWideGridRowsAreEquallyTall(t *testing.T) {
	long := "Lay the Current boxes out two to a row in a wide pane, so four workers show as a grid of two by two"
	cases := []struct {
		name     string
		tall     string // the worker with the more lines
		activity bool   // an activity line, else a title that wraps to a second line
	}{
		{"activity on the left", "k-01", true},
		{"activity on the right", "k-02", true},
		{"wrapped title on the left", "k-01", false},
		{"wrapped title on the right", "k-02", false},
	}
	var focused []string
	for _, w := range []int{120, 135, 200} {
		left := (w - 1) / 2
		for _, c := range cases {
			m := numberedDashboard(2, &focused)
			st := m.active[c.tall]
			if c.activity {
				st.Activity = "⏺ Bash(scripts/check.sh)"
			} else {
				st.Title = long[:min(len(long), 2*(left-6)-10)] // two lines, inside the two-line limit
			}
			m.active[c.tall] = st
			view := m.workerPanels(w)
			lines := strings.Split(ansi.Strip(view), "\n")
			if len(lines) != 5 { // the borders, the head, two lines of title or one and the activity
				t.Errorf("%d wide, %s: %d lines, want 5:\n%s", w, c.name, len(lines), ansi.Strip(view))
			}
			for i, l := range lines {
				r := []rune(l)
				lead, end := '│', '│'
				switch i {
				case 0:
					lead, end = '╭', '╮'
				case len(lines) - 1:
					lead, end = '╰', '╯'
				}
				if len(r) != w || r[0] != lead || r[left+1] != lead || r[left-1] != end || r[w-1] != end {
					t.Errorf("%d wide, %s: line %d doesn't have both boxes' borders in place: %q",
						w, c.name, i, l)
				}
			}
		}
	}
}

// Below 120 columns the boxes stack in one column, each the pane's width.
func TestNarrowPaneStacksTheBoxes(t *testing.T) {
	var focused []string
	for _, w := range []int{67, 119} {
		view := numberedDashboard(4, &focused).workerPanels(w)
		var rows []string
		for _, heads := range gridHeads(view) {
			rows = append(rows, strings.Join(heads, ","))
		}
		if got, want := strings.Join(rows, "/"), "1 k-01/2 k-02/3 k-03/4 k-04"; got != want {
			t.Errorf("%d wide: rows %q, want %q:\n%s", w, got, want, ansi.Strip(view))
		}
		for _, corners := range boxCorners(view) {
			if len(corners) > 0 && !sameCorners(corners, map[int]rune{0: '╭', w - 1: '╮'}) &&
				!sameCorners(corners, map[int]rune{0: '╰', w - 1: '╯'}) {
				t.Errorf("%d wide: a box isn't the pane's width, corners at %v:\n%s", w, corners, ansi.Strip(view))
			}
		}
	}
}

// The layout follows the pane's width on the next frame: two columns at 135, one at 67, two again.
func TestGridFollowsTheResize(t *testing.T) {
	var focused []string
	var m tea.Model = numberedDashboard(4, &focused)
	for _, step := range []struct {
		width   int
		columns int
	}{{135, 2}, {67, 1}, {135, 2}} {
		m, _ = m.Update(tea.WindowSizeMsg{Width: step.width, Height: 50})
		view := m.View()
		checkWithinPane(t, view, step.width, 50)
		heads := gridHeads(view)
		if len(heads) == 0 || len(heads[0]) != step.columns {
			t.Errorf("at %d columns: heads %v, want %d to a row:\n%s", step.width, heads, step.columns,
				ansi.Strip(view))
		}
	}
}

// No line of the view is wider than the pane, with one to five workers, nor taller.
func TestWideGridFitsThePane(t *testing.T) {
	var focused []string
	for _, w := range []int{120, 135, 200} {
		for n := 1; n <= 5; n++ {
			for _, h := range []int{60, 24, 12} {
				m := numberedDashboard(n, &focused)
				for id, st := range m.active {
					st.Title = strings.Repeat("A long title that wraps ", 10)
					st.Activity = "⏺ " + strings.Repeat("Bash(go test ./...) ", 20)
					m.active[id] = st
				}
				m.width, m.height = w, h
				checkWithinPane(t, m.View(), w, h)
			}
		}
	}
}
