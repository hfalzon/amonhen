package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBWSChildCommandWindowsRuntime(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launcher")
	}
	t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	t.Setenv("TEMP", `C:\Temp Space`)
	t.Setenv("BWS_ACCESS_TOKEN", "test-sentinel-do-not-forward")
	got := bwsChildCommand([]string{`C:\Tool Space\go.exe`, "tool", "a'b", "$literal"})
	if len(got) != 1 {
		t.Fatal("expected one shell script")
	}
	for _, want := range []string{"$env:PATHEXT='.COM;.EXE;.BAT;.CMD'", `$env:TEMP='C:\Temp Space'`, `& 'C:\Tool Space\go.exe' 'tool' 'a''b' '$literal'`, "exit $LASTEXITCODE"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("missing launcher behavior: %s", want)
		}
	}
	if strings.Contains(got[0], "test-sentinel-do-not-forward") || strings.Contains(got[0], "BWS_ACCESS_TOKEN") {
		t.Fatal("authentication token forwarded in command")
	}
}

func TestWindowsNativeHelper(t *testing.T) {
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 {
		return
	}
	want := []string{"space value", "a'b", `$literal; & echo injected`}
	if !reflect.DeepEqual(os.Args[separator+1:], want) {
		os.Exit(81)
	}
	if os.Getenv("BWS_ACCESS_TOKEN") != "" || os.Getenv("AMONHEN_TEST_AMBIENT") != "" {
		os.Exit(82)
	}
	if !strings.Contains(strings.ToUpper(os.Getenv("PATHEXT")), ".EXE") {
		os.Exit(83)
	}
	file, err := os.CreateTemp("", "amonhen-launch-test-*")
	if err != nil {
		os.Exit(84)
	}
	name := file.Name()
	_ = file.Close()
	_ = os.Remove(name)
	os.Stdout.WriteString("CHILD_STDOUT_OK\n")
	os.Stderr.WriteString("CHILD_STDERR_OK\n")
	os.Exit(23)
}

func TestWindowsCleanEnvironmentLaunch(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell regression")
	}
	shell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "tools with space's")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "amonhen-test-child.exe"), data, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	t.Setenv("TEMP", t.TempDir())
	t.Setenv("TMP", os.Getenv("TEMP"))
	t.Setenv("BWS_ACCESS_TOKEN", "synthetic-token")
	t.Setenv("AMONHEN_TEST_AMBIENT", "synthetic-ambient")
	args := []string{"amonhen-test-child", "-test.run=^TestWindowsNativeHelper$", "--", "space value", "a'b", `$literal; & echo injected`}
	script := bwsChildCommand(args)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-NoProfile", "-NonInteractive", "-Command", script[0])
	// Reproduce bws --no-inherit-env: keep PATH and essential OS variables only.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "SystemRoot=" + os.Getenv("SystemRoot"), "ComSpec=" + os.Getenv("ComSpec"), "windir=" + os.Getenv("windir")}
	output, err := cmd.CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 23 {
		t.Fatalf("child exit: %v; output: %s", err, output)
	}
	for _, marker := range []string{"CHILD_STDOUT_OK", "CHILD_STDERR_OK"} {
		if !strings.Contains(string(output), marker) {
			t.Fatalf("missing %s", marker)
		}
	}
}
