package cmux

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/claude"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// launchDir is the folder beside the book where a surface's launch script, and the process ID of
// what it launched, are written, named with the surface's UUID.
const launchDir = "cmux-launch"

// launchPath is the file of surface's launch with the extension ext: .sh for its script, .pid for
// the process ID of what it launched.
func (t Terminal) launchPath(surface, ext string) string {
	return filepath.Join(filepath.Dir(t.Book), launchDir, key(surface)+ext)
}

// launch runs kind with args in the surface and returns at once. cmux types into a surface's
// shell, which takes no argument with line breaks, so the command goes in a script beside the book,
// and the shell is given a line that runs it. The script goes to the folder the surface started in,
// sets orchestra's PATH, as orchestra would run the command, writes its process ID, which the
// command keeps, and replaces itself with the command. The shell stays, so the surface is still
// there, with its screen, once the command exits. With a name, the agent is given it.
//
// In its terminals cmux puts a claude of its own first on the PATH, which adds cmux's hooks to the
// session's settings and drops those it is given when it can't merge them: orchestra's hooks tell
// it what a worker does, so cmux's are turned off (CMUX_CLAUDE_HOOKS_DISABLED).
func (t Terminal) launch(ctx context.Context, surface, kind, name string, args []string) error {
	b, err := t.view()
	if err != nil {
		return err
	}
	entry, ok := b.Surfaces[key(surface)]
	if !isUUID(surface) || !ok {
		return missing("send", surface, "terminal orchestra opened")
	}
	var script strings.Builder
	script.WriteString("# orchestra's launch of " + kind + " in cmux surface " + key(surface) + "\n")
	if entry.Dir != "" {
		script.WriteString("cd " + command.ShellQuote(entry.Dir) + " || exit\n")
	}
	script.WriteString("PATH=" + command.ShellQuote(os.Getenv("PATH")) + "\nexport PATH\n")
	script.WriteString("CMUX_CLAUDE_HOOKS_DISABLED=1\nexport CMUX_CLAUDE_HOOKS_DISABLED\n")
	script.WriteString("echo $$ > " + command.ShellQuote(t.launchPath(surface, ".pid")) + "\n")
	script.WriteString("exec " + command.ShellQuote(kind))
	for _, a := range args {
		script.WriteString(" " + command.ShellQuote(a))
	}
	script.WriteString("\n")
	path := t.launchPath(surface, ".sh")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("cannot write the command for cmux surface %s: %w", surface, err)
	}
	// The process ID of an earlier launch would stand for this one until the script writes its own.
	if err := os.Remove(t.launchPath(surface, ".pid")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cannot clear the last launch in cmux surface %s: %w", surface, err)
	}
	if err := os.WriteFile(path, []byte(script.String()), 0o600); err != nil {
		return fmt.Errorf("cannot write the command for cmux surface %s: %w", surface, err)
	}
	records, _ := claude.RecordsOf(args) // "" when launched without hooks
	if err := t.edit(func(b *book) error {
		e := b.Surfaces[key(surface)]
		e.Kind, e.Records = kind, records
		b.Surfaces[key(surface)] = e
		if name != "" {
			setName(b, surface, name)
		}
		return nil
	}); err != nil {
		return err
	}
	// A leading space keeps the line out of the shell's history, where the shell is set to.
	return t.typeLine(ctx, entry.Workspace, surface, " /bin/sh "+command.ShellQuote(path))
}

// launched returns the process ID the surface's last launch wrote, and false when it wrote none:
// the script hasn't run yet, or nothing was launched.
func (t Terminal) launched(surface string) (int, bool) {
	b, err := os.ReadFile(t.launchPath(surface, ".pid"))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid, err == nil && pid > 0
}

// typeLine types line into the surface of workspace and presses Enter.
func (t Terminal) typeLine(ctx context.Context, workspace, surface, line string) error {
	if err := t.typeText(ctx, workspace, surface, line); err != nil {
		return err
	}
	return t.pressKey(ctx, workspace, surface, "enter")
}

