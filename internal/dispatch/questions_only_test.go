package dispatch

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// fakeAnswers puts a run's questions to the maintainer as the dashboard's answer talk would: Questions
// reads them from the fake bd, and Talk records each talk and has the maintainer (maintain) act on it,
// then closes it after closeAfter, its questions then put to the maintainer (Question.Put). With
// noTalk, no talk can open.
type fakeAnswers struct {
	beads      *fakeBeads
	noTalk     bool
	maintain   func(qs []Question) // what the maintainer does in the talk, on its own goroutine
	closeAfter time.Duration

	wg     sync.WaitGroup // the talks open, which the test waits for (see done)
	mu     sync.Mutex
	talks  [][]Question
	told   []string // the deferred line each talk was given
	closed bool     // the last talk is over
	put    map[string]bool
}

func (f *fakeAnswers) Questions(context.Context) ([]Question, error) {
	b := f.beads
	b.mu.Lock()
	defer b.mu.Unlock()
	var qs []Question
	for _, id := range b.order {
		if t := b.tickets[id]; !HasLabel(*t, HumanLabel) || t.Status == StatusClosed {
			continue
		}
		f.mu.Lock()
		q := Question{ID: id, Put: f.put[id]}
		f.mu.Unlock()
		for _, other := range b.order {
			for _, l := range b.links[other] {
				if l.on == id && l.typ == "blocks" {
					q.Holds = append(q.Holds, other)
				}
			}
		}
		qs = append(qs, q)
	}
	return qs, nil
}

func (f *fakeAnswers) Talk(qs []Question, deferred string) <-chan struct{} {
	if f.noTalk {
		return nil
	}
	f.mu.Lock()
	f.talks, f.told, f.closed = append(f.talks, qs), append(f.told, deferred), false
	f.mu.Unlock()
	over := make(chan struct{})
	f.wg.Go(func() {
		if f.maintain != nil {
			f.maintain(qs)
		}
		time.Sleep(f.closeAfter)
		f.mu.Lock()
		f.closed = true
		if f.put == nil {
			f.put = map[string]bool{}
		}
		for _, q := range qs {
			f.put[q.ID] = true
		}
		f.mu.Unlock()
		close(over)
	})
	return over
}

// done waits for the talks still open to be over, as orchestra does once its run ends.
func (f *fakeAnswers) done() { f.wg.Wait() }

// A run whose only open work waits on a question has the answer talk open rather than end; once the
// maintainer answers there, the ticket it held is taken up while the talk is still open, and the run
// carries on as a normal run, ending once nothing is left.
func TestOnlyQuestionsOpenTheAnswerTalkAndTheFreedTicketRuns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "ticket A", 1)
		h.beads.add("Q", "which way?", 1, HumanLabel)
		h.beads.link("A", "Q", "blocks")
		h.worker("A", finishes("a.txt"))
		answers := &fakeAnswers{beads: h.beads, closeAfter: time.Hour, maintain: func([]Question) {
			time.Sleep(time.Minute)
			h.beads.set("Q", StatusClosed) // answered: bd human respond closes it
		}}
		o := h.loop()
		o.SetAnswers(answers)
		code := o.Run(context.Background())
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) {
			t.Errorf("merged %v, want A\n%s", got, h.sink.text())
		}
		answers.mu.Lock()
		closed := answers.closed
		answers.mu.Unlock()
		answers.done()
		if len(answers.talks) != 1 || questionsHeld(answers.talks[0]) != "Q (holding A)" {
			t.Errorf("talks %+v, want one, on Q holding A", answers.talks)
		}
		if closed {
			t.Error("the run waited for the talk to be over before taking A up")
		}
		if !strings.Contains(h.sink.text(), "nothing ready but the question Q (holding A): the answer talk is open") {
			t.Errorf("the run doesn't say it waits on the answer talk\n%s", h.sink.text())
		}
	})
}

