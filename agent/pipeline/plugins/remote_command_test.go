// Package plugins_test 验证远程命令插件。
//
// 职责：
//   - 验证 remote_command 校验 with.cmd
//   - 验证 remote_command 会调用远程 runner
//
// 边界：
//   - 不连接真实 SSH
//   - 不测试 DAG 调度
package plugins_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
	"github.com/xsxdot/super-dev/agent/pipeline/plugins"
)

type fakeRemoteRunner struct {
	calls []string
}

func (f *fakeRemoteRunner) RunRemote(ctx context.Context, target pipeline.Target, cmd string, workDir string, onLine func(string, string)) error {
	f.calls = append(f.calls, target.HostID+":"+cmd+":"+workDir)
	onLine("ok", "stdout")
	return nil
}

func TestRemoteCommandRequiresCommand(t *testing.T) {
	err := plugins.NewRemoteCommand(nil).Validate(model.Step{With: map[string]interface{}{}})
	require.ErrorContains(t, err, "with.cmd")
}

func TestRemoteCommandValidateTargetsRequiresTargets(t *testing.T) {
	plugin := plugins.NewRemoteCommand(nil)

	err := plugin.ValidateTargets(model.Step{Name: "Restart", Type: "remote_command"}, nil)

	require.ErrorContains(t, err, "remote_command requires targets")
}

func TestRemoteCommandExecutesTargets(t *testing.T) {
	runner := &fakeRemoteRunner{}
	p := plugins.NewRemoteCommand(runner)
	var logs []string
	ctx := pipeline.NewRunContext(context.Background(), pipeline.RunContextOptions{
		LogLine: func(line, stream string) { logs = append(logs, stream+":"+line) },
	})
	step := model.Step{Name: "Restart", Type: "remote_command", With: map[string]interface{}{"cmd": "systemctl restart api", "workDir": "/opt/api"}}

	err := p.Execute(ctx, step, []pipeline.Target{{HostID: "h1", HostName: "box1"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"h1:systemctl restart api:/opt/api"}, runner.calls)
	assert.Contains(t, logs, "stdout:ok")
}
