package dispatch

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A ticket's new worktree has nothing installed in it, so before its worker starts there the
// project's setup command (setup in .orchestra/settings.json, such as npm ci) runs in it (see
// setUpWorktree): workers start with their dependencies installed rather than each installing them
// itself. A setup that fails there isn't the ticket's fault: no worker starts, and the ticket goes
// back in the queue, counting toward the hold for the environment (see leaveSetupFailed).
//
// A finished ticket's worktree has the dependencies set up for the code as it was then. Its rebase
// onto Base can bring in a changed lockfile, from a ticket that merged meanwhile, and the check would
// then run the rebased code against dependencies installed for the code before. So before the check
// on a rebased branch (merge, RECHECK and a hand-back's verification), or one on top of Base already
// whose worker changed one, the setup runs in the worktree
// when the commits since its dependencies were last set up change a file setup_files names. A setup
// that fails fails the check, its output kept in the worktree's setupLogName. One that fails, or is cut
// short, may leave the dependencies missing or half installed, so the next setup due there runs it
// again whatever changed, in this run or the next (see worktreeDeps, which owns all of this).
//
// A merge that changes settings.json on Base has the run take setup and setup_files from it from
// then on (see takeSettings): the environment ticket a spec's breakdown files first sets them.

// setupLogName is the file in a worktree's .orchestra/run/ that holds the whole output of the last
// setup that failed there; a setup that works there removes it (see setupFailedIn).
const setupLogName = "setup.log"

// setupMarkName is the file in a worktree's .orchestra/run/ that says a setup began there and hasn't
// worked since: made before every setup (see worktreeDeps.run), it is removed once one works there.
// One that Ctrl+C, the merge train or the end of orchestra's process cut short leaves it, its
// dependencies half installed or missing (npm ci removes node_modules first), and the next setup due
// there runs whatever changed (see setupCutShortIn).
const setupMarkName = "setup.started"

// setupConf is the project's setup command and the globs of the files it reads: setup and
// setup_files in .orchestra/settings.json.
type setupConf struct {
	cmd   string
	files []string
}

// setupNow is the setup as the run has it now: Config's, unless a merge in this run committed
// others to Base's settings.json (see takeSettings).
func (o *Loop) setupNow() setupConf {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.setupSet != nil {
		return *o.setupSet
	}
	return setupConf{cmd: o.cfg.Setup, files: o.cfg.SetupFiles}
}

// worktreeDeps is the one owner of whether a ticket's worktree has its dependencies set up: what they
// were last set up for in this run (see ticketBook.setUpSince), whether the last setup there failed or
// was cut short, in this run (see ticketBook.setupBroke) or an earlier one (see setupFailedIn,
// setupCutShortIn), and so whether the setup is due (see due) and what running it leaves (see run).
// Every setup orchestra runs or asks for in a worktree goes through it: before a worker starts there,
// before a check, in the merge train, and for a worker handed its rebase to resolve.
type worktreeDeps struct {
	o      *Loop
	id, wt string
	s      setupConf
}

// depsOf is the owner of ticket id's worktree wt's dependencies, with the setup as the run has it now.
func (o *Loop) depsOf(id, wt string) worktreeDeps {
	return worktreeDeps{o: o, id: id, wt: wt, s: o.setupNow()}
}

// setupCause is why a worktree's setup is due (see worktreeDeps.due).
type setupCause int

// The causes of a setup due; dueNone for none.
const (
	dueNone     setupCause = iota
	dueFailed              // the last setup there failed: its output is there (see setupFailedIn)
	dueCutShort            // the last setup there began and didn't finish: its mark is there (see setupCutShortIn)
	dueBroken              // a setup in this run failed or was cut short there, and left neither file
	dueUnknown             // git can't say what changed since its dependencies were last set up
	dueChanged             // a file the setup's globs match changed since they were last set up
)

// setupDue is why a worktree's setup is due: its cause, and for dueChanged the files that changed.
type setupDue struct {
	cause   setupCause
	changed []string
}

