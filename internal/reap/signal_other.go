//go:build !unix

package reap

import (
	"errors"
	"syscall"
)

const (
	sigTerm = syscall.Signal(15)
	sigKill = syscall.Signal(9)
)

// signalGroup can't signal a group: there are no process groups.
func signalGroup(int, syscall.Signal) error { return errors.New("no process groups to signal") }

// ownGroup is no group: there are none.
func ownGroup() int { return 0 }
