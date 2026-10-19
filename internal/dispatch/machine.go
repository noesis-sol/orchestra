package dispatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// Runs share the machine, and their checks slow each other down. Each run registers itself while it
// runs, a file of its own in Config.Machine (orchestra's folder in the user's cache folder, runs/)
// with its PID, repository, concurrent, start time and how many of its workers run, written again as
// that changes, and removed as the run ends. A run reads the others' as it goes: it says once of
// each other run that it is going (SHARED_MACHINE), and that it has ended; with
// Config.MachineWorkers, a free slot waits while the workers running across all the runs are at that
// cap (HOLD: MACHINE), and is filled once one of them finishes, as the run next reads the others: every
// machinePoll while it holds. A file whose PID is gone, or is a process that started after the run did
// (the PID taken again since), is a run that ended without removing it: it is ignored and removed. A
// folder that can't be read holds no run, and one removed under the runs (a cache cleaner) is made
// again as each next writes.

// registration is a run's file in the machine's folder of runs.
type registration struct {
	PID        int       `json:"pid"`
	Repo       string    `json:"repo"`
	Concurrent int       `json:"concurrent"`
	Started    time.Time `json:"started"`
	Running    int       `json:"running"` // its workers running, merges and checks included
}

// registrationExt ends a registration's name; one being written ends in .tmp until it is renamed.
const registrationExt = ".json"

// machine is the run's registration and what it knows of the other runs: Run's own.
type machine struct {
	dir     string
	path    string // the run's file; "" when there is no folder, or it couldn't be made
	me      registration
	written bool                    // me is in its file as it is
	seen    map[string]registration // the other runs' files, by name, as last read
	others  int                     // the workers running in the other runs, as last read
	full    bool                    // the run holds a free slot for Config.MachineWorkers, as last said
	process func(pid int) (start time.Time, alive bool)
}

// register registers the run in Config.Machine, if it has one, and returns its machine; a folder or
// file it can't make is logged, and the run goes on without one, still reading the others'.
func (o *Loop) register() *machine {
	c := o.cfg
	m := &machine{dir: c.Machine, seen: map[string]registration{}, process: o.process,
		me: registration{PID: os.Getpid(), Repo: c.Repo, Concurrent: c.Concurrency, Started: o.started}}
	if m.process == nil {
		m.process = runningProcess
	}
	if m.dir == "" {
		return m
	}
	path, err := createRegistration(m.dir, m.me.PID)
	if err != nil {
		o.info("  could not register the run among the machine's orchestra runs: %v", err)
		return m
	}
	m.path = path
	if err := m.write(); err != nil {
		o.info("  could not register the run among the machine's orchestra runs: %v", err)
	}
	return m
}

// createRegistration makes a new, empty registration file in dir, for the run of process pid, and
// returns its path: a name of its own, as runs in one process (tests) share a PID.
func createRegistration(dir string, pid int) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, fmt.Sprintf("run-%d-*%s", pid, registrationExt))
	if err != nil {
		return "", err
	}
	return f.Name(), f.Close()
}

// write writes the run's registration to its file, through a file beside it renamed over it, so that
// a run reading it never finds it half written; a folder removed under the run is made again.
func (m *machine) write() error {
	b, err := json.Marshal(m.me)
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	err = os.WriteFile(tmp, b, 0o644)
	if errors.Is(err, fs.ErrNotExist) {
		if err = os.MkdirAll(m.dir, 0o755); err == nil {
			err = os.WriteFile(tmp, b, 0o644)
		}
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, m.path); err != nil {
		return err
	}
	m.written = true
	return nil
}

// running writes that n of the run's workers run, when that changed; a failed write is tried again
// the next time.
func (m *machine) running(n int) {
	if m.path == "" || m.written && m.me.Running == n {
		return
	}
	m.me.Running = n
	m.written = false
	_ = m.write() // the others go on reading the count last written; this one is written again next time
}

// unregister removes the run's file, as the run ends.
func (m *machine) unregister() {
	if m.path == "" {
		return
	}
	_ = os.Remove(m.path) // one left behind is a run whose PID is gone: the others remove it
	m.path = ""
}

