package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/noesis-sol/orchestra/internal/project"
)

// An overnight run's pre-flight runs the merge check (check_fast) once on Base's head before anything
// starts: where it fails there, every ticket's would fail on its branch rebased onto Base, and the night
// would set them all aside. It runs as the full check does, in a worktree of its own made from Base's
// head and removed afterwards, the project's setup first, within the merge check's time limit, so the
// main checkout stays as it is.

// BaseCheckLogName is the file in the main checkout's .orchestra/run/ that holds the whole output of
// the merge check that failed on Base before a run.
const BaseCheckLogName = "check-base.log"

// BaseGit is what CheckBase asks of git: Base's head, and a worktree of its own to check it in.
type BaseGit interface {
	Head(ctx context.Context, repo, rev string) (string, error)
	DetachedWorktree(ctx context.Context, repo, path, rev string) (string, error)
	Prune(ctx context.Context, repo string) (string, error)
}

// BaseChecked is how the merge check went on Base's head, as CheckBase ran it.
type BaseChecked struct {
	At     string        // Base's commit it ran on
	Took   time.Duration // how long it ran, the setup's time included
	Failed string        // how it failed, as a line says it ("'make check' fails"); "" when it passed
	// Said is what its output says of the failure: the lines that say what failed, else its last lines.
	Said   []string
	Output string // where the whole output of a failure is, in Repo's .orchestra/run/; "" when it passed
	// Kept is a worktree CheckBase couldn't remove, for the caller to say; "" when it removed it.
	Kept string
}

// CheckBase runs c.Check, after c.Setup when the project has one, on c.Base's head in a new worktree
// under c.WTRoot, for at most c.CheckTimeout in all (project.DefaultCheckTimeout for 0), stopping
// everything it started then or when ctx is cancelled, and removes the worktree. The error says why it
// couldn't run: no head, no worktree, or ctx cancelled while it ran. Call it only where nothing else
// works on the repository's worktrees, as before a run's loop starts.
func CheckBase(ctx context.Context, c Config, g BaseGit) (BaseChecked, error) {
	keep := context.WithoutCancel(ctx) // the worktree is made and removed whole
	var b BaseChecked
	at, err := g.Head(keep, c.Repo, c.Base)
	if err == nil && at == "" {
		err = fmt.Errorf("git could not read %s's head", c.Base)
	}
	if err != nil {
		return b, err
	}
	b.At = at
	wt, err := detachedWorktree(keep, g, c, "check-base-", at)
	if err != nil {
		return b, fmt.Errorf("no worktree for it: %w", err)
	}
	defer func() {
		if os.RemoveAll(wt) != nil {
			b.Kept = wt
			return
		}
		_, _ = g.Prune(keep, c.Repo) // git forgets it on the next prune, whichever runs it
	}()
	cmds, what := []string{c.Check}, "'"+c.Check+"'"
	if c.Setup != "" { // always: nothing is installed in a new worktree
		cmds = []string{c.Setup, c.Check}
	}
	began := time.Now()
	run, failed := runLimited(ctx, wt, orDefault(c.CheckTimeout, project.DefaultCheckTimeout), cmds...)
	defer run.out.close()
	b.Took = time.Since(began).Round(time.Second)
	switch {
	case ctx.Err() != nil:
		return b, ctx.Err()
	case run.err == nil:
		return b, nil
	case failed == 0 && c.Setup != "":
		what = "the setup '" + c.Setup + "', run before it,"
	}
	b.Failed = what + " " + run.how()
	b.Said, _ = run.said()
	b.Output = keepRunOutput(c.Repo, BaseCheckLogName, run.out)
	return b, nil
}

// detachedWorktree makes a worktree with commit at checked out, on no branch, in a new folder under
// c.WTRoot named after prefix, and returns its path.
func detachedWorktree(ctx context.Context, g BaseGit, c Config, prefix, at string) (string, error) {
	if err := os.MkdirAll(c.WTRoot, 0o755); err != nil {
		return "", err
	}
	wt, err := os.MkdirTemp(c.WTRoot, prefix)
	if err != nil {
		return "", err
	}
	if _, err := g.DetachedWorktree(ctx, c.Repo, wt, at); err != nil {
		_ = os.RemoveAll(wt) // empty, or what git left of it: git forgets it on the next prune
		return "", err
	}
	return wt, nil
}

// keepRunOutput writes out, a failed command's whole output, to logName in the checkout dir's
// .orchestra/run/ and returns the file's path, or "" where it can't be written.
func keepRunOutput(dir, logName string, out *checkOutput) string {
	rel := project.RunPath(logName)
	root, err := project.OpenRun(dir)
	if err == nil {
		err = out.save(root, dir, rel)
		err = errors.Join(err, root.Close())
	}
	if err != nil {
		return ""
	}
	return filepath.Join(dir, rel)
}
