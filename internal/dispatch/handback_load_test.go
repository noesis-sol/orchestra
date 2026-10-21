package dispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/load"
	"github.com/noesis-sol/orchestra/internal/project"
)

// orchestra-cgko: on a loaded machine a worker's tests run several times slower, and a worker making
// progress could pass a hand-back's time limit. The time the machine is loaded, the run held at
// Config.MachineWorkers or the load reading pressed, is left out of the limit, as the usage limit's
// wait is, up to the limit again.

// heldHandBack is lateHarness's run sharing the machine with another run, whose two workers running keep
// it at Config.MachineWorkers while ticket B waits, A's hand-back included, which takes no slot, until the
// returned function frees the other run's slots.
func heldHandBack(t *testing.T) (*harness, func()) {
	t.Helper()
	h := lateHarness(t)
	dir := sharing(t, h)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "run-424242-1.json")
	writeOther := func(running int) {
		body := fmt.Sprintf(`{"pid":424242,"repo":"/elsewhere","concurrent":2,"started":"1999-12-31T00:00:00Z",`+
			`"running":%d}`, running)
		if err := os.WriteFile(other, []byte(body), 0o644); err != nil {
			t.Error(err)
		}
	}
	writeOther(2)
	h.cfg.Concurrency, h.cfg.MachineWorkers = 2, 2
	h.beads.add("B", "ticket B", 2)
	h.worker("B", slowly(time.Minute, "b.txt"))
	return h, func() { writeOther(0) }
}

// runHeld runs h's loop, on which any other run is alive, on a goroutine of its own; the channel it
// returns gets the loop and its exit code once it ends.
func runHeld(h *harness) <-chan func() (*Loop, int) {
	o := h.loop()
	o.process = func(int) (time.Time, bool) { return time.Time{}, true }
	done := make(chan func() (*Loop, int), 1)
	go func() {
		code := o.Run(context.Background())
		done <- func() (*Loop, int) { return o, code }
	}()
	return done
}

// of is the times of the prompts pasted to ticket id's worker, in order.
func (p *pasteTimes) of(id string) []time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]time.Time(nil), p.at[id]...)
}

// A hand-back during which the run holds at the machine's cap for a while has its deadline extended by
// that while: its worker, at it still with its rebase finished, is stopped that much later than the
// limit, and its branch taken.
func TestAMachineHoldDuringAHandBackExtendsItsDeadline(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, free := heldHandBack(t)
		h.worker("A", stillWorking(resolvesInMemory(h.mem)), idles)
		var pasted pasteTimes
		h.herdr.onPrompt = pasted.on
		done := runHeld(h)
		for len(pasted.of("A")) == 0 {
			time.Sleep(time.Second)
		}
		handedBack := pasted.of("A")[0]
		held := 7 * time.Minute
		// Held from the hand-back itself, not from when the loop above saw it: that is a second later
		// when the prompt is pasted at the instant the loop wakes, if the bubble wakes the loop first.
		time.Sleep(time.Until(handedBack.Add(held)))
		free()
		o, code := (<-done)()
		ev := h.sink.text()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), ev)
		}
		if !h.sink.has(EvClosed, "A") || !strings.Contains(ev, "A's worker was still at its hand-back after 20m, "+
			"but its rebase has finished: wt/A is taken as it is") {
			t.Errorf("events:\n%s", ev)
		}
		prompted := pasted.of("A")
		if len(prompted) != 2 {
			t.Fatalf("%d prompts, want the hand-back and the word that it is over\n%s", len(prompted), ev)
		}
		want := project.DefaultResolveTimeout + held
		if took := prompted[1].Sub(handedBack) - escapeSettle; took < want ||
			took > want+machinePoll+2*statusPoll {
			t.Errorf("told %s after the hand-back, want %s: the limit and the %s held", took, want, held)
		}
	})
}

