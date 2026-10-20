package dispatch

import (
	"sync"
	"time"
)

// gate is why the run takes no new tickets, under its own lock, so that Run and the merges read it
// alike, and as it is now: the reasons the run stops for (its stops), and its holds (gateKind), as the
// maintainer asking it to wind down. Run sets it; a merge reads why the run takes on no more work
// before handing a failed check back to its worker (see whyNotFixCheck), and a hold lifted by another
// goroutine wakes Run where it waits (see wakeRun, runState.wait).
type gate struct {
	mu    sync.Mutex
	stops stops
	holds [gateKinds]gateHold
	wake  chan struct{} // buffered 1: something lifted that Run waits for (see wakeRun)
}

// gateKind is a hold on the gate besides the run's stops.
type gateKind int

const (
	gateDrain gateKind = iota // the maintainer asked the run to wind down
	gateLimit                 // the run has dispatched Config.Limit tickets
	gateKinds
)

// gateHold is one hold on the gate: on while it holds; why, why the run takes on no more work for it,
// as the merges read it ("" for a hold that keeps only new tickets from starting, which the merges go
// on through); and until, when it ends of itself, for Run to wake then (zero: never, or not known).
type gateHold struct {
	on    bool
	why   string
	until time.Time
}

// newGate is the gate of a run that holds for nothing.
func newGate() gate { return gate{wake: make(chan struct{}, 1)} }

// open reports whether the run takes new tickets, as far as the gate says: nothing has stopped it and
// nothing holds it.
func (g *gate) open() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stops.first != nil {
		return false
	}
	for _, h := range g.holds {
		if h.on {
			return false
		}
	}
	return true
}

// why says why the run takes on no more work, as the merges read it, or "" if it does: it holds for
// a stop, or the maintainer asked it to wind down.
func (g *gate) why() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stops.first != nil {
		return "the run holds (" + g.stops.first.Error() + ")"
	}
	for _, h := range g.holds {
		if h.on && h.why != "" {
			return h.why
		}
	}
	return ""
}

// next is when Run next wakes for the gate, as runState.wait listens: the earliest end of a hold
// that holds (zero for none), and the channel that a hold lifted elsewhere wakes it on. A hold past
// its end wakes Run at once, each time it waits, until it lifts or moves its end: none is left
// unwoken.
func (g *gate) next() (until time.Time, wake <-chan struct{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, h := range g.holds {
		if h.on && !h.until.IsZero() && (until.IsZero() || h.until.Before(until)) {
			until = h.until
		}
	}
	return until, g.wake
}

// held reports whether hold k holds.
func (g *gate) held(k gateKind) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.holds[k].on
}

// set sets hold k to h, holding (h.on) or lifted, and reports whether that changed whether it holds.
// It is Run's, which reads the gate again as it goes round: it wakes nothing.
func (g *gate) set(k gateKind, h gateHold) (changed bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	changed = g.holds[k].on != h.on
	if !h.on {
		h = gateHold{}
	}
	g.holds[k] = h
	return changed
}

// wakeRun wakes Run where it waits (see runState.wait), as something it waits for lifts in another
// goroutine. One wake waiting is enough: Run reads the gate afresh as it goes round.
func (g *gate) wakeRun() {
	select {
	case g.wake <- struct{}{}:
	default: // Run has one to hear already
	}
}

// addStop records s among the run's stops and reports whether it is the first.
func (g *gate) addStop(s *stopReason) (first bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stops.add(s)
}

// firstStop is the first reason the run stops for, which decides its exit code, or nil.
func (g *gate) firstStop() *stopReason {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stops.first
}

// onlyStop is the run's one reason to stop, or nil when it has none, or more than one.
func (g *gate) onlyStop() *stopReason {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.stops.others) > 0 {
		return nil
	}
	return g.stops.first
}

// replaceStop replaces the run's one reason to stop (see onlyStop) with s; nil lifts it.
func (g *gate) replaceStop(s *stopReason) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stops.first = s
}

// stopLine is the line the run ends with, once something has stopped it (see stops.line).
func (g *gate) stopLine() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stops.line()
}

// woke is what ended a wait of Run's (see runState.wait).
type woke int

const (
	wokeDone    woke = iota // the run's context ended: Ctrl+C
	wokeDrain               // a request to wind down or to take tickets again, heard
	wokeVerdict             // a triage verdict, counted toward the hold for the environment
	wokeGate                // a hold lifted elsewhere, or one came to its end (see gate.next)
	wokeResult              // a worker's result, on waits.results
	wokeJoined              // a ticket went to merge, on waits.joined
	wokeFiled               // the scope is filed, as waits.filing says
	wokeTimer               // waits.timer fired
	wokePoll                // waits.poll ticked
	wokeHeld                // waits.held ticked
)

// waits are the channels a wait of Run's listens on besides those every one does (see runState.wait),
// each for what one loop waits for; nil, the zero value, is never ready.
type waits struct {
	results <-chan result
	joined  <-chan struct{}
	filing  <-chan struct{}
	timer   <-chan time.Time
	poll    <-chan time.Time
	held    <-chan time.Time
}

// wait waits for what wakes Run, and says what did, with a worker's result for wokeResult. Every
// wait of Run's listens for the run's context ending, the maintainer's requests to wind down or to take
// tickets again, which it hears, triage's verdicts, which it counts, and the gate (see gate.next),
// besides w's channels, so that no loop of Run's leaves any of them unheard.
func (r *runState) wait(w waits) (woke, result) {
	o := r.o
	until, wake := o.gate.next()
	var end <-chan time.Time
	if !until.IsZero() {
		t := time.NewTimer(time.Until(until))
		defer t.Stop()
		end = t.C
	}
	select {
	case <-r.ctx.Done():
		return wokeDone, result{}
	case req := <-o.drainReqs:
		r.hear(req)
		return wokeDrain, result{}
	case v := <-o.verdicts:
		o.triaged(r.keep, v)
		return wokeVerdict, result{}
	case <-wake:
		return wokeGate, result{}
	case <-end:
		return wokeGate, result{}
	case res := <-w.results:
		return wokeResult, res
	case <-w.joined:
		return wokeJoined, result{}
	case <-w.filing:
		return wokeFiled, result{}
	case <-w.timer:
		return wokeTimer, result{}
	case <-w.poll:
		return wokePoll, result{}
	case <-w.held:
		return wokeHeld, result{}
	}
}
