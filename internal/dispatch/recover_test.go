package dispatch

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/organ"
)

// panickyMerger merges with git, but dereferences a nil pointer fast-forwarding branch: a panic
// in a worker while it holds the merge queue and the repository.
type panickyMerger struct {
	git.Git
	branch string
}

func (m panickyMerger) FastForward(ctx context.Context, repo, branch string) (string, error) {
	if branch == m.branch {
		var t *Ticket
		return t.Title, nil
	}
	return m.Git.FastForward(ctx, repo, branch)
}

// A worker that panics stops the run like any other reason: it holds, the other ticket still
// merges, and the run ends with PANIC and exit 4, the stack in the log and the ticket left as it was.
func TestWorkerPanicHoldsTheRun(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.cfg.Concurrency = 2
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.worker("A", finishes("a.txt"))
	h.worker("B", func(w *fakeWorker) string { h.waitHeld(); return finishes("b.txt")(w) })
	o := h.loop()
	o.merger = panickyMerger{branch: "wt/A"}
	code := o.Run(context.Background())

	if code != ExitTool {
		t.Errorf("exit %d, want %d", code, ExitTool)
	}
	tab := ""
	if r := o.Running(); len(r) == 1 && r[0].Ticket == "A" {
		tab = r[0].Tab
	} else {
		t.Errorf("active: %v, want A left for review", activeIDs(o))
	}
	want := "PANIC in A: runtime error: invalid memory address or nil pointer dereference; its worktree and tab " + tab +
		" are left for review (the stack is in " + h.logPath + ")"
	if o.Final() != want {
		t.Errorf("final %q, want %q", o.Final(), want)
	}
	if holds := h.sink.of(EvHold); len(holds) != 1 || holds[0] != "A HOLD: "+want+"; no new tickets while the 1 running finish" {
		t.Errorf("holds:\n%s", strings.Join(holds, "\n"))
	}
	if main := h.mainLog(); !strings.Contains(main, "B: add b.txt") || strings.Contains(main, "A: add a.txt") {
		t.Errorf("B should merge after A's panic, and A not:\n%s", main)
	}
	if !exists(h.worktree("A")) {
		t.Error("A's worktree should be left for review")
	}
	logged := h.logged()
	for _, s := range []string{"panic in A: runtime error", "panickyMerger.FastForward", want} {
		if !strings.Contains(logged, s) {
			t.Errorf("log lacks %q:\n%s", s, logged)
		}
	}
}

// panickyNotes panics writing triage's notes on ticket id.
type panickyNotes struct {
	*fakeBeads
	id string
}

func (n panickyNotes) AppendNotes(ctx context.Context, id, note string) error {
	if id == n.id {
		var m map[string]string
		m[id] = note
	}
	return n.fakeBeads.AppendNotes(ctx, id, note)
}

// A panic in triage fails that ticket's triage, and triage goes on with the next.
func TestTriagePanicOnlyFailsTriage(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	log, err := OpenLog(logPath, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordSink{}
	o := &Loop{cfg: Config{LogPath: logPath}, log: log, sink: sink, notes: panickyNotes{newFakeBeads(), "A"},
		organ: organ.Client{Bin: fakeTriage(t)}, organCtx: context.Background()}
	o.StartTriage()
	o.queueTriage(context.Background(), organ.Deferral{ID: "A"})
	o.queueTriage(context.Background(), organ.Deferral{ID: "B"})
	o.FinishTriage(context.Background())

	got := sink.text()
	want := "  TRIAGE_FAILED for A: panic: assignment to entry in nil map (the stack is in " + logPath + ")\n"
	if !strings.Contains(got, want) || !strings.Contains(got, "  triage B: environment (high confidence)") {
		t.Errorf("A's triage should fail with the panic and B's go on; events:\n%s", got)
	}
	if logged := read(t, logPath); !strings.Contains(logged, "panic in triage of A") || !strings.Contains(logged, "panickyNotes.AppendNotes") {
		t.Errorf("log lacks the stack:\n%s", logged)
	}
}

// panickyAgents panics reading a worker's status.
type panickyAgents struct{ noAgents }

func (panickyAgents) Status(ctx context.Context, name string) (string, error) {
	var l []string
	return l[0], nil
}

// A panic watching a worker stops only the watching.
func TestWatcherPanicOnlyStopsWatching(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	log, err := OpenLog(logPath, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordSink{}
	o := &Loop{cfg: Config{LogPath: logPath}, log: log, sink: sink, agents: panickyAgents{}}
	stop := o.watch(context.Background(), o.newWatcher("wt", Status{Ticket: "A"}))
	stop() // returns: the watcher ended
	if got := sink.text(); !strings.Contains(got, "  WATCH_FAILED for A: panic: runtime error: index out of range") {
		t.Errorf("events:\n%s", got)
	}
}
