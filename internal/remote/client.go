// Package remote implements SSH command execution and SFTP file transfer.
package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"remote-host-cli/internal/config"
)

// Client is a single SSH connection. It is not safe for concurrent use except
// for closing the connection.
type Client struct {
	sshClient *ssh.Client
	sftp      *sftp.Client

	mu        sync.Mutex
	closeOnce sync.Once
	closeErr  error
}

// Connect establishes an SSH connection to the configured environment.
func Connect(ctx context.Context, env config.Environment) (*Client, error) {
	if err := env.Validate(); err != nil {
		return nil, fmt.Errorf("远程环境配置无效: %w", err)
	}

	hostKeyCallback, err := buildHostKeyCallback(env)
	if err != nil {
		return nil, err
	}

	sshConfig := &ssh.ClientConfig{
		User: env.Username,
		Auth: []ssh.AuthMethod{
			ssh.Password(env.Password),
			ssh.KeyboardInteractive(keyboardInteractiveAuth(env.Password)),
		},
		HostKeyCallback: hostKeyCallback,
		Timeout:         env.ConnectTimeout(),
		ClientVersion:   "SSH-2.0-remote-host-cli",
	}

	address := env.Address()
	dialer := net.Dialer{Timeout: env.ConnectTimeout()}
	rawConn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("连接 %s 失败: %w", address, err)
	}

	// Bound the SSH handshake as well as the TCP dial.
	if err := rawConn.SetDeadline(time.Now().Add(env.ConnectTimeout())); err != nil {
		_ = rawConn.Close()
		return nil, fmt.Errorf("设置连接超时: %w", err)
	}
	conn, chans, reqs, err := ssh.NewClientConn(rawConn, address, sshConfig)
	if err != nil {
		_ = rawConn.Close()
		return nil, fmt.Errorf("SSH 握手失败（%s@%s）: %w", env.Username, address, err)
	}
	_ = rawConn.SetDeadline(time.Time{})

	return &Client{
		sshClient: ssh.NewClient(conn, chans, reqs),
	}, nil
}

// Close releases the SFTP and SSH connections.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		handle := c.sftp
		c.sftp = nil
		c.mu.Unlock()

		var errs []error
		if handle != nil {
			if err := handle.Close(); err != nil {
				errs = append(errs, fmt.Errorf("关闭 SFTP 会话: %w", err))
			}
		}
		if err := c.sshClient.Close(); err != nil {
			errs = append(errs, fmt.Errorf("关闭 SSH 连接: %w", err))
		}
		c.closeErr = errors.Join(errs...)
	})
	return c.closeErr
}

// Exec runs command through the remote user's shell. stdin/stdout/stderr are
// connected directly to the remote process. A remote non-zero exit status is
// returned as *ssh.ExitError.
func (c *Client) Exec(ctx context.Context, command string, stdin io.Reader, stdout, stderr io.Writer) error {
	if strings.TrimSpace(command) == "" {
		return errors.New("远程命令不能为空")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	session, err := c.sshClient.NewSession()
	if err != nil {
		return fmt.Errorf("创建 SSH 会话失败: %w", err)
	}
	defer session.Close()

	if stdin != nil {
		session.Stdin = stdin
	}
	if stdout != nil {
		session.Stdout = stdout
	}
	if stderr != nil {
		session.Stderr = stderr
	}

	if err := session.Start(command); err != nil {
		return fmt.Errorf("启动远程命令失败: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- session.Wait()
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// Ask the remote process to stop, then tear down the channel. Closing
		// is needed because servers are not required to honor signals.
		_ = session.Signal(ssh.SIGINT)
		_ = session.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		return ctx.Err()
	}
}

func keyboardInteractiveAuth(password string) ssh.KeyboardInteractiveChallenge {
	return func(_, _ string, questions []string, _ []bool) ([]string, error) {
		answers := make([]string, len(questions))
		for i := range answers {
			answers[i] = password
		}
		return answers, nil
	}
}

func buildHostKeyCallback(env config.Environment) (ssh.HostKeyCallback, error) {
	if env.InsecureIgnoreHostKey {
		return ssh.InsecureIgnoreHostKey(), nil //nolint:gosec // Explicit opt-in.
	}

	knownHostsPath, err := config.ExpandHome(env.KnownHostsFile)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(knownHostsPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf(
				"known_hosts 文件不存在: %s；请先执行 ssh-keyscan -p %d %s >> %s，或在配置中设置 insecure_ignore_host_key=true（不推荐）",
				knownHostsPath, env.Port, env.Host, knownHostsPath,
			)
		}
		return nil, fmt.Errorf("读取 known_hosts 文件 %s: %w", knownHostsPath, err)
	}

	callback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("解析 known_hosts 文件 %s: %w", knownHostsPath, err)
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := callback(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) && len(keyErr.Want) == 0 {
			return fmt.Errorf(
				"未知的主机密钥: %s（%s）；请先执行 ssh-keyscan -p %d %s >> %s，或在配置中设置 insecure_ignore_host_key=true（不推荐）",
				remote, ssh.FingerprintSHA256(key), env.Port, env.Host, knownHostsPath,
			)
		}
		return fmt.Errorf("主机密钥校验失败: %w", err)
	}, nil
}
