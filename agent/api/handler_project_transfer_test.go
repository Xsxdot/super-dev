// handler_project_transfer_test.go 验证转移预检端点 POST
// /api/projects/{id}/transfer/preflight。
//
// 白盒测试（package api）：直接注入 transferRemoteRunner 假件，绕开真实
// SSH/agent 网络往返；本机侧用 t.TempDir() + 真实 git 命令构造仓库场景，
// 与 gitinfo/local_test.go 的做法保持一致（不 mock git 命令本身）。
package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

// initTransferTestRepo 在临时目录初始化一个真实 git 仓库并提交一次初始文件，
// 返回仓库根目录绝对路径。做法与 gitinfo/local_test.go 的 initTestRepo 一致，
// 复制而非跨包导出是因为该 helper 是 _test.go，两个包无法共享。
func initTransferTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runTransferGit(t, dir, "init")
	runTransferGit(t, dir, "config", "user.email", "test@example.com")
	runTransferGit(t, dir, "config", "user.name", "Test User")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0o644))
	runTransferGit(t, dir, "add", "README.md")
	runTransferGit(t, dir, "commit", "-m", "init")
	return dir
}

func runTransferGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v 执行失败: %v\n输出: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// setTransferRemoteRunner 注入测试假件并注册 t.Cleanup 复位，避免跨测试污染
// 这个包级变量（transferRemoteRunner 是唯一的远端探测测试 seam）。
func setTransferRemoteRunner(t *testing.T, fn func(cmd string) (string, int, error)) {
	t.Helper()
	transferRemoteRunner = func(_ context.Context, cmd, _ string) (string, int, error) {
		return fn(cmd)
	}
	t.Cleanup(func() { transferRemoteRunner = nil })
}

// dirAbsentRunner 是最常用的假件：任何 "test -d" 探测都回答目录不存在，
// 其余命令不应被调用到（InspectRemote 在目录不存在时短路返回）。
func dirAbsentRunner(cmd string) (string, int, error) {
	if strings.Contains(cmd, "test -d") {
		return "no", 0, nil
	}
	return "", 1, nil
}

// existingRepoRunner 模拟目标机上已存在一个 git 仓库，origin 固定为
// remoteURL；按 cmd 前缀分发 canned 输出，覆盖 gitinfo.InspectRemote 会
// 执行的全部子命令。用于构造 remote_url_mismatch 场景（目录存在 + 仓库 +
// 与本机不同源的 origin）。
func existingRepoRunner(remoteURL string) func(cmd string) (string, int, error) {
	return func(cmd string) (string, int, error) {
		switch {
		case strings.Contains(cmd, "test -d"):
			return "yes", 0, nil
		case strings.Contains(cmd, "rev-parse --is-inside-work-tree"):
			return "true", 0, nil
		case strings.Contains(cmd, "symbolic-ref --short HEAD"):
			return "main", 0, nil
		case strings.Contains(cmd, "remote get-url origin"):
			return remoteURL, 0, nil
		case strings.Contains(cmd, "status --porcelain"):
			return "", 0, nil
		case strings.Contains(cmd, "rev-list --count"):
			return "", 1, nil // 无上游配置，Ahead 降级为 -1，与本用例无关
		default:
			return "", 1, nil
		}
	}
}

// addTransferTestProject 通过 POST /api/projects 注册 rootPath 对应的项目，
// 返回创建后的 model.Project（含分配的 ID）。
func addTransferTestProject(t *testing.T, srv *httptest.Server, rootPath string) model.Project {
	t.Helper()
	body := `{"root_path": "` + rootPath + `"}`
	resp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var p model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&p))
	require.NotEmpty(t, p.ID)
	return p
}

// TestTransferPreflight_NonDevMachineHost_Returns400 验证目标 host 未开启
// DevMachineMode 时预检直接 400，不进入任何探测逻辑。
func TestTransferPreflight_NonDevMachineHost_Returns400(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)

	dir := initTransferTestRepo(t)
	project := addTransferTestProject(t, srv, dir)

	_, err := app.remoteStore.AddHost(model.Host{ID: "host-not-dev", Name: "Not Dev", DevMachineMode: false})
	require.NoError(t, err)

	reqBody := `{"host_id": "host-not-dev"}`
	resp, err := http.Post(srv.URL+"/api/projects/"+project.ID+"/transfer/preflight", "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "非开发机 host 应返回 400")
}

