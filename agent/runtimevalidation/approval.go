// approval.go 在正式 Agent HTTP 门禁上消费真人批准的一次性 token。
//
// 职责：
//   - 识别 MCP approval_required，并注册精确、短期的 operation identity allowlist
//   - 复核 plan ID、fingerprint、kind、规范化 target 与双重过期时间
//   - 等待用户在正式 SuperDev 审批面批准后领取 token，并核验它被唯一消费
//
// 边界：
//   - 不代替用户调用 approve/reject，不开启 grace window
//   - 不接受未知、过期、重复 pending 或 identity 漂移，不持久化 approval token
package runtimevalidation

import (
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
	defaultApprovalPoll      = 250 * time.Millisecond
)

// ApprovalActorOptions 配置 foundation Agent origin、campaign 身份与 tool/kind allowlist。
type ApprovalActorOptions struct {
	AgentURL     string
	CampaignID   string
	AllowedKinds map[string][]string
	HTTPClient   *http.Client
	PollInterval time.Duration
}

// ApprovalToolCaller 为需要审批的 MCP 调用复核真人批准并执行一次重试。
type ApprovalToolCaller struct {
	delegate     ToolCaller
	baseURL      *url.URL
	campaign     string
	allowed      map[string]map[string]struct{}
	http         *http.Client
	pollInterval time.Duration
	mu           sync.Mutex
	registered   map[string]struct{}
}

// DefaultRuntimeValidationApprovalKinds 返回 strict campaign 允许复核的 tool/operation 对。
//
// 注意：这是匹配白名单，不是跳过真人审批的权限列表。
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
	pollInterval := options.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultApprovalPoll
	}
	return &ApprovalToolCaller{
		delegate: delegate, baseURL: parsed, campaign: options.CampaignID, allowed: allowed,
		http: client, pollInterval: pollInterval, registered: map[string]struct{}{},
	}, nil
}

// CallTool 执行原 MCP 调用；仅在精确 identity 获得真人批准后携带一次性 token 重试。
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
		// strict campaign 中的受控写工具必须先返回 approval_required。
		// 首次直接成功意味着审批策略被关闭或命中了 grace，两者都不能作为验收通路。
		if approvalAware && !result.IsError && RawMessageMap(result.StructuredContent)["ok"] != false {
			return result, fmt.Errorf("approval-aware MCP tool %s completed without approval_required", name)
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
	log.Info("已注册短期精确 approval allowlist，等待用户在正式审批面确认")
	detail, err := c.waitForHumanApproval(ctx, required)
	if err != nil {
		return result, err
	}
	retryArguments := cloneMap(delegatedArguments)
	retryArguments["approval_token"] = detail.Token
	log.Info("真人审批 identity 已精确匹配，重试原 MCP tools/call")
	retried, retryErr := c.delegate.CallTool(ctx, name, retryArguments)
	if retryErr != nil {
		return retried, retryErr
	}
	if retried.IsError || RawMessageMap(retried.StructuredContent)["ok"] == false {
		return retried, fmt.Errorf("approved MCP tool %s still returned an application error", name)
	}
	if err := c.verifyTokenConsumedWithoutGrace(ctx, required); err != nil {
		return retried, err
	}
	log.Info("operation approval 一次性 token 已消费")
	return retried, nil
}

