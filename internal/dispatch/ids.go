package dispatch

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// IDProblem says why ticket id can't be run, or returns "". A ticket's ID names its worktree, a
// folder under the worktree root, and its branch, wt/<id>; workers file tickets themselves, and
// Beads checks only an ID's prefix, so an ID like x-../../y or x-a/b reaches the loop. It must be a
// plain local name (filepath.IsLocal, with no path separator and no ..) that git takes in a branch
// name (git help check-ref-format).
func IDProblem(id string) string {
	if id == "." || !filepath.IsLocal(id) || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return "IDs with path characters can't be run"
	}
	if strings.HasPrefix(id, ".") || strings.HasSuffix(id, ".") || strings.HasSuffix(id, ".lock") ||
		strings.Contains(id, "@{") || strings.ContainsFunc(id, notInRef) {
		return "IDs that can't name a git branch can't be run"
	}
	return ""
}

// notInRef reports whether git refuses r anywhere in a ref name.
func notInRef(r rune) bool {
	return r < 0x20 || r == 0x7f || strings.ContainsRune(" ~^:?*[", r)
}

// setAsideBadIDs sets aside each ticket in ready, not already skipped, whose ID IDProblem refuses,
// before anything is made from it, and adds it to skip. The run goes on with the others.
func (o *Loop) setAsideBadIDs(ctx context.Context, ready []Ticket, skip map[string]bool) {
	for _, t := range ready {
		if skip[t.ID] {
			continue
		}
		why := IDProblem(t.ID)
		if why == "" {
			continue
		}
		skip[t.ID] = true
		id := t.ID
		o.appendNotes(ctx, id, fmt.Sprintf("Orchestra: %s, so no worker was started on %s: "+
			"a ticket's ID names its worktree folder and its branch wt/<id>. "+
			"Give it a plain ID (bd rename %s <new-id>), then bring it back with: bd undefer <new-id>",
			why, id, id))
		if err := o.deferAside(ctx, id, why); err != nil {
			o.emit(Event{Kind: EvWarn, Ticket: id, Aside: true, Detail: why, Text: fmt.Sprintf(
				"  DEFER_FAILED: %s: %s, and bd could not defer it%s; "+
					"kept out of this run, rename it with: bd rename %s <new-id>",
				why, id, because(err), id)})
			continue
		}
		o.emit(Event{Kind: EvDeferred, Ticket: id, Title: t.Title, Detail: why, Text: fmt.Sprintf(
			"  BAD_TICKET_ID: %s: %s -> deferred without starting a worker; "+
				"rename it (bd rename %s <new-id>), then bd undefer <new-id>",
			why, id, id)})
	}
}
