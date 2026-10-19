package load

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
)

// Processes are the machine's processes, by ID, as one listing found them.
type Processes map[int]Process

// Process is one process in a listing: its parent and its resident memory, in bytes.
type Process struct {
	Parent int
	Memory int64
}

// List lists the machine's processes with ps, the same on macOS and Linux.
func List(ctx context.Context) (Processes, error) {
	out, err := command.Output(ctx, command.ReadLimit, "", "ps", "-A", "-o", "pid=,ppid=,rss=")
	if err != nil {
		return nil, err
	}
	return ParseProcesses(out)
}

// ParseProcesses reads ps's listing of each process's ID, its parent's and its resident memory in
// kilobytes, a process a line.
func ParseProcesses(out string) (Processes, error) {
	ps := Processes{}
	for line := range strings.Lines(out) {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 3 {
			return nil, fmt.Errorf("unexpected line in the process listing: %q", line)
		}
		var n [3]int64
		for i := range f {
			v, err := strconv.ParseInt(f[i], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("unexpected line in the process listing: %q", line)
			}
			n[i] = v
		}
		ps[int(n[0])] = Process{Parent: int(n[1]), Memory: n[2] * 1024}
	}
	return ps, nil
}

// children are each process's children.
func (ps Processes) children() map[int][]int {
	c := map[int][]int{}
	for pid, p := range ps {
		if p.Parent != pid {
			c[p.Parent] = append(c[p.Parent], pid)
		}
	}
	return c
}

// Tree is the memory of the processes roots and all below them, each counted once; Unknown when none
// of roots is in the listing.
func (ps Processes) Tree(roots ...int) int64 {
	seen := map[int]bool{}
	var n int64
	found := false
	kids := ps.children()
	var walk func(pid int)
	walk = func(pid int) {
		p, ok := ps[pid]
		if !ok || seen[pid] {
			return
		}
		seen[pid], found = true, true
		n += p.Memory
		for _, c := range kids[pid] {
			walk(c)
		}
	}
	for _, r := range roots {
		walk(r)
	}
	if !found {
		return Unknown
	}
	return n
}

// Below is the memory of every process below pid, itself not counted, nor the trees of skip (the
// workers', should one be below it).
func (ps Processes) Below(pid int, skip ...int) int64 {
	kids := ps.children()
	seen := map[int]bool{pid: true}
	for _, s := range skip {
		seen[s] = true
	}
	var n int64
	var walk func(pid int)
	walk = func(pid int) {
		for _, c := range kids[pid] {
			if seen[c] {
				continue
			}
			seen[c] = true
			n += ps[c].Memory
			walk(c)
		}
	}
	walk(pid)
	return n
}
