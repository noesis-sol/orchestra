package dispatch

import (
	"context"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// The commit naming a ticket, which the line saying it merged names, is the one finish read on its
// branch while the branch's head is that commit still: git is asked for it again only once a rebase
// gave the branch a new head, or the worker's last commit doesn't name the ticket.

// closedDetail is the dashboard's detail of the line saying ticket id closed, or "".
func closedDetail(h *harness, id string) string {
	h.sink.mu.Lock()
	defer h.sink.mu.Unlock()
	for _, ev := range h.sink.events {
		if ev.Kind == EvClosed && ev.Ticket == id {
			return ev.Detail
		}
	}
	return ""
}

// commitsNamingThenNot claims the ticket, commits file in a commit naming it and then other in one
// that doesn't, and closes it.
func commitsNamingThenNot(file, other string) behaviour {
	return func(w *fakeWorker) AgentState {
		w.claim()
		w.commit(file)
		w.commitAs(other, "add "+other)
		w.close()
		return "idle"
	}
}

// A merges as its worker left it, and B, which started beside it, rebased onto A's merge: git is asked
// for the commit naming B again, as rebased, and not for A's. Each line names the commit merged.
func TestAMergeNamesTheCommitFinishReadUnlessARebaseMovedTheBranch(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", finishes("a.txt"))
		h.worker("B", slowly(time.Minute, "b.txt"))
		g := &askedGit{fakeGit: h.mem}
		o := h.loop()
		o.checkout, o.worktrees, o.merger = g, g, g
		if code := o.Run(context.Background()); code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		var named []string
		for _, a := range g.asked {
			if strings.HasPrefix(a, "CommitNaming ") {
				named = append(named, a)
			}
		}
		if !slices.Equal(named, []string{"CommitNaming main..wt/B"}) {
			t.Errorf("git was asked %q for the commits naming the tickets, want B's alone", named)
		}
		log := strings.Split(strings.TrimSpace(h.mem.log("main")), "\n")
		if len(log) < 2 || !strings.HasSuffix(log[0], " B: add b.txt") || !strings.HasSuffix(log[1], " A: add a.txt") {
			t.Fatalf("main is\n%s", strings.Join(log, "\n"))
		}
		for id, commit := range map[string]string{"A": log[1], "B": log[0]} {
			hash, _, _ := strings.Cut(commit, " ")
			if want := hash + " merged into main"; closedDetail(h, id) != want {
				t.Errorf("%s closed as %q, want %q", id, closedDetail(h, id), want)
			}
		}
	})
}

// A's worker commits after the commit naming A: the branch's head isn't the commit finish read, so git
// is asked which commit names A, and finds the same one.
func TestAMergeAsksForTheCommitNamingTheTicketWhenTheLastDoesNot(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.worker("A", commitsNamingThenNot("a.txt", "z.txt"))
		g := &askedGit{fakeGit: h.mem}
		o := h.loop()
		o.checkout, o.worktrees, o.merger = g, g, g
		if code := o.Run(context.Background()); code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if !slices.Contains(g.asked, "CommitNaming main..wt/A") {
			t.Errorf("git was not asked which commit names A:\n%q", g.asked)
		}
		log := strings.Split(strings.TrimSpace(h.mem.log("main")), "\n")
		if len(log) < 2 || !strings.HasSuffix(log[1], " A: add a.txt") {
			t.Fatalf("main is\n%s", strings.Join(log, "\n"))
		}
		hash, _, _ := strings.Cut(log[1], " ")
		if want := hash + " merged into main"; closedDetail(h, "A") != want {
			t.Errorf("A closed as %q, want %q", closedDetail(h, "A"), want)
		}
	})
}
