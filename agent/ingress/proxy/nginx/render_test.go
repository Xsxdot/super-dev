// render_test.go 覆盖 nginx 入口 Raw Template 渲染行为。
//
// 职责：
//   - 验证 Raw Template 是唯一配置来源
//   - 验证域名、upstream 和证书路径变量可以用于模板
//
// 边界：
//   - 不测试远端文件写入、nginx reload 或主机检测
//   - 不申请或解析真实证书材料
package nginx

import (
	"strings"
	"testing"

	"github.com/superdev/agent/ingress"
)

func TestRenderHTTPRawTemplateVariables(t *testing.T) {
	provider := New(nil)

	cfg, err := provider.Render(ingress.Ingress{
		Domain:  "api.example.com",
		Backend: "127.0.0.1:8080",
		ProxyOptions: ingress.ProxyOptions{
			RawTemplate: "server {\n  listen 80;\n  server_name {{ .Domain }};\n  location / { proxy_pass http://{{ .Backend }}; }\n}\n",
		},
	}, nil)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	assertEqual(t, cfg.Domain, "api.example.com")
	assertEqual(t, cfg.Filename, "api.example.com.conf")
	assertContains(t, cfg.Content, "listen 80;")
	assertContains(t, cfg.Content, "server_name api.example.com;")
	assertContains(t, cfg.Content, "proxy_pass http://127.0.0.1:8080;")
	assertNotContains(t, cfg.Content, "ssl_certificate")
}

func TestRenderTLSRawTemplateVariables(t *testing.T) {
	provider := New(nil)

	cfg, err := provider.Render(ingress.Ingress{
		Domain: "api.example.com",
		Upstreams: []ingress.Upstream{
			{IP: "10.0.0.12", Port: 8080},
		},
		TLS: ingress.TLSConfig{
			Enabled: true,
		},
		ProxyOptions: ingress.ProxyOptions{
			RawTemplate: "server {\n  listen 443 ssl;\n  ssl_certificate {{ .CertFullchainPath }};\n  ssl_certificate_key {{ .CertPrivateKeyPath }};\n  server_name {{ .Domain }};\n}\n",
		},
	}, &ingress.Certificate{Domain: "api.example.com"})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	assertContains(t, cfg.Content, "listen 443 ssl;")
	assertContains(t, cfg.Content, "ssl_certificate /etc/superdev/ingress/certs/api.example.com/fullchain.pem;")
	assertContains(t, cfg.Content, "ssl_certificate_key /etc/superdev/ingress/certs/api.example.com/privkey.pem;")
}

func TestRenderRawTemplate(t *testing.T) {
	provider := New(nil)

	cfg, err := provider.Render(ingress.Ingress{
		Domain:  "api.example.com",
		Backend: "127.0.0.1:8080",
		ProxyOptions: ingress.ProxyOptions{
			RawTemplate: "server_name {{domain}};\nproxy_pass http://{{backend}};\n",
		},
	}, nil)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	assertEqual(t, cfg.Content, "server_name api.example.com;\nproxy_pass http://127.0.0.1:8080;\n")
	assertNotContains(t, cfg.Content, "location / {")
}

func TestRenderUsesRawTemplateAsConfigSource(t *testing.T) {
	provider := New(nil)
	in := ingress.Ingress{
		Domain: "api.example.com",
		Proxy:  ingress.ProxyConfig{Provider: ingress.ProviderNginx, HostIDs: []string{"edge-a"}},
		Upstreams: []ingress.Upstream{
			{IP: "10.0.0.12", Port: 8080},
		},
		ProxyOptions: ingress.ProxyOptions{
			Websocket:   false,
			RawTemplate: "server {\n  server_name {{ .Domain }};\n  location / { proxy_pass http://custom_upstream; }\n}\n",
		},
	}

	rendered, err := provider.Render(in, nil)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	assertContains(t, rendered.Content, "server_name api.example.com;")
	assertContains(t, rendered.Content, "proxy_pass http://custom_upstream;")
	assertNotContains(t, rendered.Content, "10.0.0.12:8080")
}

func TestRenderRejectsEmptyRawTemplate(t *testing.T) {
	provider := New(nil)
	_, err := provider.Render(ingress.Ingress{Domain: "api.example.com"}, nil)
	if err == nil {
		t.Fatal("Render() error = nil, want raw_template error")
	}
	assertContains(t, err.Error(), "raw_template is required")
}

func assertContains(t *testing.T, got string, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("content does not contain %q\ncontent:\n%s", want, got)
	}
}

func assertNotContains(t *testing.T, got string, unwanted string) {
	t.Helper()
	if strings.Contains(got, unwanted) {
		t.Fatalf("content contains %q\ncontent:\n%s", unwanted, got)
	}
}

func assertEqual(t *testing.T, got string, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
