package main

import (
	"io"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A run of any kind has no ticket limit unless --limit or LIMIT gives one, and one given caps it.
func TestRunHasNoTicketLimitUnlessGiven(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		env  map[string]string
		want int
	}{
		{"a plain run", nil, nil, dispatch.NoLimit},
		{"--tickets", []string{"--tickets"}, nil, dispatch.NoLimit},
		{"--ticket", []string{"--ticket", "o-1"}, nil, dispatch.NoLimit},
		{"with DONE_SO_FAR", []string{"--tickets"}, map[string]string{"DONE_SO_FAR": "80"}, dispatch.NoLimit},
		{"--limit", []string{"--tickets", "--limit", "5"}, nil, 5},
		{"LIMIT", []string{"--ticket", "o-1"}, map[string]string{"LIMIT": "7"}, 7},
		{"--limit over LIMIT", []string{"--limit", "3"}, map[string]string{"LIMIT": "7"}, 3},
		{"LIMIT=0", nil, map[string]string{"LIMIT": "0"}, 0},
	} {
		f, problems, err := readFlags(tc.args, specEnv(tc.env), io.Discard)
		if err != nil || len(problems) > 0 || f.Limit != tc.want {
			t.Errorf("%s: limit %d, want %d (%v, %q)", tc.name, f.Limit, tc.want, err, problems)
		}
	}
}
