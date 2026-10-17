package dispatch

import (
	"cmp"
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
	t      Ticket
	w      workerSpot
	resume *Session // the session to resume, its worker gone from its tab; nil: its worker is there
}

// followAsked reads each asked ticket that isn't running and acts on what its worker did in its
// tab. One it claimed again or closed is to be adopted: it comes back, its row leaves "? for you",
// and the caller counts it running from then on, whatever the free slots. With taking false (the
// run takes no more tickets) only a closed one is, to be merged, and one whose worker is gone (see
// below); one in progress is left to leaveAsked. One still in progress whose worker is idle with
// the question open waits on, as does one Herdr can't tell about, and one the last run left running
// whose worker is idle: it waits for the maintainer, as it did when that run stopped. One its
// worker deferred is set aside as deferred. One in progress whose worker is gone is adopted to have
// its session resumed, once in a run (see resumeSession), whatever taking: an adoption counts toward
// no limit. Without a session to resume, it stops the run, as a paused ticket does: the reason is
// returned with the tickets adopted before it was found, and said at once; with taking false it is
// dropped instead (askedDropped), as the run would only stop for it again and again. taking is
// runState.taking, and the tickets running are runState.inflight.
func (r *runState) followAsked() ([]adoption, *stopReason) {
	o, ctx, running, taking := r.o, r.ctx, r.inflight, r.taking()
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
			adopt = append(adopt, r.answeredInTab(t, w))
		case StatusInProgress:
			st, err := o.earlierState(ctx, id, w, 1)
			if ctx.Err() != nil {
				return adopt, nil
			}
			switch {
			case err != nil:
				o.log.Raw("", err)
			case st == StateGone:
				if s, ok := o.sessionOf(w.wt, w.hooks); ok && o.claimSessionResume(id) {
					adopt = append(adopt, r.resumedInTab(t, w, s))
					break
				}
				if !taking {
					o.askedDropped(ctx, id, w)
					break
				}
				return adopt, o.askedGone(ctx, id, w, len(running)+len(adopt))
			case !taking, st == StateUnknown,
				(st == StateIdle || st == StateDone) && (OpenQuestion(t) != nil || w.question == ""):
				// left as it is: read again at the next poll, or labelled as the run ends
			default:
				adopt = append(adopt, r.answeredInTab(t, w))
			}
		}
	}
	return adopt, nil
}

// answeredInTab says asked ticket t comes back, its worker w having claimed it again or closed it
// in its tab, and records its footprint; the caller counts it running. One the last run left
// running, with no question, comes back the same way.
func (r *runState) answeredInTab(t Ticket, w workerSpot) adoption {
	o, id := r.o, t.ID
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
	r.startFootprint(t)
	return adoption{t: t, w: w}
}

// askedDeferred sets aside asked ticket t, which its worker deferred in its tab, following the
// worker left there should it close t (see leaveOpen), and hands it to triage as triageDeferred
// does. ctx is the run's, so that after Ctrl+C no evidence is gathered (triageTaking). Run calls it
// on its own goroutine, so the deferral is offered (offerTriage).
func (o *Loop) askedDeferred(ctx context.Context, t Ticket, w workerSpot) {
	id := t.ID
	o.setAsked(id, nil)
	o.markAside(id)
	o.emit(Event{Kind: EvDeferred, Ticket: id, Title: t.Title, Detail: "by the worker", Text: fmt.Sprintf(
		"  %s deferred by worker after %s; worktree %s and tab %s left open", id, w.after(), w.wt, w.tab)})
	o.leaveOpen(context.WithoutCancel(ctx), worker{id: id, br: branchOf(id), wt: w.wt, tab: w.tab,
		agent: o.agentName(id), hooks: w.hooks})
	if o.triageTaking(ctx) {
		o.offerTriage(ctx, o.gatherDeferral(ctx, id, "the worker deferred it", w.wt))
	}
}

// askedGone notes an asked ticket left in progress by a worker no longer in its tab, and returns
// the reason the run stops for it, as for a paused ticket, holding it at once with n running. The
// ticket stays asked, so leaveAsked labels it.
func (o *Loop) askedGone(ctx context.Context, id string, w workerSpot, n int) *stopReason {
	o.noteWorkerGone(ctx, id, w.tab, w.wt, w.after())
	s := halt(ExitStuck, stopPaused, ": %s still in_progress after %s, its worker gone from tab %s "+
		"(worktree %s); stopping so it can be looked at", id, w.after(), w.tab, w.wt).over(id)
	o.emit(Event{Kind: EvHold, Ticket: id, Text: holdLine(s, n)})
	return s
}

