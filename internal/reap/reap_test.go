package reap

import (
	"slices"
	"testing"
)

// tree is a fake ps listing: orchestra (100) runs in its own group; tmux's server (200) runs worker A's
// claude (300) and worker B's (400). A's claude runs a detached Bash shell (301), which runs go test
// (302, in the shell's group) and a dev server that made a group of its own (303); an MCP server in
// claude's group (305); a hook that ended, unreaped (304); and a process that stayed in the server's
// group (306).
const tree = `
    1     0     1 Ss
  100     1   100 S+
  200     1   200 Ss
  300   200   300 Ss+
  301   300   301 Ss
  302   301   301 S
  303   301   303 Ss
  304   300   300 Z
  305   300   300 S
  306   300   200 S
  400   200   400 Ss+
  401   400   401 Ss
`

func TestCollectFindsTheGroupsBelowTheRoots(t *testing.T) {
	l, err := parse(tree)
	if err != nil {
		t.Fatal(err)
	}
	got := collect(l, 100, []int{300})
	if want := []int{300, 301, 303}; !slices.Equal(got.groups, want) {
		t.Errorf("groups = %v, want %v: claude's, its shell's and its dev server's, not tmux's or worker B's",
			got.groups, want)
	}
	if want := []int{300}; !slices.Equal(got.roots, want) {
		t.Errorf("roots = %v, want %v", got.roots, want)
	}
	for _, pid := range []int{304, 400, 401, 200} {
		if _, ok := got.procs[pid]; ok {
			t.Errorf("process %d is in the tree; it shouldn't be", pid)
		}
	}
}

func TestCollectSparesOrchestrasGroup(t *testing.T) {
	l, err := parse(tree)
	if err != nil {
		t.Fatal(err)
	}
	if got := collect(l, 301, []int{300}); slices.Contains(got.groups, 301) {
		t.Errorf("groups = %v, holding orchestra's own (301)", got.groups)
	}
}

func TestCollectOfNoRootStopsNothing(t *testing.T) {
	l, err := parse(tree)
	if err != nil {
		t.Fatal(err)
	}
	if got := collect(l, 100, []int{999}); len(got.groups) != 0 || len(got.roots) != 0 {
		t.Errorf("a root not listed gave groups %v, roots %v; want none", got.groups, got.roots)
	}
}

func TestParseRefusesAnOddLine(t *testing.T) {
	if _, err := parse("  1  0  1\n"); err == nil {
		t.Error("a line of three fields was read")
	}
	if _, err := parse("  1  0  x S\n"); err == nil {
		t.Error("a line with a group that is no number was read")
	}
}
