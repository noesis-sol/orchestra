package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

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
		if commit := mergedSince(ctx, c, o.worktrees, o.merger, t); commit != "" {
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
		o.unmerged[id] = earlierRun
		o.mu.Unlock()
		o.setLabelled(id, true)
	}
	return nil
}

// mergedSince returns the commit on Base naming ticket t, which an earlier run left unmerged, when
// it has merged since, by hand: its branch (wt/<id>) is on Base with a commit of its own naming it,
// or squash-merged (see branchMerged), or its branch is gone and a commit on Base since the branch
// was cut (ForkKey) names it. It returns "" when it hasn't, or git can't tell, as when it can't tell
// whether the branch is there. loadUnmerged and CheckNothingToRun both ask it, so that a run and its
// check agree on what is merged.
func mergedSince(ctx context.Context, c Config, worktrees Worktrees, merger Merger, t Ticket) string {
	switch has, err := worktrees.HasBranch(ctx, c.Repo, branchOf(t.ID)); {
	case err != nil:
		return "" // not a branch gone: a commit on Base naming the ticket may be another's mention of it
	case has:
		return branchMerged(ctx, c, merger, t, "")
	}
	rev := c.Base // labelled before ForkKey was noted: any commit on Base
	if fork := forkOf(t); fork != "" {
		rev = fork + ".." + c.Base
	}
	return namingOn(ctx, c, merger, rev, t.ID)
}

// namingOn is the latest commit in rev's history naming ticket id (see Merger.CommitNamingOn), or ""
// when none does or git can't tell: nothing is taken for merged on a git call that failed.
func namingOn(ctx context.Context, c Config, merger Merger, rev, id string) string {
	naming, err := merger.CommitNamingOn(ctx, c.Repo, rev, id)
	if err != nil {
		return ""
	}
	return naming
}

// branchMerged returns the commit naming ticket t that its branch added, when the branch is on Base
// (see ownCommits), or "". A branch with no commits of its own is always on Base: an older commit
// naming the ticket there, such as another ticket's mentioning it or an earlier attempt at it, is
// not its merge. fork is where the branch was cut, as the run that left its worker behind found it
// (see saveCarried), for a ticket with no ForkKey; "" if not known. A branch squash-merged by hand
// and kept is not on Base, but Base holds its changes: then a commit on Base that the branch doesn't
// have names the ticket, and is returned. Where git fails to tell, it returns "".
func branchMerged(ctx context.Context, c Config, merger Merger, t Ticket, fork string) string {
	br := branchOf(t.ID)
	switch on, err := merger.IsAncestor(ctx, c.Repo, br, c.Base); {
	case err != nil:
		return ""
	case on:
		return namingOn(ctx, c, merger, ownCommits(t, fork, br), t.ID)
	}
	if held, err := merger.Holds(ctx, c.Repo, c.Base, br); err != nil || !held {
		return ""
	}
	return namingOn(ctx, c, merger, br+".."+c.Base, t.ID)
}

// ForkKey is the metadata key holding the commit on Base that a ticket's branch was cut from (their
// merge-base), as the run that labelled it UnmergedLabel found it: the commits after it are the
// branch's own.
const ForkKey = "unmerged_fork"

// ownCommits is the revisions holding ticket t's own commits up to rev: those since its branch was
// cut, when its ForkKey says where, or else fork (a commit hash, or ""), or else rev alone.
func ownCommits(t Ticket, fork, rev string) string {
	if f := forkOf(t); f != "" {
		fork = f
	}
	if isHash(fork) {
		return fork + ".." + rev
	}
	return rev + "^!"
}

// forkOf returns the commit ticket t's ForkKey holds, or "" when it holds none, or something other
// than a commit hash: the metadata is anyone's to change, and git takes a revision beginning with a
// dash for an option.
func forkOf(t Ticket) string {
	var fork string
	if json.Unmarshal(t.metadataField(ForkKey), &fork) != nil || !isHash(fork) {
		return ""
	}
	return fork
}

