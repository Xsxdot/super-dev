// project_transfer_engine.go 实现项目归属转移的执行引擎（forward + 迁回）。
//
// 职责：
//   - 顺序编排一次转移的多个步骤（stop_dev / checkout / register / mcp_setup /
//     asset_audit / switch_home），单 goroutine 执行，状态存内存 App 字段
//   - 每步前后写 KindProjectTransfer 审计（prepared → executed/failed），
//     半完成态（进程重启丢失内存态）靠这条审计链追溯，prepared 即 outbox
//   - 汇总一份 AssetReport（需在新家启动的服务、缺失 env 文件、疑似密钥键名、
//     superdev-mcp 缺失）交人工确认
//
// 边界：
//   - 不搬运任何秘密值或 git 凭据：asset_audit 只列疑似密钥「键名」，绝不携带值；
//     checkout 走目标机自身的 git 访问权限，不代填凭据
//   - 不自动重启服务：v1 停掉本机 dev 部署后只记清单，不在新家自动拉起
//     （避免体检未过就启动）
//   - 状态不持久化：进程重启即丢失，status 端点 404 即「无进行中」
//
// 为什么 register 用 nodetransport Do 而 checkout 用 /ws/exec Runner：
//   - register / mcp_setup 是对目标机 agent 的短 HTTP 请求（30s 内返回），
//     经 nodetransport Do 直达目标 agent 的 HTTP API 最自然；
//   - checkout（git clone）以及路径探测（cd&&pwd / test -f / command -v）是
//     可能很长（clone 大仓库）或需要 shell 语义的命令，必须走 /ws/exec 的
//     Runner（无 30s 上限、逐行回流输出）。两条传输各司其职，不可互换：
//     clone 绝不能走 Do（会被 30s 掐断），注册也不该塞进一条 shell 命令。
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/gitinfo"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
	"github.com/xsxdot/super-dev/agent/operation"
	"github.com/xsxdot/super-dev/agent/pipeline"
	"github.com/xsxdot/super-dev/agent/process"
)

// transferNodeDo 是目标机短 HTTP 请求（register / mcp_setup）的测试注入 seam。
//
// 非 nil 时 resolveTransferNodeDo 优先返回它（引擎测试注入假件，绕开真实
// nodetransport 往返）；生产路径下保持 nil，回落到 a.nodeTransport.Do。
// 与 transferRemoteRunner（/ws/exec 的 Runner seam）成对存在：一个假 Do、
// 一个假 Runner，配真实 projectHomeStore + operationAudit 即可在引擎级
// 完整验证编排/失败跳过/审计留痕。
var transferNodeDo func(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error)

// 稳定步骤码：供前端/上游按类型分支，切勿随意改名。
const (
	transferStepStopDev    = "stop_dev"
	transferStepCheckout   = "checkout"
	transferStepRegister   = "register"
	transferStepMCPSetup   = "mcp_setup"
	transferStepAssetAudit = "asset_audit"
	transferStepSwitchHome = "switch_home"
	// 迁回专属步骤码
	transferStepProbeHome = "probe_home"
	transferStepPullLocal = "pull_local"
)

// 转移终态
const (
	transferStateRunning   = "running"
	transferStateSucceeded = "succeeded"
	transferStateFailed    = "failed"
)

// 步骤态
const (
	stepStatePending = "pending"
	stepStateRunning = "running"
	stepStateDone    = "done"
	stepStateFailed  = "failed"
	stepStateSkipped = "skipped"
)

// transferStatusResponse 是转移执行的状态快照，GET .../transfer/status 与引擎
// 测试消费。
type transferStatusResponse struct {
	State string         `json:"state"` // running / succeeded / failed
	Steps []transferStep `json:"steps"`
	// AssetReport 仅结束后非空：需在新家启动的服务 + EnvFile 缺失清单 +
	// 疑似秘密 env 键名清单（脱敏，只列键名）+ superdev-mcp 缺失提示。
	AssetReport []transferCheckItem `json:"asset_report,omitempty"`
	Error       string              `json:"error,omitempty"`
}

