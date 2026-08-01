// credential_lease.go 让 Windows driver 通过正式 Agent HTTP interface 使用进程内凭据 lease。
//
// 职责：
//   - 在 campaign project/service 建立后创建人类显式输入的临时凭据 lease
//   - 校验 Agent 只返回脱敏 metadata，并按 lease ID + owner 精确删除
//   - 为创建、删除和失败路径留下不含 secret/hash/token 的结构化日志
//
// 边界：
//   - 不生成凭据，不从 runtime input 读取凭据，也不把请求体写入证据
//   - 不新增 MCP tool；明文读取仍通过冻结的 get_debug_credentials
//   - 不在 HTTP 错误中透传响应 body，防止上游误反射 secret
package windowsvalidation

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
	validationCredentialName = "windows_validation_credential"
	validationCredentialDesc = "一次性 Windows validation 调试凭据"
	validationCredentialTTL  = 60 * time.Minute
	maxLeaseResponseBytes    = 64 * 1024
)

type credentialLeaseClient interface {
	Create(ctx context.Context, projectID, serviceID, owner, value string) (debugcredential.Metadata, error)
	Delete(ctx context.Context, leaseID, owner string) error
}

type credentialLeaseHTTPClient struct {
	baseURL *url.URL
	http    *http.Client
	token   string
}

type credentialLeaseToolCaller struct {
	delegate mcpToolCaller
	leases   credentialLeaseClient
	owner    string
	value    string
}

func newCredentialLeaseToolCaller(delegate mcpToolCaller, leases credentialLeaseClient, owner, value string, redactor *Redactor) (mcpToolCaller, error) {
	if delegate == nil || leases == nil || strings.TrimSpace(owner) == "" || strings.TrimSpace(value) == "" || redactor == nil {
		return nil, fmt.Errorf("Windows validation credential lease caller inputs are incomplete")
	}
	// 人类输入必须在任何 MCP response/error 进入证据前登记；别名只按进程序号生成，不由 secret hash 派生。
	redactor.RegisterSecret("DEBUG_CREDENTIAL", value)
	return &credentialLeaseToolCaller{delegate: delegate, leases: leases, owner: owner, value: value}, nil
}

func (c *credentialLeaseToolCaller) CallTool(ctx context.Context, name string, arguments map[string]any) (ToolCallResult, error) {
	if name != "get_debug_credentials" {
		return c.delegate.CallTool(ctx, name, arguments)
	}
	projectID := toolArgumentString(arguments["project_id"])
	serviceID := toolArgumentString(arguments["service_id"])
	metadata, createErr := c.leases.Create(ctx, projectID, serviceID, c.owner, c.value)
	// 即使 lease 创建失败也真实调用冻结 MCP 工具，避免把未调用的工具误记为 attempted=true。
	result, callErr := c.delegate.CallTool(ctx, name, arguments)
	var deleteErr error
	if metadata.ID != "" {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		deleteErr = c.leases.Delete(cleanupCtx, metadata.ID, c.owner)
		cancel()
	}
	return result, errors.Join(createErr, callErr, deleteErr)
}

// newCredentialLeaseHTTPClient 创建直连 Agent 的 credential lease HTTP client。
//
// 参数：
//   - agentURL: disposable/已安装 Agent 的 HTTP 基地址
//   - httpClient: 可选 HTTP client；为空时使用 15s 超时默认 client
//   - token: Agent 的本机访问 token（security.ReadLocalToken）；鉴权常开后
//     /api/debug-credential-leases* 是受保护端点，空串表示调用方未解析到凭据
//     （多为单测），请求会保持裸发
func newCredentialLeaseHTTPClient(agentURL string, httpClient *http.Client, token string) (*credentialLeaseHTTPClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(agentURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" || parsed.RawPath != "" {
		return nil, fmt.Errorf("agent URL is invalid for credential lease")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	} else {
		clone := *httpClient
		httpClient = &clone
		if httpClient.Timeout == 0 {
			httpClient.Timeout = 15 * time.Second
		}
	}
	// lease 创建请求含人类输入的 secret；禁止 HTTP redirect 把请求体重放到另一个 origin。
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &credentialLeaseHTTPClient{baseURL: parsed, http: httpClient, token: strings.TrimSpace(token)}, nil
}

