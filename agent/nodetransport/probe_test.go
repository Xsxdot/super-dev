// probe_test.go 验证传输链探活协议。
//
// 职责：
//   - 覆盖 probe result 与 route status 的 JSON 协议
//   - 锁定前端依赖的错误分类枚举
//
// 边界：
//   - 不建立真实网络连接，具体 probe 执行在 dispatcher/direct 测试覆盖
package nodetransport_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

func TestRouteStatusJSONShape(t *testing.T) {
	now := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	status := nodetransport.NodeStatus{
		HostID:    "h1",
		Reachable: true,
		Agent:     model.AgentRuntime{Health: model.AgentHealthHealthy, Reachable: true},
		Route: &nodetransport.RouteStatus{
			SelectedIndex: 1,
			SelectedType:  model.TransportTypeTunnel,
			Degraded:      true,
			LastResults: []nodetransport.ProbeResult{
				{Index: 0, TransportType: model.TransportTypeDirect, Status: nodetransport.ProbeStatusUnreachable, Error: "connection refused", CheckedAt: now},
				{Index: 1, TransportType: model.TransportTypeTunnel, Status: nodetransport.ProbeStatusReachable, Version: "0.1.0", CheckedAt: now},
			},
		},
		UpdatedAt: now,
	}

	data, err := json.Marshal(status)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"route"`)
	assert.Contains(t, string(data), `"selected_index":1`)
	assert.Contains(t, string(data), `"selected_type":"tunnel"`)
	assert.Contains(t, string(data), `"degraded":true`)
	assert.Contains(t, string(data), `"status":"unreachable"`)
	assert.NotContains(t, string(data), `"Route"`)
}
