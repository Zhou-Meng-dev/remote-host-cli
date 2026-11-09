# remote-host-cli

一个使用 Go 编写的轻量远程运维 CLI：读取本地配置中的远程环境（主机、账号、密码），通过 SSH 执行命令，并通过 SFTP 在本地与远程主机之间上传、下载文件。

## 特性

- 配置固定保存在 `~/.remote-host-cli/config.json`，安装后可在任意目录运行。
- `exec` 远程执行命令，实时转发标准输入、标准输出和标准错误，并透传远程退出码。
- `shell` 进入与 `ssh` 一致的交互式终端：申请远程 PTY，支持窗口大小同步、`vim`、`top`、Ctrl-C、Ctrl-D 等。
- `upload` / `download` 上传下载单个普通文件，自动识别目标是否为目录。
- 支持 `~`、`~/` 远程路径，以及带空格的文件名。
- 默认使用 `~/.ssh/known_hosts` 校验主机密钥，也支持显式关闭校验用于测试环境。
- 附带 DSH skill，可通过 `make install-skill` 让 DSH agent 自动使用本 CLI 操作远程环境。
- 纯标准库实现 CLI、JSON 配置、参数解析、文件复制、信号处理等；SSH 与 SFTP 协议使用成熟第三方库。

## 依赖说明

Go 标准库不包含 SSH、SFTP 和终端 raw 模式实现，因此项目引入三个必要依赖：

| 模块 | 用途 |
| --- | --- |
| `golang.org/x/crypto/ssh` | SSH 连接、密码认证、主机密钥校验、命令执行、PTY |
| `github.com/pkg/sftp` | SFTP 文件上传与下载 |
| `golang.org/x/term` | 本地终端 raw 模式、尺寸读取与恢复 |

其余功能（配置加载、参数解析、文件操作、流复制、信号处理、错误处理）均使用 Go 原生标准库。

要求 Go 1.24 或更高版本。

## 安装

安装后二进制可在任意目录执行，配置文件固定放在当前用户的 `~/.remote-host-cli/config.json`。

### 用户级安装（推荐，无需 sudo）

```bash
make install-user
```

程序会安装到 `~/.local/bin/remote-host-cli`。如果该目录不在 `PATH` 中，加入当前 shell 的启动文件：

```bash
# 对交互式 bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc

# 对登录 shell
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.profile

# 当前会话立即生效
export PATH="$HOME/.local/bin:$PATH"
```

验证：

```bash
command -v remote-host-cli
remote-host-cli --help
```

### 系统级安装

希望所有用户都能使用时，安装到 `/usr/local/bin`：

```bash
make build
sudo install -d /usr/local/bin
sudo install -m 0755 bin/remote-host-cli /usr/local/bin/remote-host-cli
```

或者：

```bash
sudo make install
```

`/usr/local/bin` 默认已经在绝大多数 Linux 系统的 `PATH` 中。

卸载只移除二进制，不会删除配置：

```bash
make uninstall-user    # 用户级安装
sudo make uninstall    # 系统级安装
```

### 安装为 DSH skill

项目包含一个 DSH skill，让 DSH agent 自动学会使用本 CLI 操作远程环境：

```bash
make install-skill
```

skill 会安装到 `~/.dsh/skills/remote-host-cli/SKILL.md`。DSH 会发现并热加载该 skill；它覆盖环境选择、远程命令、交互式 shell、文件传输、默认环境和危险操作确认。也可以使用自定义 DSH 目录：

```bash
make install-skill DSH_HOME=/path/to/.dsh
```

卸载 skill：

```bash
make uninstall-skill
```

### 编译但不安装

```bash
make build          # 输出 bin/remote-host-cli
# 或
go build -trimpath -o bin/remote-host-cli .
```

也可以直接运行：

```bash
go run . --help
```

## 配置

首次安装后生成配置模板：

```bash
remote-host-cli init
```

模板会写入 `~/.remote-host-cli/config.json`，目录权限为 `0700`、文件权限为 `0600`。模板内置 `dev`、`test` 两个示例环境。如果文件已存在，需要覆盖时使用：

```bash
remote-host-cli init --force
```

配置示例：

