package dispatch

import "testing"

// The note on a ticket whose worker is gone from its tab says after what, when it is known.
func TestWorkerGoneNote(t *testing.T) {
	cases := []struct {
		after, want string
	}{
		{"", "Orchestra: the worker in tab 3 is gone, with the ticket still in_progress (worktree /wt/a)"},
		{"its question q-1", "Orchestra: the worker in tab 3 is gone, with the ticket still in_progress " +
			"after its question q-1 (worktree /wt/a)"},
	}
	for _, tc := range cases {
		if got := workerGoneNote("3", "/wt/a", tc.after); got != tc.want {
			t.Errorf("workerGoneNote(after %q) = %q, want %q", tc.after, got, tc.want)
		}
	}
}
