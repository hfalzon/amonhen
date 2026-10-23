package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestProjectAndEnvironmentNames(t *testing.T) {
	for _, name := range []string{"api-service", "demo_1", "Project.Name"} {
		if !validName(name) {
			t.Errorf("validName(%q) = false", name)
		}
	}
	for _, name := range []string{"", "has space", "../escape", "a/b"} {
		if validName(name) {
			t.Errorf("validName(%q) = true", name)
		}
	}
}

func TestValidUUID(t *testing.T) {
	if !validUUID("12345678-1234-4123-8123-123456789abc") {
		t.Fatal("valid BWS UUID rejected")
	}
	for _, input := range []string{"", "project-1", "12345678123441238123123456789abc", "zzzzzzzz-1234-4123-8123-123456789abc"} {
		if validUUID(input) {
			t.Errorf("validUUID(%q) = true", input)
		}
	}
}

func TestParseProjectOptions(t *testing.T) {
	got, err := parseOptions([]string{"-N", "local-dev", "-U", "123", "-T"}, map[string]bool{"name": true, "uuid": true, "token": true})
	if err != nil {
		t.Fatal(err)
	}
	if got["name"] != "local-dev" || got["uuid"] != "123" || got["token"] != "true" {
		t.Fatalf("parsed options = %#v", got)
	}
	if _, err := parseOptions([]string{"-N", "demo", "machine-token"}, map[string]bool{"name": true}); err == nil || !strings.Contains(err.Error(), "hidden prompt") {
		t.Fatalf("raw token argument error = %v", err)
	}
}

func TestBWSRunUsesProjectAndDoesNotInheritEnvironment(t *testing.T) {
	got := bwsRunArgs("project-uuid", []string{"npm", "run", "dev"})
	want := []string{"run", "--no-inherit-env", "--project-id", "project-uuid", "--", "npm", "run", "dev"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("bws args = %#v, want %#v", got, want)
	}
}

func TestBWSAccessTokenIsOnlyAddedToBWSProcessEnvironment(t *testing.T) {
	got := withBWSAccessToken([]string{"PATH=/bin", "bws_access_token=ambient", "APP_MODE=dev"}, "project-token")
	joined := strings.Join(got, "\n")
	if strings.Count(strings.ToLower(joined), "bws_access_token=") != 1 || !strings.Contains(joined, "BWS_ACCESS_TOKEN=project-token") || strings.Contains(joined, "ambient") {
		t.Fatalf("BWS env = %q", joined)
	}
}

func TestHelpIsAvailableAtEveryCommandPath(t *testing.T) {
	cases := [][]string{{"--help"}, {"project", "--help"}, {"project", "add", "--help"}, {"project", "add", "-N", "demo", "-h"}, {"project", "list", "--help"}, {"project", "uuid", "--help"}, {"project", "token", "--help"}, {"project", "token", "set", "--help"}, {"project", "token", "set", "-N", "demo", "-h"}, {"project", "delete", "--help"}, {"token", "--help"}, {"token", "-h", "--", "command"}, {"ls", "--help"}, {"install", "--help"}, {"run", "--help"}}
	for _, args := range cases {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			output, err := captureRun(args)
			if err != nil {
				t.Fatalf("run(%v): %v", args, err)
			}
			if !strings.Contains(output, "Usage:") {
				t.Fatalf("help missing for %v: %q", args, output)
			}
		})
	}
}

func TestProjectTokenReadErrorDistinguishesMissingAndBackendFailures(t *testing.T) {
	missing := projectTokenReadError("local-dev", keyring.ErrNotFound)
	if !strings.Contains(missing.Error(), "not found") || !strings.Contains(missing.Error(), "project token set") {
		t.Fatalf("missing credential error = %q", missing)
	}

	backendErr := errors.New("Windows Credential Manager access denied")
	got := projectTokenReadError("local-dev", fmt.Errorf("credential lookup: %w", backendErr))
	if !errors.Is(got, backendErr) {
		t.Fatalf("backend cause not preserved: %v", got)
	}
	if strings.Contains(strings.ToLower(got.Error()), "not found") || !strings.Contains(got.Error(), backendErr.Error()) {
		t.Fatalf("backend error was misreported: %q", got)
	}
}

func TestProjectTokenSetRequiresHiddenPromptFlag(t *testing.T) {
	err := setProjectToken([]string{"-N", "local-dev"})
	if err == nil || !strings.Contains(err.Error(), "-T is required") {
		t.Fatalf("setProjectToken error = %v", err)
	}
	if _, err := parseOptions([]string{"-N", "local-dev", "plain-text-token"}, map[string]bool{"name": true, "token": true}); err == nil {
		t.Fatal("raw token argument was accepted")
	}
}

func TestVersionCommandAndFlags(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"-V"}, {"--version"}} {
		output, err := captureRun(args)
		if err != nil {
			t.Fatal(err)
		}
		if output != "amonhen "+version+"\n" {
			t.Fatalf("version output = %q", output)
		}
	}
}

func captureRun(args []string) (string, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return "", err
	}
	previous := os.Stdout
	os.Stdout = writer
	runErr := run(args)
	_ = writer.Close()
	os.Stdout = previous
	output, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if runErr != nil {
		return string(output), runErr
	}
	return string(output), readErr
}

func TestListShowsNamesAndUUIDsButNoToken(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	if err := saveProjects([]Project{{Name: "local-dev", UUID: "12345678-1234-4123-8123-123456789abc"}}); err != nil {
		t.Fatal(err)
	}
	output, err := captureRun([]string{"ls"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "token") || !strings.Contains(output, "local-dev") || !strings.Contains(output, "12345678-1234-4123-8123-123456789abc") {
		t.Fatalf("list output = %q", output)
	}
}

func TestProjectUUIDLookupByName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	const uuid = "12345678-1234-4123-8123-123456789abc"
	if err := saveProjects([]Project{{Name: "local-dev", UUID: uuid}}); err != nil {
		t.Fatal(err)
	}
	output, err := captureRun([]string{"project", "uuid", "-N", "local-dev"})
	if err != nil {
		t.Fatal(err)
	}
	if output != uuid+"\n" {
		t.Fatalf("UUID lookup output = %q, want %q", output, uuid+"\n")
	}
}
