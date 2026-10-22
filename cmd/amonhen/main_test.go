package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestMaskWriterMasksSecretsAcrossWrites(t *testing.T) {
	var out bytes.Buffer
	writer := newMaskWriter(&out, map[string]string{
		"API_KEY": "super-secret-value",
		"SHORT":   "xy",
	})

	chunks := []string{"prefix super-", "secret-", "value suffix x", "y done"}
	for _, chunk := range chunks {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	if err := writer.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}

	got := out.String()
	if strings.Contains(got, "super-secret-value") || strings.Contains(got, "xy") {
		t.Fatalf("secret leaked in masked output: %q", got)
	}
	if !strings.Contains(got, "***") {
		t.Fatalf("masked output has no redaction marker: %q", got)
	}
}

func TestMaskWriterMasksOverlappingSecretsLongestFirst(t *testing.T) {
	var out bytes.Buffer
	writer := newMaskWriter(&out, map[string]string{
		"SHORT": "secret",
		"LONG":  "secret-value",
	})
	if _, err := writer.Write([]byte("secret-value")); err != nil {
		t.Fatal(err)
	}
	if err := writer.close(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "***" {
		t.Fatalf("masked output = %q, want %q", got, "***")
	}
}

func TestMergeEnvRemovesExistingBWSAccessToken(t *testing.T) {
	got := mergeEnv([]string{
		"PATH=/usr/bin",
		"bws_access_token=ambient-token",
		"APP_MODE=dev",
	}, map[string]string{"API_KEY": "resolved-value"})

	joined := strings.Join(got, "\n")
	if strings.Contains(strings.ToLower(joined), "bws_access_token") || strings.Contains(joined, "ambient-token") {
		t.Fatalf("BWS token was inherited by the child: %q", joined)
	}
	if !strings.Contains(joined, "PATH=/usr/bin") || !strings.Contains(joined, "APP_MODE=dev") || !strings.Contains(joined, "API_KEY=resolved-value") {
		t.Fatalf("expected environment entries missing: %q", joined)
	}
}

func TestProjectAndEnvironmentNames(t *testing.T) {
	for _, name := range []string{"api-service", "demo_1", "Project.Name"} {
		if !validName(name) {
			t.Errorf("validName(%q) = false", name)
		}
	}
	for _, name := range []string{"", "has space", "../escape", "a/b"} {
		if validName(name) {
			t.Errorf("validName(%q) = true, want false", name)
		}
	}
	for _, name := range []string{"API_KEY", "_TOKEN", "a1"} {
		if !validEnvName(name) {
			t.Errorf("validEnvName(%q) = false", name)
		}
	}
	for _, name := range []string{"", "1BAD", "HAS-DASH", "HAS SPACE"} {
		if validEnvName(name) {
			t.Errorf("validEnvName(%q) = true, want false", name)
		}
	}
}

func TestHelpIsAvailableAtEveryCommandPath(t *testing.T) {
	cases := [][]string{
		{"--help"},
		{"token", "--help"},
		{"token", "set", "--help"},
		{"project", "--help"},
		{"project", "add", "--help"},
		{"project", "add", "demo", "API_KEY=bws://id", "--help"},
		{"project", "list", "--help"},
		{"install", "--help"},
		{"run", "--help"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			output, err := captureRun(args)
			if err != nil {
				t.Fatalf("run(%v) error = %v", args, err)
			}
			if !strings.Contains(output, "Usage: amonhen") {
				t.Fatalf("run(%v) did not print command help: %q", args, output)
			}
		})
	}
}

func TestVersionCommandAndFlags(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"-V"}, {"--version"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			output, err := captureRun(args)
			if err != nil {
				t.Fatalf("run(%v) error = %v", args, err)
			}
			if output != "amonhen "+version+"\n" {
				t.Fatalf("version output = %q", output)
			}
		})
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
