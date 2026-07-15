// credential.go 管理 strict campaign 的一次性调试凭据 lease 和 auth sidecar 验证。
//
// 职责：
//   - 经正式 Agent HTTP 创建 campaign-owned 进程内 credential lease
//   - 真实调用 get_debug_credentials，验证明文读回后登录 auth sidecar
//   - 无论 MCP/sidecar 成功或失败都按 lease ID + owner 精确 DELETE
//
// 边界：
//   - 不生成 secret，不将 secret 写入配置、命令行、日志或错误文本
//   - 不新增 MCP tool，不用 HTTP 直读凭据冒充 MCP 成功
package runtimevalidation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/debugcredential"
	"github.com/xsxdot/super-dev/agent/model"
)

const (
	runtimeValidationCredentialName = "runtime_validation_credential"
	runtimeValidationCredentialDesc = "一次性 runtime validation 调试凭据"
	runtimeValidationCredentialTTL  = 60 * time.Minute
	maxCredentialResponseBytes      = 64 * 1024
)

// CredentialActorOptions 提交 Agent/sidecar origin、campaign owner、人工输入 secret 与 ingestion redactor。
type CredentialActorOptions struct {
	AgentURL        string
	AuthSidecarURL  string
	CampaignID      string
	CredentialValue string
	HTTPClient      *http.Client
	Redactor        *RedactingWriter
	Cleanup         *CleanupStack
}

// CredentialToolCaller 只包装 get_debug_credentials 的 lease/readback/login/delete 生命周期。
type CredentialToolCaller struct {
	delegate ToolCaller
	agent    *url.URL
	sidecar  *url.URL
	campaign string
	secret   string
	http     *http.Client
	cleanup  *CleanupStack
}

