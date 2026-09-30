package dispatch

import (
	"fmt"
	"strings"
	"time"
)

// An environment-wide failure (a safety classifier that refuses every command, say) fails each
// worker the same way at once, whichever ticket it has. Setting each ticket aside in turn would
// burn through the queue, so the run holds instead: no new tickets, the running ones finish, and
// the tickets that failed this way are reopened, as they did nothing. Either signal holds it, with
// Config.EnvHoldCount tickets in a row:
//   - their workers settled within Config.EnvHoldWindow of dispatch without claiming the ticket,
//     committing or leaving changes (needs nothing but the loop, and works with triage off);
//   - triage blamed the environment with high confidence.

// failedAtOnce reports whether a worker settled as one failed by its environment does: soon after
// started, its ticket still open, its branch still at head and its worktree clean.
func (o *Loop) failedAtOnce(status string, started time.Time, br, head, wt string) bool {
	c := o.cfg
	return c.EnvHoldCount > 0 && status == "open" && time.Since(started) < c.EnvHoldWindow &&
		o.checkout.Head(c.Repo, br) == head && o.checkout.DirtyWorktree(wt) == ""
}

// settledFast counts a settled worker toward the hold: one that failed at once adds to the tickets
// failing in a row, and any other ends the row. A ticket failing this way once the run holds is
// reopened too.
func (o *Loop) settledFast(id string, fast bool) {
	n := o.cfg.EnvHoldCount
	if n == 0 {
		return
	}
	o.mu.Lock()
	if !fast {
		o.fastFails = nil
		o.mu.Unlock()
		return
	}
	o.fastFails = append(o.fastFails, id)
	ids := append([]string(nil), o.fastFails...)
	held := o.envStop != nil
	o.mu.Unlock()
	switch {
	case held:
		o.reopenFailed(id)
	case len(ids) >= n:
		o.holdForEnvironment(fmt.Sprintf("the last %d tickets (%s) each settled within %s of starting without being claimed or changed",
			len(ids), strings.Join(ids, ", "), ShortDuration(o.cfg.EnvHoldWindow)))
	}
}

// triaged counts a triage verdict toward the hold: one blaming the environment with high
// confidence adds to the row, any other ends it.
func (o *Loop) triaged(id, cause, confidence, summary string) {
	n := o.cfg.EnvHoldCount
	if n == 0 {
		return
	}
	o.mu.Lock()
	if cause != "environment" || confidence != "high" {
		o.envVerdicts = nil
		o.mu.Unlock()
		return
	}
	o.envVerdicts = append(o.envVerdicts, id)
	ids := append([]string(nil), o.envVerdicts...)
	o.mu.Unlock()
	if len(ids) >= n {
		o.holdForEnvironment(fmt.Sprintf("triage blamed the environment for the last %d tickets (%s) with high confidence (%s)",
			len(ids), strings.Join(ids, ", "), strings.TrimSuffix(strings.TrimSpace(summary), ".")))
	}
}

// holdForEnvironment holds the run, once, and reopens the tickets in the current row of workers
// that failed at once. Run picks the reason up before it starts another ticket.
func (o *Loop) holdForEnvironment(why string) {
	o.mu.Lock()
	if o.envStop != nil {
		o.mu.Unlock()
		return
	}
	o.envStop = halt(ExitEnvironment, "ENVIRONMENT: %s; check the machine, then restart", why)
	reopen := append([]string(nil), o.fastFails...)
	o.mu.Unlock()
	for _, id := range reopen {
		o.reopenFailed(id)
	}
	select {
	case o.envWake <- struct{}{}:
	default: // already told, or no Run to tell
	}
}

// environmentStop is the reason the run holds for the environment, or nil.
func (o *Loop) environmentStop() *stopReason {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.envStop
}

// reopenFailed puts a ticket whose worker failed at once back in the queue, keeping its notes: it
// did nothing, and its worktree is clean.
func (o *Loop) reopenFailed(id string) {
	if err := o.notes.Reopen(id); err != nil {
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  REOPEN_FAILED: %s failed at once like the tickets before it, and bd could not reopen it%s; reopen it with: bd update %s --status open",
			id, because(err), id)})
		return
	}
	o.unmarkAside(id)
	o.appendNotes(id, "Orchestra: reopened; its worker failed at once, like the tickets before it, so the run held for the environment rather than for this ticket.")
	o.info("  %s reopened: its worker failed at once, like the tickets before it", id)
}
