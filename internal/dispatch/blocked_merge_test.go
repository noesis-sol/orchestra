package dispatch

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// A finished ticket whose merge waits on the maintainer says why on its event (Event.Blocked), for
// the dashboard's row; one whose own work needs a look doesn't.

// dirtyCheckout is git in memory with a main checkout that has uncommitted changes once dirty is set.
type dirtyCheckout struct {
	*fakeGit
	dirty *atomic.Bool
}

func (c dirtyCheckout) DirtyTree(ctx context.Context, dir string) (string, error) {
	if c.dirty.Load() {
		return " M notes.txt", nil
	}
	return "", nil
}

// eventsOf is the events of kind k the run emitted.
func eventsOf(h *harness, k Kind) []Event {
	h.sink.mu.Lock()
	defer h.sink.mu.Unlock()
	var l []Event
	for _, ev := range h.sink.events {
		if ev.Kind == k {
			l = append(l, ev)
		}
	}
	return l
}

// Uncommitted changes in the main checkout, made while A ran, keep A from merging: the hold, while B
// still runs, and the stop over A say so; alone, the stop does.
func TestADirtyCheckoutSaysItBlocksTheFinishedTicket(t *testing.T) {
	t.Parallel()
	const why = "main checkout has uncommitted changes"
	for _, beside := range []bool{false, true} {
		name := "alone"
		if beside {
			name = "beside another ticket"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				var dirty atomic.Bool
				h.beads.add("A", "first", 1)
				h.worker("A", func(w *fakeWorker) AgentState {
					w.claim()
					time.Sleep(time.Second) // B starts meanwhile
					dirty.Store(true)
					return finishes("a.txt")(w)
				})
				if beside {
					h.cfg.Concurrency = 2
					h.beads.add("B", "second", 2)
					h.worker("B", busyFor(time.Hour, "b.txt")) // finishes into the same dirty checkout
				}
				o := h.loop()
				o.checkout = dirtyCheckout{fakeGit: h.mem, dirty: &dirty}
				if code := o.Run(t.Context()); code != ExitDirty || !strings.HasPrefix(o.Final(), "DIRTY_TREE: uncommitted") {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				stop := eventsOf(h, EvStop)
				if len(stop) != 1 || stop[0].Ticket != "A" || stop[0].Detail != "DIRTY_TREE" || stop[0].Blocked != why {
					t.Errorf("stop %+v, want one over A, blocked: %s", stop, why)
				}
				holds := eventsOf(h, EvHold)
				if !beside {
					if len(holds) != 0 {
						t.Errorf("holds %+v, want none with nothing else running", holds)
					}
					return
				}
				if len(holds) != 2 || holds[0].Ticket != "A" || holds[0].Blocked != why ||
					holds[1].Ticket != "B" || holds[1].Blocked != why {
					t.Errorf("holds %+v, want A's while B ran, then B's, each blocked: %s", holds, why)
				}
			})
		})
	}
}

// A merge conflict names the files it is in; a hold that isn't about a merge (PAUSED) says nothing
// of one.
func TestAMergeConflictSaysWhereItBlocksTheMerge(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.cfg.Check = "false" // never run: the rebase stops first
		h.mem.conflict("wt/A", "shared.txt", "docs/notes.md")
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			w.git.commit("main", "landed on main: shared.txt", "shared.txt")
			w.commit("shared.txt")
			w.close()
			return "idle"
		})
		h.worker("B", func(w *fakeWorker) AgentState {
			w.claim()
			time.Sleep(time.Hour) // A is set aside meanwhile
			return "idle"         // in progress: pauses the run
		})
		o, code := h.run()
		if code != ExitStuck {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		var conflict []Event
		for _, ev := range eventsOf(h, EvWarn) {
			if ev.Aside {
				conflict = append(conflict, ev)
			}
		}
		if len(conflict) != 1 || conflict[0].Ticket != "A" || conflict[0].Detail != "conflicts with main" ||
			conflict[0].Blocked != "merge conflict in shared.txt, docs/notes.md" {
			t.Errorf("set aside %+v, want A, blocked by its conflict in shared.txt and docs/notes.md", conflict)
		}
		for _, ev := range append(eventsOf(h, EvHold), eventsOf(h, EvStop)...) {
			if ev.Blocked != "" {
				t.Errorf("%s over %s says it blocks a merge: %+v", ev.Kind, ev.Ticket, ev)
			}
		}
	})
}

// conflictIn names the files git gave, or the branch when it gave none.
func TestConflictInNamesTheFiles(t *testing.T) {
	t.Parallel()
	if got := conflictIn([]string{"a.go", "b.go"}, "main"); got != "merge conflict in a.go, b.go" {
		t.Errorf("got %q", got)
	}
	if got := conflictIn(nil, "batch"); got != "merge conflict with batch" {
		t.Errorf("got %q", got)
	}
}

// The event stream gives the reason as blocked, and leaves it out of a record without one.
func TestEventStreamRecordsWhyAMergeIsBlocked(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	log.Begin(repo, RunStart{Started: time.Now()})
	log.Record(Event{Kind: EvHold, Ticket: "k-1", Blocked: "main checkout has uncommitted changes", Text: "HOLD: DIRTY_TREE: …"})
	log.Record(Event{Kind: EvHold, Ticket: "k-2", Text: "HOLD: PAUSED: …"})
	log.End(ExitDirty)
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	recs := records(t, repo)
	if len(recs) != 4 {
		t.Fatalf("records %v", recs)
	}
	has(t, recs[1], map[string]any{"kind": "hold", "ticket": "k-1", "blocked": "main checkout has uncommitted changes"})
	has(t, recs[2], map[string]any{"kind": "hold", "ticket": "k-2"}, "blocked")
}

// A check that fails on the rebased branch is the ticket's own work to look at, not its merge's:
// its warning says nothing of a blocked merge. A checkout off Base blocks it, saying so.
func TestAFailingCheckIsNotABlockedMerge(t *testing.T) {
	t.Parallel()
	f := newMergeFixture(t, "exit 3")
	wt := f.ticket(t, "k-1", "a.txt", "a\n")
	f.onMain(t, "b.txt", "b\n")
	if s := f.orch.merge(context.Background(), worker{id: "k-1", br: "wt/k-1", wt: wt, tab: "tab"}); s != nil {
		t.Fatal(s)
	}
	f.sink.mu.Lock()
	evs := f.sink.events
	f.sink.mu.Unlock()
	for _, ev := range evs {
		if ev.Kind == EvWarn && ev.Aside && (ev.Detail != "checks failed" || ev.Blocked != "") {
			t.Errorf("warning %+v, want checks failed and no blocked merge", ev)
		}
	}

	f = newMergeFixture(t, "true")
	f.git(f.repo, "branch", "other")
	wt = f.ticket(t, "k-2", "c.txt", "c\n")
	f.git(f.repo, "switch", "-q", "other")
	s := f.orch.merge(context.Background(), worker{id: "k-2", br: "wt/k-2", wt: wt, tab: "tab"})
	if s == nil || s.kind != stopDirtyTree || s.blocked != "main checkout is not on main" {
		t.Errorf("stop %+v, want DIRTY_TREE blocking k-2: main checkout is not on main", s)
	}
}
