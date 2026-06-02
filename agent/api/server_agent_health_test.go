// server_agent_health_test.go 验证 App 对 agent 健康监控组件的装配。
//
// 职责：
//   - 确认 NewApp 初始化 agenthealth.Monitor
//   - 确认 App 持有取消函数用于释放后台轮询生命周期
//
// 边界：
//   - 不建立真实隧道
//   - 不测试 HTTP handler 的 agent 字段渲染
package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewAppWiresAgentHealthMonitor(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	require.NotNil(t, app.agentHealth)
	require.NotNil(t, app.agentHealthCancel)
}
