package dispatch

import (
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
)

// The sink's answers to the questions tests ask most, read from the typed events rather than their
// wording: a test of what happened (a ticket deferred, a warning given, the run held) uses these, and
// leaves the prose to containsWords where the words themselves are what it checks.

// sinkEvents are the events a test's sink got (runSink's, recordSink's), with the questions on them.
type sinkEvents struct {
	mu     sync.Mutex
	events []Event
}

// eventsOf returns the events of kind k the sink got, in order.
func (s *sinkEvents) eventsOf(k Kind) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var l []Event
	for _, ev := range s.events {
		if ev.Kind == k {
			l = append(l, ev)
		}
	}
	return l
}

// has reports whether the sink got an event of kind k about ticket id, or about any ticket when id
// is "".
func (s *sinkEvents) has(k Kind, id string) bool {
	for _, ev := range s.eventsOf(k) {
		if id == "" || ev.Ticket == id {
			return true
		}
	}
	return false
}

// ofTicket returns the events about ticket id, in order.
func (s *sinkEvents) ofTicket(id string) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var l []Event
	for _, ev := range s.events {
		if ev.Ticket == id {
			l = append(l, ev)
		}
	}
	return l
}

// detailed reports whether the sink got an event of kind k about ticket id with detail as its Detail:
// an EvClosed's "no change to merge", an EvDeferred's "by the worker", an EvWaits' "A, B", …
func (s *sinkEvents) detailed(k Kind, id, detail string) bool {
	for _, ev := range s.eventsOf(k) {
		if ev.Ticket == id && ev.Detail == detail {
			return true
		}
	}
	return false
}

// setAside reports whether ticket id was set aside in the run: deferred, or left for review by an
// EvWarn that says so (Aside).
func (s *sinkEvents) setAside(id string) bool {
	for _, ev := range s.eventsOf(EvWarn) {
		if ev.Aside && ev.Ticket == id {
			return true
		}
	}
	return s.has(EvDeferred, id)
}

// matching returns the events of kind k tagged tag (see tagOf; any tag or none when tag is "") about
// ticket id (any ticket or none when id is "") whose line has words in it (see containsWords), in
// order.
func (s *sinkEvents) matching(k Kind, tag, id, words string) []Event {
	var l []Event
	for _, ev := range s.eventsOf(k) {
		if (tag == "" || tagOf(ev) == tag) && (id == "" || ev.Ticket == id) && containsWords(ev.Text, words) {
			l = append(l, ev)
		}
	}
	return l
}

// warned reports whether the sink got an EvWarn tagged tag (CHECKS_FAILED, MERGE_CONFLICT, …; see
// tagOf) about ticket id, or about any ticket or none when id is "".
func (s *sinkEvents) warned(tag, id string) bool { return s.warnedWith(tag, id, "") }

// warnedWith reports whether the sink got an EvWarn tagged tag about ticket id (any when id is "")
// whose line has words in it (see containsWords): for a test of what a warning says as well.
func (s *sinkEvents) warnedWith(tag, id, words string) bool {
	return len(s.matching(EvWarn, tag, id, words)) > 0
}

// tagged reports whether any event the sink got, of whatever kind, is tagged tag (see tagOf).
func (s *sinkEvents) tagged(tag string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ev := range s.events {
		if tagOf(ev) == tag {
			return true
		}
	}
	return false
}

// blocked returns the kind of the first stop that held or ended the run over ticket id (any ticket or
// none when id is ""): an EvStop's Detail, or the kind an EvHold's line gives after "HOLD: "; "" if
// nothing held the run over it.
func (s *sinkEvents) blocked(id string) stopKind {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ev := range s.events {
		if id != "" && ev.Ticket != id {
			continue
		}
		switch ev.Kind {
		case EvStop:
			return stopKind(ev.Detail)
		case EvHold:
			if k := tagOf(ev); k != "" {
				return stopKind(k)
			}
		}
	}
	return ""
}

