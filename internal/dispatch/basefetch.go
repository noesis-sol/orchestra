package dispatch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// A run starts on Base as its upstream has it: where the upstream moved since the last pull (a push from
// another machine, a pull request merged on GitHub), every ticket would otherwise build on a stale
// Base, and the maintainer's next push would need a merge. fetchBase fetches the upstream's branch and
// fast-forwards Base to it, when Base has no commits of its own; it never pushes, and never rewrites
// anything. Nothing it meets stops the run: a fetch that fails, or a Base that has diverged, is warned
// of, and the run builds on Base as it is. An overnight run fetches before its pre-flight instead
// (FetchBase), so that its checks read the Base the run builds on, and the run doesn't fetch again; so
// does a spec run before it commits its spec, or Base, a commit ahead, would read as diverged from an
// upstream that moved.

// Base's warnings as the run starts, the word each line starts with, which the event's Detail gives.
const (
	baseUnfetched = "BASE_UNFETCHED"          // the fetch failed: offline, authentication, its time limit
	baseDiverged  = "BASE_DIVERGED"           // Base and its upstream each have commits the other hasn't
	baseUnmoved   = "BASE_NOT_FAST_FORWARDED" // git failed to compare them, or to fast-forward Base
)

// BaseFetched is what fetching Base's upstream did (see FetchBase).
type BaseFetched struct {
	Upstream string // the upstream Base follows, as a person names it (origin/main); "" for none
	Behind   int    // the commits Base was fast-forwarded by; 0 where it wasn't
	// Warning is the word a warning's line starts with (BASE_UNFETCHED, BASE_DIVERGED,
	// BASE_NOT_FAST_FORWARDED); "" for none.
	Warning string
	// Text is what the warning says, or for a fast-forward what it did; "" for neither.
	Text   string
	Output string // git's output of a fetch or a fast-forward that failed, for the log
}

// BaseFetchGit is what FetchBase asks of git: the main checkout's state, and Base's upstream.
type BaseFetchGit interface {
	Upstream
	// Checkout's DirtyTreeAndBranch leaving out these paths, relative to dir and taken literally
	DirtyTreeAndBranchBesides(ctx context.Context, dir string, paths ...string) (dirty, branch string, err error)
}

// FetchBase brings c.Base up to its upstream as a run does as it starts (see above), for the overnight
// pre-flight and a spec run, before the run's loop exists: the pre-flight's checks then read the Base the
// run builds on, a spec is committed onto it, and the run, given Config.BaseFetched, doesn't fetch again.
// The error says why it fast-forwarded nothing: the main checkout has uncommitted changes, besides those
// to the paths in besides (relative to c.Repo: a spec about to be committed), isn't on Base, or git
// couldn't read it, as it started or after the fetch, right before the fast-forward, each of which stops
// the run before its first ticket. With c.NoFetch it does nothing. Call it only before a run's loop
// starts.
func FetchBase(ctx context.Context, c Config, g BaseFetchGit, besides ...string) (BaseFetched, error) {
	if c.NoFetch {
		return BaseFetched{}, nil
	}
	ready := func() error { return checkoutReady(ctx, c, g, besides) }
	if err := ready(); err != nil {
		return BaseFetched{}, err
	}
	return fetchUpstream(ctx, c, g, new(sync.Mutex), ready)
}

// checkoutReady says why the main checkout isn't ready for FetchBase's fast-forward: it has
// uncommitted changes, besides those to the paths in besides, isn't on Base, or git couldn't read it.
// It returns nil when it is ready.
func checkoutReady(ctx context.Context, c Config, g BaseFetchGit, besides []string) error {
	dirty, branch, err := g.DirtyTreeAndBranchBesides(ctx, c.Repo, besides...)
	switch {
	case err != nil:
		return fmt.Errorf("git could not read the state of %s: %w", c.Repo, err)
	case dirty != "":
		return fmt.Errorf("uncommitted changes in %s", c.Repo)
	case !isBranch(branch, c.Base):
		return fmt.Errorf("%s isn't on %s", c.Repo, c.Base)
	}
	return nil
}

// fetchBase brings Base up to its upstream before the first ticket starts (see above), with the main
// checkout clean and on Base: one that isn't stops the run before its first ticket, as it always has,
// and nothing is fetched; one that changes during the fetch has nothing fast-forwarded, and stops the
// run the same way. It does nothing with Config.NoFetch, after the pre-flight's fetch or a spec run's
// (Config.BaseFetched), without the git to ask (Deps.Upstream), or for a Base that follows no
// upstream, as in a new repository. It moves the run's start (startHead) to the commit it
// fast-forwards Base to, so the run report reads the run's own commits, and has the run take the setup
// and the requirements the commits it brought changed in settings.json, as a merge's do (see
// takeSettings), and the check commands and the worker prompt, as a merge's don't (see takeChecks and
// takePrompt): the run read them before.
func (o *Loop) fetchBase(ctx context.Context) {
	c := o.cfg
	if c.NoFetch || c.BaseFetched || o.upstream == nil || o.checkoutUnready(ctx, "", &o.repoMu) != nil {
		return
	}
	ready := func() error {
		if s := o.checkoutUnready(ctx, "", alreadyLocked{}); s != nil {
			return s
		}
		return nil
	}
	f, err := fetchUpstream(ctx, c, o.upstream, &o.repoMu, ready)
	if err != nil {
		return // the checkout changed during the fetch: the run's first look at it stops the run
	}
	o.log.Raw(f.Output, nil)
	switch {
	case f.Warning != "":
		o.emit(f.WarnEvent())
	case f.Behind > 0:
		o.info("BASE: %s", f.Text)
		keep := context.WithoutCancel(ctx)
		head, err := o.checkout.Head(keep, c.Repo, c.Base)
		o.log.Raw("", err)
		if head == "" {
			return
		}
		o.repoMu.Lock()
		by := "the fast-forward to " + f.Upstream
		if s, ok := o.takeSettings(keep, "", by, o.startHead, head); ok {
			o.takeChecks(by, s)
		}
		o.takePrompt(keep, by, o.startHead, head)
		o.repoMu.Unlock()
		o.startHead = head
	}
}

