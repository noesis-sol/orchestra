package dispatch

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/organ"
)

// On a subscription, the account's usage limit (its five-hour session limit, its weekly one) ends a
// worker's turn with an error, and Claude Code refuses every request until the limit resets, whichever
// worker makes it: they share the account. A long night of several workers reaches it. The run holds
// for it (USAGE_LIMIT) rather than ending: no new ticket starts, as its worker would only reach the
// limit too, and each worker that reached it waits, neither told to continue nor settled, nor counted
// toward the hold for the environment (see waitSettled), the time not counting toward its ticket
// limit. A worker handed its finished ticket back (see waitResolved) waits the same way, the time not
// counting toward the hand-back's time limit, unless git shows it had done what it was handed back as
// its turn ended (see handBackTo); one limited as it is handed back is given the hand-back where it
// would be told to carry on. A minute after the limit resets (Claude Code says when, to the
// minute), each worker still limited is told to carry on, and the hold lifts; Claude Code's own
// automatic continue, when on, has carried it on by then. When the reset isn't known, one limited
// worker is told to carry on every limitRetry, to try the limit. A worker seen going on for
// limitCarried after it was limited, or settling once it went on, shows that the limit has lifted: the
// hold lifts, and the workers still limited are told to carry on. A worker that reaches the limit
// again starts another hold. Each hold lasts at most Config.UsageLimitWait: then the workers still
// limited stop the run (USAGE_LIMIT, ExitUsageLimit), left in their tabs for the next run to carry
// over, or, handed back, for the maintainer to finish the hand-back.

// How the run waits out the usage limit.
const (
	// resetGrace is how long after the reset Claude Code gives, to the minute (it drops the seconds),
	// the hold lifts and the workers still limited are told to carry on.
	resetGrace = time.Minute
	// limitRetry is how often one limited worker is told to carry on when the reset isn't known; the
	// least time, too, between two messages to a worker, and between a hold lifting and the next, so
	// that a reset misread doesn't have the run tell workers into the limit poll after poll.
	limitRetry = 15 * time.Minute
	// limitCarried is how long a worker must go on after the limit, not limited again, for the limit to
	// have lifted: a request the limit refuses ends the turn within seconds.
	limitCarried = time.Minute
	// escapeSettle is how long after Escape the message to carry on is given, for the dialog Escape
	// dismisses to close first.
	escapeSettle = time.Second
)

// usageHold is the run's hold for the usage limit, all under mu: the workers' polls (see limitPoll),
// the organs (see refused) and Run (see waitOutLimit) share it. It holds the gate (gateUsage) while it
// holds, with its end at its known reset (see resetDue), when Run, woken by the gate whatever else
// happens, lifts it (see atEnd), as no limited worker's poll may: the hold an organ started, or one
// whose workers went on with room left, or are in long commands, never limited. A hold lifting wakes
// Run (see gate.change), to take tickets again. Its events go to emit, once mu is let go.
type usageHold struct {
	gate *gate
	emit func(Event)
	wait time.Duration // Config.UsageLimitWait

	mu     sync.Mutex
	on     bool      // the run holds for the limit
	since  time.Time // when the hold began
	reset  time.Time // when the limit resets, as the hold read it; zero: not known
	tried  time.Time // when one limited worker was last told to carry on to try the limit, in this hold
	trying string    // that worker's ticket, until its try is over; "" for none
	lifted time.Time // when a hold last lifted; zero: none has
}

// newUsageHold is the hold for the usage limit of a run whose gate is g, which it registers with,
// emitting its events to emit, and giving up after wait (Config.UsageLimitWait).
func newUsageHold(g *gate, emit func(Event), wait time.Duration) *usageHold {
	h := &usageHold{gate: g, emit: emit, wait: wait}
	g.register(gateUsage, h.atEnd)
	return h
}

// holds reports whether the run holds for the usage limit.
func (h *usageHold) holds() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.on
}

// unlock lets go of mu, the gate told first how the hold now stands (see gate.change), with its end
// (see resetDue).
func (h *usageHold) unlock() {
	h.gate.change(gateUsage, gateHold{on: h.on, until: h.resetDue()})
	h.mu.Unlock()
}

