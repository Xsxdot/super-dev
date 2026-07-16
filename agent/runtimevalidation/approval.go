// approval.go 在正式 Agent HTTP 门禁上执行受控无人值守审批并消费一次性 token。
//
// 职责：
//   - 识别 MCP approval_required，并注册精确、短期的 operation identity allowlist
//   - 复核 plan ID、fingerprint、kind、规范化 target 与双重过期时间
//   - 只为 exact match 调用正式 approve 接口，领取 token 并核验它被唯一消费
//   - 创建不进入 allowlist 的 pending 读探针，验证未知审批始终保持未批准
//
// 边界：
//   - 不批准 allowlist 外的 pending，不调用 reject，不开启 grace window
//   - 不接受未知、过期、重复 pending 或 identity 漂移，不持久化 approval token
//   - pending 读探针不执行对应业务 mutation，并随 disposable profile 一起删除
package runtimevalidation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/operation"
)

const (
	maxApprovalResponseBytes = 128 * 1024
	maxRuntimeApprovalTTL    = 15 * time.Minute
)

// ApprovalActorOptions 配置 foundation Agent origin、campaign 身份与 tool/kind allowlist。
type ApprovalActorOptions struct {
	AgentURL     string
	CampaignID   string
	AllowedKinds map[string][]string
	HTTPClient   *http.Client
}

// ApprovalToolCaller 为需要审批的 MCP 调用执行 exact-match 自动批准并完成一次 token 重试。
type ApprovalToolCaller struct {
	delegate   ToolCaller
	baseURL    *url.URL
	campaign   string
	allowed    map[string]map[string]struct{}
	http       *http.Client
	mu         sync.Mutex
	registered map[string]approvalIdentity
	readProbes map[string]approvalIdentity
}

// DefaultRuntimeValidationApprovalKinds 返回 strict campaign 允许复核的 tool/operation 对。
//
// 注意：只有 MCP 返回 approval_required 且 identity 精确匹配时，actor 才能使用此白名单自动批准。
func DefaultRuntimeValidationApprovalKinds() map[string][]string {
	return map[string][]string{
		"apply_config_change":        {"config.project.upsert", "config.service.upsert", "config.pipeline.upsert"},
		"upsert_project_config":      {"config.project.upsert"},
		"upsert_service":             {"config.service.upsert"},
		"upsert_project_pipeline":    {"config.pipeline.upsert"},
		"start_service":              {"runtime.start"},
		"stop_service":               {"runtime.stop"},
		"restart_service":            {"runtime.restart"},
		"open_browser_debug_session": {"browser_debug.open"},
		"debug_capture_at":           {"code_debug.open"},
		"debug_evaluate":             {"code_debug.evaluate"},
		"import_pipeline_template":   {"template.import"},
		"deploy_project_pipeline":    {"pipeline.run"},
	}
}

