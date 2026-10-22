package dispatch

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/project"
)

// errCheckTimedOut is runCheck's error for a check stopped at its time limit.
var errCheckTimedOut = errors.New("check timed out")

// checkTimeout is how long the check command may run.
func (o *Loop) checkTimeout() time.Duration {
	return orDefault(o.cfg.CheckTimeout, project.DefaultCheckTimeout)
}

// runCheck runs the project's check command in the worktree of r's ticket, its branch rebased onto the
// commit r.onto, or on top of it already. It gives up after checkTimeout, stopping everything the
// check started, and returns errCheckTimedOut then. When the check fails, the end of its output goes
// in the log and the whole of it in the worktree's checkLogName (see keepOutput), and what it said
// goes to the reviewer (see noteCheckFailed); runCheck returns where the output is. When it passes,
// each FLAKY: line it printed is a warning (see reportFlaky).
func (o *Loop) runCheck(ctx context.Context, r rebaseStop, at string) (string, error) {
	run, output := o.runIn(ctx, r, at)
	if run.err == nil {
		o.reportFlaky(r.id, "", run.out)
	}
	return output, run.err
}

// runIn runs the check in the worktree of r's ticket on its branch's commit at, and returns how it ran. It gives up
// after checkTimeout, stopping everything it started, and run.err wraps errCheckTimedOut then. When it fails, the
// end of its output goes in the log, the whole of it in the worktree's checkLogName (see keepOutput), whose path it
// returns as output, and, unless ctx was cancelled, what it said goes to the reviewer (see noteCheckFailed), as
// said of its branch as r has it. Only what is said of the output is kept in memory (see checkOutput). Either way
// it ends with a check record (see checkRunIn). The setup before the check runs through worktreeDeps.run and
// ranIn.
func (o *Loop) runIn(ctx context.Context, r rebaseStop, at string) (run commandRun, output string) {
	began := time.Now()
	run, _ = runLimited(ctx, r.wt, o.checkTimeout(), o.cfg.Check)
	defer run.out.close()
	return run, o.ranIn(ctx, r, at, "", time.Since(began), run)
}

// ranIn is what runIn does once the check, or the setup command setup when it is set, has run in the worktree
// of r's ticket on its branch's commit at, for took, as run says: it logs it, keeps the whole output of one that
// failed (setupLogName for the setup) and tells the reviewer what it said, and returns where that output is; ""
// when it passed. The caller closes run.out.
func (o *Loop) ranIn(ctx context.Context, r rebaseStop, at, setup string, took time.Duration, run commandRun) string {
	w := r.worker
	cmd, logName := o.cfg.Check, checkLogName
	if setup != "" {
		cmd, logName = setup, setupLogName
	}
	if run.err == nil {
		o.log.Check(o.checkRunIn(r, at, setup, took, run.result(ctx), ""))
		return ""
	}
	o.log.Raw(run.out.lastLines(keptLines), fmt.Errorf("'%s' in %s: %w", cmd, w.wt, run.err))
	output := o.keepOutput(w.wt, logName, "the check's", run.out)
	o.log.Check(o.checkRunIn(r, at, setup, took, run.result(ctx), output))
	if ctx.Err() == nil { // stopped by Ctrl+C, it says nothing of the ticket
		o.noteCheckFailed(ctx, r, output, run, setup)
	}
	return output
}

// checkRunIn is the check record of the check, or the setup command setup when it is set, that ran in the worktree
// of r's ticket on its branch's commit at, for took, and ended with result, its whole output kept in output: a
// setup's, a merge check's or, for a ticket checked once more after its check failed, a recheck's.
func (o *Loop) checkRunIn(r rebaseStop, at, setup string, took time.Duration, result CheckResult,
	output string) CheckRun {
	suite, cmd := SuiteMerge, o.cfg.Check
	switch {
	case setup != "":
		suite, cmd = SuiteSetup, setup
	case o.isRechecked(r.id):
		suite = SuiteRecheck
	}
	return CheckRun{Suite: suite, Ticket: r.id, Command: cmd, Commit: short(at), Took: took, Result: result,
		Output: output, Train: r.ahead}
}

