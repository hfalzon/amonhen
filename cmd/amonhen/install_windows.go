//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func installSelf() error {
	source, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate current executable: %w", err)
	}
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return fmt.Errorf("LOCALAPPDATA is not set")
	}
	installDir := filepath.Join(localAppData, "Programs", "AmonHen", "bin")
	if err := os.MkdirAll(installDir, 0700); err != nil {
		return fmt.Errorf("create install directory: %w", err)
	}
	target := filepath.Join(installDir, "amonhen.exe")
	if err := copyExecutable(source, target); err != nil {
		return err
	}
	if err := addUserPath(installDir); err != nil {
		return err
	}
	_ = os.Setenv("PATH", installDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	fmt.Printf("Installed Amon Hen to %s\n", target)
	fmt.Println("Added the install directory to your user PATH. Open a new terminal to use amonhen.")
	return nil
}

func copyExecutable(source, target string) error {
	if sourceInfo, err := os.Stat(source); err == nil {
		if targetInfo, targetErr := os.Stat(target); targetErr == nil && os.SameFile(sourceInfo, targetInfo) {
			return nil
		}
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read current executable: %w", err)
	}
	if err := os.WriteFile(target, data, 0700); err != nil {
		return fmt.Errorf("install executable: %w", err)
	}
	return nil
}

func addUserPath(installDir string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open current-user environment settings: %w", err)
	}
	defer key.Close()
	path, valueType, err := key.GetStringValue("Path")
	if err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("read current-user PATH: %w", err)
	}
	for _, entry := range strings.Split(path, ";") {
		if strings.EqualFold(filepath.Clean(os.ExpandEnv(entry)), filepath.Clean(installDir)) {
			return nil
		}
	}
	updated := strings.TrimRight(path, ";")
	if updated != "" {
		updated += ";"
	}
	updated += installDir
	if valueType == registry.EXPAND_SZ {
		err = key.SetExpandStringValue("Path", updated)
	} else {
		err = key.SetStringValue("Path", updated)
	}
	if err != nil {
		return fmt.Errorf("update current-user PATH: %w", err)
	}
	return nil
}
