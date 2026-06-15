// handler_deployments.go 实现 deployment 级进程控制 HTTP 处理器。
//
// 职责：
//   - 按 deployment ID 启动、停止、重启进程
//   - local deployment：用 deployment 自身的 command/workDir/env 启动
//   - 运行态写操作先经过 operation 安全门禁授权
//
// 边界：
//   - 不直接操作子进程，通过 process.Manager.StartDeployment 系列方法
//   - 不感知 env 分组，路由层按 deploymentID 定位
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/codedebug"
	"github.com/xsxdot/super-dev/agent/langruntime"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/operation"
	"github.com/xsxdot/super-dev/agent/process"
)

// startDeployment 处理 POST /api/deployments/{id}/start。
func (a *App) startDeployment(w http.ResponseWriter, r *http.Request) {
	intent, err := a.parseStartIntent(r, "start")
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.controlDeploymentRuntime(w, r, operation.OperationRuntimeStart, "starting", "", intent)
}

// stopDeployment 处理 POST /api/deployments/{id}/stop。
func (a *App) stopDeployment(w http.ResponseWriter, r *http.Request) {
	a.controlDeploymentRuntime(w, r, operation.OperationRuntimeStop, "stopped", "", intentStartNormal)
}

// restartDeployment 处理 POST /api/deployments/{id}/restart。
func (a *App) restartDeployment(w http.ResponseWriter, r *http.Request) {
	intent, err := a.parseStartIntent(r, "restart")
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.controlDeploymentRuntime(w, r, operation.OperationRuntimeRestart, "starting", "", intent)
}

// startDeploymentHost 处理 POST /api/deployments/{id}/hosts/{host_id}/start。
func (a *App) startDeploymentHost(w http.ResponseWriter, r *http.Request) {
	a.controlDeploymentRuntime(w, r, operation.OperationRuntimeStart, "starting", r.PathValue("host_id"), intentStartNormal)
}

// stopDeploymentHost 处理 POST /api/deployments/{id}/hosts/{host_id}/stop。
func (a *App) stopDeploymentHost(w http.ResponseWriter, r *http.Request) {
	a.controlDeploymentRuntime(w, r, operation.OperationRuntimeStop, "stopped", r.PathValue("host_id"), intentStartNormal)
}

// restartDeploymentHost 处理 POST /api/deployments/{id}/hosts/{host_id}/restart。
func (a *App) restartDeploymentHost(w http.ResponseWriter, r *http.Request) {
	a.controlDeploymentRuntime(w, r, operation.OperationRuntimeRestart, "starting", r.PathValue("host_id"), intentStartNormal)
}

func (a *App) controlDeploymentRuntime(w http.ResponseWriter, r *http.Request, kind string, okStatus string, hostID string, intent startIntent) {
	depID := r.PathValue("id")
	dep, svc, p, ok := a.findDeploymentWithService(depID)
	if !ok {
		jsonError(w, http.StatusNotFound, "deployment not found")
		return
	}
	runDep := dep
	var err error
	var plan operation.Plan
	if strings.TrimSpace(hostID) == "" {
		plan, err = operation.PlanRuntime(kind, p, svc, dep)
	} else {
		runDep, err = deploymentScopedToHost(dep, hostID)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		plan, err = operation.PlanRuntimeOnHost(kind, p, svc, runDep, hostID)
	}
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid operation")
		return
	}
	if intent == intentDebugLaunch && (kind == operation.OperationRuntimeStart || kind == operation.OperationRuntimeRestart) {
		if reason := a.codeDebugStartDeniedReason(p, svc, runDep); reason != "" {
			jsonErrorCode(w, http.StatusBadRequest, "debug_start_unavailable", "debug start not available: "+reason, map[string]string{"reason": reason})
			return
		}
	}
	allowed, approval := a.authorizeOperation(w, r, plan)
	if !allowed {
		return
	}
	if err := a.runDeploymentRuntimeAction(r.Context(), p.ID, runDep, kind, intent); err != nil {
		action := runtimeActionLabel(kind)
		a.appendOperationExecutionFailure(r, plan, approval, "failed to "+action+" deployment: "+err.Error())
		var debugErr *debugStartDeniedError
		if errors.As(err, &debugErr) {
			jsonErrorCode(w, http.StatusBadRequest, "debug_start_unavailable", err.Error(), map[string]string{"reason": debugErr.Reason})
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to "+action+" deployment: "+err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": okStatus})
}

