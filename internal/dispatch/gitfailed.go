package dispatch

import (
	"context"
	"fmt"
)

// A git call that fails to answer, as one that times out on a loaded machine, says nothing about the
// repository: its error is not a no, none or "". Each question the run asks git returns git's error
// beside its answer, and where the answer steers a merge (the ticket's own commits, whether its branch
// is on top of Base or changes nothing, where each branch is, a rebase in progress) a failure is asked
// again, then stops the run with GIT_FAILED, the ticket left unmerged for review, rather than send the
// merge down the wrong branch. A hand-back's checks reject what its worker left with git's error, and
// what only informs (a line, a note, the organs' evidence) says git couldn't tell.

// askGit asks git q, and again after a poll while it fails, gitTries times in all, as readCheckout
// does: a git call can fail for a moment on a busy machine. It returns q's last answer and error,
// logging each error it asks again after. While it waits it lets go of held, a lock the caller holds
// (repo, as a merge holds it), so the run isn't held up meanwhile: nil for none.
func askGit[T any](ctx context.Context, o *Loop, held *lockGuard, q func() (T, error)) (T, error) {
	for try := 1; ; try++ {
		v, err := q()
		if err == nil || try == gitTries || ctx.Err() != nil {
			return v, err
		}
		o.log.Raw("", err) // asked again; only the error returned is the caller's to report
		if held != nil {
			held.unlock()
		}
		waited := sleep(ctx, o.pollEvery())
		if held != nil {
			held.lock()
		}
		if !waited {
			return v, err
		}
	}
}

// head is the commit rev is on, asked of git as askGit does, letting go of held meanwhile: "" when rev
// names none.
func (o *Loop) head(ctx context.Context, held *lockGuard, rev string) (string, error) {
	return askGit(ctx, o, held, func() (string, error) { return o.checkout.Head(ctx, o.cfg.Repo, rev) })
}

// branchCommits is what a branch has beyond Base (see Merger.OwnCommits): the latest of its commits
// naming its ticket, "" for none, and how many it has.
type branchCommits struct {
	naming string
	n      int
}

// ownCommits is what br has beyond base for ticket id, asked of git as askGit does.
func (o *Loop) ownCommits(ctx context.Context, base, br, id string) (branchCommits, error) {
	return askGit(ctx, o, nil, func() (branchCommits, error) {
		naming, n, err := o.merger.OwnCommits(ctx, o.cfg.Repo, base, br, id)
		return branchCommits{naming: naming, n: n}, err
	})
}

// gitFailedMerging leaves w's closed ticket unmerged, git having failed to tell what its merge turns
// on (what, as in "whether wt/A is on top of main"), and returns the reason to stop the run: GIT_FAILED,
// with git's error. The caller lets go of repo first.
func (o *Loop) gitFailedMerging(ctx context.Context, w worker, what string, err error) *stopReason {
	o.leaveUnmerged(ctx, w.id, string(stopGitFailed))
	return halt(ExitTool, stopGitFailed, ": git can't tell %s%s; stopping before merging %s; worktree %s and tab %s "+
		"left for review", what, because(err), w.br, w.wt, w.tab).causedBy(err).blocks("git failed")
}

// gitCantTell is why a worker's hand-back, or a ticket's recheck, is turned down when git can't tell
// what (as in "whether wt/A has uncommitted changes"), with git's error.
func gitCantTell(what string, err error) string {
	return fmt.Sprintf("git could not say %s%s", what, because(err))
}