// say emits evs in order: those made under mu, emitted once it is let go.
func (h *usageHold) say(evs []Event) {
	for _, ev := range evs {
		h.emit(ev)
	}
}

// due is when the hold next acts: a resetGrace after the reset, when known, then lifting; else
// limitRetry after it began or its last try, then telling one limited worker to carry on. Never sooner
// than limitRetry after a hold last lifted. The caller holds mu.
func (h *usageHold) due() time.Time {
	if !h.reset.IsZero() {
		return later(h.reset.Add(resetGrace), h.lifted.Add(limitRetry))
	}
	return later(h.since, h.tried).Add(limitRetry)
}

// resetDue is the hold's end on the gate: when it lifts at its known reset (due); zero when the run
// doesn't hold, the reset isn't known, or the hold gives up first, after wait. The caller holds mu.
func (h *usageHold) resetDue() time.Time {
	if !h.on || h.reset.IsZero() {
		return time.Time{}
	}
	if due := h.due(); due.Before(h.since.Add(h.wait)) {
		return due
	}
	return time.Time{}
}

// atEnd is Run's turn as the gate wakes it at the hold's end (see resetDue): the hold lifts, if it
// holds still and is due, to take tickets again; the workers still limited are told to carry on at
// their next poll (see turn).
func (h *usageHold) atEnd(now time.Time) {
	var say []Event
	defer func() { h.say(say) }()
	h.mu.Lock()
	defer h.unlock()
	if due := h.resetDue(); !due.IsZero() && !now.Before(due) {
		say = append(say, h.lift(now, fmt.Sprintf("the limit reset at %s; taking tickets again",
			ResetLabel(h.reset, now))))
	}
}

// lift lifts the hold at now, for why, and returns the event that says so. The caller holds mu, and
// lets go of it with unlock, which wakes Run.
func (h *usageHold) lift(now time.Time, why string) Event {
	h.on, h.lifted, h.trying = false, now, ""
	return Event{Kind: EvLimitLift, Text: "USAGE_LIMIT lifted: " + why}
}

// idle is waitOutLimit's turn on the hold at now, with nothing running: once due (see due), and before
// it has lasted wait, it lifts, to take tickets again, the next to start trying the limit when its reset
// isn't known. It reports whether it holds still, and then when it began, its reset, and when Run next
// looks at it: when due, or when it gives up, whichever comes first.
func (h *usageHold) idle(now time.Time) (holds bool, since, reset, next time.Time) {
	var say []Event
	defer func() { h.say(say) }()
	h.mu.Lock()
	defer h.unlock()
	if !h.on {
		return false, time.Time{}, time.Time{}, time.Time{}
	}
	due, giveUp := h.due(), h.since.Add(h.wait)
	if now.Before(giveUp) && !now.Before(due) {
		why := fmt.Sprintf("the limit reset at %s; taking tickets again", ResetLabel(h.reset, now))
		if h.reset.IsZero() {
			why = fmt.Sprintf("%s on, and no limited worker is left to try it; taking tickets again, the next "+
				"to start trying it", command.ShortDuration(limitRetry))
		}
		say = append(say, h.lift(now, why))
		return false, time.Time{}, time.Time{}, time.Time{}
	}
	if giveUp.Before(due) {
		due = giveUp
	}
	return true, h.since, h.reset, due
}

// ResetLabel is when a usage limit resets, as the log and the dashboard say it at now, in the local
// time zone: the time alone ("14:05") on now's day, else with its day ("Tue Oct 20 14:05").
func ResetLabel(reset, now time.Time) string {
	reset, now = reset.Local(), now.Local()
	if ry, rm, rd := reset.Date(); ry == now.Year() && rm == now.Month() && rd == now.Day() {
		return reset.Format("15:04")
	}
	return reset.Format("Mon Jan 2 15:04")
}

// resetsSaid is "it resets at 14:05", or that when isn't known, for a line about the limit.
func resetsSaid(reset, now time.Time) string {
	if reset.IsZero() {
		return "when it resets is not known"
	}
	return "it resets at " + ResetLabel(reset, now)
}

