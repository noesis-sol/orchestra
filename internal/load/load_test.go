package load

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// listing is a fake ps listing: orchestra (100) runs the merge check (200, with go test 201 under it);
// worker A's claude (300) runs go test (301), which runs a test binary (302); worker B's claude is 400;
// 500 is another program on the machine.
const listing = `
    1     0  1000
  100     1  50000
  200   100  1000
  201   200  400000
  300     1  250000
  301   300  10000
  302   301  800000
  400     1  200000
  500     1  999999
`

const mb = 1024 * 1024

func parsed(t *testing.T) Processes {
	t.Helper()
	ps, err := ParseProcesses(listing)
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

// A worker's tree is its root and everything under it, at any depth, counted once.
func TestTreeSumsAWorkersChildren(t *testing.T) {
	ps := parsed(t)
	if got, want := ps.Tree(300), int64(250000+10000+800000)*1024; got != want {
		t.Errorf("A's tree is %d bytes, want %d", got, want)
	}
	if got, want := ps.Tree(300, 301), int64(250000+10000+800000)*1024; got != want {
		t.Errorf("roots in one tree should count once: %d bytes, want %d", got, want)
	}
	if got := ps.Tree(999); got != Unknown {
		t.Errorf("a root not in the listing gives %d, want Unknown", got)
	}
}

// orchestra's own commands count apart from the workers: everything below it but itself, and not a
// worker's tree should one be below it.
func TestBelowCountsTheMergeCheckApart(t *testing.T) {
	ps := parsed(t)
	if got, want := ps.Below(100), int64(1000+400000)*1024; got != want {
		t.Errorf("below orchestra: %d bytes, want %d", got, want)
	}
	if got, want := ps.Below(100, 201), int64(1000)*1024; got != want {
		t.Errorf("below orchestra but a worker's tree: %d bytes, want %d", got, want)
	}
}

func TestParseProcessesRefusesAnOddLine(t *testing.T) {
	if _, err := ParseProcesses("1 0\n"); err == nil {
		t.Error("a line of two fields should be refused")
	}
	if _, err := ParseProcesses("1 0 x\n"); err == nil {
		t.Error("a line with a word for a number should be refused")
	}
}

// fakeSampler is a Sampler of the listing above, orchestra being 100, its kernel k.
func fakeSampler(k *Kernel, listErr error) *Sampler {
	return &Sampler{
		Processes: func(context.Context) (Processes, error) {
			if listErr != nil {
				return nil, listErr
			}
			return ParseProcesses(listing)
		},
		Machine: func(context.Context) (Kernel, error) { return *k, nil },
		Self:    100,
	}
}

func TestReadGivesEachWorkerItsTree(t *testing.T) {
	k := Kernel{Pressure: PressureNormal, Swaps: 0, Load1: 2, Cores: 10, Available: -1}
	r := fakeSampler(&k, nil).Read(context.Background(), time.Now(), map[string][]int{
		"A": {300}, "B": {400}, "C": nil, "D": {999},
	})
	want := map[string]int64{"A": 1060000 * 1024, "B": 200000 * 1024, "C": Unknown, "D": Unknown}
	for ticket, m := range want {
		if r.Workers[ticket] != m {
			t.Errorf("worker %s: %d bytes, want %d", ticket, r.Workers[ticket], m)
		}
	}
	if r.Check != 401000*1024 {
		t.Errorf("merge check: %d bytes, want %d", r.Check, 401000*1024)
	}
	if got, want := r.Memory(), int64(1060000+200000+401000)*1024; got != want {
		t.Errorf("memory %d, want %d: the unknown ones count for nothing", got, want)
	}
	if r.Swapping != -1 {
		t.Errorf("the first reading's swapping is %v, want -1: there is nothing to tell a rate from", r.Swapping)
	}
}

// A listing that fails says nothing of any worker: each is unknown, not 0.
func TestReadWithoutAListingGivesUnknown(t *testing.T) {
	k := unknownKernel()
	r := fakeSampler(&k, errors.New("ps: no such file")).Read(context.Background(), time.Now(),
		map[string][]int{"A": {300}})
	if r.Workers["A"] != Unknown {
		t.Errorf("worker A: %d, want Unknown", r.Workers["A"])
	}
}

// The line is green; amber when the load passes BusyLoad per core or swapping is under way; red at
// memory pressure warn or worse, or with the load past OverLoad per core for OverLoadFor.
func TestLevelFollowsTheThresholds(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return start.Add(d) }
	k := Kernel{Pressure: PressureNormal, Swaps: 1000, Load1: 9.8, Cores: 10, Available: -1}
	s := fakeSampler(&k, nil)
	if r := s.Read(ctx, at(0), nil); r.Level != Green {
		t.Errorf("a load of 9.8 on 10 cores: level %v, want green", r.Level)
	}
	k.Load1 = 16
	if r := s.Read(ctx, at(5*time.Second), nil); r.Level != Amber {
		t.Errorf("a load of 16 on 10 cores: level %v, want amber", r.Level)
	}
	k.Load1, k.Swaps = 2, 1000+SwappingPerMinute
	if r := s.Read(ctx, at(65*time.Second), nil); r.Level != Amber || r.Swapping != SwappingPerMinute {
		t.Errorf("swapping %v pages a minute: level %v, want amber at %d", r.Swapping, r.Level, SwappingPerMinute)
	}
	if r := s.Read(ctx, at(125*time.Second), nil); r.Level != Green || r.Swapping != 0 {
		t.Errorf("no more swapping (%v a minute): level %v, want green", r.Swapping, r.Level)
	}
	k.Pressure = PressureWarn
	if r := s.Read(ctx, at(130*time.Second), nil); r.Level != Red {
		t.Errorf("memory pressure warn: level %v, want red", r.Level)
	}
	k.Pressure, k.Load1 = PressureNormal, 31
	if r := s.Read(ctx, at(135*time.Second), nil); r.Level != Amber {
		t.Errorf("a load of 31 on 10 cores just now: level %v, want amber until it lasts a minute", r.Level)
	}
	if r := s.Read(ctx, at(135*time.Second+OverLoadFor), nil); r.Level != Red {
		t.Errorf("a load of 31 on 10 cores for a minute: level %v, want red", r.Level)
	}
	k.Load1 = 20
	if r := s.Read(ctx, at(140*time.Second+OverLoadFor), nil); r.Level != Amber {
		t.Errorf("the load back to 20: level %v, want amber", r.Level)
	}
	k.Load1 = 31
	if r := s.Read(ctx, at(145*time.Second+OverLoadFor), nil); r.Level != Amber {
		t.Errorf("a load of 31 again: level %v, want amber, its minute starting anew", r.Level)
	}
}

