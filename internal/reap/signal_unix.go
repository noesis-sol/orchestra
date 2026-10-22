//go:build unix

package reap

import "syscall"

const (
	sigTerm = syscall.SIGTERM
	sigKill = syscall.SIGKILL
)

// signalGroup sends sig to every process in group g.
func signalGroup(g int, sig syscall.Signal) error { return syscall.Kill(-g, sig) }

// ownGroup is orchestra's own process group.
func ownGroup() int { return syscall.Getpgrp() }
