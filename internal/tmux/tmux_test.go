package tmux

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
)

// The interfaces the run loop reaches the terminal through, and the interview's panes.
var (
	_ dispatch.Tabs    = Terminal{}
	_ dispatch.Starter = Terminal{}
	_ dispatch.Namer   = Terminal{}
	_ dispatch.Agents  = Terminal{}
	_ interface {
		FocusTab(ctx context.Context, tab string) error
		SplitPane(ctx context.Context, pane, cwd string) (string, error)
		LaunchInPane(ctx context.Context, pane, kind string, args []string) error
		PaneAgent(ctx context.Context, pane string) (name, kind string, state dispatch.AgentState, err error)
		PaneOpen(ctx context.Context, pane string) (bool, error)
		ClosePane(ctx context.Context, pane string) error
	} = Terminal{}
)

// fakeTmux is a tmux on PATH that logs each call (each argument ending in a NUL, the call in an RS
// and a NUL) and answers from files in dir: out-<command> on
// stdout, fail-<command> on stderr with exit status 1, and after-<command> run as a script once it
// answered. load-buffer's input goes to buffer.
type fakeTmux struct {
	t   *testing.T
	dir string
}

// newFakeTmux puts a fake tmux on PATH, away from any tmux server of the user's: $TMUX is unset and
// tmux's sockets would be in a folder of the test's own, where none listens.
func newFakeTmux(t *testing.T) *fakeTmux {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake tmux is a shell script")
	}
	dir := t.TempDir()
	faketool.Write(t, dir, "tmux", `d='`+dir+`'
{ for a; do printf '%s\0' "$a"; done; printf '\036\0'; } >> "$d/calls"
[ "$1" = -L ] && shift 2
[ "$1" = load-buffer ] && cat > "$d/buffer"
if [ -f "$d/fail-$1" ]; then cat "$d/fail-$1" >&2; exit 1; fi
[ -f "$d/out-$1" ] && cat "$d/out-$1"
[ -f "$d/after-$1" ] && . "$d/after-$1"
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "")
	sockets, err := os.MkdirTemp("", "tmx") // short: a socket's path has a limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockets) })
	t.Setenv("TMUX_TMPDIR", sockets)
	return &fakeTmux{t: t, dir: dir}
}

func (f *fakeTmux) write(name, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// answer has tmux print out for command op.
func (f *fakeTmux) answer(op, out string) { f.write("out-"+op, out) }

// fail has tmux fail command op, saying stderr.
func (f *fakeTmux) fail(op, stderr string) { f.write("fail-"+op, stderr+"\n") }

// after has tmux run script once it answered command op.
func (f *fakeTmux) after(op, script string) { f.write("after-"+op, script) }

// serve has a tmux server listen on the default socket, as far as a connection to it can tell.
func (f *fakeTmux) serve() {
	f.t.Helper()
	path := Terminal{}.socketPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		f.t.Fatal(err)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = l.Close() })
}

// calls are the calls tmux got, each its arguments.
func (f *fakeTmux) calls() [][]string {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dir, "calls"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		f.t.Fatal(err)
	}
	var calls [][]string
	var call []string
	for l := range strings.SplitSeq(strings.TrimSuffix(string(b), "\x00"), "\x00") {
		if l == "\x1e" {
			calls = append(calls, call)
			call = nil
			continue
		}
		call = append(call, l)
	}
	return calls
}

// ops are the commands tmux was called with, in order.
func (f *fakeTmux) ops() []string {
	var ops []string
	for _, c := range f.calls() {
		ops = append(ops, c[0])
	}
	return ops
}

// call returns the arguments of the first call of command op, failing the test if there was none.
func (f *fakeTmux) call(op string) []string {
	f.t.Helper()
	for _, c := range f.calls() {
		if c[0] == op {
			return c
		}
	}
	f.t.Fatalf("no tmux %s among %v", op, f.ops())
	return nil
}

// paneLine is one pane as list-panes lists it with paneFormat.
type paneLine struct {
	id, window, agent, kind, records, repo, command, dir string
	dead                                                 bool
}

func (p paneLine) String() string {
	dead := "0"
	if p.dead {
		dead = "1"
	}
	window := p.window
	if window == "" {
		window = "@1"
	}
	return strings.Join([]string{p.id, window, dead, p.agent, p.kind, p.records, p.repo, p.command, p.dir}, "\t")
}

// listPanes has tmux list these panes.
func (f *fakeTmux) listPanes(ps ...paneLine) {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(p.String() + "\n")
	}
	f.answer("list-panes", b.String())
}

// records returns a records folder in which the hooks wrote these records, a second apart in order:
// a name and its content each.
func records(t *testing.T, kv ...string) string {
	t.Helper()
	dir := t.TempDir()
	at := time.Now().Add(-time.Minute)
	for i := 0; i+1 < len(kv); i += 2 {
		path := filepath.Join(dir, kv[i])
		if err := os.WriteFile(path, []byte(kv[i+1]), 0o644); err != nil {
			t.Fatal(err)
		}
		at = at.Add(time.Second)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Records in which a session started and no prompt came since (idle), a turn runs (working), and
// the session ended (gone).
func idleRecords(t *testing.T) string {
	return records(t, "events", `{"hook_event_name":"SessionStart","source":"startup"}`+"\n")
}

func workingRecords(t *testing.T) string {
	return records(t, "events", `{"hook_event_name":"SessionStart"}`+"\n",
		"turn.json", `{"hook_event_name":"UserPromptSubmit"}`)
}

func endedRecords(t *testing.T) string {
	return records(t, "events", `{"hook_event_name":"SessionStart"}`+"\n"+`{"hook_event_name":"SessionEnd","reason":"other"}`+"\n")
}

var bg = context.Background()

func TestCreateTabOpensANamedWindowThatKeepsItsName(t *testing.T) {
	f := newFakeTmux(t)
	f.answer("new-window", "@4 %9\n")
	tab, pane, err := Terminal{}.CreateTab(bg, "orchestra-repo", "/work/a#b", "orchestra-x.1")
	if err != nil || tab != "@4" || pane != "%9" {
		t.Fatalf("got %q %q %v", tab, pane, err)
	}
	want := [][]string{
		{"new-window", "-d", "-t", "=orchestra-repo:", "-c", "/work/a##b", "-n", "orchestra-x.1", "-P", "-F",
			"#{window_id} #{pane_id}"},
		{"set-option", "-w", "-t", "@4", "automatic-rename", "off", ";", "set-option", "-w", "-t", "@4",
			"allow-rename", "off"},
	}
	if got := f.calls(); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("tmux got:\n%q\nwant:\n%q", got, want)
	}
}

func TestCreateTabClosesAWindowItCannotUse(t *testing.T) {
	f := newFakeTmux(t)
	f.answer("new-window", "@4 nonsense\n")
	_, _, err := Terminal{}.CreateTab(bg, "s", "/w", "x-1")
	if err == nil || !strings.Contains(err.Error(), "window @4, which tmux opened, is closed again") {
		t.Errorf("an answer naming the window but no pane: %v", err)
	}
	if got := f.call("kill-window"); !slices.Equal(got, []string{"kill-window", "-t", "@4"}) {
		t.Errorf("kill-window got %q", got)
	}

	f = newFakeTmux(t)
	f.answer("new-window", "@4 %9\n")
	f.fail("set-option", "invalid option")
	if _, _, err := (Terminal{}).CreateTab(bg, "s", "/w", "x-1"); err == nil ||
		!strings.Contains(err.Error(), "is closed again") {
		t.Errorf("a window whose name can't be kept: %v", err)
	}
	f.call("kill-window")

	f = newFakeTmux(t)
	f.answer("new-window", "garbled")
	if _, _, err := (Terminal{}).CreateTab(bg, "s", "/w", "x-1"); err == nil ||
		!strings.Contains(err.Error(), "may be left open") {
		t.Errorf("an answer naming no window: %v", err)
	}
	if slices.Contains(f.ops(), "kill-window") {
		t.Error("no window to close")
	}
}

func TestCreateTabFailures(t *testing.T) {
	f := newFakeTmux(t)
	f.fail("new-window", "no server running on /tmp/tmux-501/default")
	_, _, err := Terminal{}.CreateTab(bg, "s", "/w", "x-1")
	if !NoServer(err) {
		t.Errorf("no server: %#v", err)
	}
	var te *Error
	if !errors.As(err, &te) || te.Op != "new-window" || te.Target != "s" {
		t.Errorf("the error should say what failed: %#v", err)
	}
	f.serve()
	f.fail("new-window", "can't find session: s")
	if _, _, err := (Terminal{}).CreateTab(bg, "s", "/w", "x-1"); err == nil || NoServer(err) || Missing(err) {
		t.Errorf("a server that refuses the call: %v", err)
	}

	t.Setenv("PATH", t.TempDir())
	_, _, err = Terminal{}.CreateTab(bg, "s", "/w", "x-1")
	if !errors.Is(err, exec.ErrNotFound) || NoServer(err) {
		t.Errorf("tmux missing: %v", err)
	}
}

func TestCloseTab(t *testing.T) {
	f := newFakeTmux(t)
	if err := (Terminal{}).CloseTab(bg, "@3"); err != nil {
		t.Fatal(err)
	}
	if got := f.calls(); len(got) != 1 || !slices.Equal(got[0], []string{"kill-window", "-t", "@3"}) {
		t.Errorf("tmux got %q", got)
	}

	f.serve()
	f.fail("kill-window", "can't find window: @3")
	f.answer("list-windows", "@1\torchestra\n@4\tx-2\n")
	if err := (Terminal{}).CloseTab(bg, "@3"); err != nil {
		t.Errorf("a window already gone: %v", err)
	}
	f.answer("list-windows", "@3\tx-1\n")
	if err := (Terminal{}).CloseTab(bg, "@3"); err == nil {
		t.Error("a window that is still there but wasn't closed")
	}

	f = newFakeTmux(t)
	f.fail("kill-window", "no server running")
	f.fail("list-windows", "no server running")
	if err := (Terminal{}).CloseTab(bg, "@3"); err != nil {
		t.Errorf("no server, so no window: %v", err)
	}

	if err := (Terminal{}).CloseTab(bg, ""); !Missing(err) {
		t.Errorf("no window ID: %v", err)
	}
}

func TestTabLabel(t *testing.T) {
	f := newFakeTmux(t)
	f.answer("list-windows", "@1\torchestra\n@3\tx-1.2\n@4\tname\twith tab\n")
	for tab, want := range map[string]struct {
		label string
		open  bool
	}{"@3": {"x-1.2", true}, "@4": {"name\twith tab", true}, "@5": {"", false}, "": {"", false}, "x": {"", false}} {
		label, open, err := Terminal{}.TabLabel(bg, tab)
		if label != want.label || open != want.open || err != nil {
			t.Errorf("TabLabel(%q) = %q %v %v", tab, label, open, err)
		}
	}
	if got := f.call("list-windows"); !slices.Equal(got, []string{"list-windows", "-a", "-F", "#{window_id}\t#{window_name}"}) {
		t.Errorf("list-windows got %q", got)
	}

	f.answer("list-windows", "garbled\n")
	if _, _, err := (Terminal{}).TabLabel(bg, "@3"); err == nil {
		t.Error("odd output should be an error")
	}

	f = newFakeTmux(t)
	f.fail("list-windows", "no server running")
	if _, open, err := (Terminal{}).TabLabel(bg, "@3"); open || err != nil {
		t.Errorf("no server, so no window: %v %v", open, err)
	}
	f.serve()
	if _, _, err := (Terminal{}).TabLabel(bg, "@3"); err == nil || NoServer(err) {
		t.Errorf("a server that fails the call says nothing of the window: %v", err)
	}
}

func TestFocusTab(t *testing.T) {
	f := newFakeTmux(t)
	if err := (Terminal{}).FocusTab(bg, "@3"); err != nil {
		t.Fatal(err)
	}
	if got := f.call("select-window"); !slices.Equal(got, []string{"select-window", "-t", "@3"}) {
		t.Errorf("select-window got %q", got)
	}
}

func TestSplitAndClosePane(t *testing.T) {
	f := newFakeTmux(t)
	f.answer("split-window", "%12\n")
	pane, err := Terminal{}.SplitPane(bg, "%2", "/repo#1")
	if err != nil || pane != "%12" {
		t.Fatalf("got %q %v", pane, err)
	}
	want := []string{"split-window", "-h", "-t", "%2", "-c", "/repo##1", "-P", "-F", "#{pane_id}"}
	if got := f.call("split-window"); !slices.Equal(got, want) {
		t.Errorf("split-window got %q", got)
	}
	f.answer("split-window", "oops\n")
	if _, err := (Terminal{}).SplitPane(bg, "%2", "/repo"); err == nil {
		t.Error("odd output should be an error")
	}

	if err := (Terminal{}).ClosePane(bg, "%12"); err != nil {
		t.Fatal(err)
	}
	if got := f.call("kill-pane"); !slices.Equal(got, []string{"kill-pane", "-t", "%12"}) {
		t.Errorf("kill-pane got %q", got)
	}
	f.serve()
	f.fail("kill-pane", "can't find pane: %12")
	f.listPanes(paneLine{id: "%2"})
	if err := (Terminal{}).ClosePane(bg, "%12"); err != nil {
		t.Errorf("a pane already gone: %v", err)
	}
	f.listPanes(paneLine{id: "%12"})
	if err := (Terminal{}).ClosePane(bg, "%12"); err == nil {
		t.Error("a pane still there but not closed")
	}
}

func TestPaneOpen(t *testing.T) {
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%1"}, paneLine{id: "%7", dead: true})
	for p, want := range map[string]bool{"%1": true, "%7": true, "%8": false} {
		if open, err := (Terminal{}).PaneOpen(bg, p); open != want || err != nil {
			t.Errorf("PaneOpen(%s) = %v %v", p, open, err)
		}
	}
	f.answer("list-panes", "%1\t@1\tmaybe\t\t\t\t\tzsh\t/\n")
	if _, err := (Terminal{}).PaneOpen(bg, "%1"); err == nil {
		t.Error("odd output should be an error")
	}
	f.answer("list-panes", "%1\t@1\t0\n")
	if _, err := (Terminal{}).PaneOpen(bg, "%1"); err == nil {
		t.Error("too few fields should be an error")
	}
	f.fail("list-panes", "no server running")
	if open, err := (Terminal{}).PaneOpen(bg, "%1"); open || err != nil {
		t.Errorf("no server, so no pane: %v %v", open, err)
	}
}

func TestLaunchInPaneRunsTheAgentInPlaceOfTheShell(t *testing.T) {
	f := newFakeTmux(t)
	t.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+"/opt/claude/bin")
	args := []string{"--settings", "/w/.orchestra/run/hooks.json", "--append-system-prompt-file", "/w/rules.md",
		"two\nlines and 'quotes';"}
	if err := (Terminal{}).LaunchInPane(bg, "%5", "claude", args); err != nil {
		t.Fatal(err)
	}
	want := []string{"set-option", "-p", "-t", "%5", "@orchestra-kind", "claude", ";",
		"set-option", "-p", "-t", "%5", "remain-on-exit", "on", ";",
		"set-option", "-p", "-t", "%5", "@orchestra-records", "/w/.orchestra/run"}
	if got := f.call("set-option"); !slices.Equal(got, want) {
		t.Errorf("set-option got:\n%q\nwant:\n%q", got, want)
	}
	respawn := f.call("respawn-pane")
	if len(respawn) != 9 || !slices.Equal(respawn[:6], []string{"respawn-pane", "-k", "-t", "%5", "-e",
		"PATH=" + os.Getenv("PATH")}) || respawn[6] != "/bin/sh" || respawn[7] != "-c" {
		t.Fatalf("respawn-pane got %q", respawn)
	}
	// The command line gives the agent its arguments exactly.
	script := strings.Replace(respawn[8], "exec 'claude'", `printf '%s\0'`, 1)
	out, err := exec.Command("/bin/sh", "-c", script).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00"); !slices.Equal(got, args) {
		t.Errorf("the agent would get %q, want %q", got, args)
	}
	if slices.Contains(f.ops(), "send-keys") {
		t.Error("nothing is typed into a shell")
	}

	f = newFakeTmux(t)
	if err := (Terminal{}).LaunchInPane(bg, "%5", "claude", []string{"--", "--settings", "/x/hooks.json"}); err != nil {
		t.Fatal(err)
	}
	if got := f.call("set-option"); !slices.Contains(got, "-u") || slices.Contains(got, "/x") {
		t.Errorf("an agent launched without hooks has no records folder: %q", got)
	}

	if err := (Terminal{}).LaunchInPane(bg, "", "claude", nil); !Missing(err) {
		t.Errorf("no pane: %v", err)
	}
}

func TestStartAgentNamesItAndWaitsForItToBeReady(t *testing.T) {
	f := newFakeTmux(t)
	idle := idleRecords(t)
	f.listPanes(paneLine{id: "%1", agent: "x-1", command: "zsh"}, paneLine{id: "%5", command: "zsh"})
	// Once launched, the pane holds the agent, named.
	f.after("respawn-pane", `printf '%s\n' '`+paneLine{id: "%5", agent: "x-1", kind: "claude", records: idle,
		command: "2.1.0", dir: "/w"}.String()+`' > "$d/out-list-panes"`)
	if err := (Terminal{}).StartAgent(bg, "x-1", "claude", "%5", nil); err != nil {
		t.Fatal(err)
	}
	if got := f.ops(); !slices.Equal(got, []string{"set-option", "list-panes", "set-option", "respawn-pane", "list-panes",
		"capture-pane"}) {
		t.Errorf("tmux got %v", got)
	}
	// The name moves to the new pane from one that held it.
	want := []string{"set-option", "-p", "-u", "-t", "%1", "@orchestra-agent", ";",
		"set-option", "-p", "-t", "%5", "@orchestra-agent", "x-1"}
	if got := f.calls()[2]; !slices.Equal(got, want) {
		t.Errorf("naming got:\n%q\nwant:\n%q", got, want)
	}

	f = newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude", records: idle, dead: true})
	if err := (Terminal{}).StartAgent(bg, "x-1", "claude", "%5", nil); !Missing(err) {
		t.Errorf("an agent that exited at once: %v", err)
	}
}

