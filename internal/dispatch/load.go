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
// there. Only a hand-back's time limit depends on it (see loaded).
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
		defer o.pressing.Store(false) // no longer read: plain output counts the machine hold alone
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
			r := o.readLoad(ctx, roots)
			o.pressing.Store(pressing(r))
			sink.Load(r)
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}

// pressing reports whether reading r has the machine pressed hard enough to slow a worker's tests
// several times over: the kernel's memory pressure at warn or worse, swapping under way, or the load
// past load.OverLoad per core for load.OverLoadFor. A load past load.BusyLoad alone doesn't: a run's
// workers keep it there.
func pressing(r load.Reading) bool {
	l := r.Levels
	return l.Pressure == load.Red || l.Swapping >= load.Amber || l.CPU == load.Red
}

// loaded reports whether the machine is loaded now, as a hand-back's time limit leaves out (see
// loadWait): the run holds at Config.MachineWorkers, or the last load reading has it pressed (see
// pressing), which a run reads for the dashboard alone.
func (o *Loop) loaded() bool {
	return o.gate.held(gateMachine) || o.pressing.Load()
}

// loadWait is the time a hand-back's worker has spent on a loaded machine (see Loop.loaded), from
// one poll to the next, left out of its time limit as the usage limit's wait is (see limitWait).
type loadWait struct {
	since  time.Time // when the machine was first seen loaded, this time; zero while it isn't
	waited time.Duration
}

// note notes a poll at now that finds the machine loaded or not: one with the worker at the usage
// limit counts as not, its wait left out already.
func (l *loadWait) note(now time.Time, loaded bool) {
	switch {
	case loaded && l.since.IsZero():
		l.since = now
	case !loaded && !l.since.IsZero():
		l.waited += now.Sub(l.since)
		l.since = time.Time{}
	}
}

// held is how long the machine has been loaded by now, up to most: the time it extends the limit by.
func (l *loadWait) held(now time.Time, most time.Duration) time.Duration {
	d := l.waited
	if !l.since.IsZero() {
		d += now.Sub(l.since)
	}
	return min(d, most)
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
	active := o.activeList()
	panes := make(map[string]string, len(active))
	for _, st := range active {
		w, _ := o.book.placedAt(st.Ticket)
		panes[st.Ticket] = w.pane
	}
	return panes
}