// The maintainer closes the talk with the question unanswered: the run ends as it would have, its
// last line naming the question and the ticket it holds, and opens no second talk on it.
func TestAnswerTalkClosedUnansweredEndsNamingTheQuestions(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "ticket A", 1)
		h.beads.add("B", "ticket B", 2)
		h.beads.add("Q", "which way?", 1, HumanLabel)
		h.beads.link("A", "Q", "blocks")
		h.beads.link("B", "Q", "blocks")
		answers := &fakeAnswers{beads: h.beads, closeAfter: 3 * time.Minute}
		o := h.loop()
		o.SetAnswers(answers)
		code := o.Run(context.Background())
		answers.done()
		want := "READY_EMPTY after 0 tickets; QUESTIONS_OPEN: Q (holding A, B): answer with orchestra answer, then run again"
		if code != ExitOK || o.Final() != want {
			t.Fatalf("exit %d, final %q, want %q\n%s", code, o.Final(), want, h.sink.text())
		}
		if n := len(answers.talks); n != 1 {
			t.Errorf("%d talks, want 1: the question was put to the maintainer once", n)
		}
	})
}

// Deferred tickets aren't questions: the talk is told how many there are and under which parent, and
// the run releases none of them.
func TestAnswerTalkToldOfTheDeferredTicketsNotReleased(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("E", "an epic", 1)
		h.beads.tickets["E"].IssueType = "epic"
		h.cfg.ExcludeTypes = []string{"epic"}
		h.beads.add("A", "ticket A", 1)
		h.beads.add("Q", "which way?", 1, HumanLabel)
		h.beads.link("A", "Q", "blocks")
		for _, id := range []string{"D1", "D2"} {
			h.beads.sub(id, "E", "later", 2)
			h.beads.set(id, StatusDeferred)
		}
		h.beads.add("D3", "later still", 3)
		h.beads.set("D3", StatusDeferred)
		answers := &fakeAnswers{beads: h.beads, closeAfter: time.Minute}
		o := h.loop()
		o.SetAnswers(answers)
		code := o.Run(context.Background())
		answers.done()
		if code != ExitOK {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		if want := "3 deferred: 2 under E, 1 with no parent"; len(answers.told) != 1 || answers.told[0] != want {
			t.Errorf("the talk was told %q, want %q", answers.told, want)
		}
		for _, id := range []string{"D1", "D2", "D3"} {
			if st, _ := h.beads.Status(context.Background(), id); st != StatusDeferred {
				t.Errorf("%s is %s, want still deferred", id, st)
			}
		}
	})
}

// Where no talk can open (plain output, no pane, an overnight run), the run ends at once, saying to
// answer with orchestra answer and naming the questions.
func TestNoAnswerTalkEndsPointingAtOrchestraAnswer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "ticket A", 1)
		h.beads.add("Q", "which way?", 1, HumanLabel)
		h.beads.link("A", "Q", "blocks")
		o := h.loop()
		o.SetAnswers(&fakeAnswers{beads: h.beads, noTalk: true})
		start := time.Now()
		code := o.Run(context.Background())
		if code != ExitOK || !strings.HasPrefix(o.Final(), "READY_EMPTY after 0 tickets; QUESTIONS_OPEN: Q (holding A)") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if !strings.Contains(h.sink.text(), "nothing ready but the question Q (holding A): answer with orchestra answer") {
			t.Errorf("the run doesn't point at orchestra answer\n%s", h.sink.text())
		}
		if took := time.Since(start); took >= answerPoll {
			t.Errorf("the run took %s to end, want it to end at once", took)
		}
	})
}

// The deferred line names each parent, most deferred first, and those with no parent last.
func TestDeferredLine(t *testing.T) {
	t.Parallel()
	b := newFakeBeads()
	b.add("E", "epic", 1)
	b.add("F", "epic", 1)
	b.sub("D1", "E", "later", 2)
	b.sub("D2", "F", "later", 2)
	b.sub("D3", "F", "later", 2)
	b.add("D4", "later", 2)
	for _, id := range []string{"D1", "D2", "D3", "D4"} {
		b.set(id, StatusDeferred)
	}
	o := &Loop{deps: deps{tickets: b}, log: &Log{}}
	r := &runState{o: o, ctx: context.Background()}
	if got, want := r.deferredLine(), "4 deferred: 2 under F, 1 under E, 1 with no parent"; got != want {
		t.Errorf("deferredLine = %q, want %q", got, want)
	}
	o.cfg.Ticket = "F"
	if got, want := r.deferredLine(), "2 deferred under F"; got != want {
		t.Errorf("in F's scope, deferredLine = %q, want %q", got, want)
	}
}
