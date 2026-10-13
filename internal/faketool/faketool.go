// Package faketool writes the stand-ins tests put on PATH for the tools orchestra runs (bd, herdr,
// claude, git) without making a new executable for each: macOS scans an executable the first time
// it runs, which takes 0.2-0.6 seconds, while running it again, or a hard link to it, costs nothing.
//
// Every fake tool is a hard link to one dispatcher per test binary, run once when it is made, that
// runs with /bin/sh the script beside the path it was started by, at that path with .sh added. A
// fake that installs another, as brew would, links it and copies its script beside the link:
//
//	cp "$d/bd.fake.sh" "$HOME/bin/bd.sh" && ln "$d/bd.fake" "$HOME/bin/bd"
//
// A fake too involved for a script, such as a claude that reads its settings and runs their hooks,
// is a Go function the test binary runs (Func, WriteFunc): the binary is already running, so no
// system scans it again.
package faketool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

// dispatcherScript is the dispatcher. Sourced, a tool's script keeps the tool's arguments, $0 and
// process, as if it were the tool itself.
const dispatcherScript = "#!/bin/sh\n. \"$0.sh\"\n"

// The dispatcher, made by the first Write and removed by Main once the tests are over: a test
// binary that writes no fake, such as one a test starts as a helper and kills, makes none.
var (
	inMain     bool   // Main runs the tests
	tempDir    string // where the dispatcher goes: the temporary folder when the tests started
	once       sync.Once
	dispatcher string // the dispatcher's path, once made
	madeIn     string // the folder holding it, removed by Main
	makeErr    error
)

// TestingM is a package's tests, as its TestMain gets them: a *testing.M.
type TestingM interface{ Run() int }

// Main returns m with the dispatcher removed once its tests have run. A package whose tests call
// Write runs them through it, from its TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(faketool.Main(m).Run()) }
func Main(m TestingM) TestingM { return tests{m} }

type tests struct{ m TestingM }

func (ts tests) Run() int {
	if name, ok := os.LookupEnv(funcVar); ok {
		os.Exit(runFunc(name)) // a fake tool's run, not the tests: nothing of the tests' must run after it
	}
	inMain, tempDir = true, os.TempDir()
	code := ts.m.Run()
	if madeIn != "" {
		if err := os.RemoveAll(madeIn); err != nil {
			fmt.Fprintln(os.Stderr, "faketool:", err) // said, not failed: a leftover temporary folder breaks no test
		}
	}
	return code
}

// Write puts in dir a fake tool called name that runs script with /bin/sh, and returns its path. The
// tool is a hard link to the dispatcher, and script is in the file beside it, name.sh.
func Write(t testing.TB, dir, name, script string) string {
	t.Helper()
	if !inMain {
		t.Fatal("faketool.Write: the package's TestMain doesn't run its tests through faketool.Main")
	}
	once.Do(makeDispatcher)
	if makeErr != nil {
		t.Fatal(makeErr)
	}
	bin := filepath.Join(dir, name)
	if err := os.WriteFile(bin+".sh", []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(dispatcher, bin); err != nil {
		t.Fatal(err)
	}
	return bin
}

// makeDispatcher makes the dispatcher, read-only so that no fake can write over the others, and runs
// it once, so that the system's scan is over before a test runs a fake.
func makeDispatcher() {
	madeIn, makeErr = os.MkdirTemp(tempDir, "faketool-")
	if makeErr != nil {
		return
	}
	d := filepath.Join(madeIn, "dispatcher")
	if makeErr = os.WriteFile(d, []byte(dispatcherScript), 0o555); makeErr != nil {
		return
	}
	if makeErr = os.WriteFile(d+".sh", nil, 0o644); makeErr != nil {
		return
	}
	if _, makeErr = command.Output(context.Background(), command.ReadLimit, madeIn, d); makeErr != nil {
		makeErr = fmt.Errorf("faketool: the dispatcher doesn't run: %w", makeErr)
		return
	}
	dispatcher = d
}

// funcVar names, in the environment of the test binary run as a fake tool, the Func it runs.
const funcVar = "ORCHESTRA_FAKETOOL_FUNC"

// funcs are the fake tools Func registered, by name.
var funcs = map[string]func(args []string) int{}

// Func registers run as the fake tool name, which WriteFunc puts on PATH: run gets the tool's
// arguments and returns its exit status. A package registers its fakes before its TestMain runs
// its tests, from a package variable or init:
//
//	var _ = faketool.Func("claude", fakeClaude)
func Func(name string, run func(args []string) int) bool {
	if _, dup := funcs[name]; dup {
		panic("faketool.Func: " + name + " is registered twice")
	}
	funcs[name] = run
	return true
}

// runFunc runs the fake tool name with the binary's arguments, and returns its exit status.
func runFunc(name string) int {
	run, ok := funcs[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "faketool: no fake tool %q is registered with Func\n", name)
		return 127
	}
	_ = os.Unsetenv(funcVar) // what the tool starts is no fake tool, unless it is one itself
	return run(os.Args[1:])
}

// WriteFunc puts in dir a fake tool called name that runs the function Func registered under that
// name in this test binary, with env (KEY=value each) added to its environment, and returns its
// path. The tool is a script (see Write) that runs the test binary.
func WriteFunc(t testing.TB, dir, name string, env ...string) string {
	t.Helper()
	if _, ok := funcs[name]; !ok {
		t.Fatalf("faketool.WriteFunc: no fake tool %q is registered with Func", name)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var script strings.Builder
	for _, kv := range append(env, funcVar+"="+name) {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			t.Fatalf("faketool.WriteFunc: %q is no KEY=value", kv)
		}
		script.WriteString(k + "=" + command.ShellQuote(v) + " ")
	}
	script.WriteString("exec " + command.ShellQuote(self) + " \"$@\"\n")
	return Write(t, dir, name, script.String())
}
