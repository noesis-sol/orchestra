package dispatch

import (
	"context"
	"fmt"
)

// An overnight run (Config.Overnight) has nobody to answer it until the morning, so a stop over one
// ticket that needs the maintainer only for that ticket costs the night's other tickets hours. Such a
// stop is marked where it is made (setsAside): its worker gone from its tab a second time, or idle
// with its ticket in progress (PAUSED), blocked on a dialog or a permission prompt (BLOCKED), in a
// status the terminal can't tell (UNKNOWN), or still going after the ticket limit (TICKET_LIMIT). An
// overnight run sets that ticket aside instead, deferred with a note saying why, its worktree and tab
// left for review, and goes on with the rest; the run's report lists it with the tickets set aside.
// Stops where carrying on would be unsafe, or would only fail again (git failing in the main checkout,
// a tree left dirty, the hold for the environment, a terminal or bd that can't be read), are not
// marked, and stop an overnight run as any other.

// asideOvernight is a ticket's turn on s, the reason the run would stop over the ticket of w's worker:
// in an overnight run, a stop marked setsAside sets the ticket aside instead, and the run goes on (nil).
// Its worker, gone or not, is left in its tab and followed there (see leaveOpen), as one that settled
// with its ticket unfinished is: should it close the ticket, it merges. Any other stop, or one in
// another run, is returned as it is.
func (r *ticketRun) asideOvernight(ctx context.Context, w worker, s *stopReason) *stopReason {
	o, id := r.o, w.id
	if s == nil || s.aside == "" || !o.cfg.Overnight {
		return s
	}
	r.how = settledSlow // it ran for minutes at least: the row of tickets failing at once ends
	left := fmt.Sprintf("worktree %s and tab %s left for review", w.wt, w.tab)
	a := asideTexts{
		note: fmt.Sprintf("Orchestra: %s; deferred for review rather than stopping the overnight run (%s: %s).",
			s.aside, s.kind, left),
		reason: fmt.Sprintf("overnight run: %s; see tab %s and worktree %s", s.aside, w.tab, w.wt),
		detail: "set aside overnight: " + string(s.kind),
		deferred: fmt.Sprintf("  %s: %s -> noted and deferred, the run being an overnight one (%s); %s",
			id, s.aside, s.kind, left),
		failed: fmt.Sprintf("%s: %s, and bd could not defer it", id, s.aside),
		then:   left,
	}
	why := ""
	if o.putAside(r.keep, r.t, a) {
		why = s.aside + ", so the overnight run deferred it"
	}
	r.leaveAside(ctx, w, false, why)
	return nil
}

// askedAside is followAsked's turn on asked ticket t, in progress with its worker w gone from its tab
// and no session to resume, in an overnight run: rather than stop the run for it (askedGone), it is
// set aside as asideOvernight sets a ticket aside, with a note saying so, and no longer followed.
func (o *Loop) askedAside(ctx context.Context, t Ticket, w workerSpot) {
	id := t.ID
	o.book.dropAsked(id)
	why := fmt.Sprintf("its worker is gone from tab %s with the ticket still in_progress, after %s", w.tab, w.after())
	left := "worktree " + w.wt + " left for review"
	o.putAside(context.WithoutCancel(ctx), t, asideTexts{
		note: fmt.Sprintf("Orchestra: %s; deferred for review rather than stopping the overnight run (%s: %s).",
			why, stopPaused, left),
		reason: fmt.Sprintf("overnight run: %s; see worktree %s", why, w.wt),
		detail: "set aside overnight: " + string(stopPaused),
		deferred: fmt.Sprintf("  %s: %s -> noted and deferred, the run being an overnight one (%s); %s",
			id, why, stopPaused, left),
		failed: fmt.Sprintf("%s: %s, and bd could not defer it", id, why),
		then:   left,
	})
}
