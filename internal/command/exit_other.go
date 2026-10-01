//go:build unix && !(linux || darwin || dragonfly || freebsd || netbsd || openbsd)

package command

import "errors"

// awaitExit can't wait for a child here without reaping it.
func awaitExit(pid int) error {
	return errors.ErrUnsupported
}
