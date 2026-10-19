//go:build !darwin && !linux

package dispatch

import (
	"errors"
	"time"
)

// processStart is unknown here: a registration's run is then judged by its PID alone.
func processStart(int) (time.Time, error) {
	return time.Time{}, errors.New("process start times are read on darwin and linux only")
}
