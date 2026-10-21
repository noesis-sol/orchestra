//go:build unix

package main

import (
	"testing"

	"github.com/noesis-sol/orchestra/internal/tui"
)

// The overnight pre-flight's fast-forward may rename the check commands the run read before it: the
// base check after it, and the run, take those it brought (orchestra-5zii).

func TestFetchCheckTakesTheChecksItBringsIn(t *testing.T) {
	t.Parallel()
	s, other, git := fetchSession(t)
	s.cfg.Check, s.cfg.CheckFull, s.cfg.CheckOwn = "make old", "make old-full", "make old-own"
	pushSettings(t, git, other, `{"check_fast": "scripts/check-fast.sh", "check_full": "scripts/check.sh"}`)
	wantResult(t, fetchCheck(runCtx(t), s), tui.CheckPassed, "fast-forwarded main to origin/main (1 commit)")
	if c := s.cfg; c.Check != "scripts/check-fast.sh" || c.CheckFull != "scripts/check.sh" || c.CheckOwn != "" {
		t.Errorf("check_fast %q, check_full %q, check_own %q: want those the fast-forward brought in",
			c.Check, c.CheckFull, c.CheckOwn)
	}
}
