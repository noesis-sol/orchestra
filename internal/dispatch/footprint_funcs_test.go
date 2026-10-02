package dispatch

import (
	"slices"
	"testing"
)

// Go's keywords and builtins, and calls into standard packages, are not functions a ticket works on:
// two tickets naming func() or synctest.Wait() don't overlap for it.
func TestTicketFootprintLeavesOutKeywordsAndStdCalls(t *testing.T) {
	cases := []struct {
		name, text string
		want       []string
	}{
		// orchestra-5xg's footprint was Test, Wait, func, orDefault, ….
		{"orchestra-5xg", "`synctest.Test(t, func(t *testing.T){…})` runs a test in a bubble; use synctest.Wait() " +
			"instead of `eventually(…)`, and drop the 13 `orDefault(o.wait…)` calls. Run them with func().",
			[]string{"eventually", "orDefault"}},
		{"keywords", "Wrap it in go func() and defer func(); `return f(x)`, `if (ok)`, `switch(v)`, `select()`.", []string{"f"}},
		{"builtins", "Grow it with append(), check len() and cap(), make() the map, new() the struct, then close() and " +
			"delete(), panic() and recover(), copy(), clear(), min() and max(), print() and println().", nil},
		{"builtins in code", "`n := len(l)`, `ch := make(chan int, cap(q))`, `l = append(l, x)`, `max(a, b)`.", nil},
		{"conversions", "`string(out)`, `[]byte(s)`, `int64(n)`, `error(nil)`, `any(v)`.", nil},
		{"standard packages", "`atomic.AddInt64(&n, 1)`, signal.Notify(), `maps.Keys(m)`, cmp.Or(), " +
			"`utf8.ValidString(s)`, `unicode/utf8.RuneStart(b)`, `math/rand.IntN(3)`, slog.Info(), log.Printf(), " +
			"`runtime.Gosched()`, `debug.Stack()`, `url.Parse(u)`, `hex.EncodeToString(b)`, `sha256.Sum256(b)`, " +
			"testing.Short(), `exec.CommandContext(ctx, name)`, `http.NewRequest(m, u, nil)`, iter.Pull().", nil},
		{"a method named like a builtin", "Have `o.close()` and l.len() say so.", []string{"close", "len"}},
		{"a type's method named like a builtin", "Fix Queue.len() and `Pool.close()`.", []string{"Pool.close", "Queue.len"}},
		{"real names", "Loop.merge waits in `waitSettled`, then calls `o.agentName(id)` and refreshBranch().",
			[]string{"Loop.merge", "agentName", "refreshBranch", "waitSettled"}},
		{"real names named like a package's", "TestLiveOrgans() and `Test(t)` and Wait() stay: only synctest's go.",
			[]string{"Test", "TestLiveOrgans", "Wait"}},
	}
	for _, c := range cases {
		fp := TicketFootprint(Ticket{ID: "x", Description: c.text}, trackedHere, "")
		if !slices.Equal(fp.Funcs, c.want) {
			t.Errorf("%s: functions %q, want %q", c.name, fp.Funcs, c.want)
		}
	}
}

// The acceptance case: func(), synctest.Test() and synctest.Wait() leave the real names alone.
func TestTicketFootprintKeepsRealNamesBesideKeywords(t *testing.T) {
	tk := Ticket{ID: "x", Title: "Settle Loop.merge in a bubble",
		Description: "Run it in synctest.Test() as `synctest.Test(t, func(t *testing.T){…})` and call synctest.Wait() " +
			"where `waitSettled` polled; func() literals stay. Loop.merge still calls `o.agentName(id)`."}
	fp := TicketFootprint(tk, trackedHere, "")
	if want := []string{"Loop.merge", "agentName", "waitSettled"}; !slices.Equal(fp.Funcs, want) {
		t.Errorf("functions %q, want %q", fp.Funcs, want)
	}
	other := TicketFootprint(Ticket{ID: "y", Description: "Wrap the probe in go func() and synctest.Wait() on it, " +
		"then `pickNext`."}, trackedHere, "")
	if what := shared(other, fp, nil); what != "" {
		t.Errorf("unrelated tickets share %q", what)
	}
}
