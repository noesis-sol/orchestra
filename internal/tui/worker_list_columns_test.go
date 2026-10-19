package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// columnsDashboard is a dashboard whose clock stands still, with the workers given running.
func columnsDashboard(running ...dispatch.Status) Dashboard {
	m := reviewDashboard()
	at := m.began.Add(time.Hour)
	m.now = func() time.Time { return at }
	m.active = map[string]dispatch.Status{}
	for _, st := range running {
		st.Started = at.Add(-st.Started.Sub(time.Time{}))
		m.active[st.Ticket] = st
	}
	return m
}

// ago is a start time that columnsDashboard reads as d before its clock.
func ago(d time.Duration) time.Time { return time.Time{}.Add(d) }

// column is the screen column s starts at in line, or -1 if line lacks it.
func column(line, s string) int {
	i := strings.Index(line, s)
	if i < 0 {
		return -1
	}
	return ansi.StringWidth(line[:i])
}

// The compact list lines up its workers' IDs, statuses and times as columns, the times right-aligned,
// however wide each is, and still cuts each line to the box with an ellipsis.
func TestWorkerListLinesUpItsColumns(t *testing.T) {
	type row struct{ status, time, title string }
	rows := map[string]row{
		"tutti-nrg.5.9": {"resolving", "41m5s", "Resolve the conflicts"},
		"tutti-soj":     {"fixing check", "8m8s", "Fix the check"},
		"tutti-nrg.70":  {"testing", "3s", "Test it"},
		"tutti-a":       {"working", "1m0s", "Work on it"},
		"tutti-bbbb":    {"to merge (2nd)", "59m59s", "Merge it"},
	}
	m := columnsDashboard(
		dispatch.Status{Ticket: "tutti-bbbb", Title: "Merge it", Started: ago(59*time.Minute + 59*time.Second),
			Agent: dispatch.StateDone, Merge: dispatch.MergeQueued, Place: 2},
		dispatch.Status{Ticket: "tutti-nrg.5.9", Title: "Resolve the conflicts", Started: ago(41*time.Minute + 5*time.Second),
			Agent: dispatch.StateWorking, Resolving: true},
		dispatch.Status{Ticket: "tutti-soj", Title: "Fix the check", Started: ago(8*time.Minute + 8*time.Second),
			Agent: dispatch.StateWorking, Doing: dispatch.DoingTesting, Fixing: true},
		dispatch.Status{Ticket: "tutti-a", Title: "Work on it", Started: ago(time.Minute), Agent: dispatch.StateWorking},
		dispatch.Status{Ticket: "tutti-nrg.70", Title: "Test it", Started: ago(3 * time.Second),
			Agent: dispatch.StateWorking, Doing: dispatch.DoingTesting},
	)
	list := ansi.Strip(m.workerList(100))
	cols := map[string]int{}
	seen := 0
	for line := range strings.SplitSeq(list, "\n") {
		for id, r := range rows {
			if column(line, id+" ") < 0 {
				continue
			}
			seen++
			at := map[string]int{
				"id":       column(line, id),
				"status":   column(line, r.status),
				"time end": column(line, r.time) + len(r.time),
				"title":    column(line, r.title),
			}
			for name, c := range at {
				if c < 0 || column(line, r.time) < 0 {
					t.Fatalf("%s's line lacks its %s: %q", id, name, line)
				}
				if want, ok := cols[name]; ok && c != want {
					t.Errorf("%s's %s is at column %d, the others' at %d:\n%s", id, name, c, want, list)
				}
				cols[name] = c
			}
		}
	}
	if seen != len(rows) {
		t.Fatalf("found %d of the %d workers' lines:\n%s", seen, len(rows), list)
	}
	if !strings.Contains(list, "tutti-soj      fixing check      8m8s  Fix the check") {
		t.Errorf("the columns should be as wide as their widest cell, two spaces apart:\n%s", list)
	}

	narrow := strings.Split(m.workerList(40), "\n")
	for _, line := range narrow[1 : len(narrow)-1] {
		if w := ansi.StringWidth(line); w != 40 || !strings.Contains(line, "…") {
			t.Errorf("a line %d wide, want 40 and cut with an ellipsis: %q", w, ansi.Strip(line))
		}
	}
}

// Rows of one width get no padding beyond the two spaces between their cells.
func TestWorkerListOfOneWidthIsNotPadded(t *testing.T) {
	m := columnsDashboard(
		dispatch.Status{Ticket: "k-1", Title: "First", Started: ago(2 * time.Minute), Agent: dispatch.StateWorking},
		dispatch.Status{Ticket: "k-2", Title: "Second", Started: ago(time.Minute), Agent: dispatch.StateWorking},
	)
	list := ansi.Strip(m.workerList(80))
	for _, want := range []string{"k-1  working  2m0s  First", "k-2  working  1m0s  Second"} {
		if !strings.Contains(list, want) {
			t.Errorf("the list lacks %q:\n%s", want, list)
		}
	}
}