func TestParseDarwin(t *testing.T) {
	vmstat := `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                                     4680.
Swapins:                                     8873329.
Swapouts:                                   12213778.
`
	k, err := ParseDarwin("2\n{ 54.49 68.36 71.71 }\n10\n", vmstat)
	if err != nil {
		t.Fatal(err)
	}
	want := Kernel{Pressure: PressureWarn, Swaps: 8873329 + 12213778, Load1: 54.49, Cores: 10, Available: -1}
	if k != want {
		t.Errorf("got %+v, want %+v", k, want)
	}
	for level, p := range map[string]Pressure{"1": PressureNormal, "4": PressureCritical, "0": PressureUnknown} {
		if k, _ := ParseDarwin(level+"\n{ 1.00 1.00 1.00 }\n8\n", ""); k.Pressure != p || k.Swaps != -1 {
			t.Errorf("level %s: pressure %v, swaps %d; want %v, -1", level, k.Pressure, k.Swaps, p)
		}
	}
	if _, err := ParseDarwin("1\n", vmstat); err == nil {
		t.Error("an answer short of its three lines should be refused")
	}
}

func TestParseLinux(t *testing.T) {
	meminfo := "MemTotal:       16000000 kB\nMemFree:          200000 kB\nMemAvailable:    6000000 kB\n"
	vmstat := "nr_free_pages 50000\npswpin 120\npswpout 380\n"
	pressure := "some avg10=1.50 avg60=0.40 avg300=0.10 total=1234\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"
	k, err := ParseLinux("9.80 8.12 7.40 3/1234 5678\n", pressure, meminfo, vmstat, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := Kernel{Pressure: PressureNormal, Swaps: 500, Load1: 9.8, Cores: 10, Available: 6000000 * 1024}
	if k != want {
		t.Errorf("got %+v, want %+v", k, want)
	}
	stalled := map[string]Pressure{
		"some avg10=12.00 avg60=1 avg300=0 total=9\nfull avg10=2.00 avg60=0 avg300=0 total=1\n":  PressureWarn,
		"some avg10=40.00 avg60=9 avg300=1 total=9\nfull avg10=15.00 avg60=3 avg300=0 total=1\n": PressureCritical,
	}
	for p, want := range stalled {
		if k, _ := ParseLinux("1 1 1\n", p, meminfo, vmstat, 4); k.Pressure != want {
			t.Errorf("%q: pressure %v, want %v", p, k.Pressure, want)
		}
	}
	// Without pressure stall information, the share of memory available.
	for avail, want := range map[string]Pressure{"6000000": PressureNormal, "1200000": PressureWarn,
		"600000": PressureCritical} {
		mi := "MemTotal: 16000000 kB\nMemAvailable: " + avail + " kB\n"
		if k, _ := ParseLinux("1 1 1\n", "", mi, "", 4); k.Pressure != want {
			t.Errorf("%s kB of 16000000 available: pressure %v, want %v", avail, k.Pressure, want)
		}
	}
	if _, err := ParseLinux("", "", "", "", 4); err == nil {
		t.Error("nothing readable should be an error")
	}
}

// The kernel of the machine the tests run on can be read, where orchestra reads one.
func TestReadKernelHere(t *testing.T) {
	k, err := ReadKernel(context.Background())
	if errors.Is(err, errors.ErrUnsupported) {
		t.Skip("no kernel reading on this system")
	}
	if err != nil {
		t.Fatal(err)
	}
	if k.Cores <= 0 || k.Load1 < 0 {
		t.Errorf("read %+v: want the cores and the load", k)
	}
}

// The machine's processes can be listed with ps, this test's own among them under its parent.
func TestListHere(t *testing.T) {
	ps, err := List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := ps[os.Getpid()]; !ok || p.Parent != os.Getppid() || p.Memory <= 0 {
		t.Errorf("this process is %+v (listed: %v), want under %d with its memory", p, ok, os.Getppid())
	}
}