func TestAdoptAgent(t *testing.T) {
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", kind: "claude", records: workingRecords(t), command: "2.1.0"})
	st, err := Terminal{}.AdoptAgent(bg, "%5", "claude", "x-1")
	if st != dispatch.StateWorking || err != nil {
		t.Fatalf("got %q %v", st, err)
	}
	if got := f.call("set-option"); !slices.Equal(got, []string{"set-option", "-p", "-t", "%5", "@orchestra-agent", "x-1"}) {
		t.Errorf("set-option got %q", got)
	}
	if _, err := (Terminal{}).AdoptAgent(bg, "%6", "claude", "x-1"); !Missing(err) {
		t.Errorf("no such pane: %v", err)
	}
	if _, err := (Terminal{}).AdoptAgent(bg, "%5", "codex", "x-1"); !Missing(err) {
		t.Errorf("another kind: %v", err)
	}
}

func TestPaneAgent(t *testing.T) {
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude", records: idleRecords(t), command: "2.1.0"},
		paneLine{id: "%6", command: "zsh"})
	if n, k, st, err := (Terminal{}).PaneAgent(bg, "%5"); n != "x-1" || k != "claude" || st != dispatch.StateIdle || err != nil {
		t.Errorf("an agent: %q %q %q %v", n, k, st, err)
	}
	for _, p := range []string{"%6", "%7"} {
		if n, k, st, err := (Terminal{}).PaneAgent(bg, p); n != "" || k != "" || st != dispatch.StateGone || err != nil {
			t.Errorf("no agent in %s: %q %q %q %v", p, n, k, st, err)
		}
	}
	f.serve()
	f.fail("list-panes", "lost server")
	if _, _, st, err := (Terminal{}).PaneAgent(bg, "%5"); st != "" || err == nil {
		t.Errorf("tmux failing says nothing of the agent: %q %v", st, err)
	}
}

