//go:build !unix

package project

import (
	"errors"
	"os"
)

// lockFile: no flock here, so no run lock.
func lockFile(*os.File, bool) error {
	return errors.ErrUnsupported
}
