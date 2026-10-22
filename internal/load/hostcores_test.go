package load

import (
	"strings"
	"testing"
	"time"
)

func TestHostCores(t *testing.T) {
	cpuinfo := strings.Repeat("processor\t: 0\nmodel name\t: x\n\n", 16)
	for _, c := range []struct {
		name            string
		online, cpuinfo string
		affinity, want  int
	}{
		{"online list", "0-31,48-63\n", cpuinfo, 4, 48},
		{"single cpu", "0\n", "", 1, 1},
		{"cpuinfo when online is unreadable", "", cpuinfo, 4, 16},
		{"cpuinfo when online is malformed", "0-x\n", cpuinfo, 4, 16},
		{"affinity when neither says", "", "", 8, 8},
		{"affinity never cut down", "0-1\n", "", 8, 8},
	} {
		if got := HostCores(c.online, c.cpuinfo, c.affinity); got != c.want {
			t.Errorf("%s: HostCores = %d, want %d", c.name, got, c.want)
		}
	}
}

// A process pinned to 4 of a host's 64 cores reads the host's load of 40 against the host's 64: not loaded,
// where against its own 4 it would read red.
func TestLoadAgainstHostCores(t *testing.T) {
	cores := HostCores("0-63\n", "", 4)
	k, err := ParseLinux("40.00 40.00 40.00 3/1234 5678\n", "", "", "", cores)
	if err != nil {
		t.Fatal(err)
	}
	if k.Cores != 64 {
		t.Fatalf("cores %d, want the host's 64", k.Cores)
	}
	s := &Sampler{}
	now := time.Now()
	r := Reading{Kernel: k}
	if l := s.levels(r, now); l.CPU != Green {
		t.Errorf("CPU level %v, want green", l.CPU)
	}
	if l := s.levels(r, now.Add(OverLoadFor)); l.CPU != Green {
		t.Errorf("CPU level after %v: %v, want green", OverLoadFor, l.CPU)
	}
}
