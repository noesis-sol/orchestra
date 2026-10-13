package cmux

import (
	"os"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// TestMain runs the tests through faketool, for the fake cmux they put on PATH.
func TestMain(m *testing.M) { os.Exit(faketool.Main(m).Run()) }
