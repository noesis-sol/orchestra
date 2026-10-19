package main

import (
	"os"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/gittest"
)

// TestMain runs the tests through faketool, for the fake tools they put on PATH, gittest, for the real
// git they run, withoutClaude, so that the claude they find is one they put on PATH, and withoutRuns, so
// that the runs they start don't register among the machine's.
func TestMain(m *testing.M) {
	os.Exit(faketool.Main(gittest.Main(withoutClaude(withoutRuns(m)))).Run())
}
