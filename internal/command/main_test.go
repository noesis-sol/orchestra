package command_test

import (
	"os"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// TestMain runs the tests through faketool, for the test binary they start as orchestra: it then
// doesn't sleep as it exits under -race. It is in a package of its own, as faketool imports command.
func TestMain(m *testing.M) { os.Exit(faketool.Main(m).Run()) }
