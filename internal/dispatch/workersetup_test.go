package dispatch

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A file a worker can do without is kept when it was had, left out with the failure logged when it
// wasn't, and an escape is returned rather than logged, to set the ticket aside.
func TestOptional(t *testing.T) {
	t.Parallel()
	escape := &project.EscapeError{Dir: "/wt", Path: ".orchestra/run", Err: project.ErrRunLink}
	for _, tc := range []struct {
		name    string
		v       []string
		err     error
		want    []string
		wantErr error
		logged  string
	}{
		{"had", []string{"--a"}, nil, []string{"--a"}, nil, ""},
		{"failed", []string{"--a"}, errors.New("no room"), nil, nil, "A's worker does without: no room\n"},
		{"escaped", []string{"--a"}, fmt.Errorf("writing: %w", escape), nil, escape, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "log")
			log, err := OpenLog(path, false, "p")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = log.Close() })
			got, err := optional(log, tc.v, tc.err, "A's worker does without")
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			if !errors.Is(err, tc.wantErr) || (err == nil) != (tc.wantErr == nil) {
				t.Errorf("error %v, want %v", err, tc.wantErr)
			}
			if logged := read(t, path); logged != tc.logged {
				t.Errorf("logged %q, want %q", logged, tc.logged)
			}
		})
	}
}
