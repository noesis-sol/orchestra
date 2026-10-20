package dispatch

import "testing"

// The solo queue keeps which solo ticket runs, is next or first in line, and says why tickets wait
// only when that changes.
func TestSoloQueueUpdate(t *testing.T) {
	t.Parallel()
	const waits = "waiting for solo ticket s to finish"
	const next = "solo ticket n is next: no new tickets start until the running ones finish, then it runs alone"
	a := &Ticket{ID: "a"}
	cases := []struct {
		name      string
		q         soloQueue
		next      string
		slot      bool
		t         *Ticket
		queued    int
		why       string
		want      soloQueue
		wantState SoloState
	}{
		{name: "no solo ticket", slot: true, t: a, queued: 2},
		{name: "solo running, tickets wait", q: soloQueue{running: "s"}, slot: true, queued: 2, why: waits,
			want: soloQueue{running: "s", said: waits}, wantState: SoloState{Ticket: "s"}},
		{name: "solo running, said already", q: soloQueue{running: "s", said: waits}, slot: true, queued: 2,
			want: soloQueue{running: "s", said: waits}, wantState: SoloState{Ticket: "s"}},
		{name: "solo running, nothing ready", q: soloQueue{running: "s", said: waits}, slot: true,
			want: soloQueue{running: "s"}, wantState: SoloState{Ticket: "s"}},
		{name: "solo running, no slot", q: soloQueue{running: "s"}, queued: 2,
			want: soloQueue{running: "s"}, wantState: SoloState{Ticket: "s"}},
		{name: "solo next with a slot", next: "n", slot: true, queued: 3, why: next,
			want: soloQueue{next: "n", first: "n", said: next}, wantState: SoloState{Ticket: "n", Next: true}},
		{name: "solo next, said already", q: soloQueue{next: "n", first: "n", said: next}, next: "n", slot: true,
			queued: 3, want: soloQueue{next: "n", first: "n", said: next}, wantState: SoloState{Ticket: "n", Next: true}},
		{name: "solo first without a slot", next: "n", queued: 3, want: soloQueue{first: "n"}},
		{name: "solo started", q: soloQueue{running: "n", next: "n", first: "n", said: next}, slot: true,
			want: soloQueue{running: "n"}, wantState: SoloState{Ticket: "n"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := c.q
			if why := q.update(c.next, c.slot, c.t, c.queued); why != c.why {
				t.Errorf("why: got %q, want %q", why, c.why)
			}
			if q != c.want {
				t.Errorf("queue: got %+v, want %+v", q, c.want)
			}
			if s := q.state(); s != c.wantState {
				t.Errorf("state: got %+v, want %+v", s, c.wantState)
			}
		})
	}
}
