// Package reap stops what a worker's agent leaves running once its tab is closed. Claude Code runs
// each Bash command in a process group of its own, with no terminal (a detached shell), which only
// its own shutdown stops: closing the tab hangs up the agent, and should its shutdown not finish (its
// SessionEnd hooks, its flushing), the worker's check or dev server lives on, using cores and writing
// into a worktree orchestra reuses or removes. Close notes every process group below the tab's
// processes before it closes the tab, gives the agent time to stop them itself, then stops those
// still running: SIGTERM, and SIGKILL for those that outlive it.
package reap

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// Terminal is a terminal's tabs as Close closes them.
type Terminal interface {
	CloseTab(ctx context.Context, tab string) error
	// the IDs of the processes at the root of what each of the tab's panes runs; an error when the
	// terminal can't say
	TabProcesses(ctx context.Context, tab string) ([]int, error)
}

// Close closes the tab, then stops what its processes leave running (see Stop). The processes are
// noted before the tab is closed: once its agent is gone, the commands it started are no longer below
// it. A tab whose processes can't be told is closed all the same, as is one whose processes can't be
// listed; one that can't be closed is left running.
func Close(ctx context.Context, t Terminal, tab string) error {
	var tree Tree
	if roots, err := t.TabProcesses(ctx, tab); err == nil {
		// Unlisted, the tree is empty and stops nothing: the tab is closed as it was before.
		tree, _ = Collect(ctx, roots...)
	}
	if err := t.CloseTab(ctx, tab); err != nil {
		return err
	}
	tree.Stop(ctx)
	return nil
}

// How long Stop gives each step, and how often it looks.
const (
	shutdownWithin = 5 * time.Second // for the agent's own shutdown, which stops its commands
	termWithin     = 3 * time.Second // for the commands still running to end on SIGTERM
	lookEvery      = 100 * time.Millisecond
)

// Tree is the processes at and below some roots, by ID, with the process group of each, as one
// listing found them, and the groups among theirs that Stop may signal.
type Tree struct {
	roots  []int
	procs  map[int]int // each process's group
	groups []int       // the groups Stop signals, in order

	shutdown, term, every time.Duration
}

// Collect lists the machine's processes and returns the tree at and below roots. It leaves out of the
// groups to signal orchestra's own, init's, and those of the processes above the roots (the
// terminal's), which a process below them may be in still.
func Collect(ctx context.Context, roots ...int) (Tree, error) {
	if len(roots) == 0 {
		return Tree{}, nil
	}
	l, err := list(ctx)
	if err != nil {
		return Tree{}, err
	}
	return collect(l, ownGroup(), roots), nil
}

// collect is Collect on the listing l, for orchestra in group own.
func collect(l listing, own int, roots []int) Tree {
	t := Tree{procs: map[int]int{}, shutdown: shutdownWithin, term: termWithin, every: lookEvery}
	kids := map[int][]int{}
	for pid, p := range l {
		if p.parent != pid {
			kids[p.parent] = append(kids[p.parent], pid)
		}
	}
	var walk func(pid int)
	walk = func(pid int) {
		p, ok := l[pid]
		if _, seen := t.procs[pid]; !ok || seen || p.zombie {
			return
		}
		t.procs[pid] = p.group
		for _, c := range kids[pid] {
			walk(c)
		}
	}
	for _, r := range roots {
		if _, ok := l[r]; ok {
			t.roots = append(t.roots, r)
		}
		walk(r)
	}
	spared := map[int]bool{own: true}
	for _, r := range roots {
		seen := map[int]bool{}
		for pid := l[r].parent; pid > 1 && !seen[pid]; pid = l[pid].parent {
			if _, below := t.procs[pid]; below {
				break // a root below another
			}
			seen[pid] = true
			spared[l[pid].group] = true
		}
	}
	for _, g := range t.procs {
		if g > 1 && !spared[g] && !slices.Contains(t.groups, g) {
			t.groups = append(t.groups, g)
		}
	}
	slices.Sort(t.groups)
	return t
}

