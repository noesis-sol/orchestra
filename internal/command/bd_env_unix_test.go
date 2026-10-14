//go:build unix

package command

import (
	"context"
	"os"
	"testing"
)

// A command gets orchestra's environment and nothing more: BD_JSON_ENVELOPE, which bd alone needs,
// reached a tmux server orchestra started, and every pane opened there later.
func TestOutputAddsNothingToTheEnvironment(t *testing.T) {
	t.Setenv("BD_JSON_ENVELOPE", "") // restored after the test
	if err := os.Unsetenv("BD_JSON_ENVELOPE"); err != nil {
		t.Fatal(err)
	}
	out, err := Output(context.Background(), ReadLimit, "", "sh", "-c", `printf %s "${BD_JSON_ENVELOPE-unset}"`)
	if err != nil || out != "unset" {
		t.Errorf("got %q, %v; want BD_JSON_ENVELOPE unset", out, err)
	}
}
