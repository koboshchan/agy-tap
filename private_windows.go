//go:build windows

package main

import (
	"fmt"
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
