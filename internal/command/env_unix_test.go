//go:build unix

package command

import (
	"context"
	"testing"
)

// A command run with env gets it in its environment, over a variable of the same name.
func TestOutputWithInputAddsEnv(t *testing.T) {
	t.Setenv("ORCHESTRA_TEST_VAR", "outer")
	out, err := OutputWithInput(context.Background(), ReadLimit, "", []string{"ORCHESTRA_TEST_VAR=inner"}, "",
		"sh", "-c", `printf %s "$ORCHESTRA_TEST_VAR"`)
	if err != nil || out != "inner" {
		t.Errorf("got %q, %v; want inner", out, err)
	}
}
