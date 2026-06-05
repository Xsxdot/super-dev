// Package acme 提供基于 ACME DNS-01 的证书 provider。
//
// 职责：
//   - 通过 DnsProvider 写入和清理 _acme-challenge TXT 记录
//   - 通过 lego 客户端申请和续期证书
//
// 边界：
//   - 不直接操作 DNS API，所有 DNS 写入通过 DnsProvider
//   - 不负责把证书分发到 host，分发由 proxy provider 完成
package acme

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
	"github.com/xsxdot/super-dev/agent/ingress"
)

// Client 定义 ACME 证书申请和续期能力。
type Client interface {
	// Obtain 申请一张域名证书。
	//
	// 参数：
	//   - ctx: 上下文，用于 DNS 回调取消
	//   - domains: 待申请证书覆盖的域名列表
	//   - present: 写入 DNS-01 TXT 的回调
	//   - cleanup: 清理 DNS-01 TXT 的回调
	//
	// 返回：
	//   - 申请成功后的证书材料
	//   - 申请失败时返回错误
	Obtain(ctx context.Context, domains []string, present func(name string, value string) error, cleanup func(name string, value string) error) (ingress.Certificate, error)
	// Renew 续期一张已有证书。
	//
	// 参数：
	//   - ctx: 上下文，用于 DNS 回调取消
	//   - cert: 待续期证书
	//   - domains: 续期证书覆盖的域名列表
	//   - present: 写入 DNS-01 TXT 的回调
	//   - cleanup: 清理 DNS-01 TXT 的回调
	//
	// 返回：
	//   - 续期后的证书材料
	//   - 续期失败时返回错误
	Renew(ctx context.Context, cert ingress.Certificate, domains []string, present func(name string, value string) error, cleanup func(name string, value string) error) (ingress.Certificate, error)
}

// AccountGetter 返回当前全局 ACME 账号配置。
type AccountGetter func() ingress.ACMEAccount

// Provider 使用 ACME DNS-01 管理证书。
type Provider struct {
	client        Client
	accountGetter AccountGetter
}

var _ ingress.CertProvider = (*Provider)(nil)

// New 创建生产 ACME provider。
//
// 参数：
//   - accountGetter: 运行时读取全局 ACME 账号的函数
//
// 返回：
//   - ACME Provider 实例
func New(accountGetter AccountGetter) *Provider {
	return &Provider{accountGetter: accountGetter}
}

// NewWithClient 创建带测试 seam 的 ACME provider。
//
// 参数：
//   - client: 注入的 ACME client
//
// 返回：
//   - ACME Provider 实例
func NewWithClient(client Client) *Provider {
	return &Provider{client: client}
}

// Name 返回 provider 注册名。
//
// 返回：
//   - 固定值 acme
func (p *Provider) Name() string {
	return ingress.ProviderACME
}

// Obtain 首次申请域名证书。
//
// 参数：
//   - ctx: 上下文，用于取消 DNS 回调
//   - domains: 待申请证书覆盖的域名列表
//   - dns: 用于 DNS-01 的 DNS provider
//
// 返回：
//   - 申请成功后的证书材料
//   - DNS 写入、DNS 清理或 ACME 申请失败时返回错误
func (p *Provider) Obtain(ctx context.Context, domains []string, dns ingress.DnsProvider) (ingress.Certificate, error) {
	primary := firstDomain(domains, "")
	if primary == "" {
		return ingress.Certificate{}, errors.New("at least one domain is required")
	}
	client, err := p.clientForRequest()
	if err != nil {
		return ingress.Certificate{}, err
	}
	present, cleanup, err := dnsCallbacks(ctx, dns)
	if err != nil {
		return ingress.Certificate{}, err
	}
	cert, err := client.Obtain(ctx, domains, present, cleanup)
	if err != nil {
		return ingress.Certificate{}, err
	}
	return normalizeCertificate(cert, primary), nil
}

