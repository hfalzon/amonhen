package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
)

const brokerKeyService = "amonhen/broker-grants"
const brokerMaxMessage = 4 << 20

type brokerGrant struct {
	ID      string    `json:"id"`
	Repo    string    `json:"repo"`
	Project string    `json:"project"`
	Expires time.Time `json:"expires"`
}

type brokerClientGrant struct {
	ID      string    `json:"id"`
	Project string    `json:"project"`
	Key     string    `json:"key"`
	Expires time.Time `json:"expires"`
}

type brokerResponse struct {
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

type brokerEvent struct {
	Time                           time.Time
	Repo, Project, Outcome, Reason string
}

func (e brokerEvent) String() string {
	return fmt.Sprintf("%s %s repo=%q project=%q reason=%s", e.Time.Format(time.RFC3339), e.Outcome, e.Repo, e.Project, e.Reason)
}

type brokerLimitedBuffer struct{ bytes.Buffer }

func (b *brokerLimitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > brokerMaxMessage/2 {
		return 0, errors.New("provider output exceeds limit")
	}
	return b.Buffer.Write(p)
}

var brokerGetKey = keyring.Get
var brokerSetKey = keyring.Set
var brokerDeleteKey = keyring.Delete
var errBrokerGrantNotFound = errors.New("broker grant not found")

func brokerCommand(args []string) error {
	if len(args) > 0 && args[0] == "help" {
		topic := ""
		if len(args) > 1 {
			topic = args[1]
		}
		fmt.Print(brokerHelp(topic))
		return nil
	}
	if len(args) > 1 && args[1] == "help" {
		fmt.Print(brokerHelp(args[0]))
		return nil
	}
	if len(args) == 0 || containsHelp(args) {
		topic := ""
		if len(args) > 0 && !isHelp(args[0]) {
			topic = args[0]
		}
		fmt.Print(brokerHelp(topic))
		return nil
	}
	f := flag.NewFlagSet("broker "+args[0], flag.ContinueOnError)
	f.SetOutput(io.Discard)
	repo := f.String("repo", "", "repository directory")
	project := f.String("project", "", "project name")
	duration := f.Duration("duration", 8*time.Hour, "grant lifetime")
	if err := f.Parse(args[1:]); err != nil {
		return fmt.Errorf("%v\n\n%s", err, brokerHelp(args[0]))
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q\n\n%s", f.Arg(0), brokerHelp(args[0]))
	}
	switch args[0] {
	case "grant":
		if *repo == "" || *project == "" {
			return fmt.Errorf("grant requires both --repo and --project\n\n%s", brokerHelp("grant"))
		}
		if *duration <= 0 || *duration > 30*24*time.Hour {
			return errors.New("--duration must be positive and no longer than 720h; for example, --duration 8h")
		}
		return brokerCreateGrant(*repo, *project, *duration)
	case "revoke":
		if *repo == "" || *project == "" {
			return fmt.Errorf("revoke requires both --repo and --project\n\n%s", brokerHelp("revoke"))
		}
		return brokerRevokeGrant(*repo, *project)
	case "serve":
		if *repo != "" || *project != "" {
			return errors.New("broker serve takes no options")
		}
		return brokerServe()
	case "fetch":
		return brokerFetch(*repo, *project, 30*time.Second)
	default:
		return errors.New("unknown broker command")
	}
}

func brokerHelp(topic string) string {
	switch topic {
	case "serve":
		return "Usage: amonhen broker serve\n\nRun the broker in a normal host terminal. Each request handled is shown live with its outcome, repository and project. The broker runs until stopped with Ctrl+C.\n"
	case "grant":
		return "Usage: amonhen broker grant --repo <repository-root> --project <name> [--duration <duration>]\n\nExample:\n  amonhen broker grant --repo C:\\Projects\\gadget --project gadget-dev --duration 8h\n\n--repo       Path to the root of the Git repository.\n--project    Project name from 'amonhen ls'.\n--duration   Access lifetime, for example 8h or 30m; defaults to 8h, maximum 720h.\n\nCreates a local access key for the project. Anyone who can read the key can request that project's configuration until it expires or is revoked.\n"
	case "revoke":
		return "Usage: amonhen broker revoke --repo <repository-root> --project <name>\n\nRevoke a repository's project grant and remove its access key.\n"
	case "fetch":
		return "Usage: amonhen broker fetch [--repo <repository-root>] --project <name>\n\nFetch configuration through the broker. Output contains secrets. Amon Lhaw normally uses the broker automatically.\n"
	default:
		return "Usage: amonhen broker <serve|grant|revoke|fetch>\n\n1. Start the broker in a normal host terminal and leave it running:\n     amonhen broker serve\n2. In another terminal, grant a repository access to a project:\n     amonhen broker grant --repo C:\\Projects\\gadget --project gadget-dev --duration 8h\n   Use 'amonhen ls' to see project names. Amon Lhaw then uses the grant automatically.\n3. To revoke access early:\n     amonhen broker revoke --repo C:\\Projects\\gadget --project gadget-dev\n\nUse 'amonhen broker help grant' or 'amonhen broker grant --help' for command details.\n"
	}
}

func brokerRepo(repo string) (string, error) {
	if repo == "" {
		return "", errors.New("--repo is required")
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("repository unavailable: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", errors.New("repository must be an existing directory")
	}
	return resolved, nil
}

// Reject directories that resolve outside the granted repository. This is a
// defense against accidental redirection; it does not make a repository that a
// hostile process can mutate into a strong security boundary.
func brokerCheckDir(repo, dir string) error {
	actual, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	want := filepath.Clean(dir)
	actual = filepath.Clean(actual)
	if runtime.GOOS == "windows" {
		if !strings.EqualFold(actual, want) {
			return errors.New("broker directory is redirected")
		}
	} else if actual != want {
		return errors.New("broker directory is redirected")
	}
	rel, err := filepath.Rel(repo, actual)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("broker directory outside repository")
	}
	return nil
}

func brokerGrantPath() (string, error) {
	p, err := configPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(p), "broker-grants.json"), nil
}