func TestRenameAgentAndFreeName(t *testing.T) {
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude"}, paneLine{id: "%6", agent: "x-1-1", kind: "claude"},
		paneLine{id: "%7", kind: "claude"})
	if got := (Terminal{}).FreeName(bg, "x-1"); got != "x-1-2" {
		t.Errorf("FreeName = %q", got)
	}
	if err := (Terminal{}).RenameAgent(bg, "x-1", "x-1-2"); err != nil {
		t.Fatal(err)
	}
	if got := f.call("set-option"); !slices.Equal(got, []string{"set-option", "-p", "-t", "%5", "@orchestra-agent", "x-1-2"}) {
		t.Errorf("by name: %q", got)
	}
	f = newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude"}, paneLine{id: "%7", kind: "claude"})
	if err := (Terminal{}).RenameAgent(bg, "%7", "x-1"); err != nil {
		t.Fatal(err)
	}
	want := []string{"set-option", "-p", "-u", "-t", "%5", "@orchestra-agent", ";",
		"set-option", "-p", "-t", "%7", "@orchestra-agent", "x-1"}
	if got := f.call("set-option"); !slices.Equal(got, want) {
		t.Errorf("by pane: %q", got)
	}
	if err := (Terminal{}).RenameAgent(bg, "y-2", "y"); !Missing(err) {
		t.Errorf("no such agent: %v", err)
	}
	if err := (Terminal{}).RenameAgent(bg, "%7", "x;"); err == nil || NoServer(err) {
		t.Errorf("a name tmux would take for the end of a command: %v", err)
	}

	f.fail("list-panes", "no server running")
	if got := (Terminal{}).FreeName(bg, "x-1"); got != "x-1-1" {
		t.Errorf("no server holds no names: %q", got)
	}
	f.serve()
	if got := (Terminal{}).FreeName(bg, "x-1"); got != "" {
		t.Errorf("unknown names may be taken: %q", got)
	}
}

