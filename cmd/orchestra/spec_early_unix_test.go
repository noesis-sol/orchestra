//go:build unix

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/project"
)

// earlyBd answers as bd does while a breakdown files, in turn, the root epic s-1, its environment
// ticket s-1.1 (solo, priority 0) and the ticket s-1.2, the count of those filed in bd-n: ready lists
// the tickets filed, open and blocked by nothing (here, s-1.2 doesn't wait for s-1.1, for the test to
// see when it may start). Each call goes into bd-calls.
const earlyBd = `#!/bin/sh
d=$(dirname "$0")
call=; for a in "$@"; do call=$call$a'|'; done; printf '%s\n' "$call" >> "$d/bd-calls"
n=$(cat "$d/bd-n" 2>/dev/null || echo 0)
root='{"id":"s-1","title":"An app","status":"open","issue_type":"epic","priority":1}'
env='{"id":"s-1.1","title":"Set up the project","status":"open","issue_type":"task","priority":0,"parent":"s-1","labels":["solo"]}'
rest='{"id":"s-1.2","title":"List the things","status":"open","issue_type":"task","priority":2,"parent":"s-1"}'
tickets() { [ "$n" -ge 2 ] && printf '%s' "$env"; [ "$n" -ge 3 ] && printf ',%s' "$rest"; }
all() { [ "$n" -ge 1 ] && printf '%s' "$root"; [ "$n" -ge 2 ] && printf ',%s' "$env"; [ "$n" -ge 3 ] && printf ',%s' "$rest"; }
cmd=$1 first=$2 parent= status=
while [ $# -gt 0 ]; do
	case "$1" in --parent) parent=$2 ;; --status) status=$2 ;; esac
	shift
done
case "$cmd" in
create)
	n=$((n+1)); echo $n > "$d/bd-n"
	case $n in 1) echo '{"id":"s-1"}' ;; 2) echo '{"id":"s-1.1"}' ;; *) echo '{"id":"s-1.2"}' ;; esac ;;
show)
	case "$first" in
	s-1) echo "[$root]" ;;
	s-1.1) echo "[$env]" ;;
	s-1.2) echo "[$rest]" ;;
	*) echo "no issue found matching $first" >&2; exit 1 ;;
	esac ;;
ready) echo "[$(tickets)]" ;;
list)
	if [ "$status" = closed ]; then echo '[]'
	elif [ -n "$parent" ] && [ "$parent" != s-1 ]; then echo '[]'
	elif [ -n "$parent" ]; then echo "[$(tickets)]"
	else echo "[$(all)]"
	fi ;;
dep) echo '[]' ;;
esac
`

// earlyBreakdown files the root epic s-1 and its environment ticket s-1.1, in bd, and waits for the
// run to take s-1.1 up, which a fake tool records in setups (a line with the ticket's ID), before it
// files the rest, s-1.2, and answers; or, with fail, fails instead. It notes whether the run took
// s-1.1 up before it went on.
type earlyBreakdown struct {
	repo, setups string
	fail         bool

	tookUp bool
}

func (f *earlyBreakdown) BreakDown(ctx context.Context, r breakdownRequest) (breakdownResult, error) {
	tracker := beads.Tracker{Repo: f.repo}
	file := func(t beads.NewTicket) (string, error) {
		id, err := tracker.Create(ctx, t)
		if err == nil && r.Filed != nil {
			r.Filed(id)
		}
		return id, err
	}
	root, err := file(beads.NewTicket{Title: "An app", Type: "epic", Priority: 1})
	if err != nil {
		return breakdownResult{}, err
	}
	if _, err := file(beads.NewTicket{Title: "Set up the project", Type: "task", Parent: root,
		Labels: []string{dispatch.SoloLabel}}); err != nil {
		return breakdownResult{Root: root}, err
	}
	for deadline := time.Now().Add(time.Minute); !f.tookUp && time.Now().Before(deadline) && ctx.Err() == nil; {
		got, _ := os.ReadFile(f.setups)
		f.tookUp = strings.Contains(string(got), "s-1.1\n")
		time.Sleep(10 * time.Millisecond)
	}
	if f.fail {
		return breakdownResult{Root: root}, errors.New("claude ran out of turns")
	}
	if _, err := file(beads.NewTicket{Title: "List the things", Type: "task", Priority: 2, Parent: root}); err != nil {
		return breakdownResult{Root: root}, err
	}
	return breakdownResult{Root: root}, ctx.Err()
}

