// Package config loads and validates remote environment definitions.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultPath is the per-user configuration file. It is independent of the
	// current working directory so the installed binary works anywhere.
	DefaultPath = "~/.remote-host-cli/config.json"

	// DefaultPort is used when an environment does not specify a port.
	DefaultPort = 22

	// DefaultConnectTimeout is used when an environment does not specify one.
	DefaultConnectTimeout = 15 * time.Second

	maxConfigSize = 1 << 20 // 1 MiB
)

// Config is the on-disk configuration file.
type Config struct {
	// DefaultEnvironment is used when --env is omitted and more than one
	// environment is configured.
	DefaultEnvironment string `json:"default_environment,omitempty"`

	// Environments maps a friendly name to a remote SSH environment.
	Environments map[string]Environment `json:"environments"`
}

// Environment describes one SSH endpoint.
type Environment struct {
	Host                  string `json:"host"`
	Port                  int    `json:"port,omitempty"`
	Username              string `json:"username"`
	Password              string `json:"password"`
	ConnectTimeoutSeconds int    `json:"connect_timeout_seconds,omitempty"`

	// KnownHostsFile defaults to ~/.ssh/known_hosts.
	KnownHostsFile string `json:"known_hosts_file,omitempty"`

	// InsecureIgnoreHostKey disables host key verification and should only be
	// used for throwaway/test environments.
	InsecureIgnoreHostKey bool `json:"insecure_ignore_host_key,omitempty"`
}

// Load reads, normalizes and validates a configuration file.
func Load(path string) (*Config, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("配置文件不存在: %s（可先运行 remote-host-cli init 生成模板）", path)
		}
		return nil, fmt.Errorf("读取配置文件 %s: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("配置路径是目录而不是文件: %s", path)
	}
	if info.Size() > maxConfigSize {
		return nil, fmt.Errorf("配置文件过大（上限 %d 字节）: %s", maxConfigSize, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件 %s: %w", path, err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件 %s: %w", path, err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("解析配置文件 %s: JSON 后存在多余内容", path)
		}
		return nil, fmt.Errorf("解析配置文件 %s: %w", path, err)
	}

	cfg.normalize()
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("配置文件 %s 无效: %w", path, err)
	}
	return &cfg, nil
}

func (c *Config) normalize() {
	for name, env := range c.Environments {
		env.Host = strings.TrimSpace(env.Host)
		env.Username = strings.TrimSpace(env.Username)
		env.KnownHostsFile = strings.TrimSpace(env.KnownHostsFile)
		if env.Port == 0 {
			env.Port = DefaultPort
		}
		if env.ConnectTimeoutSeconds == 0 {
			env.ConnectTimeoutSeconds = int(DefaultConnectTimeout / time.Second)
		}
		if env.KnownHostsFile == "" {
			env.KnownHostsFile = "~/.ssh/known_hosts"
		}
		c.Environments[name] = env
	}
	c.DefaultEnvironment = strings.TrimSpace(c.DefaultEnvironment)
}

func (c *Config) validate() error {
	if len(c.Environments) == 0 {
		return errors.New("environments 不能为空")
	}
	for name, env := range c.Environments {
		if strings.TrimSpace(name) == "" {
			return errors.New("环境名称不能为空")
		}
		if name != strings.TrimSpace(name) {
			return fmt.Errorf("环境名称 %q 不能以空白字符开头或结尾", name)
		}
		if err := env.Validate(); err != nil {
			return fmt.Errorf("环境 %q: %w", name, err)
		}
	}
	if c.DefaultEnvironment != "" {
		if _, ok := c.Environments[c.DefaultEnvironment]; !ok {
			return fmt.Errorf("default_environment %q 不存在", c.DefaultEnvironment)
		}
	}
	return nil
}

