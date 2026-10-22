package dispatch

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// One rule says whether a ticket has merged (pendingWhy): the ready rule, epicDone and the scope's
// end all ask it of a subticket, whatever the run did with it.

// TestEachDispositionAgainstTheMergeRule sets epic E's subticket B in each disposition the run keeps,
// with E's other subticket A merged, and checks that B holds E (the ready rule's parents), the
// ticket X it blocks once bd counts B done, and E's close, and that the scope's end names it, until
// B has merged. B set aside with its worker left in its tab, which then closes it, has not merged
// either (orchestra-4n6j.4): bd no longer lists it, but its branch is not on Base.
func TestEachDispositionAgainstTheMergeRule(t *testing.T) {
	t.Parallel()
	lo := leftOpen{w: worker{workerRef: workerRef{tab: "tab7"}, id: "B"}}
	for _, tc := range []struct {
		name    string
		status  TicketStatus
		running bool // its worker runs, or its work merges
		set     func(o *Loop, g *fakeGit)
		why     string // why the scope's end says B is not done; "" once it has merged
	}{
		{"running, closed by its worker", StatusClosed, true, func(*Loop, *fakeGit) {},
			"closed but not merged: " + pendingBusy},
		{"running, not closed yet", StatusInProgress, true, func(*Loop, *fakeGit) {}, "in progress"},
		{"asked, closed in its tab", StatusClosed, false,
			func(o *Loop, _ *fakeGit) { o.book.ask("B", workerSpot{workerRef: workerRef{tab: "tab7"}}) },
			"closed but not merged: " + pendingBusy},
		{"unmerged", StatusClosed, false,
			func(o *Loop, _ *fakeGit) { o.book.leaveUnmerged("B", string(asideChecksFailed)) },
			"closed but not merged: " + string(asideChecksFailed)},
		{"left open, closed in its tab", StatusClosed, false,
			func(o *Loop, _ *fakeGit) { o.book.setAside("B"); o.book.leaveOpen("B", lo) },
			"closed but not merged: its worker was left in tab tab7"},
		{"left open, not closed", StatusInProgress, false,
			func(o *Loop, _ *fakeGit) { o.book.setAside("B"); o.book.leaveOpen("B", lo) },
			"its worker was left in tab tab7"},
		{"deferred", StatusDeferred, false, func(*Loop, *fakeGit) {}, "deferred"},
		{"set aside", StatusOpen, false, func(o *Loop, _ *fakeGit) { o.book.setAside("B") }, "set aside in this run"},
		{"closed, its work only on its branch", StatusClosed, false, func(_ *Loop, g *fakeGit) {
			cutBranch(g, "wt/B")
			g.commit("wt/B", "B: add b.txt", "b.txt")
		}, "closed but not merged: wt/B is not on main"},
		{"merged", StatusClosed, false, func(o *Loop, _ *fakeGit) { o.merged(context.Background(), "B") }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			b := newFakeBeads()
			b.epic("E", "")
			b.sub("A", "E", "merged", 1)
			b.set("A", StatusClosed)
			b.sub("B", "E", "under test", 1)
			b.set("B", tc.status)
			b.add("X", "blocked by B", 1)
			b.link("X", "B", "blocks")
			g := newFakeGit("main")
			log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
			if err != nil {
				t.Fatal(err)
			}
			c := Config{Repo: t.TempDir(), Base: "main", Ticket: "E", ExcludeTypes: []string{"epic"}}
			o := New(c, log, "", Deps{Tickets: b, Notes: b, Checkout: g, Worktrees: g, Merger: g})
			o.SetSink(&recordSink{})
			o.book.setParent("A", "E")
			o.book.setParent("B", "E")
			running := map[string]bool{}
			if tc.running {
				running["B"] = true
				o.setActive(Status{Ticket: "B"})
			}
			tc.set(o, g)
			pending := tc.why != ""

			// The ready rule: E waits for its subtickets, and X, once bd counts B done, for B to merge.
			parents, err := o.openParents(ctx, running)
			if err != nil {
				t.Fatal(err)
			}
			want := notHeld
			if pending {
				want = heldParent
			}
			if got := o.held(ctx, Ticket{ID: "E"}, running, parents); got != want {
				t.Errorf("E held %v, want %v (parents %v)", got, want, parents)
			}
			if tc.status == StatusClosed {
				want = notHeld
				if pending {
					want = heldBlocked
				}
				if got := o.held(ctx, Ticket{ID: "X"}, running, parents); got != want {
					t.Errorf("X held %v, want %v", got, want)
				}
			}

			// The scope's end names B, and why, until it has merged.
			subs, err := b.Descendants(ctx, "E")
			end := o.scopeEnd(ctx, subs, err)
			switch {
			case pending && !strings.Contains(end, "SCOPE_OPEN: E: 1 of its 2 subtickets not done: B ("+tc.why+")"):
				t.Errorf("scope end %q, want B (%s) not done", end, tc.why)
			case !pending && !strings.HasPrefix(end, "; SCOPE_DONE: E's 2 subtickets are merged"):
				t.Errorf("scope end %q, want SCOPE_DONE", end)
			}

			// epicDone closes E once B has merged, and not before.
			o.epicDone(ctx, "E", "A", running)
			if closed := b.tickets["E"].Status == StatusClosed; closed == pending {
				t.Errorf("E closed %v after epicDone, want %v", closed, !pending)
			}
		})
	}
}