// askedDropped warns about and notes an asked ticket left in progress by a worker no longer in its
// tab, which can't be resumed while the run takes no more tickets, and stops following it, as
// carriedStands drops one carried over without a session: stopping the run for it (askedGone) would
// save it for the next run, which stops for it again if it too takes no more tickets.
func (o *Loop) askedDropped(ctx context.Context, id string, w workerSpot) {
	o.setAsked(id, nil)
	o.warnWorkerGone(ctx, id, w, "so it is no longer followed")
}

// warnWorkerGone warns about and notes ticket id, in progress with its worker w gone from its tab and
// dropped, as what says.
func (o *Loop) warnWorkerGone(ctx context.Context, id string, w workerSpot, what string) {
	o.noteWorkerGone(ctx, id, w.tab, w.wt, w.after())
	o.emit(Event{Kind: EvWarn, Ticket: id, Aside: true, Detail: "in progress, its worker gone", Text: fmt.Sprintf(
		"  WORKER_GONE: %s is in progress after %s, but its worker is gone from tab %s (worktree %s), "+
			"%s; reopen it to run it again (bd update %s --status open), or finish it by hand",
		id, w.after(), w.tab, w.wt, what, id)})
}

// noteWorkerGone notes on ticket id that its worker is gone from tab, with the ticket still in
// progress after what after says, if anything, in worktree wt.
func (o *Loop) noteWorkerGone(ctx context.Context, id, tab, wt, after string) {
	o.appendNotes(context.WithoutCancel(ctx), id, workerGoneNote(tab, wt, after)+".")
}

// workerGoneNote says that the worker in tab is gone, with its ticket still in progress after what
// after says, if anything, in worktree wt; the caller ends the sentence.
func workerGoneNote(tab, wt, after string) string {
	if after != "" {
		after = " after " + after
	}
	return fmt.Sprintf("Orchestra: the worker in tab %s is gone, with the ticket still in_progress%s (worktree %s)",
		tab, after, wt)
}

// leaveAsked reads each asked ticket once more as the run ends. Its worker may have carried on in
// its tab, and nothing merges the ticket once orchestra has gone: one closed or in progress is
// labelled UnmergedLabel, as leaveRunning labels a ticket left running. Both stay asked, so
// saveCarried leaves them to the next run, which merges a closed one as it does one closed between
// runs. One its worker deferred is set aside as deferred. ctx is the run's: the reads and
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
			kind := asideAskedUnmerged
			if w.question == "" {
				kind = asideLeftUnmerged // carried over from the last run, which left it running
			}
			text := fmt.Sprintf("  %s: %s was closed in tab %s after %s, "+
				"and the run ends before merging it; worktree %s left for the next run to merge", kind, id, w.tab, w.after(), w.wt)
			// Not leaveFor: the warning says whether bd labelled it.
			if o.leaveUnmerged(keep, id, string(kind)) {
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

// workerSpot is where a ticket set aside to wait on a question left its worker, which may carry
// on once the question is answered, and the question. Carried over from the last run (see
// loadCarried), it may instead be a worker that run left running when it stopped, with no question.
type workerSpot struct {
	tab, wt  string
	pane     string // the pane it was started in, where it is looked for unnamed (see earlierState); "" if not known
	fork     string // the commit on Base its branch was cut from, as a run that left it behind found it; "" if not known
	question string // its ID
	title    string
	hooks    bool     // it reports through hooks
	left     stopKind // why the last run left it running; "" for a question
}

// after is what the worker was left after, for a line about it: "its question Q", or "the last run
// left it running (PAUSED)".
func (w workerSpot) after() string {
	if w.question != "" {
		return "its question " + w.question
	}
	return "the last run left it running (" + w.leftWhy() + ")"
}

// leftWhy is why the last run left the worker running, as its final line began.
func (w workerSpot) leftWhy() string {
	return cmp.Or(string(w.left), "stopped")
}

// setAsked records ticket id as set aside waiting on a question, its worker left as w; nil: no
// longer.
func (o *Loop) setAsked(id string, w *workerSpot) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if w == nil {
		delete(o.askedIDs, id)
		return
	}
	o.askedIDs[id] = *w
}

// asked returns where ticket id, set aside waiting on a question, left its worker.
func (o *Loop) asked(id string) (workerSpot, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	w, ok := o.askedIDs[id]
	return w, ok
}

func (o *Loop) isAsked(id string) bool {
	_, ok := o.asked(id)
	return ok
}

// askedSet is the tickets waiting on a question, or carried over from the last run.
func (o *Loop) askedSet() map[string]bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	set := make(map[string]bool, len(o.askedIDs))
	for id := range o.askedIDs {
		set[id] = true
	}
	return set
}
