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
	RunRemote(ctx context.Context, target Target, cmd string, workDir string, onLine func(string, string)) error
	// Transfer 将本地文件传输到目标 host。
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
	if strings.TrimSpace(in.ProxyOptions.RawTemplate) != "" {
		content, err := renderRawTemplate(in.ProxyOptions.RawTemplate, in, cert)
		if err != nil {
			return ingress.RenderedConfig{}, err
		}
		return ingress.RenderedConfig{Domain: in.Domain, Filename: filename, Content: content, Certificate: cert}, nil
	}

	timeout := in.ProxyOptions.ProxyTimeout.Duration
	if timeout == 0 {
		timeout = 60 * time.Second
	}

	var b bytes.Buffer
	if cert != nil {
		fmt.Fprintf(&b, "server {\n  listen 80;\n  server_name %s;\n  return 301 https://$host$request_uri;\n}\n\n", in.Domain)
	}

	b.WriteString("server {\n")
	if cert != nil {
		b.WriteString("  listen 443 ssl;\n")
		fmt.Fprintf(&b, "  ssl_certificate %s;\n", certFile(in.Domain, "fullchain.pem"))
		fmt.Fprintf(&b, "  ssl_certificate_key %s;\n", certFile(in.Domain, "privkey.pem"))
	} else {
		b.WriteString("  listen 80;\n")
	}
	fmt.Fprintf(&b, "  server_name %s;\n\n", in.Domain)

	writeProxyLocation(&b, "/", in.Backend, timeout, in.ProxyOptions.Websocket, "")
	for _, loc := range in.ProxyOptions.ExtraLocations {
		writeProxyLocation(&b, loc.Path, in.Backend, timeout, in.ProxyOptions.Websocket, loc.Raw)
	}

	b.WriteString("}\n")
	return ingress.RenderedConfig{Domain: in.Domain, Filename: filename, Content: b.String(), Certificate: cert}, nil
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
		"Domain":  in.Domain,
		"Backend": in.Backend,
		"Cert":    cert,
	}
	if err := tpl.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
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