// TestTransferPreflight_DirtyRepo_BlockersContainUncommitted 验证本机存在
// 未提交变更时，预检响应的 blockers 包含 uncommitted。
func TestTransferPreflight_DirtyRepo_BlockersContainUncommitted(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)
	setTransferRemoteRunner(t, dirAbsentRunner)

	dir := initTransferTestRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("wip\n"), 0o644))
	project := addTransferTestProject(t, srv, dir)

	_, err := app.remoteStore.AddHost(model.Host{ID: "host-dev", Name: "Dev Machine", DevMachineMode: true})
	require.NoError(t, err)

	reqBody := `{"host_id": "host-dev"}`
	resp, err := http.Post(srv.URL+"/api/projects/"+project.ID+"/transfer/preflight", "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result transferPreflightResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))

	var codes []string
	for _, b := range result.Blockers {
		codes = append(codes, b.Code)
	}
	assert.Contains(t, codes, "uncommitted", "脏仓库应触发 uncommitted blocker，实际 blockers=%v", codes)
}

// TestTransferPreflight_CleanRepo_ReadyContainsCheckoutClone 验证干净仓库
// （且配置了 origin，local.RemoteURL 非空——checkout_clone 现在要求这一点，
// 见 FINDING #1 修复）+ 假 Runner 返回目标目录不存在时，ready 包含
// checkout_clone 且 TargetDir 使用正确的默认值 "~/workspace/<项目目录名>"。
func TestTransferPreflight_CleanRepo_ReadyContainsCheckoutClone(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)
	setTransferRemoteRunner(t, dirAbsentRunner)

	dir := initTransferTestRepo(t)
	runTransferGit(t, dir, "remote", "add", "origin", "https://example.com/clean-repo.git")
	project := addTransferTestProject(t, srv, dir)

	_, err := app.remoteStore.AddHost(model.Host{ID: "host-dev-2", Name: "Dev Machine 2", DevMachineMode: true})
	require.NoError(t, err)

	reqBody := `{"host_id": "host-dev-2"}`
	resp, err := http.Post(srv.URL+"/api/projects/"+project.ID+"/transfer/preflight", "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result transferPreflightResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))

	var codes []string
	for _, r := range result.Ready {
		codes = append(codes, r.Code)
	}
	assert.Contains(t, codes, "checkout_clone", "目标目录不存在应 ready=checkout_clone，实际 ready=%v", codes)

	wantTargetDir := "~/workspace/" + filepath.Base(dir)
	assert.Equal(t, wantTargetDir, result.TargetDir, "target_dir 留空时应取默认值")
}

// TestTransferPreflight_NoLocalOrigin_ReadyExcludesCheckoutClone 覆盖审阅
// FINDING #1：本机仓库没有配置 origin（local.RemoteURL==""）时，即便目标机
// 目录不存在，也不应该报 ready=checkout_clone——本机根本没有可供 clone 的
// 源地址，报 ready 会和已经存在的 no_upstream blocker 自相矛盾（一边说
// "不能转"，一边说"目标已就绪可以直接 clone"）。
func TestTransferPreflight_NoLocalOrigin_ReadyExcludesCheckoutClone(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)
	setTransferRemoteRunner(t, dirAbsentRunner)

	// initTransferTestRepo 只 init+commit，不添加 origin，local.RemoteURL 因此为空；
	// 新仓库也没有配置 upstream 分支，Ahead==-1，天然带出 no_upstream blocker。
	dir := initTransferTestRepo(t)
	project := addTransferTestProject(t, srv, dir)

	_, err := app.remoteStore.AddHost(model.Host{ID: "host-dev-no-origin", Name: "Dev Machine", DevMachineMode: true})
	require.NoError(t, err)

	reqBody := `{"host_id": "host-dev-no-origin"}`
	resp, err := http.Post(srv.URL+"/api/projects/"+project.ID+"/transfer/preflight", "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result transferPreflightResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))

	var readyCodes, blockerCodes []string
	for _, r := range result.Ready {
		readyCodes = append(readyCodes, r.Code)
	}
	for _, b := range result.Blockers {
		blockerCodes = append(blockerCodes, b.Code)
	}
	assert.NotContains(t, readyCodes, "checkout_clone", "本机无 origin 时不应报 ready=checkout_clone，实际 ready=%v", readyCodes)
	assert.Contains(t, blockerCodes, "no_upstream", "根因 blocker 应仍然存在，实际 blockers=%v", blockerCodes)
}