// typeText types text into the surface of workspace, as it is: cmux would take \n, \r and \t in it
// for Enter and Tab, so text holding a backslash or a line break is refused.
func (t Terminal) typeText(ctx context.Context, workspace, surface, text string) error {
	if !typeable(text) {
		return &Error{Op: "send", Target: surface, Err: errors.New("text with a line break or a backslash can't " +
			"be typed as it is")}
	}
	_, err := t.run(ctx, "send", surface, false, t.writeArgs(ctx, workspace, surface, "--", text)...)
	return err
}

// writeArgs are the arguments of a write into the surface of workspace (send, send-key, paste),
// followed by rest: with --force where cmux has its input guard (features), which would refuse to
// write over a draft in the agent's prompt or into a dialog it has open. The surfaces are
// orchestra's, and what it writes there, a worker's prompt or the key that answers a dialog, has to
// go through, as it does in Herdr and tmux.
func (t Terminal) writeArgs(ctx context.Context, workspace, surface string, rest ...string) []string {
	args := []string{"--workspace", workspace, "--surface", surface}
	if t.features(ctx).force {
		args = append(args, "--force")
	}
	return append(args, rest...)
}

// paste pastes text into the surface of workspace as Cmd+V does (cmux paste, from its stdin), which
// a program that asked for bracketed paste, as Claude Code does, takes in as one paste, its lines
// together. cmux puts a space for each control character that could act on the terminal, ESC say.
func (t Terminal) paste(ctx context.Context, workspace, surface, text string) error {
	_, err := t.runWithInput(ctx, "paste", surface, false, text, t.writeArgs(ctx, workspace, surface, "-")...)
	return err
}

// typeable reports whether cmux types text as it is: one line, without a backslash, which cmux would
// read \n, \r and \t after.
func typeable(text string) bool { return !strings.ContainsAny(text, "\\\n\r") }

// pressKey presses one key, by cmux's name for it, in the surface of workspace.
func (t Terminal) pressKey(ctx context.Context, workspace, surface, k string) error {
	_, err := t.run(ctx, "send-key", surface, false, t.writeArgs(ctx, workspace, surface, "--", k)...)
	return err
}

// setName names the agent in surface name, taking the name from any other surface that holds it, as
// Herdr's agent names are unique.
func setName(b *book, surface, name string) {
	for id, e := range b.Surfaces {
		if e.Agent == name && !sameID(id, surface) {
			e.Agent = ""
			b.Surfaces[id] = e
		}
	}
	e := b.Surfaces[key(surface)]
	e.Agent = name
	b.Surfaces[key(surface)] = e
}

// LaunchInPane runs '<kind> <args…>' in the surface and returns at once; AdoptAgent names it.
func (t Terminal) LaunchInPane(ctx context.Context, pane, kind string, args []string) error {
	return t.launch(ctx, pane, kind, "", args)
}

// StartAgent runs kind with args in the surface, names the agent name and waits up to a minute for it
// to be ready for a prompt (idle). An error says it went away or never got ready.
func (t Terminal) StartAgent(ctx context.Context, name, kind, pane string, args []string) error {
	if err := t.launch(ctx, pane, kind, name, args); err != nil {
		return err
	}
	return t.waitFor(ctx, "start", name, time.Minute, dispatch.StateIdle, dispatch.StateDone)
}

// IsArgumentRefused is always false: the script passes any argument.
func (t Terminal) IsArgumentRefused(error) bool { return false }

// IsNameRefused is always false: an agent's name is kept in the book, which takes any name.
func (t Terminal) IsNameRefused(error) bool { return false }

// AgentName returns ticket id's worker's agent name: the ID itself, as cmux has no rule for names.
func (t Terminal) AgentName(id string) string { return id }
