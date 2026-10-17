package herdr

import (
	"context"
	"testing"
)

// AgentTab reads the agent's tab from 'herdr agent get': none for an agent Herdr doesn't have, and
// an error when Herdr fails or names no tab.
func TestAgentTab(t *testing.T) {
	herdrScript(t, "#!/bin/sh\necho '"+agentInfo+"'\n")
	if tab, err := (Terminal{}).AgentTab(context.Background(), "orchestra-4wb_11"); tab != "w2D:tG" || err != nil {
		t.Errorf("Herdr's real output: %q %v", tab, err)
	}

	failingHerdr(t, notFoundStderr)
	if tab, err := (Terminal{}).AgentTab(context.Background(), "x"); tab != "" || err != nil {
		t.Errorf("no such agent: %q %v", tab, err)
	}

	for _, out := range []string{
		`{"id":"cli:agent:get","result":{"agent":{"agent":"claude","agent_status":"idle","name":"x"}}}`,
		`{"id":"cli:agent:get","data":{}}`,
		"",
	} {
		herdrScript(t, "#!/bin/sh\necho '"+out+"'\n")
		if tab, err := (Terminal{}).AgentTab(context.Background(), "x"); tab != "" || err == nil {
			t.Errorf("%q: %q %v, want an error", out, tab, err)
		}
	}

	failingHerdr(t, `{"error":{"code":"server_busy","message":"busy"},"id":"cli:agent:get"}`)
	if tab, err := (Terminal{}).AgentTab(context.Background(), "x"); tab != "" || err == nil {
		t.Errorf("Herdr failing: %q %v, want an error", tab, err)
	}
}