func TestStatus(t *testing.T) {
	trust := "Quick safety check: is this a project you created or one you trust?\n\n❯ 1. Yes, I trust this folder\n  2. No, exit\n"
	mcp := "New MCP server found in this project: github\n❯ 1. Use this and all future MCP servers\n"
	for _, c := range []struct {
		name   string
		pane   paneLine
		screen string
		want   dispatch.AgentState
	}{
		{"idle", paneLine{records: idleRecords(t)}, "", dispatch.StateIdle},
		{"working", paneLine{records: workingRecords(t)}, trust, dispatch.StateWorking},
		{"session ended", paneLine{records: endedRecords(t)}, "", dispatch.StateGone},
		{"exited", paneLine{records: workingRecords(t), dead: true}, "", dispatch.StateGone},
		{"back at its shell", paneLine{records: workingRecords(t), command: "-zsh"}, "", dispatch.StateGone},
		{"its shell, starting", paneLine{records: records(t), command: "sh"}, "", dispatch.StateUnknown},
		{"no hook yet", paneLine{records: records(t)}, "Welcome to Claude Code", dispatch.StateUnknown},
		{"trust dialog", paneLine{records: records(t)}, trust, dispatch.StateBlocked},
		{"MCP dialog", paneLine{records: idleRecords(t)}, mcp, dispatch.StateBlocked},
		{"no agent launched", paneLine{agent: "x-1", command: "zsh"}, "", dispatch.StateGone},
	} {
		f := newFakeTmux(t)
		p := c.pane
		p.id, p.agent = "%5", "x-1"
		if p.kind == "" && c.name != "no agent launched" {
			p.kind = "claude"
		}
		if p.command == "" {
			p.command = "2.1.0"
		}
		f.listPanes(paneLine{id: "%1", agent: "x-2", kind: "claude"}, p)
		f.answer("capture-pane", c.screen)
		if st, err := (Terminal{}).Status(bg, "x-1"); st != c.want || err != nil {
			t.Errorf("%s: %q %v, want %q", c.name, st, err, c.want)
		}
	}
}

