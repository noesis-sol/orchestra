//go:build !unix

package command

import (
	"os/exec"
	"time"
)

// Without process groups, cancelling cmd kills only cmd itself.
type group struct{}

func inGroup(cmd *exec.Cmd, grace time.Duration) *group { return &group{} }

func (g *group) wait() {}

func (g *group) stop() {}
