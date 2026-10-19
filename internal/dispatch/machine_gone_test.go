package dispatch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

// A file whose PID runs a process that started after the file's run did is a run that ended, its PID
// taken again since: it is ignored, said nothing of and removed. One whose process started before its
// run did is a run going on: it is said of once, its workers count toward the cap, and it is left.
func TestRunIgnoresRegistrationWhosePIDWasTakenAgain(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		after time.Duration // when the PID's process started, after the run's Started
		gone  bool
	}{{"started after", time.Hour, true}, {"started before", -time.Second, false}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				dir := sharing(t, h)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				reg := filepath.Join(dir, "run-999999-1.json")
				started := time.Now().Add(-24 * time.Hour)
				body := `{"pid":999999,"repo":"/elsewhere","concurrent":4,"started":"` +
					started.UTC().Format(time.RFC3339Nano) + `","running":4}`
				if err := os.WriteFile(reg, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
				h.cfg.MachineWorkers = 2 // the other run's 4 workers hold the second ticket, if it counts
				h.cfg.Concurrency = 2
				h.beads.add("A", "ticket A", 1)
				h.beads.add("B", "ticket B", 2)
				h.worker("A", slowly(10*time.Minute, "a.txt"))
				h.worker("B", slowly(10*time.Minute, "b.txt"))
				o := h.loop()
				o.process = func(pid int) (time.Time, bool) {
					if pid == 999999 {
						return started.Add(tc.after), true
					}
					return time.Time{}, processAlive(pid)
				}
				if code := o.Run(context.Background()); code != ExitOK {
					t.Fatalf("exit %d\n%s", code, h.sink.text())
				}
				shared, holds := h.sink.of(EvShared), h.sink.of(EvMachine)
				_, err := os.Stat(reg)
				if tc.gone {
					if len(shared) != 0 || len(holds) != 0 {
						t.Errorf("shared lines %q, holds %q; want none", shared, holds)
					}
					if !os.IsNotExist(err) {
						t.Errorf("the gone run's file is still there: %v", err)
					}
					return
				}
				if len(shared) != 1 || len(holds) != 1 {
					t.Errorf("shared lines %q, holds %q; want one of each", shared, holds)
				}
				if err != nil {
					t.Errorf("the other run's file is gone: %v", err)
				}
			})
		})
	}
}

// A runs' folder removed under the run counts no other run's workers, says nothing, and is made
// again, with the run's own file, as the run next writes how many of its workers run.
func TestMachineFolderRemovedUnderTheRun(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "runs")
	path, err := createRegistration(dir, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	m := &machine{dir: dir, path: path, seen: map[string]registration{},
		process: func(int) (time.Time, bool) { return time.Time{}, true },
		me:      registration{PID: os.Getpid(), Repo: "/here", Concurrent: 2, Started: time.Now()}}
	m.running(1)
	body := `{"pid":999999,"repo":"/elsewhere","concurrent":4,"started":"2026-10-19T08:00:00Z","running":3}`
	if err := os.WriteFile(filepath.Join(dir, "run-999999-1.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if evs := m.look(); len(evs) != 1 || m.others != 3 {
		t.Fatalf("events %+v, %d other workers; want the other run's start, 3", evs, m.others)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if evs := m.look(); len(evs) != 0 || m.others != 0 {
		t.Errorf("without the folder: events %+v, %d other workers; want none, 0", evs, m.others)
	}
	m.running(1)
	if reg, ok := readRegistration(path); !ok || reg.Running != 1 {
		t.Errorf("the run's own file, written again: %+v (%v), want 1 running", reg, ok)
	}
}

// A run that holds a free slot for the machine's cap and then stops taking tickets (it winds down)
// still hears the hold end, once the other run's worker finishes: the dashboard's note is cleared,
// and the event stream's last queue record says it no longer holds.
func TestMachineHoldEndsWhileNotTakingTickets(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		first, second := newTimedHarness(t), newTimedHarness(t)
		sharing(t, first, second)
		first.cfg.MachineWorkers, second.cfg.MachineWorkers = 2, 2
		second.cfg.Concurrency = 2
		first.beads.add("A", "ticket A", 1)
		first.worker("A", slowly(20*time.Minute, "a.txt"))
		second.beads.add("B1", "ticket B1", 1)
		second.beads.add("B2", "ticket B2", 2)
		second.worker("B1", slowly(60*time.Minute, "b1.txt"))
		second.worker("B2", finishes("b2.txt"))

		firstDone := runAside(first)
		time.Sleep(time.Minute)
		o := second.loop()
		o.log.Begin(second.repo, RunStart{Started: time.Now(), Repo: second.repo, Branch: "main", Concurrency: 2})
		done := make(chan int, 1)
		go func() { done <- o.Run(context.Background()) }()
		time.Sleep(5 * time.Minute)
		o.Drain("from the test")
		code := <-done
		o.log.End(code)
		if err := o.log.Close(); err != nil {
			t.Fatal(err)
		}
		if code != ExitOK {
			t.Fatalf("second run: exit %d\n%s", code, second.sink.text())
		}
		if code := <-firstDone; code != ExitOK {
			t.Fatalf("first run: exit %d\n%s", code, first.sink.text())
		}

		s := second.sink
		if !s.dispatchedAt("B2").IsZero() {
			t.Errorf("B2 dispatched, though the run wound down while it waited\n%s", s.text())
		}
		queues := s.eventsOf(EvQueue)
		if len(queues) < 2 || !queues[len(queues)-2].MachineFull {
			t.Fatalf("queue events %+v, want the hold, then its end\n%s", queues, s.text())
		}
		last, aClosed := queues[len(queues)-1], first.sink.closedAt("A")
		if last.MachineFull || last.Time.Before(aClosed) || last.Time.Sub(aClosed) > readyPoll+time.Second {
			t.Errorf("last queue event %+v, want the hold's end within a poll of A closing (%v)", last, aClosed)
		}
		var machine []any
		for _, r := range records(t, second.repo) {
			if r["kind"] == "queue" {
				machine = append(machine, r["machine_full"])
			}
		}
		if n := len(machine); n < 2 || machine[n-2] != true || machine[n-1] != nil {
			t.Errorf("the stream's queue records give machine_full %v, want true, then none", machine)
		}
	})
}

// A process's start time is when it started: the test's own before now, a child's about as it is
// started. A process that has exited isn't running.
func TestRunningProcess(t *testing.T) {
	t.Parallel()
	if start, alive := runningProcess(os.Getpid()); !alive || start.IsZero() || start.After(time.Now()) {
		t.Errorf("the test's own process: started %v, alive %v; want before now, alive", start, alive)
	}
	before := time.Now()
	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	start, alive := runningProcess(cmd.Process.Pid)
	after := time.Now()
	_ = cmd.Process.Kill() // done with it
	if err := cmd.Wait(); err == nil {
		t.Error("sleep, killed, exited cleanly")
	}
	if !alive || start.Before(before.Add(-startSlack)) || start.After(after.Add(startSlack)) {
		t.Errorf("a child started between %v and %v: started %v, alive %v", before, after, start, alive)
	}
	if _, alive := runningProcess(cmd.Process.Pid); alive {
		t.Errorf("process %d, which has exited, is running", cmd.Process.Pid)
	}
}
