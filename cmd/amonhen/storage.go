package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// A selected store is authoritative. Never fall back to the legacy AppData
// view, which MSIX may redirect independently of the shell's sandbox mode.
func configPath() (string, error) {
	if root := os.Getenv("AMONHEN_CONFIG_DIR"); root != "" {
		if !filepath.IsAbs(root) {
			return "", errors.New("AMONHEN_CONFIG_DIR must be an absolute path")
		}
		return filepath.Join(root, "projects.json"), nil
	}
	if runtime.GOOS == "windows" {
		root, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(root, ".amonhen", "projects.json"), nil
	}
	return legacyConfigPath()
}

func legacyConfigPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, appName, "projects.json"), nil
}

func readProjects(path string) ([]Project, error) {
	f, err := openStore(path)
	if err != nil {
		return nil, fmt.Errorf("authoritative project store unavailable at %s: %w; run 'amonhen storage migrate' in a normal host terminal for an existing installation, or 'amonhen storage init' for a new installation", path, err)
	}
	defer f.Close()
	if err := verifyStoreHandle(f, path); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, 4<<20+1))
	if err != nil {
		return nil, fmt.Errorf("read authoritative project store: %w", err)
	}
	if len(data) > 4<<20 {
		return nil, errors.New("project store exceeds 4 MiB limit")
	}
	return decodeProjects(data)
}

func decodeProjects(data []byte) ([]Project, error) {
	var projects []Project
	if err := json.Unmarshal(data, &projects); err != nil || strings.TrimSpace(string(data)) == "null" {
		return nil, errors.New("project store must contain a JSON array of project records")
	}
	names, ids := map[string]bool{}, map[string]bool{}
	for _, p := range projects {
		name, id := strings.ToLower(p.Name), strings.ToLower(p.UUID)
		if !validName(p.Name) || !validUUID(p.UUID) || names[name] || ids[id] {
			return nil, errors.New("project store contains invalid or duplicate names/UUIDs")
		}
		names[name], ids[id] = true, true
	}
	return projects, nil
}

func loadProjects() ([]Project, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	return readProjects(path)
}

func saveProjects(projects []Project) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if projects == nil {
		projects = []Project{}
	}
	data, err := json.MarshalIndent(projects, "", "  ")
	if err != nil {
		return err
	}
	if _, err := decodeProjects(data); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create project store directory: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".projects-*")
	if err != nil {
		return fmt.Errorf("create project store transaction: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	defer f.Close()
	if err := verifyStoreHandle(f, tmp); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write project store transaction: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("flush project store transaction: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := replaceStore(tmp, path); err != nil {
		return fmt.Errorf("commit project store transaction: %w", err)
	}
	return nil
}

func withProjectLock(fn func() error) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("access project store directory: %w", err)
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open project store lock: %w", err)
	}
	defer f.Close()
	if err := verifyStoreHandle(f, path+".lock"); err != nil {
		return err
	}
	if err := lockStore(f); err != nil {
		return fmt.Errorf("project store is locked or inaccessible; retry after the other writer completes: %w", err)
	}
	// Closing the handle releases the interprocess lock, including on process exit.
	return fn()
}

func storageCommand(args []string) error {
	if len(args) == 0 || containsHelp(args) {
		fmt.Println("Usage: amonhen storage <path|migrate|init>\n\nWindows stores metadata in USERPROFILE\\.amonhen, outside MSIX AppData virtualization. migrate imports the host's legacy registry once without changing credentials. init creates an empty store for a new installation. Neither overwrites an existing store.")
		return nil
	}
	if len(args) != 1 {
		return errors.New("usage: amonhen storage <path|migrate|init>")
	}
	path, err := configPath()
	if err != nil {
		return err
	}
	if args[0] == "path" {
		fmt.Println(path)
		return nil
	}
	if args[0] != "migrate" && args[0] != "init" {
		return errors.New("usage: amonhen storage <path|migrate|init>")
	}
	return withProjectLock(func() error {
		if _, err := os.Stat(path); err == nil {
			return errors.New("authoritative project store already exists; it was not overwritten")
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect authoritative project store: %w", err)
		}
		legacy, err := legacyConfigPath()
		if err != nil {
			return err
		}
		var projects []Project
		if args[0] == "migrate" {
			projects, err = readProjects(legacy)
			if err != nil {
				return fmt.Errorf("legacy migration refused; run this command from a normal host terminal: %w", err)
			}
		} else if _, err := os.Stat(legacy); err == nil {
			return errors.New("legacy registry exists; use 'amonhen storage migrate' from a normal host terminal to preserve existing projects")
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect legacy registry: %w", err)
		}
		if err := saveProjects(projects); err != nil {
			return err
		}
		fmt.Printf("Authoritative store ready at %s (%d projects). Existing OS credentials unchanged.\n", path, len(projects))
		return nil
	})
}
