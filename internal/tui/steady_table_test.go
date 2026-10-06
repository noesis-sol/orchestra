package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// tableSpan is the first line of the tickets table in view and how many lines it takes; -1 and 0
// when the view has none.
func tableSpan(view string) (top, height int) {
	lines := strings.Split(ansi.Strip(view), "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, "│ Tickets") {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "╰") {
				return i - 1, j - i + 2
			}
		}
	}
	return -1, 0
}

// boxHead matches the line below a box's top border that heads a worker's box or the list of them.
var boxHead = regexp.MustCompile(`^│ [\d ] \S+ +k-\d+`)

// workerForm is how the workers show in view: whether the Current label heads them, and how many
// boxes they take, a row of two counting once.
func workerForm(view string) string {
	lines := strings.Split(ansi.Strip(view), "\n")
	label, boxes := false, 0
	for i, l := range lines {
		label = label || strings.TrimRight(l, " ") == "  Current"
		if strings.HasPrefix(l, "╭") && i+1 < len(lines) && boxHead.MatchString(lines[i+1]) {
			boxes++
		}
	}
	return fmt.Sprintf("label %v, %d boxes", label, boxes)
}

func setStatus(m Dashboard, st dispatch.Status) Dashboard {
	next, _ := m.Update(statusMsg(st))
	return next.(Dashboard)
}

// steadyRun takes a dashboard w by h through a run, calling look after each step: workers start,
// report, the run winds down and goes on again, workers finish and tickets pile up.
func steadyRun(w, h int, look func(step string, m Dashboard)) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch/2026-10-06", Concurrency: 3}, func() {},
		func(bool) {}, func(string) {})
	m.width, m.height = w, h
	look("idle", m)
	now := time.Now()
	titles := []string{"Add a way to repeat a timeline", "Open the property model",
		"Competing timelines on the same view and property fight each other every frame, so cut them"}
	for i, title := range titles {
		id := fmt.Sprintf("k-%d", i+1)
		m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: i + 1, Ticket: id, Title: title})
		m = setStatus(m, dispatch.Status{Ticket: id, Title: title, Started: now.Add(time.Duration(i) * time.Second),
			Agent: dispatch.StateWorking})
		look(id+" starts", m)
	}
	for i, title := range titles {
		id := fmt.Sprintf("k-%d", i+1)
		m = setStatus(m, dispatch.Status{Ticket: id, Title: title, Started: now.Add(time.Duration(i) * time.Second),
			Agent: dispatch.StateWorking, Doing: "testing", Activity: "⏺ Bash(scripts/check.sh)"})
		look(id+" reports", m)
	}
	m = press(m, "s", "y")
	look("winding down", m)
	m = press(m, "s", "y")
	look("going on", m)
	m = setStatus(m, dispatch.Status{Ticket: "k-1", Gone: true})
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvClosed, Ticket: "k-1", Detail: "abc1234 merged"})
	look("k-1 finishes", m)
	for i := range 12 {
		id := fmt.Sprintf("k-%d", i+10)
		m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: i + 4, Ticket: id, Title: "Done meanwhile"},
			dispatch.Event{Kind: dispatch.EvClosed, Ticket: id, Detail: "def5678 merged"})
	}
	look("12 more tickets", m)
	m = setStatus(m, dispatch.Status{Ticket: "k-2", Gone: true})
	m = setStatus(m, dispatch.Status{Ticket: "k-3", Gone: true})
	look("all finish", m)
}