func TestStatusWithoutTheAgentOrTmux(t *testing.T) {
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%1", agent: "x-2", kind: "claude"})
	if st, err := (Terminal{}).Status(bg, "x-1"); st != dispatch.StateGone || err != nil {
		t.Errorf("no pane holds it: %q %v", st, err)
	}
	f.answer("list-panes", "%1 @1 0\n")
	if st, err := (Terminal{}).Status(bg, "x-1"); st != "" || err == nil {
		t.Errorf("odd output: %q %v", st, err)
	}
	f.fail("list-panes", "no server running")
	if st, err := (Terminal{}).Status(bg, "x-1"); st != dispatch.StateGone || err != nil {
		t.Errorf("no server: %q %v", st, err)
	}
	f.serve()
	if st, err := (Terminal{}).Status(bg, "x-1"); st != "" || err == nil || NoServer(err) {
		t.Errorf("a server that fails the call: %q %v", st, err)
	}
	t.Setenv("PATH", t.TempDir())
	if st, err := (Terminal{}).Status(bg, "x-1"); st != "" || !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("tmux missing: %q %v", st, err)
	}
}

// An agent launched without hooks (none passed) is read from its worktree's records.
func TestStatusOfAnAgentLaunchedWithoutHooks(t *testing.T) {
	f := newFakeTmux(t)
	wt := t.TempDir()
	run := filepath.Join(wt, ".orchestra", "run")
	if err := os.MkdirAll(run, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run, "turn.json"), []byte(`{"hook_event_name":"UserPromptSubmit"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude", command: "2.1.0", dir: wt})
	if st, err := (Terminal{}).Status(bg, "x-1"); st != dispatch.StateWorking || err != nil {
		t.Errorf("got %q %v", st, err)
	}
}

func TestScreen(t *testing.T) {
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude"})
	f.answer("capture-pane", "⏺ Bash(go test)\n\n❯ \n\n\n\n")
	for st, want := range map[dispatch.AgentState][]string{
		dispatch.StateWorking: {"capture-pane", "-p", "-J", "-t", "%5"},
		dispatch.StateBlocked: {"capture-pane", "-p", "-J", "-t", "%5"},
		dispatch.StateIdle:    {"capture-pane", "-p", "-J", "-t", "%5", "-S", "-60"},
		"":                    {"capture-pane", "-p", "-J", "-t", "%5", "-S", "-60"},
	} {
		if err := os.Remove(filepath.Join(f.dir, "calls")); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if got := (Terminal{}).Screen(bg, "x-1", st); got != "⏺ Bash(go test)\n\n❯ \n" {
			t.Errorf("Screen(%q) = %q", st, got)
		}
		if got := f.call("capture-pane"); !slices.Equal(got, want) {
			t.Errorf("Screen(%q) ran %q", st, got)
		}
	}
	if got := (Terminal{}).Screen(bg, "x-2", ""); got != "" {
		t.Errorf("no such agent: %q", got)
	}
	f.fail("capture-pane", "boom")
	if got := (Terminal{}).Screen(bg, "x-1", ""); got != "" {
		t.Errorf("unreadable: %q", got)
	}
}

// noPasteSettle has Prompt press Enter at once, for the test.
func noPasteSettle(t *testing.T) {
	was := pasteSettle
	pasteSettle = 0
	t.Cleanup(func() { pasteSettle = was })
}

func TestPromptPastesItAndWaitsForTheAgentToStart(t *testing.T) {
	noPasteSettle(t)
	f := newFakeTmux(t)
	rec := idleRecords(t)
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude", records: rec, command: "2.1.0"})
	// Enter submits it: the hook records the prompt.
	f.after("send-keys", `printf '%s' '{"hook_event_name":"UserPromptSubmit"}' > '`+rec+`/turn.json'`)
	prompt := "do the ticket;\nline two with 'quotes'"
	if err := (Terminal{}).Prompt(bg, "x-1", prompt); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.dir, "buffer")); string(b) != prompt {
		t.Errorf("the buffer holds %q", b)
	}
	if got := f.ops(); !slices.Equal(got, []string{"list-panes", "load-buffer", "paste-buffer", "send-keys", "list-panes"}) {
		t.Errorf("tmux got %v", got)
	}
	for _, want := range [][]string{
		{"load-buffer", "-b", "orchestra-5", "-"},
		{"paste-buffer", "-d", "-p", "-b", "orchestra-5", "-t", "%5"},
		{"send-keys", "-t", "%5", "Enter"},
	} {
		if got := f.call(want[0]); !slices.Equal(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	}

	f = newFakeTmux(t)
	f.listPanes()
	if err := (Terminal{}).Prompt(bg, "x-1", "hi"); !Missing(err) {
		t.Errorf("no such agent: %v", err)
	}
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude", records: idleRecords(t), command: "2.1.0"})
	f.fail("paste-buffer", "no buffer")
	if err := (Terminal{}).Prompt(bg, "x-1", "hi"); err == nil || slices.Contains(f.ops(), "send-keys") {
		t.Errorf("a failed paste: %v, %v", err, f.ops())
	}
}

// Prompt fails once the agent is gone rather than wait out its 30 seconds.
func TestPromptToAnAgentThatExits(t *testing.T) {
	noPasteSettle(t)
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude", records: idleRecords(t), command: "2.1.0"})
	f.after("send-keys", `printf '%s\n' '`+paneLine{id: "%5", agent: "x-1", kind: "claude", dead: true}.String()+
		`' > "$d/out-list-panes"`)
	if err := (Terminal{}).Prompt(bg, "x-1", "hi"); !Missing(err) {
		t.Errorf("got %v", err)
	}
}

