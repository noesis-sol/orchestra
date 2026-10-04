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

// setParent records the parent of a ticket dispatched or left unmerged: until the ticket merges,
// its parent waits.
func (o *Loop) setParent(id, parent string) {
	if parent == "" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.parentOf == nil {
		o.parentOf = map[string]string{}
	}
	o.parentOf[id] = parent
}

// listUnreadableError is openParents' error: bd could not list the tickets that aren't closed.
type listUnreadableError struct{ err error }

func (e listUnreadableError) Error() string { return e.err.Error() }
func (e listUnreadableError) Unwrap() error { return e.err }

// openParents returns the tickets with a subticket not yet closed and merged: one bd lists as not
// closed, one running (its worker closes it before it merges) or one left unmerged.
func (o *Loop) openParents(ctx context.Context, running map[string]bool) (map[string]bool, error) {
	open, err := o.tickets.Unclosed(ctx)
	if err != nil {
		return nil, listUnreadableError{err}
	}
	pending := map[string]string{}
	o.mu.Lock()
	for id, p := range o.parentOf {
		if running[id] || o.unmerged[id] != "" {
			pending[id] = p
		}
	}
	o.mu.Unlock()
	return parentsOf(open, pending), nil
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

// parentDone says, once, when the ticket that just finished was the last of an epic's subtickets
// to merge. An epic is never dispatched, and orchestra leaves closing it to the maintainer.
func (o *Loop) parentDone(ctx context.Context, id string, running map[string]bool) {
	o.mu.Lock()
	p := o.parentOf[id]
	said := o.doneSaid[p]
	o.mu.Unlock()
	if p == "" || said || o.unmergedWhy(id) != "" {
		return
	}
	parent, err := o.tickets.Show(ctx, p)
	if err != nil || parent.Status == StatusClosed || !o.excluded(parent) {
		return
	}
	parents, err := o.openParents(ctx, running)
	if err != nil {
		o.log.Raw("", err)
		return
	}
	if parents[p] {
		return
	}
	o.mu.Lock()
	if o.doneSaid == nil {
		o.doneSaid = map[string]bool{}
	}
	o.doneSaid[p] = true
	o.mu.Unlock()
	o.info("  all of %s's subtickets are merged; close it with: bd close %s (or bd epic close-eligible)", p, p)
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
// for a run planned from a feature request, or "".
func ScopeLabel(c Config) string {
	switch {
	case c.Ticket == "":
		return ""
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
// epic, never dispatched, is left for the maintainer to close), else SCOPE_OPEN naming each one
// not done and why. It is "" for a run of all of bd ready.
func (o *Loop) scopeEnd(ctx context.Context, subs []Ticket, err error) string {
	return o.scopeView().end(ctx, subs, err)
}

// scopeView is what is read to say where a scope stands: the tracker, the run's settings, and why
// a ticket is closed but not merged and whether it was set aside in the run. A Loop has them from
// its run (scopeView); the nothing-to-run check, before any Loop, has the tickets labelled
// UnmergedLabel and none set aside.
type scopeView struct {
	tickets  Tickets
	cfg      Config
	unmerged func(id string) string // why the ticket is closed but not merged, or ""
	aside    func(id string) bool   // whether the ticket was set aside in this run; nil for none
}

// scopeView is where the run's scope stands, as the run knows it.
func (o *Loop) scopeView() scopeView {
	return scopeView{tickets: o.tickets, cfg: o.cfg, unmerged: o.unmergedWhy,
		aside: func(id string) bool { return slices.Contains(o.setAside(), id) }}
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
	done := func(t Ticket) bool { return t.Status == StatusClosed && v.unmerged(t.ID) == "" }
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
	switch {
	case len(l.notDone) > 0:
		open := make([]string, len(l.notDone))
		for i, d := range l.notDone {
			open[i] = d.String()
		}
		l.line = fmt.Sprintf("; SCOPE_OPEN: %s: %d of its %s not done: %s",
			root, len(open), subtickets(n), strings.Join(open, ", "))
	case done(top):
		l.done = true
		l.line = fmt.Sprintf("; SCOPE_DONE: %s and its %s are merged", root, subtickets(n))
	case v.cfg.excludes(top):
		verb := "are"
		if n == 1 {
			verb = "is"
		}
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

// notDoneWhy says why ticket t of the scope (in) is not closed and merged; openKids says it has
// subtickets that aren't.
func (v scopeView) notDoneWhy(ctx context.Context, t Ticket, in map[string]bool, openKids bool) string {
	if why := v.unmerged(t.ID); why != "" {
		return "closed but not merged: " + why
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
		if why := v.unmerged(d.ID); why != "" {
			return fmt.Sprintf("blocked by %s%s, closed but not merged", d.ID, where)
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
