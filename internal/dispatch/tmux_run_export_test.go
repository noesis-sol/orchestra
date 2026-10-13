package dispatch

import "time"

// SetPoll sets how long the loop o waits between reads of a worker's status, for the tests of the
// package dispatch_test, which run whole runs on the real clock, in a real tmux (tmux_run_test.go).
func SetPoll(o *Loop, d time.Duration) { o.poll = d }