// The tickets table's height comes from the pane's: about a third of it, for 3 to 10 tickets, and
// blank rows fill it while there are fewer.
func TestTicketsTableHeightComesFromThePane(t *testing.T) {
	for h, want := range map[int]int{1: 7, 12: 7, 21: 7, 24: 8, 30: 10, 40: 13, 42: 14, 45: 14, 100: 14} {
		if got := tableLines(h); got != want {
			t.Errorf("a pane %d tall: the table takes %d lines, want %d", h, got, want)
		}
	}
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	for n := range 16 {
		for _, lines := range []int{7, 10, 14} {
			table := m.ticketsTable(80, lines)
			if got := lipgloss.Height(table); got != lines {
				t.Errorf("%d tickets in %d lines: the table takes %d:\n%s", n, lines, got, ansi.Strip(table))
			}
			plain := ansi.Strip(table)
			if more := n > lines-4; more != strings.Contains(plain, "earlier tickets") {
				t.Errorf("%d tickets in %d lines: the earlier ones are counted: %v, want %v:\n%s", n, lines,
					!more, more, plain)
			}
		}
		m.rows = append(m.rows, ticketRow{id: fmt.Sprintf("k-%02d", n), title: "A ticket", state: rowDone,
			note: "abc1234 merged"})
	}
}

// With the pane's size fixed, the tickets table keeps its place and height while workers start,
// report and finish, the winding-down line comes and goes, and tickets pile up.
func TestTicketsTableStaysPut(t *testing.T) {
	for _, size := range [][2]int{{80, 40}, {135, 45}, {66, 30}, {100, 24}, {140, 36}, {50, 28}, {120, 22}} {
		w, h := size[0], size[1]
		first := ""
		steadyRun(w, h, func(step string, m Dashboard) {
			view := m.View()
			checkWithinPane(t, view, w, h)
			top, height := tableSpan(view)
			span := fmt.Sprintf("line %d, %d lines", top, height)
			switch {
			case top < 0:
				t.Errorf("%dx%d, %s: no tickets table:\n%s", w, h, step, ansi.Strip(view))
			case height != tableLines(h):
				t.Errorf("%dx%d, %s: the table takes %d lines, want %d:\n%s", w, h, step, height, tableLines(h),
					ansi.Strip(view))
			case first == "":
				first = span
			case span != first:
				t.Errorf("%dx%d, %s: the table is at %s, was at %s:\n%s", w, h, step, span, first, ansi.Strip(view))
			}
		})
	}
}

// A pane too short for the table and a line per worker the run may have has no table, whatever the
// workers do, and the totals, the workers and the hint still show.
func TestShortPaneDropsTheTableForGood(t *testing.T) {
	for _, size := range [][2]int{{80, 20}, {135, 15}, {66, 12}, {40, 9}} {
		w, h := size[0], size[1]
		steadyRun(w, h, func(step string, m Dashboard) {
			view := m.View()
			checkWithinPane(t, view, w, h)
			if top, _ := tableSpan(view); top >= 0 {
				t.Errorf("%dx%d, %s: a tickets table:\n%s", w, h, step, ansi.Strip(view))
			}
			if plain := ansi.Strip(view); !strings.Contains(plain, " s ") && !strings.Contains(plain, "s to") {
				t.Errorf("%dx%d, %s: no hint:\n%s", w, h, step, plain)
			}
		})
	}
}

// A worker's box keeps its height as the worker reports, with a line kept for its latest action,
// and the boxes are all as tall as the tallest.
func TestWorkerBoxesKeepTheirHeight(t *testing.T) {
	long := "Competing timelines on the same view and property fight each other every frame, so cut them"
	var focused []string
	for _, w := range []int{66, 119, 120, 135} {
		for n := 1; n <= 5; n++ {
			m := numberedDashboard(n, &focused)
			st := m.active["k-01"]
			st.Title = long
			m.active["k-01"] = st
			quiet := m.workerPanels(w)
			for id, st := range m.active {
				st.Activity = "⏺ Bash(go test ./...)"
				m.active[id] = st
			}
			busy := m.workerPanels(w)
			if lipgloss.Height(quiet) != lipgloss.Height(busy) {
				t.Errorf("%d wide, %d workers: the boxes take %d lines, %d once they report:\n%s\n%s", w, n,
					lipgloss.Height(quiet), lipgloss.Height(busy), ansi.Strip(quiet), ansi.Strip(busy))
			}
			var heights []int
			top := map[int]int{}
			for i, corners := range boxCorners(busy) {
				for col, r := range corners {
					switch r {
					case '╭':
						top[col] = i
					case '╰':
						heights = append(heights, i-top[col]+1)
					}
				}
			}
			for _, height := range heights {
				if height != heights[0] {
					t.Errorf("%d wide, %d workers: boxes %v lines tall, want all the same:\n%s", w, n, heights,
						ansi.Strip(busy))
					break
				}
			}
		}
	}
}