// LimitRefusal is why an organ call failed, err, when the account's usage limit refused it, as a
// line saying the call was skipped puts it: "claude is at the account's usage limit (You've hit your
// session limit · resets 7pm (Europe/Athens))"; "" for any other error. The limit is the run's to
// wait out, not the organ's failure.
func LimitRefusal(err error) string {
	said, limited := organ.UsageLimit(err)
	switch {
	case !limited:
		return ""
	case said == "":
		return "claude is at the account's usage limit"
	}
	return "claude is at the account's usage limit (" + said + ")"
}

// resetWords match the reset time in Claude Code's words of the usage limit ("resets 7pm (Europe/Athens)")
// or of its automatic continue ("continuing automatically at 5:30am"): its day (month, day and year), its
// hour and minutes, am or pm, and its time zone; "15:04", in 24 hours, is read as well.
var resetWords = regexp.MustCompile(`\b(?:resets|continuing automatically at) ` +
	`(?:([A-Z][a-z]{2}) (\d{1,2})(?:, (\d{4}))?(?:,| at) )?(\d{1,2})(?::(\d{2}))?([ap]m)?(?: \(([^()]+)\))?`)

// LimitReset reads the reset time in message, Claude Code's words of the usage limit or of its automatic
// continue, written at at; zero when it has none it can read. A time with no day is the next one at that
// hour after at, or just before it (an hour at most): Claude Code gives the day of one more than 24 hours
// away. A time with no time zone, or one Go doesn't know, is in the local one, the user's.
func LimitReset(message string, at time.Time) time.Time {
	m := resetWords.FindStringSubmatch(message)
	if m == nil || m[5] == "" && m[6] == "" {
		return time.Time{} // "resets 7", no more: not a time
	}
	hour, _ := strconv.Atoi(m[4])
	minute, _ := strconv.Atoi(m[5])
	switch {
	case m[6] != "" && (hour < 1 || hour > 12), m[6] == "" && hour > 23, minute > 59:
		return time.Time{}
	case m[6] == "am" && hour == 12:
		hour = 0
	case m[6] == "pm" && hour != 12:
		hour += 12
	}
	loc := time.Local
	if m[7] != "" {
		if l, err := time.LoadLocation(m[7]); err == nil {
			loc = l
		}
	}
	now := at.In(loc)
	if m[1] == "" {
		t := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
		if t.Before(now.Add(-time.Hour)) {
			t = time.Date(now.Year(), now.Month(), now.Day()+1, hour, minute, 0, 0, loc)
		}
		return t
	}
	month, err := time.Parse("Jan", m[1])
	day, _ := strconv.Atoi(m[2])
	if err != nil || day < 1 || day > 31 {
		return time.Time{}
	}
	year := now.Year()
	if m[3] != "" {
		year, _ = strconv.Atoi(m[3])
	}
	return time.Date(year, month.Month(), day, hour, minute, 0, 0, loc)
}

// refused is the hold's turn on an organ call, what (as "triage of X"), that the account's usage limit
// refused, Claude Code saying said of it (organ.UsageLimit): the workers share the account, so the run holds
// for the limit, as for a worker that reached it, when it doesn't already; the limit resets when said says.
func (h *usageHold) refused(what, said string) {
	var say []Event
	defer func() { h.say(say) }()
	h.mu.Lock()
	defer h.unlock()
	now := time.Now()
	reset := LimitReset(said, now)
	switch {
	case !h.on:
		say = append(say, h.begin("", what+" was refused for the usage limit",
			&limitWait{at: now, reset: reset, message: said}, now))
	case h.reset.IsZero() && !reset.IsZero():
		h.reset = reset
		say = append(say, Event{Kind: EvInfo, Text: fmt.Sprintf("  USAGE_LIMIT: %s, as %s's refusal says",
			resetsSaid(reset, now), what)})
	}
}

