package dispatch

import (
	"context"
	"fmt"
)

// held reports whether a ready ticket must wait for a ticket blocking it, or for its subtickets,
// saying why once. Workers close their ticket before it merges, and bd ready counts a closed
// blocker as done, but until the blocker merges its code is not on Base, which the ticket's
// worktree is cut from. A parent in parents (see openParents) runs last: its worker couldn't close
// it while it has open children, and its work builds on theirs.
func (o *Loop) held(ctx context.Context, t Ticket, running, parents map[string]bool) bool {
	why := ""
	if parents[t.ID] {
		why = "its subtickets are not all closed and merged"
	} else if len(running) > 0 || o.anyUnmerged() {
		why = o.waitsFor(ctx, t, running)
	}
	o.mu.Lock()
	said := o.holdSaid[t.ID]
	if o.holdSaid == nil {
		o.holdSaid = map[string]string{}
	}
	o.holdSaid[t.ID] = why
	o.mu.Unlock()
	if why != "" && why != said {
		o.info("  %s waits: %s", t.ID, why)
	}
	return why != ""
}

// waitsFor returns why ready ticket t can't start yet, or "" if nothing blocking it is unmerged.
func (o *Loop) waitsFor(ctx context.Context, t Ticket, running map[string]bool) string {
	ids, ok := o.blockersOf(ctx, t)
	if !ok {
		return "its dependencies could not be read"
	}
	for _, id := range ids {
		if running[id] {
			return fmt.Sprintf("waiting for %s to merge", id)
		}
		if why := o.unmergedWhy(id); why != "" {
			return fmt.Sprintf("%s closed but not merged (%s)", id, why)
		}
	}
	return ""
}

// blockLinks are the tickets blocking a ready ticket, read when bd ready counted count of them.
type blockLinks struct {
	count int
	ids   []string
}

// blockersOf returns the IDs of the tickets blocking ready ticket t, or false if bd show can't say.
// They are read once per run: bd ready's count of t's blockers changes whenever a blocks link is
// added or removed, and only then are they read again. Without a count they are read every time.
func (o *Loop) blockersOf(ctx context.Context, t Ticket) ([]string, bool) {
	o.mu.Lock()
	b, seen := o.blockers[t.ID]
	o.mu.Unlock()
	if seen && t.DependencyCount != nil && b.count == *t.DependencyCount {
		return b.ids, true
	}
	info, err := o.tickets.Show(ctx, t.ID)
	if err != nil || info.Status == "unknown" {
		if err != nil {
			o.log.Raw("", err)
		}
		return nil, false
	}
	var ids []string
	for _, d := range info.Dependencies {
		if d.DependencyType == "blocks" {
			ids = append(ids, d.ID)
		}
	}
	if t.DependencyCount != nil {
		o.mu.Lock()
		if o.blockers == nil {
			o.blockers = map[string]blockLinks{}
		}
		o.blockers[t.ID] = blockLinks{count: *t.DependencyCount, ids: ids}
		o.mu.Unlock()
	}
	return ids, true
}

// earlierRun is why a ticket left unmerged by an earlier run is still unmerged.
const earlierRun = "left unmerged by an earlier run"

// loadUnmerged reads the tickets earlier runs left unmerged (closed, labelled UnmergedLabel), so the
// tickets they block wait in this run too. One that has merged since, by hand, loses its label: its
// branch is on Base with a commit naming it, or its branch is gone and a commit on Base names it.
// It returns a reason to stop when bd can't say which they are.
func (o *Loop) loadUnmerged(ctx context.Context) *stopReason {
	c := o.cfg
	closed, err := o.tickets.Closed(ctx, UnmergedLabel)
	if err != nil {
		return halt(ExitTool, stopReadyUnreadable, ": could not list the tickets labelled '%s'%s", UnmergedLabel, because(err)).causedBy(err)
	}
	for _, t := range closed {
		id, br := t.ID, "wt/"+t.ID
		rev := c.Base
		if o.worktrees.HasBranch(ctx, c.Repo, br) {
			rev = br
		}
		if o.merger.IsAncestor(ctx, c.Repo, rev, c.Base) {
			if commit := o.merger.CommitNamingOn(ctx, c.Repo, rev, id); commit != "" {
				o.info("  %s, left unmerged by an earlier run, is on %s now (%s); its '%s' label is removed", id, c.Base, commit, UnmergedLabel)
				o.unlabel(ctx, id)
				continue
			}
		}
		o.info("  %s was left unmerged by an earlier run; tickets it blocks wait until %s is merged into %s or its '%s' label is removed",
			id, br, c.Base, UnmergedLabel)
		o.setParent(id, t.Parent)
		o.mu.Lock()
		if o.unmerged == nil {
			o.unmerged = map[string]string{}
		}
		o.unmerged[id] = earlierRun
		o.mu.Unlock()
		o.setLabelled(id, true)
	}
	return nil
}