// With the pane's size and the workers fixed, the workers keep their form, a box each or a line
// each, as they report and as the winding-down line comes and goes; and so does the table. A pane
// too short for the table makes the most of the lines the foot leaves, the label first to go.
func TestWorkerAreaChangesOnlyWithTheWorkers(t *testing.T) {
	if testing.Short() {
		t.Skip("draws the dashboard at every height")
	}
	for _, w := range []int{50, 66, 135} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			t.Parallel()
			for h := 12; h <= 45; h++ {
				for n := 1; n <= 5; n++ {
					var focused []string
					m := numberedDashboard(n, &focused)
					m.cfg.Concurrency = 3
					m.width, m.height = w, h
					for i := range 4 {
						m.rows = append(m.rows, ticketRow{id: fmt.Sprintf("k-%02d", i+20), title: "Done", state: rowDone})
					}
					look := func(m Dashboard) string {
						view := m.View()
						checkWithinPane(t, view, w, h)
						top, height := tableSpan(view)
						return fmt.Sprintf("%s; table at line %d, %d lines", workerForm(view), top, height)
					}
					quiet := look(m)
					for id, st := range m.active {
						st.Activity = "⏺ Bash(go test ./...)"
						m.active[id] = st
					}
					if got := look(m); got != quiet {
						t.Errorf("%dx%d, %d workers: %s once they report, was %s:\n%s", w, h, n, got, quiet,
							ansi.Strip(m.View()))
					}
					m = press(m, "s", "y")
					if got := look(m); got != quiet && !strings.HasSuffix(quiet, "line -1, 0 lines") {
						t.Errorf("%dx%d, %d workers: %s winding down, was %s:\n%s", w, h, n, got, quiet,
							ansi.Strip(m.View()))
					}
				}
			}
		})
	}
}

// Nothing is taller or wider than the pane, at any size from 0 to 140 columns and 1 to 45 lines,
// with 0 to 11 workers, reporting or not, winding down or not. Each size is drawn once, with a
// number of workers and a state that change from size to size, so all of them are drawn at sizes
// across the range.
func TestDashboardFitsEveryPane(t *testing.T) {
	if testing.Short() {
		t.Skip("draws the dashboard at every size")
	}
	var dashboards [][3]Dashboard // by workers: quiet, reporting, winding down
	for n := 0; n <= 11; n++ {
		var focused []string
		m := numberedDashboard(n, &focused)
		m.cfg.Concurrency = max(n, 3)
		for i := range 15 {
			m.rows = append(m.rows, ticketRow{id: fmt.Sprintf("k-%02d", i+20), title: "A ticket", state: rowDone,
				note: "abc1234 merged"})
		}
		for id, st := range m.active {
			st.Title = strings.Repeat("A long title that wraps ", 5)
			m.active[id] = st
		}
		busy := m
		busy.active = map[string]dispatch.Status{}
		for id, st := range m.active {
			st.Activity = "⏺ " + strings.Repeat("Bash(go test ./...) ", 10)
			busy.active[id] = st
		}
		dashboards = append(dashboards, [3]Dashboard{m, busy, press(busy, "s", "y")})
	}
	for w := 0; w <= 140; w++ {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			t.Parallel()
			for h := 1; h <= 45; h++ {
				n := (7*w + h) % len(dashboards)
				m := dashboards[n][(w+h)%3]
				m.width, m.height = w, h
				checkWithinPane(t, m.View(), w, h)
			}
		})
	}
}
