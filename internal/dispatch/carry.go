package dispatch

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/mcp"
	"github.com/noesis-sol/orchestra/internal/project"
)

// A run leaves workers behind in their tabs: on tickets waiting on a question (askedList), and on
// tickets it leaves running when it stops (active). What it knows of them would end with it, so as
// it ends, on every path, it saves them to .orchestra/run/state.json in the main checkout, and the
// next run puts each back where its own asked workers go, for the same paths to take up: followAsked
// adopts a worker that closed its ticket or works on it again, and work resumes an idle one whose
// ticket comes back through bd ready. Both happen while the run holds its lock, so only one run
// reads or writes the file. If the file is lost, the unmerged labels (leaveRunning, leaveAsked)
// still hold the tickets they block.

// loadCarried reads the workers the last run left behind and puts each where this run's asked
// workers go, once carriedStands has checked it. One on a ticket outside this run's scope is kept
// for a later run, as is one in the other terminal, which this run can't reach. It says, in the START
// block, what it carried over and what it kept.
func (o *Loop) loadCarried(ctx context.Context) {
	s, err := project.LoadState(o.cfg.Repo)
	if err != nil {
		o.log.Raw("", fmt.Errorf("cannot read the workers the last run left behind, so none is carried over: %w", err))
		return
	}
	if len(s.Workers) == 0 {
		return
	}
	inScope := o.scoped(ctx)
	var carried, kept []string
	elsewhere := map[string][]string{} // by terminal
	for _, e := range s.Workers {
		if why := IDProblem(e.Ticket); why != "" {
			o.log.Raw("", fmt.Errorf("not carrying over %q from the last run: %s", e.Ticket, why))
			continue
		}
		w := workerSpot{tab: e.Tab, wt: e.Worktree, pane: e.Pane, fork: e.Fork, question: e.Question,
			title: e.QuestionTitle, hooks: e.Hooks, left: stopKind(e.Left)}
		if !project.SameTerminal(e.Terminal, o.cfg.Terminal) {
			o.keptOut = append(o.keptOut, e)
			elsewhere[e.Terminal] = append(elsewhere[e.Terminal], e.Ticket+" ("+w.state()+")")
			continue
		}
		if !inScope(e.Ticket) {
			o.keptOut = append(o.keptOut, e)
			kept = append(kept, e.Ticket+" ("+w.state()+")")
			continue
		}
		switch o.carriedStands(ctx, e.Ticket, &w) {
		case carryDrop:
			continue
		case carryKeep: // unchecked, not gone: the next run checks it again
			o.keptOut = append(o.keptOut, e)
			continue
		}
		o.book.carry(e.Ticket, w)
		carried = append(carried, e.Ticket+" ("+w.state()+")")
	}
	if len(carried) > 0 {
		o.info("  carried over from the last run: %s", strings.Join(carried, ", "))
	}
	if len(kept) > 0 {
		o.info("  kept for a later run, outside this run's scope: %s", strings.Join(kept, ", "))
	}
	for _, t := range slices.Sorted(maps.Keys(elsewhere)) {
		o.info("  kept for a run in %s, as this run in %s can't reach their workers: %s",
			o.cfg.terminalLabel(t).Name, o.cfg.terminalLabel(o.cfg.Terminal).Name, strings.Join(elsewhere[t], ", "))
	}
}

// isKeptOut reports whether ticket id's worker, left by the last run, is kept for a later run (see
// loadCarried): in the other terminal, outside this run's scope, or not checked. Its worker may still
// be at work, so the run doesn't start it, as it would a second worker in the same worktree, and it
// holds the tickets it blocks as one running does.
func (o *Loop) isKeptOut(id string) bool {
	o.leftMu.Lock()
	defer o.leftMu.Unlock()
	return slices.ContainsFunc(o.keptOut, func(e project.LeftWorker) bool { return e.Ticket == id })
}

// keptOutIDs is the tickets whose workers are kept for a later run (see isKeptOut).
func (o *Loop) keptOutIDs() []string {
	o.leftMu.Lock()
	defer o.leftMu.Unlock()
	return ticketsOf(o.keptOut)
}

