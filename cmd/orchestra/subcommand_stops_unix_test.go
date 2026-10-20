//go:build unix

package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/update"
)

// interruptOnRequest is a transport that sends the test's own process SIGINT at the first request,
// then holds it until the request's context ends, or for patience.
type interruptOnRequest struct{}

func (interruptOnRequest) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		return nil, err
	}
	select {
	case <-req.Context().Done():
		return nil, req.Context().Err()
	case <-time.After(patience):
		return nil, errors.New("never cancelled")
	}
}

// interruptingBd puts first on the PATH a bd that, the first time it is called with sub, sends the
// test's own process SIGINT and waits until it is stopped, or for patience; other calls print out.
func interruptingBd(t *testing.T, sub, out string) {
	t.Helper()
	dir := t.TempDir()
	once := filepath.Join(dir, "interrupted")
	faketool.Write(t, dir, "bd", "#!/bin/sh\n"+
		"if [ \"$1\" = "+sub+" ] && [ ! -e '"+once+"' ]; then\n"+
		"\t: > '"+once+"'\n\tkill -INT "+strconv.Itoa(os.Getpid())+"\n\tsleep "+strconv.Itoa(int(patience/time.Second))+"\n"+
		"\texit 1\nfi\n"+
		"echo '"+out+"'\n")
	onPath(t, dir)
}

// A stop signal while a subcommand whose stop policy catches stop signals works stops it: it says
// where it stopped, exit code 130. Each subcommand of the table has its case, so that one added
// without a stop policy that holds is noticed.
func TestEachSubcommandStopsWithItsReport(t *testing.T) {
	// Not parallel: it signals the test's own process, which the runs of tests beside it would catch too.
	if testing.Short() {
		t.Skip("skipped by -short: runs the subcommands on real git with fake tools")
	}
	// Each case sets up e for a stop signal in the middle of the subcommand, and gives its arguments
	// and the line it ends with.
	cases := map[string]func(t *testing.T, e *cmdEnv) (args []string, want string){
		"init": func(t *testing.T, e *cmdEnv) ([]string, string) { // stopped in bd init
			interruptingBd(t, "init", "")
			e.dir, _ = gitRepo(t)
			setenv(t, "HOME", t.TempDir())
			return []string{"--check", "make check"}, "orchestra init: stopped\n"
		},
		"plan": func(t *testing.T, e *cmdEnv) ([]string, string) { // stopped reading the tickets
			interruptingBd(t, "list", "[]")
			e.dir, _ = gitRepo(t)
			return nil, "orchestra plan: stopped before proposing any link; nothing changed.\n"
		},
		"update": func(t *testing.T, e *cmdEnv) ([]string, string) { // stopped asking for the latest release
			e.releases = update.Updater{API: "http://127.0.0.1:1", Client: &http.Client{Transport: interruptOnRequest{}}}
			return []string{"--check"}, "orchestra update: stopped.\n"
		},
	}
	for _, c := range subcommands {
		t.Run(c.name, func(t *testing.T) {
			if c.stops != stopCancels {
				t.Skip("a stop signal ends it outright, as Go's default has it")
			}
			setUp, ok := cases[c.name]
			if !ok {
				t.Fatalf("no case for orchestra %s: add one", c.name)
			}
			var out, errOut strings.Builder
			e := testCmdEnv(t, strings.NewReader(""), &out, &errOut)
			args, want := setUp(t, &e)
			err := run(testCtx(context.Background(), t), append([]string{"orchestra", c.name}, args...), e)
			if exitOf(err) != dispatch.ExitInterrupted || !strings.HasSuffix(errOut.String(), want) {
				t.Errorf("exit %d, stderr:\n%s\nwant exit %d, stderr ending %q\nstdout:\n%s",
					exitOf(err), errOut.String(), dispatch.ExitInterrupted, want, out.String())
			}
		})
	}
}
