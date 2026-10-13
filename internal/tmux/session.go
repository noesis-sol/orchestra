package tmux

import (
	"context"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
)

// A run's worker windows open in a tmux session: orchestra's own when it runs in tmux, or else one
// of its own, detached, which the maintainer attaches to to watch the workers.

// SessionName is name as tmux keeps a session's name: tmux turns '.' and ':' into '_', and orchestra
// gives no argument ending in ';' (see runWithInput), so it is turned into '_' too.
func SessionName(name string) string {
	return strings.NewReplacer(".", "_", ":", "_", ";", "_").Replace(name)
}

// PaneSession returns the name of the session the pane is in, such as orchestra's own ($TMUX_PANE).
func (t Terminal) PaneSession(ctx context.Context, pane string) (string, error) {
	if !isID(pane, '%') {
		return "", missing("display-message", pane, "pane")
	}
	out, err := t.run(ctx, command.ReadLimit, "display-message", pane, "-p", "-t", pane, "#{session_name}")
	if err != nil {
		return "", err
	}
	// tmux prints an empty line, and succeeds, for a pane it doesn't have.
	if name := strings.TrimSuffix(out, "\n"); name != "" && !strings.Contains(name, "\n") {
		return name, nil
	}
	if out == "" || out == "\n" {
		return "", missing("display-message", pane, "pane")
	}
	return "", odd("display-message", pane, out)
}

// NewSession starts a detached session named name, its first window in cwd, unless tmux has one so
// named already, and starts the tmux server if none runs. name is one SessionName gave.
func (t Terminal) NewSession(ctx context.Context, name, cwd string) error {
	if t.hasSession(ctx, name) {
		return nil
	}
	_, err := t.run(ctx, command.ReadLimit, "new-session", name, "-d", "-s", name, "-c", literal(cwd))
	if err != nil && t.hasSession(ctx, name) { // another orchestra, say, made it meanwhile
		return nil
	}
	return err
}

// hasSession reports whether tmux has a session named exactly name; false when it can't say.
func (t Terminal) hasSession(ctx context.Context, name string) bool {
	_, err := t.run(ctx, command.ReadLimit, "has-session", name, "-t", "="+name)
	return err == nil
}
