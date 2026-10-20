package main

import (
	"os"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/gittest"
)

// TestMain runs the tests through faketool, for the fake tools they put on PATH, gittest, for the real
// git they run, faketool.OnlyTools, so that every tool they find but git is one they put on PATH, as on
// a machine without claude, bd, brew, tmux, herdr or cmux, such as the release's runner, and
// withoutRuns, so that the runs they start don't register among the machine's.
func TestMain(m *testing.M) {
	os.Exit(faketool.Main(gittest.Main(faketool.OnlyTools(withoutRuns(m), "git"))).Run())
}
