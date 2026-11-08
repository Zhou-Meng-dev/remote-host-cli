package remote_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"remote-host-cli/internal/config"
	"remote-host-cli/internal/remote"
)

type testSSHServer struct {
	root    string
	address string
	port    int
	ln      net.Listener
	cfg     *ssh.ServerConfig
}

func newTestSSHServer(t *testing.T) *testSSHServer {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if meta.User() == "tester" && string(password) == "secret" {
				return nil, nil
			}
			return nil, errors.New("invalid credentials")
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, portText, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}

	server := &testSSHServer{
		root:    t.TempDir(),
		address: net.JoinHostPort(host, portText),
		port:    port,
		ln:      ln,
		cfg:     cfg,
	}
	go server.serve()
	t.Cleanup(func() {
		_ = ln.Close()
	})
	return server
}

func (s *testSSHServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *testSSHServer) handleConn(conn net.Conn) {
	defer conn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, s.cfg)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "only session channels are supported")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go s.handleSession(channel, requests)
	}
}

func (s *testSSHServer) handleSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()

	for req := range requests {
		switch req.Type {
		case "exec":
			var payload struct {
				Command string
			}
			if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
				_ = req.Reply(false, nil)
				return
			}
			if err := req.Reply(true, nil); err != nil {
				return
			}
			s.runCommand(channel, payload.Command)
			return
		case "subsystem":
			var payload struct {
				Name string
			}
			if err := ssh.Unmarshal(req.Payload, &payload); err != nil || payload.Name != "sftp" {
				_ = req.Reply(false, nil)
				return
			}
			server, err := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(s.root))
			if err != nil {
				_ = req.Reply(false, nil)
				return
			}
			if err := req.Reply(true, nil); err != nil {
				return
			}
			_ = server.Serve()
			return
		default:
			_ = req.Reply(false, nil)
		}
	}
}

func (s *testSSHServer) runCommand(channel ssh.Channel, command string) {
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir = s.root
	cmd.Stdin = channel
	cmd.Stdout = channel
	cmd.Stderr = channel.Stderr()

	status := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			status = exitErr.ExitCode()
		} else {
			status = 127
		}
	}
	_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct {
		Status uint32
	}{uint32(status)}))
}

func (s *testSSHServer) environment() config.Environment {
	return config.Environment{
		Host:                  "127.0.0.1",
		Port:                  s.port,
		Username:              "tester",
		Password:              "secret",
		ConnectTimeoutSeconds: 5,
		InsecureIgnoreHostKey: true,
	}
}

func connectTestClient(t *testing.T, server *testSSHServer) *remote.Client {
	t.Helper()
	client, err := remote.Connect(context.Background(), server.environment())
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})
	return client
}

func TestExec(t *testing.T) {
	server := newTestSSHServer(t)
	client := connectTestClient(t, server)

	var stdout, stderr bytes.Buffer
	err := client.Exec(
		context.Background(),
		`printf 'stdout-data'; printf 'stderr-data' >&2; exit 7`,
		strings.NewReader(""),
		&stdout,
		&stderr,
	)
	if err == nil {
		t.Fatal("Exec() returned nil error")
	}
	var exitErr *ssh.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Exec() error = %T %v, want *ssh.ExitError", err, err)
	}
	if exitErr.ExitStatus() != 7 {
		t.Fatalf("exit status = %d, want 7", exitErr.ExitStatus())
	}
	if stdout.String() != "stdout-data" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.String() != "stderr-data" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestExecStdin(t *testing.T) {
	server := newTestSSHServer(t)
	client := connectTestClient(t, server)

	var stdout, stderr bytes.Buffer
	if err := client.Exec(context.Background(), "cat", strings.NewReader("hello stdin"), &stdout, &stderr); err != nil {
		t.Fatalf("Exec() error = %v, stderr = %q", err, stderr.String())
	}
	if stdout.String() != "hello stdin" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestUploadAndDownload(t *testing.T) {
	server := newTestSSHServer(t)
	client := connectTestClient(t, server)
	ctx := context.Background()

	localDir := t.TempDir()
	content := bytes.Repeat([]byte("remote-host-cli\x00binary-data\n"), 4096)
	localPath := filepath.Join(localDir, "source file.bin")
	if err := os.WriteFile(localPath, content, 0o640); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(filepath.Join(server.root, "uploads"), 0o750); err != nil {
		t.Fatal(err)
	}

	target, err := client.Upload(ctx, localPath, "uploads/")
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	if target != "uploads/source file.bin" {
		t.Fatalf("Upload() target = %q", target)
	}
	uploaded, err := os.ReadFile(filepath.Join(server.root, "uploads", "source file.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(uploaded, content) {
		t.Fatal("uploaded content does not match")
	}

	downloadDir := t.TempDir()
	target, err = client.Download(ctx, "uploads/source file.bin", downloadDir)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if target != filepath.Join(downloadDir, "source file.bin") {
		t.Fatalf("Download() target = %q", target)
	}
	downloaded, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(downloaded, content) {
		t.Fatal("downloaded content does not match")
	}
}

func TestUploadToExplicitFileAndDownloadOverwrite(t *testing.T) {
	server := newTestSSHServer(t)
	client := connectTestClient(t, server)
	ctx := context.Background()

	localDir := t.TempDir()
	localPath := filepath.Join(localDir, "payload.txt")
	if err := os.WriteFile(localPath, []byte("first version"), 0o600); err != nil {
		t.Fatal(err)
	}

	target, err := client.Upload(ctx, localPath, "renamed.txt")
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	if target != "renamed.txt" {
		t.Fatalf("Upload() target = %q", target)
	}

	remotePath := filepath.Join(server.root, "renamed.txt")
	if err := os.WriteFile(remotePath, []byte("second version"), 0o600); err != nil {
		t.Fatal(err)
	}
	downloadPath := filepath.Join(localDir, "downloaded.txt")
	target, err = client.Download(ctx, "renamed.txt", downloadPath)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "second version" {
		t.Fatalf("downloaded content = %q", data)
	}
}

func TestTransferErrorsAndHomePath(t *testing.T) {
	server := newTestSSHServer(t)
	client := connectTestClient(t, server)
	ctx := context.Background()

	localPath := filepath.Join(t.TempDir(), "home.txt")
	if err := os.WriteFile(localPath, []byte("home"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Upload(ctx, localPath, "~/"); err != nil {
		t.Fatalf("Upload(~/ ) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(server.root, "home.txt")); err != nil {
		t.Fatalf("upload through home path: %v", err)
	}

	if _, err := client.Upload(ctx, localPath, "missing-dir/"); err == nil {
		t.Fatal("Upload() to missing directory returned nil error")
	}
	if _, err := client.Download(ctx, ".", t.TempDir()); err == nil {
		t.Fatal("Download() of a directory returned nil error")
	}
	if _, err := client.Upload(ctx, localPath, "~root/file"); err == nil {
		t.Fatal("Upload() with ~user path returned nil error")
	}
}

func TestWrongPassword(t *testing.T) {
	server := newTestSSHServer(t)
	env := server.environment()
	env.Password = "wrong"

	_, err := remote.Connect(context.Background(), env)
	if err == nil {
		t.Fatal("Connect() with wrong password returned nil error")
	}
	if !strings.Contains(fmt.Sprint(err), "握手失败") {
		t.Fatalf("Connect() error = %v", err)
	}
}
