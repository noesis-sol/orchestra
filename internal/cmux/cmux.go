// Package cmux is orchestra's adapter for cmux (https://cmux.com), a macOS terminal for coding
// agents that a CLI controls through a socket: a worker's tab is a cmux workspace of its own, named
// with its ticket's ID, and its pane the workspace's terminal surface, where its Claude Code session
// runs. cmux can't tell what a session is doing, as Herdr reads it from the screen: the session's
// hooks tell it (claude.State), and cmux whether a claude process still runs in the surface.
//
// Every workspace and surface is addressed by its UUID (--id-format uuids), never by a short ref
// such as workspace:2: before cmux 0.65.0 (manaflow-ai/cmux#13633) short refs were numbered afresh
// on each start, and a ref kept in .orchestra/run/state.json could then name another workspace, the
// user's included. A surface is named with its workspace too, which cmux otherwise takes from
// $CMUX_WORKSPACE_ID, orchestra's own. close-surface closed another surface than the one asked for
// in cmux 0.64.22 (manaflow-ai/cmux#12524), so orchestra closes only workspaces it opened, still
// under their labels, and the terminal it split off its own for the interview, and checks each
// close against cmux's list of what is left.
package cmux

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// Terminal is cmux as orchestra uses it: workspaces for workers, launching Claude Code in their
// surfaces, and reading their state and screen. What it needs to find a worker again it keeps in
// its book (see book.go).
type Terminal struct {
	// Book is the path of the book of the workspaces and surfaces orchestra opened: BookName in the
	// main checkout's .orchestra/run/.
	Book string
	// Own is orchestra's own workspace (CMUX_WORKSPACE_ID), which is never closed; "" for none.
	Own string
}

// appBin is where the cmux app keeps its CLI, which is on the PATH only in cmux's own terminals.
var appBin = "/Applications/cmux.app/Contents/Resources/bin/cmux"

// Bin is the cmux CLI: cmux on the PATH, else the app's; "" when there is neither.
func Bin() string {
	if path, err := exec.LookPath("cmux"); err == nil {
		return path
	}
	if fi, err := os.Stat(appBin); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
		return appBin
	}
	return ""
}

// poll is how often a wait reads the worker's state again.
const poll = 250 * time.Millisecond

// pasteSettle is how long Prompt leaves Claude Code to take a paste in before pressing Enter, which
// it could otherwise take as part of the paste.
var pasteSettle = 300 * time.Millisecond

// Error is a cmux call that failed, or found no workspace, surface or agent to act on. Callers read
// its fields, never its text.
type Error struct {
	Op       string // the cmux command, such as new-workspace
	Target   string // the workspace, surface or agent it was for
	NoServer bool   // cmux isn't running: its socket isn't there, or refuses connections
	Missing  bool   // cmux has no such workspace or surface, or no such agent was launched
	Err      error  // the failed command (a *command.Error), or what was wrong with its answer
}

// Error is the command's own error, which names it with its arguments, or else what went wrong.
func (e *Error) Error() string {
	if _, ok := errors.AsType[*command.Error](e.Err); ok {
		return e.Err.Error()
	}
	if e.Target == "" {
		return fmt.Sprintf("cmux %s: %v", e.Op, e.Err)
	}
	return fmt.Sprintf("cmux %s %s: %v", e.Op, e.Target, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// NoServer reports whether err is a cmux call that failed because cmux isn't running.
func NoServer(err error) bool {
	var ce *Error
	return errors.As(err, &ce) && ce.NoServer
}

// Missing reports whether err says cmux has no such workspace, surface or agent.
func Missing(err error) bool {
	var ce *Error
	return errors.As(err, &ce) && ce.Missing
}

// missing is the error for a workspace, surface or agent that cmux doesn't have.
func missing(op, target, what string) error {
	return &Error{Op: op, Target: target, Missing: true, Err: errors.New("no such " + what)}
}

// odd is the error for an answer of cmux's that can't be read.
func odd(op, target, out string) error {
	return &Error{Op: op, Target: target, Err: fmt.Errorf("unexpected output: %q", strings.TrimSpace(out))}
}

// uuidPattern is a UUID as cmux prints one.
var uuidPattern = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)

// isUUID reports whether s is a UUID: the only handle orchestra gives cmux.
func isUUID(s string) bool { return uuidPattern.MatchString(s) }

// sameID reports whether two UUIDs are the same, whatever their case.
func sameID(a, b string) bool { return strings.EqualFold(a, b) }

// key is how the book and orchestra's lists key a UUID: in capitals, as cmux prints it.
func key(id string) string { return strings.ToUpper(id) }

// run runs the cmux command op for target, with args after op, and returns its stdout. Handles in
// its answers are UUIDs, and with json its answer is JSON.
func (t Terminal) run(ctx context.Context, op, target string, json bool, args ...string) (string, error) {
	bin := Bin()
	if bin == "" {
		return "", &Error{Op: op, Target: target, Err: errors.New("cmux is not installed")}
	}
	argv := []string{"--id-format", "uuids"}
	if json {
		argv = append(argv, "--json")
	}
	argv = append(append(argv, op), args...)
	// CMUX_QUIET keeps the legacy verbs (close-workspace, select-workspace) from noting on stderr,
	// on every call, that they are aliases now.
	out, err := command.OutputWithInput(ctx, command.ReadLimit, "", []string{"CMUX_QUIET=1"}, "", bin, argv...)
	if err == nil {
		return out, nil
	}
	e := &Error{Op: op, Target: target, Err: err}
	var ce *command.Error
	var ee *exec.ExitError
	if errors.As(err, &ce) && !ce.Stopped && errors.As(ce.Err, &ee) {
		e.NoServer = noServer(socketPath())
	}
	return out, e
}

// Ping reports whether cmux answers orchestra: it runs, and lets orchestra in, as it does only
// programs started in its own terminals.
func (t Terminal) Ping(ctx context.Context) error {
	_, err := t.run(ctx, "ping", "", false)
	return err
}

// socketPath is the path of cmux's socket, found as its CLI finds it.
func socketPath() string {
	for _, env := range []string{"CMUX_SOCKET_PATH", "CMUX_SOCKET"} {
		if path := os.Getenv(env); path != "" {
			return path
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "cmux", "cmux.sock")
}

// noServer reports whether nothing listens on the socket at path: there is no socket, or it refuses
// the connection, as one cmux left as it quit does. cmux's own message isn't read.
func noServer(path string) bool {
	if path == "" {
		return false
	}
	c, err := net.DialTimeout("unix", path, time.Second)
	if err == nil {
		_ = c.Close() // only connected to see that it can be
		return false
	}
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
}
