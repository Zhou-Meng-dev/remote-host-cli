package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"

	"remote-host-cli/internal/config"
	"remote-host-cli/internal/remote"
)

const usageText = `remote-host-cli - 在远程主机上执行命令并传输文件

用法:
  remote-host-cli [全局选项] <命令> [参数...]

命令:
  exec <命令>                     在远程环境执行命令
  upload <本地文件> <远程路径>    上传本地文件
  download <远程文件> <本地路径>  下载远程文件
  list                            列出已配置的环境
  init [--force]                  生成 ~/.remote-host-cli/config.json 模板
  help                            显示帮助

全局选项（必须写在命令之前）:
  -e, --env NAME       选择环境；默认使用 default_environment 或唯一环境
  -c, --config PATH    配置文件路径；默认 ~/.remote-host-cli/config.json
  -h, --help           显示帮助

示例:
  remote-host-cli init
  remote-host-cli -e dev exec "uname -a"
  remote-host-cli -e dev upload ./app.tar.gz /opt/app/
  remote-host-cli -e dev download /var/log/app.log ./app.log

配置文件示例:
  {
    "default_environment": "dev",
    "environments": {
      "dev": {
        "host": "192.168.1.100",
        "port": 22,
        "username": "root",
        "password": "your-password"
      }
    }
  }
`

type usageError struct {
	message string
}

func (e *usageError) Error() string {
	return e.message
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("remote-host-cli", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	var configPath, envName string
	fs.StringVar(&configPath, "c", "", "配置文件路径")
	fs.StringVar(&configPath, "config", "", "配置文件路径")
	fs.StringVar(&envName, "e", "", "远程环境名称")
	fs.StringVar(&envName, "env", "", "远程环境名称")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usageText)
			return 0
		}
		fmt.Fprintf(stderr, "错误: %v\n\n%s", err, usageText)
		return 2
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}

	command, commandArgs := rest[0], rest[1:]
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var err error
	switch command {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return 0
	case "exec":
		err = commandExec(ctx, configPath, envName, commandArgs, stdin, stdout, stderr)
	case "upload":
		err = commandUpload(ctx, configPath, envName, commandArgs, stdout, stderr)
	case "download":
		err = commandDownload(ctx, configPath, envName, commandArgs, stdout, stderr)
	case "list":
		err = commandList(configPath, commandArgs, stdout, stderr)
	case "init":
		err = commandInit(configPath, commandArgs, stdout, stderr)
	default:
		err = &usageError{message: fmt.Sprintf("未知命令 %q", command)}
	}
	return reportError(ctx, err, stderr)
}

func reportError(ctx context.Context, err error, stderr io.Writer) int {
	if err == nil {
		return 0
	}

	var ue *usageError
	if errors.As(err, &ue) {
		fmt.Fprintf(stderr, "错误: %s\n\n%s", ue.message, usageText)
		return 2
	}

	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		fmt.Fprintln(stderr, "已取消")
		return 130
	}

	var exitErr *ssh.ExitError
	if errors.As(err, &exitErr) {
		if status := exitErr.ExitStatus(); status >= 0 {
			return status
		}
		return 1
	}

	var missingErr *ssh.ExitMissingError
	if errors.As(err, &missingErr) {
		fmt.Fprintln(stderr, "错误: 远程命令已结束，但服务器未返回退出状态")
		return 1
	}

	fmt.Fprintf(stderr, "错误: %v\n", err)
	return 1
}

func commandExec(ctx context.Context, configPath, envName string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	args = stripDoubleDash(args)
	if len(args) == 0 {
		return &usageError{message: "exec 需要指定要执行的远程命令"}
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

	return client.Exec(ctx, strings.Join(args, " "), stdin, stdout, stderr)
}

func commandUpload(ctx context.Context, configPath, envName string, args []string, stdout, stderr io.Writer) error {
	args = stripDoubleDash(args)
	if len(args) != 2 {
		return &usageError{message: "upload 需要两个参数: <本地文件> <远程路径>"}
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

	target, err := client.Upload(ctx, args[0], args[1])
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "已上传: %s -> %s\n", args[0], target)
	return nil
}

func commandDownload(ctx context.Context, configPath, envName string, args []string, stdout, stderr io.Writer) error {
	args = stripDoubleDash(args)
	if len(args) != 2 {
		return &usageError{message: "download 需要两个参数: <远程文件> <本地路径>"}
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

	target, err := client.Download(ctx, args[0], args[1])
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "已下载: %s -> %s\n", args[0], target)
	return nil
}

func commandList(configPath string, args []string, stdout, stderr io.Writer) error {
	if len(args) != 0 {
		return &usageError{message: "list 不接受额外参数"}
	}
	cfg, path, err := loadConfig(configPath, stderr)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "配置文件: %s\n", path)
	for _, name := range cfg.Names() {
		env := cfg.Environments[name]
		marker := " "
		if name == cfg.DefaultEnvironment {
			marker = "*"
		}
		fmt.Fprintf(stdout, "%s %-20s %s@%s:%d\n", marker, name, env.Username, env.Host, env.Port)
	}
	return nil
}

func commandInit(configPath string, args []string, stdout, stderr io.Writer) error {
	force := false
	for _, arg := range args {
		switch arg {
		case "--force", "-f":
			force = true
		default:
			return &usageError{message: fmt.Sprintf("init 不支持参数 %q", arg)}
		}
	}

	path := config.ResolvePath(configPath)
	if err := config.Init(config.InitOptions{Path: path, Force: force}); err != nil {
		return err
	}
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	fmt.Fprintf(stdout, "已生成配置模板: %s\n请编辑 host、username、password 后再使用。\n", path)
	return nil
}

func loadConfig(flagPath string, stderr io.Writer) (*config.Config, string, error) {
	path := config.ResolvePath(flagPath)
	if warning := config.PermissionWarning(path); warning != "" {
		fmt.Fprintf(stderr, "警告: %s\n", warning)
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, path, err
	}
	return cfg, path, nil
}

func connect(ctx context.Context, env config.Environment, stderr io.Writer) (*remote.Client, error) {
	if env.InsecureIgnoreHostKey {
		fmt.Fprintln(stderr, "警告: 已关闭 SSH 主机密钥校验，仅建议用于测试环境")
	}
	return remote.Connect(ctx, env)
}

func stripDoubleDash(args []string) []string {
	if len(args) > 0 && args[0] == "--" {
		return args[1:]
	}
	return args
}
