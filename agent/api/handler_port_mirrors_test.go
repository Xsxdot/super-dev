// handler_port_mirrors_test.go 验证端口镜像状态查询、WS 推送鉴权与冲突占用者停止裁决。
//
// 职责：
//   - 覆盖 GET /api/port-mirrors、POST /api/port-mirrors/retry 的基础契约
//   - 覆盖 GET /ws/port-mirrors 的鉴权常开回归（无 token 401，带 token 升级成功）
//   - 覆盖 stop-occupier 裁决函数在非托管/托管路径下的审计与停止效果
//
// 边界：
//   - 不覆盖 portmirror.Manager 自身的 reconcile 逻辑（由 portmirror 包测试覆盖）
//   - 不通过完整 SSH/lsof 链路注入真实冲突，托管/非托管裁决直接单测决策函数
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/operation"
	"github.com/xsxdot/super-dev/agent/portmirror"
)

// TestPortMirrorEndpoints 覆盖 REST 基础契约与 WS 鉴权常开回归。
func TestPortMirrorEndpoints(t *testing.T) {
	app := newTestAppForPackage(t)

	// mirror manager 空转无帧：GET /api/port-mirrors → 200 空数组。
	listResp := httptestDo(t, app, http.MethodGet, "/api/port-mirrors", nil)
	require.Equal(t, http.StatusOK, listResp.Code)
	var statuses []portmirror.MirrorStatus
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&statuses))
	assert.Empty(t, statuses)

	// POST /api/port-mirrors/retry 缺 body → 400。
	retryResp := httptestDo(t, app, http.MethodPost, "/api/port-mirrors/retry", nil)
	assert.Equal(t, http.StatusBadRequest, retryResp.Code)

	// stop-occupier 命中不存在的冲突 → 404（无需注入真实冲突即可覆盖该分支）。
	stopResp := httptestDo(t, app, http.MethodPost, "/api/port-mirrors/stop-occupier",
		strings.NewReader(`{"host_id":"h1","port":9100}`))
	assert.Equal(t, http.StatusNotFound, stopResp.Code)

	// GET /ws/port-mirrors 鉴权常开回归：new WS 路径必须被 withSecurity 覆盖。
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	wsBase := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/port-mirrors"

	// 无 access_token → 401（不是 handler 内部错误，是鉴权中间件直接拒绝）。
	conn, resp, err := websocket.DefaultDialer.Dial(wsBase, nil)
	require.Error(t, err, "无凭据的 WS 升级必须失败")
	if conn != nil {
		_ = conn.Close()
	}
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// 带 access_token（本机 token）→ 升级成功，收到一帧（初始快照，此处为空数组）。
	authedConn, _, err := websocket.DefaultDialer.Dial(wsBase+"?access_token="+app.LocalAccessToken(), nil)
	require.NoError(t, err)
	defer authedConn.Close()
	require.NoError(t, authedConn.SetReadDeadline(time.Now().Add(2*time.Second)))
	var frame []portmirror.MirrorStatus
	require.NoError(t, authedConn.ReadJSON(&frame))
	assert.Empty(t, frame)
}

