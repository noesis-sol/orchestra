package main

import (
	"os"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/gittest"
)

// TestMain runs the tests through faketool, for the fake tools they put on PATH, gittest, for the real
// git they run, and withoutClaude, so that the claude they find is one they put on PATH.
func TestMain(m *testing.M) { os.Exit(faketool.Main(gittest.Main(withoutClaude(m))).Run()) }
