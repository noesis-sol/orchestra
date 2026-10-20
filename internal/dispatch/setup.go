package dispatch

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

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
// that fails fails the check, its output kept in the worktree's setupLogName.
//
// A merge that changes settings.json on Base has the run take setup and setup_files from it from
// then on (see takeSettings): the environment ticket a spec's breakdown files first sets them.

// setupLogName is the file in a worktree's .orchestra/run/ that holds the whole output of the last
// setup that failed there; a setup before a worker that works there removes it (see setupFailedIn).
const setupLogName = "setup.log"

// setupMarkName is the file in a worktree's .orchestra/run/ that says a setup began there and hasn't
// worked since: made before the setup runs before a worker (see setUpWorktree), it is removed once
// one works there. One that Ctrl+C, or the end of orchestra's process, cut short leaves it, its
// dependencies half installed or missing (npm ci removes node_modules first), and the next run that
// takes the worktree up runs the setup again (see setupCutShortIn).
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

// setUp runs the setup command in the worktree of r's ticket, its branch rebased onto the commit r.onto
// from r.head (or on top of it already), when the commits since its dependencies were last set up in
// this run, or since r.head if they weren't, change a file the setup's globs match (see setupDue);
// when git can't say what they change, it runs it anyway. It returns as runCheck does: where the
// output of a setup that failed is, and errCheckTimedOut for one stopped at the check's time limit.
func (o *Loop) setUp(ctx context.Context, r rebaseStop) (string, error) {
	w := r.worker
	s := o.setupNow()
	if s.cmd == "" {
		return "", nil
	}
	keep := context.WithoutCancel(ctx)                  // a read cut short would look like no change
	now, err := o.checkout.Head(keep, o.cfg.Repo, w.br) // "" on failure: the setup runs
	o.log.Raw("", err)
	what := o.setupDue(keep, w.id, r.head, now, s.files)
	if what == "" {
		return "", nil
	}
	o.info("  %s: %s; running '%s' before the check", w.id, what, s.cmd)
	if run, output := o.runIn(ctx, r, s.cmd); run.err != nil {
		return output, run.err
	}
	o.setUpAt(w.id, now)
	return "", nil
}

// setupDue says why the setup runs for ticket id's branch at commit now: the files globs match that the
// commits since its dependencies were last set up change (see setUpFrom), its worker's own included, or
// that git can't say what they change; "" when they change none.
func (o *Loop) setupDue(ctx context.Context, id, before, now string, globs []string) string {
	from := o.setUpFrom(id, before)
	if from == "" || now == "" {
		return "git could not say what changed since its dependencies were last set up"
	}
	changed := setupFilesIn(o.history.ChangedFiles(ctx, o.cfg.Repo, from, now), globs)
	if len(changed) == 0 {
		return ""
	}
	return fileList(changed) + " changed since its dependencies were last set up"
}

// setUpFrom is the commit ticket id's dependencies were last set up for in this run; before, its
// branch's commit before its rebase, if they weren't, which it keeps as that commit: a setup that
// fails leaves them as they were.
func (o *Loop) setUpFrom(id, before string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if from, ok := o.setUpFor[id]; ok {
		return from
	}
	if before != "" {
		o.setUpFor[id] = before
	}
	return before
}

// setUpAt records that ticket id's dependencies are set up for commit at; "" (git couldn't say)
// records nothing.
func (o *Loop) setUpAt(id, at string) {
	if at == "" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.setUpFor[id] = at
}

// setUpWorktree runs the setup command in the ticket's worktree p before its worker starts there,
// within the check's time limit: always in a new one, where nothing is installed, and in one reused
// when the last setup there failed (see setupFailedIn) or was cut short (see setupCutShortIn), or the
// commits since its dependencies were last set up change a file the setup's globs match, as before the
// check (see setUp). It records the
// commit it set up for, so that the check after the ticket sets them up again only for what changes
// since. A worker adopted, or handed its ticket back, is in its worktree already: this runs only for
// one about to start. It reports whether the ticket's run is over, as its setup failed (see
// leaveSetupFailed), or Ctrl+C came, the reason the run stops for then.
func (r *ticketRun) setUpWorktree(ctx context.Context, p preparedWorktree) (bool, *stopReason) {
	o, id := r.o, r.id
	s := o.setupNow()
	if s.cmd == "" {
		return false, nil
	}
	now, err := o.checkout.Head(r.keep, o.cfg.Repo, r.br) // "" on failure: the setup runs
	o.log.Raw("", err)
	what, failedThere := "running '"+s.cmd+"' in its new worktree", setupFailedIn(p.wt)
	switch {
	case p.fresh:
		o.forgetSetUp(id) // a worktree it had before, since removed, was set up for itself
	case failedThere:
		what = "the last setup in its worktree failed; running '" + s.cmd + "' there again"
	case setupCutShortIn(p.wt):
		what = "the last setup in its worktree didn't finish; running '" + s.cmd + "' there again"
	default:
		why := o.setupDue(r.keep, id, p.before, now, s.files)
		if why == "" {
			return false, nil
		}
		what = why + "; running '" + s.cmd + "' in its worktree"
	}
	o.info("  %s: %s before its worker starts", id, what)
	o.markSetup(p.wt)
	run, _ := runLimited(ctx, p.wt, o.checkTimeout(), s.cmd)
	defer run.out.close()
	switch {
	case run.err == nil:
		o.setUpAt(id, now)
		if failedThere {
			o.dropRunFile(p.wt, setupLogName, "the output of a setup that failed earlier")
		}
		o.dropRunFile(p.wt, setupMarkName, "the mark of a setup under way")
		return false, nil
	case ctx.Err() != nil:
		// Stopped by Ctrl+C, it says nothing of the setup; its mark stays, for the next run to set up again.
		return true, errInterrupted
	}
	r.leaveSetupFailed(s.cmd, p.wt, run)
	return true, nil
}

