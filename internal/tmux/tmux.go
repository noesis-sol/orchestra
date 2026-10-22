// Package tmux is orchestra's adapter for tmux, the terminal workers run in where Herdr isn't: a
// worker's tab is a tmux window, named with its ticket's ID, and its agent the Claude Code session
// launched in the window's pane. tmux can't tell what a session is doing, as Herdr reads it from the
// screen: the session's hooks tell it (hookterm.AgentUI), and tmux whether its process is still there.
package tmux

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/hookterm"
)

// Terminal is tmux as orchestra uses it: windows for workers, launching Claude Code in their panes,
// and reading their state and screen. It keeps nothing itself: what it needs to find a worker again
// (its agent name, kind and records folder) is kept on the worker's pane as pane options, so a later
// run finds it there too.
type Terminal struct {
	// Socket names the tmux server (tmux -L); "" is the one tmux picks: the server orchestra runs in
	// ($TMUX), or else the default one.
	Socket string
	// Repo is the repository whose workers these are. An agent's name is its own only among the panes
	// of one repository's runs, as two runs on one tmux server, in two clones sharing a Beads database
	// say, can have workers of the same name; each pane it names is marked with it.
	Repo string

	// Names takes any agent name and argument: an agent's name is a pane option, and tmux passes any
	// argument.
	hookterm.Names
}

// The pane options a worker's pane carries. tmux has no agents: a pane holds one once orchestra
// launched one there (kind), and has its name once orchestra gave it one.
const (
	agentOption   = "@orchestra-agent"   // the agent's name, the ticket's ID
	kindOption    = "@orchestra-kind"    // the kind launched there, such as claude
	recordsOption = "@orchestra-records" // the folder its hooks write their records to (AgentUI.Records)
	repoOption    = "@orchestra-repo"    // the repository of the run that named it (Terminal.Repo)
	pidOption     = "@orchestra-pid"     // the process ID of what orchestra launched there, its pane_pid then
)

// tool names tmux in its errors.
const tool = "tmux"

// Error is a tmux call that failed, or found no window, pane or agent to act on. Callers read its
// fields, never its text.
type Error = hookterm.Error

// NoServer reports whether err is a tmux call that failed because no tmux server runs, so there are
// no windows or panes at all.
func NoServer(err error) bool { return hookterm.NoServer(err) }

// Missing reports whether err says tmux has no such window, pane or agent.
func Missing(err error) bool { return hookterm.Missing(err) }

// missing is the error for a window, pane or agent that tmux doesn't have.
func missing(op, target, what string) error { return hookterm.NoSuch(tool, op, target, what) }

// odd is the error for an answer of tmux's that can't be read.
func odd(op, target, out string) error { return hookterm.Odd(tool, op, target, out) }

// run runs the tmux command op for target, with args after op, and returns its stdout.
func (t Terminal) run(ctx context.Context, limit time.Duration, op, target string, args ...string) (string, error) {
	return t.runWithInput(ctx, limit, op, target, "", args...)
}

// runWithInput is run with input on tmux's stdin. Several commands can be given at once, separated by
// ";" arguments, op naming the first.
func (t Terminal) runWithInput(ctx context.Context, limit time.Duration, op, target, input string,
	args ...string) (string, error) {
	argv := []string{op}
	for _, a := range args {
		// tmux takes an argument ending in ';' for the end of a command, and unescapes one ending in
		// '\;' in its own way: none of the arguments orchestra gives it should end so.
		if a != ";" && strings.HasSuffix(a, ";") {
			return "", &Error{Tool: tool, Op: op, Target: target, Err: fmt.Errorf("an argument ends in ';', which tmux "+
				"takes for the end of a command: %q", a)}
		}
		argv = append(argv, a)
	}
	if t.Socket != "" {
		argv = append([]string{"-L", t.Socket}, argv...)
	}
	// -u: outside tmux, a client counts as UTF-8 only where $LC_ALL, $LC_CTYPE or $LANG says so, and the
	// server writes every other character of what it lists as '_', a repository's path among them. Under
	// cron or launchd none may be set.
	argv = append([]string{"-u"}, argv...)
	out, err := command.OutputWithInput(ctx, limit, "", nil, input, "tmux", argv...)
	if err == nil {
		return out, nil
	}
	e := &Error{Tool: tool, Op: op, Target: target, Err: err}
	var ce *command.Error
	var ee *exec.ExitError
	if errors.As(err, &ce) && !ce.Stopped && errors.As(ce.Err, &ee) {
		e.NoServer = noServer(t.socketPath(ctx))
	}
	return out, e
}

// socketPath is the path of the socket of the tmux server t talks to, found as tmux finds it, in
// the environment of the commands run with ctx (command.Getenv).
func (t Terminal) socketPath(ctx context.Context) string {
	if t.Socket == "" {
		if env := command.Getenv(ctx, "TMUX"); env != "" {
			path, _, _ := strings.Cut(env, ",")
			return path
		}
	}
	dir := command.Getenv(ctx, "TMUX_TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	name := t.Socket
	if name == "" {
		name = "default"
	}
	return filepath.Join(dir, "tmux-"+strconv.Itoa(os.Getuid()), name)
}

// noServer reports whether no tmux server listens on the socket at path: there is no socket, or it
// refuses the connection, as a server that exited leaves it. tmux's own message isn't read.
func noServer(path string) bool { return hookterm.Probe(path) == hookterm.SocketGone }

// literal escapes s for an argument tmux expands as a format (#{…}), such as a window's name or a
// start directory, so that it arrives as it is.
func literal(s string) string { return strings.ReplaceAll(s, "#", "##") }

// isID reports whether s is a tmux ID of the kind prefix says: $ for a session, @ for a window, % for
// a pane.
func isID(s string, prefix byte) bool {
	if len(s) < 2 || s[0] != prefix {
		return false
	}
	_, err := strconv.ParseUint(s[1:], 10, 32)
	return err == nil
}

// lines are the lines of a list tmux printed, none if it printed nothing.
func lines(out string) iter.Seq[string] {
	if out == "" {
		return func(func(string) bool) {}
	}
	return strings.SplitSeq(strings.TrimSuffix(out, "\n"), "\n")
}
