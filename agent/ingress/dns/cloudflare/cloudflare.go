// Package cloudflare 提供 Cloudflare DNS provider。
//
// 职责：
//   - 将 ingress.Record 收敛到 Cloudflare DNS Records API
//   - 支持记录查询、创建、更新和删除
//
// 边界：
//   - 不保存 API Token，凭据由上层配置注入
//   - 不参与证书签发流程，只提供通用 DNS 记录能力
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/superdev/agent/ingress"
)

const defaultBaseURL = "https://api.cloudflare.com"

// Config 描述 Cloudflare DNS provider 的运行配置。
type Config struct {
	Name     string
	ZoneID   string
	APIToken string
	BaseURL  string
	Client   *http.Client
}

// Provider 通过 Cloudflare DNS Records API 收敛 DNS 记录。
type Provider struct {
	name      string
	zoneID    string
	apiToken  string
	baseURL   string
	client    *http.Client
	zoneMu    sync.Mutex
	zoneCache map[string]string
}

var _ ingress.DnsProvider = (*Provider)(nil)

// New 创建 Cloudflare DNS provider。
//
// 参数：
//   - cfg: provider 名称、ZoneID、API Token 和可选 HTTP client
//
// 返回：
//   - Cloudflare Provider 实例
func New(cfg Config) *Provider {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	name := cfg.Name
	if name == "" {
		name = "cloudflare"
	}
	return &Provider{
		name:      name,
		zoneID:    cfg.ZoneID,
		apiToken:  cfg.APIToken,
		baseURL:   baseURL,
		client:    client,
		zoneCache: map[string]string{},
	}
}

// Name 返回 provider 注册名。
//
// 返回：
//   - 配置中的 provider 名称
func (p *Provider) Name() string {
	return p.name
}

// EnsureRecord 幂等创建或更新一条 Cloudflare DNS 记录。
//
// 参数：
//   - ctx: 上下文，用于取消 HTTP 请求
//   - record: 期望存在的 DNS 记录
//
// 返回：
//   - DNS 收敛结果，包含远端记录 ID 和是否变更
//   - 查询、创建或更新失败时返回错误
//
// 注意：
//   - 只匹配同 type/name 的记录，避免改动其他业务记录
func (p *Provider) EnsureRecord(ctx context.Context, record ingress.Record) (ingress.RecordResult, error) {
	zoneID, err := p.zoneIDForRecord(ctx, record.Name)
	if err != nil {
		return ingress.RecordResult{}, err
	}
	records, err := p.listRecords(ctx, record.Name, zoneID)
	if err != nil {
		return ingress.RecordResult{}, err
	}
	for _, existing := range records {
		if existing.Type != record.Type || existing.Name != record.Name {
			continue
		}
		if recordsMatch(existing, record) {
			record.ID = existing.ID
			return ingress.RecordResult{Record: record, Changed: false}, nil
		}
		updated, err := p.writeRecord(ctx, http.MethodPut, "/client/v4/zones/"+url.PathEscape(zoneID)+"/dns_records/"+url.PathEscape(existing.ID), record)
		if err != nil {
			return ingress.RecordResult{}, err
		}
		record.ID = updated.ID
		return ingress.RecordResult{Record: record, Changed: true}, nil
	}

	created, err := p.writeRecord(ctx, http.MethodPost, "/client/v4/zones/"+url.PathEscape(zoneID)+"/dns_records", record)
	if err != nil {
		return ingress.RecordResult{}, err
	}
	record.ID = created.ID
	return ingress.RecordResult{Record: record, Changed: true}, nil
}

// ListRecords 查询指定完整域名的 Cloudflare DNS 记录。
//
// 参数：
//   - ctx: 上下文，用于取消 HTTP 请求
//   - domain: 完整记录名，如 api.example.com
//
// 返回：
//   - Cloudflare 返回的 DNS 记录列表
//   - 请求或响应解析失败时返回错误
func (p *Provider) ListRecords(ctx context.Context, domain string) ([]ingress.Record, error) {
	zoneID, err := p.zoneIDForRecord(ctx, domain)
	if err != nil {
		return nil, err
	}
	return p.listRecords(ctx, domain, zoneID)
}

func (p *Provider) listRecords(ctx context.Context, domain string, zoneID string) ([]ingress.Record, error) {
	query := url.Values{}
	if strings.TrimSpace(domain) != "" {
		query.Set("name", domain)
	}
	resp, err := cfDo[[]cfRecord](ctx, p, http.MethodGet, "/client/v4/zones/"+url.PathEscape(zoneID)+"/dns_records", query, nil)
	if err != nil {
		return nil, err
	}
	records := make([]ingress.Record, 0, len(resp))
	for _, item := range resp {
		records = append(records, item.toIngress())
	}
	return records, nil
}

