package dispatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A run killed with no time to save its workers (SIGKILL, a power cut) still leaves them in its state
// file, as it writes them along the way: from when a worker is placed, and as its ticket goes to merge,
// each as left running, INTERRUPTED. So the next run carries over a ticket it was merging and merges
// it, rather than leave it closed and unmerged. Once the ticket has merged, the run saves it no more.
func TestRunSavesItsWorkersAsItGoes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		state := filepath.Join(h.repo, project.RunPath(project.StateName))
		during := filepath.Join(t.TempDir(), "during-check.json")
		h.cfg.Check = "cp '" + state + "' '" + during + "'"
		h.beads.add("A", "first", 1)
		var working []project.LeftWorker
		h.worker("A", func(w *fakeWorker) AgentState {
			working = saved(t, h)
			w.claim()
			w.commit("a.txt")
			w.close()
			return "idle"
		})
		if o, code := h.run(); code != ExitOK || !equal(closedIDs(h), []string{"A"}) {
			t.Fatalf("exit %d, final %q, merged %v\n%s", code, o.Final(), closedIDs(h), h.sink.text())
		}
		want := []project.LeftWorker{{Ticket: "A", Agent: "A", Tab: "tab1", Pane: "pane1", Worktree: h.worktree("A"),
			Left: string(stopInterrupted)}}
		if !reflect.DeepEqual(working, want) {
			t.Errorf("saved while A's worker worked: %+v\nwant %+v", working, want)
		}
		b, err := os.ReadFile(during)
		if err != nil {
			t.Fatalf("A's check found no state file: %v", err)
		}
		var merging project.RunState
		if err := json.Unmarshal(b, &merging); err != nil {
			t.Fatal(err)
		}
		for i := range merging.Workers {
			merging.Workers[i].Fork = ""
		}
		if !reflect.DeepEqual(merging.Workers, want) {
			t.Errorf("saved while A merged: %+v\nwant %+v", merging.Workers, want)
		}
		if _, err := os.Stat(state); !os.IsNotExist(err) {
			t.Errorf("A merged, the run still saves workers: %v", saved(t, h))
		}
	})
}
