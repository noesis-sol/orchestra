package main

import (
	"context"
	"fmt"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/project"
)

// A spec run commits to Base before its loop starts: the setup of a repository orchestra init hasn't
// set up, and the spec. It brings Base up to its upstream before either, as the loop would as it
// starts (dispatch.FetchBase): once Base has a commit of its own, an upstream that moved since the last
// pull reads as diverged, and the whole spec would be built on the stale Base. The loop then doesn't
// fetch again.

// fetchForSpec brings Base up to its upstream before the spec run s commits anything, unless the
// overnight pre-flight has (Config.BaseFetched), and says what it did: Base fast-forwarded, or left as
// it is and why. After a fast-forward the run takes the setup and the requirements it brought in
// (takeFetchedSettings), as the loop's own fetch would. A main checkout the fetch can't start on is
// left to the checks after it, which stop the run: uncommitted changes besides the spec before
// anything is committed, a checkout not on Base before the first ticket.
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
		s.log.Line(time.Now(), f.Warning+": "+f.Text)
	case f.Behind > 0:
		fmt.Fprintln(s.e.stdout, "BASE: "+f.Text)
		s.log.Line(time.Now(), "BASE: "+f.Text)
		if err := s.takeFetchedSettings(); err != nil {
			w := fmt.Sprintf("%s could not be read again: %s; the run keeps the setup and the requirements "+
				"it read before", project.SettingsFile, oneLine(err.Error()))
			fmt.Fprintln(s.e.stderr, "warning: "+w)
			s.log.Line(time.Now(), "SETUP: "+w)
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
