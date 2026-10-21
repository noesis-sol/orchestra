//go:build unix

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
)

// What the fetch of Base before a run's loop did is recorded as the run's start would record it: a
// warning about Base in the event stream, once it opens, with the word its line starts with, and a
// fast-forward done in the stop that follows it, whatever stopped the pre-flight as it went on.

// stopOnFastForward is git that stops the pre-flight, as Ctrl+C would, as Base is fast-forwarded.
type stopOnFastForward struct {
	git.Git
	stop context.CancelFunc
}

func (g stopOnFastForward) FastForward(ctx context.Context, repo, branch string) (string, error) {
	g.stop()
	return g.Git.FastForward(ctx, repo, branch)
}

func TestAPreflightStoppedDuringTheFastForwardSaysTheBaseMoved(t *testing.T) {
	t.Parallel()
	s, other, git := fetchSession(t)
	pushed := pushFrom(git, other)
	ctx, cancel := context.WithCancel(runCtx(t))
	defer cancel()
	s.stops = &stopWatch{}
	s.e.preflight = []preflightCheck{func(ctx context.Context, s *session) preflightResult {
		return fetchCheckWith(ctx, s, stopOnFastForward{stop: cancel})
	}}
	if code, ok := s.preflight(ctx); ok || code != dispatch.ExitInterrupted {
		t.Errorf("the pre-flight ended with %d, %t; want %d, stopped", code, ok, dispatch.ExitInterrupted)
	}
	if got := strings.TrimSpace(git(s.cfg.Repo, "rev-parse", "main")); got != pushed {
		t.Fatalf("main is at %s, want the fast-forward to %s", got, pushed)
	}
	got, want := s.e.stderr.(*strings.Builder).String(), "orchestra: stopped before the run started; its pre-flight "+
		"fast-forwarded main to origin/main (1 commit), nothing else was changed.\n"
	if !strings.HasSuffix(got, want) {
		t.Errorf("the stop says %q, want it to end %q", got, want)
	}
}

// wantBaseWarned fails t unless the event stream in repo holds start, a warn record whose detail is
// BASE_DIVERGED, and end.
func wantBaseWarned(t *testing.T, repo string) {
	t.Helper()
	recs := streamRecords(t, repo)
	if got := kindsOf(recs); got != "start warn end" {
		t.Fatalf("records %q, want start warn end", got)
	}
	if w := recs[1]; w["detail"] != "BASE_DIVERGED" ||
		!strings.Contains(w["text"].(string), "BASE_DIVERGED: main is 1 ahead of and 1 behind origin/main") {
		t.Errorf("warn record %v, want detail BASE_DIVERGED and its text", w)
	}
}

// diverge has main and its upstream each commit what the other hasn't.
func diverge(s *session, other string, git func(dir string, args ...string) string) {
	pushFrom(git, other)
	git(s.cfg.Repo, "commit", "-q", "--allow-empty", "-m", "Add c.md")
}

func TestAnOvernightRunRecordsItsDivergedBase(t *testing.T) {
	t.Parallel()
	s, other, git := fetchSession(t)
	diverge(s, other, git)
	s.stops = &stopWatch{}
	s.e.preflight = []preflightCheck{fetchCheck}
	if code, ok := s.preflight(runCtx(t)); !ok {
		t.Fatalf("the pre-flight ended with %d", code)
	}
	s.log.Begin(s.cfg.Repo, dispatch.RunStart{Started: time.Now(), Repo: s.cfg.Repo, Branch: "main",
		Overnight: true, Concurrency: 1})
	s.log.End(0)
	wantBaseWarned(t, s.cfg.Repo)
}

func TestASpecRunRecordsItsDivergedBase(t *testing.T) {
	t.Parallel()
	s, other, git := fetchSession(t)
	diverge(s, other, git)
	s.fetchForSpec(runCtx(t))
	s.log.Begin(s.cfg.Repo, dispatch.RunStart{Started: time.Now(), Repo: s.cfg.Repo, Branch: "main", Concurrency: 1})
	s.log.End(0)
	wantBaseWarned(t, s.cfg.Repo)
}
