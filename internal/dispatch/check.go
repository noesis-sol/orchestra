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

// runCheck runs the project's check command in w's worktree, its branch rebased onto the commit
// onto. It gives up after checkTimeout, stopping everything the check started, and returns
// errCheckTimedOut then. When the check fails, the end of its output goes in the log and the whole of
// it in the worktree's checkLogName (see saveCheckOutput), and what it said goes to the reviewer (see
// noteCheckFailed); runCheck returns where the output is. When it passes, each FLAKY: line it printed
// is a warning (see reportFlaky).
func (o *Loop) runCheck(ctx context.Context, w worker, onto string) (string, error) {
	out, output, err := o.runIn(ctx, w, o.cfg.Check, checkLogName)
	if err != nil {
		if ctx.Err() == nil { // stopped by Ctrl+C, it says nothing of the ticket
			o.noteCheckFailed(ctx, w, onto, output, out, err, "")
		}
		return output, err
	}
	o.reportFlaky(w.id, "", out)
	return "", nil
}

// runIn runs the command line cmd, the check or the setup, in w's worktree and returns its output. It
// gives up after checkTimeout, stopping everything cmd started, and returns errCheckTimedOut then.
// When cmd fails, the end of its output goes in the log and the whole of it in the worktree's file
// logName (see saveCheckOutput), whose path it returns as output. Only what is said of the output is
// kept in memory (see checkOutput).
func (o *Loop) runIn(ctx context.Context, w worker, cmd, logName string) (out *checkOutput, output string, err error) {
	limit := o.checkTimeout()
	runCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	out, err = runOutput(runCtx, w.wt, cmd)
	defer out.close()
	if err != nil && ctx.Err() == nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("%w: stopped after %s (%v)", errCheckTimedOut, command.ShortDuration(limit), err)
	}
	if err != nil {
		o.log.Raw(out.lastLines(keptLines), fmt.Errorf("'%s' in %s: %w", cmd, w.wt, err))
		return out, o.saveCheckOutput(w.wt, logName, out), err
	}
	return out, "", nil
}

// checkHow says how the check failed with err, for a line about it: it fails, or did not finish.
func (o *Loop) checkHow(err error) string {
	if errors.Is(err, errCheckTimedOut) {
		return "did not finish within " + command.ShortDuration(o.checkTimeout())
	}
	return "fails"
}

// checkLogName is the file in a worktree's .orchestra/run/ that holds the whole output of the last
// check that failed there: the log keeps only its end, where a long failure message can push out
// the name of the test that failed.
const checkLogName = "check.log"

// saveCheckOutput writes a failed check's whole output, out, to logName (checkLogName, or setupLogName for
// the setup) in the worktree wt's .orchestra/run/, left for review with it, and returns the file's
// path. The worker can change .orchestra/run/, so the file
// is reached through an os.Root, as every run file is (see project.OpenRun). A file that can't be
// written is logged, and the log's path returned: it has the end of the output.
func (o *Loop) saveCheckOutput(wt, logName string, out *checkOutput) string {
	rel := project.RunPath(logName)
	root, err := project.OpenRun(wt)
	if err == nil {
		err = out.save(root, wt, rel)
		_ = root.Close() // nothing written is lost: WriteRun closed its file
	}
	if err != nil {
		o.log.Raw("", fmt.Errorf("cannot keep the check's whole output in %s: %w", wt, err))
		return o.cfg.LogPath
	}
	return filepath.Join(wt, rel)
}

// checkFail is what a ticket's failed check said, for the reviewer: the run report says what
// failed, and whether in code the ticket changed, without sending the maintainer to the log.
type checkFail struct {
	br, onto string   // the branch checked, rebased onto Base at onto
	how      string   // as checkHow says
	output   string   // where the whole output is, as saveCheckOutput returned it
	said     []string // the lines of the output that say what failed (see failureLines)
	saidEnd  bool     // no line said: said is the output's last lines, if it printed any
	dirs     []string // the directories the ticket's own commits change, sorted (see ownDirs)
	setup    string   // the setup command, when it failed before the check could run (see setUp)
}

// maxDirs is the most directories the reviewer is given of those a ticket's commits change.
const maxDirs = 20

// evidence is what the reviewer is told of ticket id's failed check, check, which ran on its branch
// rebased onto base: how it failed, the lines that say what failed and the directories the ticket's
// own commits change, so the reviewer can tell a failure in its code from one elsewhere.
func (f checkFail) evidence(id, check, base string) string {
	var b strings.Builder
	if f.setup != "" {
		fmt.Fprintf(&b, "%s: the setup '%s', run before the check as the rebase changed the project's dependency "+
			"files, %s on %s rebased onto %s at %s, so the check did not run; its output is in %s.\n",
			id, f.setup, f.how, f.br, base, short(f.onto), f.output)
	} else {
		fmt.Fprintf(&b, "%s: '%s' %s on %s rebased onto %s at %s; its output is in %s.\n",
			id, check, f.how, f.br, base, short(f.onto), f.output)
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
	// failedPackage matches a line that says what failed (see failureLines) naming a package that
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

// noteCheckFailed keeps for the reviewer what w's check said as it failed with err, or its setup when
// setup is the setup command: the lines of its output, out, that say what failed, where out is kept
// (output), and the directories that the ticket's commits on top of onto change.
func (o *Loop) noteCheckFailed(ctx context.Context, w worker, onto, output string, out *checkOutput, err error,
	setup string) {
	f := checkFail{br: w.br, onto: onto, how: o.checkHow(err), output: output, setup: setup}
	if f.said = out.failures(); len(f.said) == 0 {
		f.saidEnd = true
		if end := out.lastLines(maxSaid); strings.TrimSpace(end) != "" {
			f.said = boundLines(strings.Split(end, "\n"), maxSaid)
		}
	}
	f.dirs = o.ownDirs(context.WithoutCancel(ctx), w.br, onto) // a read cut short would look like no change
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.checkSaid == nil {
		o.checkSaid = map[string]checkFail{}
	}
	o.checkSaid[w.id] = f
}

// checkSaidOf is what ticket id's last check in this run said, if it failed.
func (o *Loop) checkSaidOf(id string) (checkFail, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	f, ok := o.checkSaid[id]
	return f, ok
}

// ownDirs lists the directories of the files that the commits on br on top of onto change, sorted,
// "." for the repository's top level; nil when git can't say.
func (o *Loop) ownDirs(ctx context.Context, br, onto string) []string {
	if onto == "" {
		return nil
	}
	var dirs []string
	for _, f := range o.history.ChangedFiles(ctx, o.cfg.Repo, onto, br) {
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