// due reports whether the setup runs in the worktree for its branch at commit now: always after a setup
// there that failed or was cut short, which may have left the dependencies missing or half installed
// (npm ci removes node_modules first), and otherwise when the commits since they were last set up in
// this run, or since before (its branch's commit before its rebase) if they weren't, change a file the
// setup's globs match, or git can't say what they change. A worktree with no setup has none due.
func (d worktreeDeps) due(ctx context.Context, before, now string) setupDue {
	switch {
	case d.s.cmd == "":
		return setupDue{}
	case setupFailedIn(d.wt):
		return setupDue{cause: dueFailed}
	case setupCutShortIn(d.wt):
		return setupDue{cause: dueCutShort}
	case d.o.book.brokenSetup(d.id):
		return setupDue{cause: dueBroken}
	}
	changed, known := d.o.setupChanged(ctx, d.id, before, now, d.s.files)
	switch {
	case !known:
		return setupDue{cause: dueUnknown}
	case len(changed) == 0:
		return setupDue{}
	}
	return setupDue{cause: dueChanged, changed: changed}
}

// why says why the setup runs, for a line; "" when it doesn't.
func (u setupDue) why() string {
	switch u.cause {
	case dueFailed:
		return "the last setup in its worktree failed"
	case dueCutShort:
		return "the last setup in its worktree didn't finish"
	case dueBroken:
		return "the last setup in its worktree failed or didn't finish"
	case dueUnknown:
		return "git could not say what changed since its dependencies were last set up"
	case dueChanged:
		return fileList(u.changed) + " changed since its dependencies were last set up"
	}
	return ""
}

// again reports whether the setup runs as the last one there didn't work.
func (u setupDue) again() bool {
	return u.cause == dueFailed || u.cause == dueCutShort || u.cause == dueBroken
}

// run runs the setup in the worktree, its branch at commit now ("" when git couldn't say), within the
// check's time limit, and returns how it ran, for the caller to log and close, and how long it took. It
// marks the setup under way first (see setupMarkName); one that works records what the dependencies are
// set up for and removes the mark and the output of one that failed earlier; one that fails, or is cut
// short, leaves the mark and has the next due run it again, whatever changed meanwhile.
func (d worktreeDeps) run(ctx context.Context, now string) (commandRun, time.Duration) {
	o := d.o
	o.markSetup(d.wt)
	began := time.Now()
	run, _ := runLimited(ctx, d.wt, o.checkTimeout(), d.s.cmd)
	took := time.Since(began)
	if run.err != nil {
		o.book.setupBroke(d.id)
		return run, took
	}
	o.book.setUp(d.id, now)
	o.dropRunFile(d.wt, setupLogName, "the output of a setup that failed earlier")
	o.dropRunFile(d.wt, setupMarkName, "the mark of a setup under way")
	return run, took
}

// setUp runs the setup command in the worktree of r's ticket, its branch rebased onto the commit r.onto
// from r.head (or on top of it already), when it is due (see worktreeDeps.due): the commits since its
// dependencies were last set up in this run, or since r.head if they weren't, change a file the setup's
// globs match, git can't say what they change, or the last setup there failed or was cut short. It
// returns as runCheck does: where the output of a setup that failed is, and errCheckTimedOut for one
// stopped at the check's time limit.
func (o *Loop) setUp(ctx context.Context, r rebaseStop) (string, error) {
	w := r.worker
	d := o.depsOf(w.id, w.wt)
	if d.s.cmd == "" {
		return "", nil
	}
	keep := context.WithoutCancel(ctx)                  // a read cut short would look like no change
	now, err := o.checkout.Head(keep, o.cfg.Repo, w.br) // "" on failure: the setup runs
	o.log.Raw("", err)
	due := d.due(keep, r.head, now)
	if due.cause == dueNone {
		return "", nil
	}
	o.info("  %s: %s; running '%s' before the check", w.id, due.why(), d.s.cmd)
	run, took := d.run(ctx, now)
	defer run.out.close()
	output := o.ranIn(ctx, r, now, d.s.cmd, took, run)
	return output, run.err
}

// setupChanged lists the files globs match that the commits since ticket id's dependencies were last
// set up (see ticketBook.setUpSince) change, up to commit now; known is false when git can't say.
func (o *Loop) setupChanged(ctx context.Context, id, before, now string, globs []string) (changed []string,
	known bool) {
	from := o.book.setUpSince(id, before)
	if from == "" || now == "" {
		return nil, false
	}
	files, err := o.history.ChangedFiles(ctx, o.cfg.Repo, from, now)
	if err != nil {
		o.log.Raw("", err)
		return nil, false
	}
	return setupFilesIn(files, globs), true
}

