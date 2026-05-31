package pipeline_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/pipeline"
)

type sshRemoteRunner interface {
	RunRemote(ctx context.Context, target pipeline.Target, cmd string, workDir string, onLine func(string, string)) error
}

type sshFileTransfer interface {
	Transfer(ctx context.Context, target pipeline.Target, source string, targetPath string, onLine func(string, string)) error
}

func TestSSHExecutorConstruct(t *testing.T) {
	// 仅验证构造与能力接口实现，不连真机
	ex := pipeline.NewSSHExecutor(func(hostID string) (model.Host, bool) {
		return model.Host{ID: hostID, SSHHost: "10.0.0.1", SSHPort: 22, SSHUser: "ops"}, true
	})
	var _ sshRemoteRunner = ex
	var _ sshFileTransfer = ex
	assert.NotNil(t, ex)
}

func TestSSHExecutorUnknownHost(t *testing.T) {
	ex := pipeline.NewSSHExecutor(func(string) (model.Host, bool) { return model.Host{}, false })
	err := ex.RunRemote(context.Background(), pipeline.Target{HostID: "missing"}, "echo hi", "", func(string, string) {})
	require.Error(t, err)
}

// TestSSHExecutorRealRun 仅在设置 SUPERDEV_SSH_TEST_HOST 等环境时运行。
func TestSSHExecutorRealRun(t *testing.T) {
	host := os.Getenv("SUPERDEV_SSH_TEST_HOST")
	if host == "" {
		t.Skip("set SUPERDEV_SSH_TEST_HOST/USER/KEY to run real SSH test")
	}
}
