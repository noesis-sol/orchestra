package organ

import (
	"os"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// TestMain runs the tests through faketool, for the fake claudes they run.
func TestMain(m *testing.M) { os.Exit(faketool.Main(m).Run()) }
