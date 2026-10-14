package dispatch

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// pickNext counts the ready tickets it holds back by what holds them, apart from those queued for a
// slot; a ticket skipped (running, or set aside) counts in neither.
func TestPickNextCountsHeldBackTicketsByReason(t *testing.T) {
	kinds := map[string]holdKind{"o": heldOverlap, "p": heldParent, "b": heldBlocked, "f": heldFollowUp, "o2": heldOverlap}
	held := func(t Ticket) holdKind { return kinds[t.ID] }
	ready := []Ticket{{ID: "o"}, {ID: "a"}, {ID: "p"}, {ID: "b"}, {ID: "f"}, {ID: "o2"}, {ID: "c"}, {ID: "r"}}
	tk, q, _ := pickNext(ready, map[string]bool{"r": true, "o2": true}, held, 1, "")
	want := queue{queued: 1, held: HeldBack{Overlap: 1, Parent: 1, Merge: 2}}
	if tk == nil || tk.ID != "a" || q != want {
		t.Errorf("got %v, %+v, want a, %+v", tk, q, want)
	}
	if n := q.held.Total(); n != 4 {
		t.Errorf("total %d, want 4", n)
	}
	if _, q, _ := pickNext([]Ticket{{ID: "a"}, {ID: "c"}}, nil, held, 0, ""); q != (queue{queued: 1}) {
		t.Errorf("none held: got %+v", q)
	}
}

// queueHolds is each dispatch's and queue update's queue, "queued+held back", with the held back
// by reason, as the dashboard gets them.
func (s *runSink) queueHolds() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var l []string
	for _, ev := range s.events {
		switch ev.Kind {
		case EvDispatch:
			l = append(l, fmt.Sprintf("%s %d+%d %+v", ev.Ticket, ev.Queued, ev.Held.Total(), ev.Held))
		case EvQueue:
			l = append(l, fmt.Sprintf("queue %d+%d %+v", ev.Queued, ev.Held.Total(), ev.Held))
		}
	}
	return l
}

// A ready ticket skipped for overlapping a running one, with a slot free, is held back, not
// queued: the dispatch beside it and the queue update once the slot is free again say so, as does
// the event stream; once nothing holds it back, the count is gone.
func TestATicketSkippedForAnOverlapIsHeldBack(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "fix the merge", 1)
		h.beads.describe("A", "When `Loop.merge` aborts a rebase, say why.")
		h.beads.add("B", "time the merge", 2)
		h.beads.describe("B", "Log how long Loop.merge() takes.")
		h.beads.add("C", "pick faster", 3)
		h.beads.describe("C", "Speed up pickNext().")
		h.worker("A", func(w *fakeWorker) AgentState {
			time.Sleep(3 * readyPoll) // a few polls, with C finished and a slot free
			return finishes("a.txt")(w)
		})
		h.worker("B", finishes("b.txt"))
		h.worker("C", finishes("c.txt"))
		o := h.loop()
		o.log.Begin(h.repo, RunStart{Started: time.Now(), Version: "v1", Repo: h.repo, Branch: "main", Concurrency: 2})
		code := o.Run(context.Background())
		o.log.End(code)
		if err := o.log.Close(); err != nil {
			t.Fatal(err)
		}
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		overlap := HeldBack{Overlap: 1}
		got := h.sink.queueHolds()
		for _, want := range []string{
			fmt.Sprintf("C 0+1 %+v", overlap),     // B is held back as C takes the free slot
			fmt.Sprintf("queue 0+1 %+v", overlap), // and still once C has finished
			fmt.Sprintf("B 0+0 %+v", HeldBack{}),  // nothing once A has merged
		} {
			if !slices.Contains(got, want) {
				t.Errorf("no %q in\n%v", want, got)
			}
		}

		var queues []map[string]any
		for _, r := range records(t, h.repo) {
			if r["kind"] == "queue" || r["kind"] == "dispatch" {
				queues = append(queues, r)
			}
		}
		i := slices.IndexFunc(queues, func(r map[string]any) bool { return r["held"] != nil })
		if i < 0 {
			t.Fatalf("no record gives the tickets held back: %v", queues)
		}
		has(t, queues[i], map[string]any{"queued": 0.0, "held": 1.0})
		if by, _ := queues[i]["held_by"].(map[string]any); len(by) != 1 || by["overlap"] != 1.0 {
			t.Errorf("held_by %v, want overlap 1", queues[i]["held_by"])
		}
		has(t, queues[len(queues)-1], map[string]any{}, "held", "held_by")
	})
}

// With a slot free, the tickets that wait for a solo ticket running are held back behind it, not
// queued for a slot.
func TestTicketsBehindASoloTicketAreHeldBack(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("S", "alone", 1, SoloLabel)
		h.beads.add("A", "first", 2)
		h.beads.add("B", "second", 3)
		h.worker("S", func(w *fakeWorker) AgentState {
			time.Sleep(2 * readyPoll)
			return finishes("s.txt")(w)
		})
		h.worker("A", finishes("a.txt"))
		h.worker("B", finishes("b.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		want := fmt.Sprintf("queue 0+2 %+v", HeldBack{Solo: 2})
		if got := h.sink.queueHolds(); !slices.Contains(got, want) {
			t.Errorf("no %q in\n%v", want, got)
		}
	})
}