// RemoveRecord 删除指定 ID 的 Cloudflare DNS 记录。
//
// 参数：
//   - ctx: 上下文，用于取消 HTTP 请求
//   - record: 待删除记录；必须包含 ID
//
// 返回：
//   - 删除失败时返回错误
func (p *Provider) RemoveRecord(ctx context.Context, record ingress.Record) error {
	if strings.TrimSpace(record.ID) == "" {
		return errors.New("cloudflare record id is required")
	}
	zoneID, err := p.zoneIDForRecord(ctx, record.Name)
	if err != nil {
		return err
	}
	_, err = cfDo[cfRecord](ctx, p, http.MethodDelete, "/client/v4/zones/"+url.PathEscape(zoneID)+"/dns_records/"+url.PathEscape(record.ID), nil, nil)
	return err
}

func (p *Provider) writeRecord(ctx context.Context, method string, endpoint string, record ingress.Record) (cfRecord, error) {
	payload := map[string]any{
		"type":    string(record.Type),
		"name":    record.Name,
		"content": record.Value,
	}
	if record.TTL > 0 {
		payload["ttl"] = record.TTL
	}
	return cfDo[cfRecord](ctx, p, method, endpoint, nil, payload)
}

type cfRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
}

type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (r cfRecord) toIngress() ingress.Record {
	return ingress.Record{
		ID:    r.ID,
		Type:  ingress.RecordType(r.Type),
		Name:  r.Name,
		Value: r.Content,
		TTL:   r.TTL,
	}
}

func (p *Provider) zoneIDForRecord(ctx context.Context, recordName string) (string, error) {
	if strings.TrimSpace(p.zoneID) != "" {
		return p.zoneID, nil
	}
	candidates := zoneCandidates(recordName)
	for _, candidate := range candidates {
		p.zoneMu.Lock()
		cached := p.zoneCache[candidate]
		p.zoneMu.Unlock()
		if cached != "" {
			return cached, nil
		}

		query := url.Values{}
		query.Set("name", candidate)
		zones, err := cfDo[[]cfZone](ctx, p, http.MethodGet, "/client/v4/zones", query, nil)
		if err != nil {
			return "", err
		}
		if len(zones) == 0 || strings.TrimSpace(zones[0].ID) == "" {
			continue
		}
		p.zoneMu.Lock()
		p.zoneCache[candidate] = zones[0].ID
		p.zoneMu.Unlock()
		return zones[0].ID, nil
	}
	return "", fmt.Errorf("cloudflare zone id could not be discovered for %s; ensure the API token has Zone Zone Read permission or configure zone_id", recordName)
}

func zoneCandidates(recordName string) []string {
	parts := strings.Split(strings.Trim(strings.ToLower(strings.TrimSpace(recordName)), "."), ".")
	if len(parts) < 2 {
		return nil
	}
	out := make([]string, 0, len(parts)-1)
	for i := 0; i < len(parts)-1; i++ {
		out = append(out, strings.Join(parts[i:], "."))
	}
	return out
}

type cfResponse[T any] struct {
	Success bool `json:"success"`
	Result  T    `json:"result"`
	Errors  []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func cfDo[T any](ctx context.Context, provider *Provider, method string, endpoint string, query url.Values, body any) (T, error) {
	var zero T
	reqURL, err := url.Parse(provider.baseURL + endpoint)
	if err != nil {
		return zero, err
	}
	if query != nil {
		reqURL.RawQuery = query.Encode()
	}

	var bodyReader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return zero, err
		}
		bodyReader = bytes.NewReader(data)
	} else {
		bodyReader = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL.String(), bodyReader)
	if err != nil {
		return zero, err
	}
	req.Header.Set("Authorization", "Bearer "+provider.apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := provider.client.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()

	var decoded cfResponse[T]
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return zero, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zero, fmt.Errorf("cloudflare API %s failed with status %d", endpoint, resp.StatusCode)
	}
	if !decoded.Success {
		if len(decoded.Errors) > 0 && decoded.Errors[0].Message != "" {
			return zero, errors.New(decoded.Errors[0].Message)
		}
		return zero, errors.New("cloudflare API returned success=false")
	}
	return decoded.Result, nil
}

func recordsMatch(existing ingress.Record, desired ingress.Record) bool {
	if existing.Value != desired.Value {
		return false
	}
	return desired.TTL <= 0 || existing.TTL == desired.TTL
}
