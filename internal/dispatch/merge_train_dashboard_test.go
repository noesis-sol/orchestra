package dispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
)

// A ticket checked ahead of its turn in the merge queue (see train.go) says so on the dashboard while
// it waits: rebased onto the branch ahead, checked on top of it, and whether its check passed.

// activitySink passes events and statuses on to Sink, and keeps the activities ticket on shows while it
// waits in line, each once as it changes. It writes passed in dir once it shows its check passed ahead.
type activitySink struct {
	Sink
	on, dir string
	mu      sync.Mutex
	shown   []string
}

func (s *activitySink) Status(st Status) {
	s.Sink.Status(st)
	if st.Ticket != s.on || st.Merge != MergeQueued {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.shown) == 0 || s.shown[len(s.shown)-1] != st.Activity {
		s.shown = append(s.shown, st.Activity)
	}
	if strings.HasPrefix(st.Activity, "passed its check ahead") {
		if err := os.WriteFile(filepath.Join(s.dir, "passed"), nil, 0o644); err != nil {
			panic(err)
		}
	}
}

func (s *activitySink) activities() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.shown)
}

func TestATicketCheckedAheadShowsHowItsCheckGoes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		// A's check passes once B has shown its own passed ahead of its turn, so B is still in line then.
		check := fmt.Sprintf(`if [ "$(basename "$PWD")" = A ]; then i=0; `+
			`while [ ! -f '%[1]s'/passed ] && [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done; `+
			`test -f '%[1]s'/passed; fi`, dir)
		h, _, o := trainLoop(t, 1, check)
		sink := &activitySink{Sink: o.sink, on: "B", dir: dir}
		o.SetSink(sink)
		if code := o.Run(context.Background()); code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		want := []string{
			"waiting for its turn to merge into main",
			"rebasing onto wt/A, to be checked ahead of its turn on top of it as it is checked",
			"checked ahead of its turn, on top of wt/A as it is checked",
			"passed its check ahead of its turn, on top of wt/A: merges with no further check if wt/A merges as checked",
		}
		if got := sink.activities(); !equal(got, want) {
			t.Errorf("B showed, as it waited in line:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})
}

func TestTrainActivitySaysHowTheCheckAheadGoes(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		t    trainRun
		want string
	}{
		{"rebasing", trainRun{}, "rebasing onto wt/A, to be checked ahead of its turn on top of it as it is checked"},
		{"checking", trainRun{head: "b1"}, "checked ahead of its turn, on top of wt/A as it is checked"},
		{"passed", trainRun{head: "b1", passed: "b1"},
			"passed its check ahead of its turn, on top of wt/A: merges with no further check if wt/A merges as checked"},
		{"failed", trainRun{head: "b1", failed: true}, "failed its check ahead of its turn, on top of wt/A: checked again in its turn"},
		{"cut short", trainRun{head: "b1", failed: true, cut: true}, ""},
		{"not rebased", trainRun{failed: true}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			tr := c.t
			tr.id, tr.behind = "B", "wt/A"
			q := &mergeQueue{trains: map[string]*trainRun{"B": &tr}}
			if got := q.trainActivity("B"); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
			if got := q.trainActivity("C"); got != "" {
				t.Errorf("a ticket with no train shows %q, want nothing", got)
			}
		})
	}
}
