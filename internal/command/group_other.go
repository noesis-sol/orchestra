//go:build !unix

package command

import (
	"os"
	"os/exec"
	"time"
)

// Without process groups, cancelling cmd kills only cmd itself.
type group struct{}

func inGroup(cmd *exec.Cmd, grace time.Duration) *group { return &group{} }

func (g *group) wait() {}

func (g *group) stop() {}

// terminate stops a process; without signals, by killing it.
func terminate(p *os.Process) error { return p.Kill() }
