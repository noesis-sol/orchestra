package dispatch

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// Predicting footprints. A ready ticket naming nothing (no files, functions, area labels or files
// metadata) runs beside anything. The predictor organ guesses the files it will change, in the
// background and one ticket at a time, like triage; the guess is cached on the ticket as
// PredictedKey metadata, a JSON list, and TicketFootprint reads it back. Picking never waits for
// it: until a prediction is in, the ticket keeps its empty footprint.

// predictor is the run's predictions, all under predictMu: whether the predictor runs, its queue,
// the tickets queued in this run and the predictions made. predictWake (buffered 1) says one was
// queued.
type predictor struct {
	predictMu   sync.Mutex
	predictOn   bool
	predictQ    []Ticket
	predictSeen map[string]bool
	predicted   map[string][]string
	predictWake chan struct{}
}

// newPredictor is a predictor that hasn't started, with nothing queued or predicted.
func newPredictor() predictor {
	return predictor{predictSeen: map[string]bool{}, predicted: map[string][]string{}}
}

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
	o.turnOn()
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

// turnOn has p take tickets to predict from now on.
func (p *predictor) turnOn() {
	p.predictMu.Lock()
	defer p.predictMu.Unlock()
	p.predictOn = true
	p.predictWake = make(chan struct{}, 1)
}

// predicting reports whether p takes tickets to predict.
func (p *predictor) predicting() bool {
	p.predictMu.Lock()
	defer p.predictMu.Unlock()
	return p.predictOn
}

// nextPrediction waits for the next queued ticket; false once ctx is cancelled.
func (p *predictor) nextPrediction(ctx context.Context) (Ticket, bool) {
	for {
		p.predictMu.Lock()
		if len(p.predictQ) > 0 {
			t := p.predictQ[0]
			p.predictQ = p.predictQ[1:]
			p.predictMu.Unlock()
			return t, ctx.Err() == nil
		}
		wake := p.predictWake
		p.predictMu.Unlock()
		select {
		case <-wake:
		case <-ctx.Done():
			return Ticket{}, false
		}
	}
}

// queuePrediction queues ticket t, unless it was queued in this run already.
func (p *predictor) queuePrediction(t Ticket) {
	p.predictMu.Lock()
	defer p.predictMu.Unlock()
	if p.predictSeen[t.ID] {
		return
	}
	p.predictSeen[t.ID] = true
	p.predictQ = append(p.predictQ, t)
	select {
	case p.predictWake <- struct{}{}:
	default: // already told
	}
}

// queuePredictions queues the ready tickets naming nothing that haven't been queued in this run.
// A solo ticket runs alone anyway, and the skipped ones (running, set aside) are left out.
func (r *runState) queuePredictions(ready []Ticket, skip map[string]bool) {
	if !r.o.predicting() {
		return
	}
	for _, t := range ready {
		if skip[t.ID] || HasLabel(t, SoloLabel) || !r.footprintOf(t).Empty() {
			continue
		}
		r.o.queuePrediction(t)
	}
}

// prediction is the files predicted for the ticket in this run, or nil.
func (p *predictor) prediction(id string) []string {
	p.predictMu.Lock()
	defer p.predictMu.Unlock()
	return p.predicted[id]
}

// setPrediction keeps files as the prediction for ticket id.
func (p *predictor) setPrediction(id string, files []string) {
	p.predictMu.Lock()
	defer p.predictMu.Unlock()
	p.predicted[id] = files
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
	case LimitRefusal(err) != "":
		o.info("  %s footprint not predicted: skipped, as %s", t.ID, LimitRefusal(err))
		said, _ := organ.UsageLimit(err)
		o.organLimited("the footprint prediction of "+t.ID, said)
		return
	case err != nil:
		o.info("  %s footprint not predicted: %s", t.ID, FirstLine(err.Error()))
		return
	case len(files) == 0:
		o.info("  %s footprint not predicted: the ticket gives no clue", t.ID)
		return
	}
	o.setPrediction(t.ID, files)
	// As a JSON list, which metadataList reads back whole: a path may hold a comma or a space.
	list, _ := json.Marshal(files) // a list of strings always encodes
	if err := o.notes.SetMetadata(ctx, t.ID, PredictedKey, string(list)); err != nil {
		o.log.Raw("", err)
	}
	o.givePrediction(t, files, tracked)
	o.info("  %s footprint predicted: %s", t.ID, strings.Join(files, ", "))
}

// givePrediction gives ticket t, when it is running with an empty footprint, its predicted files.
func (o *Loop) givePrediction(t Ticket, files, tracked []string) {
	o.mu.Lock()
	defer o.mu.Unlock() // deferred: a panic must not leave the loop locked
	if r := o.footprints[t.ID]; r != nil && r.fp.Empty() {
		r.fp = predictedFootprint(files, newRepoFiles(tracked, checkCommands(o.cfg)), specFile(t))
	}
}
