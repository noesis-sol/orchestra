package cmux

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

// top065 is the part of cmux 0.65.0's top --processes --json for a workspace that names processes:
// the app's own, the terminal's processes it puts down to no program (tty-collision), listed by ID
// only, and one gone.
const top065 = `{
  "memory_diagnostic": {"app": {"name": "cmux", "pid": 45219}, "children": {"pids": [45296]}},
  "windows": [{"workspaces": [{"id": "` + workWS + `", "panes": [{"surfaces": [{
    "id": "` + workSF + `",
    "processes": [],
    "tty_ownership_reasons": {"63676": "tty-collision", "63692": "tty-collision", "64435": "tty-collision"},
    "unattributed_resources": {"pids": [63676, 63692, 64435], "missing_pids": [70001]},
    "unattributed_tty_process_pids": [63676, 63692, 64435]
  }]}]}]}]
}`

// A process cmux 0.65.0 lists by ID only, in a list of a terminal's processes, runs there; one of
// its missing_pids, or one it doesn't list, doesn't. 0.64's objects with a pid are read as before.
func TestHasPIDReadsCmux065Lists(t *testing.T) {
	var v any
	if err := json.Unmarshal([]byte(top065), &v); err != nil {
		t.Fatal(err)
	}
	for pid, want := range map[int]bool{64435: true, 63692: true, 45219: true, 45296: true, 70001: false, 4242: false} {
		if got := hasPID(v, pid); got != want {
			t.Errorf("process %d listed: %v, want %v", pid, got, want)
		}
	}
	if err := json.Unmarshal([]byte(topJSON(workSF, 4242)), &v); err != nil {
		t.Fatal(err)
	}
	if !hasPID(v, 4242) || hasPID(v, 4243) {
		t.Errorf("0.64's answer for process 4242: %v, and for 4243: %v", hasPID(v, 4242), hasPID(v, 4243))
	}
}

// From 0.65.0 top answers from a sample of the processes: one that doesn't list the process says it
// is gone only when it was taken after the launch and found every process. Before 0.65.0 the answer
// has no sample, and lists what runs.
func TestSampledSince(t *testing.T) {
	launch := time.Date(2026, 10, 14, 7, 30, 21, 500e6, time.UTC)
	for _, c := range []struct {
		answer string
		want   bool
	}{
		{`{"windows": []}`, true},
		{`{"sample": {"sampled_at": "2026-10-14T07:30:22Z", "enumeration_complete": true}}`, true},
		{`{"sample": {"sampled_at": "2026-10-14T07:30:21Z", "enumeration_complete": true}}`, false},
		{`{"sample": {"sampled_at": "2026-10-14T07:30:20Z", "enumeration_complete": true}}`, false},
		{`{"sample": {"sampled_at": "2026-10-14T07:30:25Z", "enumeration_complete": false}}`, false},
		{`{"sample": {"enumeration_complete": true}}`, true},
	} {
		var v any
		if err := json.Unmarshal([]byte(c.answer), &v); err != nil {
			t.Fatal(err)
		}
		if got := sampledSince(v, launch); got != c.want {
			t.Errorf("%s: %v, want %v", c.answer, got, c.want)
		}
	}
}

// A process top's sample can't have listed, taken before its launch or missing some, runs as long as
// it exists; a sample taken since that doesn't list it says it is gone.
func TestRunningTrustsAStaleSampleOnlyForALiveProcess(t *testing.T) {
	f := newFakeCmux(t)
	term := terminal(t)
	ended := exec.Command("true")
	if err := ended.Run(); err != nil {
		t.Fatal(err)
	}
	launch := time.Now()
	for _, c := range []struct {
		name, sample string
		pid          int
		want         bool
	}{
		{"taken before, live", `"sampled_at": "2000-01-01T00:00:00Z", "enumeration_complete": true`, os.Getpid(), true},
		{"incomplete, live", `"sampled_at": "2100-01-01T00:00:00Z", "enumeration_complete": false`, os.Getpid(), true},
		{"taken before, ended", `"sampled_at": "2000-01-01T00:00:00Z", "enumeration_complete": true`,
			ended.Process.Pid, false},
		{"taken since, live", `"sampled_at": "2100-01-01T00:00:00Z", "enumeration_complete": true`, os.Getpid(), false},
	} {
		f.answer("top", `{"sample": {`+c.sample+`}, "windows": []}`)
		if got, err := term.running(bg, workWS, c.pid, launch); got != c.want || err != nil {
			t.Errorf("%s: running %v, %v; want %v", c.name, got, err, c.want)
		}
	}
}
