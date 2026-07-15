// campaign_test.go 验证 active campaign 对远端清理终态的 fail-closed 派生。
//
// 职责：
//   - 只有 cleanup run 终态与受控脚本日志都通过时才确认远端已清理
//   - 区分正常 cleanup 与异常路径 cleanup 的完整证据链
//
// 边界：
//   - 不启动 Agent/MCP，不访问真实远端节点
package runtimevalidation

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPipelineCleanupConfirmedRequiresTerminalRunAndAbsenceLog(t *testing.T) {
	t.Parallel()

	result := ToolCampaignResult{Scenarios: []ToolScenarioExecution{{
		ID: "remote-pipeline",
		Steps: []ToolStepExecution{
			{StepID: "pipeline-cleanup", Status: StatusPass},
			{StepID: "pipeline-wait-cleanup", Status: StatusPass},
			{StepID: "pipeline-logs-cleanup", Status: StatusFail},
		},
	}}}
	require.False(t, pipelineCleanupConfirmed(result))

	result.Scenarios[0].Steps[2].Status = StatusPass
	require.True(t, pipelineCleanupConfirmed(result))
}

func TestPipelineCleanupConfirmedAcceptsCompleteAbortCleanup(t *testing.T) {
	t.Parallel()

	result := ToolCampaignResult{Scenarios: []ToolScenarioExecution{{
		ID: "remote-pipeline",
		Cleanup: []ToolStepExecution{
			{StepID: "pipeline-cleanup-on-abort", Status: StatusPass},
			{StepID: "pipeline-wait-cleanup-on-abort", Status: StatusPass},
			{StepID: "pipeline-logs-cleanup-on-abort", Status: StatusPass},
		},
	}}}
	require.True(t, pipelineCleanupConfirmed(result))
}

func TestAuthSidecarCredentialUsesAnonymousStdinNotEnvironment(t *testing.T) {
	t.Parallel()

	secret := "one-time-fixture-secret"
	spec := authSidecarProcessSpec("/tmp/auth-sidecar", "/tmp", 18190, "rv-linux-amd64-test", secret, io.Discard)
	for _, value := range spec.Env {
		require.NotContains(t, value, secret)
	}
	require.NotContains(t, strings.Join(spec.Arguments, " "), secret)
	raw, err := io.ReadAll(spec.Stdin)
	require.NoError(t, err)
	require.Equal(t, secret+"\n", string(raw))
}

func TestRemoteIdentityAttestationRequiresLiveScenarioPass(t *testing.T) {
	t.Parallel()

	result := ToolCampaignResult{Scenarios: []ToolScenarioExecution{{
		ID: "remote-pipeline", Steps: []ToolStepExecution{{StepID: "pipeline-host-id-preflight", Status: StatusFail}},
	}}}
	require.False(t, remoteIdentityConfirmed(result))

	result.Scenarios[0].Steps[0].Status = StatusPass
	require.True(t, remoteIdentityConfirmed(result))
}

func TestRemotePipelineGuardRetainsStartedUnconfirmedRun(t *testing.T) {
	t.Parallel()

	started, cleaned := true, false
	action := &remotePipelineGuardAction{id: "campaign-1", started: func() bool { return started }, cleaned: func() bool { return cleaned }}
	require.ErrorContains(t, action.Release(context.Background()), "without confirmed")

	cleaned = true
	require.NoError(t, action.Release(context.Background()))
	started = false
	cleaned = false
	require.NoError(t, action.Release(context.Background()))
}
