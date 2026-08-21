package dbprovision

import (
	"context"
	"testing"
)

// fakeProvisioner 是测试专用的最小实现，后续任务的单测也复用它。
type fakeProvisioner struct {
	kind string
}

func (f *fakeProvisioner) Kind() string { return f.kind }
func (f *fakeProvisioner) Probe(context.Context, DataSource) (ProbeResult, error) {
	return ProbeResult{OK: true}, nil
}
func (f *fakeProvisioner) Plan(_ context.Context, _ DataSource, req PlanRequest) (Plan, error) {
	return Plan{Kind: f.kind, ResourceName: req.NameSeed}, nil
}
func (f *fakeProvisioner) Provision(_ context.Context, _ DataSource, p Plan) (Resource, error) {
	return Resource{Kind: f.kind, Name: p.ResourceName, DSN: "fake://dsn"}, nil
}
func (f *fakeProvisioner) Reclaim(context.Context, DataSource, Resource) error { return nil }
func (f *fakeProvisioner) Reconcile(context.Context, DataSource, []Resource) ([]Orphan, error) {
	return nil, nil
}

func TestRegisterAndLookupProvisioner(t *testing.T) {
	RegisterProvisioner(&fakeProvisioner{kind: "fake-kind-a"})

	got, ok := LookupProvisioner("fake-kind-a")
	if !ok {
		t.Fatal("注册后应能查到 provisioner")
	}
	if got.Kind() != "fake-kind-a" {
		t.Fatalf("查到的 kind 不对: %s", got.Kind())
	}

	if _, ok := LookupProvisioner("never-registered"); ok {
		t.Fatal("未注册的 kind 不应被查到")
	}
}
