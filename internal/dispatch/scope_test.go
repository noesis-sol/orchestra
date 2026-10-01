package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Scoped runs (--ticket) and parents that run last, as whole runs.

// kind sets the ticket's issue type.
func (b *fakeBeads) kind(id, typ string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tickets[id].IssueType = typ
}

// filed adds a ticket as a worker files one during the run: not a subticket of anything.
func (b *fakeBeads) filed(id, title string, prio int) {
	b.add(id, title, prio)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tickets[id].CreatedAt = time.Now().UTC().Format(time.RFC3339)
}

// prompted records the prompt each worker was launched with.
type prompted struct{ got map[string]string }

// then reads the worker's launch prompt, then does b.
func (p *prompted) then(b behaviour) behaviour {
	return func(w *fakeWorker) AgentState {
		raw, err := os.ReadFile(filepath.Join(w.wt, ".orchestra", "run", "prompt.md"))
		if err != nil {
			w.t.Error(err)
		}
		p.got[w.id] = string(raw)
		return b(w)
	}
}

func scopedHarness(t *testing.T, root string) *harness {
	h := newHarness(t)
	h.cfg.Ticket, h.cfg.ExcludeTypes = root, []string{"epic"}
	return h
}

// A run of R takes R and its descendants only, each parent after its children, and ends saying
// all of them are merged. The unrelated ready ticket U, first in priority, is never started.
func TestScopedRunTakesOnlyTheTicketAndItsSubtickets(t *testing.T) {
	t.Parallel()
	h := scopedHarness(t, "R")
	h.beads.add("U", "unrelated", 0)
	h.beads.add("R", "root", 2)
	h.beads.sub("R.1", "R", "child", 1)
	h.beads.sub("R.2", "R", "second child", 3)
	h.beads.sub("R.1.1", "R.1", "grandchild", 0)
	p := &prompted{got: map[string]string{}}
	h.worker("R.1.1", p.then(finishes("r11.txt")))
	h.worker("R.1", finishes("r1.txt"))
	h.worker("R.2", finishes("r2.txt"))
	h.worker("R", p.then(finishes("r.txt")))
	o, code := h.run()
	want := "READY_EMPTY after 4 tickets; SCOPE_DONE: R and its 3 subtickets are merged"
	if code != ExitOK || o.Final() != want {
		t.Fatalf("exit %d, final %q, want %q\n%s", code, o.Final(), want, h.sink.text())
	}
	var order []string
	for _, d := range h.sink.of(EvDispatch) {
		order = append(order, strings.Fields(d)[0])
	}
	if !equal(order, []string{"R.1.1", "R.1", "R.2", "R"}) {
		t.Errorf("dispatched %v, want R.1.1 R.1 R.2 R", order)
	}
	if st, _ := h.beads.Status(context.Background(), "U"); st != "open" {
		t.Errorf("U is %s; a scoped run must leave it alone", st)
	}
	ev := h.sink.text()
	for _, want := range []string{"START orchestra ", " on main · ticket R (", "R.1 waits: its subtickets are not all closed and merged",
		"R waits: its subtickets are not all closed and merged"} {
		if !strings.Contains(ev, want) {
			t.Errorf("events lack %q:\n%s", want, ev)
		}
	}
	if note := "File a follow-up that belongs to this work as a child of R (bd create --parent R …)"; !strings.Contains(p.got["R.1.1"], note) {
		t.Errorf("R.1.1's prompt lacks the scope note:\n%s", p.got["R.1.1"])
	} else if strings.Contains(p.got["R"], note) {
		t.Errorf("R's own worker was told to file children of R, which would keep it open:\n%s", p.got["R"])
	}
}

