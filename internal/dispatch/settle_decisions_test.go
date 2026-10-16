package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// countingReporter counts the reads of a worker's hooks: its last tool use and its hooks' record.
type countingReporter struct {
	recordReporter
	uses, records int
}

func (r *countingReporter) LastToolUse(wt string) (ToolUse, bool) {
	r.uses++
	return r.recordReporter.LastToolUse(wt)
}

func (r *countingReporter) HookRecord(wt string) HookRecord {
	r.records++
	return r.recordReporter.HookRecord(wt)
}

// The settle loop reads a worker's hooks once a poll, whatever it decides from them: whether it
// waits on a permission prompt, whether its turn ended, how long it has been quiet.
func TestSettleReadsTheHooksOnceAPoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o, a, _ := newSettleLoop(t, "working", "idle", "blocked", "idle")
		hooks := &countingReporter{}
		hooks.use("wt", ToolUse{Event: "PreToolUse", Tool: "Bash"})
		hooks.note("wt", func(r *HookRecord) { r.Subagents = 1 })
		o.reporter = hooks
		start := time.Now()
		w := worker{id: "A", agent: "A", tab: "tab", wt: "wt", started: start, hooks: true}
		if _, stop := o.waitSettled(t.Context(), w, start, nil); stop != nil {
			t.Fatalf("stopped: %s", stop)
		}
		if polls := a.reads - 1; hooks.uses != polls || hooks.records != polls { // none once it is gone
			t.Errorf("read the last tool use %d times and the hooks' record %d times in %d polls, want once a poll",
				hooks.uses, hooks.records, polls)
		}
	})
}

