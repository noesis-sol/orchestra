package cmux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// layout is what cmux has, in every window, as tree --all lists it: each workspace's title, and the
// workspace each terminal surface is in, all keyed by UUID (key).
type layout struct {
	titles     map[string]string // workspace → its title
	workspaces map[string]string // surface → its workspace
}

// treeAnswer is the part of tree --all --json that layout reads.
type treeAnswer struct {
	Windows []struct {
		Workspaces []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Panes []struct {
				Surfaces []struct {
					ID string `json:"id"`
				} `json:"surfaces"`
			} `json:"panes"`
		} `json:"workspaces"`
	} `json:"windows"`
}

// layout lists the workspaces and surfaces cmux has. One that isn't running has none, which is no
// error.
func (t Terminal) layout(ctx context.Context) (layout, error) {
	l := layout{titles: map[string]string{}, workspaces: map[string]string{}}
	out, err := t.run(ctx, "tree", "", true, "--all")
	if NoServer(err) {
		return l, nil
	}
	if err != nil {
		return l, err
	}
	var a treeAnswer
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		return l, odd("tree", "", out)
	}
	for _, w := range a.Windows {
		for _, ws := range w.Workspaces {
			if !isUUID(ws.ID) {
				return l, odd("tree", "", out)
			}
			l.titles[key(ws.ID)] = ws.Title
			for _, p := range ws.Panes {
				for _, s := range p.Surfaces {
					if !isUUID(s.ID) {
						return l, odd("tree", "", out)
					}
					l.workspaces[key(s.ID)] = key(ws.ID)
				}
			}
		}
	}
	return l, nil
}

// handles is the part of cmux's answers to workspace create and new-split that names what they made.
type handles struct {
	Workspace string `json:"workspace_id"`
	Surface   string `json:"surface_id"`
}

// CreateTab opens a workspace named label in orchestra's window, its terminal starting in cwd,
// without switching to it, and returns the workspace's UUID and its terminal's. The workspace keeps
// its name: cmux takes it for one the user gave, which neither the terminal's title nor cmux's own
// naming changes. cmux has no workspace in a workspace, so the workspace asked for is not used. The
// book keeps the workspace under its label, which is what lets orchestra close it later. A workspace
// opened whose answer can't be read, or that can't be kept in the book, is closed again.
func (t Terminal) CreateTab(ctx context.Context, _, cwd, label string) (tab, pane string, err error) {
	out, err := t.run(ctx, "workspace", label, true,
		"create", "--name", label, "--cwd", cwd, "--focus", "false")
	if err != nil {
		return "", "", err
	}
	var h handles
	if jerr := json.Unmarshal([]byte(out), &h); jerr != nil || !isUUID(h.Workspace) {
		return "", "", fmt.Errorf("%w; a workspace named %s may be left open", odd("workspace create", label, out), label)
	}
	tab = key(h.Workspace)
	if !isUUID(h.Surface) {
		err = odd("workspace create", label, out)
	} else {
		pane = key(h.Surface)
		err = t.edit(func(b *book) error {
			b.Workspaces[tab] = workspaceEntry{Label: label}
			b.Surfaces[pane] = surfaceEntry{Workspace: tab, Dir: cwd}
			return nil
		})
		if err == nil {
			// One cmux doesn't list yet would read as gone; a list that fails undoes nothing.
			_, _ = t.awaitLayout(ctx, false, func(l layout) bool { return l.workspaces[pane] == tab })
			return tab, pane, nil
		}
	}
	// Even once the run is stopped, the workspace is closed, with the limit of any cmux call.
	if cerr := t.closeWorkspace(context.WithoutCancel(ctx), tab, label); cerr != nil {
		return "", "", fmt.Errorf("%w; workspace %s, which cmux opened, could not be closed (%v)", err, tab, cerr)
	}
	return "", "", fmt.Errorf("%w; workspace %s, which cmux opened, is closed again", err, tab)
}

// CloseTab closes a workspace orchestra opened, ending what runs in it, and checks that cmux closed
// it and no other. A workspace that is already gone is no error. One orchestra didn't open, has
// since been renamed, or is orchestra's own, is left open, and the error says why.
func (t Terminal) CloseTab(ctx context.Context, tab string) error {
	if !isUUID(tab) {
		return missing("close-workspace", tab, "workspace")
	}
	b, err := t.view()
	if err != nil {
		return err
	}
	entry, ok := b.Workspaces[key(tab)]
	if ok {
		return t.closeWorkspace(ctx, tab, entry.Label)
	}
	label, open, err := t.TabLabel(ctx, tab)
	switch {
	case err != nil:
		return fmt.Errorf("orchestra didn't open workspace %s, which is left open as cmux can't say "+
			"whether it still is: %w", tab, err)
	case open:
		return &Error{Op: "close-workspace", Target: tab, Err: fmt.Errorf("orchestra didn't open workspace %q, "+
			"so it leaves it open", label)}
	}
	return nil // gone
}

