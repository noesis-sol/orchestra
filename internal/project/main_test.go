package project

import (
	"os"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/gittest"
)

// TestMain runs the tests through faketool, for the fake tools they put on PATH, and gittest, for the real
// git they run.
func TestMain(m *testing.M) { os.Exit(faketool.Main(gittest.Main(m)).Run()) }
