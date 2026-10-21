package requires

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/project"
)

// check runs Check on list in a new folder, with o's other options, and returns the folder and the report.
func check(t *testing.T, list []project.Requirement, o Options) (string, Report) {
	t.Helper()
	o.Dir = t.TempDir()
	return o.Dir, Check(context.Background(), list, o)
}

// A requirement whose check passes is met, its install not run; one whose check fails is installed and
// checked again, its install's whole output kept in .orchestra/run/.
func TestCheckInstallsWhatIsMissing(t *testing.T) {
	t.Parallel()
	list := []project.Requirement{
		{Name: "there", Check: "true", Install: "touch there-installed"},
		{Name: "tool", Check: "test -e tool", Install: "echo installing the tool; touch tool"},
	}
	dir, report := check(t, list, Options{})
	if report[0].State != Met || report[0].Ran || exists(filepath.Join(dir, "there-installed")) {
		t.Errorf("met: %+v", report[0])
	}
	r := report[1]
	output := filepath.Join(dir, project.RunPath(project.RequireLogName("tool")))
	if r.State != Installed || !r.Ran || r.InstallErr != nil || r.Output != output {
		t.Errorf("installed: %+v, want its output in %s", r, output)
	}
	if b, err := os.ReadFile(output); err != nil || string(b) != "installing the tool\n" {
		t.Errorf("its output: %q, %v", b, err)
	}
	if got := report.Summary(); got != "there met; tool (installed with 'echo installing the tool; touch tool') installed" {
		t.Errorf("summary %q", got)
	}
	if report.MissingLine() != "" || len(report.Missing()) != 0 {
		t.Errorf("missing %q", report.MissingLine())
	}
}

// What is still missing is named with what to do: its manual step, or why its install didn't meet it
// and where its output is.
func TestCheckSaysWhatIsStillMissing(t *testing.T) {
	t.Parallel()
	list := []project.Requirement{
		{Name: "docker", Check: "false", Manual: "start Docker Desktop"},
		{Name: "broken", Check: "false", Install: "echo no such package; exit 3"},
		{Name: "useless", Check: "false", Install: "true"},
		{Name: "elsewhere", Check: "false", Install: "true", Platforms: []string{"plan9"}},
	}
	dir, report := check(t, list, Options{GOOS: "linux"})
	states := []State{Missing, Missing, Missing, OtherOS}
	for i, want := range states {
		if report[i].State != want {
			t.Errorf("%s: state %d, want %d", report[i].Name, report[i].State, want)
		}
	}
	if report[0].Ran || report[3].Ran {
		t.Errorf("an install ran for the manual one or the one for another system: %+v", report)
	}
	out := func(name string) string { return filepath.Join(dir, project.RunPath(project.RequireLogName(name))) }
	want := "REQUIREMENTS_MISSING: docker (start Docker Desktop); " +
		"broken ('echo no such package; exit 3' failed: exit status 3; its output is in " + out("broken") + "); " +
		"useless ('true' ran, but 'false' still fails; its output is in " + out("useless") + "); " +
		"meet them (each entry of 'requires' in .orchestra/settings.json says how), then run again"
	if got := report.MissingLine(); got != want {
		t.Errorf("line:\n%s\nwant:\n%s", got, want)
	}
	if got := report[3].Says(); got != "not checked: only on plan9" {
		t.Errorf("another system's: %q", got)
	}
}

// With NoInstall nothing is installed: a failed check is missing, and says the install that would meet it.
func TestCheckWithoutInstalling(t *testing.T) {
	t.Parallel()
	dir, report := check(t, []project.Requirement{{Name: "tool", Check: "test -e tool", Install: "touch tool"}},
		Options{NoInstall: true})
	if r := report[0]; r.State != Missing || r.Ran || exists(filepath.Join(dir, "tool")) {
		t.Errorf("%+v", r)
	}
	if got := report[0].Says(); got != "missing: 'test -e tool' fails; install it with 'touch tool'" {
		t.Errorf("%q", got)
	}
}

// A check or install that runs past its time limit is stopped there, and says so.
func TestCheckTimeLimits(t *testing.T) {
	t.Parallel()
	list := []project.Requirement{
		{Name: "slow-check", Check: "sleep 30", Manual: "wait"},
		{Name: "slow-install", Check: "false", Install: "sleep 30"},
	}
	start := time.Now()
	_, report := check(t, list, Options{CheckLimit: 200 * time.Millisecond, InstallLimit: 200 * time.Millisecond})
	if took := time.Since(start); took > 15*time.Second {
		t.Errorf("took %s", took)
	}
	if r := report[0]; r.State != Missing || !errors.Is(r.CheckErr, errTimedOut) {
		t.Errorf("slow check: %+v", r)
	}
	if r := report[1]; r.State != Missing || !errors.Is(r.InstallErr, errTimedOut) ||
		!strings.Contains(r.Why(), "timed out: stopped after 200ms") {
		t.Errorf("slow install: %+v, %q", r, r.Why())
	}
}

// The checks run side by side: four that each wait for the others to start finish together.
func TestChecksRunSideBySide(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var list []project.Requirement
	for _, name := range []string{"a", "b", "c", "d"} {
		// Each marks its start, then waits for the four marks (or gives up, failing).
		list = append(list, project.Requirement{Name: name, Manual: "x",
			Check: "touch " + name + ".started; i=0; while [ $(ls *.started | wc -l) -lt 4 ]; do " +
				"i=$((i+1)); [ $i -gt 100 ] && exit 1; sleep 0.1; done"})
	}
	report := Check(context.Background(), list, Options{Dir: dir})
	for _, r := range report {
		if r.State != Met {
			t.Errorf("%s: %+v", r.Name, r)
		}
	}
}

// A stop cuts the check short: what it didn't finish is unchecked, not missing.
func TestCheckStopped(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := Check(ctx, []project.Requirement{{Name: "x", Check: "sleep 30", Install: "true"}},
		Options{Dir: t.TempDir()})
	if r := report[0]; r.State != Unchecked || r.Ran || report.MissingLine() != "" {
		t.Errorf("%+v", r)
	}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
