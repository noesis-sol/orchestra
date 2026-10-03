package dispatch

import (
	"context"
	"fmt"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A run with nothing to do says so before it starts, rather than open the dashboard only to end at
// once: CheckNothingToRun reads what the run would start from (bd ready in its scope, and the
// workers the last run left behind) and, when neither gives it anything, says why: everything is
// done, or nothing is ready.

// NothingToRun is why a run has nothing to do: everything is done, or nothing is ready, and what
// holds the tickets left.
type NothingToRun struct {
	Scope string // the run's scope (Config.Ticket), or "" for all of bd ready
	// AllDone: no ticket is left but those of the excluded types, never run, and none is closed but
	// not merged. In a scoped run: SCOPE_DONE.
	AllDone bool
	// StillOpen, when all is done, is the tickets of the excluded types (epics) still open, for the
	// maintainer to close.
	StillOpen []string

	// Nothing ready, in a run of all of bd ready: the tickets not closed, other than those of the
	// excluded types, by what holds them, and the tickets closed but not merged.
	Questions  int // questions (HumanLabel) waiting for the maintainer's answer
	Waiting    int // open or blocked, waiting on other tickets
	InProgress int
	Deferred   int
	Other      int // in any other status
	Unmerged   int // closed but not merged (UnmergedLabel)

	// NotDone, nothing ready in a scoped run, is each ticket of the scope not done, as SCOPE_OPEN
	// names it.
	NotDone []NotDone

	// Done is the run's done line for this early exit, as the log and events.jsonl record it.
	Done string
}

// Open is how many tickets not closed are left, in a run of all of bd ready: those Nothing ready
// counts, not those closed but not merged.
func (n NothingToRun) Open() int {
	return n.Questions + n.Waiting + n.InProgress + n.Deferred + n.Other
}

// CheckNothingToRun returns why a run as c sets it has nothing to run, or nil when it has
// something: bd ready, in the run's scope and as the loop reads it, lists a ticket, or the last run
// left a worker (on a ticket in the scope) for this one to carry on with. A ticket labelled
// UnmergedLabel that git finds merged since (see mergedSince) counts as merged, as the loop would
// find it, though only the loop removes the label. It only reads, so it can run before any Loop
// exists. Its error is bd's, or the state file's, when it can't tell: the run then goes ahead, and
// the loop reports it.
func CheckNothingToRun(
	ctx context.Context, c Config, tickets Tickets, worktrees Worktrees, merger Merger,
) (*NothingToRun, error) {
	ready, err := tickets.Ready(ctx, c.Ticket)
	if err != nil || len(ready) > 0 {
		return nil, err
	}
	var subs []Ticket
	if c.Ticket != "" {
		if subs, err = tickets.Descendants(ctx, c.Ticket); err != nil {
			return nil, err
		}
	}
	s, err := project.LoadState(c.Repo)
	if err != nil {
		return nil, err
	}
	in := scopeIDs(c.Ticket, subs)
	for _, w := range s.Workers {
		if c.Ticket == "" || in[w.Ticket] {
			return nil, nil // carried over, as loadCarried does
		}
	}
	labelled, err := tickets.Closed(ctx, UnmergedLabel)
	if err != nil {
		return nil, err
	}
	var unmerged []Ticket
	for _, t := range labelled {
		if mergedSince(ctx, c, worktrees, merger, t.ID) == "" {
			unmerged = append(unmerged, t)
		}
	}
	if c.Ticket != "" {
		return scopeNothing(ctx, c, tickets, subs, unmerged)
	}
	return allNothing(ctx, c, tickets, unmerged)
}

// readyEmpty is the done line of a run with nothing to run, before a scoped run's SCOPE_ part.
func readyEmpty(c Config, allDone bool) string {
	what := "nothing ready"
	if allDone {
		what = "everything is done"
	}
	return fmt.Sprintf("READY_EMPTY after %d tickets: %s", c.DoneSoFar, what)
}

// allNothing says why a run of all of bd ready has nothing to run, given the tickets closed but
// not merged.
func allNothing(ctx context.Context, c Config, tickets Tickets, unmerged []Ticket) (*NothingToRun, error) {
	unclosed, err := tickets.Unclosed(ctx)
	if err != nil {
		return nil, err
	}
	n := &NothingToRun{Unmerged: len(unmerged)}
	var excluded []string
	for _, t := range unclosed {
		switch {
		case c.excludes(t):
			excluded = append(excluded, t.ID)
		case HasLabel(t, HumanLabel):
			n.Questions++
		case t.Status == "open" || t.Status == "blocked":
			n.Waiting++
		case t.Status == "in_progress":
			n.InProgress++
		case t.Status == "deferred":
			n.Deferred++
		default:
			n.Other++
		}
	}
	if n.AllDone = n.Open() == 0 && n.Unmerged == 0; n.AllDone {
		n.StillOpen = excluded
	}
	n.Done = readyEmpty(c, n.AllDone)
	return n, nil
}

// scopeNothing says why a scoped run has nothing to run, as scopeEnd would at its end: its
// subtickets (subs) all merged, or each ticket not done and why, given the tickets closed but not
// merged. No ticket has been set aside: no run has started.
func scopeNothing(ctx context.Context, c Config, tickets Tickets, subs, unmerged []Ticket) (*NothingToRun, error) {
	labelled := map[string]bool{}
	for _, t := range unmerged {
		labelled[t.ID] = true
	}
	v := scopeView{tickets: tickets, cfg: c, unmerged: func(id string) string {
		if labelled[id] {
			return earlierRun // as loadUnmerged holds it
		}
		return ""
	}}
	l, err := v.left(ctx, subs)
	if err != nil {
		return nil, err
	}
	n := &NothingToRun{Scope: c.Ticket, AllDone: l.done, NotDone: l.notDone, Done: readyEmpty(c, l.done) + l.line}
	if l.closeIt {
		n.StillOpen = []string{c.Ticket}
	}
	return n, nil
}