// NewCredentialToolCaller 创建 credential actor，并在任何输出写入前登记 secret 脱敏。
func NewCredentialToolCaller(delegate ToolCaller, options CredentialActorOptions) (*CredentialToolCaller, error) {
	if delegate == nil || strings.TrimSpace(options.CampaignID) == "" || options.CredentialValue == "" || options.Redactor == nil {
		return nil, fmt.Errorf("credential actor delegate, campaign_id, credential and redactor are required")
	}
	agentURL, err := canonicalCredentialOrigin(options.AgentURL)
	if err != nil {
		return nil, fmt.Errorf("credential Agent origin: %w", err)
	}
	sidecarURL, err := canonicalCredentialOrigin(options.AuthSidecarURL)
	if err != nil {
		return nil, fmt.Errorf("credential auth sidecar origin: %w", err)
	}
	if err := options.Redactor.RegisterSecret(options.CredentialValue); err != nil {
		return nil, fmt.Errorf("register credential redaction: %w", err)
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
	// POST 请求含 secret，禁止 redirect 把请求重放到其他 origin。
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &CredentialToolCaller{
		delegate: delegate, agent: agentURL, sidecar: sidecarURL, campaign: options.CampaignID,
		secret: options.CredentialValue, http: client, cleanup: options.Cleanup,
	}, nil
}

// CallTool 对 get_debug_credentials 执行完整 lease 闭环，其他 tool 原样透传。
func (c *CredentialToolCaller) CallTool(ctx context.Context, name string, arguments map[string]any) (ToolCallResult, error) {
	if name != "get_debug_credentials" {
		return c.delegate.CallTool(ctx, name, arguments)
	}
	projectID := strings.TrimSpace(fmt.Sprint(arguments["project_id"]))
	serviceID := strings.TrimSpace(fmt.Sprint(arguments["service_id"]))
	if projectID == "" || serviceID == "" {
		return ToolCallResult{}, fmt.Errorf("credential lease requires exact project_id and service_id")
	}
	log := logger.GetLogger().WithEntryName("RuntimeValidationCredential").WithFields(map[string]any{
		"campaign_id": c.campaign, "project_id": projectID, "service_id": serviceID,
	})
	log.Info("开始创建 runtime validation 一次性 credential lease")
	var metadata debugcredential.Metadata
	leaseResourceID := projectID + "/" + serviceID
	var createErr error
	leaseTracked := false
	if c.cleanup == nil {
		metadata, createErr = c.createLease(ctx, projectID, serviceID)
	} else {
		action, acquireErr := c.cleanup.Acquire("credential-lease", leaseResourceID, map[string]any{
			"project_id": projectID, "service_id": serviceID, "state": "absent",
		}, func() (CleanupAction, error) {
			created, err := c.createLease(ctx, projectID, serviceID)
			metadata = created
			if err != nil {
				return nil, err
			}
			return &credentialLeaseCleanupAction{actor: c, id: leaseResourceID, metadata: created}, nil
		})
		leaseTracked = action != nil
		createErr = acquireErr
	}
	// 即使 lease 创建失败也要真实调用 MCP，但最终不会把旧持久凭据误当为 campaign lease PASS。
	result, callErr := c.delegate.CallTool(ctx, name, arguments)
	var readbackErr, sidecarErr error
	if createErr == nil && callErr == nil {
		readbackErr = c.verifyMCPReadback(result)
		if readbackErr == nil {
			sidecarErr = c.verifyAuthSidecar(ctx)
		}
	}
	var deleteErr error
	if metadata.ID != "" {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		if c.cleanup == nil || !leaseTracked {
			deleteErr = c.deleteLease(cleanupCtx, metadata)
		} else {
			deleteErr = c.cleanup.ReleaseTracked(cleanupCtx, "credential-lease", leaseResourceID)
		}
		cancel()
	}
	joined := errors.Join(createErr, callErr, readbackErr, sidecarErr, deleteErr)
	if joined != nil {
		log.WithFields(map[string]any{"lease_created": metadata.ID != "", "delete_attempted": metadata.ID != "", "cause_code": "credential_validation_failed"}).Error("runtime validation credential 闭环失败")
		return result, joined
	}
	log.WithField("lease_id", metadata.ID).Info("runtime validation credential MCP 读回、sidecar 登录与精确删除完成")
	return result, nil
}

func (c *CredentialToolCaller) createLease(ctx context.Context, projectID, serviceID string) (debugcredential.Metadata, error) {
	requestBody := debugcredential.CreateRequest{
		ProjectID: projectID, ServiceID: serviceID, Owner: c.campaign,
		TTLSeconds:  int(runtimeValidationCredentialTTL / time.Second),
		Credentials: []model.DebugCredential{{Name: runtimeValidationCredentialName, Value: c.secret, Desc: runtimeValidationCredentialDesc}},
	}
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return debugcredential.Metadata{}, fmt.Errorf("encode credential lease request")
	}
	defer zeroCredentialBytes(payload)
	endpoint := *c.agent
	endpoint.Path = "/api/debug-credential-leases"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return debugcredential.Metadata{}, fmt.Errorf("create credential lease request")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return debugcredential.Metadata{}, fmt.Errorf("create credential lease: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxCredentialResponseBytes))
		return debugcredential.Metadata{}, fmt.Errorf("create credential lease returned HTTP %d", response.StatusCode)
	}
	metadata, err := decodeCredentialMetadata(response.Body)
	if err != nil {
		return metadata, err
	}
	if err := validateCredentialMetadata(metadata, projectID, serviceID, c.campaign); err != nil {
		return metadata, err
	}
	return metadata, nil
}

func (c *CredentialToolCaller) deleteLease(ctx context.Context, expected debugcredential.Metadata) error {
	endpoint := *c.agent
	endpoint.Path = "/api/debug-credential-leases/" + url.PathEscape(expected.ID)
	query := endpoint.Query()
	query.Set("owner", c.campaign)
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("create credential lease delete request")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("delete credential lease: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		// 精确 lease ID 已不存在时 cleanup 已达到目标；这也保证 released journal 失败后的重试幂等。
		return nil
	}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxCredentialResponseBytes))
		return fmt.Errorf("delete credential lease returned HTTP %d", response.StatusCode)
	}
	actual, err := decodeCredentialMetadata(response.Body)
	if err != nil {
		return err
	}
	if actual.ID != expected.ID || actual.Owner != c.campaign || actual.ProjectID != expected.ProjectID || actual.ServiceID != expected.ServiceID {
		return fmt.Errorf("deleted credential lease identity mismatch")
	}
	return nil
}

