package main

import (
	"context"
	"testing"
)

// startIn runs orchestra's subcommand name with args in e, its stop signals handled as its row of
// subcommands says, and returns its exit code.
func startIn(ctx context.Context, name string, args []string, e cmdEnv) int {
	c, ok := findSubcommand(name)
	if !ok {
		panic("no subcommand " + name)
	}
	return c.start(ctx, args, e)
}

// Every subcommand has its stop handling decided in the table.
func TestEachSubcommandHasAStopPolicy(t *testing.T) {
	t.Parallel()
	for _, c := range subcommands {
		if c.stops != stopEnds && c.stops != stopCancels {
			t.Errorf("orchestra %s: stop policy %d, neither stopEnds nor stopCancels", c.name, c.stops)
		}
	}
}

// Under stopEnds a subcommand works under its own context and is never stopped; under stopCancels the
// end of its context isn't taken for a stop signal.
func TestStopCatchTellsAStopFromTheContextEnding(t *testing.T) {
	t.Parallel()
	for _, p := range []stopPolicy{stopEnds, stopCancels} {
		ctx, cancel := context.WithCancel(context.Background())
		stop, release := p.catch(ctx)
		if stop.stopped() || stop.ctx.Err() != nil {
			t.Errorf("policy %d: stopped %v, ctx %v before anything ended", p, stop.stopped(), stop.ctx.Err())
		}
		cancel()
		if stop.stopped() || stop.ctx.Err() == nil {
			t.Errorf("policy %d: stopped %v, ctx %v once its context ended: want not stopped, ctx done",
				p, stop.stopped(), stop.ctx.Err())
		}
		release()
	}
}