// setupToResolve is what the worker handed r's rebase back to resolve (see handBack) is told of the
// setup, and what the line saying so adds: to run it once the rebase has finished, before the tests,
// when it is due for r.onto, which the rebase brings in (see worktreeDeps.due): the commits since its
// dependencies were last set up change a file the setup's globs match, git can't say what they change,
// or the last setup in its worktree failed or was cut short; "" for both when it isn't, or there is no
// setup. Its tests would otherwise run on dependencies set up for the code before the rebase, as
// tutti-nrg.62's did ("Cannot find module"). orchestra runs the setup itself before it checks the
// resolution (see setUp).
func (o *Loop) setupToResolve(ctx context.Context, r rebaseStop) (prompt, line string) {
	d := o.depsOf(r.id, r.wt)
	due := d.due(ctx, r.head, r.onto)
	var why string
	switch due.cause {
	case dueNone:
		return "", ""
	case dueUnknown:
		why = "git could not say whether the rebase brings in a change to the files the project's setup reads"
	case dueChanged:
		why = "the rebase brings in a change to " + fileList(due.changed) + ", which the project's setup reads"
	default:
		why = due.why()
	}
	return fmt.Sprintf("As %s, once the rebase has finished run the setup, '%s', in the foreground before the "+
			"tests, so that they run on dependencies set up for the rebased code. ", why, d.s.cmd),
		fmt.Sprintf("; told to run '%s' once the rebase has finished, as %s", d.s.cmd, why)
}

// setUpWorktree runs the setup command in the ticket's worktree p before its worker starts there,
// within the check's time limit: always in a new one, where nothing is installed, and in one reused
// when it is due there, as before the check (see worktreeDeps.due): the last setup there failed or was
// cut short, or the commits since its dependencies were last set up change a file the setup's globs
// match. It records the commit it set up for, so that the check after the ticket sets them up again
// only for what changes since. A worker adopted, or handed its ticket back, is in its worktree already:
// this runs only for one about to start. It reports whether the ticket's run is over, as its setup
// failed (see leaveSetupFailed), or Ctrl+C came, the reason the run stops for then.
func (r *ticketRun) setUpWorktree(ctx context.Context, p preparedWorktree) (bool, *stopReason) {
	o, id := r.o, r.id
	d := o.depsOf(id, p.wt)
	if d.s.cmd == "" {
		return false, nil
	}
	now, err := o.checkout.Head(r.keep, o.cfg.Repo, r.br) // "" on failure: the setup runs
	o.log.Raw("", err)
	what := "running '" + d.s.cmd + "' in its new worktree"
	if p.fresh {
		o.book.freshWorktree(id) // a worktree it had before, since removed, was set up for itself
	} else {
		due := d.due(r.keep, p.before, now)
		switch {
		case due.cause == dueNone:
			return false, nil
		case due.again():
			what = due.why() + "; running '" + d.s.cmd + "' there again"
		default:
			what = due.why() + "; running '" + d.s.cmd + "' in its worktree"
		}
	}
	o.info("  %s: %s before its worker starts", id, what)
	run, took := d.run(ctx, now)
	defer run.out.close()
	ran := CheckRun{Suite: SuiteSetup, Ticket: id, Command: d.s.cmd, Commit: short(now), Took: took,
		Result: run.result(ctx)}
	switch {
	case run.err == nil:
		o.log.Check(ran)
		return false, nil
	case ctx.Err() != nil:
		// Stopped by Ctrl+C, it says nothing of the setup; its mark stays, for the next run to set up again.
		o.log.Check(ran)
		return true, errInterrupted
	}
	ran.Output = r.leaveSetupFailed(d.s.cmd, p.wt, run)
	o.log.Check(ran)
	return true, nil
}

// setupFailedIn reports whether the last setup in worktree wt failed, in this run or an earlier one:
// the whole output it kept is there (see keepOutput), until a setup works there (see worktreeDeps.run).
// Its dependencies may then be missing, or half installed.
func setupFailedIn(wt string) bool {
	_, err := os.Lstat(filepath.Join(wt, project.RunPath(setupLogName)))
	return err == nil
}

// setupCutShortIn reports whether a setup began in worktree wt, in this run or an earlier one, and neither worked
// nor failed there: its mark is there (see setupMarkName). A failure keeps the mark too, but
// setupFailedIn says so first.
func setupCutShortIn(wt string) bool {
	_, err := os.Lstat(filepath.Join(wt, project.RunPath(setupMarkName)))
	return err == nil
}

