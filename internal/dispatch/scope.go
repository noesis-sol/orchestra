package dispatch

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Parents run last, in every run: a ticket with subtickets not yet closed and merged waits for
// them, as its worker couldn't close it before they are (bd refuses) and its work builds on theirs.
// A scoped run (Config.Ticket) takes only that ticket and its descendants, and says at its end
// whether they are all merged.

// listUnreadableError is openParents' error: bd could not list the tickets that aren't closed.
type listUnreadableError struct{ err error }

func (e listUnreadableError) Error() string { return e.err.Error() }
func (e listUnreadableError) Unwrap() error { return e.err }

// openParents returns the tickets with a subticket not yet closed and merged: one bd lists as not
// closed, or one closed that has not merged, as the holds find it (see parentsOpen).
func (o *Loop) openParents(ctx context.Context, running map[string]bool) (map[string]bool, error) {
	open, err := o.tickets.Unclosed(ctx)
	if err != nil {
		return nil, listUnreadableError{err}
	}
	return o.parentsOpen(ctx, open, running), nil
}

// parentsOpen is openParents from open, the tickets bd lists as not closed. A subticket bd no longer
// lists, which the run dispatched or left unmerged (see ticketRecord.parent), holds its parent while
// pendingWhy says it has not merged, as the holds know the run with running running: its worker runs
// or waits on a question, it was set aside with its worker left in its tab, or it is closed but
// merging, left unmerged or with its work only on its branch. The ready rule, epicDone and the
// scope's end so agree on what has merged.
func (o *Loop) parentsOpen(ctx context.Context, open []Ticket, running map[string]bool) map[string]bool {
	listed := make(map[string]bool, len(open))
	for _, t := range open {
		listed[t.ID] = true
	}
	ids, recs := o.book.matching(func(r *ticketRecord) bool { return r.parent != "" })
	h := o.holds(running, nil)
	pending := map[string]string{}
	for i, id := range ids {
		if !listed[id] && pendingWhy(ctx, h, id, true) != "" {
			pending[id] = recs[i].parent
		}
	}
	return parentsOf(open, pending)
}

// countUnclosed sets runState.unclosed from open, the tickets bd lists as not closed: all of them, or
// in a scoped run those of the scope, its ticket and its descendants, as bd ready is read there. A
// failed read of the descendants keeps the last count, as nothing depends on it.
func (r *runState) countUnclosed(ctx context.Context, open []Ticket) {
	o := r.o
	if o.cfg.Ticket == "" {
		r.unclosed = len(open)
		return
	}
	subs, err := o.tickets.Descendants(ctx, o.cfg.Ticket)
	if err != nil {
		return
	}
	in := scopeIDs(o.cfg.Ticket, subs)
	n := 0
	for _, t := range open {
		if in[t.ID] {
			n++
		}
	}
	r.unclosed = n
}

// parentsOf returns the tickets that wait for their subtickets: the parents of open, the tickets bd
// lists as not closed, and of pending, closed but not yet merged, each given with its parent.
func parentsOf(open []Ticket, pending map[string]string) map[string]bool {
	parents := map[string]bool{}
	for _, t := range open {
		if t.Parent != "" {
			parents[t.Parent] = true
		}
	}
	for _, p := range pending {
		if p != "" {
			parents[p] = true
		}
	}
	return parents
}

// excluded reports whether tickets of the type are never dispatched, as epics aren't.
func (o *Loop) excluded(t Ticket) bool { return o.cfg.excludes(t) }

// excludes reports whether the ticket is of a type never dispatched (ExcludeTypes).
func (c Config) excludes(t Ticket) bool {
	return slices.Contains(c.ExcludeTypes, t.IssueType)
}

// parentDone closes the epic whose subtickets the ticket that just finished was the last to merge,
// once: an epic is never dispatched, so nothing else would. An epic so closed that was itself the
// last open subticket of an epic closes that one too, up the chain.
func (o *Loop) parentDone(ctx context.Context, id string, running map[string]bool) {
	r, _ := o.book.get(id)
	p := r.parent
	if o.unmergedWhy(id) != "" {
		return
	}
	for p != "" {
		p = o.epicDone(ctx, p, id, running)
	}
}

// epicClosed is doneSaid's record of an epic orchestra closed in the run.
const epicClosed = "closed"