// NewApprovalToolCaller 创建仅允许 loopback foundation Agent 的审批 wrapper。
func NewApprovalToolCaller(delegate ToolCaller, options ApprovalActorOptions) (*ApprovalToolCaller, error) {
	if delegate == nil || strings.TrimSpace(options.CampaignID) == "" || len(options.AllowedKinds) == 0 {
		return nil, fmt.Errorf("approval actor delegate, campaign_id and allowlist are required")
	}
	canonical, err := canonicalLoopbackAgentURL(options.AgentURL)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(canonical)
	if err != nil {
		return nil, err
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	} else {
		clone := *client
		client = &clone
		if client.Timeout == 0 {
			client.Timeout = 15 * time.Second
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	allowed := make(map[string]map[string]struct{}, len(options.AllowedKinds))
	for tool, kinds := range options.AllowedKinds {
		allowed[tool] = map[string]struct{}{}
		for _, kind := range kinds {
			if strings.TrimSpace(kind) != "" {
				allowed[tool][kind] = struct{}{}
			}
		}
	}
	return &ApprovalToolCaller{
		delegate: delegate, baseURL: parsed, campaign: options.CampaignID, allowed: allowed,
		http: client, registered: map[string]approvalIdentity{}, readProbes: map[string]approvalIdentity{},
	}, nil
}

// PreparePendingReadProbe 通过真实 MCP 受审批调用创建一个不进入 allowlist 的 pending 读探针。
//
// 参数：
//   - ctx: campaign 上下文，用于取消真实 MCP 与 Agent 查询
//   - name: 会稳定产生 approval_required 的受审批 MCP 工具名
//   - arguments: 指向 campaign-owned 唯一目标的工具参数
//
// 返回：
//   - 保持 pending 的 approval ID，供 list/get 工具执行成功路径验证
//   - 工具、identity、TTL 或重复目标不满足严格合同时的错误
//
// 注意：该方法刻意绕过自动批准分支，只创建审批历史而不执行对应业务 mutation；
// 后续即使同一 pending 再次进入 CallTool，也会被 readProbes 硬门拒绝批准。
func (c *ApprovalToolCaller) PreparePendingReadProbe(ctx context.Context, name string, arguments map[string]any) (string, error) {
	allowedKinds, approvalAware := c.allowed[name]
	if !approvalAware {
		return "", fmt.Errorf("approval read probe tool %s is not allowlisted", name)
	}
	delegatedArguments := cloneMap(arguments)
	delete(delegatedArguments, "approval_token")
	delegatedArguments["approval_wait_seconds"] = 0
	log := logger.GetLogger().WithEntryName("RuntimeValidationApproval").WithFields(map[string]any{
		"campaign_id": c.campaign, "tool": name,
	})
	log.Info("开始创建不进入 allowlist 的 pending approval 读探针")
	result, err := c.delegate.CallTool(ctx, name, delegatedArguments)
	if err != nil {
		log.WithErr(err).Error("pending approval 读探针 MCP 调用失败")
		return "", err
	}
	required, ok := parseApprovalRequired(result)
	if !ok {
		if result.IsError || RawMessageMap(result.StructuredContent)["ok"] == false {
			return "", toolApplicationError(name, result)
		}
		return "", fmt.Errorf("approval read probe tool %s completed without approval_required", name)
	}
	if _, allowed := allowedKinds[required.Kind]; !allowed {
		return "", fmt.Errorf("approval read probe tool/kind is not allowlisted: %s/%s", name, required.Kind)
	}
	if err := validateTransientApprovalIdentity(required, time.Now().UTC()); err != nil {
		return "", err
	}
	detail, err := c.getApproval(ctx, required.ID)
	if err != nil {
		return "", err
	}
	if err := matchApprovalIdentity(required, detail); err != nil {
		return "", err
	}
	if detail.Status != "pending" {
		return "", fmt.Errorf("operation approval %s read probe is not pending", required.ID)
	}
	if err := c.rejectDuplicatePending(ctx, required); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.registered[required.ID]; exists {
		return "", fmt.Errorf("operation approval %s is already registered in the campaign allowlist", required.ID)
	}
	if _, exists := c.readProbes[required.ID]; exists {
		return "", fmt.Errorf("operation approval %s is already a pending read probe", required.ID)
	}
	c.readProbes[required.ID] = required
	log.WithFields(map[string]any{
		"approval_id": required.ID, "plan_id": required.PlanID, "operation_kind": required.Kind,
		"fingerprint": required.Fingerprint,
	}).Info("pending approval 读探针已创建并保持在 allowlist 外")
	return required.ID, nil
}

// CallTool 执行原 MCP 调用；仅为已登记的精确 identity 自动批准并携带一次性 token 重试。
func (c *ApprovalToolCaller) CallTool(ctx context.Context, name string, arguments map[string]any) (ToolCallResult, error) {
	delegatedArguments := arguments
	allowedKinds, approvalAware := c.allowed[name]
	if approvalAware {
		delegatedArguments = cloneMap(arguments)
		// MCP 内建等待会在 wrapper 看到 approval_required 前消费 token；强制归零后由本层独占精确 identity 复核。
		delegatedArguments["approval_wait_seconds"] = 0
	}
	result, err := c.delegate.CallTool(ctx, name, delegatedArguments)
	if err != nil {
		return result, err
	}
	required, ok := parseApprovalRequired(result)
	if !ok {
		if approvalAware && !result.IsError && RawMessageMap(result.StructuredContent)["ok"] != false {
			// 本地 dev runtime 控制由产品策略明确判为低风险；它可以直接成功，
			// 但配置、调试和 pipeline 等固定必审操作直接成功仍表示策略或 grace 漂移。
			if runtimeControlMaySkipApproval(name) {
				logger.GetLogger().WithEntryName("RuntimeValidationApproval").WithFields(map[string]any{
					"campaign_id": c.campaign, "tool": name,
				}).Info("低风险本地 runtime 操作无需 pending approval，直接沿用 MCP 成功结果")
				return result, nil
			}
			return result, markMutationApplied(fmt.Errorf("approval-aware MCP tool %s completed without approval_required", name))
		}
		return result, nil
	}
	if _, allowed := allowedKinds[required.Kind]; !allowed {
		return result, fmt.Errorf("approval tool/kind is not allowlisted: %s/%s", name, required.Kind)
	}
	if err := c.registerTransientAllowlist(required, time.Now().UTC()); err != nil {
		return result, err
	}

	log := logger.GetLogger().WithEntryName("RuntimeValidationApproval").WithFields(map[string]any{
		"campaign_id": c.campaign, "tool": name, "approval_id": required.ID,
		"plan_id": required.PlanID, "operation_kind": required.Kind, "fingerprint": required.Fingerprint,
	})
	log.Info("已注册短期精确 approval allowlist，准备调用正式审批接口")
	detail, err := c.approveRegisteredPending(ctx, required.ID)
	if err != nil {
		log.WithErr(err).Error("短期精确 approval 自动审批失败")
		return result, err
	}
	// 即使请求明确 grant_grace=false，也要在业务重试前复核审计，防止服务端行为漂移。
	if err := c.rejectForbiddenGrace(ctx, required); err != nil {
		log.WithErr(err).Error("自动审批出现禁止的 grace 审计")
		return result, err
	}
	retryArguments := cloneMap(delegatedArguments)
	retryArguments["approval_token"] = detail.Token
	log.Info("自动审批 identity 已精确匹配，重试原 MCP tools/call")
	retried, retryErr := c.delegate.CallTool(ctx, name, retryArguments)
	if retryErr != nil {
		return retried, retryErr
	}
	if retried.IsError || RawMessageMap(retried.StructuredContent)["ok"] == false {
		return retried, toolApplicationError(name, retried)
	}
	if err := c.verifyTokenConsumedWithoutGrace(ctx, required); err != nil {
		// 业务重试已成功；后置审计证明失败时仍必须让外层 journal 接管 cleanup。
		return retried, markMutationApplied(err)
	}
	log.Info("自动 operation approval 一次性 token 已消费")
	return retried, nil
}

func runtimeControlMaySkipApproval(tool string) bool {
	switch tool {
	case "start_service", "stop_service", "restart_service":
		return true
	default:
		return false
	}
}

func (c *ApprovalToolCaller) approveRegisteredPending(ctx context.Context, approvalID string) (approvalIdentity, error) {
	c.mu.Lock()
	expected, registered := c.registered[approvalID]
	c.mu.Unlock()
	if !registered {
		return approvalIdentity{}, fmt.Errorf("operation approval %s is not registered in the campaign allowlist", approvalID)
	}
	detail, err := c.getApproval(ctx, expected.ID)
	if err != nil {
		return approvalIdentity{}, err
	}
	if err := matchApprovalIdentity(expected, detail); err != nil {
		return approvalIdentity{}, err
	}
	if detail.Status != "pending" {
		return approvalIdentity{}, fmt.Errorf("operation approval %s is not a new pending request", expected.ID)
	}
	if err := c.rejectDuplicatePending(ctx, expected); err != nil {
		return approvalIdentity{}, err
	}
	if err := c.approveOperation(ctx, expected); err != nil {
		return approvalIdentity{}, err
	}
	approved, err := c.getApproval(ctx, expected.ID)
	if err != nil {
		return approvalIdentity{}, err
	}
	if err := matchApprovalIdentity(expected, approved); err != nil {
		return approvalIdentity{}, err
	}
	if approved.Status != "approved" || strings.TrimSpace(approved.Token) == "" {
		return approvalIdentity{}, fmt.Errorf("automatically approved operation %s did not issue a one-time token", expected.ID)
	}
	return approved, nil
}

func (c *ApprovalToolCaller) approveOperation(ctx context.Context, expected approvalIdentity) error {
	body, err := json.Marshal(map[string]any{
		"decided_by":  "runtime-validation:" + c.campaign,
		"note":        "strict runtime validation exact allowlist actor",
		"grant_grace": false,
	})
	if err != nil {
		return err
	}
	endpoint := *c.baseURL
	endpoint.Path = "/api/operation-approvals/" + url.PathEscape(expected.ID) + "/approve"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	log := logger.GetLogger().WithEntryName("RuntimeValidationApproval").WithFields(map[string]any{
		"campaign_id": c.campaign, "approval_id": expected.ID, "plan_id": expected.PlanID,
		"operation_kind": expected.Kind, "fingerprint": expected.Fingerprint,
	})
	log.Info("开始调用正式 operation approve 接口")
	response, err := c.http.Do(request)
	if err != nil {
		log.WithErr(err).Error("调用正式 operation approve 接口失败")
		return fmt.Errorf("approve allowlisted operation: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxApprovalResponseBytes))
		err := fmt.Errorf("approve allowlisted operation returned HTTP %d", response.StatusCode)
		log.WithErr(err).Error("正式 operation approve 接口拒绝请求")
		return err
	}
	var payload struct {
		Approval       map[string]any `json:"approval"`
		GraceGranted   bool           `json:"grace_granted"`
		GraceExpiresAt any            `json:"grace_expires_at"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxApprovalResponseBytes)).Decode(&payload); err != nil {
		log.WithErr(err).Error("解析正式 operation approve 响应失败")
		return fmt.Errorf("decode allowlisted operation approval: %w", err)
	}
	approved, err := approvalIdentityFromMaps(payload.Approval, RawMessageMap(payload.Approval["plan"]))
	if err != nil {
		return fmt.Errorf("decode approved operation identity: %w", err)
	}
	if err := matchApprovalIdentity(expected, approved); err != nil {
		return err
	}
	if approved.Status != "approved" {
		return fmt.Errorf("operation approval %s returned status %s after approve", expected.ID, approved.Status)
	}
	if payload.GraceGranted || payload.GraceExpiresAt != nil {
		return fmt.Errorf("operation approval %s unexpectedly granted grace", expected.ID)
	}
	log.Info("正式 operation approve 接口已精确批准且未开启 grace")
	return nil
}

func (c *ApprovalToolCaller) rejectForbiddenGrace(ctx context.Context, expected approvalIdentity) error {
	events, err := c.listMatchingOperationAudit(ctx, expected)
	if err != nil {
		return err
	}
	return rejectForbiddenGraceEvents(events, expected)
}

func (c *ApprovalToolCaller) verifyTokenConsumedWithoutGrace(ctx context.Context, expected approvalIdentity) error {
	events, err := c.listMatchingOperationAudit(ctx, expected)
	if err != nil {
		return err
	}
	if err := rejectForbiddenGraceEvents(events, expected); err != nil {
		return err
	}
	executed := 0
	for _, event := range events {
		if strings.TrimSpace(fmt.Sprint(event["action"])) != operation.AuditExecuted {
			continue
		}
		if strings.TrimSpace(fmt.Sprint(event["approval_id"])) != expected.ID {
			continue
		}
		executed++
	}
	if executed != 1 {
		return fmt.Errorf("operation approval %s one-time token consumption audit count is %d, want 1", expected.ID, executed)
	}
	return nil
}

func (c *ApprovalToolCaller) listMatchingOperationAudit(ctx context.Context, expected approvalIdentity) ([]map[string]any, error) {
	log := logger.GetLogger().WithEntryName("RuntimeValidationApproval").WithFields(map[string]any{
		"campaign_id": c.campaign, "approval_id": expected.ID, "operation_kind": expected.Kind,
	})
	endpoint := *c.baseURL
	endpoint.Path = "/api/operation-audit"
	query := endpoint.Query()
	query.Set("kind", expected.Kind)
	query.Set("since", expected.PlanCreatedAt.Format(time.RFC3339Nano))
	query.Set("limit", "0")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	log.Info("开始读取 operation approval 审计")
	response, err := c.http.Do(request)
	if err != nil {
		log.WithErr(err).Error("读取 operation approval 审计失败")
		return nil, fmt.Errorf("list operation audit for approval verification: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxApprovalResponseBytes))
		err := fmt.Errorf("list operation audit for approval verification returned HTTP %d", response.StatusCode)
		log.WithErr(err).Error("operation approval 审计接口返回非成功状态")
		return nil, err
	}
	var payload struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxApprovalResponseBytes)).Decode(&payload); err != nil {
		log.WithErr(err).Error("解析 operation approval 审计失败")
		return nil, fmt.Errorf("decode operation audit for approval verification: %w", err)
	}

	matched := make([]map[string]any, 0, len(payload.Events))
	for _, event := range payload.Events {
		plan := RawMessageMap(event["plan"])
		if strings.TrimSpace(fmt.Sprint(event["kind"])) != expected.Kind ||
			strings.TrimSpace(fmt.Sprint(plan["kind"])) != expected.Kind ||
			strings.TrimSpace(fmt.Sprint(plan["fingerprint"])) != expected.Fingerprint {
			continue
		}
		target, targetErr := normalizeApprovalTarget(plan["target"])
		if targetErr != nil {
			return nil, fmt.Errorf("operation audit for approval %s has incomplete target identity", expected.ID)
		}
		if target != expected.NormalizedTarget {
			continue
		}
		matched = append(matched, event)
	}
	log.WithFields(map[string]any{"event_count": len(payload.Events), "matched_count": len(matched)}).Info("operation approval 审计读取完成")
	return matched, nil
}

func rejectForbiddenGraceEvents(events []map[string]any, expected approvalIdentity) error {
	for _, event := range events {
		action := strings.TrimSpace(fmt.Sprint(event["action"]))
		if action == operation.AuditGraceGranted || action == operation.AuditApprovedByGrace {
			return fmt.Errorf("operation approval %s used forbidden grace action %s", expected.ID, action)
		}
	}
	return nil
}

type approvalIdentity struct {
	ID               string
	PlanID           string
	Kind             string
	Fingerprint      string
	NormalizedTarget string
	PlanCreatedAt    time.Time
	PlanExpiresAt    time.Time
	ExpiresAt        time.Time
	Status           string
	Token            string
}

func parseApprovalRequired(result ToolCallResult) (approvalIdentity, bool) {
	structured := RawMessageMap(result.StructuredContent)
	if fmt.Sprint(structured["code"]) != "approval_required" {
		return approvalIdentity{}, false
	}
	data := RawMessageMap(structured["data"])
	approval := RawMessageMap(data["approval"])
	if len(approval) == 0 {
		approval = RawMessageMap(structured["approval"])
	}
	plan := RawMessageMap(data["plan"])
	if len(plan) == 0 {
		plan = RawMessageMap(structured["plan"])
	}
	if len(plan) == 0 {
		plan = RawMessageMap(approval["plan"])
	}
	identity, err := approvalIdentityFromMaps(approval, plan)
	if err != nil {
		return approvalIdentity{}, false
	}
	if nestedPlan := RawMessageMap(approval["plan"]); len(nestedPlan) > 0 {
		nested, nestedErr := approvalIdentityFromMaps(approval, nestedPlan)
		if nestedErr != nil || matchApprovalPlan(identity, nested) != nil {
			return approvalIdentity{}, false
		}
	}
	return identity, true
}

func (c *ApprovalToolCaller) registerTransientAllowlist(identity approvalIdentity, now time.Time) error {
	if err := validateTransientApprovalIdentity(identity, now); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, readProbe := c.readProbes[identity.ID]; readProbe {
		return fmt.Errorf("approval %s is a pending read probe and cannot enter the campaign allowlist", identity.ID)
	}
	if _, exists := c.registered[identity.ID]; exists {
		return fmt.Errorf("approval %s was already registered by this campaign", identity.ID)
	}
	c.registered[identity.ID] = identity
	return nil
}

func validateTransientApprovalIdentity(identity approvalIdentity, now time.Time) error {
	if identity.Status != "pending" {
		return fmt.Errorf("approval %s is not a new pending request", identity.ID)
	}
	if !identity.PlanExpiresAt.After(now) || !identity.ExpiresAt.After(now) {
		return fmt.Errorf("approval %s or its plan is expired", identity.ID)
	}
	if identity.PlanExpiresAt.After(now.Add(maxRuntimeApprovalTTL)) || identity.ExpiresAt.After(now.Add(maxRuntimeApprovalTTL)) {
		return fmt.Errorf("approval %s exceeds the %s short-lived allowlist TTL", identity.ID, maxRuntimeApprovalTTL)
	}
	return nil
}

func (c *ApprovalToolCaller) getApproval(ctx context.Context, id string) (approvalIdentity, error) {
	log := logger.GetLogger().WithEntryName("RuntimeValidationApproval").WithFields(map[string]any{
		"campaign_id": c.campaign, "approval_id": id,
	})
	endpoint := *c.baseURL
	endpoint.Path = "/api/operation-approvals/" + url.PathEscape(id)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return approvalIdentity{}, err
	}
	log.Info("开始读取 operation approval detail")
	response, err := c.http.Do(request)
	if err != nil {
		log.WithErr(err).Error("读取 operation approval detail 失败")
		return approvalIdentity{}, fmt.Errorf("get operation approval: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxApprovalResponseBytes))
		err := fmt.Errorf("get operation approval returned HTTP %d", response.StatusCode)
		log.WithErr(err).Error("operation approval detail 接口返回非成功状态")
		return approvalIdentity{}, err
	}
	detail, err := decodeApprovalIdentity(response.Body)
	if err != nil {
		log.WithErr(err).Error("解析 operation approval detail 失败")
		return approvalIdentity{}, err
	}
	log.WithField("status", detail.Status).Info("operation approval detail 读取完成")
	return detail, nil
}

func (c *ApprovalToolCaller) rejectDuplicatePending(ctx context.Context, expected approvalIdentity) error {
	log := logger.GetLogger().WithEntryName("RuntimeValidationApproval").WithFields(map[string]any{
		"campaign_id": c.campaign, "approval_id": expected.ID, "operation_kind": expected.Kind,
	})
	endpoint := *c.baseURL
	endpoint.Path = "/api/operation-approvals"
	query := endpoint.Query()
	query.Set("status", "pending")
	query.Set("limit", "0")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	log.Info("开始读取 pending operation approvals 以排除重复目标")
	response, err := c.http.Do(request)
	if err != nil {
		log.WithErr(err).Error("读取 pending operation approvals 失败")
		return fmt.Errorf("list pending operation approvals: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxApprovalResponseBytes))
		err := fmt.Errorf("list pending operation approvals returned HTTP %d", response.StatusCode)
		log.WithErr(err).Error("pending operation approvals 接口返回非成功状态")
		return err
	}
	var approvals []map[string]any
	if err := json.NewDecoder(io.LimitReader(response.Body, maxApprovalResponseBytes)).Decode(&approvals); err != nil {
		log.WithErr(err).Error("解析 pending operation approvals 失败")
		return fmt.Errorf("decode pending operation approvals: %w", err)
	}
	for _, raw := range approvals {
		candidate, decodeErr := approvalIdentityFromMaps(raw, RawMessageMap(raw["plan"]))
		if decodeErr != nil {
			return fmt.Errorf("pending operation approval metadata is incomplete: %w", decodeErr)
		}
		if candidate.ID != expected.ID && candidate.Kind == expected.Kind && candidate.NormalizedTarget == expected.NormalizedTarget {
			log.WithField("pending_count", len(approvals)).Error("发现相同 kind 与目标的重复 pending approval")
			return fmt.Errorf("duplicate pending approvals for operation kind %s and the same normalized target", expected.Kind)
		}
	}
	log.WithField("pending_count", len(approvals)).Info("pending operation approvals 重复目标检查完成")
	return nil
}

func decodeApprovalIdentity(reader io.Reader) (approvalIdentity, error) {
	var payload map[string]any
	decoder := json.NewDecoder(io.LimitReader(reader, maxApprovalResponseBytes))
	if err := decoder.Decode(&payload); err != nil {
		return approvalIdentity{}, fmt.Errorf("decode operation approval metadata: %w", err)
	}
	approval := RawMessageMap(payload["approval"])
	identity, err := approvalIdentityFromMaps(approval, RawMessageMap(approval["plan"]))
	if err != nil {
		return identity, err
	}
	identity.Token = strings.TrimSpace(fmt.Sprint(payload["approval_token"]))
	return identity, nil
}

func approvalIdentityFromMaps(approval map[string]any, plan map[string]any) (approvalIdentity, error) {
	target, err := normalizeApprovalTarget(plan["target"])
	if err != nil {
		return approvalIdentity{}, err
	}
	identity := approvalIdentity{
		ID: strings.TrimSpace(fmt.Sprint(approval["id"])), PlanID: strings.TrimSpace(fmt.Sprint(plan["id"])),
		Kind: strings.TrimSpace(fmt.Sprint(plan["kind"])), Fingerprint: strings.TrimSpace(fmt.Sprint(plan["fingerprint"])),
		NormalizedTarget: target, Status: strings.TrimSpace(fmt.Sprint(approval["status"])),
	}
	identity.PlanCreatedAt, err = parseApprovalTime(plan["created_at"])
	if err != nil {
		return identity, fmt.Errorf("parse operation plan creation time: %w", err)
	}
	identity.PlanExpiresAt, err = parseApprovalTime(plan["expires_at"])
	if err != nil {
		return identity, fmt.Errorf("parse operation plan expiry: %w", err)
	}
	identity.ExpiresAt, err = parseApprovalTime(approval["expires_at"])
	if err != nil {
		return identity, fmt.Errorf("parse operation approval expiry: %w", err)
	}
	if identity.ID == "" || identity.PlanID == "" || identity.Kind == "" || identity.Fingerprint == "" || identity.NormalizedTarget == "" || identity.Status == "" {
		return identity, fmt.Errorf("operation approval metadata is incomplete")
	}
	return identity, nil
}

func normalizeApprovalTarget(value any) (string, error) {
	if len(RawMessageMap(value)) == 0 {
		return "", fmt.Errorf("operation approval target is missing")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("normalize operation approval target: %w", err)
	}
	return string(encoded), nil
}

func parseApprovalTime(value any) (time.Time, error) {
	raw := strings.TrimSpace(fmt.Sprint(value))
	if raw == "" || raw == "<nil>" {
		return time.Time{}, fmt.Errorf("timestamp is missing")
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func matchApprovalPlan(expected, actual approvalIdentity) error {
	if expected.PlanID != actual.PlanID || expected.Kind != actual.Kind || expected.Fingerprint != actual.Fingerprint ||
		expected.NormalizedTarget != actual.NormalizedTarget || !expected.PlanCreatedAt.Equal(actual.PlanCreatedAt) ||
		!expected.PlanExpiresAt.Equal(actual.PlanExpiresAt) {
		return fmt.Errorf("operation approval plan identity drift for plan %s", expected.PlanID)
	}
	return nil
}

func matchApprovalIdentity(expected, actual approvalIdentity) error {
	if expected.ID != actual.ID || !expected.ExpiresAt.Equal(actual.ExpiresAt) {
		return fmt.Errorf("operation approval identity drift for approval %s", expected.ID)
	}
	return matchApprovalPlan(expected, actual)
}
