package dispatch

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
)

// stoppingShows is a tracker whose first bd show stops the run, as Ctrl+C does while the run reads
// its ready tickets' links: that show and every one after it fail as bd's do on a cancelled context.
type stoppingShows struct {
	*fakeBeads
	stop context.CancelFunc
}

func (b *stoppingShows) Show(ctx context.Context, id string) (Ticket, error) {
	b.stop()
	if err := ctx.Err(); err != nil {
		return Ticket{ID: id, Status: "unknown"}, fmt.Errorf("bd show %s --json: %w", id, err)
	}
	return b.fakeBeads.Show(ctx, id)
}

// orchestra-nr1v.13: a stop while the run reads its ready tickets' links logs none of the bd shows
// it cut short: the INTERRUPTED line says what happened.
func TestStopDuringReadsLogsNoCancelledCommands(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		for _, id := range []string{"A", "B", "C"} {
			h.beads.add(id, "ticket "+id, 1)
		}
		o := h.loop()
		o.ReportInterrupt = true
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		o.tickets = &stoppingShows{fakeBeads: h.beads, stop: cancel}
		o.Run(ctx)
		logged := h.logged()
		if !strings.Contains(logged, "INTERRUPTED") {
			t.Fatalf("the run was not interrupted; log:\n%s", logged)
		}
		if strings.Contains(logged, context.Canceled.Error()) {
			t.Errorf("logged the commands the stop cut short:\n%s", logged)
		}
	})
}