func brokerLoadGrants() ([]brokerGrant, error) {
	p, err := brokerGrantPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) > brokerMaxMessage {
		return nil, errors.New("broker grant store too large")
	}
	var grants []brokerGrant
	if err := json.Unmarshal(b, &grants); err != nil {
		return nil, errors.New("invalid broker grant store")
	}
	return grants, nil
}

func brokerSaveGrants(grants []brokerGrant) error {
	p, err := brokerGrantPath()
	if err != nil {
		return err
	}
	if grants == nil {
		grants = []brokerGrant{}
	}
	b, err := json.Marshal(grants)
	if err != nil {
		return err
	}
	return brokerAtomicWrite(p, b, 0600)
}

func brokerAtomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".amonhen-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	defer f.Close()
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return replaceStore(tmp, path)
}

func brokerRandom(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	return b, err
}

func brokerCreateGrant(repo, project string, duration time.Duration) error {
	root, err := brokerRepo(repo)
	if err != nil {
		return err
	}
	if !validName(project) || duration <= 0 || duration > 30*24*time.Hour {
		return errors.New("valid --project and duration between zero and 30 days required")
	}
	p, err := findProject(project)
	if err != nil {
		return err
	}
	gitRoot, err := exec.Command("git", "-C", root, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return errors.New("repository must be a Git working tree")
	}
	gitRootPath, err := filepath.EvalSymlinks(strings.TrimSpace(string(gitRoot)))
	if err != nil || !strings.EqualFold(filepath.Clean(gitRootPath), filepath.Clean(root)) {
		return errors.New("--repo must name the Git repository root")
	}
	idBytes, err := brokerRandom(16)
	if err != nil {
		return err
	}
	key, err := brokerRandom(32)
	if err != nil {
		return err
	}
	id, encoded := hex.EncodeToString(idBytes), base64.RawStdEncoding.EncodeToString(key)
	g := brokerGrant{ID: id, Repo: root, Project: p.Name, Expires: time.Now().UTC().Add(duration)}
	client := brokerClientGrant{ID: id, Project: p.Name, Key: encoded, Expires: g.Expires}
	return withProjectLock(func() error {
		grants, err := brokerLoadGrants()
		if err != nil {
			return err
		}
		for _, old := range grants {
			if strings.EqualFold(old.Repo, root) && strings.EqualFold(old.Project, p.Name) {
				return errors.New("grant already exists; revoke it before granting again")
			}
		}
		dir := filepath.Join(root, ".amonlhaw")
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		if err := brokerCheckDir(root, dir); err != nil {
			return err
		}
		ignore := filepath.Join(dir, ".gitignore")
		if _, err := os.Stat(ignore); errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(ignore, []byte("*\n"), 0600); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		rel := filepath.ToSlash(filepath.Join(".amonlhaw", "grants", p.Name+".json"))
		if err := exec.Command("git", "-C", root, "check-ignore", "-q", "--", rel).Run(); err != nil {
			return errors.New("grant key must be ignored by Git; add .amonlhaw/ to .gitignore")
		}
		clientPath := filepath.Join(dir, "grants", p.Name+".json")
		if _, err := os.Lstat(clientPath); err == nil {
			return errors.New("grant key file already exists; remove it after confirming it is no longer needed")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := brokerSetKey(brokerKeyService, id, encoded); err != nil {
			return fmt.Errorf("store broker grant key: %w", err)
		}
		clientJSON, _ := json.Marshal(client)
		if err := brokerAtomicWrite(clientPath, clientJSON, 0600); err != nil {
			_ = brokerDeleteKey(brokerKeyService, id)
			return err
		}
		if err := brokerSaveGrants(append(grants, g)); err != nil {
			_ = os.Remove(clientPath)
			_ = brokerDeleteKey(brokerKeyService, id)
			return err
		}
		fmt.Printf("Granted %s access to %s until %s.\n", root, p.Name, g.Expires.Format(time.RFC3339))
		return nil
	})
}

func brokerRevokeGrant(repo, project string) error {
	root, err := brokerRepo(repo)
	if err != nil {
		return err
	}
	if !validName(project) {
		return errors.New("valid --project is required")
	}
	return withProjectLock(func() error {
		grants, err := brokerLoadGrants()
		if err != nil {
			return err
		}
		var kept []brokerGrant
		var removed []brokerGrant
		for _, g := range grants {
			if strings.EqualFold(g.Repo, root) && strings.EqualFold(g.Project, project) {
				removed = append(removed, g)
			} else {
				kept = append(kept, g)
			}
		}
		if len(removed) == 0 {
			return errors.New("no matching broker grant")
		}
		if err := brokerSaveGrants(kept); err != nil {
			return err
		}
		var cleanup error
		for _, g := range removed {
			if err := brokerDeleteKey(brokerKeyService, g.ID); err != nil && !errors.Is(err, keyring.ErrNotFound) {
				cleanup = err
			}
			if err := os.Remove(filepath.Join(root, ".amonlhaw", "grants", g.Project+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
				cleanup = err
			}
		}
		if cleanup != nil {
			return fmt.Errorf("grant revoked; cleanup failed: %w", cleanup)
		}
		fmt.Println("Grant revoked.")
		return nil
	})
}

func brokerSeal(key, plaintext []byte, aad string) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce, err := brokerRandom(g.NonceSize())
	if err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, plaintext, []byte(aad)), nil
}

