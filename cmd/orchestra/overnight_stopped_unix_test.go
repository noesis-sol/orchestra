//go:build unix

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// An overnight run stopped at its pre-flight says what the pre-flight changed: the base branch, where
// its fetch fast-forwarded it, and the answers recorded.

// stoppedAfterFetch runs s's pre-flight with the fetch and a check that stops it, as Ctrl+C would, and
// returns what it said.
func stoppedAfterFetch(t *testing.T, s *session) string {
	t.Helper()
	ctx, cancel := context.WithCancel(runCtx(t))
	defer cancel()
	s.stops = &stopWatch{}
	s.e.preflight = []preflightCheck{fetchCheck, func(context.Context, *session) preflightResult {
		cancel()
		return preflightResult{}
	}}
	if code, ok := s.preflight(ctx); ok || code != dispatch.ExitInterrupted {
		t.Errorf("the pre-flight ended with %d, %t; want %d, stopped", code, ok, dispatch.ExitInterrupted)
	}
	return s.e.stderr.(*strings.Builder).String()
}

func TestAStoppedPreflightSaysTheBaseWasFastForwarded(t *testing.T) {
	t.Parallel()
	s, other, git := fetchSession(t)
	pushFrom(git, other)
	got, want := stoppedAfterFetch(t, s), "orchestra: stopped before the run started; its pre-flight "+
		"fast-forwarded main to origin/main (1 commit), nothing else was changed.\n"
	if !strings.HasSuffix(got, want) {
		t.Errorf("the stop says %q, want it to end %q", got, want)
	}
}

func TestAStoppedPreflightWithTheBaseUpToDateChangedNothing(t *testing.T) {
	t.Parallel()
	s, _, _ := fetchSession(t)
	got, want := stoppedAfterFetch(t, s), "orchestra: stopped before the run started; nothing was changed.\n"
	if !strings.HasSuffix(got, want) {
		t.Errorf("the stop says %q, want it to end %q", got, want)
	}
}

func TestStoppedQuestionsSayTheBaseWasFastForwarded(t *testing.T) {
	t.Parallel()
	s, other, git := fetchSession(t)
	pushFrom(git, other)
	fetchCheck(runCtx(t), s)
	if code, ok := s.questionsStopped(2); ok || code != dispatch.ExitInterrupted {
		t.Errorf("the questions ended with %d, %t; want %d, stopped", code, ok, dispatch.ExitInterrupted)
	}
	if got, want := s.e.stderr.(*strings.Builder).String(), "orchestra: stopped before the run started; its "+
		"pre-flight fast-forwarded main to origin/main (1 commit), the answers to 2 questions are recorded, "+
		"nothing else was changed.\n"; got != want {
		t.Errorf("the stop says %q, want %q", got, want)
	}
}
