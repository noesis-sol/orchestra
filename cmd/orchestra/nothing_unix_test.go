//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/project"
)

// backlog is what nothingTools' bd answers: bd ready (or, with readyFails, an error), the tickets
// not closed, those closed but not merged, and by ID each ticket's subtickets and bd show. What
// isn't given is [] (bd show: an open task).
type backlog struct {
	ready, unclosed, unmerged string
	readyFails                bool
	children, show            map[string]string
}

// nothingTools puts on PATH a bd answering b, a claude and an osascript that only record their
// calls (claude-calls, osascript-calls), and a herdr that fails. bd records its calls in bd-calls,
// each argument followed by |. It returns the folder they keep their files in.
func nothingTools(t *testing.T, b backlog) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{"ready": b.ready, "unclosed": b.unclosed, "unmerged": b.unmerged} {
		if body != "" {
			write(name, body)
		}
	}
	for id, body := range b.children {
		write("children."+id, body)
	}
	for id, body := range b.show {
		write("show."+id, body)
	}
	if b.readyFails {
		write("ready-fails", "")
	}
	faketool.Write(t, dir, "bd", `#!/bin/sh
d='`+dir+`'
for a in "$@"; do printf '%s|' "$a"; done >> "$d/bd-calls"; echo >> "$d/bd-calls"
answer() { if [ -f "$d/$1" ]; then cat "$d/$1"; else echo "${2:-[]}"; fi; }
parent=; prev=
for a in "$@"; do [ "$prev" = --parent ] && parent=$a; prev=$a; done
case "$1" in
ready) [ -f "$d/ready-fails" ] && { echo 'Error: database is locked' >&2; exit 1; }; answer ready ;;
show) answer "show.$2" "[{\"id\":\"$2\",\"status\":\"open\",\"issue_type\":\"task\"}]" ;;
list)
	case "$*" in
	*"--label unmerged"*) answer unmerged ;;
	*--parent*) answer "children.$parent" ;;
	*) answer unclosed ;;
	esac ;;
esac
`)
	for _, name := range []string{"claude", "osascript"} {
		faketool.Write(t, dir, name, "#!/bin/sh\nprintf '[%s]\\n' \"$@\" >> '"+filepath.Join(dir, name+"-calls")+"'\n"+
			"echo '{\"is_error\":true,\"result\":\"not in this test\"}'\n")
	}
	faketool.Write(t, dir, "herdr", "#!/bin/sh\necho 'herdr: not in this test' >&2\nexit 1\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// leaveWorker returns what leaves, in a repository, a worker the last run left on ticket id in
// state.json, its worktree gone since: the check counts it as something to run, and the loop
// drops it.
func leaveWorker(t *testing.T, id string) func(repo string) {
	t.Helper()
	return func(repo string) {
		t.Helper()
		if err := project.SaveState(repo, project.RunState{Workers: []project.LeftWorker{{
			Ticket: id, Agent: "orchestra-" + id, Tab: "w7Q:t9", Worktree: filepath.Join(t.TempDir(), id), Left: "PAUSED",
		}}}); err != nil {
			t.Fatal(err)
		}
	}
}

// runNothing is orchestra with these arguments in a repository set up for it and then by setup,
// inside a Herdr pane, with notifications, triage and the run report on, and stdout not a terminal.
func runNothing(t *testing.T, setup func(repo string), args ...string) (repo, stdout, stderr string, code int) {
	t.Helper()
	repo = configFixture(t, `{"concurrent": 1}`)
	for k, v := range map[string]string{"NOTIFY": "1", "TRIAGE": "1", "REVIEW": "1", "ORCHESTRA_TICKETS": ""} {
		t.Setenv(k, v)
	}
	setup(repo)
	var out, errOut strings.Builder
	err := run(context.Background(), append([]string{"orchestra"}, args...), os.Getenv, strings.NewReader(""),
		&out, &errOut)
	return repo, out.String(), errOut.String(), exitOf(err)
}

// stamped matches the time a plain line starts with.
var stamped = regexp.MustCompile(`(?m)^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d `)

// unstamped is plain output without the time each line starts with.
func unstamped(out string) string { return stamped.ReplaceAllString(out, "") }

// nothingRecords checks that the run in repo recorded only its start, its done line and its end,
// with code 0, in the event stream, and the done line in the log.
func nothingRecords(t *testing.T, repo, done string) {
	t.Helper()
	recs := streamRecords(t, repo)
	if got := kindsOf(recs); got != "start done end" {
		t.Fatalf("records: %s, want start done end\n%v", got, recs)
	}
	if recs[1]["text"] != done || recs[2]["code"] != 0.0 {
		t.Errorf("done and end records: %v, %v; want the done line %q and code 0", recs[1], recs[2], done)
	}
	if lines := unstamped(read(t, filepath.Join(repo, project.Dir, "orchestra.log"))); !strings.Contains(lines, done+"\n") {
		t.Errorf("the log lacks %q:\n%s", done, lines)
	}
}

// noCalls checks that the run called neither claude (no triage, no run report) nor osascript (no
// notification).
func noCalls(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"claude", "osascript"} {
		if b, err := os.ReadFile(filepath.Join(dir, name+"-calls")); !os.IsNotExist(err) {
			t.Errorf("%s was called (%v):\n%s", name, err, b)
		}
	}
}

