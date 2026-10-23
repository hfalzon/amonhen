//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// Package identity does not reliably propagate to child processes, but the
// inherited filesystem virtualization does. Inspect the opened handle itself.
func verifyStoreHandle(f *os.File, expected string) error {
	buf := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), &buf[0], uint32(len(buf)), 0)
	if err != nil || n >= uint32(len(buf)) {
		return fmt.Errorf("cannot verify project store backing path; environment unsupported")
	}
	actual := windows.UTF16ToString(buf[:n])
	want, err := filepath.Abs(expected)
	if err != nil {
		return err
	}
	// 8.3 aliases (for example RUNNER~1 in Windows TEMP) name the same path,
	// unlike a package-private AppData location. Expand spelling only; do not
	// resolve the expected path through another opened handle or a junction.
	wantPtr, err := windows.UTF16PtrFromString(want)
	if err != nil {
		return err
	}
	longBuf := make([]uint16, 32768)
	longN, err := windows.GetLongPathName(wantPtr, &longBuf[0], uint32(len(longBuf)))
	if err != nil || longN >= uint32(len(longBuf)) {
		return fmt.Errorf("cannot normalize project store path; environment unsupported")
	}
	want = windows.UTF16ToString(longBuf[:longN])
	if !strings.EqualFold(normalizeWindowsStorePath(actual), normalizeWindowsStorePath(want)) {
		return fmt.Errorf("project store path is redirected (%s -> %s); refusing an alternative registry; use a shared directory outside AppData and run migration from a normal host terminal", expected, actual)
	}
	return nil
}

func normalizeWindowsStorePath(path string) string {
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	}
	return filepath.Clean(strings.TrimPrefix(path, `\\?\`))
}

func lockStore(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}

func openStore(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

func replaceStore(from, to string) error {
	// Windows can temporarily deny replacement while another client (or AV)
	// holds the destination open. Keep the complete old file until commit;
	// never truncate it or fall back to a non-atomic overwrite.
	deadline := time.Now().Add(time.Second)
	for {
		err := os.Rename(from, to)
		if err == nil || (!errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION)) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
}
