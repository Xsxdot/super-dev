// approval.go 通过正式 Agent HTTP 门禁为精确 fingerprint 执行一次性审批。
//
// 职责：
//   - 识别 MCP approval_required，复核 approval ID、operation kind 与 plan fingerprint
//   - 仅对 campaign allowlist 执行 grant_grace=false 的真实批准
//   - 领取一次性 token 后重试原 tools/call，token 不进入日志或证据
//
// 边界：
//   - 不批准未列入 allowlist 的 tool/kind，不忽略 fingerprint 漂移
//   - 不开启 grace window，不持久化 approval token
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
	"time"

	"github.com/xsxdot/gokit/logger"
)

const maxApprovalResponseBytes = 128 * 1024

// ApprovalActorOptions 配置 foundation Agent origin、campaign 身份与 tool/kind allowlist。
type ApprovalActorOptions struct {
	AgentURL     string
	CampaignID   string
	AllowedKinds map[string][]string
	HTTPClient   *http.Client
}

// ApprovalToolCaller 为需要审批的 MCP 调用执行 fingerprint 匹配和一次重试。
type ApprovalToolCaller struct {
	delegate ToolCaller
	baseURL  *url.URL
	campaign string
	allowed  map[string]map[string]struct{}
	http     *http.Client
}

// DefaultRuntimeValidationApprovalKinds 返回 strict campaign 允许自动复核的 tool/operation 对。
//
// 注意：这是审批匹配白名单，不是跳过审批的权限列表。
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
	return &ApprovalToolCaller{delegate: delegate, baseURL: parsed, campaign: options.CampaignID, allowed: allowed, http: client}, nil
}

// CallTool 执行原 MCP 调用；仅在精确 approval_required 合同上批准并重试一次。
func (c *ApprovalToolCaller) CallTool(ctx context.Context, name string, arguments map[string]any) (ToolCallResult, error) {
	result, err := c.delegate.CallTool(ctx, name, arguments)
	if err != nil {
		return result, err
	}
	required, ok := parseApprovalRequired(result)
	if !ok {
		return result, nil
	}
	allowedKinds := c.allowed[name]
	if _, allowed := allowedKinds[required.Kind]; !allowed {
		return result, fmt.Errorf("approval tool/kind is not allowlisted: %s/%s", name, required.Kind)
	}
	log := logger.GetLogger().WithEntryName("RuntimeValidationApproval").WithFields(map[string]any{
		"campaign_id": c.campaign, "tool": name, "approval_id": required.ID,
		"operation_kind": required.Kind, "fingerprint": required.Fingerprint,
	})
	log.Info("开始复核 runtime validation operation approval fingerprint")
	pending, err := c.getApproval(ctx, required.ID)
	if err != nil {
		return result, err
	}
	if err := matchApprovalIdentity(required, pending); err != nil {
		return result, err
	}
	approved, err := c.approve(ctx, required)
	if err != nil {
		return result, err
	}
	if err := matchApprovalIdentity(required, approved); err != nil {
		return result, err
	}
	detail, err := c.getApproval(ctx, required.ID)
	if err != nil {
		return result, err
	}
	if err := matchApprovalIdentity(required, detail); err != nil {
		return result, err
	}
	if detail.Status != "approved" || strings.TrimSpace(detail.Token) == "" {
		return result, fmt.Errorf("approved operation %s did not issue a one-time token", required.ID)
	}
	retryArguments := cloneMap(arguments)
	retryArguments["approval_token"] = detail.Token
	log.Info("operation approval 已精确批准，重试原 MCP tools/call")
	retried, retryErr := c.delegate.CallTool(ctx, name, retryArguments)
	if retryErr != nil {
		return retried, retryErr
	}
	if retried.IsError || RawMessageMap(retried.StructuredContent)["ok"] == false {
		return retried, fmt.Errorf("approved MCP tool %s still returned an application error", name)
	}
	log.Info("operation approval 一次性 token 已消费")
	return retried, nil
}

type approvalIdentity struct {
	ID          string
	Kind        string
	Fingerprint string
	Status      string
	Token       string
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
	identity := approvalIdentity{ID: strings.TrimSpace(fmt.Sprint(approval["id"])), Kind: strings.TrimSpace(fmt.Sprint(plan["kind"])), Fingerprint: strings.TrimSpace(fmt.Sprint(plan["fingerprint"]))}
	if identity.ID == "" || identity.Kind == "" || identity.Fingerprint == "" {
		return identity, false
	}
	return identity, true
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

func (c *ApprovalToolCaller) approve(ctx context.Context, expected approvalIdentity) (approvalIdentity, error) {
	payload, err := json.Marshal(map[string]any{
		"decided_by":  "runtime-validation:" + c.campaign,
		"note":        "strict runtime validation fingerprint approved",
		"grant_grace": false,
	})
	if err != nil {
		return approvalIdentity{}, err
	}
	endpoint := *c.baseURL
	endpoint.Path = "/api/operation-approvals/" + url.PathEscape(expected.ID) + "/approve"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return approvalIdentity{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return approvalIdentity{}, fmt.Errorf("approve operation: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxApprovalResponseBytes))
		return approvalIdentity{}, fmt.Errorf("approve operation returned HTTP %d", response.StatusCode)
	}
	identity, err := decodeApprovalIdentity(response.Body)
	if err != nil {
		return identity, err
	}
	return identity, nil
}

func decodeApprovalIdentity(reader io.Reader) (approvalIdentity, error) {
	var payload map[string]any
	decoder := json.NewDecoder(io.LimitReader(reader, maxApprovalResponseBytes))
	if err := decoder.Decode(&payload); err != nil {
		return approvalIdentity{}, fmt.Errorf("decode operation approval metadata: %w", err)
	}
	approval := RawMessageMap(payload["approval"])
	plan := RawMessageMap(approval["plan"])
	identity := approvalIdentity{
		ID: strings.TrimSpace(fmt.Sprint(approval["id"])), Kind: strings.TrimSpace(fmt.Sprint(plan["kind"])),
		Fingerprint: strings.TrimSpace(fmt.Sprint(plan["fingerprint"])), Status: strings.TrimSpace(fmt.Sprint(approval["status"])),
		Token: strings.TrimSpace(fmt.Sprint(payload["approval_token"])),
	}
	if identity.ID == "" || identity.Kind == "" || identity.Fingerprint == "" {
		return identity, fmt.Errorf("operation approval metadata is incomplete")
	}
	return identity, nil
}

func matchApprovalIdentity(expected, actual approvalIdentity) error {
	if expected.ID != actual.ID || expected.Kind != actual.Kind || expected.Fingerprint != actual.Fingerprint {
		return fmt.Errorf("operation approval fingerprint identity drift: expected %s/%s/%s", expected.ID, expected.Kind, expected.Fingerprint)
	}
	return nil
}
