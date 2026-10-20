package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The foot keeps room for every line it draws, the winding-down line's whether the run winds down or not,
// so that the workers' form doesn't change as that line comes and goes; and the foot drawn is the footer's.
func TestFooterKeepsRoomForItsLines(t *testing.T) {
	for _, watch := range []bool{false, true} {
		for _, warn := range []bool{false, true} {
			cfg := dispatch.Config{Limit: 40, Base: "batch", Concurrency: 2, MCP: noServers}
			want := footFixedLines
			if watch {
				cfg.Watch, want = "herdr attach", want+1
			}
			if warn {
				cfg.MCP, want = nil, want+1
			}
			name := fmt.Sprintf("watch %v, warning %v", watch, warn)
			f := run(t).config(cfg).running(dispatch.Status{Ticket: "k-1", Title: "one"}).sized(80, 40)
			steady := f.dash().footer(80)
			f.keys("s", "y") // winding down
			drained := f.dash().footer(80)
			for _, foot := range []footer{steady, drained} {
				if foot.room != want {
					t.Errorf("%s: room %d, want %d", name, foot.room, want)
				}
				if n := len(foot.lines()); n > foot.room {
					t.Errorf("%s: %d lines, room for %d:\n%s", name, n, foot.room, foot.text)
				}
			}
			if steady.steadyLines() != drained.steadyLines() {
				t.Errorf("%s: the foot takes %d lines, winding down %d", name, steady.steadyLines(),
					drained.steadyLines())
			}
			view := strings.Split(f.dash().View(), "\n")
			lines := drained.lines()
			for i, l := range lines {
				got := strings.TrimRight(ansi.Strip(view[len(view)-len(lines)+i]), " ")
				if want := strings.TrimRight(ansi.Strip(l), " "); got != want {
					t.Errorf("%s: foot line %d drawn %q, footer %q", name, i, got, want)
				}
			}
		}
	}
}

// A question asked on a line of its own goes above the foot and takes a line more than its room only
// where the foot has no line to spare.
func TestFooterBelowAQuestion(t *testing.T) {
	m := run(t).config(dispatch.Config{Limit: 40, Base: "batch"}).sized(80, 40).dash()
	foot := m.footer(80)
	asked := foot.below("question?")
	if asked.room != foot.room {
		t.Errorf("room %d below a question, want %d", asked.room, foot.room)
	}
	if got := asked.lines(); len(got) != len(foot.lines())+1 || strings.TrimRight(got[0], " ") != "question?" {
		t.Errorf("below a question: %q", got)
	}
	if want := max(len(foot.lines())+1, foot.room); asked.steadyLines() != want {
		t.Errorf("below a question the foot takes %d lines, want %d", asked.steadyLines(), want)
	}
}

// Current's height estimate is what its one-line form takes with every worker the run may have.
func TestCurrentListLinesIsWhatIsDrawn(t *testing.T) {
	for _, slots := range []int{1, 3, 6} {
		f := run(t).config(dispatch.Config{Limit: 40, Base: "batch", Concurrency: slots})
		for i := range slots {
			f.running(dispatch.Status{Ticket: fmt.Sprintf("k-%d", i), Title: "a ticket"})
		}
		m := f.sized(80, 40).dash()
		if got := lineCount(withCurrentLabel(m.workerList(80))); got != m.currentListLines() {
			t.Errorf("%d workers: Current takes %d lines, estimated %d", slots, got, m.currentListLines())
		}
	}
}
