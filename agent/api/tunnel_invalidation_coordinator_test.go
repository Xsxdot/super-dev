// tunnel_invalidation_coordinator_test.go 验证 tunnel 失效审计编排的故障恢复语义。
//
// 职责：
//   - 证明配置中的 pending marker 在审计读取失败时仍保留已提交事实
//   - 证明恢复路径会优先断开可能残留的旧 tunnel
//
// 边界：
//   - 不读写真实审计文件
//   - 不建立真实 SSH tunnel
package api

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/operation"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

type staticTunnelInvalidationAuditStore struct {
	events []operation.AuditEvent
	err    error
}

func (s *staticTunnelInvalidationAuditStore) Append(_ context.Context, event operation.AuditEvent) (operation.AuditEvent, error) {
	return event, nil
}

func (s *staticTunnelInvalidationAuditStore) List(context.Context, operation.AuditFilter) ([]operation.AuditEvent, error) {
	return s.events, s.err
}

func TestTunnelInvalidationRecoveryKeepsPersistedFactsWhenAuditListFails(t *testing.T) {
	listErr := errors.New("audit file temporarily unavailable")
	store := &staticTunnelInvalidationAuditStore{err: listErr}
	disconnected := make([]string, 0, 1)
	invalidator := newAuditedTunnelRuntimeInvalidator(
		func(string) tunnel.Status { return tunnel.StatusConnected },
		func(hostID string) { disconnected = append(disconnected, hostID) },
		func() operation.AuditStore { return store },
	)

	result, err := invalidator.Recover(context.Background(), tunnelRuntimeInvalidationRecovery{
		HostID:           "host-1",
		TargetKind:       tunnelInvalidationTargetHost,
		Mutation:         tunnelInvalidationMutationUpdate,
		ExpectedRevision: "revision-from-persisted-host",
		Persisted:        true,
	})

	require.ErrorIs(t, err, listErr)
	assert.True(t, result.AuditPrepared)
	assert.True(t, result.Persisted)
	assert.True(t, result.TunnelInvalidated)
	assert.False(t, result.AuditCompleted)
	assert.Equal(t, []string{"host-1"}, disconnected)
}

func TestTunnelInvalidationRecoveryAcceptsExecutedEventAfterPreparedWasTrimmed(t *testing.T) {
	const revision = "revision-from-persisted-host"
	store := &staticTunnelInvalidationAuditStore{events: []operation.AuditEvent{{
		Action: operation.AuditExecuted,
		Plan: operation.Plan{
			ID:     "op-terminal-only",
			Kind:   operation.OperationTunnelInvalidate,
			Target: operation.Target{HostID: "host-1"},
		},
		Data: map[string]any{
			"target_kind":        tunnelInvalidationTargetHost,
			"mutation":           tunnelInvalidationMutationUpdate,
			"expected_revision":  revision,
			"mutation_persisted": true,
		},
	}}}
	disconnected := make([]string, 0, 1)
	invalidator := newAuditedTunnelRuntimeInvalidator(
		func(string) tunnel.Status { return tunnel.StatusDisconnected },
		func(hostID string) { disconnected = append(disconnected, hostID) },
		func() operation.AuditStore { return store },
	)

	result, err := invalidator.Recover(context.Background(), tunnelRuntimeInvalidationRecovery{
		HostID:           "host-1",
		TargetKind:       tunnelInvalidationTargetHost,
		Mutation:         tunnelInvalidationMutationUpdate,
		ExpectedRevision: revision,
		Persisted:        true,
	})

	require.NoError(t, err)
	assert.True(t, result.AuditPrepared)
	assert.True(t, result.Persisted)
	assert.True(t, result.TunnelInvalidated)
	assert.True(t, result.AuditCompleted)
	assert.Empty(t, disconnected)
}
