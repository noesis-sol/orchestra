package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/project"
)

// The full check is the project's check_full (scripts/check-full.sh): every suite, including those
// too slow or too demanding to run on every merge. It runs once a run has ended by itself
// (READY_EMPTY, LIMIT_REACHED, DRAINED) having merged a ticket, after its last merge and before
// the run report, on Base's head in a worktree of its own, so the main checkout stays as it is. A
// failure files a ticket for the suite it failed in, unless one is open for it already.

// FullCheckLabel marks the ticket filed for a suite the full check failed in.
const FullCheckLabel = "check-full"

// fullCheckLogName is the file in the main checkout's .orchestra/run/ that holds the whole output
// of the last full check that failed.
const fullCheckLogName = "check-full.log"

// How the full check went, as its EvFullCheck's Detail says.
const (
	FullCheckPassed   = "passed"
	FullCheckFailed   = "failed"
	FullCheckTimedOut = "timed out"
	FullCheckSkipped  = "skipped" // stopped with Ctrl+C, or another stop signal
	FullCheckNotRun   = "not run" // no worktree could be made for it
)

// fullCheckEnd is how many of the last lines of a failed full check's output the log, the reviewer
// and the report are given.
const fullCheckEnd = 40

// fullCheck is how the full check went, for the reviewer and the report.
type fullCheck struct {
	outcome string        // FullCheckPassed, …
	at      string        // Base's commit it ran on
	took    time.Duration // how long it ran
	suite   string        // the suite it failed in
	output  string        // where its whole output is
	end     string        // the end of its output
	ticket  string        // the ticket filed for the suite, or the one open for it already
	filed   string        // what became of the ticket, for a line: "filed X", "X is open for it already"
}

// countMerge counts a ticket merged into Base in this run: the full check runs only after a run
// that merged one.
func (o *Loop) countMerge() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.mergedN++
}

// FullCheckDue reports whether the full check runs after a run that ended with code: when the
// project has one (check_full), and the run ended by itself, not stopped or held, having merged a
// ticket.
func (o *Loop) FullCheckDue(code int) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.cfg.CheckFull != "" && code == ExitOK && o.mergedN > 0
}

// fullCheckTimeout is how long the full check may run.
func (o *Loop) fullCheckTimeout() time.Duration {
	return orDefault(o.cfg.CheckFullTimeout, project.DefaultCheckFullTimeout)
}

// FullCheck runs the full check on Base's head in a worktree of its own, removed afterwards, for at
// most CheckFullTimeout, stopping everything it started then or when ctx is cancelled (Ctrl+C
// skips it). It says how it went in an EvFullCheck. A failure keeps the whole output in the main
// checkout's .orchestra/run/check-full.log, its end in the log, and files a P2 ticket labelled
// FullCheckLabel for the suite it failed in, unless one for that suite is open already. Call it
// after Run, when FullCheckDue says so.
func (o *Loop) FullCheck(ctx context.Context) {
	c := o.cfg
	keep := context.WithoutCancel(ctx) // the worktree is made and removed whole
	f := fullCheck{at: o.checkout.Head(keep, c.Repo, c.Base)}
	defer func() { o.setFullCheck(f) }()
	wt, err := o.fullCheckWorktree(keep, f.at)
	if err != nil {
		f.outcome = FullCheckNotRun
		o.emit(Event{Kind: EvFullCheck, Detail: f.outcome, Text: fmt.Sprintf(
			"  FULL_CHECK_NOT_RUN: '%s' could not run on %s: no worktree for it%s", c.CheckFull, c.Base, because(err))})
		return
	}
	defer o.removeFullCheckWorktree(keep, wt)
	limit := o.fullCheckTimeout()
	o.info("  FULL_CHECK: running '%s' on %s at %s in %s (time limit %s)",
		c.CheckFull, c.Base, short(f.at), wt, command.ShortDuration(limit))
	began := time.Now()
	checkCtx, cancel := context.WithTimeout(ctx, limit)
	out, err := command.GroupOutput(checkCtx, 5*time.Second, wt, "sh", "-c", c.CheckFull)
	timedOut := ctx.Err() == nil && errors.Is(checkCtx.Err(), context.DeadlineExceeded)
	cancel()
	f.took = time.Since(began).Round(time.Second)
	switch {
	case ctx.Err() != nil:
		f.outcome = FullCheckSkipped
		o.emit(Event{Kind: EvFullCheck, Detail: f.outcome, Text: fmt.Sprintf(
			"  FULL_CHECK_SKIPPED: '%s' was stopped after %s, as asked", c.CheckFull, command.ShortDuration(f.took))})
	case err == nil:
		f.outcome = FullCheckPassed
		o.emit(Event{Kind: EvFullCheck, Detail: f.outcome, Text: fmt.Sprintf(
			"  FULL_CHECK passed: '%s' on %s at %s (%s)", c.CheckFull, c.Base, short(f.at), command.ShortDuration(f.took))})
		o.reportFlaky("", "the full check '"+c.CheckFull+"'", out)
	default:
		o.fullCheckFailed(keep, &f, out, err, timedOut)
	}
}

