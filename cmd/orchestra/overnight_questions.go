package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// Once its checks have cleared an overnight run to start, its pre-flight puts every open question in
// the run's scope (--ticket's, else every one) to the maintainer in the answer talk (answer_talk.go),
// one by one with its options, so that none keeps its tickets waiting all night. The run starts once
// the talk is over: a question the maintainer left open (/exit), or every one where nothing is asked
// (no terminal, plain output), is said in a checklist of its own with the tickets it keeps waiting.

// questionsTalk is the answer talk an overnight run's pre-flight opens: newAnswerTalk's, unless the
// test gives its own (cmdEnv.answers).
func (s *session) questionsTalk() answerTalk {
	if s.e.answers != nil {
		return s.e.answers(s)
	}
	return newAnswerTalk(s)
}

// askQuestions puts the open questions in the run's scope to the maintainer in the answer talk, where
// asks has the pre-flight ask, then says those left open, each with the tickets it keeps waiting. ok is
// false where the run ends there with code, all said: stopped during the talk.
func (s *session) askQuestions(ctx context.Context, asks bool) (code int, ok bool) {
	talk, scope, say := s.questionsTalk(), s.cfg.Ticket, s.printer()
	if asks {
		r, err := talk.answer(ctx, scope, "")
		switch {
		case ctx.Err() != nil:
			return s.questionsStopped(len(r.Answered))
		case err != nil:
			text := fmt.Sprintf("overnight pre-flight: the questions couldn't be talked through: %v", err)
			say.Warn(text)
			s.log.Line(time.Now(), text)
		}
		if len(r.Answered) > 0 {
			text := "overnight pre-flight: " + plural(len(r.Answered), "question") + " answered"
			say.Say(text, "Answered "+plural(len(r.Answered), "question"))
			s.log.Line(time.Now(), text)
		}
	}
	left, err := readQuestions(ctx, talk.tracker, scope, "")
	switch {
	case ctx.Err() != nil:
		return s.preflightStopped()
	case err != nil:
		text := fmt.Sprintf("overnight pre-flight: couldn't read the questions left open: %v", err)
		say.Warn(text)
		s.log.Line(time.Now(), text)
		return dispatch.ExitOK, true
	case len(left) == 0:
		return dispatch.ExitOK, true
	}
	lines := make([]string, len(left))
	now := time.Now()
	for i, q := range left {
		lines[i] = questionLeft(q)
		s.log.Line(now, "overnight pre-flight: question left open: "+lines[i])
	}
	say.QuestionsLeft(lines)
	return dispatch.ExitOK, true
}

// questionLeft says the question q, left open, with the tickets it keeps waiting and how to answer it.
func questionLeft(q question) string {
	var waiting []string
	for _, t := range q.blocks {
		if t.Status != dispatch.StatusClosed {
			waiting = append(waiting, t.ID)
		}
	}
	keeps := "it keeps no ticket waiting"
	switch n := len(waiting); {
	case n == 1:
		keeps = waiting[0] + " waits on it"
	case n > 1:
		keeps = strings.Join(waiting[:n-1], ", ") + " and " + waiting[n-1] + " wait on it"
	}
	return fmt.Sprintf("%s (%s): %s. Answer it with 'orchestra answer %s' (or 'bd human respond %s')",
		q.ID, planLine(q.Title), keeps, q.ID, q.ID)
}

// questionsStopped ends a run stopped during its pre-flight's answer talk, the answers given recorded.
func (s *session) questionsStopped(answered int) (code int, ok bool) {
	if answered == 0 {
		return s.preflightStopped()
	}
	return s.stoppedHaving("the answers to " + plural(answered, "question") + " are recorded")
}
