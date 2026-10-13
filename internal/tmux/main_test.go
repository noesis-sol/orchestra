package tmux

import (
	"testing"

	"go.uber.org/goleak"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// TestMain runs the tests through faketool, for the fake tmux and claude they put on PATH, and fails
// them if a goroutine one of them started is still running once they have all returned.
func TestMain(m *testing.M) { goleak.VerifyTestMain(faketool.Main(m)) }
