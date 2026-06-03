// certmanager_test.go 验证托管证书续期巡检。
//
// 职责：
//   - 验证即将过期的证书会续期并重新分发到 host
//   - 验证未到续期窗口的证书会跳过
//
// 边界：
//   - 不访问真实 ACME CA
//   - 不访问真实远端 host
package ingress

import (
	"context"
	"testing"
	"time"
)

func TestCertManagerRunOnceIgnoresIngressAppliedStates(t *testing.T) {
	store := NewFileStore(t.TempDir())
	reg := NewRegistry()
	events := []string{}
	reg.RegisterDNS(&orderedDNS{name: "dns-prod", events: &events})
	reg.RegisterCert(&orderedCert{name: ProviderACME, events: &events})
	reg.RegisterProxy(&orderedProxy{name: ProviderNginx, events: &events})
	in, err := store.UpsertIngress(validAutomaticIngress())
	requireNoError(t, err)
	requireNoError(t, store.SaveState(AppliedState{
		IngressID: in.ID,
		Hosts:     []HostState{{HostID: "host-a"}},
	}))
	manager := NewCertManager(CertManagerConfig{
		Store:       store,
		Registry:    reg,
		RenewBefore: 30 * 24 * time.Hour,
	})

	err = manager.RunOnce(context.Background(), time.Now())
	requireNoError(t, err)
	assertStringSliceEqual(t, events, []string{})
}

func TestCertManagerSkipsFreshCertificate(t *testing.T) {
	store := NewFileStore(t.TempDir())
	reg := NewRegistry()
	events := []string{}
	reg.RegisterDNS(&orderedDNS{name: "dns-prod", events: &events})
	reg.RegisterCert(&orderedCert{name: ProviderACME, events: &events})
	reg.RegisterProxy(&orderedProxy{name: ProviderNginx, events: &events})
	in, err := store.UpsertIngress(validAutomaticIngress())
	requireNoError(t, err)
	requireNoError(t, store.SaveState(AppliedState{
		IngressID: in.ID,
		Hosts:     []HostState{{HostID: "host-a"}},
	}))
	manager := NewCertManager(CertManagerConfig{
		Store:       store,
		Registry:    reg,
		RenewBefore: 30 * 24 * time.Hour,
	})

	err = manager.RunOnce(context.Background(), time.Now())
	requireNoError(t, err)
	assertStringSliceEqual(t, events, []string{})
}
