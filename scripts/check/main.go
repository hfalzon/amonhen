// Command check runs the same checks locally and in GitHub Actions.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func run(env []string, name string, args ...string) error {
	fmt.Println(name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), env...)
	return cmd.Run()
}
func check() error {
	entries, err := filepath.Glob("cmd/amonhen/*.go")
	if err != nil {
		return err
	}
	entries = append(entries, "scripts/check/main.go", "scripts/credential-probe/main.go")
	cmd := exec.Command("gofmt", append([]string{"-l"}, entries...)...)
	output, err := cmd.Output()
	if err != nil {
		return err
	}
	if len(output) > 0 {
		return fmt.Errorf("run gofmt on: %s", output)
	}
	for _, args := range [][]string{{"test", "./...", "-count=1"}, {"vet", "./..."}, {"run", "github.com/rhysd/actionlint/cmd/actionlint@v1.7.7", "-shellcheck=", "-pyflakes="}} {
		if err := run(nil, "go", args...); err != nil {
			return err
		}
	}
	dir, err := os.MkdirTemp("", "amonhen-release-check-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	for _, target := range []struct{ os, arch string }{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"}, {"windows", "amd64"}} {
		env := []string{"CGO_ENABLED=0", "GOOS=" + target.os, "GOARCH=" + target.arch}
		output := filepath.Join(dir, "amonhen-"+target.os+"-"+target.arch)
		if target.os == "windows" {
			output += ".exe"
		}
		if err := run(env, "go", "build", "-buildvcs=false", "-trimpath", "-ldflags", "-s -w -X main.version=pre-push", "-o", output, "./cmd/amonhen"); err != nil {
			return err
		}
	}
	for _, name := range []string{"README.md", "LICENSE", "uninstall.ps1", "uninstall.sh"} {
		if _, err := os.Stat(name); err != nil {
			return err
		}
	}
	return nil
}
func main() {
	if err := check(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
