package herdr

import (
	"context"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// What Herdr 0.9.3 answers 'herdr agent get' with, on stdout, for a pane holding a Claude Code agent.
const agentInfo = `{"id":"cli:agent:get","result":{"agent":{"agent":"claude","agent_status":"working",` +
	`"name":"orchestra-4wb_11","pane_id":"w2D:pH","tab_id":"w2D:tG","workspace_id":"w2D"},"type":"agent_info"}}`

// Only a missing agent reads as gone: Herdr's agent_not_found (its answer for a pane without an
// agent, too), or a result with no agent in it.
func TestNoAgentReadsAsGone(t *testing.T) {
	for _, out := range []string{
		`{"id":"cli:agent:get","result":{"agent":null,"type":"agent_info"}}`,
		`{"id":"cli:agent:get","result":{"type":"agent_info"}}`,
	} {
		if n, k, s, err := readAgent(out, nil); n != "" || k != "" || s != dispatch.StateGone || err != nil {
			t.Errorf("%s: %q %q %q %v", out, n, k, s, err)
		}
	}

	// What Herdr really answers for a pane that holds no agent.
	failingHerdr(t, `{"error":{"code":"agent_not_found","message":"agent target w2D:pD not found"},"id":"cli:agent:get"}`)
	if s, err := (Terminal{}).Status(context.Background(), "w2D:pD"); s != dispatch.StateGone || err != nil {
		t.Errorf("a pane without an agent: %q %v", s, err)
	}
}

// An agent with a status reads as that status, whichever it is.
func TestAgentReadsAsItsStatus(t *testing.T) {
	for _, want := range []dispatch.AgentState{
		dispatch.StateIdle, dispatch.StateWorking, dispatch.StateBlocked, dispatch.StateDone, dispatch.StateUnknown,
	} {
		out := `{"result":{"agent":{"name":"kinieta-9g6","agent":"claude","agent_status":"` + string(want) + `"}}}`
		if n, k, s, err := readAgent(out, nil); n != "kinieta-9g6" || k != "claude" || s != want || err != nil {
			t.Errorf("%s: %q %q %q %v", want, n, k, s, err)
		}
	}

	herdrScript(t, "#!/bin/sh\necho '"+agentInfo+"'\n")
	if s, err := (Terminal{}).Status(context.Background(), "orchestra-4wb_11"); s != dispatch.StateWorking || err != nil {
		t.Errorf("Herdr's real output: %q %v", s, err)
	}
}

// An agent that is there without a status, as from a Herdr that renamed or dropped agent_status, is
// unexpected output and an error, not gone: the loop would otherwise settle a worker still at work.
// So is output with no result, which says nothing about the agent.
func TestAgentWithoutStatusIsAnError(t *testing.T) {
	for _, out := range []string{
		`{"id":"cli:agent:get","result":{"agent":{"agent":"claude","name":"kinieta-9g6","status":"working"}}}`,
		`{"id":"cli:agent:get","result":{"agent":{"agent":"claude","agent_status":""}}}`,
		`{"id":"cli:agent:get","result":{"agent":{}}}`,
		`{"id":"cli:agent:get","data":{"agent":{"agent":"claude","agent_status":"working"}}}`,
		`{}`,
		"",
	} {
		if n, k, s, err := readAgent(out, nil); n != "" || k != "" || s != "" || err == nil {
			t.Errorf("%q: %q %q %q %v, want an error and no state", out, n, k, s, err)
		}
	}

	herdrScript(t, "#!/bin/sh\necho '{\"result\":{\"agent\":{\"agent\":\"claude\",\"name\":\"kinieta-9g6\"}}}'\n")
	if s, err := (Terminal{}).Status(context.Background(), "kinieta-9g6"); s == dispatch.StateGone || err == nil {
		t.Errorf("Status of an agent without a status: %q %v, want an error", s, err)
	}
	// FreeName must not take the name of an agent whose status it cannot read.
	if name := (Terminal{}).FreeName(context.Background(), "kinieta-9g6"); name != "" {
		t.Errorf("FreeName took %q", name)
	}
}
