//go:build !unix

package main

// pidAlive cannot probe processes here; the bridge only supports macOS and Linux.
func pidAlive(pid int) bool { return false }
