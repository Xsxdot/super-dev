// apply_test.go 验证 nginx provider 远端落地动作。
//
// 职责：
//   - 验证配置和证书传输顺序
//   - 验证 nginx -t 成功后才 reload
//   - 验证 Detect 和 Remove 的远端命令行为
//
// 边界：
//   - 不连接真实 SSH
//   - 不执行真实 nginx
package nginx

import (
	"context"
	"errors"
	"testing"

	"github.com/xsxdot/super-dev/agent/ingress"
	"github.com/xsxdot/super-dev/agent/model"
)

type fakeTransport struct {
	commands  []string
	transfers []string
	events    []string
	failOn    map[string]error
}

func (f *fakeTransport) RunRemote(ctx context.Context, target Target, cmd string, workDir string, onLine func(string, string)) error {
	f.commands = append(f.commands, cmd)
	f.events = append(f.events, "cmd:"+cmd)
	if err := f.failOn[cmd]; err != nil {
		return err
	}
	if cmd == "find /etc/nginx/conf.d -maxdepth 1 -name 'superdev-*.conf' -print" && onLine != nil {
		onLine("/etc/nginx/conf.d/superdev-api.example.com.conf", "stdout")
		onLine("/etc/nginx/conf.d/superdev-old.example.com.conf", "stdout")
	}
	return nil
}

func (f *fakeTransport) Transfer(ctx context.Context, target Target, source string, targetPath string, onLine func(string, string)) error {
	f.transfers = append(f.transfers, targetPath)
	f.events = append(f.events, "transfer:"+targetPath)
	return nil
}

func TestApplyTransfersConfigCertAndReloads(t *testing.T) {
	transport := &fakeTransport{}
	provider := New(transport)
	cfg := ingress.RenderedConfig{
		Domain:   "api.example.com",
		Filename: "api.example.com.conf",
		Content:  "server {}",
		Certificate: &ingress.Certificate{
			Domain:  "api.example.com",
			CertPEM: "CERT",
			KeyPEM:  "KEY",
		},
	}

	state, err := provider.Apply(context.Background(), model.Host{ID: "host-a", Name: "gateway-a"}, cfg)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	assertEqual(t, state.HostID, "host-a")
	assertEqual(t, state.ConfigPath, "/etc/nginx/conf.d/superdev-api.example.com.conf")
	assertStringSliceEqual(t, state.CertPaths, []string{
		"/etc/superdev/ingress/certs/api.example.com/fullchain.pem",
		"/etc/superdev/ingress/certs/api.example.com/privkey.pem",
	})
	if state.CertDeployment == nil {
		t.Fatal("CertDeployment = nil, want deployment details for renewal")
	}
	assertEqual(t, state.CertDeployment.PostDeployCommand, nginxPostDeployCommand)
	assertStringSliceEqual(t, transport.events, []string{
		"cmd:mkdir -p /etc/nginx/conf.d /etc/superdev/ingress/certs/api.example.com",
		"transfer:/etc/nginx/conf.d/superdev-api.example.com.conf",
		"transfer:/etc/superdev/ingress/certs/api.example.com/fullchain.pem",
		"transfer:/etc/superdev/ingress/certs/api.example.com/privkey.pem",
		"cmd:nginx -t",
		"cmd:if command -v systemctl >/dev/null 2>&1; then systemctl reload nginx || nginx -s reload; else nginx -s reload; fi",
	})
}

func TestApplyDoesNotReloadWhenNginxTestFails(t *testing.T) {
	nginxTestErr := errors.New("nginx syntax error")
	transport := &fakeTransport{failOn: map[string]error{"nginx -t": nginxTestErr}}
	provider := New(transport)

	_, err := provider.Apply(context.Background(), model.Host{ID: "host-a"}, ingress.RenderedConfig{
		Domain:   "api.example.com",
		Filename: "api.example.com.conf",
		Content:  "server {",
	})
	if !errors.Is(err, nginxTestErr) {
		t.Fatalf("Apply() error = %v, want %v", err, nginxTestErr)
	}
	assertStringSliceEqual(t, transport.commands, []string{
		"mkdir -p /etc/nginx/conf.d /etc/superdev/ingress/certs/api.example.com",
		"nginx -t",
	})
}

