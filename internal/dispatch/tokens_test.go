package dispatch

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// orchestra-rie6.3: the record that ends a ticket's part in the run gives what its workers used, by
// model, and the run's end record the run's totals.

// tokensReporter is the hooks of Claude workers whose hooks noted sessions, by worktree, and the tokens
// each session's transcript says it used, by transcript; a transcript it has none for can't be read.
type tokensReporter struct {
	fakeReporter
	sessions map[string][]Session
	used     map[string]Tokens

	mu    sync.Mutex
	since []time.Time // what each call of Tokens counted from
}

func (r *tokensReporter) HookRecord(wt string) HookRecord {
	return HookRecord{Sessions: r.sessions[wt]}
}

func (r *tokensReporter) Tokens(sessions []Session, since time.Time) (Tokens, []error) {
	r.mu.Lock()
	r.since = append(r.since, since)
	r.mu.Unlock()
	var t Tokens
	var errs []error
	for _, s := range sessions {
		u, ok := r.used[s.Transcript]
		if !ok {
			errs = append(errs, fmt.Errorf("open %s: no such file or directory", s.Transcript))
			continue
		}
		t = t.Add(u)
	}
	return t, errs
}

// session is a session of the test's, its transcript named after it.
func session(id string) Session {
	return Session{ID: id, Transcript: "/home/u/.claude/projects/-w/" + id + ".jsonl"}
}

// tokensOn is record r's tokens, by model, failing the test unless it has them.
func tokensOn(t *testing.T, r map[string]any) map[string]any {
	t.Helper()
	tk, ok := r["tokens"].(map[string]any)
	if !ok {
		t.Fatalf("%s record without tokens: %v", r["kind"], r)
	}
	return tk
}

// model is what record tokens say was used of one model, as the stream gives it.
func model(in, out, write, read float64) map[string]any {
	return map[string]any{"input": in, "output": out, "cache_write": write, "cache_read": read}
}

// A ticket's closed record gives what its worker's sessions used, both of them, by model; a ticket
// whose transcript can't be read has no tokens, which the log says once; the end record sums the
// tickets'.
func TestClosedRecordsGiveTheirWorkersTokens(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Check = "true"
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", sleeps(time.Minute, finishes("a.txt")))
		h.worker("B", sleeps(time.Minute, finishes("b.txt")))
		a1, a2, b := session("a1"), session("a2"), session("b")
		rep := &tokensReporter{
			sessions: map[string][]Session{h.worktree("A"): {a1, a2, a1}, h.worktree("B"): {b}},
			used: map[string]Tokens{
				a1.Transcript: {"claude-opus-5-5": {Input: 10, Output: 200, CacheWrite: 3000, CacheRead: 40000}},
				a2.Transcript: {"claude-opus-5-5": {Input: 1, Output: 2, CacheWrite: 3, CacheRead: 4},
					"claude-haiku-5-5": {Input: 5, Output: 6, CacheWrite: 7, CacheRead: 8}},
			},
		}
		h.reporter = rep
		started := time.Now()
		o, code, recs := streamedRun(t, h)
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}

		got := tokensOn(t, ending(t, recs, "A"))
		want := map[string]any{"claude-opus-5-5": model(11, 202, 3003, 40004), "claude-haiku-5-5": model(5, 6, 7, 8)}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("A's tokens %v, want %v", got, want)
		}
		has(t, ending(t, recs, "B"), map[string]any{"kind": "closed"}, "tokens")
		for _, r := range recs {
			if r["kind"] != "closed" && r["kind"] != "end" {
				has(t, r, nil, "tokens")
			}
		}
		end := recs[len(recs)-1]
		has(t, end, map[string]any{"kind": "end"}, "organs")
		if got := tokensOn(t, end); !reflect.DeepEqual(got, want) {
			t.Errorf("the end's tokens %v, want A's, %v", got, want)
		}

		log := h.logged()
		if n := strings.Count(log, b.Transcript); n != 1 {
			t.Errorf("the log names B's unread transcript %d times, want once:\n%s", n, log)
		}
		if !strings.Contains(log, "the tokens B's workers used leave out a transcript that can't be read") {
			t.Errorf("the log doesn't say B's tokens leave out its transcript:\n%s", log)
		}
		rep.mu.Lock()
		defer rep.mu.Unlock()
		for _, since := range rep.since {
			if since.Before(started) {
				t.Errorf("tokens counted from %v, before the run started at %v", since, started)
			}
		}
	})
}

// The end record sums the tickets' tokens by model, and gives what the run's organ calls cost.
func TestTheEndRecordGivesTheRunsTotals(t *testing.T) {
	repo := t.TempDir()
	l, err := OpenLog(filepath.Join(repo, "orchestra.log"), false, "repo")
	if err != nil {
		t.Fatal(err)
	}
	l.Begin(repo, RunStart{Started: time.Now(), Repo: repo, Branch: "main", Concurrency: 2})
	l.Record(Event{Kind: EvClosed, Ticket: "A", Tokens: Tokens{"claude-opus-5-5": {Input: 1, Output: 2, CacheWrite: 3,
		CacheRead: 4}}})
	l.Record(Event{Kind: EvDeferred, Ticket: "B", Tokens: Tokens{"claude-opus-5-5": {Input: 10, Output: 20},
		"claude-haiku-5-5": {CacheRead: 9}}})
	l.Record(Event{Kind: EvClosed, Ticket: "C"})
	l.OrganSpent(organ.Spend{Organ: "triage", CostUSD: 0.25, Turns: 1})
	l.OrganSpent(organ.Spend{Organ: "review", CostUSD: 0.5, Turns: 3})
	l.End(ExitOK)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	recs := records(t, repo)
	end := recs[len(recs)-1]
	want := map[string]any{"claude-opus-5-5": model(11, 22, 3, 4), "claude-haiku-5-5": model(0, 0, 0, 9)}
	if got := tokensOn(t, end); !reflect.DeepEqual(got, want) {
		t.Errorf("the end's tokens %v, want %v", got, want)
	}
	if got, want := end["organs"], map[string]any{"calls": 2.0, "turns": 4.0, "cost_usd": 0.75}; !reflect.DeepEqual(got,
		want) {
		t.Errorf("the end's organs %v, want %v", got, want)
	}
	has(t, recs[len(recs)-2], map[string]any{"ticket": "C"}, "tokens")
}
