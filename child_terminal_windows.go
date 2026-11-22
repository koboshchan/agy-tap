//go:build windows

package main

import "os/exec"

// ConPTY owns its console and child setup; Unix session flags do not apply.
func prepareChildTerminal(cmd *exec.Cmd) {}
