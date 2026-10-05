//go:build darwin || linux

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// A test that ends before orchestra exits, as one whose wait fails does, stops orchestra and waits
// for it: no run is left going on a closed terminal, in a repository removed and an environment
// restored beneath it, where the race detector blames whichever test runs then.
func TestTerminalRunStopsAsTheTestEnds(t *testing.T) {
	t.Run("ends while claude screens the feature", func(t *testing.T) {
		dir := featureTools(t, featureScreenOK, "", 0)
		// claude screens the request until it is stopped, in place of featureTools' claude.
		if err := os.Remove(filepath.Join(dir, "claude")); err != nil {
			t.Fatal(err)
		}
		faketool.Write(t, dir, "claude", "#!/bin/sh\nexec sleep 600\n")
		term, _, _ := runOnTerminal(t, "--feature", "Add a --json flag")
		term.waitFor(t, "Screening the request with Claude…")
	})
	if n := runsLeft(); n > 0 {
		t.Errorf("%d of orchestra's runs on a terminal still running after their test ended", n)
	}
}

// runsLeft counts the runs runOnTerminal started that haven't returned.
func runsLeft() int {
	stacks := make([]byte, 1<<16)
	for {
		n := runtime.Stack(stacks, true)
		if n < len(stacks) {
			stacks = stacks[:n]
			break
		}
		stacks = make([]byte, 2*len(stacks))
	}
	left := 0
	for g := range strings.SplitSeq(string(stacks), "\n\n") {
		if strings.Contains(g, "/cmd/orchestra.run(") && strings.Contains(g, "/cmd/orchestra.runOnTerminal in ") {
			left++
		}
	}
	return left
}
