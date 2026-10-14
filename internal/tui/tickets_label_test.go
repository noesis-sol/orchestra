package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The tickets table has no header row and no rule under it: its rows start below its top border.
func TestTicketsTableHasNoHeaderRow(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m.rows = []ticketRow{{id: "k-01", state: rowDone, note: "abc1234 merged"}, {id: "k-02", title: "Second"}}
	table := ansi.Strip(m.ticketsTable(80, 4))
	lines := strings.Split(table, "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "╭") || !strings.Contains(lines[1], "k-01") ||
		!strings.Contains(lines[2], "k-02") || strings.Contains(table, "├") || strings.Contains(table, "Tickets") {
		t.Errorf("want the two tickets between the borders, with no header:\n%s", table)
	}
	section := strings.Split(ansi.Strip(m.tickets(80, 5)), "\n")
	if len(section) != 5 || strings.TrimRight(section[0], " ") != "  Tickets" || section[1] != lines[0] {
		t.Errorf("want the Tickets label over the table:\n%s", strings.Join(section, "\n"))
	}
}

// The Tickets label heads the table as the Current label heads the workers: indented the same and
// bold, but in chalk rather than the working cyan.
func TestTicketsLabelIsLikeCurrentInItsOwnColour(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	var focused []string
	m := numberedDashboard(3, &focused)
	m.width, m.height = 70, 40
	v := m.View()
	// Bold (1), with the exact dark-background chalk #E5E7EB and cyan #22D3EE.
	for _, label := range []string{"  \x1b[1;38;2;229;231;235mTickets\x1b[0m", "  \x1b[1;38;2;34;211;238mCurrent\x1b[0m"} {
		if !strings.Contains(v, label+strings.Repeat(" ", 70-2-7)+"\n") {
			t.Errorf("the view lacks the label line %q:\n%q", label, v)
		}
	}
}

// The label and borders take a line fewer than the header, rule and borders did: a pane as tall
// shows as many tickets, and the line goes to the workers.
func TestTheTicketsLabelSavesTheWorkersALine(t *testing.T) {
	for h := 21; h <= 45; h++ {
		if rows, was := tableLines(h)-3, min(max(h/3-4, 3), 10); rows != was {
			t.Errorf("a pane %d tall: room for %d tickets, was %d", h, rows, was)
		}
	}
	var focused []string
	m := numberedDashboard(3, &focused)
	// 35 lines held the table and one line per worker; now a box each fits under the same 7 tickets.
	m.width, m.height = 66, 35
	view := m.View()
	checkWithinPane(t, view, 66, 35)
	if form := workerForm(view); form != "label true, 3 boxes" {
		t.Errorf("66x35: the workers show as %s, want 3 boxes:\n%s", form, ansi.Strip(view))
	}
	if top, height := tableSpan(view); top != 6 || height != 10 {
		t.Errorf("66x35: the table at line %d, %d lines, want line 6, 10 lines:\n%s", top, height, ansi.Strip(view))
	}
}

// Where the pane leaves the Current label out under the table, the Tickets label goes with it; a
// pane a line shorter has no table. The workers' one box is their list, a line each: three of them,
// in a run that may have two, as after its concurrency was lowered.
func TestTheTicketsLabelGoesWithCurrent(t *testing.T) {
	var focused []string
	m := numberedDashboard(3, &focused)
	m.cfg.Concurrency = 2
	m.width = 66
	for h, want := range map[int]string{19: "label false, 1 boxes; table 5 lines", 18: "label true, 1 boxes; table 0 lines"} {
		m.height = h
		view := m.View()
		checkWithinPane(t, view, 66, h)
		_, height := tableSpan(view)
		got := fmt.Sprintf("%s; table %d lines", workerForm(view), height)
		if strings.Contains(ansi.Strip(view), "Tickets") {
			got += ", labelled"
		}
		if got != want {
			t.Errorf("66x%d: %s, want %s:\n%s", h, got, want, ansi.Strip(view))
		}
	}
}
