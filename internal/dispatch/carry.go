package dispatch

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/mcp"
	"github.com/noesis-sol/orchestra/internal/project"
)

// A run leaves workers behind in their tabs: on tickets waiting on a question (askedIDs), and on
// tickets it leaves running when it stops (active). What it knows of them would end with it, so as
// it ends, on every path, it saves them to .orchestra/run/state.json in the main checkout, and the
// next run puts each back where its own asked workers go, for the same paths to take up: followAsked
// adopts a worker that closed its ticket or works on it again, and work resumes an idle one whose
// ticket comes back through bd ready. Both happen while the run holds its lock, so only one run
// reads or writes the file. If the file is lost, the unmerged labels (leaveRunning, leaveAsked)
// still hold the tickets they block.

// loadCarried reads the workers the last run left behind and puts each where this run's asked
// workers go, once carriedStands has checked it. One on a ticket outside this run's scope is kept
// for a later run. It says, in the START block, what it carried over.
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
	for _, e := range s.Workers {
		if why := IDProblem(e.Ticket); why != "" {
			o.log.Raw("", fmt.Errorf("not carrying over %q from the last run: %s", e.Ticket, why))
			continue
		}
		w := askedWorker{tab: e.Tab, wt: e.Worktree, pane: e.Pane, question: e.Question, title: e.QuestionTitle,
			hooks: e.Hooks, left: stopKind(e.Left)}
		if !inScope(e.Ticket) {
			o.keptOut = append(o.keptOut, e)
			kept = append(kept, e.Ticket+" ("+w.state()+")")
			continue
		}
		if !o.carriedStands(ctx, e.Ticket, &w) {
			continue
		}
		o.setAsked(e.Ticket, &w)
		carried = append(carried, e.Ticket+" ("+w.state()+")")
	}
	if len(carried) > 0 {
		o.info("  carried over from the last run: %s", strings.Join(carried, ", "))
	}
	if len(kept) > 0 {
		o.info("  kept for a later run, outside this run's scope: %s", strings.Join(kept, ", "))
	}
}

