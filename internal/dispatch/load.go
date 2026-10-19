package dispatch

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/noesis-sol/orchestra/internal/load"
)

// LoadSink is a Sink that shows whether the workers are overloading the machine, as the dashboard
// does: while the run's sink is one, the run reads the machine's load every loadEvery and gives it
// there. Nothing the run does depends on it.
type LoadSink interface {
	Load(load.Reading)
}

// loadEvery is how often the run reads the machine's load: one process listing and the kernel's
// figures each time, whatever the number of workers.
const loadEvery = 5 * time.Second

// watchLoad reads the machine's load every loadEvery and gives it to the sink, while the sink takes
// it, until the returned stop function is called, which waits for the last reading. A run whose sink
// doesn't take it at the start (plain output) reads nothing: the sink is only ever switched from the
// dashboard to plain output.
func (o *Loop) watchLoad(ctx context.Context) (stop func()) {
	if _, ok := o.loadSink(); !ok {
		return func() {}
	}
	if o.sampler == nil {
		o.sampler = load.NewSampler(os.Getpid())
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if p := recover(); p != nil { // the run goes on; only the dashboard's load line stops changing
				o.emit(Event{Kind: EvWarn, Text: fmt.Sprintf("  LOAD_FAILED: panic: %s; the machine's load is "+
					"not shown for the rest of the run (the stack is in %s)",
					o.logPanic("reading the machine's load", p), o.cfg.LogPath)})
			}
		}()
		roots := map[string][]int{} // the processes at the root of each pane's worker, as the terminal said
		tick := time.NewTicker(loadEvery)
		defer tick.Stop()
		for {
			sink, ok := o.loadSink()
			if !ok {
				return
			}
			sink.Load(o.readLoad(ctx, roots))
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}

// loadSink is the run's sink, and whether it takes the machine's load.
func (o *Loop) loadSink() (LoadSink, bool) {
	o.sinkMu.Lock()
	defer o.sinkMu.Unlock()
	s, ok := o.sink.(LoadSink)
	return s, ok
}

// readLoad reads the machine's load, with the memory of each running worker's process tree: its
// roots, by pane, are asked of the terminal once and kept in roots while its worker runs, and asked
// again once none of them runs any more. A worker whose pane isn't known, or whose terminal can't say,
// is unknown.
func (o *Loop) readLoad(ctx context.Context, roots map[string][]int) load.Reading {
	panes := o.runningPanes()
	byTicket := map[string][]int{}
	for id, pane := range panes {
		if pane == "" || o.processes == nil {
			byTicket[id] = nil
			continue
		}
		if _, ok := roots[pane]; !ok {
			pids, err := o.processes.PaneProcesses(ctx, pane)
			if err != nil {
				byTicket[id] = nil
				continue // asked again next time
			}
			roots[pane] = pids
		}
		byTicket[id] = roots[pane]
	}
	r := o.sampler.Read(ctx, time.Now(), byTicket)
	running := map[string]bool{}
	for id, pane := range panes {
		running[pane] = r.Workers[id] != load.Unknown // gone, or replaced: the terminal is asked again
	}
	for pane := range roots {
		if !running[pane] {
			delete(roots, pane)
		}
	}
	return r
}

// runningPanes are the panes of the tickets being worked on, by ticket: "" for one not known.
func (o *Loop) runningPanes() map[string]string {
	o.mu.Lock()
	defer o.mu.Unlock()
	panes := make(map[string]string, len(o.active))
	for id := range o.active {
		panes[id] = o.placed[id].pane
	}
	return panes
}
