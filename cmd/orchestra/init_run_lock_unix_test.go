//go:build unix

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/project"
)

// startRun is a run starting in the repository of the folder it runs in, as far as its lock: it takes
// the lock as a run does, and says on stdout whether it started or who refused it.
func startRun([]string) int {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Println(err)
		return 1
	}
	lock, err := project.LockRun(context.Background(), dir, project.Holder{PID: os.Getpid()})
	if held, ok := errors.AsType[*project.HeldError](err); ok {
		fmt.Println("refused:", held)
		return 2
	}
	if err != nil {
		fmt.Println(err)
		return 1
	}
	_ = lock.Close() // the run is over
	fmt.Println("started")
	return 0
}

var _ = faketool.Func("start-run", startRun)

// A run started while init renames the tickets is refused, naming init: init holds the lock from the
// form's answers to its end, so no run works on tickets renamed under it, in a tree init writes into.
// Once init is over, a run starts.
func TestARunStartedDuringInitIsRefused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	faketool.WriteFunc(t, dir, "start-run")
	faketool.Write(t, dir, "bd", `#!/bin/sh
d='`+dir+`'
echo "bd $*" >> "$d/calls"
case "$1" in
config) echo '{"schema_version":1,"data":{"key":"issue_prefix","value":"orchestra"}}' ;;
count) echo '{"schema_version":1,"data":{"count":3}}' ;;
list) echo '[]' ;;
rename-prefix) "$d/start-run" >> "$d/run"; echo renamed ;;
esac
`)
	onPath(t, dir)
	repo, _ := beadsRepo(t)

	stdout, stderr, err := runIn(t, repo, nil, "init", "--check", "true", "--mcp", "", "--prefix", "orc")
	if err != nil {
		t.Fatalf("init: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if calls := toolCalls(t, dir); !strings.Contains(calls, "bd rename-prefix orc-\n") {
		t.Fatalf("calls:\n%s", calls)
	}
	run := read(t, filepath.Join(dir, "run"))
	if want := fmt.Sprintf(" (orchestra init, pid %d)\n", os.Getpid()); !strings.HasPrefix(run,
		"refused: orchestra is already running in ") || !strings.HasSuffix(run, want) {
		t.Errorf("a run started during the rename: %q, want it refused, naming init (%q)", run, want)
	}
	if h, held, err := project.RunHolder(runCtx(t), repo); err != nil || held {
		t.Errorf("after init: held %v (%+v, %v)", held, h, err)
	}
	lock, err := project.LockRun(runCtx(t), repo, project.Holder{PID: 2})
	if err != nil {
		t.Fatalf("a run after init: %v", err)
	}
	_ = lock.Close()
}
