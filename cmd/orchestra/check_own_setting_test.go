package main

import "testing"

// check_own in settings.json is a worker's own check, which the run's Config carries for its
// workers' rules; without it there is none, and the rules name the merge check.
func TestCheckOwnSetting(t *testing.T) {
	t.Parallel()
	c, _, p := settle(t, nil, nil, `{"check_fast": "make check", "check_own": "make own"}`)
	if len(p) > 0 || c.Check != "make check" || c.CheckOwn != "make own" {
		t.Errorf("set: check %q, own %q, problems %v", c.Check, c.CheckOwn, p)
	}
	if c, _, p = settle(t, nil, nil, `{"check_fast": "make check"}`); len(p) > 0 || c.CheckOwn != "" {
		t.Errorf("unset: own %q, problems %v", c.CheckOwn, p)
	}
}
