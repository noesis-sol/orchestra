package dispatch

import (
	"cmp"
	"context"
	"fmt"
)

// held reports whether a ready ticket must wait for a ticket blocking it, or for its subtickets,
// saying why once (see holds).
func (o *Loop) held(ctx context.Context, t Ticket, running, parents map[string]bool) bool {
	why, _ := o.holds(running, parents).why(ctx, t) // blockersOf has logged bd's error; the ticket waits
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

// holds is what keeps a ready ticket waiting. Workers close their ticket before it merges, and bd
// ready counts a closed blocker as done, but until the blocker merges its code is not on Base, which
// the ticket's worktree is cut from. A parent (see openParents) runs last: its worker couldn't close
// it while it has open children, and its work builds on theirs. The loop knows them from its run
// (Loop.holds); the nothing-to-run check, before any Loop, from bd and git, with nothing running.
type holds struct {
	parents  map[string]bool        // the tickets with a subticket not yet closed and merged
	running  map[string]bool        // the tickets whose workers are running
	unmerged func(id string) string // why the ticket is closed but not merged, or ""
	// pending: a ticket is running or closed but not merged; only then can one blocking a ready
	// ticket hold it, and its blockers are read
	pending bool
	// blockers returns the IDs of the tickets blocking ready ticket t, or why bd can't say
	blockers func(ctx context.Context, t Ticket) ([]string, error)
}

// holds is what keeps a ready ticket waiting, as the run knows it.
func (o *Loop) holds(running, parents map[string]bool) holds {
	return holds{parents: parents, running: running, unmerged: o.unmergedWhy,
		pending: len(running) > 0 || o.anyUnmerged(), blockers: o.blockersOf}
}

// why returns why ready ticket t can't start yet, or "". When bd can't show the tickets blocking
// it, t waits, and the error is bd's.
func (h holds) why(ctx context.Context, t Ticket) (string, error) {
	if h.parents[t.ID] {
		return "its subtickets are not all closed and merged", nil
	}
	if !h.pending {
		return "", nil
	}
	ids, err := h.blockers(ctx, t)
	if err != nil {
		return "its dependencies could not be read", err
	}
	for _, id := range ids {
		if h.running[id] {
			return fmt.Sprintf("waiting for %s to merge", id), nil
		}
		if why := h.unmerged(id); why != "" {
			return fmt.Sprintf("%s closed but not merged (%s)", id, why), nil
		}
	}
	return "", nil
}

// blockLinks are the tickets blocking a ready ticket, read when bd ready counted count of them.
type blockLinks struct {
	count int
	ids   []string
}

// blockersOf returns the IDs of the tickets blocking ready ticket t, or why bd show can't say, which
// it logs. They are read once per run: bd ready's count of t's blockers changes whenever a blocks
// link is added or removed, and only then are they read again. Without a count they are read every
// time.
func (o *Loop) blockersOf(ctx context.Context, t Ticket) ([]string, error) {
	o.mu.Lock()
	b, seen := o.blockers[t.ID]
	o.mu.Unlock()
	if seen && t.DependencyCount != nil && b.count == *t.DependencyCount {
		return b.ids, nil
	}
	ids, err := showBlockers(ctx, o.tickets, t.ID)
	if err != nil {
		o.log.Raw("", err)
		return nil, err
	}
	if t.DependencyCount != nil {
		o.mu.Lock()
		if o.blockers == nil {
			o.blockers = map[string]blockLinks{}
		}
		o.blockers[t.ID] = blockLinks{count: *t.DependencyCount, ids: ids}
		o.mu.Unlock()
	}
	return ids, nil
}

// showBlockers returns the IDs of the tickets blocking ticket id, as bd show gives them, or why it
// can't.
func showBlockers(ctx context.Context, tickets Tickets, id string) ([]string, error) {
	info, err := tickets.Show(ctx, id)
	switch {
	case err != nil:
		return nil, err
	case info.Status == StatusUnknown:
		return nil, fmt.Errorf("bd show %s: its status is unknown", id)
	}
	var ids []string
	for _, d := range info.Dependencies {
		if d.DependencyType == "blocks" {
			ids = append(ids, d.ID)
		}
	}
	return ids, nil
}

// earlierRun is why a ticket left unmerged by an earlier run is still unmerged.
const earlierRun = "left unmerged by an earlier run"

// loadUnmerged reads the tickets earlier runs left unmerged (closed, labelled UnmergedLabel), so the
// tickets they block wait in this run too. One that has merged since, by hand (see mergedSince),
// loses its label. It returns a reason to stop when bd can't say which they are.
func (o *Loop) loadUnmerged(ctx context.Context) *stopReason {
	c := o.cfg
	closed, err := o.tickets.Closed(ctx, UnmergedLabel)
	if err != nil {
		return halt(ExitTool, stopReadyUnreadable, ": could not list the tickets labelled '%s'%s",
			UnmergedLabel, because(err)).causedBy(err)
	}
	for _, t := range closed {
		id := t.ID
		if commit := mergedSince(ctx, c, o.worktrees, o.merger, id); commit != "" {
			o.info("  %s, left unmerged by an earlier run, is on %s now (%s); its '%s' label is removed",
				id, c.Base, commit, UnmergedLabel)
			o.unlabel(ctx, id)
			continue
		}
		o.info("  %s was left unmerged by an earlier run; "+
			"tickets it blocks wait until wt/%s is merged into %s or its '%s' label is removed",
			id, id, c.Base, UnmergedLabel)
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

// mergedSince returns the commit on Base naming ticket id, which an earlier run left unmerged, when
// it has merged since, by hand: its branch (wt/<id>) is on Base with a commit naming it, or its
// branch is gone and a commit on Base names it. It returns "" when it hasn't, or git can't tell.
// loadUnmerged and CheckNothingToRun both ask it, so that a run and its check agree on what is
// merged.
func mergedSince(ctx context.Context, c Config, worktrees Worktrees, merger Merger, id string) string {
	rev := c.Base
	if br := branchOf(id); worktrees.HasBranch(ctx, c.Repo, br) {
		rev = br
	}
	if !merger.IsAncestor(ctx, c.Repo, rev, c.Base) {
		return ""
	}
	return merger.CommitNamingOn(ctx, c.Repo, rev, id)
}

// leaveUnmerged sets aside a closed ticket that was not merged; tickets it blocks wait for it, in
// this run and, through its UnmergedLabel, in later ones. It reports whether bd labelled it.
func (o *Loop) leaveUnmerged(ctx context.Context, id, why string) bool {
	o.markAside(id)
	o.mu.Lock()
	if o.unmerged == nil {
		o.unmerged = map[string]string{}
	}
	o.unmerged[id] = why
	o.mu.Unlock()
	return o.label(ctx, id)
}

// leaveRunning labels UnmergedLabel each ticket whose worker is left running as the run ends, after
// a stop or an interrupt. Its worker may still commit and close it once orchestra has gone, and
// nothing merges it then: bd ready would count it done, and a later run would start the tickets it
// blocks on a Base without its code. The label holds them (see loadUnmerged) until it merges, by
// hand or when the ticket, still open, is dispatched again; while the ticket isn't closed it holds
// nothing.
func (o *Loop) leaveRunning(ctx context.Context) {
	for _, st := range o.activeList() {
		id := st.Ticket
		o.mu.Lock()
		labelled := o.labelled[id]
		o.mu.Unlock()
		if labelled || !o.label(ctx, id) {
			continue
		}
		o.emit(Event{Kind: EvInfo, Ticket: id, Text: fmt.Sprintf(
			"  %s is left running in tab %s and labelled '%s': tickets it blocks wait until wt/%s is merged into %s, "+
				"in later runs too", id, st.Tab, UnmergedLabel, id, o.cfg.Base)})
	}
}

// label adds UnmergedLabel to the ticket, warning when bd can't, and reports whether it did.
func (o *Loop) label(ctx context.Context, id string) bool {
	if err := o.notes.AddLabel(ctx, id, UnmergedLabel); err != nil {
		o.log.Raw("", err)
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  LABEL_FAILED: bd could not label %s '%s'%s; "+
				"later runs may start the tickets it blocks before it merges (bd label add %s %s)",
			id, UnmergedLabel, because(err), id, UnmergedLabel)})
		return false
	}
	o.setLabelled(id, true)
	return true
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
			"  LABEL_FAILED: bd could not remove %s's '%s' label%s; "+
				"later runs hold the tickets it blocks until it is removed (bd label remove %s %s)",
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

// askedWorker is where a ticket set aside to wait on a question left its worker, which may carry
// on once the question is answered, and the question. Carried over from the last run (see
// loadCarried), it may instead be a worker that run left running when it stopped, with no question.
type askedWorker struct {
	tab, wt  string
	pane     string // the pane it was started in, where it is looked for unnamed (see earlierState); "" if not known
	question string // its ID
	title    string
	hooks    bool     // it reports through hooks
	left     stopKind // why the last run left it running; "" for a question
}

// after is what the worker was left after, for a line about it: "its question Q", or "the last run
// left it running (PAUSED)".
func (w askedWorker) after() string {
	if w.question != "" {
		return "its question " + w.question
	}
	return "the last run left it running (" + w.leftWhy() + ")"
}

// leftWhy is why the last run left the worker running, as its final line began.
func (w askedWorker) leftWhy() string {
	return cmp.Or(string(w.left), "stopped")
}

// setAsked records ticket id as set aside waiting on a question, its worker left as w; nil: no
// longer.
func (o *Loop) setAsked(id string, w *askedWorker) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if w == nil {
		delete(o.askedIDs, id)
		return
	}
	if o.askedIDs == nil {
		o.askedIDs = map[string]askedWorker{}
	}
	o.askedIDs[id] = *w
}

// asked returns where ticket id, set aside waiting on a question, left its worker.
func (o *Loop) asked(id string) (askedWorker, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	w, ok := o.askedIDs[id]
	return w, ok
}

func (o *Loop) isAsked(id string) bool {
	_, ok := o.asked(id)
	return ok
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
