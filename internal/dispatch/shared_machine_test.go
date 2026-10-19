package dispatch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// sharing gives the harnesses' runs one folder of the machine's runs, and returns it.
func sharing(t *testing.T, hs ...*harness) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "runs")
	for _, h := range hs {
		h.cfg.Machine = dir
	}
	return dir
}

// runAside runs the harness's loop on a goroutine of its own; the channel it returns gets its exit
// code.
func runAside(h *harness) <-chan int {
	o := h.loop()
	code := make(chan int, 1)
	go func() { code <- o.Run(context.Background()) }()
	return code
}

// registered returns the names of the runs' files in dir.
func registered(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// closedAt is when ticket id closed, zero if it didn't.
func (s *runSink) closedAt(id string) time.Time {
	for _, ev := range s.eventsOf(EvClosed) {
		if ev.Ticket == id {
			return ev.Time
		}
	}
	return time.Time{}
}

// A run that starts beside another says so once, naming the other's repository and its workers; the
// first, reading the runs as it polls, says so of the second once, and then that it has ended. The
// dashboard hears how many other runs are going. Both remove their files as they end.
func TestRunBesideAnotherSaysTheMachineIsShared(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		first, second := newTimedHarness(t), newTimedHarness(t)
		dir := sharing(t, first, second)
		first.cfg.Concurrency = 3
		first.beads.add("A", "ticket A", 1)
		first.worker("A", slowly(20*time.Minute, "a.txt"))
		second.beads.add("B", "ticket B", 1)
		second.worker("B", slowly(5*time.Minute, "b.txt"))

		firstDone := runAside(first)
		time.Sleep(time.Minute)
		_, code := second.run()
		if code != ExitOK {
			t.Fatalf("second run: exit %d\n%s", code, second.sink.text())
		}
		if code := <-firstDone; code != ExitOK {
			t.Fatalf("first run: exit %d\n%s", code, first.sink.text())
		}

		want := "SHARED_MACHINE: another orchestra run is going in " + first.repo +
			" with 3 workers; their checks slow each other down"
		if got := second.sink.of(EvShared); len(got) != 1 || got[0] != want {
			t.Errorf("second run's shared lines %q, want one: %q", got, want)
		}
		got := first.sink.eventsOf(EvShared)
		if len(got) != 2 || !strings.HasPrefix(got[0].Text, "SHARED_MACHINE: another orchestra run is going in "+
			second.repo+" with 1 worker;") || got[0].Shared != 1 ||
			got[1].Text != "  the other orchestra run in "+second.repo+" has ended" || got[1].Shared != 0 {
			t.Errorf("first run's shared events %+v, want the second's start, then its end", got)
		}
		if !strings.Contains(read(t, second.logPath), want) {
			t.Errorf("the log hasn't the line %q", want)
		}
		if left := registered(t, dir); len(left) != 0 {
			t.Errorf("files left in the runs' folder: %v", left)
		}
	})
}

// A file whose run's process is gone is ignored, said nothing of, and removed; a file that isn't a
// run's is left as it is.
func TestRunIgnoresRegistrationWhoseProcessIsGone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := sharing(t, h)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		gone := filepath.Join(dir, "run-999999-1.json")
		body := `{"pid":999999,"repo":"/elsewhere","concurrent":4,"started":"2026-10-19T08:00:00Z","running":4}`
		if err := os.WriteFile(gone, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		other := filepath.Join(dir, "notes.txt")
		if err := os.WriteFile(other, []byte("not a run"), 0o644); err != nil {
			t.Fatal(err)
		}
		h.cfg.MachineWorkers = 1 // the gone run's 4 workers would hold the second ticket
		h.cfg.Concurrency = 2
		h.beads.add("A", "ticket A", 1)
		h.beads.add("B", "ticket B", 2)
		h.worker("A", slowly(10*time.Minute, "a.txt"))
		h.worker("B", slowly(10*time.Minute, "b.txt"))
		o := h.loop()
		o.process = func(pid int) (time.Time, bool) { return time.Time{}, pid != 999999 }
		if code := o.Run(context.Background()); code != ExitOK {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		if got := h.sink.of(EvShared); len(got) != 0 {
			t.Errorf("shared lines %q, want none", got)
		}
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("the gone run's file is still there: %v", err)
		}
		if got := registered(t, dir); len(got) != 1 || got[0] != "notes.txt" {
			t.Errorf("the runs' folder holds %v, want only notes.txt", got)
		}
	})
}

