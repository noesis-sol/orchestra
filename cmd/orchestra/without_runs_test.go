package main

import (
	"fmt"
	"os"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// withoutRuns returns m set to run with ORCHESTRA_RUNS_DIR off in the process's environment, which the
// orchestra a test runs, in its process or one of its own, reads unless the test sets it: the runs the
// tests start register nowhere, so that they don't tell each other, or the maintainer's own runs on
// this machine, that they share it, nor count toward a cap on its workers.
func withoutRuns(m faketool.TestingM) faketool.TestingM { return noRuns{m} }

type noRuns struct{ m faketool.TestingM }

func (n noRuns) Run() int {
	if err := os.Setenv(runsDirEnv, "off"); err != nil {
		fmt.Fprintln(os.Stderr, "withoutRuns:", err)
		return 1
	}
	return n.m.Run()
}
