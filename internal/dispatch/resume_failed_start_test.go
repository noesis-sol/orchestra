package dispatch

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

// recordsReporter is sessionReporter with the session records kept as the hooks keep them: a new
// worker's hooks remove the record of the one before it, a resumed worker's keep it (its own
// SessionStart hook writes the same session again once it runs).
type recordsReporter struct {
	sessionReporter
	mu sync.Mutex
}

func (r *recordsReporter) ReportArgs(wt string) ([]string, error) {
	r.mu.Lock()
	delete(r.sessions, wt)
	r.mu.Unlock()
	return r.sessionReporter.ReportArgs(wt)
}

func (r *recordsReporter) ResumeArgs(wt string) ([]string, error) {
	return r.sessionReporter.ResumeArgs(wt)
}

func (r *recordsReporter) Session(wt string) (Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[wt]
	return s, ok
}

// refusingTabs is Herdr's tabs, refusing to open one once armed, as a Herdr that is down would.
type refusingTabs struct {
	Tabs
	armed *atomic.Bool
}

func (t refusingTabs) CreateTab(ctx context.Context, workspace, cwd, label string) (string, string, error) {
	if t.armed.Load() {
		return "", "", errors.New("herdr tab create: server not running")
	}
	return t.Tabs.CreateTab(ctx, workspace, cwd, label)
}

// A gone worker whose resumed worker can't be started (Herdr refuses the tab) keeps its session: the
// run stops, and the next one resumes that session, rather than dropping the ticket as gone.
func TestResumeWhoseStartFailsIsResumedByTheNextRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		r := &recordsReporter{}
		h.reporter = r
		var armed atomic.Bool
		h.worker("A", func(w *fakeWorker) AgentState {
			// Its SessionStart hook records the session, once its hooks are set up.
			r.mu.Lock()
			r.sessions = map[string]Session{w.wt: {ID: testSession,
				Transcript: "/home/u/.claude/projects/x/" + testSession + ".jsonl"}}
			r.mu.Unlock()
			armed.Store(true) // Herdr goes down as the worker does
			return vanishes(w)
		}, finishes("a.txt"))
		o := h.loop()
		o.tabs = refusingTabs{Tabs: o.tabs, armed: &armed}
		if code := o.Run(context.Background()); code != ExitTool || !strings.HasPrefix(o.Final(), "TAB_FAILED") {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if n := len(h.herdr.argsFor("A")); n != 1 {
			t.Fatalf("run 1 started %d workers on A; want 1", n)
		}
		if _, ok := r.Session(h.worktree("A")); !ok {
			t.Fatal("the failed resume removed the session it was to resume")
		}

		armed.Store(false) // Herdr is back
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" { // adopted, not dispatched
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if starts := h.herdr.argsFor("A"); len(starts) != 2 || !resumedWith(starts[1], testSession) {
			t.Fatalf("starts %q; want run 2 to resume %s", starts, testSession)
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) {
			t.Errorf("merged %v; want A", got)
		}
	})
}
