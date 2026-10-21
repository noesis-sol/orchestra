package dispatch

import (
	"context"
	"fmt"
	"strings"
)

// A run starts on Base as its upstream has it: where the upstream moved since the last pull (a push from
// another machine, a pull request merged on GitHub), every ticket would otherwise build on a stale
// Base, and the maintainer's next push would need a merge. fetchBase fetches the upstream's branch and
// fast-forwards Base to it, when Base has no commits of its own; it never pushes, and never rewrites
// anything. Nothing it meets stops the run: a fetch that fails, or a Base that has diverged, is warned
// of, and the run builds on Base as it is.

// Base's warnings as the run starts, the word each line starts with, which the event's Detail gives.
const (
	baseUnfetched = "BASE_UNFETCHED"          // the fetch failed: offline, authentication, its time limit
	baseDiverged  = "BASE_DIVERGED"           // Base and its upstream each have commits the other hasn't
	baseUnmoved   = "BASE_NOT_FAST_FORWARDED" // git failed to compare them, or to fast-forward Base
)

// fetchBase brings Base up to its upstream before the first ticket starts (see above), with the main
// checkout clean and on Base: one that isn't stops the run before its first ticket, as it always has,
// and nothing is fetched. It does nothing with Config.NoFetch, without the git to ask (Deps.Upstream),
// or for a Base that follows no upstream, as in a new repository. It moves the run's start (startHead)
// to the commit it fast-forwards Base to, so the run report reads the run's own commits.
func (o *Loop) fetchBase(ctx context.Context) {
	c, g := o.cfg, o.upstream
	if c.NoFetch || g == nil || o.checkoutUnready(ctx, "", &o.repoMu) != nil {
		return
	}
	ref, remote, remoteRef, err := g.UpstreamOf(ctx, c.Repo, c.Base)
	if ctx.Err() != nil {
		return // Ctrl+C: the run ends, and an answer it cut short says nothing
	}
	if err != nil {
		o.warnBase(baseUnmoved, "could not read the upstream of %s%s; the run builds on %s as it is",
			c.Base, because(err), c.Base)
		return
	}
	if ref == "" {
		return
	}
	up := shortRef(ref)
	if out, err := g.Fetch(ctx, c.Repo, remote, remoteRef); err != nil {
		if ctx.Err() == nil {
			o.log.Raw(out, nil)
			o.warnBase(baseUnfetched, "could not fetch %s%s; the run builds on %s as it is", up, because(err), c.Base)
		}
		return
	}
	ahead, behind, err := g.AheadBehind(ctx, c.Repo, c.Base, ref)
	switch {
	case ctx.Err() != nil:
	case err != nil:
		o.warnBase(baseUnmoved, "could not compare %s with %s%s; the run builds on %s as it is",
			c.Base, up, because(err), c.Base)
	case behind == 0: // the same, or Base ahead only: what it has the next push takes
	case ahead > 0:
		o.warnBase(baseDiverged, "%s is %d ahead of and %d behind %s; the run builds on %s as it is",
			c.Base, ahead, behind, up, c.Base)
	default:
		o.fastForwardBase(ctx, ref, up, behind)
	}
}

// fastForwardBase fast-forwards Base, checked out in the main checkout, to its upstream ref, behind
// commits ahead of it, which up names for the log. Once begun, it isn't cut short by Ctrl+C.
func (o *Loop) fastForwardBase(ctx context.Context, ref, up string, behind int) {
	c, keep := o.cfg, context.WithoutCancel(ctx)
	o.repoMu.Lock()
	out, err := o.upstream.FastForward(keep, c.Repo, ref)
	o.repoMu.Unlock()
	if err != nil {
		o.log.Raw(out, nil)
		o.warnBase(baseUnmoved, "could not fast-forward %s to %s%s; the run builds on %s as it is",
			c.Base, up, because(err), c.Base)
		return
	}
	commits := "1 commit"
	if behind > 1 {
		commits = fmt.Sprintf("%d commits", behind)
	}
	o.info("BASE: fast-forwarded %s to %s (%s)", c.Base, up, commits)
	head, err := o.checkout.Head(keep, c.Repo, c.Base)
	o.log.Raw("", err)
	if head != "" {
		o.startHead = head
	}
}

// warnBase warns of what fetchBase met, kind being the word its line starts with.
func (o *Loop) warnBase(kind, format string, a ...any) {
	o.emit(Event{Kind: EvWarn, Detail: kind, Text: "  " + kind + ": " + fmt.Sprintf(format, a...)})
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
