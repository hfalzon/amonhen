// Command credential-probe tests OS credential access across the desktop/host
// boundary using an isolated service and a random synthetic token. It never
// reads Amon Hen's production credential service or prints secret material.
package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

type fixture struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

const prefix = "amonhen/investigation-only/"

func run() error {
	if len(os.Args) == 3 && os.Args[1] == "child" {
		h := sha256.Sum256([]byte(os.Getenv("BWS_ACCESS_TOKEN")))
		if hex.EncodeToString(h[:]) != os.Args[2] {
			return errors.New("child credential verification failed")
		}
		fmt.Println("child credential verification succeeded")
		return nil
	}
	if len(os.Args) != 3 {
		return errors.New("usage: credential-probe <init|check|remove> <absolute-fixture-path>")
	}
	action, path := os.Args[1], os.Args[2]
	if !filepath.IsAbs(path) {
		return errors.New("fixture path must be absolute")
	}
	var f fixture
	if action == "init" {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return errors.New("fixture already exists or cannot be created")
		}
		defer file.Close()
		id := make([]byte, 16)
		secret := make([]byte, 32)
		if _, err := rand.Read(id); err != nil {
			return errors.New("random source unavailable")
		}
		if _, err := rand.Read(secret); err != nil {
			return errors.New("random source unavailable")
		}
		token := hex.EncodeToString(secret)
		defer clear(secret)
		h := sha256.Sum256([]byte(token))
		f = fixture{ID: hex.EncodeToString(id), Digest: hex.EncodeToString(h[:])}
		// Persist fixture identity before creating an OS credential so cleanup is
		// possible if the process is interrupted after credential creation.
		if err := json.NewEncoder(file).Encode(f); err != nil {
			return errors.New("cannot record fixture identity")
		}
		if err := file.Sync(); err != nil {
			return errors.New("cannot persist fixture identity")
		}
		if err := keyring.Set(prefix+f.ID, "fixture", token); err != nil {
			return errors.New("synthetic credential write failed")
		}
		fmt.Println("synthetic credential created; no production credentials accessed")
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errors.New("cannot read credential fixture")
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return errors.New("invalid credential fixture")
	}
	id, err := hex.DecodeString(f.ID)
	if err != nil || len(id) != 16 {
		return errors.New("invalid credential fixture identity")
	}
	digest, err := hex.DecodeString(f.Digest)
	if err != nil || len(digest) != 32 {
		return errors.New("invalid credential fixture digest")
	}
	switch action {
	case "remove":
		if err := keyring.Delete(prefix+f.ID, "fixture"); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return errors.New("synthetic credential cleanup failed")
		}
		fmt.Println("synthetic credential removed")
	case "check":
		token, err := keyring.Get(prefix+f.ID, "fixture")
		if errors.Is(err, keyring.ErrNotFound) {
			return errors.New("synthetic credential not visible in this environment")
		}
		if err != nil {
			return errors.New("OS credential store unavailable or access denied")
		}
		h := sha256.Sum256([]byte(token))
		if hex.EncodeToString(h[:]) != f.Digest {
			return errors.New("OS credential value differs across environments")
		}
		cmd := exec.Command(os.Args[0], "child", f.Digest)
		for _, entry := range os.Environ() {
			name, _, _ := strings.Cut(entry, "=")
			if !strings.EqualFold(name, "BWS_ACCESS_TOKEN") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "BWS_ACCESS_TOKEN="+token)
		output, err := cmd.CombinedOutput()
		if strings.Contains(string(output), token) {
			return errors.New("child exposed synthetic credential; output suppressed")
		}
		if err != nil || strings.TrimSpace(string(output)) != "child credential verification succeeded" {
			return errors.New("credential child check failed; output suppressed")
		}
		fmt.Println("OS credential retrieval and child injection succeeded")
	default:
		return errors.New("unknown credential probe action")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
