package dispatch

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

// A ticket's check can fail on something the ticket didn't change: a flaky test, or a bug that
// another ticket fixes later in the run. So a ticket set aside because its check failed
// (CHECKS_FAILED) is kept in view for the rest of the run, and once Base has moved on (another
// ticket merged), its branch is rebased and checked once more, through the merge queue (see
// merge): it merges if the check passes now, a rebase that stops on conflicts is handed back to its
// worker, and it stays set aside if the check fails again. Each ticket is checked again at most
// once, taking a slot as a running ticket does, and none is while the run winds down or holds, or
// while a solo ticket runs or is next. One whose check failed in code its own commits change isn't
// kept: another ticket's change won't fix that.

// recheck is a ticket set aside for a failed check, to be checked again: its worker, its title, the
// commit of Base its check failed on and its branch's commit then.
type recheck struct {
	w          worker
	title      string
	onto, head string
}

// awaitRecheck keeps w's ticket, just set aside as its check failed on its branch rebased onto
// onto, to be checked once more after Base moves on, unless its check failed in a directory its
// own commits change. It returns what the CHECKS_FAILED line adds about it.
func (o *Loop) awaitRecheck(ctx context.Context, w worker, onto string) string {
	c := o.cfg
	if f, ok := o.checkSaidOf(w.id); ok {
		if own := f.ownFailures(); len(own) > 0 {
			return fmt.Sprintf("; not checked again in this run, as it failed in %s, which %s's own commits change",
				strings.Join(own, ", "), w.id)
		}
	}
	if onto == "" {
		return "" // git couldn't say what it was checked on, so nor when Base moves on from it
	}
	rc := recheck{w: w, title: o.titleOf(w.id), onto: onto, head: o.checkout.Head(ctx, c.Repo, w.br)}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.rechecks = append(o.rechecks, rc)
	return fmt.Sprintf("; checked once more if %s moves on in this run", c.Base)
}

// isRechecked reports whether ticket id has been taken up to be checked again in this run.
func (o *Loop) isRechecked(id string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.rechecked[id]
}

// dueRecheck takes up the first ticket waiting to be checked again whose Base has moved on since
// its check failed, if any.
func (o *Loop) dueRecheck(ctx context.Context) (recheck, bool) {
	c := o.cfg
	o.mu.Lock()
	waiting := slices.Clone(o.rechecks)
	o.mu.Unlock()
	if len(waiting) == 0 {
		return recheck{}, false
	}
	base := o.checkout.Head(ctx, c.Repo, c.Base)
	for i, rc := range waiting {
		// On, not merely elsewhere: on a Base moved back, its branch would fast-forward unchecked.
		if base == rc.onto || !o.merger.IsAncestor(ctx, c.Repo, rc.onto, c.Base) {
			continue
		}
		o.mu.Lock()
		o.rechecks = slices.Delete(o.rechecks, i, i+1) // only Run takes from it; merges only append
		if o.rechecked == nil {
			o.rechecked = map[string]bool{}
		}
		o.rechecked[rc.w.id] = true
		o.mu.Unlock()
		return rc, true
	}
	return recheck{}, false
}

// recheckTickets starts checking again, while slots are free, each ticket set aside for a failed
// check whose Base has moved on since: none once something has stopped the run or the maintainer
// has asked it to wind down, nor while a solo ticket runs or is next.
func (r *runState) recheckTickets() {
	o := r.o
	for r.stops.first == nil && r.ctx.Err() == nil && o.solo == "" && o.soloNext == "" &&
		len(r.inflight) < o.cfg.Concurrency {
		if r.winding() {
			return
		}
		rc, ok := o.dueRecheck(r.ctx)
		if !ok {
			return
		}
		r.launch(rc.w.id, func(ctx context.Context, _ *settling) *stopReason { return o.recheck(ctx, rc) })
	}
}