// leaveUnmerged sets aside a closed ticket that was not merged; tickets it blocks wait for it, in
// this run and, through its UnmergedLabel, in later ones.
func (o *Loop) leaveUnmerged(ctx context.Context, id, why string) {
	o.markAside(id)
	o.mu.Lock()
	if o.unmerged == nil {
		o.unmerged = map[string]string{}
	}
	o.unmerged[id] = why
	o.mu.Unlock()
	if err := o.notes.AddLabel(ctx, id, UnmergedLabel); err != nil {
		o.log.Raw("", err)
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  LABEL_FAILED: bd could not label %s '%s'%s; later runs may start the tickets it blocks before it merges (bd label add %s %s)",
			id, UnmergedLabel, because(err), id, UnmergedLabel)})
		return
	}
	o.setLabelled(id, true)
}

// merged forgets that the ticket was unmerged, and removes its UnmergedLabel if it has one.
func (o *Loop) merged(ctx context.Context, id string) {
	o.mu.Lock()
	delete(o.unmerged, id)
	labelled := o.labelled[id]
	o.mu.Unlock()
	if labelled {
		o.unlabel(ctx, id)
	}
}

// unlabel removes the ticket's UnmergedLabel, warning when bd can't.
func (o *Loop) unlabel(ctx context.Context, id string) {
	if err := o.notes.RemoveLabel(ctx, id, UnmergedLabel); err != nil {
		o.log.Raw("", err)
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  LABEL_FAILED: bd could not remove %s's '%s' label%s; later runs hold the tickets it blocks until it is removed (bd label remove %s %s)",
			id, UnmergedLabel, because(err), id, UnmergedLabel)})
		return
	}
	o.setLabelled(id, false)
}

func (o *Loop) setLabelled(id string, labelled bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.labelled == nil {
		o.labelled = map[string]bool{}
	}
	o.labelled[id] = labelled
}

func (o *Loop) unmergedWhy(id string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.unmerged[id]
}

// setAsked records whether ticket id is set aside waiting on a question.
func (o *Loop) setAsked(id string, asked bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.askedIDs == nil {
		o.askedIDs = map[string]bool{}
	}
	o.askedIDs[id] = asked
}

func (o *Loop) isAsked(id string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.askedIDs[id]
}

func (o *Loop) anyUnmerged() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.unmerged) > 0
}

// appendNotes adds a note to the ticket, logging a failure: a lost note doesn't stop the run.
func (o *Loop) appendNotes(ctx context.Context, id, note string) {
	if err := o.notes.AppendNotes(ctx, id, note); err != nil {
		o.log.Raw("", err)
	}
}

// deferAside defers the ticket and keeps it out of the rest of the run, which matters most when
// bd fails to defer it: it would still be ready and dispatched again at once. The error is
// returned for the caller to warn with.
func (o *Loop) deferAside(ctx context.Context, id, reason string) error {
	o.markAside(id)
	return o.notes.Defer(ctx, id, reason)
}

// setAside lists tickets deferred or left unmerged in this run, in order, without repeats.
func (o *Loop) setAside() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.asideIDs...)
}

func (o *Loop) markAside(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, x := range o.asideIDs {
		if x == id {
			return
		}
	}
	o.asideIDs = append(o.asideIDs, id)
}

// unmarkAside takes a ticket back out of those set aside in this run.
func (o *Loop) unmarkAside(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i, x := range o.asideIDs {
		if x == id {
			o.asideIDs = append(o.asideIDs[:i:i], o.asideIDs[i+1:]...)
			return
		}
	}
}
