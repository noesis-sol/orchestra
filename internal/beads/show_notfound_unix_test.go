//go:build unix

package beads

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
)

// orchestra-hu0k: bd show of a ticket bd has no trace of, as one deleted, fails with an error
// errors.Is takes for dispatch.ErrNoTicket, carrying bd's stderr; bd failing otherwise doesn't.
func TestShowOfATicketBdHasNoTraceOf(t *testing.T) {
	for _, c := range []struct {
		name   string
		script string
		gone   bool
		stderr string
	}{
		{
			"deleted",
			"#!/bin/sh\necho 'Issue k-1 not found' >&2\n" +
				`printf '{\n  "error": "no issues found matching the provided IDs",\n  "hint": "try bd history",\n` +
				`  "schema_version": 1\n}\n'` + "\nexit 1\n",
			true,
			"Issue k-1 not found",
		},
		{
			"locked",
			"#!/bin/sh\necho 'Error: database is locked' >&2\nprintf '{\"error\": \"database is locked\"}\\n'\nexit 1\n",
			false,
			"database is locked",
		},
		{
			"no output",
			"#!/bin/sh\nexit 1\n",
			false,
			"exit status 1",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			faketool.Write(t, dir, "bd", c.script)
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			b := Tracker{Repo: t.TempDir()}
			tk, err := b.Show(context.Background(), "k-1")
			if err == nil || !strings.Contains(err.Error(), c.stderr) {
				t.Errorf("Show error %v; want bd's %q", err, c.stderr)
			}
			if got := errors.Is(err, dispatch.ErrNoTicket); got != c.gone {
				t.Errorf("errors.Is(%v, ErrNoTicket) = %v; want %v", err, got, c.gone)
			}
			if tk.Status != dispatch.StatusUnknown {
				t.Errorf("Show status = %q; want unknown", tk.Status)
			}
			st, err := b.Status(context.Background(), "k-1")
			if errors.Is(err, dispatch.ErrNoTicket) != c.gone || st != dispatch.StatusUnknown {
				t.Errorf("Status = %q, %v", st, err)
			}
		})
	}
}
