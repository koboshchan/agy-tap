//go:build !windows

package main

func prepareOutput() (func(), error) { return func() {}, nil }