// markSetup marks that a setup begins in worktree wt (see setupMarkName). A failure is logged: the
// setup runs all the same, and only Ctrl+C cutting it short would go unnoticed by the next run.
func (o *Loop) markSetup(wt string) {
	root, err := project.OpenRun(wt)
	if err == nil {
		err = project.WriteRun(root, wt, project.RunPath(setupMarkName), nil, 0o644)
		_ = root.Close() // WriteRun closed its file
	}
	if err != nil {
		o.log.Raw("", fmt.Errorf("cannot mark the setup under way in %s: %w", wt, err))
	}
}

// dropRunFile removes the file name, what, from worktree wt's .orchestra/run/, now that a setup has
// worked there: the output of one that failed (setupLogName), or the mark of one under way
// (setupMarkName). A failure is logged: the setup then runs again when the worktree is next taken up.
func (o *Loop) dropRunFile(wt, name, what string) {
	root, err := project.OpenRun(wt)
	if err == nil {
		err = project.RemoveRun(root, wt, project.RunPath(name))
		_ = root.Close() // nothing written through it
	}
	if err != nil {
		o.log.Raw("", fmt.Errorf("cannot remove %s in %s: %w", what, wt, err))
	}
}

// failedSetup is a ticket whose worktree's setup failed (see leaveSetupFailed): where the whole output
// is, and whether bd failed to reopen it, leaving it out of bd ready.
type failedSetup struct {
	output      string
	notReopened bool
}

// leaveSetupFailed puts the ticket back in the queue, open, as the setup cmd failed in its worktree wt
// (run) before its worker could start: its environment failed, not the ticket, so it isn't set aside,
// not even as a ticket that asked a question earlier in the run (see waitForMerge). Its worktree is
// kept, with the whole output, for the next worker to take up. It stays out of the run until a ticket
// merges, which may bring what the setup needs, or a probe ends a hold for the environment (see
// retrySetups): taken again at once, it would fail again. It counts toward that hold as a worker
// failing at once does (see countSettling), so that setups failing ticket after ticket hold the run
// rather than leave the queue to them. One bd fails to reopen stays as it was, set aside if it was. It
// returns where the whole output is.
func (r *ticketRun) leaveSetupFailed(cmd, wt string, run commandRun) string {
	o, id, keep := r.o, r.id, r.keep
	r.how = setupFailed
	o.log.Raw(run.out.lastLines(keptLines), fmt.Errorf("'%s' in %s: %w", cmd, wt, run.err))
	f := failedSetup{output: o.keepOutput(wt, setupLogName, "the setup's", run.out)}
	f.notReopened = !o.reopen(keep, id, r.t.Status, "be back in the queue after its setup failed")
	// Back in the queue, it is no longer set aside: retrySetups brings it back, and the run's end names
	// it as waiting on its setup.
	o.setupFailed(id, f)
	o.appendNotes(keep, id, fmt.Sprintf("Orchestra: the setup '%s' %s in worktree %s, so no worker was started; "+
		"back in the queue (the whole output is in %s).", cmd, run.how(), wt, f.output))
	o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
		"  SETUP_FAILED: '%s' %s in %s for %s; the whole output is in %s", cmd, run.how(), wt, id, f.output)})
	return f.output
}

// failedSetupOf reports whether ticket id's worktree's setup failed, keeping it out of the run for now
// (see leaveSetupFailed), and where the whole output is.
func (o *Loop) failedSetupOf(id string) (output string, failed bool) {
	r, _ := o.book.get(id)
	return r.setup.output, r.disp == dispSetupFailed
}

// setupWaits is what the run's end line adds for the tickets kept out of it as their worktrees' setup
// failed (see leaveSetupFailed), each still ready and waiting on a setup that works: SETUP_WAITS,
// naming them and where each one's output is, for the maintainer to fix the setup and run again; ""
// for none. One bd failed to reopen isn't ready, and its REOPEN_FAILED said so: it is left out.
func (o *Loop) setupWaits() string {
	var outputs []string
	ids, recs := o.book.matching(func(r *ticketRecord) bool { return r.disp == dispSetupFailed && !r.setup.notReopened })
	for _, r := range recs {
		outputs = append(outputs, r.setup.output)
	}
	switch len(ids) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("; SETUP_WAITS: %s waits on the setup that failed in its worktree (the whole output is in %s); "+
			"fix the setup, then run again", ids[0], outputs[0])
	}
	return fmt.Sprintf("; SETUP_WAITS: %s wait on the setups that failed in their worktrees (the whole output is in "+
		"%s); fix the setup, then run again", strings.Join(ids, ", "), strings.Join(outputs, ", "))
}

