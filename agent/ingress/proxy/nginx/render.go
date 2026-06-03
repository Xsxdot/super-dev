// Package nginx 实现 Ingress 的 nginx proxy provider。
//
// 职责：
//   - 将 Ingress 声明纯渲染为 nginx server block
//
// 边界：
//   - render.go 不连接远端 host，不执行 nginx -t，不 reload nginx
//   - apply.go 负责后续传输、校验和 reload
package nginx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"text/template"
	"time"

	"github.com/superdev/agent/ingress"
)

const (
	configDir = "/etc/nginx/conf.d"
	certDir   = "/etc/superdev/ingress/certs"
)

// RemoteTransport 定义 nginx provider 后续落地配置时使用的远端通道。
type RemoteTransport interface {
	// RunRemote 在目标 host 上执行命令。
	//
	// 参数：
	//   - ctx: 上下文，用于取消远端命令
	//   - target: 目标 host
	//   - cmd: 待执行命令
	//   - workDir: 命令工作目录，可为空
	//   - onLine: 输出回调，可为空
	//
	// 返回：
	//   - 命令执行失败时返回错误
	RunRemote(ctx context.Context, target Target, cmd string, workDir string, onLine func(string, string)) error
	// Transfer 将本地文件传输到目标 host。
	//
	// 参数：
	//   - ctx: 上下文，用于取消传输
	//   - target: 目标 host
	//   - source: 本地文件路径
	//   - targetPath: 远端目标路径
	//   - onLine: 输出回调，可为空
	//
	// 返回：
	//   - 传输失败时返回错误
	Transfer(ctx context.Context, target Target, source string, targetPath string, onLine func(string, string)) error
}

// Target 描述 nginx provider 操作的目标 host。
type Target struct {
	HostID   string
	HostName string
}

// Provider 是 nginx 反向代理 provider。
type Provider struct {
	transport RemoteTransport
}

// New 创建 nginx provider。
//
// 参数：
//   - transport: 远端执行和传输通道；仅 Render 时可为空
//
// 返回：
//   - nginx Provider 实例
func New(transport RemoteTransport) *Provider {
	return &Provider{transport: transport}
}

// Name 返回 provider 注册名。
//
// 返回：
//   - 固定值 nginx
func (p *Provider) Name() string {
	return ingress.ProviderNginx
}

// Render 将入口声明纯渲染成 nginx 配置。
//
// 参数：
//   - in: 待渲染的入口声明
//   - cert: 已托管证书；传入时渲染 HTTPS server block
//
// 返回：
//   - 渲染后的配置文件名和内容
//   - raw template 解析或执行失败时返回错误
//
// 注意：
//   - raw template 是逃生舱，存在时完全接管 server block 内容
func (p *Provider) Render(in ingress.Ingress, cert *ingress.Certificate) (ingress.RenderedConfig, error) {
	filename := safeDomainFilename(in.Domain)
	raw := in.ProxyOptions.RawTemplate
	if strings.TrimSpace(raw) == "" {
		return ingress.RenderedConfig{}, errors.New("raw_template is required")
	}
	content, err := renderRawTemplate(raw, in, cert)
	if err != nil {
		return ingress.RenderedConfig{}, err
	}
	return ingress.RenderedConfig{Domain: in.Domain, Filename: filename, Content: content, Certificate: cert}, nil
}

func writeProxyLocation(b *bytes.Buffer, locPath string, backend string, timeout time.Duration, websocket bool, raw string) {
	fmt.Fprintf(b, "  location %s {\n", locPath)
	if strings.TrimSpace(raw) != "" {
		for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
			fmt.Fprintf(b, "    %s\n", line)
		}
		b.WriteString("  }\n\n")
		return
	}

	fmt.Fprintf(b, "    proxy_pass %s;\n", proxyPassTarget(backend))
	b.WriteString("    proxy_set_header Host $host;\n")
	b.WriteString("    proxy_set_header X-Real-IP $remote_addr;\n")
	b.WriteString("    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n")
	b.WriteString("    proxy_set_header X-Forwarded-Proto $scheme;\n")
	fmt.Fprintf(b, "    proxy_read_timeout %s;\n", durationSeconds(timeout))
	fmt.Fprintf(b, "    proxy_send_timeout %s;\n", durationSeconds(timeout))
	if websocket {
		b.WriteString("    proxy_http_version 1.1;\n")
		b.WriteString("    proxy_set_header Upgrade $http_upgrade;\n")
		b.WriteString("    proxy_set_header Connection \"upgrade\";\n")
	}
	b.WriteString("  }\n\n")
}

func renderRawTemplate(raw string, in ingress.Ingress, cert *ingress.Certificate) (string, error) {
	normalized := strings.ReplaceAll(raw, "{{domain}}", "{{.Domain}}")
	normalized = strings.ReplaceAll(normalized, "{{backend}}", "{{.Backend}}")
	tpl, err := template.New("nginx-raw").Parse(normalized)
	if err != nil {
		return "", err
	}

	var b bytes.Buffer
	data := map[string]any{
		"Domain":             in.Domain,
		"Backend":            primaryBackend(in.Upstreams),
		"Ingress":            in,
		"Upstreams":          in.Upstreams,
		"TLS":                in.TLS,
		"Cert":               cert,
		"CertFullchainPath":  certFile(in.Domain, "fullchain.pem"),
		"CertPrivateKeyPath": certFile(in.Domain, "privkey.pem"),
	}
	if err := tpl.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

func primaryBackend(upstreams []ingress.Upstream) string {
	if len(upstreams) == 0 {
		return ""
	}
	upstream := upstreams[0]
	if upstream.Port <= 0 {
		return strings.TrimSpace(upstream.IP)
	}
	return fmt.Sprintf("%s:%d", strings.TrimSpace(upstream.IP), upstream.Port)
}

func safeDomainFilename(domain string) string {
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(domain) + ".conf"
}

func certFile(domain string, name string) string {
	return path.Join(certDir, domain, name)
}

func proxyPassTarget(backend string) string {
	trimmed := strings.TrimSpace(backend)
	if strings.Contains(trimmed, "://") {
		return trimmed
	}
	return "http://" + trimmed
}

func durationSeconds(d time.Duration) string {
	return fmt.Sprintf("%ds", int(d.Seconds()))
}
