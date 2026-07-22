// store_test.go 通过 debugcredential 的公开 interface 验证 lease 生命周期。
//
// 职责：
//   - 验证 owner 精确删除、TTL 回收和 restart-clears 语义
//   - 验证冲突与非法输入 fail closed
//
// 边界：
//   - 不启动 HTTP Agent 或 MCP
//   - 测试凭据只使用固定假值，不读取真实环境 secret
package debugcredential_test

import (
	"bytes"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/debugcredential"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestLeaseOwnerTTLAndRestartLifecycle(t *testing.T) {
	now := time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC)
	store := debugcredential.NewStore(debugcredential.Options{Now: func() time.Time { return now }})
	created, err := store.Create(debugcredential.CreateRequest{
		ProjectID: "p1", ServiceID: "s1", Owner: "campaign-1", TTLSeconds: 60,
		Credentials: []model.DebugCredential{{Name: "login", Value: "test-only-secret", Desc: "test login"}},
	})
	require.NoError(t, err)
	require.Len(t, store.Active("p1", "s1").Service, 1)

	_, err = store.Delete(created.ID, "other-campaign")
	assert.ErrorIs(t, err, debugcredential.ErrLeaseNotFound)
	require.Len(t, store.Active("p1", "s1").Service, 1)

	now = now.Add(60 * time.Second)
	assert.Empty(t, store.Active("p1", "s1").Service)
	_, err = store.Delete(created.ID, "campaign-1")
	assert.ErrorIs(t, err, debugcredential.ErrLeaseNotFound)

	// Store 没有任何持久化装载入口，新 Agent 创建的新实例天然不继承旧进程的 lease。
	restarted := debugcredential.NewStore(debugcredential.Options{Now: func() time.Time { return now }})
	assert.Empty(t, restarted.Active("p1", "s1").Service)
}

func TestLeaseRejectsConflictAndInvalidTTL(t *testing.T) {
	store := debugcredential.NewStore(debugcredential.Options{})
	req := debugcredential.CreateRequest{
		ProjectID: "p1", Owner: "campaign-1", TTLSeconds: 60,
		Credentials: []model.DebugCredential{{Name: "login", Value: "test-only-secret", Desc: "test login"}},
	}
	_, err := store.Create(req)
	require.NoError(t, err)
	_, err = store.Create(req)
	assert.ErrorIs(t, err, debugcredential.ErrLeaseConflict)

	req.Owner = "campaign-2"
	req.TTLSeconds = int(debugcredential.MaxTTL/time.Second) + 1
	_, err = store.Create(req)
	assert.True(t, errors.Is(err, debugcredential.ErrInvalidLease))
}

func TestLeaseRejectsTTLThatWouldOverflowDurationIntoValidRange(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("overflow regression requires the supported 64-bit Agent target")
	}

	// 2^55 秒乘以 time.Second 后会按 int64 回绕；再加 3600 秒会伪装成一小时。
	// 公共 TTL 合同必须按调用方原始秒数拒绝，而不是接受回绕后的表面有效时长。
	const overflowingTTLSeconds int64 = (1 << 55) + 3600
	store := debugcredential.NewStore(debugcredential.Options{})
	created, err := store.Create(debugcredential.CreateRequest{
		ProjectID: "p1", Owner: "campaign-overflow", TTLSeconds: int(overflowingTTLSeconds),
		Credentials: []model.DebugCredential{{Name: "login", Value: "test-only-secret"}},
	})

	assert.ErrorIs(t, err, debugcredential.ErrInvalidLease)
	assert.Empty(t, created.ID)
}