func TestSendKeysByHerdrsNames(t *testing.T) {
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude"})
	if err := (Terminal{}).SendKeys(bg, "x-1", "enter", "esc", "ctrl+c", "Up", "shift+tab", "y", "yes"); err != nil {
		t.Fatal(err)
	}
	want := []string{"send-keys", "-t", "%5", "Enter", ";", "send-keys", "-t", "%5", "Escape", ";",
		"send-keys", "-t", "%5", "C-c", ";", "send-keys", "-t", "%5", "Up", ";", "send-keys", "-t", "%5", "BTab", ";",
		"send-keys", "-t", "%5", "-l", "--", "y", ";", "send-keys", "-t", "%5", "-l", "--", "yes"}
	if got := f.call("send-keys"); !slices.Equal(got, want) {
		t.Errorf("send-keys got:\n%q\nwant:\n%q", got, want)
	}
	if err := (Terminal{}).SendKeys(bg, "x-2", "enter"); !Missing(err) {
		t.Errorf("no such agent: %v", err)
	}
}

func TestWaits(t *testing.T) {
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", agent: "idle", kind: "claude", records: idleRecords(t), command: "2.1.0"},
		paneLine{id: "%6", agent: "working", kind: "claude", records: workingRecords(t), command: "2.1.0"},
		paneLine{id: "%7", agent: "dead", kind: "claude", dead: true})
	term := Terminal{}
	if !term.WaitReady(bg, "idle") || term.WaitReady(bg, "dead") || term.WaitReady(bg, "none") {
		t.Error("WaitReady: idle is ready, gone isn't")
	}
	if !term.WaitStarted(bg, "working") || term.WaitStarted(bg, "dead") {
		t.Error("WaitStarted: working has started, gone hasn't")
	}
	ctx, cancel := context.WithTimeout(bg, 3*poll)
	defer cancel()
	if term.WaitStarted(ctx, "idle") {
		t.Error("an idle agent hasn't started")
	}
	if ctx.Err() == nil {
		t.Error("it should have waited until stopped")
	}
}

