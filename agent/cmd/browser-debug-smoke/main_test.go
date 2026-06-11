// Command browser-debug-smoke tests smoke command configuration parsing.
//
// 职责：
//   - 验证本机浏览器调试 smoke 命令的环境变量解析
//   - 防止必填 deployment ID 被静默忽略
//
// 边界：
//   - 不连接真实 agent
//   - 不启动真实浏览器
package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadSmokeConfigFromEnv(t *testing.T) {
	t.Setenv("SUPERDEV_AGENT_URL", " http://127.0.0.1:57019 ")
	t.Setenv("SUPERDEV_BROWSER_ID", " arc ")
	t.Setenv("SUPERDEV_DEPLOYMENT_ID", " dep-admin-dev ")
	t.Setenv("SUPERDEV_SKIP_CLOSE", "true")

	cfg, err := loadSmokeConfigFromEnv()

	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:57019", cfg.AgentURL)
	assert.Equal(t, "arc", cfg.BrowserID)
	assert.Equal(t, "dep-admin-dev", cfg.DeploymentID)
	assert.True(t, cfg.SkipClose)
}

func TestLoadSmokeConfigRequiresDeploymentID(t *testing.T) {
	t.Setenv("SUPERDEV_DEPLOYMENT_ID", "")

	_, err := loadSmokeConfigFromEnv()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "SUPERDEV_DEPLOYMENT_ID")
}
