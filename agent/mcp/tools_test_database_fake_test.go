// 本文件为临时测试数据库 MCP 工具提供 fake AgentClient 行为。
//
// 职责：隔离新增工具测试状态，避免改动共享的运行态 fake 结构。
// 边界：仅用于测试，不代表生产 client 的存储或并发模型。
package mcp

import (
	"context"
	"sync"

	"github.com/xsxdot/super-dev/agent/dbprovision"
)

type testDatabaseFakeState struct {
	lease       dbprovision.Lease
	leases      []dbprovision.Lease
	err         error
	errs        []error
	acquireCall int
	releaseCall int
}

var testDatabaseFakeStates sync.Map

func newTestDatabaseFake(state testDatabaseFakeState) *fakeAgentClient {
	client := &fakeAgentClient{}
	testDatabaseFakeStates.Store(client, &state)
	return client
}

func testDatabaseState(client *fakeAgentClient) *testDatabaseFakeState {
	value, _ := testDatabaseFakeStates.Load(client)
	return value.(*testDatabaseFakeState)
}

func (f *fakeAgentClient) AcquireTestDatabase(_ context.Context, projectID string, req TestDatabaseAcquireRequest, approvalToken string) (dbprovision.Lease, error) {
	state := testDatabaseState(f)
	state.acquireCall++
	f.lastApprovalToken = approvalToken
	if state.acquireCall <= len(state.errs) && state.errs[state.acquireCall-1] != nil {
		return dbprovision.Lease{}, state.errs[state.acquireCall-1]
	}
	if state.err != nil {
		return dbprovision.Lease{}, state.err
	}
	return state.lease, nil
}

func (f *fakeAgentClient) ReleaseTestDatabase(context.Context, string) error {
	testDatabaseState(f).releaseCall++
	return nil
}

func (f *fakeAgentClient) RenewTestDatabase(context.Context, string, TestDatabaseRenewRequest) (dbprovision.Lease, error) {
	return testDatabaseState(f).lease, nil
}

func (f *fakeAgentClient) ListTestDatabases(context.Context) ([]dbprovision.Lease, error) {
	return testDatabaseState(f).leases, nil
}