// transferStep 是转移中的单个步骤状态。
type transferStep struct {
	Code   string `json:"code"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// transferRun 是一次转移的完整内存态：引擎单 goroutine 写，status 端点读，
// 经 mu 互斥。字段区分「不变入参」与「运行中可变态」。
type transferRun struct {
	direction string // "forward" | "back"
	projectID string
	hostID    string
	hostName  string
	targetDir string // 原始目标目录（可能含 ~，未展开）
	branch    string
	repoURL   string

	plan operation.Plan // 本次转移共享的审计计划（免审批门，只承载 Kind/Target/Fingerprint）

	mu           sync.Mutex
	absTargetDir string // 展开 ~ / cd&&pwd 规范化后的目标机绝对路径
	state        string
	steps        []transferStep
	assetReport  []transferCheckItem
	errMsg       string
	startedAt    time.Time
	finishedAt   time.Time
}

// newTransferRun 构造一次转移的初始内存态（终态先置 running）。
func newTransferRun(projectID, hostID, hostName, targetDir, branch, repoURL string) *transferRun {
	return &transferRun{
		projectID: projectID,
		hostID:    hostID,
		hostName:  hostName,
		targetDir: targetDir,
		branch:    branch,
		repoURL:   repoURL,
		state:     transferStateRunning,
		startedAt: time.Now(),
	}
}

// transferDeps 是一次转移执行所需的注入依赖，execute 入口解析一次后透传给各步骤。
type transferDeps struct {
	runner  gitinfo.Runner
	do      func(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error)
	project model.Project
	mgr     *process.Manager
}

// transferStepSpec 描述引擎驱动一个步骤所需的元信息。
type transferStepSpec struct {
	code string
	fn   func(ctx context.Context, run *transferRun, deps *transferDeps) (string, error)
	// soft 为 true 时该步骤失败不阻断转移（仅 mcp_setup）：记 failed 步骤 +
	// 审计 failed + asset_report 提示，但后续步骤照常执行、归属照常切换。
	soft bool
}

// executeProjectTransfer 执行一次正向转移（本机 → 目标归属机），同步阻塞直到
// 全部步骤结束；HTTP 层负责在 goroutine 中调用它。
func (a *App) executeProjectTransfer(ctx context.Context, run *transferRun) {
	run.direction = "forward"
	run.plan = transferPlan(run)
	a.initSteps(run, []string{
		transferStepStopDev, transferStepCheckout, transferStepRegister,
		transferStepMCPSetup, transferStepAssetAudit, transferStepSwitchHome,
	})

	project, mgr, ok := a.transferProjectSnapshot(run.projectID)
	if !ok {
		a.abortRunBeforeSteps(run, fmt.Errorf("project %s not found", run.projectID))
		return
	}
	deps := &transferDeps{
		runner:  a.resolveTransferRunner(pipeline.Target{HostID: run.hostID, HostName: run.hostName}),
		do:      a.resolveTransferNodeDo(),
		project: project,
		mgr:     mgr,
	}

	log.Printf("[SuperDev] 项目转移开始 project=%s host=%s targetDir=%s branch=%s", run.projectID, run.hostID, run.targetDir, run.branch)
	a.runSteps(ctx, run, deps, []transferStepSpec{
		{transferStepStopDev, a.transferStopDev, false},
		{transferStepCheckout, a.transferCheckout, false},
		{transferStepRegister, a.transferRegister, false},
		{transferStepMCPSetup, a.transferMCPSetup, true},
		{transferStepAssetAudit, a.transferAssetAudit, false},
		{transferStepSwitchHome, a.transferSwitchHome, false},
	})
}

// executeProjectTransferBack 执行一次迁回（目标归属机 → 本机），v1 简化：
// 归属机侧探测未提交/未推送 → 本机 pull → 清除归属。run.hostID 为当前归属机。
func (a *App) executeProjectTransferBack(ctx context.Context, run *transferRun) {
	run.direction = "back"
	run.plan = transferPlan(run)
	a.initSteps(run, []string{transferStepProbeHome, transferStepPullLocal, transferStepSwitchHome})

	project, mgr, ok := a.transferProjectSnapshot(run.projectID)
	if !ok {
		a.abortRunBeforeSteps(run, fmt.Errorf("project %s not found", run.projectID))
		return
	}
	deps := &transferDeps{
		runner:  a.resolveTransferRunner(pipeline.Target{HostID: run.hostID, HostName: run.hostName}),
		do:      a.resolveTransferNodeDo(),
		project: project,
		mgr:     mgr,
	}

	log.Printf("[SuperDev] 项目迁回开始 project=%s home=%s targetDir=%s", run.projectID, run.hostID, run.targetDir)
	a.runSteps(ctx, run, deps, []transferStepSpec{
		{transferStepProbeHome, a.transferProbeHome, false},
		{transferStepPullLocal, a.transferPullLocal, false},
		{transferStepSwitchHome, a.transferSwitchHomeBack, false},
	})
}

// runSteps 是引擎的核心驱动：顺序执行步骤，每步写 prepared/executed/failed 审计，
// 任一硬失败后剩余步骤置 skipped、终态 failed；soft 步骤失败不阻断。
func (a *App) runSteps(ctx context.Context, run *transferRun, deps *transferDeps, steps []transferStepSpec) {
	failed := false
	for _, step := range steps {
		if failed {
			a.setStep(run, step.code, stepStateSkipped, "前序步骤失败，已跳过")
			continue
		}

		a.setStep(run, step.code, stepStateRunning, "")
		a.auditStep(ctx, run, step.code, operation.AuditPrepared, "transfer step prepared", nil)
		log.Printf("[SuperDev] 转移步骤开始 project=%s host=%s step=%s", run.projectID, run.hostID, step.code)

		detail, err := step.fn(ctx, run, deps)
		if err == nil {
			a.setStep(run, step.code, stepStateDone, detail)
			a.auditStep(ctx, run, step.code, operation.AuditExecuted, "transfer step executed", nil)
			log.Printf("[SuperDev] 转移步骤完成 project=%s host=%s step=%s detail=%s", run.projectID, run.hostID, step.code, redactCreds(detail))
			continue
		}

		// err.Error() 可能回显带内嵌凭据的 remote URL（git clone 失败输出），落库/落日志前脱敏。
		errMsg := redactCreds(err.Error())
		if step.soft {
			// 软失败：步骤本身记 failed，但转移继续、归属照常切换。
			a.setStep(run, step.code, stepStateFailed, detail)
			a.auditStep(ctx, run, step.code, operation.AuditFailed, "transfer step soft-failed: "+errMsg, map[string]any{"soft": true})
			log.Printf("[SuperDev] 转移步骤软失败(继续) project=%s host=%s step=%s err=%s", run.projectID, run.hostID, step.code, errMsg)
			continue
		}

		a.setStep(run, step.code, stepStateFailed, detail+"失败: "+errMsg)
		a.auditStep(ctx, run, step.code, operation.AuditFailed, "transfer step failed: "+errMsg, nil)
		log.Printf("[SuperDev] 转移步骤失败 project=%s host=%s step=%s err=%s", run.projectID, run.hostID, step.code, errMsg)
		run.setError(errMsg)
		failed = true
	}

	if failed {
		a.finalizeRun(run, transferStateFailed)
		log.Printf("[SuperDev] 项目转移失败 project=%s host=%s direction=%s，归属未切换", run.projectID, run.hostID, run.direction)
		return
	}
	a.finalizeRun(run, transferStateSucceeded)
	// switch_home 成功后的总结行——这是切面里最需要事后排障的一条。
	log.Printf("[SuperDev] 项目转移完成 project=%s host=%s direction=%s，归属已切换", run.projectID, run.hostID, run.direction)
}

// ---- 各步骤实现 ----

// transferStopDev 停止本机运行中的 dev 部署，并把清单写进 AssetReport
// （需在新家手动启动）。v1 不自动重启，避免体检未过就拉起。
func (a *App) transferStopDev(ctx context.Context, run *transferRun, deps *transferDeps) (string, error) {
	if deps.mgr == nil {
		return "无进程管理器，无运行中 dev 部署", nil
	}
	devEnvs := devEnvSet(deps.project)
	if len(devEnvs) == 0 {
		return "项目无 dev 环境", nil
	}
	var stopped []string
	var stoppedIDs []string
	for _, svc := range deps.project.Services {
		for _, dep := range svc.Deployments {
			if !devEnvs[dep.EnvName] {
				continue
			}
			if deps.mgr.IsDeploymentActive(dep.ID) {
				deps.mgr.StopDeployment(dep.ID)
				stopped = append(stopped, svc.Name+"("+dep.EnvName+")")
				stoppedIDs = append(stoppedIDs, dep.ID)
			}
		}
	}
	if len(stopped) == 0 {
		return "无运行中的 dev 部署", nil
	}

	// 核实真的停了：StopDeployment 不返回结果（停止失败只 emitLog），若不复查
	// 就计入「已停止」，本机进程可能还活着——转移完成后它与归属机上同端口的
	// 服务/镜像打架，用户面对的是「明明转移成功了怎么端口还冲突」。有界轮询
	// IsDeploymentActive，超时仍活跃即失败，把没停掉的清单如实报出来。
	if lingering := waitDeploymentsStopped(ctx, deps.mgr, stoppedIDs, transferStopVerifyTimeout); len(lingering) > 0 {
		return "核实 dev 部署停止", fmt.Errorf("以下 deployment 在 %s 内未停止：%s，请先手动停止后重试转移", transferStopVerifyTimeout, strings.Join(lingering, "、"))
	}
	run.addAsset(transferCheckItem{
		Code:   "restart_needed",
		Detail: fmt.Sprintf("以下 dev 服务已在本机停止，需在新家手动启动：%s", strings.Join(stopped, "、")),
	})
	return fmt.Sprintf("已停止 %d 个运行中的 dev 部署：%s", len(stopped), strings.Join(stopped, "、")), nil
}

// transferCheckout 让目标机把仓库检出到目标分支的最新提交。
//
// 为什么先解析绝对路径：EnsureCheckout 内部对 path 做 shell 单引号转义，若把
// 带 ~ 的路径直接传入，~ 不会被展开，会 clone 出一个字面量 ~ 目录。因此先把
// ~ 依目标机 $HOME 展开成绝对路径再交给 EnsureCheckout。
func (a *App) transferCheckout(ctx context.Context, run *transferRun, deps *transferDeps) (string, error) {
	absDir, err := resolveRemoteAbsDir(ctx, deps.runner, run.targetDir)
	if err != nil {
		return "解析目标绝对路径", err
	}
	run.setAbsDir(absDir)

	var lines []string
	if err := gitinfo.EnsureCheckout(ctx, deps.runner, absDir, run.repoURL, run.branch, func(l string) {
		lines = append(lines, l)
	}); err != nil {
		return "检出", err
	}
	return fmt.Sprintf("已检出到 %s；%s", absDir, strings.Join(tailLines(lines, 3), " ⏎ ")), nil
}

// transferRegister 在目标机注册项目：先 cd&&pwd 拿规范化绝对路径（此时目录已
// 被 checkout 建好），再经 nodetransport Do 调目标机 POST /api/projects。
// 目标机读到 project.yaml 里既有 ID 保留之；响应的 project.id 必须等于本机
// projectID，不等即失败（撞机重分配场景，拒绝静默双身份）。
func (a *App) transferRegister(ctx context.Context, run *transferRun, deps *transferDeps) (string, error) {
	absDir := run.getAbsDir()
	res, err := deps.runner(ctx, "cd "+shellQuoteTransfer(absDir)+" && pwd", "")
	if err != nil {
		return "解析规范化路径", err
	}
	if res.ExitCode != 0 {
		stderr := strings.Join(tailLines(strings.Split(strings.TrimRight(res.Stderr, "\n"), "\n"), 3), " ⏎ ")
		if stderr != "" {
			stderr = "：" + redactCreds(stderr)
		}
		return "解析规范化路径", fmt.Errorf("cd&&pwd 非零退出 exitCode=%d（目录可能未成功检出）%s", res.ExitCode, stderr)
	}
	rootPath := strings.TrimSpace(res.Stdout)
	if rootPath == "" {
		rootPath = absDir
	}
	run.setAbsDir(rootPath)

	body, _ := json.Marshal(map[string]string{"root_path": rootPath})
	resp, err := deps.do(ctx, run.hostID, nodetransport.NodeRequest{
		Method:  http.MethodPost,
		Path:    "/api/projects",
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body:    bytes.NewReader(body),
	})
	if err != nil {
		return "调用目标机注册项目", err
	}
	respBody, statusCode := readNodeBody(resp)
	if statusCode != http.StatusOK {
		return "目标机注册项目", fmt.Errorf("目标机返回 %d: %s", statusCode, truncateStr(respBody, 200))
	}
	var registered struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(respBody), &registered); err != nil {
		return "解析目标机注册响应", fmt.Errorf("响应非合法 JSON: %w", err)
	}
	if registered.ID != run.projectID {
		return "校验目标机项目身份", fmt.Errorf("目标机项目 ID 不一致：期望 %s 得到 %s（撞机重分配，拒绝双身份）", run.projectID, registered.ID)
	}
	return fmt.Sprintf("目标机项目已注册且 ID 一致（root_path=%s）", rootPath), nil
}

// transferMCPSetup 在目标机配置 claude-code 的 superdev-mcp（Task 6 提供目标侧
// handler）。失败不阻断转移：降级为 asset_report 提示并继续。
func (a *App) transferMCPSetup(ctx context.Context, run *transferRun, deps *transferDeps) (string, error) {
	body, _ := json.Marshal(map[string]string{"project_id": run.projectID, "root_path": run.getAbsDir()})
	resp, err := deps.do(ctx, run.hostID, nodetransport.NodeRequest{
		Method:  http.MethodPost,
		Path:    "/api/mcp-setup/claude-code",
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body:    bytes.NewReader(body),
	})
	if err != nil {
		run.addAsset(transferCheckItem{
			Code:   "mcp_setup_failed",
			Detail: "目标机 superdev-mcp 配置调用失败：" + redactCreds(err.Error()) + "，请在目标机手动运行 claude-code mcp-setup",
		})
		return "MCP 配置失败，已降级为资产提示，不阻断转移", err
	}
	respBody, statusCode := readNodeBody(resp)
	if statusCode != http.StatusOK {
		run.addAsset(transferCheckItem{
			Code:   "mcp_setup_failed",
			Detail: fmt.Sprintf("目标机 superdev-mcp 配置返回 %d，请在目标机手动运行 claude-code mcp-setup", statusCode),
		})
		return "MCP 配置失败，已降级为资产提示，不阻断转移", fmt.Errorf("目标机返回 %d: %s", statusCode, truncateStr(respBody, 200))
	}
	return "目标机 MCP 已配置", nil
}

// transferAssetAudit 只产 AssetReport 清单，不改变任何状态：
//   - 每个 dev 部署的 EnvFile（相对 targetDir）经 /ws/exec test -f 探是否就位
//   - 本机共享层 ScanSharedLayer 的疑似密钥「键名」（脱敏，绝不带值），提示目标机自行确认
//   - superdev-mcp 是否存在（command -v）
//
// 只有 Runner 传输层故障才让本步骤失败（连目标机都探不通就不该切归属）；
// 「文件缺失/mcp 缺失」是探测的负向结果，记进清单，步骤仍算 done。
func (a *App) transferAssetAudit(ctx context.Context, run *transferRun, deps *transferDeps) (string, error) {
	rootPath := run.getAbsDir()
	devEnvs := devEnvSet(deps.project)

	missing := 0
	for _, svc := range deps.project.Services {
		for _, dep := range svc.Deployments {
			if !devEnvs[dep.EnvName] || strings.TrimSpace(dep.EnvFile) == "" {
				continue
			}
			remotePath := path.Join(rootPath, dep.EnvFile)
			res, err := deps.runner(ctx, "test -f "+shellQuoteTransfer(remotePath)+" && echo yes || echo no", "")
			if err != nil {
				return "体检 env 文件", err
			}
			if strings.TrimSpace(res.Stdout) != "yes" {
				missing++
				run.addAsset(transferCheckItem{
					Code:   "env_file_missing",
					Detail: fmt.Sprintf("目标机缺失 env 文件 %s（服务 %s/%s），需在新家自行放置", dep.EnvFile, svc.Name, dep.EnvName),
				})
			}
		}
	}

	// 疑似密钥：照搬本机扫描结果，只列键名交目标机确认。绝不携带任何值。
	suspects, err := config.NewLoader(deps.project.RootPath).ScanSharedLayer()
	if err != nil {
		log.Printf("[SuperDev] 转移 asset_audit 扫描共享层疑似密钥失败 project=%s err=%v", run.projectID, err)
	}
	secretCount := 0
	for _, s := range suspects {
		secretCount++
		run.addAsset(transferCheckItem{
			Code:   "suspect_secret",
			Detail: fmt.Sprintf("疑似密钥键名 %q（作用域 %s），目标机需自行确认对应值是否已就位", s.Key, s.Scope),
		})
	}

	// git 忽略的顶层文件：这一类是转移**结构性**搬不走的——目标机靠 git clone
	// 取代码，被忽略的文件根本不在 clone 里。而它们恰恰常常是服务跑起来的前提
	// （config.yaml、证书、本地覆盖配置）。真机上 openrouter 的 config.yaml 正是
	// 这样静默缺失的：审计报了 superdev-mcp 缺失，唯独没报它，因为此前只按
	// dep.EnvFile 这一条线索找。
	ignoredMissing := a.auditIgnoredTopLevelFiles(ctx, run, deps, rootPath)

	// superdev-mcp 存在性
	res, err := deps.runner(ctx, "command -v superdev-mcp", "")
	if err != nil {
		return "体检 superdev-mcp", err
	}
	mcpMissing := res.ExitCode != 0
	if mcpMissing {
		run.addAsset(transferCheckItem{
			Code:   "superdev_mcp_missing",
			Detail: "目标机未找到 superdev-mcp 可执行文件，代码调试/日志采集能力将不可用，请先在目标机安装",
		})
	}

	return fmt.Sprintf("资产体检完成：缺失 env %d 项，git 忽略文件缺失 %d 项，疑似密钥 %d 项，superdev-mcp %s",
		missing, ignoredMissing, secretCount, ternaryStr(mcpMissing, "缺失", "就位")), nil
}

// transferIgnoredFileScanLimit 是每次转移最多列举的「git 忽略文件」条数。
// 上限存在的理由：仓库根目录下可能有大量被忽略的杂项，全部列出会把真正
// 重要的两三条淹掉。命中上限时必须记日志说明被截断——静默截断会让清单
// 读起来像「已经查全了」。
const transferIgnoredFileScanLimit = 20

// auditIgnoredTopLevelFiles 列出本机项目根目录下被 git 忽略的**顶层文件**，
// 逐个到目标机探是否存在，缺失的记进资产清单。
//
// 参数：
//   - rootPath: 目标机项目根的绝对路径
//
// 返回：
//   - 目标机缺失的条数（只用于摘要文案）
//
// 为什么只扫顶层文件（--directory 让被忽略的整个目录折叠成一条目录项，
// 随后按结尾的 "/" 剔除）：node_modules/、target/、.venv/ 这类目录动辄上万
// 个文件，逐个探测既没有意义也探不完；而真正「服务跑不起来」的那一类
// ——config.yaml、*.local.yaml、证书——几乎总在仓库根目录。
//
// 为什么探测失败不让整个步骤失败：这是审计的负向结果，与 env 文件缺失同级；
// 探不通目标机的情况由后面的 superdev-mcp 探测统一暴露。
func (a *App) auditIgnoredTopLevelFiles(ctx context.Context, run *transferRun, deps *transferDeps, rootPath string) int {
	out, err := gitinfo.ListIgnoredEntries(ctx, deps.project.RootPath)
	if err != nil {
		log.Printf("[SuperDev] 转移 asset_audit 列举 git 忽略文件失败 project=%s err=%v", run.projectID, err)
		return 0
	}

	var candidates []string
	for _, name := range out {
		name = strings.TrimSpace(name)
		// 目录项（以 / 结尾）与子目录下的条目一律跳过——见上方注释。
		if name == "" || strings.HasSuffix(name, "/") || strings.Contains(name, "/") {
			continue
		}
		candidates = append(candidates, name)
	}
	if len(candidates) > transferIgnoredFileScanLimit {
		log.Printf("[SuperDev] 转移 asset_audit: git 忽略文件过多，只体检前 %d 条 project=%s total=%d",
			transferIgnoredFileScanLimit, run.projectID, len(candidates))
		run.addAsset(transferCheckItem{
			Code: "ignored_scan_truncated",
			Detail: fmt.Sprintf("本机根目录下有 %d 个被 git 忽略的文件，只体检了前 %d 个，其余需自行核对",
				len(candidates), transferIgnoredFileScanLimit),
		})
		candidates = candidates[:transferIgnoredFileScanLimit]
	}

	missing := 0
	for _, name := range candidates {
		remotePath := path.Join(rootPath, name)
		res, err := deps.runner(ctx, "test -f "+shellQuoteTransfer(remotePath)+" && echo yes || echo no", "")
		if err != nil {
			log.Printf("[SuperDev] 转移 asset_audit 探测 git 忽略文件失败 project=%s file=%s err=%v", run.projectID, name, err)
			return missing
		}
		if strings.TrimSpace(res.Stdout) == "yes" {
			continue
		}
		missing++
		run.addAsset(transferCheckItem{
			Code:   "ignored_file_missing",
			Detail: fmt.Sprintf("本机有 %s 但目标机没有——该文件被 git 忽略，clone 不会带过去，需自行放置（SuperDev 不搬运其内容）", name),
		})
	}
	return missing
}

// transferSwitchHome 切换项目归属到目标机。
//
// SetHome 生效后立即调用 rebuildProjectBackendsOnHomeChange：a.backends 是
// 按 dep.ID 缓存的 LogBackend 实例，只在 register 时按当时的归属状态构建
// 一次，SetHome 本身不会让已缓存的实例感知归属变化——不重建的话，归属切换
// 后下一次日志读取仍会打向本机 SQLite，直到进程重启或其他偶然触发
// register 的操作发生，期间日志读取悄悄读错机器且没有任何报错提示。
func (a *App) transferSwitchHome(_ context.Context, run *transferRun, _ *transferDeps) (string, error) {
	// 随归属一并持久化归属机侧项目目录（checkout/register 阶段已规范化的
	// 绝对路径）：迁回时靠它定位归属机仓库，不能依赖内存中的转移记录
	// （agent 重启即丢）或默认目录猜测（自定义目录必然打错）。
	if err := a.projectHomeStore.SetHome(run.projectID, run.hostID, run.getAbsDir()); err != nil {
		return "切换归属", err
	}
	a.rebuildProjectBackendsOnHomeChange(run.projectID, run.hostID)
	return fmt.Sprintf("归属已切换到 %s(%s)", run.hostName, run.hostID), nil
}

// ---- 迁回步骤实现 ----

// transferProbeHome 在归属机侧探测未提交/未推送变更（同 gitinfo 口径：
// status --porcelain + rev-list @{u}..HEAD）。任一存在即失败：避免本机 pull
// 覆盖丢失归属机上尚未回流的改动。
func (a *App) transferProbeHome(ctx context.Context, run *transferRun, deps *transferDeps) (string, error) {
	absDir, err := resolveRemoteAbsDir(ctx, deps.runner, run.targetDir)
	if err != nil {
		return "解析归属机路径", err
	}
	run.setAbsDir(absDir)

	res, err := deps.runner(ctx, "cd "+shellQuoteTransfer(absDir)+" && pwd", "")
	if err != nil {
		return "定位归属机项目目录", err
	}
	if res.ExitCode != 0 {
		stderr := strings.Join(tailLines(strings.Split(strings.TrimRight(res.Stderr, "\n"), "\n"), 3), " ⏎ ")
		if stderr != "" {
			stderr = "：" + redactCreds(stderr)
		}
		return "定位归属机项目目录", fmt.Errorf("cd&&pwd 非零退出 exitCode=%d（归属机目录可能不存在）%s", res.ExitCode, stderr)
	}
	homeDir := strings.TrimSpace(res.Stdout)
	if homeDir == "" {
		homeDir = absDir
	}

	statusRes, err := deps.runner(ctx, "git -C "+shellQuoteTransfer(homeDir)+" status --porcelain", "")
	if err != nil {
		return "探测归属机未提交变更", err
	}
	if strings.TrimSpace(statusRes.Stdout) != "" {
		return "归属机存在未提交变更", fmt.Errorf("归属机有未提交变更，请先在归属机提交或暂存后再迁回")
	}

	countRes, err := deps.runner(ctx, "git -C "+shellQuoteTransfer(homeDir)+" rev-list --count @{u}..HEAD", "")
	if err != nil {
		return "探测归属机未推送提交", err
	}
	// rev-list 非零退出多为「无上游」，v1 简化：无从判定则不阻断。
	if countRes.ExitCode == 0 {
		if n := strings.TrimSpace(countRes.Stdout); n != "" && n != "0" {
			return "归属机存在未推送提交", fmt.Errorf("归属机有 %s 个未推送提交，请先在归属机推送后再迁回", n)
		}
	}

	// 对称性检查：正向转移有 running_dev blocker + stop_dev 步骤，迁回同样不能
	// 把归属机上运行中的 dev 服务丢下不管——迁回后它们变成无人认领的孤儿进程
	// 继续占端口（镜像语义混乱，本机再启动同服务即撞端口）。v1 不跨机自动停止
	// （远程停进程应由用户经归属路由的启停界面显式操作），探到即失败并列清单。
	if running, checkErr := a.probeHomeRunningDev(ctx, run, deps); checkErr != nil {
		// 探测本身失败（网络/端点异常）不阻断迁回：git 探测已证明归属机可达，
		// 这条附加信号拿不到时降级为日志留痕，与版本核对「失败只提示」同哲学。
		log.Printf("[SuperDev] 迁回 probe_home: 归属机运行态探测失败（不阻断） project=%s home=%s err=%v", run.projectID, run.hostID, checkErr)
	} else if len(running) > 0 {
		return "归属机存在运行中的 dev 服务", fmt.Errorf("归属机上有 %d 个 dev 服务仍在运行：%s，请先在服务面板停止它们再迁回", len(running), strings.Join(running, "、"))
	}

	return fmt.Sprintf("归属机工作区干净、无未推送提交（%s）", homeDir), nil
}

// probeHomeRunningDev 经 nodetransport Do 查询归属机上该项目的运行态快照，
// 返回 dev 环境下仍处于活跃健康态（running/healthy/restarting）的实例描述列表。
//
// 返回：
//   - 活跃 dev 实例的 "服务名(环境)" 列表，空表示归属机上没有在跑的 dev 服务
//   - 查询失败（传输层错误/非 2xx/响应不可解析）返回 error，由调用方决定降级
func (a *App) probeHomeRunningDev(ctx context.Context, run *transferRun, deps *transferDeps) ([]string, error) {
	resp, err := deps.do(ctx, run.hostID, nodetransport.NodeRequest{
		Method: http.MethodGet,
		Path:   "/api/projects/" + run.projectID + "/runtime-status",
	})
	if err != nil {
		return nil, err
	}
	body, statusCode := readNodeBody(resp)
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("归属机 runtime-status 返回 %d", statusCode)
	}
	var status model.RuntimeStatusResponse
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		return nil, fmt.Errorf("归属机 runtime-status 响应解析失败: %w", err)
	}

	devEnvs := devEnvSet(deps.project)
	var running []string
	for _, env := range status.Environments {
		if !devEnvs[env.EnvName] {
			continue
		}
		for _, inst := range env.Instances {
			switch inst.Metrics.Health {
			case model.HealthRunning, model.HealthHealthy, model.HealthRestarting:
				running = append(running, inst.ServiceName+"("+env.EnvName+")")
			}
		}
	}
	return running, nil
}

// transferPullLocal 在本机执行 git pull --ff-only，把归属机推上来的最新提交拉回。
func (a *App) transferPullLocal(ctx context.Context, _ *transferRun, deps *transferDeps) (string, error) {
	out, err := runLocalGit(ctx, deps.project.RootPath, "pull", "--ff-only")
	if err != nil {
		return "本机 pull", fmt.Errorf("%v: %s", err, truncateStr(out, 200))
	}
	return "本机已 pull 到最新（" + truncateStr(strings.TrimSpace(out), 120) + "）", nil
}

// transferSwitchHomeBack 清除归属，迁回本机（SetHome 传空串）。
//
// 同 transferSwitchHome，清除归属后同样必须重建 backend，否则本机日志读取
// 会继续误转发给已经不再是归属机的旧目标（buildBackend 缓存的
// RemoteAgentBackend 不会因为 SetHome("") 自动失效）。
func (a *App) transferSwitchHomeBack(_ context.Context, run *transferRun, _ *transferDeps) (string, error) {
	if err := a.projectHomeStore.SetHome(run.projectID, "", ""); err != nil {
		return "迁回本机（清除归属）", err
	}
	a.rebuildProjectBackendsOnHomeChange(run.projectID, "")
	return "归属已迁回本机", nil
}

// rebuildProjectBackendsOnHomeChange 在项目归属变更（切换到新机或清除回本机）
// 生效后，重建该项目的日志读取 backend（Task 8：归属路由——日志链路）。
//
// 参数：
//   - projectID: 归属发生变化的项目
//   - newHomeHostID: 变更后的归属主机 ID；空串表示清除归属、迁回本机
//
// 为什么必须重建：a.backends 是按 dep.ID 缓存的 LogBackend 实例，只在
// registerProjectBackendsLocked 时依据当时的归属状态构建一次；SetHome
// 只改落盘/内存的归属记录本身，不会让已缓存的 backend 感知变化。清除 +
// 重新注册（复用 clearProjectBackendsLocked/registerProjectBackendsLocked
// 既有惯例，与 handler_vscode.go/handler_config_changes.go 等其余重建点
// 同一套写法）能让下一次日志读取立刻用上新的归属路由。
//
// 加锁：clearProjectBackendsLocked/registerProjectBackendsLocked/findProject
// 均要求调用方持有 a.mu；本函数持有完整写锁 Lock（而非 RLock），因为
// register 会写 a.backends 这张共享 map。transferSwitchHome(Back) 调用本
// 函数前不持有 a.mu（转移引擎的 project 快照在 transferProjectSnapshot 里
// 已经 RLock 后立即释放），这里新取一次 Lock 不会自锁。
func (a *App) rebuildProjectBackendsOnHomeChange(projectID, newHomeHostID string) {
	a.mu.Lock()
	project, ok := a.findProject(projectID)
	if ok {
		a.clearProjectBackendsLocked(project)
		a.registerProjectBackendsLocked(project)
	}
	a.mu.Unlock()

	if !ok {
		// 项目在归属切换的同时被删除，理论上不该发生（转移引擎全程持有
		// 该项目的执行上下文），防御性跳过，不重复报错——SetHome 本身已经
		// 成功，缺失项目不影响归属状态的正确性，只是没有 backend 可重建。
		return
	}
	homeDesc := newHomeHostID
	if homeDesc == "" {
		homeDesc = "本机"
	}
	log.Printf("[SuperDev] home-transfer: 归属变更触发 backend 重建 project=%s new_home=%s", projectID, homeDesc)
}

// ---- 依赖解析 / 快照 ----

// resolveTransferNodeDo 返回本次转移使用的 nodetransport Do。
// transferNodeDo 非 nil 时优先（测试注入）；否则回落 a.nodeTransport.Do。
func (a *App) resolveTransferNodeDo() func(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	if transferNodeDo != nil {
		return transferNodeDo
	}
	if a.nodeTransport == nil {
		return func(context.Context, string, nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
			return nodetransport.NodeResponse{}, fmt.Errorf("nodeTransport 未装配")
		}
	}
	return a.nodeTransport.Do
}

// transferProjectSnapshot 在 RLock 下取项目快照与其进程管理器。
func (a *App) transferProjectSnapshot(projectID string) (model.Project, *process.Manager, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	p, ok := a.findProject(projectID)
	if !ok {
		return model.Project{}, nil, false
	}
	return p, a.managers[projectID], true
}

// ---- 审计 ----

// transferPlan 构造本次转移共享的审计计划——RequiresApproval 恒为 false
// （免审批门，preflight→execute 人审对话框即人工审查），只承载
// Kind/Target/Fingerprint 供审计事件引用。Target.ProjectID 必填，审计按它过滤。
func transferPlan(run *transferRun) operation.Plan {
	now := time.Now().UTC()
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s", operation.KindProjectTransfer, run.projectID, run.hostID, run.direction)))
	return operation.Plan{
		ID:               "op_" + uuid.NewString(),
		Kind:             operation.KindProjectTransfer,
		Target:           operation.Target{ProjectID: run.projectID, HostID: run.hostID},
		TargetSummary:    fmt.Sprintf("transfer project %s home to host %s (%s)", run.projectID, run.hostID, run.direction),
		RiskLevel:        operation.RiskMedium,
		RequiresApproval: false,
		ExpectedEffects:  []string{fmt.Sprintf("move project %s dev home to %s", run.projectID, run.hostID)},
		Fingerprint:      "sha256:" + hex.EncodeToString(sum[:]),
		CreatedAt:        now,
		ExpiresAt:        now.Add(operation.DefaultPlanTTL),
	}
}

// auditStep 写一条转移步骤审计事件。Data 只承载步骤码/项目/host/结果，
// 绝不携带任何秘密值或 git 凭据。审计写失败只记日志、不阻断转移。
func (a *App) auditStep(ctx context.Context, run *transferRun, stepCode, action, summary string, extra map[string]any) {
	data := map[string]any{
		"step":       stepCode,
		"project_id": run.projectID,
		"host_id":    run.hostID,
		"host_name":  run.hostName,
		"direction":  run.direction,
	}
	for k, v := range extra {
		data[k] = v
	}
	if _, err := a.operationAudit.Append(ctx, operation.AuditEvent{
		Kind:    operation.KindProjectTransfer,
		Action:  action,
		Plan:    run.plan,
		Summary: summary,
		Data:    data,
	}); err != nil {
		log.Printf("[SuperDev] 转移审计写入失败 project=%s step=%s action=%s err=%v", run.projectID, stepCode, action, err)
	}
}

// ---- transferRun 内部态操作（全部经 mu 互斥） ----

func (a *App) initSteps(run *transferRun, codes []string) {
	run.mu.Lock()
	defer run.mu.Unlock()
	run.steps = make([]transferStep, 0, len(codes))
	for _, c := range codes {
		run.steps = append(run.steps, transferStep{Code: c, State: stepStatePending})
	}
}

func (a *App) setStep(run *transferRun, code, state, detail string) {
	run.mu.Lock()
	defer run.mu.Unlock()
	for i := range run.steps {
		if run.steps[i].Code == code {
			run.steps[i].State = state
			if detail != "" {
				// 统一在写入前脱敏：任何步骤 detail 都可能夹带 git 输出里的内嵌凭据。
				run.steps[i].Detail = redactCreds(detail)
			}
			return
		}
	}
}

func (a *App) finalizeRun(run *transferRun, state string) {
	run.mu.Lock()
	defer run.mu.Unlock()
	run.state = state
	run.finishedAt = time.Now()
}

// abortRunBeforeSteps 处理步骤开始前的致命错误（如项目不存在）：置终态 failed。
func (a *App) abortRunBeforeSteps(run *transferRun, err error) {
	log.Printf("[SuperDev] 项目转移启动失败 project=%s host=%s err=%v", run.projectID, run.hostID, err)
	run.setError(err.Error())
	a.finalizeRun(run, transferStateFailed)
}

func (r *transferRun) setError(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.errMsg == "" {
		r.errMsg = msg
	}
}

func (r *transferRun) addAsset(item transferCheckItem) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.assetReport = append(r.assetReport, item)
}

func (r *transferRun) setAbsDir(dir string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.absTargetDir = dir
}

func (r *transferRun) getAbsDir() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.absTargetDir
}

// snapshot 返回当前状态的深拷贝。AssetReport 仅在转移结束后（非 running）暴露，
// 运行中不暴露半成品清单。
func (r *transferRun) snapshot() transferStatusResponse {
	r.mu.Lock()
	defer r.mu.Unlock()
	steps := make([]transferStep, len(r.steps))
	copy(steps, r.steps)
	resp := transferStatusResponse{
		State: r.state,
		Steps: steps,
		Error: r.errMsg,
	}
	if r.state != transferStateRunning && len(r.assetReport) > 0 {
		report := make([]transferCheckItem, len(r.assetReport))
		copy(report, r.assetReport)
		resp.AssetReport = report
	}
	return resp
}

// ---- 小工具 ----

// devEnvSet 返回项目 dev 环境名集合。
func devEnvSet(project model.Project) map[string]bool {
	set := make(map[string]bool, len(project.Environments))
	for _, env := range project.Environments {
		if env.IsDev {
			set[env.Name] = true
		}
	}
	return set
}

// resolveRemoteAbsDir 把目标目录展开成目标机上的绝对路径。
//
// 不含 ~ 时原样返回（视为已是绝对路径）。含 ~ 时依目标机 $HOME 展开：
// `cd && pwd`（cd 无参进 $HOME，恒成功，不依赖目标目录已存在）拿到 $HOME 后
// 在 Go 侧拼接——刻意不靠 shell 展开 ~（EnsureCheckout 会单引号转义路径，
// shell 不会展开引号内的 ~）。
func resolveRemoteAbsDir(ctx context.Context, run gitinfo.Runner, dir string) (string, error) {
	if !strings.HasPrefix(dir, "~") {
		return dir, nil
	}
	res, err := run(ctx, "cd && pwd", "")
	if err != nil {
		return "", fmt.Errorf("解析目标机 $HOME: %w", err)
	}
	if res.ExitCode != 0 {
		stderr := strings.Join(tailLines(strings.Split(strings.TrimRight(res.Stderr, "\n"), "\n"), 3), " ⏎ ")
		if stderr != "" {
			stderr = "：" + redactCreds(stderr)
		}
		return "", fmt.Errorf("解析目标机 $HOME 非零退出 exitCode=%d%s", res.ExitCode, stderr)
	}
	home := strings.TrimSpace(res.Stdout)
	if home == "" {
		return "", fmt.Errorf("目标机 $HOME 解析为空")
	}
	return home + strings.TrimPrefix(dir, "~"), nil
}

// shellQuoteTransfer 单引号包裹并转义，用于把路径安全嵌入 shell 命令。
// 与 gitinfo.shellQuote 同款（gitinfo 内的是私有，无法跨包复用）。
func shellQuoteTransfer(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// urlCredRe 命中把凭据内嵌进 URL 的写法：scheme:// 与首个 @ 之间的整段
// userinfo（user:pass@ 与裸 token@ 两种形态都算）。git 输出会原样回显
// remote URL，若源地址内嵌了凭据（https://x-access-token:ghp_xxx@github.com/
// 或 GitHub PAT 惯用的裸 token 形态 https://ghp_xxx@github.com/），不脱敏就
// 会随 detail/日志/审计外泄。userinfo 段不含 /、空白，故 http://host:8080/path
// （@ 只会出现在 path/query 里、且 host:port 后必有 / 或串尾）不会误伤。
var urlCredRe = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://)[^/\s@]+@`)

