// Package plugins_test 验证内置本地命令插件。
//
// 职责：
//   - 验证 local_command 不接受 roles
//   - 验证 local_command 能执行命令并上报日志
//
// 边界：
//   - 不测试 DAG 调度
//   - 不访问远程主机
package plugins_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/pipeline"
	"github.com/superdev/agent/pipeline/plugins"
)

func TestLocalCommandRejectsRoles(t *testing.T) {
	p := plugins.NewLocalCommand()
	err := p.Validate(model.Step{Name: "Build", Type: "local_command", Roles: []string{"compute"}, With: map[string]interface{}{"cmd": "echo ok"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "roles")
}

func TestLocalCommandExecutesAndLogs(t *testing.T) {
	p := plugins.NewLocalCommand()
	var logs []string
	ctx := pipeline.NewRunContext(context.Background(), pipeline.RunContextOptions{
		LogLine: func(line, stream string) { logs = append(logs, stream+":"+line) },
	})
	step := model.Step{Name: "Build", Type: "local_command", With: map[string]interface{}{"cmd": "printf ok"}}
	err := p.Execute(ctx, step, nil)
	require.NoError(t, err)
	assert.Contains(t, logs, "stdout:ok")
}

func TestLocalCommandInjectsRunTempDirEnv(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	ctx := pipeline.NewRunContext(context.Background(), pipeline.RunContextOptions{RunTempDir: dir})
	step := model.Step{
		Name: "Env",
		Type: "local_command",
		With: map[string]interface{}{
			"cmd": "printf %s \"$RUN_TEMP_DIR\" > " + out,
		},
	}
	require.NoError(t, plugins.NewLocalCommand().Execute(ctx, step, nil))
	data, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, dir, string(data))
}