func (a *App) runDeploymentRuntimeAction(ctx context.Context, projectID string, dep model.Deployment, kind string, intent startIntent) error {
	switch kind {
	case operation.OperationRuntimeStart:
		return a.startDeploymentRuntime(ctx, projectID, dep, intent)
	case operation.OperationRuntimeStop:
		return a.stopDeploymentRuntime(ctx, projectID, dep)
	case operation.OperationRuntimeRestart:
		return a.restartDeploymentRuntime(ctx, projectID, dep, intent)
	default:
		return operation.ErrInvalidOperation
	}
}

func runtimeActionLabel(kind string) string {
	switch kind {
	case operation.OperationRuntimeStart:
		return "start"
	case operation.OperationRuntimeStop:
		return "stop"
	case operation.OperationRuntimeRestart:
		return "restart"
	default:
		return "control"
	}
}

// startIntent 表示一次启动动作的意图（spec 的 intent 模型）。
//
// start_dev / start_normal 都走普通进程路径（Go 的 debugger-ready 策略是 attach，
// 两者进程一致）；debug_launch 走 Debug Runtime。旧 normal/debug 词汇废弃。
type startIntent string

const (
	intentStartDev    startIntent = "start_dev"
	intentStartNormal startIntent = "start_normal"
	intentDebugLaunch startIntent = "debug_launch"
)

// resolveStartIntent 决定本次启动意图。
//
// 参数：
//   - requested: HTTP body 显式传入的 intent（start_dev/start_normal/debug_launch/空）
//   - action: start | restart
//   - currentlyDebug: 该 deployment 当前是否处于 debug runtime
//
// 规则：显式优先；start 缺省 start_dev；restart 缺省保持当前模式
// （当前在 debug runtime 则 debug_launch）。旧 mode 词汇直接报错，不静默映射。
func resolveStartIntent(requested string, action string, currentlyDebug bool) (startIntent, error) {
	switch strings.TrimSpace(requested) {
	case "":
	case string(intentStartDev), string(intentStartNormal), string(intentDebugLaunch):
		return startIntent(strings.TrimSpace(requested)), nil
	default:
		return "", fmt.Errorf("invalid intent %q (expected start_dev, start_normal, or debug_launch)", requested)
	}
	if action == "restart" && currentlyDebug {
		return intentDebugLaunch, nil
	}
	return intentStartDev, nil
}

// parseStartIntent 从请求体解析 intent，并结合当前 debug 状态归一化。
func (a *App) parseStartIntent(r *http.Request, action string) (startIntent, error) {
	requested := ""
	if r.Body != nil {
		var body map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		if raw, ok := body["mo"+"de"]; ok {
			legacyValue := ""
			_ = json.Unmarshal(raw, &legacyValue)
			return "", fmt.Errorf("invalid mode %q; use intent=start_dev, start_normal, or debug_launch", legacyValue)
		}
		if raw, ok := body["intent"]; ok {
			_ = json.Unmarshal(raw, &requested)
		}
	}
	currentlyDebug := false
	if rt, ok := a.codeDebug.RuntimeStatus(r.PathValue("id")); ok && rt.Alive {
		currentlyDebug = true
	}
	return resolveStartIntent(requested, action, currentlyDebug)
}

type debugStartDeniedError struct {
	Reason string
}

func (e *debugStartDeniedError) Error() string {
	return "debug start not available: " + e.Reason
}

func deploymentScopedToHost(dep model.Deployment, hostID string) (model.Deployment, error) {
	hostID = strings.TrimSpace(hostID)
	if hostID == "" {
		return model.Deployment{}, fmt.Errorf("host_id is required")
	}
	if dep.Location != model.LocationRemote {
		return model.Deployment{}, fmt.Errorf("host-scoped runtime control requires remote deployment")
	}
	for _, candidate := range dep.HostIDs {
		if strings.TrimSpace(candidate) == hostID {
			scoped := dep
			scoped.HostIDs = []string{hostID}
			return scoped, nil
		}
	}
	return model.Deployment{}, fmt.Errorf("host %s is not configured for deployment %s", hostID, dep.ID)
}

