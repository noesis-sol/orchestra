package dispatch

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
)

// What the workers' hooks noted, for the run report: each ticket's permission prompts and
// compactions in this run. A worker's notes go when its worktree is removed, or when the next worker
// in it starts, so they are read before either, and once more for the rest as the report is written.

// hookTally is the hooks' record of each ticket's workers in this run.
type hookTally struct {
	mu      sync.Mutex
	tickets map[string]*ticketHooks
}

// ticketHooks is one ticket's record: its earlier workers' and its latest worker's.
type ticketHooks struct {
	wt     string
	before HookRecord // the earlier workers', oldest first
	latest HookRecord // the latest worker's, as last read
	gone   bool       // its worktree is removed: latest is final
}

// hooksRecorded reports whether the run's workers note their permission prompts and compactions.
func (o *Loop) hooksRecorded() bool {
	return o.reporter != nil && o.cfg.ClaudeWorkers()
}

// readHooks reads what ticket id's worker in wt noted, as its latest worker's record; gone says its
// worktree is about to be removed.
func (o *Loop) readHooks(id, wt string, gone bool) {
	if !o.hooksRecorded() {
		return
	}
	rec := o.reporter.HookRecord(wt)
	t := &o.hookTally
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tickets == nil {
		t.tickets = map[string]*ticketHooks{}
	}
	h := t.tickets[id]
	if h == nil {
		h = &ticketHooks{}
		t.tickets[id] = h
	}
	h.wt, h.latest, h.gone = wt, rec, gone
}

// newWorkerHooks keeps what ticket id's worker before in wt noted in this run, as a new worker's
// hooks are set up there, replacing its notes.
func (o *Loop) newWorkerHooks(id, wt string) {
	if !o.hooksRecorded() {
		return
	}
	t := &o.hookTally
	t.mu.Lock()
	h := t.tickets[id]
	t.mu.Unlock()
	if h != nil && !h.gone && h.wt == wt { // a worker this run started, or one it took over, whose notes are still there
		o.readHooks(id, wt, false)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tickets == nil {
		t.tickets = map[string]*ticketHooks{}
	}
	if h = t.tickets[id]; h == nil {
		h = &ticketHooks{}
		t.tickets[id] = h
	}
	h.before.Permissions = append(h.before.Permissions, h.latest.Permissions...)
	h.before.Compactions = append(h.before.Compactions, h.latest.Compactions...)
	h.wt, h.latest, h.gone = wt, HookRecord{}, false
}

// hookEvidence reads the notes of the workers whose worktrees are still there, those in progress
// included, and says, one line per ticket, which waited on permission prompts or were compacted.
func (o *Loop) hookEvidence() string {
	if !o.hooksRecorded() {
		return "Not recorded: the workers don't report through Claude Code hooks."
	}
	left := map[string]string{} // ticket: worktree
	o.mu.Lock()
	for id, w := range o.placed { // workers taken over from the last run as well as those started in this one
		if _, ok := o.active[id]; ok && w.wt != "" {
			left[id] = w.wt
		}
	}
	o.mu.Unlock()
	t := &o.hookTally
	t.mu.Lock()
	for id, h := range t.tickets {
		if !h.gone {
			left[id] = h.wt
		}
	}
	t.mu.Unlock()
	for id, wt := range left {
		o.readHooks(id, wt, false)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	var lines []string
	for _, id := range slices.Sorted(maps.Keys(t.tickets)) {
		h := t.tickets[id]
		if line := hooksLine(id, slices.Concat(h.before.Permissions, h.latest.Permissions),
			slices.Concat(h.before.Compactions, h.latest.Compactions)); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return "None: no worker's hooks noted a permission prompt or a compaction."
	}
	return strings.Join(lines, "\n")
}

// hooksLine reads "orchestra-1: its workers waited on 2 permission prompts (Bash, Edit); their
// context was compacted once (auto)."; "" when there were neither.
func hooksLine(id string, permissions, compactions []string) string {
	var said []string
	if n := len(permissions); n > 0 {
		said = append(said, fmt.Sprintf("waited on %s (%s)", counted(n, "permission prompt"),
			strings.Join(permissions, ", ")))
	}
	if n := len(compactions); n > 0 {
		said = append(said, fmt.Sprintf("had their context compacted %s (%s)", times(n),
			strings.Join(compactions, ", ")))
	}
	if len(said) == 0 {
		return ""
	}
	return id + ": its workers " + strings.Join(said, "; ") + "."
}
