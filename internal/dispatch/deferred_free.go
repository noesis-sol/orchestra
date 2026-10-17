package dispatch

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// A ticket deferred because it started before its blocker merged (as workers' tickets were before
// a ticket that started too early went back in the queue) stays deferred once the blocker merges:
// nothing brings it back. The run's START block, and the box of a run with nothing to run, list
// each such ticket with the command that reopens it. Only list: a deferral can have other reasons,
// so the maintainer decides. One deferred until a later date (bd defer --until) was deferred on
// purpose, with a time to look again, and isn't listed until that time has come.

// DeferredFree is a deferred ticket in the run's scope whose blockers have all merged.
type DeferredFree struct {
	ID       string
	Blockers []string // the tickets blocking it, each closed and merged
	// Worktree is the ticket's worktree (wt/<id>), left with no commits beyond Base and no
	// uncommitted changes, and so safe to remove; "" when there is none, or it holds work.
	Worktree string
	// Uncommitted is the ticket's worktree when it has no commits beyond Base but uncommitted
	// changes, which removing it with --force would lose; "" otherwise.
	Uncommitted string
}

// Reopen is the command that puts the ticket back in bd ready.
func (d DeferredFree) Reopen() string {
	return "bd update " + d.ID + " --status open"
}

// Cleanup is the command that removes the ticket's empty worktree and its branch; "" without one.
func (d DeferredFree) Cleanup() string {
	if d.Worktree == "" {
		return ""
	}
	return fmt.Sprintf("git worktree remove %s && git branch -d %s", command.ShellWord(d.Worktree), branchOf(d.ID))
}

// Changes is the command that shows the uncommitted changes in the ticket's worktree; "" without them.
func (d DeferredFree) Changes() string {
	if d.Uncommitted == "" {
		return ""
	}
	return "git -C " + command.ShellWord(d.Uncommitted) + " status"
}

// Why is what frees the ticket: "its blockers have merged (A, B)".
func (d DeferredFree) Why() string {
	return fmt.Sprintf("its blockers have merged (%s)", strings.Join(d.Blockers, ", "))
}

// Line is the DEFERRED_FREE line the START block gives the ticket, naming its empty worktree too,
// or the uncommitted changes that keep it.
func (d DeferredFree) Line(base string) string {
	l := fmt.Sprintf("DEFERRED_FREE: %s: %s; to run it: %s", d.ID, d.Why(), d.Reopen())
	switch {
	case d.Worktree != "":
		l += fmt.Sprintf("; its worktree has no commits beyond %s, safe to remove: %s", base, d.Cleanup())
	case d.Uncommitted != "":
		l += "; its worktree holds uncommitted changes, not safe to remove: " + d.Changes()
	}
	return l
}

// freeing is what listing the deferred tickets whose blockers have merged needs to know of the run.
type freeing struct {
	cfg       Config
	tickets   Tickets
	worktrees Worktrees
	merger    Merger
	checkout  Checkout
	now       time.Time // the run's clock, against which a ticket's DeferUntil is due
	inScope   func(id string) bool
	state     mergeState // what the run knows of the tickets not merged yet
}

// list returns the deferred tickets among unclosed, the tickets bd lists as not closed, that are in
// the run's scope, not deferred until a later date, and have at least one blocker, each closed and
// merged. bd show is read for those
// with blockers; its error is bd's, and drops the list.
func (f freeing) list(ctx context.Context, unclosed []Ticket) ([]DeferredFree, error) {
	var free []DeferredFree
	for _, t := range unclosed {
		if t.Status != StatusDeferred || !f.inScope(t.ID) || f.cfg.excludes(t) || HasLabel(t, HumanLabel) ||
			t.DependencyCount != nil && *t.DependencyCount == 0 || t.DeferredUntilLater(f.now) {
			continue
		}
		info, err := f.tickets.Show(ctx, t.ID)
		switch {
		case err != nil:
			return nil, err
		case info.Status == StatusUnknown:
			return nil, fmt.Errorf("bd show %s: its status is unknown", t.ID)
		}
		blockers, merged := f.blockers(ctx, info)
		if !merged || len(blockers) == 0 {
			continue
		}
		d := DeferredFree{ID: t.ID, Blockers: blockers}
		d.Worktree, d.Uncommitted = f.worktree(ctx, t.ID)
		free = append(free, d)
	}
	return free, nil
}

// blockers returns the tickets blocking ticket info, as bd show gives it, and whether each is
// closed and merged (see pendingWhy).
func (f freeing) blockers(ctx context.Context, info Ticket) ([]string, bool) {
	var ids []string
	for _, d := range info.Dependencies {
		if d.DependencyType != "blocks" {
			continue
		}
		if pendingWhy(ctx, f.state, d.ID, d.Status == StatusClosed) != "" {
			return nil, false
		}
		ids = append(ids, d.ID)
	}
	return ids, true
}

// worktree returns the worktree a worker left for ticket id when its branch has no commits beyond
// Base: as empty when it has no uncommitted changes either, else as dirty. Both are "" when there
// is none, it has commits, or git can't list the worktrees or count the commits; a failed git
// status counts as uncommitted changes.
func (f freeing) worktree(ctx context.Context, id string) (empty, dirty string) {
	br := branchOf(id)
	wt, err := f.worktrees.WorktreeOf(ctx, f.cfg.Repo, br)
	switch {
	case err != nil || wt == "" || f.merger.CountCommits(ctx, f.cfg.Repo, f.cfg.Base+".."+br) != 0:
		return "", ""
	case f.checkout.DirtyWorktree(ctx, wt) != "":
		return "", wt
	}
	return wt, ""
}

// freeUnreadable is the log's line when bd can't say which deferred tickets are free to run.
func freeUnreadable(err error) error {
	return fmt.Errorf("cannot list the deferred tickets whose blockers have merged: %w", err)
}

// sayDeferredFree lists, in the START block, the deferred tickets whose blockers have merged (see
// DeferredFree), once the run knows the tickets carried over and left unmerged. A bd failure only
// drops the list, with a line in the log.
func (o *Loop) sayDeferredFree(ctx context.Context) {
	open, err := o.tickets.Unclosed(ctx)
	var free []DeferredFree
	if err == nil {
		free, err = freeing{
			cfg: o.cfg, tickets: o.tickets, worktrees: o.worktrees, merger: o.merger, checkout: o.checkout,
			now:     time.Now(),
			inScope: o.scoped(ctx),
			state:   o,
		}.list(ctx, open)
	}
	if err != nil {
		if ctx.Err() == nil {
			o.log.Raw("", freeUnreadable(err))
		}
		return
	}
	for _, d := range free {
		o.emit(Event{Kind: EvInfo, Ticket: d.ID, Text: "  " + d.Line(o.cfg.Base)})
	}
}
