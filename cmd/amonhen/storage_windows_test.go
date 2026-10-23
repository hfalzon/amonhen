//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