func brokerOpen(key, data []byte, aad string) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < g.NonceSize() {
		return nil, errors.New("invalid encrypted message")
	}
	return g.Open(nil, data[:g.NonceSize()], data[g.NonceSize():], []byte(aad))
}

func brokerFindClientGrant(repo, project string) (string, brokerClientGrant, error) {
	var grant brokerClientGrant
	if !validName(project) {
		return "", grant, errors.New("valid --project is required")
	}
	start := repo
	if start == "" {
		var err error
		start, err = os.Getwd()
		if err != nil {
			return "", grant, err
		}
	}
	root, err := filepath.Abs(start)
	if err != nil {
		return "", grant, err
	}
	for {
		p := filepath.Join(root, ".amonlhaw", "grants", project+".json")
		b, e := os.ReadFile(p)
		if e == nil {
			if len(b) > 4096 || json.Unmarshal(b, &grant) != nil || !strings.EqualFold(grant.Project, project) || len(grant.ID) != 32 || !time.Now().Before(grant.Expires) {
				return "", grant, errors.New("invalid or expired broker grant")
			}
			if _, e = hex.DecodeString(grant.ID); e != nil {
				return "", grant, errors.New("invalid broker grant ID")
			}
			return root, grant, nil
		}
		if !errors.Is(e, os.ErrNotExist) {
			return "", grant, e
		}
		if repo != "" || filepath.Dir(root) == root {
			return "", grant, errBrokerGrantNotFound
		}
		root = filepath.Dir(root)
	}
}

func brokerFetch(repo, project string, timeout time.Duration) error {
	root, g, err := brokerFindClientGrant(repo, project)
	if err != nil {
		return err
	}
	key, err := base64.RawStdEncoding.DecodeString(g.Key)
	if err != nil || len(key) != 32 {
		return errors.New("invalid broker grant key")
	}
	id, err := brokerRandom(16)
	if err != nil {
		return err
	}
	name := g.ID + "." + hex.EncodeToString(id)
	message, err := brokerSeal(key, []byte("secret-list"), "request:"+name)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, ".amonlhaw", "requests")
	request, response := filepath.Join(dir, name+".req"), filepath.Join(dir, name+".res")
	defer os.Remove(request)
	defer os.Remove(response)
	if err = brokerAtomicWrite(request, message, 0600); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		b, e := os.ReadFile(response)
		if e == nil {
			if len(b) > brokerMaxMessage {
				return errors.New("broker response too large")
			}
			plain, e := brokerOpen(key, b, "response:"+name)
			if e != nil {
				return errors.New("broker response authentication failed")
			}
			var result brokerResponse
			if json.Unmarshal(plain, &result) != nil {
				return errors.New("invalid broker response")
			}
			if result.Error != "" {
				return errors.New(result.Error)
			}
			_, err = os.Stdout.Write(result.Data)
			return err
		}
		if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("broker did not respond; start amonhen broker serve on the host")
}

