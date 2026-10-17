package dispatch

import (
	"context"
	"fmt"
	"sync"
)

// held returns what makes a ready ticket wait for a ticket blocking it, or for its subtickets, or
// notHeld, saying why once (see holds).
func (o *Loop) held(ctx context.Context, t Ticket, running, parents map[string]bool) holdKind {
	h, _ := o.holds(running, parents).why(ctx, t) // linksOf has logged bd's error; the ticket waits
	if o.holdSaid.changed(t.ID, h.why) {
		o.info("  %s waits: %s", t.ID, h.why)
	}
	if h.why == "" {
		return notHeld
	}
	return h.kind
}

// sayOnce is why each ticket was last said to wait, or be skipped, so that it is said again only
// when the reason changes. Its zero value is ready to use, from any goroutine.
type sayOnce struct {
	mu   sync.Mutex
	said map[string]string
}

// changed records why as ticket id's reason, "" for none, and reports whether it is one to say: not
// "", and not the reason last recorded.
func (s *sayOnce) changed(id, why string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	said := s.said[id]
	if s.said == nil {
		s.said = map[string]string{}
	}
	s.said[id] = why
	return why != "" && why != said
}

// last is the reason last recorded for ticket id, or "".
func (s *sayOnce) last(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.said[id]
}

// forget drops ticket id's reason, so that the next one is said whatever it is.
func (s *sayOnce) forget(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.said, id)
}

// holds is what keeps a ready ticket waiting. Workers close their ticket before it merges, and bd
// ready counts a closed blocker as done, but until the blocker merges its code is not on Base, which
// the ticket's worktree is cut from. An asked ticket counts as running: its worker, answered in its
// tab, may close it there, and it merges only once the run adopts it (see followAsked). A parent (see
// openParents) runs last: its worker couldn't close it while it has open children, and its work
// builds on theirs. A follow-up a worker filed, linked discovered-from its ticket (which bd doesn't
// count as blocking), waits for that ticket as it would for a blocker: it was written against the
// ticket's code, which is not on Base until it merges. A closed ticket the run didn't see close (by
// hand, in a tab the run left, under an earlier run) has not merged while its branch isn't on Base
// (see closedBranches). A ticket set aside with its worker left in its tab, closed or not, has not
// merged: the worker may close it there, and it merges only once the run has seen it close (see
// mergeLate). The loop knows them from its run (Loop.holds); the nothing-to-run check, before any
// Loop, from bd and git, with nothing running.
type holds struct {
	parents  map[string]bool        // the tickets with a subticket not yet closed and merged
	running  map[string]bool        // the tickets whose workers are running
	asked    map[string]bool        // the tickets waiting on a question, carried over (askedIDs) or kept out
	unmerged func(id string) string // why the ticket is closed but not merged, as the run left it, or ""
	left     func(id string) string // why the ticket, its worker left in its tab, has not merged, or ""; nil for none
	// branches returns why closed ticket id's work is only on its branch (see closedBranches), or "";
	// nil for none
	branches func(ctx context.Context, id string) string
	// pending: a ticket is running, asked, left open, or closed but not merged as the run left it.
	// Without one, a ready ticket whose links bd can't show goes ahead: only a closed ticket's branch
	// could hold it, a backstop not worth keeping it out of the run for a bd failure.
	pending bool
	// links returns the tickets ready ticket t waits on, or why bd can't say
	links func(ctx context.Context, t Ticket) (links, error)
}

// holds is what keeps a ready ticket waiting, as the run knows it.
func (o *Loop) holds(running, parents map[string]bool) holds {
	asked := o.askedSet()
	for _, id := range o.keptOutIDs() { // their workers may still close them (see isKeptOut)
		asked[id] = true
	}
	return holds{parents: parents, running: running, asked: asked, unmerged: o.unmergedWhy, left: o.leftOpenWhy,
		branches: o.branchWhy, pending: len(running) > 0 || len(asked) > 0 || o.anyUnmerged() || o.anyLeftOpen(),
		links: o.linksOf}
}

// holdKind is what holds a ready ticket back.
type holdKind int