// Renew 续期已有托管证书。
//
// 参数：
//   - ctx: 上下文，用于取消 DNS 回调
//   - cert: 待续期证书
//   - domains: 续期证书覆盖的域名列表
//   - dns: 用于 DNS-01 的 DNS provider
//
// 返回：
//   - 续期后的证书材料
//   - DNS 写入、DNS 清理或 ACME 续期失败时返回错误
func (p *Provider) Renew(ctx context.Context, cert ingress.Certificate, domains []string, dns ingress.DnsProvider) (ingress.Certificate, error) {
	primary := firstDomain(domains, cert.Domain)
	if primary == "" {
		return ingress.Certificate{}, errors.New("at least one domain is required")
	}
	client, err := p.clientForRequest()
	if err != nil {
		return ingress.Certificate{}, err
	}
	present, cleanup, err := dnsCallbacks(ctx, dns)
	if err != nil {
		return ingress.Certificate{}, err
	}
	renewed, err := client.Renew(ctx, cert, domains, present, cleanup)
	if err != nil {
		return ingress.Certificate{}, err
	}
	return normalizeCertificate(renewed, primary), nil
}

// ExpiresAt 返回证书过期时间。
//
// 参数：
//   - cert: 待读取证书
//
// 返回：
//   - 证书过期时间
func (p *Provider) ExpiresAt(cert ingress.Certificate) time.Time {
	return cert.ExpiresAt
}

func (p *Provider) clientForRequest() (Client, error) {
	if p.client != nil {
		return p.client, nil
	}
	if p.accountGetter == nil {
		return nil, errors.New("acme account getter is required")
	}
	account := p.accountGetter()
	if strings.TrimSpace(account.Email) == "" {
		return nil, errors.New("acme account email is required")
	}
	return newLegoClient(account.Email, account.DirectoryURL), nil
}

func dnsCallbacks(ctx context.Context, dns ingress.DnsProvider) (func(string, string) error, func(string, string) error, error) {
	if dns == nil {
		return nil, nil, errors.New("dns provider is required")
	}

	var mu sync.Mutex
	created := map[string]ingress.Record{}

	present := func(name string, value string) error {
		record := ingress.Record{Type: ingress.RecordTXT, Name: trimFQDN(name), Value: value, TTL: dns01.DefaultTTL}
		result, err := dns.EnsureRecord(ctx, record)
		if err != nil {
			return err
		}
		stored := result.Record
		if stored.Type == "" {
			stored.Type = record.Type
		}
		if stored.Name == "" {
			stored.Name = record.Name
		}
		if stored.Value == "" {
			stored.Value = record.Value
		}
		if stored.TTL == 0 {
			stored.TTL = record.TTL
		}
		mu.Lock()
		created[challengeKey(record.Name, record.Value)] = stored
		mu.Unlock()
		return nil
	}

	cleanup := func(name string, value string) error {
		record := ingress.Record{Type: ingress.RecordTXT, Name: trimFQDN(name), Value: value, TTL: dns01.DefaultTTL}
		mu.Lock()
		if stored, ok := created[challengeKey(record.Name, record.Value)]; ok {
			record = stored
		}
		mu.Unlock()
		return dns.RemoveRecord(ctx, record)
	}
	return present, cleanup, nil
}

func normalizeCertificate(cert ingress.Certificate, fallbackDomain string) ingress.Certificate {
	if cert.Domain == "" {
		cert.Domain = fallbackDomain
	}
	if cert.Provider == "" {
		cert.Provider = ingress.ProviderACME
	}
	if cert.ObtainedAt.IsZero() {
		cert.ObtainedAt = time.Now()
	}
	return cert
}

func challengeKey(name string, value string) string {
	return trimFQDN(name) + "\x00" + value
}

func trimFQDN(name string) string {
	return strings.TrimSuffix(strings.TrimSpace(name), ".")
}

type legoClient struct {
	mu        sync.Mutex
	account   *legoAccount
	directory string
	initErr   error
}

func newLegoClient(email string, directoryURL string) *legoClient {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if directoryURL == "" {
		directoryURL = lego.LEDirectoryProduction
	}
	return &legoClient{
		account:   &legoAccount{email: email, key: key},
		directory: directoryURL,
		initErr:   err,
	}
}

