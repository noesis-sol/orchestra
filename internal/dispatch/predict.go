package dispatch

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// Predicting footprints. A ready ticket naming nothing (no files, functions, area labels or files
// metadata) runs beside anything. The predictor organ guesses the files it will change, in the
// background and one ticket at a time, like triage; the guess is cached on the ticket as
// PredictedKey metadata, a JSON list, and TicketFootprint reads it back. Picking never waits for
// it: until a prediction is in, the ticket keeps its empty footprint.

// startPredicting starts the predictor for this run, when the organs and footprints are on, and
// returns what stops it: that cancels a prediction in progress and waits for it to end.
func (o *Loop) startPredicting() (stop func()) {
	if !o.cfg.Predict || !o.footprintOn() {
		return func() {}
	}
	parent := o.organCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	o.predictMu.Lock()
	o.predictOn = true
	o.predictWake = make(chan struct{}, 1)
	o.predictMu.Unlock()
	go func() {
		defer close(done)
		for {
			t, ok := o.nextPrediction(ctx)
			if !ok {
				return
			}
			o.predict(ctx, t)
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// nextPrediction waits for the next queued ticket; false once ctx is cancelled.
func (o *Loop) nextPrediction(ctx context.Context) (Ticket, bool) {
	for {
		o.predictMu.Lock()
		if len(o.predictQ) > 0 {
			t := o.predictQ[0]
			o.predictQ = o.predictQ[1:]
			o.predictMu.Unlock()
			return t, ctx.Err() == nil
		}
		wake := o.predictWake
		o.predictMu.Unlock()
		select {
		case <-wake:
		case <-ctx.Done():
			return Ticket{}, false
		}
	}
}

// queuePredictions queues the ready tickets naming nothing that haven't been queued in this run.
// A solo ticket runs alone anyway, and the skipped ones (running, set aside) are left out.
func (o *Loop) queuePredictions(ready []Ticket, skip map[string]bool) {
	o.predictMu.Lock()
	on := o.predictOn
	o.predictMu.Unlock()
	if !on {
		return
	}
	for _, t := range ready {
		if skip[t.ID] || HasLabel(t, SoloLabel) || !o.footprintOf(t).Empty() {
			continue
		}
		o.predictMu.Lock()
		if !o.predictSeen[t.ID] {
			if o.predictSeen == nil {
				o.predictSeen = map[string]bool{}
			}
			o.predictSeen[t.ID] = true
			o.predictQ = append(o.predictQ, t)
			select {
			case o.predictWake <- struct{}{}:
			default: // already told
			}
		}
		o.predictMu.Unlock()
	}
}

// prediction is the files predicted for the ticket in this run, or nil.
func (o *Loop) prediction(id string) []string {
	o.predictMu.Lock()
	defer o.predictMu.Unlock()
	return o.predicted[id]
}

// predict asks the predictor for ticket t's files, caches them on the ticket and, when it is
// already running with an empty footprint, gives it them. A panic loses that prediction, not the
// run.
func (o *Loop) predict(ctx context.Context, t Ticket) {
	defer func() {
		if p := recover(); p != nil {
			o.info("  %s footprint not predicted: panic: %s (the stack is in %s)",
				t.ID, o.logPanic("predicting "+t.ID+"'s footprint", p), o.cfg.LogPath)
		}
	}()
	tracked := o.checkout.TrackedFiles(ctx, o.cfg.Repo)
	if len(tracked) == 0 {
		return // no files to choose from
	}
	files, err := o.organs().PredictFiles(ctx, organ.Footprint{
		ID: t.ID, Ticket: o.tickets.Describe(ctx, t.ID), Files: tracked,
	})
	switch {
	case ctx.Err() != nil:
		return // the run ended
	case err != nil:
		o.info("  %s footprint not predicted: %s", t.ID, FirstLine(err.Error()))
		return
	case len(files) == 0:
		o.info("  %s footprint not predicted: the ticket gives no clue", t.ID)
		return
	}
	o.predictMu.Lock()
	if o.predicted == nil {
		o.predicted = map[string][]string{}
	}
	o.predicted[t.ID] = files
	o.predictMu.Unlock()
	// As a JSON list, which metadataList reads back whole: a path may hold a comma or a space.
	list, _ := json.Marshal(files) // a list of strings always encodes
	if err := o.notes.SetMetadata(ctx, t.ID, PredictedKey, string(list)); err != nil {
		o.log.Raw("", err)
	}
	o.givePrediction(t.ID, files, tracked)
	o.info("  %s footprint predicted: %s", t.ID, strings.Join(files, ", "))
}

// givePrediction gives a running ticket with an empty footprint its predicted files.
func (o *Loop) givePrediction(id string, files, tracked []string) {
	o.mu.Lock()
	defer o.mu.Unlock() // deferred: a panic must not leave the loop locked
	if r := o.footprints[id]; r != nil && r.fp.Empty() {
		r.fp = predictedFootprint(files, newRepoFiles(tracked, o.cfg.Check))
	}
}