// TestStopOccupierAuditTrail 单测 stop-occupier 的裁决函数：给定一个真实子进程 pid 和
// 非托管 Occupier，断言进程被 SIGTERM、审计里同时留下 prepared+executed；再用一个
// 忽略 SIGTERM 的子进程验证失败路径同样落 prepared+failed（两条路径都不建审批门）。
func TestStopOccupierAuditTrail(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM 语义与 Signal(0) 存活探测依赖 unix 信号模型，Windows 下该路径天然不可达（LookupOccupier 也仅 unix 生效）")
	}
	app := newTestAppForPackage(t)

	t.Run("non-managed success", func(t *testing.T) {
		cmd := exec.Command("sleep", "60")
		require.NoError(t, cmd.Start())
		pid := cmd.Process.Pid
		// 子进程是本测试进程的直接子进程：SIGTERM 送达后若无人 Wait()，它会停在
		// zombie 态，Signal(0) 对 zombie 仍返回成功（进程表项还在），导致存活探测
		// 永远判定"活着"。用后台 goroutine 及时收割，让 Signal(0) 反映真实状态；
		// Cleanup 只做兜底 Kill，不再二次 Wait（同一个 cmd.Wait 只能被调用一次）。
		waitDone := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(waitDone)
		}()
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			<-waitDone
		})

		occ := &portmirror.Occupier{PID: pid, Name: "sleep"}
		err := app.resolvePortMirrorConflict(context.Background(), "host-success", 9101, occ)
		require.NoError(t, err, "非托管占用者应被 SIGTERM 成功终止")

		require.Eventually(t, func() bool {
			return !processAlivePID(pid)
		}, 4*time.Second, 50*time.Millisecond, "occupier 进程应在 SIGTERM 后退出")

		events := listPortMirrorAuditEvents(t, app, "host-success")
		require.Len(t, events, 2, "应同时落 prepared 与 executed 两条审计")
		assertHasAction(t, events, operation.AuditPrepared)
		assertHasAction(t, events, operation.AuditExecuted)
		for _, ev := range events {
			assert.Equal(t, "host-success", ev.Data["host_id"])
			assert.EqualValues(t, 9101, ev.Data["port"])
			assert.EqualValues(t, pid, ev.Data["occupier_pid"])
			assert.Equal(t, "sleep", ev.Data["occupier_name"])
			assert.Equal(t, false, ev.Data["managed"])
			assert.False(t, ev.Plan.RequiresApproval, "stop-occupier 不建审批门")
		}
	})

	t.Run("non-managed failure", func(t *testing.T) {
		old := portMirrorTerminateGrace
		portMirrorTerminateGrace = 200 * time.Millisecond
		t.Cleanup(func() { portMirrorTerminateGrace = old })

		// 忽略 SIGTERM 的子进程：模拟「宽限期内仍存活」的失败路径。
		cmd := exec.Command("sh", "-c", "trap '' TERM; sleep 60")
		require.NoError(t, cmd.Start())
		pid := cmd.Process.Pid
		// cmd.Start() 只保证进程已 fork+exec，不保证 sh 已经执行到 trap 语句——
		// 若紧接着就发 SIGTERM，可能在 trap 生效前送达，命中默认终止语义，
		// 制造「明明该被忽略却死了」的假阴性。给 shell 一点时间完成 trap 安装。
		time.Sleep(150 * time.Millisecond)
		// 同上一子用例：后台收割避免 zombie 让 Signal(0) 探测失真；Cleanup 只 Kill 不再 Wait。
		waitDone := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(waitDone)
		}()
		t.Cleanup(func() {
			_ = cmd.Process.Kill() // SIGKILL 清理测试进程,不能被 trap 拦截
			<-waitDone
		})

		occ := &portmirror.Occupier{PID: pid, Name: "sh"}
		err := app.resolvePortMirrorConflict(context.Background(), "host-failure", 9102, occ)
		require.Error(t, err, "忽略 SIGTERM 的进程应在宽限期后返回错误，绝不 SIGKILL")
		assert.True(t, processAlivePID(pid), "失败路径不得杀死进程——不 SIGKILL 是硬约束")

		events := listPortMirrorAuditEvents(t, app, "host-failure")
		require.Len(t, events, 2, "应同时落 prepared 与 failed 两条审计")
		assertHasAction(t, events, operation.AuditPrepared)
		assertHasAction(t, events, operation.AuditFailed)
	})

	t.Run("managed occupier stops via runDeploymentRuntimeAction", func(t *testing.T) {
		const projectID = "proj-portmirror-stop"
		const depID = "dep-portmirror-stop"
		dep := model.Deployment{
			ID:          depID,
			EnvName:     "dev",
			Location:    model.LocationLocal,
			ControlMode: model.ControlModeManaged,
			Command:     "sleep 60",
			WorkDir:     t.TempDir(),
		}
		project := model.Project{
			ID:   projectID,
			Name: projectID,
			Services: []model.Service{{
				ID:          "svc-portmirror-stop",
				ProjectID:   projectID,
				Name:        "api",
				Deployments: []model.Deployment{dep},
			}},
		}
		app.mu.Lock()
		app.projects = append(app.projects, project)
		app.managedProjectIDs[projectID] = struct{}{}
		app.mu.Unlock()

		ctx := context.Background()
		require.NoError(t, app.startDeploymentRuntime(ctx, projectID, dep, intentStartNormal))
		mgr := app.getOrCreateManager(projectID)
		t.Cleanup(func() { mgr.StopDeployment(depID) })

		require.Eventually(t, func() bool {
			return mgr.DeploymentStatus(depID) == model.StatusRunning
		}, 2*time.Second, 20*time.Millisecond, "managed deployment 应先真实运行起来")
		pid := mgr.DeploymentPID(depID)
		require.Positive(t, pid)

		occ := &portmirror.Occupier{PID: pid, Name: "sleep", ManagedDeploymentID: depID}
		err := app.resolvePortMirrorConflict(ctx, "host-managed", 9103, occ)
		require.NoError(t, err, "托管占用者应经 runDeploymentRuntimeAction 的 stop 语义被停止")

		assert.False(t, mgr.IsDeploymentActive(depID), "托管路径必须真的走 process.Manager 停止，不是空跑")

		events := listPortMirrorAuditEvents(t, app, "host-managed")
		require.Len(t, events, 2)
		assertHasAction(t, events, operation.AuditPrepared)
		assertHasAction(t, events, operation.AuditExecuted)
		for _, ev := range events {
			assert.Equal(t, true, ev.Data["managed"])
			assert.Equal(t, depID, ev.Plan.Target.DeploymentID)
		}
	})
}

