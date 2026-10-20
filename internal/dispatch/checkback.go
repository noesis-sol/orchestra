package dispatch

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
)

// When the check fails on a finished ticket's branch rebased onto Base, after a clean rebase or once
// its worker has resolved the rebase's conflicts, the failure often comes from its change meeting what
// landed meanwhile: a test another ticket added for the code as it was. The worker that did the ticket,
// idle in its tab with the change in mind, is asked to fix it, given the check's output and what
// landed, as it is asked to resolve conflicts (see handBack): up to Config.CheckHandBacks times in the
// ticket's merge, while the run takes on work. It commits the fix on the branch, each commit naming
// the ticket, and orchestra checks the branch again itself. A failure it can't fix leaves the ticket
// set aside as before. A worker that finds nothing to fix, the failure not coming from the change (a
// test timing out on a loaded machine, say), commits nothing: orchestra then runs the check once more,
// and a second failure sets the ticket aside without another hand-back.

// checked is how the check on a finished ticket's rebased branch came out (see checkRebased).
type checked struct {
	err    error    // nil once the check passes; else what the last setup or check to fail returned
	setup  bool     // that was the setup's, run before the check
	output string   // where its whole output is
	tries  []string // what came of each hand-back of a failure to the ticket's worker, in turn
	not    string   // why the last failure wasn't handed back, when hand-backs are on
	at     string   // the branch's commit the check passed on, once it passes; "" for a branch with none
	moved  bool     // Base moved on while its worker fixed the branch: not checked, to be rebased and checked
}

// fixes is what came of the hand-backs of a failing check to a ticket's worker in its merge (see
// checkRebased).
type fixes struct {
	n int // the hand-backs so far
	// What its worker committed on the last one, as checkRebased's fixed, when Base moved on meanwhile:
	// the check on the branch rebased onto Base says whether it fixed it. tries is what came of those
	// before it.
	pending string
	tries   []string
}

// overtaken records that the fix pending, if any, was not checked, as the rebase onto Base that
// followed it stopped on conflicts: what its worker resolves is checked with it.
func (f *fixes) overtaken(base string) {
	if f.pending != "" {
		f.tries = append(f.tries, fmt.Sprintf("its worker committed %s, but rebasing it onto %s, which moved on "+
			"meanwhile, stopped on conflicts", f.pending, base))
		f.pending = ""
	}
}

// checkRebased sets up and checks the branch of r's ticket, rebased onto r.onto from r.head (or on top
// of it already, at r.head, as r.onTop says), and hands a failure back to its worker to fix while it
// can (see whyNotFixCheck), fx keeping the hand-backs in the ticket's merge; resolved says its
// worker resolved the rebase. A fix its worker commits while Base moves on isn't checked here: it
// returns moved, and fx keeps the fix for the check on the branch rebased onto Base (see
// mergeRun.pass), so that a hand-back costs one check. The caller holds its turn in the merge queue,
// which a hand-back lets go of while the worker works and takes again after, and not repo, which is
// taken to undo a fix that is rejected. It returns errInterrupted after Ctrl+C, or the usage limit's
// stop during a hand-back, the worker's work left as it is; GIT_FAILED's when git can't tell where the
// branch is, the ticket left unmerged.
func (o *Loop) checkRebased(ctx context.Context, repo *lockGuard, turn *mergeTurn, r rebaseStop, fx *fixes,
	resolved bool) (checked, *stopReason) {
	c := o.cfg
	keep := context.WithoutCancel(ctx) // a read cut short would look like a failure
	res := checked{tries: fx.tries}
	// What the worker committed on its last hand-back, until the check says whether it fixed it, or
	// committedNothing when it found nothing to fix.
	fixed := fx.pending
	fx.pending, fx.tries = "", nil
	for {
		if ctx.Err() != nil {
			return res, errInterrupted
		}
		at, err := o.head(keep, nil, r.br)
		if err != nil {
			return res, o.gitFailedMerging(keep, r.worker, "where "+r.br+" is", err)
		}
		o.checkStarts(ctx, r.br, at) // the next in line may be checked ahead meanwhile, on top of it (see train.go)
		// The rebase, or the fix, may have changed the dependencies: set them up again first (see setUp).
		output, err := o.setUp(ctx, r)
		setup := err != nil
		if !setup {
			output, err = o.runCheck(ctx, r)
		}
		o.checkEnds(err == nil)
		if err != nil && ctx.Err() != nil {
			return res, errInterrupted
		}
		switch {
		case fixed == committedNothing && err == nil:
			res.tries = append(res.tries, "its worker committed nothing and the check passes on a second run")
		case fixed == committedNothing:
			res.tries = append(res.tries, fmt.Sprintf("its worker committed nothing, and %s again (output is in %s)",
				o.failedHow(setup, err), output))
		case fixed != "" && err == nil:
			res.tries = append(res.tries, "its worker committed "+fixed+" and the check passes")
		case fixed != "":
			res.tries = append(res.tries, fmt.Sprintf("its worker committed %s, but %s still (output is in %s)",
				fixed, o.failedHow(setup, err), output))
		}
		if err == nil {
			switch {
			case fixed == committedNothing:
				o.info("  '%s' passes on %s on a second run; its worker committed nothing", c.Check, r.br)
			case fixed != "":
				o.info("  '%s' passes on %s as its worker fixed it", c.Check, r.br)
			case resolved:
				o.info("  '%s' passes on %s as its worker resolved it", c.Check, r.br)
			case r.onTop:
				o.info("  '%s' passes on %s", c.Check, r.branchOn(c.Base))
			default:
				o.info("  '%s' passes on the rebased %s", c.Check, r.br)
			}
			o.book.forgetCheck(r.id)
			return checked{tries: res.tries, at: at}, nil
		}
		res.err, res.setup, res.output = err, setup, output
		if c.CheckHandBacks == 0 {
			return res, nil
		}
		if fixed == committedNothing {
			res.not = "its worker found nothing to fix"
			return res, nil
		}
		if res.not = o.whyNotFixCheck(keep, r, res, fx.n, at); res.not != "" {
			return res, nil
		}
		if ctx.Err() != nil {
			return res, errInterrupted // its worker, which could no longer be prompted, isn't handed it
		}
		fx.n++
		// The worker takes minutes: let the other finished tickets merge meanwhile.
		turn.release()
		f, why, stop := o.fixCheck(ctx, r, at, res, fx.n)
		retaken := turn.retake(ctx)
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
		if retaken != nil {
			return res, retaken // the run stopped as the queue was taken again: the fix is left as it is
		}
		fixed = f
		if o.baseMoved(keep, r) {
			o.info("  %s's worker committed %s, but %s moved on meanwhile: rebasing %s onto it before checking it",
				r.id, fixed, c.Base, r.br)
			fx.pending, fx.tries = fixed, res.tries
			return checked{tries: res.tries, moved: true}, nil
		}
	}
}

