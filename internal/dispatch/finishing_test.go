package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// finishingSeen is git, recording what the loop says is finishing as a ticket's worktree is made
// and as its branch is fast-forwarded.
type finishingSeen struct {
	*fakeGit
	o    **Loop
	seen map[string][]Finishing
}

func (g finishingSeen) NewWorktree(ctx context.Context, repo, path, branch, base string) (string, error) {
	g.seen["worktree"] = (*g.o).Finishing()
	return g.fakeGit.NewWorktree(ctx, repo, path, branch, base)
}

func (g finishingSeen) FastForward(ctx context.Context, repo, branch string) (string, error) {
	g.seen["merge"] = (*g.o).Finishing()
	return g.fakeGit.FastForward(ctx, repo, branch)
}

// Finishing names what a worker is doing that a stop doesn't cut short, while it does it: making
// the ticket's worktree, then merging it. Nothing is left marked once the run is over.
func TestFinishingNamesWhatAStopDoesNotCutShort(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		var o *Loop
		g := finishingSeen{fakeGit: h.mem, o: &o, seen: map[string][]Finishing{}}
		o = h.loop()
		o.worktrees, o.merger = g, g
		if code := o.Run(context.Background()); code != ExitOK {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		for what, want := range map[string]Finishing{"worktree": {"A", "worktree setup"}, "merge": {"A", "merge"}} {
			if got := g.seen[what]; len(got) != 1 || got[0] != want {
				t.Errorf("during the %s: %v, want %v", what, got, want)
			}
		}
		if got := o.Finishing(); len(got) != 0 {
			t.Errorf("after the run: %v", got)
		}
	})
}

// Several at once come by ticket.
func TestFinishingIsByTicket(t *testing.T) {
	o := &Loop{}
	defer o.markFinishing("b-2", finishMerge)()
	unmark := o.markFinishing("a-1", finishWorktree)
	got := o.Finishing()
	if len(got) != 2 || got[0] != (Finishing{"a-1", "worktree setup"}) || got[1] != (Finishing{"b-2", "merge"}) {
		t.Errorf("finishing: %v", got)
	}
	unmark()
	if got := o.Finishing(); len(got) != 1 || got[0].Ticket != "b-2" {
		t.Errorf("after a-1's cleared: %v", got)
	}
}

func TestUnderWayLineSaysHowToAbandonIt(t *testing.T) {
	for _, tc := range []struct {
		under []Finishing
		again string
		want  string
	}{
		{[]Finishing{{"a-1", "merge"}}, "press Ctrl+C again",
			"  a-1's merge is under way; press Ctrl+C again to abandon it (the repository may be left half merged)"},
		{[]Finishing{{"a-1", "worktree setup"}}, "send SIGTERM again",
			"  a-1's worktree setup is under way; send SIGTERM again to abandon it (its worktree may be left half made)"},
		{[]Finishing{{"a-1", "worktree setup"}, {"a-2", "merge"}}, "press Ctrl+C again",
			"  a-1's worktree setup and a-2's merge are under way; press Ctrl+C again to abandon them " +
				"(the repository may be left half merged)"},
	} {
		if got := UnderWayLine(tc.under, tc.again); got != tc.want {
			t.Errorf("%v:\n got %q\nwant %q", tc.under, got, tc.want)
		}
	}
}

func TestQuitLineNamesWhatIsLeftAndAbandoned(t *testing.T) {
	a1, a2 := Status{Ticket: "a-1", Tab: "w1:2"}, Status{Ticket: "a-2", Tab: "w1:3"}
	for _, tc := range []struct {
		why       string
		running   []Status
		abandoned []Finishing
		want      string
	}{
		{"with Ctrl+C", nil, nil, "INTERRUPTED: quit at once with Ctrl+C"},
		{"by SIGTERM", []Status{a1, a2}, nil,
			"INTERRUPTED: quit at once by SIGTERM, leaving a-1 (tab w1:2), a-2 (tab w1:3) running " +
				"with their tabs and worktrees open"},
		{"with Ctrl+C", []Status{a1}, []Finishing{{"a-1", "merge"}},
			"INTERRUPTED: quit at once with Ctrl+C, leaving a-1 (tab w1:2) running with its tab and worktree open; " +
				"abandoned a-1's merge: git may still finish it, so check git status before starting another run"},
	} {
		if got := QuitLine(tc.why, tc.running, tc.abandoned); got != tc.want {
			t.Errorf("%s, %v, %v:\n got %q\nwant %q", tc.why, tc.running, tc.abandoned, got, tc.want)
		}
	}
}

// CloseNow closes the log without waiting for a notification still being shown, which would hold
// up a quit; what was logged is in the file.
func TestCloseNowDoesNotWaitForNotifications(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orchestra.log")
	l, err := OpenLog(path, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	defer close(release)
	l.alert = l.inBackground(func([]string) { <-release })
	l.Line(time.Now(), "INTERRUPTED: stopped with Ctrl+C")
	l.Notify("Stopped: INTERRUPTED")
	closed := make(chan error)
	go func() { closed <- l.CloseNow() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CloseNow waited for the notification")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "INTERRUPTED: stopped with Ctrl+C") {
		t.Errorf("log:\n%s", b)
	}
}
