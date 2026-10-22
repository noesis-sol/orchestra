package dispatch

import (
	"slices"
)

// A spec's breakdown files its environment ticket first, and every other ticket waits for that one
// (internal/project's spec-prompt.md, The environment first): the run starts on it while the
// breakdown files the rest (Config.Filing), rather than wait for the whole tree with every slot idle.

// Filing is a scope still being filed as the run starts on it: until Done is closed, only First of
// it starts, and the run waits for Done rather than end once nothing runs. Once Done is closed, the
// rest of the scope joins the run as ready tickets do. Done is closed only once what was filed may
// run; a filing that fails cancels the run's context instead, its cause (an InterruptedError) saying
// why, and leaves Done open.
type Filing struct {
	First string
	Done  <-chan struct{}
}

// filingDone is what Run waits on while the scope is being filed: Config.Filing's Done, or nil (never
// ready) when there is no filing, or it is over.
func (c Config) filingDone() <-chan struct{} {
	if c.Filing == nil {
		return nil
	}
	select {
	case <-c.Filing.Done:
		return nil
	default:
		return c.Filing.Done
	}
}

// filingOnly is ready, as bd lists it, as the run may take it while the scope is being filed: only
// the filing's first ticket. Once the filing is over (filing nil), it is all of ready.
func (r *runState) filingOnly(ready []Ticket) []Ticket {
	if r.filing == nil {
		return ready
	}
	first := r.o.cfg.Filing.First
	return slices.DeleteFunc(slices.Clone(ready), func(t Ticket) bool { return t.ID != first })
}

// filed notes that the scope's filing is over, as Done says once it is closed: from now on all of the
// scope may start. The scope's own ticket, kept open while it was filed (see epicDone), closes now
// should everything filed under it be merged already, as when the filing added nothing to the first
// ticket.
func (r *runState) filed() {
	o := r.o
	r.filing = nil
	o.info("  the rest of %s is filed: its tickets join the run", o.cfg.Ticket)
	for p := o.cfg.Ticket; p != ""; {
		p = o.epicDone(r.ctx, p, o.cfg.Filing.First, r.inflight)
	}
}

// awaitFiling is Run's wait once nothing runs while the scope is still being filed and the run would
// take its tickets but for the usage limit (see idleWaits): it waits for the filing to be over, as the
// idle step's exit rule allows (see idleWake), and reports whether it applies, and whether Run goes
// round: it does once the rest is filed, as when the wait ends otherwise.
func (r *runState) awaitFiling(from gateMark) (applies, goOn bool) {
	o := r.o
	if r.filing == nil || !r.wouldTake() {
		return false, false
	}
	if !r.filingSaid {
		r.filingSaid = true
		o.info("  waiting for the rest of %s to be filed", o.cfg.Ticket)
	}
	for {
		switch why, over := r.idleWake(waits{filing: r.filing}, from); {
		case why == wokeFiled:
			r.filed()
			return true, true
		case over:
			return true, true
		}
	}
}