// baseMoved reports whether Base has moved on from r.onto, the commit r's branch was rebased onto or
// on top of, as when other tickets merged while its worker worked on a hand-back; false when git can't
// say, the check then running on the branch as it is, and the merge rebasing it after if need be.
func (o *Loop) baseMoved(ctx context.Context, r rebaseStop) bool {
	now, err := o.checkout.Head(ctx, o.cfg.Repo, o.cfg.Base)
	o.log.Raw("", err)
	return now != "" && r.onto != "" && now != r.onto
}

// failedWhat names what failed on a rebased branch, for a line about it: the check, or the setup
// before it.
func (o *Loop) failedWhat(setup bool) string {
	if setup {
		return "the setup '" + o.setupNow().cmd + "', run before the check,"
	}
	return "'" + o.cfg.Check + "'"
}

// failedHow says what failed on a rebased branch with err and how, for a line about it, as
// failedWhat and checkHow say: "'go test ./...' fails", say.
func (o *Loop) failedHow(setup bool, err error) string {
	return o.failedWhat(setup) + " " + o.checkHow(err)
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
	if why := o.gate.why(); why != "" {
		return why
	}
	return o.workerAway(ctx, r.worker)
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
// or the reason the run stops meanwhile: errInterrupted after Ctrl+C, or the usage limit's. Neither
// lock is held: the worker may take minutes. The time limit is the one for resolving conflicts, from
// the hand-back (see handBackTo): a fix committed by then is checked, its worker still at it or not.
func (o *Loop) fixCheck(ctx context.Context, r rebaseStop, at string, res checked, n int) (fixed, why string,
	stop *stopReason) {
	c := o.cfg
	line := fmt.Sprintf("  FIXING: %s on %s; handed back to its worker in tab %s to fix "+
		"(%d of %d, up to %s)", o.failedHow(res.setup, res.err), r.branchOn(c.Base), r.tab, n, c.CheckHandBacks,
		command.ShortDuration(o.handBackLimit()))
	prompt := o.fixPrompt(context.WithoutCancel(ctx), r, res)
	why, stop = o.handBackTo(ctx, r, handFixing, line, prompt, at, func(unsettled bool) string {
		var rejected string
		fixed, rejected = o.verifyFixed(ctx, r, at)
		if unsettled && rejected == "" && fixed == committedNothing {
			return "its worker has committed no fix yet" // still at it: not done
		}
		return rejected
	})
	return fixed, why, stop
}

// maxLanded is the most commits a check hand-back lists of those that landed on Base.
const maxLanded = 30

// fixPrompt is what the worker is asked to do with a failure on its rebased branch, as res says: what
// failed, what the output says (see noteCheckFailed), and what the rebase brought in from Base; or on
// its branch on top of Base already, which brought in nothing.
func (o *Loop) fixPrompt(ctx context.Context, r rebaseStop, res checked) string {
	c := o.cfg
	said := "it printed nothing"
	if rec, _ := o.book.get(r.id); rec.failedCheck != nil && len(rec.failedCheck.said) > 0 {
		f := rec.failedCheck
		said = "it ends:"
		if !f.saidEnd {
			said = "these lines say what failed:"
		}
		said += "\n" + strings.Join(f.said, "\n")
	}
	if r.onTop {
		return fmt.Sprintf("Before %s merges into %s, %s on it. Its whole output is in %s; %s\n\n"+
			"Fix %s so that the check passes. "+fixSteps,
			r.br, c.Base, o.failedHow(res.setup, res.err), res.output, said,
			r.br, r.br, r.id, c.Check, r.br)
	}
	landed := strings.TrimSpace(o.history.OneLineLog(ctx, c.Repo, r.head+".."+r.onto))
	if landed == "" {
		landed = "(git could not list them)"
	} else {
		landed = strings.Join(boundLines(strings.Split(landed, "\n"), maxLanded), "\n")
	}
	return fmt.Sprintf("%s moved on while you worked, and %s was rebased onto it; now %s on it. "+
		"Its whole output is in %s; %s\n\n"+
		"What landed on %s since your branch was cut (git log --oneline %s..%s):\n%s\n\n"+
		"Fix %s so that the check passes, keeping the intent of both your change and what landed on %s. "+fixSteps,
		c.Base, r.br, o.failedHow(res.setup, res.err), res.output, said,
		c.Base, short(r.head), short(r.onto), landed,
		r.br, c.Base, r.br, r.id, c.Check, r.br)
}

// fixSteps is how a fix prompt goes on, given the branch, the ticket, the check and the branch: the
// worker runs what failed and the tests of its change, and leaves the whole check to the merge that
// follows.
const fixSteps = "Run what failed (the tests or other checks the output names) and the tests of the code " +
	"you changed in the foreground until they pass, then commit the fix on %s with %s in the commit message. " +
	"Leave the whole check, '%s', to orchestra: it runs it on the branch next. " +
	"Don't do anything else: no rebase, no amending or rewriting of commits, no merge, no push, no ticket " +
	"changes, and no uncommitted changes left. Say DONE when the fix is committed. If what failed passes " +
	"without a change, the failure not coming from %s, commit nothing and say DONE: orchestra runs the check again."

// committedNothing is what verifyFixed returns as committed when the worker found nothing to fix.
const committedNothing = "nothing"

// verifyFixed checks what the worker left after a hand-back of a failed check on r's branch at commit
// at, without taking its word for it: no rebase in progress, the worktree clean and on the branch, and
// the branch at with new commits on top, each naming the ticket, or still at at. It returns what was
// committed, as in "2 commits", committedNothing for the branch still at at, or why it is rejected.
// The check runs next (see checkRebased), on what the worktree has checked out, and the merge then
// brings in the branch: they must be the same.
func (o *Loop) verifyFixed(ctx context.Context, r rebaseStop, at string) (fixed, why string) {
	c := o.cfg
	keep := context.WithoutCancel(ctx) // a read cut short would look like a failed fix
	if why := o.leftClean(keep, r, "a rebase in progress in "+r.wt); why != "" {
		return "", why
	}
	switch added, err := o.merger.IsAncestor(keep, c.Repo, at, r.br); {
	case err != nil:
		return "", gitCantTell("whether its worker added to the commits on "+r.br, err)
	case !added:
		return "", fmt.Sprintf("its worker rewrote the commits on %s rather than add to them", r.br)
	}
	n, err := o.merger.CountCommits(keep, c.Repo, at+".."+r.br)
	other, notErr := o.merger.CommitNotNaming(keep, c.Repo, at+".."+r.br, r.id)
	switch {
	case err != nil || notErr != nil:
		return "", gitCantTell("what its worker committed", errors.Join(err, notErr))
	case n == 0: // with at an ancestor, the branch is at at: the worker found nothing to fix
		return committedNothing, ""
	case other != "":
		return "", fmt.Sprintf("its worker's commit %s does not name %s", other, r.id)
	}
	if n == 1 {
		return "1 commit", ""
	}
	return fmt.Sprintf("%d commits", n), ""
}

// undoFix puts r's branch back at at, where its check failed, after the fix its worker was asked for
// is rejected (see restoreBranch). It says what it did, as words to add to why the fix was rejected.
// The caller holds repoMu.
func (o *Loop) undoFix(ctx context.Context, r rebaseStop, at string) string {
	return o.restoreBranch(ctx, r, at).afterFix()
}

// afterFix says what restoreBranch did after a rejected fix of a failed check, as words to add to why
// it was rejected: to is the commit the check failed on.
func (x restored) afterFix() string {
	switch {
	case x.stuck:
		return "; its rebase could not be aborted, so " + x.wt + " is left mid-rebase"
	case x.unknown != nil:
		return fmt.Sprintf("; git could not say what %s has%s, so it is left as its worker left it", x.wt,
			because(x.unknown))
	case x.dirty:
		return "; " + x.wt + " is left as its worker left it"
	case x.off != "":
		return fmt.Sprintf("; %s is left at %s, as %s has %s", x.br, short(x.now), x.wt, x.off)
	case x.now == x.to:
		return ""
	case x.err != nil:
		return fmt.Sprintf("; %s could not be reset to %s, where the check failed, and is left at %s",
			x.br, short(x.to), short(x.now))
	}
	return fmt.Sprintf("; %s was reset to %s, where the check failed (its worker's attempt is %s)",
		x.br, short(x.to), short(x.now))
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