// commandRun is how a command run within a time limit went (see runLimited): the check, its setup
// or the full check.
type commandRun struct {
	out   *checkOutput  // what is kept of the output of the command that ran last, for the caller to close
	err   error         // why it failed, wrapping errCheckTimedOut when its time limit stopped it; nil if it passed
	limit time.Duration // the time limit it ran within
}

// runLimited runs the command lines cmds in dir one after another, for at most limit in all, until
// one fails, stopping everything they started then or when ctx is cancelled. It returns how the last
// one that ran went, its error wrapping errCheckTimedOut when limit stopped it (not when ctx did), and
// the index in cmds of the one that failed, -1 when none did.
func runLimited(ctx context.Context, dir string, limit time.Duration, cmds ...string) (commandRun, int) {
	runCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	run := commandRun{limit: limit}
	for i, cmd := range cmds {
		run.out.close()
		if run.out, run.err = runOutput(runCtx, dir, cmd); run.err != nil {
			if ctx.Err() == nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
				run.err = fmt.Errorf("%w: stopped after %s (%v)", errCheckTimedOut, command.ShortDuration(limit), run.err)
			}
			return run, i
		}
	}
	return run, -1
}

// CheckSuite is which of orchestra's checks a check record is of.
type CheckSuite string

// The suites of a check record.
const (
	SuiteMerge   CheckSuite = "merge"   // a ticket's merge check (check_fast), the merge train's included
	SuiteRecheck CheckSuite = "recheck" // a ticket's merge check once more, after it failed (see recheck.go)
	SuiteFull    CheckSuite = "full"    // the full check (check_full) at the end of the run
	SuiteBase    CheckSuite = "base"    // the overnight pre-flight's merge check of Base's head
	SuiteSetup   CheckSuite = "setup"   // the setup in a ticket's worktree, before its worker or its check
)

// CheckResult is how a check or a setup ended, as its check record says.
type CheckResult string

// The results of a check record.
const (
	CheckPassed   CheckResult = "passed"
	CheckFailed   CheckResult = "failed"
	CheckTimedOut CheckResult = "timed_out" // stopped at its time limit
	CheckCutShort CheckResult = "cut_short" // a stop, or Ctrl+C, ended it
)

// CheckRun is a check or a setup orchestra ran, as its check record in the event stream gives it,
// written as it ends (see Log.Check): which suite, for which ticket, what ran on which commit, how long
// it took and how it ended.
type CheckRun struct {
	Suite   CheckSuite
	Ticket  string // the ticket whose check or setup it was; "" for the full check and the base's
	Command string
	Commit  string // the commit checked, short
	Took    time.Duration
	Result  CheckResult
	Output  string // where its whole output is, when it was kept: as it failed, or was cut short
	Train   bool   // it ran ahead of its ticket's turn, as the second in the merge train (see train.go)
}

// result is how r ended, ctx being the one it ran in: passed, timed out, cut short or failed.
func (r commandRun) result(ctx context.Context) CheckResult {
	switch {
	case r.err == nil:
		return CheckPassed
	case r.timedOut():
		return CheckTimedOut
	case ctx.Err() != nil:
		return CheckCutShort
	}
	return CheckFailed
}

// timedOut reports whether r was stopped at its time limit.
func (r commandRun) timedOut() bool {
	return errors.Is(r.err, errCheckTimedOut)
}

// how says how r failed, for a line about it: it fails, or did not finish within its time limit.
func (r commandRun) how() string {
	if r.timedOut() {
		return "did not finish within " + command.ShortDuration(r.limit)
	}
	return "fails"
}

// said is what r's output says of its failure: the lines that say what failed (see
// checkOutput.failures), or with end set, when none do, its last lines, at most maxSaid of them, and
// none when it printed nothing.
func (r commandRun) said() (lines []string, end bool) {
	if lines = r.out.failures(); len(lines) > 0 {
		return lines, false
	}
	if last := r.out.lastLines(maxSaid); strings.TrimSpace(last) != "" {
		lines = boundLines(strings.Split(last, "\n"), maxSaid)
	}
	return lines, true
}

// checkHow says how the check failed with err, for a line about it: it fails, or did not finish.
func (o *Loop) checkHow(err error) string {
	return commandRun{err: err, limit: o.checkTimeout()}.how()
}