// emitControlEvent 发出一条控制指令进度事件，归属到 deploymentID。
//
// phase 取值：command_received / reconciling / executing / succeeded / failed。
func (a *App) emitControlEvent(depID, action, phase, detail string) {
	msg := fmt.Sprintf("[控制] %s · %s", action, phase)
	if detail != "" {
		msg += "：" + detail
	}
	a.buf.Append(model.LogEntry{
		DeploymentID: depID,
		Timestamp:    time.Now().UTC(),
		Level:        "INFO",
		Message:      msg,
		Stream:       "control",
	})
}

func (a *App) startDeploymentRuntime(ctx context.Context, projectID string, dep model.Deployment, intent startIntent) error {
	a.emitControlEvent(dep.ID, "start", "command_received", "")
	a.emitControlEvent(dep.ID, "start", "reconciling", "")
	if intent == intentDebugLaunch {
		_, svc, project, ok := a.findDeploymentWithService(dep.ID)
		if !ok {
			return fmt.Errorf("deployment %s not found", dep.ID)
		}
		if reason := a.codeDebugStartDeniedReason(project, svc, dep); reason != "" {
			a.emitControlEvent(dep.ID, "start", "failed", reason)
			return &debugStartDeniedError{Reason: reason}
		}
		a.emitControlEvent(dep.ID, "start", "executing", "debug runtime")
		if _, err := a.codeDebug.StartRuntime(ctx, project, svc, dep, codedebug.OpenRequest{DeploymentID: dep.ID}); err != nil {
			a.emitControlEvent(dep.ID, "start", "failed", err.Error())
			return err
		}
		a.pidStore.Set(dep.ID, a.codeDebugRuntimePID(dep.ID))
		if err := a.pidStore.Flush(); err != nil {
			a.emitControlEvent(dep.ID, "start", "failed", err.Error())
			return err
		}
		a.emitControlEvent(dep.ID, "start", "succeeded", "debug runtime")
		return nil
	}
	if dep.Location == model.LocationRemote {
		a.emitControlEvent(dep.ID, "start", "executing", "remote")
		if err := a.newRemoteRuntimeController().Start(ctx, dep); err != nil {
			a.emitControlEvent(dep.ID, "start", "failed", err.Error())
			return err
		}
		a.emitControlEvent(dep.ID, "start", "succeeded", "")
		return nil
	}
	a.reconcileLocalDeployment(projectID, dep.ID)
	mgr := a.getOrCreateManager(projectID)
	a.emitControlEvent(dep.ID, "start", "executing", "")
	if dep.Runtime != nil && dep.Runtime.Type == model.RuntimeTypeLanguage {
		_, svc, project, ok := a.findDeploymentWithService(dep.ID)
		if !ok {
			err := fmt.Errorf("deployment %s not found", dep.ID)
			a.emitControlEvent(dep.ID, "start", "failed", err.Error())
			return err
		}
		spec, err := a.languageRuntimeProcessSpec(project, svc, dep, intent)
		if err != nil {
			a.emitControlEvent(dep.ID, "start", "failed", err.Error())
			return err
		}
		if err := mgr.StartDeploymentSpec(dep, spec); err != nil {
			a.emitControlEvent(dep.ID, "start", "failed", err.Error())
			return err
		}
	} else if err := mgr.StartDeployment(dep); err != nil {
		a.emitControlEvent(dep.ID, "start", "failed", err.Error())
		return err
	}
	a.pidStore.Set(dep.ID, mgr.DeploymentPID(dep.ID))
	if err := a.pidStore.Flush(); err != nil {
		a.emitControlEvent(dep.ID, "start", "failed", err.Error())
		return err
	}
	a.emitControlEvent(dep.ID, "start", "succeeded", "")
	return nil
}

// codeDebugStartDeniedReason 返回该 deployment 不可进入 debug 启动的原因，空表示可以。
func (a *App) codeDebugStartDeniedReason(project model.Project, svc model.Service, dep model.Deployment) string {
	plan, err := operation.PlanCodeDebugOpen(project, svc, dep, svc.Language)
	if err != nil {
		return "invalid debug target"
	}
	if plan.Denied {
		if len(plan.Reasons) > 0 {
			return plan.Reasons[0]
		}
		return "debug not allowed for this deployment"
	}
	return ""
}