// state is what the worker was left waiting on, for a list of them: "asked Q", or "left running:
// PAUSED".
func (w workerSpot) state() string {
	if w.question != "" {
		return "asked " + w.question
	}
	return "left running: " + w.leftWhy()
}

// scoped returns whether a ticket is in this run's scope: any, in a run of all of bd ready. When bd
// can't list the scope's subtickets, only the scope's own ticket is.
func (o *Loop) scoped(ctx context.Context) func(id string) bool {
	r := o.cfg.Ticket
	if r == "" {
		return func(string) bool { return true }
	}
	in := map[string]bool{r: true}
	subs, err := o.tickets.Descendants(ctx, r)
	if err != nil {
		o.log.Raw("", fmt.Errorf("cannot list %s's subtickets to carry over the workers left on them: %w", r, err))
	}
	for _, t := range subs {
		in[t.ID] = true
	}
	return func(id string) bool { return in[id] }
}

// carryCheck is what carriedStands makes of a worker the last run left behind.
type carryCheck int

const (
	carryOn   carryCheck = iota // carried over into this run
	carryDrop                   // gone: forgotten, as said
	carryKeep                   // not checked: kept as it was, for the next run to check
)

// carriedStands checks a worker the last run left behind on ticket id, as w says, before this run
// trusts it: the ticket must still be there, and its worktree (WorktreeOf wt/<id>); a ticket closed
// with its branch's own commits already on Base, since its ForkKey or else w's fork, was merged by
// hand (see branchMerged). A worker
// left unnamed in its pane is named (see earlierState). A ticket in progress whose worker is gone is
// carried over if its session can be resumed (see resumeSession); without one, it has nothing to carry
// on with: it is warned about and noted, once, and the run goes on. Each of those drops the worker.
// One it can't check, as bd can't show its ticket or git can't list the worktrees, isn't gone for
// that: it is kept, saying why, as is one whose check Ctrl+C cut short, without a word; one whose
// ticket bd has no trace of (ErrNoTicket), as one deleted, is gone, and dropped. The question
// the ticket now waits on, if any, replaces the one in w, and a ticket labelled UnmergedLabel loses
// the label when it merges.
func (o *Loop) carriedStands(ctx context.Context, id string, w *workerSpot) carryCheck {
	c := o.cfg
	br := branchOf(id)
	unchecked := func(why string, err error) carryCheck {
		if ctx.Err() == nil { // else cut short by Ctrl+C, not gone (see loadCarried)
			o.info("  %s, carried over from the last run, is kept for the next run: %s%s", id, why, because(err))
		}
		return carryKeep
	}
	t, err := o.showTries(ctx, id)
	switch {
	case errors.Is(err, ErrNoTicket):
		o.info("  %s, carried over from the last run, is dropped: bd has no such ticket", id)
		return carryDrop
	case err != nil:
		return unchecked("bd can't show it", err)
	}
	wt, err := o.worktrees.WorktreeOf(ctx, c.Repo, br)
	if err != nil {
		return unchecked("git can't list the worktrees, to find its own", err)
	}
	if wt == "" || !mcp.SamePath(wt, w.wt) {
		if ctx.Err() != nil {
			return carryKeep
		}
		o.info("  %s, carried over from the last run, is dropped: its worktree %s is gone", id, w.wt)
		return carryDrop
	}
	if t.Status == StatusClosed {
		if commit := branchMerged(ctx, c, o.merger, t, w.fork); commit != "" {
			o.info("  %s, carried over from the last run, is dropped: %s is on %s already (%s)", id, br, c.Base, commit)
			return carryDrop
		}
	}
	if q := OpenQuestion(t); q != nil {
		w.question, w.title, w.left = q.ID, q.Title, ""
	}
	// Read whatever the ticket's status, so that a worker left unnamed is named before anything looks
	// for it by its name.
	st, err := o.earlierState(ctx, id, *w, 1)
	if err != nil && ctx.Err() == nil { // one the stop cut short says nothing
		o.log.Raw("", err) // read again as the ticket is followed (followAsked) or comes back (work)
	}
	_, resumable := o.sessionOf(w.wt, w.hooks)
	if t.Status == StatusInProgress && err == nil && st == StateGone && !resumable {
		o.warnWorkerGone(ctx, id, *w, "so it isn't carried over")
		return carryDrop
	}
	if HasLabel(t, UnmergedLabel) {
		o.setLabelled(id, true)
	}
	o.book.setParent(id, t.Parent) // its parent waits for it, should it close in its tab (see openParents)
	return carryOn
}