// redactCreds 把 URL 里内嵌的整段 userinfo 替换为 ***，用于一切要落库/落日志/
// 落审计的字符串。这是转移引擎自身输出的红线兜底，不替代 gitinfo 侧的处理。
//
// 为什么整段替换而不是只遮 pass 段：pull_local 走的是本机仓库自己配置的
// origin，不经过 stripURLCredentials 的源头摘除，这里是它唯一的防线——
// GitHub PAT 恰恰是 token-as-username 的裸形态（https://ghp_xxx@host），
// 只遮「user:pass」的冒号形态会漏掉它。宁可把无害的 ssh://git@host 一并
// 遮掉（安全方向的误伤），也不能放走一个真 token。
func redactCreds(s string) string {
	return urlCredRe.ReplaceAllString(s, "${1}***@")
}

// stripURLCredentials 从 remote URL 上摘除内嵌的 userinfo（user:pass@ 或裸
// token@），返回不含任何凭据的 clone 源地址。
//
// 这是「不搬运 git 凭据」的根因修复：转移的模型是「目标机用它自己的 git 访问权限
// 检出，clone 失败即目标机自身的权限问题、如实上报供人工处理」，因此传给目标机
// 的 clone URL 绝不能夹带控制面用户的凭据。摘除后：
//   - 凭据不会作为 `git clone <url>` 参数抵达目标机（关闭「搬运凭据」通路）；
//   - clone 失败时 gitinfo 在 Task 5 下一层原样回显的 URL 里也不含凭据
//     （关闭那条 Task 5 无法在返回错误里拦截的日志泄漏）；
//   - 裸 token 形式（redactCreds 命不中）也一并被摘除。
//
// URL 无法解析时（scp-like `git@host:path` 等边界形态）保持原样返回：这类形态
// url.Parse 不产生 userinfo（u.User==nil），本就无凭据可摘，不冒重排/丢字符的风险。
func stripURLCredentials(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.User == nil {
		return rawURL
	}
	u.User = nil
	return u.String()
}

