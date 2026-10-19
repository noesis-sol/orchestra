package scripts

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// The commands check-own.sh runs through go, as the fake go logs them.
const (
	ownVet   = "go vet "
	ownTests = "go run gotest.tools/gotestsum@v1.13.0 "
	ownLint  = "go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run --allow-serial-runners "
)

// fakeGo logs each of its runs to $FAKE_LOG and answers what race-tests.sh and lint.sh ask of go: vet, the
// tests and lint pass unless FAKE_VET, FAKE_TESTS or FAKE_LINT gives another exit status.
const fakeGo = `echo "go $*" >>"$FAKE_LOG"
case "$1" in
env) echo go1.27.1 ;;
list) echo example.com/m ;;
vet) exit "${FAKE_VET:-0}" ;;
run)
	case "$2" in
	gotest.tools/*) exit "${FAKE_TESTS:-0}" ;;
	*) exit "${FAKE_LINT:-0}" ;;
	esac
	;;
esac`

// ownRepo is a repository for check-own.sh: on main, its scripts (hard links to this folder's, so macOS
// doesn't scan new executables, where it can) and a fake ShellCheck, Go packages, a shell script and docs; then on a
// branch of its own. The fake go is first on PATH. A test that makes one skips under -short.
type ownRepo struct {
	t   *testing.T
	dir string
	env []string
	log string
}

func newOwnRepo(t *testing.T) *ownRepo {
	t.Helper()
	if testing.Short() {
		t.Skip("skipped by -short: runs git and check-own.sh, a hundred processes or more")
	}
	r := &ownRepo{t: t, dir: t.TempDir(), log: filepath.Join(t.TempDir(), "go.log")}
	bin := t.TempDir()
	faketool.Write(t, bin, "go", fakeGo)
	r.env = []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + t.TempDir(),
		"TMPDIR=" + t.TempDir(), "FAKE_LOG=" + r.log, "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t",
		"GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com"}
	scripts := filepath.Join(r.dir, "scripts")
	if err := os.MkdirAll(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"check-own.sh", "race-tests.sh", "lint.sh"} {
		linkOrCopy(t, name, filepath.Join(scripts, name))
	}
	faketool.Write(t, scripts, "shellcheck.sh", `echo "shellcheck $*" >>"$FAKE_LOG"; exit "${FAKE_SHELLCHECK:-0}"`)
	for name, text := range map[string]string{
		"go.mod":                       "module example.com/m\n",
		"cmd/orchestra/main.go":        "package main\n",
		"internal/a/a.go":              "package a\n",
		"internal/a/testdata/in.txt":   "in\n",
		"internal/b/b.go":              "package b\n",
		"internal/b/sub/sub.go":        "package sub\n",
		"internal/organ/organ.go":      "package organ\n",
		"internal/organ/prompts/x.md":  "a prompt\n",
		"tools/run.sh":                 "#!/bin/sh\necho run\n",
		"tools/bash.sh":                "#!/bin/bash\necho run\n",
		"README.md":                    "# m\n",
		"docs/organs.md":               "# Organs\n",
		".orchestra/worker-prompt.md":  "Work on TICKET_ID.\n",
		".orchestra/run/rules.md":      "ignored\n",
		".orchestra/.gitignore":        "run/\n",
		"internal/b/sub/sub_test.go":   "package sub\n",
		"internal/organ/organ_test.go": "package organ\n",
	} {
		r.write(name, text)
	}
	r.git("init", "-q", "-b", "main")
	r.git("add", ".")
	r.git("commit", "-q", "-m", "main")
	r.git("switch", "-q", "-c", "wt/m-1")
	return r
}

