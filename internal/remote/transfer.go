package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/pkg/sftp"
)

const copyBufferSize = 32 * 1024

// Upload copies one local regular file to remotePath. If remotePath is an
// existing directory (or ends in /), the local base name is appended. It
// returns the final remote path.
func (c *Client) Upload(ctx context.Context, localPath, remotePath string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	info, err := os.Stat(localPath)
	if err != nil {
		return "", fmt.Errorf("读取本地文件 %s: %w", localPath, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("仅支持上传普通文件: %s", localPath)
	}

	sc, err := c.sftpClient()
	if err != nil {
		return "", err
	}

	destination, err := normalizeRemotePath(remotePath)
	if err != nil {
		return "", err
	}
	wantDir := strings.HasSuffix(remotePath, "/")
	target, err := resolveRemoteTarget(sc, destination, wantDir, filepath.Base(filepath.Clean(localPath)))
	if err != nil {
		return "", err
	}

	parent := path.Dir(target)
	parentInfo, err := sc.Stat(parent)
	if err != nil {
		return "", fmt.Errorf("检查远程目录 %s: %w", parent, err)
	}
	if !parentInfo.IsDir() {
		return "", fmt.Errorf("远程路径的父级不是目录: %s", parent)
	}

	src, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("打开本地文件 %s: %w", localPath, err)
	}
	defer src.Close()

	dst, err := sc.Create(target)
	if err != nil {
		return "", fmt.Errorf("创建远程文件 %s: %w", target, err)
	}

	_, copyErr := copyWithContext(ctx, dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		_ = sc.Remove(target)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("上传 %s 到 %s 失败: %w", localPath, target, copyErr)
	}
	if closeErr != nil {
		_ = sc.Remove(target)
		return "", fmt.Errorf("写入远程文件 %s: %w", target, closeErr)
	}

	if err := sc.Chmod(target, info.Mode().Perm()); err != nil {
		return "", fmt.Errorf("文件已上传到 %s，但设置权限失败: %w", target, err)
	}
	return target, nil
}

// Download copies one remote regular file to localPath. If localPath is an
// existing directory (or ends in a path separator), the remote base name is
// appended. It returns the final local path.
func (c *Client) Download(ctx context.Context, remotePath, localPath string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	destination, err := normalizeRemotePath(remotePath)
	if err != nil {
		return "", err
	}

	sc, err := c.sftpClient()
	if err != nil {
		return "", err
	}

	info, err := sc.Stat(destination)
	if err != nil {
		return "", fmt.Errorf("读取远程文件 %s: %w", destination, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("远程路径是目录，当前只支持下载单个文件: %s", destination)
	}

	target, err := resolveLocalTarget(localPath, path.Base(strings.TrimSuffix(destination, "/")))
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(target)
	dirInfo, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("检查本地目录 %s: %w", dir, err)
	}
	if !dirInfo.IsDir() {
		return "", fmt.Errorf("本地路径的父级不是目录: %s", dir)
	}

	src, err := sc.Open(destination)
	if err != nil {
		return "", fmt.Errorf("打开远程文件 %s: %w", destination, err)
	}
	defer src.Close()

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("创建本地临时文件: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	_, copyErr := copyWithContext(ctx, tmp, src)
	closeErr := tmp.Close()
	if copyErr != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("下载 %s 到 %s 失败: %w", destination, target, copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("写入本地临时文件 %s: %w", tmpName, closeErr)
	}

	if err := os.Chmod(tmpName, info.Mode().Perm()); err != nil {
		return "", fmt.Errorf("设置本地文件权限 %s: %w", target, err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return "", fmt.Errorf("保存本地文件 %s: %w", target, err)
	}
	return target, nil
}

func (c *Client) sftpClient() (*sftp.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.sftp != nil {
		return c.sftp, nil
	}
	sc, err := sftp.NewClient(c.sshClient)
	if err != nil {
		return nil, fmt.Errorf("建立 SFTP 会话失败: %w", err)
	}
	c.sftp = sc
	return sc, nil
}

func normalizeRemotePath(p string) (string, error) {
	if p == "" {
		return "", errors.New("远程路径不能为空")
	}
	if p == "~" {
		return ".", nil
	}
	if strings.HasPrefix(p, "~/") {
		p = p[2:]
		if p == "" {
			return ".", nil
		}
		return p, nil
	}
	if strings.HasPrefix(p, "~") {
		return "", fmt.Errorf("不支持 ~user 形式的远程路径: %s", p)
	}
	return p, nil
}

func resolveRemoteTarget(sc *sftp.Client, destination string, wantDir bool, base string) (string, error) {
	info, err := sc.Stat(destination)
	switch {
	case err == nil:
		if info.IsDir() {
			return path.Join(destination, base), nil
		}
		if wantDir {
			return "", fmt.Errorf("远程路径不是目录: %s", destination)
		}
		return destination, nil
	case os.IsNotExist(err):
		if wantDir {
			return "", fmt.Errorf("远程目录不存在: %s", destination)
		}
		return destination, nil
	default:
		return "", fmt.Errorf("检查远程路径 %s: %w", destination, err)
	}
}

func resolveLocalTarget(destination, base string) (string, error) {
	info, err := os.Stat(destination)
	switch {
	case err == nil:
		if info.IsDir() {
			return filepath.Join(destination, base), nil
		}
		return destination, nil
	case errors.Is(err, os.ErrNotExist):
		if strings.HasSuffix(destination, string(os.PathSeparator)) {
			return "", fmt.Errorf("本地目录不存在: %s", destination)
		}
		return destination, nil
	default:
		return "", fmt.Errorf("检查本地路径 %s: %w", destination, err)
	}
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, copyBufferSize)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}

		n, readErr := src.Read(buf)
		if n > 0 {
			written, writeErr := dst.Write(buf[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return total, nil
			}
			return total, readErr
		}
	}
}
