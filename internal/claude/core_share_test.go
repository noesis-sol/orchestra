package claude

import (
	"maps"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

var _ dispatch.EnvReporter = Reporter{}

// A worker's settings give its environment what WithEnv adds, its share of the machine's cores, beside
// fork mode turned off, new or resumed; without WithEnv they give fork mode alone.
func TestWorkerSettingsGiveTheEnvironmentAdded(t *testing.T) {
	wt := t.TempDir()
	share := map[string]string{dispatch.CoresVar: "2", "GOFLAGS": "-mod=mod -p=2"}
	r := Reporter{}.WithEnv(share)
	want := map[string]any{"CLAUDE_CODE_FORK_SUBAGENT": "0", dispatch.CoresVar: "2", "GOFLAGS": "-mod=mod -p=2"}
	for _, args := range [][]string{must(t)(r.ReportArgs(wt)), must(t)(r.ResumeArgs(wt))} {
		if got := readSettings(t, args[1])["env"]; !maps.Equal(got.(map[string]any), want) {
			t.Errorf("env %v, want %v", got, want)
		}
	}
	if got := readSettings(t, must(t)(Reporter{}.ReportArgs(wt))[1])["env"]; !maps.Equal(got.(map[string]any),
		map[string]any{"CLAUDE_CODE_FORK_SUBAGENT": "0"}) {
		t.Errorf("env without WithEnv %v, want fork mode's alone", got)
	}
	if len(workerEnv) != 1 {
		t.Errorf("workerEnv %v changed by WithEnv", workerEnv)
	}
}