// retrySetups lets the tickets whose worktree's setup failed be taken again: a ticket merged, which
// may bring what the setup needs, or a probe found the machine working after a hold.
func (o *Loop) retrySetups() { o.book.retrySetups() }

// takeSettings has the run take setup and setup_files from settings.json as ticket id's merge
// committed it to Base, when the merge, from before, changed it: a setup set during the run counts
// from then on, as the environment ticket a spec's breakdown files first sets it for the tickets after
// it. Its requirements too, checked again before the next ticket starts (see takeRequires). One git
// can't read, or that doesn't parse, is warned of, and the setup stays as it was, as do the
// requirements; so is a merge git can't say changed it or not. now is Base's commit after the merge:
// the branch's it fast-forwarded to. The caller holds repoMu, so Base stays where the merge left it. by
// names the merge for the lines, "<id>'s merge". Its check commands count from the next run; a
// fast-forward of Base before the first ticket has the run take them too (see TakeFastForward).
func (o *Loop) takeSettings(ctx context.Context, id, by, before, now string) {
	c := o.cfg
	if before == "" || now == "" {
		return
	}
	changed, err := o.history.ChangedFiles(ctx, c.Repo, before, now)
	if err != nil {
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  SETTINGS_UNREAD: git could not say whether %s changed %s on %s%s; the setup stays %s, and the "+
				"requirements as they were", by, project.SettingsFile, c.Base, because(err), setupSaid(o.setupNow().cmd))})
		return
	}
	if !slices.Contains(changed, project.SettingsFile) {
		return
	}
	text, err := o.checkout.FileAt(ctx, c.Repo, now, project.SettingsFile)
	var s project.Settings
	if err == nil {
		s, err = project.ParseSettings([]byte(text))
	}
	if err == nil {
		o.takeRequires(id, by, s)
	}
	o.takeSetup(id, by, s, err)
}

// takeSetup has the run take setup and setup_files from s, the settings.json by (ticket id's merge)
// committed to Base, unless err says why it couldn't be read (see takeSettings).
func (o *Loop) takeSetup(id, by string, s project.Settings, err error) {
	c := o.cfg
	was := o.setupNow()
	var files []string
	if err == nil {
		files, err = project.ResolveSetupFiles(s)
	}
	if err != nil {
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  SETTINGS_UNREAD: %s changed %s on %s, but it could not be read%s; the setup stays %s",
			by, project.SettingsFile, c.Base, because(err), setupSaid(was.cmd))})
		return
	}
	if s.Setup == was.cmd && slices.Equal(files, was.files) {
		return
	}
	o.mu.Lock()
	o.setupSet = &setupConf{cmd: s.Setup, files: files}
	o.mu.Unlock()
	o.info("  SETUP: %s changed %s on %s: the setup is %s from now on",
		by, project.SettingsFile, c.Base, setupSaid(s.Setup))
}

// setupSaid names the setup command cmd for a line: quoted, or "none".
func setupSaid(cmd string) string {
	if cmd == "" {
		return "none"
	}
	return "'" + cmd + "'"
}

// setupFilesIn lists the files that one of globs matches: its name, or its path from the
// repository's top when the glob has a slash (a leading one aside).
func setupFilesIn(files, globs []string) []string {
	var hit []string
	for _, f := range files {
		for _, g := range globs {
			name := path.Base(f)
			if strings.Contains(g, "/") {
				g, name = strings.TrimPrefix(g, "/"), f
			}
			if ok, _ := path.Match(g, name); ok { // a bad glob matches nothing; settings reject them
				hit = append(hit, f)
				break
			}
		}
	}
	return hit
}

// maxFilesNamed is the most files a line names of those a rebase changed.
const maxFilesNamed = 5

// fileList names files for a line, at most maxFilesNamed of them.
func fileList(files []string) string {
	if len(files) <= maxFilesNamed {
		return strings.Join(files, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(files[:maxFilesNamed], ", "), len(files)-maxFilesNamed)
}
