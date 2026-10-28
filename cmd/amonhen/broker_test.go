package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBrokerCommandHelp(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"broker", "--help"}, "broker grant --repo"},
		{[]string{"broker", "grant", "--help"}, "--duration"},
		{[]string{"broker", "serve", "--help"}, "Ctrl+C"},
		{[]string{"broker", "revoke", "--help"}, "--project"},
		{[]string{"broker", "fetch", "--help"}, "Output contains secrets"},
	} {
		out, err := captureRun(tc.args)
		if err != nil || !strings.Contains(out, tc.want) {
			t.Fatalf("help %v: %q, %v", tc.args, out, err)
		}
	}
}

func TestBrokerAuthenticatedResponseAndRevocation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AMONHEN_CONFIG_DIR", filepath.Join(root, "host"))
	if err := os.MkdirAll(filepath.Join(root, "host"), 0700); err != nil {
		t.Fatal(err)
	}
	key := []byte("01234567890123456789012345678901")
	id := "0123456789abcdef0123456789abcdef"
	grant := brokerGrant{ID: id, Repo: root, Project: "test-project", Expires: time.Now().Add(time.Hour)}
	if err := brokerSaveGrants([]brokerGrant{grant}); err != nil {
		t.Fatal(err)
	}
	oldKey, oldSecrets := brokerGetKey, brokerGetSecrets
	t.Cleanup(func() { brokerGetKey, brokerGetSecrets = oldKey, oldSecrets })
	brokerGetKey = func(service, name string) (string, error) { return base64.RawStdEncoding.EncodeToString(key), nil }
	brokerGetSecrets = func(g brokerGrant) ([]byte, error) { return []byte(`[{"key":"TEST","value":"synthetic-value"}]`), nil }
	base := id + "." + strings.Repeat("a", 32)
	path := filepath.Join(root, base+".req")
	writeRequest := func() {
		sealed, err := brokerSeal(key, []byte("secret-list"), "request:"+base)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, sealed, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeRequest()
	brokerHandle(grant, path, base)
	response := strings.TrimSuffix(path, ".req") + ".res"
	ciphertext, err := os.ReadFile(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ciphertext), "synthetic-value") {
		t.Fatal("plaintext response persisted")
	}
	plain, err := brokerOpen(key, ciphertext, "response:"+base)
	if err != nil {
		t.Fatal(err)
	}
	var result brokerResponse
	if json.Unmarshal(plain, &result) != nil || !strings.Contains(string(result.Data), "synthetic-value") {
		t.Fatal("invalid decrypted response")
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err = brokerOpen(key, ciphertext, "response:"+base); err == nil {
		t.Fatal("tampered response accepted")
	}
	if err = os.Remove(response); err != nil {
		t.Fatal(err)
	}
	if err = brokerSaveGrants(nil); err != nil {
		t.Fatal(err)
	}
	writeRequest()
	brokerHandle(grant, path, base)
	if _, err = os.Stat(response); !os.IsNotExist(err) {
		t.Fatal("revoked grant produced a response")
	}
}

func TestBrokerConcurrentRequests(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AMONHEN_CONFIG_DIR", filepath.Join(root, "host"))
	if err := os.MkdirAll(filepath.Join(root, "host"), 0700); err != nil {
		t.Fatal(err)
	}
	key := []byte("01234567890123456789012345678901")
	grant := brokerGrant{ID: strings.Repeat("a", 32), Repo: root, Project: "project", Expires: time.Now().Add(time.Hour)}
	if err := brokerSaveGrants([]brokerGrant{grant}); err != nil {
		t.Fatal(err)
	}
	oldKey, oldSecrets := brokerGetKey, brokerGetSecrets
	t.Cleanup(func() { brokerGetKey, brokerGetSecrets = oldKey, oldSecrets })
	brokerGetKey = func(string, string) (string, error) { return base64.RawStdEncoding.EncodeToString(key), nil }
	brokerGetSecrets = func(brokerGrant) ([]byte, error) { return []byte(`[{"key":"TEST","value":"synthetic"}]`), nil }
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		id, err := brokerRandom(16)
		if err != nil {
			t.Fatal(err)
		}
		base := grant.ID + "." + fmt.Sprintf("%x", id)
		path := filepath.Join(root, base+".req")
		request, err := brokerSeal(key, []byte("secret-list"), "request:"+base)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, request, 0600); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() { defer wg.Done(); brokerHandle(grant, path, base) }()
	}
	wg.Wait()
	responses, err := filepath.Glob(filepath.Join(root, "*.res"))
	if err != nil || len(responses) != 12 {
		t.Fatalf("got %d responses, expected 12: %v", len(responses), err)
	}
}

func TestBrokerRejectsWrongKeyAndExpiredGrant(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	b, err := brokerSeal(key, []byte("secret-list"), "request:one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = brokerOpen(key, b, "request:two"); err == nil {
		t.Fatal("request identifier not authenticated")
	}
	wrong := []byte("11234567890123456789012345678901")
	if _, err = brokerOpen(wrong, b, "request:one"); err == nil {
		t.Fatal("wrong key accepted")
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".amonlhaw", "grants")
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	g := brokerClientGrant{ID: strings.Repeat("a", 32), Project: "project", Key: base64.RawStdEncoding.EncodeToString(key), Expires: time.Now().Add(-time.Second)}
	data, _ := json.Marshal(g)
	if err = os.WriteFile(filepath.Join(dir, "project.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = brokerFindClientGrant(root, "project"); err == nil {
		t.Fatal("expired grant accepted")
	}
}

func TestBrokerRejectsRedirectedDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating Windows links requires additional privileges")
	}
	root, outside := t.TempDir(), t.TempDir()
	link := filepath.Join(root, "mailbox")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := brokerCheckDir(root, link); err == nil {
		t.Fatal("redirected mailbox accepted")
	}
}