// TestTransferPreflight_AllClear_WireFormatUsesEmptyArraysNotNull 覆盖审阅
// Critical 修复：预检"全绿"（无 blocker、无 ready 项）是最常见的 happy path，
// 此时 blockers/ready 必须在 HTTP 响应体里编码成 `[]` 而不是 `null`。
//
// 为什么本用例要断言原始字节而不是结构体解码结果：json.Unmarshal 把 `null`
// 和 `[]` 都无害地解码成 Go 的 nil 切片，对 nil 切片 range/len 都不会 panic，
// 结构体往返测试会对这个 bug 完全失明——正是既有测试套件没能拦住这次线上
// 崩溃的根因。前端 TS 把 blockers/ready 声明为非空数组类型，拿到 wire 上的
// `null` 后 `.length`/`.map(...)` 会直接 TypeError。因此本用例必须直接读
// resp.Body 的原始字节，对 JSON 字符串做 Contains 断言，才能真正覆盖这个
// bug（修复前会拿到 `"blockers":null`，assert.Contains 会失败）。
//
// 构造"全绿"场景的关键：
//   - 本机仓库干净（不脏）
//   - Ahead 必须是 0（不能是 -1 走 no_upstream、不能 >0 走 unpushed）——用
//     `git branch --set-upstream-to` 把当前分支追踪同一提交的另一个本地
//     分支，全程不配置 origin，即可拿到 Ahead=0 而无需真实远端
//   - local.RemoteURL 留空 + 目标机目录不存在（dirAbsentRunner）：命中预检
//     switch 里 "!DirExists 但 local.RemoteURL=="" " 分支，其 if 条件不成立，
//     不会往 ready 追加 checkout_clone——这是唯一能让 blockers 和 ready
//     同时保持字面为空的组合，其余分支都必然至少往其中一个追加一项
//   - 无 dev 环境/无运行中部署，跳过 running_dev blocker
func TestTransferPreflight_AllClear_WireFormatUsesEmptyArraysNotNull(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)
	setTransferRemoteRunner(t, dirAbsentRunner)

	dir := initTransferTestRepo(t)
	project := addTransferTestProject(t, srv, dir)
	// 注册项目会在仓库根目录落一份 .superdev/project.yaml（未纳入版本控制），
	// 若不提交，status --porcelain 会因这个未跟踪文件判定仓库"脏"，
	// 触发 uncommitted blocker，污染本用例要构造的"全绿"场景——
	// 提交它才能让"全绿"真正只由 git 事实决定，而不是被测试基础设施的
	// 副作用意外带出一个不相关的 blocker。
	runTransferGit(t, dir, "add", "-A")
	runTransferGit(t, dir, "commit", "-m", "add superdev config")
	// 在提交完所有内容（含上面的配置文件提交）之后，再用同一提交上的另一个
	// 本地分支充当追踪目标，使 Ahead=0；全程不配置 origin。顺序很重要：
	// 若提前建 upstream-shadow，随后再提交配置文件会让 HEAD 领先它一个
	// 提交，误触发 unpushed blocker。
	runTransferGit(t, dir, "branch", "upstream-shadow")
	runTransferGit(t, dir, "branch", "--set-upstream-to=upstream-shadow")

	_, err := app.remoteStore.AddHost(model.Host{ID: "host-dev-all-clear", Name: "Dev Machine", DevMachineMode: true})
	require.NoError(t, err)

	reqBody := `{"host_id": "host-dev-all-clear"}`
	resp, err := http.Post(srv.URL+"/api/projects/"+project.ID+"/transfer/preflight", "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result transferPreflightResponse
	bodyBytes := readAndDecode(t, resp, &result)

	// 先用结构体断言确认场景本身真的是"全绿"（不是误配出了别的 blocker/ready）。
	require.Empty(t, result.Blockers, "本用例要覆盖的就是全绿场景，实际 blockers=%+v", result.Blockers)
	require.Empty(t, result.Ready, "本用例要覆盖的就是全绿场景，实际 ready=%+v", result.Ready)

	// 核心断言：原始 JSON 必须是 `[]`，不能是 `null`。
	body := string(bodyBytes)
	assert.Contains(t, body, `"blockers":[]`, "全绿场景下 blockers 必须编码成 []，不能是 null，实际响应体=%s", body)
	assert.Contains(t, body, `"ready":[]`, "全绿场景下 ready 必须编码成 []，不能是 null，实际响应体=%s", body)
	assert.NotContains(t, body, `"blockers":null`, "回归：blockers 不应再编码成 null，实际响应体=%s", body)
	assert.NotContains(t, body, `"ready":null`, "回归：ready 不应再编码成 null，实际响应体=%s", body)
}

