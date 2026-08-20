// project_home_routing_test.go 验证归属路由——项目已设置非本机归属后，
// deployment 运行控制、startEnvSelected、项目配置读写端点被原样转发到
// 归属 agent 的同路径端点，本机不产生任何本地副作用；prod 环境部署与未
// 设置归属的项目保持本机路径不变（回归）。
//
// 白盒测试（package api）：用 recordingNodeTransport 直接替换
// app.nodeTransport，断言收到的 NodeRequest 的 method/path/host/body，
// 不依赖真实网络往返。
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

// homeRouteTestHost 是测试场景里统一使用的（非本机）归属主机 ID。
const homeRouteTestHost = "host-home"

// recordedNodeCall 是 recordingNodeTransport.Do 收到的一次调用的快照。
type recordedNodeCall struct {
	hostID string
	method string
	path   string
	body   []byte
	header http.Header
}

// recordingNodeTransport 记录每次 Do 调用收到的 NodeRequest（含请求体原文），
// 不发起任何真实网络请求，用于断言 forwardToHome 是否被调用、调用参数是否
// 原样保留。respStatus/respBody 为零值时默认回 200 空 JSON 对象；err 非 nil
// 时模拟归属 host 不可达。
type recordingNodeTransport struct {
	mu         sync.Mutex
	calls      []recordedNodeCall
	respStatus int
	respBody   []byte
	err        error
}

func (t *recordingNodeTransport) Do(_ context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
	}
	t.calls = append(t.calls, recordedNodeCall{hostID: hostID, method: req.Method, path: req.Path, body: body, header: req.Headers})
	if t.err != nil {
		return nodetransport.NodeResponse{}, t.err
	}
	status := t.respStatus
	if status == 0 {
		status = http.StatusOK
	}
	respBody := t.respBody
	if respBody == nil {
		respBody = []byte(`{}`)
	}
	return nodetransport.NodeResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}, nil
}

func (t *recordingNodeTransport) Stream(context.Context, string, nodetransport.NodeRequest) (nodetransport.NodeStream, error) {
	return nil, nodetransport.ErrHostUnreachable
}

func (t *recordingNodeTransport) SubscribeNodes(context.Context) (<-chan []nodetransport.NodeStatus, func()) {
	ch := make(chan []nodetransport.NodeStatus)
	close(ch)
	return ch, func() {}
}

func (t *recordingNodeTransport) Covers() []string { return nil }

func (t *recordingNodeTransport) lastCall() recordedNodeCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.calls) == 0 {
		return recordedNodeCall{}
	}
	return t.calls[len(t.calls)-1]
}

func (t *recordingNodeTransport) callCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.calls)
}

// TestForwardToHomeAllowsOnlyTokensIssuedByThatHost 是**闸门一**，不能省。
//
// 为什么判据必须钉在「谁签的」而不是「要发给谁」：有一种看着更省事的写法是
// 「转发目标是归属机就放行 token」——正常路径上它恰好成立，但一个本机签发的
// token 若被误带到归属路由请求上就会被原样发出去，那是实打实的凭据外流，
// 而且从判据本身完全看不出问题。
func TestForwardToHomeAllowsOnlyTokensIssuedByThatHost(t *testing.T) {
	forwardedToken := func(t *testing.T, registerHost, targetHost string) string {
		t.Helper()
		tr := &fakeApprovalTransport{respCode: http.StatusOK, respBody: `{}`}
		app, err := NewApp(AppConfig{DataDir: t.TempDir(), NodeTransportOverride: tr})
		require.NoError(t, err)
		t.Cleanup(app.Close)

		if registerHost != "" {
			app.approvalTokenOrigin.Remember("tok_x", registerHost, time.Now().Add(time.Minute))
		}
		req := httptest.NewRequest(http.MethodPost, "/api/deployments/d1/start", strings.NewReader(`{}`))
		req.Header.Set("X-SuperDev-Approval-Token", "tok_x")
		app.forwardToHome(httptest.NewRecorder(), req, targetHost)
		return tr.header("X-SuperDev-Approval-Token")
	}

	t.Run("归属机签发的 token → 放行", func(t *testing.T) {
		require.Equal(t, "tok_x", forwardedToken(t, "h1", "h1"))
	})
	t.Run("本机签发的 token（未登记）→ 不放行", func(t *testing.T) {
		require.Empty(t, forwardedToken(t, "", "h1"),
			"本机签发的 token 绝不能因为转发目标恰好是归属机而被发出去")
	})
	t.Run("由 h2 签发的 token 转发去 h1 → 不放行", func(t *testing.T) {
		require.Empty(t, forwardedToken(t, "h2", "h1"))
	})
}

