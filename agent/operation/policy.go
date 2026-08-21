// 本文件负责 MCP 写操作的安全策略规划。
//
// 职责：
//   - 根据项目、环境、服务和 deployment 生成可解释的 operation plan
//   - 为模板导入生成绑定 digest 的审批计划
//
// 边界：
//   - 不执行运行态变更
//   - 不读写模板文件或审批存储
package operation

import (
	"fmt"
	"sort"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

// PlanTestDatabaseTerminate 为临时库克隆前的开发库断连生成中风险计划。
//
// 参数：
//   - projectID: 发生断连的项目 ID
//   - template: 将被断连并克隆的开发库名
//   - count: 当前检测到的活跃连接数
//   - detail: 已脱敏的占用者摘要
//
// 返回：
//   - 始终要求审批、风险级别为 medium 的 operation plan
//
// 注意：
//   - 该函数只描述断连副作用，不执行任何数据库操作。
func PlanTestDatabaseTerminate(projectID, template string, count int, detail string) Plan {
	now := time.Now().UTC()
	summary := fmt.Sprintf("断开开发库 %s 上的 %d 个活跃连接以克隆临时库", template, count)
	plan := Plan{
		ID:               newID("op"),
		Kind:             OperationTestDatabaseTerminate,
		Target:           Target{ProjectID: projectID},
		TargetSummary:    summary,
		RiskLevel:        RiskMedium,
		RequiresApproval: true,
		ExpectedEffects:  []string{summary},
		CreatedAt:        now,
		ExpiresAt:        now.Add(DefaultPlanTTL),
	}
	if detail != "" {
		plan.Checks = []Check{{Name: "active_connections", Status: "warning", Message: detail}}
	}
	plan.Fingerprint = stableFingerprint(map[string]any{
		"kind":     plan.Kind,
		"project":  projectID,
		"template": template,
		"count":    count,
		"detail":   detail,
	})
	return plan
}

// RuntimeDeploymentTarget 描述批量运行态操作中的单个 deployment 目标。
//
// 参数：
//   - Service: deployment 所属服务，用于生成可读目标摘要
//   - Deployment: 将要被操作的 deployment
//
// 注意：
//   - 调用方应先完成“哪些服务被选中、哪些已运行”的业务过滤
//   - 本类型只承载安全预检所需的稳定目标信息
type RuntimeDeploymentTarget struct {
	Service    model.Service
	Deployment model.Deployment
}

// PlanTunnelInvalidation 为持久化连接目标变更生成审计专用计划。
//
// 参数：
//   - req: host、触发来源和不含秘密的变更字段名
//
// 返回：
//   - 无需审批、只减少访问面的 tunnel 失效计划
//   - host、trigger 或 changed fields 为空时返回 ErrInvalidOperation
//
// 注意：
//   - 调用方必须先持久化 prepared 审计，再提交配置并执行对应的运行态失效
//   - 本计划不能复用于用户主动建立或断开 tunnel
func PlanTunnelInvalidation(req TunnelInvalidationRequest) (Plan, error) {
	hostID := trim(req.HostID)
	trigger := trim(req.Trigger)
	changedFields := make([]string, 0, len(req.ChangedFields))
	seen := make(map[string]struct{}, len(req.ChangedFields))
	for _, field := range req.ChangedFields {
		field = trim(field)
		if field == "" {
			continue
		}
		if _, exists := seen[field]; exists {
			continue
		}
		seen[field] = struct{}{}
		changedFields = append(changedFields, field)
	}
	if hostID == "" || trigger == "" || len(changedFields) == 0 {
		return Plan{}, ErrInvalidOperation
	}
	sort.Strings(changedFields)
	now := time.Now().UTC()
	plan := Plan{
		ID:               newID("op"),
		Kind:             OperationTunnelInvalidate,
		Target:           Target{HostID: hostID},
		TargetSummary:    fmt.Sprintf("stale tunnel runtime on host %s", hostID),
		RiskLevel:        RiskLow,
		RequiresApproval: false,
		ExpectedEffects: []string{
			"disconnect stale tunnel runtime and clear cached host-key evidence",
		},
		Checks: []Check{
			{Name: "prepared_audit_required", Status: "passed", Message: "connection target mutation is gated on durable prepared audit"},
		},
		CreatedAt: now,
		ExpiresAt: now.Add(DefaultPlanTTL),
	}
	plan.Fingerprint = stableFingerprint(map[string]any{
		"kind":           plan.Kind,
		"target":         plan.Target,
		"trigger":        trigger,
		"changed_fields": changedFields,
	})
	return plan, nil
}

// PlanRuntime 为 deployment 运行态写操作生成安全预检计划。
//
// 参数：
//   - kind: runtime.start/runtime.stop/runtime.restart
//   - project: deployment 所属项目
//   - service: deployment 所属服务
//   - dep: 解析后的 deployment
//
// 返回：
//   - 可用于审批和执行授权的稳定 plan
//   - 操作类型非法时返回错误
//
// 注意：
//   - 此函数只做策略判断，不启动或停止进程
func PlanRuntime(kind string, project model.Project, service model.Service, dep model.Deployment) (Plan, error) {
	if kind != OperationRuntimeStart && kind != OperationRuntimeStop && kind != OperationRuntimeRestart {
		return Plan{}, ErrInvalidOperation
	}

	now := time.Now().UTC()
	env, isDev := findEnvironment(project, dep.EnvName)
	location := effectiveDeployLocation(dep)
	effectLocation := "local"
	if location == model.LocationRemote {
		effectLocation = "remote"
	}
	target := Target{
		ProjectID:    project.ID,
		ProjectName:  project.Name,
		EnvName:      dep.EnvName,
		ServiceID:    service.ID,
		ServiceName:  service.Name,
		DeploymentID: dep.ID,
	}
	plan := Plan{
		ID:            newID("op"),
		Kind:          kind,
		Target:        target,
		TargetSummary: fmt.Sprintf("%s/%s/%s", project.Name, dep.EnvName, service.Name),
		RiskLevel:     RiskLow,
		CreatedAt:     now,
		ExpiresAt:     now.Add(DefaultPlanTTL),
		ExpectedEffects: []string{
			fmt.Sprintf("%s %s deployment %s", runtimeVerb(kind), effectLocation, dep.ID),
		},
		Checks: []Check{
			{Name: "target_resolved", Status: "passed", Message: "deployment target resolved by agent"},
		},
	}

	if env.Name == "" {
		plan.RiskLevel = RiskHigh
		plan.RequiresApproval = true
		plan.Reasons = append(plan.Reasons, "environment definition not found")
	}
	if dep.IsReadOnly() {
		plan.Denied = true
		plan.RiskLevel = RiskCritical
		plan.Reasons = append(plan.Reasons, "deployment is read-only")
	}
	if location == model.LocationRemote && !plan.Denied {
		plan.RiskLevel = RiskHigh
		plan.RequiresApproval = true
		plan.Reasons = append(plan.Reasons, "remote deployment control requires approval")
	}
	if location == model.LocationLocal && !isDev && !plan.Denied {
		plan.RiskLevel = RiskHigh
		plan.RequiresApproval = true
		plan.Reasons = append(plan.Reasons, "environment is not marked as dev")
	}
	if location == model.LocationLocal && isDev && !plan.Denied {
		plan.RiskLevel = RiskLow
		plan.RequiresApproval = false
	}

	// fingerprint 只绑定稳定目标和策略结果，避免时间戳或随机 plan ID 导致审批不可复用。
	plan.Fingerprint = stableFingerprint(map[string]any{
		"kind":             plan.Kind,
		"target":           plan.Target,
		"expected_effects": plan.ExpectedEffects,
		"denied":           plan.Denied,
	})
	return plan, nil
}

// PlanRuntimeOnHost 为远端 deployment 的单 host 运行态写操作生成安全预检计划。
//
// 参数：
//   - kind: runtime.start/runtime.stop/runtime.restart
//   - project: deployment 所属项目
//   - service: deployment 所属服务
//   - dep: 解析后的 remote deployment
//   - hostID: 本次操作限定的远端 host ID
//
// 返回：
//   - 绑定 host_id 的稳定 plan
//   - 非远端 deployment、空 hostID 或非法操作类型返回错误
//
// 注意：
//   - 此函数只做策略判断，不验证 hostID 是否属于 dep.HostIDs；调用方在解析目标时负责。
func PlanRuntimeOnHost(kind string, project model.Project, service model.Service, dep model.Deployment, hostID string) (Plan, error) {
	hostID = trim(hostID)
	if hostID == "" || effectiveDeployLocation(dep) != model.LocationRemote {
		return Plan{}, ErrInvalidOperation
	}
	plan, err := PlanRuntime(kind, project, service, dep)
	if err != nil {
		return Plan{}, err
	}
	plan.Target.HostID = hostID
	plan.TargetSummary = fmt.Sprintf("%s/%s/%s on %s", project.Name, dep.EnvName, service.Name, hostID)
	plan.ExpectedEffects = []string{
		fmt.Sprintf("%s remote deployment %s on host %s", runtimeVerb(kind), dep.ID, hostID),
	}
	plan.Fingerprint = stableFingerprint(map[string]any{
		"kind":             plan.Kind,
		"target":           plan.Target,
		"expected_effects": plan.ExpectedEffects,
		"denied":           plan.Denied,
	})
	return plan, nil
}

// PlanRuntimeStartSelected 为项目环境下的“启动所选”批量操作生成安全预检计划。
//
// 参数：
//   - project: deployment 所属项目
//   - envName: 被操作的环境名称
//   - targets: 已解析且需要启动的 deployment 目标列表
//
// 返回：
//   - 可用于审批和执行授权的稳定 plan
//   - envName 为空或 targets 为空时返回错误
//
// 注意：
//   - 此函数不判断服务是否被用户选中，只验证批量目标的安全边界
//   - fingerprint 会绑定 deployment ID 列表，避免 token 被换目标复用
func PlanRuntimeStartSelected(project model.Project, envName string, targets []RuntimeDeploymentTarget) (Plan, error) {
	envName = trim(envName)
	if envName == "" || len(targets) == 0 {
		return Plan{}, ErrInvalidOperation
	}

	now := time.Now().UTC()
	env, isDev := findEnvironment(project, envName)
	target := Target{
		ProjectID:   project.ID,
		ProjectName: project.Name,
		EnvName:     envName,
	}
	plan := Plan{
		ID:               newID("op"),
		Kind:             OperationRuntimeStartSelected,
		Target:           target,
		TargetSummary:    fmt.Sprintf("%s/%s selected services", project.Name, envName),
		RiskLevel:        RiskLow,
		RequiresApproval: false,
		CreatedAt:        now,
		ExpiresAt:        now.Add(DefaultPlanTTL),
		Checks: []Check{
			{Name: "target_resolved", Status: "passed", Message: fmt.Sprintf("%d deployment target(s) resolved by agent", len(targets))},
		},
	}

	deploymentIDs := make([]string, 0, len(targets))
	effects := make([]string, 0, len(targets))
	hasRemoteTarget := false
	for _, item := range targets {
		dep := item.Deployment
		location := effectiveDeployLocation(dep)
		effectLocation := "local"
		if location == model.LocationRemote {
			effectLocation = "remote"
			hasRemoteTarget = true
		}
		deploymentIDs = append(deploymentIDs, dep.ID)
		effects = append(effects, fmt.Sprintf("start %s deployment %s", effectLocation, dep.ID))
		if dep.EnvName != envName {
			plan.Denied = true
			plan.RiskLevel = RiskCritical
			plan.Reasons = append(plan.Reasons, fmt.Sprintf("deployment %s is not in environment %s", dep.ID, envName))
		}
		if dep.IsReadOnly() {
			plan.Denied = true
			plan.RiskLevel = RiskCritical
			plan.Reasons = append(plan.Reasons, fmt.Sprintf("deployment %s is read-only", dep.ID))
		}
	}
	sort.Strings(deploymentIDs)
	sort.Strings(effects)
	plan.ExpectedEffects = effects

	if env.Name == "" {
		plan.RiskLevel = RiskHigh
		plan.RequiresApproval = true
		plan.Reasons = append(plan.Reasons, "environment definition not found")
	}
	if !plan.Denied && hasRemoteTarget {
		plan.RiskLevel = RiskHigh
		plan.RequiresApproval = true
		plan.Reasons = append(plan.Reasons, "remote deployment control requires approval")
	}
	if !plan.Denied && !isDev {
		plan.RiskLevel = RiskHigh
		plan.RequiresApproval = true
		plan.Reasons = append(plan.Reasons, "environment is not marked as dev")
	}
	if !plan.Denied && isDev && !hasRemoteTarget {
		plan.RiskLevel = RiskLow
		plan.RequiresApproval = false
	}

	plan.Fingerprint = stableFingerprint(map[string]any{
		"kind":             plan.Kind,
		"target":           plan.Target,
		"deployment_ids":   deploymentIDs,
		"expected_effects": plan.ExpectedEffects,
		"denied":           plan.Denied,
	})
	return plan, nil
}

func effectiveDeployLocation(dep model.Deployment) model.DeployLocation {
	if dep.Location == "" {
		return model.LocationLocal
	}
	return dep.Location
}

// PlanBrowserDebugOpen 为本机前端浏览器调试会话生成安全预检计划。
//
// 参数：
//   - project: deployment 所属项目
//   - service: deployment 所属服务
//   - dep: 解析后的 deployment
//   - targetURL: 即将被调试浏览器打开的 loopback URL
//
// 返回：
//   - 需要审批的浏览器调试 plan
//   - deployment ID 或目标 URL 缺失时返回错误
//
// 注意：
//   - 此函数只规划安全策略，不启动浏览器进程
//   - v1 仅支持本机 deployment，远端目标会被标记为 denied
func PlanBrowserDebugOpen(project model.Project, service model.Service, dep model.Deployment, targetURL string) (Plan, error) {
	targetURL = trim(targetURL)
	if dep.ID == "" || targetURL == "" {
		return Plan{}, ErrInvalidOperation
	}
	now := time.Now().UTC()
	plan := Plan{
		ID:   newID("op"),
		Kind: OperationBrowserDebugOpen,
		Target: Target{
			ProjectID:    project.ID,
			ProjectName:  project.Name,
			EnvName:      dep.EnvName,
			ServiceID:    service.ID,
			ServiceName:  service.Name,
			DeploymentID: dep.ID,
		},
		TargetSummary:    fmt.Sprintf("%s/%s/%s", project.Name, dep.EnvName, service.Name),
		RiskLevel:        RiskMedium,
		RequiresApproval: true,
		ExpectedEffects:  []string{fmt.Sprintf("open debug browser for %s", targetURL)},
		Checks:           []Check{{Name: "target_resolved", Status: "passed", Message: "local frontend target resolved by agent"}},
		CreatedAt:        now,
		ExpiresAt:        now.Add(DefaultPlanTTL),
	}
	if effectiveDeployLocation(dep) != model.LocationLocal {
		plan.Denied = true
		plan.RiskLevel = RiskCritical
		plan.Reasons = append(plan.Reasons, "browser debug v1 supports local deployments only")
	}
	plan.Fingerprint = stableFingerprint(map[string]any{
		"kind":             plan.Kind,
		"target":           plan.Target,
		"target_url":       targetURL,
		"expected_effects": plan.ExpectedEffects,
		"denied":           plan.Denied,
	})
	return plan, nil
}

// PlanCodeDebugOpen 为本机代码调试会话生成安全预检计划。
//
// 参数：
//   - project: deployment 所属项目
//   - service: deployment 所属服务
//   - dep: 解析后的 deployment
//   - lang: 服务主要实现语言，用于推导调试能力
//
// 返回：
//   - 需要审批的代码调试 plan
//   - deployment ID 缺失时返回错误
//
// 注意：
//   - 此函数只规划安全策略，不启动 adapter 或目标进程
//   - v1 仅支持本机 managed language runtime deployment，policy/语言/环境共同决定是否放行
func PlanCodeDebugOpen(project model.Project, service model.Service, dep model.Deployment, lang model.ServiceLanguage) (Plan, error) {
	if dep.ID == "" {
		return Plan{}, ErrInvalidOperation
	}
	effectProvider := string(lang)
	if effectProvider == "" {
		effectProvider = "configured"
	}
	now := time.Now().UTC()
	plan := Plan{
		ID:   newID("op"),
		Kind: OperationCodeDebugOpen,
		Target: Target{
			ProjectID:    project.ID,
			ProjectName:  project.Name,
			EnvName:      dep.EnvName,
			ServiceID:    service.ID,
			ServiceName:  service.Name,
			DeploymentID: dep.ID,
		},
		TargetSummary:    fmt.Sprintf("%s/%s/%s", project.Name, dep.EnvName, service.Name),
		RiskLevel:        RiskMedium,
		RequiresApproval: true,
		ExpectedEffects:  []string{fmt.Sprintf("launch %s code debug session for deployment %s", effectProvider, dep.ID)},
		Checks:           []Check{{Name: "target_resolved", Status: "passed", Message: "local code debug target resolved by agent"}},
		CreatedAt:        now,
		ExpiresAt:        now.Add(DefaultPlanTTL),
	}
	if reason := codeDebugDeniedReason(project, dep, lang); reason != "" {
		plan.Denied = true
		plan.RiskLevel = RiskCritical
		plan.Reasons = append(plan.Reasons, reason)
	}
	plan.Fingerprint = stableFingerprint(map[string]any{
		"kind":             plan.Kind,
		"target":           plan.Target,
		"language":         string(lang),
		"expected_effects": plan.ExpectedEffects,
		"denied":           plan.Denied,
	})
	return plan, nil
}

// PlanCodeDebugEvaluate 为代码调试 evaluate 生成安全预检计划。
//
// 参数：
//   - req: 调试 session、deployment 和表达式 hash
//
// 返回：
//   - 需要审批的 evaluate plan
//   - session ID 或表达式 hash 缺失时返回错误
//
// 注意：
//   - expression 明文永远不进入 plan、fingerprint 或审计目标
func PlanCodeDebugEvaluate(req CodeDebugEvaluateRequest) (Plan, error) {
	req.ProjectID = trim(req.ProjectID)
	req.ProjectName = trim(req.ProjectName)
	req.DeploymentID = trim(req.DeploymentID)
	req.DebugSessionID = trim(req.DebugSessionID)
	req.ExpressionHash = trim(req.ExpressionHash)
	if req.DebugSessionID == "" || req.ExpressionHash == "" {
		return Plan{}, ErrInvalidOperation
	}
	now := time.Now().UTC()
	plan := Plan{
		ID:   newID("op"),
		Kind: OperationCodeDebugEvaluate,
		Target: Target{
			ProjectID:      req.ProjectID,
			ProjectName:    req.ProjectName,
			DeploymentID:   req.DeploymentID,
			DebugSessionID: req.DebugSessionID,
		},
		TargetSummary:    fmt.Sprintf("code debug session %s", req.DebugSessionID),
		RiskLevel:        RiskHigh,
		RequiresApproval: true,
		ExpectedEffects:  []string{fmt.Sprintf("evaluate expression in code debug session %s", req.DebugSessionID)},
		Checks:           []Check{{Name: "expression_hashed", Status: "passed", Message: "evaluate expression is represented by hash only"}},
		CreatedAt:        now,
		ExpiresAt:        now.Add(DefaultPlanTTL),
	}
	plan.Fingerprint = stableFingerprint(map[string]any{
		"kind":             plan.Kind,
		"target":           plan.Target,
		"expression_hash":  req.ExpressionHash,
		"expected_effects": plan.ExpectedEffects,
	})
	return plan, nil
}

func codeDebugRuntimeType(dep model.Deployment) model.RuntimeType {
	if dep.Runtime != nil && dep.Runtime.Type != "" {
		return dep.Runtime.Type
	}
	return model.RuntimeTypeCommand
}

// codeDebugDeniedReason 返回拒绝代码调试的原因，空表示放行。
// 与 codedebug 包 can_open 判定保持同语义：形态 + 语言 + policy/dev。
func codeDebugDeniedReason(project model.Project, dep model.Deployment, lang model.ServiceLanguage) string {
	if effectiveDeployLocation(dep) != model.LocationLocal ||
		dep.EffectiveControlMode() != model.ControlModeManaged ||
		!codeDebugRuntimeSupported(dep) {
		return "code debug supports local managed language runtime deployments only"
	}
	policy := model.CodeDebugPolicyAuto
	if dep.CodeDebug != nil {
		policy = dep.CodeDebug.Policy.Effective()
	}
	if policy == model.CodeDebugPolicyDisabled {
		return "code debug is disabled for this deployment"
	}
	if !lang.Known() {
		return "service language does not support code debug"
	}
	if policy == model.CodeDebugPolicyEnabled {
		return ""
	}
	if !envIsDev(project, dep.EnvName) {
		return "code debug is only available in dev environments by default"
	}
	return ""
}

func codeDebugRuntimeSupported(dep model.Deployment) bool {
	return codeDebugRuntimeType(dep) == model.RuntimeTypeLanguage
}

func envIsDev(project model.Project, envName string) bool {
	for _, e := range project.Environments {
		if e.Name == envName {
			return e.IsDev
		}
	}
	return false
}

// PlanTemplateImport 为用户模板导入生成安全预检计划。
//
// 参数：
//   - req: preview 通过后的模板路径、digest 和摘要
//
// 返回：
//   - 需要用户审批的 template.import plan
//   - 缺少 path 或 digest 时返回错误
//
// 注意：
//   - 模板 YAML preview 必须由调用方先完成
func PlanTemplateImport(req TemplateImportRequest) (Plan, error) {
	req.Path = trim(req.Path)
	req.Digest = trim(req.Digest)
	if req.Path == "" || req.Digest == "" {
		return Plan{}, ErrInvalidOperation
	}

	now := time.Now().UTC()
	summary := req.Summary
	if summary.Digest == "" {
		summary.Digest = req.Digest
	}
	plan := Plan{
		ID:               newID("op"),
		Kind:             OperationTemplateImport,
		Target:           Target{TemplatePath: req.Path, TemplateDigest: req.Digest},
		TargetSummary:    fmt.Sprintf("%s@%s from %s", summary.ID, summary.Version, req.Path),
		RiskLevel:        RiskMedium,
		RequiresApproval: true,
		CreatedAt:        now,
		ExpiresAt:        now.Add(DefaultPlanTTL),
		ExpectedEffects: []string{
			fmt.Sprintf("import user pipeline template %s@%s", summary.ID, summary.Version),
		},
		Checks: []Check{
			{Name: "template_preview", Status: "passed", Message: "template preview succeeded before import"},
		},
	}
	plan.Fingerprint = stableFingerprint(map[string]any{
		"kind":             plan.Kind,
		"target":           plan.Target,
		"expected_effects": plan.ExpectedEffects,
	})
	return plan, nil
}

// PlanPipelineRun 为运行项目级流水线（部署或回滚）生成安全预检计划。
//
// 参数：
//   - project: 流水线所属项目
//   - pipelineID: 流水线 ID
//   - envName: 运行环境名
//   - isRollback: 是否为回滚（影响 fingerprint 与摘要）
//   - artifactVersion: 回滚目标制品版本，部署时为空
//
// 返回：
//   - 基准要求审批的 plan；pipelineID 为空时返回错误
//
// 注意：
//   - 基准不区分环境，是否真正审批由 API 层按 settings 开关覆盖
//   - Target 带 ProjectID，使项目级豁免窗口可命中
func PlanPipelineRun(project model.Project, pipelineID, envName string, isRollback bool, artifactVersion string) (Plan, error) {
	pipelineID = trim(pipelineID)
	if pipelineID == "" {
		return Plan{}, ErrInvalidOperation
	}
	now := time.Now().UTC()
	verb := "deploy"
	if isRollback {
		verb = "rollback"
	}
	target := Target{
		ProjectID:       project.ID,
		ProjectName:     project.Name,
		EnvName:         trim(envName),
		PipelineID:      pipelineID,
		ArtifactVersion: trim(artifactVersion),
	}
	plan := Plan{
		ID:               newID("op"),
		Kind:             OperationPipelineRun,
		Target:           target,
		TargetSummary:    fmt.Sprintf("%s/%s pipeline %s (%s)", project.Name, trim(envName), pipelineID, verb),
		RiskLevel:        RiskHigh,
		RequiresApproval: true,
		CreatedAt:        now,
		ExpiresAt:        now.Add(DefaultPlanTTL),
		ExpectedEffects: []string{
			fmt.Sprintf("%s pipeline %s in %s", verb, pipelineID, trim(envName)),
		},
		Checks: []Check{
			{Name: "target_resolved", Status: "passed", Message: "pipeline target resolved by agent"},
		},
	}
	plan.Fingerprint = stableFingerprint(map[string]any{
		"kind":             plan.Kind,
		"target":           plan.Target,
		"expected_effects": plan.ExpectedEffects,
		"is_rollback":      isRollback,
		"artifact_version": trim(artifactVersion),
	})
	return plan, nil
}

// PlanAgentAdopt 为无凭据接入请求生成安全预检计划。
//
// 参数：
//   - requestID: security.AdoptionManager 生成的接入请求 ID
//   - name: 接入方（新控制面）**自报**的展示名，空则显示为「控制面」
//   - origin: 服务器侧推导的请求来源（调用方从 http.Request.RemoteAddr 取），空则显示为 unknown
//   - pairingCode: 由 requestID 派生的配对码（security.PairingCode）
//
// 返回：
//   - 恒为 RiskHigh 且必须审批的 agent.adopt plan
//   - requestID 为空时返回 ErrInvalidOperation
//
// 注意：
//   - Fingerprint 直接取 requestID，而非常规的 stableFingerprint 哈希——
//     纳管场景下 approve/reject 是通过 approval.Plan.Fingerprint 反查
//     AdoptionManager 中同一个请求的驱动键，用请求 ID 本身最直接、无需
//     额外维护一张 fingerprint→请求 ID 的映射表
//   - **摘要里 origin/配对码在前、自报名在后且显式标注 self-reported**：自报名
//     完全由请求方控制，攻击者可以原样填成真实桌面用的那个名字，把自己的请求
//     伪装成操作员正在等的那一条。操作员真正该核对的是服务器侧推导的来源和
//     发起方念出来的配对码，自报名只是补充上下文
//   - 本函数只规划安全策略，不接触 security.Store，也不生成任何 token
func PlanAgentAdopt(requestID, name, origin, pairingCode string) (Plan, error) {
	requestID = trim(requestID)
	if requestID == "" {
		return Plan{}, ErrInvalidOperation
	}
	name = trim(name)
	if name == "" {
		name = "控制面"
	}
	origin = trim(origin)
	if origin == "" {
		origin = "unknown"
	}
	pairingCode = trim(pairingCode)
	now := time.Now().UTC()
	plan := Plan{
		ID:   newID("op"),
		Kind: KindAgentAdopt,
		Target: Target{
			RequestOrigin: origin,
			PairingCode:   pairingCode,
		},
		TargetSummary:    fmt.Sprintf("adopt request from %s (pairing code %s, self-reported name: %s)", origin, pairingCode, name),
		RiskLevel:        RiskHigh,
		RequiresApproval: true,
		ExpectedEffects: []string{
			fmt.Sprintf("issue independent long-term credential to control plane at %s (self-reported name: %s)", origin, name),
		},
		Checks: []Check{
			{Name: "adoption_request_pending", Status: "passed", Message: "adoption request awaiting approval"},
		},
		CreatedAt: now,
		ExpiresAt: now.Add(DefaultPlanTTL),
	}
	plan.Fingerprint = requestID
	return plan, nil
}

func findEnvironment(project model.Project, envName string) (model.Environment, bool) {
	for _, env := range project.Environments {
		if env.Name == envName {
			return env, env.IsDev
		}
	}
	return model.Environment{}, false
}

func runtimeVerb(kind string) string {
	switch kind {
	case OperationRuntimeStart:
		return "start"
	case OperationRuntimeStop:
		return "stop"
	case OperationRuntimeRestart:
		return "restart"
	default:
		return "operate"
	}
}
