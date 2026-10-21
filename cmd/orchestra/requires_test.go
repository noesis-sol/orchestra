package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// requiresFixture writes settings, a settings.json, in a new repository's .orchestra/, and returns
// the repository.
func requiresFixture(t *testing.T, settings string) string {
	t.Helper()
	repo, _ := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, project.Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(project.SettingsPath(repo), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

// otherOS is the operating system of project.RequirePlatforms this one isn't.
var otherOS = map[bool]string{true: "linux", false: "darwin"}[runtime.GOOS == "darwin"]

// requiresList is a settings.json with a requirement met, one installed, one manual and one for another
// system.
var requiresList = `{"requires": [
	{"name": "sh", "check": "command -v sh", "manual": "install a shell"},
	{"name": "tool", "check": "test -e tool", "install": "echo installing; touch tool"},
	{"name": "db", "check": "false", "manual": "start the database"},
	{"name": "other", "check": "false", "manual": "x", "platforms": ["` + otherOS + `"]}]}`

// orchestra requires prints each requirement's state, having installed what it could, and says what is
// still missing, exit 2; with --no-install it installs nothing.
func TestRequiresPrintsEachState(t *testing.T) {
	t.Parallel()
	repo := requiresFixture(t, requiresList)
	stdout, stderr, err := runIn(t, repo, nil, "requires", "--no-install")
	want := "sh: met\ntool: missing: 'test -e tool' fails; install it with 'echo installing; touch tool'\n" +
		"db: missing: start the database\nother: not checked: only on " + otherOS + "\n"
	if exitOf(err) != 2 || stdout != want || !strings.HasPrefix(stderr, "REQUIREMENTS_MISSING: tool (") {
		t.Errorf("--no-install: exit %d, stdout:\n%s\nwant:\n%s\nstderr:\n%s", exitOf(err), stdout, want, stderr)
	}
	stdout, stderr, err = runIn(t, repo, nil, "requires")
	want = "sh: met\ntool: installed with 'echo installing; touch tool'\ndb: missing: start the database\n" +
		"other: not checked: only on " + otherOS + "\n"
	if exitOf(err) != 2 || stdout != want || !strings.HasPrefix(stderr, "REQUIREMENTS_MISSING: db (start the database); meet it") {
		t.Errorf("exit %d, stdout:\n%s\nwant:\n%s\nstderr:\n%s", exitOf(err), stdout, want, stderr)
	}
	if b, err := os.ReadFile(filepath.Join(repo, project.RunPath(project.RequireLogName("tool")))); err != nil ||
		string(b) != "installing\n" {
		t.Errorf("the install's output: %q, %v", b, err)
	}
	// Once the database is "started", everything is met.
	fixed := strings.Replace(requiresList, `"check": "false", "manual": "start`, `"check": "true", "manual": "start`, 1)
	if err := os.WriteFile(project.SettingsPath(repo), []byte(fixed), 0o644); err != nil {
		t.Fatal(err)
	}
	if stdout, stderr, err = runIn(t, repo, nil, "requires"); err != nil || stderr != "" ||
		!strings.HasPrefix(stdout, "sh: met\ntool: met\ndb: met\n") {
		t.Errorf("all met: %v, stdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
}

// A project without a list says so; one whose list a run would refuse says why, exit 2.
func TestRequiresWithoutAListOrARefusedOne(t *testing.T) {
	t.Parallel()
	stdout, _, err := runIn(t, requiresFixture(t, `{"concurrent": 1}`), nil, "requires")
	if err != nil || stdout != "No requirements: .orchestra/settings.json has no 'requires' list.\n" {
		t.Errorf("no list: %v, %q", err, stdout)
	}
	refused := `{"requires": [{"name": "jq", "check": "command -v jq", "install": "sudo apt-get install jq"}]}`
	_, stderr, err := runIn(t, requiresFixture(t, refused), nil, "requires")
	if exitOf(err) != 2 || !strings.Contains(stderr, "requires entry 1 (jq) install runs sudo") {
		t.Errorf("refused: exit %d, %q", exitOf(err), stderr)
	}
}

// An install that runs sudo is refused as the run loads its settings, a setup problem like any other.
func TestARunRefusesAnInstallWithSudo(t *testing.T) {
	t.Parallel()
	configFixture(t, `{"concurrent": 1, "requires": [{"name": "jq", "check": "command -v jq", "install": "sudo apt install jq"}]}`)
	if _, p := loadWith(t); len(p) != 1 || !strings.Contains(p[0], "requires entry 1 (jq) install runs sudo, which asks for a password") {
		t.Errorf("problems %v, want the sudo refused", p)
	}
	fine := `{"concurrent": 1, "requires": [{"name": "jq", "check": "command -v jq", "install": "brew install jq"}]}`
	if err := os.WriteFile(project.SettingsPath(workDir(t)), []byte(fine), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, p := loadWith(t); len(p) > 0 || len(c.Requires) != 1 || c.Requires[0].Install != "brew install jq" {
		t.Errorf("requires %+v, problems %v", c.Requires, p)
	}
}

// init reports the project's requirements among what the machine needs, as a run finds them: what it
// installed, and what is still missing with what to do; a list a run would refuse is missing too.
func TestInitReportsTheRequirements(t *testing.T) {
	t.Parallel()
	repo := requiresFixture(t, requiresList)
	var said []string
	steps := initRequires(runCtx(t), repo, func(what string) { said = append(said, what) })
	want := []project.Step{
		{Kind: project.StepDone, Label: "sh"},
		{Kind: project.StepDone, Label: "tool", Detail: "installed with 'echo installing; touch tool'"},
		{Kind: project.StepMissing, Label: "db", Detail: "start the database"},
		{Kind: project.StepKept, Label: "other", Detail: "not checked: only on " + otherOS},
	}
	if len(steps) != len(want) {
		t.Fatalf("steps %+v, want %+v", steps, want)
	}
	for i := range want {
		if s := steps[i]; s.Kind != want[i].Kind || s.Label != want[i].Label || s.Detail != want[i].Detail {
			t.Errorf("step %d: %+v, want %+v", i, s, want[i])
		}
	}
	if len(said) != 2 || said[1] != "installing tool: echo installing; touch tool" {
		t.Errorf("working said %q", said)
	}
	refused := requiresFixture(t, `{"requires": [{"name": "x", "check": "true"}]}`)
	if steps := initRequires(runCtx(t), refused, func(string) {}); len(steps) != 1 ||
		steps[0].Kind != project.StepMissing || !strings.Contains(steps[0].Detail, "needs an install") {
		t.Errorf("refused: %+v", steps)
	}
	if steps := initRequires(runCtx(t), requiresFixture(t, `{}`), func(string) {}); len(steps) != 0 {
		t.Errorf("none: %+v", steps)
	}
}
