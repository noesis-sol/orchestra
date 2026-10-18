package claude

import (
	"os"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// TestMain runs the tests through faketool, for the hooks they run: the test binary, which then
// doesn't sleep as it exits under -race.
func TestMain(m *testing.M) { os.Exit(faketool.Main(m).Run()) }
