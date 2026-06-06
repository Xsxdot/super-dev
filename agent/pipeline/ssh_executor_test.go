package pipeline

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"golang.org/x/crypto/ssh"
)

type sshRemoteRunner interface {
	RunRemote(ctx context.Context, target Target, cmd string, workDir string, onLine func(string, string)) error
}

type sshFileTransfer interface {
	Transfer(ctx context.Context, target Target, source string, targetPath string, onLine func(string, string)) error
}

func TestSSHExecutorConstruct(t *testing.T) {
	// 仅验证构造与能力接口实现，不连真机
	ex := NewSSHExecutor(func(hostID string) (model.Host, bool) {
		host := model.Host{ID: hostID}
		tunnelParams := host.EnsureTunnelAgent()
		tunnelParams.SSHHost = "10.0.0.1"
		tunnelParams.SSHPort = 22
		tunnelParams.SSHUser = "ops"
		return host, true
	})
	var _ sshRemoteRunner = ex
	var _ sshFileTransfer = ex
	assert.NotNil(t, ex)
}

func TestSSHExecutorUnknownHost(t *testing.T) {
	ex := NewSSHExecutor(func(string) (model.Host, bool) { return model.Host{}, false })
	err := ex.RunRemote(context.Background(), Target{HostID: "missing"}, "echo hi", "", func(string, string) {})
	require.Error(t, err)
}

func TestRemoteCommandExitErrorIncludesCommand(t *testing.T) {
	err := remoteCommandExitError("nginx -t", 127)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "nginx -t")
	assert.Contains(t, err.Error(), "code 127")
	var coded interface{ ExitCode() int }
	require.ErrorAs(t, err, &coded)
	assert.Equal(t, 127, coded.ExitCode())
}

func TestSSHExecutorRunRemoteDrainsStdoutAndStderrBeforeReturn(t *testing.T) {
	addr := startSSHExecServer(t, 0)
	host, portText, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	ex := NewSSHExecutor(func(hostID string) (model.Host, bool) {
		testHost := model.Host{ID: hostID}
		tunnelParams := testHost.EnsureTunnelAgent()
		tunnelParams.SSHHost = host
		tunnelParams.SSHPort = port
		tunnelParams.SSHUser = "ops"
		tunnelParams.SSHPassword = "pw"
		return testHost, true
	})

	var mu sync.Mutex
	var lines []string
	err = ex.RunRemote(context.Background(), Target{HostID: "h1"}, "printf remote", "", func(line, stream string) {
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, stream+":"+line)
	})

	require.NoError(t, err)
	mu.Lock()
	got := append([]string(nil), lines...)
	mu.Unlock()
	assert.ElementsMatch(t, []string{
		"stdout:remote stdout",
		"stderr:remote stderr",
	}, got)
}

func TestPrepareTransferSourcePackagesDirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("ok"), 0o644))
	prepared, cleanup, err := prepareTransferSource(dir)
	require.NoError(t, err)
	defer cleanup()
	assert.NotEqual(t, dir, prepared)
	require.FileExists(t, prepared)
	assert.True(t, tarGzContains(t, prepared, "index.html"))
}

// TestSSHExecutorRealRun 仅在设置 SUPERDEV_SSH_TEST_HOST 等环境时运行。
func TestSSHExecutorRealRun(t *testing.T) {
	host := os.Getenv("SUPERDEV_SSH_TEST_HOST")
	if host == "" {
		t.Skip("set SUPERDEV_SSH_TEST_HOST/USER/KEY to run real SSH test")
	}
}

func startSSHExecServer(t *testing.T, exitStatus uint32) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(key)
	require.NoError(t, err)
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if c.User() == "ops" && string(password) == "pw" {
				return nil, nil
			}
			return nil, errors.New("invalid test ssh credentials")
		},
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = ln.Close()
	})
	go serveSSHExec(ln, cfg, exitStatus)
	return ln.Addr().String()
}

func serveSSHExec(ln net.Listener, cfg *ssh.ServerConfig, exitStatus uint32) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go handleSSHExecConn(conn, cfg, exitStatus)
	}
}

func handleSSHExecConn(conn net.Conn, cfg *ssh.ServerConfig, exitStatus uint32) {
	serverConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		_ = conn.Close()
		return
	}
	defer serverConn.Close()
	go ssh.DiscardRequests(reqs)
	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "session channel required")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go handleSSHExecChannel(channel, requests, exitStatus)
	}
}

func handleSSHExecChannel(channel ssh.Channel, requests <-chan *ssh.Request, exitStatus uint32) {
	defer channel.Close()
	for req := range requests {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		_ = req.Reply(true, nil)
		_, _ = channel.Write([]byte("remote stdout\n"))
		_, _ = channel.Stderr().Write([]byte("remote stderr\n"))
		_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct {
			Status uint32
		}{Status: exitStatus}))
		return
	}
}

func tarGzContains(t *testing.T, filePath, name string) bool {
	t.Helper()
	f, err := os.Open(filePath)
	require.NoError(t, err)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return false
		}
		require.NoError(t, err)
		if header.Name == name {
			return true
		}
	}
}
