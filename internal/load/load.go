// Package load reads whether the workers are overloading the machine: the memory each worker's
// process tree takes, and orchestra's own (its merge check above all), from one process listing, and
// the kernel's memory pressure, swapping and CPU load, on macOS and Linux. The dashboard shows it on a
// line of its own, each figure coloured by its own Level (Levels).
package load

import (
	"context"
	"time"
)

// Unknown is the memory of a worker whose process tree can't be found: its root not known, or gone
// from the listing.
const Unknown int64 = -1

// The thresholds that colour the line's figures (see levels): tune them here.
const (
	// BusyLoad is the 1-minute load average per core past which the CPU turns amber.
	BusyLoad = 1.5
	// OverLoad is the load per core past which, held for OverLoadFor, the CPU turns red.
	OverLoad    = 3.0
	OverLoadFor = time.Minute
	// SwappingPerMinute is the pages swapped in and out per minute at which swapping is under way, and
	// shows, in amber.
	SwappingPerMinute = 600
	// Linux's pressure stall information (/proc/pressure/memory) gives the level of memory pressure
	// macOS gives itself: the share of the last 10 seconds, in percent, in which some task stalled on
	// memory (StallWarn: warn), or every task did (StallCritical: critical). A kernel without it gives
	// the share of memory available: under AvailableWarn, warn; under AvailableCritical, critical.
	StallWarn         = 10.0
	StallCritical     = 10.0
	AvailableWarn     = 0.10
	AvailableCritical = 0.05
)

// Level is how hard the machine is pressed, for the line's colour.
type Level int

// The levels.
const (
	Green Level = iota // nothing pressed
	Amber              // the load past BusyLoad per core, or swapping under way
	Red                // the kernel's memory pressure at warn or worse, or the load past OverLoad per core for OverLoadFor
)

// Levels is each signal's own level, for its figure on the line: the memory the workers take has no
// threshold, and none.
type Levels struct {
	CPU      Level // the load per core: amber past BusyLoad, red past OverLoad for OverLoadFor
	Pressure Level // the kernel's memory pressure: red at warn or worse
	Swapping Level // amber while swapping is under way
}

// Worst is the highest of the levels.
func (l Levels) Worst() Level { return max(l.CPU, l.Pressure, l.Swapping) }

// Reading is one look at the machine.
type Reading struct {
	At time.Time
	// Workers is the memory of each running worker's process tree, in bytes, by ticket: Unknown for one
	// whose tree can't be found.
	Workers map[string]int64
	// Check is the memory of what orchestra itself runs, its merge check above all, in bytes.
	Check int64
	Kernel
	// Swapping is the pages swapped in and out per minute since the last reading; -1 before there are
	// two to tell.
	Swapping float64
	// Levels is each signal's level; Level the worst of them, how hard the machine is pressed.
	Levels Levels
	Level  Level
}

// Memory is the workers' memory and orchestra's own together, in bytes: those whose tree is unknown
// count for nothing.
func (r Reading) Memory() int64 { return r.WorkerMemory() + r.Check }

// WorkerMemory is the workers' memory, those whose tree is known, in bytes.
func (r Reading) WorkerMemory() int64 {
	var n int64
	for _, m := range r.Workers {
		if m > 0 {
			n += m
		}
	}
	return n
}

// Sampler takes Readings, keeping what one needs of the last: its swap count, for the rate, and since
// when the load has been past OverLoad per core.
type Sampler struct {
	// Processes lists the machine's processes; Machine reads the kernel's figures. Tests give their own.
	Processes func(ctx context.Context) (Processes, error)
	Machine   func(ctx context.Context) (Kernel, error)
	Self      int // orchestra's own process, whose descendants are its merge check and its other commands

	last      Kernel
	lastAt    time.Time
	overSince time.Time // when the load went past OverLoad per core; zero while it isn't
}

// NewSampler is a Sampler of this machine, for orchestra's process self.
func NewSampler(self int) *Sampler {
	return &Sampler{Processes: List, Machine: ReadKernel, Self: self}
}

// Read takes a Reading at now: the memory of each worker's tree from roots, the processes at the root
// of each worker by ticket (none: Unknown), and the kernel's figures. Figures that can't be read are
// left out: a listing that fails gives every worker Unknown, a kernel that can't be read no pressure
// or load.
func (s *Sampler) Read(ctx context.Context, now time.Time, roots map[string][]int) Reading {
	r := Reading{At: now, Workers: map[string]int64{}, Kernel: unknownKernel(), Swapping: -1}
	ps, err := s.Processes(ctx)
	var all []int
	for _, rs := range roots {
		all = append(all, rs...)
	}
	for ticket, rs := range roots {
		r.Workers[ticket] = Unknown
		if err == nil {
			r.Workers[ticket] = ps.Tree(rs...)
		}
	}
	if err == nil {
		r.Check = ps.Below(s.Self, all...)
	}
	if k, err := s.Machine(ctx); err == nil {
		r.Kernel = k
		if s.last.Swaps >= 0 && k.Swaps >= s.last.Swaps && !s.lastAt.IsZero() && now.After(s.lastAt) {
			r.Swapping = float64(k.Swaps-s.last.Swaps) / now.Sub(s.lastAt).Minutes()
		}
		s.last, s.lastAt = k, now
	}
	r.Levels = s.levels(r, now)
	r.Level = r.Levels.Worst()
	return r
}

// levels is r's Levels at now, by the thresholds above, keeping since when the load has been past
// OverLoad per core.
func (s *Sampler) levels(r Reading, now time.Time) Levels {
	perCore := -1.0
	if r.Cores > 0 && r.Load1 >= 0 {
		perCore = r.Load1 / float64(r.Cores)
	}
	switch {
	case perCore > OverLoad && s.overSince.IsZero():
		s.overSince = now
	case perCore <= OverLoad:
		s.overSince = time.Time{}
	}
	var l Levels
	switch {
	case !s.overSince.IsZero() && now.Sub(s.overSince) >= OverLoadFor:
		l.CPU = Red
	case perCore > BusyLoad:
		l.CPU = Amber
	}
	if r.Pressure >= PressureWarn {
		l.Pressure = Red
	}
	if r.Swapping >= SwappingPerMinute {
		l.Swapping = Amber
	}
	return l
}
