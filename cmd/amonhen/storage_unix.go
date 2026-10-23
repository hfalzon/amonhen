//go:build !windows

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

func verifyStoreHandle(_ *os.File, _ string) error { return nil }
func openStore(path string) (*os.File, error)      { return os.Open(path) }
func lockStore(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}
func replaceStore(from, to string) error { return os.Rename(from, to) }
