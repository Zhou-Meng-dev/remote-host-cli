package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAppliesDefaults(t *testing.T) {
	path := writeConfig(t, `{
		"environments": {
			"prod": {
				"host": "example.com",
				"username": "deploy",
				"password": "secret"
			}
		}
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	env := cfg.Environments["prod"]
	if env.Port != DefaultPort {
		t.Fatalf("Port = %d, want %d", env.Port, DefaultPort)
	}
	if env.ConnectTimeout() != DefaultConnectTimeout {
		t.Fatalf("ConnectTimeout() = %v, want %v", env.ConnectTimeout(), DefaultConnectTimeout)
	}
	if env.KnownHostsFile != "~/.ssh/known_hosts" {
		t.Fatalf("KnownHostsFile = %q", env.KnownHostsFile)
	}
	if got, want := env.Address(), "example.com:22"; got != want {
		t.Fatalf("Address() = %q, want %q", got, want)
	}
}

func TestSelect(t *testing.T) {
	path := writeConfig(t, `{
		"default_environment": "dev",
		"environments": {
			"dev": {"host": "dev.example.com", "username": "dev", "password": "x"},
			"prod": {"host": "prod.example.com", "username": "prod", "password": "y"}
		}
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	name, env, err := cfg.Select("")
	if err != nil {
		t.Fatalf("Select(default) error = %v", err)
	}
	if name != "dev" || env.Username != "dev" {
		t.Fatalf("Select(default) = %q, %+v", name, env)
	}

	name, _, err = cfg.Select("prod")
	if err != nil || name != "prod" {
		t.Fatalf("Select(prod) = %q, %v", name, err)
	}

	if _, _, err := cfg.Select("missing"); err == nil {
		t.Fatal("Select(missing) returned nil error")
	}
}

func TestSelectSingleEnvironmentWithoutDefault(t *testing.T) {
	path := writeConfig(t, `{
		"environments": {
			"only": {"host": "example.com", "username": "u", "password": "p"}
		}
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	name, _, err := cfg.Select("")
	if err != nil || name != "only" {
		t.Fatalf("Select() = %q, %v", name, err)
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "unknown field",
			content: `{"environments":{"dev":{"host":"h","username":"u","password":"p","typo":true}}}`,
			want:    "unknown field",
		},
		{
			name:    "missing password",
			content: `{"environments":{"dev":{"host":"h","username":"u"}}}`,
			want:    "password",
		},
		{
			name:    "bad port",
			content: `{"environments":{"dev":{"host":"h","username":"u","password":"p","port":70000}}}`,
			want:    "port",
		},
		{
			name:    "default does not exist",
			content: `{"default_environment":"prod","environments":{"dev":{"host":"h","username":"u","password":"p"}}}`,
			want:    "default_environment",
		},
		{
			name:    "no environments",
			content: `{"environments":{}}`,
			want:    "environments",
		},
		{
			name:    "trailing JSON",
			content: `{"environments":{"dev":{"host":"h","username":"u","password":"p"}}} {}`,
			want:    "多余内容",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.content))
			if err == nil {
				t.Fatal("Load() returned nil error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err, tt.want)
			}
		})
	}
}

func TestInitCreatesPrivateTemplateAndRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".remote-host-cli", "config.json")

	if err := Init(InitOptions{Path: path}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions = %04o, want 0600", info.Mode().Perm())
	}
	if warning := PermissionWarning(path); warning != "" {
		t.Fatalf("PermissionWarning() = %q, want empty", warning)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("generated template is not loadable: %v", err)
	}
	if len(cfg.Environments) != 2 || cfg.DefaultEnvironment != "dev" {
		t.Fatalf("template environments = %v, default = %q", cfg.Names(), cfg.DefaultEnvironment)
	}

	if err := Init(InitOptions{Path: path}); err == nil {
		t.Fatal("second Init() returned nil error")
	}
	if err := Init(InitOptions{Path: path, Force: true}); err != nil {
		t.Fatalf("Init(force) error = %v", err)
	}
}

func TestLoadMissingFileMentionsInit(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil || !strings.Contains(err.Error(), "init") {
		t.Fatalf("Load(missing) error = %v", err)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	got, err := ExpandHome("~/.ssh/known_hosts")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".ssh", "known_hosts")
	if got != want {
		t.Fatalf("ExpandHome() = %q, want %q", got, want)
	}
}

func TestResolvePathDefaultsToHomeConfig(t *testing.T) {
	t.Setenv("REMOTE_HOST_CLI_CONFIG", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}

	got := ResolvePath("")
	want := filepath.Join(home, ".remote-host-cli", "config.json")
	if got != want {
		t.Fatalf("ResolvePath(\"\") = %q, want %q", got, want)
	}
}
