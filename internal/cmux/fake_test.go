package cmux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
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

var bg = context.Background()

// UUIDs for the tests' workspaces and surfaces, as cmux prints them.
const (
	ownWS   = "00000000-0000-4000-8000-0000000000A0" // orchestra's own workspace
	ownSF   = "00000000-0000-4000-8000-0000000000A1" // orchestra's own surface
	userWS  = "00000000-0000-4000-8000-0000000000B0" // a workspace of the user's
	workWS  = "00000000-0000-4000-8000-0000000000C0" // a worker's workspace
	workSF  = "00000000-0000-4000-8000-0000000000C1" // its terminal
	splitSF = "00000000-0000-4000-8000-0000000000A2" // a terminal split off orchestra's
)

// fakeCmux is a cmux on PATH that logs each call (each argument ending in a NUL, the call in an RS
// and a NUL) and answers from files in dir, by command (the first argument after the global
// options; workspace create is "workspace"): out-<command>-<n> on stdout for its n-th call, or else
// out-<command>, fail-<command> on stderr with exit status 1, and after-<command> run as a script
// once it answered. What paste reads on its stdin is kept in stdin-paste-<n>.
type fakeCmux struct {
	t   *testing.T
	dir string
}

// newFakeCmux puts a fake cmux on PATH, with no cmux app to fall back on, and a socket path where
// nothing listens.
func newFakeCmux(t *testing.T) *fakeCmux {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake cmux is a shell script")
	}
	dir := t.TempDir()
	faketool.Write(t, dir, "cmux", `d='`+dir+`'
{ for a; do printf '%s\0' "$a"; done; printf '\036\0'; } >> "$d/calls"
while :; do
	case "$1" in
	--json) shift ;;
	--id-format) shift 2 ;;
	*) break ;;
	esac
done
n=$(( $(cat "$d/count-$1" 2>/dev/null || echo 0) + 1 ))
echo "$n" > "$d/count-$1"
[ "$1" = paste ] && cat > "$d/stdin-paste-$n"
if [ -f "$d/fail-$1" ]; then cat "$d/fail-$1" >&2; exit 1; fi
if [ -f "$d/out-$1-$n" ]; then cat "$d/out-$1-$n"; elif [ -f "$d/out-$1" ]; then cat "$d/out-$1"; fi
[ -f "$d/after-$1" ] && . "$d/after-$1"
exit 0
`)
	t.Setenv("PATH", faketool.Path(t, dir))
	t.Setenv("CMUX_SOCKET", "")
	sockets, err := os.MkdirTemp("", "cmx") // short: a socket's path has a limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockets) })
	t.Setenv("CMUX_SOCKET_PATH", filepath.Join(sockets, "cmux.sock"))
	app := appBin
	appBin = filepath.Join(dir, "no-app", "cmux")
	t.Cleanup(func() { appBin = app })
	wait := settle
	settle = 0 // the fake's list shows at once what it opened or closed
	t.Cleanup(func() { settle = wait })
	return &fakeCmux{t: t, dir: dir}
}

func (f *fakeCmux) write(name, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// answer has cmux print out for command op.
func (f *fakeCmux) answer(op, out string) { f.write("out-"+op, out) }

// answerNth has cmux print out for the n-th call of command op.
func (f *fakeCmux) answerNth(op string, n int, out string) {
	f.write(fmt.Sprintf("out-%s-%d", op, n), out)
}

// fail has cmux fail command op, saying stderr.
func (f *fakeCmux) fail(op, stderr string) { f.write("fail-"+op, stderr+"\n") }

// serve has cmux listen on its socket, as far as a connection to it can tell.
func (f *fakeCmux) serve() {
	f.t.Helper()
	l, err := net.Listen("unix", os.Getenv("CMUX_SOCKET_PATH"))
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = l.Close() })
}