// setupFailedIn reports whether the last setup in worktree wt failed, in this run or an earlier one:
// the whole output it kept is there (see keepOutput), until a setup before a worker works there (see
// dropRunFile). Its dependencies may then be missing, or half installed.
func setupFailedIn(wt string) bool {
	_, err := os.Lstat(filepath.Join(wt, project.RunPath(setupLogName)))
	return err == nil
}

// setupCutShortIn reports whether a setup began in worktree wt, in an earlier run, and neither worked
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

// forgetSetUp forgets what ticket id's dependencies were set up for: its worktree is new.
func (o *Loop) forgetSetUp(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.setUpFor, id)
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
// rather than leave the queue to them. One bd fails to reopen stays as it was, set aside if it was.
func (r *ticketRun) leaveSetupFailed(cmd, wt string, run commandRun) {
	o, id, keep := r.o, r.id, r.keep
	r.how = setupFailed
	o.log.Raw(run.out.lastLines(keptLines), fmt.Errorf("'%s' in %s: %w", cmd, wt, run.err))
	f := failedSetup{output: o.keepOutput(wt, setupLogName, "the setup's", run.out)}
	if r.t.Status != StatusOpen {
		if err := o.notes.Reopen(keep, id); err != nil {
			f.notReopened = true
			o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
				"  REOPEN_FAILED: %s stays %s, so it won't be back in the queue after its setup failed%s; "+
					"reopen it with: bd update %s --status open", id, r.t.Status, because(err), id)})
		}
	}
	o.mu.Lock()
	o.failedSetups[id] = f
	o.mu.Unlock()
	if !f.notReopened {
		// Back in the queue, it is no longer set aside: retrySetups brings it back, and the run's end
		// names it as waiting on its setup.
		o.unmarkAside(id)
	}
	o.appendNotes(keep, id, fmt.Sprintf("Orchestra: the setup '%s' %s in worktree %s, so no worker was started; "+
		"back in the queue (the whole output is in %s).", cmd, run.how(), wt, f.output))
	o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
		"  SETUP_FAILED: '%s' %s in %s for %s; the whole output is in %s", cmd, run.how(), wt, id, f.output)})
}

// failedSetupOf reports whether ticket id's worktree's setup failed, keeping it out of the run for now
// (see leaveSetupFailed), and where the whole output is.
func (o *Loop) failedSetupOf(id string) (output string, failed bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	f, failed := o.failedSetups[id]
	return f.output, failed
}

// setupWaits is what the run's end line adds for the tickets kept out of it as their worktrees' setup
// failed (see leaveSetupFailed), each still ready and waiting on a setup that works: SETUP_WAITS,
// naming them and where each one's output is, for the maintainer to fix the setup and run again; ""
// for none. One bd failed to reopen isn't ready, and its REOPEN_FAILED said so: it is left out.
func (o *Loop) setupWaits() string {
	o.mu.Lock()
	var ids, outputs []string
	for _, id := range slices.Sorted(maps.Keys(o.failedSetups)) {
		if f := o.failedSetups[id]; !f.notReopened {
			ids, outputs = append(ids, id), append(outputs, f.output)
		}
	}
	o.mu.Unlock()
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
func (o *Loop) retrySetups() {
	o.mu.Lock()
	defer o.mu.Unlock()
	clear(o.failedSetups)
}

// takeSettings has the run take setup and setup_files from settings.json as ticket id's merge
// committed it to Base, when the merge, from before, changed it: a setup set during the run counts
// from then on, as the environment ticket a spec's breakdown files first sets it for the tickets after
// it. One git can't read, or that doesn't parse, is warned of, and the setup stays as it was. now is
// Base's commit after the merge: the branch's it fast-forwarded to. The caller holds repoMu, so Base
// stays where the merge left it.
func (o *Loop) takeSettings(ctx context.Context, id, before, now string) {
	c := o.cfg
	if before == "" || now == "" ||
		!slices.Contains(o.history.ChangedFiles(ctx, c.Repo, before, now), project.SettingsFile) {
		return
	}
	was := o.setupNow()
	text, err := o.checkout.FileAt(ctx, c.Repo, now, project.SettingsFile)
	var s project.Settings
	if err == nil {
		s, err = project.ParseSettings([]byte(text))
	}
	var files []string
	if err == nil {
		files, err = project.ResolveSetupFiles(s)
	}
	if err != nil {
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  SETTINGS_UNREAD: %s's merge changed %s on %s, but it could not be read%s; the setup stays %s",
			id, project.SettingsFile, c.Base, because(err), setupSaid(was.cmd))})
		return
	}
	if s.Setup == was.cmd && slices.Equal(files, was.files) {
		return
	}
	o.mu.Lock()
	o.setupSet = &setupConf{cmd: s.Setup, files: files}
	o.mu.Unlock()
	o.info("  SETUP: %s's merge changed %s on %s: the setup is %s from now on",
		id, project.SettingsFile, c.Base, setupSaid(s.Setup))
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
