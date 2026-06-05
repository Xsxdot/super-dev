// Package template_test 验证 include 模板展开行为。
//
// 职责：
//   - 验证 include 步骤被命名空间化展开
//   - 验证 include 入口依赖与叶子依赖重连
//   - 验证 include vars 渲染到模板步骤
//
// 边界：
//   - 不访问真实模板库文件
//   - 不执行 DAG 校验或插件命令
package template_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	pipelinetemplate "github.com/xsxdot/super-dev/agent/template"
)

type memoryResolver map[string]pipelinetemplate.VersionedTemplate

func (m memoryResolver) Resolve(uri, version, digest string) (pipelinetemplate.VersionedTemplate, error) {
	return m[uri+"@"+version], nil
}

func TestExpandIncludeRelinksEntrypointsAndLeaves(t *testing.T) {
	tpl := pipelinetemplate.Template{
		ID: "deploy", Name: "Deploy", Version: "1.0.0",
		Steps: []model.Step{
			{Name: "Prepare", Type: "remote_command", Roles: []string{"${vars.role}"}},
			{Name: "Upload", Type: "transfer", Needs: []string{"Prepare"}},
			{Name: "Restart", Type: "remote_command", Needs: []string{"Upload"}},
		},
	}
	digest, err := pipelinetemplate.Digest(tpl)
	require.NoError(t, err)
	resolver := memoryResolver{
		"builtin://deploy@1.0.0": {Source: "builtin", Template: tpl, Digest: digest},
	}
	steps := []model.Step{
		{Name: "Build", Type: "local_command"},
		{Name: "Deploy", Type: "include", Needs: []string{"Build"}, With: map[string]interface{}{
			"template": "builtin://deploy",
			"version":  "1.0.0",
			"digest":   digest,
			"vars": map[string]interface{}{
				"role": "compute",
			},
		}},
		{Name: "After", Type: "local_command", Needs: []string{"Deploy"}},
	}

	got, err := pipelinetemplate.ExpandSteps(steps, resolver, map[string]string{}, 5)
	require.NoError(t, err)
	names := make([]string, 0, len(got))
	needs := map[string][]string{}
	for _, s := range got {
		names = append(names, s.Name)
		needs[s.Name] = s.Needs
	}
	assert.Equal(t, []string{"Build", "Deploy.Prepare", "Deploy.Upload", "Deploy.Restart", "After"}, names)
	assert.Equal(t, []string{"Build"}, needs["Deploy.Prepare"])
	assert.Equal(t, []string{"Deploy.Prepare"}, needs["Deploy.Upload"])
	assert.Equal(t, []string{"Deploy.Upload"}, needs["Deploy.Restart"])
	assert.Equal(t, []string{"Deploy.Restart"}, needs["After"])
	assert.Equal(t, []string{"compute"}, got[1].Roles)
}

func TestExpandIncludeInheritsIncludeRolesForTemplateStepsWithoutRoles(t *testing.T) {
	tpl := pipelinetemplate.Template{
		ID: "runner-aware", Name: "Runner Aware", Version: "1.0.0",
		Steps: []model.Step{
			{Name: "Prepare", Type: "remote_command"},
			{Name: "Deploy", Type: "remote_command", Roles: []string{"${vars.role}"}, Needs: []string{"Prepare"}},
		},
	}
	digest, err := pipelinetemplate.Digest(tpl)
	require.NoError(t, err)
	resolver := memoryResolver{
		"builtin://runner-aware@1.0.0": {Source: "builtin", Template: tpl, Digest: digest},
	}
	steps := []model.Step{{
		Name:  "Run Template",
		Type:  "include",
		Roles: []string{"build_runner"},
		With: map[string]interface{}{
			"template": "builtin://runner-aware",
			"version":  "1.0.0",
			"digest":   digest,
			"vars": map[string]interface{}{
				"role": "deploy_targets",
			},
		},
	}}

	got, err := pipelinetemplate.ExpandSteps(steps, resolver, map[string]string{}, 5)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, []string{"build_runner"}, got[0].Roles)
	assert.Equal(t, []string{"deploy_targets"}, got[1].Roles)
}

func TestExpandIncludePreservesStructuredVars(t *testing.T) {
	tpl := pipelinetemplate.Template{
		ID: "package", Name: "Package", Version: "1.0.0",
		Steps: []model.Step{{
			Name: "Archive",
			Type: "archive_package",
			With: map[string]interface{}{
				"artifact": "${vars.artifact}",
				"files":    "${vars.files}",
			},
		}},
	}
	digest, err := pipelinetemplate.Digest(tpl)
	require.NoError(t, err)
	resolver := memoryResolver{
		"builtin://package@1.0.0": {Source: "builtin", Template: tpl, Digest: digest},
	}
	steps := []model.Step{{
		Name: "Package",
		Type: "include",
		With: map[string]interface{}{
			"template": "builtin://package",
			"version":  "1.0.0",
			"digest":   digest,
			"vars": map[string]interface{}{
				"artifact": "${artifacts}/api.tar.gz",
				"files": []interface{}{
					map[string]interface{}{"from": "${workspace}/bin/api", "to": "bin/api"},
					map[string]interface{}{"from": "${workspace}/config.yaml", "to": "config/config.yaml"},
				},
			},
		},
	}}

	got, err := pipelinetemplate.ExpandSteps(steps, resolver, map[string]string{
		"workspace": "/repo",
		"artifacts": "/tmp/artifacts",
	}, 5)

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "/tmp/artifacts/api.tar.gz", got[0].With["artifact"])
	assert.Equal(t, []interface{}{
		map[string]interface{}{"from": "/repo/bin/api", "to": "bin/api"},
		map[string]interface{}{"from": "/repo/config.yaml", "to": "config/config.yaml"},
	}, got[0].With["files"])
}
