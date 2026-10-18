//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/project"
)

// specTreeBd answers as bd does once a breakdown has filed s-1, the root epic, with the epic s-1.1
// (and its ticket s-1.1.1) and the ticket s-1.2 under it, none of them ready, and no blocks cycle.
// bd create files, in turn, s-1, s-1.1, s-1.1.1 and s-1.2. Each call goes into bd-calls, its
// arguments followed by |.
const specTreeBd = `#!/bin/sh
d=$(dirname "$0")
for a in "$@"; do printf '%s|' "$a"; done >> "$d/bd-calls"; echo >> "$d/bd-calls"
cmd=$1 parent=
while [ $# -gt 0 ]; do [ "$1" = --parent ] && parent=$2; shift; done
case "$cmd" in
create)
	n=$(cat "$d/bd-n" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "$d/bd-n"
	case $n in 1) id=s-1 ;; 2) id=s-1.1 ;; 3) id=s-1.1.1 ;; *) id=s-1.2 ;; esac
	echo "{\"id\":\"$id\"}" ;;
show) echo '[{"id":"s-1","status":"open","issue_type":"epic"}]' ;;
list)
	case "$parent" in
	s-1) echo '[{"id":"s-1.1","status":"open","issue_type":"epic"},{"id":"s-1.2","status":"open","issue_type":"task"}]' ;;
	s-1.1) echo '[{"id":"s-1.1.1","status":"open","issue_type":"task"}]' ;;
	*) echo '[]' ;;
	esac ;;
ready|dep) echo '[]' ;;
esac
`

// fakeBreakdown files the tree specTreeBd knows, in bd, and answers with its root epic, s-1, and
// assumptions; or, with root "", files nothing and names none. during runs once it is asked, before
// it files: it may stop the run. It keeps the request it was given, and the run lock's holder then.
type fakeBreakdown struct {
	repo        string
	root        string
	assumptions []string
	during      func(ctx context.Context)

	got    breakdownRequest
	holder project.Holder
	asked  bool
}

func (f *fakeBreakdown) BreakDown(ctx context.Context, r breakdownRequest) (breakdownResult, error) {
	f.got, f.asked = r, true
	if h, held, err := project.RunHolder(ctx, f.repo); err == nil && held {
		f.holder = h
	}
	if f.during != nil {
		f.during(ctx)
	}
	if f.root == "" {
		return breakdownResult{}, ctx.Err()
	}
	tracker := beads.Tracker{Repo: f.repo}
	for _, t := range []beads.NewTicket{
		{Title: "An app", Type: "epic", Priority: 1},
		{Title: "Listing", Type: "epic", Priority: 1, Parent: "s-1"},
		{Title: "List the things", Type: "task", Priority: 1, Parent: "s-1.1"},
		{Title: "Sort the things", Type: "task", Priority: 2, Parent: "s-1"},
	} {
		if _, err := tracker.Create(ctx, t); err != nil {
			return breakdownResult{Root: f.root}, err
		}
	}
	return breakdownResult{Root: f.root, Assumptions: f.assumptions}, ctx.Err()
}

// useBreakdown has the spec runs of the test broken down by f.
func useBreakdown(t *testing.T, f specBreakdown) {
	t.Helper()
	old := breakdownOf
	breakdownOf = func(*session) specBreakdown { return f }
	t.Cleanup(func() { breakdownOf = old })
}

// specRunFixture is a repository set up for orchestra, inside a Herdr pane, without notifications,
// triage or the run report, with specTreeBd on PATH and a herdr and a claude that fail; a spec
// outside it, which it returns with the folder of the fake bd.
func specRunFixture(t *testing.T) (repo, spec, tools string) {
	t.Helper()
	repo = specFixture(t)
	for k, v := range map[string]string{"NOTIFY": "0", "TRIAGE": "0", "REVIEW": "0"} {
		t.Setenv(k, v)
	}
	tools = t.TempDir()
	faketool.Write(t, tools, "bd", specTreeBd)
	faketool.Write(t, tools, "herdr", "#!/bin/sh\necho 'herdr: not in this test' >&2\nexit 1\n")
	faketool.Write(t, tools, "claude", "#!/bin/sh\necho 'claude: not in this test' >&2\nexit 1\n")
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	spec = writeSpec(t, filepath.Join(t.TempDir(), "app.md"), "# An app\n\nIt lists things, sorted.\n")
	return repo, spec, tools
}

