//go:build unix

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/project"
)

// A spec run whose fetch brings in a setup with "keep_awake": true keeps the machine awake from then
// on, though it started without; where that setup's terminal isn't the one the run chose as it
// started, the run says it stays in its own, and that the next run takes the setup's.
func TestSpecRunTakesTheFetchedKeepAwakeAndTerminal(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools")
	}
	c, ok := keepAwakeOf(runtime.GOOS)
	if !ok {
		t.Skip("no keep-awake command on " + runtime.GOOS)
	}
	repo, spec, tools, _ := fetchedSetupFixture(t, `{"concurrent": 1, "keep_awake": true, "terminal": "cmux"}`)
	calls := filepath.Join(tools, c.name+"-calls")
	faketool.Write(t, tools, c.name, "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '"+calls+"'\n"+
		"trap \"echo stopped >> '"+calls+"'; exit 0\" TERM\nwhile :; do sleep 1; done\n")

	stdout, stderr, code := runSpecIn(t, repo, leaveWorker(t, "s-1.2"), "--spec", spec, "--plain")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	const line = "SPEC: the fetched settings choose the terminal cmux: this run stays in herdr, chosen as it started; " +
		"the next run takes cmux\n"
	if !strings.Contains(stdout, line) {
		t.Errorf("stdout lacks %q:\n%s", line, stdout)
	}
	want := strings.Join(c.args(os.Getpid()), " ") + "\nstopped\n"
	if got := read(t, calls); got != want {
		t.Errorf("%s's calls:\n%s\nwant (started with orchestra's PID, then stopped):\n%s", c.name, got, want)
	}
	log := unstamped(read(t, filepath.Join(repo, project.Dir, "orchestra.log")))
	if !strings.Contains(log, line) {
		t.Errorf("the log lacks %q:\n%s", line, log)
	}
	if !strings.Contains(log, "keeping the machine awake while the run goes: "+c.line(os.Getpid())+"\n") {
		t.Errorf("the log doesn't say the machine is kept awake:\n%s", log)
	}
}
