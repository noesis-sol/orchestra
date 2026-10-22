package dispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// A setup the merge train cuts short is run again before the ticket's own check: A changes
// package-lock.json, B is rebased onto A ahead of its turn and its setup starts there, removing its
// dependencies first as npm ci does; A's check fails, which stops B's train mid-setup. In B's turn it
// is rebased onto main, which lacks A's lockfile, so no file the setup reads changed since its
// dependencies were last set up, yet they are half installed: the setup runs again before the check.
// orchestra-4n6j.3: setUp neither read nor left a mark of a setup that failed or was cut short.
func TestATrainSetupCutShortIsRunAgainBeforeTheCheck(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.cfg.MergeTrain = 1
		h.cfg.CheckHandBacks = 0
		h.cfg.SetupFiles = []string{"package-lock.json"}
		// Each setup counts its runs in dir and removes the dependencies (deps.ok) before it installs
		// them; B's second, the train's, waits up to 10 seconds first, for A's check to fail and stop it.
		h.cfg.Setup = fmt.Sprintf(`id=$(basename "$PWD"); echo run >> '%[1]s'/$id.setups; rm -f deps.ok; `+
			`if [ "$id" = B ] && [ "$(wc -l < '%[1]s'/B.setups)" -eq 2 ]; then `+
			`i=0; while [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done; fi; touch deps.ok`, dir)
		// A's check fails once B's train has started its setup; B's passes with its dependencies there.
		h.cfg.Check = fmt.Sprintf(`if [ "$(basename "$PWD")" = A ]; then `+
			`i=0; while [ "$(cat '%[1]s'/B.setups 2>/dev/null | wc -l)" -lt 2 ] && [ $i -lt 200 ]; do `+
			`sleep 0.05; i=$((i+1)); done; exit 1; fi; test -f deps.ok`, dir)
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", func(w *fakeWorker) AgentState {
			time.Sleep(10 * time.Second) // B's worktree is made from main before the commit lands
			w.claim()
			w.commit("package-lock.json")
			w.git.commit("main", "landed on main: landed.txt", "landed.txt")
			w.close()
			return "idle"
		})
		h.worker("B", busyFor(time.Minute, "b.txt"))
		queued := make(chan struct{})
		g := &trainGit{fakeGit: h.mem, queued: queued, onto: map[string][]string{}}
		o := h.loop()
		o.merger, o.checkout = g, g
		o.SetSink(queuedSink{Sink: h.sink, on: "B", once: &sync.Once{}, queued: queued})
		code := o.Run(context.Background())
		ev := h.sink.text()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), ev)
		}
		if got := closedIDs(h); !equal(got, []string{"B"}) {
			t.Fatalf("merged %v, want B alone, its check passing on its dependencies set up again\n%s", got, ev)
		}
		for _, want := range []string{
			"checking wt/B ahead of its turn to merge, rebased onto wt/A as it is checked\n",
			"B: package-lock.json changed since its dependencies were last set up; running '" + h.cfg.Setup +
				"' before the check\n",
			"wt/B was checked ahead of its turn on top of wt/A, which did not merge as it was checked: " +
				"put back as its worker left it\n",
			"B: the last setup in its worktree failed; running '" + h.cfg.Setup + "' before the check\n",
			"'" + h.cfg.Check + "' passes on the rebased wt/B\n",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		}
		b, err := os.ReadFile(filepath.Join(dir, "B.setups"))
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(b), "run\n"); n != 3 {
			t.Errorf("B's setup ran %d times, want 3: before its worker, in the train, and in its turn", n)
		}
		if setupFailedIn(h.worktree("B")) || setupCutShortIn(h.worktree("B")) {
			t.Error("the setup that worked in B's turn left the marks of the one cut short")
		}
	})
}
