// 本文件验证临时测试数据库 MCP 工具。
//
// 职责：覆盖明文 DSN 唯一出口、参数校验、审批轮询、配额提示与幂等回收。
// 边界：不连接真实 PG/Redis，所有 agent 行为由 fakeAgentClient 提供。
package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/dbprovision"
)

func testLease(withDSN bool) dbprovision.Lease {
	dsn := "postgres://sdev_eph_tk:secret@127.0.0.1/sdev_eph_tk"
	if !withDSN {
		dsn = ""
	}
	return dbprovision.Lease{
		ID: "lease-1", ProjectID: "project-1", Purpose: "integration tests",
		Resources: []dbprovision.Resource{{Kind: dbprovision.KindPostgres, Name: "sdev_eph_tk_abc", DSN: dsn}},
	}
}

func TestAcquireTestDatabaseReturnsPlaintextDSN(t *testing.T) {
	client := newTestDatabaseFake(testDatabaseFakeState{lease: testLease(true)})
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "acquire_test_database", `{"project_id":"project-1","purpose":"integration tests"}`)
	require.NoError(t, err)
	require.False(t, result.IsError)
	payload := result.StructuredContent.(toolPayload)
	data := payload.Data.(map[string]any)
	lease, ok := data["lease"].(dbprovision.Lease)
	require.True(t, ok)
	require.Contains(t, lease.Resources[0].DSN, "secret")
}

func TestAcquireTestDatabaseRequiresPurpose(t *testing.T) {
	server := NewServer(&fakeAgentClient{})

	result, err := server.callToolForTest(context.Background(), "acquire_test_database", `{"project_id":"project-1"}`)
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.StructuredContent.(toolErrorPayload).Message, "purpose")
}

func TestAcquireTestDatabaseWaitsForApproval(t *testing.T) {
	client := newTestDatabaseFake(testDatabaseFakeState{
		lease: dbprovision.Lease{ID: "lease-approved"},
		errs: []error{AgentError{
			Code: "approval_required", Message: "approval required",
			Approval: OperationApproval{ID: "approval-db", Status: "pending"},
		}},
	})
	client.operationApprovalDetail = OperationApprovalDetail{
		Approval:      OperationApproval{ID: "approval-db", Status: "approved"},
		ApprovalToken: "token-db",
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "acquire_test_database", `{"project_id":"project-1","purpose":"integration tests","approval_wait_seconds":1}`)
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, 2, testDatabaseState(client).acquireCall)
	require.Equal(t, "token-db", client.lastApprovalToken)
}

func TestAcquireTestDatabaseSurfacesQuotaListing(t *testing.T) {
	client := newTestDatabaseFake(testDatabaseFakeState{err: AgentError{
		Code: "quota_exceeded", Message: "quota exceeded: sdev_eph_tk_abc expires soon",
	}})
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "acquire_test_database", `{"project_id":"project-1","purpose":"integration tests"}`)
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.StructuredContent.(toolErrorPayload).Message, "sdev_eph_")
}

func TestListTestDatabasesNeverReturnsDSN(t *testing.T) {
	client := newTestDatabaseFake(testDatabaseFakeState{leases: []dbprovision.Lease{testLease(false)}})
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "list_test_databases", `{}`)
	require.NoError(t, err)
	require.False(t, result.IsError)
	payload := result.StructuredContent.(toolPayload)
	raw, err := json.Marshal(payload.Data)
	require.NoError(t, err)
	require.NotContains(t, strings.ToLower(string(raw)), "secret")
}

func TestReleaseTestDatabaseIsIdempotent(t *testing.T) {
	client := newTestDatabaseFake(testDatabaseFakeState{})
	server := NewServer(client)
	for i := 0; i < 2; i++ {
		result, err := server.callToolForTest(context.Background(), "release_test_database", `{"lease_id":"lease-1"}`)
		require.NoError(t, err)
		require.False(t, result.IsError)
	}
	require.Equal(t, 2, testDatabaseState(client).releaseCall)
}
