package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
)

// A spec run commits to Base before its loop starts: the setup of a repository orchestra init hasn't
// set up, and the spec. It brings Base up to its upstream before either, as the loop would as it
// starts (dispatch.FetchBase): once Base has a commit of its own, an upstream that moved since the last
// pull reads as diverged, and the whole spec would be built on the stale Base. The loop then doesn't
// fetch again.

// fetchForSpec brings Base up to its upstream before the spec run s commits anything, unless the
// overnight pre-flight has (Config.BaseFetched), and says what it did: Base fast-forwarded, or left as
// it is and why, the warning recorded in the event stream once it opens. After a fast-forward the run
// takes the settings and the worker prompt it brought in (takeFetched), as the loop's own fetch
// would, and says so. A main checkout the fetch can't start on is left to the checks after it, which
// stop the run: uncommitted changes besides the spec before anything is committed, a checkout not on
// Base before the first ticket.
func (s *session) fetchForSpec(ctx context.Context) {
	c := &s.cfg
	if c.NoFetch || c.BaseFetched {
		return
	}
	f, err := dispatch.FetchBase(ctx, c.Config, git.Git{}, specBesides(c.Config)...)
	if err != nil || ctx.Err() != nil {
		s.log.Raw("", err)
		return
	}
	c.BaseFetched = true
	s.log.Raw(f.Output, nil)
	switch {
	case f.Warning != "":
		fmt.Fprintf(s.e.stderr, "warning: %s: %s\n", f.Warning, f.Text)
		ev := f.WarnEvent()
		ev.Time = time.Now()
		s.log.Line(ev.Time, f.Warning+": "+f.Text)
		s.log.Record(ev) // written once the stream opens, after the run's start record
	case f.Behind > 0:
		fmt.Fprintln(s.e.stdout, "BASE: "+f.Text)
		s.log.Line(time.Now(), "BASE: "+f.Text)
		for _, ev := range s.takeFetched(ctx, git.Git{}, f) {
			if ev.Kind == dispatch.EvWarn {
				fmt.Fprintln(s.e.stderr, "warning: "+strings.TrimSpace(ev.Text))
			} else {
				fmt.Fprintln(s.e.stdout, ev.Text)
			}
		}
	}
}

// specBesides is what a fetch of Base for the run c leaves out of the main checkout's uncommitted
// changes (dispatch.FetchBase): the spec it commits, where it has one (Config.Spec).
func specBesides(c dispatch.Config) []string {
	if c.Spec == "" {
		return nil
	}
	return []string{c.Spec}
}