const (
	notHeld      holdKind = iota
	heldParent            // its subtickets are not all closed and merged
	heldBlocked           // a ticket blocking it has not merged, or bd can't say which block it
	heldFollowUp          // the ticket it was discovered from has not merged
	heldOverlap           // its footprint overlaps a running ticket's, with a slot free (see overlapsRunning)
)

// hold is why a ready ticket can't start yet, and what holds it; why is "" when nothing does.
type hold struct {
	why  string
	kind holdKind
}

// why returns why ready ticket t can't start yet; its why is "" when it can. When bd can't show the
// tickets t waits on, t waits, and the error is bd's, unless nothing is pending (see holds.pending).
func (h holds) why(ctx context.Context, t Ticket) (hold, error) {
	if h.parents[t.ID] {
		return hold{"its subtickets are not all closed and merged", heldParent}, nil
	}
	l, err := h.links(ctx, t)
	switch {
	case err != nil && !h.pending:
		return hold{}, nil
	case err != nil:
		return hold{"its dependencies could not be read", heldBlocked}, err
	}
	// bd ready lists t once its blockers are closed. A source open or deferred, and not running, holds
	// nothing: it may never run. Either way, what the run knows of the ticket says whether it merged.
	for _, id := range l.blockers {
		switch why := pendingWhy(ctx, h, id, true); why {
		case "":
		case pendingBusy:
			return hold{fmt.Sprintf("waiting for %s to merge", id), heldBlocked}, nil
		default:
			return hold{fmt.Sprintf("%s closed but not merged (%s)", id, why), heldBlocked}, nil
		}
	}
	for _, id := range l.sources {
		switch why := pendingWhy(ctx, h, id, l.closed[id]); why {
		case "", pendingOpen:
		case pendingBusy:
			return hold{fmt.Sprintf("filed by %s's worker; waiting for it to merge", id), heldFollowUp}, nil
		default:
			if !l.closed[id] { // its worker left in its tab
				return hold{fmt.Sprintf("filed by %s's worker; waiting for it to merge (%s)", id, why), heldFollowUp}, nil
			}
			return hold{fmt.Sprintf("filed by %s's worker; %s closed but not merged (%s)", id, id, why), heldFollowUp}, nil
		}
	}
	return hold{}, nil
}

func (h holds) busy(id string) bool { return h.running[id] || h.asked[id] }

func (h holds) leftOpenWhy(id string) string {
	if h.left == nil {
		return ""
	}
	return h.left(id)
}

func (h holds) closedWhy(ctx context.Context, id string) string {
	if why := h.unmerged(id); why != "" || h.branches == nil {
		return why
	}
	return h.branches(ctx, id)
}

// mergeState is what a run knows of the tickets not merged yet: the loop's (Loop), what it holds a
// ready ticket on (holds), or, before any Loop, what the run starts from (startState).
type mergeState interface {
	// busy reports whether the ticket's worker runs, or waits on a question: it closes the ticket
	// before it merges
	busy(id string) bool
	// leftOpenWhy is why the ticket, set aside with its worker left in its tab, has not merged, closed
	// or not: the worker may close it there (see leaveOpen); or ""
	leftOpenWhy(id string) string
	// closedWhy is why the ticket, closed, is not merged: as one merging or left unmerged is, or one
	// whose work is only on its branch (see closedBranches); or ""
	closedWhy(ctx context.Context, id string) string
}

// Why a ticket has not merged, besides why it is closed but not merged (see pendingWhy).
const (
	pendingBusy = "its worker runs or waits on a question"
	pendingOpen = "not closed"
)

// pendingWhy returns why ticket id, blocking another ticket or the one it was discovered from, has
// not merged, as s knows it, and closed is whether bd shows it closed; "" once it has merged, and its
// code is on Base. A ticket whose worker runs or waits on a question has not (pendingBusy), closed
// or not, nor has one set aside with its worker left in its tab, whose reason is s's; nor has one not
// closed (pendingOpen), or one closed but merging, left unmerged or with its work only on its branch,
// whose reason is s's. The holds, a worker stopped with nothing done (unmergedBlockers) and the
// deferred tickets freed (freeing) all ask it, so that they agree on what has merged.
func pendingWhy(ctx context.Context, s mergeState, id string, closed bool) string {
	switch {
	case s.busy(id):
		return pendingBusy
	case s.leftOpenWhy(id) != "":
		return s.leftOpenWhy(id)
	case !closed:
		return pendingOpen
	}
	return s.closedWhy(ctx, id)
}

