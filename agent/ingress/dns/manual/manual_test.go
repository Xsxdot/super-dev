// Package manual 验证人工 DNS provider 的只读指令语义。
//
// 职责：
//   - 确保 EnsureRecord 返回用户应手动创建的记录
//   - 确保删除不伪装成自动成功
//
// 边界：
//   - 不访问任何 DNS API
package manual

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/ingress"
)

func TestEnsureRecordReturnsManualInstructions(t *testing.T) {
	provider := New()
	result, err := provider.EnsureRecord(context.Background(), ingress.Record{
		Type:  ingress.RecordA,
		Name:  "api.example.com",
		Value: "203.0.113.10",
		TTL:   300,
	})
	require.NoError(t, err)
	assert.True(t, result.Manual)
	assert.False(t, result.Changed)
	assert.Contains(t, result.Instructions[0], "Create DNS A record api.example.com -> 203.0.113.10")
}

func TestRemoveRecordRequiresManualAction(t *testing.T) {
	err := New().RemoveRecord(context.Background(), ingress.Record{Name: "api.example.com"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "manual DNS provider cannot remove records automatically")
}
