package git

import (
	"os"
	"testing"

	"github.com/noesis-sol/orchestra/internal/gittest"
)

// TestMain runs the tests through gittest, for the real git they run.
func TestMain(m *testing.M) { os.Exit(gittest.Main(m).Run()) }
