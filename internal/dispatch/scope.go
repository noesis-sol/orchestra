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
	parents := map[string]bool{}
	for _, t := range open {
		if t.Parent != "" {
			parents[t.Parent] = true
		}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for id, p := range o.parentOf {
		if running[id] || o.unmerged[id] != "" {
			parents[p] = true
		}
	}
	return parents, nil
}

// excluded reports whether tickets of the type are never dispatched, as epics aren't.
func (o *Loop) excluded(t Ticket) bool {
	return slices.Contains(o.cfg.ExcludeTypes, t.IssueType)
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
	if err != nil || parent.Status == "closed" || !o.excluded(parent) {
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

// ScopeLabel is how a run's scope is shown after the branch: " · ticket <id>", or "".
func ScopeLabel(ticket string) string {
	if ticket == "" {
		return ""
	}
	return " · ticket " + ticket
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
	root := o.cfg.Ticket
	if root == "" {
		return ""
	}
	if err != nil {
		return fmt.Sprintf("; SCOPE_UNREADABLE: could not list %s's subtickets%s", root, because(err))
	}
	top, err := o.tickets.Show(ctx, root)
	if err != nil {
		return fmt.Sprintf("; SCOPE_UNREADABLE: could not read %s%s", root, because(err))
	}
	in := map[string]bool{root: true}
	for _, t := range subs {
		in[t.ID] = true
	}
	// A ticket is done once closed and merged; a parent waits for its children to be.
	done := func(t Ticket) bool { return t.Status == "closed" && o.unmergedWhy(t.ID) == "" }
	openKids := map[string]bool{}
	for _, t := range subs {
		if !done(t) {
			openKids[t.Parent] = true
		}
	}
	var open []string
	for _, t := range subs {
		if !done(t) {
			open = append(open, fmt.Sprintf("%s (%s)", t.ID, o.notDoneWhy(ctx, t, in, openKids[t.ID])))
		}
	}
	n := len(subs)
	switch {
	case len(open) > 0:
		return fmt.Sprintf("; SCOPE_OPEN: %s: %d of its %s not done: %s",
			root, len(open), subtickets(n), strings.Join(open, ", "))
	case done(top):
		return fmt.Sprintf("; SCOPE_DONE: %s and its %s are merged", root, subtickets(n))
	case o.excluded(top):
		verb := "are"
		if n == 1 {
			verb = "is"
		}
		return fmt.Sprintf("; SCOPE_DONE: %s's %s %s merged; close it with: bd close %s", root, subtickets(n), verb, root)
	}
	return fmt.Sprintf("; SCOPE_OPEN: %s: its %s are merged, but %s itself is not done (%s)",
		root, subtickets(n), root, o.notDoneWhy(ctx, top, in, false))
}

// notDoneWhy says why ticket t of the scope (in) is not closed and merged; openKids says it has
// subtickets that aren't.
func (o *Loop) notDoneWhy(ctx context.Context, t Ticket, in map[string]bool, openKids bool) string {
	if why := o.unmergedWhy(t.ID); why != "" {
		return "closed but not merged: " + why
	}
	info, err := o.tickets.Show(ctx, t.ID)
	if err == nil {
		t = info
	}
	if q := OpenQuestion(t); q != nil {
		return "waiting on your answer to " + q.ID
	}
	if slices.Contains(o.setAside(), t.ID) {
		return "set aside in this run"
	}
	switch t.Status {
	case "deferred":
		return "deferred"
	case "in_progress":
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
		if d.Status != "closed" {
			return "blocked by " + d.ID + where
		}
		if why := o.unmergedWhy(d.ID); why != "" {
			return fmt.Sprintf("blocked by %s%s, closed but not merged", d.ID, where)
		}
	}
	switch {
	case openKids:
		return "waiting for its subtickets"
	case o.excluded(t):
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
	in := map[string]bool{o.cfg.Ticket: true}
	for _, t := range subs {
		in[t.ID] = true
	}
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