// state is what the worker was left waiting on, for a list of them: "asked Q", or "left running:
// PAUSED".
func (w askedWorker) state() string {
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

// carriedStands checks a worker the last run left behind on ticket id, as w says, before this run
// trusts it: the ticket must still be there, and its worktree (WorktreeOf wt/<id>); a ticket closed
// with its branch already on Base was merged by hand. A worker left unnamed in its pane is named
// (see earlierState). A ticket in progress whose worker is gone is carried over if its session can
// be resumed (see resumeGone); without one, it has nothing to carry on with: it is warned about and
// noted, once, and the run goes on. Each of those drops the worker. The
// question the ticket now waits on, if any, replaces the one in w, and a ticket labelled
// UnmergedLabel loses the label when it merges.
func (o *Loop) carriedStands(ctx context.Context, id string, w *askedWorker) bool {
	c := o.cfg
	br := branchOf(id)
	t, err := o.showTries(ctx, id)
	if err != nil {
		o.info("  %s, carried over from the last run, is dropped: bd can't show it%s", id, because(err))
		return false
	}
	if wt := o.worktrees.WorktreeOf(ctx, c.Repo, br); wt == "" || !mcp.SamePath(wt, w.wt) {
		o.info("  %s, carried over from the last run, is dropped: its worktree %s is gone", id, w.wt)
		return false
	}
	if t.Status == StatusClosed && o.merger.IsAncestor(ctx, c.Repo, br, c.Base) {
		if commit := o.merger.CommitNamingOn(ctx, c.Repo, br, id); commit != "" {
			o.info("  %s, carried over from the last run, is dropped: %s is on %s already (%s)", id, br, c.Base, commit)
			return false
		}
	}
	if q := OpenQuestion(t); q != nil {
		w.question, w.title, w.left = q.ID, q.Title, ""
	}
	// Read whatever the ticket's status, so that a worker left unnamed is named before anything looks
	// for it by its name.
	st, err := o.earlierState(ctx, id, *w, 1)
	if err != nil {
		o.log.Raw("", err) // read again as the ticket is followed (followAsked) or comes back (work)
	}
	_, resumable := o.sessionOf(w.wt, w.hooks)
	if t.Status == StatusInProgress && err == nil && st == StateGone && !resumable {
		o.appendNotes(context.WithoutCancel(ctx), id, fmt.Sprintf(
			"Orchestra: the worker in Herdr tab %s is gone, with the ticket still in_progress after %s (worktree %s).",
			w.tab, w.after(), w.wt))
		o.emit(Event{Kind: EvWarn, Ticket: id, Aside: true, Detail: "in progress, its worker gone", Text: fmt.Sprintf(
			"  WORKER_GONE: %s is in progress after %s, but its worker is gone from tab %s (worktree %s), "+
				"so it isn't carried over; reopen it to run it again (bd update %s --status open), or finish it by hand",
			id, w.after(), w.tab, w.wt, id)})
		return false
	}
	if HasLabel(t, UnmergedLabel) {
		o.setLabelled(id, true)
	}
	return true
}

// earlierState reads the state of ticket id's earlier worker by its Herdr name, as readStatus does
// with tries; w says where it was left (zero if it wasn't). One whose start a run cut short before
// Herdr saw it came up without that name (see nameLeft): with no agent under the name, an unnamed
// agent of the configured kind in w's pane is named, and its state returned. Herdr numbers tabs and
// panes afresh when it restarts, so the pane is looked in only while Herdr has w's tab labelled
// with the ticket's ID, as merging checks before closing it. The error is Herdr not saying what is
// there, or not taking the name.
func (o *Loop) earlierState(ctx context.Context, id string, w askedWorker, tries int) (AgentState, error) {
	agent := o.agentName(id)
	st, err := o.readStatus(ctx, agent, tries)
	if err != nil || st != StateGone || w.pane == "" {
		return st, err
	}
	label, open, err := o.tabs.TabLabel(ctx, w.tab)
	if err != nil {
		return "", fmt.Errorf("cannot tell whether tab %s is still %s's, to look for its worker there: %w", w.tab, id, err)
	}
	if !open || label != id {
		return StateGone, nil
	}
	name, kind, st, err := o.namer.PaneAgent(ctx, w.pane)
	switch {
	case err != nil:
		return "", err
	case st == StateGone || kind != o.cfg.AgentKind || name != "":
		return StateGone, nil // nothing there, or another's
	}
	if err := o.namer.RenameAgent(ctx, w.pane, agent); err != nil {
		return "", fmt.Errorf("cannot give %s's worker, unnamed in tab %s, the name %s: %w", id, w.tab, agent, err)
	}
	o.info("  %s's worker in tab %s came up without its name; named it %s", id, w.tab, agent)
	return st, nil
}

// showTries reads ticket id, trying up to three times while bd fails, as when another bd holds the
// database: a ticket bd can't show isn't carried over.
func (o *Loop) showTries(ctx context.Context, id string) (Ticket, error) {
	for try := 1; ; try++ {
		t, err := o.tickets.Show(ctx, id)
		if err == nil || try == 3 || ctx.Err() != nil || !sleep(ctx, o.pollEvery()) {
			return t, err
		}
		o.log.Raw("", err)
	}
}

// leaveBehind, as the run ends, labels the tickets it leaves running (leaveRunning) and those
// waiting on a question (leaveAsked), and saves their workers for the next run. ctx is the run's,
// which tells leaveAsked whether Ctrl+C came; the labelling goes on regardless.
func (o *Loop) leaveBehind(ctx context.Context) {
	o.leaveRunning(context.WithoutCancel(ctx))
	o.leaveAsked(ctx)
	o.saveCarried()
}

// saveCarried writes the workers this run leaves behind for the next one, as it ends: those on
// tickets still waiting on a question, those it leaves running, and those it kept for a later run.
// A ticket it left for review (unmerged) isn't among them, nor one whose rebase it left in progress
// for the maintainer to finish (see workerNames). It says which, and warns when they can't be
// saved: the next run then carries none of them over.
func (o *Loop) saveCarried() {
	var left []project.LeftWorker
	var said []string
	add := func(id string, w askedWorker) {
		left = append(left, project.LeftWorker{Ticket: id, Agent: o.agentName(id), Tab: w.tab, Pane: w.pane,
			Worktree: w.wt, Hooks: w.hooks, Question: w.question, QuestionTitle: w.title, Left: string(w.left)})
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
		why := o.unmerged[id]
		o.mu.Unlock()
		if ok && !st.Resolving && !st.Fixing && (why == "" || why == earlierRun) {
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
		return
	}
	if len(said) > 0 {
		slices.Sort(said)
		o.info("  left for the next run: %s", strings.Join(said, ", "))
	}
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
func (o *Loop) place(id string, w askedWorker) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.placed == nil {
		o.placed = map[string]askedWorker{}
	}
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