// earlierState reads the state of ticket id's earlier worker by its Herdr name, as readStatus does
// with tries; w says where it was left (zero if it wasn't). One whose start a run cut short before
// Herdr saw it came up without that name (see nameLeft): with no agent under the name, an unnamed
// agent of the configured kind in w's pane (see unnamedInPane) is named, and its state returned. The
// error is Herdr not saying what is there, or not taking the name.
func (o *Loop) earlierState(ctx context.Context, id string, w workerSpot, tries int) (AgentState, error) {
	agent := o.agentName(id)
	st, err := o.readStatus(ctx, agent, tries)
	if err != nil || st != StateGone || w.pane == "" {
		return st, err
	}
	if st, err = o.unnamedInPane(ctx, id, w.tab, w.pane); err != nil || st == StateGone {
		return st, err
	}
	if err := o.namer.RenameAgent(ctx, w.pane, agent); err != nil {
		return "", fmt.Errorf("cannot give %s's worker, unnamed in tab %s, the name %s: %w", id, w.tab, agent, err)
	}
	o.info("  %s's worker in tab %s came up without its name; named it %s", id, w.tab, agent)
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
	case st == StateGone || kind != o.cfg.AgentKind || name != "":
		return StateGone, nil // nothing there, or another's
	}
	return st, nil
}

// unknownRereads is how many more times earlierKnownState reads an earlier worker Herdr shows as
// unknown, a poll apart, before taking it as it is.
const unknownRereads = 5

// earlierKnownState reads the state of ticket id's earlier worker as earlierState does, reading it
// again while Herdr can't tell what it is doing, as for a moment while it redraws: StateUnknown is
// one that stays so.
func (o *Loop) earlierKnownState(ctx context.Context, id string, w workerSpot) (AgentState, error) {
	st, err := o.earlierState(ctx, id, w, 5)
	for range unknownRereads {
		if err != nil || st != StateUnknown || !sleep(ctx, o.pollEvery()) {
			break
		}
		st, err = o.earlierState(ctx, id, w, 5)
	}
	return st, err
}

// showTries reads ticket id, trying up to three times while bd fails, as when another bd holds the
// database: a ticket bd can't show isn't carried over, but kept for the next run. One bd has no
// trace of (ErrNoTicket) isn't tried again: it is gone.
func (o *Loop) showTries(ctx context.Context, id string) (Ticket, error) {
	for try := 1; ; try++ {
		t, err := o.tickets.Show(ctx, id)
		if err == nil || errors.Is(err, ErrNoTicket) || try == 3 || ctx.Err() != nil || !sleep(ctx, o.pollEvery()) {
			return t, err
		}
		o.log.Raw("", err)
	}
}

// leaveBehind, as the run ends, labels the tickets it leaves running (leaveRunning) and those
// waiting on a question (leaveAsked), and saves their workers for the next run. ctx is the run's,
// which tells leaveAsked whether Ctrl+C came; the labelling goes on regardless. When workers run
// out of the maintainer's sight (Config.Watch) and some are left in their tabs, it says how to see them.
func (o *Loop) leaveBehind(ctx context.Context) {
	o.leaveRunning(context.WithoutCancel(ctx))
	o.leaveAsked(ctx)
	o.leftMu.Lock()
	left := o.saveCarried(context.WithoutCancel(ctx), "")
	o.leftSaved = true
	o.leftMu.Unlock()
	left = left || o.anyUnmerged()
	if left && o.cfg.Watch != "" {
		o.info("  to see their tabs: %s", o.cfg.Watch)
	}
}

