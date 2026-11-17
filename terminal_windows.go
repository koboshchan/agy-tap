//go:build windows

package main

import (
	"golang.org/x/sys/windows"
	"os"
)

// Native Windows console handles need VT output enabled independently of raw
// input. Pipes (including mintty) already carry VT bytes and must remain untouched.
func prepareOutput() (func(), error) {
	h := windows.Handle(os.Stdout.Fd())
	var old uint32
	if windows.GetConsoleMode(h, &old) != nil {
		return func() {}, nil
	}
	mode := old | windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING | windows.DISABLE_NEWLINE_AUTO_RETURN
	if err := windows.SetConsoleMode(h, mode); err != nil {
		mode &^= windows.DISABLE_NEWLINE_AUTO_RETURN
		if err := windows.SetConsoleMode(h, mode); err != nil {
			return nil, err
		}
	}
	return func() { _ = windows.SetConsoleMode(h, old) }, nil
}
