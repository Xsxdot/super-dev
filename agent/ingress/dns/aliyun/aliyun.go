// Package aliyun 提供阿里云 DNS provider。
//
// 职责：
//   - 将 ingress.Record 收敛到阿里云云解析 DNS API
//   - 支持记录查询、创建、更新和删除
//
// 边界：
//   - 不保存 AccessKey，凭据由上层配置注入
//   - 不处理域名注册商或解析线路策略
package aliyun

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/ingress"
)

const defaultBaseURL = "https://alidns.aliyuncs.com/"

// Config 描述阿里云 DNS provider 的运行配置。
type Config struct {
	Name            string
	AccessKeyID     string
	AccessKeySecret string
	BaseURL         string
	Client          *http.Client
	Signer          Signer
}

// Signer 定义阿里云 RPC API 签名能力。
type Signer interface {
	// Sign 对待提交参数计算签名。
	//
	// 参数：
	//   - values: 不包含 Signature 的请求参数
	//   - secret: AccessKeySecret
	//
	// 返回：
	//   - 签名字符串
	Sign(values url.Values, secret string) string
}

// Provider 通过阿里云云解析 DNS API 收敛 DNS 记录。
type Provider struct {
	name            string
	accessKeyID     string
	accessKeySecret string
	baseURL         string
	client          *http.Client
	signer          Signer
}

var _ ingress.DnsProvider = (*Provider)(nil)

// New 创建阿里云 DNS provider。
//
// 参数：
//   - cfg: provider 名称、AccessKey、可选 HTTP client 和 signer
//
// 返回：
//   - 阿里云 Provider 实例
func New(cfg Config) *Provider {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	signer := cfg.Signer
	if signer == nil {
		signer = defaultSigner{}
	}
	name := cfg.Name
	if name == "" {
		name = "aliyun"
	}
	return &Provider{
		name:            name,
		accessKeyID:     cfg.AccessKeyID,
		accessKeySecret: cfg.AccessKeySecret,
		baseURL:         baseURL,
		client:          client,
		signer:          signer,
	}
}

// Name 返回 provider 注册名。
//
// 返回：
//   - 配置中的 provider 名称
func (p *Provider) Name() string {
	return p.name
}

// EnsureRecord 幂等创建或更新一条阿里云 DNS 记录。
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
//   - 完整记录名会拆成 DomainName 与 RR，例如 api.example.com -> example.com/api
func (p *Provider) EnsureRecord(ctx context.Context, record ingress.Record) (ingress.RecordResult, error) {
	records, err := p.ListRecords(ctx, record.Name)
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
		updatedID, err := p.updateRecord(ctx, existing.ID, record)
		if err != nil {
			return ingress.RecordResult{}, err
		}
		record.ID = updatedID
		return ingress.RecordResult{Record: record, Changed: true}, nil
	}

	createdID, err := p.addRecord(ctx, record)
	if err != nil {
		return ingress.RecordResult{}, err
	}
	record.ID = createdID
	return ingress.RecordResult{Record: record, Changed: true}, nil
}

// ListRecords 查询指定完整域名的阿里云 DNS 记录。
//
// 参数：
//   - ctx: 上下文，用于取消 HTTP 请求
//   - domain: 完整记录名，如 api.example.com
//
// 返回：
//   - 阿里云返回的 DNS 记录列表
//   - 请求或响应解析失败时返回错误
func (p *Provider) ListRecords(ctx context.Context, domain string) ([]ingress.Record, error) {
	domainName, _ := splitRecordName(domain)
	params := url.Values{}
	params.Set("DomainName", domainName)

	var resp describeResponse
	if err := p.call(ctx, "DescribeDomainRecords", params, &resp); err != nil {
		return nil, err
	}

	records := make([]ingress.Record, 0, len(resp.DomainRecords.Record))
	for _, item := range resp.DomainRecords.Record {
		record := item.toIngress(domainName)
		if strings.TrimSpace(domain) == "" || record.Name == domain {
			records = append(records, record)
		}
	}
	return records, nil
}

// RemoveRecord 删除指定 ID 的阿里云 DNS 记录。
//
// 参数：
//   - ctx: 上下文，用于取消 HTTP 请求
//   - record: 待删除记录；必须包含 ID
//
// 返回：
//   - 删除失败时返回错误
func (p *Provider) RemoveRecord(ctx context.Context, record ingress.Record) error {
	if strings.TrimSpace(record.ID) == "" {
		return errors.New("aliyun record id is required")
	}
	params := url.Values{}
	params.Set("RecordId", record.ID)
	var resp recordIDResponse
	return p.call(ctx, "DeleteDomainRecord", params, &resp)
}

