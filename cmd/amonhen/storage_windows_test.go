//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestStoreHandleAcceptsShortPathAlias(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "long-project-store-filename.json")
	if err := os.WriteFile(path, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 32768)
	n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil {
		t.Skip("8.3 aliases unavailable on this volume")
	}
	short := windows.UTF16ToString(buf[:n])
	f, err := openStore(short)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := verifyStoreHandle(f, short); err != nil {
		t.Fatal(err)
	}
}

func TestInstallReplacesExecutableAtomically(t *testing.T) {
	dir := t.TempDir()
	source, target := filepath.Join(dir, "candidate.exe"), filepath.Join(dir, "installed.exe")
	if err := os.WriteFile(source, []byte("new-build"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old-build"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyExecutable(source, target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "new-build" {
		t.Fatal("installed binary was not replaced")
	}
	if err := copyExecutable(target, target); err != nil {
		t.Fatal(err)
	}
}

func TestMSIXRedirectedRegistryRejected(t *testing.T) {
	path := os.Getenv("AMONHEN_REDIRECTED_FIXTURE")
	if path == "" {
		t.Skip("requires a dedicated redirected fixture in the affected packaged desktop environment")
	}
	_, err := readProjects(path)
	if err == nil || !strings.Contains(err.Error(), "project store path is redirected") {
		t.Fatalf("redirected registry was not explicitly rejected: %v", err)
	}
}
