// Package plugins_test 验证远程文件传输插件。
//
// 职责：
//   - 验证 transfer 校验 source/target
//   - 验证 transfer 会调用文件传输能力
//
// 边界：
//   - 不连接真实 SSH
//   - 不读取真实源文件
package plugins_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
	"github.com/xsxdot/super-dev/agent/pipeline/plugins"
)

type fakeFileTransfer struct {
	calls []string
}

func (f *fakeFileTransfer) Transfer(ctx context.Context, target pipeline.Target, source string, targetPath string, onLine func(string, string)) error {
	f.calls = append(f.calls, target.HostID+":"+source+":"+targetPath)
	onLine("sent", "stdout")
	return nil
}

type fakeSyncTransport struct {
	transferCalls []string
	remoteCalls   []string
}

func (f *fakeSyncTransport) Transfer(ctx context.Context, target pipeline.Target, source string, targetPath string, onLine func(string, string)) error {
	f.transferCalls = append(f.transferCalls, target.HostID+":"+source+":"+targetPath)
	return nil
}

func (f *fakeSyncTransport) RunRemote(ctx context.Context, target pipeline.Target, cmd string, workDir string, onLine func(string, string)) error {
	f.remoteCalls = append(f.remoteCalls, target.HostID+":"+workDir+":"+cmd)
	onLine("synced", "stdout")
	return nil
}

func TestTransferRequiresTarget(t *testing.T) {
	err := plugins.NewTransfer(nil).Validate(model.Step{With: map[string]interface{}{"source": "a"}})
	require.ErrorContains(t, err, "with.target")
}

func TestTransferValidateTargetsRequiresTargets(t *testing.T) {
	plugin := plugins.NewTransfer(nil)

	err := plugin.ValidateTargets(model.Step{Name: "Upload", Type: "transfer"}, nil)

	require.ErrorContains(t, err, "transfer requires targets")
}

func TestTransferExecutesTargets(t *testing.T) {
	transfer := &fakeFileTransfer{}
	p := plugins.NewTransfer(transfer)
	var logs []string
	ctx := pipeline.NewRunContext(context.Background(), pipeline.RunContextOptions{
		LogLine: func(line, stream string) { logs = append(logs, stream+":"+line) },
	})
	step := model.Step{Name: "Upload", Type: "transfer", With: map[string]interface{}{"source": "a.tar.gz", "target": "/opt/api/a.tar.gz"}}

	err := p.Execute(ctx, step, []pipeline.Target{{HostID: "h1", HostName: "box1"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"h1:a.tar.gz:/opt/api/a.tar.gz"}, transfer.calls)
	assert.Contains(t, logs, "stdout:sent")
}

func TestTransferEmitsCommandBeforeTransferOutput(t *testing.T) {
	transfer := &fakeFileTransfer{}
	p := plugins.NewTransfer(transfer)
	var logs []string
	ctx := pipeline.NewRunContext(context.Background(), pipeline.RunContextOptions{
		LogLine: func(line, stream string) { logs = append(logs, stream+":"+line) },
	})
	step := model.Step{
		Name: "Upload",
		Type: "transfer",
		With: map[string]interface{}{"source": "a.tar.gz", "target": "/opt/api/a.tar.gz"},
	}

	err := p.Execute(ctx, step, []pipeline.Target{{HostID: "h1", HostName: "box1"}})

	require.NoError(t, err)
	assert.Equal(t, []string{
		model.StreamCommand + ":transfer: a.tar.gz -> /opt/api/a.tar.gz",
		model.StreamStdout + ":sent",
	}, logs)
}

func TestTransferAllowsDirectorySource(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("ok"), 0o644))
	transfer := &fakeFileTransfer{}
	plugin := plugins.NewTransfer(transfer)
	step := model.Step{Type: "transfer", With: map[string]interface{}{"source": dir, "target": "/tmp/site.tar.gz"}}

	err := plugin.Execute(pipeline.NewRunContext(context.Background(), pipeline.RunContextOptions{}), step, []pipeline.Target{{HostID: "h1"}})

	require.NoError(t, err)
	assert.Equal(t, []string{"h1:" + dir + ":/tmp/site.tar.gz"}, transfer.calls)
}

func TestTransferRemoteCmdModeRunsRemoteSyncCommand(t *testing.T) {
	transport := &fakeSyncTransport{}
	plugin := plugins.NewTransfer(transport)
	var logs []string
	ctx := pipeline.NewRunContext(context.Background(), pipeline.RunContextOptions{
		Vars:    map[string]string{"sync_mode": "remote_cmd"},
		LogLine: func(line, stream string) { logs = append(logs, stream+":"+line) },
	})
	step := model.Step{
		Name: "Sync Artifact",
		Type: "transfer",
		With: map[string]interface{}{
			"source":     "a.tar.gz",
			"target":     "/opt/api/a.tar.gz",
			"remote_cmd": "git pull --ff-only",
			"workDir":    "/srv/api",
		},
	}

	err := plugin.Execute(ctx, step, []pipeline.Target{{HostID: "h1", HostName: "box1"}})

	require.NoError(t, err)
	assert.Empty(t, transport.transferCalls)
	assert.Equal(t, []string{"h1:/srv/api:git pull --ff-only"}, transport.remoteCalls)
	assert.Contains(t, logs, model.StreamCommand+":remote sync: git pull --ff-only")
	assert.Contains(t, logs, "stdout:synced")
}

func TestTransferRemoteCmdModeUsesPipelineSyncCommand(t *testing.T) {
	transport := &fakeSyncTransport{}
	plugin := plugins.NewTransfer(transport)
	ctx := pipeline.NewRunContext(context.Background(), pipeline.RunContextOptions{
		Vars: map[string]string{
			"sync_mode":    "remote_cmd",
			"sync_command": "git fetch --all && git reset --hard origin/main",
		},
	})
	step := model.Step{
		Name: "Sync Artifact",
		Type: "transfer",
		With: map[string]interface{}{"source": "a.tar.gz", "target": "/opt/api/a.tar.gz"},
	}

	err := plugin.Execute(ctx, step, []pipeline.Target{{HostID: "h1", HostName: "box1"}})

	require.NoError(t, err)
	assert.Empty(t, transport.transferCalls)
	assert.Equal(t, []string{"h1::git fetch --all && git reset --hard origin/main"}, transport.remoteCalls)
}

func TestTransferRemoteCmdModeRequiresRemoteSyncCommand(t *testing.T) {
	plugin := plugins.NewTransfer(&fakeSyncTransport{})
	ctx := pipeline.NewRunContext(context.Background(), pipeline.RunContextOptions{
		Vars: map[string]string{"sync_mode": "remote_cmd"},
	})
	step := model.Step{Name: "Sync Artifact", Type: "transfer", With: map[string]interface{}{"source": "a", "target": "/tmp/a"}}

	err := plugin.Execute(ctx, step, []pipeline.Target{{HostID: "h1"}})

	require.ErrorContains(t, err, "transfer remote_cmd mode requires with.remote_cmd")
}