// limitWait is what waitSettled keeps of a worker's usage limit from one poll to the next.
type limitWait struct {
	since   time.Time // when it was first seen limited, this time; zero while it isn't
	at      time.Time // when its turn ended on the limit, as its hooks say, else since
	reset   time.Time // when the limit resets, as its hooks say; zero when not known
	message string    // what Claude Code said of the limit, as its hooks say
	waited  time.Duration
	told    time.Time // when it was last told to carry on after the limit; zero: never
	// left: it was limited and hasn't yet been seen going on for limitCarried since; carrying, when it
	// was first seen going on (working, blocked, idle or done) since, zero while it isn't.
	left     bool
	carrying time.Time
	// read: its ticket was read since it was last seen limited (see limitTicket); closed: it was closed then.
	read, closed bool
}

// limited is how long the worker has waited on the limit by now, this time and before: time that
// doesn't count toward its ticket limit.
func (l *limitWait) limited(now time.Time) time.Duration {
	if l.since.IsZero() {
		return l.waited
	}
	return l.waited + now.Sub(l.since)
}

// enter notes a poll at now that finds the worker limited, u being the limit its hooks report (zero:
// none, as from a worker without them).
func (l *limitWait) enter(now time.Time, u UsageLimit) {
	l.left, l.carrying = false, time.Time{}
	if l.since.IsZero() {
		l.since = now
	}
	switch {
	case u.Reached():
		l.at, l.reset, l.message = u.At, u.Reset, u.Message
	case l.at.Before(l.since):
		l.at = l.since
	}
}

// leave notes a poll at now that finds the worker in state st, not limited.
func (l *limitWait) leave(now time.Time, st AgentState) {
	l.read = false
	if !l.since.IsZero() {
		l.waited += now.Sub(l.since)
		l.since, l.left = time.Time{}, true
	}
	switch st {
	case StateWorking, StateBlocked, StateIdle, StateDone:
		if l.left && l.carrying.IsZero() {
			l.carrying = now
		}
	default: // unknown or gone: not seen to go on
		l.carrying = time.Time{}
	}
}

// What a limited worker does at a poll, as the hold says (see usageHold.turn).
type limitAction int

const (
	limitWaits   limitAction = iota // waits on
	limitTells                      // is told to carry on
	limitGivesUp                    // stops the run: the hold has lasted Config.UsageLimitWait
)

// limitPoll is waitSettled's and waitResolved's turn on the usage limit at each poll of the worker w
// that finds it in state st (not gone), its hooks having reported h; lw is what it keeps of it. A
// limited worker joins the hold, starting one if the run has none, and is told to carry on with msg
// when the hold says (told: it took that up; see tellAfterLimit), or stops the run (stop) once the
// hold has gone on for Config.UsageLimitWait (see usageHold.turn). One seen going on after the limit for
// limitCarried lifts the hold (see usageHold.carriedOn).
func (o *Loop) limitPoll(ctx context.Context, w worker, st AgentState, h hooksSeen, lw *limitWait, msg string) (
	stop *stopReason, told bool) {
	now := time.Now()
	if st != StateLimited {
		lw.leave(now, st)
		if !lw.carrying.IsZero() && now.Sub(lw.carrying) >= limitCarried {
			o.usage.carriedOn(w.id, lw.carrying)
			lw.left, lw.carrying = false, time.Time{}
		}
		return nil, false
	}
	// Its hooks report the limit its last turn ended on, whenever that was: one adopted reports it too.
	lw.enter(now, h.use.Limit)
	switch o.usage.turn(w.id, lw, now) {
	case limitTells:
		return nil, o.tellAfterLimit(ctx, w, lw, msg)
	case limitGivesUp:
		return o.limitStop(ctx, w, lw), false
	}
	return nil, false
}