func TestDeployCertificateTransfersCertAndReloads(t *testing.T) {
	transport := &fakeTransport{}
	provider := New(transport)

	deployment, err := provider.DeployCertificate(context.Background(), model.Host{ID: "host-a", Name: "gateway-a"}, "api.example.com", ingress.Certificate{
		Domain:  "api.example.com",
		CertPEM: "CERT",
		KeyPEM:  "KEY",
	})
	if err != nil {
		t.Fatalf("DeployCertificate() error = %v", err)
	}

	assertEqual(t, deployment.HostID, "host-a")
	assertEqual(t, deployment.CertPath, "/etc/superdev/ingress/certs/api.example.com/fullchain.pem")
	assertEqual(t, deployment.KeyPath, "/etc/superdev/ingress/certs/api.example.com/privkey.pem")
	assertStringSliceEqual(t, transport.events, []string{
		"cmd:mkdir -p /etc/superdev/ingress/certs/api.example.com",
		"transfer:/etc/superdev/ingress/certs/api.example.com/fullchain.pem",
		"transfer:/etc/superdev/ingress/certs/api.example.com/privkey.pem",
		"cmd:nginx -t",
		"cmd:if command -v systemctl >/dev/null 2>&1; then systemctl reload nginx || nginx -s reload; else nginx -s reload; fi",
	})
}

func TestDeployCertificateUsesPortableNginxReload(t *testing.T) {
	transport := &fakeTransport{}
	provider := New(transport)

	_, err := provider.DeployCertificate(context.Background(), model.Host{ID: "host-a"}, "api.example.com", ingress.Certificate{
		Domain: "api.example.com", CertPEM: "CERT", KeyPEM: "KEY",
	})

	if err != nil {
		t.Fatalf("DeployCertificate() error = %v", err)
	}
	assertStringSliceContains(t, transport.commands, "if command -v systemctl >/dev/null 2>&1; then systemctl reload nginx || nginx -s reload; else nginx -s reload; fi")
}

func TestDetectReturnsOnlyUndeclaredSuperdevConfigs(t *testing.T) {
	transport := &fakeTransport{}
	provider := New(transport)

	orphan, err := provider.Detect(context.Background(), model.Host{ID: "host-a"}, []ingress.Ingress{{Domain: "api.example.com"}})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if len(orphan) != 1 {
		t.Fatalf("len(orphan) = %d, want 1: %#v", len(orphan), orphan)
	}
	assertEqual(t, orphan[0].HostID, "host-a")
	assertEqual(t, orphan[0].Domain, "old.example.com")
	assertEqual(t, orphan[0].Path, "/etc/nginx/conf.d/superdev-old.example.com.conf")
}

func TestRemoveDeletesConfigAfterConfirmationAndReloads(t *testing.T) {
	transport := &fakeTransport{}
	provider := New(transport)

	err := provider.Remove(context.Background(), model.Host{ID: "host-a"}, ingress.OrphanConfig{
		HostID: "host-a",
		Path:   "/etc/nginx/conf.d/superdev-old.example.com.conf",
		Domain: "old.example.com",
	})
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	assertStringSliceEqual(t, transport.commands, []string{
		"rm -f /etc/nginx/conf.d/superdev-old.example.com.conf",
		"nginx -t",
		"if command -v systemctl >/dev/null 2>&1; then systemctl reload nginx || nginx -s reload; else nginx -s reload; fi",
	})
}

func TestRemoveRejectsPathOutsideManagedConfigDir(t *testing.T) {
	transport := &fakeTransport{}
	provider := New(transport)

	err := provider.Remove(context.Background(), model.Host{ID: "host-a"}, ingress.OrphanConfig{
		HostID: "host-a",
		Path:   "/tmp/not-managed.conf",
		Domain: "old.example.com",
	})
	if err == nil {
		t.Fatal("Remove() error = nil, want managed path validation error")
	}
	if len(transport.commands) != 0 {
		t.Fatalf("commands = %#v, want none", transport.commands)
	}
}

func TestRemoveQuotesManagedConfigPath(t *testing.T) {
	transport := &fakeTransport{}
	provider := New(transport)

	err := provider.Remove(context.Background(), model.Host{ID: "host-a"}, ingress.OrphanConfig{
		HostID: "host-a",
		Path:   "/etc/nginx/conf.d/superdev-old'name.conf",
		Domain: "old.example.com",
	})
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	assertStringSliceEqual(t, transport.commands, []string{
		"rm -f '/etc/nginx/conf.d/superdev-old'\\''name.conf'",
		"nginx -t",
		"if command -v systemctl >/dev/null 2>&1; then systemctl reload nginx || nginx -s reload; else nginx -s reload; fi",
	})
}

func assertStringSliceEqual(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}

func assertStringSliceContains(t *testing.T, got []string, want string) {
	t.Helper()
	for _, item := range got {
		if item == want {
			return
		}
	}
	t.Fatalf("got %#v, want item %q", got, want)
}