// TestVerdictForOccupierRecheck 覆盖 stop-occupier 执行前的实时复核裁决：
// pid 一致才放行；端口已释放或占用者已变化都不得执行停止——快照 pid 可能陈旧 ≥30s
// （冷却记忆），OS 复用 pid 后按旧快照发 SIGTERM 会误伤无辜进程。
func TestVerdictForOccupierRecheck(t *testing.T) {
	snap := &portmirror.Occupier{PID: 100, Name: "node"}
	assert.Equal(t, occupierVerdictProceed,
		verdictForOccupierRecheck(snap, &portmirror.Occupier{PID: 100, Name: "node"}))
	assert.Equal(t, occupierVerdictAlreadyFreed,
		verdictForOccupierRecheck(snap, nil),
		"端口已释放：跳过停止，只重试镜像")
	assert.Equal(t, occupierVerdictChanged,
		verdictForOccupierRecheck(snap, &portmirror.Occupier{PID: 101, Name: "node"}),
		"pid 已变化：拒绝执行，让用户刷新冲突详情")
}

func listPortMirrorAuditEvents(t *testing.T, app *App, hostID string) []operation.AuditEvent {
	t.Helper()
	all, err := app.operationAudit.List(context.Background(), operation.AuditFilter{Kind: operation.KindPortMirrorStopOccupier})
	require.NoError(t, err)
	out := make([]operation.AuditEvent, 0, len(all))
	for _, ev := range all {
		if s, ok := ev.Data["host_id"].(string); ok && s == hostID {
			out = append(out, ev)
		}
	}
	return out
}

func assertHasAction(t *testing.T, events []operation.AuditEvent, action string) {
	t.Helper()
	for _, ev := range events {
		if ev.Action == action {
			return
		}
	}
	t.Fatalf("expected an audit event with action %q, got %d events", action, len(events))
}
