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

// The merge train (see train.go): while A holds the merge queue and its check runs, B, next in line,
// is rebased onto A's branch as it is checked and checked there, beside A's check. When A merges as
// checked, B merges in its turn with no further check; when A's check fails, B is put back as its
// worker left it, rebased onto Base and checked again in its turn. With the train at 0, B is checked
// in its turn alone, as before.

// trainGit is git in memory that keeps the commit each worktree was rebased onto, and holds the first
// read of wt/A's commit after A's rebase, which is its check's (see checkRebased), until B waits in
// line behind it: B's worker finishes a minute in, and a check, a command, holds the bubble's clock.
type trainGit struct {
	*fakeGit
	queued <-chan struct{}
	mu     sync.Mutex
	onto   map[string][]string // by worktree
	gated  bool                // A was rebased, and its next read of wt/A waits for B
	once   sync.Once
}

func (g *trainGit) Rebase(ctx context.Context, worktree, onto string) (string, error) {
	g.mu.Lock()
	g.onto[worktree] = append(g.onto[worktree], onto)
	g.mu.Unlock()
	out, err := g.fakeGit.Rebase(ctx, worktree, onto)
	g.mu.Lock()
	g.gated = g.gated || g.worktreeBranch(worktree) == "wt/A"
	g.mu.Unlock()
	return out, err
}

func (g *trainGit) Head(ctx context.Context, repo, rev string) (string, error) {
	g.mu.Lock()
	gated := g.gated && rev == "wt/A"
	g.mu.Unlock()
	if gated {
		g.once.Do(func() {
			select {
			case <-g.queued:
			case <-time.After(time.Hour):
			}
		})
	}
	return g.fakeGit.Head(ctx, repo, rev)
}

// rebasedOnto is the commits worktree wt was rebased onto, in order.
func (g *trainGit) rebasedOnto(wt string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.onto[wt]
}

// worktreeBranch is the branch worktree wt is on.
func (g *fakeGit) worktreeBranch(wt string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.worktrees[wt]
}

// queuedSink passes events and statuses on to Sink, and closes queued as ticket on is first shown
// waiting for its turn to merge.
type queuedSink struct {
	Sink
	on     string
	once   *sync.Once
	queued chan struct{}
}

func (s queuedSink) Status(st Status) {
	s.Sink.Status(st)
	if st.Ticket == s.on && st.Merge == MergeQueued {
		s.once.Do(func() { close(s.queued) })
	}
}

// trainCheck counts each ticket's runs in dir, as countedCheck does, and passes on B. On A it passes
// when dir has A.passes; with waits, only once B's check has started beside it, waiting for it for up to
// 10 seconds; without, only while B's hasn't.
func trainCheck(dir string, waits bool) string {
	a := fmt.Sprintf(`test ! -f '%s'/B.runs`, dir)
	if waits {
		a = fmt.Sprintf(`i=0; while [ ! -f '%[1]s'/B.runs ] && [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done; `+
			`test -f '%[1]s'/B.runs`, dir)
	}
	return fmt.Sprintf(`id=$(basename "$PWD"); echo run >> '%[1]s'/$id.runs; `+
		`if [ "$id" = A ]; then %[2]s && test -f '%[1]s'/A.passes; fi`, dir, a)
}