```json
{
  "default_environment": "dev",
  "environments": {
    "dev": {
      "host": "192.168.1.100",
      "port": 22,
      "username": "root",
      "password": "your-password",
      "connect_timeout_seconds": 15,
      "known_hosts_file": "~/.ssh/known_hosts",
      "insecure_ignore_host_key": false
    },
    "test": {
      "host": "10.0.0.20",
      "port": 2222,
      "username": "deploy",
      "password": "another-password"
    }
  }
}
```

字段说明：

| 字段 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `default_environment` | 否 | 无 | 未传 `--env` 时使用的环境名。只有一个环境时可省略 |
| `environments` | 是 | 无 | 环境名到连接信息的映射，环境名可自定义 |
| `environments.*.host` | 是 | 无 | 主机名或 IP，不要包含端口 |
| `environments.*.port` | 否 | `22` | SSH 端口 |
| `environments.*.username` | 是 | 无 | SSH 账号 |
| `environments.*.password` | 是 | 无 | SSH 密码 |
| `environments.*.connect_timeout_seconds` | 否 | `15` | TCP 连接与 SSH 握手超时，范围 1-600 秒 |
| `environments.*.known_hosts_file` | 否 | `~/.ssh/known_hosts` | 主机密钥文件 |
| `environments.*.insecure_ignore_host_key` | 否 | `false` | 设为 `true` 时关闭主机密钥校验，仅建议测试用 |

配置文件使用严格的 JSON 字段校验，拼错字段（例如把 `username` 写成 `user`）会直接报错，不会静默忽略。

### 主机密钥

默认开启主机密钥校验。首次连接某台主机前，需要把它的公钥加入 `known_hosts`：

```bash
ssh-keyscan -p 22 192.168.1.100 >> ~/.ssh/known_hosts
```

如果使用自定义的 `known_hosts_file`，请写入对应文件。只有在临时测试环境中才建议设置：

```json
"insecure_ignore_host_key": true
```

此时 CLI 会在标准错误输出安全警告。

## 使用

全局参数必须写在子命令之前：

```text
remote-host-cli [--config PATH] [--env NAME] <命令> [参数...]
```

### 查看环境

```bash
remote-host-cli list
```

输出包含环境名、账号和主机地址，不会输出密码。默认环境前面带 `*`。

### 执行远程命令

```bash
remote-host-cli -e dev exec "uname -a"
remote-host-cli -e dev exec "df -h && free -m"
remote-host-cli exec -- echo hello
```

远程命令的输出会直接写到当前终端；命令退出码会成为 CLI 的退出码，因此可以直接用于脚本判断：

```bash
if remote-host-cli -e dev exec "test -f /opt/app/ready"; then
  echo "ready"
fi
```

需要通过标准输入传递内容时：

```bash
echo "hello" | remote-host-cli -e dev exec "cat > /tmp/hello.txt"
```

复杂 shell 语法建议整体加引号，作为一个参数传入：

```bash
remote-host-cli -e dev exec 'for i in 1 2 3; do echo $i; done'
```

`exec` 不分配 PTY，适合脚本和非交互命令；需要运行 `vim`、`top` 等全屏交互程序时请使用 `shell`。

### 进入交互式 shell

```bash
remote-host-cli -e dev shell
```

进入后与直接执行 `ssh user@host` 类似：

- 自动申请远程 PTY，使用远端登录 shell。
- 本地终端切换到 raw 模式，Ctrl-C、Ctrl-D、方向键、Tab 补全等由远端处理。
- 调整本地终端窗口大小时，远程 PTY 尺寸同步更新。
- 在远端执行 `exit` 或按 Ctrl-D 退出，终端会自动恢复原状态。

`shell` 必须在真实交互式终端中运行；通过管道或重定向调用时会报错。若需要非交互执行，请使用 `exec`。

### 上传文件

```bash
remote-host-cli -e dev upload ./app.tar.gz /opt/app/
remote-host-cli -e dev upload ./config.yaml /etc/myapp/config.yaml
remote-host-cli -e dev upload "./local file.txt" "~/uploads/local file.txt"
```

行为说明：

- 目标是已存在目录，或以 `/` 结尾的远程目录时，自动拼接本地文件名。
- 目标是文件路径时直接写入该路径；已存在则覆盖。
- 远程父目录必须已存在。
- 上传完成后按本地文件权限设置远程文件权限。
- 当前只支持上传普通文件，不支持目录递归上传。