func TestLeaseAppliesDefaultTTLAndAcceptsMaximumBoundary(t *testing.T) {
	now := time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC)
	store := debugcredential.NewStore(debugcredential.Options{Now: func() time.Time { return now }})
	created, err := store.Create(debugcredential.CreateRequest{
		ProjectID: "p1", Owner: "campaign-default",
		Credentials: []model.DebugCredential{{Name: "login", Value: "test-only-secret"}},
	})
	require.NoError(t, err)
	assert.Equal(t, now.Add(debugcredential.DefaultTTL), created.ExpiresAtUTC)

	created, err = store.Create(debugcredential.CreateRequest{
		ProjectID: "p1", Owner: "campaign-max", TTLSeconds: int(debugcredential.MaxTTL / time.Second),
		Credentials: []model.DebugCredential{{Name: "login", Value: "test-only-secret"}},
	})
	require.NoError(t, err)
	assert.Equal(t, now.Add(debugcredential.MaxTTL), created.ExpiresAtUTC)
}

func TestLeaseClearRemovesAllActiveCredentials(t *testing.T) {
	store := debugcredential.NewStore(debugcredential.Options{})
	_, err := store.Create(debugcredential.CreateRequest{
		ProjectID: "p1", Owner: "campaign-1",
		Credentials: []model.DebugCredential{{Name: "login", Value: "test-only-secret"}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, store.Clear())
	assert.Empty(t, store.Active("p1", "").Project)
	assert.Equal(t, 0, store.Clear())
}

func TestLeaseScopeRevocationPreservesUnrelatedScopes(t *testing.T) {
	store := debugcredential.NewStore(debugcredential.Options{})
	create := func(projectID, serviceID, owner, value string) {
		t.Helper()
		_, err := store.Create(debugcredential.CreateRequest{
			ProjectID: projectID, ServiceID: serviceID, Owner: owner,
			Credentials: []model.DebugCredential{{Name: "login", Value: value}},
		})
		require.NoError(t, err)
	}
	create("p1", "", "project-owner", "p1-project-secret")
	create("p1", "s1", "service-one-owner", "p1-s1-secret")
	create("p1", "s2", "service-two-owner", "p1-s2-secret")
	create("p2", "s1", "other-project-owner", "p2-s1-secret")

	assert.Equal(t, 1, store.RevokeService("p1", "s1"))
	assert.Empty(t, store.Active("p1", "s1").Service)
	assert.Len(t, store.Active("p1", "s2").Project, 1)
	assert.Len(t, store.Active("p1", "s2").Service, 1)
	assert.Len(t, store.Active("p2", "s1").Service, 1)

	assert.Equal(t, 2, store.RevokeProject("p1"))
	assert.Empty(t, store.Active("p1", "s2").Project)
	assert.Empty(t, store.Active("p1", "s2").Service)
	assert.Len(t, store.Active("p2", "s1").Service, 1)
	assert.Equal(t, 0, store.RevokeProject(""))
	assert.Equal(t, 0, store.RevokeService("p2", ""))
}

func TestLeaseScopeRevocationLogsOnlyScopeAndCount(t *testing.T) {
	store := debugcredential.NewStore(debugcredential.Options{})
	created, err := store.Create(debugcredential.CreateRequest{
		ProjectID: "p1", ServiceID: "s1", Owner: "owner-must-not-be-logged-on-revoke",
		Credentials: []model.DebugCredential{{Name: "credential-name", Value: "secret-must-not-be-logged", Desc: "credential-desc"}},
	})
	require.NoError(t, err)

	var logBuffer bytes.Buffer
	structuredLogger := logger.GetLogger().GetLogger().Logger
	oldWriter := structuredLogger.Out
	structuredLogger.SetOutput(&logBuffer)
	t.Cleanup(func() { structuredLogger.SetOutput(oldWriter) })

	assert.Equal(t, 1, store.RevokeService("p1", "s1"))
	output := logBuffer.String()
	assert.Contains(t, output, "project_id=p1")
	assert.Contains(t, output, "service_id=s1")
	assert.Contains(t, output, "count=1")
	assert.NotContains(t, output, created.ID)
	assert.NotContains(t, output, "owner-must-not-be-logged-on-revoke")
	assert.NotContains(t, output, "credential-name")
	assert.NotContains(t, output, "credential-desc")
	assert.NotContains(t, output, "secret-must-not-be-logged")
}
