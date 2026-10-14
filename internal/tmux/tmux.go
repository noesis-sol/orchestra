// Package tmux is orchestra's adapter for tmux, the terminal workers run in where Herdr isn't: a
// worker's tab is a tmux window, named with its ticket's ID, and its agent the Claude Code session
// launched in the window's pane. tmux can't tell what a session is doing, as Herdr reads it from the
// screen: the session's hooks tell it (claude.State), and tmux whether its process is still there.
package tmux

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/noesis-sol/orchestra/internal/claude"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
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
}

// The pane options a worker's pane carries. tmux has no agents: a pane holds one once orchestra
// launched one there (kind), and has its name once orchestra gave it one.
const (
	agentOption   = "@orchestra-agent"   // the agent's name, the ticket's ID
	kindOption    = "@orchestra-kind"    // the kind launched there, such as claude
	recordsOption = "@orchestra-records" // the folder its hooks write their records to (claude.Records)
	repoOption    = "@orchestra-repo"    // the repository of the run that named it (Terminal.Repo)
)

// poll is how often a wait reads the worker's state again.
const poll = 250 * time.Millisecond

// pasteSettle is how long Prompt leaves Claude Code to take a paste in before pressing Enter, which
// it could otherwise take as part of the paste.
var pasteSettle = 300 * time.Millisecond

// Error is a tmux call that failed, or found no window, pane or agent to act on. Callers read its
// fields, never its text.
type Error struct {
	Op       string // the tmux command, such as new-window
	Target   string // the session, window, pane or agent it was for
	NoServer bool   // no tmux server runs on the socket, so there are no windows or panes at all
	Missing  bool   // tmux has no such window, pane or agent
	Err      error  // the failed command (a *command.Error), or what was wrong with its answer
}