// runSpecIn is orchestra with these arguments in repo, after setup.
func runSpecIn(t *testing.T, repo string, setup func(repo string), args ...string) (stdout, stderr string, code int) {
	t.Helper()
	setup(repo)
	var out, errOut strings.Builder
	err := run(context.Background(), append([]string{"orchestra"}, args...), cmdEnv{dir: ".", getenv: os.Getenv, stdin: strings.NewReader(""), stdout: &out, stderr: &errOut})
	return out.String(), errOut.String(), exitOf(err)
}

// gitIn runs git in repo, failing the test if it fails.
func gitIn(t *testing.T, repo string, args ...string) string {
	t.Helper()
	out, err := command.Output(context.Background(), 0, repo, "git", args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// A spec run commits the spec, has it broken down, and runs the root epic the breakdown names,
// without asking and with no ticket limit; the lock, the START line, the log and the start record
// name the spec. Run again, the spec unchanged is not committed again, and --limit and --refine are
// passed on.
func TestSpecRunRunsTheRootEpic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra twice on real git with fake tools")
	}
	repo, spec, tools := specRunFixture(t)
	f := &fakeBreakdown{repo: repo, root: "s-1", assumptions: []string{"Things are sorted by name,\nA to Z."}}
	useBreakdown(t, f)
	const kept = ".orchestra/specs/app.md"

	stdout, stderr, code := runSpecIn(t, repo, leaveWorker(t, "s-1.2"), "--spec", spec, "--plain")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	head := gitIn(t, repo, "rev-parse", "HEAD")
	if subject, files := gitIn(t, repo, "log", "-1", "--format=%s"), gitIn(t, repo, "show", "--name-only",
		"--format=", "HEAD"); subject != "orchestra: add spec app.md" || files != kept {
		t.Errorf("HEAD: %q with %q", subject, files)
	}
	want := breakdownRequest{Spec: project.Spec{Path: kept, Commit: head}}
	want.SHA256 = f.got.SHA256
	if f.got != want || len(f.got.SHA256) != 64 {
		t.Errorf("the breakdown was asked %+v, want %+v", f.got, want)
	}
	if f.holder.Spec != kept {
		t.Errorf("the lock's holder during the breakdown: %+v", f.holder)
	}
	short := f.got.SHA256[:12]
	for _, want := range []string{"committed the spec " + kept + " (sha256 " + short + ")\n",
		"broke the spec down into s-1 (2 epics, 2 tickets)\n",
		"Assumed where the spec leaves a choice open:\n  - Things are sorted by name, A to Z.\n",
		" · spec " + kept + " (done so far: 0, limit: none,", "  spec: epic s-1, broken down from: " + kept + "\n"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if calls := read(t, filepath.Join(tools, "bd-calls")); !strings.Contains(calls, "|--parent|s-1|\n") ||
		!strings.Contains(calls, "ready|--json|--limit|0|--exclude-label|human|--exclude-type|epic|--parent|s-1|\n") {
		t.Errorf("the run wasn't scoped to s-1:\n%s", calls)
	}
	log := unstamped(read(t, filepath.Join(repo, project.Dir, "orchestra.log")))
	for _, want := range []string{"SPEC: committed " + kept + " (sha256 " + short + ")\n",
		"SPEC: broken down into s-1 (2 epics, 2 tickets)\n", "SPEC: assumed: Things are sorted by name, A to Z.\n"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q:\n%s", want, log)
		}
	}
	if recs := streamRecords(t, repo); recs[0]["kind"] != "start" || recs[0]["spec"] != kept || recs[0]["scope"] != "s-1" {
		t.Errorf("start record: %v", recs[0])
	}

	stdout, stderr, code = runSpecIn(t, repo, leaveWorker(t, "s-1.2"), "--spec", spec, "--plain",
		"--refine", "--limit", "3")
	if code != 0 {
		t.Fatalf("again: exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	if again := gitIn(t, repo, "rev-parse", "HEAD"); again != head {
		t.Errorf("an unchanged spec was committed again: HEAD %s, was %s", again, head)
	}
	want.Refine = true
	if f.got != want {
		t.Errorf("again: the breakdown was asked %+v, want %+v", f.got, want)
	}
	for _, want := range []string{"the spec " + kept + " is committed already (sha256 " + short + ")\n",
		" · spec " + kept + " (done so far: 0, limit: 3,"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("again: stdout lacks %q:\n%s", want, stdout)
		}
	}
}

