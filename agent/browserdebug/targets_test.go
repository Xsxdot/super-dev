// Package browserdebug 测试本机前端浏览器调试目标解析。
//
// 职责：
//   - 验证只暴露本机 loopback Web entrypoint
//   - 验证远端和非 loopback URL 被拒绝
package browserdebug

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestListTargetsOnlyReturnsLocalEnabledLoopbackEntries(t *testing.T) {
	projects := []model.Project{{
		ID:   "p1",
		Name: "demo",
		Services: []model.Service{{
			ID:        "svc-admin",
			ProjectID: "p1",
			Name:      "admin",
			Deployments: []model.Deployment{{
				ID:       "dep-admin-dev",
				EnvName:  "dev",
				Location: model.LocationLocal,
				Web: &model.WebEntrypointConfig{
					Enabled:     true,
					URL:         "http://127.0.0.1:3000",
					DefaultPath: "/",
					AIDebug:     model.WebAIDebugConfig{Enabled: true},
				},
			}, {
				ID:       "dep-api-dev",
				EnvName:  "dev",
				Location: model.LocationLocal,
			}, {
				ID:       "dep-admin-prod",
				EnvName:  "prod",
				Location: model.LocationRemote,
				Web:      &model.WebEntrypointConfig{Enabled: true, URL: "http://127.0.0.1:3000"},
			}},
		}},
	}}

	targets := ListTargets(projects)
	require.Len(t, targets, 1)
	assert.Equal(t, "dep-admin-dev", targets[0].DeploymentID)
	assert.Equal(t, "http://127.0.0.1:3000", targets[0].BaseURL)
}

func TestListTargetsTreatsEmptyLocationAsLocal(t *testing.T) {
	projects := []model.Project{{
		ID:   "p1",
		Name: "demo",
		Services: []model.Service{{
			ID:        "svc-admin",
			ProjectID: "p1",
			Name:      "admin",
			Deployments: []model.Deployment{{
				ID:      "dep-legacy",
				EnvName: "dev",
				Web: &model.WebEntrypointConfig{
					Enabled:     true,
					URL:         "http://127.0.0.1:5173",
					DefaultPath: "/",
					AIDebug:     model.WebAIDebugConfig{Enabled: true},
				},
			}},
		}},
	}}

	targets := ListTargets(projects)
	require.Len(t, targets, 1)
	assert.Equal(t, "dep-legacy", targets[0].DeploymentID)
}

func TestBuildTargetURLRejectsNonLoopback(t *testing.T) {
	_, err := BuildTargetURL("https://example.com", "/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loopback")
}

func TestBuildTargetURLAppliesPath(t *testing.T) {
	got, err := BuildTargetURL("http://localhost:5173/app", "/users")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:5173/users", got)
}
