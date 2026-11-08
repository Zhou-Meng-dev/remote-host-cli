package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	defaultTerminalWidth  = 80
	defaultTerminalHeight = 24
)

// WindowSize is a terminal size in character cells.
type WindowSize struct {
	Width  int
	Height int
}

// ShellOptions controls an interactive remote shell.
type ShellOptions struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// Term is the terminal type reported to the server, for example
	// "xterm-256color".
	Term   string
	Width  int
	Height int

	// Resize receives terminal size updates while the shell is running.
	// It may be nil.
	Resize <-chan WindowSize
}

// Shell opens an interactive shell with a remote PTY. The caller is
// responsible for putting the local terminal into raw mode and restoring it.
func (c *Client) Shell(ctx context.Context, opts ShellOptions) error {
	if opts.Stdin == nil {
		return errors.New("交互式 shell 需要 stdin")
	}
	if opts.Stdout == nil {
		return errors.New("交互式 shell 需要 stdout")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	termType := opts.Term
	if termType == "" {
		termType = "xterm-256color"
	}
	width, height := opts.Width, opts.Height
	if width <= 0 {
		width = defaultTerminalWidth
	}
	if height <= 0 {
		height = defaultTerminalHeight
	}

	session, err := c.sshClient.NewSession()
	if err != nil {
		return fmt.Errorf("创建 SSH 会话失败: %w", err)
	}
	defer session.Close()

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty(termType, height, width, modes); err != nil {
		return fmt.Errorf("申请远程 PTY 失败: %w", err)
	}

	session.Stdin = opts.Stdin
	session.Stdout = opts.Stdout
	if opts.Stderr != nil {
		session.Stderr = opts.Stderr
	}

	if err := session.Shell(); err != nil {
		return fmt.Errorf("启动远程 shell 失败: %w", err)
	}

	resizeDone := make(chan struct{})
	defer close(resizeDone)
	if opts.Resize != nil {
		go func() {
			for {
				select {
				case size, ok := <-opts.Resize:
					if !ok {
						return
					}
					if size.Width > 0 && size.Height > 0 {
						_ = session.WindowChange(size.Height, size.Width)
					}
				case <-resizeDone:
					return
				}
			}
		}()
	}

	done := make(chan error, 1)
	go func() {
		done <- session.Wait()
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGINT)
		_ = session.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		return ctx.Err()
	}
}
