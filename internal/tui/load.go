package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/load"
)

// The load line, above the stats strip, says whether the workers are overloading the machine:
//
//	▶ 4 of 4 workers · memory 6.2 GB (workers 4.8, merge check 1.4) · pressure normal · CPU 9.8/10 cores
//
// the workers at work out of their slots, the memory their process trees take and orchestra's own
// commands' (its merge check above all), the kernel's memory pressure (and on Linux the memory
// available), swapping while it is under way, and the 1-minute load average against the cores: the
// pressure, the swapping and the CPU each in the colour of its own level (load.Levels), the memory,
// which has no threshold, uncoloured.

// levelStyles are the load line's colours, by level.
var levelStyles = map[load.Level]lipgloss.Style{
	load.Green: doneStyle,
	load.Amber: windDownStyle,
	load.Red:   stopStyle,
}

// loadLine is the load line at width w. A pane too narrow for it all drops the memory's breakdown,
// then the memory, keeping the workers, the pressure and the CPU, and then cuts it. Before the loop
// has read the machine's load it has the workers alone.
func (m Dashboard) loadLine(w int) string {
	sep := dimStyle.Render(" · ")
	workers := m.workersPart()
	if m.load == nil {
		return ansi.Truncate(" "+workers, w, "…")
	}
	levels := m.load.Levels
	pressure := inLevel(levels.Pressure, m.pressurePart())
	swapping := inLevel(levels.Swapping, m.swappingPart())
	cpu := inLevel(levels.CPU, m.cpuPart())
	render := func(parts ...string) string {
		l := []string{workers}
		for _, p := range parts {
			if p != "" {
				l = append(l, p)
			}
		}
		return strings.Join(l, sep)
	}
	var line string
	for _, memory := range []string{m.memoryPart(true), m.memoryPart(false), ""} {
		if line = " " + render(memory, pressure, swapping, cpu); lipgloss.Width(line) <= w {
			return line
		}
	}
	return ansi.Truncate(line, w, "…")
}

// inLevel is part in level's colour; "" stays "".
func inLevel(level load.Level, part string) string {
	if part == "" {
		return ""
	}
	return levelStyles[level].Render(part)
}

// workersPart is the workers at work out of their slots: "▶ 4 of 4 workers", faint with none.
func (m Dashboard) workersPart() string {
	n, of := m.inSlots(), m.slots()
	noun := "workers"
	if of == 1 {
		noun = "worker"
	}
	if n == 0 {
		return dimStyle.Render(fmt.Sprintf("▶ 0 of %d %s", of, noun))
	}
	return pickedStyle.Render(fmt.Sprintf("▶ %d", n)) + dimStyle.Render(fmt.Sprintf(" of %d %s", of, noun))
}

// memoryPart is the memory the workers' process trees and orchestra's own commands take: "memory 6.2
// GB", with breakdown "(workers 4.8, merge check 1.4)" after it, and how many workers' trees are
// unknown, if any.
func (m Dashboard) memoryPart(breakdown bool) string {
	r := m.load
	s := "memory " + gigabytes(r.Memory())
	unknown := 0
	for _, mem := range r.Workers {
		if mem == load.Unknown {
			unknown++
		}
	}
	if !breakdown {
		return s
	}
	var parts []string
	if r.Check > 0 || unknown > 0 {
		parts = append(parts, "workers "+number(r.WorkerMemory()))
	}
	if r.Check > 0 {
		parts = append(parts, "merge check "+number(r.Check))
	}
	if unknown > 0 {
		parts = append(parts, fmt.Sprintf("%d unknown", unknown))
	}
	if len(parts) > 0 {
		s += " (" + strings.Join(parts, ", ") + ")"
	}
	return s
}

// pressurePart is the kernel's memory pressure, with the memory available where the kernel says
// (Linux): "pressure normal", "pressure warn (1.2 GB available)"; "" when unknown.
func (m Dashboard) pressurePart() string {
	r := m.load
	if r.Pressure == load.PressureUnknown {
		return ""
	}
	s := "pressure " + r.Pressure.String()
	if r.Available >= 0 {
		s += " (" + gigabytes(r.Available) + " available)"
	}
	return s
}

// swappingPart is the pages swapped in and out a minute while swapping is under way
// (load.SwappingPerMinute); "" while it isn't.
func (m Dashboard) swappingPart() string {
	if m.load.Swapping < load.SwappingPerMinute {
		return ""
	}
	return fmt.Sprintf("swapping %.0f pages/min", m.load.Swapping)
}

// cpuPart is the 1-minute load average against the cores: "CPU 9.8/10 cores"; "" when unknown.
func (m Dashboard) cpuPart() string {
	r := m.load
	if r.Load1 < 0 || r.Cores <= 0 {
		return ""
	}
	return fmt.Sprintf("CPU %.1f/%d cores", r.Load1, r.Cores)
}

// workerMemory is the memory of the running worker on ticket id's process tree, for its line in
// Current: "1.2 GB", "? GB" when its tree can't be found, and "" before the loop has read it.
func (m Dashboard) workerMemory(id string) string {
	if m.load == nil {
		return ""
	}
	mem, ok := m.load.Workers[id]
	switch {
	case !ok:
		return ""
	case mem == load.Unknown:
		return "? GB"
	}
	return gigabytes(mem)
}

// gibibyte is the unit the load line counts memory in, as GB.
const gibibyte = 1 << 30

// gigabytes is n bytes in GB, to a tenth: "6.2 GB".
func gigabytes(n int64) string { return number(n) + " GB" }

// number is n bytes in GB, to a tenth, without the unit: "4.8".
func number(n int64) string { return fmt.Sprintf("%.1f", float64(n)/gibibyte) }
