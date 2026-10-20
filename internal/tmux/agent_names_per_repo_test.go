package tmux

import (
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/agentio"
)

// Two runs on one tmux server, in two repositories whose tickets share IDs (two clones sharing a
// Beads database), each find, name and prompt only their own workers.

func TestAgentNamesArePerRepository(t *testing.T) {
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%1", agent: "x-1", kind: "claude", records: workingRecords(t), repo: "/a"},
		paneLine{id: "%2", agent: "x-1", kind: "claude", records: idleRecords(t), repo: "/b"},
		paneLine{id: "%3", agent: "x-1-1", kind: "claude", records: idleRecords(t), repo: "/a"})
	for repo, want := range map[string]agentio.State{
		"/a": agentio.StateWorking, "/b": agentio.StateIdle, "/c": agentio.StateGone,
	} {
		if st, err := (Terminal{Repo: repo}).Status(bg, "x-1"); st != want || err != nil {
			t.Errorf("Status in %s = %q %v, want %q", repo, st, err, want)
		}
	}
	if got := (Terminal{Repo: "/b"}).FreeName(bg, "x-1"); got != "x-1-1" {
		t.Errorf("FreeName counted another repository's names: %q", got)
	}

	// Naming a worker takes the name from no other repository's pane, and marks the pane with its own.
	f.listPanes(paneLine{id: "%1", agent: "x-1", kind: "claude", repo: "/a"},
		paneLine{id: "%2", agent: "x-1", kind: "claude", repo: "/b"}, paneLine{id: "%4", kind: "claude"})
	if _, err := (Terminal{Repo: "/b"}).AdoptAgent(bg, "%4", "claude", "x-1"); err != nil {
		t.Fatal(err)
	}
	want := []string{"set-option", "-p", "-u", "-t", "%2", "@orchestra-agent", ";",
		"set-option", "-p", "-t", "%4", "@orchestra-agent", "x-1", ";", "set-option", "-p", "-t", "%4", "@orchestra-repo", "/b"}
	if got := f.call("set-option"); !slices.Equal(got, want) {
		t.Errorf("tmux got\n%q\nwant\n%q", got, want)
	}
}

// A worker named by an orchestra that marked no repository, as an older one left running, is still
// found, but after one named for the repository, and naming another takes its name.
func TestAgentNamedWithoutARepository(t *testing.T) {
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%1", agent: "x-1", kind: "claude", records: idleRecords(t)})
	if st, err := (Terminal{Repo: "/a"}).Status(bg, "x-1"); st != agentio.StateIdle || err != nil {
		t.Errorf("Status = %q %v", st, err)
	}
	f.listPanes(paneLine{id: "%1", agent: "x-1", kind: "claude", records: idleRecords(t)},
		paneLine{id: "%2", agent: "x-1", kind: "claude", records: workingRecords(t), repo: "/a"})
	if st, err := (Terminal{Repo: "/a"}).Status(bg, "x-1"); st != agentio.StateWorking || err != nil {
		t.Errorf("Status with a pane named for the repository = %q %v", st, err)
	}

	f.listPanes(paneLine{id: "%1", agent: "x-1", kind: "claude"}, paneLine{id: "%2", kind: "claude"})
	if err := (Terminal{Repo: "/a"}).RenameAgent(bg, "%2", "x-1"); err != nil {
		t.Fatal(err)
	}
	if got := f.call("set-option"); !slices.Contains(got, "%1") {
		t.Errorf("the unmarked pane kept the name: %q", got)
	}
}

// With real tmux, two runs whose workers have the same name each find, prompt and read only their own.
func TestLiveAgentNamesArePerRepository(t *testing.T) {
	l := newLive(t)
	l.script(false, "start", "prompt", "idle")
	a, b := Terminal{Socket: l.term.Socket, Repo: "/repo/a"}, Terminal{Socket: l.term.Socket, Repo: "/repo/b"}
	_, paneA := l.tab("x-1")
	_, paneB := l.tab("x-1")
	if err := a.StartAgent(bg, "x-1", "claude", paneA, l.hooks()); err != nil {
		t.Fatal(err)
	}
	if err := b.StartAgent(bg, "x-1", "claude", paneB, l.hooks()); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		term Terminal
		pane string
	}{{a, paneA}, {b, paneB}} {
		if st, err := c.term.Status(bg, "x-1"); st != agentio.StateIdle || err != nil {
			t.Errorf("%s's worker: %q %v", c.term.Repo, st, err)
		}
		if n, _, _, err := c.term.PaneAgent(bg, c.pane); n != "x-1" || err != nil {
			t.Errorf("%s's pane is named %q (%v)", c.term.Repo, n, err)
		}
	}

	if err := b.Prompt(bg, "x-1", "Work on x-1."); err != nil {
		t.Fatal(err)
	}
	if st, err := a.Status(bg, "x-1"); st != agentio.StateIdle || err != nil {
		t.Errorf("the other repository's prompt reached /repo/a's worker: %q %v", st, err)
	}
	if st, err := b.Status(bg, "x-1"); st != agentio.StateWorking || err != nil {
		t.Errorf("/repo/b's worker on its prompt: %q %v", st, err)
	}
	if got := (Terminal{Socket: l.term.Socket, Repo: "/repo/c"}).Screen(bg, "x-1", ""); got != "" {
		t.Errorf("a third repository read a worker's screen: %q", got)
	}
}