// look reads the other runs' files, removing those whose run is gone (see gone), and returns the
// events that say which runs started and ended since it last read them, those that started first by
// when they started. A folder it can't read counts no other run's workers, and says nothing of the
// runs last seen in it: they are heard of again once it can be read.
func (m *machine) look() []Event {
	if m.dir == "" {
		return nil
	}
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		// No folder yet, as no run has registered, or one removed under the runs: the run's own file
		// is written again, the folder with it (see write).
		m.others, m.written = 0, false
		return nil
	}
	now := map[string]registration{}
	mine := false
	for _, e := range entries {
		name := e.Name()
		path := filepath.Join(m.dir, name)
		if path == m.path {
			mine = true
		}
		if !strings.HasSuffix(name, registrationExt) || path == m.path {
			continue
		}
		reg, ok := readRegistration(path)
		switch {
		case !ok: // one being made, or not orchestra's
		case m.gone(reg):
			_ = os.Remove(path) // another run may have removed it first
		default:
			now[name] = reg
		}
	}
	if !mine {
		m.written = false // removed under the run: written again as it next writes
	}
	var started []string
	m.others = 0
	for name, reg := range now {
		m.others += reg.Running
		if _, ok := m.seen[name]; !ok {
			started = append(started, name)
		}
	}
	slices.SortFunc(started, func(a, b string) int {
		if c := now[a].Started.Compare(now[b].Started); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	var evs []Event
	for name, reg := range m.seen {
		if _, ok := now[name]; !ok {
			evs = append(evs, Event{Kind: EvShared, Shared: len(now),
				Text: fmt.Sprintf("  the other orchestra run in %s has ended", reg.Repo)})
		}
	}
	for _, name := range started {
		reg := now[name]
		evs = append(evs, Event{Kind: EvShared, Shared: len(now), Text: fmt.Sprintf(
			"SHARED_MACHINE: another orchestra run is going in %s with %s; their checks slow each other down",
			reg.Repo, workersCount(reg.Concurrent))})
	}
	m.seen = now
	return evs
}

// readRegistration reads the registration in path, and reports whether it is one.
func readRegistration(path string) (registration, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return registration{}, false
	}
	var reg registration
	if json.Unmarshal(b, &reg) != nil || reg.PID <= 0 {
		return registration{}, false
	}
	return reg, true
}

// workersCount is "1 worker" or "<n> workers".
func workersCount(n int) string {
	if n == 1 {
		return "1 worker"
	}
	return fmt.Sprintf("%d workers", n)
}

// startSlack is how far a process's start time, as the platform gives it, may fall after the
// Started of its run's registration: Linux gives it to the second.
const startSlack = 2 * time.Second

// gone reports whether reg's run has ended: its PID runs no process, or a process that started after
// the run did, which took the PID once the run had ended. A start time that can't be read doesn't say.
func (m *machine) gone(reg registration) bool {
	start, alive := m.process(reg.PID)
	return !alive || !start.IsZero() && start.After(reg.Started.Add(startSlack))
}

// runningProcess reports whether process pid runs (see processAlive) and, when it can be read, when it
// started; the zero time when it can't.
func runningProcess(pid int) (time.Time, bool) {
	if !processAlive(pid) {
		return time.Time{}, false
	}
	start, err := processStart(pid)
	if err != nil {
		return time.Time{}, true // another user's, or gone since: judged by its PID alone
	}
	return start, true
}

// processAlive reports whether process pid runs: one that can't be signalled for want of permission
// does, as another user's.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH)
}

// lookAround reads the other runs on the machine, saying which started and ended, and writes how
// many of the run's own workers run. The hold for Config.MachineWorkers ends once they are under it,
// and the dashboard hears so then, whether or not the run goes on taking tickets.
func (r *runState) lookAround() {
	r.machine.running(len(r.inflight))
	for _, ev := range r.machine.look() {
		r.o.emit(ev)
	}
	if !r.atCap() {
		r.machine.full = false
		if r.fullShown {
			r.reportQueue(r.queued)
		}
	}
}

// atCap reports whether the workers running across the machine's runs, as last read, this run's
// included, are at Config.MachineWorkers, with one of this run's among them: a run never holds with no
// worker of its own running, so that one at the cap with others' workers alone still gets on.
func (r *runState) atCap() bool {
	limit, own := r.o.cfg.MachineWorkers, len(r.inflight)
	return limit > 0 && own > 0 && own+r.machine.others >= limit
}

// machineFull reports whether the ticket to start waits for a free slot across the machine (see
// atCap), and says HOLD: MACHINE once as the run begins to hold.
func (r *runState) machineFull() bool {
	m, limit, own := r.machine, r.o.cfg.MachineWorkers, len(r.inflight)
	if !r.atCap() {
		m.full = false
		return false
	}
	if !m.full {
		m.full = true
		r.o.emit(Event{Kind: EvMachine, Text: fmt.Sprintf(
			"HOLD: MACHINE: the machine's orchestra runs have %s running, %d of them this run's, at the cap of %d "+
				"(ORCHESTRA_MACHINE_WORKERS); the next ticket starts once one of them finishes",
			workersCount(own+m.others), own, limit)})
	}
	return true
}

// heldPoll returns a channel that ticks every machinePoll while the run holds a free slot for
// Config.MachineWorkers, nil (which never ticks) while it doesn't, and the function that stops it.
func (r *runState) heldPoll() (<-chan time.Time, func()) {
	if !r.machine.full || !r.taking() {
		return nil, func() {}
	}
	t := time.NewTicker(machinePoll)
	return t.C, t.Stop
}

// stillHeld reads the other runs as heldPoll ticks (see lookAround), and reports whether the run still
// holds for Config.MachineWorkers: only once it doesn't does Run go round to start the next ticket,
// reading bd ready, which it would otherwise do every machinePoll.
func (r *runState) stillHeld() bool {
	r.lookAround()
	return r.machine.full
}