// A run that doesn't ask, with nothing in the backlog, says all is done and exits 0, before its
// loop: no DIRTY_TREE for the uncommitted file, no triage or run report, no notification.
func TestNothingToRunWhenAllIsDone(t *testing.T) {
	for _, args := range [][]string{{"--tickets"}, {"-plain"}} {
		t.Run(args[0], func(t *testing.T) {
			dir := nothingTools(t, backlog{})
			repo, stdout, stderr, code := runNothing(t, func(repo string) {
				if err := os.WriteFile(filepath.Join(repo, "dirty.go"), []byte("package x\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}, args...)
			if code != dispatch.ExitOK || stderr != "" {
				t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
			}
			if !stamped.MatchString(stdout) || unstamped(stdout) != "✓ All done\n" {
				t.Errorf("stdout:\n%s", stdout)
			}
			nothingRecords(t, repo, "READY_EMPTY after 0 tickets: everything is done")
			noCalls(t, dir)
		})
	}
}

// With tickets left but none ready, the run says what holds them, and exits 0: epics, never run,
// don't count, and the tickets closed but not merged do.
func TestNothingReadyToRun(t *testing.T) {
	dir := nothingTools(t, backlog{
		unclosed: `[{"id":"k-q","status":"open","issue_type":"task","labels":["human"]},` +
			`{"id":"k-1","status":"open","issue_type":"task"},{"id":"k-2","status":"blocked","issue_type":"bug"},` +
			`{"id":"k-3","status":"in_progress","issue_type":"task"},{"id":"k-4","status":"deferred","issue_type":"task"},` +
			`{"id":"k-e","status":"open","issue_type":"epic"}]`,
		unmerged: `[{"id":"k-5","status":"closed","issue_type":"task","labels":["unmerged"]}]`,
	})
	repo, stdout, stderr, code := runNothing(t, func(string) {}, "--tickets")
	if code != dispatch.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	if want := "○ Nothing ready to run\n" +
		"  1 question waits for your answer: bd human list\n" +
		"  2 tickets wait on other tickets: bd blocked\n" +
		"  1 in progress · 1 deferred\n" +
		"  1 closed but not merged: bd list --label unmerged\n"; unstamped(stdout) != want {
		t.Errorf("stdout:\n%s\nwant, after the time:\n%s", stdout, want)
	}
	nothingRecords(t, repo, "READY_EMPTY after 0 tickets: nothing ready")
	noCalls(t, dir)
}

// --ticket with nothing ready under it says why each of its tickets isn't done, in the message and,
// as SCOPE_OPEN gives them, at the end of the done line.
func TestNothingUnderTheScopeIsReady(t *testing.T) {
	dir := nothingTools(t, backlog{
		children: map[string]string{
			"k-e": `[{"id":"k-e.1","status":"open","issue_type":"task","parent":"k-e"},` +
				`{"id":"k-e.2","status":"in_progress","issue_type":"task","parent":"k-e"}]`,
		},
		show: map[string]string{
			"k-e": `[{"id":"k-e","status":"open","issue_type":"epic"}]`,
			"k-e.1": `[{"id":"k-e.1","status":"open","issue_type":"task","dependencies":` +
				`[{"id":"k-x","status":"open","dependency_type":"blocks"}]}]`,
			"k-e.2": `[{"id":"k-e.2","status":"in_progress","issue_type":"task"}]`,
		},
	})
	repo, stdout, stderr, code := runNothing(t, func(string) {}, "--ticket", "k-e")
	if code != dispatch.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	if want := "○ Nothing under k-e is ready to run\n" +
		"  k-e.1 (blocked by k-x outside the scope)\n" +
		"  k-e.2 (in progress)\n"; unstamped(stdout) != want {
		t.Errorf("stdout:\n%s\nwant, after the time:\n%s", stdout, want)
	}
	nothingRecords(t, repo, "READY_EMPTY after 0 tickets: nothing ready; SCOPE_OPEN: k-e: 2 of its 2 subtickets not done: "+
		"k-e.1 (blocked by k-x outside the scope), k-e.2 (in progress)")
	if calls := read(t, filepath.Join(dir, "bd-calls")); !strings.Contains(calls, "--parent|k-e|") {
		t.Errorf("the check wasn't scoped to k-e:\n%s", calls)
	}
	noCalls(t, dir)
}

// With nothing ready, a worker the last run left (in the scope) is still something to run: the
// run goes to its loop as before, which ends READY_EMPTY and notifies.
func TestNothingReadyButAWorkerLeftRuns(t *testing.T) {
	for _, tc := range []struct {
		name, left string
		args       []string
	}{
		{"all of bd ready", "k-1", []string{"--tickets"}},
		{"scoped", "k-e", []string{"--ticket", "k-e"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := nothingTools(t, backlog{show: map[string]string{"k-e": `[{"id":"k-e","status":"open","issue_type":"epic"}]`}})
			// The report would be claude's: not this test's.
			repo, stdout, stderr, code := runNothing(t, leaveWorker(t, tc.left), append(tc.args, "-review=false")...)
			if code != dispatch.ExitOK {
				t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
			}
			for _, want := range []string{"START orchestra", tc.left + ", carried over from the last run, is dropped",
				"READY_EMPTY after 0 tickets"} {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout lacks %q:\n%s", want, stdout)
				}
			}
			if strings.Contains(stdout, "✓ All done") || strings.Contains(stdout, "○ Nothing") {
				t.Errorf("said there was nothing to run:\n%s", stdout)
			}
			if got := kindsOf(streamRecords(t, repo)); !strings.HasPrefix(got, "start info") {
				t.Errorf("records: %s, want the loop's", got)
			}
			if _, err := os.Stat(filepath.Join(dir, "osascript-calls")); err != nil {
				t.Errorf("the run's end wasn't notified: %v", err)
			}
		})
	}
}

// What the check doesn't change: a run whose limit is reached ends LIMIT_REACHED, and one whose
// bd ready fails goes to its loop, which reports it.
func TestNothingToRunLeavesTheLoopsEnds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		b     backlog
		args  []string
		code  int
		final string
	}{
		{"limit reached", backlog{}, []string{"--tickets", "--done-so-far", "3", "--limit", "3"}, dispatch.ExitOK,
			"LIMIT_REACHED at 3 tickets"},
		{"bd ready fails", backlog{readyFails: true}, []string{"--tickets"}, dispatch.ExitTool, "READY_UNREADABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nothingTools(t, tc.b)
			_, stdout, stderr, code := runNothing(t, func(string) {}, append(tc.args, "-review=false")...)
			if code != tc.code || !strings.Contains(stdout, tc.final) || strings.Contains(stdout, "✓ All done") {
				t.Errorf("exit %d, want %d and %s; stderr:\n%s\nstdout:\n%s", code, tc.code, tc.final, stderr, stdout)
			}
		})
	}
}
