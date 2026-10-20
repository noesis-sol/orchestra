package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/noesis-sol/orchestra/internal/load"
)

// Each figure on the load line is in the colour of its own signal's level, not the worst one's: only
// the figure past its threshold says so, and the memory, with no threshold, is never coloured.
func TestEachLoadFigureHasItsOwnColour(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
	const memory = "memory 6.2 GB (workers 4.8, merge check 1.4)"
	for _, c := range []struct {
		name   string
		set    func(r *load.Reading)
		want   []string
		absent []string
	}{
		{
			name: "the CPU past its threshold",
			set: func(r *load.Reading) {
				r.Load1 = 16
				r.Levels, r.Level = load.Levels{CPU: load.Amber}, load.Amber
			},
			want: []string{windDownStyle.Render("CPU 16.0/10 cores"), doneStyle.Render("pressure normal")},
		},
		{
			name: "memory pressure at warn",
			set: func(r *load.Reading) {
				r.Pressure = load.PressureWarn
				r.Levels, r.Level = load.Levels{Pressure: load.Red}, load.Red
			},
			want: []string{stopStyle.Render("pressure warn"), doneStyle.Render("CPU 9.8/10 cores")},
		},
		{
			name: "swapping",
			set: func(r *load.Reading) {
				r.Swapping = 1200
				r.Levels, r.Level = load.Levels{Swapping: load.Amber}, load.Amber
			},
			want: []string{windDownStyle.Render("swapping 1200 pages/min"), doneStyle.Render("pressure normal"),
				doneStyle.Render("CPU 9.8/10 cores")},
		},
		{
			name: "everything at red",
			set: func(r *load.Reading) {
				r.Pressure, r.Load1 = load.PressureCritical, 31
				r.Levels, r.Level = load.Levels{CPU: load.Red, Pressure: load.Red}, load.Red
			},
			want: []string{stopStyle.Render("pressure critical"), stopStyle.Render("CPU 31.0/10 cores")},
		},
	} {
		r := reading()
		c.set(&r)
		line := loadedDashboard(t, r).loadLine(200)
		for _, w := range c.want {
			if !strings.Contains(line, w) {
				t.Errorf("%s: the line lacks %q: %q", c.name, w, line)
			}
		}
		if !strings.Contains(line, dimStyle.Render(" · ")+memory+dimStyle.Render(" · ")) {
			t.Errorf("%s: the memory should be uncoloured: %q", c.name, line)
		}
	}
}
