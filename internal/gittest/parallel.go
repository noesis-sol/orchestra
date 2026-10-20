package gittest

import (
	"flag"
	"runtime"
	"strconv"
)

// parallelFlag is go test's -parallel, as the test binary takes it.
const parallelFlag = "test.parallel"

// capParallel runs at most half as many parallel tests at once as go test would, and at least two,
// on macOS, unless -parallel is given. There a Go program starts a process by forking, which marks
// all of its memory copy-on-write, and a test binary built with -race writes its memory all the
// time, the race detector's shadow of every access included: each process it starts costs it a
// fault for every page it writes next, some 1,500 pages, in system time, and the more tests run at
// once, the more pages each fork costs. internal/dispatch's tests start some 6,500 processes: on an
// idle machine with ten CPUs they took 38s and 132s of system time ten at a time, 40s and 86s five
// at a time. Linux forks a Go program's processes without copying its memory (CLONE_VM), so the
// cap is macOS's alone.
func capParallel() error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return capParallelTo(max(2, runtime.GOMAXPROCS(0)/2))
}

// capParallelTo sets the test flags' -parallel to n, once they are parsed, unless the test binary
// was given it.
func capParallelTo(n int) error {
	if !flag.Parsed() {
		flag.Parse() // TestMain parses the test flags, as testing.M's Run would
	}
	return capParallelIn(flag.CommandLine, n)
}

// capParallelIn sets -parallel in the parsed flags fs to n, unless they were given it, or have none.
func capParallelIn(fs *flag.FlagSet, n int) error {
	given := false
	fs.Visit(func(f *flag.Flag) { given = given || f.Name == parallelFlag })
	if given || fs.Lookup(parallelFlag) == nil {
		return nil
	}
	return fs.Set(parallelFlag, strconv.Itoa(n))
}
