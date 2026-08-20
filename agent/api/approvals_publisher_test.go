package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/operation"
)

// seedHomedProject 让 app 认识一个归属在 hostID 上的项目。
func seedHomedProject(t *testing.T, app *App, projectID, hostID string) {
	t.Helper()
	require.NoError(t, app.projectHomeStore.SetHome(projectID, hostID, "/opt/"+projectID))
	app.mu.Lock()
	app.projects = append(app.projects, model.Project{ID: projectID, Name: projectID})
	app.mu.Unlock()
}

func foreignPending(id, projectID string) operation.Approval {
	return operation.Approval{
		ID:     id,
		Status: operation.ApprovalPending,
		Plan:   operation.Plan{Target: operation.Target{ProjectID: projectID}},
	}
}

// TestSnapshotMergesForeignApprovalsWithOrigin 钉死外来审批带来源进快照。
func TestSnapshotMergesForeignApprovalsWithOrigin(t *testing.T) {
	app := newTestAppForPackage(t)
	seedHomedProject(t, app, "proj-1", "h1")
	app.approvalAggregator.ApplyForTest(map[string]remoteApprovals{
		"h1": {Reachable: true, Snapshot: approvalsSnapshot{
			Pending: []operation.Approval{foreignPending("opa_mine", "proj-1")},
		}},
	})

	snap, err := app.approvalsSnapshotNow(context.Background())
	require.NoError(t, err)
	require.Len(t, snap.Pending, 1)
	require.Equal(t, "opa_mine", snap.Pending[0].ID)
	require.Equal(t, "h1", snap.Pending[0].OriginHostID)
}

// TestSnapshotFiltersUnmanagedForeignApprovals 是**闸门二**，不能省。
//
// 为什么：订阅到的是归属机上的全部审批——包括另一个控制面发起的、以及那台
// 机器上本地用户自己操作产生的。照单全收等于让本控制面去裁决它并不管辖的操作。
// 与端口镜像「不为本控制面不管理的服务擅自占用端口」同类，但后果更重：
// 那边是占一个端口，这边是替别人按下「批准」。
func TestSnapshotFiltersUnmanagedForeignApprovals(t *testing.T) {
	app := newTestAppForPackage(t)
	seedHomedProject(t, app, "proj-1", "h1")
	app.approvalAggregator.ApplyForTest(map[string]remoteApprovals{
		"h1": {Reachable: true, Snapshot: approvalsSnapshot{Pending: []operation.Approval{
			foreignPending("opa_mine", "proj-1"),
			foreignPending("opa_other", "proj-unknown"),
		}}},
	})

	snap, err := app.approvalsSnapshotNow(context.Background())
	require.NoError(t, err)

	var ids []string
	for _, ap := range snap.Pending {
		ids = append(ids, ap.ID)
	}
	require.Equal(t, []string{"opa_mine"}, ids,
		"本控制面不管辖的审批不得出现在快照里——出现即意味着可以替别人按下批准")
}

// TestSnapshotKeepsLocalApprovalsUntagged 钉死本机审批不被误标来源。
func TestSnapshotKeepsLocalApprovalsUntagged(t *testing.T) {
	app := newTestAppForPackage(t)
	_, err := app.operationApprovals.FindOrCreatePending(context.Background(),
		operation.Plan{ID: "op_1", Kind: operation.KindProjectTransfer,
			Target: operation.Target{ProjectID: "local-proj"}},
		"test", "Test")
	require.NoError(t, err)

	snap, err := app.approvalsSnapshotNow(context.Background())
	require.NoError(t, err)
	require.Len(t, snap.Pending, 1)
	require.Empty(t, snap.Pending[0].OriginHostID, "本机签发的审批 OriginHostID 必须为空")
}
