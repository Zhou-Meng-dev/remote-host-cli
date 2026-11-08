package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"

	"golang.org/x/term"

	"remote-host-cli/internal/remote"
)

func commandShell(ctx context.Context, configPath, envName string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	args = stripDoubleDash(args)
	if len(args) != 0 {
		return &usageError{message: "shell 不接受额外参数"}
	}

	stdinFile, ok := stdin.(*os.File)
	if !ok || !term.IsTerminal(int(stdinFile.Fd())) {
		return fmt.Errorf("shell 需要交互式终端：stdin 不是 TTY")
	}
	stdoutFile, ok := stdout.(*os.File)
	if !ok || !term.IsTerminal(int(stdoutFile.Fd())) {
		return fmt.Errorf("shell 需要交互式终端：stdout 不是 TTY")
	}

	cfg, _, err := loadConfig(configPath, stderr)
	if err != nil {
		return err
	}
	_, env, err := cfg.Select(envName)
	if err != nil {
		return err
	}

	client, err := connect(ctx, env, stderr)
	if err != nil {
		return err
	}
	defer client.Close()

	width, height, err := term.GetSize(int(stdoutFile.Fd()))
	if err != nil || width <= 0 || height <= 0 {
		width, height = 80, 24
	}

	state, err := term.MakeRaw(int(stdinFile.Fd()))
	if err != nil {
		return fmt.Errorf("切换终端为 raw 模式失败: %w", err)
	}
	defer func() {
		_ = term.Restore(int(stdinFile.Fd()), state)
	}()

	resizes, stopResize := watchTerminalSize(int(stdoutFile.Fd()))
	defer stopResize()

	termType := os.Getenv("TERM")
	if termType == "" {
		termType = "xterm-256color"
	}

	return client.Shell(ctx, remote.ShellOptions{
		Stdin:  stdinFile,
		Stdout: stdoutFile,
		Stderr: stderr,
		Term:   termType,
		Width:  width,
		Height: height,
		Resize: resizes,
	})
}

func watchTerminalSize(fd int) (<-chan remote.WindowSize, func()) {
	signals := terminalResizeSignals()
	if len(signals) == 0 {
		return nil, func() {}
	}

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, signals...)

	updates := make(chan remote.WindowSize, 1)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-signalCh:
				width, height, err := term.GetSize(fd)
				if err != nil || width <= 0 || height <= 0 {
					continue
				}
				select {
				case updates <- remote.WindowSize{Width: width, Height: height}:
				default:
				}
			case <-done:
				return
			}
		}
	}()

	return updates, func() {
		signal.Stop(signalCh)
		close(done)
	}
}