// The hooks of a worker without them read as nothing; a report of a turn before the worker was
// adopted doesn't count, though its time is kept.
func TestHooksOf(t *testing.T) {
	adopted := time.Now()
	rec := HookRecord{Subagents: 1, Noted: adopted}
	cases := []struct {
		name   string
		w      worker
		u      *ToolUse // nil: none reported
		nilRep bool
		want   hooksSeen
	}{
		{"no hooks", worker{wt: "wt"}, &ToolUse{Event: "Stop", At: adopted}, false, hooksSeen{}},
		{"no reporter", worker{wt: "wt", hooks: true}, &ToolUse{Event: "Stop", At: adopted}, true, hooksSeen{}},
		{"nothing reported", worker{wt: "wt", hooks: true}, nil, false, hooksSeen{rec: rec}},
		{"started", worker{wt: "wt", hooks: true}, &ToolUse{Event: "Stop"}, false,
			hooksSeen{use: ToolUse{Event: "Stop"}, ok: true, rec: rec}},
		{"since the adoption", worker{wt: "wt", hooks: true, since: adopted}, &ToolUse{Event: "Stop", At: adopted},
			false, hooksSeen{use: ToolUse{Event: "Stop", At: adopted}, ok: true, rec: rec}},
		{"before the adoption", worker{wt: "wt", hooks: true, since: adopted},
			&ToolUse{Event: "Stop", At: adopted.Add(-time.Second)}, false,
			hooksSeen{use: ToolUse{Event: "Stop", At: adopted.Add(-time.Second)}, rec: rec}},
		{"of unknown time, adopted", worker{wt: "wt", hooks: true, since: adopted}, &ToolUse{Event: "Stop"}, false,
			hooksSeen{use: ToolUse{Event: "Stop"}, rec: rec}},
	}
	for _, c := range cases {
		r := &recordReporter{records: map[string]HookRecord{"wt": rec}}
		if c.u != nil {
			r.last = map[string]ToolUse{"wt": *c.u}
		}
		o := New(Config{}, nil, "", Deps{Reporter: r})
		if c.nilRep {
			o.reporter = nil
		}
		got := o.hooksOf(c.w)
		if got.ok != c.want.ok || got.use != c.want.use || got.rec.Subagents != c.want.rec.Subagents ||
			!got.rec.Noted.Equal(c.want.rec.Noted) {
			t.Errorf("%s: hooksOf = %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestTurnEnded(t *testing.T) {
	now := time.Now()
	stop := now.Add(-time.Minute)
	cases := []struct {
		name     string
		h        hooksSeen
		at       time.Time
		ended    bool
		replaced bool
	}{
		{"nothing reported", hooksSeen{}, time.Time{}, false, false},
		{"of a turn before the adoption", hooksSeen{use: ToolUse{Event: "Stop", At: stop}}, time.Time{}, false, false},
		{"Stop", hooksSeen{use: ToolUse{Event: "Stop", At: stop}, ok: true}, stop, true, false},
		{"Stop of unknown time", hooksSeen{use: ToolUse{Event: "Stop"}, ok: true}, time.Time{}, true, false},
		{"replaced Stop", hooksSeen{use: ToolUse{Event: "PreToolUse", At: stop.Add(time.Second), Stopped: stop},
			ok: true}, stop, true, true},
		{"PreToolUse before the Stop", hooksSeen{use: ToolUse{Event: "PreToolUse", At: stop.Add(-time.Second),
			Stopped: stop}, ok: true}, time.Time{}, false, false},
		{"PreToolUse of unknown time", hooksSeen{use: ToolUse{Event: "PreToolUse", Stopped: stop}, ok: true},
			time.Time{}, false, false},
		{"PostToolUse after the Stop", hooksSeen{use: ToolUse{Event: "PostToolUse", At: stop.Add(time.Second),
			Stopped: stop}, ok: true}, time.Time{}, false, false},
		{"mid-turn", hooksSeen{use: ToolUse{Event: "PreToolUse", At: stop}, ok: true}, time.Time{}, false, false},
		{"permission asked", hooksSeen{use: ToolUse{Event: EventPermission, At: stop}, ok: true},
			time.Time{}, false, false},
		{"permission refused", hooksSeen{use: ToolUse{Event: EventPermission, At: stop.Add(-time.Minute), Idle: stop},
			ok: true}, stop, true, false},
		{"permission refused, subagent running", hooksSeen{use: ToolUse{Event: EventPermission,
			At: stop.Add(-time.Minute), Idle: stop}, ok: true, rec: HookRecord{Subagents: 1}}, time.Time{}, false, false},
		{"subagent running", hooksSeen{use: ToolUse{Event: "Stop", At: stop}, ok: true,
			rec: HookRecord{Subagents: 1}}, time.Time{}, false, false},
		{"subagent quiet too long", hooksSeen{use: ToolUse{Event: "Stop", At: now.Add(-2 * SubagentQuiet)}, ok: true,
			rec: HookRecord{Subagents: 1}}, now.Add(-2 * SubagentQuiet), true, false},
		{"subagent stopped after the Stop", hooksSeen{use: ToolUse{Event: "Stop", At: stop}, ok: true,
			rec: HookRecord{SubagentStopped: stop.Add(time.Second)}}, time.Time{}, false, false},
		{"subagent stopped before the Stop", hooksSeen{use: ToolUse{Event: "Stop", At: stop}, ok: true,
			rec: HookRecord{SubagentStopped: stop.Add(-time.Second)}}, stop, true, false},
	}
	for _, c := range cases {
		at, ended, replaced := turnEnded(c.h, now)
		if !at.Equal(c.at) || ended != c.ended || replaced != c.replaced {
			t.Errorf("%s: turnEnded = %v, %v, %v, want %v, %v, %v", c.name, at, ended, replaced, c.at, c.ended, c.replaced)
		}
	}
}

func TestAsksPermission(t *testing.T) {
	asked := ToolUse{Event: EventPermission, Tool: "Bash", At: time.Now()}
	cases := []struct {
		name string
		h    hooksSeen
		st   AgentState
		want bool
	}{
		{"asked, idle", hooksSeen{use: asked, ok: true}, StateIdle, true},
		{"asked, blocked", hooksSeen{use: asked, ok: true}, StateBlocked, true},
		{"asked, done", hooksSeen{use: asked, ok: true}, StateDone, true},
		{"asked, working", hooksSeen{use: asked, ok: true}, StateWorking, false},
		{"asked, refused", hooksSeen{use: ToolUse{Event: EventPermission, Tool: "Bash", At: asked.At,
			Idle: asked.At.Add(time.Minute)}, ok: true}, StateIdle, false},
		{"asked before the adoption", hooksSeen{use: asked}, StateIdle, false},
		{"no hooks", hooksSeen{}, StateIdle, false},
		{"a tool use", hooksSeen{use: ToolUse{Event: "PreToolUse", Tool: "Bash"}, ok: true}, StateIdle, false},
	}
	for _, c := range cases {
		if got := asksPermission(c.h, c.st); got != c.want {
			t.Errorf("%s: asksPermission = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestQuietFor(t *testing.T) {
	now := time.Now()
	idle := now.Add(-time.Hour)
	running := HookRecord{Subagents: 1, Noted: now.Add(-30 * time.Minute)}
	cases := []struct {
		name string
		h    hooksSeen
		want time.Duration
	}{
		{"no hooks", hooksSeen{}, time.Hour},
		{"noted before it went idle", hooksSeen{rec: HookRecord{Noted: now.Add(-2 * time.Hour)}}, time.Hour},
		{"noted since", hooksSeen{rec: HookRecord{Noted: now.Add(-time.Minute)}}, time.Minute},
		{"a subagent's tool use", hooksSeen{use: ToolUse{Event: "PreToolUse", At: now.Add(-2 * time.Minute)}, ok: true,
			rec: running}, 2 * time.Minute},
		{"a tool use with no subagent", hooksSeen{use: ToolUse{Event: "PreToolUse", At: now.Add(-2 * time.Minute)},
			ok: true}, time.Hour},
		{"a tool use before the adoption", hooksSeen{use: ToolUse{Event: "PreToolUse", At: now.Add(-2 * time.Minute)},
			rec: running}, 30 * time.Minute},
	}
	for _, c := range cases {
		if got := quietFor(c.h, idle, now); got != c.want {
			t.Errorf("%s: quietFor = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestIdleSettledDecisions(t *testing.T) {
	now := time.Now()
	stop := now.Add(-time.Minute)
	adopted := now.Add(-time.Hour)
	cases := []struct {
		name    string
		ticket  TicketStatus
		h       hooksSeen
		since   time.Time
		idleFor time.Duration
		settled bool
		why     string
	}{
		{"in progress", StatusInProgress, hooksSeen{use: ToolUse{Event: "Stop", At: stop}, ok: true}, time.Time{},
			time.Minute, false, "idle for 10m with the ticket still in progress"},
		{"in progress past the grace", StatusInProgress, hooksSeen{}, time.Time{}, idleGrace, true,
			"idle for 10m with the ticket still in progress"},
		{"Stop", StatusClosed, hooksSeen{use: ToolUse{Event: "Stop", At: stop}, ok: true}, adopted, time.Minute, true,
			"Stop hook at " + stop.Format("15:04:05")},
		{"replaced Stop", StatusClosed, hooksSeen{use: ToolUse{Event: "PreToolUse", Tool: "Read",
			At: stop.Add(time.Second), Stopped: stop}, ok: true}, time.Time{}, time.Minute, true,
			"Stop hook at " + stop.Format("15:04:05") + "; then a PreToolUse record with no turn of its own, written at " +
				stop.Add(time.Second).Format("15:04:05") +
				" by tool Read, session unnamed, main agent, transcript unnamed"},
		{"permission refused", StatusClosed, hooksSeen{use: ToolUse{Event: EventPermission, Tool: "Bash",
			At: stop.Add(-time.Minute), Idle: stop}, ok: true}, time.Time{}, time.Minute, true,
			"idle at its prompt at " + stop.Format("15:04:05") + " after a permission prompt for Bash refused"},
		{"replaced Stop before the adoption", StatusClosed, hooksSeen{use: ToolUse{Event: "PreToolUse", Tool: "Read",
			At: stop, Stopped: adopted.Add(-time.Second)}, ok: true}, adopted, time.Minute, false,
			"idle for 10m after a PreToolUse hook, with no Stop hook; that record was written at " +
				stop.Format("15:04:05") + " by tool Read, session unnamed, main agent, transcript unnamed"},
		{"subagent running", StatusClosed, hooksSeen{use: ToolUse{Event: "Stop", At: stop}, ok: true,
			rec: HookRecord{Subagents: 1}}, time.Time{}, idleGrace, true,
			"idle for 10m after a Stop hook, with a subagent still running"},
		{"subagent's answer pending", StatusClosed, hooksSeen{use: ToolUse{Event: "Stop", At: stop}, ok: true,
			rec: HookRecord{SubagentStopped: stop.Add(time.Second)}}, time.Time{}, time.Minute, false,
			"idle for 10m after a subagent stopped at " + stop.Add(time.Second).Format("15:04:05") +
				", past the turn's Stop hook, with no turn on its answer"},
		{"mid-turn", StatusOpen, hooksSeen{use: ToolUse{Event: "PostToolUse", Tool: "Bash", At: stop}, ok: true},
			time.Time{}, time.Minute, false, "idle for 10m after a PostToolUse hook, with no Stop hook"},
		{"open, nothing reported", StatusOpen, hooksSeen{}, time.Time{}, time.Hour, false,
			"idle 3m after it started, with the ticket still open"},
		{"open, a report before the adoption", StatusOpen, hooksSeen{use: ToolUse{Event: "Stop", At: stop}},
			adopted, time.Hour, false, "idle 3m after it started, with the ticket still open"},
		{"closed, nothing reported", StatusClosed, hooksSeen{}, time.Time{}, 0, true, "idle with the ticket closed"},
	}
	for _, c := range cases {
		settled, why := idleSettled(c.ticket, c.h, c.since, now, c.idleFor, time.Minute)
		if settled != c.settled || !strings.HasPrefix(why, c.why) {
			t.Errorf("%s: idleSettled = %v, %q, want %v, %q", c.name, settled, why, c.settled, c.why)
		}
	}
}

// A worker stuck in a state the run can't wait out stops it, saying why; one that only passed
// through it doesn't.
func TestStuck(t *testing.T) {
	w := worker{id: "A", tab: "tab"}
	long := func(d time.Duration) streak { return streak{since: time.Now().Add(-d - time.Second)} }
	cases := []struct {
		name             string
		st               AgentState
		asks             bool
		blocked, unknown streak
		want             string // the stop's detail; "" for none
	}{
		{"working", StateWorking, false, long(blockedLimit), long(unknownLimit), ""},
		{"blocked a while", StateBlocked, false, streak{since: time.Now()}, streak{}, ""},
		{"blocked too long", StateBlocked, false, long(blockedLimit), streak{}, " >4min: tab tab (A) needs attention"},
		{"asking too long", StateIdle, true, long(blockedLimit), streak{},
			" >4min: tab tab (A) needs attention: its worker waits on a permission prompt"},
		{"unknown too long", StateUnknown, false, streak{}, long(unknownLimit),
			" >5min: the terminal can't tell what the worker in tab tab (A) is doing; it needs attention"},
	}
	for _, c := range cases {
		blocked, unknown := c.blocked, c.unknown
		stop := stuck(w, c.st, c.asks, &blocked, &unknown)
		switch {
		case c.want == "" && stop != nil:
			t.Errorf("%s: stopped: %s", c.name, stop.detail)
		case c.want != "" && (stop == nil || stop.code != ExitStuck || stop.detail != c.want):
			t.Errorf("%s: stop = %+v, want %q", c.name, stop, c.want)
		}
	}
}
