package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// A ticket closed but not merged, set aside in this run or left unmerged by an earlier one, may be
// merged by hand while the run goes on: the maintainer reviews it and merges its branch. So each
// time Base has moved on, Run asks git of each such ticket, as loadUnmerged does as the run starts
// (see mergedSince): one on Base now is set aside no longer, its UnmergedLabel removed, and counts as
// merged in this run; the tickets it blocks start, and it isn't checked again (see recheck).

// mergedByHand finds the tickets closed but not merged that have been merged by hand since Run last
// looked, Base having moved on since, and takes each one as merged (see handMerged). Those in flight,
// a check of them once more or a worker at their rebase or check, are left to their merge.
func (r *runState) mergedByHand() {
	o, c, ctx := r.o, r.o.cfg, r.ctx
	ids := o.book.ids(func(rec *ticketRecord) bool { return rec.unmerged != "" })
	ids = slices.DeleteFunc(ids, func(id string) bool { return r.inflight[id] })
	if len(ids) == 0 {
		return
	}
	base, err := o.checkout.Head(ctx, c.Repo, c.Base)
	if err != nil { // asked again as Run next goes round
		o.log.Raw("", err)
		return
	}
	if base == r.baseSeen {
		return
	}
	r.baseSeen = base
	for _, id := range ids {
		if commit := mergedSince(ctx, c, o.worktrees, o.merger, o.unmergedTicket(id)); commit != "" {
			o.handMerged(r.keep, id, commit) // bd's label not left half removed by Ctrl+C
		}
	}
}

// unmergedTicket is ticket id, closed but not merged, as mergedSince asks of it: with the ForkKey its
// label noted, when the run knows it.
func (o *Loop) unmergedTicket(id string) Ticket {
	o.mu.Lock()
	fork := o.forks[id]
	o.mu.Unlock()
	t := Ticket{ID: id}
	if fork != "" {
		t.Metadata, _ = json.Marshal(map[string]string{ForkKey: fork}) // a map of strings always marshals
	}
	return t
}

// handMerged takes ticket id, closed but not merged, as merged: commit, on Base, names it. It is set
// aside no longer, nor checked again, and its UnmergedLabel is removed. One set aside in this run
// counts among the tickets it closed.
func (o *Loop) handMerged(ctx context.Context, id, commit string) {
	c := o.cfg
	aside := o.book.countsAside(id)
	title := ""
	o.mu.Lock()
	o.rechecks = slices.DeleteFunc(o.rechecks, func(rc recheck) bool {
		if rc.w.id == id {
			title = rc.title
		}
		return rc.w.id == id
	})
	labelled := o.labelled[id]
	o.mu.Unlock()
	o.merged(ctx, id)
	o.countMerge()
	o.retrySetups() // what it brought may be what a setup that failed needed
	label := ""
	o.mu.Lock()
	if labelled && !o.labelled[id] {
		label = fmt.Sprintf("; its '%s' label is removed", UnmergedLabel)
	}
	o.mu.Unlock()
	if !aside {
		o.info("  %s, left unmerged by an earlier run, is on %s now (%s): merged by hand%s", id, c.Base, commit, label)
		return
	}
	hash, _, _ := strings.Cut(commit, " ")
	o.emit(Event{Kind: EvClosed, Ticket: id, Title: title, Detail: hash + " merged by hand", Text: fmt.Sprintf(
		"  %s, set aside in this run, is on %s now (%s): merged by hand, so no longer set aside%s",
		id, c.Base, commit, label)})
}

// dropRechecks gives up checking again the tickets waiting for it, as why rules it out ("the run
// winds down"), and says so of each: they stay set aside.
func (o *Loop) dropRechecks(why string) {
	o.mu.Lock()
	dropped := o.rechecks
	o.rechecks = nil
	o.mu.Unlock()
	for _, rc := range dropped {
		o.emit(Event{Kind: EvInfo, Ticket: rc.w.id, Text: fmt.Sprintf(
			"  %s is not checked again in this run, as %s; it stays set aside", rc.w.id, why)})
	}
}