// epicDone closes epic p if all of its subtickets are closed and merged, last the one that merged
// last, and returns p's own parent to look at next; "" when p stays open or has no parent. Should
// bd fail to close it, the log says so and how to close it, and the run goes on. The scope's own
// ticket stays open while the scope is being filed (Config.Filing), its tree not all filed yet:
// filed looks at it again once the filing is over.
func (o *Loop) epicDone(ctx context.Context, p, last string, running map[string]bool) string {
	if o.doneSaid.last(p) != "" || p == o.cfg.Ticket && o.cfg.filingDone() != nil {
		return ""
	}
	parent, err := o.tickets.Show(ctx, p)
	if err != nil || parent.Status == StatusClosed || !o.excluded(parent) {
		return ""
	}
	parents, err := o.openParents(ctx, running)
	if err != nil {
		o.log.Raw("", err)
		return ""
	}
	if parents[p] || !o.doneSaid.changed(p, "ready to close") {
		return ""
	}
	n := -1 // its subtickets, should bd fail to list them
	if subs, err := o.tickets.Descendants(ctx, p); err == nil {
		n = len(subs)
	} else {
		o.log.Raw("", err)
	}
	reason := fmt.Sprintf("%s; closed by orchestra after %s", allMerged(n), last)
	if err := o.notes.Close(ctx, p, strings.ToUpper(reason[:1])+reason[1:]); err != nil {
		o.log.Raw("", err)
		o.info("  %s, but bd could not close %s%s; close it with: bd close %s", allMerged(n), p, because(err), p)
		return ""
	}
	o.doneSaid.changed(p, epicClosed)
	o.emit(Event{Kind: EvInfo, Ticket: p, Text: fmt.Sprintf("  closed epic %s: %s", p, allMerged(n))})
	return parent.Parent
}

// allMerged says an epic's n subtickets are merged: "all 3 subtickets are merged", "its 1
// subticket is merged", or with n -1, not known, "all its subtickets are merged".
func allMerged(n int) string {
	switch {
	case n < 0:
		return "all its subtickets are merged"
	case n == 1:
		return "its 1 subticket is merged"
	}
	return fmt.Sprintf("all %s are merged", subtickets(n))
}

// scopeNote is what a scoped run adds to a worker's prompt, so the follow-ups that belong to the
// work join the run. The scope's own ticket runs last, and a new child would keep it open, so its
// worker gets none.
func (o *Loop) scopeNote(id string) string {
	r := o.cfg.Ticket
	if r == "" || id == r {
		return ""
	}
	return fmt.Sprintf("\n\nThis run works on %s and its subtickets only. "+
		"File a follow-up that belongs to this work as a child of %s (bd create --parent %s …) "+
		"so this run picks it up; anything else waits for a later run.\n", r, r, r)
}

// ScopeLabel is how a run's scope is shown after the branch: " · ticket <id>", " · feature <epic>"
// for a run on a feature request talked through, " · spec <path>" for one broken down from a spec, or "".
func ScopeLabel(c Config) string {
	switch {
	case c.Ticket == "":
		return ""
	case c.Spec != "":
		return " · spec " + c.Spec
	case c.Feature != "":
		return " · feature " + c.Ticket
	}
	return " · ticket " + c.Ticket
}

// FeatureLine is a feature request on one line, its whitespace collapsed and cut after 200
// characters, for the log.
func FeatureLine(text string) string {
	line := strings.Join(strings.Fields(text), " ")
	if r := []rune(line); len(r) > 200 {
		line = string(r[:200]) + "…"
	}
	return line
}

// subtickets is "1 subticket" or "n subtickets".
func subtickets(n int) string {
	if n == 1 {
		return "1 subticket"
	}
	return fmt.Sprintf("%d subtickets", n)
}

// scopeEnd is what a scoped run adds to its last line, given the scope's descendants as listed
// (err if they couldn't be): SCOPE_DONE when the ticket and all of them are closed and merged (an
// epic, never dispatched, closed by the run as its last subticket merged, or else left for the
// maintainer to close), else SCOPE_OPEN naming each one not done and why. It is "" for a run of all of bd ready.
func (o *Loop) scopeEnd(ctx context.Context, subs []Ticket, err error) string {
	return o.scopeView().end(ctx, subs, err)
}

