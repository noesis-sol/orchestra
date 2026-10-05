package dispatch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/project"
)

// When the check fails on a finished ticket's branch rebased onto Base, after a clean rebase or once
// its worker has resolved the rebase's conflicts, the failure often comes from its change meeting what
// landed meanwhile: a test another ticket added for the code as it was. The worker that did the ticket,
// idle in its tab with the change in mind, is asked to fix it, given the check's output and what
// landed, as it is asked to resolve conflicts (see handBack): up to Config.CheckHandBacks times in the
// ticket's merge, while the run takes on work. It commits the fix on the branch, each commit naming
// the ticket, and orchestra checks the branch again itself. A failure it can't fix leaves the ticket
// set aside as before.

// checked is how the check on a finished ticket's rebased branch came out (see checkRebased).
type checked struct {
	err    error    // nil once the check passes; else what the last setup or check to fail returned
	setup  bool     // that was the setup's, run before the check
	output string   // where its whole output is
	tries  []string // what came of each hand-back of a failure to the ticket's worker, in turn
	not    string   // why the last failure wasn't handed back, when hand-backs are on
}

// checkRebased sets up and checks the branch of r's ticket, rebased onto r.onto from r.head, and hands
// a failure back to its worker to fix while it can (see whyNotFixCheck), *fixes counting the hand-backs
// in the ticket's merge; resolved says its worker resolved the rebase. The caller holds the merge queue,
// which a hand-back lets go of while the worker works, and not repo, which is taken to undo a fix that
// is rejected. It returns errInterrupted after Ctrl+C, the worker's work left as it is.
func (o *Loop) checkRebased(ctx context.Context, repo, queue *held, r rebaseStop, fixes *int,
	resolved bool) (checked, *stopReason) {
	c := o.cfg
	keep := context.WithoutCancel(ctx) // a read cut short would look like a failure
	var res checked
	fixed := "" // what the worker committed on its last hand-back, until the check says whether it fixed it
	for {
		// The rebase, or the fix, may have changed the dependencies: set them up again first (see setUp).
		output, err := o.setUp(ctx, r.worker, r.head, r.onto)
		setup := err != nil
		if !setup {
			output, err = o.runCheck(ctx, r.worker, r.onto)
		}
		if err != nil && ctx.Err() != nil {
			return res, errInterrupted
		}
		if fixed != "" {
			if err == nil {
				res.tries = append(res.tries, "its worker committed "+fixed+" and the check passes")
			} else {
				res.tries = append(res.tries, fmt.Sprintf("its worker committed %s, but %s %s still (output is in %s)",
					fixed, o.failedWhat(setup), o.checkHow(err), output))
			}
		}
		if err == nil {
			switch {
			case fixed != "":
				o.info("  '%s' passes on %s as its worker fixed it", c.Check, r.br)
			case resolved:
				o.info("  '%s' passes on %s as its worker resolved it", c.Check, r.br)
			default:
				o.info("  '%s' passes on the rebased %s", c.Check, r.br)
			}
			return checked{tries: res.tries}, nil
		}
		res.err, res.setup, res.output = err, setup, output
		if c.CheckHandBacks == 0 {
			return res, nil
		}
		at := o.checkout.Head(keep, c.Repo, r.br)
		if res.not = o.whyNotFixCheck(keep, r, res, *fixes, at); res.not != "" {
			return res, nil
		}
		*fixes++
		// The worker takes minutes: let the other finished tickets merge meanwhile.
		queue.unlock()
		f, why, stop := o.fixCheck(ctx, r, at, res, *fixes)
		queue.lock()
		if stop != nil {
			return res, stop
		}
		if why != "" {
			repo.lock()
			undone := o.undoFix(keep, r, at)
			repo.unlock()
			res.tries = append(res.tries, why+undone)
			return res, nil
		}
		fixed = f
	}
}

// failedWhat names what failed on a rebased branch, for a line about it: the check, or the setup
// before it.
func (o *Loop) failedWhat(setup bool) string {
	if setup {
		return "the setup '" + o.cfg.Setup + "', run before the check,"
	}
	return "'" + o.cfg.Check + "'"
}