// Outside a scoped run too, a parent waits for its subtickets; an epic is never dispatched, and
// once its last subticket merges the log says it can be closed.
func TestParentsRunLastInEveryRun(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.cfg.ExcludeTypes = []string{"epic"}
	h.beads.add("P", "parent", 0)
	h.beads.sub("C", "P", "child", 2)
	h.beads.add("E", "epic", 1)
	h.beads.kind("E", "epic")
	h.beads.sub("F", "E", "epic's child", 3)
	h.worker("C", finishes("c.txt"))
	h.worker("P", finishes("p.txt"))
	h.worker("F", finishes("f.txt"))
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	var order []string
	for _, d := range h.sink.of(EvDispatch) {
		order = append(order, strings.Fields(d)[0])
	}
	if !equal(order, []string{"C", "P", "F"}) {
		t.Errorf("dispatched %v, want C P F", order)
	}
	if ev, want := h.sink.text(), "all of E's subtickets are merged; close it with: bd close E (or bd epic close-eligible)"; !strings.Contains(ev, want) {
		t.Errorf("events lack %q:\n%s", want, ev)
	}
	if strings.Contains(h.sink.text(), "all of P's subtickets") {
		t.Error("P isn't an epic: it runs rather than waiting to be closed")
	}
}

// A follow-up filed as a subticket of the scope joins the run; one filed without --parent waits
// for a later run, and the log and the run report name it.
func TestScopedRunPicksUpFollowUpsFiledAsSubtickets(t *testing.T) {
	t.Parallel()
	h := scopedHarness(t, "R")
	h.beads.add("R", "root", 2)
	h.beads.sub("R.1", "R", "child", 1)
	h.worker("R.1", func(w *fakeWorker) AgentState {
		w.beads.sub("R.2", "R", "follow-up in scope", 1)
		w.beads.filed("X", "follow-up elsewhere", 0)
		return finishes("r1.txt")(w)
	})
	h.worker("R.2", finishes("r2.txt"))
	h.worker("R", finishes("r.txt"))
	o, code := h.run()
	want := "READY_EMPTY after 3 tickets; SCOPE_DONE: R and its 2 subtickets are merged"
	if code != ExitOK || o.Final() != want {
		t.Fatalf("exit %d, final %q, want %q\n%s", code, o.Final(), want, h.sink.text())
	}
	for _, d := range h.sink.of(EvDispatch) {
		if strings.HasPrefix(d, "X ") {
			t.Errorf("X is outside the scope, yet dispatched: %s", d)
		}
	}
	if ev, want := h.sink.text(), "filed during the run outside R's scope, left for a later run: X (follow-up elsewhere)"; !strings.Contains(ev, want) {
		t.Errorf("events lack %q:\n%s", want, ev)
	}
	in := o.reviewInput(context.Background(), code, o.Final())
	for _, want := range []string{"Run of ticket R and its subtickets only, on branch main",
		"## Follow-ups filed in this run outside the scope of R, left for a later run\n\n<evidence id=\"",
		"\">\nX: follow-up elsewhere [open]\n</evidence id=\""} {
		if !strings.Contains(in, want) {
			t.Errorf("report input lacks %q:\n%s", want, in)
		}
	}
}

// A scope left unfinished ends with SCOPE_OPEN, saying why each subticket isn't done.
func TestScopeOpenSaysWhyEachSubticketIsNotDone(t *testing.T) {
	t.Parallel()
	h := scopedHarness(t, "E")
	h.beads.add("B", "blocker outside", 0)
	h.beads.add("E", "epic", 1)
	h.beads.kind("E", "epic")
	h.beads.sub("E.1", "E", "done", 1)
	h.beads.sub("E.2", "E", "blocked", 1)
	h.beads.link("E.2", "B", "blocks")
	h.beads.sub("E.3", "E", "set aside", 2)
	h.beads.sub("E.4", "E", "asks", 3)
	h.worker("E.1", finishes("e1.txt"))
	h.worker("E.3", func(w *fakeWorker) AgentState { w.claim(); w.deferIt(); return "idle" })
	h.worker("E.4", func(w *fakeWorker) AgentState { w.claim(); w.ask("Q", "which way?"); return "idle" })
	o, code := h.run()
	want := "READY_EMPTY after 3 tickets; SCOPE_OPEN: E: 3 of its 4 subtickets not done: " +
		"E.2 (blocked by B outside the scope), E.3 (set aside in this run), E.4 (waiting on your answer to Q)"
	if code != ExitOK || o.Final() != want {
		t.Fatalf("exit %d, final %q, want %q\n%s", code, o.Final(), want, h.sink.text())
	}
	if strings.Contains(h.sink.text(), "B dispatching") {
		t.Error("B is outside the scope, yet dispatched")
	}
}

