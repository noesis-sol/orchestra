package tmux

import (
	"context"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
)

// A run's worker windows open in a tmux session: orchestra's own when it runs in tmux, or else one
// of its own, detached, which the maintainer attaches to to watch the workers.

// SessionName is name as a session name every tmux keeps as it is: '.' and ':' are turned into '_',
// as tmux 3.2 to 3.6 turn them (3.0, 3.1 and 3.7 refuse them; 3.8 keeps them), and so is ';', as
// orchestra gives no argument ending in one (see runWithInput). '#' stays: NewSession escapes it.
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

// NewSession returns the ID of the session named name, which it starts, detached, its first window
// in cwd, if tmux has none so named, starting the tmux server too if none runs. tmux before 3.8
// doesn't keep a name holding '.' or ':' (see SessionName): there the session is the one named as
// SessionName gives. The ID, not the name, is what to target the session by: tmux reads a target
// "my.sess" as window "my", pane "sess", and one holding ':' as a session and a window.
func (t Terminal) NewSession(ctx context.Context, name, cwd string) (string, error) {
	id, err := t.newSession(ctx, name, cwd)
	if err != nil && SessionName(name) != name {
		// tmux 3.2 to 3.6 made the session under the name SessionName gives, and now say it is a
		// duplicate; 3.0, 3.1 and 3.7 refuse the name.
		if id, serr := t.newSession(ctx, SessionName(name), cwd); serr == nil {
			return id, nil
		}
	}
	return id, err
}

// newSession is NewSession for a name tmux keeps as it is.
func (t Terminal) newSession(ctx context.Context, name, cwd string) (string, error) {
	if id := t.sessionID(ctx, name); id != "" {
		return id, nil
	}
	// tmux expands -s as a format, as it does -c.
	out, err := t.run(ctx, command.ReadLimit, "new-session", name,
		"-d", "-s", literal(name), "-c", literal(cwd), "-P", "-F", "#{session_id}")
	if err == nil {
		if id := strings.TrimSuffix(out, "\n"); isID(id, '$') {
			return id, nil
		}
		err = odd("new-session", name, out)
	}
	if id := t.sessionID(ctx, name); id != "" { // another orchestra, say, made it meanwhile
		return id, nil
	}
	return "", err
}

// sessionID returns the ID of the session named exactly name; "" when tmux has none, or can't say.
func (t Terminal) sessionID(ctx context.Context, name string) string {
	out, err := t.run(ctx, command.ReadLimit, "list-sessions", name, "-F", "#{session_id} #{session_name}")
	if err != nil {
		return ""
	}
	for line := range strings.Lines(out) {
		id, n, ok := strings.Cut(strings.TrimSuffix(line, "\n"), " ")
		if ok && n == name && isID(id, '$') {
			return id
		}
	}
	return ""
}
