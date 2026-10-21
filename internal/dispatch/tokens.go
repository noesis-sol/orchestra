package dispatch

import (
	"fmt"
	"slices"
	"sync"
)

// What each ticket's workers used: the record that ends a ticket's part in the run gives the tokens its
// workers' Claude Code sessions used from when the run took it up, by model, as their transcripts say
// (Reporter.Tokens). Its workers' hooks note each session they start, so a ticket whose worker was
// started again, or whose session was cleared, counts every session it had; a hand-back resumes the same
// session, whose transcript is read once, at the record. The run's end record gives the run's totals
// (see Log.End).

// unreadTranscripts are the transcripts that couldn't be read, by what was said of them: each is said
// once in the log.
type unreadTranscripts struct {
	mu   sync.Mutex
	said map[string]bool
}

// ticketTokens returns what ticket id's workers used from when the run took it up, by model; nil when
// the run didn't take it up, its workers don't report through hooks, or their transcripts say nothing.
// A transcript that can't be read is left out, and said once in the log.
func (o *Loop) ticketTokens(id string) Tokens {
	since, ok := o.clocks.takenUp(id)
	if !ok || !o.hooksRecorded() {
		return nil
	}
	sessions := o.ticketSessions(id)
	if len(sessions) == 0 {
		return nil
	}
	t, errs := o.reporter.Tokens(sessions, since)
	for _, err := range errs {
		o.unreadTranscript(id, err)
	}
	return t
}

// ticketSessions returns the sessions ticket id's workers' hooks noted, its earlier workers' and its
// latest's, whose notes are read again while its worktree is there.
func (o *Loop) ticketSessions(id string) []Session {
	t := &o.hookTally
	t.mu.Lock()
	h := t.tickets[id]
	wt := ""
	if h != nil && !h.gone {
		wt = h.wt
	}
	t.mu.Unlock()
	if h == nil { // a worker the run took over rather than started: its hooks were never read
		o.mu.Lock()
		if w, ok := o.placed[id]; ok && w.hooks {
			wt = w.wt
		}
		o.mu.Unlock()
	}
	if wt != "" {
		o.readHooks(id, wt, false)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if h = t.tickets[id]; h == nil {
		return nil
	}
	var sessions []Session
	for _, s := range slices.Concat(h.before.Sessions, h.latest.Sessions) {
		if !slices.Contains(sessions, s) { // a resumed session is noted again
			sessions = append(sessions, s)
		}
	}
	return sessions
}

// unreadTranscript logs, once for what err says, that a transcript of ticket id's workers couldn't be
// read, so the tokens its record gives leave it out.
func (o *Loop) unreadTranscript(id string, err error) {
	u := &o.unread
	u.mu.Lock()
	said := u.said[err.Error()]
	if u.said == nil {
		u.said = map[string]bool{}
	}
	u.said[err.Error()] = true
	u.mu.Unlock()
	if !said {
		o.log.Raw("", fmt.Errorf("the tokens %s's workers used leave out a transcript that can't be read: %w", id, err))
	}
}
