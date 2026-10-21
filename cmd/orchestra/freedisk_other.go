//go:build !darwin && !linux

package main

import "errors"

// freeDisk is how many bytes of the file system that holds path are free: orchestra can't tell here.
func freeDisk(string) (uint64, error) { return 0, errors.ErrUnsupported }