// scopeView is what is read to say where a scope stands: the tracker, the run's settings, what the
// run knows of the tickets not merged yet (state, which pendingWhy asks), whether a ticket was set
// aside in the run, whether the run closed it and whether the setup failed in its worktree. A Loop
// has them from its run (scopeView); the nothing-to-run check, before any Loop, has its holds, and
// none set aside, closed or failing its setup.
type scopeView struct {
	tickets Tickets
	cfg     Config
	state   mergeState
	aside   func(id string) bool   // whether the ticket was set aside in this run; nil for none
	closed  func(id string) bool   // whether the run closed the epic, its subtickets merged; nil for none
	setup   func(id string) string // the output of the setup that failed in its worktree (see leaveSetupFailed)
}

// scopeView is where the run's scope stands, as the run knows it.
func (o *Loop) scopeView() scopeView {
	return scopeView{tickets: o.tickets, cfg: o.cfg, state: o,
		aside:  o.book.countsAside,
		closed: func(id string) bool { return o.doneSaid.last(id) == epicClosed },
		setup:  func(id string) string { output, _ := o.failedSetupOf(id); return output }}
}

// end is scopeEnd's line.
func (v scopeView) end(ctx context.Context, subs []Ticket, err error) string {
	root := v.cfg.Ticket
	if root == "" {
		return ""
	}
	if err != nil {
		return fmt.Sprintf("; SCOPE_UNREADABLE: could not list %s's subtickets%s", root, because(err))
	}
	l, err := v.left(ctx, subs)
	if err != nil {
		return fmt.Sprintf("; SCOPE_UNREADABLE: could not read %s%s", root, because(err))
	}
	return l.line
}

// NotDone is a ticket of a run's scope that isn't closed and merged, and why, as SCOPE_OPEN names
// it.
type NotDone struct {
	ID  string
	Why string // "blocked by Y", "in progress", "waiting for its subtickets", …
}

// String is "<id> (<why>)".
func (n NotDone) String() string { return fmt.Sprintf("%s (%s)", n.ID, n.Why) }

// scopeLeft is where a scope stands: done (SCOPE_DONE) or the tickets not done, as SCOPE_OPEN names
// them, and the line scopeEnd adds.
type scopeLeft struct {
	done    bool
	closeIt bool      // done, with the scope's own ticket, of an excluded type, left for the maintainer to close
	notDone []NotDone // the subtickets not done, or, once they all are, the scope's own ticket
	line    string
}

// left says where the scope stands, given its descendants (subs); the error is bd's when it can't
// show the scope's own ticket.
func (v scopeView) left(ctx context.Context, subs []Ticket) (scopeLeft, error) {
	root := v.cfg.Ticket
	top, err := v.tickets.Show(ctx, root)
	if err != nil {
		return scopeLeft{}, err
	}
	in := scopeIDs(root, subs)
	// A ticket is done once closed and merged; a parent waits for its children to be.
	done := func(t Ticket) bool { return v.pending(ctx, t) == "" }
	openKids := map[string]bool{}
	for _, t := range subs {
		if !done(t) {
			openKids[t.Parent] = true
		}
	}
	var l scopeLeft
	for _, t := range subs {
		if !done(t) {
			l.notDone = append(l.notDone, NotDone{t.ID, v.notDoneWhy(ctx, t, in, openKids[t.ID])})
		}
	}
	n := len(subs)
	verb := "are"
	if n == 1 {
		verb = "is"
	}
	switch {
	case len(l.notDone) > 0:
		open := make([]string, len(l.notDone))
		for i, d := range l.notDone {
			open[i] = d.String()
		}
		l.line = fmt.Sprintf("; SCOPE_OPEN: %s: %d of its %s not done: %s",
			root, len(open), subtickets(n), strings.Join(open, ", "))
	case done(top) && v.closed != nil && v.closed(root):
		l.done = true
		l.line = fmt.Sprintf("; SCOPE_DONE: %s's %s %s merged; orchestra closed %s", root, subtickets(n), verb, root)
	case done(top):
		l.done = true
		l.line = fmt.Sprintf("; SCOPE_DONE: %s and its %s are merged", root, subtickets(n))
	case v.cfg.excludes(top):
		l.done, l.closeIt = true, true
		l.line = fmt.Sprintf("; SCOPE_DONE: %s's %s %s merged; close it with: bd close %s", root, subtickets(n), verb, root)
	default:
		why := v.notDoneWhy(ctx, top, in, false)
		l.notDone = []NotDone{{root, why}}
		l.line = fmt.Sprintf("; SCOPE_OPEN: %s: its %s are merged, but %s itself is not done (%s)",
			root, subtickets(n), root, why)
	}
	return l, nil
}

