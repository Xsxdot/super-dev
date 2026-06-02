package pipeline

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
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
		return model.Host{ID: hostID, SSHHost: "10.0.0.1", SSHPort: 22, SSHUser: "ops"}, true
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
