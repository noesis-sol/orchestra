// Package cmuxtest is for tests that run the real cmux, a macOS app with one instance on a machine:
// each test starts a cmux of its own, with a home folder of its own, so that its settings, saved
// session and socket are the test's, and quits it as the test ends. The app is started with
// CMUX_SOCKET_MODE=allowAll, which has its socket answer any process, not only those started in
// its terminals: the test's.
//
// cmux won't run twice, so a test never runs beside the user's cmux, nor beside another test's: the
// tests of every package take turns (a lock file), and a test finding a cmux it didn't start running
// skips. They skip, too, without cmux installed, and unless asked for: ORCHESTRA_CMUX_TEST=1 runs
// them, as a maintainer would with their own cmux quit. ORCHESTRA_REQUIRE_CMUX=1 also runs them and
// has them fail where they would skip: CI's macOS job sets it, so the tests can't be skipped there
// unseen.
package cmuxtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/hookterm"
)

// RequireVar is the variable that, set to 1, runs the tests that need cmux and has them fail where
// they would skip: without cmux, or with another cmux running.
const RequireVar = "ORCHESTRA_REQUIRE_CMUX"

// OptInVar is the variable that, set to 1, runs the tests that need cmux, which otherwise skip: they
// start the cmux app, which won't run beside the user's.
const OptInVar = "ORCHESTRA_CMUX_TEST"

// AppVar is the variable that names the cmux app the tests start, /Applications/cmux.app when unset:
// one of another version, say, unpacked elsewhere.
const AppVar = "ORCHESTRA_CMUX_APP"

// bundle is the cmux app the tests start.
func bundle() string {
	if app := os.Getenv(AppVar); app != "" {
		return app
	}
	return "/Applications/cmux.app"
}

// appPath is the app's executable, and cliPath its CLI.
func appPath() string { return filepath.Join(bundle(), "Contents", "MacOS", "cmux") }
func cliPath() string { return filepath.Join(bundle(), "Contents", "Resources", "bin", "cmux") }

// lockName is the file, in the system's temporary folder, that the tests of every package lock in
// turn while their cmux runs.
const lockName = "orchestra-cmux-test.lock"

// Limits on the app: how long it may take to answer once started, and to exit once asked to quit,
// and how long a test waits for the others' cmux to be done.
const (
	startLimit = time.Minute
	quitLimit  = 30 * time.Second
	turnLimit  = 8 * time.Minute
)

// App is a cmux app of the test's own.
type App struct {
	t      testing.TB
	home   string        // its home folder
	cmd    *exec.Cmd     // the running app; nil once quit
	exited chan struct{} // closed once it has exited
	// Socket is the path of the app's socket, which CMUX_SOCKET_PATH names for the test.
	Socket string
	// Own is the workspace the app opened with, and OwnSurface its terminal: orchestra's own, as the
	// tests take them, as if orchestra ran there.
	Own, OwnSurface string
}

