package claude

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/agentio"
)

// A dialog's notification written in the same tick of the filesystem's clock (1-second file times on
// HFS+ or exFAT) as the prompt or the tool use that asks still reads as waiting on its answer: it can
// only have come after them. One that ties a record it comes before, the tool done or the turn's
// Stop, reads as answered.
func TestNoticeTyingItsCauseReadsBlocked(t *testing.T) {
	t.Parallel()
	turn := []hookStep{stepSessionStart("startup"), stepPrompt, stepUseBash, stepUsedIt}
	for _, c := range []struct {
		name  string
		steps []hookStep
		tied  []string // the records dated as the notice is
		want  agentio.State
	}{
		{"elicitation as its tool use", append(turn, stepUseBash, stepNotification("elicitation_dialog")),
			[]string{activityName}, agentio.StateBlocked},
		{"elicitation as the prompt and its tool use",
			[]hookStep{stepSessionStart("startup"), stepPrompt, stepUseBash, stepNotification("elicitation_dialog")},
			[]string{activityName, turnName}, agentio.StateBlocked},
		{"permission notice as its prompt",
			append(turn, stepUseBash, stepAskBash, stepNotification("permission_prompt")),
			[]string{activityName}, agentio.StateBlocked},
		{"elicitation as its tool done", append(turn, stepUseBash, stepNotification("elicitation_dialog"), stepUsedIt),
			[]string{activityName}, agentio.StateWorking},
		{"elicitation as the turn's Stop",
			append(turn, stepUseBash, stepNotification("elicitation_dialog"), stepUsedIt, stepStop),
			[]string{activityName, turnName}, agentio.StateIdle},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			wt := t.TempDir()
			playHooks(t, wt, c.steps)
			records := Records(wt, "")
			fi, err := os.Stat(filepath.Join(records, noticeName))
			if err != nil {
				t.Fatal(err)
			}
			at := fi.ModTime().Truncate(time.Second)
			for _, name := range append([]string{noticeName}, c.tied...) {
				if err := os.Chtimes(filepath.Join(records, name), at, at); err != nil {
					t.Fatal(err)
				}
			}
			if got := State(records); got != c.want {
				t.Errorf("state with the notice tying %v: %s, want %s", c.tied, got, c.want)
			}
		})
	}
}