func TestSocketNamesTheServer(t *testing.T) {
	f := newFakeTmux(t)
	if err := (Terminal{Socket: "orch-test"}).FocusTab(bg, "@1"); err != nil {
		t.Fatal(err)
	}
	if got := f.calls(); len(got) != 1 || !slices.Equal(got[0], []string{"-L", "orch-test", "select-window", "-t", "@1"}) {
		t.Errorf("tmux got %q", got)
	}
	uid := strconv.Itoa(os.Getuid())
	if got, want := (Terminal{Socket: "orch-test"}).socketPath(), filepath.Join(os.Getenv("TMUX_TMPDIR"), "tmux-"+uid, "orch-test"); got != want {
		t.Errorf("socket %q, want %q", got, want)
	}
	t.Setenv("TMUX", "/private/tmp/tmux-501/work,1234,0")
	if got := (Terminal{}).socketPath(); got != "/private/tmp/tmux-501/work" {
		t.Errorf("inside tmux, its server: %q", got)
	}
	if got := (Terminal{Socket: "x"}).socketPath(); !strings.HasSuffix(got, "/x") {
		t.Errorf("a socket named overrides $TMUX: %q", got)
	}
}

func TestErrorText(t *testing.T) {
	err := missing("rename", "x-1", "agent")
	if err.Error() != "tmux rename x-1: no such agent" {
		t.Errorf("got %q", err)
	}
	if got := (&Error{Op: "list-panes", Err: errors.New("boom")}).Error(); got != "tmux list-panes: boom" {
		t.Errorf("got %q", got)
	}
}
