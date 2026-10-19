package dispatch

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/load"
)

// fakePanes is a terminal's PaneProcesses: the roots of each pane, or an error for a pane not in it,
// counting the calls.
type fakePanes struct {
	mu    sync.Mutex
	roots map[string][]int
	asked map[string]int
}

func (f *fakePanes) PaneProcesses(_ context.Context, pane string) ([]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked[pane]++
	if r, ok := f.roots[pane]; ok {
		return r, nil
	}
	return nil, errors.New("no such pane")
}

// loadListing is a process listing: orchestra (100) runs the merge check (200); worker A's pane runs
// 300, whose child 301 runs tests; worker B's runs 400.
const loadListing = "100 1 1000\n200 100 2000\n300 1 3000\n301 300 4000\n400 1 5000\n"

// loadLoop is a loop with workers A, B and C running, in panes pa, pb and none, its listing listing.
func loadLoop(panes *fakePanes, listing *string) *Loop {
	o := &Loop{ledger: newLedger(), tallies: newTallies(nil)}
	o.processes = panes
	o.sampler = &load.Sampler{
		Processes: func(context.Context) (load.Processes, error) { return load.ParseProcesses(*listing) },
		Machine: func(context.Context) (load.Kernel, error) {
			return load.Kernel{Pressure: load.PressureNormal, Swaps: -1, Load1: 1, Cores: 4, Available: -1}, nil
		},
		Self: 100,
	}
	for id, pane := range map[string]string{"A": "pa", "B": "pb", "C": ""} {
		o.active[id] = Status{Ticket: id}
		o.placed[id] = workerSpot{pane: pane}
	}
	return o
}

// Each running worker's memory is its pane's process tree's, children included; the merge check's,
// orchestra's own, counts apart; a worker whose pane or roots aren't known is unknown. The terminal is
// asked for a pane's roots once, and again once they are gone or it couldn't say.
func TestReadLoadSumsEachWorkersTree(t *testing.T) {
	panes := &fakePanes{roots: map[string][]int{"pa": {300}}, asked: map[string]int{}}
	listing := loadListing
	o := loadLoop(panes, &listing)
	roots := map[string][]int{}
	r := o.readLoad(context.Background(), roots)
	want := map[string]int64{"A": 7000 * 1024, "B": load.Unknown, "C": load.Unknown}
	for id, mem := range want {
		if r.Workers[id] != mem {
			t.Errorf("worker %s: %d, want %d", id, r.Workers[id], mem)
		}
	}
	if r.Check != 2000*1024 {
		t.Errorf("merge check: %d, want %d", r.Check, 2000*1024)
	}

	panes.roots["pb"] = []int{400}
	r = o.readLoad(context.Background(), roots)
	if r.Workers["B"] != 5000*1024 {
		t.Errorf("worker B once its terminal says: %d, want %d", r.Workers["B"], 5000*1024)
	}
	if panes.asked["pa"] != 1 || panes.asked["pb"] != 2 {
		t.Errorf("the terminal was asked %v, want pa once and pb twice", panes.asked)
	}

	listing = "100 1 1000\n400 1 5000\n500 1 3000\n" // A's tree is gone
	panes.roots["pa"] = []int{500}                   // a worker resumed in the pane
	if r = o.readLoad(context.Background(), roots); r.Workers["A"] != load.Unknown {
		t.Errorf("worker A, its tree gone: %d, want unknown", r.Workers["A"])
	}
	if r = o.readLoad(context.Background(), roots); r.Workers["A"] != 3000*1024 || panes.asked["pa"] != 2 {
		t.Errorf("worker A after: %d, asked %d times; want its new tree's, asked twice", r.Workers["A"],
			panes.asked["pa"])
	}
}

// loadSink is a Sink that takes the machine's load.
type loadSink struct {
	mu       sync.Mutex
	readings []load.Reading
}

func (*loadSink) Event(Event)   {}
func (*loadSink) Status(Status) {}
func (s *loadSink) Load(r load.Reading) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readings = append(s.readings, r)
}

// plainSink is a Sink that doesn't take the machine's load, as plain output doesn't.
type plainSink struct{}

func (plainSink) Event(Event)   {}
func (plainSink) Status(Status) {}

// The run reads the machine's load every loadEvery for a sink that takes it, and for one that doesn't,
// never.
func TestTheRunReadsTheLoadForTheDashboard(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		panes := &fakePanes{roots: map[string][]int{"pa": {300}, "pb": {400}}, asked: map[string]int{}}
		listing := loadListing
		o := loadLoop(panes, &listing)
		sink := &loadSink{}
		o.sink = sink
		stop := o.watchLoad(t.Context())
		time.Sleep(2*loadEvery + time.Second)
		stop()
		if n := len(sink.readings); n != 3 {
			t.Errorf("%d readings in %s, want 3", n, 2*loadEvery+time.Second)
		}
		if r := sink.readings[0]; r.Workers["A"] != 7000*1024 || r.Cores != 4 {
			t.Errorf("first reading %+v", r)
		}

		o = loadLoop(panes, &listing)
		o.sink = plainSink{}
		stop = o.watchLoad(t.Context())
		time.Sleep(2 * loadEvery)
		stop()
		if panes.asked["pa"] != 1 {
			t.Errorf("the terminal was asked %v: plain output should read no load", panes.asked)
		}
	})
}
