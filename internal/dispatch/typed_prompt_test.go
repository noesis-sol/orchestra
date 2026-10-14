package dispatch

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
)

// boxAgents is a worker whose terminal puts typed in its input box for any prompt, without the Enter
// registering: it starts once Enter is pressed.
type boxAgents struct {
	noAgents
	typed   string // what the terminal puts in the box for a prompt
	box     string // what the box holds
	prompts int    // the prompts given
	entered bool   // Enter pressed on what the box holds
}

func (a *boxAgents) Status(context.Context, string) (AgentState, error) {
	if a.entered {
		return StateWorking, nil
	}
	return StateIdle, nil
}

func (a *boxAgents) Screen(context.Context, string, AgentState) string {
	return "⏺ Ready.\n\n────────\n❯ " + a.box + "\n────────\n  ? for shortcuts"
}

func (a *boxAgents) Prompt(_ context.Context, _, _ string) error {
	a.prompts++
	a.box += a.typed
	return errPromptNotStarted
}

func (a *boxAgents) SendKeys(_ context.Context, _ string, keys ...string) error {
	if a.box != "" && slices.Contains(keys, "enter") {
		a.entered = true
	}
	return nil
}

func (a *boxAgents) WaitStarted(context.Context, string) bool { return a.entered }

// typingAgents is boxAgents on a terminal that says what it types (PromptTyper).
type typingAgents struct{ *boxAgents }

func (a typingAgents) TypedPrompt(context.Context, string, string) string { return a.typed }

// errPromptNotStarted is the terminal not seeing the worker start on its prompt.
var errPromptNotStarted = errors.New("the agent did not start")

// A prompt whose Enter was lost is started with Enter, not given again, when the box holds what the
// terminal typed for it: cmux before 0.65 types a line attaching the file a prompt of several lines
// is in, which shows none of its lines, and from 0.65 pastes it.
func TestLostEnterAfterATypedLinePressesEnter(t *testing.T) {
	prompt := "Work on orchestra-1: the check fails on main.\n\nRead the ticket first, then fix the test it names."
	for _, c := range []struct{ name, typed string }{
		{"cmux 0.64, through a file", "My message to you is in @.orchestra/run/cmux-prompt-c1.md : read it, " +
			"and take it as if I had typed it here."},
		{"cmux 0.65, pasted", "[Pasted text #1 +3 lines]"},
		{"typed as it is", prompt},
	} {
		t.Run(c.name, func(t *testing.T) {
			log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
			if err != nil {
				t.Fatal(err)
			}
			a := &boxAgents{typed: c.typed}
			o := &Loop{log: log, sink: &recordSink{}, agents: typingAgents{a}}
			if !o.deliverPrompt(context.Background(), "x-1", prompt) {
				t.Error("the worker was not started on its prompt")
			}
			if a.prompts != 1 || !a.entered {
				t.Errorf("given the prompt %d times, Enter pressed: %v; the box holds %q", a.prompts, a.entered, a.box)
			}
		})
	}
}