// earlyFixture is specRunFixture with earlyBd, two workers at a time, no hold for the environment
// and a setup, which records the ticket of each worktree it runs in, in setups, and then fails, or
// with hang, waits to be stopped. It returns the breakdown, which runs the spec runs of the test.
func earlyFixture(t *testing.T, fail, hang bool) (repo, spec string, f *earlyBreakdown) {
	t.Helper()
	repo = configFixture(t, `{"concurrent": 2, "setup": "fakesetup", "footprint": false, "environment_hold": {"count": 0}}`)
	for k, v := range map[string]string{"ORCHESTRA_SPEC": "", "ORCHESTRA_REFINE": "", "NOTIFY": "0", "TRIAGE": "0",
		"REVIEW": "0"} {
		setenv(t, k, v)
	}
	tools := t.TempDir()
	setups := filepath.Join(tools, "setups")
	then := "exit 1"
	if hang {
		then = "sleep 60"
	}
	faketool.Write(t, tools, "fakesetup", "#!/bin/sh\nbasename \"$PWD\" >> '"+setups+"'\n"+then+"\n")
	faketool.Write(t, tools, "bd", earlyBd)
	// Herdr has no earlier worker in a tab, and is asked nothing else: no worker starts, as the setup fails.
	faketool.Write(t, tools, "herdr", `#!/bin/sh
if [ "$1 $2" = "agent get" ]; then
	echo '{"error":{"code":"agent_not_found","message":"no agent"},"id":"cli:agent:get"}' >&2
else
	echo 'herdr: not in this test' >&2
fi
exit 1
`)
	faketool.Write(t, tools, "claude", "#!/bin/sh\necho 'claude: not in this test' >&2\nexit 1\n")
	onPath(t, tools)
	spec = writeSpec(t, filepath.Join(t.TempDir(), "app.md"), "# An app\n\nIt lists things.\n")
	f = &earlyBreakdown{repo: repo, setups: setups, fail: fail}
	useBreakdown(t, f)
	return repo, spec, f
}

// A spec run starts on the environment ticket as soon as the breakdown has filed it, the rest
// waiting: the run takes it up before the breakdown goes on, waits once it is done for the breakdown
// rather than end, and takes the rest up once the breakdown has filed it and its tree is checked.
func TestSpecRunStartsTheEnvironmentTicketDuringTheBreakdown(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools")
	}
	repo, spec, f := earlyFixture(t, false, false)
	stdout, stderr, code := runSpecIn(t, repo, func(string) {}, "--spec", spec, "--plain")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	if !f.tookUp {
		t.Errorf("the run didn't take s-1.1 up during the breakdown; stdout:\n%s", stdout)
	}
	if got := read(t, f.setups); got != "s-1.1\ns-1.2\n" {
		t.Errorf("set up the worktrees of %q, want s-1.1's, then s-1.2's", got)
	}
	out := unstamped(stdout)
	steps := []string{
		"SPEC: the run starts on s-1.1, the environment ticket, while the breakdown files the rest of s-1\n",
		"[1] s-1.1 dispatching solo: Set up the project\n",
		"for s-1.1; the whole output is in ", // its SETUP_FAILED
		"  waiting for the rest of s-1 to be filed\n",
		"broke the spec down into s-1 (1 epic, 2 tickets)\n",
		"  the rest of s-1 is filed: its tickets join the run\n",
		"[1] s-1.2 dispatching: List the things\n",
		"for s-1.2; the whole output is in ",
		"READY_EMPTY after 0 tickets; SETUP_WAITS: s-1.1, s-1.2 wait",
	}
	at := 0
	for _, step := range steps {
		i := strings.Index(out[at:], step)
		if i < 0 {
			t.Fatalf("stdout lacks %q after the steps before it:\n%s", step, out)
		}
		at += i + len(step)
	}
	log := unstamped(read(t, filepath.Join(repo, project.Dir, "orchestra.log")))
	if !strings.Contains(log, "SPEC: the run starts on s-1.1, the environment ticket, while the breakdown files "+
		"the rest of s-1\n") {
		t.Errorf("the log:\n%s", log)
	}
	if recs := streamRecords(t, repo); recs[0]["kind"] != "start" || recs[0]["scope"] != "s-1" {
		t.Errorf("start record: %v", recs[0])
	}
}

// A breakdown that fails after the run started on the environment ticket ends the run as a breakdown
// failure does, exit 2, saying what it filed and how to carry on; the loop is stopped as Ctrl+C stops
// it, the environment ticket's worktree left, and nothing else starts.
func TestSpecRunWhoseBreakdownFailsAfterTheEnvironmentTicketStarted(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools")
	}
	repo, spec, f := earlyFixture(t, true, true)
	stdout, stderr, code := runSpecIn(t, repo, func(string) {}, "--spec", spec, "--plain")
	if code != 2 || !f.tookUp {
		t.Fatalf("exit %d (took s-1.1 up: %v), stderr:\n%s\nstdout:\n%s", code, f.tookUp, stderr, stdout)
	}
	for _, want := range []string{"orchestra couldn't break the spec down: claude ran out of turns\n",
		"It filed 1 epic and 1 ticket for .orchestra/specs/app.md, under s-1:\n",
		"Run it again to carry on: orchestra --spec " + spec + "\n"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if !strings.Contains(stdout, " INTERRUPTED: stopped because the spec's breakdown failed") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if got := read(t, f.setups); got != "s-1.1\n" {
		t.Errorf("set up the worktrees of %q, want s-1.1's alone", got)
	}
	wts, err := os.ReadDir(repo + "-worktrees")
	if err != nil || len(wts) != 1 || wts[0].Name() != "s-1.1" {
		t.Errorf("worktrees left: %v (%v), want s-1.1's", wts, err)
	}
	recs := streamRecords(t, repo)
	if last := recs[len(recs)-1]; last["kind"] != "end" || last["code"] != 2.0 {
		t.Errorf("end record: %v", last)
	}
}