// homeRouteTestProject 构造一个同时带 dev/prod 两个环境、各一个本地部署的
// 项目，供归属路由用例复用。RootPath 固定为 t.TempDir()——部分用例（如
// prod env 不转发）会真正落到本机既有的写路径（如 putEnvSelected 的
// config.Loader.Save），RootPath 留空会让那些写操作把 .superdev/project.yaml
// 落到进程当前工作目录，污染仓库；这里必须给一个真实可写的临时目录。
func homeRouteTestProject(t *testing.T) model.Project {
	t.Helper()
	const projectID = "proj-home-route"
	return model.Project{
		ID:       projectID,
		Name:     "home-route-demo",
		RootPath: t.TempDir(),
		Environments: []model.Environment{
			{ID: "env-dev", Name: "dev", IsDev: true, Order: 0},
			{ID: "env-prod", Name: "prod", IsDev: false, Order: 1},
		},
		Services: []model.Service{{
			ID:        "svc-web",
			ProjectID: projectID,
			Name:      "web",
			Deployments: []model.Deployment{
				{ID: "dep-web-dev", EnvName: "dev", Location: model.LocationLocal, Command: "sleep 30"},
				{ID: "dep-web-prod", EnvName: "prod", Location: model.LocationLocal, Command: "sleep 30"},
			},
		}},
	}
}

// setupHomeRoutedApp 注册 homeRouteTestProject 并把它的归属设为
// homeRouteTestHost（非本机），装配 recordingNodeTransport 替代真实
// nodeTransport。
func setupHomeRoutedApp(t *testing.T) (*App, string, *recordingNodeTransport) {
	t.Helper()
	app := newTestAppForPackage(t)
	transport := &recordingNodeTransport{}
	app.nodeTransport = transport
	project := homeRouteTestProject(t)
	app.mu.Lock()
	app.appendProjectLocked(project)
	app.mu.Unlock()
	require.NoError(t, app.projectHomeStore.SetHome(project.ID, homeRouteTestHost, ""))
	srv := newHTTPServerForPackage(t, app)
	return app, srv.URL, transport
}

// ---- (a) deployment 运行控制：dev env 且已设归属 → 转发，本机零副作用 ----

func TestControlDeploymentRuntime_ForwardsDevDeploymentToHome(t *testing.T) {
	app, baseURL, transport := setupHomeRoutedApp(t)

	postJSONForRawTest(t, baseURL+"/api/deployments/dep-web-dev/start", map[string]any{}, http.StatusOK)

	require.Equal(t, 1, transport.callCount(), "dev deployment start 必须被转发恰好一次")
	call := transport.lastCall()
	assert.Equal(t, homeRouteTestHost, call.hostID)
	assert.Equal(t, http.MethodPost, call.method)
	assert.Equal(t, "/api/deployments/dep-web-dev/start", call.path, "转发路径必须与原始请求路径原样一致")

	// 本机 process.Manager 必须零调用：controlDeploymentRuntime 在
	// getOrCreateManager 之前就已经短路转发，proj-home-route 不应出现在
	// a.managers 里。
	app.mu.RLock()
	_, hasMgr := app.managers["proj-home-route"]
	app.mu.RUnlock()
	assert.False(t, hasMgr, "转发必须在本机 process.Manager 创建之前短路，不能有任何本地进程管理副作用")
}

func TestControlDeploymentRuntime_ForwardsDevDeploymentStopAndRestart(t *testing.T) {
	_, baseURL, transport := setupHomeRoutedApp(t)

	postJSONForRawTest(t, baseURL+"/api/deployments/dep-web-dev/stop", map[string]any{}, http.StatusOK)
	assert.Equal(t, http.MethodPost, transport.lastCall().method)
	assert.Equal(t, "/api/deployments/dep-web-dev/stop", transport.lastCall().path)

	postJSONForRawTest(t, baseURL+"/api/deployments/dep-web-dev/restart", map[string]any{}, http.StatusOK)
	assert.Equal(t, "/api/deployments/dep-web-dev/restart", transport.lastCall().path)

	require.Equal(t, 2, transport.callCount())
}

