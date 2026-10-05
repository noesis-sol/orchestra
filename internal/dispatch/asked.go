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
// does one Herdr can't tell about, and one the last run left running whose worker is idle: it
// waits for the maintainer, as it did when that run stopped. One its worker deferred is set aside
// as deferred. One in progress whose worker is gone stops the run, as a paused ticket does: the
// reason is returned with the tickets adopted before it was found, and said at once. running is
// the tickets running.
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
		case StatusDeferred:
			o.askedDeferred(ctx, t, w)
		case StatusClosed:
			adopt = append(adopt, o.answeredInTab(t, w))
		case StatusInProgress:
			st, err := o.earlierState(ctx, id, w, 1)
			if ctx.Err() != nil {
				return adopt, nil
			}
			switch {
			case err != nil:
				o.log.Raw("", err)
			case st == StateGone:
				return adopt, o.askedGone(ctx, id, w, len(running)+len(adopt))
			case !taking, st == StateUnknown,
				(st == StateIdle || st == StateDone) && (OpenQuestion(t) != nil || w.question == ""):
				// left as it is: read again at the next poll, or labelled as the run ends
			default:
				adopt = append(adopt, o.answeredInTab(t, w))
			}
		}
	}
	return adopt, nil
}

// answeredInTab says asked ticket t comes back, its worker w having claimed it again or closed it
// in its tab, and records its footprint; the caller counts it running. One the last run left
// running, with no question, comes back the same way.
func (o *Loop) answeredInTab(t Ticket, w askedWorker) adoption {
	id := t.ID
	o.setAsked(id, nil) // back from a question: set aside again, it stays out
	o.setParent(id, t.Parent)
	what := "claimed again"
	switch {
	case t.Status == StatusClosed:
		what = "closed"
	case w.question == "":
		what = "at work again"
	}
	if w.question == "" { // no question was answered
		o.info("  %s, which the last run left running (%s), is %s in tab %s, so its worker is adopted",
			id, w.leftWhy(), what, w.tab)
	} else {
		o.emit(Event{Kind: EvAnswered, Ticket: id, Title: t.Title, Detail: w.question + ": " + w.title, Text: fmt.Sprintf(
			"  ANSWERED: %s was %s in tab %s after %s (%s), so its worker is adopted", id, what, w.tab, w.question, w.title)})
	}
	o.startFootprint(t)
	return adoption{t: t, w: w}
}

// askedDeferred sets aside asked ticket t, which its worker deferred in its tab, and hands it to
// triage as triageDeferred does. ctx is the run's, so that after Ctrl+C no evidence is gathered
// (triageTaking). Run calls it on its own goroutine, so the deferral is offered (offerTriage).
func (o *Loop) askedDeferred(ctx context.Context, t Ticket, w askedWorker) {
	id := t.ID
	o.setAsked(id, nil)
	o.markAside(id)
	o.emit(Event{Kind: EvDeferred, Ticket: id, Title: t.Title, Detail: "by the worker", Text: fmt.Sprintf(
		"  %s deferred by worker after %s; worktree %s and tab %s left open", id, w.after(), w.wt, w.tab)})
	if o.triageTaking(ctx) {
		o.offerTriage(ctx, o.gatherDeferral(ctx, id, "the worker deferred it", w.wt))
	}
}

// askedGone notes an asked ticket left in progress by a worker no longer in its tab, and returns
// the reason the run stops for it, as for a paused ticket, holding it at once with n running. The
// ticket stays asked, so leaveAsked labels it.
func (o *Loop) askedGone(ctx context.Context, id string, w askedWorker, n int) *stopReason {
	o.appendNotes(context.WithoutCancel(ctx), id, fmt.Sprintf(
		"Orchestra: the worker in Herdr tab %s is gone, with the ticket still in_progress after %s (worktree %s).",
		w.tab, w.after(), w.wt))
	s := halt(ExitStuck, stopPaused, ": %s still in_progress after %s, its worker gone from tab %s "+
		"(worktree %s); stopping so it can be looked at", id, w.after(), w.tab, w.wt).over(id)
	o.emit(Event{Kind: EvHold, Ticket: id, Text: holdLine(s, n)})
	return s
}

// leaveAsked reads each asked ticket once more as the run ends. Its worker may have carried on in
// its tab, and nothing merges the ticket once orchestra has gone: one closed or in progress is
// labelled UnmergedLabel, as leaveRunning labels a ticket left running, and a closed one is left
// for review. One its worker deferred is set aside as deferred. ctx is the run's: the reads and
// labels go on after Ctrl+C, triage doesn't (see askedDeferred).
func (o *Loop) leaveAsked(ctx context.Context) {
	c := o.cfg
	keep := context.WithoutCancel(ctx)
	for _, id := range o.askedList() {
		w, _ := o.asked(id)
		t, err := o.tickets.Show(keep, id)
		if err != nil {
			o.log.Raw("", err)
			continue
		}
		switch t.Status {
		case StatusDeferred:
			o.askedDeferred(ctx, t, w)
		case StatusClosed:
			kind := "ASKED_UNMERGED"
			if w.question == "" {
				kind = "LEFT_UNMERGED" // carried over from the last run, which left it running
			}
			text := fmt.Sprintf("  %s: %s was closed in tab %s after %s, "+
				"and the run ends before merging it; worktree %s left for review", kind, id, w.tab, w.after(), w.wt)
			if o.leaveUnmerged(keep, id, kind) {
				text += fmt.Sprintf(", labelled '%s': tickets it blocks wait until wt/%s is merged into %s, "+
					"in later runs too", UnmergedLabel, id, c.Base)
			}
			o.emit(Event{Kind: EvWarn, Ticket: id, Aside: true, Detail: "closed, but the run ended before merging it",
				Text: text})
		case StatusInProgress:
			o.mu.Lock()
			labelled := o.labelled[id]
			o.mu.Unlock()
			if labelled || !o.label(keep, id) {
				continue
			}
			what := "after its question"
			if w.question == "" {
				what = "where the last run left it running"
			}
			o.emit(Event{Kind: EvInfo, Ticket: id, Text: fmt.Sprintf(
				"  %s is in progress %s (tab %s) and labelled '%s': "+
					"tickets it blocks wait until wt/%s is merged into %s, in later runs too",
				id, what, w.tab, UnmergedLabel, id, c.Base)})
		}
	}
}

// askedList is the tickets waiting on a question, in order.
func (o *Loop) askedList() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Sorted(maps.Keys(o.askedIDs))
}
