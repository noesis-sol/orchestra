package claude

import (
	"context"
	"os"

	"github.com/noesis-sol/orchestra/internal/command"
)

// CLI is the claude command, Bin, that workers run, asked what it takes.
type CLI struct{ Bin string }

// Help is claude --help, run outside any project, so no project's settings come into it.
func (c CLI) Help(ctx context.Context) (string, error) {
	return command.Output(ctx, command.ReadLimit, os.TempDir(), c.Bin, "--help")
}
