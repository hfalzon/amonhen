//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// installOnExplorerDoubleClick distinguishes an Explorer launch from a CLI
// invocation so double-clicking the release executable gives visible feedback.
func installOnExplorerDoubleClick() bool {
	parent, err := parentProcessName()
	if err != nil || !strings.EqualFold(parent, "explorer.exe") {
		return false
	}
	err = installSelf()
	message := "Amon Hen installed. Open a new terminal and run amonhen --help."
	icon := uintptr(0x40) // MB_ICONINFORMATION
	if err != nil {
		message = "Amon Hen installation failed:\n\n" + err.Error()
		icon = 0x10
	}
	title, _ := windows.UTF16PtrFromString("Amon Hen installer")
	body, _ := windows.UTF16PtrFromString(message)
	user32 := windows.NewLazySystemDLL("user32.dll")
	_, _, _ = user32.NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(body)), uintptr(unsafe.Pointer(title)), icon|0x0)
	return true
}

func parentProcessName() (string, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return "", err
	}
	for {
		if entry.ProcessID == uint32(os.Getppid()) {
			return windows.UTF16ToString(entry.ExeFile[:]), nil
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			return "", err
		}
	}
}

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
	// A packaged desktop parent can redirect this install path too. Refuse to
	// update a private executable copy while the host keeps running an older one.
	if existing, err := openStore(target); err == nil {
		verifyErr := verifyStoreHandle(existing, target)
		_ = existing.Close()
		if verifyErr != nil {
			return fmt.Errorf("install from a normal host terminal: %w", verifyErr)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect installed executable: %w", err)
	}
	if sourceInfo, err := os.Stat(source); err == nil {
		if targetInfo, targetErr := os.Stat(target); targetErr == nil && os.SameFile(sourceInfo, targetInfo) {
			return nil
		}
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read current executable: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".amonhen-install-*")
	if err != nil {
		return fmt.Errorf("create install transaction: %w", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := verifyStoreHandle(f, f.Name()); err != nil {
		return fmt.Errorf("install from a normal host terminal: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write installed executable: %w", err)
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := replaceStore(f.Name(), target); err != nil {
		return fmt.Errorf("commit installed executable: %w", err)
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
