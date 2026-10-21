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
// escapes Claude Code writes change it. tmux 3.7 refuses a window name holding '.' or ':', as a child
// ticket's ID holds '.': there the window is named as SessionName gives, and keeps label in its
// option labelOption, which TabLabel reads. A window opened whose answer can't be read, or whose name
// can't be kept, is closed again.
func (t Terminal) CreateTab(ctx context.Context, session, cwd, label string) (tab, pane string, err error) {
	target := "=" + session + ":" // the session so named, not one whose name starts so; '.' and all
	if isID(session, '$') {
		target = session + ":"
	}
	newWindow := func(name string) (string, error) {
		return t.run(ctx, command.ReadLimit, "new-window", session,
			"-d", "-t", target, "-c", literal(cwd), "-n", literal(name), "-P", "-F", "#{window_id} #{pane_id}")
	}
	name := label
	out, err := newWindow(name)
	if err != nil && SessionName(label) != label {
		if o, rerr := newWindow(SessionName(label)); rerr == nil {
			name, out, err = SessionName(label), o, nil
		}
	}
	if err != nil {
		return "", "", err
	}
	f := strings.Fields(out)
	if len(f) == 2 && isID(f[0], '@') && isID(f[1], '%') {
		tab, pane = f[0], f[1]
		args := []string{"-w", "-t", tab, "automatic-rename", "off",
			";", "set-option", "-w", "-t", tab, "allow-rename", "off"}
		if name != label {
			args = append(args, ";", "set-option", "-w", "-t", tab, labelOption, label)
		}
		_, err = t.run(ctx, command.ReadLimit, "set-option", tab, args...)
		if err == nil {
			return tab, pane, nil
		}
	} else {
		err = odd("new-window", session, out)
		if len(f) == 0 || !isID(f[0], '@') {
			return "", "", fmt.Errorf("%w; a window named %s may be left open", err, name)
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

// labelOption is the window option holding the label of a window CreateTab couldn't name so.
const labelOption = "@orchestra_label"

// window returns the label of the window tab, its name unless labelOption holds another, and false
// if tmux has no such window.
func (t Terminal) window(ctx context.Context, tab string) (name string, open bool, err error) {
	out, err := t.run(ctx, command.ReadLimit, "list-windows", "", "-a", "-F",
		"#{window_id}\t#{?"+labelOption+",#{"+labelOption+"},#{window_name}}")
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
	return t.split(ctx, pane, cwd)
}

// SplitPaneAside splits the pane as SplitPane does, but leaves the keyboard focus where it is
// (split-window -d).
func (t Terminal) SplitPaneAside(ctx context.Context, pane, cwd string) (string, error) {
	return t.split(ctx, pane, cwd, "-d")
}

// FocusPane makes the pane its window's active one, which has the keyboard focus while the window is
// in view.
func (t Terminal) FocusPane(ctx context.Context, pane string) error {
	if !isID(pane, '%') {
		return missing("select-pane", pane, "pane")
	}
	_, err := t.run(ctx, command.ReadLimit, "select-pane", pane, "-t", pane)
	return err
}

// split splits the pane, opening a new one to its right that starts in cwd, with split-window's
// further flags, and returns its ID.
func (t Terminal) split(ctx context.Context, pane, cwd string, flags ...string) (string, error) {
	if !isID(pane, '%') {
		return "", missing("split-window", pane, "pane")
	}
	args := append([]string{"-h"}, flags...)
	out, err := t.run(ctx, command.ReadLimit, "split-window", pane,
		append(args, "-t", pane, "-c", literal(cwd), "-P", "-F", "#{pane_id}")...)
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
