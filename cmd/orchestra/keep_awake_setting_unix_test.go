//go:build unix

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// "keep_awake": true in settings.json has every run keep the machine awake, as an overnight run does:
// the keep-awake command starts with orchestra's PID, and stops as the run ends.
func TestKeepAwakeSettingKeepsEveryRunAwake(t *testing.T) {
	t.Parallel()
	c, ok := keepAwakeOf(runtime.GOOS)
	if !ok {
		t.Skip("no keep-awake command on " + runtime.GOOS)
	}
	dir := nothingTools(t, backlog{})
	repo, stdout, stderr, code := runNothing(t, func(repo string) {
		if err := os.WriteFile(project.SettingsPath(repo), []byte(`{"concurrent": 1, "keep_awake": true}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}, "--tickets")
	if code != dispatch.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	want := strings.Join(c.args(os.Getpid()), " ") + "\nstopped\n"
	if calls := read(t, filepath.Join(dir, c.name+"-calls")); calls != want {
		t.Errorf("%s's calls:\n%s\nwant (started with orchestra's PID, then stopped):\n%s", c.name, calls, want)
	}
	if lines := read(t, filepath.Join(repo, project.Dir, "orchestra.log")); !strings.Contains(lines,
		"keeping the machine awake while the run goes: "+c.line(os.Getpid())+"\n") {
		t.Errorf("the log doesn't say the machine is kept awake:\n%s", lines)
	}
}

// keep_awake is off by default, and false leaves it off.
func TestConfigKeepAwake(t *testing.T) {
	t.Parallel()
	configFixture(t, `{"concurrent": 2}`)
	if c, p := loadWith(t); len(p) > 0 || c.keepAwake {
		t.Errorf("default: keep awake %v, %v", c.keepAwake, p)
	}
	for settings, want := range map[string]bool{`{"concurrent": 2, "keep_awake": true}`: true,
		`{"concurrent": 2, "keep_awake": false}`: false} {
		if err := os.WriteFile(filepath.Join(workDir(t), ".orchestra", "settings.json"), []byte(settings), 0o644); err != nil {
			t.Fatal(err)
		}
		if c, p := loadWith(t); len(p) > 0 || c.keepAwake != want {
			t.Errorf("%s: keep awake %v, %v", settings, c.keepAwake, p)
		}
	}
}
