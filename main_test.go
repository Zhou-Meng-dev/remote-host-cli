package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--help"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(--help) = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "remote-host-cli") || !strings.Contains(stdout.String(), "upload") {
		t.Fatalf("help output = %q", stdout.String())
	}
}

func TestRunInitAndList(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".remote-host-cli", "config.json")

	var stdout, stderr bytes.Buffer
	code := run([]string{"--config", path, "init"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(init) = %d, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config was not created: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"--config", path, "list"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(list) = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "dev") || strings.Contains(stdout.String(), "change-me") {
		t.Fatalf("list output = %q", stdout.String())
	}
}

func TestRunUsageErrors(t *testing.T) {
	tests := [][]string{
		{},
		{"unknown"},
		{"upload", "only-one-argument"},
		{"download"},
		{"exec"},
	}
	for _, args := range tests {
		var stdout, stderr bytes.Buffer
		if code := run(args, strings.NewReader(""), &stdout, &stderr); code != 2 {
			t.Fatalf("run(%q) = %d, want 2; stderr = %q", args, code, stderr.String())
		}
	}
}