// checkLogName is the file in a worktree's .orchestra/run/ that holds the whole output of the last
// check that failed there: the log keeps only its end, where a long failure message can push out
// the name of the test that failed.
const checkLogName = "check.log"

// keepOutput writes a failed command's whole output, out, to logName in the checkout dir's
// .orchestra/run/ and returns the file's path: checkLogName or setupLogName in a ticket's worktree,
// left for review with it, or fullCheckLogName in the main checkout. The worker can change
// .orchestra/run/, so the file is reached through an os.Root, as every run file is (see
// project.OpenRun). A file that can't be written is logged, what saying whose output it is ("the
// check's"), and the log's path returned: it has the end of the output.
func (o *Loop) keepOutput(dir, logName, what string, out *checkOutput) string {
	rel := project.RunPath(logName)
	root, err := project.OpenRun(dir)
	if err == nil {
		err = out.save(root, dir, rel)
		_ = root.Close() // nothing written is lost: WriteRun closed its file
	}
	if err != nil {
		o.log.Raw("", fmt.Errorf("cannot keep %s whole output in %s: %w", what, dir, err))
		return o.cfg.LogPath
	}
	return filepath.Join(dir, rel)
}

// checkFail is what a ticket's failed check said, for the reviewer: the run report says what
// failed, and whether in code the ticket changed, without sending the maintainer to the log.
type checkFail struct {
	br, onto string   // the branch checked, rebased onto Base at onto
	onTop    bool     // it was on top of Base at onto already, not rebased
	how      string   // as checkHow says
	output   string   // where the whole output is, as keepOutput returned it
	said     []string // the lines of the output that say what failed (see checkOutput.failures)
	saidEnd  bool     // no line said: said is the output's last lines, if it printed any
	dirs     []string // the directories the ticket's own commits change, sorted (see ownDirs)
	setup    string   // the setup command, when it failed before the check could run (see setUp)
}

// maxDirs is the most directories the reviewer is given of those a ticket's commits change.
const maxDirs = 20

// evidence is what the reviewer is told of ticket id's failed check, check, which ran on its branch
// rebased onto base, or on top of it already: how it failed, the lines that say what failed and the
// directories the ticket's own commits change, so the reviewer can tell a failure in its code from one
// elsewhere.
func (f checkFail) evidence(id, check, base string) string {
	var b strings.Builder
	on, changed := "rebased onto", "the rebase changed"
	if f.onTop {
		on, changed = "on top of", "its commits change"
	}
	if f.setup != "" {
		fmt.Fprintf(&b, "%s: the setup '%s', run before the check as %s the project's dependency "+
			"files, %s on %s %s %s at %s, so the check did not run; its output is in %s.\n",
			id, f.setup, changed, f.how, f.br, on, base, short(f.onto), f.output)
	} else {
		fmt.Fprintf(&b, "%s: '%s' %s on %s %s %s at %s; its output is in %s.\n",
			id, check, f.how, f.br, on, base, short(f.onto), f.output)
	}
	switch {
	case len(f.said) == 0:
		b.WriteString("It printed nothing.\n")
	case f.saidEnd:
		b.WriteString("No line of its output says what failed; its last lines:\n")
	default:
		b.WriteString("The lines of its output that say what failed:\n")
	}
	for _, l := range f.said {
		b.WriteString("    " + l + "\n")
	}
	dirs := "none that git could list"
	if len(f.dirs) > 0 {
		var named []string
		for _, d := range f.dirs[:min(len(f.dirs), maxDirs)] {
			if d == "." {
				d = "the top level (.)"
			}
			named = append(named, d)
		}
		dirs = strings.Join(named, ", ")
		if more := len(f.dirs) - len(named); more > 0 {
			dirs += fmt.Sprintf(" and %d more", more)
		}
	}
	fmt.Fprintf(&b, "Directories that %s's own commits change (%s..%s): %s\n", id, short(f.onto), f.br, dirs)
	return b.String()
}