// ---- (b) prod env 部署不转发，走本机原有路径 ----

func TestControlDeploymentRuntime_ProdDeploymentNotForwarded(t *testing.T) {
	_, baseURL, transport := setupHomeRoutedApp(t)

	// prod（非 dev）本地部署走本机既有安全策略：需要审批，403。关键断言是
	// transport 零调用——prod 部署的 host 钉死，不随项目归属移动。
	resp := postJSONForRawTest(t, baseURL+"/api/deployments/dep-web-prod/start", map[string]any{}, http.StatusForbidden)
	assert.Equal(t, "approval_required", resp["code"])
	assert.Equal(t, 0, transport.callCount(), "prod 环境部署不应被转发到归属节点")
}

// ---- (c) 归属 host 不可达 → 502 home_unreachable ----

func TestControlDeploymentRuntime_HomeUnreachableReturns502(t *testing.T) {
	_, baseURL, transport := setupHomeRoutedApp(t)
	transport.err = nodetransport.ErrHostUnreachable

	resp := postJSONForRawTest(t, baseURL+"/api/deployments/dep-web-dev/start", map[string]any{}, http.StatusBadGateway)
	assert.Equal(t, "home_unreachable", resp["code"])
}

// ---- (d) 未设归属 → 本机路径不变（回归） ----

func TestControlDeploymentRuntime_NoHomeRunsLocally(t *testing.T) {
	app := newTestAppForPackage(t)
	transport := &recordingNodeTransport{}
	app.nodeTransport = transport
	project := homeRouteTestProject(t)
	app.mu.Lock()
	app.appendProjectLocked(project)
	app.mu.Unlock()
	// 刻意不调用 SetHome：projectHomeStore.HomeOf 应返回空串，回归本机路径。
	srv := newHTTPServerForPackage(t, app)

	resp := postJSONForRawTest(t, srv.URL+"/api/deployments/dep-web-dev/start", map[string]any{}, http.StatusOK)
	assert.Equal(t, "starting", resp["status"])
	assert.Equal(t, 0, transport.callCount(), "未设归属时不应发生任何转发")
}

// ---- startEnvSelected 接入点 ----

func TestStartEnvSelected_ForwardsDevEnvToHome(t *testing.T) {
	_, baseURL, transport := setupHomeRoutedApp(t)

	postJSONForRawTest(t, baseURL+"/api/projects/proj-home-route/envs/dev/start-selected", map[string]any{}, http.StatusOK)

	require.Equal(t, 1, transport.callCount())
	call := transport.lastCall()
	assert.Equal(t, homeRouteTestHost, call.hostID)
	assert.Equal(t, http.MethodPost, call.method)
	assert.Equal(t, "/api/projects/proj-home-route/envs/dev/start-selected", call.path)
}

func TestStartEnvSelected_ProdEnvNotForwarded(t *testing.T) {
	_, baseURL, transport := setupHomeRoutedApp(t)

	postJSONForRawTest(t, baseURL+"/api/projects/proj-home-route/envs/prod/start-selected", map[string]any{}, http.StatusOK)

	assert.Equal(t, 0, transport.callCount(), "prod env 的 start-selected 不应被转发")
}

// ---- putEnvSelected（与 startEnvSelected 成对接入） ----