// readNodeBody 读取并关闭 NodeResponse.Body，返回响应体与状态码。
func readNodeBody(resp nodetransport.NodeResponse) (string, int) {
	if resp.Body == nil {
		return "", resp.StatusCode
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), resp.StatusCode
}

// transferStopVerifyTimeout 是 stop_dev 步骤核实进程真正退出的等待上限。
// 托管服务默认 SIGTERM + 宽限期强杀，正常在几秒内退出；超过这个窗口仍活跃
// 说明停止路径本身出了问题，应中断转移交人工处理，而不是带病切归属。
const transferStopVerifyTimeout = 15 * time.Second

// waitDeploymentsStopped 有界轮询等待一组 deployment 全部退出活跃态，
// 返回超时后仍活跃的 deployment ID 列表（空表示全部已停止）。
func waitDeploymentsStopped(ctx context.Context, mgr *process.Manager, ids []string, timeout time.Duration) []string {
	deadline := time.Now().Add(timeout)
	for {
		var lingering []string
		for _, id := range ids {
			if mgr.IsDeploymentActive(id) {
				lingering = append(lingering, id)
			}
		}
		if len(lingering) == 0 {
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return lingering
		}
		select {
		case <-ctx.Done():
			return lingering
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// runLocalGit 在本机 rootPath 上执行一条 git 命令，返回合并输出。
func runLocalGit(ctx context.Context, rootPath string, args ...string) (string, error) {
	full := append([]string{"-C", rootPath}, args...)
	out, err := exec.CommandContext(ctx, "git", full...).CombinedOutput()
	return string(out), err
}

// tailLines 返回末尾至多 n 行。
func tailLines(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

// truncateStr 把字符串截断到至多 n 个 rune，超长追加省略号。
func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func ternaryStr(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
