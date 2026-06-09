// Package api 验证 remote managed deployment 生命周期控制器。
//
// 职责：
//   - 验证远程 start/stop/restart 命令按 host_ids 分发
//   - 验证命令输出归属到 deployment 日志
//   - 验证缺失命令、未知主机和远程执行失败会返回明确错误
//
// 边界：
//   - 不建立真实 SSH 或远端 agent 连接
//   - 不通过 HTTP handler 或 MCP 工具调用
package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
)

type recordingRemoteRuntimeRunner struct {
	requests []remoteRuntimeRequest
	err      error
}

type remoteRuntimeRequest struct {
	target  pipeline.Target
	command string
	workDir string
}

func (r *recordingRemoteRuntimeRunner) RunRemote(ctx context.Context, target pipeline.Target, cmd string, workDir string, onLine func(string, string)) error {
	r.requests = append(r.requests, remoteRuntimeRequest{target: target, command: cmd, workDir: workDir})
	if onLine != nil {
		onLine("remote command output", "stdout")
	}
	return r.err
}

func (r *recordingRemoteRuntimeRunner) Transfer(ctx context.Context, target pipeline.Target, source string, targetPath string, onLine func(string, string)) error {
	return nil
}

func TestRemoteRuntimeControllerRunsStopCommandOnEachHost(t *testing.T) {
	app := newTestAppForPackage(t)
	_, err := app.remoteStore.AddHost(model.Host{ID: "h1", Name: "prod-a"})
	require.NoError(t, err)
	_, err = app.remoteStore.AddHost(model.Host{ID: "h2", Name: "prod-b"})
	require.NoError(t, err)
	runner := &recordingRemoteRuntimeRunner{}
	ctrl := &remoteRuntimeController{
		runner: runner,
		hosts:  app.remoteStore.ListHosts,
		emit:   app.buf.Append,
	}
	dep := model.Deployment{
		ID: "dep-api-prod", EnvName: "prod", Location: model.LocationRemote,
		HostIDs: []string{"h1", "h2"}, StartCommand: "systemctl start api", StopCommand: "systemctl stop api",
	}

	err = ctrl.Stop(context.Background(), dep)

	require.NoError(t, err)
	require.Len(t, runner.requests, 2)
	assert.Equal(t, "systemctl stop api", runner.requests[0].command)
	assert.Equal(t, "h1", runner.requests[0].target.HostID)
	assert.Equal(t, "prod-a", runner.requests[0].target.HostName)
	assert.Equal(t, "", runner.requests[0].workDir)
	assert.Equal(t, "systemctl stop api", runner.requests[1].command)
	assert.Equal(t, "h2", runner.requests[1].target.HostID)
	recent := app.buf.Recent(1)
	require.Len(t, recent, 1)
	assert.Equal(t, "dep-api-prod", recent[0].DeploymentID)
	assert.Equal(t, "h1", recent[0].SourceID)
	assert.Equal(t, "remote command output", recent[0].Message)
	assert.Equal(t, 2, recent[0].RepeatCount)
	assert.NotEmpty(t, recent[0].FoldKey)
}

func TestRemoteRuntimeControllerRequiresStopCommand(t *testing.T) {
	app := newTestAppForPackage(t)
	_, err := app.remoteStore.AddHost(model.Host{ID: "h1", Name: "prod-a"})
	require.NoError(t, err)
	ctrl := &remoteRuntimeController{
		runner: &recordingRemoteRuntimeRunner{},
		hosts:  app.remoteStore.ListHosts,
		emit:   app.buf.Append,
	}
	dep := model.Deployment{ID: "dep-api-prod", Location: model.LocationRemote, HostIDs: []string{"h1"}}

	err = ctrl.Stop(context.Background(), dep)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote stop command is required")
}

func TestRemoteRuntimeControllerRequiresKnownHosts(t *testing.T) {
	app := newTestAppForPackage(t)
	ctrl := &remoteRuntimeController{
		runner: &recordingRemoteRuntimeRunner{},
		hosts:  app.remoteStore.ListHosts,
		emit:   app.buf.Append,
	}
	dep := model.Deployment{
		ID: "dep-api-prod", Location: model.LocationRemote,
		HostIDs: []string{"missing-host"}, StopCommand: "systemctl stop api",
	}

	err := ctrl.Stop(context.Background(), dep)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown remote host missing-host")
}

func TestRemoteRuntimeControllerPropagatesCommandFailure(t *testing.T) {
	app := newTestAppForPackage(t)
	_, err := app.remoteStore.AddHost(model.Host{ID: "h1", Name: "prod-a"})
	require.NoError(t, err)
	runner := &recordingRemoteRuntimeRunner{err: errors.New("systemctl failed")}
	ctrl := &remoteRuntimeController{
		runner: runner,
		hosts:  app.remoteStore.ListHosts,
		emit:   app.buf.Append,
	}
	dep := model.Deployment{
		ID: "dep-api-prod", Location: model.LocationRemote,
		HostIDs: []string{"h1"}, StopCommand: "systemctl stop api",
	}

	err = ctrl.Stop(context.Background(), dep)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote stop command failed on host h1")
	assert.True(t, strings.Contains(err.Error(), "systemctl failed"))
}
