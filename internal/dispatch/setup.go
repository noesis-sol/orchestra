package dispatch

import (
	"context"
	"fmt"
	"path"
	"strings"
)

// A finished ticket's worktree has the dependencies its worker installed for the code as it closed the
// ticket. Its rebase onto Base can bring in a changed lockfile, from a ticket that merged meanwhile,
// and the check would then run the rebased code against dependencies installed for the code before.
// So before the check on a rebased branch (merge, RECHECK and a hand-back's verification), the
// project's setup command (setup in .orchestra/settings.json, such as npm ci) runs in the worktree
// when the commits since its dependencies were last set up change a file setup_files names. A setup
// that fails fails the check, its output kept in the worktree's setupLogName.

// setupLogName is the file in a worktree's .orchestra/run/ that holds the whole output of the last
// setup that failed there.
const setupLogName = "setup.log"

// setUp runs the setup command in w's worktree, its branch rebased onto the commit onto from before,
// when the commits since its dependencies were last set up in this run, or since before if they
// weren't, change a file Config.SetupFiles matches; when git can't say what they change, it runs it
// anyway. It returns as runCheck does: where the output of a setup that failed is, and
// errCheckTimedOut for one stopped at the check's time limit.
func (o *Loop) setUp(ctx context.Context, w worker, before, onto string) (string, error) {
	c := o.cfg
	if c.Setup == "" {
		return "", nil
	}
	keep := context.WithoutCancel(ctx) // a read cut short would look like no change
	from := o.setUpFrom(w.id, before)
	now := o.checkout.Head(keep, c.Repo, w.br)
	what := "git could not say what the rebase changed"
	if from != "" && now != "" {
		changed := setupFilesIn(o.history.ChangedFiles(keep, c.Repo, from, now), c.SetupFiles)
		if len(changed) == 0 {
			return "", nil
		}
		what = fileList(changed) + " changed in the rebase"
	}
	o.info("  %s: %s; running '%s' before the check", w.id, what, c.Setup)
	out, output, err := o.runIn(ctx, w, c.Setup, setupLogName)
	if err != nil {
		if ctx.Err() == nil { // stopped by Ctrl+C, it says nothing of the ticket
			o.noteCheckFailed(ctx, w, onto, output, out, err, c.Setup)
		}
		return output, err
	}
	if now != "" {
		o.mu.Lock()
		if o.setUpFor == nil {
			o.setUpFor = map[string]string{}
		}
		o.setUpFor[w.id] = now
		o.mu.Unlock()
	}
	return "", nil
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
		if o.setUpFor == nil {
			o.setUpFor = map[string]string{}
		}
		o.setUpFor[id] = before
	}
	return before
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