// fullCheckWorktree makes a worktree with Base's commit at checked out, on no branch, in a new
// folder under WTRoot, and returns its path.
func (o *Loop) fullCheckWorktree(ctx context.Context, at string) (string, error) {
	c := o.cfg
	if at == "" {
		return "", fmt.Errorf("git could not read %s's head", c.Base)
	}
	if err := os.MkdirAll(c.WTRoot, 0o755); err != nil {
		return "", err
	}
	wt, err := os.MkdirTemp(c.WTRoot, "check-full-")
	if err != nil {
		return "", err
	}
	o.repoMu.Lock()
	out, err := o.worktrees.DetachedWorktree(ctx, c.Repo, wt, at)
	o.repoMu.Unlock()
	o.log.Raw(out, err)
	if err != nil {
		_ = os.RemoveAll(wt) // empty, or what git left of it: git forgets it on the next prune
		return "", err
	}
	return wt, nil
}

// removeFullCheckWorktree removes the full check's worktree wt, whatever its check left in it, and
// has git forget it; one that can't be removed is said.
func (o *Loop) removeFullCheckWorktree(ctx context.Context, wt string) {
	err := os.RemoveAll(wt)
	if err == nil {
		o.repoMu.Lock()
		var out string
		out, err = o.worktrees.Prune(ctx, o.cfg.Repo)
		o.repoMu.Unlock()
		o.log.Raw(out, err)
	}
	if err != nil {
		o.emit(Event{Kind: EvWarn, Text: fmt.Sprintf(
			"  CLEANUP_FAILED: the full check's worktree %s is left%s (git worktree remove --force %s removes it)",
			wt, because(err), wt)})
	}
}

// fullCheckFailed says that the full check failed with err (timedOut: it ran out of time), printing
// out: it keeps the output, files a ticket for the suite it failed in, and records it all in f.
func (o *Loop) fullCheckFailed(ctx context.Context, f *fullCheck, out []byte, err error, timedOut bool) {
	c := o.cfg
	how := "fails"
	f.outcome = FullCheckFailed
	if timedOut {
		how = "did not finish within " + command.ShortDuration(o.fullCheckTimeout())
		f.outcome = FullCheckTimedOut
	}
	f.end = lastLines(string(out), fullCheckEnd)
	o.log.Raw(f.end, fmt.Errorf("full check '%s': %w", c.CheckFull, err))
	f.suite = failingSuite(out)
	if f.suite == "" {
		f.suite = c.CheckFull
	}
	f.output = o.saveFullCheckOutput(out)
	f.ticket, f.filed = o.fileFullCheck(ctx, *f, how, out)
	o.emit(Event{Kind: EvFullCheck, Ticket: f.ticket, Detail: f.outcome, Suite: f.suite, Output: f.output,
		Text: fmt.Sprintf("  FULL_CHECK_FAILED: '%s' %s on %s at %s, in the suite %s; the whole output is in %s; %s",
			c.CheckFull, how, c.Base, short(f.at), f.suite, f.output, f.filed)})
}

// failingSuite is the suite a failed full check's output says it was running as it stopped: the
// last line that starts with project.SuiteMarker; "" when none does.
func failingSuite(out []byte) string {
	suite := ""
	for line := range strings.Lines(string(out)) {
		if name, ok := strings.CutPrefix(strings.TrimSpace(line), project.SuiteMarker); ok {
			if name = strings.Join(strings.Fields(name), " "); name != "" {
				suite = name
			}
		}
	}
	return suite
}

// saveFullCheckOutput writes a failed full check's whole output to the main checkout's
// .orchestra/run/check-full.log and returns the file's path; the log's when it can't be written,
// as the log has the end of the output.
func (o *Loop) saveFullCheckOutput(out []byte) string {
	c := o.cfg
	rel := project.RunPath(fullCheckLogName)
	root, err := project.OpenRun(c.Repo)
	if err == nil {
		err = project.WriteRun(root, c.Repo, rel, out, 0o644)
		_ = root.Close() // nothing written is lost: WriteRun closed its file
	}
	if err != nil {
		o.log.Raw("", fmt.Errorf("cannot keep the full check's whole output in %s: %w", c.Repo, err))
		return c.LogPath
	}
	return filepath.Join(c.Repo, rel)
}

