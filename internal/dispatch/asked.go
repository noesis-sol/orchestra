package dispatch

import (
	"context"
	"fmt"
	"maps"
	"slices"
)

// A ticket set aside to wait on a question comes back through bd ready once the question is
// answered, which lists it only while it is open. Its worker, answered in its tab, may claim it
// again and close it before a slot is free, and then it would never come back: Run follows the
// asked tickets by their own status as well, on each poll and before the run ends.

// adoption is an asked ticket whose worker carried on in its tab, as bd showed it, with where
// that worker is.
type adoption struct {
	t Ticket
	w askedWorker
}

// followAsked reads each asked ticket that isn't running and acts on what its worker did in its
// tab. One it claimed again or closed is to be adopted: it comes back, its row leaves "? for you",
// and the caller counts it running from then on, whatever the free slots. With taking false (the
// run takes no more tickets) only a closed one is, to be merged; one in progress is left to
// leaveAsked. One still in progress whose worker is idle with the question open waits on, as
// does one Herdr can't tell about. One its worker deferred is set aside as deferred. One in
// progress whose worker is gone stops the run, as a paused ticket does: the reason is returned
// with the tickets adopted before it was found, and said at once. running is the tickets running.
func (o *Loop) followAsked(ctx context.Context, running map[string]bool, taking bool) ([]adoption, *stopReason) {
	var adopt []adoption
	for _, id := range o.askedList() {
		if running[id] {
			continue // its worker, back from the question, hasn't settled yet
		}
		w, _ := o.asked(id)
		t, err := o.tickets.Show(ctx, id)
		if ctx.Err() != nil {
			return adopt, nil
		}
		if err != nil {
			o.log.Raw("", err) // read again at the next poll
			continue
		}
		switch t.Status {
		case "deferred":
			o.askedDeferred(ctx, id, w)
		case "closed":
			adopt = append(adopt, o.answeredInTab(t, w))
		case "in_progress":
			st, err := o.agents.Status(ctx, o.agentName(id))
			if ctx.Err() != nil {
				return adopt, nil
			}
			switch {
			case err != nil:
				o.log.Raw("", err)
			case st == StateGone:
				return adopt, o.askedGone(ctx, id, w, len(running)+len(adopt))
			case !taking, st == StateUnknown, (st == StateIdle || st == StateDone) && OpenQuestion(t) != nil:
				// left as it is: read again at the next poll, or labelled as the run ends
			default:
				adopt = append(adopt, o.answeredInTab(t, w))
			}
		}
	}
	return adopt, nil
}

// answeredInTab says asked ticket t comes back, its worker w having claimed it again or closed it
// in its tab, and records its footprint; the caller counts it running.
func (o *Loop) answeredInTab(t Ticket, w askedWorker) adoption {
	id := t.ID
	o.setAsked(id, nil) // back from a question: set aside again, it stays out
	o.setParent(id, t.Parent)
	what := "claimed again"
	if t.Status == "closed" {
		what = "closed"
	}
	o.emit(Event{Kind: EvAnswered, Ticket: id, Detail: w.question + ": " + w.title, Text: fmt.Sprintf(
		"  ANSWERED: %s was %s in tab %s after %s (%s), so its worker is adopted", id, what, w.tab, w.question, w.title)})
	o.startFootprint(t)
	return adoption{t: t, w: w}
}

// askedDeferred sets aside an asked ticket its worker deferred in its tab.
func (o *Loop) askedDeferred(ctx context.Context, id string, w askedWorker) {
	o.setAsked(id, nil)
	o.markAside(id)
	o.emit(Event{Kind: EvDeferred, Ticket: id, Detail: "by the worker", Text: fmt.Sprintf(
		"  %s deferred by worker after its question %s; worktree %s and tab %s left open", id, w.question, w.wt, w.tab)})
	o.triageDeferred(ctx, id, "the worker deferred it", w.wt)
}

// askedGone notes an asked ticket left in progress by a worker no longer in its tab, and returns
// the reason the run stops for it, as for a paused ticket, holding it at once with n running. The
// ticket stays asked, so leaveAsked labels it.
func (o *Loop) askedGone(ctx context.Context, id string, w askedWorker, n int) *stopReason {
	o.appendNotes(context.WithoutCancel(ctx), id, fmt.Sprintf(
		"Orchestra: the worker in Herdr tab %s is gone, with the ticket still in_progress after its question %s "+
			"(worktree %s).", w.tab, w.question, w.wt))
	s := halt(ExitStuck, stopPaused, ": %s still in_progress after its question %s, its worker gone from tab %s "+
		"(worktree %s); stopping so it can be looked at", id, w.question, w.tab, w.wt)
	hold := "HOLD: " + s.Error()
	if n > 0 {
		hold += fmt.Sprintf("; no new tickets while the %d running finish", n)
	}
	o.emit(Event{Kind: EvHold, Ticket: id, Text: hold})
	return s
}

// leaveAsked reads each asked ticket once more as the run ends. Its worker may have carried on in
// its tab, and nothing merges the ticket once orchestra has gone: one closed or in progress is
// labelled UnmergedLabel, as leaveRunning labels a ticket left running, and a closed one is left
// for review. One its worker deferred is set aside as deferred.
func (o *Loop) leaveAsked(ctx context.Context) {
	c := o.cfg
	for _, id := range o.askedList() {
		w, _ := o.asked(id)
		t, err := o.tickets.Show(ctx, id)
		if err != nil {
			o.log.Raw("", err)
			continue
		}
		switch t.Status {
		case "deferred":
			o.askedDeferred(ctx, id, w)
		case "closed":
			text := fmt.Sprintf("  ASKED_UNMERGED: %s was closed in tab %s after its question %s, "+
				"and the run ends before merging it; worktree %s left for review", id, w.tab, w.question, w.wt)
			if o.leaveUnmerged(ctx, id, "ASKED_UNMERGED") {
				text += fmt.Sprintf(", labelled '%s': tickets it blocks wait until wt/%s is merged into %s, "+
					"in later runs too", UnmergedLabel, id, c.Base)
			}
			o.emit(Event{Kind: EvWarn, Ticket: id, Aside: true, Text: text})
		case "in_progress":
			o.mu.Lock()
			labelled := o.labelled[id]
			o.mu.Unlock()
			if labelled || !o.label(ctx, id) {
				continue
			}
			o.emit(Event{Kind: EvInfo, Ticket: id, Text: fmt.Sprintf(
				"  %s is in progress after its question (tab %s) and labelled '%s': "+
					"tickets it blocks wait until wt/%s is merged into %s, in later runs too",
				id, w.tab, UnmergedLabel, id, c.Base)})
		}
	}
}

// askedList is the tickets waiting on a question, in order.
func (o *Loop) askedList() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Sorted(maps.Keys(o.askedIDs))
}