// linkOrCopy makes to a hard link to from, or a copy where they are on different file systems.
func linkOrCopy(t *testing.T, from, to string) {
	t.Helper()
	if os.Link(from, to) == nil {
		return
	}
	b, err := os.ReadFile(from)
	if err == nil {
		err = os.WriteFile(to, b, 0o755)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// write writes text to the repository's file name, making its folders.
func (r *ownRepo) write(name, text string) {
	r.t.Helper()
	path := filepath.Join(r.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// commit writes each file's text and commits them on the branch.
func (r *ownRepo) commit(files ...string) {
	r.t.Helper()
	for _, name := range files {
		r.write(name, "changed "+name+"\n")
	}
	r.git(append([]string{"add", "--"}, files...)...)
	r.git("commit", "-q", "-m", "change")
}

func (r *ownRepo) git(args ...string) {
	r.t.Helper()
	cmd := exec.CommandContext(r.t.Context(), "git", args...)
	cmd.Dir, cmd.Env = r.dir, r.env
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// check runs check-own.sh with the environment env added, and returns its output, its last line, the go and
// ShellCheck commands it ran, and the error it exited with.
func (r *ownRepo) check(env ...string) (out, verdict string, ran []string, err error) {
	r.t.Helper()
	if err := os.WriteFile(r.log, nil, 0o644); err != nil {
		r.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(r.t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(r.dir, "scripts", "check-own.sh"))
	cmd.Dir, cmd.Env, cmd.Stdin = r.dir, append(slices.Clone(r.env), env...), strings.NewReader("")
	b, err := cmd.CombinedOutput()
	out = string(b)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	logged, rerr := os.ReadFile(r.log)
	if rerr != nil {
		r.t.Fatal(rerr)
	}
	for line := range strings.Lines(string(logged)) {
		if line = strings.TrimSuffix(line, "\n"); !strings.HasPrefix(line, "go env") && line != "go list -m" {
			ran = append(ran, line)
		}
	}
	return out, lines[len(lines)-1], ran, err
}

// ranTests are the tests' runs among ran: the packages and the go test flags after the shuffle seed.
func ranTests(t *testing.T, ran []string) []string {
	t.Helper()
	var tests []string
	for _, c := range ran {
		rest, ok := strings.CutPrefix(c, ownTests)
		if !ok {
			continue
		}
		pkgs, _, _ := strings.Cut(strings.TrimPrefix(rest, "--format=standard-quiet --packages="), " --jsonfile=")
		_, flags, _ := strings.Cut(rest, " -- -race -shuffle=")
		_, flags, _ = strings.Cut(flags, " ")
		tests = append(tests, strings.TrimSpace(pkgs+" "+flags))
	}
	return tests
}

// A branch that changes one package checks that package alone: vet, its race tests without -short, and lint;
// a step that fails fails the check, which says so in its last line.
func TestCheckOwnChecksTheChangedPackage(t *testing.T) {
	t.Parallel()
	r := newOwnRepo(t)
	r.commit("internal/a/a.go")
	out, verdict, ran, err := r.check()
	if err != nil || verdict != "scripts/check-own.sh: PASS" {
		t.Fatalf("%v, last line %q:\n%s", err, verdict, out)
	}
	want := []string{ownVet + "./internal/a", ownLint + "./internal/a"}
	if len(ran) != 3 || ran[0] != want[0] || ran[2] != want[1] {
		t.Errorf("it ran %q, want %q with the tests between", ran, want)
	}
	if tests := ranTests(t, ran); !slices.Equal(tests, []string{"./internal/a"}) {
		t.Errorf("tests %q, want ./internal/a's, with no -short", tests)
	}
	if !strings.Contains(out, "Go packages: ./internal/a\n") {
		t.Errorf("it doesn't say which packages it checked:\n%s", out)
	}

	for _, tc := range []struct{ env, step string }{
		{"FAKE_VET=1", "vet"}, {"FAKE_TESTS=1", "tests"}, {"FAKE_LINT=3", "lint"},
	} {
		out, verdict, _, err := r.check(tc.env)
		code := tc.env[len(tc.env)-1:]
		if err == nil || verdict != "scripts/check-own.sh: FAIL in "+tc.step+" (exit "+code+")" {
			t.Errorf("%s: %v, last line %q:\n%s", tc.env, err, verdict, out)
		}
	}
}

// Changes not committed and untracked files count, a package's testdata is the package's, and a subpackage
// is a package of its own.
func TestCheckOwnCountsChangesNotCommitted(t *testing.T) {
	t.Parallel()
	r := newOwnRepo(t)
	r.write("internal/b/sub/sub.go", "package sub // changed\n")
	r.write("internal/a/testdata/new.txt", "new\n")
	out, verdict, ran, err := r.check()
	if err != nil || verdict != "scripts/check-own.sh: PASS" {
		t.Fatalf("%v, last line %q:\n%s", err, verdict, out)
	}
	if want := ownVet + "./internal/a ./internal/b/sub"; len(ran) == 0 || ran[0] != want {
		t.Errorf("it ran %q, want %q first", ran, want)
	}
	if tests := ranTests(t, ran); !slices.Equal(tests, []string{"./internal/a ./internal/b/sub"}) {
		t.Errorf("tests %q", tests)
	}
}

// A branch that changes only a shell script runs ShellCheck on it, as sh where its shebang is /bin/sh, and
// nothing of Go's.
func TestCheckOwnRunsShellcheckOnAChangedScript(t *testing.T) {
	t.Parallel()
	r := newOwnRepo(t)
	r.write("tools/run.sh", "#!/bin/sh\necho changed\n")
	r.write("tools/bash.sh", "#!/bin/bash\necho changed\n")
	r.git("add", "tools")
	r.git("commit", "-q", "-m", "change")
	out, verdict, ran, err := r.check()
	if err != nil || verdict != "scripts/check-own.sh: PASS" {
		t.Fatalf("%v, last line %q:\n%s", err, verdict, out)
	}
	if want := []string{"shellcheck -- tools/bash.sh", "shellcheck -s sh -- tools/run.sh"}; !slices.Equal(ran, want) {
		t.Errorf("it ran %q, want %q", ran, want)
	}
	if out, verdict, _, err := r.check("FAKE_SHELLCHECK=1"); err == nil ||
		verdict != "scripts/check-own.sh: FAIL in shellcheck (exit 1)" {
		t.Errorf("%v, last line %q:\n%s", err, verdict, out)
	}
}

// With no Go change, it runs no Go tests but those that read the changed docs, and neither vet nor lint;
// those of a package that changed run with the rest of its tests.
func TestCheckOwnRunsTheTestsThatReadTheChangedDocs(t *testing.T) {
	t.Parallel()
	r := newOwnRepo(t)
	r.commit("docs/organs.md", "README.md", ".orchestra/worker-prompt.md")
	out, verdict, ran, err := r.check()
	if err != nil || verdict != "scripts/check-own.sh: PASS" {
		t.Fatalf("%v, last line %q:\n%s", err, verdict, out)
	}
	want := []string{
		"./cmd/orchestra -run ^(TestDocLinksResolve|TestNoTextWritesALongFlagWithOneDash)$",
		"./internal/organ -run ^(TestDocsGiveTheOrgansSpecs)$",
		"./internal/project -run ^(TestWorkerPromptEndsWithWorkingUnattended|TestWorkerPromptWaitsForUnmergedWork)$",
	}
	if tests := ranTests(t, ran); len(ran) != len(want) || !slices.Equal(tests, want) {
		t.Errorf("it ran %q, tests %q, want the tests %q alone", ran, tests, want)
	}

	r.commit("internal/organ/prompts/x.md")
	_, _, ran, _ = r.check()
	want = []string{"./internal/organ", "./cmd/orchestra -run ^(TestDocLinksResolve|TestNoTextWritesALongFlagWithOneDash)$",
		"./internal/project -run ^(TestWorkerPromptEndsWithWorkingUnattended|TestWorkerPromptWaitsForUnmergedWork)$"}
	if tests := ranTests(t, ran); !slices.Equal(tests, want) {
		t.Errorf("with internal/organ changed, tests %q, want %q", tests, want)
	}
}

// A change to go.mod checks every package, and one to nothing it checks passes with nothing run.
func TestCheckOwnChecksEveryPackageForGoMod(t *testing.T) {
	t.Parallel()
	r := newOwnRepo(t)
	if _, verdict, ran, err := r.check(); err != nil || verdict != "scripts/check-own.sh: PASS" || len(ran) != 0 {
		t.Errorf("with no change: %v, last line %q, ran %q", err, verdict, ran)
	}
	r.commit("go.mod")
	_, _, ran, err := r.check()
	if err != nil || len(ran) != 3 || ran[0] != ownVet+"./..." || ran[2] != ownLint+"./..." ||
		!slices.Equal(ranTests(t, ran), []string{"./..."}) {
		t.Errorf("%v, it ran %q", err, ran)
	}
}
