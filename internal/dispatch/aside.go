package dispatch

import (
	"context"
	"slices"
)

// appendNotes adds a note to the ticket, logging a failure: a lost note doesn't stop the run.
func (o *Loop) appendNotes(ctx context.Context, id, note string) {
	if err := o.notes.AppendNotes(ctx, id, note); err != nil {
		o.log.Raw("", err)
	}
}

// asideTexts is what putAside writes as it sets a ticket aside.
type asideTexts struct {
	note     string // added to the ticket's notes first
	reason   string // the reason bd records with the deferral
	detail   string // the dashboard's short why, whether bd defers it or not
	deferred string // the EvDeferred event's text, once bd has deferred it
	// Should bd fail to defer it, the DEFER_FAILED warning reads
	// "  DEFER_FAILED: <failed><bd's error>; kept out of this run, <then>".
	failed, then string
}

// putAside adds a note to ticket t and defers it with a reason, then reports it deferred or, should
// bd fail, warns with DEFER_FAILED; a says what each says. Either way it keeps the ticket out of the
// rest of the run, which matters most when bd fails to defer it: it would still be ready and
// dispatched again at once. It reports whether bd deferred it.
func (o *Loop) putAside(ctx context.Context, t Ticket, a asideTexts) bool {
	id := t.ID
	o.appendNotes(ctx, id, a.note)
	o.markAside(id)
	if err := o.notes.Defer(ctx, id, a.reason); err != nil {
		o.emit(Event{Kind: EvWarn, Ticket: id, Aside: true, Detail: a.detail,
			Text: "  DEFER_FAILED: " + a.failed + because(err) + "; kept out of this run, " + a.then})
		return false
	}
	o.emit(Event{Kind: EvDeferred, Ticket: id, Title: t.Title, Detail: a.detail, Text: a.deferred})
	return true
}

// asideList lists tickets deferred or left unmerged in this run, in order, without repeats.
func (o *Loop) asideList() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.asideIDs...)
}

func (o *Loop) markAside(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if slices.Contains(o.asideIDs, id) {
		return
	}
	o.asideIDs = append(o.asideIDs, id)
}

// unmarkAside takes a ticket back out of those set aside in this run.
func (o *Loop) unmarkAside(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i, x := range o.asideIDs {
		if x == id {
			o.asideIDs = append(o.asideIDs[:i:i], o.asideIDs[i+1:]...)
			return
		}
	}
}
