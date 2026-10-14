//go:build !unix

package main

// processGroup is the ID of orchestra's process group; there are none here, so -1, which no group is.
func processGroup() int { return -1 }