func (a *App) stopDeploymentRuntime(ctx context.Context, projectID string, dep model.Deployment) error {
	a.emitControlEvent(dep.ID, "stop", "command_received", "")
	a.emitControlEvent(dep.ID, "stop", "reconciling", "")
	if dep.Location == model.LocationRemote {
		a.emitControlEvent(dep.ID, "stop", "executing", "remote")
		if err := a.newRemoteRuntimeController().Stop(ctx, dep); err != nil {
			a.emitControlEvent(dep.ID, "stop", "failed", err.Error())
			return err
		}
		a.pidStore.Remove(dep.ID)
		if err := a.pidStore.Flush(); err != nil {
			a.emitControlEvent(dep.ID, "stop", "failed", err.Error())
			return err
		}
		a.emitControlEvent(dep.ID, "stop", "succeeded", "")
		return nil
	}
	if runtime, ok := a.codeDebug.RuntimeStatus(dep.ID); ok && runtime.Alive {
		a.emitControlEvent(dep.ID, "stop", "executing", "debug runtime")
		if err := a.codeDebug.StopRuntime(dep.ID); err != nil {
			a.emitControlEvent(dep.ID, "stop", "failed", err.Error())
			return err
		}
		a.pidStore.Remove(dep.ID)
		if err := a.pidStore.Flush(); err != nil {
			a.emitControlEvent(dep.ID, "stop", "failed", err.Error())
			return err
		}
		a.emitControlEvent(dep.ID, "stop", "succeeded", "debug runtime")
		return nil
	}
	a.reconcileLocalDeployment(projectID, dep.ID)
	mgr := a.getOrCreateManager(projectID)
	a.emitControlEvent(dep.ID, "stop", "executing", "")
	mgr.StopDeployment(dep.ID)
	a.pidStore.Remove(dep.ID)
	if err := a.pidStore.Flush(); err != nil {
		a.emitControlEvent(dep.ID, "stop", "failed", err.Error())
		return err
	}
	a.emitControlEvent(dep.ID, "stop", "succeeded", "")
	return nil
}

func (a *App) restartDeploymentRuntime(ctx context.Context, projectID string, dep model.Deployment, intent startIntent) error {
	a.emitControlEvent(dep.ID, "restart", "command_received", "")
	a.emitControlEvent(dep.ID, "restart", "reconciling", "")
	if intent == intentDebugLaunch {
		_, svc, project, ok := a.findDeploymentWithService(dep.ID)
		if !ok {
			return fmt.Errorf("deployment %s not found", dep.ID)
		}
		if reason := a.codeDebugStartDeniedReason(project, svc, dep); reason != "" {
			a.emitControlEvent(dep.ID, "restart", "failed", reason)
			return &debugStartDeniedError{Reason: reason}
		}
		if err := a.stopDeploymentRuntime(ctx, projectID, dep); err != nil {
			a.emitControlEvent(dep.ID, "restart", "failed", err.Error())
			return err
		}
		if err := a.startDeploymentRuntime(ctx, projectID, dep, intentDebugLaunch); err != nil {
			a.emitControlEvent(dep.ID, "restart", "failed", err.Error())
			return err
		}
		a.emitControlEvent(dep.ID, "restart", "succeeded", "debug runtime")
		return nil
	}
	if dep.Location == model.LocationRemote {
		a.emitControlEvent(dep.ID, "restart", "executing", "remote")
		if err := a.newRemoteRuntimeController().Restart(ctx, dep); err != nil {
			a.emitControlEvent(dep.ID, "restart", "failed", err.Error())
			return err
		}
		a.emitControlEvent(dep.ID, "restart", "succeeded", "")
		return nil
	}
	a.reconcileLocalDeployment(projectID, dep.ID)
	mgr := a.getOrCreateManager(projectID)
	a.emitControlEvent(dep.ID, "restart", "executing", "")
	if dep.Runtime != nil && dep.Runtime.Type == model.RuntimeTypeLanguage {
		_, svc, project, ok := a.findDeploymentWithService(dep.ID)
		if !ok {
			err := fmt.Errorf("deployment %s not found", dep.ID)
			a.emitControlEvent(dep.ID, "restart", "failed", err.Error())
			return err
		}
		spec, err := a.languageRuntimeProcessSpec(project, svc, dep, intent)
		if err != nil {
			a.emitControlEvent(dep.ID, "restart", "failed", err.Error())
			return err
		}
		mgr.StopDeployment(dep.ID)
		if err := mgr.StartDeploymentSpec(dep, spec); err != nil {
			a.emitControlEvent(dep.ID, "restart", "failed", err.Error())
			return err
		}
	} else if err := mgr.RestartDeployment(dep); err != nil {
		a.emitControlEvent(dep.ID, "restart", "failed", err.Error())
		return err
	}
	a.pidStore.Set(dep.ID, mgr.DeploymentPID(dep.ID))
	if err := a.pidStore.Flush(); err != nil {
		a.emitControlEvent(dep.ID, "restart", "failed", err.Error())
		return err
	}
	a.emitControlEvent(dep.ID, "restart", "succeeded", "")
	return nil
}

