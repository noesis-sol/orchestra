package dispatch

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// A ticket set aside with its worker left in its tab (deferred by the worker, settled with the
// ticket unfinished, or its prompt never taken) is out of the run, but its worker may carry on
// there and close it: as when Claude Code continues a worker by itself once the account's usage
// limit resets. bd ready then counts it done, and nothing would merge it. So Run follows such a
// ticket by its status on each poll, as it does an asked ticket (see followAsked): one closed is
// merged once its worker has settled (LATE_CLOSE), and the tickets it blocks, and those filed from
// it, wait for it meanwhile (see pendingWhy). It is labelled UnmergedLabel as it is left, so that
// should it close once the run has ended, later runs hold them too (see loadUnmerged).

// leftOpen is a worker left in its tab as its ticket was set aside: where it is (its since is when
// it was left, as its hooks' reports since then are its own), and its branch's commit then (head),
// which is where any work it does from then on begins.
type leftOpen struct {
	w    worker
	head string
}

// leaveOpen records ticket w.id, just set aside with its worker w left in its tab, as left open,
// and labels it UnmergedLabel the first time, noting ForkKey, as leaveRunning does: while the ticket
// isn't closed the label holds nothing.
func (o *Loop) leaveOpen(ctx context.Context, w worker) {
	w.since = time.Now()
	head, err := o.checkout.Head(ctx, o.cfg.Repo, w.br) // "" on failure: its work is not taken for none
	o.log.Raw("", err)
	o.book.leaveOpen(w.id, leftOpen{w: w, head: head})
	o.mu.Lock()
	labelled := o.labelled[w.id]
	o.mu.Unlock()
	if !labelled {
		o.label(ctx, w.id)
	}
}

// leftOpenSpot is where ticket id's worker was left in its tab as the ticket was set aside, and
// whether it was.
func (o *Loop) leftOpenSpot(id string) (workerSpot, bool) {
	r, _ := o.book.get(id)
	return workerSpot{tab: r.left.w.tab, wt: r.left.w.wt, hooks: r.left.w.hooks}, r.disp == dispLeftOpen
}

// leftOpenList is the workers left in their tabs as their tickets were set aside, by ticket.
func (o *Loop) leftOpenList() []leftOpen {
	_, recs := o.book.matching(func(r *ticketRecord) bool { return r.disp == dispLeftOpen })
	l := make([]leftOpen, len(recs))
	for i, r := range recs {
		l[i] = r.left
	}
	return l
}

// leftOpenWhy is why ticket id, set aside with its worker left in its tab, has not merged, closed
// or not (see mergeState), or "" when it wasn't so left.
func (o *Loop) leftOpenWhy(id string) string {
	if r, _ := o.book.get(id); r.disp == dispLeftOpen {
		return leftInTab(r.left.w.tab)
	}
	return ""
}

// leftInTab is why a ticket whose worker was left in tab has not merged.
func leftInTab(tab string) string { return "its worker was left in tab " + tab }

// lateClose is a ticket closed in the tab its worker was left in, to be merged: as bd shows it, and
// that worker.
type lateClose struct {
	t    Ticket
	left leftOpen
}

// followLeft reads the tickets set aside with their workers left in their tabs, and returns those
// closed there since whose workers have settled: idle or done, or gone. A worker at work, or one the
// terminal can't tell about, is read again at the next poll, as is one bd or the terminal fails to
// say anything of; one gone with its ticket not closed is no longer followed. A worker the terminal
// has no agent under its name for is looked for in its pane first (see workerState). bd lists the tickets
// not closed in one call, and only one missing from the list is shown.
func (r *runState) followLeft() []lateClose {
	o, ctx := r.o, r.ctx
	left := o.leftOpenList()
	if len(left) == 0 {
		return nil
	}
	unclosed, err := o.tickets.Unclosed(ctx)
	if err != nil {
		if ctx.Err() == nil {
			o.log.Raw("", err) // read again at the next poll
		}
		return nil
	}
	status := make(map[string]TicketStatus, len(unclosed))
	for _, t := range unclosed {
		status[t.ID] = t.Status
	}
	var late []lateClose
	for _, lo := range left {
		id, tab := lo.w.id, lo.w.tab
		if r.inflight[id] {
			continue
		}
		t := Ticket{ID: id, Status: status[id]}
		if t.Status == "" {
			t, err = o.tickets.Show(ctx, id)
			switch {
			case ctx.Err() != nil:
				return late
			case errors.Is(err, ErrNoTicket):
				o.book.unfollow(id)
				o.info("  %s, set aside with its worker left in tab %s, is gone from bd; it is no longer followed", id, tab)
				continue
			case err != nil:
				o.log.Raw("", err)
				continue
			}
		}
		st, err := o.workerState(ctx, lo.w)
		switch {
		case ctx.Err() != nil:
			return late
		case err != nil:
			o.log.Raw("", err)
		case t.Status != StatusClosed && st == StateGone:
			o.book.unfollow(id)
			o.info("  %s's worker, left in tab %s as %s was set aside, is gone with it %s; it is no longer followed",
				id, tab, id, t.Status)
		case t.Status != StatusClosed, st == StateWorking, st == StateBlocked, st == StateUnknown:
			// read again at the next poll
		default:
			o.book.unfollow(id)
			late = append(late, lateClose{t: t, left: lo})
		}
	}
	return late
}

// mergeLate starts merging each ticket closed in the tab its worker was left in (see followLeft),
// whatever the free slots: its worker's work is done. It reports whether it started any.
func (r *runState) mergeLate() bool {
	o := r.o
	late := r.followLeft()
	for _, l := range late {
		id := l.t.ID
		o.emit(Event{Kind: EvInfo, Ticket: id, Text: fmt.Sprintf(
			"  LATE_CLOSE: %s was closed in tab %s after it was set aside; merging it now", id, l.left.w.tab)})
		tr := o.newTicketRun(r.ctx, l.t)
		r.launch(id, false, nil, func(ctx context.Context) *stopReason { return tr.lateClose(ctx, l.left) })
	}
	return len(late) > 0
}

// lateClose merges the ticket, closed in the tab its worker was left in (lo), as the worker's run
// would have had it closed then: conclude waits for the worker to settle, as of when it was left,
// and finish rebases, checks and merges it, or leaves it unmerged for review (MERGE_CONFLICT,
// CHECKS_FAILED, …), labelled. Merged, it is no longer set aside. It returns as work does.
func (r *ticketRun) lateClose(ctx context.Context, lo leftOpen) *stopReason {
	o, id := r.o, r.id
	w := lo.w
	w.started = time.Now()
	o.place(id, workerSpot{tab: w.tab, wt: w.wt, hooks: w.hooks})
	st := Status{Ticket: id, Title: r.t.Title, Tab: w.tab, Started: w.started}
	o.showActive(st)
	defer o.status(Status{Ticket: id, Gone: true})
	return r.conclude(ctx, w, lo.head, o.newWatcher(w.wt, st).report) // merged, it is no longer set aside
}
