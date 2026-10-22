package dispatch

import (
	"context"
	"fmt"
	"time"
)

// workerRef is where a ticket's worker is: the terminal's tab it runs in, the pane in that tab, its
// name there, its worktree, and whether it reports what it does through hooks. It is made where the
// worker is started or found, and passed whole: worker and workerSpot embed it, and the ticket's
// record keeps where its worker was placed (see ticketBook.place). Whether the worker is still there
// is presence's to say, for every path that waits on it or follows it.
type workerRef struct {
	tab, wt, agent string
	pane           string // the pane it was started in, where it is looked for unnamed (see presence); "" if not known
	hooks          bool   // it reports what it does through hooks
}

// located reports whether ref says where a worker is: its tab, its worktree, and its name there, which
// the run looks for it by (see presence). The ticket book keeps no worker that isn't (see
// ticketRecord.check).
func (ref workerRef) located() bool {
	return ref.tab != "" && ref.wt != "" && ref.agent != ""
}

// presence reads the state of ticket id's worker, ref: by its name, or, the terminal having no agent
// under the name, unnamed in its pane (see unnamedInPane), where it is named again. A program the
// worker runs can take the name from it while it works, as a Claude Code started there with the
// pane's Herdr variables does, Herdr's hook taking that one for the pane's agent; and a start a run
// cut short leaves one that never had it (see nameLeft). One the terminal refuses the name is taken
// for gone, the refusal logged. The error is the terminal not saying what is there, which says
// nothing of the worker, and leaves gone as it was.
//
// One the terminal shows nowhere is StateGone only once it has been so for longer than grace: Herdr
// can lose sight of a worker still at work for a moment (see goneGrace). Until then it is lost: read
// it again. gone is how long it has been shown nowhere, which the caller keeps from one read to the
// next; nil, with grace 0, as for a read that decides nothing on its own, has the read alone say.
func (o *Loop) presence(ctx context.Context, id string, ref workerRef, gone *streak, grace time.Duration) (
	st AgentState, lost bool, err error) {
	st, err = o.agents.Status(ctx, ref.agent)
	if err == nil && st == StateGone {
		st, err = o.nameLost(ctx, id, ref)
	}
	if err != nil || gone == nil {
		return st, false, err
	}
	goneFor := gone.track(st == StateGone)
	return st, st == StateGone && grace > 0 && goneFor <= grace, nil
}

// presenceFor reads ticket id's worker, ref, as presence does, a poll apart while it is lost, so that
// one the terminal shows nowhere is gone only once it has been so for grace, and while the terminal
// fails to answer, up to statusTries times in a row: each failure but the last is logged, and the
// last returned for the caller to report. It is for a path that decides once, rather than poll after
// poll: Ctrl+C cuts it short, with the state as last read.
func (o *Loop) presenceFor(ctx context.Context, id string, ref workerRef, grace time.Duration) (AgentState, error) {
	var gone streak
	failed := 0
	for {
		st, lost, err := o.presence(ctx, id, ref, &gone, grace)
		switch {
		case err != nil:
			if failed++; failed == statusTries {
				return st, err
			}
			o.log.Raw("", err)
		case !lost:
			return st, nil
		}
		if !sleep(ctx, o.pollEvery()) {
			return st, err
		}
	}
}

// statusTries is how many times in a row presenceFor reads a worker the terminal fails to answer about.
const statusTries = 5

// nameLost looks in its pane for ticket id's worker, ref, which the terminal has no agent under its
// name for. Found there unnamed (see unnamedInPane), it is named again and its state returned;
// StateGone otherwise, and when the terminal refuses the name, which is logged. The error is the
// terminal not saying what is there.
func (o *Loop) nameLost(ctx context.Context, id string, ref workerRef) (AgentState, error) {
	if ref.pane == "" || o.namer == nil {
		return StateGone, nil
	}
	st, err := o.unnamedInPane(ctx, id, ref.tab, ref.pane)
	if err != nil || st == StateGone {
		return st, err
	}
	if err := o.namer.RenameAgent(ctx, ref.pane, ref.agent); err != nil {
		if ctx.Err() != nil {
			return "", err // cut short, not refused
		}
		o.log.Raw("", fmt.Errorf("%s's worker is without its name in tab %s and can't be named %s again, so it is "+
			"taken for gone: %w", id, ref.tab, ref.agent, err))
		return StateGone, nil
	}
	o.info("  %s's worker was without its name in tab %s; named it %s again", id, ref.tab, ref.agent)
	return st, nil
}

// unnamedInPane returns the state of the agent of the configured kind without a name in pane, which
// is ticket id's worker gone from under its name, or StateGone when there is none: nothing there, or
// another's. Herdr numbers tabs and panes afresh when it restarts, so the pane is looked in only while
// the terminal has its tab open and labelled with the ticket's ID, as merging checks before closing
// it. The error is the terminal not saying what is there.
func (o *Loop) unnamedInPane(ctx context.Context, id, tab, pane string) (AgentState, error) {
	label, open, err := o.tabs.TabLabel(ctx, tab)
	if err != nil {
		return "", fmt.Errorf("cannot tell whether tab %s is still %s's, to look for its worker there: %w", tab, id, err)
	}
	if !open || label != id {
		return StateGone, nil
	}
	name, kind, st, err := o.namer.PaneAgent(ctx, pane)
	switch {
	case err != nil:
		return "", err
	case st == StateGone || kind != o.cfg.Agent().Name || name != "":
		return StateGone, nil // nothing there, or another's
	}
	return st, nil
}

// goneStreak is how long the terminal has shown ticket id's worker, followed in its tab (see
// followAsked, followLeft), nowhere, kept from one poll's read to the next.
func (r *runState) goneStreak(id string) *streak {
	s := r.gone[id]
	if s == nil {
		s = &streak{}
		r.gone[id] = s
	}
	return s
}

// awaitSighting is Run's wait once nothing runs while a worker it follows in its tab is lost (see
// presence): whether it is gone decides what becomes of its ticket (see followAsked, followLeft), so the
// run doesn't end before the terminal has said, but waits a poll, and goes round to read it again. Once
// something has stopped the run, it doesn't apply.
func (r *runState) awaitSighting(from gateMark) (applies, goOn bool) {
	if r.o.gate.firstStop() != nil { // nor are workers followed any more (see follow)
		return false, false
	}
	lost := false
	for _, s := range r.gone {
		lost = lost || !s.since.IsZero()
	}
	if !lost {
		return false, false
	}
	poll := time.NewTimer(r.o.pollEvery())
	defer poll.Stop()
	r.idleWake(waits{poll: poll.C}, from)
	return true, true
}

// forgetUnfollowed drops what goneStreak kept of the workers no longer followed, so that one followed
// again later starts afresh.
func (r *runState) forgetUnfollowed() {
	for id := range r.gone {
		if _, left := r.o.leftOpenSpot(id); !left && !r.o.isAsked(id) {
			delete(r.gone, id)
		}
	}
}
