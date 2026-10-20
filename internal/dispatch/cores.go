package dispatch

import (
	"context"
	"runtime"
	"strconv"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
)

// Workers' checks share the machine's cores. Go's test runner, left alone, runs as many packages at
// once as there are cores, each with as many threads, so four workers' checks on ten cores, or a
// second run's beside them, crowd the machine into a load of 20 to 30, every check takes two or three
// times as long, and tests time out in code no ticket touched. Each Claude worker starts with its share
// of the cores in its environment instead: the machine's cores divided among the workers running across
// its orchestra runs, this run's at its Config.Concurrency, at least one. ORCHESTRA_CORES says it to
// any project's check scripts, and GOFLAGS has Go's runner run no more packages at once than that.
// The share is taken as the worker starts, and kept: one started as another run begins has more than
// its share until it is started again.

// CoresVar is the environment variable that gives a worker its share of the machine's cores.
const CoresVar = "ORCHESTRA_CORES"

// coreShare is a worker's share of cores: divided among the workers of a run at concurrency and those
// running in the machine's other runs, others, no more workers than limit, the cap on them across the
// machine (Config.MachineWorkers, 0 for none); at least 1.
func coreShare(cores, concurrency, others, limit int) int {
	workers := max(concurrency, 1) + others
	if limit > 0 {
		workers = min(workers, limit)
	}
	return max(cores/workers, 1)
}

// shareCores sets the share of the machine's cores the run's next workers start with (see coreShare),
// from the workers running in the other runs as last read. Run calls it as it reads them.
func (o *Loop) shareCores(others int) {
	cores := o.cores
	if cores == 0 {
		cores = runtime.NumCPU()
	}
	o.share.Store(int64(coreShare(cores, o.cfg.Concurrency, others, o.cfg.MachineWorkers)))
}

// workerEnv is the environment a worker starts with beside the run's own: its share of the machine's
// cores (see shareCores), as CoresVar and as GOFLAGS's -p, after the GOFLAGS the run has, unless those
// set -p already. nil before Run has shared the cores.
func (o *Loop) workerEnv(ctx context.Context) map[string]string {
	n := o.share.Load()
	if n == 0 {
		return nil
	}
	share := strconv.FormatInt(n, 10)
	env := map[string]string{CoresVar: share}
	flags := strings.Fields(command.Getenv(ctx, "GOFLAGS"))
	if !hasParallelFlag(flags) {
		env["GOFLAGS"] = strings.Join(append(flags, "-p="+share), " ")
	}
	return env
}

// hasParallelFlag reports whether the go command's flags set -p, as the maintainer may in GOFLAGS.
func hasParallelFlag(flags []string) bool {
	for _, f := range flags {
		if name, _, _ := strings.Cut(strings.TrimLeft(f, "-"), "="); name == "p" && strings.HasPrefix(f, "-") {
			return true
		}
	}
	return false
}

// envReporter is the run's Reporter, its hooks' settings giving the worker env (see EnvReporter), when
// it can; else the Reporter itself, and the worker starts with the run's environment alone.
func (o *Loop) envReporter(env map[string]string) Reporter {
	if e, ok := o.reporter.(EnvReporter); ok && env != nil {
		return e.WithEnv(env)
	}
	return o.reporter
}
