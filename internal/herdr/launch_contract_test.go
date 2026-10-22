package herdr

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// processInfoJSON is Herdr's answer to 'herdr pane process-info' for a pane whose shell is shell
// and whose foreground group is group, running name.
func processInfoJSON(group, shell int, name string) string {
	return `{"id":"cli:pane:process_info","result":{"process_info":{"foreground_process_group_id":` +
		strconv.Itoa(group) + `,"foreground_processes":[{"argv0":"` + name + `","name":"` + name + `",` +
		`"pid":` + strconv.Itoa(group) + `}],"pane_id":"w2E:p3T","shell_pid":` + strconv.Itoa(shell) + `},` +
		`"type":"pane_process_info"}}`
}

// The launch contract (dispatch.Starter): LaunchInPane types into the pane's shell only once that
// shell waits at its prompt, in the foreground of its terminal, and never into a program that still
// runs there, nor into a pane whose processes Herdr can't say. Each case runs in a synctest bubble, so
// the clock shellWaits reads moves only while it waits between looks, never while the fake herdr runs:
// on a loaded machine a fake herdr can take longer than all of shellSettle to answer.
func TestLaunchInPaneTypesOnlyIntoAWaitingShell(t *testing.T) {
	defer func(d time.Duration) { shellSettle = d }(shellSettle)
	shellSettle = time.Second
	t.Setenv("TMPDIR", t.TempDir()) // for the launch scripts, never run here
	for _, c := range []struct {
		name    string
		answers []string // process-info's answers in turn, the last one repeated; "fail" fails the call
		typed   bool
		err     string
	}{
		{"at its prompt", []string{processInfoJSON(7, 7, "zsh")}, true, ""},
		{"still starting", []string{processInfoJSON(8, 7, "brew"), processInfoJSON(7, 7, "zsh")}, true, ""},
		{"a program runs", []string{processInfoJSON(8, 7, "claude")},
			false, "herdr pane run w2E:p3T: claude still runs there; nothing is typed into it"},
		{"no shell named", []string{processInfoJSON(8, 0, "claude")},
			false, "Herdr names no shell in it"},
		{"Herdr can't say", []string{"fail"}, false, "cannot tell whether a program runs in Herdr pane w2E:p3T"},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				dir := t.TempDir()
				calls, answers := filepath.Join(dir, "calls"), filepath.Join(dir, "answers")
				if err := os.WriteFile(answers, []byte(strings.Join(c.answers, "\n")+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				// Each process-info takes the first answer left, keeping the last one.
				herdrScript(t, "#!/bin/sh\necho \"$*\" >> '"+calls+"'\n"+
					`[ "$1 $2" = "pane run" ] && exit 0`+"\n"+
					`a=$(head -n 1 '`+answers+`')`+"\n"+
					`[ "$(wc -l < '`+answers+`')" -gt 1 ] && sed -i.bak 1d '`+answers+`'`+"\n"+
					`[ "$a" = fail ] && { echo '{"error":{"code":"server_busy","message":"busy"}}' >&2; exit 1; }`+"\n"+
					`echo "$a"`+"\n")
				err := (Terminal{}).LaunchInPane(context.Background(), "w2E:p3T", "claude", []string{"--x", "a b"})
				got, _ := os.ReadFile(calls)
				typed := strings.Contains(string(got), "pane run w2E:p3T  /bin/sh ")
				if typed != c.typed || (err == nil) != (c.err == "") || err != nil && !strings.Contains(err.Error(), c.err) {
					t.Errorf("typed %v, err %v; want typed %v, err %q\ncalls:\n%s", typed, err, c.typed, c.err, got)
				}
				if !strings.HasPrefix(string(got), "pane process-info --pane w2E:p3T\n") {
					t.Errorf("the pane's processes should be read first:\n%s", got)
				}
			})
		})
	}
}
