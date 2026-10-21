package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/load"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// An overnight run (--overnight, ORCHESTRA_OVERNIGHT=1) runs the current tickets as --tickets does,
// once its pre-flight has cleared it to work through the night. Each check passes, warns or fails,
// some with a fix orchestra can make: the checklist lists them all; in a terminal each fix is offered,
// then, while a failed check is left, whether to start anyway. A failed check left refuses to start
// the run, exit code 2 as a setup problem's, unless --force-overnight or that answer overrides it.
// Without a terminal nothing is asked: the checklist is printed, and a failed check refuses. Cleared
// to start, the run's open questions are put to the maintainer (overnight_questions.go). The
// pre-flight runs once the run holds its lock, before the run's start is recorded, so that the START
// line and the start record give the run as its fixes left it.

// preflightCheck is one of an overnight run's pre-flight checks, of the run s is about to start.
type preflightCheck func(ctx context.Context, s *session) preflightResult

// preflightResult is what a pre-flight check found.
type preflightResult struct {
	verdict tui.CheckVerdict
	text    string        // what it found, and for a warning or a failure what to do about it by hand
	fix     *preflightFix // what orchestra can do about it, offered in a terminal; nil for nothing
}

// preflightFix is a fix orchestra can make for what a pre-flight check found, before the run starts.
type preflightFix struct {
	question string                 // asks whether to make it: "Run without a limit?"
	done     string                 // says it is made: "the run has no ticket limit"
	apply    func(s *session) error // makes it, in the run s and what it keeps; nil for nothing more to do
}

// preflightChecks are an overnight run's checks, in the order its checklist lists them: the fetch of
// Base's upstream first, as the others read Base, then those that refuse to start (overnight_checks.go)
// and the warnings (overnight_warnings.go) by what they are about, the merge check on Base last, as it
// takes longest.
var preflightChecks = []preflightCheck{
	fetchCheck, limitCheck, concurrencyCheck, permissionCheck, claudeCheck("claude"),
	usageNowCheck("claude"), usageWaitCheck, ticketLimitCheck,
	diskCheck(freeDisk), otherRunsCheck, loadCheck(load.ReadKernel, listNamed), vmCheck(listNamed),
	batteryCheck(runtime.GOOS, onBattery), keepAwakeCheck(runtime.GOOS),
	templatesCheck, readyCheck(beadsTracker), baseCheck,
}

// preflight clears an overnight run to start: it runs its checks, prints their checklist and, in a
// terminal, offers their fixes and makes those taken, then puts the open questions to the maintainer
// (askQuestions). ok is false where the run ends there with code, all said: a failed check left, or
// the questions stopped. A run that isn't overnight passes at once.
func (s *session) preflight(ctx context.Context) (code int, ok bool) {
	cfg, e := &s.cfg, s.e
	if !cfg.Overnight {
		return dispatch.ExitOK, true
	}
	// A stop signal ends the pre-flight as Ctrl+C does; until its fixes are made, nothing has changed but
	// Base, fast-forwarded to its upstream as the run's start would (fetchCheck).
	ctx, stop := s.stops.context(ctx)
	defer stop()
	checks := preflightChecks
	if e.preflight != nil {
		checks = e.preflight
	}
	results := make([]preflightResult, 0, len(checks))
	for _, check := range checks {
		results = append(results, check(ctx, s))
		if ctx.Err() != nil {
			return s.preflightStopped()
		}
	}
	say := s.printer()
	say.Checklist(checklist(results))
	s.logChecklist(results)
	answer := tui.PreflightAnswer{Fix: make([]bool, len(results)), Force: cfg.forceOvernight}
	asks := isTerminal(e.stdin) && !plainOutput(cfg.Plain, e.getenv, e.stdin, e.stdout)
	if asks {
		var err error
		answer, err = tui.AskPreflight(ctx, e.stdin, e.stdout, e.getenv("TERM"), checklist(results), cfg.forceOvernight)
		switch {
		case ctx.Err() != nil || errors.Is(err, tui.ErrCancelled):
			return s.preflightStopped()
		case err != nil:
			fmt.Fprintf(e.stderr, "orchestra couldn't ask about the overnight pre-flight's checks: %v. "+
				"Fix them by hand, or run without a terminal to have them listed.\n", err)
			return dispatch.ExitSetup, false
		}
	}
	failed := 0
	for i, r := range results {
		if r.fix != nil && answer.Fix[i] {
			if err := s.makeFix(r); err == nil {
				continue
			}
		}
		if r.verdict == tui.CheckFailed {
			failed++
		}
	}
	switch {
	case failed == 0:
	case answer.Force:
		how := "as answered"
		if cfg.forceOvernight {
			how = "--force-overnight"
		}
		text := fmt.Sprintf("overnight pre-flight: starting with %s (%s)", tui.ChecksFailed(failed), how)
		say.Warn(text)
		s.log.Line(time.Now(), text)
	default:
		text := fmt.Sprintf("overnight pre-flight: %s", tui.ChecksFailed(failed))
		s.log.Line(time.Now(), text+": the run doesn't start")
		fmt.Fprintf(e.stderr, "orchestra --overnight cannot start: %s. Fix what the checklist says, "+
			"or start anyway with --force-overnight.\n", tui.ChecksFailed(failed))
		return dispatch.ExitSetup, false
	}
	return s.askQuestions(ctx, asks) // overnight_questions.go
}

// makeFix makes the fix of r, as answered: it says what it did or why it couldn't, and returns the
// error.
func (s *session) makeFix(r preflightResult) error {
	var err error
	if r.fix.apply != nil {
		err = r.fix.apply(s)
	}
	say := s.printer()
	if err != nil {
		text := fmt.Sprintf("overnight pre-flight: couldn't fix %s: %v", r.text, err)
		say.Warn(text)
		s.log.Line(time.Now(), text)
		return err
	}
	text := "overnight pre-flight: fixed: " + r.fix.done
	say.Say(text, "Fixed: "+r.fix.done)
	s.log.Line(time.Now(), text)
	return nil
}

// preflightStopped ends a run stopped at its pre-flight, before anything changed.
func (s *session) preflightStopped() (code int, ok bool) {
	fmt.Fprintln(s.e.stderr, "orchestra: stopped before the run started; nothing was changed.")
	return dispatch.ExitInterrupted, false
}

// logChecklist writes the checklist to the log, for the morning.
func (s *session) logChecklist(results []preflightResult) {
	now := time.Now()
	for _, r := range results {
		s.log.Line(now, "overnight pre-flight: "+r.verdict.String()+": "+r.text)
	}
}

// checklist is the checklist of results, as the terminal shows it.
func checklist(results []preflightResult) []tui.PreflightCheck {
	checks := make([]tui.PreflightCheck, len(results))
	for i, r := range results {
		checks[i] = tui.PreflightCheck{Verdict: r.verdict, Text: r.text}
		if r.fix != nil {
			checks[i].Fix = r.fix.question
		}
	}
	return checks
}
