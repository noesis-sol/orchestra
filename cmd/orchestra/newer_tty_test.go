//go:build darwin || linux

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A run on a release build with a newer release in the cache ends with one line saying so, below
// the box of a run with nothing to run; GitHub isn't asked, as the cache is a day old at most. Up to
// date, on a dev build, with ORCHESTRA_NO_UPDATE_CHECK=1, or with plain output and stderr not a
// terminal, it says nothing, and the check turned off writes no cache.
func TestRunEndsSayingANewerReleaseIsOut(t *testing.T) {
	const line = "orchestra v0.6.0 is available (you have v0.5.1): orchestra update"
	for _, tc := range []struct {
		name    string
		version string
		cached  string // the latest release in the cache, checked an hour ago; "" for no cache
		env     string // ORCHESTRA_NO_UPDATE_CHECK
		args    []string
		lines   int
	}{
		{"newer release cached", "v0.5.1", "v0.6.0", "", nil, 1},
		{"up to date", "v0.6.0", "v0.6.0", "", nil, 0},
		{"dev build", "dev", "v0.6.0", "", nil, 0},
		{"check turned off", "v0.5.1", "", "1", nil, 0},
		{"plain output", "v0.5.1", "v0.6.0", "", []string{"--plain"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asked := latestRelease(t, tc.version, "v0.6.0")
			cache := cacheIn(t)
			if tc.cached != "" {
				cacheRelease(t, cache, tc.cached)
			}
			setenv(t, noUpdateCheck, tc.env)
			nothingTools(t, backlog{})
			term, _, exit := runOnTerminal(t, append([]string{"--tickets"}, tc.args...)...)
			code, stderr := exit()
			if code != dispatch.ExitOK || stderr != "" {
				t.Fatalf("exit %d, stderr:\n%s\nthe terminal:\n%s", code, stderr, screenOf(term))
			}
			if got := strings.Count(screenOf(term), line); got != tc.lines {
				t.Errorf("the terminal has the line %d times, want %d:\n%s", got, tc.lines, screenOf(term))
			}
			if n := asked.Load(); n > 0 {
				t.Errorf("asked GitHub %d times, with an answer an hour old", n)
			}
			if _, err := os.Stat(cache); tc.cached == "" && !os.IsNotExist(err) {
				t.Errorf("the cache's folder exists (%v)", err)
			}
		})
	}
}