// calls are the calls cmux got, each its arguments.
func (f *fakeCmux) calls() [][]string {
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

// cmdOf is a call's command, after the global options, with its arguments.
func cmdOf(call []string) []string {
	for len(call) > 0 {
		switch call[0] {
		case "--json":
			call = call[1:]
		case "--id-format":
			call = call[2:]
		default:
			return call
		}
	}
	return call
}

// ops are the commands cmux was called with, in order.
func (f *fakeCmux) ops() []string {
	var ops []string
	for _, c := range f.calls() {
		ops = append(ops, cmdOf(c)[0])
	}
	return ops
}

// writes are the commands that wrote into a terminal (send, send-key, paste), with their arguments,
// in order.
func (f *fakeCmux) writes() [][]string {
	var writes [][]string
	for _, c := range f.calls() {
		if c := cmdOf(c); c[0] == "send" || c[0] == "send-key" || c[0] == "paste" {
			writes = append(writes, c)
		}
	}
	return writes
}

// pasted is what the n-th call of paste read on its stdin.
func (f *fakeCmux) pasted(n int) string {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dir, fmt.Sprintf("stdin-paste-%d", n)))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

// call returns the first call of command op, with its global options, failing the test if there was
// none.
func (f *fakeCmux) call(op string) []string {
	f.t.Helper()
	for _, c := range f.calls() {
		if cmdOf(c)[0] == op {
			return c
		}
	}
	f.t.Fatalf("no cmux %s among %v", op, f.ops())
	return nil
}

// ws is a workspace as tree lists it: its UUID, title and surfaces.
type ws struct {
	id, title string
	surfaces  []string
}

// treeJSON is tree --all --json's answer for these workspaces, in one window.
func treeJSON(wss ...ws) string {
	type surface struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	type pane struct {
		ID       string    `json:"id"`
		Surfaces []surface `json:"surfaces"`
	}
	type workspace struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Panes []pane `json:"panes"`
	}
	var list []workspace
	for _, w := range wss {
		var panes []pane
		for _, s := range w.surfaces {
			panes = append(panes, pane{ID: "11111111-1111-4111-8111-111111111111", Surfaces: []surface{{ID: s, Type: "terminal"}}})
		}
		list = append(list, workspace{ID: w.id, Title: w.title, Panes: panes})
	}
	b, _ := json.MarshalIndent(map[string]any{"active": nil, "windows": []any{map[string]any{
		"id": "22222222-2222-4222-8222-222222222222", "workspaces": list}}}, "", "  ")
	return string(b) + "\n"
}

// topJSON is top --processes --json's answer for a workspace whose surface runs the processes pids.
func topJSON(surface string, pids ...int) string {
	var procs []any
	for _, pid := range pids {
		procs = append(procs, map[string]any{"kind": "process", "pid": pid, "name": "2.1.296",
			"cmux_surface_id": surface, "children": []any{}})
	}
	b, _ := json.Marshal(map[string]any{"windows": []any{map[string]any{"workspaces": []any{map[string]any{
		"panes": []any{map[string]any{"surfaces": []any{map[string]any{"id": surface, "processes": procs}}}}}}}}})
	return string(b) + "\n"
}

// terminal is a Terminal whose book is in a folder of the test's own, with orchestra's own workspace.
func terminal(t *testing.T) Terminal {
	return Terminal{Book: filepath.Join(t.TempDir(), ".orchestra", "run", BookName), Own: ownWS}
}

// keep writes b as the terminal's book.
func keep(t *testing.T, term Terminal, b book) {
	t.Helper()
	if err := term.edit(func(nb *book) error { *nb = b; return nil }); err != nil {
		t.Fatal(err)
	}
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
	return records(t, "events", `{"hook_event_name":"SessionStart"}`+"\n"+
		`{"hook_event_name":"SessionEnd","reason":"other"}`+"\n")
}

// launchedPID has the surface's launch write pid, as its script does once it runs.
func launchedPID(t *testing.T, term Terminal, surface string, pid int) {
	t.Helper()
	path := term.launchPath(surface, ".pid")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", pid)), 0o644); err != nil {
		t.Fatal(err)
	}
}
