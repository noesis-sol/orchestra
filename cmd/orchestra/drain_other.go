//go:build !unix

package main

import "os"

// drainSignals: none where there is no SIGUSR1.
var drainSignals []os.Signal