func (c *legoClient) Obtain(ctx context.Context, domains []string, present func(string, string) error, cleanup func(string, string) error) (ingress.Certificate, error) {
	if err := ctx.Err(); err != nil {
		return ingress.Certificate{}, err
	}
	primary := firstDomain(domains, "")
	if primary == "" {
		return ingress.Certificate{}, errors.New("at least one domain is required")
	}
	client, err := c.client(present, cleanup)
	if err != nil {
		return ingress.Certificate{}, err
	}
	resource, err := client.Certificate.Obtain(certificate.ObtainRequest{Domains: domains, Bundle: true})
	if err != nil {
		return ingress.Certificate{}, err
	}
	return resourceToCertificate(primary, resource)
}

func (c *legoClient) Renew(ctx context.Context, cert ingress.Certificate, domains []string, present func(string, string) error, cleanup func(string, string) error) (ingress.Certificate, error) {
	if err := ctx.Err(); err != nil {
		return ingress.Certificate{}, err
	}
	primary := firstDomain(domains, cert.Domain)
	if primary == "" {
		return ingress.Certificate{}, errors.New("at least one domain is required")
	}
	client, err := c.client(present, cleanup)
	if err != nil {
		return ingress.Certificate{}, err
	}
	resource, err := client.Certificate.RenewWithOptions(certificate.Resource{
		Domain:      primary,
		Certificate: []byte(cert.CertPEM),
		PrivateKey:  []byte(cert.KeyPEM),
	}, &certificate.RenewOptions{Bundle: true})
	if err != nil {
		return ingress.Certificate{}, err
	}
	return resourceToCertificate(primary, resource)
}

func firstDomain(domains []string, fallback string) string {
	if len(domains) > 0 && strings.TrimSpace(domains[0]) != "" {
		return strings.TrimSpace(domains[0])
	}
	return strings.TrimSpace(fallback)
}

func (c *legoClient) client(present func(string, string) error, cleanup func(string, string) error) (*lego.Client, error) {
	if c.initErr != nil {
		return nil, c.initErr
	}
	config := lego.NewConfig(c.account)
	config.CADirURL = c.directory
	client, err := lego.NewClient(config)
	if err != nil {
		return nil, err
	}
	if err := client.Challenge.SetDNS01Provider(legoDNSProvider{present: present, cleanup: cleanup}); err != nil {
		return nil, err
	}
	if err := c.ensureRegistration(client); err != nil {
		return nil, err
	}
	return client, nil
}

func (c *legoClient) ensureRegistration(client *lego.Client) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.account.registration != nil {
		return nil
	}
	reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err != nil {
		return err
	}
	c.account.registration = reg
	return nil
}

type legoAccount struct {
	email        string
	key          crypto.PrivateKey
	registration *registration.Resource
}

func (a *legoAccount) GetEmail() string {
	return a.email
}

func (a *legoAccount) GetRegistration() *registration.Resource {
	return a.registration
}

func (a *legoAccount) GetPrivateKey() crypto.PrivateKey {
	return a.key
}

type legoDNSProvider struct {
	present func(name string, value string) error
	cleanup func(name string, value string) error
}

func (p legoDNSProvider) Present(domain string, token string, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)
	return p.present(trimFQDN(info.FQDN), info.Value)
}

func (p legoDNSProvider) CleanUp(domain string, token string, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)
	return p.cleanup(trimFQDN(info.FQDN), info.Value)
}

func (p legoDNSProvider) Timeout() (time.Duration, time.Duration) {
	return dns01.DefaultPropagationTimeout, dns01.DefaultPollingInterval
}

func resourceToCertificate(domain string, resource *certificate.Resource) (ingress.Certificate, error) {
	if resource == nil {
		return ingress.Certificate{}, errors.New("lego returned nil certificate resource")
	}
	expiresAt, err := parseCertificateExpiry(resource.Certificate)
	if err != nil {
		return ingress.Certificate{}, err
	}
	return ingress.Certificate{
		Domain:     domain,
		CertPEM:    string(resource.Certificate),
		KeyPEM:     string(resource.PrivateKey),
		Issuer:     string(resource.IssuerCertificate),
		ObtainedAt: time.Now(),
		ExpiresAt:  expiresAt,
		Provider:   ingress.ProviderACME,
	}, nil
}

func parseCertificateExpiry(certPEM []byte) (time.Time, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return time.Time{}, errors.New("certificate PEM is empty")
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.NotAfter, nil
}
