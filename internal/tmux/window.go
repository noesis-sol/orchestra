package tmux

import (
	"context"
	"fmt"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
)

// CreateTab opens a window named label in session, starting in cwd, without switching to it, and
// returns the window's ID and its pane's. session is the session's ID (NewSession), or its name,
// which must hold no ':'. The window keeps its name: neither tmux's automatic renaming nor the title
// escapes Claude Code writes change it. A window opened whose answer can't be read, or whose name
// can't be kept, is closed again.
func (t Terminal) CreateTab(ctx context.Context, session, cwd, label string) (tab, pane string, err error) {
	target := "=" + session + ":" // the session so named, not one whose name starts so; '.' and all
	if isID(session, '$') {
		target = session + ":"
	}
	out, err := t.run(ctx, command.ReadLimit, "new-window", session,
		"-d", "-t", target, "-c", literal(cwd), "-n", literal(label), "-P", "-F", "#{window_id} #{pane_id}")
	if err != nil {
		return "", "", err
	}
	f := strings.Fields(out)
	if len(f) == 2 && isID(f[0], '@') && isID(f[1], '%') {
		tab, pane = f[0], f[1]
		_, err = t.run(ctx, command.ReadLimit, "set-option", tab,
			"-w", "-t", tab, "automatic-rename", "off", ";", "set-option", "-w", "-t", tab, "allow-rename", "off")
		if err == nil {
			return tab, pane, nil
		}
	} else {
		err = odd("new-window", session, out)
		if len(f) == 0 || !isID(f[0], '@') {
			return "", "", fmt.Errorf("%w; a window named %s may be left open", err, label)
		}
		tab = f[0]
	}
	// Even once the run is stopped, the window is closed, with the limit of any tmux call.
	if cerr := t.CloseTab(context.WithoutCancel(ctx), tab); cerr != nil {
		return "", "", fmt.Errorf("%w; window %s, which tmux opened, could not be closed (%v)", err, tab, cerr)
	}
	return "", "", fmt.Errorf("%w; window %s, which tmux opened, is closed again", err, tab)
}

// CloseTab closes a window, ending what runs in it. A window that is already gone is no error.
func (t Terminal) CloseTab(ctx context.Context, tab string) error {
	if !isID(tab, '@') {
		return missing("kill-window", tab, "window")
	}
	_, err := t.run(ctx, command.ReadLimit, "kill-window", tab, "-t", tab)
	if err == nil {
		return nil
	}
	if _, open, lerr := t.window(ctx, tab); lerr == nil && !open {
		return nil
	}
	return err
}

// FocusTab makes the window its session's current one, as the maintainer would by selecting it.
func (t Terminal) FocusTab(ctx context.Context, tab string) error {
	if !isID(tab, '@') {
		return missing("select-window", tab, "window")
	}
	_, err := t.run(ctx, command.ReadLimit, "select-window", tab, "-t", tab)
	return err
}

// TabLabel returns the window's name, and false if tmux has no such window, which is no error, nor
// is no tmux server running. tmux numbers windows afresh when its server starts again, so an ID
// recorded before may by then name another window: its name tells them apart.
func (t Terminal) TabLabel(ctx context.Context, tab string) (label string, open bool, err error) {
	if !isID(tab, '@') {
		return "", false, nil
	}
	return t.window(ctx, tab)
}

// window returns the name of the window tab, and false if tmux has no such window.
func (t Terminal) window(ctx context.Context, tab string) (name string, open bool, err error) {
	out, err := t.run(ctx, command.ReadLimit, "list-windows", "", "-a", "-F", "#{window_id}\t#{window_name}")
	if NoServer(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	for line := range lines(out) {
		id, name, ok := strings.Cut(line, "\t")
		if !ok || !isID(id, '@') {
			return "", false, odd("list-windows", "", out)
		}
		if id == tab {
			return name, true, nil
		}
	}
	return "", false, nil
}

// SplitPane splits the pane, opening a new one to its right that starts in cwd and has the keyboard
// focus, and returns the new pane's ID.
func (t Terminal) SplitPane(ctx context.Context, pane, cwd string) (string, error) {
	if !isID(pane, '%') {
		return "", missing("split-window", pane, "pane")
	}
	out, err := t.run(ctx, command.ReadLimit, "split-window", pane,
		"-h", "-t", pane, "-c", literal(cwd), "-P", "-F", "#{pane_id}")
	if err != nil {
		return "", err
	}
	if id := strings.TrimSpace(out); isID(id, '%') {
		return id, nil
	}
	return "", odd("split-window", pane, out)
}

// ClosePane closes a pane, ending what runs in it. A pane that is already gone, closed by the user
// say, is no error.
func (t Terminal) ClosePane(ctx context.Context, pane string) error {
	if !isID(pane, '%') {
		return missing("kill-pane", pane, "pane")
	}
	_, err := t.run(ctx, command.ReadLimit, "kill-pane", pane, "-t", pane)
	if err == nil {
		return nil
	}
	if open, lerr := t.PaneOpen(ctx, pane); lerr == nil && !open {
		return nil
	}
	return err
}

// PaneOpen reports whether tmux still has the pane; false is no error, nor is no tmux server running.
// A pane whose process exited stays open, dead, for its screen to be read.
func (t Terminal) PaneOpen(ctx context.Context, pane string) (bool, error) {
	ps, err := t.panes(ctx)
	if err != nil {
		return false, err
	}
	_, ok := find(ps, func(p paneInfo) bool { return p.id == pane })
	return ok, nil
}