// TestTransferPreflight_StartingDevDeployment_FlaggedAsRunningDev 覆盖审阅
// FINDING #2（人工决策：running_dev 判定改用 IsDeploymentActive 而非字面
// StatusRunning，starting 态也算活跃，宁可多报不漏报）。
//
// 复现手法与 process/manager_test.go 的 TestStartDeploymentStaysStartingUntilReady
// 完全一致：就绪探测目标先返回 503 卡住 readiness，deployment 停在 starting
// 一段可观察的窗口期，在这个窗口期内调用预检，断言 running_dev 命中——
// 如果没有这次修复（还在用字面 StatusRunning 比较），这里必然断言失败，
// 是一个会真正失败的负向验证，不是重言式空测试。
func TestTransferPreflight_StartingDevDeployment_FlaggedAsRunningDev(t *testing.T) {
	app := newTestAppForPackage(t)
	httpSrv := newHTTPServerForPackage(t, app)
	setTransferRemoteRunner(t, dirAbsentRunner)

	dir := initTransferTestRepo(t)

	// readiness 探测目标：在测试主动放行前恒定返回 503，让 deployment 卡在
	// starting，不进 running，也不因超时转 failed（TimeoutSeconds 留足余量）。
	ready := make(chan struct{})
	readinessSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-ready:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(readinessSrv.Close)

	const projectID = "proj-transfer-starting-dev"
	const depID = "dep-transfer-starting"
	project := model.Project{
		ID:       projectID,
		Name:     "starting-dev-demo",
		RootPath: dir,
		Environments: []model.Environment{
			{ID: "env-dev", Name: "dev", IsDev: true, Order: 0},
		},
		Services: []model.Service{{
			ID:        "svc-web",
			ProjectID: projectID,
			Name:      "web",
			Deployments: []model.Deployment{{
				ID:          depID,
				EnvName:     "dev",
				Location:    model.LocationLocal,
				ControlMode: model.ControlModeManaged,
				Command:     "sleep 5",
				WorkDir:     t.TempDir(),
				Readiness:   &model.ReadinessProbe{Type: "http", Target: readinessSrv.URL, TimeoutSeconds: 10},
			}},
		}},
	}
	app.mu.Lock()
	app.appendProjectLocked(project)
	app.mu.Unlock()

	mgr := app.getOrCreateManager(projectID)
	require.NoError(t, mgr.StartDeployment(project.Services[0].Deployments[0]))
	t.Cleanup(func() {
		close(ready) // 避免遗留 goroutine 永远卡在 503 轮询上
		mgr.StopDeployment(depID)
	})

	// 与 manager_test.go 同款等待窗口：给 readiness 探测一次失败轮询的时间，
	// 确认此刻确实还在 starting（而不是运气好已经翻成了别的状态）。
	time.Sleep(700 * time.Millisecond)
	require.Equal(t, model.StatusStarting, mgr.DeploymentStatus(depID), "本用例要覆盖的就是 starting 这个中间态，必须先确认真的停在这里")

	_, err := app.remoteStore.AddHost(model.Host{ID: "host-dev-starting", Name: "Dev Machine", DevMachineMode: true})
	require.NoError(t, err)

	reqBody := `{"host_id": "host-dev-starting"}`
	resp, err := http.Post(httpSrv.URL+"/api/projects/"+projectID+"/transfer/preflight", "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result transferPreflightResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))

	var codes []string
	for _, b := range result.Blockers {
		codes = append(codes, b.Code)
	}
	assert.Contains(t, codes, "running_dev", "starting 态的 dev deployment 也应触发 running_dev，实际 blockers=%v", codes)
}

// TestTransferPreflight_RemoteURLMismatchDetail_RedactsCredentials 覆盖秘密
// 红线泄露修复：本机与目标机 origin 不一致时，remote_url_mismatch 的 Detail
// 会把两个 RemoteURL 原文拼进 HTTP 响应体——如果 URL 里带凭据（无论是
// user:token@ 形式还是裸 token@ 形式），修复前会原样泄露到调用方能看见的
// 响应里。本用例让本机、目标机分别携带两种不同形式的凭据，断言 Detail
// 里两个凭据子串都不出现，但主机名等非敏感部分仍然保留（证明是"摘除
// userinfo"而不是把整条 Detail 打成不可读的占位符）。
func TestTransferPreflight_RemoteURLMismatchDetail_RedactsCredentials(t *testing.T) {
	const localSecret = "s3cr3t-local-pw"
	const remoteSecret = "ghp_remoteBareToken123"

	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)
	setTransferRemoteRunner(t, existingRepoRunner("https://"+remoteSecret+"@remote-origin.example.com/other-repo.git"))

	dir := initTransferTestRepo(t)
	// user:token@ 形式的内嵌凭据。
	runTransferGit(t, dir, "remote", "add", "origin", "https://user:"+localSecret+"@local-origin.example.com/repo.git")
	project := addTransferTestProject(t, srv, dir)

	_, err := app.remoteStore.AddHost(model.Host{ID: "host-dev-mismatch", Name: "Dev Machine", DevMachineMode: true})
	require.NoError(t, err)

	reqBody := `{"host_id": "host-dev-mismatch"}`
	resp, err := http.Post(srv.URL+"/api/projects/"+project.ID+"/transfer/preflight", "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result transferPreflightResponse
	bodyBytes := readAndDecode(t, resp, &result)

	var mismatch *transferCheckItem
	for i := range result.Blockers {
		if result.Blockers[i].Code == "remote_url_mismatch" {
			mismatch = &result.Blockers[i]
			break
		}
	}
	require.NotNil(t, mismatch, "两个不同源的 origin 应触发 remote_url_mismatch，实际 blockers=%+v", result.Blockers)

	assert.NotContains(t, mismatch.Detail, localSecret, "本机 origin 的凭据不应出现在响应 Detail 里")
	assert.NotContains(t, mismatch.Detail, remoteSecret, "目标机 origin 的凭据不应出现在响应 Detail 里")
	// 同时用完整响应体兜底：防止凭据从其它字段（如未来新增字段）泄露出去，
	// 而不仅仅是 Detail 这一处。
	assert.NotContains(t, string(bodyBytes), localSecret, "完整响应体不应包含本机凭据")
	assert.NotContains(t, string(bodyBytes), remoteSecret, "完整响应体不应包含目标机凭据")

	// 非敏感部分应保留，证明是"摘除 userinfo"而非整体打码。
	assert.Contains(t, mismatch.Detail, "local-origin.example.com", "摘除凭据不应连带丢失主机名等非敏感信息")
	assert.Contains(t, mismatch.Detail, "remote-origin.example.com", "摘除凭据不应连带丢失主机名等非敏感信息")
}

// readAndDecode 读取响应体全文（供整体兜底断言）并同时解码进 out。
func readAndDecode(t *testing.T, resp *http.Response, out any) []byte {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(body, out))
	return body
}