// closeWorkspace closes workspace tab, as long as it is still named label, and checks with cmux's
// list that it went, and that every other workspace stayed. It drops the workspace from the book
// once it is gone.
func (t Terminal) closeWorkspace(ctx context.Context, tab, label string) error {
	if t.Own != "" && sameID(tab, t.Own) {
		return &Error{Op: "close-workspace", Target: tab, Err: errors.New("it is orchestra's own workspace")}
	}
	before, err := t.layout(ctx)
	if err != nil {
		return err
	}
	title, open := before.titles[key(tab)]
	switch {
	case !open:
		return t.forget(tab)
	case title != label:
		return &Error{Op: "close-workspace", Target: tab, Err: fmt.Errorf("it is named %q now, not %s, so "+
			"orchestra leaves it open", title, label)}
	}
	_, err = t.run(ctx, "close-workspace", tab, false, t.closeArgs(ctx, "--workspace", tab)...)
	after, lerr := t.awaitLayout(ctx, err != nil, func(l layout) bool {
		_, still := l.titles[key(tab)]
		return !still
	})
	if lerr != nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("cmux closed workspace %s, but could not say what is left open: %w", tab, lerr)
	}
	var lost []string
	for id, other := range before.titles {
		if _, still := after.titles[id]; !still && id != key(tab) {
			lost = append(lost, fmt.Sprintf("%s (%s)", id, other))
		}
	}
	if len(lost) > 0 {
		slices.Sort(lost)
		return &Error{Op: "close-workspace", Target: tab, Err: fmt.Errorf("cmux closed other workspaces too: %s",
			strings.Join(lost, ", "))}
	}
	if _, still := after.titles[key(tab)]; still {
		if err != nil {
			return err
		}
		return &Error{Op: "close-workspace", Target: tab, Err: errors.New("cmux still has the workspace")}
	}
	return t.forget(tab) // gone, whatever cmux answered
}

// closeArgs are the arguments of a close (close-workspace, close-surface), with --force where cmux
// has its input guard (features), which from 0.65.0 refuses to close a terminal a process runs in:
// orchestra closes only what it opened, to end what runs there.
func (t Terminal) closeArgs(ctx context.Context, args ...string) []string {
	if t.features(ctx).force {
		args = append(args, "--force")
	}
	return args
}

// settle is how long orchestra waits for cmux's list to show what it opened or closed: from 0.65.0,
// cmux answers before a new terminal is listed, and a forced close before the terminal it ends is
// gone.
var settle = 5 * time.Second

// awaitLayout lists what cmux has, again until done says the list shows what was asked of cmux, for
// up to settle, and returns the last list. With once, after a command that failed, it lists it
// once.
func (t Terminal) awaitLayout(ctx context.Context, once bool, done func(layout) bool) (layout, error) {
	deadline := time.Now().Add(settle)
	for {
		l, err := t.layout(ctx)
		if err != nil || once || done(l) || time.Now().After(deadline) {
			return l, err
		}
		select {
		case <-ctx.Done():
			return l, nil // what is left open is as the list says
		case <-time.After(poll / 2):
		}
	}
}

// forget drops workspace tab, and its surfaces, from the book.
func (t Terminal) forget(tab string) error {
	return t.edit(func(b *book) error {
		delete(b.Workspaces, key(tab))
		for id, e := range b.Surfaces {
			if sameID(e.Workspace, tab) {
				delete(b.Surfaces, id)
			}
		}
		return nil
	})
}

// FocusTab selects the workspace, bringing its window to the front, as the maintainer would by
// clicking it in cmux's sidebar.
func (t Terminal) FocusTab(ctx context.Context, tab string) error {
	if !isUUID(tab) {
		return missing("select-workspace", tab, "workspace")
	}
	_, err := t.run(ctx, "select-workspace", tab, false, "--workspace", tab)
	return err
}

// TabLabel returns the workspace's name, and false if cmux has no such workspace, which is no error,
// nor is cmux not running. A UUID names one workspace for good, but one cmux restored after a
// restart holds a shell, not the worker: what runs there is read by Status.
func (t Terminal) TabLabel(ctx context.Context, tab string) (label string, open bool, err error) {
	if !isUUID(tab) {
		return "", false, nil
	}
	l, err := t.layout(ctx)
	if err != nil {
		return "", false, err
	}
	label, open = l.titles[key(tab)]
	return label, open, nil
}

