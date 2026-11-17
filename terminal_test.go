package main

import (
	"os"
	"testing"
)

func TestOutputHandleSize(t *testing.T) {
	calls := []int{}
	w, h, err := sizeFromHandles(func(fd int) (int, int, error) {
		calls = append(calls, fd)
		if fd == 1 {
			return 161, 58, nil
		}
		return 0, 0, os.ErrInvalid
	}, 1, 0)
	if err != nil || w != 161 || h != 58 || len(calls) != 1 {
		t.Fatalf("%dx%d %v %v", w, h, err, calls)
	}
}

func TestSizeFallback(t *testing.T) {
	for _, tt := range []struct {
		w, h int
		err  error
	}{
		{0, 0, nil}, {0, 0, os.ErrInvalid},
	} {
		w, h, e := sizeFromHandles(func(fd int) (int, int, error) {
			if fd == 1 {
				return tt.w, tt.h, tt.err
			}
			return 88, 31, nil
		}, 1, 0)
		if e != nil || w != 88 || h != 31 {
			t.Fatalf("%dx%d %v", w, h, e)
		}
	}
}
