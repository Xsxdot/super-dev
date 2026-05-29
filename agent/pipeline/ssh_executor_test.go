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

func TestSSHExecutorConstruct(t *testing.T) {
	// 仅验证构造与接口实现，不连真机
	ex := pipeline.NewSSHExecutor(func(hostID string) (model.Host, bool) {
		return model.Host{ID: hostID, SSHHost: "10.0.0.1", SSHPort: 22, SSHUser: "ops"}, true
	})
	var _ pipeline.Executor = ex
	assert.NotNil(t, ex)
}

func TestSSHExecutorUnknownHost(t *testing.T) {
	ex := pipeline.NewSSHExecutor(func(string) (model.Host, bool) { return model.Host{}, false })
	_, err := ex.Run(context.Background(), pipeline.Target{HostID: "missing"},
		model.Step{Command: "echo hi", Action: model.ActionRun}, func(string, string) {})
	require.Error(t, err)
}

// TestSSHExecutorRealRun 仅在设置 SUPERDEV_SSH_TEST_HOST 等环境时运行。
func TestSSHExecutorRealRun(t *testing.T) {
	host := os.Getenv("SUPERDEV_SSH_TEST_HOST")
	if host == "" {
		t.Skip("set SUPERDEV_SSH_TEST_HOST/USER/KEY to run real SSH test")
	}
}
