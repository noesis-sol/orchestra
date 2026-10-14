//go:build unix

package main

import "syscall"

// processGroup is the ID of orchestra's process group.
func processGroup() int { return syscall.Getpgrp() }
