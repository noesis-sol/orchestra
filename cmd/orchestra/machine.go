package main

import (
	"path/filepath"

	"github.com/noesis-sol/orchestra/internal/update"
)

// The machine's orchestra runs register in a folder they share (internal/dispatch's machine), so that
// each says when another shares the machine, and can keep to a cap on the workers across them
// (--machine-workers, ORCHESTRA_MACHINE_WORKERS).

// runsDirEnv is the environment variable that names the folder the machine's runs register in, over
// runs/ in orchestra's folder in the user's cache folder; "off" registers the run nowhere, so that it
// neither sees the others nor is seen.
const runsDirEnv = "ORCHESTRA_RUNS_DIR"

// runsDirName is the folder of the machine's runs in orchestra's cache folder (update.CacheDir).
const runsDirName = "runs"

// runsDir is the folder the machine's runs register in, in the environment getenv reads: "" for none,
// when ORCHESTRA_RUNS_DIR is off or there is no cache folder.
func runsDir(getenv func(string) string) string {
	switch dir := getenv(runsDirEnv); dir {
	case "off":
		return ""
	case "":
	default:
		return dir
	}
	dir, err := update.CacheDir(getenv)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, runsDirName)
}