// limitTicket is waitSettled's look at ticket w.id as a poll first finds its worker limited, each time it
// does, st and h being what the poll read: it returns them as the rest of the poll takes them. A ticket the
// worker closed before its turn ended at the limit, as one that finishes within Claude Code's grace allowance
// does, is done with: the worker is idle, as after any turn's end, and settles as usual. One the worker
// deferred as it stopped is brought back in progress, with a note, for it to carry on after the reset,
// however it carries on: told to by the hold, or by Claude Code itself.
func (o *Loop) limitTicket(ctx context.Context, w worker, st AgentState, h hooksSeen, lw *limitWait) (
	AgentState, hooksSeen) {
	if st != StateLimited {
		return st, h
	}
	if !lw.read {
		lw.read, lw.closed = true, false
		t, err := o.tickets.Show(ctx, w.id)
		switch {
		case err != nil: // unread: it waits as limited
		case t.Status == StatusClosed:
			lw.closed = true
		case t.Status == StatusDeferred:
			o.undeferAtLimit(ctx, w.id)
		}
	}
	if !lw.closed {
		return st, h
	}
	h.use.Limit = UsageLimit{}
	return StateIdle, h
}

// undeferAtLimit brings ticket id back in progress, its worker having deferred it as it stopped at the
// usage limit, so that the worker isn't set aside when it settles.
func (o *Loop) undeferAtLimit(ctx context.Context, id string) {
	ctx = context.WithoutCancel(ctx) // once begun, the ticket is brought back whole
	if err := o.notes.Undefer(ctx, id); err != nil {
		o.log.Raw("", fmt.Errorf("cannot bring back %s, which its worker deferred at the usage limit: %w", id, err))
		return
	}
	o.appendNotes(ctx, id, "Orchestra: the worker deferred this ticket as it stopped at the account's usage limit; "+
		"brought back in progress, for it to carry on after the reset.")
	o.info("  %s's worker deferred its ticket as it stopped at the usage limit; brought back in progress, for it "+
		"to carry on after the reset", id)
}

// turn takes a poll at now that finds ticket id's worker limited, as lw has it, and says what the
// worker does. A worker limited since before the last hold lifted, or whose limit has reset already
// (when the hold would lift at once), is told to carry on, once, then again only limitRetry later; any
// other starts a hold, unless the run holds already. Then:
//   - the hold gone on for Config.UsageLimitWait: it stops the run;
//   - before the hold is due (see due): it waits;
//   - the reset known: the hold lifts, and it is told to carry on;
//   - the reset not known: it is told to carry on, to try the limit, unless another try is out, or it
//     was told less than limitRetry ago. The try is over once it reaches the limit again, goes on, or
//     limitRetry has passed.
func (h *usageHold) turn(id string, lw *limitWait, now time.Time) limitAction {
	var say []Event
	defer func() { h.say(say) }() // runs after the deferred unlock below
	h.mu.Lock()
	defer h.unlock()
	switch {
	case h.trying == "":
	case h.trying == id && lw.at.After(h.tried):
		h.trying = ""
		say = append(say, Event{Kind: EvInfo, Ticket: id, Text: fmt.Sprintf(
			"  USAGE_LIMIT: %s's worker reached the usage limit again; the next try is at %s",
			id, ResetLabel(h.tried.Add(limitRetry), now))})
	case now.Sub(h.tried) >= limitRetry: // it never showed how the try went
		h.trying = ""
	}
	if !h.on {
		switch {
		case !h.lifted.IsZero() && !lw.at.After(h.lifted): // limited since before the hold lifted
			if lw.told.Before(h.lifted) || now.Sub(lw.told) >= limitRetry {
				return limitTells
			}
			return limitWaits
		case !lw.reset.IsZero() && !now.Before(later(lw.reset.Add(resetGrace), h.lifted.Add(limitRetry))):
			// Its limit has reset already, as a worker an earlier run left may find: told at once, no hold.
			if lw.told.IsZero() || now.Sub(lw.told) >= limitRetry {
				return limitTells
			}
			return limitWaits
		}
		say = append(say, h.begin(id, id+"'s worker reached the usage limit", lw, now))
	} else if h.reset.IsZero() && !lw.reset.IsZero() {
		h.reset = lw.reset
		say = append(say, Event{Kind: EvInfo, Ticket: id, Text: fmt.Sprintf(
			"  USAGE_LIMIT: %s, as %s's worker's limit says", resetsSaid(lw.reset, now), id)})
	}
	switch {
	case now.Sub(h.since) >= h.wait:
		return limitGivesUp
	case now.Before(h.due()):
		return limitWaits
	case !h.reset.IsZero():
		say = append(say, h.lift(now, fmt.Sprintf(
			"the limit reset at %s; telling the workers still limited to carry on, and taking tickets again",
			ResetLabel(h.reset, now))))
		return limitTells
	case h.trying != "" || !lw.told.IsZero() && now.Sub(lw.told) < limitRetry:
		return limitWaits
	}
	h.trying, h.tried = id, now
	say = append(say, Event{Kind: EvInfo, Ticket: id, Text: fmt.Sprintf(
		"  USAGE_LIMIT: telling %s's worker to carry on, to see whether the limit has lifted", id)})
	return limitTells
}

