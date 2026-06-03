// render_test.go 覆盖 nginx 入口配置渲染行为。
//
// 职责：
//   - 验证普通 HTTP、TLS、WebSocket 和自定义 location 的配置输出
//   - 验证 raw template 能完全接管配置内容
//
// 边界：
//   - 不测试远端文件写入、nginx reload 或主机检测
//   - 不申请或解析真实证书材料
package nginx

import (
	"strings"
	"testing"
	"time"

	"github.com/superdev/agent/ingress"
)

func TestRenderDefaultHTTPServer(t *testing.T) {
	provider := New(nil)

	cfg, err := provider.Render(ingress.Ingress{
		Domain:  "api.example.com",
		Backend: "127.0.0.1:8080",
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

func TestRenderTLSWebsocketAndExtraLocation(t *testing.T) {
	provider := New(nil)

	cfg, err := provider.Render(ingress.Ingress{
		Domain:  "api.example.com",
		Backend: "127.0.0.1:8080",
		TLS: ingress.TLSConfig{
			Enabled: true,
		},
		ProxyOptions: ingress.ProxyOptions{
			Websocket:    true,
			ProxyTimeout: ingress.Duration{Duration: 75 * time.Second},
			ExtraLocations: []ingress.LocationOption{
				{
					Path: "/metrics",
					Raw:  "return 404;",
				},
			},
		},
	}, &ingress.Certificate{Domain: "api.example.com"})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	assertContains(t, cfg.Content, "listen 80;")
	assertContains(t, cfg.Content, "return 301 https://$host$request_uri;")
	assertContains(t, cfg.Content, "listen 443 ssl;")
	assertContains(t, cfg.Content, "ssl_certificate /etc/superdev/ingress/certs/api.example.com/fullchain.pem;")
	assertContains(t, cfg.Content, "ssl_certificate_key /etc/superdev/ingress/certs/api.example.com/privkey.pem;")
	assertContains(t, cfg.Content, "proxy_set_header Upgrade $http_upgrade;")
	assertContains(t, cfg.Content, "proxy_set_header Connection \"upgrade\";")
	assertContains(t, cfg.Content, "proxy_read_timeout 75s;")
	assertContains(t, cfg.Content, "location /metrics {")
	assertContains(t, cfg.Content, "return 404;")
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
