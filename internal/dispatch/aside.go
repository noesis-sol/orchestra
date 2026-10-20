package dispatch

import (
	"context"
	"fmt"
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
	o.book.setAside(id)
	if err := o.notes.Defer(ctx, id, a.reason); err != nil {
		o.emit(Event{Kind: EvWarn, Ticket: id, Aside: true, Detail: a.detail,
			Text: "  DEFER_FAILED: " + a.failed + because(err) + "; kept out of this run, " + a.then})
		return false
	}
	o.emit(Event{Kind: EvDeferred, Ticket: id, Title: t.Title, Detail: a.detail, Text: a.deferred})
	return true
}

// asideList lists tickets set aside in this run, in order, without repeats.
func (o *Loop) asideList() []string { return o.book.asideList() }

// reopen puts ticket id, status as the caller found it, back in bd's queue, open, should it not be
// open already, and reports whether it is. Should bd fail, it warns with REOPEN_FAILED, its status
// "set aside" when not known, and what it won't do then, as "so it won't <wont>".
func (o *Loop) reopen(ctx context.Context, id string, status TicketStatus, wont string) bool {
	if status == StatusOpen {
		return true
	}
	err := o.notes.Reopen(ctx, id)
	if err == nil {
		return true
	}
	stays := "set aside"
	if status != "" {
		stays = string(status)
	}
	o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
		"  REOPEN_FAILED: %s stays %s, so it won't %s%s; reopen it with: bd update %s --status open",
		id, stays, wont, because(err), id)})
	return false
}
