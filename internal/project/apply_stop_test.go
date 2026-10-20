package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Apply on a context that has ended, as a stop signal ends it before the setup starts, writes
// nothing: the repository isn't taken for set up.
func TestApplyAfterAStopWritesNothing(t *testing.T) {
	repo, _ := gitRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	steps, err := Apply(ctx, repo, DefaultChoice(Settings{CheckFast: "make check"}, ""), false)
	if !errors.Is(err, context.Canceled) || steps != nil {
		t.Errorf("steps %+v, error %v; want none, %v", steps, err, context.Canceled)
	}
	if _, err := os.Stat(filepath.Join(repo, Dir)); !os.IsNotExist(err) {
		t.Errorf("%s: %v, want none", Dir, err)
	}
	if IsSetUp(repo) {
		t.Error("set up")
	}
}
