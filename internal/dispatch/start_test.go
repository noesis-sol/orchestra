package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// refusingHerdr is Herdr refusing every agent name: the start, the adoption and the rename.
type refusingHerdr struct {
	starts, adopts []string // names asked for
}

var errNameRefused = errors.New(`herdr agent start: exit status 1: {"error":{"code":"invalid_agent_name","message":"agent names must be lowercase"}}`)

func (h *refusingHerdr) LaunchInPane(pane, kind string, args []string) error { return nil }
func (h *refusingHerdr) StartAgent(ctx context.Context, name, kind, pane string, args []string) error {
	h.starts = append(h.starts, name)
	return errNameRefused
}
func (h *refusingHerdr) IsArgumentRefused(err error) bool { return false }
func (h *refusingHerdr) IsNameRefused(err error) bool {
	return err != nil && strings.Contains(err.Error(), "invalid_agent_name")
}
func (h *refusingHerdr) WaitReady(ctx context.Context, name string) bool { return false }
func (h *refusingHerdr) AgentName(id string) string                      { return "agent-for-" + id }
func (h *refusingHerdr) AdoptAgent(ctx context.Context, pane, kind, name string) (string, error) {
	h.adopts = append(h.adopts, name)
	return "working", errNameRefused
}
func (h *refusingHerdr) PaneAgent(pane string) (string, string, string) {
	return "", "claude", "working"
}
func (h *refusingHerdr) RenameAgent(name, to string) error  { return errNameRefused }
func (h *refusingHerdr) FreeName(name string) string        { return name + "-1" }
func (h *refusingHerdr) Status(name string) (string, error) { return "gone", nil }
func (h *refusingHerdr) Screen(name string) string          { return "" }
func (h *refusingHerdr) Prompt(ctx context.Context, name, prompt string) error {
	return errors.New("no agent")
}
func (h *refusingHerdr) SendKeys(name string, keys ...string) error        { return nil }
func (h *refusingHerdr) WaitStarted(ctx context.Context, name string) bool { return false }

func TestRefusedAgentNameEndsTheStartWithoutRetries(t *testing.T) {
	for _, launch := range []bool{false, true} {
		f := newMergeFixture(t, "true")
		h := &refusingHerdr{}
		o := f.orch
		o.cfg.WTRoot, o.cfg.AgentKind, o.cfg.LaunchPrompt = t.TempDir(), "claude", launch
		o.starter, o.namer, o.agents = h, h, h
		s := o.work(context.Background(), Ticket{ID: "Cal-bl0.1", Title: "t"})
		if s == nil || s.code != ExitTool {
			t.Fatalf("launch=%v: want START_FAILED, got %+v", launch, s)
		}
		if !strings.Contains(s.text, "agent-for-Cal-bl0.1") || !strings.Contains(s.text, "agent names must be lowercase") {
			t.Errorf("launch=%v: the stop should name the agent and give Herdr's message: %s", launch, s.text)
		}
		if launch {
			if len(h.adopts) != 1 || h.adopts[0] != "agent-for-Cal-bl0.1" || len(h.starts) != 0 {
				t.Errorf("launch: adopted as %v and started as %v; want one adoption and no fallback start", h.adopts, h.starts)
			}
		} else if len(h.starts) != 1 || h.starts[0] != "agent-for-Cal-bl0.1" {
			t.Errorf("started as %v, want once as agent-for-Cal-bl0.1", h.starts)
		}
	}
}