// SaveLeft saves the workers the run leaves behind for the next run, as the run does as it ends
// (see leaveBehind), for orchestra to call as it quits at once, with the stopped run still winding
// down: the run's own saving never comes then. A worker still at work is saved as left running,
// INTERRUPTED. It labels no ticket (leaveRunning, leaveAsked), which would wait on bd: the next run
// carries the workers over from .orchestra/run/state.json alone. It does nothing before the run has
// carried over the last run's workers, as the file then still holds those it has yet to check, nor
// once the run has saved its own. It is safe to call from another goroutine than Run's.
func (o *Loop) SaveLeft() {
	o.leftMu.Lock()
	defer o.leftMu.Unlock()
	if o.carriedIn && !o.leftSaved {
		ctx := context.Background()
		if o.organCtx != nil {
			ctx = context.WithoutCancel(o.organCtx) // for the environment it gives commands (command.WithEnv)
		}
		o.saveCarried(ctx, stopInterrupted)
	}
}

// saveCarried writes the workers this run leaves behind for the next one, as it ends: those on
// tickets asked (still waiting on a question, or closed or claimed again in their tabs too late for
// this run to merge or adopt them: see leaveAsked), those it leaves running, and those it kept for
// a later run. A worker left running that hasn't stopped the run is saved as left so, running: ""
// when Run's workers have all returned. The caller holds leftMu.
// A ticket it left for review (unmerged) isn't among them, nor one whose rebase it left in progress
// for the maintainer to finish (see workerNames). It notes where each one's branch was cut from Base,
// unless that is known already, so that the next run takes only the branch's own commits for its
// merge: a worker waiting on a question has no ForkKey (see carriedStands). It says which, and warns
// when they can't be saved: the next run then carries none of them over. It reports whether it left
// any of this run's workers in their tabs.
func (o *Loop) saveCarried(ctx context.Context, running stopKind) bool {
	var left []project.LeftWorker
	var said []string
	add := func(id string, w workerSpot) {
		if w.fork == "" {
			var err error // "" on failure: the next run takes the branch's tip alone for its own
			w.fork, err = o.merger.MergeBase(ctx, o.cfg.Repo, o.cfg.Base, branchOf(id))
			o.log.Raw("", err)
		}
		left = append(left, project.LeftWorker{Ticket: id, Agent: o.agentName(id), Terminal: o.cfg.Terminal,
			Tab: w.tab, Pane: w.pane, Worktree: w.wt, Fork: w.fork, Hooks: w.hooks, Question: w.question, QuestionTitle: w.title,
			Left: string(w.left)})
		said = append(said, id+" ("+w.state()+")")
	}
	for _, id := range o.askedList() {
		w, _ := o.asked(id)
		add(id, w)
	}
	for _, st := range o.activeList() {
		id := st.Ticket
		o.mu.Lock()
		w, ok := o.placed[id]
		o.mu.Unlock()
		why := o.unmergedWhy(id)
		if ok && !st.handedBack() && (why == "" || why == earlierRun) {
			w.left = cmp.Or(w.left, running)
			add(id, w)
		}
	}
	left = append(left, o.keptOut...)
	slices.SortFunc(left, func(a, b project.LeftWorker) int { return strings.Compare(a.Ticket, b.Ticket) })
	if err := project.SaveState(o.cfg.Repo, project.RunState{Saved: time.Now(), Workers: left}); err != nil {
		o.log.Raw("", err)
		if len(left) > 0 {
			o.emit(Event{Kind: EvWarn, Text: fmt.Sprintf(
				"  STATE_UNSAVED: could not write %s%s; the next run won't carry over %s",
				project.RunPath(project.StateName), because(err), strings.Join(ticketsOf(left), ", "))})
		}
		return len(said) > 0
	}
	if len(said) > 0 {
		slices.Sort(said)
		o.info("  left for the next run: %s", strings.Join(said, ", "))
	}
	return len(said) > 0
}

// ticketsOf is the tickets of the workers left.
func ticketsOf(left []project.LeftWorker) []string {
	ids := make([]string, len(left))
	for i, e := range left {
		ids[i] = e.Ticket
	}
	return ids
}

// place records where ticket id's worker is, as it was adopted in this run, or from when its tab was
// opened (see startWorker).
func (o *Loop) place(id string, w workerSpot) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.placed[id] = w
}

// stoppedBy records why ticket id's worker, as it returned, stopped the run (s; nil for nothing):
// should the run leave it running, the next run is told.
func (o *Loop) stoppedBy(id string, s *stopReason) {
	if s == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if w, ok := o.placed[id]; ok {
		w.left = s.kind
		o.placed[id] = w
	}
}