// A run held at the machine's cap all through a hand-back extends its deadline by the limit at most:
// a worker still mid-rebase is stopped at twice the limit and the ticket set aside, the line saying why.
func TestAMachineHoldExtendsAHandBackByTheLimitAtMost(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, _ := heldHandBack(t)
		wt := h.worktree("A")
		h.worker("A", func(w *fakeWorker) AgentState {
			w.git.replayed(wt, "main", "A: add shared.txt", "shared.txt")
			return "working"
		}, idles)
		var pasted pasteTimes
		h.herdr.onPrompt = pasted.on
		o, code := (<-runHeld(h))()
		ev := h.sink.text()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), ev)
		}
		if !h.sink.warnedWith("MERGE_CONFLICT", "A", "its worker was still working after 20m (and 20m more for the "+
			"machine's load); the rebase was aborted") {
			t.Errorf("events:\n%s", ev)
		}
		prompted := pasted.of("A")
		if len(prompted) != 2 {
			t.Fatalf("%d prompts, want the hand-back and the word that it is over\n%s", len(prompted), ev)
		}
		want := 2 * project.DefaultResolveTimeout
		if took := prompted[1].Sub(prompted[0]) - escapeSettle; took < want || took > want+2*statusPoll {
			t.Errorf("told %s after the hand-back, want %s: the limit, extended by itself at most", took, want)
		}
		if !h.sink.has(EvDispatch, "B") {
			t.Errorf("B should start once A is set aside, freeing the slot:\n%s", ev)
		}
	})
}

// The machine counts as loaded while the dashboard's last load reading has it pressed: memory
// pressure at warn or worse, swapping, or the load per core past OverLoad for OverLoadFor; a busy CPU
// alone doesn't count, nor does anything once the load is no longer read.
func TestAPressedLoadReadingCountsAsLoaded(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		levels load.Levels
		want   bool
	}{
		{"calm", load.Levels{}, false},
		{"busy CPU", load.Levels{CPU: load.Amber}, false},
		{"overloaded CPU", load.Levels{CPU: load.Red}, true},
		{"memory pressure", load.Levels{Pressure: load.Red}, true},
		{"swapping", load.Levels{Swapping: load.Amber}, true},
	} {
		if got := pressing(load.Reading{Levels: tc.levels}); got != tc.want {
			t.Errorf("%s: pressing %t, want %t", tc.name, got, tc.want)
		}
	}

	synctest.Test(t, func(t *testing.T) {
		panes := &fakePanes{roots: map[string][]int{}, asked: map[string]int{}}
		listing := loadListing
		o := loadLoop(panes, &listing)
		o.sampler.Machine = func(context.Context) (load.Kernel, error) {
			return load.Kernel{Pressure: load.PressureWarn, Swaps: -1, Load1: 1, Cores: 4, Available: -1}, nil
		}
		o.sink = &loadSink{}
		if o.loaded() {
			t.Error("loaded before any reading")
		}
		stop := o.watchLoad(t.Context())
		time.Sleep(time.Second)
		if !o.loaded() {
			t.Error("not loaded with the kernel's memory pressure at warn")
		}
		stop()
		if o.loaded() {
			t.Error("loaded still once the load is no longer read")
		}
	})
}

// The load's time counts from the poll that first finds the machine loaded to the one that finds it
// no longer, every such while added up, up to the most given.
func TestLoadWaitAddsUpTheLoadedWhiles(t *testing.T) {
	t.Parallel()
	start := time.Now()
	at := func(m int) time.Time { return start.Add(time.Duration(m) * time.Minute) }
	var l loadWait
	l.note(at(0), false)
	l.note(at(1), true)
	l.note(at(2), true)
	l.note(at(4), false)
	l.note(at(5), true)
	if got := l.held(at(6), time.Hour); got != 4*time.Minute {
		t.Errorf("held %s, want 4m: 3m, then 1m loaded still", got)
	}
	if got := l.held(at(6), 2*time.Minute); got != 2*time.Minute {
		t.Errorf("held %s, want the most, 2m", got)
	}
}