// begin starts a hold at now, for ticket id's worker, limited as lw has it ("" and the limit an
// organ was refused for), and returns the event that says so, why: what reached the limit. The caller
// holds mu.
func (h *usageHold) begin(id, why string, lw *limitWait, now time.Time) Event {
	h.on, h.since, h.reset = true, now, lw.reset
	h.tried, h.trying = time.Time{}, ""
	text := fmt.Sprintf("USAGE_LIMIT: %s; %s", why, resetsSaid(lw.reset, now))
	switch wait := h.wait; {
	case wait <= 0:
		text += "; the run doesn't wait for it (usage_limit_wait 0)"
	case lw.reset.IsZero():
		text += fmt.Sprintf(". No new tickets start, and one limited worker is told to carry on every %s, "+
			"for up to %s", command.ShortDuration(limitRetry), command.ShortDuration(wait))
	default:
		text += fmt.Sprintf(". No new tickets start until then, for up to %s", command.ShortDuration(wait))
	}
	return Event{Kind: EvLimited, Ticket: id, Detail: lw.message, Reset: lw.reset, Text: text}
}

// carriedOn lifts the hold, if one holds since before ticket id's worker went on after the limit,
// at from: the limit has lifted, or it would have reached it again. Its try is over, if it was one.
func (h *usageHold) carriedOn(id string, from time.Time) {
	var say []Event
	defer func() { h.say(say) }()
	h.mu.Lock()
	defer h.unlock()
	if h.on && !from.Before(h.since) {
		say = append(say, h.lift(time.Now(), fmt.Sprintf("%s's worker went on past the limit; telling the "+
			"workers still limited to carry on, and taking tickets again", id)))
	}
	if h.trying == id {
		h.trying = ""
	}
}

// done is waitSettled's end on the usage limit for ticket id's worker, as lw has it: settled
// (idleAt, no stop) once it went on after the limit, it carried on past it, as a turn the limit refuses
// ends without settling. Either way, its try is over, if it was one.
func (h *usageHold) done(id string, lw *limitWait, idleAt time.Time, stop *stopReason) {
	if stop == nil && !idleAt.IsZero() && !lw.carrying.IsZero() {
		h.carriedOn(id, lw.carrying)
		return
	}
	h.mu.Lock()
	defer h.unlock()
	if h.trying == id {
		h.trying = ""
	}
}

// carryOnAfterLimit is what the limited worker on ticket id is told to carry on with (see
// tellAfterLimit).
func carryOnAfterLimit(id string) string {
	return fmt.Sprintf("Orchestra: the account's usage limit has reset. Carry on with %s where you left off, "+
		"as your instructions say.", id)
}

