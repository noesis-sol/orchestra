package dispatch

import (
	"testing"
	"time"
)

// A spec run's start record names its spec, and its scope is shown by the spec, its root epic named
// on a line of its own.
func TestASpecRunIsNamedByItsSpec(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	l, _ := openLog(t)
	l.Begin(repo, RunStart{Started: time.Now(), Scope: "s-1", Spec: ".orchestra/specs/app.md", Concurrency: 1})
	l.End(ExitOK)
	has(t, records(t, repo)[0], map[string]any{"scope": "s-1", "spec": ".orchestra/specs/app.md"}, "feature")

	c := Config{Ticket: "s-1", Spec: ".orchestra/specs/app.md"}
	if got := ScopeLabel(c); got != " · spec .orchestra/specs/app.md" {
		t.Errorf("scope label %q", got)
	}
}

// A run with no ticket limit counts its tickets without one: [3], not [3/40], and its dispatch
// records give no limit.
func TestARunWithNoTicketLimit(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	l, _ := openLog(t)
	l.Begin(repo, RunStart{Started: time.Now(), Concurrency: 1})
	l.Record(Event{Kind: EvDispatch, Time: time.Now(), N: 3, Limit: NoLimit, Ticket: "s-1.2"})
	l.Record(Event{Kind: EvDispatch, Time: time.Now(), N: 4, Limit: 40, Ticket: "s-1.3"})
	l.End(ExitOK)
	recs := records(t, repo)
	has(t, recs[1], map[string]any{"n": 3.0}, "limit")
	has(t, recs[2], map[string]any{"n": 4.0, "limit": 40.0})
	for _, tc := range []struct{ limit, want string }{
		{LimitLabel(NoLimit), "none"}, {LimitLabel(40), "40"}, {Progress(3, NoLimit), "3"}, {Progress(3, 40), "3/40"},
	} {
		if tc.limit != tc.want {
			t.Errorf("got %q, want %q", tc.limit, tc.want)
		}
	}
}