// Error is the command's own error, which names it with its arguments, or else what went wrong.
func (e *Error) Error() string {
	if _, ok := errors.AsType[*command.Error](e.Err); ok {
		return e.Err.Error()
	}
	if e.Target == "" {
		return fmt.Sprintf("tmux %s: %v", e.Op, e.Err)
	}
	return fmt.Sprintf("tmux %s %s: %v", e.Op, e.Target, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// NoServer reports whether err is a tmux call that failed because no tmux server runs.
func NoServer(err error) bool {
	var te *Error
	return errors.As(err, &te) && te.NoServer
}

// Missing reports whether err says tmux has no such window, pane or agent.
func Missing(err error) bool {
	var te *Error
	return errors.As(err, &te) && te.Missing
}

// missing is the error for a window, pane or agent that tmux doesn't have.
func missing(op, target, what string) error {
	return &Error{Op: op, Target: target, Missing: true, Err: errors.New("no such " + what)}
}

// odd is the error for an answer of tmux's that can't be read.
func odd(op, target, out string) error {
	return &Error{Op: op, Target: target, Err: fmt.Errorf("unexpected output: %q", strings.TrimSpace(out))}
}

// run runs the tmux command op for target, with args after op, and returns its stdout.
func (t Terminal) run(ctx context.Context, limit time.Duration, op, target string, args ...string) (string, error) {
	return t.runWithInput(ctx, limit, op, target, nil, args...)
}

// runWithInput is run with input on tmux's stdin (nil for none). Several commands can be given at
// once, separated by ";" arguments, op naming the first.
func (t Terminal) runWithInput(ctx context.Context, limit time.Duration, op, target string, input *string,
	args ...string) (string, error) {
	argv := []string{op}
	for _, a := range args {
		// tmux takes an argument ending in ';' for the end of a command, and unescapes one ending in
		// '\;' in its own way: none of the arguments orchestra gives it should end so.
		if a != ";" && strings.HasSuffix(a, ";") {
			return "", &Error{Op: op, Target: target, Err: fmt.Errorf("an argument ends in ';', which tmux "+
				"takes for the end of a command: %q", a)}
		}
		argv = append(argv, a)
	}
	if t.Socket != "" {
		argv = append([]string{"-L", t.Socket}, argv...)
	}
	var out string
	var err error
	if input == nil {
		out, err = command.Output(ctx, limit, "", "tmux", argv...)
	} else {
		out, err = command.OutputWithInput(ctx, limit, "", nil, *input, "tmux", argv...)
	}
	if err == nil {
		return out, nil
	}
	e := &Error{Op: op, Target: target, Err: err}
	var ce *command.Error
	var ee *exec.ExitError
	if errors.As(err, &ce) && !ce.Stopped && errors.As(ce.Err, &ee) {
		e.NoServer = noServer(t.socketPath())
	}
	return out, e
}

// socketPath is the path of the socket of the tmux server t talks to, found as tmux finds it.
func (t Terminal) socketPath() string {
	if t.Socket == "" {
		if env := os.Getenv("TMUX"); env != "" {
			path, _, _ := strings.Cut(env, ",")
			return path
		}
	}
	dir := os.Getenv("TMUX_TMPDIR")
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
func noServer(path string) bool {
	c, err := net.DialTimeout("unix", path, time.Second)
	if err == nil {
		_ = c.Close() // only connected to see that it can be
		return false
	}
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
}

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

// paneInfo is a pane as tmux lists it, with the options orchestra gave it.
type paneInfo struct {
	id, window   string
	dead         bool   // its process exited (remain-on-exit keeps it open)
	agent, kind  string // the agent's name and kind; kind "" if no agent was launched there
	records      string // where its hooks write their records; "" if it was launched without them
	repo         string // the repository of the run that named its agent; "" if none, or an older orchestra did
	command, dir string // its foreground process's name, and that process's working directory
}

// paneFormat lists a pane for panes, its fields in the order panes reads them.
const paneFormat = "#{pane_id}\t#{window_id}\t#{pane_dead}\t#{" + agentOption + "}\t#{" + kindOption + "}\t#{" +
	recordsOption + "}\t#{" + repoOption + "}\t#{pane_current_command}\t#{pane_current_path}"

// panes lists every pane tmux has, in every session: none when no tmux server runs.
func (t Terminal) panes(ctx context.Context) ([]paneInfo, error) {
	out, err := t.run(ctx, command.ReadLimit, "list-panes", "", "-a", "-F", paneFormat)
	if NoServer(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ps []paneInfo
	for line := range lines(out) {
		f := strings.SplitN(line, "\t", 9)
		if len(f) != 9 || !isID(f[0], '%') || !isID(f[1], '@') || f[2] != "0" && f[2] != "1" {
			return nil, odd("list-panes", "", out)
		}
		ps = append(ps, paneInfo{id: f[0], window: f[1], dead: f[2] == "1", agent: f[3], kind: f[4], records: f[5],
			repo: f[6], command: f[7], dir: f[8]})
	}
	return ps, nil
}

// lines are the lines of a list tmux printed, none if it printed nothing.
func lines(out string) iter.Seq[string] {
	if out == "" {
		return func(func(string) bool) {}
	}
	return strings.SplitSeq(strings.TrimSuffix(out, "\n"), "\n")
}

// find returns the first of ps that match is true of.
func find(ps []paneInfo, match func(paneInfo) bool) (paneInfo, bool) {
	if i := slices.IndexFunc(ps, match); i >= 0 {
		return ps[i], true
	}
	return paneInfo{}, false
}

// ours reports whether the pane's agent name is one of t's repository's: the pane was named for it,
// or by an older orchestra, which marked none, as a run it was stopped in left it.
func (t Terminal) ours(p paneInfo) bool { return p.repo == t.Repo || p.repo == "" }

// named returns the pane of ps whose agent is named name in t's repository, and false if none is: one
// named for the repository before one an orchestra that marked none named.
func (t Terminal) named(ps []paneInfo, name string) (paneInfo, bool) {
	if p, ok := find(ps, func(p paneInfo) bool { return p.kind != "" && p.agent == name && p.repo == t.Repo }); ok {
		return p, true
	}
	return find(ps, func(p paneInfo) bool { return p.kind != "" && p.agent == name && t.ours(p) })
}

// agentPane returns the pane of the agent named name, and false if no pane of t's repository holds an
// agent so named.
func (t Terminal) agentPane(ctx context.Context, name string) (paneInfo, bool, error) {
	ps, err := t.panes(ctx)
	if err != nil {
		return paneInfo{}, false, err
	}
	p, ok := t.named(ps, name)
	return p, ok, nil
}

// launch runs kind with args in the pane, in place of what ran there, and returns at once. The
// command runs with orchestra's PATH, as orchestra would run it, through no shell's line editor, so
// an argument may be of any length and span lines. The pane stays open when it exits, for its
// screen to be read. With a name, the agent is given it.
func (t Terminal) launch(ctx context.Context, pane, kind, name string, args []string) error {
	if !isID(pane, '%') {
		return missing("respawn-pane", pane, "pane")
	}
	var script strings.Builder
	script.WriteString("exec " + command.ShellQuote(kind))
	for _, a := range args {
		script.WriteString(" " + command.ShellQuote(a))
	}
	opts := []string{"-p", "-t", pane, kindOption, kind, ";", "set-option", "-p", "-t", pane, "remain-on-exit", "on"}
	if records, ok := claude.RecordsOf(args); ok {
		opts = append(opts, ";", "set-option", "-p", "-t", pane, recordsOption, records)
	} else {
		opts = append(opts, ";", "set-option", "-p", "-u", "-t", pane, recordsOption)
	}
	if _, err := t.run(ctx, command.ReadLimit, "set-option", pane, opts...); err != nil {
		return err
	}
	if name != "" {
		if err := t.setName(ctx, pane, name); err != nil {
			return err
		}
	}
	_, err := t.run(ctx, command.ReadLimit, "respawn-pane", pane,
		"-k", "-t", pane, "-e", "PATH="+os.Getenv("PATH"), "/bin/sh", "-c", script.String())
	return err
}

// setName names the agent in the pane name, for t's repository, taking the name from any other pane
// of the repository's that holds it, as Herdr's agent names are unique. Another repository's panes
// keep theirs.
func (t Terminal) setName(ctx context.Context, pane, name string) error {
	ps, err := t.panes(ctx)
	if err != nil {
		return err
	}
	var args []string
	for _, p := range ps {
		if p.agent == name && p.id != pane && t.ours(p) {
			args = append(args, "-p", "-u", "-t", p.id, agentOption, ";", "set-option")
		}
	}
	args = append(args, "-p", "-t", pane, agentOption, name)
	if t.Repo != "" {
		args = append(args, ";", "set-option", "-p", "-t", pane, repoOption, t.Repo)
	}
	_, err = t.run(ctx, command.ReadLimit, "set-option", pane, args...)
	return err
}

// LaunchInPane runs '<kind> <args…>' in the pane and returns at once; AdoptAgent names it.
func (t Terminal) LaunchInPane(ctx context.Context, pane, kind string, args []string) error {
	return t.launch(ctx, pane, kind, "", args)
}

// StartAgent runs kind with args in the pane, names the agent name and waits up to a minute for it to
// be ready for a prompt (idle). An error says it went away or never got ready.
func (t Terminal) StartAgent(ctx context.Context, name, kind, pane string, args []string) error {
	if err := t.launch(ctx, pane, kind, name, args); err != nil {
		return err
	}
	return t.waitFor(ctx, "start", name, time.Minute, dispatch.StateIdle, dispatch.StateDone)
}

// IsArgumentRefused is always false: tmux passes any argument.
func (t Terminal) IsArgumentRefused(error) bool { return false }

// IsNameRefused is always false: an agent's name is a pane option, which takes any name.
func (t Terminal) IsNameRefused(error) bool { return false }

// AgentName returns ticket id's worker's agent name: the ID itself, as tmux has no rule for names.
func (t Terminal) AgentName(id string) string { return id }

// AdoptAgent names name the agent of kind launched in the pane (LaunchInPane), and returns its state.
// It needn't wait for the agent to show, as Herdr's does: the pane holds it from its launch.
func (t Terminal) AdoptAgent(ctx context.Context, pane, kind, name string) (dispatch.AgentState, error) {
	ps, err := t.panes(ctx)
	if err != nil {
		return "", err
	}
	p, ok := find(ps, func(p paneInfo) bool { return p.id == pane })
	switch {
	case !ok:
		return "", missing("adopt", pane, "pane")
	case p.kind != kind:
		return "", missing("adopt", pane, kind+" agent launched in the pane")
	}
	if err := t.setName(ctx, pane, name); err != nil {
		return "", err
	}
	return t.state(ctx, p), nil
}

// PaneAgent returns the agent in a pane: its name ("" if it was given none), kind and state, with
// state StateGone if the pane is gone or no agent was launched there, and an error if tmux could
// not be asked.
func (t Terminal) PaneAgent(
	ctx context.Context, pane string,
) (name, kind string, state dispatch.AgentState, err error) {
	ps, err := t.panes(ctx)
	if err != nil {
		return "", "", "", err
	}
	p, ok := find(ps, func(p paneInfo) bool { return p.id == pane })
	if !ok || p.kind == "" {
		return "", "", dispatch.StateGone, nil
	}
	return p.agent, p.kind, t.state(ctx, p), nil
}

// RenameAgent gives the agent, by name in t's repository or by its pane's ID, a new name.
func (t Terminal) RenameAgent(ctx context.Context, name, to string) error {
	ps, err := t.panes(ctx)
	if err != nil {
		return err
	}
	p, ok := t.named(ps, name)
	if !ok {
		p, ok = find(ps, func(p paneInfo) bool { return p.kind != "" && p.id == name })
	}
	if !ok {
		return missing("rename", name, "agent")
	}
	return t.setName(ctx, p.id, to)
}

// FreeName returns an agent name unused in t's repository for an earlier worker that holds name:
// name-1, name-2, …, or "" if none of the first 20 is free or tmux can't say.
func (t Terminal) FreeName(ctx context.Context, name string) string {
	ps, err := t.panes(ctx)
	if err != nil {
		return ""
	}
	for n := 1; n <= 20; n++ {
		free := fmt.Sprintf("%s-%d", name, n)
		if _, taken := find(ps, func(p paneInfo) bool { return p.agent == free && t.ours(p) }); !taken {
			return free
		}
	}
	return ""
}

// Status returns the agent's state, StateGone if no pane holds it. If tmux cannot be asked it returns
// the error and no state: the agent may well be there.
func (t Terminal) Status(ctx context.Context, name string) (dispatch.AgentState, error) {
	p, ok, err := t.agentPane(ctx, name)
	switch {
	case err != nil:
		return "", err
	case !ok:
		return dispatch.StateGone, nil
	}
	return t.state(ctx, p), nil
}

// shells are the processes a pane runs when it is back at a shell's prompt.
var shells = []string{"sh", "bash", "zsh", "fish", "dash", "ksh", "mksh", "tcsh", "csh", "nu"}

// state is the state of the agent in the pane p: gone once its process exited (the pane dead), its
// session ended or the pane is back at a shell; otherwise what its hooks tell (claude.State), and
// blocked while its screen shows a dialog that runs no hook, which only comes before its first turn
// (claude.TurnRecorded). A permission prompt its hooks tell asked is blocked only while its screen
// still shows it: once allowed, which runs no hook, its tool runs.
// A shell counts only once a hook ran: as the agent starts, the shell that runs it shows for a moment.
func (t Terminal) state(ctx context.Context, p paneInfo) dispatch.AgentState {
	if p.dead || p.kind == "" {
		return dispatch.StateGone
	}
	records := p.records
	if records == "" {
		records = claude.Records(p.dir, "") // launched without hooks: a ticket's worktree has them there
	}
	st, asked := claude.StateAsked(records)
	switch {
	case st == dispatch.StateGone:
		return st
	case st != dispatch.StateUnknown && slices.Contains(shells, strings.TrimPrefix(p.command, "-")):
		return dispatch.StateGone
	case st == dispatch.StateUnknown || (st == dispatch.StateIdle && !claude.TurnRecorded(records)):
		if screen, err := t.capture(ctx, p.id, false); err == nil && showsDialog(screen) {
			return dispatch.StateBlocked
		}
	case asked:
		if screen, err := t.capture(ctx, p.id, true); err == nil && screen != "" && !claude.ShowsPermission(screen) {
			return dispatch.StateWorking // allowed, which runs no hook: its tool runs
		}
	}
	return st
}

// dialogs are what Claude Code shows of the dialogs it asks before a session starts, which run no
// hook: whether to trust the folder, and whether to use the project's MCP servers.
var dialogs = []string{"trust this folder", "mcp server found in this project", "mcp servers found in this project"}

// showsDialog reports whether the bottom of screen shows one of Claude Code's dialogs.
func showsDialog(screen string) bool {
	lines := strings.Split(strings.ToLower(screen), "\n")
	lines = lines[max(0, len(lines)-25):]
	for _, l := range lines {
		for _, d := range dialogs {
			if strings.Contains(l, d) {
				return true
			}
		}
	}
	return false
}

// capture reads the pane's screen: the visible screen, and the 60 lines above it unless visible,
// with wrapped lines joined and the empty lines below its last one left out.
func (t Terminal) capture(ctx context.Context, pane string, visible bool) (string, error) {
	args := []string{"-p", "-J", "-t", pane}
	if !visible {
		args = append(args, "-S", "-60")
	}
	out, err := t.run(ctx, command.ReadLimit, "capture-pane", pane, args...)
	if err != nil {
		return "", err
	}
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return "", nil
	}
	return out + "\n", nil
}

// Screen returns the end of the agent's terminal, given its state as just read ("" if not known):
// the visible screen while it works or is blocked, as Herdr gives it, and otherwise with the 60
// lines above it. "" when tmux can't read it.
func (t Terminal) Screen(ctx context.Context, name string, state dispatch.AgentState) string {
	p, ok, err := t.agentPane(ctx, name)
	if err != nil || !ok {
		return ""
	}
	screen, _ := t.capture(ctx, p.id, state == dispatch.StateWorking || state == dispatch.StateBlocked)
	return screen // "" when tmux can't read it
}

// Prompt pastes the prompt into the agent's input box (bracketed, so its lines don't submit it one by
// one), presses Enter, and returns once the agent starts on it: working, or blocked (on a permission
// prompt, say). It does not wait for the turn to end. It fails if neither shows within 30 seconds.
func (t Terminal) Prompt(ctx context.Context, name, prompt string) error {
	p, ok, err := t.agentPane(ctx, name)
	switch {
	case err != nil:
		return err
	case !ok:
		return missing("prompt", name, "agent")
	}
	buf := "orchestra-" + strings.TrimPrefix(p.id, "%")
	if _, err := t.runWithInput(ctx, command.ReadLimit, "load-buffer", name, &prompt, "-b", buf, "-"); err != nil {
		return err
	}
	if _, err := t.run(ctx, command.ReadLimit, "paste-buffer", name, "-d", "-p", "-b", buf, "-t", p.id); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(pasteSettle):
	}
	if _, err := t.run(ctx, command.ReadLimit, "send-keys", name, "-t", p.id, "Enter"); err != nil {
		return err
	}
	return t.waitFor(ctx, "prompt", name, 30*time.Second, dispatch.StateWorking, dispatch.StateBlocked)
}

// keyNames are Herdr's names of the keys SendKeys presses, and tmux's for them.
var keyNames = map[string]string{
	"enter": "Enter", "return": "Enter", "esc": "Escape", "escape": "Escape", "tab": "Tab", "shift+tab": "BTab",
	"space": "Space", "backspace": "BSpace", "delete": "DC", "up": "Up", "down": "Down", "left": "Left", "right": "Right",
	"home": "Home", "end": "End", "pageup": "PPage", "pagedown": "NPage",
}

// tmuxKey is tmux's name for Herdr's key name k, and false for text to type as it is: ctrl+c is
// C-c and alt+x M-x.
func tmuxKey(k string) (string, bool) {
	if name, ok := keyNames[strings.ToLower(k)]; ok {
		return name, true
	}
	for prefix, mod := range map[string]string{"ctrl+": "C-", "alt+": "M-"} {
		if rest, ok := strings.CutPrefix(strings.ToLower(k), prefix); ok && utf8.RuneCountInString(rest) == 1 {
			return mod + rest, true
		}
	}
	return "", false
}

// SendKeys presses keys in the agent's pane, by Herdr's names (enter, esc, ctrl+c, up…); anything
// else is typed as it is.
func (t Terminal) SendKeys(ctx context.Context, name string, keys ...string) error {
	p, ok, err := t.agentPane(ctx, name)
	switch {
	case err != nil:
		return err
	case !ok:
		return missing("send-keys", name, "agent")
	}
	var cmds [][]string
	for _, k := range keys {
		if key, ok := tmuxKey(k); ok {
			cmds = append(cmds, []string{"-t", p.id, key})
			continue
		}
		// "--" keeps text that starts with '-' from reading as options. tmux takes an argument ending
		// in ';' for the end of a command, so the text's last semicolons go as their code.
		text := strings.TrimRight(k, ";")
		if text != "" {
			cmds = append(cmds, []string{"-t", p.id, "-l", "--", text})
		}
		if n := len(k) - len(text); n > 0 {
			cmds = append(cmds, append([]string{"-t", p.id, "-H"}, slices.Repeat([]string{"3b"}, n)...))
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	args := cmds[0]
	for _, c := range cmds[1:] {
		args = append(append(args, ";", "send-keys"), c...)
	}
	_, err = t.run(ctx, command.ReadLimit, "send-keys", name, args...)
	return err
}

// WaitReady waits up to a minute for an agent that is already present to become idle.
func (t Terminal) WaitReady(ctx context.Context, name string) bool {
	return t.waitFor(ctx, "wait", name, time.Minute, dispatch.StateIdle, dispatch.StateDone) == nil
}

// WaitStarted waits up to 20 seconds for an agent to start working (or block).
func (t Terminal) WaitStarted(ctx context.Context, name string) bool {
	return t.waitFor(ctx, "wait", name, 20*time.Second, dispatch.StateWorking, dispatch.StateBlocked) == nil
}

// waitFor reads the agent's state until it is one of want, for up to limit; the error says it went
// away, or what it was when the time ran out. A state tmux can't give is read again.
func (t Terminal) waitFor(ctx context.Context, op, name string, limit time.Duration,
	want ...dispatch.AgentState) error {
	deadline := time.Now().Add(limit)
	for {
		st, err := t.Status(ctx, name)
		switch {
		case err == nil && slices.Contains(want, st):
			return nil
		case err == nil && st == dispatch.StateGone:
			return &Error{Op: op, Target: name, Missing: true, Err: errors.New("the agent is gone")}
		case time.Now().After(deadline) && err != nil:
			return fmt.Errorf("agent %s was not %s within %s: %w", name, states(want),
				command.ShortDuration(limit), err)
		case time.Now().After(deadline):
			return &Error{Op: op, Target: name, Err: fmt.Errorf("the agent was not %s within %s, but %s",
				states(want), command.ShortDuration(limit), st)}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// states is "idle or done", for a list of states.
func states(sts []dispatch.AgentState) string {
	s := make([]string, len(sts))
	for i, st := range sts {
		s[i] = string(st)
	}
	return strings.Join(s, " or ")
}
