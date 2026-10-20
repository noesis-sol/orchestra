package dispatch

import "sync"

// mergeSlots is Run's slot accounting for the tickets whose workers have settled and that have gone
// to merge, until their goroutines return, a hand-back to their workers included: up to Concurrency of
// them hold no slot (see runState.slotsTaken). Run hears of each on joined (buffered 1), as a slot may
// be free.
type mergeSlots struct {
	mu     sync.Mutex
	gone   map[string]bool
	joined chan struct{}
}

func newMergeSlots() mergeSlots { return mergeSlots{joined: make(chan struct{}, 1)} }

// goToMerge says that ticket id's worker has settled and the ticket goes to merge: Run no longer
// counts it among the workers at work.
func (s *mergeSlots) goToMerge(id string) {
	s.mu.Lock()
	if s.gone == nil {
		s.gone = map[string]bool{}
	}
	s.gone[id] = true
	s.mu.Unlock()
	select {
	case s.joined <- struct{}{}:
	default: // Run has yet to hear an earlier one, and counts this one with it
	}
}

// doneMerging says that ticket id's goroutine has returned: merged, set aside or stopped.
func (s *mergeSlots) doneMerging(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.gone, id)
}

// goneToMerge is how many of the tickets in flight have gone to merge (see goToMerge).
func (s *mergeSlots) goneToMerge(inflight map[string]bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id := range s.gone {
		if inflight[id] {
			n++
		}
	}
	return n
}