// Stop gives the agent at the tree's roots time to stop what it started, and once its roots are gone,
// or the time is up, stops each of the tree's groups that still has a process of the tree in it:
// SIGTERM, then SIGKILL for those that outlive it. A group signalled is one a process of the tree is
// still in, by its ID and its group's, so a group whose processes have all ended, and whose ID
// another has since taken, is left alone. Once ctx ends it waits no more, and sends SIGTERM only.
func (t Tree) Stop(ctx context.Context) {
	if len(t.groups) == 0 {
		return
	}
	t.await(ctx, t.shutdown, func(l listing) bool { return len(t.alive(l, t.roots)) == 0 })
	left := t.left(ctx)
	if len(left) == 0 {
		return
	}
	for _, g := range left {
		// Best effort: the group may have ended since it was listed, and nothing more can be done if not.
		_ = signalGroup(g, sigTerm)
	}
	if !t.await(ctx, t.term, func(l listing) bool { return len(t.running(l)) == 0 }) {
		for _, g := range t.left(context.WithoutCancel(ctx)) {
			_ = signalGroup(g, sigKill) // as with SIGTERM
		}
	}
}

// await looks at the machine's processes every t.every until done says so, for up to within, and
// reports whether it did; false too when ctx ends first or the processes can't be listed.
func (t Tree) await(ctx context.Context, within time.Duration, done func(listing) bool) bool {
	deadline := time.Now().Add(within)
	for {
		l, err := list(context.WithoutCancel(ctx))
		if err != nil {
			return false
		}
		if done(l) {
			return true
		}
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(t.every):
		}
	}
}

// left are the tree's groups still running, as the machine's processes are now: none when they
// can't be listed.
func (t Tree) left(ctx context.Context) []int {
	l, err := list(ctx)
	if err != nil {
		return nil
	}
	return t.running(l)
}

// running are the groups to signal that a process of the tree is still in, by the listing l.
func (t Tree) running(l listing) []int {
	var gs []int
	for _, g := range t.groups {
		for pid, pg := range t.procs {
			if pg == g && len(t.alive(l, []int{pid})) > 0 {
				gs = append(gs, g)
				break
			}
		}
	}
	return gs
}

// alive are those of pids that l has, in the group the tree found them in, and not ended.
func (t Tree) alive(l listing, pids []int) []int {
	var a []int
	for _, pid := range pids {
		if p, ok := l[pid]; ok && !p.zombie && p.group == t.procs[pid] {
			a = append(a, pid)
		}
	}
	return a
}

// listing is the machine's processes, by ID.
type listing map[int]process

// process is one process in a listing: its parent, its process group, and whether it has ended,
// unreaped (a zombie).
type process struct {
	parent, group int
	zombie        bool
}

// list lists the machine's processes with ps, the same on macOS and Linux.
func list(ctx context.Context) (listing, error) {
	out, err := command.Output(ctx, command.ReadLimit, "", "ps", "-A", "-o", "pid=,ppid=,pgid=,stat=")
	if err != nil {
		return nil, err
	}
	return parse(out)
}

// parse reads ps's listing of each process's ID, its parent's, its group's and its state, a process
// a line.
func parse(out string) (listing, error) {
	l := listing{}
	for line := range strings.Lines(out) {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 4 {
			return nil, fmt.Errorf("unexpected line in the process listing: %q", line)
		}
		var n [3]int
		for i := range n {
			v, err := strconv.Atoi(f[i])
			if err != nil {
				return nil, fmt.Errorf("unexpected line in the process listing: %q", line)
			}
			n[i] = v
		}
		l[n[0]] = process{parent: n[1], group: n[2], zombie: strings.HasPrefix(f[3], "Z")}
	}
	return l, nil
}