// SplitPane splits the surface, opening a terminal to its right that has the keyboard focus, and
// returns its UUID. What is launched there starts in cwd.
func (t Terminal) SplitPane(ctx context.Context, pane, cwd string) (string, error) {
	if !isUUID(pane) {
		return "", missing("new-split", pane, "surface")
	}
	l, err := t.layout(ctx)
	if err != nil {
		return "", err
	}
	ws, ok := l.workspaces[key(pane)]
	if !ok {
		return "", missing("new-split", pane, "surface")
	}
	out, err := t.run(ctx, "new-split", pane, true, "right", "--workspace", ws, "--surface", pane, "--focus", "true")
	if err != nil {
		return "", err
	}
	var h handles
	if err := json.Unmarshal([]byte(out), &h); err != nil || !isUUID(h.Surface) {
		return "", odd("new-split", pane, out)
	}
	split := key(h.Surface)
	if err := t.edit(func(b *book) error {
		b.Surfaces[split] = surfaceEntry{Workspace: ws, Dir: cwd, Split: true}
		return nil
	}); err != nil {
		if cerr := t.ClosePane(context.WithoutCancel(ctx), split); cerr != nil {
			return "", fmt.Errorf("%w; the terminal %s, which cmux opened, could not be closed (%v)", err, split, cerr)
		}
		return "", err
	}
	// One cmux doesn't list yet would read as closed; a list that fails undoes nothing.
	_, _ = t.awaitLayout(ctx, false, func(l layout) bool { _, ok := l.workspaces[split]; return ok })
	return split, nil
}

// ClosePane closes a terminal split off another (SplitPane), ending what runs in it, and checks
// that cmux closed it and no other terminal in its workspace. A terminal that is already gone,
// closed by the user say, is no error; one orchestra didn't split off is left open.
func (t Terminal) ClosePane(ctx context.Context, pane string) error {
	b, err := t.view()
	if err != nil {
		return err
	}
	entry, ok := b.Surfaces[key(pane)]
	if !isUUID(pane) || !ok || !entry.Split {
		return &Error{Op: "close-surface", Target: pane, Err: errors.New("orchestra didn't split that terminal off, " +
			"so it leaves it open")}
	}
	before, err := t.layout(ctx)
	if err != nil {
		return err
	}
	if _, open := before.workspaces[key(pane)]; !open {
		return t.forgetSurface(pane)
	}
	_, err = t.run(ctx, "close-surface", pane, false,
		t.closeArgs(ctx, "--workspace", entry.Workspace, "--surface", pane)...)
	after, lerr := t.awaitLayout(ctx, err != nil, func(l layout) bool {
		_, still := l.workspaces[key(pane)]
		return !still
	})
	if lerr != nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("cmux closed terminal %s, but could not say what is left open: %w", pane, lerr)
	}
	var lost []string
	for id, ws := range before.workspaces {
		if _, still := after.workspaces[id]; !still && id != key(pane) && ws == key(entry.Workspace) {
			lost = append(lost, id)
		}
	}
	if len(lost) > 0 {
		slices.Sort(lost)
		return &Error{Op: "close-surface", Target: pane, Err: fmt.Errorf("cmux closed other terminals too: %s",
			strings.Join(lost, ", "))}
	}
	if _, still := after.workspaces[key(pane)]; still {
		if err != nil {
			return err
		}
		return &Error{Op: "close-surface", Target: pane, Err: errors.New("cmux still has the terminal")}
	}
	return t.forgetSurface(pane)
}

// forgetSurface drops the surface from the book.
func (t Terminal) forgetSurface(pane string) error {
	return t.edit(func(b *book) error {
		delete(b.Surfaces, key(pane))
		return nil
	})
}

// PaneOpen reports whether cmux still has the surface; false is no error, nor is cmux not running.
func (t Terminal) PaneOpen(ctx context.Context, pane string) (bool, error) {
	l, err := t.layout(ctx)
	if err != nil {
		return false, err
	}
	_, ok := l.workspaces[key(pane)]
	return ok, nil
}

// readScreen reads the surface's screen: the visible screen, or else the last 60 lines with the
// scrollback, with the empty lines below its last one left out.
func (t Terminal) readScreen(ctx context.Context, workspace, surface string, visible bool) (string, error) {
	args := []string{"--workspace", workspace, "--surface", surface}
	if !visible {
		args = append(args, "--scrollback", "--lines", "60")
	}
	out, err := t.run(ctx, "read-screen", surface, false, args...)
	if err != nil {
		return "", err
	}
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return "", nil
	}
	return out + "\n", nil
}