func (p *Provider) addRecord(ctx context.Context, record ingress.Record) (string, error) {
	domainName, rr := splitRecordName(record.Name)
	params := url.Values{}
	params.Set("DomainName", domainName)
	params.Set("RR", rr)
	params.Set("Type", string(record.Type))
	params.Set("Value", record.Value)
	if record.TTL > 0 {
		params.Set("TTL", strconv.Itoa(record.TTL))
	}

	var resp recordIDResponse
	if err := p.call(ctx, "AddDomainRecord", params, &resp); err != nil {
		return "", err
	}
	return resp.RecordID, nil
}

func (p *Provider) updateRecord(ctx context.Context, recordID string, record ingress.Record) (string, error) {
	_, rr := splitRecordName(record.Name)
	params := url.Values{}
	params.Set("RecordId", recordID)
	params.Set("RR", rr)
	params.Set("Type", string(record.Type))
	params.Set("Value", record.Value)
	if record.TTL > 0 {
		params.Set("TTL", strconv.Itoa(record.TTL))
	}

	var resp recordIDResponse
	if err := p.call(ctx, "UpdateDomainRecord", params, &resp); err != nil {
		return "", err
	}
	return resp.RecordID, nil
}

func (p *Provider) call(ctx context.Context, action string, params url.Values, out any) error {
	values := url.Values{}
	for key, vals := range params {
		for _, value := range vals {
			values.Add(key, value)
		}
	}
	values.Set("Action", action)
	values.Set("Format", "JSON")
	values.Set("Version", "2015-01-09")
	values.Set("AccessKeyId", p.accessKeyID)
	values.Set("SignatureMethod", "HMAC-SHA1")
	values.Set("SignatureVersion", "1.0")
	values.Set("SignatureNonce", fmt.Sprintf("%d", time.Now().UnixNano()))
	values.Set("Timestamp", time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	values.Set("Signature", p.signer.Sign(values, p.accessKeySecret))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("aliyun API %s failed with status %d", action, resp.StatusCode)
	}
	var apiErr struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	}
	if err := json.Unmarshal(data, &apiErr); err == nil && apiErr.Code != "" {
		if apiErr.Message != "" {
			return errors.New(apiErr.Message)
		}
		return errors.New(apiErr.Code)
	}
	return json.Unmarshal(data, out)
}

type describeResponse struct {
	DomainRecords struct {
		Record []aliRecord `json:"Record"`
	} `json:"DomainRecords"`
}

type recordIDResponse struct {
	RecordID string `json:"RecordId"`
}

type aliRecord struct {
	RecordID string `json:"RecordId"`
	RR       string `json:"RR"`
	Type     string `json:"Type"`
	Value    string `json:"Value"`
	TTL      int    `json:"TTL"`
}

func (r aliRecord) toIngress(domainName string) ingress.Record {
	name := domainName
	if r.RR != "@" {
		name = r.RR + "." + domainName
	}
	return ingress.Record{
		ID:    r.RecordID,
		Type:  ingress.RecordType(r.Type),
		Name:  name,
		Value: r.Value,
		TTL:   r.TTL,
	}
}

func splitRecordName(name string) (string, string) {
	parts := strings.Split(strings.Trim(strings.TrimSpace(name), "."), ".")
	if len(parts) <= 2 {
		return strings.Join(parts, "."), "@"
	}
	domainName := strings.Join(parts[len(parts)-2:], ".")
	rr := strings.Join(parts[:len(parts)-2], ".")
	return domainName, rr
}

func recordsMatch(existing ingress.Record, desired ingress.Record) bool {
	if existing.Value != desired.Value {
		return false
	}
	return desired.TTL <= 0 || existing.TTL == desired.TTL
}

type defaultSigner struct{}

func (defaultSigner) Sign(values url.Values, secret string) string {
	canonical := canonicalQuery(values)
	stringToSign := http.MethodPost + "&%2F&" + percentEncode(canonical)
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	_, _ = mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func canonicalQuery(values url.Values) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	pairs := make([]string, 0)
	for _, key := range keys {
		vals := append([]string(nil), values[key]...)
		sort.Strings(vals)
		for _, value := range vals {
			pairs = append(pairs, percentEncode(key)+"="+percentEncode(value))
		}
	}
	return strings.Join(pairs, "&")
}

func percentEncode(value string) string {
	encoded := url.QueryEscape(value)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}
