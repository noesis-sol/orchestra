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
	lead, list, tail := DrainWords(ids)
	return Event{Kind: EvDrain, Text: "DRAIN: " + lead + list + tail + asked}
}

// DrainWords says that the run winds down after the tickets in ids, in the words the DRAIN line,
// the dashboard and its question share: lead + list + tail reads "stopping after the 2 running
// tickets finish (A, B): no new tickets will start". A display short of room shortens list alone.
func DrainWords(ids []string) (lead, list, tail string) {
	switch len(ids) {
	case 0:
		return "stopping now, as nothing is running", "", ""
	case 1:
		return "stopping after ", ids[0], " finishes: no new tickets will start"
	}
	return fmt.Sprintf("stopping after the %d running tickets finish (", len(ids)), strings.Join(ids, ", "), "): no new tickets will start"
}