type credentialLeaseCleanupAction struct {
	actor    *CredentialToolCaller
	id       string
	metadata debugcredential.Metadata
}

func (a *credentialLeaseCleanupAction) Kind() string { return "credential-lease" }
func (a *credentialLeaseCleanupAction) ID() string   { return a.id }
func (a *credentialLeaseCleanupAction) Release(ctx context.Context) error {
	return a.actor.deleteLease(ctx, a.metadata)
}

func (c *CredentialToolCaller) verifyMCPReadback(result ToolCallResult) error {
	if result.IsError || RawMessageMap(result.StructuredContent)["ok"] == false {
		return fmt.Errorf("get_debug_credentials returned an application error")
	}
	data := structuredData(result)
	credentials, ok := data["credentials"].([]any)
	if !ok {
		return fmt.Errorf("get_debug_credentials did not return credentials")
	}
	for _, raw := range credentials {
		credential := RawMessageMap(raw)
		if credential["name"] == runtimeValidationCredentialName && credential["source"] == "ephemeral_service" && credential["value_present"] == true {
			if value, ok := credential["value"].(string); ok && value == c.secret {
				return nil
			}
			return fmt.Errorf("get_debug_credentials returned mismatched credential value")
		}
	}
	return fmt.Errorf("get_debug_credentials did not return the campaign service lease")
}

func (c *CredentialToolCaller) verifyAuthSidecar(ctx context.Context) error {
	endpoint := *c.sidecar
	endpoint.Path = "/login"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("create auth sidecar login request")
	}
	request.Header.Set("Authorization", "Bearer "+c.secret)
	request.Header.Set("X-Runtime-Validation-Campaign", c.campaign)
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("auth sidecar login: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxCredentialResponseBytes))
		return fmt.Errorf("auth sidecar login returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		OK         bool   `json:"ok"`
		CampaignID string `json:"campaign_id"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxCredentialResponseBytes)).Decode(&payload); err != nil || !payload.OK || payload.CampaignID != c.campaign {
		return fmt.Errorf("auth sidecar login identity mismatch")
	}
	return nil
}

func decodeCredentialMetadata(reader io.Reader) (debugcredential.Metadata, error) {
	var metadata debugcredential.Metadata
	decoder := json.NewDecoder(io.LimitReader(reader, maxCredentialResponseBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		// 错误不包装原 JSON，避免上游错误反射 secret。
		return metadata, fmt.Errorf("decode sanitized credential lease metadata failed")
	}
	return metadata, nil
}

func validateCredentialMetadata(metadata debugcredential.Metadata, projectID, serviceID, owner string) error {
	if metadata.ID == "" || metadata.ProjectID != projectID || metadata.ServiceID != serviceID || metadata.Owner != owner || metadata.Count != 1 || metadata.ExpiresAtUTC.IsZero() || len(metadata.Hints) != 1 {
		return fmt.Errorf("credential lease metadata identity mismatch")
	}
	hint := metadata.Hints[0]
	if hint.Name != runtimeValidationCredentialName || hint.Desc != runtimeValidationCredentialDesc || hint.Source != "ephemeral_service" {
		return fmt.Errorf("credential lease hint contract mismatch")
	}
	return nil
}

func canonicalCredentialOrigin(raw string) (*url.URL, error) {
	canonical, err := canonicalLoopbackAgentURL(raw)
	if err != nil {
		return nil, err
	}
	return url.Parse(canonical)
}

func zeroCredentialBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
