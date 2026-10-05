//go:build unix

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/faketool"
)

const featureScreenOK = `{"type":"result","is_error":false,"structured_output":{"verdict":"ok","reason":"A clear change."}}`

const featurePlanJSON = `{"epic":{"title":"JSON output","description":"Machine-readable output."},"tickets":[` +
	`{"key":"t1","title":"Add the JSON encoder","type":"feature","priority":1,"description":"d1","acceptance":"a1",` +
	`"files":["enc.go"],"blocked_by":[]},` +
	`{"key":"t2","title":"Add the --json flag","type":"task","priority":2,"description":"d2","acceptance":"a2",` +
	`"files":["README.md","nowhere/x.go"],"blocked_by":["t1"]}],"questions":[]}`

// featureTools puts on PATH a claude answering the screen organ with screen and the plan organ with
// plan, a bd that files tickets as f-1 (the epic), f-1.1 and so on and knows of nothing else, and a
// herdr that fails. bd fails the create numbered failCreate (from 1); 0 fails none. It returns
// the folder where claude keeps each call's arguments and input (args.N, in.N) and bd its calls,
// one per line with each argument followed by |.
func featureTools(t *testing.T, screen, plan string, failCreate int) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("screen", screen)
	write("plan", plan)
	faketool.Write(t, dir, "claude", `#!/bin/sh
d='`+dir+`'
n=$(cat "$d/claude-n" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "$d/claude-n"
printf '[%s]\n' "$@" > "$d/args.$n"
cat > "$d/in.$n"
case "$*" in
*"You screen feature requests"*) cat "$d/screen" ;;
*"You plan feature requests"*) cat "$d/plan" ;;
*) echo '{"is_error":true,"result":"not this organ"}' ;;
esac
`)
	faketool.Write(t, dir, "bd", `#!/bin/sh
d='`+dir+`'
for a in "$@"; do printf '%s|' "$a"; done >> "$d/bd-calls"; echo >> "$d/bd-calls"
case "$1" in
create)
	n=$(cat "$d/bd-n" 2>/dev/null || echo 0); n=$((n+1))
	[ "$n" = "`+strconv.Itoa(failCreate)+`" ] && { echo 'Error: database is locked' >&2; exit 1; }
	echo $n > "$d/bd-n"
	if [ $n = 1 ]; then echo '{"id":"f-1"}'; else echo "{\"id\":\"f-1.$((n-1))\"}"; fi ;;
show) echo "[{\"id\":\"$2\",\"status\":\"open\",\"issue_type\":\"epic\"}]" ;;
list|ready) echo '[]' ;;
esac
`)
	faketool.Write(t, dir, "herdr", "#!/bin/sh\necho 'herdr: not in this test' >&2\nexit 1\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// runFeatureIn is orchestra with these arguments in a repository set up for it, with a README,
// inside a Herdr pane, without notifications, triage or the run report.
func runFeatureIn(t *testing.T, args ...string) (repo, stdout, stderr string, code int) {
	t.Helper()
	return runFeatureFrom(t, strings.NewReader(""), args...)
}

// runFeatureFrom is runFeatureIn reading stdin from in.
func runFeatureFrom(t *testing.T, in io.Reader, args ...string) (repo, stdout, stderr string, code int) {
	t.Helper()
	return runFeatureAfter(t, func(string) {}, in, args...)
}

// runFeatureAfter is runFeatureFrom with setup given the repository before orchestra runs in it.
func runFeatureAfter(
	t *testing.T, setup func(repo string), in io.Reader, args ...string,
) (repo, stdout, stderr string, code int) {
	t.Helper()
	repo = configFixture(t, `{"concurrent": 1}`)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# lister\n\nLists things.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := command.Output(context.Background(), 0, repo, "sh", "-c",
		"git add README.md && git -c user.name=t -c user.email=t@t commit -q -m readme"); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"NOTIFY": "0", "TRIAGE": "0", "REVIEW": "0"} {
		t.Setenv(k, v)
	}
	setup(repo)
	var out, errOut strings.Builder
	err := run(context.Background(), append([]string{"orchestra"}, args...), os.Getenv, in, &out, &errOut)
	return repo, out.String(), errOut.String(), exitOf(err)
}

