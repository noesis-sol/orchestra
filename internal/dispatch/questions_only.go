package dispatch

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// A run with nothing running and nothing ready ends (READY_EMPTY), unless what keeps its tickets back
// is questions the maintainer could answer there and then: it then has the answer talk open on them
// beside the dashboard (Answers) and waits, taking up each ticket an answer frees as bd lists it ready,
// and carries on as a normal run. Once the talk is over with nothing freed, the run ends, naming the
// questions left open and the tickets they hold. Deferred tickets aren't questions: the talk is told
// how many there are, and under which parent, and releases them only when the maintainer asks.

// Question is an open question for the maintainer in a run's scope: its ID, the tickets waiting on its
// answer, and whether it was put to the maintainer in an answer talk now over (Put), however the run
// was waiting then: a talk the run opens leaves it out, and one with only such questions opens none.
type Question struct {
	ID    string
	Holds []string
	Put   bool
}

// Answers is how a run puts its open questions to the maintainer once nothing else is ready (see
// Loop.SetAnswers).
type Answers interface {
	// Questions lists the open questions in the run's scope: those under it, or blocking a ticket in
	// it, each marked Put once a talk that put it to the maintainer is over.
	Questions(ctx context.Context) ([]Question, error)
	// Talk has the answer talk open on qs, told of the deferred tickets (deferred: how many, and where;
	// "" for none), and returns a channel closed once that talk is over; nil where no talk can open
	// (plain output, no pane beside orchestra's, an overnight run), and the run ends.
	Talk(qs []Question, deferred string) <-chan struct{}
}

// SetAnswers gives the run its way of putting its open questions to the maintainer once nothing else
// is ready; without one, the run ends then.
func (o *Loop) SetAnswers(a Answers) {
	o.answersMu.Lock()
	defer o.answersMu.Unlock()
	o.answers = a
}

func (o *Loop) answerer() Answers {
	o.answersMu.Lock()
	defer o.answersMu.Unlock()
	return o.answers
}

// answerPoll is how often a run waiting on its questions reads bd for the tickets their answers free:
// the talk records each answer as it is given, and the ticket can start before the talk is over.
const answerPoll = 5 * time.Second

// awaitAnswers is Run's wait once nothing runs and nothing is ready, the last of idleWaits: with open
// questions in the run's scope that no talk now over has put to the maintainer (Question.Put), it has
// the answer talk open on them and waits, reading bd every answerPoll, and reports whether it applies,
// and whether Run goes round: it does once a ticket an answer freed is there to take (see freed), as
// when the idle step's exit rule ends the wait (see idleWake). Once the talk is over with none, it looks
// again for questions asked since, and the run ends, naming those still open (left, see questionsEnd).
// Where no talk can open, it says to answer them with orchestra answer.
func (r *runState) awaitAnswers(from gateMark) (applies, goOn bool) {
	o := r.o
	a := o.answerer()
	r.left = nil
	if a == nil || !r.taking() || r.count >= o.cfg.Limit {
		return false, false
	}
	qs, err := a.Questions(r.ctx)
	if err != nil {
		o.log.Raw("", err) // the run ends as it would have
		return false, false
	}
	r.left = qs
	if !slices.ContainsFunc(qs, func(q Question) bool { return !q.Put }) {
		return false, false // none, or each put to the maintainer in a talk now over
	}
	talk := a.Talk(qs, r.deferredLine())
	if talk == nil {
		o.info("  nothing ready but %s: answer with orchestra answer, then run again", questionsLine(qs))
		return false, false
	}
	o.info("  nothing ready but %s: the answer talk is open beside the dashboard, and the run takes up "+
		"each ticket an answer frees", questionsLine(qs))
	poll := time.NewTicker(answerPoll)
	defer poll.Stop()
	for {
		switch why, over := r.idleWake(waits{talk: talk, poll: poll.C}, from); {
		case why == wokeTalk:
			if r.freed() {
				r.left = nil
				return true, true
			}
			return r.awaitAnswers(from) // questions asked since are put to the maintainer in a talk of their own
		case why == wokePoll && r.freed():
			r.left = nil
			return true, true // the talk stays open: the queue running dry again waits on it
		case over:
			return true, true // the talk stays open, as Run takes up what ended the wait
		}
	}
}

// freed reports whether there is a ticket for the run to take: an asked ticket came back (see follow),
// or bd lists one ready that the run would start.
func (r *runState) freed() bool {
	if r.follow() {
		return true
	}
	t, _, err := r.pick(true)
	if err != nil {
		r.o.log.Raw("", err) // read again at the next poll
	}
	return t != nil
}

// deferredLine says how many tickets of the run's scope are deferred, and under which parent, for the
// answer talk to mention: "35 deferred under tutti-kkb", or "38 deferred: 35 under tutti-kkb, 3 with no
// parent"; "" for none, or when bd can't say.
func (r *runState) deferredLine() string {
	o, ctx := r.o, r.ctx
	open, err := o.tickets.Unclosed(ctx)
	if err != nil {
		o.log.Raw("", err)
		return ""
	}
	var in map[string]bool // nil: every ticket is in the run's scope
	if o.cfg.Ticket != "" {
		subs, err := o.tickets.Descendants(ctx, o.cfg.Ticket)
		if err != nil {
			o.log.Raw("", err)
			return ""
		}
		in = scopeIDs(o.cfg.Ticket, subs)
	}
	under := map[string]int{} // by parent, "" for none
	total := 0
	for _, t := range open {
		if t.Status == StatusDeferred && !o.cfg.excludes(t) && (in == nil || in[t.ID]) {
			under[t.Parent]++
			total++
		}
	}
	parents := slices.SortedFunc(maps.Keys(under), func(a, b string) int {
		if c := cmp.Compare(under[b], under[a]); c != 0 {
			return c
		}
		switch { // the keys differ: no parent goes last
		case a == "":
			return 1
		case b == "":
			return -1
		}
		return strings.Compare(a, b)
	})
	where := make([]string, len(parents))
	for i, p := range parents {
		where[i] = fmt.Sprintf("%d under %s", under[p], p)
		if p == "" {
			where[i] = fmt.Sprintf("%d with no parent", under[p])
		}
	}
	switch len(where) {
	case 0:
		return ""
	case 1:
		return strings.Replace(where[0], " ", " deferred ", 1)
	}
	return fmt.Sprintf("%d deferred: %s", total, strings.Join(where, ", "))
}

// questionsLine names the questions qs and the tickets each holds: "the questions Q1 (holding A, B),
// Q2 (holding C)".
func questionsLine(qs []Question) string {
	if len(qs) == 1 {
		return "the question " + questionsHeld(qs)
	}
	return "the questions " + questionsHeld(qs)
}

// questionsHeld names each question and the tickets it holds: "Q1 (holding A, B), Q2 (holding C)".
func questionsHeld(qs []Question) string {
	each := make([]string, len(qs))
	for i, q := range qs {
		each[i] = q.ID
		if len(q.Holds) > 0 {
			each[i] += " (holding " + strings.Join(q.Holds, ", ") + ")"
		}
	}
	return strings.Join(each, ", ")
}

// questionsEnd is what a run's done line says of the questions left open as it ends with nothing ready
// (left): each, and the tickets it holds; "" for none.
func questionsEnd(left []Question) string {
	if len(left) == 0 {
		return ""
	}
	return "; QUESTIONS_OPEN: " + questionsHeld(left) + ": answer with orchestra answer, then run again"
}
