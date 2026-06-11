// Package codedebug 验证代码调试目标解析。
//
// 职责：
//   - 确认只有本机 managed command deployment 会进入可调试目标
//   - 确认程序路径和断点路径被限制在项目根目录内
//
// 边界：
//   - 不启动 DAP adapter
//   - 不访问真实文件系统以外的远端资源
package codedebug

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestListTargetsIncludesOnlyEnabledLocalCommandDeployments(t *testing.T) {
	root := t.TempDir()
	projects := []model.Project{{
		ID:       "p1",
		Name:     "demo",
		RootPath: root,
		Services: []model.Service{{
			ID:   "svc-api",
			Name: "api",
			Deployments: []model.Deployment{
				{
					ID:        "dep-api-dev",
					EnvName:   "dev",
					Location:  model.LocationLocal,
					Command:   "go run ./cmd/api",
					WorkDir:   root,
					CodeDebug: &model.CodeDebugConfig{Enabled: true, Provider: model.CodeDebugProviderGo},
				},
				{
					ID:        "dep-api-prod",
					EnvName:   "prod",
					Location:  model.LocationRemote,
					CodeDebug: &model.CodeDebugConfig{Enabled: true, Provider: model.CodeDebugProviderGo},
				},
			},
		}},
	}}

	targets := ListTargets(projects)

	require.Len(t, targets, 1)
	assert.Equal(t, "dep-api-dev", targets[0].DeploymentID)
	assert.Equal(t, model.CodeDebugProviderGo, targets[0].Provider)
	assert.False(t, targets[0].Experimental)
	assert.Equal(t, root, targets[0].RootPath)
}

func TestListTargetsMarksNodeAsExperimental(t *testing.T) {
	root := t.TempDir()
	projects := []model.Project{{
		ID: "p1", Name: "demo", RootPath: root,
		Services: []model.Service{{
			ID: "svc-web", Name: "web",
			Deployments: []model.Deployment{{
				ID: "dep-web-dev", EnvName: "dev", Location: model.LocationLocal,
				Command: "node server.js", WorkDir: root,
				CodeDebug: &model.CodeDebugConfig{Enabled: true, Provider: model.CodeDebugProviderNode},
			}},
		}},
	}}

	targets := ListTargets(projects)

	require.Len(t, targets, 1)
	assert.Equal(t, model.CodeDebugProviderNode, targets[0].Provider)
	assert.True(t, targets[0].Experimental)
}

func TestResolvePathRejectsOutsideProjectRoot(t *testing.T) {
	root := t.TempDir()
	_, err := ResolveInsideRoot(root, "../outside.go")

	require.ErrorIs(t, err, ErrPathOutsideProject)
}

func TestResolvePathAcceptsProjectFile(t *testing.T) {
	root := t.TempDir()
	got, err := ResolveInsideRoot(root, "cmd/api/main.go")

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "cmd", "api", "main.go"), got)
}
