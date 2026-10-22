package dispatch

import "testing"

// TestSetAsideCountReadsTheBook moves a ticket aside and back through each of the book's transitions,
// emitting no event for any of them, and checks after each step how many tickets the run's EvDone
// says it set aside.
func TestSetAsideCountReadsTheBook(t *testing.T) {
	t.Parallel()
	type step struct {
		name string
		do   func(b *ticketBook)
		want int
	}
	spot := workerSpot{workerRef: refOf("A"), question: "Q-1"}
	lo := leftOpen{w: worker{workerRef: refOf("A"), id: "A"}}
	for _, path := range []struct {
		name  string
		steps []step
	}{
		{"deferred, then back in the queue", []step{
			{"set aside", func(b *ticketBook) { b.setAside("A") }, 1},
			{"set aside again", func(b *ticketBook) { b.setAside("A") }, 1},
			{"back in the queue", func(b *ticketBook) { b.requeue("A") }, 0},
		}},
		{"deferred, dispatched again, merged", []step{
			{"set aside", func(b *ticketBook) { b.setAside("A") }, 1},
			{"running again", func(b *ticketBook) { b.answer("A") }, 1},
			{"merged", func(b *ticketBook) { b.merged("A") }, 0},
		}},
		{"asked, answered, deferred in its tab and left open", []step{
			{"asked: waits on the maintainer", func(b *ticketBook) { b.ask("A", spot) }, 0},
			{"answered: running, set aside since it asked", func(b *ticketBook) { b.answer("A") }, 1},
			{"set aside", func(b *ticketBook) { b.setAside("A") }, 1},
			{"left open", func(b *ticketBook) { b.leaveOpen("A", lo) }, 1},
			{"no longer followed", func(b *ticketBook) { b.unfollow("A") }, 1},
			{"merged", func(b *ticketBook) { b.merged("A") }, 0},
		}},
		{"left open, back in the queue", []step{
			{"left open", func(b *ticketBook) { b.setAside("A"); b.leaveOpen("A", lo) }, 1},
			{"back in the queue, followed still", func(b *ticketBook) { b.requeue("A") }, 0},
			{"dispatched again", func(b *ticketBook) { b.unfollow("A"); b.answer("A") }, 0},
		}},
		{"asked, closed as the run ends", []step{
			{"asked", func(b *ticketBook) { b.ask("A", spot) }, 0},
			{"closed, unmerged: asked still", func(b *ticketBook) { b.leaveUnmerged("A", string(asideAskedUnmerged)) }, 1},
		}},
		{"asked, its worker gone", []step{
			{"asked", func(b *ticketBook) { b.ask("A", spot) }, 0},
			{"no longer followed: set aside", func(b *ticketBook) { b.dropAsked("A") }, 1},
		}},
		{"carried over from the last run", []step{
			{"carried, asked", func(b *ticketBook) { b.carry("A", spot) }, 0},
			{"no longer followed: never set aside", func(b *ticketBook) { b.dropAsked("A") }, 0},
		}},
		{"unmerged, then merged", []step{
			{"left unmerged", func(b *ticketBook) { b.leaveUnmerged("A", string(asideChecksFailed)) }, 1},
			{"merged", func(b *ticketBook) { b.merged("A") }, 0},
		}},
		{"unmerged by an earlier run", []step{
			{"noted unmerged", func(b *ticketBook) { b.noteUnmerged("A", earlierRun) }, 0},
		}},
		{"setup failed after it was set aside", []step{
			{"set aside, running again", func(b *ticketBook) { b.setAside("A"); b.answer("A") }, 1},
			{"setup failed, reopened", func(b *ticketBook) { b.setupFailed("A", failedSetup{output: "setup.log"}) }, 0},
			{"taken again", func(b *ticketBook) { b.retrySetups() }, 0},
		}},
		{"setup failed, bd failing to reopen it", []step{
			{"set aside, running again", func(b *ticketBook) { b.setAside("A"); b.answer("A") }, 1},
			{"setup failed, not reopened", func(b *ticketBook) {
				b.setupFailed("A", failedSetup{output: "setup.log", notReopened: true})
			}, 1},
			{"taken again: set aside still", func(b *ticketBook) { b.retrySetups() }, 1},
		}},
	} {
		t.Run(path.name, func(t *testing.T) {
			t.Parallel()
			var o Loop
			for _, s := range path.steps {
				s.do(&o.book)
				if err := o.book.check(); err != nil {
					t.Fatalf("%s: %v", s.name, err)
				}
				ev := Event{Kind: EvDone}
				o.tally(&ev)
				if ev.SetAside != s.want {
					t.Errorf("%s: %d set aside, want %d", s.name, ev.SetAside, s.want)
				}
			}
		})
	}
}