// scopeIDs is the tickets of the scope of root, whose descendants are subs: root and subs.
func scopeIDs(root string, subs []Ticket) map[string]bool {
	in := map[string]bool{root: true}
	for _, t := range subs {
		in[t.ID] = true
	}
	return in
}

// pending is why ticket t, as bd lists it, has not merged (see pendingWhy), or "" once it has.
func (v scopeView) pending(ctx context.Context, t Ticket) string {
	return pendingWhy(ctx, v.state, t.ID, t.Status == StatusClosed)
}

// notDoneWhy says why ticket t of the scope (in) is not closed and merged; openKids says it has
// subtickets that aren't.
func (v scopeView) notDoneWhy(ctx context.Context, t Ticket, in map[string]bool, openKids bool) string {
	switch why := v.pending(ctx, t); {
	case t.Status == StatusClosed:
		return "closed but not merged: " + why
	case why != pendingBusy && why != pendingOpen: // its worker left in its tab
		return why
	}
	info, err := v.tickets.Show(ctx, t.ID)
	if err == nil {
		t = info
	}
	if q := OpenQuestion(t); q != nil {
		return "waiting on your answer to " + q.ID
	}
	if v.aside != nil && v.aside(t.ID) {
		return "set aside in this run"
	}
	if v.setup != nil {
		if output := v.setup(t.ID); output != "" {
			return "its setup failed; the whole output is in " + output
		}
	}
	switch t.Status {
	case StatusDeferred:
		return "deferred"
	case StatusInProgress:
		return "in progress"
	}
	for _, d := range t.Dependencies {
		if d.DependencyType != "blocks" {
			continue
		}
		where := ""
		if !in[d.ID] {
			where = " outside the scope"
		}
		if d.Status != StatusClosed {
			return "blocked by " + d.ID + where
		}
		if v.pending(ctx, d) != "" {
			return fmt.Sprintf("blocked by %s%s, closed but not merged", d.ID, where)
		}
	}
	for _, d := range t.Dependencies {
		if d.DependencyType == "discovered-from" && d.Status == StatusClosed && v.pending(ctx, d) != "" {
			return fmt.Sprintf("filed by %s's worker; %s is closed but not merged", d.ID, d.ID)
		}
	}
	switch {
	case openKids:
		return "waiting for its subtickets"
	case v.cfg.excludes(t):
		return fmt.Sprintf("its subtickets are merged; close it with: bd close %s", t.ID)
	}
	return "not started"
}

// filedOutside returns the tickets filed since the run started that a scoped run left out, as
// they aren't among the scope's descendants (subs): they wait for a later run. Questions for the
// maintainer aren't follow-ups.
func (o *Loop) filedOutside(ctx context.Context, subs []Ticket) ([]Ticket, error) {
	open, err := o.tickets.Unclosed(ctx)
	if err != nil {
		return nil, err
	}
	in := scopeIDs(o.cfg.Ticket, subs)
	since := o.started.Truncate(time.Second)
	var out []Ticket
	for _, t := range open {
		created, err := time.Parse(time.RFC3339, t.CreatedAt)
		if in[t.ID] || HasLabel(t, HumanLabel) || err != nil || created.Before(since) {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// listTickets is "A (title), B (title)".
func listTickets(ts []Ticket) string {
	var l []string
	for _, t := range ts {
		l = append(l, fmt.Sprintf("%s (%s)", t.ID, t.Title))
	}
	return strings.Join(l, ", ")
}

// endScope logs the follow-ups a scoped run left out and returns what its last line adds.
func (o *Loop) endScope(ctx context.Context) string {
	if o.cfg.Ticket == "" {
		return ""
	}
	subs, err := o.tickets.Descendants(ctx, o.cfg.Ticket)
	if err == nil {
		if out, err := o.filedOutside(ctx, subs); err != nil {
			o.log.Raw("", err)
		} else if len(out) > 0 {
			o.info("  filed during the run outside %s's scope, left for a later run: %s", o.cfg.Ticket, listTickets(out))
		}
	}
	return o.scopeEnd(ctx, subs, err)
}