// trainScenario runs A and B with the train at train, A's check passing or not, and returns the harness,
// the git it ran on, the run's exit code and where the checks counted their runs. A lands a commit on
// main before it closes, so both are rebased; B finishes a minute later, while A merges.
func trainScenario(t *testing.T, train int, aPasses, aWaits bool) (*harness, *trainGit, *Loop, int, string) {
	dir := t.TempDir()
	if aPasses {
		if err := os.WriteFile(filepath.Join(dir, "A.passes"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h, g, o := trainLoop(t, train, trainCheck(dir, aWaits))
	return h, g, o, o.Run(context.Background()), dir
}

// trainLoop is trainScenario's run, yet to start, its check check.
func trainLoop(t *testing.T, train int, check string) (*harness, *trainGit, *Loop) {
	h := newTimedHarness(t)
	h.cfg.Concurrency = 2
	h.cfg.MergeTrain = train
	h.cfg.CheckHandBacks = 0
	h.cfg.Check = check
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.worker("A", func(w *fakeWorker) AgentState {
		time.Sleep(10 * time.Second) // B's worktree is made from main before the commit lands
		w.claim()
		w.commit("a.txt")
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
	return h, g, o
}

// commitOf is the hash of the commit on branch whose subject is subject, as the fake's log gives it.
func commitOf(g *fakeGit, branch, subject string) string {
	for line := range strings.Lines(g.log(branch)) {
		if hash, s, _ := strings.Cut(strings.TrimSpace(line), " "); s == subject {
			return hash
		}
	}
	return ""
}

func TestTheNextTicketIsCheckedAheadOnTheBranchBeforeIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, g, o, code, dir := trainScenario(t, 1, true, true)
		ev := h.sink.text()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), ev)
		}
		if got := closedIDs(h); !equal(got, []string{"A", "B"}) {
			t.Fatalf("merged %v, want A then B\n%s", got, ev)
		}
		a := commitOf(h.mem, "main", "A: add a.txt")
		if got := g.rebasedOnto(h.worktree("B")); len(got) != 1 || got[0] != a {
			t.Errorf("B was rebased onto %v, want once, onto A's branch as it was checked and merged (%s)", got, a)
		}
		for _, want := range []string{
			"checking wt/B ahead of its turn to merge, rebased onto wt/A as it is checked\n",
			"'" + h.cfg.Check + "' passes on wt/B ahead of its turn, on top of wt/A\n",
			"wt/B was checked ahead of its turn on top of wt/A, which merged as it was checked: " +
				"merging it with no further check\n",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		}
		if n := checkRuns(t, dir, "B"); n != 1 {
			t.Errorf("B's check ran %d times, want once, beside A's", n)
		}
		if n := checkRuns(t, dir, "A"); n != 1 {
			t.Errorf("A's check ran %d times, want 1", n)
		}
		main := h.mem.log("main")
		if !strings.HasPrefix(main, commitOf(h.mem, "main", "B: add b.txt")+" B: add b.txt\n"+a+" A: add a.txt\n") {
			t.Errorf("main should end with A, then B on top of it:\n%s", main)
		}
	})
}

func TestTheNextTicketIsCheckedAgainWhenTheOneBeforeItFails(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, g, o, code, dir := trainScenario(t, 1, false, true)
		ev := h.sink.text()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), ev)
		}
		if got := closedIDs(h); !equal(got, []string{"B"}) {
			t.Fatalf("merged %v, want B alone\n%s", got, ev)
		}
		if got := o.asideList(); !equal(got, []string{"A"}) {
			t.Errorf("set aside %v, want A", got)
		}
		main := h.mem.log("main")
		if strings.Contains(main, "A: add") || !strings.Contains(main, "B: add b.txt") {
			t.Errorf("main should have B without A:\n%s", main)
		}
		onto := g.rebasedOnto(h.worktree("B"))
		landed := commitOf(h.mem, "main", "landed on main: landed.txt")
		if len(onto) != 2 || onto[0] == landed || onto[1] != landed {
			t.Errorf("B was rebased onto %v, want onto A's branch ahead of its turn, then onto main's %s", onto, landed)
		}
		for _, want := range []string{
			"checking wt/B ahead of its turn to merge, rebased onto wt/A as it is checked\n",
			"wt/B was checked ahead of its turn on top of wt/A, which did not merge as it was checked: " +
				"put back as its worker left it\n",
			"rebased wt/B onto main, which moved on while it ran\n",
			"'" + h.cfg.Check + "' passes on the rebased wt/B\n",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		}
		if n := checkRuns(t, dir, "B"); n != 2 {
			t.Errorf("B's check ran %d times, want 2: ahead of its turn, and in it", n)
		}
	})
}

func TestWithTheTrainOffTheNextTicketIsCheckedInItsTurn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, g, o, code, dir := trainScenario(t, 0, true, false)
		ev := h.sink.text()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), ev)
		}
		if got := closedIDs(h); !equal(got, []string{"A", "B"}) {
			t.Fatalf("merged %v, want A then B\n%s", got, ev)
		}
		a := commitOf(h.mem, "main", "A: add a.txt")
		if got := g.rebasedOnto(h.worktree("B")); !equal(got, []string{a}) {
			t.Errorf("B was rebased onto %v, want onto main's %s alone", got, a)
		}
		if strings.Contains(ev, "ahead of its turn") {
			t.Errorf("nothing should be checked ahead of its turn:\n%s", ev)
		}
		if !strings.Contains(ev, "'"+h.cfg.Check+"' passes on the rebased wt/B\n") {
			t.Errorf("B should be checked as rebased in its turn:\n%s", ev)
		}
		if n := checkRuns(t, dir, "B"); n != 1 {
			t.Errorf("B's check ran %d times, want 1", n)
		}
	})
}
