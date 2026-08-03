// project_transfer_engine_test.go 验证项目转移执行引擎（forward + 迁回）。
//
// 白盒测试（package api）：直接注入 transferRemoteRunner（/ws/exec 假件）与
// transferNodeDo（nodetransport 假件），配真实的 projectHomeStore /
// operationAudit（newTestAppForPackage 已建），断言步骤序列、终态、审计留痕、
// 归属切换、失败跳过语义——engine 级测试，不走 HTTP 层。
package api

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
	"github.com/xsxdot/super-dev/agent/operation"
)

// setTransferNodeDo 注入 nodetransport Do 假件并注册复位，避免污染包级 seam。
func setTransferNodeDo(t *testing.T, fn func(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error)) {
	t.Helper()
	transferNodeDo = fn
	t.Cleanup(func() { transferNodeDo = nil })
}

// nodeRespJSON 构造一个 200 JSON NodeResponse。
func nodeRespJSON(body string) nodetransport.NodeResponse {
	return nodetransport.NodeResponse{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// transferSuccessRunner 是全绿路径的 /ws/exec 假件：目标目录不存在→clone，
// register 的 cd&&pwd 回显绝对路径，asset_audit 的 test -f / command -v 均通过。
func transferSuccessRunner(absDir string) func(ctx context.Context, cmd, workDir string) (string, int, error) {
	return func(_ context.Context, cmd, _ string) (string, int, error) {
		switch {
		case strings.Contains(cmd, "test -d"):
			return "no", 0, nil // 目录不存在 → EnsureCheckout 走 clone
		case strings.Contains(cmd, "git clone"):
			return "Cloning into ...\nDone.", 0, nil
		case strings.Contains(cmd, "pwd"):
			return absDir, 0, nil // cd '<absDir>' && pwd
		case strings.Contains(cmd, "test -f"):
			return "", 0, nil // env 文件存在
		case strings.Contains(cmd, "command -v superdev-mcp"):
			return "/usr/local/bin/superdev-mcp", 0, nil
		default:
			return "", 0, nil
		}
	}
}

// registerStep / mcpSetupStep 用的 Do 假件：/api/projects 回指定 id，
// /api/mcp-setup/claude-code 视 mcpOK 决定成功或失败。
func transferDo(projectID string, mcpOK bool) func(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	return func(_ context.Context, _ string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
		switch req.Path {
		case "/api/projects":
			return nodeRespJSON(`{"id":"` + projectID + `"}`), nil
		case "/api/mcp-setup/claude-code":
			if mcpOK {
				return nodeRespJSON(`{"ok":true}`), nil
			}
			return nodetransport.NodeResponse{}, nodetransport.ErrHostUnreachable
		default:
			return nodeRespJSON(`{}`), nil
		}
	}
}

// seedTransferProject 在 App 内落地一个带 dev 环境 + 一个本机托管 dev 部署的项目。
func seedTransferProject(t *testing.T, app *App, projectID, rootPath, envFile, command string) model.Project {
	t.Helper()
	project := model.Project{
		ID:       projectID,
		Name:     "transfer-demo",
		RootPath: rootPath,
		Environments: []model.Environment{
			{ID: "env-dev", Name: "dev", IsDev: true, Order: 0},
		},
		Services: []model.Service{{
			ID:        "svc-web",
			ProjectID: projectID,
			Name:      "web",
			Deployments: []model.Deployment{{
				ID:          "dep-web-dev",
				EnvName:     "dev",
				Location:    model.LocationLocal,
				ControlMode: model.ControlModeManaged,
				Command:     command,
				WorkDir:     t.TempDir(),
				EnvFile:     envFile,
			}},
		}},
	}
	app.mu.Lock()
	app.appendProjectLocked(project)
	app.mu.Unlock()
	return project
}

// stepStates 抽取步骤码→状态映射，供断言序列/跳过语义。
func stepStates(resp transferStatusResponse) map[string]string {
	m := make(map[string]string, len(resp.Steps))
	for _, s := range resp.Steps {
		m[s.Code] = s.State
	}
	return m
}

func auditActions(t *testing.T, app *App, projectID string) []string {
	t.Helper()
	events, err := app.operationAudit.List(context.Background(), operation.AuditFilter{
		Kind:      operation.KindProjectTransfer,
		ProjectID: projectID,
	})
	require.NoError(t, err)
	var actions []string
	for _, e := range events {
		actions = append(actions, e.Action)
	}
	return actions
}

// TestTransferEngine_AllGreen 全绿路径：6 步全 done，终态 succeeded，运行中的
// dev 部署被停止并进 asset_report，归属切到目标机，审计含 prepared/executed。
func TestTransferEngine_AllGreen(t *testing.T) {
	app := newTestAppForPackage(t)

	const projectID = "proj-transfer-green"
	const hostID = "host-green"
	const absDir = "/remote/workspace/transfer-demo"
	dir := initTransferTestRepo(t)
	runTransferGit(t, dir, "remote", "add", "origin", "https://example.com/x.git")

	project := seedTransferProject(t, app, projectID, dir, ".env", "sleep 30")

	// 真起一个 dev 部署，验证 stop_dev 真的把它停了（非重言式）。
	mgr := app.getOrCreateManager(projectID)
	require.NoError(t, mgr.StartDeployment(project.Services[0].Deployments[0]))
	t.Cleanup(func() { mgr.StopDeployment("dep-web-dev") })
	require.Eventually(t, func() bool { return mgr.IsDeploymentActive("dep-web-dev") }, 3e9, 5e7)

	setTransferRemoteRunner(t, func(cmd string) (string, int, error) {
		return transferSuccessRunner(absDir)(context.Background(), cmd, "")
	})
	setTransferNodeDo(t, transferDo(projectID, true))

	run := newTransferRun(projectID, hostID, "Green Host", absDir, "master", "https://example.com/x.git")
	app.executeProjectTransfer(context.Background(), run)
	resp := run.snapshot()

	assert.Equal(t, transferStateSucceeded, resp.State, "全绿路径终态应 succeeded，err=%s", resp.Error)

	states := stepStates(resp)
	for _, code := range []string{transferStepStopDev, transferStepCheckout, transferStepRegister, transferStepMCPSetup, transferStepAssetAudit, transferStepSwitchHome} {
		assert.Equal(t, stepStateDone, states[code], "步骤 %s 应 done", code)
	}
	// 步骤顺序固定
	var order []string
	for _, s := range resp.Steps {
		order = append(order, s.Code)
	}
	assert.Equal(t, []string{transferStepStopDev, transferStepCheckout, transferStepRegister, transferStepMCPSetup, transferStepAssetAudit, transferStepSwitchHome}, order)

	assert.False(t, mgr.IsDeploymentActive("dep-web-dev"), "stop_dev 应真的停掉运行中的 dev 部署")

	var restartCodes []string
	for _, item := range resp.AssetReport {
		restartCodes = append(restartCodes, item.Code)
	}
	assert.Contains(t, restartCodes, "restart_needed", "被停的 dev 服务应进 asset_report，实际=%v", restartCodes)

	assert.Equal(t, hostID, app.projectHomeStore.HomeOf(projectID), "归属应切到目标机")

	actions := auditActions(t, app, projectID)
	assert.Contains(t, actions, operation.AuditPrepared, "审计应含 prepared")
	assert.Contains(t, actions, operation.AuditExecuted, "审计应含 executed")
}

// TestTransferEngine_CheckoutFails checkout 失败：后续步骤 skipped，终态 failed，
// 审计含 failed，归属未切换。
func TestTransferEngine_CheckoutFails(t *testing.T) {
	app := newTestAppForPackage(t)

	const projectID = "proj-transfer-checkout-fail"
	const hostID = "host-cf"
	const absDir = "/remote/workspace/cf"
	dir := initTransferTestRepo(t)

	seedTransferProject(t, app, projectID, dir, ".env", "sleep 30")

	setTransferRemoteRunner(t, func(cmd string) (string, int, error) {
		if strings.Contains(cmd, "test -d") {
			return "no", 0, nil
		}
		if strings.Contains(cmd, "git clone") {
			return "fatal: repository not found", 128, nil // clone 非零退出
		}
		return "", 0, nil
	})
	setTransferNodeDo(t, transferDo(projectID, true))

	run := newTransferRun(projectID, hostID, "CF Host", absDir, "master", "https://example.com/x.git")
	app.executeProjectTransfer(context.Background(), run)
	resp := run.snapshot()

	assert.Equal(t, transferStateFailed, resp.State)
	states := stepStates(resp)
	assert.Equal(t, stepStateFailed, states[transferStepCheckout], "checkout 应 failed")
	for _, code := range []string{transferStepRegister, transferStepMCPSetup, transferStepAssetAudit, transferStepSwitchHome} {
		assert.Equal(t, stepStateSkipped, states[code], "checkout 失败后 %s 应 skipped", code)
	}
	assert.Equal(t, "", app.projectHomeStore.HomeOf(projectID), "失败时归属不应切换")
	assert.Contains(t, auditActions(t, app, projectID), operation.AuditFailed, "审计应含 failed")
}

// TestTransferEngine_RegisterIDMismatch register 返回的 id 与本机 projectID 不符
// → register 失败，终态 failed，归属未切换。
func TestTransferEngine_RegisterIDMismatch(t *testing.T) {
	app := newTestAppForPackage(t)

	const projectID = "proj-transfer-idmismatch"
	const hostID = "host-idm"
	const absDir = "/remote/workspace/idm"
	dir := initTransferTestRepo(t)

	seedTransferProject(t, app, projectID, dir, ".env", "sleep 30")

	setTransferRemoteRunner(t, func(cmd string) (string, int, error) {
		return transferSuccessRunner(absDir)(context.Background(), cmd, "")
	})
	// 目标机返回一个不同的 id（撞机重分配场景）
	setTransferNodeDo(t, transferDo("some-other-id", true))

	run := newTransferRun(projectID, hostID, "IDM Host", absDir, "master", "https://example.com/x.git")
	app.executeProjectTransfer(context.Background(), run)
	resp := run.snapshot()

	assert.Equal(t, transferStateFailed, resp.State)
	states := stepStates(resp)
	assert.Equal(t, stepStateFailed, states[transferStepRegister], "register 应 failed（ID 不一致）")
	assert.Equal(t, "", app.projectHomeStore.HomeOf(projectID), "失败时归属不应切换")
}

// TestTransferEngine_MCPSetupFailsContinues mcp_setup 失败不阻断：终态仍 succeeded，
// asset_report 含 mcp 降级提示，归属仍切换。
func TestTransferEngine_MCPSetupFailsContinues(t *testing.T) {
	app := newTestAppForPackage(t)

	const projectID = "proj-transfer-mcp-fail"
	const hostID = "host-mcpf"
	const absDir = "/remote/workspace/mcpf"
	dir := initTransferTestRepo(t)

	seedTransferProject(t, app, projectID, dir, ".env", "sleep 30")

	setTransferRemoteRunner(t, func(cmd string) (string, int, error) {
		return transferSuccessRunner(absDir)(context.Background(), cmd, "")
	})
	setTransferNodeDo(t, transferDo(projectID, false)) // mcp-setup 报错

	run := newTransferRun(projectID, hostID, "MCPF Host", absDir, "master", "https://example.com/x.git")
	app.executeProjectTransfer(context.Background(), run)
	resp := run.snapshot()

	assert.Equal(t, transferStateSucceeded, resp.State, "mcp_setup 失败不应阻断转移")
	var codes []string
	for _, item := range resp.AssetReport {
		codes = append(codes, item.Code)
	}
	assert.Contains(t, codes, "mcp_setup_failed", "mcp_setup 失败应降级为 asset_report 提示，实际=%v", codes)
	assert.Equal(t, hostID, app.projectHomeStore.HomeOf(projectID), "mcp 软失败后归属仍应切换")
}

// TestTransferEngine_RedactsEmbeddedCredsInCheckoutError 秘密红线：checkout 失败
// 输出里若回显了带内嵌凭据的 remote URL，step Detail 与 Error 都必须脱敏，
// 绝不把 token 落进状态/日志/审计。
func TestTransferEngine_RedactsEmbeddedCredsInCheckoutError(t *testing.T) {
	app := newTestAppForPackage(t)

	const projectID = "proj-transfer-redact"
	const hostID = "host-redact"
	const absDir = "/remote/workspace/redact"
	const secret = "ghp_SUPERSECRETTOKEN0123456789"
	dir := initTransferTestRepo(t)
	seedTransferProject(t, app, projectID, dir, ".env", "sleep 30")

	setTransferRemoteRunner(t, func(cmd string) (string, int, error) {
		if strings.Contains(cmd, "test -d") {
			return "no", 0, nil
		}
		if strings.Contains(cmd, "git clone") {
			// git 把内嵌凭据的 URL 原样回显进失败输出
			return "fatal: unable to access 'https://x-access-token:" + secret + "@github.com/x/y.git/'", 128, nil
		}
		return "", 0, nil
	})
	setTransferNodeDo(t, transferDo(projectID, true))

	run := newTransferRun(projectID, hostID, "Redact Host", absDir, "master", "https://x-access-token:"+secret+"@github.com/x/y.git")
	app.executeProjectTransfer(context.Background(), run)
	resp := run.snapshot()

	assert.Equal(t, transferStateFailed, resp.State)
	assert.NotContains(t, resp.Error, secret, "Error 不得包含明文 token")
	for _, s := range resp.Steps {
		assert.NotContains(t, s.Detail, secret, "步骤 %s 的 Detail 不得包含明文 token", s.Code)
	}
	// 审计里也不得出现明文 token
	events, err := app.operationAudit.List(context.Background(), operation.AuditFilter{Kind: operation.KindProjectTransfer, ProjectID: projectID})
	require.NoError(t, err)
	for _, e := range events {
		assert.NotContains(t, e.Summary, secret, "审计 summary 不得包含明文 token")
	}
}

// TestTransferEngineBack_HomeDirtyBlocks 迁回时归属机存在未提交变更 → probe_home
// 失败，SetHome 不执行，归属保持在原目标机（防止本机 pull 覆盖丢失远端改动）。
func TestTransferEngineBack_HomeDirtyBlocks(t *testing.T) {
	app := newTestAppForPackage(t)

	const projectID = "proj-transfer-back-dirty"
	const homeHost = "host-home"
	const absDir = "/remote/workspace/back"
	dir := initTransferTestRepo(t)

	seedTransferProject(t, app, projectID, dir, ".env", "sleep 30")
	require.NoError(t, app.projectHomeStore.SetHome(projectID, homeHost)) // 先在远端

	setTransferRemoteRunner(t, func(cmd string) (string, int, error) {
		if strings.Contains(cmd, "pwd") {
			return absDir, 0, nil
		}
		if strings.Contains(cmd, "status --porcelain") {
			return " M main.go", 0, nil // 归属机有未提交变更
		}
		return "", 0, nil
	})

	run := newTransferRun(projectID, homeHost, "Home Host", absDir, "master", "https://example.com/x.git")
	app.executeProjectTransferBack(context.Background(), run)
	resp := run.snapshot()

	assert.Equal(t, transferStateFailed, resp.State, "归属机有未提交变更时迁回应失败")
	assert.Equal(t, homeHost, app.projectHomeStore.HomeOf(projectID), "迁回失败时归属应保持在原目标机")
}