var brokerGetSecrets = func(g brokerGrant) ([]byte, error) {
	p, err := findProject(g.Project)
	if err != nil {
		return nil, errors.New("broker project unavailable")
	}
	token, err := keyring.Get(service, p.Name)
	if err != nil {
		return nil, errors.New("broker could not access project credential")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bws", "secret", "list", p.UUID)
	cmd.Env = withBWSAccessToken(os.Environ(), token)
	var out brokerLimitedBuffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, errors.New("broker could not fetch project configuration")
	}
	if out.Len() > brokerMaxMessage/2 || !json.Valid(out.Bytes()) {
		return nil, errors.New("invalid or oversized provider response")
	}
	return out.Bytes(), nil
}

func brokerServe() error {
	p, err := configPath()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(filepath.Dir(p), "broker.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = lockStore(f); err != nil {
		return errors.New("another broker is running or broker lock is unavailable")
	}
	fmt.Println("Amon Hen broker is running. Press Ctrl+C to stop.")
	active := map[string]bool{}
	var mu sync.Mutex
	limit := make(chan struct{}, 8)
	for {
		grants, e := brokerLoadGrants()
		if e != nil {
			return fmt.Errorf("broker grant store: %w", e)
		}
		for _, g := range grants {
			dir := filepath.Join(g.Repo, ".amonlhaw", "requests")
			entries, e := os.ReadDir(dir)
			if e != nil {
				continue
			}
			if err := brokerCheckDir(g.Repo, dir); err != nil {
				continue
			}
			for _, entry := range entries {
				name := entry.Name()
				if entry.Type() != 0 || !strings.HasPrefix(name, g.ID+".") || !strings.HasSuffix(name, ".req") {
					continue
				}
				base := strings.TrimSuffix(name, ".req")
				if len(base) != 65 {
					continue
				}
				if _, e := hex.DecodeString(base[33:]); e != nil {
					continue
				}
				path := filepath.Join(dir, name)
				mu.Lock()
				if active[path] {
					mu.Unlock()
					continue
				}
				active[path] = true
				mu.Unlock()
				select {
				case limit <- struct{}{}:
					go func(g brokerGrant, path, base string) {
						defer func() { <-limit; mu.Lock(); delete(active, path); mu.Unlock() }()
						if event := brokerHandle(g, path, base); event != nil {
							fmt.Println(event.String())
						}
					}(g, path, base)
				default:
					mu.Lock()
					delete(active, path)
					mu.Unlock()
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func brokerHandle(g brokerGrant, path, base string) *brokerEvent {
	event := &brokerEvent{Time: time.Now(), Repo: g.Repo, Project: g.Project, Outcome: "FAIL", Reason: "mailbox-unavailable"}
	if err := brokerCheckDir(g.Repo, filepath.Dir(path)); err != nil {
		return event
	}
	defer os.Remove(path)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || len(b) > 4096 {
		event.Reason = "invalid-request"
		return event
	}
	encoded, err := brokerGetKey(brokerKeyService, g.ID)
	if err != nil {
		event.Reason = "grant-key-unavailable"
		return event
	}
	key, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		event.Reason = "invalid-grant-key"
		return event
	}
	plain, err := brokerOpen(key, b, "request:"+base)
	if err != nil || string(plain) != "secret-list" {
		event.Reason = "request-rejected"
		return event
	}
	if !time.Now().Before(g.Expires) {
		event.Reason = "grant-expired"
		return event
	}
	data, err := brokerGetSecrets(g)
	result := brokerResponse{Data: data}
	if err != nil {
		event.Reason = "configuration-unavailable"
		result = brokerResponse{Error: "broker could not fetch project configuration"}
	}
	providerFailed := err != nil
	grants, err := brokerLoadGrants()
	if err != nil {
		event.Reason = "grant-store-unavailable"
		return event
	}
	allowed := false
	for _, current := range grants {
		if current.ID == g.ID && time.Now().Before(current.Expires) {
			allowed = true
			break
		}
	}
	if !allowed {
		event.Reason = "grant-expired-or-revoked"
		return event
	}
	if err := brokerCheckDir(g.Repo, filepath.Dir(path)); err != nil {
		event.Reason = "mailbox-unavailable"
		return event
	}
	payload, err := json.Marshal(result)
	if err != nil {
		event.Reason = "response-encoding-failed"
		return event
	}
	sealed, err := brokerSeal(key, payload, "response:"+base)
	if err != nil {
		event.Reason = "response-encryption-failed"
		return event
	}
	if err := brokerAtomicWrite(strings.TrimSuffix(path, ".req")+".res", sealed, 0600); err != nil {
		event.Reason = "response-write-failed"
		return event
	}
	if !providerFailed {
		event.Outcome, event.Reason = "SUCCESS", "configuration-sent"
	}
	return event
}
