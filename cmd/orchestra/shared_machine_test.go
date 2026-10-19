package main

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/noesis-sol/orchestra/internal/update"
)

// The machine's runs register in runs/ in orchestra's cache folder, or in the folder
// ORCHESTRA_RUNS_DIR names; off registers nowhere, nor does a run with no cache folder.
func TestRunsDir(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cache, err := update.CacheDir(func(k string) string {
		return map[string]string{"HOME": home, "XDG_CACHE_HOME": home + "/.cache"}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"cache folder", map[string]string{"HOME": home, "XDG_CACHE_HOME": home + "/.cache"}, filepath.Join(cache, "runs")},
		{"named", map[string]string{"HOME": home, runsDirEnv: "/srv/runs"}, "/srv/runs"},
		{"off", map[string]string{"HOME": home, runsDirEnv: "off"}, ""},
		{"no cache folder", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := runsDir(func(k string) string { return tc.env[k] }); got != tc.want {
				t.Errorf("runsDir = %q, want %q", got, tc.want)
			}
		})
	}
}

// --machine-workers, or ORCHESTRA_MACHINE_WORKERS, caps the workers across the machine's runs; none by
// default, and a value that isn't a whole number is a setup problem.
func TestMachineWorkersFlag(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		args    []string
		env     map[string]string
		want    int
		problem bool
	}{
		{"default", nil, nil, 0, false},
		{"variable", nil, map[string]string{"ORCHESTRA_MACHINE_WORKERS": "8"}, 8, false},
		{"flag over variable", []string{"--machine-workers", "6"}, map[string]string{"ORCHESTRA_MACHINE_WORKERS": "8"}, 6, false},
		{"not a number", nil, map[string]string{"ORCHESTRA_MACHINE_WORKERS": "lots"}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, problems, err := readFlags(tc.args, func(k string) string { return tc.env[k] }, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if tc.problem {
				if len(problems) == 0 {
					t.Error("no setup problem")
				}
				return
			}
			if len(problems) > 0 || f.MachineWorkers != tc.want {
				t.Errorf("MachineWorkers = %d, problems %q; want %d", f.MachineWorkers, problems, tc.want)
			}
		})
	}
}
