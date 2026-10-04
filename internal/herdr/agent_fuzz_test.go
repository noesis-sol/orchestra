package herdr

import (
	"errors"
	"strings"
	"testing"
)

// FuzzAgentName gives AgentName any ticket ID: the name is one Herdr takes, and AgentName keeps a
// name Herdr takes as it is. go test runs the seeds; docs/development.md says how to fuzz.
func FuzzAgentName(f *testing.F) {
	for _, id := range []string{"orchestra-aix", "CalendarView-bl0.12.3", "platform-backend-services-a3f.12.3", "Ω",
		"x y/z:w", strings.Repeat("A", 100), "9lives-x1", "_x", "", "émile-q2", "\xff"} {
		f.Add(id)
	}
	f.Fuzz(func(t *testing.T, id string) {
		name := AgentName(id)
		if !validName(name) {
			t.Errorf("AgentName(%q) = %q, which Herdr would refuse", id, name)
		}
		if again := AgentName(name); again != name {
			t.Errorf("AgentName(%q) = %q changes a valid name", name, again)
		}
	})
}

// FuzzReadAgent gives readAgent any output of 'herdr agent get', from a call that succeeded or that
// failed with any code. It doesn't panic, and it answers either an agent's state or an error, with
// no name or kind; a failed call's error is the call's, but for agent_not_found, which is gone.
func FuzzReadAgent(f *testing.F) {
	for _, out := range []string{
		`{"id":"cli:agent:get","result":{"agent":{"name":null,"agent":"claude","agent_status":"working","pane_id":"w2B:p1D"}}}`,
		`{"result":{"agent":{"name":"kinieta-9g6","agent":"claude","agent_status":"idle"}}}`,
		`{"id":"cli:agent:get","result":{"agent":null,"type":"agent_info"}}`,
		`{"id":"cli:agent:get","result":{"type":"agent_info"}}`,
		`{"id":"cli:agent:get","result":{"agent":{"agent":"claude","name":"kinieta-9g6","status":"working"}}}`,
		`{"id":"cli:agent:get","result":{"agent":{}}}`,
		`{"id":"cli:agent:get","data":{"agent":{"agent":"claude","agent_status":"working"}}}`,
		`{}`, "", "not json",
	} {
		f.Add(out, false, "")
	}
	f.Add("", true, AgentNotFound)
	f.Add("", true, "server_busy")
	f.Fuzz(func(t *testing.T, out string, failed bool, code string) {
		var callErr error
		if failed {
			callErr = &Error{Code: code, Err: errors.New("exit status 1")}
		}
		name, kind, state, err := readAgent(out, callErr)
		switch {
		case (err == nil) == (state == ""):
			t.Errorf("readAgent(%q, %v) = state %q, error %v: want one of them", out, callErr, state, err)
		case err != nil && (name != "" || kind != ""):
			t.Errorf("readAgent(%q, %v) = %q, %q with the error %v", out, callErr, name, kind, err)
		case failed && code != AgentNotFound && err != callErr:
			t.Errorf("readAgent(%q, %v) = error %v, want the call's", out, callErr, err)
		}
	})
}
