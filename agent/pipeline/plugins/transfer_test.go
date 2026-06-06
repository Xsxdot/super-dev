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