func (c *ApprovalToolCaller) verifyTokenConsumedWithoutGrace(ctx context.Context, expected approvalIdentity) error {
	endpoint := *c.baseURL
	endpoint.Path = "/api/operation-audit"
	query := endpoint.Query()
	query.Set("kind", expected.Kind)
	query.Set("limit", "0")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("list operation audit after approved retry: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxApprovalResponseBytes))
		return fmt.Errorf("list operation audit after approved retry returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxApprovalResponseBytes)).Decode(&payload); err != nil {
		return fmt.Errorf("decode operation audit after approved retry: %w", err)
	}

	executed := 0
	for _, event := range payload.Events {
		plan := RawMessageMap(event["plan"])
		if strings.TrimSpace(fmt.Sprint(event["kind"])) != expected.Kind ||
			strings.TrimSpace(fmt.Sprint(plan["kind"])) != expected.Kind ||
			strings.TrimSpace(fmt.Sprint(plan["fingerprint"])) != expected.Fingerprint {
			continue
		}
		target, targetErr := normalizeApprovalTarget(plan["target"])
		if targetErr != nil {
			return fmt.Errorf("operation audit for approval %s has incomplete target identity", expected.ID)
		}
		if target != expected.NormalizedTarget {
			continue
		}
		action := strings.TrimSpace(fmt.Sprint(event["action"]))
		switch action {
		case operation.AuditGraceGranted, operation.AuditApprovedByGrace:
			return fmt.Errorf("operation approval %s used forbidden grace action %s", expected.ID, action)
		case operation.AuditExecuted:
			if strings.TrimSpace(fmt.Sprint(event["approval_id"])) != expected.ID {
				return fmt.Errorf("operation approval %s execution audit has a different approval identity", expected.ID)
			}
			executed++
		}
	}
	if executed != 1 {
		return fmt.Errorf("operation approval %s one-time token consumption audit count is %d, want 1", expected.ID, executed)
	}
	return nil
}

type approvalIdentity struct {
	ID               string
	PlanID           string
	Kind             string
	Fingerprint      string
	NormalizedTarget string
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
	if identity.Status != "pending" {
		return fmt.Errorf("approval %s is not a new pending request", identity.ID)
	}
	if !identity.PlanExpiresAt.After(now) || !identity.ExpiresAt.After(now) {
		return fmt.Errorf("approval %s or its plan is expired", identity.ID)
	}
	if identity.PlanExpiresAt.After(now.Add(maxRuntimeApprovalTTL)) || identity.ExpiresAt.After(now.Add(maxRuntimeApprovalTTL)) {
		return fmt.Errorf("approval %s exceeds the %s short-lived allowlist TTL", identity.ID, maxRuntimeApprovalTTL)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.registered[identity.ID]; exists {
		return fmt.Errorf("approval %s was already registered by this campaign", identity.ID)
	}
	c.registered[identity.ID] = struct{}{}
	return nil
}

func (c *ApprovalToolCaller) waitForHumanApproval(ctx context.Context, expected approvalIdentity) (approvalIdentity, error) {
	detail, err := c.getApproval(ctx, expected.ID)
	if err != nil {
		return approvalIdentity{}, err
	}
	if err := matchApprovalIdentity(expected, detail); err != nil {
		return approvalIdentity{}, err
	}
	if detail.Status == "pending" {
		if err := c.rejectDuplicatePending(ctx, expected); err != nil {
			return approvalIdentity{}, err
		}
	}

	deadline := expected.PlanExpiresAt
	if expected.ExpiresAt.Before(deadline) {
		deadline = expected.ExpiresAt
	}
	for {
		switch detail.Status {
		case "approved":
			if strings.TrimSpace(detail.Token) == "" {
				return approvalIdentity{}, fmt.Errorf("approved operation %s did not issue a one-time token", expected.ID)
			}
			return detail, nil
		case "pending":
			// pending 只表示用户尚未决策；actor 自己绝不能调用 approve 接口。
		case "rejected", "expired", "used":
			return approvalIdentity{}, fmt.Errorf("operation approval %s reached terminal status %s", expected.ID, detail.Status)
		default:
			return approvalIdentity{}, fmt.Errorf("operation approval %s returned unknown status %q", expected.ID, detail.Status)
		}
		if !time.Now().UTC().Before(deadline) {
			return approvalIdentity{}, fmt.Errorf("operation approval %s expired while waiting for human decision", expected.ID)
		}
		timer := time.NewTimer(c.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return approvalIdentity{}, ctx.Err()
		case <-timer.C:
		}
		detail, err = c.getApproval(ctx, expected.ID)
		if err != nil {
			return approvalIdentity{}, err
		}
		if err := matchApprovalIdentity(expected, detail); err != nil {
			return approvalIdentity{}, err
		}
	}
}

func (c *ApprovalToolCaller) getApproval(ctx context.Context, id string) (approvalIdentity, error) {
	endpoint := *c.baseURL
	endpoint.Path = "/api/operation-approvals/" + url.PathEscape(id)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return approvalIdentity{}, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return approvalIdentity{}, fmt.Errorf("get operation approval: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxApprovalResponseBytes))
		return approvalIdentity{}, fmt.Errorf("get operation approval returned HTTP %d", response.StatusCode)
	}
	return decodeApprovalIdentity(response.Body)
}

func (c *ApprovalToolCaller) rejectDuplicatePending(ctx context.Context, expected approvalIdentity) error {
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
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("list pending operation approvals: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxApprovalResponseBytes))
		return fmt.Errorf("list pending operation approvals returned HTTP %d", response.StatusCode)
	}
	var approvals []map[string]any
	if err := json.NewDecoder(io.LimitReader(response.Body, maxApprovalResponseBytes)).Decode(&approvals); err != nil {
		return fmt.Errorf("decode pending operation approvals: %w", err)
	}
	for _, raw := range approvals {
		candidate, decodeErr := approvalIdentityFromMaps(raw, RawMessageMap(raw["plan"]))
		if decodeErr != nil {
			return fmt.Errorf("pending operation approval metadata is incomplete: %w", decodeErr)
		}
		if candidate.ID != expected.ID && candidate.Kind == expected.Kind && candidate.NormalizedTarget == expected.NormalizedTarget {
			return fmt.Errorf("duplicate pending approvals for operation kind %s and the same normalized target", expected.Kind)
		}
	}
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
		expected.NormalizedTarget != actual.NormalizedTarget || !expected.PlanExpiresAt.Equal(actual.PlanExpiresAt) {
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
