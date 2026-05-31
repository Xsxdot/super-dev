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
	"github.com/superdev/agent/model"
	pipelinetemplate "github.com/superdev/agent/template"
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
