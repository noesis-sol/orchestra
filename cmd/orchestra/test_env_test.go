package main

import (
	"context"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/update"
)

// A test's environment is what it runs orchestra in, in place of the process's environment and
// working directory, which the tests running in parallel share: t.Setenv and t.Chdir would keep it
// from running in parallel. setenv and chdir set it for the test and its subtests, until the test
// ends; getenv reads it, and testCtx gives it to the commands orchestra runs (command.WithEnv).
var (
	testEnvsMu sync.Mutex
	testEnvs   = map[string]*testEnv{} // by test name
)

// testEnv is the environment one test set: its variables, over its parent's, its working
// directory, "" for its parent's, the breakdown of its spec runs, nil for its parent's, and the build
// orchestra is, nil for its parent's.
type testEnv struct {
	vars      map[string]string
	dir       string
	breakdown func(s *session) specBreakdown
	build     *testBuild
}

// testBuild is the build a test's orchestra is, as cmdEnv has it: its version and where its releases are.
type testBuild struct {
	version  string
	releases update.Updater
}

// envOf is the environment t sets, made if need be, for setenv and chdir.
func envOf(t testing.TB) *testEnv {
	t.Helper()
	testEnvsMu.Lock()
	defer testEnvsMu.Unlock()
	name := t.Name()
	e, ok := testEnvs[name]
	if !ok {
		e = &testEnv{vars: map[string]string{}}
		testEnvs[name] = e
		t.Cleanup(func() {
			testEnvsMu.Lock()
			defer testEnvsMu.Unlock()
			delete(testEnvs, name)
		})
	}
	return e
}

// lineage is the environments of t and of the tests it is a subtest of, t's first.
func lineage(t testing.TB) []*testEnv {
	testEnvsMu.Lock()
	defer testEnvsMu.Unlock()
	var envs []*testEnv
	for name := t.Name(); ; {
		if e, ok := testEnvs[name]; ok {
			envs = append(envs, e)
		}
		i := strings.LastIndexByte(name, '/')
		if i < 0 {
			return envs
		}
		name = name[:i]
	}
}

// setenv sets k to v in t's environment, as t.Setenv does the process's.
func setenv(t testing.TB, k, v string) {
	t.Helper()
	e := envOf(t)
	testEnvsMu.Lock()
	defer testEnvsMu.Unlock()
	e.vars[k] = v
}

// getenv is t's environment as orchestra's getenv: a variable as setenv set it for t or a test it
// is a subtest of, else the process's.
func getenv(t testing.TB) func(string) string {
	return func(k string) string {
		v, _ := lookupEnv(t, k)
		return v
	}
}

// lookupEnv is k as setenv set it for t or a test it is a subtest of, else as the process has it.
func lookupEnv(t testing.TB, k string) (string, bool) {
	envs := lineage(t)
	testEnvsMu.Lock()
	defer testEnvsMu.Unlock()
	for _, e := range envs {
		if v, ok := e.vars[k]; ok {
			return v, true
		}
	}
	return os.LookupEnv(k)
}

// onPath puts dir first on the PATH of t's environment.
func onPath(t testing.TB, dir string) {
	t.Helper()
	setenv(t, "PATH", dir+string(os.PathListSeparator)+getenv(t)("PATH"))
}

// chdir makes dir t's working directory, as t.Chdir does the process's.
func chdir(t testing.TB, dir string) {
	t.Helper()
	e := envOf(t)
	testEnvsMu.Lock()
	defer testEnvsMu.Unlock()
	e.dir = dir
}

// workDir is t's working directory: as chdir set it for t or a test it is a subtest of, else the
// process's (".").
func workDir(t testing.TB) string {
	envs := lineage(t)
	testEnvsMu.Lock()
	defer testEnvsMu.Unlock()
	for _, e := range envs {
		if e.dir != "" {
			return e.dir
		}
	}
	return "."
}

// testCtx is ctx giving the commands run with it t's environment (command.WithEnv).
func testCtx(ctx context.Context, t testing.TB) context.Context {
	return command.WithEnv(ctx, testVars(t)...)
}

// testVars is the variables setenv set for t and the tests it is a subtest of, as KEY=value each,
// the outermost test's first, so that t's own win.
func testVars(t testing.TB) []string {
	envs := lineage(t)
	testEnvsMu.Lock()
	defer testEnvsMu.Unlock()
	var vars []string
	for _, e := range slices.Backward(envs) {
		for k, v := range e.vars {
			vars = append(vars, k+"="+v)
		}
	}
	return vars
}

// breakDownWith has t's spec runs broken down by the breakdown b gives, as cmdEnv.breakdown.
func breakDownWith(t testing.TB, b func(s *session) specBreakdown) {
	t.Helper()
	e := envOf(t)
	testEnvsMu.Lock()
	defer testEnvsMu.Unlock()
	e.breakdown = b
}

// buildOf makes t's orchestra the build of version v, its releases read from releases, as cmdEnv's
// version and releases; else it is this build, its releases GitHub's.
func buildOf(t testing.TB, v string, releases update.Updater) {
	t.Helper()
	e := envOf(t)
	testEnvsMu.Lock()
	defer testEnvsMu.Unlock()
	e.build = &testBuild{version: v, releases: releases}
}

// testCmdEnv is orchestra's cmdEnv in t's environment and working directory, with these streams.
func testCmdEnv(t testing.TB, stdin io.Reader, stdout, stderr io.Writer) cmdEnv {
	e := cmdEnv{dir: workDir(t), getenv: getenv(t), stdin: stdin, stdout: stdout, stderr: stderr,
		version: buildVersion()}
	envs := lineage(t)
	testEnvsMu.Lock()
	defer testEnvsMu.Unlock()
	for _, te := range envs {
		if te.breakdown != nil {
			e.breakdown = te.breakdown
			break
		}
	}
	for _, te := range envs {
		if te.build != nil {
			e.version, e.releases = te.build.version, te.build.releases
			break
		}
	}
	return e
}
