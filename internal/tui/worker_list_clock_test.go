package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A time that gains a digit while the compact list is drawn (59s to 1m0s) still fits its column: each
// line's cells are read once, so the times stay right-aligned and the titles at one column.
func TestWorkerListReadsTheClockOncePerLine(t *testing.T) {
	m := columnsDashboard(t,
		dispatch.Status{Ticket: "k-1", Title: "First", Started: ago(59 * time.Second), Agent: dispatch.StateWorking},
		dispatch.Status{Ticket: "k-2", Title: "Second", Started: ago(3 * time.Second), Agent: dispatch.StateWorking},
	)
	at := m.clock()
	m.now = func() time.Time { // a second passes at every reading
		now := at
		at = at.Add(time.Second)
		return now
	}
	list := ansi.Strip(m.workerList(80))
	titles := map[int]bool{}
	for line := range strings.SplitSeq(list, "\n") {
		for _, title := range []string{"s  First", "s  Second"} { // a time's last letter, then its title
			if c := column(line, title); c >= 0 {
				titles[c] = true
			}
		}
	}
	if len(titles) != 1 {
		t.Errorf("the times should end and the titles start at one column:\n%s", list)
	}
}