// tellAfterLimit tells the limited worker w to carry on now that the usage limit has reset, or may
// have, with msg: with its ticket (carryOnAfterLimit), with its hand-back (carryOnHandBack), or with
// the hand-back it waited to be given (see waitResolved). It reports whether it took that up. Escape
// goes first: with Claude Code's automatic continue off, it asks what to do in a dialog that text typed
// into would answer, and Escape dismisses it; with it on, Escape cancels its own continue if it still
// waits, which this message takes the place of. One that has gone on since its status was read, its
// own continue having fired, is not interrupted: it isn't told.
func (o *Loop) tellAfterLimit(ctx context.Context, w worker, lw *limitWait, msg string) bool {
	if st, err := o.agents.Status(ctx, w.agent); err == nil && st != StateLimited {
		return false // gone on by itself: the polls that follow see it
	}
	lw.told = time.Now()
	if err := o.agents.SendKeys(ctx, w.agent, "esc"); err != nil {
		o.log.Raw("", fmt.Errorf("cannot press Escape for %s: %w", w.agent, err))
	}
	if !sleep(ctx, escapeSettle) {
		return false
	}
	if !o.deliverPrompt(ctx, w.agent, msg) {
		if ctx.Err() == nil {
			o.log.Raw("", fmt.Errorf("%s's worker did not take the message to carry on after the usage limit", w.id))
		}
		return false
	}
	o.info("  %s's worker was told to carry on after the usage limit", w.id)
	return true
}

// limitStop is the reason the limited worker w stops the run with once the hold has gone on for
// Config.UsageLimitWait, as lw has its limit: it is left in its tab, noted on its ticket, for the next
// run to carry over; one handed its finished ticket back, with the hand-back left under way for the
// maintainer to finish (see handBackLeft).
func (o *Loop) limitStop(ctx context.Context, w worker, lw *limitWait) *stopReason {
	now := time.Now()
	waited := "and usage_limit_wait is 0"
	if wait := o.cfg.UsageLimitWait; wait > 0 {
		waited = "still waiting after " + command.ShortDuration(wait)
	}
	left := o.handBackLeft(w.id)
	if left != "" {
		left = "; " + left
	}
	o.appendNotes(context.WithoutCancel(ctx), w.id, fmt.Sprintf(
		"Orchestra: the worker in tab %s reached the account's usage limit (%s), %s; the run stopped, "+
			"leaving it there (worktree %s)%s.", w.tab, resetsSaid(lw.reset, now), waited, w.wt, left))
	return halt(ExitUsageLimit, stopUsageLimit, ": %s's worker reached the usage limit (%s), %s; stopping, "+
		"with its tab %s and worktree %s left as they are%s", w.id, resetsSaid(lw.reset, now), waited, w.tab, w.wt,
		left)
}

// waitOutLimit is Run's step once nothing runs while the run holds for the usage limit and would take
// tickets but for it, as when the limited workers have left their tabs. It waits until the hold is due
// (see usageHold.idle), then lifts it, to take tickets again: the next to start tries the limit when its
// reset isn't known. It reports whether the run goes on: not once the hold has lasted
// Config.UsageLimitWait, which stops it (USAGE_LIMIT), nor after Ctrl+C or a request to wind down; and it
// goes on at once when triage holds the run for the environment meanwhile, for Run to take that up.
func (r *runState) waitOutLimit() bool {
	o := r.o
	for {
		now := time.Now()
		on, since, reset, next := o.usage.idle(now)
		switch {
		case !on:
			return true
		case !now.Before(since.Add(o.cfg.UsageLimitWait)):
			r.addStop(halt(ExitUsageLimit, stopUsageLimit, ": the run held for the usage limit for %s (%s), "+
				"with nothing running; stopping", command.ShortDuration(now.Sub(since)), resetsSaid(reset, now)))
			return false
		}
		timer := time.NewTimer(next.Sub(now))
		goOn, done := r.awaitLimit(timer.C)
		timer.Stop()
		if done {
			return goOn
		}
	}
}

// awaitLimit is waitOutLimit's wait until timeout, or until something ends it (done): Ctrl+C or a
// request to wind down, which end the run, or triage holding the run for the environment, after which
// it goes on (goOn) for Run to take that up. A verdict that doesn't, and a request that changes
// nothing, leave it waiting.
func (r *runState) awaitLimit(timeout <-chan time.Time) (goOn, done bool) {
	switch why, _ := r.wait(waits{timer: timeout}); why {
	case wokeDone:
		return false, true
	case wokeDrain:
		return false, r.drained()
	case wokeVerdict:
		return true, r.o.envStop != nil
	default: // timed out, or the gate woke it: the hold is read again
		return false, false
	}
}