// heldFor reports whether the run held, or ended, for a stop of kind k (see blocked), over whatever
// ticket.
func (s *sinkEvents) heldFor(k stopKind) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ev := range s.events {
		if ev.Kind == EvStop && stopKind(ev.Detail) == k || ev.Kind == EvHold && stopKind(tagOf(ev)) == k {
			return true
		}
	}
	return false
}

// stopped is the kind of the stop the last run ended with, its EvStop's Detail; "" when it finished
// (its EvDone) or hasn't ended. A harness's sink keeps the events of all its runs: the last ending is
// the last run's.
func (s *sinkEvents) stopped() stopKind {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ev := range slices.Backward(s.events) {
		switch ev.Kind {
		case EvStop:
			return stopKind(ev.Detail)
		case EvDone:
			return ""
		}
	}
	return ""
}

// tagWord is the tag a line starts with: an upper-case word of two letters or more, its words joined
// by "_", then the line's end or what separates it from the rest (": ", " for ", " >4min: ", …), not
// more of a name ("AB-1", "AB's").
var tagWord = regexp.MustCompile(`^([A-Z][A-Z0-9]+(?:_[A-Z0-9]+)*)(?:$|[^A-Za-z0-9_'-])`)

// tagOf is the tag ev's line starts with, which names what it reports among the events of its kind:
// an EvWarn's CHECKS_FAILED or MERGE_CONFLICT, an EvStop's PAUSED, the stop kind an EvHold's line
// gives after its "HOLD: " (ENVIRONMENT, DIRTY_TREE, …). "" when the line starts with no such word.
func tagOf(ev Event) string {
	line := strings.TrimSpace(ev.Text)
	if ev.Kind == EvHold {
		line = strings.TrimPrefix(line, "HOLD: ")
	}
	if m := tagWord.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

// ansiCode is a terminal escape sequence: a colour, a cursor move.
var ansiCode = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// plainWords is text without its terminal codes, its runs of whitespace (line breaks included) one
// space each.
func plainWords(text string) string {
	return strings.Join(strings.Fields(ansiCode.ReplaceAllString(text, "")), " ")
}

// containsWords reports whether text has words in it, whatever the line breaks, runs of spaces and
// terminal codes in either: for the prose a test must check, which a re-wrapped line doesn't break.
func containsWords(text, words string) bool {
	return strings.Contains(plainWords(text), plainWords(words))
}

func TestTagOfReadsTheLinesLeadingWord(t *testing.T) {
	for _, tc := range []struct {
		ev   Event
		want string
	}{
		{Event{Kind: EvWarn, Text: "  CHECKS_FAILED: A closed, but 'true' fails"}, "CHECKS_FAILED"},
		{Event{Kind: EvWarn, Text: "  TRIAGE_FAILED for A: panic"}, "TRIAGE_FAILED"},
		{Event{Kind: EvInfo, Text: "  A closed"}, ""},
		{Event{Kind: EvInfo, Text: "  AB's worker"}, ""},
		{Event{Kind: EvClosed, Text: "  AB-1 closed"}, ""},
		{Event{Kind: EvHold, Text: "HOLD: BLOCKED >4min: tab tab2 (B) needs attention"}, "BLOCKED"},
		{Event{Kind: EvHold, Text: "HOLD: ENVIRONMENT: triage blamed it; no new tickets"}, "ENVIRONMENT"},
		{Event{Kind: EvHold, Text: "PROBE: the run holds for the environment"}, "PROBE"},
		{Event{Kind: EvStop, Text: "PAUSED: A's worker waits"}, "PAUSED"},
		{Event{Kind: EvStop, Text: "INTERRUPTED"}, "INTERRUPTED"},
		{Event{Kind: EvClosed, Text: "  A closed (abc A: add a.txt)"}, ""},
		{Event{Kind: EvInfo, Text: "  B waits: A"}, ""},
	} {
		if got := tagOf(tc.ev); got != tc.want {
			t.Errorf("tagOf(%q) = %q, want %q", tc.ev.Text, got, tc.want)
		}
	}
}

func TestContainsWordsIgnoresWrappingAndColour(t *testing.T) {
	text := "the prompt reads 'have the verifier\n  subagent (.claude/agents/verifier.md)' and \x1b[31mfails\x1b[0m  here"
	for _, words := range []string{"have the verifier subagent (.claude/agents/verifier.md)", "fails here", "verifier\nsubagent"} {
		if !containsWords(text, words) {
			t.Errorf("containsWords misses %q in %q", words, text)
		}
	}
	if containsWords(text, "verifier.md) fails") {
		t.Error("containsWords matched words that aren't next to each other")
	}
}

func TestSinkQueriesReadTheEvents(t *testing.T) {
	s := &runSink{held: make(chan struct{})}
	for _, ev := range []Event{
		{Kind: EvDeferred, Ticket: "A", Text: "  A deferred by worker"},
		{Kind: EvWarn, Ticket: "B", Aside: true, Text: "  MERGE_CONFLICT: B closed, but wt/B conflicts with main"},
		{Kind: EvHold, Ticket: "C", Text: "HOLD: DIRTY_TREE: the main checkout has uncommitted changes"},
		{Kind: EvStop, Ticket: "D", Detail: string(stopBlocked), Text: "BLOCKED: D"},
	} {
		s.Event(ev)
	}
	if !s.has(EvDeferred, "A") || !s.has(EvDeferred, "") || s.has(EvDeferred, "B") || s.has(EvClosed, "") {
		t.Error("has doesn't follow the events' kinds and tickets")
	}
	if !s.warned("MERGE_CONFLICT", "B") || !s.warned("MERGE_CONFLICT", "") || s.warned("MERGE_CONFLICT", "A") ||
		s.warned("CHECKS_FAILED", "") || !s.warnedWith("MERGE_CONFLICT", "B", "conflicts with\n main") ||
		s.warnedWith("MERGE_CONFLICT", "B", "conflicts with a.go") {
		t.Error("warned doesn't follow the warnings' tags and tickets")
	}
	if got := s.blocked("C"); got != "DIRTY_TREE" {
		t.Errorf("blocked(C) = %q, want DIRTY_TREE", got)
	}
	if got := s.blocked("D"); got != stopBlocked {
		t.Errorf("blocked(D) = %q, want %q", got, stopBlocked)
	}
	if got := s.blocked("A"); got != "" {
		t.Errorf("blocked(A) = %q, want none", got)
	}
	if got := s.ofTicket("B"); len(got) != 1 || got[0].Kind != EvWarn {
		t.Errorf("ofTicket(B) = %v, want its one warning", got)
	}
	if !s.setAside("A") || !s.setAside("B") || s.setAside("C") {
		t.Error("setAside doesn't follow the deferrals and the warnings that set a ticket aside")
	}
	if got := s.stopped(); got != stopBlocked {
		t.Errorf("stopped() = %q, want %q", got, stopBlocked)
	}
	s.Event(Event{Kind: EvDone, Text: "READY_EMPTY after 0 tickets"})
	if got := s.stopped(); got != "" {
		t.Errorf("stopped() = %q after a run that finished, want none", got)
	}
	if !s.heldFor(stopDirtyTree) || !s.heldFor(stopBlocked) || s.heldFor(stopEnvironment) {
		t.Error("heldFor doesn't follow the holds and the stop")
	}
	if !s.detailed(EvStop, "D", string(stopBlocked)) || s.detailed(EvStop, "D", "") || s.detailed(EvStop, "A", string(stopBlocked)) {
		t.Error("detailed doesn't follow the events' details")
	}
	if got := s.matching(EvDeferred, "", "A", "by worker"); len(got) != 1 || len(s.matching(EvWarn, "", "", "")) != 1 {
		t.Errorf("matching(EvDeferred, A, by worker) = %v; want A's deferral", got)
	}
	if !s.tagged("DIRTY_TREE") || s.tagged("RECHECK") {
		t.Error("tagged doesn't follow the events' tags")
	}
}
