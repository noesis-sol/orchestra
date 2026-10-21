//go:build unix

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// Once its checks clear an overnight run to start, its pre-flight puts the open questions in the run's
// scope to the maintainer in the answer talk, and says those left open with the tickets they keep
// waiting.

// fakeAnswers is the answer talk on the questions of tickets, its session a fake that answers those in
// answer, closing them, and leaves the rest open. It records each talk's first message, and whether
// the run had started by then: its start recorded, or bd asked what is ready (in calls, nothingTools's
// bd-calls).
type fakeAnswers struct {
	tickets askedTickets
	answer  []string
	calls   string
	talks   []string
	started []bool
}

func (f *fakeAnswers) talk(s *session) answerTalk {
	return answerTalk{claude: claudeSession{repo: s.cfg.Repo}, tracker: f.tickets, out: s.e.stdout, err: s.e.stderr,
		terminal: func(_ context.Context, message string) error {
			f.talks = append(f.talks, message)
			_, err := os.Stat(filepath.Join(s.cfg.Repo, project.RunPath(dispatch.EventsName)))
			calls, _ := os.ReadFile(f.calls) // none yet when bd hasn't been called
			f.started = append(f.started, !errors.Is(err, os.ErrNotExist) || strings.Contains(string(calls), "ready|"))
			for _, id := range f.answer {
				q := f.tickets[id]
				q.Status = dispatch.StatusClosed
				f.tickets[id] = q
			}
			return nil
		}}
}

// talkAnswering is the answer talk on asked()'s questions that answers those named, with nothingTools's
// bd recording its calls in dir.
func talkAnswering(t *testing.T, dir string, ids ...string) *fakeAnswers {
	f := &fakeAnswers{tickets: asked(), answer: ids, calls: filepath.Join(dir, "bd-calls")}
	answerWith(t, f.talk)
	return f
}

// Without a terminal nothing is asked: the run starts, every open question listed with the tickets it
// keeps waiting, in the checklist and the log.
func TestOvernightWithoutATerminalListsTheOpenQuestions(t *testing.T) {
	t.Parallel()
	preflightWith(t, passing().check)
	dir := nothingTools(t, backlog{})
	f := talkAnswering(t, dir)
	repo, stdout, stderr, code := runNothing(t, func(string) {}, "--overnight")
	if code != dispatch.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	if len(f.talks) != 0 {
		t.Errorf("the talk opened without a terminal: %q", f.talks)
	}
	want := "Overnight pre-flight: 2 questions left open\n" +
		"  warning: k-7 (Which encoding?): k-1.2 waits on it. Answer it with 'orchestra answer k-7' (or 'bd human respond k-7')\n" +
		"  warning: k-9 (Which flag name?): k-5 waits on it. Answer it with 'orchestra answer k-9' (or 'bd human respond k-9')\n"
	if out := unstamped(stdout); !strings.Contains(out, want) {
		t.Errorf("stdout lacks:\n%s\nin:\n%s", want, out)
	}
	if lines := read(t, filepath.Join(repo, project.Dir, "orchestra.log")); !strings.Contains(lines,
		"overnight pre-flight: question left open: k-9 (Which flag name?): k-5 waits on it.") {
		t.Errorf("the log lacks the question left open:\n%s", lines)
	}
}

// A run scoped to a ticket (--ticket) lists only the questions in its scope: those blocking the
// ticket or one under it.
func TestOvernightListsOnlyTheQuestionsInTheRunsScope(t *testing.T) {
	t.Parallel()
	preflightWith(t, passing().check)
	talkAnswering(t, nothingTools(t, backlog{}))
	_, stdout, stderr, code := runNothing(t, func(string) {}, "--overnight", "--ticket", "k-1")
	if code != dispatch.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	out := unstamped(stdout)
	if !strings.Contains(out, "Overnight pre-flight: 1 question left open\n  warning: k-7 (Which encoding?)") ||
		strings.Contains(out, "k-9") {
		t.Errorf("stdout doesn't list k-7 alone:\n%s", out)
	}
}

// A refused run puts no question: it doesn't start.
func TestARefusedOvernightRunListsNoQuestions(t *testing.T) {
	t.Parallel()
	preflightWith(t, limitFix().check)
	talkAnswering(t, nothingTools(t, backlog{}))
	_, stdout, _, code := runNothing(t, func(string) {}, "--overnight", "--limit", "10")
	if code != dispatch.ExitSetup || strings.Contains(stdout, "question") {
		t.Errorf("exit %d, stdout:\n%s", code, stdout)
	}
}

// A question left open says the tickets it keeps waiting: none, one, or several, closed ones left out.
func TestAQuestionLeftOpenSaysTheTicketsItKeepsWaiting(t *testing.T) {
	t.Parallel()
	q := func(blocks ...dispatch.Ticket) question {
		return question{Ticket: dispatch.Ticket{ID: "k-9", Title: "Which\nflag?"}, blocks: blocks}
	}
	open := func(id string) dispatch.Ticket { return dispatch.Ticket{ID: id, Status: dispatch.StatusOpen} }
	closed := dispatch.Ticket{ID: "k-4", Status: dispatch.StatusClosed}
	for _, tc := range []struct {
		q    question
		want string
	}{
		{q(), "k-9 (Which flag?): it keeps no ticket waiting. Answer it with 'orchestra answer k-9' (or 'bd human respond k-9')"},
		{q(closed, open("k-5")), "k-9 (Which flag?): k-5 waits on it. Answer it with 'orchestra answer k-9' (or 'bd human respond k-9')"},
		{q(open("k-1"), open("k-2"), open("k-5")),
			"k-9 (Which flag?): k-1, k-2 and k-5 wait on it. Answer it with 'orchestra answer k-9' (or 'bd human respond k-9')"},
	} {
		if got := questionLeft(tc.q); got != tc.want {
			t.Errorf("questionLeft = %q, want %q", got, tc.want)
		}
	}
}
