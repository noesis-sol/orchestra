package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// A run with nothing ready but tickets waiting on questions asks for the answer talk rather than end
// (dispatch.Answers), at its start as when its queue runs dry: runAnswers reads the open questions in
// the run's scope as the talk does (readQuestions), and has the dashboard open the talk on them,
// unfocused, as it opens by itself when a question waits (answer_dashboard.go). Plain output, a run in
// no pane, which the talk opens beside, and an overnight run, whose full check and report shouldn't
// wait for the morning, open none: the run ends as before, pointing at orchestra answer.

// runAnswers is how the loop puts its open questions to the maintainer once nothing else is ready.
type runAnswers struct {
	tracker answerTracker
	scope   string
	talks   *answerTalks // nil where no talk can open
}

// Questions lists the open questions in the run's scope, each with the tickets not closed it blocks.
func (a runAnswers) Questions(ctx context.Context) ([]dispatch.Question, error) {
	qs, err := readQuestions(ctx, a.tracker, a.scope, "")
	if err != nil {
		return nil, err
	}
	out := make([]dispatch.Question, len(qs))
	for i, q := range qs {
		out[i].ID = q.ID
		for _, b := range q.blocks {
			if b.Status != dispatch.StatusClosed {
				out[i].Holds = append(out[i].Holds, b.ID)
			}
		}
	}
	return out, nil
}

// Talk has the dashboard open the answer talk on qs, or take them in while open, telling it of the
// deferred tickets; nil where no talk can open.
func (a runAnswers) Talk(qs []dispatch.Question, deferred string) <-chan struct{} {
	if a.talks == nil {
		return nil
	}
	return a.talks.ask(qs, deferred)
}

// talksWhenIdle reports whether the run c, its output plain or not, opens the answer talk when nothing
// is ready but questions: on the dashboard, in a pane, and not overnight.
func talksWhenIdle(c options, plain bool) bool {
	return !plain && c.where.pane != "" && !c.Overnight
}

// talksByItself reports whether the dashboard of the run c opens the answer talk by itself as soon as a
// question waits: with "questions": "auto", and not overnight, where nobody is there to answer it and
// the run's end would wait for its pane to close until the morning. The a key opens it all the same.
func talksByItself(c options) bool {
	return c.answerAuto && !c.Overnight
}

// ask has the dashboard open the talk on qs, unfocused, or queue them into the talk open, the talk it
// opens told of the deferred tickets; the channel it returns is closed once that talk is over: the
// loop's callback, with nothing ready but questions.
func (a *answerTalks) ask(qs []dispatch.Question, deferred string) <-chan struct{} {
	a.deferred.Store(&deferred)
	ids := make([]string, len(qs))
	for i, q := range qs {
		ids[i] = q.ID
	}
	over := make(chan struct{})
	a.send(tui.AskQuestions{IDs: ids, Over: over})
	return over
}

// deferredText is what the answer talk's questions file says of the deferred tickets (deferred: how
// many, and where), which aren't questions: the talk mentions them, and releases them only when the
// maintainer asks; "" for none.
func deferredText(deferred string) string {
	if deferred == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n# Deferred tickets\n\nIn the run's scope, besides the questions: %s. ", deferred)
	b.WriteString("They are not questions, and nothing runs them until they are released. Say so in one " +
		"line as you begin, before the first question, and release none unless the user asks you to: then " +
		"`bd undefer <id>` each one they name (`bd list --status deferred` lists them).\n")
	return b.String()
}