// A request the screen organ turns down, or can't judge, stops the run before anything is filed.
func TestFeatureScreeningStopsTheRun(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools, per case")
	}
	for _, tc := range []struct {
		name, screen, want string
	}{
		{"reject", `{"type":"result","is_error":false,"structured_output":{"verdict":"reject","reason":"That sends passwords away."}}`,
			"orchestra won't plan this request: That sends passwords away."},
		{"unclear", `{"type":"result","is_error":false,"structured_output":{"verdict":"unclear","reason":"Say what to improve."}}`,
			"orchestra can't plan this request yet: Say what to improve."},
		{"organ error", `{"type":"result","is_error":true,"result":"usage limit reached"}`,
			"orchestra couldn't screen the request: claude reported an error: usage limit reached"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := featureTools(t, tc.screen, featurePlanJSON, 0)
			_, _, stderr, code := runFeatureIn(t, "--feature", "Add a --json flag", "--yes")
			if code != 2 || !strings.Contains(stderr, tc.want) {
				t.Errorf("exit %d, stderr:\n%s", code, stderr)
			}
			if _, err := os.Stat(filepath.Join(dir, "bd-n")); !os.IsNotExist(err) {
				t.Errorf("bd filed something: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "args.2")); !os.IsNotExist(err) {
				t.Errorf("planned after a %s: %v", tc.name, err)
			}
		})
	}
}