// whyNotFixCheck says why a failure on r's branch at commit at, as res says, can't be handed back to
// the ticket's worker to fix, fixes being the hand-backs so far, or "" if it can.
func (o *Loop) whyNotFixCheck(ctx context.Context, r rebaseStop, res checked, fixes int, at string) string {
	c := o.cfg
	switch {
	case fixes >= c.CheckHandBacks:
		return fmt.Sprintf("it was handed back to its worker %s already", times(fixes))
	case errors.Is(res.err, errCheckTimedOut):
		return "it did not finish, so there is no failure to show its worker"
	case at == "" || r.onto == "" || r.head == "":
		return "git could not say what the check failed on"
	}
	o.mu.Lock()
	noMore := o.noMore
	o.mu.Unlock()
	if noMore != "" {
		return noMore
	}
	return o.workerAway(ctx, r.id)
}

// times says how many times something was done, as in "twice".
func times(n int) string {
	switch n {
	case 1:
		return "once"
	case 2:
		return "twice"
	}
	return fmt.Sprintf("%d times", n)
}

// fixCheck hands a failure on r's branch at commit at, as res says, back to the ticket's worker to fix,
// the nth hand-back in its merge, waits for it to finish, and then looks at what it left (see
// verifyFixed). It returns what the worker committed, as in "2 commits", or why the hand-back failed,
// or errInterrupted after Ctrl+C. Neither lock is held: the worker may take minutes. The time limit is
// the one for resolving conflicts, from the hand-back.
func (o *Loop) fixCheck(ctx context.Context, r rebaseStop, at string, res checked, n int) (fixed, why string,
	stop *stopReason) {
	c := o.cfg
	agent := o.agentName(r.id)
	limit := orDefault(c.ResolveTimeout, project.DefaultResolveTimeout)
	deadline := time.Now().Add(limit)
	w := o.newWatcher(r.wt, o.setHandedBack(r.id, false, true))
	o.emit(Event{Kind: EvInfo, Ticket: r.id, Text: fmt.Sprintf(
		"  FIXING: %s %s on %s rebased onto %s; handed back to its worker in tab %s to fix (%d of %d, up to %s)",
		o.failedWhat(res.setup), o.checkHow(res.err), r.br, c.Base, r.tab, n, c.CheckHandBacks,
		command.ShortDuration(limit))})
	if !o.deliverPrompt(ctx, agent, o.fixPrompt(context.WithoutCancel(ctx), r, res)) {
		if ctx.Err() != nil {
			return "", "", errInterrupted
		}
		o.setHandedBack(r.id, false, false)
		return "", "its worker did not take the prompt", nil
	}
	why = o.waitResolved(ctx, agent, r.wt, deadline, limit, w.report)
	if ctx.Err() != nil {
		return "", "", errInterrupted
	}
	o.setHandedBack(r.id, false, false)
	if why != "" {
		return "", why, nil
	}
	fixed, why = o.verifyFixed(ctx, r, at)
	return fixed, why, nil
}

// maxLanded is the most commits a check hand-back lists of those that landed on Base.
const maxLanded = 30

// fixPrompt is what the worker is asked to do with a failure on its rebased branch, as res says: what
// failed, what the output says (see noteCheckFailed), and what the rebase brought in from Base.
func (o *Loop) fixPrompt(ctx context.Context, r rebaseStop, res checked) string {
	c := o.cfg
	said := "it printed nothing"
	if f, ok := o.checkSaidOf(r.id); ok && len(f.said) > 0 {
		said = "it ends:"
		if !f.saidEnd {
			said = "these lines say what failed:"
		}
		said += "\n" + strings.Join(f.said, "\n")
	}
	landed := strings.TrimSpace(o.history.OneLineLog(ctx, c.Repo, r.head+".."+r.onto))
	if landed == "" {
		landed = "(git could not list them)"
	} else {
		landed = strings.Join(boundLines(strings.Split(landed, "\n"), maxLanded), "\n")
	}
	return fmt.Sprintf("%s moved on while you worked, and %s was rebased onto it; now %s %s on it. "+
		"Its whole output is in %s; %s\n\n"+
		"What landed on %s since your branch was cut (git log --oneline %s..%s):\n%s\n\n"+
		"Fix %s so that the check passes, keeping the intent of both your change and what landed on %s. "+
		"Run '%s' in the foreground until it passes, then commit the fix on %s with %s in the commit message. "+
		"Don't do anything else: no rebase, no amending or rewriting of commits, no merge, no push, no ticket "+
		"changes, and no uncommitted changes left. Say DONE when the fix is committed.",
		c.Base, r.br, o.failedWhat(res.setup), o.checkHow(res.err), res.output, said,
		c.Base, short(r.head), short(r.onto), landed,
		r.br, c.Base, c.Check, r.br, r.id)
}

