//go:build unix

package main

import (
	"errors"
	"syscall"
)

// pidAlive reports whether pid is a live process (signal 0 probes without
// signalling; EPERM means it exists under another user).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
