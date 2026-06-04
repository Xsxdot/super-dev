// routing_runner_test.go 验证 pipeline 远程执行路由策略。
//
// 职责：
//   - 验证 healthy 走 agent
//   - 验证非 healthy 走 SSH
//   - 验证 healthy 下仅 agent 通道不可用时 fallback
//
// 边界：
//   - 不测试真实 SSH 或 WebSocket
//   - 不测试 agenthealth.Monitor 轮询
package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/agenthealth"
)

type routeHealth map[string]agenthealth.Status

func (h routeHealth) Status(hostID string) agenthealth.Status {
	if status, ok := h[hostID]; ok {
		return status
	}
	return agenthealth.StatusUnknown
}

type recordingRunner struct {
	runCalls      []string
	transferCalls []string
	err           error
}

func (r *recordingRunner) RunRemote(ctx context.Context, target Target, cmd string, workDir string, onLine func(string, string)) error {
	r.runCalls = append(r.runCalls, target.HostID+":"+cmd)
	if onLine != nil {
		onLine("runner "+target.HostID, "stdout")
	}
	return r.err
}

func (r *recordingRunner) Transfer(ctx context.Context, target Target, source string, targetPath string, onLine func(string, string)) error {
	r.transferCalls = append(r.transferCalls, target.HostID+":"+source+"->"+targetPath)
	return r.err
}

func TestRoutingRunnerUsesAgentWhenHealthy(t *testing.T) {
	agent := &recordingRunner{}
	ssh := &recordingRunner{}
	runner := NewRoutingRunner(routeHealth{"h1": agenthealth.StatusHealthy}, agent, ssh)
	var lines []string

	err := runner.RunRemote(context.Background(), Target{HostID: "h1"}, "echo hi", "", func(line, stream string) {
		lines = append(lines, stream+":"+line)
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"h1:echo hi"}, agent.runCalls)
	assert.Empty(t, ssh.runCalls)
	assert.Contains(t, lines, "system:remote route host h1 -> agent")
}

func TestRoutingRunnerUsesSSHWhenNotHealthy(t *testing.T) {
	for _, status := range []agenthealth.Status{
		agenthealth.StatusUnknown,
		agenthealth.StatusUnreachable,
		agenthealth.StatusVersionMismatch,
	} {
		t.Run(string(status), func(t *testing.T) {
			agent := &recordingRunner{}
			ssh := &recordingRunner{}
			runner := NewRoutingRunner(routeHealth{"h1": status}, agent, ssh)
			var lines []string

			err := runner.RunRemote(context.Background(), Target{HostID: "h1"}, "echo hi", "", func(line, stream string) {
				lines = append(lines, stream+":"+line)
			})

			require.NoError(t, err)
			assert.Empty(t, agent.runCalls)
			assert.Equal(t, []string{"h1:echo hi"}, ssh.runCalls)
			assert.Contains(t, lines, "system:remote route host h1 -> ssh")
		})
	}
}

func TestRoutingRunnerDoesNotFallbackWhenHealthyAgentFails(t *testing.T) {
	agent := &recordingRunner{err: errors.New("agent down")}
	ssh := &recordingRunner{}
	runner := NewRoutingRunner(routeHealth{"h1": agenthealth.StatusHealthy}, agent, ssh)

	err := runner.RunRemote(context.Background(), Target{HostID: "h1"}, "echo hi", "", nil)

	require.Error(t, err)
	assert.Equal(t, []string{"h1:echo hi"}, agent.runCalls)
	assert.Empty(t, ssh.runCalls)
}

func TestRoutingRunnerFallsBackToSSHWhenHealthyAgentUnavailable(t *testing.T) {
	agent := &recordingRunner{err: AgentUnavailableError("dial remote agent: connection refused")}
	ssh := &recordingRunner{}
	runner := NewRoutingRunner(routeHealth{"h1": agenthealth.StatusHealthy}, agent, ssh)
	var lines []string

	err := runner.RunRemote(context.Background(), Target{HostID: "h1"}, "echo hi", "", func(line, stream string) {
		lines = append(lines, stream+":"+line)
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"h1:echo hi"}, agent.runCalls)
	assert.Equal(t, []string{"h1:echo hi"}, ssh.runCalls)
	assert.Contains(t, lines, "system:remote route host h1 -> agent")
	assert.Contains(t, lines, "system:remote route host h1 -> ssh")
}

func TestRoutingRunnerFallsBackToSSHTransferWhenHealthyAgentUnavailable(t *testing.T) {
	agent := &recordingRunner{err: AgentUnavailableError("remote agent transfer endpoint unreachable")}
	ssh := &recordingRunner{}
	runner := NewRoutingRunner(routeHealth{"h1": agenthealth.StatusHealthy}, agent, ssh)

	err := runner.Transfer(context.Background(), Target{HostID: "h1"}, "a", "/tmp/a", nil)

	require.NoError(t, err)
	assert.Equal(t, []string{"h1:a->/tmp/a"}, agent.transferCalls)
	assert.Equal(t, []string{"h1:a->/tmp/a"}, ssh.transferCalls)
}

func TestRoutingRunnerTransferUsesSameRoutePolicy(t *testing.T) {
	agent := &recordingRunner{}
	ssh := &recordingRunner{}
	runner := NewRoutingRunner(routeHealth{"h1": agenthealth.StatusHealthy, "h2": agenthealth.StatusUnknown}, agent, ssh)

	require.NoError(t, runner.Transfer(context.Background(), Target{HostID: "h1"}, "a", "/tmp/a", nil))
	require.NoError(t, runner.Transfer(context.Background(), Target{HostID: "h2"}, "b", "/tmp/b", nil))

	assert.Equal(t, []string{"h1:a->/tmp/a"}, agent.transferCalls)
	assert.Equal(t, []string{"h2:b->/tmp/b"}, ssh.transferCalls)
}
