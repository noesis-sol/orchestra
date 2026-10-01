package dispatch

import (
	"fmt"
	"sort"
	"strings"
)

// Drain asks the run to wind down: no new ticket starts, the running ones finish and merge as
// usual, and then the run ends with DRAINED and exit 0. how says who asked, as in "from the
// dashboard". It is safe from any goroutine and never waits on the loop, which logs the request
// when it next looks.
func (o *Loop) Drain(how string) { o.setDrain(true, how) }

// Resume takes back a Drain: the run takes new tickets again.
func (o *Loop) Resume(how string) { o.setDrain(false, how) }

func (o *Loop) setDrain(on bool, how string) {
	o.drainMu.Lock()
	o.drain, o.drainHow = on, how
	o.drainMu.Unlock()
	select {
	case o.drainWake <- struct{}{}:
	default: // already told
	}
}

// draining says whether the run is winding down, and who last asked either way.
func (o *Loop) draining() (bool, string) {
	o.drainMu.Lock()
	defer o.drainMu.Unlock()
	return o.drain, o.drainHow
}

// drainEvent is the line saying the run winds down after the tickets in flight, or, with on
// false, that it takes new tickets again.
func drainEvent(on bool, how string, inflight map[string]bool) Event {
	asked := ""
	if how != "" {
		asked = ", asked " + how
	}
	if !on {
		return Event{Kind: EvResume, Text: "DRAIN cancelled: taking new tickets again" + asked}
	}
	var ids []string
	for id := range inflight {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var text string
	switch len(ids) {
	case 0:
		text = "DRAIN: stopping now, as nothing is running" + asked
	case 1:
		text = fmt.Sprintf("DRAIN: stopping after the running ticket (%s)%s", ids[0], asked)
	default:
		text = fmt.Sprintf("DRAIN: stopping after the %d running tickets (%s)%s", len(ids), strings.Join(ids, ", "), asked)
	}
	return Event{Kind: EvDrain, Text: text}
}