// Validate checks a single environment.
func (e Environment) Validate() error {
	if e.Host == "" {
		return errors.New("host 不能为空")
	}
	if strings.ContainsAny(e.Host, " \t\r\n") {
		return errors.New("host 不能包含空白字符")
	}
	if e.Port < 1 || e.Port > 65535 {
		return fmt.Errorf("port 必须在 1-65535 之间，当前为 %d", e.Port)
	}
	if e.Username == "" {
		return errors.New("username 不能为空")
	}
	if strings.ContainsAny(e.Username, " \t\r\n") {
		return errors.New("username 不能包含空白字符")
	}
	if e.Password == "" {
		return errors.New("password 不能为空")
	}
	if e.ConnectTimeoutSeconds < 1 || e.ConnectTimeoutSeconds > 600 {
		return fmt.Errorf("connect_timeout_seconds 必须在 1-600 之间，当前为 %d", e.ConnectTimeoutSeconds)
	}
	return nil
}

// Address returns the dial address (host:port), handling bracketed IPv6 hosts.
func (e Environment) Address() string {
	host := strings.TrimPrefix(strings.TrimSuffix(e.Host, "]"), "[")
	return net.JoinHostPort(host, strconv.Itoa(e.Port))
}

// ConnectTimeout returns the configured timeout.
func (e Environment) ConnectTimeout() time.Duration {
	return time.Duration(e.ConnectTimeoutSeconds) * time.Second
}

// Names returns environment names in deterministic order.
func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Environments))
	for name := range c.Environments {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Select returns the requested environment. When name is empty it falls back to
// default_environment, or to the only configured environment.
func (c *Config) Select(name string) (string, Environment, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		if c.DefaultEnvironment != "" {
			name = c.DefaultEnvironment
		} else if len(c.Environments) == 1 {
			for only := range c.Environments {
				name = only
			}
		} else {
			return "", Environment{}, fmt.Errorf("存在多个环境，请用 -e/--env 指定；可选环境: %s", strings.Join(c.Names(), ", "))
		}
	}

	env, ok := c.Environments[name]
	if !ok {
		return "", Environment{}, fmt.Errorf("环境 %q 不存在；可选环境: %s", name, strings.Join(c.Names(), ", "))
	}
	return name, env, nil
}

// ResolvePath applies the --config flag, REMOTE_HOST_CLI_CONFIG, and the
// default path, in that order.
func ResolvePath(flagValue string) string {
	path := flagValue
	if path == "" {
		path = os.Getenv("REMOTE_HOST_CLI_CONFIG")
	}
	if path == "" {
		path = DefaultPath
	}
	if expanded, err := ExpandHome(path); err == nil {
		return expanded
	}
	return path
}

// ExpandHome expands a leading ~ or ~/ in path.
func ExpandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法确定用户主目录: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, filepath.FromSlash(path[2:])), nil
}

// PermissionWarning reports whether the configuration file is readable or
// writable by group/others. It returns an empty string on platforms where Unix
// permission bits are not meaningful.
func PermissionWarning(path string) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Sprintf("配置文件 %s 的权限为 %04o，建议执行 chmod 600 %s 以保护密码", path, mode, path)
	}
	return ""
}

// InitOptions controls configuration template creation.
type InitOptions struct {
	Path  string
	Force bool
}

// Init creates a configuration template with restrictive permissions.
func Init(opts InitOptions) error {
	path := opts.Path
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建配置目录 %s: %w", dir, err)
	}

	flag := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if opts.Force {
		flag = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flag, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("配置文件已存在: %s（如需覆盖请加 --force）", path)
		}
		return fmt.Errorf("创建配置文件 %s: %w", path, err)
	}
	defer f.Close()

	if _, err := f.WriteString(template); err != nil {
		return fmt.Errorf("写入配置文件 %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭配置文件 %s: %w", path, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("设置配置文件权限: %w", err)
		}
	}
	return nil
}

const template = `{
  "default_environment": "dev",
  "environments": {
    "dev": {
      "host": "192.168.1.100",
      "port": 22,
      "username": "root",
      "password": "change-me",
      "connect_timeout_seconds": 15,
      "known_hosts_file": "~/.ssh/known_hosts",
      "insecure_ignore_host_key": false
    },
    "test": {
      "host": "192.168.1.101",
      "port": 2222,
      "username": "deploy",
      "password": "change-me",
      "connect_timeout_seconds": 15,
      "known_hosts_file": "~/.ssh/known_hosts",
      "insecure_ignore_host_key": false
    }
  }
}
`
