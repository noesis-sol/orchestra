//go:build unix

package command

import (
	"context"
	"slices"
	"testing"
)

// A name alone in env leaves the variable out of the command's environment, whether orchestra's
// environment or WithEnv set it; a NAME=value after it sets it again.
func TestOutputWithEnvUnsetsANameAlone(t *testing.T) {
	t.Setenv("ORCHESTRA_TEST_OUTER", "outer")
	ctx := WithEnv(context.Background(), "ORCHESTRA_TEST_ADDED=added")
	out, err := OutputWithEnv(ctx, ReadLimit, "", []string{"ORCHESTRA_TEST_OUTER", "ORCHESTRA_TEST_ADDED",
		"ORCHESTRA_TEST_AGAIN=1", "ORCHESTRA_TEST_AGAIN", "ORCHESTRA_TEST_AGAIN=2"},
		"sh", "-c", `printf '%s|%s|%s|%s' "${ORCHESTRA_TEST_OUTER-unset}" "${ORCHESTRA_TEST_ADDED-unset}" `+
			`"$ORCHESTRA_TEST_AGAIN" "$(env | grep -c '^ORCHESTRA_TEST_AGAIN=')"`)
	if want := "unset|unset|2|1"; err != nil || out != want {
		t.Errorf("got %q, %v; want %q", out, err, want)
	}
}

func TestUnset(t *testing.T) {
	got := unset([]string{"A=1", "B=2", "A", "C", "B=3"})
	if want := []string{"B=2", "B=3"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
