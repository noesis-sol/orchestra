package tmux

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/faketool"
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
	dead, launched                                       bool
}

func (p paneLine) String() string {
	dead := "0"
	if p.dead {
		dead = "1"
	}
	launched := "0"
	if p.launched {
		launched = "1"
	}
	window := p.window
	if window == "" {
		window = "@1"
	}
	return strings.Join([]string{p.id, window, dead, launched, p.agent, p.kind, p.records, p.repo, p.command, p.dir},
		"\t")
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
