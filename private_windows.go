//go:build windows

package main

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"os/user"
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
	u, err := user.Current()
	if err != nil {
		return err
	}
	out, err := exec.Command("icacls", path, "/inheritance:r", "/grant:r", "*"+u.Uid+":(OI)(CI)F").CombinedOutput()
	if err != nil {
		return fmt.Errorf("restrict state ACL: %w: %s", err, out)
	}
	return nil
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if e != nil {
		return e != windows.ERROR_INVALID_PARAMETER
	}
	defer windows.CloseHandle(h)
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil {
		return true
	}
	return code == 259
}