// A spec inside the repository, untracked, is committed where it is; the checkout must have nothing
// else uncommitted, or nothing is committed or broken down.
func TestSpecRunCommitsASpecInsideInPlace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra twice on real git with fake tools")
	}
	repo, _, _ := specRunFixture(t)
	f := &fakeBreakdown{repo: repo, root: "s-1"}
	useBreakdown(t, f)
	spec := writeSpec(t, filepath.Join(repo, "docs", "app.md"), "# An app\n")
	stray := writeSpec(t, filepath.Join(repo, "stray.txt"), "not mine\n")
	head := gitIn(t, repo, "rev-parse", "HEAD")

	_, stderr, code := runSpecIn(t, repo, func(string) {}, "--spec", spec, "--plain")
	if code != 5 || !strings.Contains(stderr, "orchestra cannot start: uncommitted changes in") {
		t.Errorf("a stray file: exit %d, stderr:\n%s", code, stderr)
	}
	if gitIn(t, repo, "rev-parse", "HEAD") != head || f.asked {
		t.Errorf("committed or broke the spec down with a stray file (asked %v)", f.asked)
	}

	if err := os.Remove(stray); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runSpecIn(t, repo, leaveWorker(t, "s-1.2"), "--spec", "docs/app.md", "--plain")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	if files := gitIn(t, repo, "show", "--name-only", "--format=%s", "HEAD"); files != "orchestra: add spec app.md\n\ndocs/app.md" {
		t.Errorf("HEAD: %q", files)
	}
	if f.got.Path != "docs/app.md" || !strings.Contains(stdout, " · spec docs/app.md (") {
		t.Errorf("asked about %q; stdout:\n%s", f.got.Path, stdout)
	}
}

// A breakdown that names no root epic ends the run, exit 2, once the spec is committed; the run
// doesn't start its loop.
func TestSpecRunWithoutARootEpic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools")
	}
	repo, spec, _ := specRunFixture(t)
	useBreakdown(t, &fakeBreakdown{repo: repo})
	stdout, stderr, code := runSpecIn(t, repo, func(string) {}, "--spec", spec, "--plain")
	if code != 2 || !strings.Contains(stderr, "orchestra: the spec's breakdown named no root epic, so there is "+
		"nothing to run.") || strings.Contains(stdout, "START") {
		t.Errorf("no root epic: exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	if subject := gitIn(t, repo, "log", "-1", "--format=%s"); subject != "orchestra: add spec app.md" {
		t.Errorf("the spec wasn't committed: HEAD %q", subject)
	}
	recs := streamRecords(t, repo)
	if kindsOf(recs) != "start end" || recs[0]["spec"] != ".orchestra/specs/app.md" || recs[0]["scope"] != nil ||
		recs[1]["code"] != 2.0 {
		t.Errorf("records: %v", recs)
	}
}

// Ctrl+C during the breakdown ends the run, exit 130, naming the root epic filed so far and how to
// carry on; the run's records name the spec.
func TestSpecRunCtrlCDuringTheBreakdown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools")
	}
	repo, spec, _ := specRunFixture(t)
	f := &fakeBreakdown{repo: repo, root: "s-1", during: func(ctx context.Context) {
		kill(t, syscall.SIGINT)
		<-ctx.Done()
	}}
	useBreakdown(t, f)
	stdout, stderr, code := runSpecIn(t, repo, func(string) {}, "--spec", spec, "--plain")
	if code != 130 || strings.Contains(stdout, "START") {
		t.Errorf("exit %d, stdout:\n%s", code, stdout)
	}
	if want := "orchestra: stopped while the spec was broken down, with s-1 filed.\n" +
		"Run it again to carry on: orchestra --spec " + spec + "\n"; !strings.HasSuffix(stderr, want) {
		t.Errorf("stderr:\n%s\nwant it to end in:\n%s", stderr, want)
	}
	recs := streamRecords(t, repo)
	if kindsOf(recs) != "start end" || recs[0]["spec"] != ".orchestra/specs/app.md" || recs[1]["code"] != 130.0 {
		t.Errorf("records: %v", recs)
	}
	if log := read(t, filepath.Join(repo, project.Dir, "orchestra.log")); !strings.Contains(log,
		"SPEC: stopped during the breakdown, with s-1 filed\n") {
		t.Errorf("the log:\n%s", log)
	}
}
