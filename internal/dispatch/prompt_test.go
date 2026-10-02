package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A prompt orchestra gives a worker mid-ticket (a hand-back, a nudge, a resume, the probe's) returns
// once the worker starts on it, and the caller watches the turn from there.

// handBackHarness is a timed run whose ticket A closes with a commit that conflicts with main, so
// its rebase is handed back to its worker, which then behaves as resolve.
func handBackHarness(t *testing.T, resolve behaviour) *harness {
	t.Helper()
	h := newTimedHarness(t)
	h.cfg.Check = "false" // never run: no resolution gets that far
	h.cfg.ResolveConflicts = true
	h.mem.conflict("wt/A", "shared.txt")
	h.beads.add("A", "first", 1)
	h.worker("A", func(w *fakeWorker) AgentState {
		w.claim()
		w.git.commit("main", "landed on main: shared.txt", "shared.txt")
		w.commit("shared.txt")
		w.close()
		return "idle"
	}, resolve)
	return h
}

// The dashboard shows the ticket as resolving from the hand-back on, before its worker takes it.
func TestAHandBackShowsAsResolvingAtOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := handBackHarness(t, func(w *fakeWorker) AgentState { return "idle" })
		var shown Status
		h.herdr.onPrompt = func(id string) { shown = h.sink.lastStatus(id) }
		if _, code := h.run(); code != ExitOK {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		if !shown.Resolving || shown.Ticket != "A" || shown.Agent != StateIdle {
			t.Errorf("when the hand-back was pasted the dashboard showed %+v, want A idle and resolving", shown)
		}
	})
}

// The resolve timeout runs from the hand-back, not from when Herdr is done confirming the worker
// took it.
func TestTheResolveTimeoutRunsFromTheHandBack(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := handBackHarness(t, func(w *fakeWorker) AgentState { return "working" })
		h.herdr.promptTakes = time.Minute
		var handedBack time.Time
		h.herdr.onPrompt = func(id string) { handedBack = time.Now() }
		if _, code := h.run(); code != ExitOK {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		limit := project.DefaultResolveTimeout
		if took := time.Since(handedBack); took < limit || took > limit+2*statusPoll {
			t.Errorf("set aside %s after the hand-back, want %s", took, limit)
		}
		if ev := h.sink.text(); !strings.Contains(ev, "handed back to its worker, but its worker was still working after 20m") {
			t.Errorf("events:\n%s", ev)
		}
	})
}

// The probe starts watching its worker while it works: one that writes its file after a while
// ends the hold, and one that stops after a while without it ends the run.
func TestTheProbeWatchesItsWorkerWork(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		probe behaviour
		code  int
	}{
		{"runs its command", func(w *fakeWorker) AgentState { time.Sleep(time.Minute); return runsProbe(w) }, ExitOK},
		{"runs none", func(w *fakeWorker) AgentState { time.Sleep(time.Minute); return givesUp(w) }, ExitEnvironment},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				h.holdForEnvironment(time.Minute)
				h.cfg.EnvProbe = time.Minute
				h.beads.add("A", "a", 1)
				h.beads.add("B", "b", 2)
				h.worker("A", givesUp, finishes("a.txt"))
				h.worker("B", givesUp, finishes("b.txt"))
				h.worker(probeID, tc.probe)

				o, code := h.run()
				if code != tc.code {
					t.Fatalf("exit code %d, want %d:\n%s", code, tc.code, h.logged())
				}
				switch tc.code {
				case ExitOK:
					if !h.alerts.has("PROBE_OK: a worker without a ticket ran a command 1m after the hold; taking tickets again") {
						t.Errorf("no PROBE_OK notification:\n%s", h.logged())
					}
				default:
					if !strings.Contains(o.Final(), "it stopped without running its command; see tab "+h.probeTab()) {
						t.Errorf("final line %q", o.Final())
					}
				}
			})
		})
	}
}
