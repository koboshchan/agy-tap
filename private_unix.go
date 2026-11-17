//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	s, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !s.IsDir() || s.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe state directory")
	}
	return os.Chmod(path, 0700)
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	e := syscall.Kill(pid, 0)
	return e == nil || e == syscall.EPERM
}
