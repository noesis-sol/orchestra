//go:build !(darwin || linux)

package command

import "os/exec"

// runOnTerminal starts cmd and waits for it to exit. A command that stops itself stays stopped
// here, orchestra waiting on it: following its stops takes darwin's or Linux's ways of telling.
func runOnTerminal(cmd *exec.Cmd) error { return cmd.Run() }