// takePrompt has the run give its workers the worker prompt (Config.PromptFile) as the fast-forward of
// Base as the run starts (by), from before to now, changed it: no ticket has started yet, so every
// one is told by the prompt of the Base it builds on, as its worktree is made from it. A prompt outside
// the repository, or one the fast-forward left as it was, stays the one the run was given; one git
// can't read at now, or without the TICKET_ID placeholder, too, with a warning. Run takes it before
// any worker starts, so it takes no lock; the caller holds repoMu.
func (o *Loop) takePrompt(ctx context.Context, by, before, now string) {
	c := o.cfg
	rel, err := filepath.Rel(c.Repo, c.PromptFile)
	if c.PromptFile == "" || err != nil || !filepath.IsLocal(rel) {
		return
	}
	rel = filepath.ToSlash(rel)
	if !slices.Contains(o.history.ChangedFiles(ctx, c.Repo, before, now), rel) {
		return
	}
	text, err := o.checkout.FileAt(ctx, c.Repo, now, rel)
	if err == nil && !strings.Contains(text, "TICKET_ID") {
		err = errors.New("it has no TICKET_ID placeholder")
	}
	if err != nil {
		o.emit(Event{Kind: EvWarn, Text: fmt.Sprintf("  PROMPT_UNREAD: %s changed %s on %s, but it could not be "+
			"read%s; the run's workers get the worker prompt it read before", by, rel, c.Base, because(err))})
		return
	}
	if text == o.prompt {
		return
	}
	o.prompt = text
	o.info("  PROMPT: %s changed %s on %s: the run's workers get it as committed", by, rel, c.Base)
}

// fetchUpstream fetches the upstream of Base, checked out in the main checkout, and fast-forwards Base
// to it when Base is behind it only, holding lock while it does. git fast-forwards whichever branch is
// checked out, and the fetch may take a minute, so ready looks at the checkout again under lock right
// before the fast-forward: when it says the checkout is no longer clean and on Base, nothing is
// fast-forwarded and its error is fetchUpstream's, the only one it returns. Once begun, the
// fast-forward isn't cut short by Ctrl+C; what Ctrl+C cuts short before it says nothing.
func fetchUpstream(
	ctx context.Context, c Config, g Upstream, lock sync.Locker, ready func() error,
) (BaseFetched, error) {
	var f BaseFetched
	warn := func(kind, format string, a ...any) (BaseFetched, error) {
		f.Warning, f.Text = kind, fmt.Sprintf(format, a...)
		return f, nil
	}
	ref, remote, remoteRef, err := g.UpstreamOf(ctx, c.Repo, c.Base)
	switch {
	case ctx.Err() != nil: // the run ends, and an answer it cut short says nothing
		return BaseFetched{}, nil
	case err != nil:
		return warn(baseUnmoved, "could not read the upstream of %s%s; the run builds on %s as it is",
			c.Base, because(err), c.Base)
	case ref == "":
		return f, nil
	}
	f.Upstream = shortRef(ref)
	up := f.Upstream
	if out, err := g.Fetch(ctx, c.Repo, remote, remoteRef); err != nil {
		if ctx.Err() != nil {
			return BaseFetched{}, nil
		}
		f.Output = out
		return warn(baseUnfetched, "could not fetch %s%s; the run builds on %s as it is", up, because(err), c.Base)
	}
	ahead, behind, err := g.AheadBehind(ctx, c.Repo, c.Base, ref)
	switch {
	case ctx.Err() != nil:
		return BaseFetched{}, nil
	case err != nil:
		return warn(baseUnmoved, "could not compare %s with %s%s; the run builds on %s as it is",
			c.Base, up, because(err), c.Base)
	case behind == 0: // the same, or Base ahead only: what it has the next push takes
		return f, nil
	case ahead > 0:
		return warn(baseDiverged, "%s is %d ahead of and %d behind %s; the run builds on %s as it is",
			c.Base, ahead, behind, up, c.Base)
	}
	lock.Lock()
	if err := ready(); err != nil {
		lock.Unlock()
		return BaseFetched{}, err
	}
	out, err := g.FastForward(context.WithoutCancel(ctx), c.Repo, ref)
	lock.Unlock()
	if err != nil {
		f.Output = out
		return warn(baseUnmoved, "could not fast-forward %s to %s%s; the run builds on %s as it is",
			c.Base, up, because(err), c.Base)
	}
	commits := "1 commit"
	if behind > 1 {
		commits = fmt.Sprintf("%d commits", behind)
	}
	f.Behind, f.Text = behind, fmt.Sprintf("fast-forwarded %s to %s (%s)", c.Base, up, commits)
	return f, nil
}

// WarnEvent is the warning of f, which has one (Warning), as the run's start gives it: its Detail is
// the word its line starts with. The overnight pre-flight records it before the event stream opens.
func (f BaseFetched) WarnEvent() Event {
	return Event{Kind: EvWarn, Detail: f.Warning, Text: "  " + f.Warning + ": " + f.Text}
}

// shortRef is how a person names ref: origin/main for refs/remotes/origin/main, main for refs/heads/main.
func shortRef(ref string) string {
	for _, p := range []string{"refs/remotes/", "refs/heads/"} {
		if s, ok := strings.CutPrefix(ref, p); ok {
			return s
		}
	}
	return ref
}
