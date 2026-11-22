//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// xpty connects the slave descriptors but does not make it the child's
// controlling terminal. Give the child its own session and foreground group:
// TIOCSWINSZ then delivers SIGWINCH for the inner PTY, after its size changes,
// rather than the child receiving the outer terminal's earlier notification.
func prepareChildTerminal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0, // Child descriptor; xpty.Start connects stdin to the slave.
	}
}
