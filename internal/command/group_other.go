//go:build !unix

package command

import (
	"os/exec"
	"time"
)

// Without process groups, cancelling cmd kills only cmd itself.
func inGroup(cmd *exec.Cmd, grace time.Duration) {}

func killGroup(cmd *exec.Cmd) {}