// Start starts a cmux of the test's own, waits until its socket answers and returns it, with
// CMUX_SOCKET_PATH naming its socket for the rest of the test, the variables cmux sets in its
// terminals unset, and the app's CLI first on PATH, so that orchestra's adapter talks to it. It skips the test under
// -short, unless asked for (Require), and where another cmux runs, or fails it there with
// ORCHESTRA_REQUIRE_CMUX=1. The app is quit, and the test waits for it to exit, as the test ends.
func Start(t testing.TB) *App {
	t.Helper()
	if testing.Short() {
		t.Skip("skipped by -short: starts the cmux app")
	}
	Require(t)
	takeTurn(t)
	if pid := running(t); pid != 0 {
		skipOrFail(t, fmt.Sprintf("cmux is running (process %d), and only one cmux runs at a time", pid))
	}
	home, err := os.MkdirTemp("", "cmx") // short: a socket's path has a limit of about 100 bytes
	if err != nil {
		t.Fatal(err)
	}
	a := &App{t: t, home: home}
	t.Cleanup(func() {
		a.Quit()
		_ = os.RemoveAll(home) // the app is gone by then; a leftover is no failure
	})
	for _, v := range []string{"CMUX_SOCKET", "CMUX_WORKSPACE_ID", "CMUX_SURFACE_ID", "CMUX_TAB_ID", "CMUX_PANEL_ID"} {
		t.Setenv(v, "")
	}
	// orchestra runs the cmux on PATH, which is to be the app's own CLI.
	cli := t.TempDir()
	if err := os.Symlink(cliPath(), filepath.Join(cli, "cmux")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", cli+string(os.PathListSeparator)+os.Getenv("PATH"))
	a.start()
	t.Logf("%s", strings.TrimSpace(a.Version()))
	tree := a.Tree()
	if len(tree.Order) != 1 || len(tree.Surfaces[tree.Order[0]]) != 1 {
		t.Fatalf("cmux started with workspaces %v, want one with one terminal", tree.Titles)
	}
	a.Own = tree.Order[0]
	a.OwnSurface = tree.Surfaces[a.Own][0]
	return a
}

// Require skips the test where cmux isn't installed, or where the tests that need it weren't asked
// for (OptInVar), or fails it where cmux isn't installed with ORCHESTRA_REQUIRE_CMUX=1.
func Require(t testing.TB) {
	t.Helper()
	required := os.Getenv(RequireVar) == "1"
	if runtime.GOOS != "darwin" {
		if required {
			t.Fatalf("cmux runs only on macOS, and %s=1 requires the tests that run it", RequireVar)
			return
		}
		t.Skipf("cmux runs only on macOS")
		return
	}
	if fi, err := os.Stat(appPath()); err != nil || fi.IsDir() {
		if required {
			t.Fatalf("cmux is not installed (no %s), and %s=1 requires the tests that run it", appPath(), RequireVar)
			return
		}
		t.Skipf("cmux is not installed (no %s); %s=1 fails the test instead", appPath(), RequireVar)
		return
	}
	if !required && os.Getenv(OptInVar) != "1" {
		t.Skipf("starts the cmux app, which won't run beside yours: quit cmux and set %s=1 to run it", OptInVar)
	}
}

// skipOrFail skips the test for why, or fails it with ORCHESTRA_REQUIRE_CMUX=1.
func skipOrFail(t testing.TB, why string) {
	t.Helper()
	if os.Getenv(RequireVar) == "1" {
		t.Fatalf("%s; %s=1 requires the tests that run it", why, RequireVar)
	}
	t.Skipf("%s; quit it to run the test", why)
}

// takeTurn waits until no other test, in this package or another, has a cmux running, and holds
// the turn until the test ends.
func takeTurn(t testing.TB) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(os.TempDir(), lockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(turnLimit); ; time.Sleep(100 * time.Millisecond) {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			_ = f.Close() // only read for its lock
			t.Fatalf("cannot take the turn to run cmux (%s): %v", f.Name(), err)
		}
	}
	t.Cleanup(func() { _ = f.Close() }) // closing it releases the lock
}

// running returns the process ID of a cmux app running, 0 for none.
func running(t testing.TB) int {
	t.Helper()
	out, err := command.Output(context.Background(), command.ReadLimit, "", "ps", "-axo", "pid=,comm=")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(out) {
		pid, comm, _ := strings.Cut(strings.TrimSpace(line), " ")
		if strings.HasSuffix(strings.TrimSpace(comm), ".app/Contents/MacOS/cmux") {
			n, _ := strconv.Atoi(pid) // ps prints digits
			return n
		}
	}
	return 0
}

// start starts the app in its home folder and waits until its socket answers, which then names it
// for the test (CMUX_SOCKET_PATH).
func (a *App) start() {
	a.t.Helper()
	state := filepath.Join(a.home, ".local", "state", "cmux")
	// The app writes the path of its socket here, and a socket left by an app that was killed has it
	// pick another.
	last := filepath.Join(state, "last-socket-path")
	if err := os.Remove(last); err != nil && !errors.Is(err, os.ErrNotExist) {
		a.t.Fatal(err)
	}
	log, err := os.OpenFile(filepath.Join(a.home, "cmux.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		a.t.Fatal(err)
	}
	defer func() { _ = log.Close() }() // the app has its own copy
	// The app is the test's for as long as the test runs, so it isn't started through internal/command,
	// which runs a command to its end: Quit ends it.
	cmd := exec.Command(appPath())
	cmd.Dir, cmd.Stdout, cmd.Stderr = a.home, log, log
	// HOME keeps cmux's files, CFFIXED_USER_HOME those macOS keeps for it (its settings, its saved
	// session), in the test's folder. Its terminals' shells are login shells, whose HOME is the
	// user's again.
	cmd.Env = append(env(), "HOME="+a.home, "CFFIXED_USER_HOME="+a.home, "CMUX_SOCKET_MODE=allowAll")
	if err := cmd.Start(); err != nil {
		a.t.Fatal(err)
	}
	a.cmd, a.exited = cmd, make(chan struct{})
	go func(exited chan struct{}) {
		_ = cmd.Wait() // an app quit by a signal exits with an error, as expected
		close(exited)
	}(a.exited)
	for deadline := time.Now().Add(startLimit); ; time.Sleep(100 * time.Millisecond) {
		select {
		case <-a.exited:
			a.cmd = nil
			a.t.Fatalf("cmux exited as it started, as it does with another cmux running; its output:\n%s", a.log())
		default:
		}
		if b, err := os.ReadFile(last); err == nil && answers(strings.TrimSpace(string(b))) {
			a.Socket = strings.TrimSpace(string(b))
			break
		}
		if time.Now().After(deadline) {
			a.t.Fatalf("cmux's socket didn't answer within %s of its start; its output:\n%s", startLimit, a.log())
		}
	}
	a.t.Setenv("CMUX_SOCKET_PATH", a.Socket)
	a.CLI("ping")
}

// env is the test's environment for the app, without what cmux sets in its terminals, should the
// test run in one.
func env() []string {
	var e []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "CMUX_") {
			e = append(e, kv)
		}
	}
	return e
}

