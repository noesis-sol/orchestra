package beads

import (
	"context"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/tracker"
)

// Questions returns the questions for the maintainer that aren't closed (tracker.HumanLabel), with
// their text and metadata, which carries their options. When bd's output can't be read, the error
// carries bd's stderr.
func (b Tracker) Questions(ctx context.Context) ([]tracker.Ticket, error) {
	issues, err := b.list(ctx, "", "--label", tracker.HumanLabel)
	if err != nil {
		return nil, err
	}
	var open []tracker.Ticket
	for _, t := range issues {
		if t.Status != tracker.StatusClosed { // bd list leaves closed ones out already
			open = append(open, t)
		}
	}
	return open, nil
}

// Dependents returns the tickets that depend on the ticket id, closed or not, with their text and
// metadata, each with its DependencyType: how it links to id (blocks for one waiting on it). When
// bd's output can't be read, the error carries bd's stderr.
func (b Tracker) Dependents(ctx context.Context, id string) ([]tracker.Ticket, error) {
	out, runErr := b.bd(ctx, command.ReadLimit, "dep", "list", id, "--direction", "up", "--json")
	dependents, err := parseList([]byte(out), "")
	if err = readFailed("bd dep list --json", err, runErr); err != nil {
		return nil, err
	}
	return dependents, nil
}
