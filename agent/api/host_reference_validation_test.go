// host_reference_validation_test.go 测试 host 引用归一化与校验。
//
// 职责：
//   - 验证 pipeline roles 中的 host name 被归一化为 host ID
//   - 验证已是 ID 的引用与未知引用的处理
//
// 边界：
//   - 不触达 store/网络，纯函数测试
package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestNormalizePipelineRoleHostsTranslatesNameToID(t *testing.T) {
	hosts := []model.Host{
		{ID: "h-uuid-1", Name: "local-01"},
		{ID: "h-uuid-2", Name: "local-02"},
	}
	project := model.Project{
		Pipelines: []model.ProjectPipeline{{
			ID: "pp-1",
			Pipeline: model.Pipeline{
				Roles: map[string][]string{
					// 一个存 name、一个已存 ID，混合
					"compute": {"local-01", "h-uuid-2"},
				},
			},
		}},
	}

	normalizePipelineRoleHosts(&project, hosts)

	assert.Equal(t, []string{"h-uuid-1", "h-uuid-2"}, project.Pipelines[0].Pipeline.Roles["compute"])
}

func TestNormalizePipelineRoleHostsKeepsUnknownReference(t *testing.T) {
	hosts := []model.Host{{ID: "h-uuid-1", Name: "local-01"}}
	project := model.Project{
		Pipelines: []model.ProjectPipeline{{
			ID: "pp-1",
			Pipeline: model.Pipeline{
				Roles: map[string][]string{"compute": {"ghost-host"}},
			},
		}},
	}

	normalizePipelineRoleHosts(&project, hosts)

	// 未知引用原样保留，交给现有 host 校验报错，不静默吞掉
	assert.Equal(t, []string{"ghost-host"}, project.Pipelines[0].Pipeline.Roles["compute"])
}

func TestNormalizePipelineRoleHostsDedupesAfterTranslation(t *testing.T) {
	hosts := []model.Host{{ID: "h-uuid-1", Name: "local-01"}}
	project := model.Project{
		Pipelines: []model.ProjectPipeline{{
			ID: "pp-1",
			Pipeline: model.Pipeline{
				// name 和 ID 指向同一台机，归一化后应去重为一个 ID
				Roles: map[string][]string{"compute": {"local-01", "h-uuid-1"}},
			},
		}},
	}

	normalizePipelineRoleHosts(&project, hosts)

	assert.Equal(t, []string{"h-uuid-1"}, project.Pipelines[0].Pipeline.Roles["compute"])
}