// recheck checks rc's ticket again, Base having moved on since its check failed, and merges it if
// the check passes now (see merge). It leaves the ticket as it is, saying why, when something has
// touched it since, as when the maintainer has taken it up: the ticket reopened, its worktree
// changed or its branch moved, or its worker at work again. It returns as work does, a panic
// included.
func (o *Loop) recheck(ctx context.Context, rc recheck) (stop *stopReason) {
	c := o.cfg
	id := rc.w.id
	defer func() {
		if p := recover(); p != nil {
			stop = o.panicStop(id, p)
		} else if stop == nil {
			o.clearActive(id)
		}
	}()
	if why := o.whyNotRecheck(ctx, rc); why != "" {
		if ctx.Err() != nil {
			return errInterrupted // a read cut short says nothing of the ticket
		}
		o.info("  %s is not checked again: %s; it stays set aside", id, why)
		return nil
	}
	st := Status{Ticket: id, Title: rc.title, Tab: rc.w.tab, Started: time.Now()}
	o.setActive(st)
	st.Activity = "checking again: " + c.Base + " moved on since its check failed"
	o.status(st)
	defer o.status(Status{Ticket: id, Gone: true})
	o.emit(Event{Kind: EvInfo, Ticket: id, Text: fmt.Sprintf(
		"  RECHECK: %s's check failed on %s at %s, which has moved on since; rebasing %s and checking it once more",
		id, c.Base, short(rc.onto), rc.w.br)})
	if stop = o.merge(ctx, rc.w); stop == nil && o.unmergedWhy(id) == "" {
		o.unmarkAside(id) // merged: no longer set aside, nor for the reviewer
	}
	return stop
}

// whyNotRecheck says why rc's ticket is left as it is rather than checked again, or "".
func (o *Loop) whyNotRecheck(ctx context.Context, rc recheck) string {
	c := o.cfg
	w := rc.w
	switch st, err := o.tickets.Status(ctx, w.id); {
	case err != nil:
		return "bd could not say whether it is still closed" + because(err)
	case st != StatusClosed:
		return "it is " + string(st) + " now"
	}
	switch {
	case o.checkout.DirtyWorktree(ctx, w.wt) != "":
		return w.wt + " has uncommitted changes"
	case o.checkout.Head(ctx, c.Repo, w.br) != rc.head:
		return w.br + " has moved since its check failed"
	}
	switch st, err := o.agents.Status(ctx, o.agentName(w.id)); {
	case err != nil:
		return "its worker's status could not be read" + because(err)
	case st == StateWorking || st == StateBlocked:
		return fmt.Sprintf("its worker is %s in tab %s", st, w.tab)
	}
	return ""
}

var (
	// failedPackage matches a line that says what failed (see failureLines) naming a package that
	// failed: go test's FAIL line, or gotestsum's === FAIL:.
	failedPackage = regexp.MustCompile(`^(?:FAIL|=== FAIL:)\s+(\S+)`)
	// failedFile matches one naming the file of a lint, vet or build error, at its position.
	failedFile = regexp.MustCompile(`^([^\s:]+\.\w+):\d+(?::\d+)?: `)
)

// ownFailures lists the directories the ticket's own commits change (f.dirs) that the lines of its
// failed check say it failed in, naming a package there or a file; none when they name none, or say
// nothing (saidEnd). A package is named by its import path, which ends with its directory.
func (f checkFail) ownFailures() []string {
	if f.saidEnd {
		return nil
	}
	var own []string
	for _, line := range f.said {
		var at string
		if m := failedPackage.FindStringSubmatch(line); m != nil {
			at = path.Clean(m[1])
		} else if m := failedFile.FindStringSubmatch(line); m != nil {
			at = path.Dir(path.Clean(m[1]))
		} else {
			continue
		}
		for _, d := range f.dirs {
			if (at == d || d != "." && strings.HasSuffix(at, "/"+d)) && !slices.Contains(own, d) {
				own = append(own, d)
			}
		}
	}
	return own
}