func TestFeatureQuestionsStopTheRun(t *testing.T) {
	dir := featureTools(t, featureScreenOK, `{"type":"result","is_error":false,"structured_output":`+
		`{"epic":{"title":"","description":""},"tickets":[],"questions":["Which commands?","Pretty-printed?"]}}`, 0)
	_, _, stderr, code := runFeatureIn(t, "--feature", "Add JSON output", "--yes")
	for _, want := range []string{"needs answers before it can plan this request:\n  - Which commands?\n  - Pretty-printed?\n",
		"Nothing was filed. Run it again with the answers in the request"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if code != 2 {
		t.Errorf("exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "bd-n")); !os.IsNotExist(err) {
		t.Errorf("bd filed something: %v", err)
	}
}

func TestFeatureWithoutATerminalNeedsYes(t *testing.T) {
	dir := featureTools(t, featureScreenOK, `{"type":"result","is_error":false,"structured_output":`+featurePlanJSON+`}`, 0)
	_, stdout, stderr, code := runFeatureIn(t, "--feature", "Add a --json flag")
	if code != 2 || !strings.Contains(stderr, "Pass --yes to file the plan without asking.") {
		t.Errorf("exit %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "Epic: JSON output") {
		t.Errorf("the plan wasn't shown:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "bd-n")); !os.IsNotExist(err) {
		t.Errorf("bd filed something: %v", err)
	}
}

// With --yes the plan is filed, the epic and its children with their parent, files and links,
// and the run is scoped to the epic; the organs get the model and effort flags. The fake bd has
// none of the epic ready, so a worker the last run left on it has the run go to its loop, as the
// epic's ready tickets would.
func TestFeatureYesFilesThePlanAndRunsTheEpic(t *testing.T) {
	dir := featureTools(t, featureScreenOK, `{"type":"result","is_error":false,"structured_output":`+featurePlanJSON+`}`, 0)
	repo, stdout, stderr, code := runFeatureAfter(t, leaveWorker(t, "f-1"), strings.NewReader(""),
		"--feature", "Add a --json flag, see README.md", "--yes", "--plain", "--organ-model", "opus-x", "--organ-effort", "medium")
	if code != 0 {
		t.Errorf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	for _, want := range []string{"screening the request with claude…", "Epic: JSON output",
		`dropped "nowhere/x.go" from t2's files`, "filed epic f-1 with 2 tickets",
		" · feature f-1 (", "feature: epic f-1, planned from: Add a --json flag, see README.md"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	calls := read(t, filepath.Join(dir, "bd-calls"))
	for _, want := range []string{
		"create|--json|--title=JSON output|--description=Machine-readable output.|--type=epic|--priority=1|\n",
		"create|--json|--title=Add the JSON encoder|--description=d1|--type=feature|--priority=1|--acceptance=a1|" +
			`--parent=f-1|--metadata={"files":["enc.go"]}|` + "\n",
		`--parent=f-1|--metadata={"files":["README.md"]}|` + "\n",
		"dep|add|f-1.2|f-1.1|\n",
		"ready|--json|--limit|0|--exclude-label|human|--exclude-type|epic|--parent|f-1|\n",
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("bd calls lack %q:\n%s", want, calls)
		}
	}
	screenArgs, planArgs := read(t, filepath.Join(dir, "args.1")), read(t, filepath.Join(dir, "args.2"))
	for _, args := range []string{screenArgs, planArgs} {
		if !strings.Contains(args, "[--model]\n[opus-x]\n") || !strings.Contains(args, "[--effort]\n[medium]\n") {
			t.Errorf("an organ ran without the model and effort flags:\n%s", args)
		}
	}
	if in := read(t, filepath.Join(dir, "in.1")); !strings.Contains(in, "repository "+filepath.Base(repo)) ||
		!strings.Contains(in, "Lists things.") {
		t.Errorf("the screen organ wasn't given the README:\n%s", in)
	}
	if in := read(t, filepath.Join(dir, "in.2")); !strings.Contains(in, "File named in the request: README.md") {
		t.Errorf("the plan organ wasn't given the file the request names:\n%s", in)
	}
}

// A bd failure midway stops before the run, listing what was filed.
func TestFeatureFilingFailureStopsBeforeTheRun(t *testing.T) {
	dir := featureTools(t, featureScreenOK, `{"type":"result","is_error":false,"structured_output":`+featurePlanJSON+`}`, 3)
	_, stdout, stderr, code := runFeatureIn(t, "--feature", "Add a --json flag", "--yes", "--plain")
	for _, want := range []string{"orchestra couldn't file ticket t2: bd create", "database is locked",
		"Filed before it:\n  f-1 (epic) JSON output\n  f-1.1 (t1) Add the JSON encoder\n",
		"Remove them with: bd delete f-1.1 f-1 --force", "carry on with: orchestra --ticket f-1"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if code != 4 || strings.Contains(stdout, "START") {
		t.Errorf("exit %d, stdout:\n%s", code, stdout)
	}
	if calls := read(t, filepath.Join(dir, "bd-calls")); strings.Contains(calls, "dep|add") || strings.Contains(calls, "ready|") {
		t.Errorf("went on after the failure:\n%s", calls)
	}
}

// The run's startup checks come first: a run that couldn't start screens and files nothing.
func TestFeatureStartupProblemsComeFirst(t *testing.T) {
	dir := featureTools(t, featureScreenOK, `{"type":"result","is_error":false,"structured_output":`+featurePlanJSON+`}`, 0)
	repo := configFixture(t, `{"concurrent": 1}`)
	if err := os.WriteFile(filepath.Join(repo, "dirty.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errOut strings.Builder
	err := run(context.Background(), []string{"orchestra", "--feature", "Add a flag", "--yes"}, os.Getenv,
		strings.NewReader(""), &out, &errOut)
	if exitOf(err) != 5 || !strings.Contains(errOut.String(), "uncommitted changes in") {
		t.Errorf("dirty checkout: exit %d, stderr:\n%s", exitOf(err), errOut.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "claude-n")); !os.IsNotExist(err) {
		t.Errorf("screened with a dirty checkout: %v", err)
	}

	t.Setenv("HERDR_ENV", "")
	errOut.Reset()
	err = run(context.Background(), []string{"orchestra", "--feature", "Add a flag", "--yes"}, os.Getenv,
		strings.NewReader(""), &out, &errOut)
	if exitOf(err) != 2 || !strings.Contains(errOut.String(), "Not running inside a Herdr pane") {
		t.Errorf("outside Herdr: exit %d, stderr:\n%s", exitOf(err), errOut.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "claude-n")); !os.IsNotExist(err) {
		t.Errorf("screened outside Herdr: %v", err)
	}
}
