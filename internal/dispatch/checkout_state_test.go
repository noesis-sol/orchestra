package dispatch

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// flakyCheckout is a main checkout whose git calls fail the first fails times.
type flakyCheckout struct {
	cleanCheckout
	fails *int
}

func (f flakyCheckout) DirtyTree(ctx context.Context, dir string) (string, error) {
	if *f.fails > 0 {
		*f.fails--
		return "", errors.New("git -C repo status --porcelain: signal: killed")
	}
	return "", nil
}

// A git call that fails says nothing about the checkout: it is tried again, and when it keeps
// failing the run stops for git, not for uncommitted changes.
func TestFailingGitIsNotADirtyTree(t *testing.T) {
	for _, tc := range []struct {
		fails int
		want  string
	}{
		{gitTries - 1, ""},
		{gitTries, "GIT_FAILED: could not read the state of repo: git -C repo status --porcelain: signal: killed; stopping"},
	} {
		log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
		if err != nil {
			t.Fatal(err)
		}
		fails := tc.fails
		o := New(Config{Repo: "repo", Base: "main"}, log, "", Deps{Tickets: readyTickets{{ID: "A"}}, Checkout: flakyCheckout{fails: &fails}})
		o.wait.poll = time.Millisecond
		tk, _, s := o.next(context.Background(), nil)
		switch {
		case tc.want == "" && (s != nil || tk == nil || tk.ID != "A"):
			t.Errorf("%d failures: got %v (%+v), want A", tc.fails, tk, s)
		case tc.want != "" && (s == nil || s.code != ExitTool || s.kind != stopGitFailed || s.Error() != tc.want):
			t.Errorf("%d failures: got %+v, want %q", tc.fails, s, tc.want)
		}
	}
}
