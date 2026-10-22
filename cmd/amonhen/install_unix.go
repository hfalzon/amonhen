//go:build !windows

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func installOnExplorerDoubleClick() bool { return false }

func installSelf() error {
	source, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate current executable: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate user home directory: %w", err)
	}
	installDir := filepath.Join(home, ".local", "bin")
	target := filepath.Join(installDir, "amonhen")
	if err := os.MkdirAll(installDir, 0700); err != nil {
		return fmt.Errorf("create install directory: %w", err)
	}
	if err := copyExecutable(source, target); err != nil {
		return err
	}
	pathFile, pathLine := shellPathConfig(home)
	if err := ensurePathLine(pathFile, pathLine, installDir); err != nil {
		return err
	}
	_ = os.Setenv("PATH", installDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	fmt.Printf("Installed Amon Hen to %s\n", target)
	fmt.Printf("Added %s to %s when needed. Open a new terminal or reload that file to use amonhen.\n", installDir, pathFile)
	return nil
}

func copyExecutable(source, target string) error {
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("inspect current executable: %w", err)
	}
	if targetInfo, err := os.Stat(target); err == nil && os.SameFile(sourceInfo, targetInfo) {
		return nil
	}
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open current executable: %w", err)
	}
	defer input.Close()
	output, err := os.CreateTemp(filepath.Dir(target), ".amonhen-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary executable: %w", err)
	}
	tmpName := output.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return fmt.Errorf("copy executable: %w", err)
	}
	if err := output.Chmod(0755); err != nil {
		output.Close()
		return fmt.Errorf("set executable permissions: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close copied executable: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("install executable: %w", err)
	}
	return nil
}

func shellPathConfig(home string) (string, string) {
	shell := filepath.Base(os.Getenv("SHELL"))
	switch shell {
	case "zsh":
		return filepath.Join(home, ".zshrc"), `export PATH="$HOME/.local/bin:$PATH"`
	case "bash":
		return filepath.Join(home, ".bashrc"), `export PATH="$HOME/.local/bin:$PATH"`
	case "fish":
		return filepath.Join(home, ".config", "fish", "config.fish"), `fish_add_path "$HOME/.local/bin"`
	default:
		return filepath.Join(home, ".profile"), `export PATH="$HOME/.local/bin:$PATH"`
	}
}

func ensurePathLine(pathFile, pathLine, installDir string) error {
	contents, err := os.ReadFile(pathFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read shell startup file: %w", err)
	}
	if strings.Contains(string(contents), "# Added by Amon Hen installer") || pathContains(os.Getenv("PATH"), installDir) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(pathFile), 0700); err != nil {
		return fmt.Errorf("create shell config directory: %w", err)
	}
	file, err := os.OpenFile(pathFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open shell startup file: %w", err)
	}
	defer file.Close()
	if len(contents) > 0 && contents[len(contents)-1] != '\n' {
		if _, err := file.WriteString("\n"); err != nil {
			return fmt.Errorf("update shell startup file: %w", err)
		}
	}
	if _, err := fmt.Fprintf(file, "\n# Added by Amon Hen installer\n%s\n", pathLine); err != nil {
		return fmt.Errorf("update shell startup file: %w", err)
	}
	return nil
}

func pathContains(pathValue, directory string) bool {
	for _, entry := range strings.Split(pathValue, string(os.PathListSeparator)) {
		if filepath.Clean(entry) == filepath.Clean(directory) {
			return true
		}
	}
	return false
}
