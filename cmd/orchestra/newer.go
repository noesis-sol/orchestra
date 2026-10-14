package main

import (
	"context"
	"io"
	"os"

	"github.com/noesis-sol/orchestra/internal/tui"
	"github.com/noesis-sol/orchestra/internal/update"
)

// A run ends, on a terminal, with a line saying when a newer release than this build is out: a check
// started as the run starts asks GitHub, at most once a day (internal/update's Checker). The event
// stream and the log don't have it.

// noUpdateCheck is the environment variable that, set on (1, true, yes or on, as envBool reads it),
// turns the check off: no request, no cache. A value envBool doesn't take is a setup problem
// (readFlags), and turns the check off too.
const noUpdateCheck = "ORCHESTRA_NO_UPDATE_CHECK"

// releases is where a run looks for the latest release: GitHub; tests serve their own.
var releases = update.Updater{}

// startUpdateCheck starts the check for a newer release in the background, under ctx. It is nil, and
// asks nothing, when ORCHESTRA_NO_UPDATE_CHECK turns it off or there is no cache folder to keep its
// answer in, as GitHub would then be asked on every run.
func startUpdateCheck(ctx context.Context, getenv func(string) string) *update.Check {
	if off, problem := envBool(getenv, noUpdateCheck, false); off || problem != "" {
		return nil
	}
	dir, err := update.CacheDir()
	if err != nil {
		return nil
	}
	return update.Checker{Updater: releases, Current: buildVersion(), Cache: dir}.Start(ctx)
}

// showNewer stops check, if it is still going, and says what it found, if it found a newer release:
// below the dashboard's end, or the box of a run with nothing to run, on stdout (dashboard), or else,
// with plain output, on stderr when that is a terminal. Not on a terminal, it says nothing.
func showNewer(check *update.Check, dashboard bool, stdout, stderr io.Writer) {
	newer := check.Newer()
	if newer == "" {
		return
	}
	if out, ok := stdout.(*os.File); ok && dashboard {
		tui.Terminal(out, termWidth(out)).Newer(newer, buildVersion())
	} else if isTerminal(stderr) {
		tui.Printer{Out: stderr}.Newer(newer, buildVersion())
	}
}