var (
	// failedPackage matches a line that says what failed (see checkOutput.failures) naming a package that
	// failed: go test's FAIL line, or gotestsum's === FAIL:.
	failedPackage = regexp.MustCompile(`^(?:FAIL|=== FAIL:)\s+(\S+)`)
	// failedFile matches one naming the file of a lint, vet or build error, at its position.
	failedFile = regexp.MustCompile(`^([^\s:]+\.\w+):\d+(?::\d+)?: `)
)

// ownFailures lists the directories the ticket's own commits change (f.dirs) that the lines of its
// failed check say it failed in, naming a package there or a file; none when they name none, or say
// nothing (saidEnd), or when its setup failed: the setup's output names none of the ticket's code. A
// package is named by its import path, which ends with its directory.
func (f checkFail) ownFailures() []string {
	if f.saidEnd || f.setup != "" {
		return nil
	}
	var own []string
	for _, line := range f.said {
		var at string
		if m := failedPackage.FindStringSubmatch(line); m != nil {
			at = path.Clean(m[1])
		} else if m := failedFile.FindStringSubmatch(line); m != nil {
			at = path.Dir(path.Clean(m[1]))
		} else {
			continue
		}
		for _, d := range f.dirs {
			if (at == d || d != "." && strings.HasSuffix(at, "/"+d)) && !slices.Contains(own, d) {
				own = append(own, d)
			}
		}
	}
	return own
}

// noteCheckFailed keeps for the reviewer what the check of r's ticket said as it failed (run), or its
// setup when setup is the setup command: the lines of its output that say what failed (see
// commandRun.said), where the output is kept (output), and the directories that the ticket's commits
// on top of r.onto change.
func (o *Loop) noteCheckFailed(ctx context.Context, r rebaseStop, output string, run commandRun, setup string) {
	w, onto := r.worker, r.onto
	f := checkFail{br: w.br, onto: onto, onTop: r.onTop, how: run.how(), output: output, setup: setup}
	f.said, f.saidEnd = run.said()
	f.dirs = o.ownDirs(context.WithoutCancel(ctx), w.br, onto) // a read cut short would look like no change
	o.book.checkFailed(w.id, f)
}

// ownDirs lists the directories of the files that the commits on br on top of onto change, sorted,
// "." for the repository's top level; nil when git can't say.
func (o *Loop) ownDirs(ctx context.Context, br, onto string) []string {
	if onto == "" {
		return nil
	}
	files, err := o.history.ChangedFiles(ctx, o.cfg.Repo, onto, br)
	if err != nil {
		o.log.Raw("", err)
		return nil
	}
	var dirs []string
	for _, f := range files {
		if d := path.Dir(f); !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	slices.Sort(dirs)
	return dirs
}

// How much of a failed check's output the reviewer is given: at most maxSaid lines, each cut to
// saidWidth runes.
const (
	maxSaid   = 30
	saidWidth = 200
)

// boundLines cuts each line to saidWidth runes and keeps at most n of them, the first; the last kept
// then says how many more there were.
func boundLines(lines []string, n int) []string {
	if len(lines) > n {
		more := len(lines) - n + 1
		lines = append(lines[:n-1:n-1], fmt.Sprintf("(… and %d more lines like these)", more))
	}
	for i, l := range lines {
		if r := []rune(l); len(r) > saidWidth {
			lines[i] = string(r[:saidWidth-1]) + "…"
		}
	}
	return lines
}

// reportFlaky warns of each FLAKY: line in the output of a passing check, which was ticket id's when
// id is set, else what what says ("the full check 'scripts/check-full.sh'"): the check passed, so the
// ticket merges, but a test that passes only on a rerun is a bug to fix, and nobody would see it
// otherwise. Past maxFlaky of them, one more warning counts the rest.
func (o *Loop) reportFlaky(id, what string, out *checkOutput) {
	if id != "" {
		what = fmt.Sprintf("%s's check '%s'", id, o.cfg.Check)
	}
	for _, test := range out.flaky {
		if test == "" {
			test = "it doesn't say which"
		}
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  FLAKY: %s passed, but a test failed and then passed on a rerun: %s", what, test)})
	}
	if more := out.flakyN - len(out.flaky); more > 0 {
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  FLAKY: %s passed, but %d more tests failed and then passed on a rerun", what, more)})
	}
}