// unmergedBlockers returns the tickets blocking ticket info, as bd show gives it, that have not
// merged: those not closed (running, asked, open or deferred), and those closed but not merged yet,
// as one merging, waiting on a question, left unmerged or with its work only on its branch is (see
// pendingWhy). A closed blocker none of those is on Base, though bd may still label it UnmergedLabel:
// the run and git, not a label it couldn't remove, say what has merged.
func (o *Loop) unmergedBlockers(ctx context.Context, info Ticket) []string {
	var ids []string
	for _, d := range info.Dependencies {
		if d.DependencyType != "blocks" {
			continue
		}
		if pendingWhy(ctx, o, d.ID, d.Status == StatusClosed) != "" {
			ids = append(ids, d.ID)
		}
	}
	return ids
}

// links are the tickets a ready ticket waits on, as bd show gives them: those blocking it, and the
// tickets it was discovered from (sources), a follow-up filed with --deps discovered-from:<id>,
// which bd doesn't count as blocking, and which of those sources bd showed closed (closed).
type links struct {
	blockers []string
	sources  []string
	closed   map[string]bool
}

// blockLinks are the links of a ready ticket, read when bd ready counted count tickets blocking it,
// in the pick'th pick of the run.
type blockLinks struct {
	count int
	pick  int
	links links
}

// linksOf returns the tickets ready ticket t waits on, or why bd show can't say, which it logs.
// They are kept for the run: bd ready's count of t's blockers changes whenever a blocks link is
// added or removed, and then they are read again. Nothing bd ready shows changes with a
// discovered-from link, as one bd dep add makes after the follow-up is filed, so the ticket picked
// to start has its links read again first (see staleLinks). Without a count they are read every time.
func (o *Loop) linksOf(ctx context.Context, t Ticket) (links, error) {
	o.mu.Lock()
	b, seen := o.blockers[t.ID]
	o.mu.Unlock()
	if seen && t.DependencyCount != nil && b.count == *t.DependencyCount {
		return b.links, nil
	}
	l, err := showLinks(ctx, o.tickets, t.ID)
	if err != nil {
		o.log.Raw("", err)
		return links{}, err
	}
	if t.DependencyCount != nil {
		o.mu.Lock()
		o.blockers[t.ID] = blockLinks{count: *t.DependencyCount, pick: o.picks, links: l}
		o.mu.Unlock()
	}
	return l, nil
}

// staleLinks forgets ticket id's links when they were kept from an earlier pick, so that the next
// hold read them again, and reports whether it did. A ticket picked to start is held on links read
// in this pick, not on ones a link added since has made stale.
func (o *Loop) staleLinks(id string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	b, seen := o.blockers[id]
	if !seen || b.pick == o.picks {
		return false
	}
	delete(o.blockers, id)
	return true
}

// showLinks returns the tickets ticket id waits on, as bd show gives them, or why it can't.
func showLinks(ctx context.Context, tickets Tickets, id string) (links, error) {
	info, err := tickets.Show(ctx, id)
	switch {
	case err != nil:
		return links{}, err
	case info.Status == StatusUnknown:
		return links{}, fmt.Errorf("bd show %s: its status is unknown", id)
	}
	var l links
	for _, d := range info.Dependencies {
		switch d.DependencyType {
		case "blocks":
			l.blockers = append(l.blockers, d.ID)
		case "discovered-from":
			l.sources = append(l.sources, d.ID)
			if d.Status == StatusClosed {
				if l.closed == nil {
					l.closed = map[string]bool{}
				}
				l.closed[d.ID] = true
			}
		}
	}
	return l, nil
}