// isHash reports whether s is a commit hash, abbreviated or whole, and so a revision git can't take
// for an option or a range.
func isHash(s string) bool {
	return len(s) >= 7 && len(s) <= 64 && strings.Trim(s, "0123456789abcdef") == ""
}

// leaveUnmerged sets aside a closed ticket that was not merged; tickets it blocks wait for it, in
// this run and, through its UnmergedLabel, in later ones. It reports whether bd labelled it.
func (o *Loop) leaveUnmerged(ctx context.Context, id, why string) bool {
	o.markAside(id)
	o.mu.Lock()
	o.unmerged[id] = why
	o.mu.Unlock()
	return o.label(ctx, id)
}

// asideKind is what a closed ticket is left unmerged for: the word its warning begins with, kept as
// why it is unmerged (see unmergedWhy).
type asideKind string

// The kinds of closed ticket left unmerged for review, or for the next run to merge.
const (
	asideNoCommit      asideKind = "CLOSED_WITHOUT_COMMIT" // no commit on its branch names it
	asideUncommitted   asideKind = "CLOSED_UNCOMMITTED"    // its worktree has uncommitted changes
	asideConflict      asideKind = "MERGE_CONFLICT"        // its branch conflicts with Base, or Base kept changing
	asideDirty         asideKind = "DIRTY_WORKTREE"        // uncommitted changes keep its branch from a rebase
	asideRebaseFailed  asideKind = "REBASE_FAILED"         // git could not rebase its branch onto Base
	asideOffBranch     asideKind = "OFF_BRANCH"            // its worktree is not on its branch
	asideChecksFailed  asideKind = "CHECKS_FAILED"         // its check failed on its branch rebased onto Base
	asideAskedUnmerged asideKind = "ASKED_UNMERGED"        // closed after its question, as the run ended
	asideLeftUnmerged  asideKind = "LEFT_UNMERGED"         // closed where the last run left it, as this one ended
)

// leaveFor leaves closed ticket id unmerged for k (see leaveUnmerged) and warns of it with
// "  <k>: <text>"; detail and blocked are the dashboard's (Event.Detail, Event.Blocked).
func (o *Loop) leaveFor(ctx context.Context, id string, k asideKind, detail, blocked, text string) {
	o.leaveUnmerged(ctx, id, string(k))
	o.emit(Event{Kind: EvWarn, Ticket: id, Aside: true, Detail: detail, Blocked: blocked,
		Text: "  " + string(k) + ": " + text})
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

// label adds UnmergedLabel to the ticket, warning when bd can't, and reports whether it did. It
// first notes where the ticket's branch left Base (ForkKey), so that a later run takes only the
// branch's own commits for its merge (see mergedSince).
func (o *Loop) label(ctx context.Context, id string) bool {
	fork, err := o.merger.MergeBase(ctx, o.cfg.Repo, o.cfg.Base, branchOf(id))
	o.log.Raw("", err) // none noted then, as for a branch with no commit in common with Base
	if fork != "" {
		if err := o.notes.SetMetadata(ctx, id, ForkKey, fork); err != nil {
			o.log.Raw("", err) // a later run then takes the branch's tip alone for its own
		}
	}
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

// clearUnmerged forgets that the ticket was unmerged, and removes its UnmergedLabel if it has one.
func (o *Loop) clearUnmerged(ctx context.Context, id string) {
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
	o.labelled[id] = labelled
}

// busy reports whether ticket id's worker runs, its work merges, or it waits on a question, or is
// one the last run left that this run keeps for a later one (see isKeptOut, mergeState).
func (o *Loop) busy(id string) bool {
	return o.isActive(id) || o.isAsked(id) || o.isKeptOut(id)
}

// unmergedWhy is why ticket id is closed but not merged, as the run left it, or "".
func (o *Loop) unmergedWhy(id string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.unmerged[id]
}

func (o *Loop) anyUnmerged() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.unmerged) > 0
}
