package beads

import (
	"context"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// Filed returns the tickets carrying the label that aren't closed, without their text: those init
// filed, which it doesn't file again.
func (b Tracker) Filed(ctx context.Context, label string) ([]dispatch.Ticket, error) {
	return b.list(ctx, "", "--brief", "--label", label)
}
