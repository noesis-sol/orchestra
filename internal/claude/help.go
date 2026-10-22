package claude

import (
	"context"
	"fmt"
	"os"

	"github.com/noesis-sol/orchestra/internal/command"
)

// CLI is the claude command, Bin, that workers run, asked what it takes.
type CLI struct{ Bin string }

// Help is claude --help, run outside any project, so no project's settings come into it. It runs in an
// empty directory of its own, made for the call and private to the user, never in the temporary directory
// itself: claude reads .claude/settings.json and settings.local.json in its working directory as project
// settings, and on Linux with TMPDIR unset that directory is the world-writable /tmp, where another user
// could leave settings that run a command of theirs.
func (c CLI) Help(ctx context.Context) (string, error) {
	dir, err := os.MkdirTemp("", "orchestra-help-") // mode 0700, under a name nobody had
	if err != nil {
		return "", fmt.Errorf("making a directory for claude --help: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }() // best effort: an empty directory left in TMPDIR harms nothing
	return command.Output(ctx, command.ReadLimit, dir, c.Bin, "--help")
}