// languageRuntimeProcessSpec 由语言 provider 的 start plan 构造进程 spec。
//
// start_dev / start_normal 的产物目录由 App 注入到 agent 数据目录；
// intent 仍透传给 provider，为 prearm 语言（Phase C）保留分叉点。
func (a *App) languageRuntimeProcessSpec(project model.Project, svc model.Service, dep model.Deployment, intent startIntent) (process.ProcessSpec, error) {
	provider, ok := langruntime.Core().Provider(svc.Language)
	if !ok {
		return process.ProcessSpec{}, fmt.Errorf("no language runtime provider for %q", svc.Language)
	}
	buildIntent := langruntime.IntentStartDev
	if intent == intentStartNormal {
		buildIntent = langruntime.IntentStartNormal
	}
	ctx := context.Background()
	normalized, diagnostics, err := provider.Normalize(ctx, langruntime.RuntimeConfigInput{
		ProjectRoot: project.RootPath,
		CWD:         dep.Runtime.EffectiveCWD(),
		Env:         dep.Runtime.EffectiveEnv(),
		Config:      dep.Runtime.Config,
	})
	if err != nil {
		return process.ProcessSpec{}, err
	}
	if langruntime.HasErrorDiagnostic(diagnostics) {
		return process.ProcessSpec{}, fmt.Errorf("invalid language runtime config: %s", diagnostics[0].Message)
	}
	artifactDir := filepath.Join(a.cfg.DataDir, "run-bin", dep.ID)
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return process.ProcessSpec{}, fmt.Errorf("prepare artifact dir: %w", err)
	}
	buildInput := langruntime.BuildPlanInput{
		Intent:      buildIntent,
		Config:      normalized,
		ArtifactDir: artifactDir,
	}
	// prearm-listen 语言（Python）start_dev 需要空闲调试端口预埋 debugpy --listen。
	if provider.Capabilities().DebugReady == langruntime.DebugReadyByPrearm {
		port, err := codedebug.AllocateFreePort()
		if err != nil {
			return process.ProcessSpec{}, fmt.Errorf("allocate debug port: %w", err)
		}
		buildInput.DebugPort = port
	}
	plan, diagnostics, err := provider.BuildPlan(ctx, buildInput)
	if err != nil {
		return process.ProcessSpec{}, err
	}
	if langruntime.HasErrorDiagnostic(diagnostics) || plan.Command == nil {
		return process.ProcessSpec{}, fmt.Errorf("language runtime start plan unavailable")
	}
	spec := process.ProcessSpec{
		Argv:    append([]string{plan.Command.Executable}, plan.Command.Args...),
		WorkDir: plan.WorkingDir,
		Env:     plan.Env,
	}
	if plan.Command.PreRun != nil {
		spec.PreRun = &process.CommandStep{
			Argv: append([]string{plan.Command.PreRun.Executable}, plan.Command.PreRun.Args...),
		}
	}
	return spec, nil
}

func (a *App) codeDebugRuntimePID(depID string) int {
	runtime, ok := a.codeDebug.RuntimeStatus(depID)
	if !ok {
		return 0
	}
	return runtime.ProcessID
}

// findDeployment 在所有项目的所有服务中按 deployment ID 查找。
//
// 注意：调用方无需持锁，此函数内部持有 RLock。
func (a *App) findDeployment(depID string) (model.Deployment, model.Project, bool) {
	dep, _, project, ok := a.findDeploymentWithService(depID)
	return dep, project, ok
}

func (a *App) findDeploymentWithService(depID string) (model.Deployment, model.Service, model.Project, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, p := range a.projects {
		for _, svc := range p.Services {
			for _, dep := range svc.Deployments {
				if dep.ID == depID {
					return dep, svc, p, true
				}
			}
		}
	}
	return model.Deployment{}, model.Service{}, model.Project{}, false
}
