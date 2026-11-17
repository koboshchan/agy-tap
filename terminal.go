package main

import (
	"golang.org/x/term"
	"os"
)

// On Windows GetConsoleScreenBufferInfo requires an OUTPUT handle. Using stdin
// silently fell back to 120x40, even when the real console was 161x58.
func terminalSize() (int, int, error) {
	return sizeFromHandles(term.GetSize, int(os.Stdout.Fd()), int(os.Stdin.Fd()))
}

func sizeFromHandles(get func(int) (int, int, error), handles ...int) (int, int, error) {
	var err error
	for _, fd := range handles {
		var w, h int
		w, h, err = get(fd)
		if err == nil && w > 0 && h > 0 {
			return w, h, nil
		}
	}
	return 0, 0, os.ErrInvalid
}
