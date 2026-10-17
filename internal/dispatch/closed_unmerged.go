package dispatch

import (
	"context"
	"fmt"
	"sync"
)

// A ticket closed where the run didn't see it close (by hand, by another tool, in a tab the run left
// or under an earlier orchestra) went through no merge: bd counts it done, and the run would take it
// for merged, though its work may be only on its branch. As a backstop for any such close, a closed
// ticket is not merged while git says so: its branch exists, has commits Base doesn't have, and Base
// doesn't hold its changes (branchMerged's two tests). A branch with no commits beyond Base holds
// nothing back, and no branch at all counts as merged.

// branchOff returns why closed ticket id's work is only on its branch, as git finds it, or "".
func branchOff(ctx context.Context, c Config, worktrees Worktrees, merger Merger, id string) string {
	br := branchOf(id)
	if !worktrees.HasBranch(ctx, c.Repo, br) || merger.IsAncestor(ctx, c.Repo, br, c.Base) ||
		merger.Holds(ctx, c.Repo, c.Base, br) {
		return ""
	}
	return fmt.Sprintf("%s is not on %s", br, c.Base)
}

// closedBranches keeps what git said of closed tickets' branches (see branchOff), for each ticket
// and the commit Base was on, so that a poll asks git once for each ticket, not once for each ready
// ticket it blocks. A branch found not on Base is asked of again once it is on another commit or
// gone: deleted, as CLOSED_UNMERGED offers, it frees the tickets it blocks at the next poll. Its
// zero value is ready to use, from any goroutine.
type closedBranches struct {
	mu     sync.Mutex
	known  map[string]branchAt
	warned map[string]bool
}

// branchAt is why a closed ticket's work was only on its branch, or "", with Base on head and the
// branch on tip.
type branchAt struct {
	head, tip, why string
}

// why returns why closed ticket id's work is only on its branch, with Base on head, asking git (off)
// unless it has already for that head and, when it found the work only on the branch, for the commit
// the branch is on (tip, asked each time); "" for a head or a tip means git couldn't say, and git is
// asked again. first is whether this is the first time the ticket was found so.
func (b *closedBranches) why(id, head string, tip, off func() string) (why string, first bool) {
	b.mu.Lock()
	k, ok := b.known[id]
	b.mu.Unlock()
	if ok && head != "" && k.head == head && (k.why == "" || k.tip != "" && tip() == k.tip) {
		return k.why, false
	}
	at := tip() // before off: a branch that moves meanwhile is asked of again
	why = off()
	b.mu.Lock()
	defer b.mu.Unlock()
	if head != "" {
		if b.known == nil {
			b.known = map[string]branchAt{}
		}
		b.known[id] = branchAt{head: head, tip: at, why: why}
	}
	if why == "" || b.warned[id] {
		return why, false
	}
	if b.warned == nil {
		b.warned = map[string]bool{}
	}
	b.warned[id] = true
	return why, true
}

// closedWhy is why ticket id, closed, is not merged (see mergeState): as the run left it, or with
// its work only on its branch.
func (o *Loop) closedWhy(ctx context.Context, id string) string {
	if why := o.unmergedWhy(id); why != "" {
		return why
	}
	return o.branchWhy(ctx, id)
}

// branchWhy returns why closed ticket id's work is only on its branch, as git finds it with Base
// where this pick found it (see pickHead), or "". The first time it finds a ticket so, it warns: the
// tickets it blocks wait until it merges, and only the maintainer can merge it.
func (o *Loop) branchWhy(ctx context.Context, id string) string {
	c := o.cfg
	why, first := o.branches.why(id, o.pickHead(ctx), func() string {
		return o.checkout.Head(ctx, c.Repo, branchOf(id))
	}, func() string {
		return branchOff(ctx, c, o.worktrees, o.merger, id)
	})
	if first {
		br := branchOf(id)
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  CLOSED_UNMERGED: %s is closed, but %s is not on %s: the tickets it blocks wait until it merges; "+
				"merge it into %s, or delete the branch if its work isn't wanted (git branch -D %s)",
			id, br, c.Base, c.Base, br)})
	}
	return why
}

// pickHead is the commit Base is on, read once for each pick of the run (see Loop.picks), or "" when
// git can't say.
func (o *Loop) pickHead(ctx context.Context) string {
	o.mu.Lock()
	at := o.baseAt
	pick := o.picks
	o.mu.Unlock()
	if at.head != "" && at.pick == pick {
		return at.head
	}
	head := o.checkout.Head(ctx, o.cfg.Repo, o.cfg.Base)
	o.mu.Lock()
	o.baseAt = pickedHead{pick: pick, head: head}
	o.mu.Unlock()
	return head
}

// pickedHead is the commit Base was on in the pick'th pick of the run.
type pickedHead struct {
	pick int
	head string
}