// answers reports whether something listens on the socket at path.
func answers(path string) bool { return hookterm.Probe(path) == hookterm.SocketListening }

// log is what the app printed.
func (a *App) log() string {
	b, _ := os.ReadFile(filepath.Join(a.home, "cmux.log")) // none reads as nothing
	return string(b)
}

// Quit quits the app, as quitting cmux from its menu does, and waits for it to exit, killing it if
// it hasn't within 30 seconds. An app already quit is left as it is.
func (a *App) Quit() {
	a.t.Helper()
	if a.cmd == nil {
		return
	}
	cmd := a.cmd
	a.cmd = nil
	_ = cmd.Process.Signal(syscall.SIGTERM) // one that has exited already needs none
	select {
	case <-a.exited:
		return
	case <-time.After(quitLimit):
	}
	_ = cmd.Process.Kill() // nor this
	<-a.exited
	a.t.Errorf("cmux was still running %s after it was asked to quit, so it was killed", quitLimit)
}

// Restart starts the app again, after Quit, in the same home folder: it restores the workspaces
// it had, as cmux does after a restart, and its socket names it again for the test.
func (a *App) Restart() {
	a.t.Helper()
	if a.cmd != nil {
		a.t.Fatal("cmux is still running")
	}
	a.start()
}

// AwaitSaved waits, for up to 30 seconds, until the app has saved the workspace with its session,
// which it does every few seconds: one opened since is lost when it quits, as on any quit, and
// isn't restored.
func (a *App) AwaitSaved(workspace string) {
	a.t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		saved, _ := filepath.Glob(filepath.Join(a.home, "Library", "Application Support", "cmux", "session-*.json"))
		for _, path := range saved {
			b, err := os.ReadFile(path)
			if err == nil && !strings.HasSuffix(path, "-previous.json") &&
				strings.Contains(strings.ToUpper(string(b)), strings.ToUpper(workspace)) {
				return
			}
		}
		if time.Now().After(deadline) {
			a.t.Fatalf("cmux didn't save workspace %s with its session within 30s (%q)", workspace, saved)
		}
	}
}

// Version is what cmux version prints: cmux 0.65.0 (95) [1a2b3c4].
func (a *App) Version() string {
	a.t.Helper()
	return a.CLI("version")
}

// CLI runs the cmux CLI with args, handles as UUIDs, and returns its output, failing the test if it
// fails.
func (a *App) CLI(args ...string) string {
	a.t.Helper()
	out, err := command.Output(context.Background(), command.ReadLimit, "", cliPath(),
		append([]string{"--id-format", "uuids"}, args...)...)
	if err != nil {
		a.t.Fatalf("%v; cmux's output:\n%s", err, a.log())
	}
	return out
}

// Tree is what the app has, as cmux tree --all lists it, every handle a UUID in capitals.
type Tree struct {
	Order    []string            // the workspaces, in the sidebar's order
	Titles   map[string]string   // workspace → its title
	Surfaces map[string][]string // workspace → its terminals, in its panes' order
	Selected string              // the workspace in view
	Focused  string              // the terminal with the keyboard focus, in the workspace in view
}

// treeAnswer is the part of tree --all --json that Tree reads.
type treeAnswer struct {
	Windows []struct {
		Selected   string `json:"selected_workspace_id"`
		Workspaces []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Panes []struct {
				Focused  bool `json:"focused"`
				Surfaces []struct {
					ID      string `json:"id"`
					Focused bool   `json:"focused"`
				} `json:"surfaces"`
			} `json:"panes"`
		} `json:"workspaces"`
	} `json:"windows"`
}

// Tree lists what the app has. The test's cmux has one window.
func (a *App) Tree() Tree {
	a.t.Helper()
	out := a.CLI("--json", "tree", "--all")
	var ans treeAnswer
	if err := json.Unmarshal([]byte(out), &ans); err != nil || len(ans.Windows) != 1 {
		a.t.Fatalf("cmux tree --all: %v\n%s", err, out)
	}
	w := ans.Windows[0]
	tree := Tree{Titles: map[string]string{}, Surfaces: map[string][]string{}, Selected: strings.ToUpper(w.Selected)}
	for _, ws := range w.Workspaces {
		id := strings.ToUpper(ws.ID)
		tree.Order = append(tree.Order, id)
		tree.Titles[id] = ws.Title
		for _, p := range ws.Panes {
			for _, s := range p.Surfaces {
				tree.Surfaces[id] = append(tree.Surfaces[id], strings.ToUpper(s.ID))
				if id == tree.Selected && p.Focused && s.Focused {
					tree.Focused = strings.ToUpper(s.ID)
				}
			}
		}
	}
	return tree
}
