package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/load"
)

const gb = 1 << 30

// gbs is n GB in bytes.
func gbs(n float64) int64 { return int64(n * gb) }

// loadedDashboard is a dashboard 120x40 with four workers in four slots, given reading r.
func loadedDashboard(t *testing.T, r load.Reading) Dashboard {
	f := run(t).config(dispatch.Config{Limit: 40, Base: "main", Concurrency: 4, MCP: noServers}).sized(120, 40)
	for _, id := range []string{"k-1", "k-2", "k-3", "k-4"} {
		f.running(dispatch.Status{Ticket: id, Title: "Ticket " + id, Started: time.Now(), Agent: dispatch.StateWorking})
	}
	return f.send(loadMsg(r)).dash()
}

// reading is four workers taking 4.8 GB together, the merge check 1.4 GB, at pressure normal and a
// load of 9.8 on 10 cores.
func reading() load.Reading {
	return load.Reading{
		Workers: map[string]int64{"k-1": gbs(2), "k-2": gbs(1.2), "k-3": gbs(0.8), "k-4": gbs(0.8)},
		Check:   gbs(1.4),
		Kernel:  load.Kernel{Pressure: load.PressureNormal, Swaps: 0, Load1: 9.8, Cores: 10, Available: -1},
	}
}

// The load line, above the strip, has the workers at work out of their slots, the memory their trees
// and the merge check take, the memory pressure and the CPU load against the cores; the strip has no
// Workers column.
func TestTheLoadLineSaysWhatTheWorkersTake(t *testing.T) {
	m := loadedDashboard(t, reading())
	want := "▶ 4 of 4 workers · memory 6.2 GB (workers 4.8, merge check 1.4) · pressure normal · CPU 9.8/10 cores"
	if line := ansi.Strip(m.loadLine(120)); strings.TrimSpace(line) != want {
		t.Errorf("load line %q, want %q", line, want)
	}
	view := ansi.Strip(m.View())
	lines := strings.Split(view, "\n")
	at := -1
	for i, l := range lines {
		if strings.Contains(l, "▶ 4 of 4 workers") {
			at = i
		}
	}
	if at < 0 || at+2 >= len(lines) || !strings.Contains(lines[at+2], "Completed") {
		t.Errorf("the load line should be right above the strip:\n%s", view)
	}
	if strings.Contains(m.statsTable(120), "Workers") {
		t.Errorf("the strip has a Workers column:\n%s", ansi.Strip(m.statsTable(120)))
	}
	// Each worker's memory in its line in Current, lined up with the others'.
	for _, l := range []string{"k-1  working  0s  2.0 GB", "k-2  working  0s  1.2 GB"} {
		if !strings.Contains(view, l) {
			t.Errorf("Current lacks %q:\n%s", l, view)
		}
	}
}

// Before the loop has read the machine's load, the line has the workers alone, and Current no memory.
func TestTheLoadLineBeforeAReading(t *testing.T) {
	m := loadedDashboard(t, reading())
	m.load = nil
	if line := strings.TrimSpace(ansi.Strip(m.loadLine(120))); line != "▶ 4 of 4 workers" {
		t.Errorf("load line %q, want the workers alone", line)
	}
	if view := ansi.Strip(m.View()); strings.Contains(view, " GB") {
		t.Errorf("no memory should show before a reading:\n%s", view)
	}
}

// A worker whose tree can't be found shows as unknown, not 0: in its line, and counted on the load
// line.
func TestAWorkerWithoutARootIsUnknown(t *testing.T) {
	r := reading()
	r.Workers["k-4"] = load.Unknown
	m := loadedDashboard(t, r)
	if line := ansi.Strip(m.loadLine(120)); !strings.Contains(line, "memory 5.4 GB (workers 4.0, merge check 1.4, 1 unknown)") {
		t.Errorf("load line %q should count the unknown worker apart", line)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "k-4  working  0s  ? GB") {
		t.Errorf("k-4's line should say its memory is unknown:\n%s", view)
	}
}

// A narrow pane shortens the line: the memory's breakdown goes first, then the memory, keeping the
// workers, the pressure and the CPU, and then it is cut.
func TestANarrowPaneShortensTheLoadLine(t *testing.T) {
	m := loadedDashboard(t, reading())
	for w, want := range map[int]string{
		90: "▶ 4 of 4 workers · memory 6.2 GB · pressure normal · CPU 9.8/10 cores",
		60: "▶ 4 of 4 workers · pressure normal · CPU 9.8/10 cores",
		40: "▶ 4 of 4 workers · pressure normal · C…",
	} {
		line := m.loadLine(w)
		if got := strings.TrimSpace(ansi.Strip(line)); got != want {
			t.Errorf("width %d: %q, want %q", w, got, want)
		}
		if lipgloss.Width(line) > w {
			t.Errorf("width %d: the line is %d wide", w, lipgloss.Width(line))
		}
	}
}

// A pane too short for the strip and the load line has the totals on one line, the workers among them.
func TestAShortPaneCarriesTheWorkerCount(t *testing.T) {
	m := loadedDashboard(t, reading())
	m.height = 12
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "▶ 4/4") || strings.Contains(view, "pressure") {
		t.Errorf("a short pane should have the count on the totals' line, without the load line:\n%s", view)
	}
}

// Swapping under way shows, and on Linux the memory available.
func TestTheLoadLineShowsSwappingAndAvailableMemory(t *testing.T) {
	r := reading()
	r.Swapping, r.Available = 1200, 3*gb
	line := ansi.Strip(loadedDashboard(t, r).loadLine(200))
	for _, want := range []string{"pressure normal (3.0 GB available)", "swapping 1200 pages/min"} {
		if !strings.Contains(line, want) {
			t.Errorf("load line %q lacks %q", line, want)
		}
	}
	r.Swapping = load.SwappingPerMinute - 1
	if line := ansi.Strip(loadedDashboard(t, r).loadLine(200)); strings.Contains(line, "swapping") {
		t.Errorf("load line %q: swapping below the threshold isn't under way", line)
	}
}

// The CPU is in its level's colour: green, amber or red.
func TestTheLoadLineIsInTheLevelsColour(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
	for level, style := range map[load.Level]lipgloss.Style{load.Green: doneStyle, load.Amber: windDownStyle,
		load.Red: stopStyle} {
		r := reading()
		r.Levels.CPU = level
		if line := loadedDashboard(t, r).loadLine(200); !strings.Contains(line, style.Render("CPU 9.8/10 cores")) {
			t.Errorf("level %v: the CPU isn't in its colour: %q", level, line)
		}
	}
}