// verifyFixed checks what the worker left after a hand-back of a failed check on r's branch at commit
// at, without taking its word for it: no rebase in progress, the worktree clean, and the branch at
// with new commits on top, each naming the ticket. It returns what was committed, as in "2 commits",
// or why it is rejected. The check runs next (see checkRebased).
func (o *Loop) verifyFixed(ctx context.Context, r rebaseStop, at string) (fixed, why string) {
	c := o.cfg
	keep := context.WithoutCancel(ctx) // a read cut short would look like a failed fix
	switch {
	case o.merger.RebaseInProgress(keep, r.wt):
		return "", "its worker left a rebase in progress in " + r.wt
	case o.checkout.DirtyWorktree(keep, r.wt) != "":
		return "", "its worker left uncommitted changes in " + r.wt
	case !o.merger.IsAncestor(keep, c.Repo, at, r.br):
		return "", fmt.Sprintf("its worker rewrote the commits on %s rather than add to them", r.br)
	}
	n := o.merger.CountCommits(keep, c.Repo, at+".."+r.br)
	switch other, err := o.merger.CommitNotNaming(keep, c.Repo, at+".."+r.br, r.id); {
	case n < 0 || err != nil:
		return "", "git could not say what its worker committed" + because(err)
	case n == 0:
		return "", "its worker committed nothing"
	case other != "":
		return "", fmt.Sprintf("its worker's commit %s does not name %s", other, r.id)
	}
	if n == 1 {
		return "1 commit", ""
	}
	return fmt.Sprintf("%d commits", n), ""
}

// undoFix puts r's branch back at at, where its check failed, after the fix its worker was asked for
// is rejected: a rebase it began aborted, and the branch reset when the worktree is clean. It says
// what it did, as words to add to why the fix was rejected. The caller holds repoMu.
func (o *Loop) undoFix(ctx context.Context, r rebaseStop, at string) string {
	c := o.cfg
	if o.merger.RebaseInProgress(ctx, r.wt) {
		if err := o.abortRebase(ctx, r.wt); err != nil {
			return "; its rebase could not be aborted, so " + r.wt + " is left mid-rebase"
		}
	}
	if o.checkout.DirtyWorktree(ctx, r.wt) != "" {
		return "; " + r.wt + " is left as its worker left it"
	}
	now := o.checkout.Head(ctx, c.Repo, r.br)
	if now == at {
		return ""
	}
	out, err := o.merger.ResetBranch(ctx, r.wt, at)
	o.log.Raw(out, err)
	if err != nil {
		return fmt.Sprintf("; %s could not be reset to %s, where the check failed, and is left at %s",
			r.br, short(at), short(now))
	}
	return fmt.Sprintf("; %s was reset to %s, where the check failed (its worker's attempt is %s)",
		r.br, short(at), short(now))
}

// handedBack says what came of the hand-backs of a failure to the ticket's worker, for a line or a
// note: "" when there were none.
func (k checked) handedBack() string {
	if len(k.tries) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "handed back to its worker to fix %s: ", times(len(k.tries)))
	for i, t := range k.tries {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "(%d) %s", i+1, t)
	}
	if k.not != "" {
		b.WriteString("; not handed back again: " + k.not)
	}
	return b.String()
}