func (c *credentialLeaseHTTPClient) Create(ctx context.Context, projectID, serviceID, owner, value string) (debugcredential.Metadata, error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationCredentialLease")
	fields := map[string]any{"project_id": projectID, "service_id": serviceID, "owner": owner, "ttl_seconds": int(validationCredentialTTL / time.Second)}
	log.WithFields(fields).Info("开始创建 Windows validation 进程内调试凭据 lease")
	reqBody := debugcredential.CreateRequest{
		ProjectID: projectID, ServiceID: serviceID, Owner: owner,
		TTLSeconds:  int(validationCredentialTTL / time.Second),
		Credentials: []model.DebugCredential{{Name: validationCredentialName, Value: value, Desc: validationCredentialDesc}},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		log.WithErr(err).WithFields(fields).Error("编码 Windows validation 调试凭据 lease 请求失败")
		return debugcredential.Metadata{}, fmt.Errorf("encode credential lease request: %w", err)
	}
	defer zeroBytes(payload)

	endpoint := *c.baseURL
	endpoint.Path += "/api/debug-credential-leases"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		log.WithErr(err).WithFields(fields).Error("创建 Windows validation 调试凭据 lease HTTP 请求失败")
		return debugcredential.Metadata{}, fmt.Errorf("create credential lease request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// /api/debug-credential-leases 是受保护端点（鉴权常开），创建的是进程内凭据 lease
	// 这类受保护资源，按 Step 5 规则带上 Agent 的本机访问 token。
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		log.WithErr(err).WithFields(fields).Error("Windows validation 调试凭据 lease HTTP 创建失败")
		return debugcredential.Metadata{}, fmt.Errorf("create credential lease: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxLeaseResponseBytes))
		err := fmt.Errorf("create credential lease returned HTTP %d", resp.StatusCode)
		log.WithErr(err).WithFields(fields).Error("Windows validation 调试凭据 lease 被 Agent 拒绝")
		return debugcredential.Metadata{}, err
	}
	metadata, err := decodeCredentialLeaseMetadata(resp.Body)
	if err != nil {
		log.WithErr(err).WithFields(fields).Error("Windows validation 调试凭据 lease metadata 无效")
		return debugcredential.Metadata{}, err
	}
	if err := validateCreatedCredentialLease(metadata, projectID, serviceID, owner); err != nil {
		log.WithErr(err).WithFields(fields).Error("Windows validation 调试凭据 lease 身份不匹配")
		return debugcredential.Metadata{}, err
	}
	log.WithFields(map[string]any{
		"lease_id": metadata.ID, "project_id": metadata.ProjectID, "service_id": metadata.ServiceID,
		"owner": metadata.Owner, "count": metadata.Count, "expires_at_utc": metadata.ExpiresAtUTC,
	}).Info("Windows validation 进程内调试凭据 lease 创建完成")
	return metadata, nil
}

func (c *credentialLeaseHTTPClient) Delete(ctx context.Context, leaseID, owner string) error {
	log := logger.GetLogger().WithEntryName("WindowsValidationCredentialLease")
	fields := map[string]any{"lease_id": leaseID, "owner": owner}
	log.WithFields(fields).Info("开始精确删除 Windows validation 进程内调试凭据 lease")
	endpoint := *c.baseURL
	endpoint.Path += "/api/debug-credential-leases/" + url.PathEscape(leaseID)
	query := endpoint.Query()
	query.Set("owner", owner)
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint.String(), nil)
	if err != nil {
		log.WithErr(err).WithFields(fields).Error("创建 Windows validation 调试凭据 lease 删除请求失败")
		return fmt.Errorf("create credential lease delete request: %w", err)
	}
	// 同上：精确删除同一个 lease 资源，同样需要 Agent 的本机访问 token。
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		log.WithErr(err).WithFields(fields).Error("Windows validation 调试凭据 lease HTTP 删除失败")
		return fmt.Errorf("delete credential lease: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxLeaseResponseBytes))
		err := fmt.Errorf("delete credential lease returned HTTP %d", resp.StatusCode)
		log.WithErr(err).WithFields(fields).Error("Windows validation 调试凭据 lease 精确删除被 Agent 拒绝")
		return err
	}
	metadata, err := decodeCredentialLeaseMetadata(resp.Body)
	if err != nil {
		log.WithErr(err).WithFields(fields).Error("Windows validation 调试凭据 lease 删除响应无效")
		return err
	}
	if metadata.ID != leaseID || metadata.Owner != owner {
		err := fmt.Errorf("deleted credential lease identity mismatch")
		log.WithErr(err).WithFields(fields).Error("Windows validation 调试凭据 lease 删除身份不匹配")
		return err
	}
	log.WithFields(map[string]any{"lease_id": metadata.ID, "owner": metadata.Owner, "project_id": metadata.ProjectID, "service_id": metadata.ServiceID, "count": metadata.Count}).Info("Windows validation 进程内调试凭据 lease 删除完成")
	return nil
}

func decodeCredentialLeaseMetadata(body io.Reader) (debugcredential.Metadata, error) {
	var metadata debugcredential.Metadata
	decoder := json.NewDecoder(io.LimitReader(body, maxLeaseResponseBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		// 不包装原始 JSON；上游即使误回显 secret，也不能进入日志或最终错误。
		return metadata, fmt.Errorf("decode sanitized credential lease metadata failed")
	}
	return metadata, nil
}

func validateCreatedCredentialLease(metadata debugcredential.Metadata, projectID, serviceID, owner string) error {
	if metadata.ID == "" || metadata.ProjectID != projectID || metadata.ServiceID != serviceID || metadata.Owner != owner || metadata.ExpiresAtUTC.IsZero() || metadata.Count != 1 || len(metadata.Hints) != 1 {
		return fmt.Errorf("created credential lease metadata does not match request")
	}
	hint := metadata.Hints[0]
	expectedSource := "ephemeral_project"
	if serviceID != "" {
		expectedSource = "ephemeral_service"
	}
	if hint.Name != validationCredentialName || hint.Desc != validationCredentialDesc || hint.Source != expectedSource {
		return fmt.Errorf("created credential lease hint contract mismatch")
	}
	return nil
}

func toolArgumentString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func zeroBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