func TestPutEnvSelected_ForwardsDevEnvToHome(t *testing.T) {
	_, baseURL, transport := setupHomeRoutedApp(t)

	req, err := http.NewRequest(http.MethodPut, baseURL+"/api/projects/proj-home-route/env-selected",
		bytes.NewReader([]byte(`{"env_name":"dev","names":["web"]}`)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.Equal(t, 1, transport.callCount())
	call := transport.lastCall()
	assert.Equal(t, homeRouteTestHost, call.hostID)
	assert.Equal(t, http.MethodPut, call.method)
	assert.Equal(t, "/api/projects/proj-home-route/env-selected", call.path)

	var echoed map[string]any
	require.NoError(t, json.Unmarshal(call.body, &echoed))
	assert.Equal(t, "dev", echoed["env_name"])
}

func TestPutEnvSelected_ProdEnvNotForwarded(t *testing.T) {
	_, baseURL, transport := setupHomeRoutedApp(t)

	req, err := http.NewRequest(http.MethodPut, baseURL+"/api/projects/proj-home-route/env-selected",
		bytes.NewReader([]byte(`{"env_name":"prod","names":["web"]}`)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 0, transport.callCount(), "prod env 的 env-selected 写入不应被转发")
}

// ---- 项目配置读写端点（get/preview/apply） ----

func TestGetProjectConfig_ForwardsToHome(t *testing.T) {
	_, baseURL, transport := setupHomeRoutedApp(t)

	resp, err := http.Get(baseURL + "/api/projects/proj-home-route/config")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	require.Equal(t, 1, transport.callCount())
	call := transport.lastCall()
	assert.Equal(t, homeRouteTestHost, call.hostID)
	assert.Equal(t, http.MethodGet, call.method)
	assert.Equal(t, "/api/projects/proj-home-route/config", call.path)
}

func TestGetProjectConfig_NoHomeRunsLocally(t *testing.T) {
	app := newTestAppForPackage(t)
	transport := &recordingNodeTransport{}
	app.nodeTransport = transport
	project := homeRouteTestProject(t)
	app.mu.Lock()
	app.appendProjectLocked(project)
	app.mu.Unlock()
	srv := newHTTPServerForPackage(t, app)

	resp, err := http.Get(srv.URL + "/api/projects/proj-home-route/config")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 0, transport.callCount())

	var got model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, "proj-home-route", got.ID)
}

func TestPreviewConfigChange_ForwardsToHome(t *testing.T) {
	_, baseURL, transport := setupHomeRoutedApp(t)

	body := map[string]any{
		"kind":       "config.project.upsert",
		"project_id": "proj-home-route",
	}
	postJSONForRawTest(t, baseURL+"/api/config-changes/preview", body, http.StatusOK)

	require.Equal(t, 1, transport.callCount())
	call := transport.lastCall()
	assert.Equal(t, http.MethodPost, call.method)
	assert.Equal(t, "/api/config-changes/preview", call.path)
}

func TestApplyConfigChange_ForwardsToHomeBeforeAuthorize(t *testing.T) {
	_, baseURL, transport := setupHomeRoutedApp(t)

	body := map[string]any{
		"kind":       "config.project.upsert",
		"project_id": "proj-home-route",
		"project":    map[string]any{"name": "renamed"},
	}
	postJSONForRawTest(t, baseURL+"/api/config-changes/apply", body, http.StatusOK)

	require.Equal(t, 1, transport.callCount())
	call := transport.lastCall()
	assert.Equal(t, http.MethodPost, call.method)
	assert.Equal(t, "/api/config-changes/apply", call.path)

	// 请求体必须原样透传（转发发生在本机 authorizeOperation/buildConfigChangePreview
	// 之前，归属机拿到的必须是调用方原始意图，不是本机改写过的版本）。
	var echoed map[string]any
	require.NoError(t, json.Unmarshal(call.body, &echoed))
	assert.Equal(t, "proj-home-route", echoed["project_id"])
	assert.Equal(t, "config.project.upsert", echoed["kind"])
}

func TestApplyConfigChange_UnknownProjectNotForwarded(t *testing.T) {
	// 全新项目（config-changes 创建流程）在 projectHomeStore 里没有任何归属
	// 记录，必须留在本机处理，不能被误判转发。走本机既有安全策略（配置写入
	// 默认需要审批）得到 403，与本任务无关——这里只关心 transport 零调用。
	app := newTestAppForPackage(t)
	transport := &recordingNodeTransport{}
	app.nodeTransport = transport
	srv := newHTTPServerForPackage(t, app)

	body := map[string]any{
		"kind":      "config.project.upsert",
		"root_path": t.TempDir(),
		"project":   map[string]any{"name": "brand-new"},
	}
	resp := postJSONForRawTest(t, srv.URL+"/api/config-changes/apply", body, http.StatusForbidden)
	assert.Equal(t, "approval_required", resp["code"])

	assert.Equal(t, 0, transport.callCount(), "全新项目不应被转发")
}

// jsonBodyForTest 把 body 序列化为 JSON reader，供手工构造 PUT 请求使用。
func jsonBodyForTest(t *testing.T, body any) *bytes.Reader {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	return bytes.NewReader(data)
}

// ---- 桌面主配置编辑端点（setup / rules） ----

// putProjectSetup 是桌面配置编辑器（ProjectConfigSurface/ProjectPipelineEditor）
// 的保存主路径：归属他机时必须转发，否则保存静默落在本机过期镜像上（零报错、
// 弄脏本机工作树、卡住迁回 pull）。
func TestPutProjectSetup_ForwardsToHome(t *testing.T) {
	_, baseURL, transport := setupHomeRoutedApp(t)

	body := map[string]any{"environments": []any{}, "services": []any{}, "pipelines": []any{}}
	req, err := http.NewRequest(http.MethodPut, baseURL+"/api/projects/proj-home-route/setup", jsonBodyForTest(t, body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	require.Equal(t, 1, transport.callCount())
	call := transport.lastCall()
	assert.Equal(t, homeRouteTestHost, call.hostID)
	assert.Equal(t, http.MethodPut, call.method)
	assert.Equal(t, "/api/projects/proj-home-route/setup", call.path)
	assert.Contains(t, string(call.body), "environments", "请求体必须原文转发")
}

// rules（日志过滤规则）属共享层 project.yaml：读写成对转发，否则会出现
// 「写进归属机、读回本机旧值」的假回滚。
func TestProjectRules_ForwardsToHome(t *testing.T) {
	_, baseURL, transport := setupHomeRoutedApp(t)

	req, err := http.NewRequest(http.MethodPut, baseURL+"/api/projects/proj-home-route/rules", jsonBodyForTest(t, []any{}))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	getResp, err := http.Get(baseURL + "/api/projects/proj-home-route/rules")
	require.NoError(t, err)
	getResp.Body.Close()
	assert.Equal(t, http.StatusOK, getResp.StatusCode)

	require.Equal(t, 2, transport.callCount())
	assert.Equal(t, "/api/projects/proj-home-route/rules", transport.lastCall().path)
}

// ---- 转发头白名单 ----

func TestForwardToHome_PropagatesWhitelistedHeaders(t *testing.T) {
	_, baseURL, transport := setupHomeRoutedApp(t)

	postJSONWithHeadersForTest[map[string]any](t, baseURL+"/api/deployments/dep-web-dev/start", map[string]any{}, map[string]string{
		"X-SuperDev-Requester":       "alice",
		"X-SuperDev-Requester-Label": "Alice Desktop",
	}, http.StatusOK)

	call := transport.lastCall()
	assert.Equal(t, "alice", call.header.Get("X-SuperDev-Requester"))
	assert.Equal(t, "Alice Desktop", call.header.Get("X-SuperDev-Requester-Label"))
}

// 红线回归：Authorization / X-SuperDev-Approval-Token 绝不能进入转发头——
// 调用方带的是本机 agent 的凭据，透传会（经 applyAgentHeaders 的 override
// 语义）覆盖 nodetransport 注入的归属机正确 token，导致转发必 401，且等于
// 在机器间搬运本机凭据。审批 token 同理是签发 agent 的本机语义，跨机无效。
func TestForwardToHome_NeverForwardsCredentialHeaders(t *testing.T) {
	app, baseURL, transport := setupHomeRoutedApp(t)

	// 显式带上「本机的真实 local-access-token」——它能通过本机鉴权中间件，
	// 但绝不能出现在转发给归属机的请求头里。
	postJSONWithHeadersForTest[map[string]any](t, baseURL+"/api/deployments/dep-web-dev/start", map[string]any{}, map[string]string{
		"Authorization":             "Bearer " + app.LocalAccessToken(),
		"X-SuperDev-Approval-Token": "tok-123",
	}, http.StatusOK)

	call := transport.lastCall()
	assert.Empty(t, call.header.Get("Authorization"), "本机凭据绝不能被转发到归属机")
	assert.Empty(t, call.header.Get("X-SuperDev-Approval-Token"), "审批 token 是本机语义，不跨机透传")
}
