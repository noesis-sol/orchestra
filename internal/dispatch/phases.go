package dispatch

import (
	"sync"
	"time"
)

// phaseCount is how many phases there are, for the arrays indexed by Phase.
const phaseCount = int(PhaseMerging) + 1

// PhaseTimes is how long a ticket spent in each phase, as the dashboard shows them, from when the run
// first took it up (dispatched it, adopted its worker or took it up to merge or check again) to the
// record that ends its part in the run, and the hand-backs it had meanwhile. Time held for the usage
// limit or the machine counts in the phase it held. A ticket out of the run's hands meanwhile, waiting
// on an answer to its question or for a ticket to merge, counts in no phase until the run takes it up
// again.
type PhaseTimes struct {
	Spent   [phaseCount]time.Duration // by Phase
	Entered [phaseCount]bool          // the phases it entered: Spent is 0 for the others
	// Resolves and Fixes: its conflict hand-backs (PhaseResolving) and check hand-backs (PhaseFixing).
	Resolves, Fixes int
	Carried         bool // carried over from an earlier run (see loadCarried)
}

// phaseClock times a ticket's phases: phase is the one it is in, since when; taken, when the run first
// took it up; paused, that the run has let it go for now, its part not ended (see pause).
type phaseClock struct {
	PhaseTimes
	phase  Phase
	since  time.Time
	taken  time.Time
	paused bool
}

// stop adds the time since cl entered its phase to it, at now, unless cl is paused.
func (cl *phaseClock) stop(now time.Time) {
	if !cl.paused {
		cl.Spent[cl.phase] += now.Sub(cl.since)
	}
}

// phaseClocks are the running tickets' phase clocks, and the tickets carried over from an earlier run,
// under their own lock, which is taken last: nothing else is taken while it is held.
type phaseClocks struct {
	mu      sync.Mutex
	clocks  map[string]*phaseClock
	carried map[string]bool
}

// carry notes that ticket id was carried over from an earlier run: its times say so.
func (c *phaseClocks) carry(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.carried == nil {
		c.carried = map[string]bool{}
	}
	c.carried[id] = true
}

// start starts ticket id's clock at now, working, as the run takes it up. One it has already, its part in
// the run not ended, as when it is taken up again once its question is answered, carries on from what it
// counted, and from when the run first took it up.
func (c *phaseClocks) start(id string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.clocks == nil {
		c.clocks = map[string]*phaseClock{}
	}
	cl := c.clocks[id]
	if cl == nil {
		cl = &phaseClock{taken: now}
		cl.Carried = c.carried[id]
		c.clocks[id] = cl
	} else {
		cl.stop(now)
	}
	cl.phase, cl.since, cl.paused, cl.Entered[PhaseWorking] = PhaseWorking, now, false, true
}

// pause stops ticket id's clock at now, its part in the run not ended: the run has let it go for now, as
// when it waits on an answer to its question or for a ticket to merge. The time until the run takes it up
// again (see start) counts in no phase. Nothing changes when it has no clock.
func (c *phaseClocks) pause(id string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cl := c.clocks[id]; cl != nil {
		cl.stop(now)
		cl.paused = true
	}
}

// drop forgets ticket id's clock, as when the run took it up for nothing: the ticket stays as it was.
func (c *phaseClocks) drop(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.clocks, id)
}

// enter moves ticket id's clock into phase p at now, counting a hand-back as it enters one, and runs it
// again should it be paused; nothing changes when it is in p already, running, or has no clock.
func (c *phaseClocks) enter(id string, p Phase, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cl := c.clocks[id]
	if cl == nil || cl.phase == p && !cl.paused {
		return
	}
	cl.stop(now)
	again := cl.phase == p
	cl.phase, cl.since, cl.paused, cl.Entered[p] = p, now, false, true
	if again {
		return
	}
	switch p {
	case PhaseResolving:
		cl.Resolves++
	case PhaseFixing:
		cl.Fixes++
	}
}

// takenUp returns when the run first took ticket id up, and false when its clock isn't running: the run didn't
// take it up, or its part in the run has ended.
func (c *phaseClocks) takenUp(id string) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cl := c.clocks[id]; cl != nil {
		return cl.taken, true
	}
	return time.Time{}, false
}

// end stops ticket id's clock at now and returns its times, or nil when it has none: the run didn't
// take it up, or its part in the run has ended already.
func (c *phaseClocks) end(id string, now time.Time) *PhaseTimes {
	c.mu.Lock()
	defer c.mu.Unlock()
	cl := c.clocks[id]
	if cl == nil {
		return nil
	}
	delete(c.clocks, id)
	cl.stop(now)
	return &cl.PhaseTimes
}

// endsPart reports whether ev ends its ticket's part in the run: it closed, was deferred, was set
// aside with a warning, or was stopped at its merge, left unmerged (DIRTY_TREE, GIT_FAILED,
// MERGE_FAILED), which only a hold or stop with Blocked says.
func endsPart(ev Event) bool {
	return ev.Ticket != "" && (ev.Kind == EvClosed || ev.Kind == EvDeferred || ev.Kind == EvWarn && ev.Aside ||
		(ev.Kind == EvHold || ev.Kind == EvStop) && ev.Blocked != "")
}