### 下载文件

```bash
remote-host-cli -e dev download /var/log/app.log ./app.log
remote-host-cli -e dev download /var/log/app.log ./logs/
remote-host-cli -e dev download "~/data/report.csv" "./report.csv"
```

行为说明：

- 本地目标是已存在目录时，自动拼接远程文件名。
- 本地目标不存在时视为目标文件名，父目录必须已存在。
- 下载先写入同目录临时文件，成功后再原子重命名，避免中断后留下半截目标文件。
- 下载完成后按远程文件权限设置本地文件权限。
- 当前只支持下载普通文件，不支持目录递归下载。

### 指定配置文件

```bash
remote-host-cli --config /path/to/config.json -e prod exec "hostname"
```

也可以通过环境变量指定：

```bash
REMOTE_HOST_CLI_CONFIG=/path/to/config.json remote-host-cli list
```

优先级：`--config` > `REMOTE_HOST_CLI_CONFIG` > `~/.remote-host-cli/config.json`。

### 选择环境

配置中通过环境名称指定默认环境：

```json
{
  "default_environment": "dev",
  "environments": {
    "dev":  { "host": "192.168.1.100", "port": 22, "username": "root", "password": "dev-password" },
    "test": { "host": "192.168.1.101", "port": 2222, "username": "deploy", "password": "test-password" }
  }
}
```

此时不传 `-e`，所有操作都会自动在 `dev` 环境执行：

```bash
# 等价于 remote-host-cli -e dev exec "hostname"
remote-host-cli exec "hostname"

# 等价于 remote-host-cli -e dev upload ./app.tar.gz /opt/app/
remote-host-cli upload ./app.tar.gz /opt/app/
```

需要临时操作其他环境时，再显式指定 `-e`：

```bash
remote-host-cli -e test exec "hostname"
```

环境选择优先级：

1. `--env` / `-e` 指定的环境；
2. 配置中的 `default_environment`；
3. 配置中唯一的环境。

存在多个环境且未配置默认环境、也未传 `--env` 时，会报错并列出可选环境。

## 退出码

| 退出码 | 含义 |
| --- | --- |
| 远程命令/shell 退出码 | `exec` 或 `shell` 成功连接，但远程进程以非零状态退出 |
| `0` | 成功 |
| `1` | 本地错误，例如配置错误、连接失败、文件传输失败 |
| `2` | 命令用法错误 |
| `130` | 用户按下 Ctrl-C |

## 测试

项目包含配置单元测试，以及使用内置 SSH/SFTP 测试服务器完成的端到端测试（命令执行、stdin、退出码、PTY 交互式 shell、上传、下载、错误路径）：

```bash
go test ./...
go vet ./...
```

## 目录结构

```text
.
├── main.go                     # CLI 参数解析与命令分发
├── main_test.go
├── shell.go                    # 交互式 shell 命令与终端 raw 模式
├── resize_signal_unix.go       # Unix SIGWINCH 窗口尺寸监听
├── resize_signal_windows.go    # Windows 空实现
├── Makefile                    # 编译、安装、测试
├── internal/
│   ├── config/
│   │   ├── config.go           # 配置加载、校验、初始化
│   │   └── config_test.go
│   └── remote/
│       ├── client.go           # SSH 连接、主机密钥、命令执行
│       ├── shell.go            # 远程 PTY 与交互式 shell
│       ├── transfer.go         # SFTP 上传下载
│       └── remote_test.go      # SSH/SFTP/PTY 端到端测试
├── skills/
│   └── remote-host-cli/
│       └── SKILL.md            # DSH skill 源文件
├── bin/remote-host-cli         # make build 输出（不提交）
├── go.mod
└── go.sum
```

配置文件不在项目目录中，默认路径为 `~/.remote-host-cli/config.json`。

## 安全提示

- 配置文件中保存明文密码，这是需求约定的方式。请确保 `~/.remote-host-cli/config.json` 权限为 `0600`，并且不要提交到版本库。
- 默认不要设置 `insecure_ignore_host_key: true`，否则无法防御中间人攻击。
- 密码会出现在进程内存中，这是密码认证本身的限制。生产环境更推荐后续扩展 SSH key / ssh-agent 认证。