// With a cap on the machine's workers, a run's free slot waits while the workers across the runs are
// at it, saying so once, and is filled once the other run's worker finishes, while that run goes on.
// A run with no worker of its own running starts one even at the cap. Without a cap, nothing waits.
func TestMachineCapHoldsFreeSlot(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		cap  int
	}{{"cap", 3}, {"no cap", 0}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				first, second := newTimedHarness(t), newTimedHarness(t)
				sharing(t, first, second)
				first.cfg.Concurrency, second.cfg.Concurrency = 2, 2
				first.cfg.MachineWorkers, second.cfg.MachineWorkers = tc.cap, tc.cap
				first.beads.add("A1", "ticket A1", 1)
				first.beads.add("A2", "ticket A2", 2)
				first.worker("A1", slowly(10*time.Minute, "a1.txt"))
				first.worker("A2", slowly(40*time.Minute, "a2.txt"))
				second.beads.add("B1", "ticket B1", 1)
				second.beads.add("B2", "ticket B2", 2)
				second.worker("B1", slowly(60*time.Minute, "b1.txt"))
				second.worker("B2", slowly(time.Minute, "b2.txt"))

				firstDone := runAside(first)
				time.Sleep(time.Minute)
				_, code := second.run()
				if code != ExitOK {
					t.Fatalf("second run: exit %d\n%s", code, second.sink.text())
				}
				if code := <-firstDone; code != ExitOK {
					t.Fatalf("first run: exit %d\n%s", code, first.sink.text())
				}

				s := second.sink
				b1, b2 := s.dispatchedAt("B1"), s.dispatchedAt("B2")
				a1Closed, a2Closed := first.sink.closedAt("A1"), first.sink.closedAt("A2")
				holds := s.of(EvMachine)
				if tc.cap == 0 {
					if len(holds) != 0 || b2.Sub(b1) > time.Second {
						t.Errorf("without a cap B2 started %v after B1, holds %q; want at once, no hold\n%s",
							b2.Sub(b1), holds, s.text())
					}
					return
				}
				want := "HOLD: MACHINE: the machine's orchestra runs have 3 workers running, 1 of them this run's, " +
					"at the cap of 3 (ORCHESTRA_MACHINE_WORKERS); the next ticket starts once one of them finishes"
				if len(holds) != 1 || holds[0] != want {
					t.Errorf("holds %q, want one: %q", holds, want)
				}
				if b1.IsZero() || !b1.Before(a1Closed) {
					t.Errorf("B1 dispatched at %v, want beside the first run's two workers, before A1 closed (%v)",
						b1, a1Closed)
				}
				if !b2.After(a1Closed) || !b2.Before(a2Closed) || b2.Sub(a1Closed) > readyPoll+time.Second {
					t.Errorf("B2 dispatched at %v, want within a poll of A1 closing (%v), while A2 runs (closed %v)",
						b2, a1Closed, a2Closed)
				}
				if !slices.ContainsFunc(s.eventsOf(EvQueue), func(ev Event) bool { return ev.MachineFull && ev.Queued == 1 }) {
					t.Errorf("the dashboard never heard that B2 waits for the machine\n%s", s.text())
				}
				for _, ev := range s.eventsOf(EvDispatch) {
					if ev.MachineFull {
						t.Errorf("%s dispatched with the machine full", ev.Ticket)
					}
				}
			})
		})
	}
}

// A process that has exited is gone; the test's own runs.
func TestProcessAlive(t *testing.T) {
	t.Parallel()
	if !processAlive(os.Getpid()) {
		t.Error("the test's own process isn't alive")
	}
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if processAlive(cmd.Process.Pid) {
		t.Errorf("process %d, which has exited, is alive", cmd.Process.Pid)
	}
}
