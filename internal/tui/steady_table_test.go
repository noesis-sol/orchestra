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

// tableSpan is the first line of the tickets table in view, its Tickets label when it has one, and
// how many lines it takes down to its bottom border; -1 and 0 when the view has none. The table is
// the box with columns that isn't the totals'.
func tableSpan(view string) (top, height int) {
	lines := strings.Split(ansi.Strip(view), "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, "╭") || !strings.Contains(l, "┬") ||
			i+1 < len(lines) && strings.Contains(lines[i+1], "Completed") {
			continue
		}
		top = i
		if i > 0 && strings.TrimRight(lines[i-1], " ") == "  Tickets" {
			top = i - 1
		}
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "╰") {
				return top, j - top + 1
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

// steadyRun takes a dashboard w by h through a run, calling look after each step: workers start,
// report, the run winds down and goes on again, workers finish and tickets pile up.
func steadyRun(t *testing.T, w, h int, look func(step string, m Dashboard)) {
	f := run(t).config(dispatch.Config{Limit: 40, Base: "batch/2026-10-06", Concurrency: 3}).sized(w, h)
	look("idle", f.dash())
	now := time.Now()
	titles := []string{"Add a way to repeat a timeline", "Open the property model",
		"Competing timelines on the same view and property fight each other every frame, so cut them"}
	for i, title := range titles {
		id := fmt.Sprintf("k-%d", i+1)
		look(id+" starts", f.running(dispatch.Status{Ticket: id, Title: title,
			Started: now.Add(time.Duration(i) * time.Second), Agent: dispatch.StateWorking}).dash())
	}
	for i, title := range titles {
		id := fmt.Sprintf("k-%d", i+1)
		look(id+" reports", f.status(dispatch.Status{Ticket: id, Title: title,
			Started: now.Add(time.Duration(i) * time.Second), Agent: dispatch.StateWorking, Doing: "testing",
			Activity: "⏺ Bash(scripts/check.sh)"}).dash())
	}
	look("winding down", f.keys("s", "y").dash())
	look("going on", f.keys("s", "y").dash())
	look("k-1 finishes", f.gone("k-1").closed("k-1", "abc1234 merged").dash())
	for i := range 12 {
		f.done(fmt.Sprintf("k-%d", i+10), "Done meanwhile", "def5678 merged")
	}
	look("12 more tickets", f.dash())
	look("all finish", f.gone("k-2", "k-3").dash())
}

// The tickets table's height comes from the pane's: about a third of it, for 3 to 10 tickets, and
// blank rows fill it while there are fewer.
func TestTicketsTableHeightComesFromThePane(t *testing.T) {
	for h, want := range map[int]int{1: 6, 12: 6, 21: 6, 24: 7, 30: 9, 40: 12, 42: 13, 45: 13, 100: 13} {
		if got := tableLines(h); got != want {
			t.Errorf("a pane %d tall: the table takes %d lines, want %d", h, got, want)
		}
	}
	f := run(t)
	for n := range 16 {
		m := f.dash()
		for _, lines := range []int{6, 9, 13} {
			table := m.tickets(80, lines)
			if got := lipgloss.Height(table); got != lines {
				t.Errorf("%d tickets in %d lines: the table takes %d:\n%s", n, lines, got, ansi.Strip(table))
			}
			plain := ansi.Strip(table)
			if more := n > lines-3; more != strings.Contains(plain, "earlier tickets") {
				t.Errorf("%d tickets in %d lines: the earlier ones are counted: %v, want %v:\n%s", n, lines,
					!more, more, plain)
			}
		}
		f.done(fmt.Sprintf("k-%02d", n), "A ticket", "abc1234 merged")
	}
}

// With the pane's size fixed, the tickets table keeps its place and height while workers start,
// report and finish, the winding-down line comes and goes, and tickets pile up.
func TestTicketsTableStaysPut(t *testing.T) {
	for _, size := range [][2]int{{80, 40}, {135, 45}, {66, 30}, {100, 24}, {140, 36}, {50, 28}, {120, 22}} {
		w, h := size[0], size[1]
		first := ""
		steadyRun(t, w, h, func(step string, m Dashboard) {
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
	for _, size := range [][2]int{{80, 19}, {135, 14}, {66, 11}, {40, 8}} {
		w, h := size[0], size[1]
		steadyRun(t, w, h, func(step string, m Dashboard) {
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
			m := numberedDashboard(t, n, &focused)
			st := m.statusOf("k-01")
			st.Title = long
			m = setStatus(m, st)
			quiet := m.workerPanels(w)
			m = reported(m, func(st *dispatch.Status) { st.Activity = "⏺ Bash(go test ./...)" })
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
					f := numberedRun(t, n, &focused).sized(w, h)
					for i := range 4 {
						f.done(fmt.Sprintf("k-%02d", i+20), "Done", "")
					}
					m := f.dash()
					m.cfg.Concurrency = 3
					look := func(m Dashboard) string {
						view := m.View()
						checkWithinPane(t, view, w, h)
						top, height := tableSpan(view)
						return fmt.Sprintf("%s; table at line %d, %d lines", workerForm(view), top, height)
					}
					quiet := look(m)
					m = reported(m, func(st *dispatch.Status) { st.Activity = "⏺ Bash(go test ./...)" })
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
		// Each built anew: the workers of dashboards built one from another are one map.
		build := func(activity bool) Dashboard {
			var focused []string
			f := numberedRun(t, n, &focused)
			for i := range 15 {
				f.done(fmt.Sprintf("k-%02d", i+20), "A ticket", "abc1234 merged")
			}
			m := reported(f.dash(), func(st *dispatch.Status) {
				st.Title = strings.Repeat("A long title that wraps ", 5)
				if activity {
					st.Activity = "⏺ " + strings.Repeat("Bash(go test ./...) ", 10)
				}
			})
			m.cfg.Concurrency = max(n, 3)
			return m
		}
		busy := build(true)
		dashboards = append(dashboards, [3]Dashboard{build(false), busy, press(busy, "s", "y")})
	}
	// A few groups of widths run side by side, each width in turn: drawing allocates so much that more
	// at once spend more CPU in the garbage collector's pauses than they save in time.
	const groups = 4
	for g := range groups {
		t.Run(fmt.Sprintf("widths %d+%dn", g, groups), func(t *testing.T) {
			t.Parallel()
			for w := g; w <= 140; w += groups {
				t.Run(fmt.Sprint(w), func(t *testing.T) {
					for h := 1; h <= 45; h++ {
						n := (7*w + h) % len(dashboards)
						m := dashboards[n][(w+h)%3]
						m.width, m.height = w, h
						checkWithinPane(t, m.View(), w, h)
					}
				})
			}
		})
	}
}
