// Package gittest is for tests that run the real git: Main keeps the git they start, theirs and
// orchestra's, from working on in their repositories in the background once it has exited.
//
// After a commit, a merge or a rebase, git runs `git maintenance run --auto --detach`, which goes on
// without it. Since git 2.54 that maintenance repacks a repository once objects/17 holds two loose
// objects, which a test's few commits now and then put there, and a repack still writing in a test's
// repository as the test ends breaks TempDir's removal: "unlinkat .../.git/objects: directory not
// empty".
package gittest

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

// TestingM is a package's tests, as its TestMain gets them: a *testing.M.
type TestingM interface{ Run() int }

// Main returns m set to run with git's automatic maintenance off. A package whose tests run the real
// git runs them through it, from its TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(gittest.Main(m).Run()) }
func Main(m TestingM) TestingM { return tests{m} }

type tests struct{ m TestingM }

func (ts tests) Run() int {
	if err := setConfig("maintenance.auto", "false"); err != nil {
		fmt.Fprintln(os.Stderr, "gittest:", err)
		return 1
	}
	return ts.m.Run()
}

// setConfig sets name to value for every git started from here on, through GIT_CONFIG_COUNT, after
// the entries the environment already holds. It overrides the repositories' and the user's settings.
func setConfig(name, value string) error {
	n := 0
	if s := os.Getenv("GIT_CONFIG_COUNT"); s != "" {
		var err error
		if n, err = strconv.Atoi(s); err != nil {
			return fmt.Errorf("GIT_CONFIG_COUNT=%q: %w", s, err)
		}
	}
	i := strconv.Itoa(n)
	return errors.Join(
		os.Setenv("GIT_CONFIG_KEY_"+i, name),
		os.Setenv("GIT_CONFIG_VALUE_"+i, value),
		os.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(n+1)),
	)
}
