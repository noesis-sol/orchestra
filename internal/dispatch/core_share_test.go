package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/project"
)

// A worker's share of the cores is the machine's divided among the run's workers, at its concurrency,
// and those running in the other runs, kept to the cap across the machine; never under one.
func TestCoreShare(t *testing.T) {
	for _, c := range []struct {
		cores, concurrency, others, limit, want int
	}{
		{cores: 10, concurrency: 1, want: 10},
		{cores: 10, concurrency: 4, want: 2},
		{cores: 10, concurrency: 4, others: 4, want: 1},
		{cores: 12, concurrency: 2, others: 4, want: 2},
		{cores: 12, concurrency: 4, others: 8, limit: 6, want: 2},
		{cores: 3, concurrency: 8, want: 1},
		{cores: 8, concurrency: 0, want: 8},
	} {
		if got := coreShare(c.cores, c.concurrency, c.others, c.limit); got != c.want {
			t.Errorf("coreShare(%d cores, concurrency %d, %d others, cap %d) = %d, want %d",
				c.cores, c.concurrency, c.others, c.limit, got, c.want)
		}
	}
}

// The worker's environment gives its share as ORCHESTRA_CORES and as GOFLAGS's -p, after the flags the
// run has, unless they set -p themselves; no share before the cores are shared.
func TestWorkerEnvGivesTheShare(t *testing.T) {
	o := New(Config{Concurrency: 4, Limit: NoLimit}, &Log{}, "", Deps{})
	o.cores = 10
	env := o.workerEnv(command.WithEnv(context.Background(), "GOFLAGS="))
	if _, set := env[CoresVar]; set {
		t.Errorf("env before the cores are shared %v, want no share", env)
	}
	if _, set := env["GOFLAGS"]; set {
		t.Errorf("env before the cores are shared %v, want no GOFLAGS", env)
	}
	o.shareCores(1)
	for goflags, want := range map[string]string{
		"":                  "-p=2",
		"-mod=mod":          "-mod=mod -p=2",
		"-pgo=off -tags=x":  "-pgo=off -tags=x -p=2",
		"-p=8":              "",
		"-mod=mod -p 8":     "",
		"-mod=mod --p=8 -v": "",
	} {
		env := o.workerEnv(command.WithEnv(context.Background(), "GOFLAGS="+goflags))
		if env[CoresVar] != "2" {
			t.Errorf("GOFLAGS %q: %s %q, want 2", goflags, CoresVar, env[CoresVar])
		}
		if got, set := env["GOFLAGS"]; got != want || set != (want != "") {
			t.Errorf("GOFLAGS %q: the worker's GOFLAGS %q (set: %v), want %q", goflags, got, set, want)
		}
	}
}

// envRecorder is a reporter whose workers' settings give them an environment, which it records.
type envRecorder struct {
	fakeReporter
	mu   sync.Mutex
	envs []map[string]string
}

func (r *envRecorder) WithEnv(env map[string]string) Reporter {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.envs = append(r.envs, env)
	return r.fakeReporter
}

// shares returns the ORCHESTRA_CORES the workers started with, in turn.
func (r *envRecorder) shares() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var got []string
	for _, env := range r.envs {
		got = append(got, env[CoresVar])
	}
	return got
}

// A run's one worker has all the machine's cores.
func TestOneWorkerHasAllTheCores(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		rec := &envRecorder{}
		h.reporter = rec
		h.beads.add("A", "ticket A", 1)
		h.worker("A", slowly(time.Minute, "a.txt"))
		o := h.loop()
		o.cores = 10
		if code := o.Run(context.Background()); code != ExitOK {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		if got := rec.shares(); !slices.Equal(got, []string{"10"}) {
			t.Errorf("the worker's cores %q, want 10", got)
		}
	})
}

// A run's workers share the cores with each other and with the workers running in another run on the
// machine: 12 cores among this run's 2 and the other's 4 are 2 each.
func TestWorkersShareTheCoresAcrossTheMachine(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := sharing(t, h)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		other := `{"pid":` + strconv.Itoa(os.Getpid()) + `,"repo":"/elsewhere","concurrent":4,` +
			`"started":"2026-10-20T08:00:00Z","running":4}`
		if err := os.WriteFile(filepath.Join(dir, "run-other.json"), []byte(other), 0o644); err != nil {
			t.Fatal(err)
		}
		rec := &envRecorder{}
		h.reporter = rec
		h.cfg.Concurrency = 2
		for _, id := range []string{"A", "B", "C"} {
			h.beads.add(id, "ticket "+id, 1)
			h.worker(id, slowly(time.Minute, id+".txt"))
		}
		o := h.loop()
		o.cores = 12
		if code := o.Run(context.Background()); code != ExitOK {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		if got := rec.shares(); !slices.Equal(got, []string{"2", "2", "2"}) {
			t.Errorf("the workers' cores %q, want 2 each", got)
		}
	})
}

// The rules' note on checks tells a worker given its share of the cores to keep its checks to it, and
// says nothing of it to one whose hooks can't give it one.
func TestCheckNoteSaysTheShareOfTheCores(t *testing.T) {
	t.Parallel()
	for _, given := range []bool{true, false} {
		synctest.Test(t, func(t *testing.T) {
			h := newTimedHarness(t)
			h.reporter = fakeReporter{}
			if given {
				h.reporter = &envRecorder{}
			}
			h.beads.add("A", "first", 1)
			var rules string
			h.worker("A", func(w *fakeWorker) AgentState {
				rules = readRun(t, w.wt, project.RulesName)
				return finishes("a.txt")(w)
			})
			if o, code := h.run(); code != ExitOK {
				t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
			}
			said := strings.Contains(rules, "Piping a check through tail hides its exit status. Other workers' "+
				"checks run on this machine beside yours: ORCHESTRA_CORES in your environment is your share of its cores")
			if said != given {
				t.Errorf("given a share: %v; the rules say it: %v\n%s", given, said, rules)
			}
		})
	}
}
