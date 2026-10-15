package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// orchestra closes an epic once the run merges the last of its subtickets, as nothing else would:
// an epic is never dispatched.

// epicHarness is a timed run of every ready ticket, epics left out, given the tickets to run.
func epicHarness(t *testing.T) *harness {
	h := newTimedHarness(t)
	h.cfg.ExcludeTypes = []string{"epic"}
	return h
}

// epic adds an epic, a subticket of parent unless that is "".
func (b *fakeBeads) epic(id, parent string) {
	if parent == "" {
		b.add(id, "epic "+id, 1)
	} else {
		b.sub(id, parent, "epic "+id, 1)
	}
	b.kind(id, "epic")
}

// A subticket still running when another merges keeps the epic open; once it merges too, the
// epic closes with a reason naming it, and the log says so.
func TestEpicClosesWhenItsLastSubticketMerges(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := epicHarness(t)
		h.cfg.Concurrency = 2
		h.beads.epic("E", "")
		h.beads.sub("F", "E", "quick", 1)
		h.beads.sub("G", "E", "slow", 2)
		h.worker("F", finishes("f.txt"))
		h.worker("G", slowly(time.Hour, "g.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if st, _ := h.beads.Status(context.Background(), "E"); st != StatusClosed {
			t.Fatalf("E is %s, want closed\n%s", st, h.sink.text())
		}
		if got, want := h.beads.notesOf("E"), "closed: All 2 subtickets are merged; closed by orchestra after G"; got != want {
			t.Errorf("E's notes %q, want %q", got, want)
		}
		ev := h.sink.text()
		if strings.Count(ev, "closed epic E: all 2 subtickets are merged") != 1 {
			t.Errorf("the log should say once that E was closed:\n%s", ev)
		}
		if strings.Index(ev, "G closed (") > strings.Index(ev, "closed epic E") {
			t.Errorf("E was closed before G merged:\n%s", ev)
		}
		if !strings.Contains(h.logged(), "closed epic E: all 2 subtickets are merged") {
			t.Errorf("the log file lacks the line:\n%s", h.logged())
		}
	})
}

// An epic with a subticket still open, or closed but not merged, stays open.
func TestEpicWithASubticketNotDoneStaysOpen(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := epicHarness(t)
		h.beads.epic("E", "")
		h.beads.sub("F", "E", "done", 1)
		h.beads.sub("G", "E", "deferred", 2)
		h.beads.set("G", StatusDeferred)
		h.beads.epic("U", "")
		h.beads.sub("V", "U", "closed but not merged", 1)
		h.beads.sub("W", "U", "done", 2)
		h.worker("F", finishes("f.txt"))
		h.worker("V", closesUnnamed("v.txt"))
		h.worker("W", finishes("w.txt"))
		o, code := h.run()
		if code != ExitOK || !strings.HasPrefix(o.Final(), "READY_EMPTY after 3 tickets") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if v, _ := h.beads.Show(context.Background(), "V"); v.Status != StatusClosed || !HasLabel(v, UnmergedLabel) {
			t.Fatalf("V should be closed but not merged: %s %v\n%s", v.Status, v.Labels, h.sink.text())
		}
		for _, id := range []string{"E", "U"} {
			if st, _ := h.beads.Status(context.Background(), id); st != StatusOpen {
				t.Errorf("%s is %s, want open\n%s", id, st, h.sink.text())
			}
		}
		if ev := h.sink.text(); strings.Contains(ev, "closed epic") || strings.Contains(ev, "close it with") {
			t.Errorf("neither epic is done:\n%s", ev)
		}
	})
}

// An epic closed as its last subticket merges, itself the last open subticket of an epic, closes
// that one too.
func TestNestedEpicsCloseUpTheChain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := epicHarness(t)
		h.beads.epic("E", "")
		h.beads.epic("E.1", "E")
		h.beads.sub("E.1.1", "E.1", "leaf", 1)
		h.worker("E.1.1", finishes("leaf.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		for id, why := range map[string]string{
			"E.1": "closed: Its 1 subticket is merged; closed by orchestra after E.1.1",
			"E":   "closed: All 2 subtickets are merged; closed by orchestra after E.1.1",
		} {
			if st, _ := h.beads.Status(context.Background(), id); st != StatusClosed {
				t.Errorf("%s is %s, want closed", id, st)
			}
			if got := h.beads.notesOf(id); got != why {
				t.Errorf("%s's notes %q, want %q", id, got, why)
			}
		}
		ev := h.sink.text()
		inner, outer := strings.Index(ev, "closed epic E.1: its 1 subticket is merged"), strings.Index(ev, "closed epic E: all 2 subtickets are merged")
		if inner < 0 || outer < inner {
			t.Errorf("E.1 should be closed, then E:\n%s", ev)
		}
	})
}

// closeFails is a tracker whose bd close fails.
type closeFails struct{ *fakeBeads }

func (closeFails) Close(context.Context, string, string) error {
	return errors.New("database is locked")
}

// A failing bd close leaves the epic open, logs bd's error and how to close the epic, and the run
// goes on to its next ticket.
func TestEpicCloseFailingKeepsTheHint(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := epicHarness(t)
		h.beads.epic("E", "")
		h.beads.sub("F", "E", "only child", 1)
		h.beads.add("Z", "next", 2)
		h.worker("F", finishes("f.txt"))
		h.worker("Z", finishes("z.txt"))
		o := h.loop()
		o.notes = closeFails{h.beads}
		code := o.Run(context.Background())
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if st, _ := h.beads.Status(context.Background(), "E"); st != StatusOpen {
			t.Errorf("E is %s, want open", st)
		}
		want := "its 1 subticket is merged, but bd could not close E: database is locked; close it with: bd close E"
		if ev := h.sink.text(); !strings.Contains(ev, want) || strings.Contains(ev, "closed epic E") {
			t.Errorf("events lack %q:\n%s", want, ev)
		}
		if log := h.logged(); strings.Count(log, "database is locked") < 2 {
			t.Errorf("the log should carry bd's error, and the hint:\n%s", log)
		}
	})
}

// A scoped run on an epic whose bd close fails ends saying how to close it, as before.
func TestScopedEpicCloseFailingSaysHowToClose(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := scoped(newTimedHarness(t), "E")
		h.beads.epic("E", "")
		h.beads.sub("E.1", "E", "only child", 1)
		h.worker("E.1", finishes("e1.txt"))
		o := h.loop()
		o.notes = closeFails{h.beads}
		code := o.Run(context.Background())
		want := "READY_EMPTY after 1 tickets; SCOPE_DONE: E's 1 subticket is merged; close it with: bd close E"
		if code != ExitOK || o.Final() != want {
			t.Fatalf("exit %d, final %q, want %q\n%s", code, o.Final(), want, h.sink.text())
		}
	})
}
