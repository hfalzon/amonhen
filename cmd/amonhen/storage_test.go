package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

const fixtureUUID = "00000000-0000-4000-8000-000000000001"

func isolatedStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AMONHEN_CONFIG_DIR", dir)
	return filepath.Join(dir, "projects.json")
}

func TestMissingAuthoritativeStoreDoesNotUseLegacy(t *testing.T) {
	isolatedStore(t)
	t.Setenv("HOME", t.TempDir()) // macOS UserConfigDir uses HOME, not XDG_CONFIG_HOME.
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	legacy, err := legacyConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`[{"name":"stale","uuid":"`+fixtureUUID+`"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadProjects(); err == nil || !strings.Contains(err.Error(), "authoritative project store unavailable") {
		t.Fatalf("missing store: %v", err)
	}
}

func TestMigrationPreservesLegacyAndCredentials(t *testing.T) {
	isolatedStore(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	legacy, err := legacyConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(`[{"name":"existing","uuid":"` + fixtureUUID + `"}]`)
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, data, 0600); err != nil {
		t.Fatal(err)
	}
	keyring.MockInit()
	const sentinel = "synthetic-existing-credential-do-not-print"
	if err := keyring.Set(service, "existing", sentinel); err != nil {
		t.Fatal("seed credential")
	}
	output, err := captureRun([]string{"storage", "migrate"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, sentinel) {
		t.Fatal("credential appeared in output")
	}
	token, err := keyring.Get(service, "existing")
	if err != nil || token != sentinel {
		t.Fatal("credential changed during migration")
	}
	got, err := os.ReadFile(legacy)
	if err != nil || string(got) != string(data) {
		t.Fatal("legacy file changed")
	}
	if err := storageCommand([]string{"migrate"}); err == nil {
		t.Fatal("migration overwrote authoritative store")
	}
	if _, err := findProject("existing"); err != nil {
		t.Fatal(err)
	}
}

func TestNextLookupObservesAtomicChanges(t *testing.T) {
	isolatedStore(t)
	for _, projects := range [][]Project{
		{{Name: "one", UUID: fixtureUUID}},
		{{Name: "renamed", UUID: fixtureUUID}},
		{},
	} {
		if err := withProjectLock(func() error { return saveProjects(projects) }); err != nil {
			t.Fatal(err)
		}
		got, err := loadProjects()
		if err != nil || len(got) != len(projects) {
			t.Fatalf("lookup did not observe current store: %v", err)
		}
		if len(got) > 0 && got[0] != projects[0] {
			t.Fatal("stale result")
		}
	}
}

func TestAtomicReplacementWhileReaderHasStoreOpen(t *testing.T) {
	path := isolatedStore(t)
	if err := saveProjects([]Project{{Name: "before", UUID: fixtureUUID}}); err != nil {
		t.Fatal(err)
	}
	reader, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() {
		time.Sleep(60 * time.Millisecond)
		_ = reader.Close()
		close(closed)
	}()
	if err := saveProjects([]Project{{Name: "after", UUID: fixtureUUID}}); err != nil {
		t.Fatal(err)
	}
	<-closed
	got, err := loadProjects()
	if err != nil || len(got) != 1 || got[0].Name != "after" {
		t.Fatalf("next reader did not observe committed replacement: %v", err)
	}
	old, err := decodeProjects(data)
	if err != nil || len(old) != 1 || old[0].Name != "before" {
		t.Fatal("open reader did not retain a complete snapshot")
	}
}

func TestCorruptStoreFailsClosed(t *testing.T) {
	path := isolatedStore(t)
	for _, data := range []string{"null", "{", `[{"name":"invalid space","uuid":"` + fixtureUUID + `"}]`, `[{"name":"a","uuid":"` + fixtureUUID + `"},{"name":"A","uuid":"` + fixtureUUID + `"}]`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadProjects(); err == nil {
			t.Fatal("invalid store accepted")
		}
	}
}

// Each writer is a real separate process: the lock must cover the read and
// atomic commit together, not just os.WriteFile or a process-local mutex.
func TestConcurrentProjectWriters(t *testing.T) {
	if os.Getenv("AMONHEN_TEST_WRITER") == "1" {
		name := os.Getenv("AMONHEN_TEST_NAME")
		deadline := time.Now().Add(10 * time.Second)
		for {
			err := withProjectLock(func() error {
				p, err := loadProjects()
				if err != nil {
					return err
				}
				p = append(p, Project{Name: name, UUID: os.Getenv("AMONHEN_TEST_UUID")})
				return saveProjects(p)
			})
			if err == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("writer did not complete")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	isolatedStore(t)
	if err := saveProjects(nil); err != nil {
		t.Fatal(err)
	}
	var children []*exec.Cmd
	for i := 1; i <= 8; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestConcurrentProjectWriters$")
		cmd.Env = append(os.Environ(), "AMONHEN_TEST_WRITER=1", fmt.Sprintf("AMONHEN_TEST_NAME=writer-%d", i), fmt.Sprintf("AMONHEN_TEST_UUID=00000000-0000-4000-8000-%012d", i))
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		children = append(children, cmd)
	}
	for _, cmd := range children {
		if err := cmd.Wait(); err != nil {
			t.Fatal("concurrent writer failed")
		}
	}
	p, err := loadProjects()
	if err != nil || len(p) != 8 {
		t.Fatalf("lost concurrent update: records=%d error=%v", len(p), err)
	}
}

func TestWriterLockRejectsConcurrentMutation(t *testing.T) {
	isolatedStore(t)
	err := withProjectLock(func() error {
		return withProjectLock(func() error { t.Fatal("second writer acquired held lock"); return nil })
	})
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("lock error: %v", err)
	}
}

func TestRenamePreservesCredentialWithoutReentry(t *testing.T) {
	isolatedStore(t)
	keyring.MockInit()
	const sentinel = "synthetic-rename-token-do-not-print"
	if err := saveProjects([]Project{{Name: "old", UUID: fixtureUUID}}); err != nil {
		t.Fatal(err)
	}
	if err := keyring.Set(service, "old", sentinel); err != nil {
		t.Fatal("seed credential")
	}
	output, err := captureRun([]string{"project", "rename", "-N", "old", "--new-name", "new"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, sentinel) {
		t.Fatal("credential appeared in output")
	}
	token, err := keyring.Get(service, "new")
	if err != nil || token != sentinel {
		t.Fatal("renamed credential unavailable")
	}
	if _, err := keyring.Get(service, "old"); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatal("old credential retained")
	}
	if _, err := findProject("new"); err != nil {
		t.Fatal(err)
	}
}