// titleWidth is the most runes a ticket's title has, as workers are asked to keep them.
const titleWidth = 60

// fullCheckTitle is the title of the ticket filed for suite: one open with it, and FullCheckLabel,
// is the suite's.
func fullCheckTitle(suite string) string {
	title := "Full check fails: " + suite
	if r := []rune(title); len(r) > titleWidth {
		title = string(r[:titleWidth-1]) + "…"
	}
	return title
}

// fileFullCheck files a P2 ticket for f's suite, which the full check failed in (how: "fails"), with
// the lines of its output, out, that say what failed, unless one is open for that suite already,
// which is noted instead. It returns the ticket's ID, if any, and what became of it, for a line.
func (o *Loop) fileFullCheck(ctx context.Context, f fullCheck, how string, out []byte) (string, string) {
	c := o.cfg
	title := fullCheckTitle(f.suite)
	open, err := o.tickets.Unclosed(ctx)
	if err != nil {
		o.log.Raw("", err) // a second ticket is easier to close than a failure nobody hears of
	}
	for _, t := range open {
		if t.Title == title && HasLabel(t, FullCheckLabel) {
			o.appendNotes(ctx, t.ID, fmt.Sprintf("Orchestra: the full check '%s' %s again on %s at %s, in the suite %s, "+
				"at the end of the run of %s; the whole output is in %s.",
				c.CheckFull, how, c.Base, short(f.at), f.suite, o.started.Format("2006-01-02 15:04"), f.output))
			return t.ID, t.ID + " is open for it already"
		}
	}
	said := failureLines(string(out))
	if len(said) == 0 {
		said = boundLines(strings.Split(lastLines(string(out), maxSaid), "\n"), maxSaid)
	}
	desc := fmt.Sprintf("orchestra's full check, '%s' (check_full in .orchestra/settings.json), %s on %s at %s, "+
		"in the suite %s, at the end of the run of %s. Its whole output is in %s, until the next full check "+
		"fails; the lines that say what failed:\n\n    %s\n\nGet the suite passing on %s, then run '%s' again.",
		c.CheckFull, how, c.Base, short(f.at), f.suite, o.started.Format("2006-01-02 15:04"), f.output,
		strings.Join(said, "\n    "), c.Base, c.CheckFull)
	id, err := o.notes.FileBug(ctx, title, desc, FullCheckLabel)
	if err != nil {
		o.log.Raw("", err)
		return "", "no ticket filed: bd failed" + because(err)
	}
	return id, "filed " + id
}

// setFullCheck keeps how the full check went for the reviewer.
func (o *Loop) setFullCheck(f fullCheck) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.fullChecked = &f
}

// fullCheckOf is how the full check went, if it ran.
func (o *Loop) fullCheckOf() (fullCheck, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.fullChecked == nil {
		return fullCheck{}, false
	}
	return *o.fullChecked, true
}

// evidence is what the reviewer is told of the full check, check, which ran on base.
func (f fullCheck) evidence(check, base string) string {
	switch f.outcome {
	case FullCheckPassed:
		return fmt.Sprintf("'%s' passed on %s at %s, in %s.\n", check, base, short(f.at), command.ShortDuration(f.took))
	case FullCheckSkipped:
		return fmt.Sprintf("'%s' was stopped by the maintainer after %s, before it finished.\n",
			check, command.ShortDuration(f.took))
	case FullCheckNotRun:
		return fmt.Sprintf("'%s' could not run: git could not make a worktree for it.\n", check)
	}
	return fmt.Sprintf("'%s' %s on %s at %s, in the suite %s; its whole output is in %s, and %s. "+
		"The end of its output:\n%s\n", check, f.outcome, base, short(f.at), f.suite, f.output, f.filed, f.end)
}

// report is what the run report says of a full check that failed, after the reviewer's text, so it
// is there whatever the reviewer wrote; "" for one that didn't fail.
func (f fullCheck) report(check string) string {
	if f.outcome != FullCheckFailed && f.outcome != FullCheckTimedOut {
		return ""
	}
	return fmt.Sprintf("\n## Full check\n\n`%s` %s in the suite %s; %s. The whole output is in `%s`; its end:\n\n"+
		"```\n%s\n```\n", check, f.outcome, f.suite, f.filed, f.output, f.end)
}