// An epic's scope ends done once its subtickets merge, leaving the epic for the maintainer to close.
func TestScopedEpicIsLeftToClose(t *testing.T) {
	t.Parallel()
	h := scopedHarness(t, "E")
	h.beads.add("E", "epic", 1)
	h.beads.kind("E", "epic")
	h.beads.sub("E.1", "E", "only child", 1)
	h.worker("E.1", finishes("e1.txt"))
	o, code := h.run()
	want := "READY_EMPTY after 1 tickets; SCOPE_DONE: E's 1 subticket is merged; close it with: bd close E"
	if code != ExitOK || o.Final() != want {
		t.Fatalf("exit %d, final %q, want %q\n%s", code, o.Final(), want, h.sink.text())
	}
	if st, _ := h.beads.Status(context.Background(), "E"); st != "open" {
		t.Errorf("E is %s; orchestra doesn't close an epic itself", st)
	}
}

// A ticket without subtickets is a scope of one; the limit ends a scoped run as it does any other,
// naming what is left.
func TestScopeOfOneAndTheLimit(t *testing.T) {
	t.Parallel()
	h := scopedHarness(t, "A")
	h.beads.add("A", "alone", 1)
	h.beads.add("Z", "unrelated", 0)
	h.worker("A", finishes("a.txt"))
	o, code := h.run()
	if want := "READY_EMPTY after 1 tickets; SCOPE_DONE: A and its 0 subtickets are merged"; code != ExitOK || o.Final() != want {
		t.Fatalf("exit %d, final %q, want %q\n%s", code, o.Final(), want, h.sink.text())
	}

	h = scopedHarness(t, "R")
	h.cfg.Limit = 1
	h.beads.add("R", "root", 2)
	h.beads.sub("R.1", "R", "child", 1)
	h.beads.sub("R.2", "R", "second child", 2)
	h.worker("R.1", finishes("r1.txt"))
	o, code = h.run()
	if want := "LIMIT_REACHED at 1 tickets; SCOPE_OPEN: R: 1 of its 2 subtickets not done: R.2 (not started)"; code != ExitOK || o.Final() != want {
		t.Fatalf("exit %d, final %q, want %q\n%s", code, o.Final(), want, h.sink.text())
	}
}

// A subticket closed but not yet merged, running or left unmerged, still holds its parent: bd no
// longer lists it as open.
func TestParentWaitsForAClosedSubticketToMerge(t *testing.T) {
	o := New(Config{}, nil, "", Deps{Tickets: fakeTickets{}})
	o.setParent("C", "P")
	o.setParent("D", "Q")
	if parents, err := o.openParents(context.Background(), map[string]bool{"C": true}); err != nil || !parents["P"] || parents["Q"] {
		t.Errorf("C running: %v %v, want P only", parents, err)
	}
	o.unmerged = map[string]string{"D": "MERGE_CONFLICT"}
	if parents, _ := o.openParents(context.Background(), nil); parents["P"] || !parents["Q"] {
		t.Errorf("D unmerged: %v, want Q only", parents)
	}
}

// A scoped run stopped after its running tickets says what is left of its scope, as any end does.
func TestDrainedScopedRunSaysWhatIsLeft(t *testing.T) {
	t.Parallel()
	h := scopedHarness(t, "R")
	h.beads.add("R", "root", 2)
	h.beads.sub("R.1", "R", "child", 1) // no behaviour: a worker on it fails the test
	o := h.loop()
	o.Drain("by SIGUSR1")
	if want := "DRAINED after 0 tickets; SCOPE_OPEN: R: 1 of its 1 subticket not done: R.1 (not started)"; o.Run(context.Background()) != ExitOK || o.Final() != want {
		t.Fatalf("final %q, want %q\n%s", o.Final(), want, h.sink.text())
	}
}
