---
name: remote-host-cli
description: "Operate configured remote SSH environments through the installed remote-host-cli. Use when asked to run or inspect commands on a remote/dev/staging/prod host, open an SSH-like interactive shell, upload or download files, or manage remote environments and the default environment."
---

# remote-host-cli

Use the installed `remote-host-cli` for remote SSH work. It reads accounts, passwords, ports, and environments from `~/.remote-host-cli/config.json`; never ask the user to paste the password into chat, and never print the config file.

## Workflow

### 1. Confirm the tool and target environment

```bash
command -v remote-host-cli
remote-host-cli list
```

`list` is safe: it prints environment names, usernames, and hosts, never passwords. Do not use `cat` or `jq` on the config to identify a target.

Choose the environment:

- If the user named an environment, use that name.
- Otherwise use `default_environment`, shown with `*` in `list`, for ordinary read-only work.
- If several environments are configured and the target is unclear, ask before connecting.
- For production or a destructive operation, state the exact environment and confirm before running it.

**Done when:** you know the environment name, and for risky work have explicit confirmation.

If the command is missing, the binary is normally at `~/.local/bin/remote-host-cli`; use that path or tell the user to run `source ~/.bashrc`. If no config exists, tell the user to run `remote-host-cli init` and edit `~/.remote-host-cli/config.json`. Do not invent credentials.

### 2. Run the remote operation

For non-interactive commands:

```bash
remote-host-cli -e ENV exec 'command'
```

Pass the remote command as one quoted argument. Use a single shell script string for compound commands:

```bash
remote-host-cli -e ENV exec 'set -e; cd /opt/app && ls -la && systemctl is-active app'
```

The remote command's stdout, stderr, and exit status are returned directly. Preserve and report a non-zero status rather than assuming success. Quote variables that must expand remotely; avoid local expansion by using single quotes around the whole command.

For an interactive shell:

```bash
remote-host-cli -e ENV shell
```

`shell` requires a real local TTY. If your execution tool has no TTY, do not run it there; give the user the exact command to run in their terminal. Use `exec` for automation.

**Done when:** the remote operation has finished and you have reported its output and exit status.

### 3. Transfer files when needed

Upload one regular file:

```bash
remote-host-cli -e ENV upload ./local-file /remote/path/
```

Download one regular file:

```bash
remote-host-cli -e ENV download /remote/path/file ./local-path
```

Rules:

- Only single regular files are supported; directories are not recursive.
- The remote parent directory must exist for uploads.
- A destination directory (or trailing `/`) appends the source base name.
- Existing files are overwritten. Confirm before overwriting anything important.
- `~` and `~/...` work for remote paths.
- For important transfers, verify both sides with `sha256sum`.

**Done when:** the command reports the final path and, when integrity matters, checksums match.

### 4. Manage environments and defaults

- Configuration lives at `~/.remote-host-cli/config.json` with mode `0600`.
- `default_environment` selects the environment used when `-e` is omitted.
- `remote-host-cli -e NAME ...` overrides the default.
- `--config PATH` overrides the config location; `REMOTE_HOST_CLI_CONFIG` is the environment-variable equivalent.
- Create the template with `remote-host-cli init`; use `--force` only after confirming the user wants to overwrite their config.

Do not edit the config to change credentials unless the user explicitly asks; the user owns that file.

## Safety

- Confirm destructive commands (`rm`, `reboot`, service restarts, deployments, database changes) before running them, and name the environment.
- Prefer read-only inspection first when diagnosing.
- Never disable host key verification (`insecure_ignore_host_key`) without explicit user consent.
- On an unknown host key, offer the `ssh-keyscan` command and ask the user to verify the fingerprint; do not silently bypass verification.
- Do not echo passwords, tokens, private keys, or full config contents into the transcript or logs.
- Do not use raw `ssh`/`scp` for operations this CLI covers; it already handles the configured credentials and host-key policy.

## Reference

| Task | Command |
| --- | --- |
| List environments | `remote-host-cli list` |
| Run a command | `remote-host-cli -e ENV exec 'command'` |
| Interactive shell | `remote-host-cli -e ENV shell` |
| Upload | `remote-host-cli -e ENV upload LOCAL REMOTE` |
| Download | `remote-host-cli -e ENV download REMOTE LOCAL` |
| Create config template | `remote-host-cli init [--force]` |

Environment selection: explicit `-e` > `default_environment` > the only configured environment. Multiple environments without a default is an error listing valid names.

Exit status: remote command/shell status is passed through; `2` is a usage error; `130` means interrupted.

Troubleshooting:

- `command not found`: use `~/.local/bin/remote-host-cli` or reload the shell PATH.
- Config missing: run `remote-host-cli init`, then edit the config.
- Unknown host key: add it with `ssh-keyscan -p PORT HOST >> ~/.ssh/known_hosts` after verifying the host.
- `shell` reports "需要交互式终端": run it from a real terminal, not a pipe or headless tool.
- Transfer fails on a directory: this tool only transfers single regular files.
